package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// The settings read tools cross the model boundary with arr configuration,
// so the properties pinned here are: instance identity without URLs, verbatim
// full-JSON views, explicit instance targeting (a mistyped instance_id must
// never fall back to another instance), and the unsupported-endpoint hint.

// newSettingsToolServer builds a ToolServer over the given pre-built
// instances (Create assigns their IDs in place).
func newSettingsToolServer(t *testing.T, instances []*instance.Instance) *ToolServer {
	t.Helper()
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
	for _, inst := range instances {
		if err := store.Create(inst); err != nil {
			t.Fatalf("create instance %s: %v", inst.Name, err)
		}
	}
	return NewToolServer(nil, nil, instance.NewRegistry(store), nil)
}

func settingsFakeArr(t *testing.T, recorder *callRecorder, routes map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if recorder != nil {
			recorder.record(r)
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestListArrInstancesShowsIdentityAndNeverURLs(t *testing.T) {
	mainInst := &instance.Instance{ServiceType: "radarr", Name: "Main Movies", URL: "http://radarr-internal:7878", APIKey: "k", IsDefault: true}
	fourK := &instance.Instance{ServiceType: "radarr", Name: "4K Movies", URL: "http://radarr-4k:7878", APIKey: "k"}
	tv := &instance.Instance{ServiceType: "sonarr", Name: "TV", URL: "http://sonarr-internal:8989", APIKey: "k", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{mainInst, fourK, tv})

	result, err := server.ExecuteTool(context.Background(), "list_arr_instances", json.RawMessage(`{}`), adminCallContext())
	if err != nil {
		t.Fatalf("list_arr_instances: %v", err)
	}
	for _, want := range []string{
		`"Main Movies" — instance_id: ` + mainInst.ID + ` (used when no instance_id is given)`,
		`"4K Movies" — instance_id: ` + fourK.ID + "\n",
		`"TV" — instance_id: ` + tv.ID + ` (used when no instance_id is given)`,
		"Chaptarr: no instances configured.",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("listing missing %q:\n%s", want, result.Text)
		}
	}
	for _, leaked := range []string{"radarr-internal", "radarr-4k", "sonarr-internal", "7878", "8989", "http://"} {
		if strings.Contains(result.Text, leaked) {
			t.Errorf("listing leaked %q:\n%s", leaked, result.Text)
		}
	}

	filtered, err := server.ExecuteTool(context.Background(), "list_arr_instances", json.RawMessage(`{"service":"sonarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("filtered list_arr_instances: %v", err)
	}
	if strings.Contains(filtered.Text, "Radarr") || !strings.Contains(filtered.Text, "Sonarr instances:") {
		t.Errorf("service filter not applied:\n%s", filtered.Text)
	}
}

const settingsProfileHD = `{"id":1,"name":"HD","upgradeAllowed":true,"cutoff":7,"items":[{"quality":{"id":2,"name":"DVD"},"items":[],"allowed":false},{"quality":{"id":7,"name":"Bluray-1080p"},"items":[],"allowed":true},{"id":1000,"name":"WEB 1080p","items":[{"quality":{"id":3,"name":"WEBDL-1080p"},"items":[],"allowed":true}],"allowed":true}],"minFormatScore":0,"cutoffFormatScore":10000,"minUpgradeFormatScore":1,"formatItems":[{"format":3,"name":"Not English","score":-10000},{"format":4,"name":"x265","score":0}],"language":{"id":-1,"name":"Any"},"futureField":"round-trip-me"}`

func TestGetQualityProfilesSummaryAndFullViews(t *testing.T) {
	arr := settingsFakeArr(t, nil, map[string]string{
		"/api/v3/qualityprofile": `[` + settingsProfileHD + `]`,
	})
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "k", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})

	summary, err := server.ExecuteTool(context.Background(), "get_quality_profiles", json.RawMessage(`{"service":"radarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("summary view: %v", err)
	}
	for _, want := range []string{
		`Quality profiles on Radarr instance "Main" (1):`,
		`[1] "HD" — upgrades on until Bluray-1080p`,
		"allowed (worst→best): Bluray-1080p, WEB 1080p",
		"custom format scores: 1 of 2 nonzero — Not English (-10000)",
		"min score 0, cutoff score 10000",
		"language: Any",
	} {
		if !strings.Contains(summary.Text, want) {
			t.Errorf("summary missing %q:\n%s", want, summary.Text)
		}
	}
	if strings.Contains(summary.Text, "futureField") {
		t.Errorf("summary view should not inline raw JSON:\n%s", summary.Text)
	}

	full, err := server.ExecuteTool(context.Background(), "get_quality_profiles", json.RawMessage(`{"service":"radarr","profile_id":1}`), adminCallContext())
	if err != nil {
		t.Fatalf("full view: %v", err)
	}
	if !strings.Contains(full.Text, "full stored JSON") || !strings.Contains(full.Text, settingsProfileHD) {
		t.Errorf("full view is not verbatim:\n%s", full.Text)
	}

	miss, err := server.ExecuteTool(context.Background(), "get_quality_profiles", json.RawMessage(`{"service":"radarr","profile_id":99}`), adminCallContext())
	if err != nil {
		t.Fatalf("missing profile view: %v", err)
	}
	if !strings.Contains(miss.Text, "No quality profile with ID 99") || !strings.Contains(miss.Text, "1 (HD)") {
		t.Errorf("miss should teach valid ids:\n%s", miss.Text)
	}
}

func TestGetQualityProfilesTargetsExactInstance(t *testing.T) {
	defaultCalls := &callRecorder{}
	defaultArr := settingsFakeArr(t, defaultCalls, map[string]string{
		"/api/v3/qualityprofile": `[{"id":1,"name":"Default Profile","items":[],"formatItems":[]}]`,
	})
	secondCalls := &callRecorder{}
	secondArr := settingsFakeArr(t, secondCalls, map[string]string{
		"/api/v3/qualityprofile": `[{"id":5,"name":"4K Profile","items":[],"formatItems":[]}]`,
	})
	defaultInst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: defaultArr.URL, APIKey: "k", IsDefault: true}
	secondInst := &instance.Instance{ServiceType: "radarr", Name: "4K", URL: secondArr.URL, APIKey: "k"}
	server := newSettingsToolServer(t, []*instance.Instance{defaultInst, secondInst})

	result, err := server.ExecuteTool(context.Background(), "get_quality_profiles",
		json.RawMessage(`{"service":"radarr","instance_id":"`+secondInst.ID+`"}`), adminCallContext())
	if err != nil {
		t.Fatalf("targeted read: %v", err)
	}
	if !strings.Contains(result.Text, "4K Profile") || !strings.Contains(result.Text, `Radarr instance "4K"`) {
		t.Errorf("targeted read shows wrong instance:\n%s", result.Text)
	}
	if calls := defaultCalls.all(); len(calls) != 0 {
		t.Errorf("default instance received %d requests, want 0", len(calls))
	}
	if calls := secondCalls.all(); len(calls) != 1 {
		t.Errorf("target instance received %d requests, want 1", len(calls))
	}

	unknown, err := server.ExecuteTool(context.Background(), "get_quality_profiles",
		json.RawMessage(`{"service":"radarr","instance_id":"no-such-instance"}`), adminCallContext())
	if err != nil {
		t.Fatalf("unknown instance: %v", err)
	}
	if !strings.Contains(unknown.Text, `No radarr instance with ID "no-such-instance"`) {
		t.Errorf("unknown instance_id must not fall back:\n%s", unknown.Text)
	}
	if calls := append(defaultCalls.all(), secondCalls.all()...); len(calls) != 1 {
		t.Errorf("unknown instance_id must reach no upstream (calls now %d, want the 1 prior)", len(calls))
	}

	missing, err := server.ExecuteTool(context.Background(), "get_quality_profiles", json.RawMessage(`{"service":"sonarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("unconfigured service: %v", err)
	}
	if missing.Text != "Sonarr is not configured." {
		t.Errorf("unconfigured service message = %q", missing.Text)
	}
}

func TestGetCustomFormatsSummaryFullAndUnsupported(t *testing.T) {
	const format = `{"id":3,"name":"Language: Not English","includeCustomFormatWhenRenaming":false,"specifications":[{"name":"Not English","implementation":"LanguageSpecification","negate":true,"required":false,"fields":[{"name":"value","value":1}]}]}`
	arr := settingsFakeArr(t, nil, map[string]string{
		"/api/v3/customformat": `[` + format + `]`,
	})
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "k", IsDefault: true}
	sonarrV3 := settingsFakeArr(t, nil, map[string]string{})
	tvInst := &instance.Instance{ServiceType: "sonarr", Name: "TV", URL: sonarrV3.URL, APIKey: "k", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst, tvInst})

	summary, err := server.ExecuteTool(context.Background(), "get_custom_formats", json.RawMessage(`{"service":"radarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("summary view: %v", err)
	}
	for _, want := range []string{
		`[3] "Language: Not English" — specs: Not English (LanguageSpecification, negate)`,
		"live in each quality profile's formatItems",
	} {
		if !strings.Contains(summary.Text, want) {
			t.Errorf("summary missing %q:\n%s", want, summary.Text)
		}
	}

	full, err := server.ExecuteTool(context.Background(), "get_custom_formats", json.RawMessage(`{"service":"radarr","format_id":3}`), adminCallContext())
	if err != nil {
		t.Fatalf("full view: %v", err)
	}
	if !strings.Contains(full.Text, format) {
		t.Errorf("full view is not verbatim:\n%s", full.Text)
	}

	// A 404 cannot tell an older build from a wrong URL base, so the tool must
	// offer both causes rather than assert a version diagnosis.
	notFound, err := server.ExecuteTool(context.Background(), "get_custom_formats", json.RawMessage(`{"service":"sonarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("not-found view: %v", err)
	}
	for _, want := range []string{"returned 404", "v4", "URL base"} {
		if !strings.Contains(notFound.Text, want) {
			t.Errorf("404 message missing %q:\n%s", want, notFound.Text)
		}
	}
	if strings.Contains(notFound.Text, "does not support custom formats") {
		t.Errorf("404 must not be reported as a settled version diagnosis:\n%s", notFound.Text)
	}
}

// A profile's ceiling is the decision-relevant end of a worst→best list, so
// truncation must drop the worst entries, not the best.
func TestQualityProfileSummaryKeepsTheBestQualities(t *testing.T) {
	qualities := []string{"SDTV", "DVD", "HDTV-720p", "WEBDL-720p", "Bluray-720p", "HDTV-1080p", "WEBDL-1080p", "Bluray-1080p", "HDTV-2160p", "WEBDL-2160p", "Bluray-2160p"}
	items := make([]string, 0, len(qualities))
	for i, name := range qualities {
		items = append(items, fmt.Sprintf(`{"quality":{"id":%d,"name":%q},"items":[],"allowed":true}`, i+1, name))
	}
	profile := fmt.Sprintf(`{"id":1,"name":"Any","upgradeAllowed":true,"cutoff":11,"items":[%s],"formatItems":[]}`, strings.Join(items, ","))
	arr := settingsFakeArr(t, nil, map[string]string{"/api/v3/qualityprofile": `[` + profile + `]`})
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "k", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})

	result, err := server.ExecuteTool(context.Background(), "get_quality_profiles", json.RawMessage(`{"service":"radarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("summary view: %v", err)
	}
	if !strings.Contains(result.Text, "Bluray-2160p") || !strings.Contains(result.Text, "WEBDL-2160p") {
		t.Errorf("summary dropped the best qualities:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "SDTV") || strings.Contains(result.Text, "+3 more") == false {
		t.Errorf("summary should elide the worst qualities and count them:\n%s", result.Text)
	}
}

// Chaptarr rows never carry is_default, and a deleted default leaves
// Radarr/Sonarr in the same state: the listing must mark whatever an omitted
// instance_id actually resolves to, or the model cannot predict the target.
func TestListArrInstancesMarksTheEffectiveDefault(t *testing.T) {
	first := &instance.Instance{ServiceType: "chaptarr", Name: "Books Audio", URL: "http://books-a:8787", APIKey: "k"}
	second := &instance.Instance{ServiceType: "chaptarr", Name: "Books Ebook", URL: "http://books-e:8787", APIKey: "k"}
	server := newSettingsToolServer(t, []*instance.Instance{first, second})

	result, err := server.ExecuteTool(context.Background(), "list_arr_instances", json.RawMessage(`{"service":"chaptarr"}`), adminCallContext())
	if err != nil {
		t.Fatalf("list_arr_instances: %v", err)
	}
	if !strings.Contains(result.Text, `"Books Audio" — instance_id: `+first.ID+` (used when no instance_id is given)`) {
		t.Errorf("first chaptarr instance is the effective default but is unmarked:\n%s", result.Text)
	}
	if strings.Contains(result.Text, second.ID+" (used") {
		t.Errorf("only one instance may be marked:\n%s", result.Text)
	}
}

// An instance whose stored credentials will not decrypt still resolves in the
// listing, so telling the admin the ID does not exist would hide the fault.
func TestSettingsToolsDistinguishUnreadableInstanceFromUnknownID(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	writeCipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: "http://radarr:7878", APIKey: "k", IsDefault: true}
	if err := instance.NewStore(database, writeCipher).Create(inst); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	// Reopen the same rows under a different key, as a rotated or lost
	// encryption key would leave them.
	readCipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	server := NewToolServer(nil, nil, instance.NewRegistry(instance.NewStore(database, readCipher)), nil)

	result, err := server.ExecuteTool(context.Background(), "get_quality_profiles",
		json.RawMessage(`{"service":"radarr","instance_id":"`+inst.ID+`"}`), adminCallContext())
	if err != nil {
		t.Fatalf("unreadable instance: %v", err)
	}
	if !strings.Contains(result.Text, "exists but could not be opened") {
		t.Errorf("an unreadable instance must not read as a mistyped id:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "radarr:7878") {
		t.Errorf("refusal leaked the instance host:\n%s", result.Text)
	}
}

func TestSettingsToolsRefuseNonAdminRoles(t *testing.T) {
	server := newSettingsToolServer(t, nil)
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		return auth.RoleUser, nil
	})
	callCtx := CallContext{UserID: 7, Role: auth.RoleUser, DeviceID: "device-1"}
	for _, tool := range []string{"list_arr_instances", "get_quality_profiles", "get_custom_formats"} {
		result, err := server.ExecuteTool(context.Background(), tool, json.RawMessage(`{"service":"radarr"}`), callCtx)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if result.Text != "This action is not permitted for your role." {
			t.Errorf("%s allowed for user role: %q", tool, result.Text)
		}
	}
}
