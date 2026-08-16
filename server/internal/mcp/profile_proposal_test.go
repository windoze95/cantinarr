package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// recordingAdminNotifier captures NotifyAdmins calls so a test can pin the
// parked-proposal push without a gateway.
type recordingAdminNotifier struct {
	mu     sync.Mutex
	events []string
	datas  []map[string]interface{}
}

func (r *recordingAdminNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, eventType)
	r.datas = append(r.datas, data)
}

func (r *recordingAdminNotifier) calls() ([]string, []map[string]interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...), append([]map[string]interface{}(nil), r.datas...)
}

// newProfileProposalHarness builds a tool server over a real schema with one
// default Radarr instance served by the shared fake, an admin proposer with
// an MCP device row, and a recording notifier.
func newProfileProposalHarness(t *testing.T) (*ToolServer, *profileToolFakeArr, *sql.DB, *recordingAdminNotifier) {
	t.Helper()
	fake := newProfileToolFakeArr()
	upstream := httptest.NewServer(fake)
	t.Cleanup(upstream.Close)
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Movies", URL: upstream.URL, APIKey: "profile-secret-key", IsDefault: true}
	if err := store.Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	server := NewToolServer(nil, nil, instance.NewRegistry(store), nil)
	server.SetSettingsChangeDatabase(database)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) { return auth.RoleAdmin, nil })
	notifier := &recordingAdminNotifier{}
	server.SetAdminNotifier(notifier)
	mustExecSQL(t, database, `INSERT INTO users (id, username, password_hash, role) VALUES (77, 'julian', '', 'admin')`)
	mustExecSQL(t, database, `INSERT INTO devices (id, user_id, device_name) VALUES ('device-77', 77, 'MCP: Claude Desktop')`)
	return server, fake, database, notifier
}

func mustExecSQL(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// externalProfileCallContext is what an authenticated external MCP session
// carries: identity and origin, but no turn provenance.
func externalProfileCallContext() CallContext {
	return CallContext{UserID: 77, Role: auth.RoleAdmin, DeviceID: "device-77", Reauthorize: true, Origin: OriginExternalMCP}
}

// adminApprovalCallContext is the approving admin's REST call context (a
// different admin than the proposer, pinning that decided_by is the approver).
func adminApprovalCallContext() CallContext {
	return CallContext{UserID: 88, Role: auth.RoleAdmin, DeviceID: "device-88", Reauthorize: true, Origin: OriginInteractiveChat}
}

func parkExternalProposal(t *testing.T, server *ToolServer, changes string) int64 {
	t.Helper()
	result, err := server.ExecuteTool(context.Background(), "preview_profile_change",
		json.RawMessage(`{"service":"radarr","profile_id":1,"changes":`+changes+`}`), externalProfileCallContext())
	if err != nil {
		t.Fatalf("external preview: %v", err)
	}
	if !strings.Contains(result.Text, "parked for admin approval") {
		t.Fatalf("external preview text = %q", result.Text)
	}
	pending, err := server.profileProposals.list("")
	if err != nil || len(pending) == 0 {
		t.Fatalf("pending proposals = %v, err=%v", pending, err)
	}
	return pending[0].ID
}

func TestExternalPreviewParksProposalAndNotifies(t *testing.T) {
	server, fake, database, notifier := newProfileProposalHarness(t)

	result, err := server.ExecuteTool(context.Background(), "preview_profile_change",
		json.RawMessage(`{"service":"radarr","profile_id":1,"changes":{"upgrade_allowed":false}}`), externalProfileCallContext())
	if err != nil {
		t.Fatalf("external preview: %v", err)
	}
	if !strings.Contains(result.Text, "parked for admin approval") || !strings.Contains(result.Text, "upgrade policy: on -> off") {
		t.Fatalf("parked text = %q", result.Text)
	}
	// The same-turn capability reference is an in-app concept: leaking one to
	// an external session would invite the model to try apply_profile_change.
	if strings.Contains(result.Text, "Change reference:") || strings.Contains(result.Text, "apply_profile_change") {
		t.Errorf("parked text leaks the in-app apply handoff: %q", result.Text)
	}

	pending, err := server.profileProposals.list("")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v err=%v, want exactly one", pending, err)
	}
	p := pending[0]
	if p.Status != profileProposalStatusPending || p.ProposedBy != 77 || p.ProposedByName != "julian" ||
		p.SourceClient != "MCP: Claude Desktop" || p.ProfileName != "HD" || p.InstanceName != "Movies" || len(p.Diff) == 0 {
		t.Errorf("parked proposal = %+v", p)
	}

	events, datas := notifier.calls()
	if len(events) != 1 || events[0] != "profile_change_pending" {
		t.Fatalf("notifier events = %v, want one profile_change_pending", events)
	}
	if id, ok := datas[0]["proposal_id"].(int64); !ok || id != p.ID {
		t.Errorf("push proposal_id = %v, want %d", datas[0]["proposal_id"], p.ID)
	}
	// The authoritative badge count rides the event so the app applies it
	// without a refetch.
	if count, ok := datas[0]["pending_count"].(int); !ok || count != 1 {
		t.Errorf("push pending_count = %v, want 1", datas[0]["pending_count"])
	}

	// A second proposal for the same profile supersedes the first: the admin
	// only ever reviews the latest diff.
	if _, err := server.ExecuteTool(context.Background(), "preview_profile_change",
		json.RawMessage(`{"service":"radarr","profile_id":1,"changes":{"min_format_score":100}}`), externalProfileCallContext()); err != nil {
		t.Fatalf("second external preview: %v", err)
	}
	pending, err = server.profileProposals.list("")
	if err != nil || len(pending) != 1 || pending[0].ID == p.ID {
		t.Fatalf("after supersession pending = %v err=%v", pending, err)
	}
	var superseded int
	if err := database.QueryRow(`SELECT COUNT(*) FROM profile_change_proposals WHERE status = 'superseded'`).Scan(&superseded); err != nil || superseded != 1 {
		t.Errorf("superseded rows = %d err=%v, want 1", superseded, err)
	}

	// Parking never writes.
	if fake.putCount() != 0 {
		t.Errorf("parking performed %d writes, want 0", fake.putCount())
	}
}

