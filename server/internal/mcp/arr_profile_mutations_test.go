package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	arrcommon "github.com/windoze95/cantinarr-server/internal/arr"
)

const profileMutationDefaultItems = `[
	{"quality":{"id":0,"name":"Unknown"},"items":[],"allowed":false,"futureLeaf":"keep-unknown"},
	{"quality":{"id":2,"name":"DVD"},"items":[],"allowed":false,"futureLeaf":"keep-dvd"},
	{"id":1000,"name":"WEB","allowed":true,"futureGroup":"keep-group","items":[
		{"quality":{"id":3,"name":"WEB-720p"},"items":[],"allowed":true,"futureLeaf":"keep-720"},
		{"quality":{"id":7,"name":"WEB-1080p"},"items":[],"allowed":true,"futureLeaf":"keep-1080"}
	]},
	{"quality":{"id":8,"name":"Bluray-1080p"},"items":[],"allowed":true}
]`

const profileMutationDefaultFormatItems = `[
	{"id":41,"format":4,"name":" x265 ","score":0,"futureFormat":"keep-x265"},
	{"id":31,"format":3,"name":"Not English","score":0,"futureFormat":"keep-language"}
]`

var profileMutationFormats = []json.RawMessage{
	json.RawMessage(`{"id":3,"name":"Not English","specifications":[{"name":"language","implementation":"LanguageSpecification","fields":[]}]}`),
	json.RawMessage(`{"id":4,"name":" x265 ","specifications":[{"name":"codec","implementation":"ReleaseTitleSpecification","fields":[]}]}`),
}

func profileMutationFixture(items, formatItems, language string) json.RawMessage {
	if items == "" {
		items = profileMutationDefaultItems
	}
	if formatItems == "" {
		formatItems = profileMutationDefaultFormatItems
	}
	if language == "" {
		language = `{"id":-1,"name":"Any"}`
	}
	return json.RawMessage(fmt.Sprintf(`{
		"id":1,"name":"HD","upgradeAllowed":true,"cutoff":1000,
		"items":%s,"formatItems":%s,
		"minFormatScore":0,"cutoffFormatScore":100,"minUpgradeFormatScore":1,
		"language":%s,
		"futureTop":{"keep":[1,2,3]}
	}`, items, formatItems, language))
}

func mutationTestPtr[T any](value T) *T { return &value }

func TestResolveProfileChangePlanPreservesFullObjectAndOrdering(t *testing.T) {
	changes := profileChangesInput{
		UpgradeAllowed:        mutationTestPtr(false),
		QualityCutoffID:       mutationTestPtr(8),
		MinFormatScore:        mutationTestPtr[int64](-50),
		CutoffFormatScore:     mutationTestPtr[int64](200),
		MinUpgradeFormatScore: mutationTestPtr[int64](2),
		CustomFormatScores: []profileFormatScoreChange{
			{FormatName: " x265 ", Score: 10},
			{FormatName: "Not English", Score: -10000},
		},
	}

	plan, body, diff, name, err := resolveProfileChangePlan("radarr", profileMutationFixture("", "", ""), profileMutationFormats, nil, changes)
	if err != nil {
		t.Fatalf("resolveProfileChangePlan: %v", err)
	}
	if name != "HD" || len(diff) != 7 {
		t.Fatalf("name=%q diff=%v", name, diff)
	}
	if len(plan.CustomFormatScores) != 2 || plan.CustomFormatScores[0].FormatID != 3 || plan.CustomFormatScores[1].FormatName != " x265 " {
		t.Fatalf("normalized score plan = %#v", plan.CustomFormatScores)
	}

	object, err := decodeJSONObject(body)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if object["futureTop"] == nil || object["upgradeAllowed"] != false {
		t.Fatalf("top-level fields were lost or not changed: %#v", object)
	}
	items := object["items"].([]any)
	if exact, _ := exactJSONInt(items[0].(map[string]any)["quality"].(map[string]any)["id"]); exact != 0 {
		t.Fatalf("quality order changed: %#v", items)
	}
	if items[0].(map[string]any)["futureLeaf"] != "keep-unknown" {
		t.Fatalf("Unknown quality fields were lost: %#v", items[0])
	}
	group := items[2].(map[string]any)
	children := group["items"].([]any)
	if group["futureGroup"] != "keep-group" || children[0].(map[string]any)["futureLeaf"] != "keep-720" {
		t.Fatalf("nested quality fields were lost: %#v", group)
	}
	formatItems := object["formatItems"].([]any)
	first := formatItems[0].(map[string]any)
	second := formatItems[1].(map[string]any)
	firstFormat, _ := exactJSONInt(first["format"])
	firstScore, _ := exactJSONInt64(first["score"])
	secondFormat, _ := exactJSONInt(second["format"])
	secondScore, _ := exactJSONInt64(second["score"])
	if firstFormat != 4 || firstScore != 10 || secondFormat != 3 || secondScore != -10000 {
		t.Fatalf("format item order/scores = %#v", formatItems)
	}
	if first["futureFormat"] != "keep-x265" || second["futureFormat"] != "keep-language" {
		t.Fatalf("unmodeled format-item fields were lost: %#v", formatItems)
	}
}

