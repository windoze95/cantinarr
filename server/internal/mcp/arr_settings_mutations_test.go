package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	arrcommon "github.com/windoze95/cantinarr-server/internal/arr"
)

type fakeCustomFormatMutator struct {
	mu          sync.Mutex
	formats     []json.RawMessage
	createBody  json.RawMessage
	updateID    int
	updateBody  json.RawMessage
	createCalls int
	updateCalls int
	createRaw   json.RawMessage
	createErr   error
	updateRaw   json.RawMessage
	updateErr   error
	getErr      error
	getErrs     []error
	getCalls    int
	createStore json.RawMessage
	profiles    []json.RawMessage
	profilesErr error
}

func (f *fakeCustomFormatMutator) GetQualityProfilesRawContext(context.Context) ([]json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.profilesErr != nil {
		return nil, f.profilesErr
	}
	return append([]json.RawMessage(nil), f.profiles...), nil
}

func (f *fakeCustomFormatMutator) GetCustomFormatsRawContext(context.Context) ([]json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := f.getCalls
	f.getCalls++
	if call < len(f.getErrs) && f.getErrs[call] != nil {
		return nil, f.getErrs[call]
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	return append([]json.RawMessage(nil), f.formats...), nil
}

func (f *fakeCustomFormatMutator) CreateCustomFormatRawContext(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.createBody = append(json.RawMessage(nil), body...)
	stored := f.createStore
	if len(stored) == 0 {
		var responseHead customFormatHead
		var object map[string]any
		if json.Unmarshal(f.createRaw, &responseHead) == nil && responseHead.ID > 0 &&
			json.Unmarshal(body, &object) == nil {
			object["id"] = responseHead.ID
			stored, _ = json.Marshal(object)
		} else {
			stored = f.createRaw
		}
	}
	if f.createErr == nil && len(stored) > 0 {
		f.formats = append(f.formats, append(json.RawMessage(nil), stored...))
	}
	return f.createRaw, f.createErr
}

func (f *fakeCustomFormatMutator) UpdateCustomFormatRawContext(_ context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	f.updateID = id
	f.updateBody = append(json.RawMessage(nil), body...)
	if f.updateErr == nil {
		for i, raw := range f.formats {
			head, err := decodeCustomFormatHead(raw)
			if err == nil && head.ID == id {
				f.formats[i] = append(json.RawMessage(nil), body...)
				break
			}
		}
	}
	return f.updateRaw, f.updateErr
}

func TestUpsertCustomFormatCreatesFromTrashShapeAndIgnoresCallerID(t *testing.T) {
	fake := &fakeCustomFormatMutator{createRaw: json.RawMessage(`{"id":12,"name":"Not English"}`)}
	payload := json.RawMessage(`{
		"id":999,
		"name":"Not English",
		"trash_id":"trash-123",
		"trash_scores":{"default":-10000},
		"futureField":{"keep":true},
		"specifications":[{
			"name":"Not English",
			"implementation":"LanguageSpecification",
			"negate":true,
			"fields":{"value":1,"exceptLanguage":false}
		}]
	}`)

	result, err := UpsertCustomFormatHelper(context.Background(), fake, payload, nil)
	if err != nil {
		t.Fatalf("UpsertCustomFormatHelper: %v", err)
	}
	if result.Action != "created" || result.ID != 12 || result.Name != "Not English" {
		t.Fatalf("result = %+v", result)
	}
	if fake.createCalls != 1 || fake.updateCalls != 0 {
		t.Fatalf("calls = create %d update %d", fake.createCalls, fake.updateCalls)
	}

	var body map[string]any
	if err := json.Unmarshal(fake.createBody, &body); err != nil {
		t.Fatalf("decode create body: %v", err)
	}
	if _, exists := body["id"]; exists {
		t.Fatalf("caller id survived create: %s", fake.createBody)
	}
	if body["name"] != "Not English" || body["trash_id"] != "trash-123" || body["futureField"] == nil {
		t.Fatalf("top-level fields were not preserved: %#v", body)
	}
	specs := body["specifications"].([]any)
	fields := specs[0].(map[string]any)["fields"].([]any)
	if len(fields) != 2 || fields[0].(map[string]any)["name"] != "exceptLanguage" || fields[1].(map[string]any)["name"] != "value" {
		t.Fatalf("TRaSH fields were not converted deterministically: %#v", fields)
	}
}

func TestUpsertCustomFormatUpdatesFreshFullObjectAndPreservesUnknownFields(t *testing.T) {
	fake := &fakeCustomFormatMutator{
		formats: []json.RawMessage{json.RawMessage(`{
			"id":4,"name":"Not English","includeCustomFormatWhenRenaming":false,
			"futureField":{"keep":"me"},
			"specifications":[{"name":"old","implementation":"ReleaseTitleSpecification","fields":[{"name":"value","value":"old"}]}]
		}`)},
		updateRaw: json.RawMessage(`{"id":4,"name":"Not English"}`),
	}
	payload := json.RawMessage(`{
		"id":999,"name":"Not English","trash_description":"new rules",
		"specifications":[{"name":"new","implementation":"ReleaseTitleSpecification","fields":[{"name":"value","value":"new"}]}]
	}`)

	result, err := UpsertCustomFormatHelper(context.Background(), fake, payload, nil)
	if err != nil {
		t.Fatalf("UpsertCustomFormatHelper: %v", err)
	}
	if result.Action != "updated" || result.ID != 4 || result.Name != "Not English" {
		t.Fatalf("result = %+v", result)
	}
	if fake.updateCalls != 1 || fake.createCalls != 0 || fake.updateID != 4 {
		t.Fatalf("calls = create %d update %d id %d", fake.createCalls, fake.updateCalls, fake.updateID)
	}

	var body map[string]any
	if err := json.Unmarshal(fake.updateBody, &body); err != nil {
		t.Fatalf("decode update body: %v", err)
	}
	if body["id"] != float64(4) || body["futureField"] == nil || body["includeCustomFormatWhenRenaming"] != false {
		t.Fatalf("live full-object fields were not preserved/rebound: %#v", body)
	}
	specs := body["specifications"].([]any)
	if specs[0].(map[string]any)["name"] != "new" {
		t.Fatalf("incoming specifications did not replace the old rules: %#v", specs)
	}
	fields := specs[0].(map[string]any)["fields"].([]any)
	if fields[0].(map[string]any)["value"] != "new" {
		t.Fatalf("native fields array changed shape: %#v", fields)
	}
}

func TestUpsertCustomFormatUsesExactNameIdentity(t *testing.T) {
	fake := &fakeCustomFormatMutator{
		formats:   []json.RawMessage{json.RawMessage(`{"id":4,"name":"HDR10","specifications":[]}`)},
		createRaw: json.RawMessage(`{"id":9,"name":"hdr10"}`),
	}
	payload := json.RawMessage(`{"name":"hdr10","specifications":[]}`)

	result, err := UpsertCustomFormatHelper(context.Background(), fake, payload, nil)
	if err != nil {
		t.Fatalf("UpsertCustomFormatHelper: %v", err)
	}
	if result.Action != "created" || result.ID != 9 || fake.createCalls != 1 || fake.updateCalls != 0 {
		t.Fatalf("case-distinct upsert = %+v; create=%d update=%d", result, fake.createCalls, fake.updateCalls)
	}
}

func TestUpsertCustomFormatPreservesNameWhitespaceForIdentity(t *testing.T) {
	fake := &fakeCustomFormatMutator{createRaw: json.RawMessage(`{"id":8,"name":" x265 "}`)}
	result, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(`{"name":" x265 ","specifications":[]}`), nil)
	if err != nil {
		t.Fatalf("UpsertCustomFormatHelper: %v", err)
	}
	if result.Name != " x265 " || !strings.Contains(string(fake.createBody), `"name":" x265 "`) {
		t.Fatalf("name whitespace was changed: result=%+v body=%s", result, fake.createBody)
	}
}

