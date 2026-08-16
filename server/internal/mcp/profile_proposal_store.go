package mcp

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// Profile-change proposals parked by external MCP agents. The in-app
// preview/apply pair proves admin consent through same-turn chat provenance;
// an external agent has no server-witnessed turn, so its previewed change
// parks here instead and consent happens in the app: an admin reviews the
// stored diff and approves, which re-validates live state against the stored
// hashes and runs the same verified write path as the in-app apply.
const (
	profileProposalStatusPending    = "pending"
	profileProposalStatusExecuting  = "executing"
	profileProposalStatusApplied    = "applied"
	profileProposalStatusRejected   = "rejected"
	profileProposalStatusSuperseded = "superseded"
	profileProposalStatusExpired    = "expired"
	profileProposalStatusFailed     = "failed"
)

// profileProposalTTL bounds how long a parked proposal waits for a decision.
// Long enough to survive a weekend away; the live drift guards, not this
// window, are what protect correctness — an approved proposal is refused the
// moment the profile or its dependencies no longer match the stored hashes.
const profileProposalTTL = 7 * 24 * time.Hour

// profileProposalExecutingTimeout ages out an approval interrupted by a crash
// mid-write: the row cannot honestly report the arr's state, so it fails with
// a pointer at Configuration history, where the write's own record (applied /
// failed / outcome_unknown) is the durable truth.
const profileProposalExecutingTimeout = 15 * time.Minute

const maxProfileProposalTextBytes = 4 << 10

// ProfileChangeProposal is the public projection served to the app. Plan,
// hashes, and the instance fingerprint stay server-side.
type ProfileChangeProposal struct {
	ID              int64    `json:"id"`
	Status          string   `json:"status"`
	Service         string   `json:"service"`
	InstanceID      string   `json:"instance_id"`
	InstanceName    string   `json:"instance_name"`
	ProfileID       int      `json:"profile_id"`
	ProfileName     string   `json:"profile_name"`
	ProposedByName  string   `json:"proposed_by_name"`
	SourceClient    string   `json:"source_client,omitempty"`
	Diff            []string `json:"diff"`
	CreatedAt       string   `json:"created_at"`
	ExpiresAt       string   `json:"expires_at"`
	DecidedByName   string   `json:"decided_by_name,omitempty"`
	DecidedAt       string   `json:"decided_at,omitempty"`
	RejectNote      string   `json:"reject_note,omitempty"`
	ResultText      string   `json:"result_text,omitempty"`
	SettingChangeID *int64   `json:"setting_change_id,omitempty"`
	// CurrentStatus is set only by the detail read of a pending proposal:
	// "applicable" (live settings still match the stored hashes), "stale"
	// (they drifted; approval would refuse), or "unavailable" (the arr could
	// not be read).
	CurrentStatus string `json:"current_status,omitempty"`
}

// storedProfileProposal adds the server-only execution material.
type storedProfileProposal struct {
	ProfileChangeProposal
	ProposedBy       int64
	ProposerDeviceID string
	Plan             profileChangePlan
	ProfileHash      [32]byte
	DesiredHash      [32]byte
	CustomFormatHash [32]byte
	LanguageHash     [32]byte
	HasLanguageHash  bool
	InstanceBinding  instance.ArrSettingsFingerprint
	ExpiresAtTime    time.Time
}

type newProfileProposal struct {
	ProposedBy       int64
	ProposerDeviceID string
	SourceClient     string
	Service          string
	InstanceID       string
	InstanceName     string
	ProfileID        int
	ProfileName      string
	Plan             profileChangePlan
	Diff             []string
	ProfileHash      [32]byte
	DesiredHash      [32]byte
	CustomFormatHash [32]byte
	LanguageHash     [32]byte
	HasLanguageHash  bool
	InstanceBinding  instance.ArrSettingsFingerprint
}

type profileProposalStore struct {
	db  *sql.DB
	now func() time.Time
}

// newProfileProposalStore returns nil for a nil database; every method
// nil-checks so a server without the ledger refuses parking instead of
// silently dropping proposals.
func newProfileProposalStore(database *sql.DB) *profileProposalStore {
	if database == nil {
		return nil
	}
	return &profileProposalStore{db: database, now: time.Now}
}

const profileProposalColumns = `p.id, p.status, p.service_type, p.instance_id, p.instance_name,
	p.profile_id, p.profile_name, p.proposed_by, p.proposer_device_id, p.source_client,
	p.plan_json, p.diff_json, p.profile_hash, p.desired_profile_hash, p.custom_format_hash,
	p.language_hash, p.has_language_hash, p.instance_binding,
	p.reject_note, p.result_text, p.setting_change_id,
	p.created_at, p.expires_at, p.decided_at,
	COALESCE(proposer.username, 'Administrator'), COALESCE(decider.username, '')`