func TestProfileCutoffValidationRejectsNestedCutoffAndAmbiguity(t *testing.T) {
	plan := profileChangePlan{QualityCutoffID: mutationTestPtr(3)}
	if _, _, err := mutateProfileWithPlan("sonarr", profileMutationFixture("", "", ""), profileMutationFormats, plan); err == nil || !strings.Contains(err.Error(), "not an allowed quality or group") {
		t.Fatalf("nested cutoff err = %v", err)
	}

	duplicateItems := `[
		{"quality":{"id":3,"name":"Duplicate"},"items":[],"allowed":true},
		{"id":1000,"name":"WEB","allowed":true,"items":[
			{"quality":{"id":3,"name":"WEB-720p"},"items":[],"allowed":true}
		]}
	]`
	if _, _, err := mutateProfileWithPlan("sonarr", profileMutationFixture(duplicateItems, "", ""), profileMutationFormats, profileChangePlan{UpgradeAllowed: mutationTestPtr(false)}); err == nil || !strings.Contains(err.Error(), "appears more than once") {
		t.Fatalf("duplicate cutoff id err = %v", err)
	}

	malformedItems := `[{"quality":{"id":3,"name":"WEB-720p"},"allowed":true,"items":[{"quality":{"id":7,"name":"nested"},"allowed":true,"items":[]}]}]`
	if _, _, err := mutateProfileWithPlan("sonarr", profileMutationFixture(malformedItems, "", ""), profileMutationFormats, profileChangePlan{UpgradeAllowed: mutationTestPtr(false)}); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed cutoff tree err = %v", err)
	}
}

func TestProfileCutoffValidationRejectsMissingAllowedField(t *testing.T) {
	items := `[{"quality":{"id":3,"name":"WEB-720p"},"items":[]}]`
	_, _, err := mutateProfileWithPlan("sonarr", profileMutationFixture(items, "", ""), profileMutationFormats, profileChangePlan{UpgradeAllowed: mutationTestPtr(false)})
	if err == nil || !strings.Contains(err.Error(), "invalid items") {
		t.Fatalf("missing allowed field err = %v", err)
	}
}

func TestProfileCutoffAllowsChaptarrQualityIDZero(t *testing.T) {
	items := `[
		{"quality":{"id":0,"name":"Unknown Text"},"items":[],"allowed":true},
		{"quality":{"id":1,"name":"PDF"},"items":[],"allowed":true}
	]`
	profile := strings.Replace(string(profileMutationFixture(items, "", "")), `"cutoff":1000`, `"cutoff":1`, 1)
	body, diff, err := mutateProfileWithPlan("chaptarr", json.RawMessage(profile), profileMutationFormats, profileChangePlan{QualityCutoffID: mutationTestPtr(0)})
	if err != nil {
		t.Fatalf("mutate ID-0 cutoff: %v", err)
	}
	object, decodeErr := decodeJSONObject(body)
	cutoff, _ := exactJSONInt(object["cutoff"])
	if decodeErr != nil || cutoff != 0 || len(diff) != 1 {
		t.Fatalf("ID-0 cutoff body=%s diff=%v decodeErr=%v", body, diff, decodeErr)
	}
}