func TestUpsertCustomFormatRepeatIsAnUnrecordedNoOp(t *testing.T) {
	fake := &fakeCustomFormatMutator{createRaw: json.RawMessage(`{"id":7,"name":"x265"}`)}
	payload := json.RawMessage(`{"name":"x265","specifications":[]}`)

	first, err := UpsertCustomFormatHelper(context.Background(), fake, payload, nil)
	if err != nil || first.Action != "created" {
		t.Fatalf("first upsert = %+v, %v", first, err)
	}
	second, err := UpsertCustomFormatHelper(context.Background(), fake, payload, nil)
	if err != nil || second.Action != "unchanged" || second.ID != 7 {
		t.Fatalf("second upsert = %+v, %v", second, err)
	}
	if fake.createCalls != 1 || fake.updateCalls != 0 {
		t.Fatalf("calls = create %d update %d", fake.createCalls, fake.updateCalls)
	}
}

func TestUpsertCustomFormatConfirmsCreateAfterUnusableSuccessResponse(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response json.RawMessage
	}{
		{name: "empty response"},
		{name: "malformed response", response: json.RawMessage(`{"id":`)},
		{name: "wrong-name response", response: json.RawMessage(`{"id":7,"name":"other"}`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeCustomFormatMutator{
				createRaw:   tt.response,
				createStore: json.RawMessage(`{"id":7,"name":"x265","specifications":[]}`),
			}
			result, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
			if err != nil || result.Action != "created" || result.ID != 7 || fake.getCalls != 2 || fake.createCalls != 1 {
				t.Fatalf("confirmed create = %+v err=%v gets=%d creates=%d", result, err, fake.getCalls, fake.createCalls)
			}
		})
	}
}