func TestApproveProfileProposalExecutesVerifiedWrite(t *testing.T) {
	server, fake, database, notifier := newProfileProposalHarness(t)
	id := parkExternalProposal(t, server, `{"upgrade_allowed":false}`)

	updated, err := server.approveProfileProposal(context.Background(), id, adminApprovalCallContext())
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	// The decision broadcasts the drained count so other admins' badges follow.
	events, datas := notifier.calls()
	if len(events) != 2 || events[1] != "profile_change_decided" {
		t.Fatalf("notifier events = %v, want parked then decided", events)
	}
	if count, ok := datas[1]["pending_count"].(int); !ok || count != 0 {
		t.Errorf("decided pending_count = %v, want 0", datas[1]["pending_count"])
	}
	if updated.Status != profileProposalStatusApplied || updated.SettingChangeID == nil {
		t.Fatalf("approved proposal = %+v", updated)
	}
	if fake.putCount() != 1 {
		t.Fatalf("PUT count = %d, want exactly one verified write", fake.putCount())
	}

	// The history record carries the PROPOSER as actor with the external_mcp
	// source — configuration history answers "what changed my arr and through
	// what channel" — while consent lives on the proposal row (decided_by =
	// the approving admin).
	var source, status string
	var actor int64
	if err := database.QueryRow(`SELECT source, actor_user_id, status FROM external_setting_changes WHERE id = ?`,
		*updated.SettingChangeID).Scan(&source, &actor, &status); err != nil {
		t.Fatalf("read history row: %v", err)
	}
	if source != "external_mcp" || actor != 77 || status != "applied" {
		t.Errorf("history row source=%q actor=%d status=%q", source, actor, status)
	}
	var decidedBy int64
	if err := database.QueryRow(`SELECT decided_by FROM profile_change_proposals WHERE id = ?`, id).Scan(&decidedBy); err != nil || decidedBy != 88 {
		t.Errorf("decided_by = %d err=%v, want the approving admin 88", decidedBy, err)
	}

	// A second approval finds nothing pending.
	if _, err := server.approveProfileProposal(context.Background(), id, adminApprovalCallContext()); !errors.Is(err, errProfileProposalNotPending) {
		t.Errorf("re-approve error = %v, want errProfileProposalNotPending", err)
	}
}