const profileProposalJoins = ` FROM profile_change_proposals p
	LEFT JOIN users proposer ON proposer.id = p.proposed_by
	LEFT JOIN users decider ON decider.id = p.decided_by AND p.decided_by > 0`

// park supersedes any pending proposal for the same profile and inserts the
// new one. Callers hold the per-instance settings mutation lock, which is
// what serializes parking against a concurrently executing approval of the
// same instance's proposals.
func (s *profileProposalStore) park(p newProfileProposal) (storedProfileProposal, error) {
	if s == nil || s.db == nil {
		return storedProfileProposal{}, fmt.Errorf("profile change proposals are unavailable")
	}
	if p.ProposedBy <= 0 || p.ProposerDeviceID == "" {
		return storedProfileProposal{}, fmt.Errorf("profile change proposal is not bound to an authenticated proposer")
	}
	if p.Service == "" || p.InstanceID == "" || p.ProfileID <= 0 {
		return storedProfileProposal{}, fmt.Errorf("profile change proposal is missing its target")
	}
	s.sweep()
	planJSON, err := json.Marshal(p.Plan)
	if err != nil {
		return storedProfileProposal{}, fmt.Errorf("encode proposal plan: %w", err)
	}
	diffJSON, err := json.Marshal(p.Diff)
	if err != nil {
		return storedProfileProposal{}, fmt.Errorf("encode proposal diff: %w", err)
	}
	if _, err := s.db.Exec(
		`UPDATE profile_change_proposals
		 SET status = ?, decided_at = CURRENT_TIMESTAMP,
		     result_text = 'Replaced by a newer proposal for the same profile.'
		 WHERE service_type = ? AND instance_id = ? AND profile_id = ? AND status = ?`,
		profileProposalStatusSuperseded, p.Service, p.InstanceID, p.ProfileID, profileProposalStatusPending,
	); err != nil {
		return storedProfileProposal{}, fmt.Errorf("supersede pending proposal: %w", err)
	}
	expiresAt := s.now().UTC().Add(profileProposalTTL)
	res, err := s.db.Exec(
		`INSERT INTO profile_change_proposals
		   (proposed_by, proposer_device_id, source_client, service_type, instance_id, instance_name,
		    profile_id, profile_name, plan_json, diff_json,
		    profile_hash, desired_profile_hash, custom_format_hash, language_hash, has_language_hash,
		    instance_binding, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProposedBy, p.ProposerDeviceID, truncateProposalText(p.SourceClient), p.Service, p.InstanceID, p.InstanceName,
		p.ProfileID, p.ProfileName, string(planJSON), string(diffJSON),
		hex.EncodeToString(p.ProfileHash[:]), hex.EncodeToString(p.DesiredHash[:]),
		hex.EncodeToString(p.CustomFormatHash[:]), hex.EncodeToString(p.LanguageHash[:]), boolToInt(p.HasLanguageHash),
		p.InstanceBinding[:], expiresAt,
	)
	if err != nil {
		return storedProfileProposal{}, fmt.Errorf("insert profile change proposal: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return storedProfileProposal{}, fmt.Errorf("insert profile change proposal id: %w", err)
	}
	return s.get(id)
}

// get returns one proposal with its server-only execution material.
func (s *profileProposalStore) get(id int64) (storedProfileProposal, error) {
	if s == nil || s.db == nil {
		return storedProfileProposal{}, fmt.Errorf("profile change proposals are unavailable")
	}
	row := s.db.QueryRow(`SELECT `+profileProposalColumns+profileProposalJoins+` WHERE p.id = ?`, id)
	return scanProfileProposal(row)
}

// list returns proposals newest-first: pending only by default, or the full
// recent history for status "all". The expiry sweep runs first so a listing
// never shows a proposal approval would refuse as expired.
func (s *profileProposalStore) list(status string) ([]storedProfileProposal, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("profile change proposals are unavailable")
	}
	s.sweep()
	query := `SELECT ` + profileProposalColumns + profileProposalJoins
	args := []any{}
	if status != "all" {
		query += ` WHERE p.status = ?`
		args = append(args, profileProposalStatusPending)
	}
	query += ` ORDER BY p.id DESC LIMIT 200`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list profile change proposals: %w", err)
	}
	defer rows.Close()
	// Collect fully before any further DB work: the pool is a single
	// connection, so issuing a query inside an open cursor deadlocks.
	var out []storedProfileProposal
	for rows.Next() {
		p, err := scanProfileProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// claimExecuting moves one pending proposal to executing. The rows-affected
// count is the verdict: 0 means another decision won.
func (s *profileProposalStore) claimExecuting(id int64) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("profile change proposals are unavailable")
	}
	res, err := s.db.Exec(
		`UPDATE profile_change_proposals
		 SET status = ?, executing_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND status = ? AND expires_at > ?`,
		profileProposalStatusExecuting, id, profileProposalStatusPending, s.now().UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("claim profile change proposal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim profile change proposal rows: %w", err)
	}
	return n == 1, nil
}

// release returns an executing proposal to pending after a transient failure
// (arr unreachable, authorization refused, tool disabled) — nothing about the
// proposal itself was disproven. Safe against the one-pending-per-profile
// index because park and approval serialize on the instance mutation lock.
func (s *profileProposalStore) release(id int64) {
	if s == nil || s.db == nil {
		return
	}
	_, _ = s.db.Exec(
		`UPDATE profile_change_proposals SET status = ?, executing_at = NULL
		 WHERE id = ? AND status = ?`,
		profileProposalStatusPending, id, profileProposalStatusExecuting,
	)
}

// markApplied finishes an executing proposal after the verified write and
// links the durable configuration-history record.
func (s *profileProposalStore) markApplied(id, decidedBy, settingChangeID int64) error {
	return s.finishExecuting(id, profileProposalStatusApplied, decidedBy, "", &settingChangeID)
}

// markFailed finishes an executing proposal terminally: positive drift proof,
// a failed write, or an unknowable outcome. The text is the admin-facing
// explanation.
func (s *profileProposalStore) markFailed(id, decidedBy int64, text string) error {
	return s.finishExecuting(id, profileProposalStatusFailed, decidedBy, text, nil)
}

func (s *profileProposalStore) finishExecuting(id int64, status string, decidedBy int64, text string, settingChangeID *int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("profile change proposals are unavailable")
	}
	res, err := s.db.Exec(
		`UPDATE profile_change_proposals
		 SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP,
		     result_text = ?, setting_change_id = ?, executing_at = NULL
		 WHERE id = ? AND status = ?`,
		status, decidedBy, truncateProposalText(text), settingChangeID, id, profileProposalStatusExecuting,
	)
	if err != nil {
		return fmt.Errorf("finish profile change proposal: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("profile change proposal outcome was already decided")
	}
	return nil
}

// reject declines a pending proposal without touching the arr.
func (s *profileProposalStore) reject(id, decidedBy int64, note string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("profile change proposals are unavailable")
	}
	res, err := s.db.Exec(
		`UPDATE profile_change_proposals
		 SET status = ?, decided_by = ?, decided_at = CURRENT_TIMESTAMP, reject_note = ?
		 WHERE id = ? AND status = ?`,
		profileProposalStatusRejected, decidedBy, truncateProposalText(note), id, profileProposalStatusPending,
	)
	if err != nil {
		return false, fmt.Errorf("reject profile change proposal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("reject profile change proposal rows: %w", err)
	}
	return n == 1, nil
}

// pendingCount reports how many proposals await a decision, after sweeping
// expiries — it feeds the app's attention-menu badge, so a count that
// includes already-expired rows would page an admin toward an empty screen.
func (s *profileProposalStore) pendingCount() (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("profile change proposals are unavailable")
	}
	s.sweep()
	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM profile_change_proposals WHERE status = ?`,
		profileProposalStatusPending,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count pending profile change proposals: %w", err)
	}
	return count, nil
}

