package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	server, _ := newSettingsToolServerWithStore(t, instances)
	return server
}

func newSettingsToolServerWithStore(t *testing.T, instances []*instance.Instance) (*ToolServer, *instance.Store) {
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
	return NewToolServer(nil, nil, instance.NewRegistry(store), nil), store
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
	for _, tool := range []string{"list_arr_instances", "get_quality_profiles", "get_custom_formats", "upsert_custom_format"} {
		result, err := server.ExecuteTool(context.Background(), tool, json.RawMessage(`{"service":"radarr"}`), callCtx)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if result.Text != "This action is not permitted for your role." {
			t.Errorf("%s allowed for user role: %q", tool, result.Text)
		}
	}
}

func TestUpsertCustomFormatTargetsExactInstanceAndTransformsTrash(t *testing.T) {
	targetCalls := &callRecorder{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/customformat":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"formatItems":[{"format":13,"score":0}]}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/customformat":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":13,"name":"Not English"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(target.Close)
	decoyCalls := &callRecorder{}
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoyCalls.record(r)
		http.Error(w, "decoy must not be touched", http.StatusInternalServerError)
	}))
	t.Cleanup(decoy.Close)

	defaultInst := &instance.Instance{ServiceType: "radarr", Name: "Default", URL: decoy.URL, APIKey: "decoy", IsDefault: true}
	targetInst := &instance.Instance{ServiceType: "radarr", Name: "4K", URL: target.URL, APIKey: "target"}
	server := newSettingsToolServer(t, []*instance.Instance{defaultInst, targetInst})
	result, err := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{
		"service":"radarr",
		"instance_id":"`+targetInst.ID+`",
		"custom_format":{
			"id":999,"name":"Not English","trash_id":"abc",
			"specifications":[{"name":"Not English","implementation":"LanguageSpecification","fields":{"value":1,"exceptLanguage":false}}]
		}
	}`), adminCallContext())
	if err != nil {
		t.Fatalf("upsert_custom_format: %v", err)
	}
	if !strings.Contains(result.Text, `Created custom format 13 ("Not English") on Radarr instance "4K"`) || !strings.Contains(result.Text, "score 0") {
		t.Fatalf("result = %q", result.Text)
	}
	if calls := decoyCalls.all(); len(calls) != 0 {
		t.Fatalf("default instance was touched: %+v", calls)
	}
	mutations := targetCalls.mutations()
	if len(mutations) != 1 || mutations[0].Method != http.MethodPost || mutations[0].URI != "/api/v3/customformat" {
		t.Fatalf("target mutations = %+v", mutations)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(mutations[0].Body), &body); err != nil {
		t.Fatalf("decode mutation: %v", err)
	}
	if _, exists := body["id"]; exists || body["trash_id"] != "abc" {
		t.Fatalf("create id/unknown fields = %#v", body)
	}
	fields := body["specifications"].([]any)[0].(map[string]any)["fields"].([]any)
	if len(fields) != 2 || fields[0].(map[string]any)["name"] != "exceptLanguage" || fields[1].(map[string]any)["name"] != "value" {
		t.Fatalf("TRaSH fields not transformed: %#v", fields)
	}
}

func TestUpsertCustomFormatSurfacesOnlyTypedValidationDetails(t *testing.T) {
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`[{
			"propertyName":"Specifications[0].Fields",
			"errorMessage":"A required field is missing",
			"attemptedValue":"https://arr.invalid/a?apiKey=must-not-leak"
		}]`))
	}))
	t.Cleanup(arr.Close)
	inst := &instance.Instance{ServiceType: "sonarr", Name: "TV", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})

	_, err := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{
		"service":"sonarr","custom_format":{"name":"Broken","specifications":[]}
	}`), adminCallContext())
	if err == nil || !strings.Contains(err.Error(), "Specifications[0].Fields: A required field is missing") {
		t.Fatalf("validation err = %v", err)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("validation error leaked attemptedValue: %v", err)
	}
}