func TestUpsertCustomFormatCreateConfirmationFailureIsPartial(t *testing.T) {
	fake := &fakeCustomFormatMutator{
		createRaw: json.RawMessage(`{"id":`),
		getErrs:   []error{nil, errors.New("confirmation unavailable")},
	}
	_, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
	var partial *PartialMutationError
	if !errors.As(err, &partial) || !strings.Contains(err.Error(), "create was accepted") || fake.createCalls != 1 {
		t.Fatalf("confirmation failure = %T %v", err, err)
	}
}

func TestUpsertCustomFormatVerifiesCreatedFormatInEveryProfile(t *testing.T) {
	fake := &fakeCustomFormatMutator{
		createRaw: json.RawMessage(`{"id":7,"name":"x265"}`),
		profiles: []json.RawMessage{
			json.RawMessage(`{"id":1,"formatItems":[{"format":7,"score":0}]}`),
			json.RawMessage(`{"id":2,"formatItems":[{"format":3,"score":10},{"format":7,"score":0}]}`),
		},
	}
	result, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
	if err != nil || result.Action != "created" {
		t.Fatalf("verified create = %+v err=%v", result, err)
	}
}

func TestUpsertCustomFormatProfileSideEffectFailureIsPartial(t *testing.T) {
	for _, tt := range []struct {
		name        string
		profiles    []json.RawMessage
		profilesErr error
	}{
		{name: "profile read failed", profilesErr: errors.New("profile read unavailable")},
		{name: "missing format", profiles: []json.RawMessage{json.RawMessage(`{"id":1,"formatItems":[]}`)}},
		{name: "nonzero score", profiles: []json.RawMessage{json.RawMessage(`{"id":1,"formatItems":[{"format":7,"score":10}]}`)}},
		{name: "duplicate format", profiles: []json.RawMessage{json.RawMessage(`{"id":1,"formatItems":[{"format":7,"score":0},{"format":7,"score":0}]}`)}},
		{name: "malformed profile", profiles: []json.RawMessage{json.RawMessage(`{"id":`)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeCustomFormatMutator{
				createRaw:   json.RawMessage(`{"id":7,"name":"x265"}`),
				profiles:    tt.profiles,
				profilesErr: tt.profilesErr,
			}
			_, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
			var partial *PartialMutationError
			if !errors.As(err, &partial) || !strings.Contains(err.Error(), "was created") || !strings.Contains(err.Error(), "every quality profile") {
				t.Fatalf("profile side-effect failure = %T %v", err, err)
			}
		})
	}
}

func TestUpsertCustomFormatUnknownWriteOutcomesArePartial(t *testing.T) {
	unknown := &arrcommon.SettingsWriteOutcomeUnknownError{Detail: "write outcome unknown"}
	for _, tt := range []struct {
		name string
		fake *fakeCustomFormatMutator
	}{
		{name: "create", fake: &fakeCustomFormatMutator{createErr: unknown}},
		{name: "update", fake: &fakeCustomFormatMutator{
			formats:   []json.RawMessage{json.RawMessage(`{"id":7,"name":"x265","specifications":[{"name":"old","fields":[]}]}`)},
			updateErr: unknown,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := UpsertCustomFormatHelper(context.Background(), tt.fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
			var partial *PartialMutationError
			if !errors.As(err, &partial) || !strings.Contains(err.Error(), "may have been accepted") {
				t.Fatalf("unknown outcome = %T %v", err, err)
			}
		})
	}
}

func TestUpsertCustomFormatInitialReadFailureIsNotStarted(t *testing.T) {
	cause := errors.New("collection unavailable")
	fake := &fakeCustomFormatMutator{getErr: cause}
	_, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
	var notStarted *MutationNotStartedError
	if !errors.As(err, &notStarted) || !errors.Is(err, cause) || fake.createCalls != 0 || fake.updateCalls != 0 {
		t.Fatalf("initial read failure = %T %v", err, err)
	}
}

func TestUpsertCustomFormatCanceledBeforePreflightDispatchesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeCustomFormatMutator{}
	_, err := UpsertCustomFormatHelper(ctx, fake, json.RawMessage(`{"name":"x265","specifications":[]}`), nil)
	if !errors.Is(err, context.Canceled) || fake.getCalls != 0 || fake.createCalls != 0 || fake.updateCalls != 0 {
		t.Fatalf("canceled helper = %T %v calls=%d/%d/%d", err, err, fake.getCalls, fake.createCalls, fake.updateCalls)
	}
}

