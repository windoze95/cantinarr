package remediation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// These tests cover the repair path for a season the *arr filled before the
// episodes aired: the delete_media_files action's typed shape and scope binding,
// the aired-only search, the facets those become as standing rule keys, and the
// scoped read the agent needs to see any of it.

func TestDeleteMediaFilesParamsValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		params  string
		wantErr string
		want    string // canonical JSON when valid
	}{
		{
			name:   "tv season with episodes",
			params: `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1,2,3],"blocklist":true}`,
			want:   `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1,2,3],"blocklist":true}`,
		},
		{
			// Canonicalization: the same set of episodes must produce the same
			// bytes — and therefore the same fingerprint — however the model
			// listed them.
			name:   "episodes are sorted and deduplicated",
			params: `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[3,1,2,1,3]}`,
			want:   `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1,2,3]}`,
		},
		{
			name:   "movie needs no season or episodes",
			params: `{"media_type":"movie","tmdb_id":772,"blocklist":true}`,
			want:   `{"media_type":"movie","tmdb_id":772,"blocklist":true}`,
		},
		{
			name:   "specials are a real season",
			params: `{"media_type":"tv","tmdb_id":615,"season":0,"episodes":[4]}`,
			want:   `{"media_type":"tv","tmdb_id":615,"season":0,"episodes":[4]}`,
		},
		{name: "books need the record id", params: `{"media_type":"book","tmdb_id":1}`, wantErr: "book_id"},
		{name: "book shape is exact", params: `{"media_type":"book","book_id":5,"season":1}`, wantErr: "only book_id"},
		{name: "books validate with their id", params: `{"media_type":"book","book_id":5,"blocklist":true}`, want: `{"media_type":"book","book_id":5,"blocklist":true}`},
		{name: "tmdb id is required", params: `{"media_type":"tv","tmdb_id":0,"season":11,"episodes":[1]}`, wantErr: "positive tmdb_id"},
		{name: "tv needs a season", params: `{"media_type":"tv","tmdb_id":615,"episodes":[1]}`, wantErr: "requires a season"},
		{name: "tv needs episodes", params: `{"media_type":"tv","tmdb_id":615,"season":11}`, wantErr: "at least one episode"},
		{name: "tv rejects a zero episode", params: `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[0]}`, wantErr: "must be positive"},
		{name: "movie rejects episodes", params: `{"media_type":"movie","tmdb_id":772,"episodes":[1]}`, wantErr: "only to media_type tv"},
		{name: "movie rejects a season", params: `{"media_type":"movie","tmdb_id":772,"season":1}`, wantErr: "only to media_type tv"},
		{name: "unknown fields are rejected", params: `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1],"path":"/media"}`, wantErr: "invalid params"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, err := validateActionParams(ActionDeleteMediaFiles, json.RawMessage(tc.params))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(canonical) != tc.want {
				t.Errorf("canonical = %s, want %s", canonical, tc.want)
			}
		})
	}
}

// A whole season of impossible files must fit in one proposal, but an episode
// list that has stopped being about one incident must fail loudly rather than
// arrive as a hundred-line approval card.
func TestDeleteMediaFilesBoundsTheEpisodeList(t *testing.T) {
	episodes := make([]string, 0, maxDeleteEpisodes+1)
	for i := 1; i <= maxDeleteEpisodes; i++ {
		episodes = append(episodes, itoa(i))
	}
	atLimit := `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[` + strings.Join(episodes, ",") + `]}`
	if _, err := validateActionParams(ActionDeleteMediaFiles, json.RawMessage(atLimit)); err != nil {
		t.Fatalf("a %d-episode season was rejected: %v", maxDeleteEpisodes, err)
	}
	episodes = append(episodes, itoa(maxDeleteEpisodes+1))
	overLimit := `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[` + strings.Join(episodes, ",") + `]}`
	if _, err := validateActionParams(ActionDeleteMediaFiles, json.RawMessage(overLimit)); err == nil {
		t.Fatalf("an over-limit episode list was accepted")
	}
}