func TestProfileFormatItemsMustExactlyMatchCustomFormatCollection(t *testing.T) {
	tests := []struct {
		name        string
		formatItems string
		formats     []json.RawMessage
		want        string
	}{
		{
			name:        "missing item",
			formatItems: `[{"format":3,"score":0}]`,
			formats:     profileMutationFormats,
			want:        "1 formatItems but the service has 2",
		},
		{
			name:        "extra item",
			formatItems: `[{"format":3,"score":0},{"format":4,"score":0},{"format":9,"score":0}]`,
			formats:     profileMutationFormats,
			want:        "3 formatItems but the service has 2",
		},
		{
			name:        "duplicate item",
			formatItems: `[{"format":3,"score":0},{"format":3,"score":1}]`,
			formats:     []json.RawMessage{profileMutationFormats[0]},
			want:        "more than once",
		},
		{
			name:        "duplicate format id",
			formatItems: profileMutationDefaultFormatItems,
			formats: []json.RawMessage{
				json.RawMessage(`{"id":3,"name":"A","specifications":[]}`),
				json.RawMessage(`{"id":3,"name":"B","specifications":[]}`),
			},
			want: "multiple custom formats use id 3",
		},
		{
			name:        "duplicate format name",
			formatItems: profileMutationDefaultFormatItems,
			formats: []json.RawMessage{
				json.RawMessage(`{"id":3,"name":"same","specifications":[]}`),
				json.RawMessage(`{"id":4,"name":"same","specifications":[]}`),
			},
			want: `multiple custom formats are named exactly "same"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := mutateProfileWithPlan("sonarr", profileMutationFixture("", tt.formatItems, ""), tt.formats, profileChangePlan{UpgradeAllowed: mutationTestPtr(false)})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestResolveProfileChangePlanUsesExactWhitespaceBearingFormatName(t *testing.T) {
	plan, body, _, _, err := resolveProfileChangePlan("sonarr", profileMutationFixture("", "", ""), profileMutationFormats, nil, profileChangesInput{
		CustomFormatScores: []profileFormatScoreChange{{FormatName: " x265 ", Score: 25}},
	})
	if err != nil {
		t.Fatalf("resolve whitespace-bearing name: %v", err)
	}
	if len(plan.CustomFormatScores) != 1 || plan.CustomFormatScores[0].FormatName != " x265 " {
		t.Fatalf("plan = %#v", plan.CustomFormatScores)
	}
	if !strings.Contains(string(body), `"name":" x265 "`) || !strings.Contains(string(body), `"score":25`) {
		t.Fatalf("body = %s", body)
	}
}

func TestRadarrLanguageCustomFormatRequiresAnyProfileLanguage(t *testing.T) {
	englishProfile := profileMutationFixture("", "", `{"id":1,"name":"English","futureLanguage":"keep-language"}`)
	changes := profileChangesInput{
		CustomFormatScores: []profileFormatScoreChange{{FormatName: "Not English", Score: -10000}},
	}
	if _, _, _, _, err := resolveProfileChangePlan("radarr", englishProfile, profileMutationFormats, nil, changes); err == nil || !strings.Contains(err.Error(), `include language_name "Any"`) {
		t.Fatalf("language trap err = %v", err)
	}

	changes.LanguageName = mutationTestPtr("Any")
	languages := []json.RawMessage{
		json.RawMessage(`{"id":-1,"name":"Any"}`),
		json.RawMessage(`{"id":1,"name":"English"}`),
	}
	_, body, diff, _, err := resolveProfileChangePlan("radarr", englishProfile, profileMutationFormats, languages, changes)
	if err != nil {
		t.Fatalf("language + score plan: %v", err)
	}
	if !strings.Contains(string(body), `"score":-10000`) {
		t.Fatalf("body = %s", body)
	}
	object, decodeErr := decodeJSONObject(body)
	if decodeErr != nil {
		t.Fatalf("decode body: %v", decodeErr)
	}
	language := object["language"].(map[string]any)
	languageID, _ := exactJSONInt(language["id"])
	if languageID != -1 || language["name"] != "Any" || language["futureLanguage"] != "keep-language" {
		t.Fatalf("Radarr language replacement lost fields: %#v", language)
	}
	if len(diff) != 2 {
		t.Fatalf("diff = %v", diff)
	}

	for _, service := range []string{"sonarr", "chaptarr"} {
		if err := validateProfileChangesInput(service, profileChangesInput{LanguageName: mutationTestPtr("English")}); err == nil || !strings.Contains(err.Error(), "only for Radarr") {
			t.Errorf("%s language_name err = %v", service, err)
		}
	}
}

func TestProfileChangeInputBoundsAndNoOp(t *testing.T) {
	overflowScores := make([]profileFormatScoreChange, maxProfileFormatScoreChanges+1)
	tests := []struct {
		name    string
		service string
		changes profileChangesInput
		want    string
	}{
		{name: "empty", service: "radarr", changes: profileChangesInput{}, want: "at least one"},
		{name: "cutoff negative", service: "radarr", changes: profileChangesInput{QualityCutoffID: mutationTestPtr(-1)}, want: "zero or positive"},
		{name: "score threshold overflow", service: "radarr", changes: profileChangesInput{MinFormatScore: mutationTestPtr[int64](math.MaxInt32 + 1)}, want: "signed 32-bit"},
		{name: "upgrade threshold zero", service: "radarr", changes: profileChangesInput{MinUpgradeFormatScore: mutationTestPtr[int64](0)}, want: "at least 1"},
		{name: "too many scores", service: "radarr", changes: profileChangesInput{CustomFormatScores: overflowScores}, want: "256-item limit"},
		{name: "blank format name", service: "radarr", changes: profileChangesInput{CustomFormatScores: []profileFormatScoreChange{{FormatName: " \t ", Score: 1}}}, want: "must be nonblank"},
		{name: "long format name", service: "radarr", changes: profileChangesInput{CustomFormatScores: []profileFormatScoreChange{{FormatName: strings.Repeat("x", 257), Score: 1}}}, want: "256-byte limit"},
		{name: "score overflow", service: "radarr", changes: profileChangesInput{CustomFormatScores: []profileFormatScoreChange{{FormatName: "x", Score: math.MinInt32 - 1}}}, want: "signed 32-bit"},
		{name: "long language", service: "radarr", changes: profileChangesInput{LanguageName: mutationTestPtr(strings.Repeat("x", 257))}, want: "256-byte limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProfileChangesInput(tt.service, tt.changes)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}

	_, _, _, _, err := resolveProfileChangePlan("sonarr", profileMutationFixture("", "", ""), profileMutationFormats, nil, profileChangesInput{UpgradeAllowed: mutationTestPtr(true)})
	if err == nil || !strings.Contains(err.Error(), "already match") {
		t.Fatalf("no-op err = %v", err)
	}

	_, _, _, _, err = resolveProfileChangePlan("sonarr", profileMutationFixture("", "", ""), profileMutationFormats, nil, profileChangesInput{
		CustomFormatScores: []profileFormatScoreChange{{FormatName: " x265 ", Score: 1}, {FormatName: " x265 ", Score: 2}},
	})
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate requested score err = %v", err)
	}
}

type profileMutationFake struct {
	profiles           []json.RawMessage
	afterWriteProfiles []json.RawMessage
	getProfilesErr     error
	updateErr          error
	updateCalls        int
	updateID           int
	updateBody         json.RawMessage
}

func (f *profileMutationFake) GetQualityProfilesRawContext(context.Context) ([]json.RawMessage, error) {
	if f.getProfilesErr != nil {
		return nil, f.getProfilesErr
	}
	profiles := f.profiles
	if f.updateCalls > 0 && f.afterWriteProfiles != nil {
		profiles = f.afterWriteProfiles
	}
	return append([]json.RawMessage(nil), profiles...), nil
}

func (f *profileMutationFake) GetCustomFormatsRawContext(context.Context) ([]json.RawMessage, error) {
	return nil, nil
}

func (f *profileMutationFake) UpdateQualityProfileRawContext(_ context.Context, id int, body json.RawMessage) (json.RawMessage, error) {
	f.updateCalls++
	f.updateID = id
	f.updateBody = append(json.RawMessage(nil), body...)
	return json.RawMessage(`{"accepted":true}`), f.updateErr
}

func TestUpdateQualityProfileHelperRejectsRouteBodyMismatchBeforeGuardOrWrite(t *testing.T) {
	body := json.RawMessage(`{"id":2,"name":"HD","items":[],"formatItems":[]}`)
	fake := &profileMutationFake{}
	guardCalls := 0
	err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, func(context.Context) error {
		guardCalls++
		return nil
	})
	var notStarted *MutationNotStartedError
	if !errors.As(err, &notStarted) || fake.updateCalls != 0 || guardCalls != 0 {
		t.Fatalf("err=%v updateCalls=%d guardCalls=%d", err, fake.updateCalls, guardCalls)
	}
}

func TestUpdateQualityProfileHelperOutcomesAndReadback(t *testing.T) {
	const bodyText = `{"id":1,"name":"HD","items":[],"formatItems":[{"format":2,"score":20},{"format":1,"score":10}],"future":{"a":1,"b":2}}`
	const reorderedText = `{ "future":{"b":2,"a":1}, "formatItems":[{"score":10,"format":1},{"score":20,"format":2}], "name":"HD", "items":[], "id":1 }`
	body := json.RawMessage(bodyText)

	t.Run("canonical readback success", func(t *testing.T) {
		fake := &profileMutationFake{afterWriteProfiles: []json.RawMessage{json.RawMessage(reorderedText)}}
		guardCalls := 0
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, func(context.Context) error {
			guardCalls++
			return nil
		})
		if err != nil || fake.updateCalls != 1 || fake.updateID != 1 || string(fake.updateBody) != bodyText || guardCalls != 1 {
			t.Fatalf("err=%v fake=%+v guardCalls=%d", err, fake, guardCalls)
		}
	})

	t.Run("guard failure is not started", func(t *testing.T) {
		guardErr := errors.New("authorization changed")
		fake := &profileMutationFake{}
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, func(context.Context) error { return guardErr })
		var notStarted *MutationNotStartedError
		if !errors.As(err, &notStarted) || !errors.Is(err, guardErr) || fake.updateCalls != 0 {
			t.Fatalf("err=%v updateCalls=%d", err, fake.updateCalls)
		}
	})

	t.Run("unknown write outcome is partial", func(t *testing.T) {
		fake := &profileMutationFake{updateErr: &arrcommon.SettingsWriteOutcomeUnknownError{Detail: "outcome unknown"}}
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, nil)
		var partial *PartialMutationError
		if !errors.As(err, &partial) || fake.updateCalls != 1 {
			t.Fatalf("err=%v updateCalls=%d", err, fake.updateCalls)
		}
	})

	t.Run("definitive write rejection remains definitive", func(t *testing.T) {
		writeErr := errors.New("returned status 400")
		fake := &profileMutationFake{updateErr: writeErr}
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, nil)
		var partial *PartialMutationError
		if !errors.Is(err, writeErr) || errors.As(err, &partial) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("readback failure is partial", func(t *testing.T) {
		fake := &profileMutationFake{getProfilesErr: errors.New("read failed")}
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, nil)
		var partial *PartialMutationError
		if !errors.As(err, &partial) || !strings.Contains(err.Error(), "reading the updated profile") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("mismatched readback is partial", func(t *testing.T) {
		fake := &profileMutationFake{afterWriteProfiles: []json.RawMessage{json.RawMessage(`{"id":1,"name":"HD","items":[],"formatItems":[],"future":{"a":9,"b":2}}`)}}
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, nil)
		var partial *PartialMutationError
		if !errors.As(err, &partial) || !strings.Contains(err.Error(), "differs") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("ambiguous profile readback is partial", func(t *testing.T) {
		fake := &profileMutationFake{afterWriteProfiles: []json.RawMessage{body, body}}
		err := UpdateQualityProfileHelper(context.Background(), fake, 1, body, nil)
		var partial *PartialMutationError
		if !errors.As(err, &partial) || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("canceled before guard dispatches nothing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		fake := &profileMutationFake{}
		err := UpdateQualityProfileHelper(ctx, fake, 1, body, nil)
		var notStarted *MutationNotStartedError
		if !errors.As(err, &notStarted) || !errors.Is(err, context.Canceled) || fake.updateCalls != 0 {
			t.Fatalf("err=%v updateCalls=%d", err, fake.updateCalls)
		}
	})
}

func TestCanonicalJSONCollectionHashNormalizesOrderAndRejectsInvalidIDs(t *testing.T) {
	first := []json.RawMessage{
		json.RawMessage(`{"id":2,"name":"B","specifications":[{"implementation":"X"}]}`),
		json.RawMessage(`{"id":1,"name":"A","specifications":[]}`),
	}
	second := []json.RawMessage{
		json.RawMessage(`{ "specifications":[], "name":"A", "id":1 }`),
		json.RawMessage(`{"name":"B","id":2,"specifications":[{"implementation":"X"}]}`),
	}
	firstHash, err := canonicalJSONCollectionHash(first)
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	secondHash, err := canonicalJSONCollectionHash(second)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if firstHash != secondHash {
		t.Fatal("irrelevant collection/object order changed the canonical hash")
	}

	changed, err := canonicalJSONCollectionHash([]json.RawMessage{
		json.RawMessage(`{"id":1,"name":"A","specifications":[]}`),
		json.RawMessage(`{"id":2,"name":"B","specifications":[{"implementation":"Y"}]}`),
	})
	if err != nil || changed == firstHash {
		t.Fatalf("semantic change hash=%x err=%v", changed, err)
	}

	for name, values := range map[string][]json.RawMessage{
		"duplicate": {json.RawMessage(`{"id":1}`), json.RawMessage(`{"id":1}`)},
		"zero":      {json.RawMessage(`{"id":0}`)},
		"negative":  {json.RawMessage(`{"id":-1}`)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := canonicalJSONCollectionHash(values); err == nil {
				t.Fatal("invalid collection id was accepted")
			}
		})
	}
}