func TestUpsertCustomFormatRejectsUnknownOuterFieldsBeforeTraffic(t *testing.T) {
	recorder := &callRecorder{}
	arr := settingsFakeArr(t, recorder, map[string]string{"/api/v3/customformat": `[]`})
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})

	_, err := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{
		"service":"radarr","surprise":true,"custom_format":{"name":"x265","specifications":[]}
	}`), adminCallContext())
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field err = %v", err)
	}
	if calls := recorder.all(); len(calls) != 0 {
		t.Fatalf("invalid input reached arr: %+v", calls)
	}
}

func TestUpsertCustomFormatSerializesSameInstance(t *testing.T) {
	var (
		mu          sync.Mutex
		current     string
		gets        int
		posts       int
		puts        int
		postOnce    sync.Once
		postStarted = make(chan struct{})
		releasePost = make(chan struct{})
	)
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/customformat":
			mu.Lock()
			gets++
			value := current
			mu.Unlock()
			if value == "" {
				_, _ = w.Write([]byte(`[]`))
			} else {
				_, _ = w.Write([]byte(`[` + value + `]`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":1,"formatItems":[{"format":5,"score":0}]}]`))
		case r.Method == http.MethodPost:
			mu.Lock()
			posts++
			mu.Unlock()
			postOnce.Do(func() { close(postStarted) })
			<-releasePost
			mu.Lock()
			current = `{"id":5,"name":"x265","specifications":[]}`
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(current))
		case r.Method == http.MethodPut:
			mu.Lock()
			puts++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":5,"name":"x265"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(arr.Close)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})
	input := json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`)

	results := make(chan error, 2)
	go func() {
		_, err := server.ExecuteTool(context.Background(), "upsert_custom_format", input, adminCallContext())
		results <- err
	}()
	select {
	case <-postStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first upsert did not reach POST")
	}
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		_, err := server.ExecuteTool(context.Background(), "upsert_custom_format", input, adminCallContext())
		results <- err
	}()
	<-secondStarted
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	getsWhileBlocked := gets
	mu.Unlock()
	if getsWhileBlocked != 1 {
		t.Fatalf("second upsert entered the GET→write critical section early; gets=%d", getsWhileBlocked)
	}
	close(releasePost)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent upsert: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if gets != 2 || posts != 1 || puts != 1 {
		t.Fatalf("calls = GET %d POST %d PUT %d, want 2/1/1", gets, posts, puts)
	}
}

func TestUpsertCustomFormatCancellationWhileQueuedDoesNotPoisonLock(t *testing.T) {
	recorder := &callRecorder{}
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5,"name":"x265"}`))
	}))
	t.Cleanup(arr.Close)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})

	unlock, err := server.lockArrSettingsMutation(context.Background(), "radarr", inst.ID)
	if err != nil {
		t.Fatalf("hold mutation lock: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, callErr := server.ExecuteTool(ctx, "upsert_custom_format", json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`), adminCallContext())
		done <- callErr
	}()
	cancel()
	select {
	case callErr := <-done:
		if callErr != context.Canceled {
			t.Fatalf("canceled queued call error = %T %v", callErr, callErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled queued call did not return")
	}
	if calls := recorder.all(); len(calls) != 0 {
		t.Fatalf("canceled queued call reached arr: %+v", calls)
	}
	unlocksafely := unlock
	unlocksafely()

	result, err := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`), adminCallContext())
	if err != nil || !strings.Contains(result.Text, "Created custom format 5") {
		t.Fatalf("lock was poisoned after cancellation: result=%#v err=%v", result, err)
	}
}

func TestUpsertCustomFormatReauthorizesAfterQueueWait(t *testing.T) {
	recorder := &callRecorder{}
	arr := settingsFakeArr(t, recorder, map[string]string{"/api/v3/customformat": `[]`})
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})

	unlock, err := server.lockArrSettingsMutation(context.Background(), "radarr", inst.ID)
	if err != nil {
		t.Fatalf("hold mutation lock: %v", err)
	}
	initialAuthorization := make(chan struct{})
	var authCalls atomic.Int32
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		if authCalls.Add(1) == 1 {
			close(initialAuthorization)
			return auth.RoleAdmin, nil
		}
		return "", errors.New("device revoked")
	})
	done := make(chan error, 1)
	go func() {
		_, callErr := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`), CallContext{UserID: 7, Role: auth.RoleAdmin, DeviceID: "device-7"})
		done <- callErr
	}()
	<-initialAuthorization
	unlock()
	if callErr := <-done; callErr != ErrToolAuthorization {
		t.Fatalf("queued revocation error = %T %v", callErr, callErr)
	}
	if authCalls.Load() < 2 {
		t.Fatalf("authorization checks = %d, want at least 2", authCalls.Load())
	}
	if calls := recorder.all(); len(calls) != 0 {
		t.Fatalf("revoked queued call reached arr: %+v", calls)
	}
}

func TestUpsertCustomFormatReauthorizesImmediatelyBeforeWrite(t *testing.T) {
	getStarted := make(chan struct{})
	releaseGet := make(chan struct{})
	var closeGet sync.Once
	t.Cleanup(func() { closeGet.Do(func() { close(releaseGet) }) })
	var posts atomic.Int32
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			close(getStarted)
			<-releaseGet
			_, _ = w.Write([]byte(`[]`))
			return
		}
		posts.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5,"name":"x265"}`))
	}))
	t.Cleanup(arr.Close)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})
	var revoked atomic.Bool
	server.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		if revoked.Load() {
			return "", errors.New("device revoked")
		}
		return auth.RoleAdmin, nil
	})
	done := make(chan error, 1)
	go func() {
		_, callErr := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`), CallContext{UserID: 7, Role: auth.RoleAdmin, DeviceID: "device-7"})
		done <- callErr
	}()
	<-getStarted
	revoked.Store(true)
	closeGet.Do(func() { close(releaseGet) })
	if callErr := <-done; callErr != ErrToolAuthorization {
		t.Fatalf("pre-dispatch revocation error = %T %v", callErr, callErr)
	}
	if posts.Load() != 0 {
		t.Fatalf("revoked call dispatched %d writes", posts.Load())
	}
}