// sourceClientName reads the proposing device's display name ("MCP: Claude
// Desktop") for the proposal record. Denormalized on purpose: the parked
// value documents what proposed the change even if the device row is later
// renamed or revoked. Best-effort — a miss is an empty string.
func (s *profileProposalStore) sourceClientName(deviceID string) string {
	if s == nil || s.db == nil || deviceID == "" {
		return ""
	}
	var name string
	if err := s.db.QueryRow(`SELECT device_name FROM devices WHERE id = ?`, deviceID).Scan(&name); err != nil {
		return ""
	}
	return name
}

// sweep expires overdue pending proposals and fails approvals a crash
// interrupted mid-write. Best-effort by design: a sweep failure must never
// block the read or park it piggybacks on.
func (s *profileProposalStore) sweep() {
	if s == nil || s.db == nil {
		return
	}
	now := s.now().UTC()
	_, _ = s.db.Exec(
		`UPDATE profile_change_proposals
		 SET status = ?, decided_at = CURRENT_TIMESTAMP,
		     result_text = 'Expired before any admin decided.'
		 WHERE status = ? AND expires_at <= ?`,
		profileProposalStatusExpired, profileProposalStatusPending, now,
	)
	_, _ = s.db.Exec(
		`UPDATE profile_change_proposals
		 SET status = ?, decided_at = CURRENT_TIMESTAMP, executing_at = NULL,
		     result_text = 'The approval was interrupted mid-write. Configuration history holds the write''s own outcome record.'
		 WHERE status = ? AND executing_at <= ?`,
		profileProposalStatusFailed, profileProposalStatusExecuting, now.Add(-profileProposalExecutingTimeout),
	)
}