// One problem gets one proposal. Replacing what a bad import destroyed is part
// of delete_media_files, so the agent has no way to propose it separately —
// structurally, not by prompt instruction.
func TestTriggerSearchHasNoAiredOnlyVariant(t *testing.T) {
	_, err := validateActionParams(ActionTriggerSearch,
		json.RawMessage(`{"media_type":"tv","tmdb_id":615,"season":11,"aired_only":true}`))
	if err == nil || !strings.Contains(err.Error(), "invalid params") {
		t.Fatalf("error = %v, want the unknown field rejected", err)
	}
}

func TestTriggerSearchWithoutAiredOnlyKeepsItsOldCanonicalForm(t *testing.T) {
	canonical, err := validateActionParams(ActionTriggerSearch,
		json.RawMessage(`{"media_type":"tv","tmdb_id":615,"season":11}`))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	const want = `{"media_type":"tv","tmdb_id":615,"season":11}`
	if string(canonical) != want {
		t.Fatalf("canonical = %s, want %s", canonical, want)
	}
}

func TestDeleteMediaFilesScopeBinding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seed    string
		params  string
		wantErr string
	}{
		{
			name:   "season-scoped issue accepts the episodes the timeline found",
			seed:   `'user', 'observing', 'tv', 615, 'Futurama', 'sonarr-1', 11, 0`,
			params: `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1,2,3],"blocklist":true}`,
		},
		{
			name:    "another season is refused",
			seed:    `'user', 'observing', 'tv', 615, 'Futurama', 'sonarr-1', 11, 0`,
			params:  `{"media_type":"tv","tmdb_id":615,"season":10,"episodes":[1]}`,
			wantErr: "does not match issue season 11",
		},
		{
			name:    "another title is refused",
			seed:    `'user', 'observing', 'tv', 615, 'Futurama', 'sonarr-1', 11, 0`,
			params:  `{"media_type":"tv","tmdb_id":616,"season":11,"episodes":[1]}`,
			wantErr: "does not match issue tmdb_id",
		},
		{
			name:   "an episode-scoped issue may delete exactly that episode",
			seed:   `'user', 'observing', 'tv', 615, 'Futurama', 'sonarr-1', 11, 3`,
			params: `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[3]}`,
		},
		{
			name:    "an episode-scoped issue may not widen to its neighbours",
			seed:    `'user', 'observing', 'tv', 615, 'Futurama', 'sonarr-1', 11, 3`,
			params:  `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[3,4]}`,
			wantErr: "do not match issue episode 3",
		},
		{
			name:    "media type must match the issue",
			seed:    `'user', 'observing', 'tv', 615, 'Futurama', 'sonarr-1', 11, 0`,
			params:  `{"media_type":"movie","tmdb_id":615}`,
			wantErr: "does not match issue media_type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.Open(":memory:")
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			defer database.Close()
			res, err := database.Exec(
				`INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, season_number, episode_number)
				 VALUES (` + tc.seed + `)`)
			if err != nil {
				t.Fatalf("seed issue: %v", err)
			}
			issueID, _ := res.LastInsertId()

			canonical, err := validateActionParams(ActionDeleteMediaFiles, json.RawMessage(tc.params))
			if err != nil {
				t.Fatalf("validate params: %v", err)
			}
			err = validateActionScopeWith(database, issueID, ActionDeleteMediaFiles, canonical)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected scope error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("scope error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// The wrong-book repair is bound to the issue's OWN durable record: params
// naming any other book are refused, and the issue's exact id passes.
func TestBookDeleteScopeBoundToOwnRecord(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	res, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, book_id, author_id)
		 VALUES ('user', 'observing', 'book', 0, 'Book', 'chaptarr-1', 123, 456)`)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()
	err = validateActionScopeWith(database, issueID, ActionDeleteMediaFiles,
		json.RawMessage(`{"media_type":"book","book_id":999,"blocklist":true}`))
	if err == nil || !strings.Contains(err.Error(), "own book record") {
		t.Fatalf("wrong-record scope error = %v", err)
	}
	if err := validateActionScopeWith(database, issueID, ActionDeleteMediaFiles,
		json.RawMessage(`{"media_type":"book","book_id":123,"blocklist":true}`)); err != nil {
		t.Fatalf("own-record delete refused: %v", err)
	}
}

// The facet is half of a standing rule's key, so an admin who auto-approves
// deleting files has NOT thereby auto-approved standing a release down for good.
func TestDeleteAndSearchFacetsSeparateTheDestructiveVariants(t *testing.T) {
	for _, tc := range []struct {
		kind      ActionKind
		params    string
		wantFacet string
		wantLabel string
	}{
		{ActionDeleteMediaFiles, `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1],"blocklist":true}`, "blocklist", "Delete the wrong files and block that release · Wrong content"},
		{ActionDeleteMediaFiles, `{"media_type":"tv","tmdb_id":615,"season":11,"episodes":[1]}`, "files_only", "Delete the wrong files · Wrong content"},
		{ActionTriggerSearch, `{"media_type":"tv","tmdb_id":615,"season":11}`, "", "Search again · Wrong content"},
	} {
		canonical, err := validateActionParams(tc.kind, json.RawMessage(tc.params))
		if err != nil {
			t.Fatalf("validate %s: %v", tc.kind, err)
		}
		facet, ok := actionAutoFacet(tc.kind, canonical)
		if !ok {
			t.Fatalf("%s params disqualified from rule matching", tc.kind)
		}
		if facet != tc.wantFacet {
			t.Errorf("%s facet = %q, want %q", tc.kind, facet, tc.wantFacet)
		}
		if label := approvalRuleLabel("Wrong content", tc.kind, facet); label != tc.wantLabel {
			t.Errorf("%s label = %q, want %q", tc.kind, label, tc.wantLabel)
		}
	}
}

// The finding is season-shaped: a file only looks impossible next to the
// episodes around it. An issue naming one episode must therefore still see the
// whole season — and never anything outside its own series.
func TestEpisodeTimelineIsScopedToTheSeasonNotTheEpisode(t *testing.T) {
	issue := &Issue{MediaType: "tv", TmdbID: 615, TvdbID: 73871, SeasonNumber: 11, EpisodeNumber: 3}
	scoped, err := scopeReadToolInput(issue, "get_episode_timeline",
		json.RawMessage(`{"media_type":"movie","tmdb_id":999,"season_number":4,"episode_number":9}`))
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(scoped, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["media_type"] != "tv" || got["tmdb_id"] != float64(615) || got["tvdb_id"] != float64(73871) {
		t.Fatalf("model-supplied identity survived scoping: %v", got)
	}
	if got["season_number"] != float64(11) {
		t.Errorf("season_number = %v, want the issue's season 11", got["season_number"])
	}
	if _, present := got["episode_number"]; present {
		t.Errorf("episode_number was injected: %v — the timeline must show the whole season", got)
	}
}

// A series-wide issue leaves the season open so the tool can roll up every
// season and find the one that is wrong.
func TestEpisodeTimelineLeavesASeriesWideIssueUnseasoned(t *testing.T) {
	issue := &Issue{MediaType: "tv", TmdbID: 615, TvdbID: 73871}
	scoped, err := scopeReadToolInput(issue, "get_episode_timeline", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(scoped, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := got["season_number"]; present {
		t.Errorf("season_number = %v, want it absent for a series-wide issue", got["season_number"])
	}
}

func TestEpisodeTimelineIsOnTheAgentsReadAllowList(t *testing.T) {
	if !readToolAllowSet["get_episode_timeline"] {
		t.Fatal("the agent cannot call get_episode_timeline")
	}
	// It is also the read that proves a season is clean after a repair, so it
	// has to satisfy the conclusion gate's "you looked at live state" check.
	if !isVerificationRead("get_episode_timeline", &mcp.ToolResult{Text: "Futurama season 11 — 10 episode(s)"}) {
		t.Fatal("get_episode_timeline does not count as a verification read")
	}
	if isVerificationRead("get_episode_timeline", &mcp.ToolResult{Text: "Sonarr is not configured."}) {
		t.Fatal("an unconfigured-service reply counted as verification")
	}
}

// A user report can never self-close — that judgment is the reporter's. What the
// closing message must not do is describe an applied repair the same way it
// describes achieving nothing.
func TestEscalatedCloseMessageDistinguishesAnAppliedFix(t *testing.T) {
	userIssue := &Issue{Source: SourceUser}
	autoIssue := &Issue{Source: SourceAuto}

	applied := escalatedCloseMessage(userIssue, true)
	if !strings.Contains(applied, "applied the approved fix") {
		t.Errorf("an applied fix reads as %q", applied)
	}
	nothing := escalatedCloseMessage(userIssue, false)
	if nothing == applied {
		t.Error("an applied fix and an empty-handed run say the same thing")
	}
	if !strings.Contains(nothing, "couldn't verify") {
		t.Errorf("an empty-handed run reads as %q", nothing)
	}
	// Auto incidents keep the original wording either way: they have their own
	// typed recovery proof, and "your call" makes no sense to a machine report.
	if got := escalatedCloseMessage(autoIssue, true); got != nothing {
		t.Errorf("auto incident message = %q, want the unchanged text", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// A settings read is about the INSTANCE the issue belongs to, never about a
// title — so it is scoped by instance and pinned to the bounded summary form.
func TestSettingsReadsAreScopedToTheIssuesOwnInstance(t *testing.T) {
	for _, tc := range []struct {
		media   string
		service string
	}{{"movie", "radarr"}, {"tv", "sonarr"}, {"book", "chaptarr"}} {
		for _, tool := range []string{"get_quality_profiles", "get_custom_formats"} {
			issue := &Issue{MediaType: tc.media, InstanceID: "inst-7", TmdbID: 615, BookID: 3}
			scoped, err := scopeReadToolInput(issue, tool,
				json.RawMessage(`{"service":"sonarr","instance_id":"someone-elses","profile_id":4,"format_id":9,"include_languages":true,"language_name":"English"}`))
			if err != nil {
				t.Fatalf("%s/%s: %v", tc.media, tool, err)
			}
			var got map[string]any
			if err := json.Unmarshal(scoped, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got["service"] != tc.service {
				t.Errorf("%s/%s service = %v, want %q", tc.media, tool, got["service"], tc.service)
			}
			if got["instance_id"] != "inst-7" {
				t.Errorf("%s/%s instance_id = %v — a model-chosen instance survived scoping", tc.media, tool, got["instance_id"])
			}
			// Each of these selects the header+raw-JSON form or the full language
			// catalog. The agent only ever gets the bounded summary.
			for _, key := range []string{"profile_id", "format_id", "include_languages", "language_name", "media_type"} {
				if _, present := got[key]; present {
					t.Errorf("%s/%s leaked %q: %v", tc.media, tool, key, got)
				}
			}
		}
	}
}

// Without an instance these tools fall back to the service's DEFAULT instance,
// so an instance-less issue would silently read someone else's configuration.
// Refusing is the only safe answer.
func TestSettingsReadsRefuseAnInstancelessIssue(t *testing.T) {
	for _, tool := range []string{"get_quality_profiles", "get_custom_formats"} {
		issue := &Issue{MediaType: "tv", TmdbID: 615}
		if _, err := scopeReadToolInput(issue, tool, json.RawMessage(`{}`)); err == nil {
			t.Errorf("%s accepted an issue with no instance", tool)
		}
	}
}

// A settings read needs no media identity — it is a question about the instance
// — so the guard that refuses a title-less issue must not apply to it.
func TestSettingsReadsDoNotNeedAMediaIdentity(t *testing.T) {
	issue := &Issue{MediaType: "movie", InstanceID: "inst-7"} // no TmdbID at all
	if _, err := scopeReadToolInput(issue, "get_quality_profiles", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("refused a settings read for want of a media identity: %v", err)
	}
	// The contrast: a media-scoped read on the same issue still fails closed.
	if _, err := scopeReadToolInput(issue, "get_history", json.RawMessage(`{}`)); err == nil {
		t.Fatal("get_history accepted an issue with no movie identity")
	}
}

func TestSettingsReadsAreOnTheAgentsAllowList(t *testing.T) {
	for _, tool := range []string{"get_quality_profiles", "get_custom_formats"} {
		if !readToolAllowSet[tool] {
			t.Errorf("the agent cannot call %s", tool)
		}
	}
	// The write side stays unreachable.
	for _, tool := range []string{"upsert_custom_format", "preview_profile_change", "apply_profile_change"} {
		if readToolAllowSet[tool] {
			t.Errorf("%s is reachable from the remediation agent", tool)
		}
	}
}