func TestUpsertCustomFormatRechecksToolEnablementBeforeWrite(t *testing.T) {
	getStarted := make(chan struct{})
	releaseGet := make(chan struct{})
	var closeGet sync.Once
	t.Cleanup(func() { closeGet.Do(func() { close(releaseGet) }) })
	var posts atomic.Int32
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			close(getStarted)
			<-releaseGet
			_, _ = w.Write([]byte(`[]`))
			return
		}
		posts.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5,"name":"x265"}`))
	}))
	t.Cleanup(arr.Close)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})
	type callResult struct {
		result *ToolResult
		err    error
	}
	done := make(chan callResult, 1)
	go func() {
		result, callErr := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`), adminCallContext())
		done <- callResult{result: result, err: callErr}
	}()
	<-getStarted
	if err := server.SetToolEnabled("upsert_custom_format", false); err != nil {
		t.Fatalf("disable tool: %v", err)
	}
	closeGet.Do(func() { close(releaseGet) })
	got := <-done
	if got.err != nil || got.result == nil || !strings.Contains(got.result.Text, "disabled") || !strings.Contains(got.result.Text, "No custom format was changed") {
		t.Fatalf("disabled pre-dispatch result=%#v err=%v", got.result, got.err)
	}
	if posts.Load() != 0 {
		t.Fatalf("disabled call dispatched %d writes", posts.Load())
	}
}

func TestUpsertCustomFormatRefusesInstanceRepointBeforeWrite(t *testing.T) {
	getStarted := make(chan struct{})
	releaseGet := make(chan struct{})
	var closeGet sync.Once
	t.Cleanup(func() { closeGet.Do(func() { close(releaseGet) }) })
	var oldWrites atomic.Int32
	oldArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			close(getStarted)
			<-releaseGet
			_, _ = w.Write([]byte(`[]`))
			return
		}
		oldWrites.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(oldArr.Close)
	var newWrites atomic.Int32
	newArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			newWrites.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(newArr.Close)

	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: oldArr.URL, APIKey: "old-key", IsDefault: true}
	server, store := newSettingsToolServerWithStore(t, []*instance.Instance{inst})
	type callResult struct {
		result *ToolResult
		err    error
	}
	done := make(chan callResult, 1)
	go func() {
		result, callErr := server.ExecuteTool(context.Background(), "upsert_custom_format", json.RawMessage(`{"service":"radarr","instance_id":"`+inst.ID+`","custom_format":{"name":"x265","specifications":[]}}`), adminCallContext())
		done <- callResult{result: result, err: callErr}
	}()
	<-getStarted
	inst.URL = newArr.URL
	inst.APIKey = "new-key"
	if err := store.Update(inst); err != nil {
		t.Fatalf("repoint instance: %v", err)
	}
	// Deliberately do not invalidate the registry cache: this pins the narrow
	// Store.Update-before-InvalidateClient interval in the HTTP handler.
	closeGet.Do(func() { close(releaseGet) })
	got := <-done
	if got.err != nil || got.result == nil || !strings.Contains(got.result.Text, "instance changed") || !strings.Contains(got.result.Text, "No custom format was changed") {
		t.Fatalf("repoint result=%#v err=%v", got.result, got.err)
	}
	if oldWrites.Load() != 0 || newWrites.Load() != 0 {
		t.Fatalf("repointed call wrote old=%d new=%d", oldWrites.Load(), newWrites.Load())
	}
}

func TestUpsertCustomFormatPreservesPartialStateWhenVerificationIsCanceled(t *testing.T) {
	profileStarted := make(chan struct{})
	arr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/customformat":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/customformat":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":5,"name":"x265"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/qualityprofile":
			close(profileStarted)
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(arr.Close)
	inst := &instance.Instance{ServiceType: "radarr", Name: "Main", URL: arr.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, callErr := server.ExecuteTool(ctx, "upsert_custom_format", json.RawMessage(`{"service":"radarr","custom_format":{"name":"x265","specifications":[]}}`), adminCallContext())
		done <- callErr
	}()
	<-profileStarted
	cancel()
	callErr := <-done
	if callErr == nil || callErr == context.Canceled || !strings.Contains(callErr.Error(), `custom format 5 ("x265") was created`) || !strings.Contains(callErr.Error(), "verifying that every quality profile") {
		t.Fatalf("partial cancellation error = %T %v", callErr, callErr)
	}
}