type profileProposalScanner interface {
	Scan(dest ...any) error
}

func scanProfileProposal(row profileProposalScanner) (storedProfileProposal, error) {
	var (
		p                                                  storedProfileProposal
		planJSON, diffJSON                                 string
		profileHash, desiredHash, formatHash, languageHash string
		hasLanguage                                        int
		binding                                            []byte
		settingChangeID                                    sql.NullInt64
		createdAt, expiresAt                               time.Time
		decidedAt                                          sql.NullTime
		proposerName, deciderName                          string
	)
	err := row.Scan(
		&p.ID, &p.Status, &p.Service, &p.InstanceID, &p.InstanceName,
		&p.ProfileID, &p.ProfileName, &p.ProposedBy, &p.ProposerDeviceID, &p.SourceClient,
		&planJSON, &diffJSON, &profileHash, &desiredHash, &formatHash,
		&languageHash, &hasLanguage, &binding,
		&p.RejectNote, &p.ResultText, &settingChangeID,
		&createdAt, &expiresAt, &decidedAt,
		&proposerName, &deciderName,
	)
	if err != nil {
		return storedProfileProposal{}, err
	}
	if err := json.Unmarshal([]byte(planJSON), &p.Plan); err != nil {
		return storedProfileProposal{}, fmt.Errorf("decode proposal plan: %w", err)
	}
	if err := json.Unmarshal([]byte(diffJSON), &p.Diff); err != nil {
		return storedProfileProposal{}, fmt.Errorf("decode proposal diff: %w", err)
	}
	for _, h := range []struct {
		src string
		dst *[32]byte
	}{{profileHash, &p.ProfileHash}, {desiredHash, &p.DesiredHash}, {formatHash, &p.CustomFormatHash}, {languageHash, &p.LanguageHash}} {
		decoded, err := hex.DecodeString(h.src)
		if err != nil || len(decoded) != len(h.dst) {
			return storedProfileProposal{}, fmt.Errorf("decode proposal hash")
		}
		copy(h.dst[:], decoded)
	}
	if len(binding) != len(p.InstanceBinding) {
		return storedProfileProposal{}, fmt.Errorf("decode proposal instance binding")
	}
	copy(p.InstanceBinding[:], binding)
	p.HasLanguageHash = hasLanguage != 0
	p.ProposedByName = proposerName
	p.DecidedByName = deciderName
	p.CreatedAt = createdAt.UTC().Format(time.RFC3339)
	p.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	p.ExpiresAtTime = expiresAt.UTC()
	if decidedAt.Valid {
		p.DecidedAt = decidedAt.Time.UTC().Format(time.RFC3339)
	}
	if settingChangeID.Valid {
		id := settingChangeID.Int64
		p.SettingChangeID = &id
	}
	return p, nil
}

// executionProposal reconstructs the in-memory proposal shape the shared
// mutation helpers take, so approval and the in-app apply exercise the exact
// same drift checks and verified write.
func (p storedProfileProposal) executionProposal() profileChangeProposal {
	return profileChangeProposal{
		UserID:             p.ProposedBy,
		DeviceID:           p.ProposerDeviceID,
		Service:            p.Service,
		InstanceID:         p.InstanceID,
		InstanceBinding:    p.InstanceBinding,
		ProfileID:          p.ProfileID,
		ProfileName:        p.ProfileName,
		ProfileHash:        p.ProfileHash,
		DesiredProfileHash: p.DesiredHash,
		CustomFormatHash:   p.CustomFormatHash,
		LanguageHash:       p.LanguageHash,
		HasLanguageHash:    p.HasLanguageHash,
		Plan:               p.Plan.clone(),
		Diff:               append([]string(nil), p.Diff...),
	}
}

func truncateProposalText(text string) string {
	if len(text) > maxProfileProposalTextBytes {
		return text[:maxProfileProposalTextBytes]
	}
	return text
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