func TestUpsertCustomFormatPreflightFailuresDispatchNothing(t *testing.T) {
	tests := []struct {
		name    string
		formats []json.RawMessage
		payload string
		want    string
	}{
		{name: "not object", payload: `[]`, want: "exactly one JSON object"},
		{name: "blank name", payload: `{"name":" ","specifications":[]}`, want: "nonblank string"},
		{name: "long name", payload: `{"name":"` + strings.Repeat("n", maxCustomFormatNameBytes+1) + `","specifications":[]}`, want: "256-byte limit"},
		{name: "specifications not array", payload: `{"name":"x","specifications":{}}`, want: "must be an array"},
		{name: "spec not object", payload: `{"name":"x","specifications":[1]}`, want: "must be an object"},
		{name: "missing fields", payload: `{"name":"x","specifications":[{}]}`, want: ".fields is required"},
		{name: "invalid fields", payload: `{"name":"x","specifications":[{"fields":1}]}`, want: "array or TRaSH-style object"},
		{name: "malformed existing", formats: []json.RawMessage{json.RawMessage(`{"id":1}`)}, payload: `{"name":"x","specifications":[]}`, want: "unreadable id or name"},
		{name: "duplicate exact", formats: []json.RawMessage{json.RawMessage(`{"id":1,"name":"x"}`), json.RawMessage(`{"id":2,"name":"x"}`)}, payload: `{"name":"x","specifications":[]}`, want: "multiple existing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeCustomFormatMutator{formats: tt.formats}
			_, err := UpsertCustomFormatHelper(context.Background(), fake, json.RawMessage(tt.payload), nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			var preflight interface{ MutationNotStarted() bool }
			if !errors.As(err, &preflight) || !preflight.MutationNotStarted() {
				t.Fatalf("error is not a preflight classification: %T %v", err, err)
			}
			if fake.createCalls != 0 || fake.updateCalls != 0 {
				t.Fatalf("preflight failure dispatched create=%d update=%d", fake.createCalls, fake.updateCalls)
			}
		})
	}
}

func TestCustomFormatReadbackAllowsServerFieldsAndArrayReordering(t *testing.T) {
	plan := customFormatUpsertPlan{AfterRaw: json.RawMessage(`{
		"name":"Not English",
		"specifications":[{
			"name":"Language",
			"implementation":"LanguageSpecification",
			"fields":[{"name":"value","value":1},{"name":"exceptLanguage","value":false}]
		}]
	}`)}
	readback := json.RawMessage(`{
		"id":7,
		"name":"Not English",
		"specifications":[{
			"implementationName":"Language",
			"implementation":"LanguageSpecification",
			"name":"Language",
			"fields":[{"name":"exceptLanguage","value":false},{"name":"value","value":1}]
		}]
	}`)
	if err := customFormatReadbackMatchesPlan(plan, readback); err != nil {
		t.Fatalf("compatible normalized readback was rejected: %v", err)
	}

	missing := json.RawMessage(`{"id":7,"name":"Not English","specifications":[]}`)
	if err := customFormatReadbackMatchesPlan(plan, missing); err == nil {
		t.Fatal("readback that dropped the requested specification was accepted")
	}
}

func TestBuildCustomFormatPlanTreatsSpecificationReorderingAsNoOp(t *testing.T) {
	existing := []json.RawMessage{json.RawMessage(`{
		"id":7,"name":"Not English",
		"specifications":[{"name":"Language","implementation":"LanguageSpecification",
			"fields":[{"name":"exceptLanguage","value":false},{"name":"value","value":1}]}]
	}`)}
	payload := json.RawMessage(`{
		"name":"Not English",
		"specifications":[{"name":"Language","implementation":"LanguageSpecification",
			"fields":[{"name":"value","value":1},{"name":"exceptLanguage","value":false}]}]
	}`)
	plan, err := buildCustomFormatUpsertPlan(existing, payload)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Action != "unchanged" {
		t.Fatalf("reordered equivalent plan action = %q, want unchanged", plan.Action)
	}
}