func TestApproveRefusesDriftedProfile(t *testing.T) {
	server, fake, _, _ := newProfileProposalHarness(t)
	id := parkExternalProposal(t, server, `{"upgrade_allowed":false}`)

	// The profile changes between parking and approval: positive drift proof
	// must refuse the write and finish the proposal terminally.
	fake.setProfile(strings.Replace(settingsProfileHD, `"cutoffFormatScore":10000`, `"cutoffFormatScore":9000`, 1))

	_, err := server.approveProfileProposal(context.Background(), id, adminApprovalCallContext())
	if !errors.Is(err, errProfileProposalStale) {
		t.Fatalf("approve error = %v, want errProfileProposalStale", err)
	}
	if fake.putCount() != 0 {
		t.Errorf("drifted approval performed %d writes, want 0", fake.putCount())
	}
	stored, err := server.profileProposals.get(id)
	if err != nil || stored.Status != profileProposalStatusFailed {
		t.Errorf("proposal after drift = %+v err=%v, want terminal failed", stored.ProfileChangeProposal, err)
	}
}

func TestRejectProposalNeverTouchesTheArr(t *testing.T) {
	server, fake, _, _ := newProfileProposalHarness(t)
	id := parkExternalProposal(t, server, `{"upgrade_allowed":false}`)

	rejected, err := server.profileProposals.reject(id, 88, "not now")
	if err != nil || !rejected {
		t.Fatalf("reject = %v err=%v", rejected, err)
	}
	stored, err := server.profileProposals.get(id)
	if err != nil || stored.Status != profileProposalStatusRejected || stored.RejectNote != "not now" {
		t.Fatalf("rejected proposal = %+v err=%v", stored.ProfileChangeProposal, err)
	}
	if _, err := server.approveProfileProposal(context.Background(), id, adminApprovalCallContext()); !errors.Is(err, errProfileProposalNotPending) {
		t.Errorf("approve after reject error = %v, want errProfileProposalNotPending", err)
	}
	if fake.putCount() != 0 {
		t.Errorf("reject path performed %d writes, want 0", fake.putCount())
	}
}

func TestApproveWhileApplyToolDisabledStaysPending(t *testing.T) {
	server, fake, _, _ := newProfileProposalHarness(t)
	id := parkExternalProposal(t, server, `{"upgrade_allowed":false}`)

	if err := server.SetToolEnabled("apply_profile_change", false); err != nil {
		t.Fatalf("disable apply tool: %v", err)
	}
	_, err := server.approveProfileProposal(context.Background(), id, adminApprovalCallContext())
	if !errors.Is(err, errProfileProposalToolDisabled) {
		t.Fatalf("approve error = %v, want errProfileProposalToolDisabled", err)
	}
	// A disabled tool is a transient admin state, not drift: the proposal
	// stays pending for after it is re-enabled.
	stored, getErr := server.profileProposals.get(id)
	if getErr != nil || stored.Status != profileProposalStatusPending {
		t.Errorf("proposal while tool disabled = %+v err=%v, want still pending", stored.ProfileChangeProposal, getErr)
	}
	if fake.putCount() != 0 {
		t.Errorf("disabled-tool approval performed %d writes, want 0", fake.putCount())
	}

	if err := server.SetToolEnabled("apply_profile_change", true); err != nil {
		t.Fatalf("re-enable apply tool: %v", err)
	}
	if _, err := server.approveProfileProposal(context.Background(), id, adminApprovalCallContext()); err != nil {
		t.Fatalf("approve after re-enable: %v", err)
	}
	if fake.putCount() != 1 {
		t.Errorf("PUT count after re-enable = %d, want 1", fake.putCount())
	}
}

func TestProfileProposalDetailReportsLiveApplicability(t *testing.T) {
	server, fake, _, _ := newProfileProposalHarness(t)
	id := parkExternalProposal(t, server, `{"upgrade_allowed":false}`)

	detail, err := server.profileProposalDetail(context.Background(), id)
	if err != nil || detail.CurrentStatus != "applicable" {
		t.Fatalf("fresh detail = %+v err=%v, want current_status applicable", detail, err)
	}

	fake.setProfile(strings.Replace(settingsProfileHD, `"cutoffFormatScore":10000`, `"cutoffFormatScore":9000`, 1))
	detail, err = server.profileProposalDetail(context.Background(), id)
	if err != nil || detail.CurrentStatus != "stale" {
		t.Fatalf("drifted detail = %+v err=%v, want current_status stale", detail, err)
	}
}
