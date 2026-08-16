package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
)

// get_episode_timeline is the only read tool that can see an episode, so it is
// the whole evidence base for a "wrong episode" report. What is pinned here is
// the rendering: the per-episode facts an admin reads, the aired/unaired mark
// that must agree with the detector rather than restate it, and the → next line
// that is the difference between an agent that proposes the remedy and one that
// gives up. The fixture is the live season from issue #814.

// --- live fixture, evaluated at a fixed instant ---

// futuramaNow sits a day and a half before the live case premiered, so every
// unaired margin clears arr.PreAirMarginFloor and the fixtures keep testing
// the pure disease. (It originally sat ten hours out - inside the floor the
// Reacher premiere false-positive later earned.)
// yet nine files have been sitting in the library for thirteen days.
var futuramaNow = time.Date(2026, 8, 2, 14, 3, 0, 0, time.UTC)

// futuramaImportedAt is when every one of the nine files landed — one batch,
// 2026-07-21, from nine separate RSS grabs.
var futuramaImportedAt = time.Date(2026, 7, 21, 9, 30, 0, 0, time.UTC)

func mustTime(t *testing.T, value string) *time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return &parsed
}

// futuramaS11States is season 11 as Sonarr reports it, in the service-neutral
// shape groupEpisodeStates produces (no import stamps yet — those live on the
// file records and are joined in by the renderer).
func futuramaS11States(t *testing.T) []arr.EpisodeState {
	t.Helper()
	airs := []string{
		"2026-08-04T00:00:00Z", "2026-08-04T00:23:00Z", "2026-08-11T00:00:00Z",
		"2026-08-18T00:00:00Z", "2026-08-25T00:00:00Z", "2026-09-01T00:00:00Z",
		"2026-09-08T00:00:00Z", "2026-09-15T00:00:00Z", "2026-09-22T00:00:00Z",
		"2026-09-29T00:00:00Z",
	}
	titles := []string{
		"Beef", "The Cure for Boredom", "How the West Was 1010001",
		"Attack of the Clothes", "Ship Happens", "Lord Nibbler in the Nothingverse",
		"Love at First Scam", "Cold Warriors II", "The Bots and the Bees II", "Finale II",
	}
	states := make([]arr.EpisodeState, 0, 10)
	for i := range airs {
		state := arr.EpisodeState{Number: i + 1, Title: titles[i], AirsAt: mustTime(t, airs[i])}
		if i < 9 {
			state.HasFile = true
			state.FileID = 5100 + i + 1
		}
		states = append(states, state)
	}
	return states
}

// futuramaS11Files are the nine episode files, every one imported in the same
// batch on 2026-07-21 with the scene name that made Sonarr accept it.
func futuramaS11Files(t *testing.T) map[int]sonarr.EpisodeFile {
	t.Helper()
	titles := []string{
		"Beef", "The.Cure.for.Boredom", "How.the.West.Was.1010001",
		"Attack.of.the.Clothes", "Ship.Happens", "Lord.Nibbler.in.the.Nothingverse",
		"Love.at.First.Scam", "Cold.Warriors.II", "The.Bots.and.the.Bees.II",
	}
	out := make(map[int]sonarr.EpisodeFile, len(titles))
	for i, title := range titles {
		imported := futuramaImportedAt
		file := sonarr.EpisodeFile{
			ID: 5100 + i + 1, SeriesID: 28, SeasonNumber: 11,
			RelativePath: fmt.Sprintf("Season 11/Futurama - S11E%02d.mkv", i+1),
			SceneName:    fmt.Sprintf("Futurama.S11E%02d.%s.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM", i+1, title),
			DateAdded:    &imported,
		}
		file.Quality.Quality.Name = "WEBDL-1080p"
		out[file.ID] = file
	}
	return out
}

func futuramaSeriesRecord() *sonarr.Series {
	return &sonarr.Series{ID: 28, Title: "Futurama", TvdbID: 73871, TmdbID: 615}
}

// --- season-scoped rendering ---

// TestEpisodeTimelineRendersTheLiveSeason pins the whole season-scoped output
// against the real case: every episode named with its air time and the file the
// library holds, the unaired ones marked as such, the detector's own sentence
// carried through, and the prescriptive next calls.
func TestEpisodeTimelineRendersTheLiveSeason(t *testing.T) {
	states := futuramaS11States(t)
	files := futuramaS11Files(t)
	bySeason := map[int][]arr.EpisodeState{11: states}

	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 11, bySeason, files, futuramaNow)

	wantLines := []string{
		"Futurama season 11 — 10 episode(s), 0 aired, 9 with files. Now 2026-08-02T14:03:00Z.",
		`- E01 "Beef" — airs 2026-08-04T00:00:00Z (NOT YET AIRED) · file imported 2026-07-21T09:30:00Z [WEBDL-1080p] Futurama.S11E01.Beef.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM`,
		`- E02 "The Cure for Boredom" — airs 2026-08-04T00:23:00Z (NOT YET AIRED) · file imported 2026-07-21T09:30:00Z [WEBDL-1080p] Futurama.S11E02.The.Cure.for.Boredom.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM`,
		`- E09 "The Bots and the Bees II" — airs 2026-09-22T00:00:00Z (NOT YET AIRED) · file imported 2026-07-21T09:30:00Z`,
		`- E10 "Finale II" — airs 2026-09-29T00:00:00Z (NOT YET AIRED) · no file`,
	}
	for _, want := range wantLines {
		if !strings.Contains(text, want) {
			t.Fatalf("timeline missing line %q:\n%s", want, text)
		}
	}
	// The detector's sentence, verbatim: the rendering must not paraphrase the
	// verdict an admin is approving a deletion on.
	transparency := arr.BuildSeasonTimeline(11, withImportTimes(states, files), futuramaNow).Transparency
	if !strings.Contains(text, transparency) {
		t.Fatalf("timeline dropped the finding sentence %q:\n%s", transparency, text)
	}
	if !strings.Contains(text, "No aired episode of this season is missing a file.") {
		t.Fatalf("timeline should state nothing aired is missing:\n%s", text)
	}
	if strings.Contains(text, "…and") {
		t.Fatalf("a ten-episode season should not be truncated:\n%s", text)
	}
}

// TestEpisodeTimelinePrescribesOneFixForOneProblem: the → next line is what
// turns evidence into a proposal, and it prescribes exactly ONE call. Deleting
// the impossible files, standing their releases down, and searching for what
// has actually aired are three halves of a single repair, and splitting them
// across two approvals asks an admin to approve the second half of a decision
// they already made. The negative half of this test is the guard on that: a
// timeline that names trigger_search or aired_only again has reintroduced the
// split, whatever the surrounding prose says.
func TestEpisodeTimelinePrescribesOneFixForOneProblem(t *testing.T) {
	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 11,
		map[int][]arr.EpisodeState{11: futuramaS11States(t)}, futuramaS11Files(t), futuramaNow)

	wantDelete := `→ next: propose_action delete_media_files {"media_type": "tv", "tmdb_id": 615, "season": 11, "episodes": [1, 2, 3, 4, 5, 6, 7, 8, 9], "blocklist": true}`
	if !strings.Contains(text, wantDelete) {
		t.Fatalf("timeline missing the delete proposal %q:\n%s", wantDelete, text)
	}
	if got := strings.Count(text, "propose_action"); got != 1 {
		t.Fatalf("timeline prescribes %d calls, want exactly 1:\n%s", got, text)
	}
	for _, unwanted := range []string{"trigger_search", "aired_only", "once that is approved"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("timeline splits the repair back into two proposals (%q):\n%s", unwanted, text)
		}
	}
	// And it says so, because an agent reading only the one call has to know the
	// search is already in it rather than infer that from its absence.
	for _, want := range []string{
		"searches the episodes that have already aired",
		"Do not propose a separate search afterwards.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeline does not say the one fix is the whole repair (%q):\n%s", want, text)
		}
	}
	// E10 has no file, so it must not be in the delete list.
	if strings.Contains(text, "[1, 2, 3, 4, 5, 6, 7, 8, 9, 10]") {
		t.Fatalf("delete proposal names an episode with no file:\n%s", text)
	}
}

// TestEpisodeTimelineHealthySeasonProposesNothing is the false-positive guard.
// An admin reading a timeline for a report with nothing behind it must not be
// shown a deletion to approve, and an episode that has aired must never be
// labelled as one that has not.
func TestEpisodeTimelineHealthySeasonProposesNothing(t *testing.T) {
	aired := futuramaNow.Add(-30 * 24 * time.Hour)
	imported := aired.Add(2 * time.Hour)
	var states []arr.EpisodeState
	files := make(map[int]sonarr.EpisodeFile, 4)
	for i := 1; i <= 4; i++ {
		airsAt := aired.AddDate(0, 0, 7*i)
		importedAt := imported.AddDate(0, 0, 7*i)
		states = append(states, arr.EpisodeState{
			Number: i, Title: fmt.Sprintf("Aired %d", i), AirsAt: &airsAt,
			HasFile: true, FileID: 7000 + i,
		})
		file := sonarr.EpisodeFile{ID: 7000 + i, SeriesID: 28, SeasonNumber: 4, DateAdded: &importedAt}
		file.Quality.Quality.Name = "WEBDL-1080p"
		files[file.ID] = file
	}

	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 4,
		map[int][]arr.EpisodeState{4: states}, files, futuramaNow)

	if strings.Contains(text, "→ next:") {
		t.Fatalf("a healthy season prescribed a fix:\n%s", text)
	}
	if strings.Contains(text, "NOT YET AIRED") {
		t.Fatalf("an aired episode was marked unaired:\n%s", text)
	}
	if strings.Contains(text, "delete_media_files") {
		t.Fatalf("a healthy season named the delete action:\n%s", text)
	}
	if !strings.Contains(text, "Futurama season 4 — 4 episode(s), 4 aired, 4 with files") {
		t.Fatalf("healthy header = \n%s", text)
	}
	if !strings.Contains(text, "— aired ") {
		t.Fatalf("aired episodes should render their air date:\n%s", text)
	}
}

// TestEpisodeTimelineNextLineIsGatedOnTheFinding walks the boundary the → next
// line hangs on. Deleting somebody's files is irreversible, so the prescription
// appears only when the detector is conclusive AND the import stamps prove it.
func TestEpisodeTimelineNextLineIsGatedOnTheFinding(t *testing.T) {
	unaired := futuramaNow.Add(48 * time.Hour)
	laterUnaired := futuramaNow.Add(96 * time.Hour)

	oneUnairedFile := []arr.EpisodeState{
		{Number: 1, Title: "Aired", AirsAt: ptrTime(futuramaNow.Add(-72 * time.Hour)), HasFile: true, FileID: 8001},
		{Number: 2, Title: "Unaired, has a file", AirsAt: &unaired, HasFile: true, FileID: 8002},
		{Number: 3, Title: "Unaired", AirsAt: &laterUnaired},
	}
	twoUnairedFiles := []arr.EpisodeState{
		{Number: 1, Title: "Aired", AirsAt: ptrTime(futuramaNow.Add(-72 * time.Hour)), HasFile: true, FileID: 8001},
		{Number: 2, Title: "Unaired, has a file", AirsAt: &unaired, HasFile: true, FileID: 8002},
		{Number: 3, Title: "Unaired, has a file too", AirsAt: &laterUnaired, HasFile: true, FileID: 8003},
	}
	stamped := map[int]sonarr.EpisodeFile{
		8001: {ID: 8001, DateAdded: ptrTime(futuramaNow.Add(-71 * time.Hour))},
		8002: {ID: 8002, DateAdded: ptrTime(futuramaImportedAt)},
		8003: {ID: 8003, DateAdded: ptrTime(futuramaImportedAt)},
	}
	unstamped := map[int]sonarr.EpisodeFile{
		8001: {ID: 8001}, 8002: {ID: 8002}, 8003: {ID: 8003},
	}

	cases := []struct {
		name     string
		states   []arr.EpisodeState
		files    map[int]sonarr.EpisodeFile
		wantNext bool
	}{
		{
			name:   "one unaired episode holding a file is a near miss, not a finding",
			states: oneUnairedFile, files: stamped, wantNext: false,
		},
		{
			name:   "two unaired episodes holding files is content that does not exist",
			states: twoUnairedFiles, files: stamped, wantNext: true,
		},
		{
			name:   "conclusive but with no import stamps to name in the fix",
			states: twoUnairedFiles, files: unstamped, wantNext: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 5,
				map[int][]arr.EpisodeState{5: tc.states}, tc.files, futuramaNow)
			if got := strings.Contains(text, "→ next:"); got != tc.wantNext {
				t.Fatalf("→ next present = %v, want %v:\n%s", got, tc.wantNext, text)
			}
			// The per-episode facts are reported either way — a near miss is
			// still evidence, it just is not grounds for a deletion.
			if !strings.Contains(text, "NOT YET AIRED") {
				t.Fatalf("unaired episodes must still be marked:\n%s", text)
			}
		})
	}
}

// TestEpisodeTimelineRendersEpisodesWithNoAirDate: an episode the service
// carries no air date for is neither aired nor unaired, and the line must say
// so rather than pick a side.
func TestEpisodeTimelineRendersEpisodesWithNoAirDate(t *testing.T) {
	states := []arr.EpisodeState{
		{Number: 1, Title: "Unscheduled"},
		{Number: 2, Title: "Unscheduled with a file", HasFile: true, FileID: 9002},
	}
	files := map[int]sonarr.EpisodeFile{9002: {ID: 9002, RelativePath: "Season 6/unscheduled.mkv"}}

	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 6,
		map[int][]arr.EpisodeState{6: states}, files, futuramaNow)

	if !strings.Contains(text, `- E01 "Unscheduled" — no air date · no file`) {
		t.Fatalf("unscheduled episode line missing:\n%s", text)
	}
	// No import stamp came back, so the line says a file exists without
	// inventing a time for it, and falls back to the path as its identity.
	if !strings.Contains(text, `- E02 "Unscheduled with a file" — no air date · has a file Season 6/unscheduled.mkv`) {
		t.Fatalf("unscheduled-with-file line missing:\n%s", text)
	}
	if strings.Contains(text, "NOT YET AIRED") || strings.Contains(text, "— aired ") {
		t.Fatalf("an episode with no air date was placed in a bucket:\n%s", text)
	}
	if strings.Contains(text, "→ next:") {
		t.Fatalf("unscheduled episodes prescribed a deletion:\n%s", text)
	}
}

// --- unscoped (whole-series) rendering ---

// TestEpisodeTimelineUnscopedRollsUpThenDetailsTheFinding covers the shape the
// agent gets when the issue names no season: enough of every season to judge
// it, plus the full episode list for the one season a fix would act on.
func TestEpisodeTimelineUnscopedRollsUpThenDetailsTheFinding(t *testing.T) {
	healthyAir := futuramaNow.Add(-90 * 24 * time.Hour)
	healthyImport := healthyAir.Add(3 * time.Hour)
	var season10 []arr.EpisodeState
	files := futuramaS11Files(t)
	for i := 1; i <= 3; i++ {
		airsAt := healthyAir.AddDate(0, 0, 7*i)
		importedAt := healthyImport.AddDate(0, 0, 7*i)
		season10 = append(season10, arr.EpisodeState{
			Number: i, Title: fmt.Sprintf("Season Ten Only %d", i), AirsAt: &airsAt, HasFile: true, FileID: 6000 + i,
		})
		files[6000+i] = sonarr.EpisodeFile{ID: 6000 + i, DateAdded: &importedAt}
	}

	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 0, map[int][]arr.EpisodeState{
		10: season10,
		11: futuramaS11States(t),
	}, files, futuramaNow)

	for _, want := range []string{
		"Futurama — 2 season(s). Now 2026-08-02T14:03:00Z.",
		"- Season 10: 3 episode(s), 3 aired, 3 with files",
		"- Season 11: 10 episode(s), 0 aired, 9 with files — 9 file(s) for episodes that have NOT aired",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rollup missing %q:\n%s", want, text)
		}
	}
	// Full detail follows for the flagged season only.
	if !strings.Contains(text, "Futurama season 11 — 10 episode(s), 0 aired, 9 with files.") {
		t.Fatalf("flagged season has no detail block:\n%s", text)
	}
	if !strings.Contains(text, `- E01 "Beef" — airs 2026-08-04T00:00:00Z (NOT YET AIRED)`) {
		t.Fatalf("flagged season detail is missing its episode lines:\n%s", text)
	}
	if strings.Contains(text, "Season Ten Only") {
		t.Fatalf("a healthy season was expanded, burying the finding:\n%s", text)
	}
	if !strings.Contains(text, `"season": 11`) {
		t.Fatalf("the fix must name the flagged season:\n%s", text)
	}
}

// TestEpisodeTimelineUnscopedDetailsTheMostRecentFlaggedSeason: when more than
// one season trips the finding, the detail belongs to the newest — that is the
// one the reporter is watching.
func TestEpisodeTimelineUnscopedDetailsTheMostRecentFlaggedSeason(t *testing.T) {
	unaired := futuramaNow.Add(48 * time.Hour)
	flagged := func(prefix string, fileBase int) []arr.EpisodeState {
		return []arr.EpisodeState{
			{Number: 1, Title: prefix + " one", AirsAt: &unaired, HasFile: true, FileID: fileBase + 1},
			{Number: 2, Title: prefix + " two", AirsAt: &unaired, HasFile: true, FileID: fileBase + 2},
		}
	}
	files := map[int]sonarr.EpisodeFile{
		101: {ID: 101, DateAdded: ptrTime(futuramaImportedAt)},
		102: {ID: 102, DateAdded: ptrTime(futuramaImportedAt)},
		201: {ID: 201, DateAdded: ptrTime(futuramaImportedAt)},
		202: {ID: 202, DateAdded: ptrTime(futuramaImportedAt)},
	}

	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 0, map[int][]arr.EpisodeState{
		10: flagged("Older season", 100),
		11: flagged("Newer season", 200),
	}, files, futuramaNow)

	if !strings.Contains(text, "Futurama season 11 —") || strings.Contains(text, "Futurama season 10 —") {
		t.Fatalf("detail block should belong to the newest flagged season:\n%s", text)
	}
	if !strings.Contains(text, `"season": 11`) {
		t.Fatalf("the fix names the wrong season:\n%s", text)
	}
}

// TestEpisodeTimelineUnscopedSaysWhenNothingIsWrong: a report with nothing
// behind it must end in a plain statement, not an empty section an agent can
// read as a finding.
func TestEpisodeTimelineUnscopedSaysWhenNothingIsWrong(t *testing.T) {
	aired := futuramaNow.Add(-14 * 24 * time.Hour)
	states := []arr.EpisodeState{
		{Number: 1, Title: "One", AirsAt: &aired, HasFile: true, FileID: 3001},
		{Number: 2, Title: "Two", AirsAt: &aired, HasFile: true, FileID: 3002},
	}
	files := map[int]sonarr.EpisodeFile{
		3001: {ID: 3001, DateAdded: ptrTime(aired.Add(time.Hour))},
		3002: {ID: 3002, DateAdded: ptrTime(aired.Add(time.Hour))},
	}

	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 0,
		map[int][]arr.EpisodeState{7: states}, files, futuramaNow)

	if !strings.Contains(text, "No season holds files for episodes that have not aired.") {
		t.Fatalf("clean series should say so plainly:\n%s", text)
	}
	if strings.Contains(text, "→ next:") || strings.Contains(text, "NOT YET AIRED") {
		t.Fatalf("clean series raised a finding:\n%s", text)
	}
}

// TestEpisodeTimelineBoundsOneRendering keeps a daily soap from flooding the
// model's context; the finding does not need every line to land.
func TestEpisodeTimelineBoundsOneRendering(t *testing.T) {
	aired := futuramaNow.Add(-365 * 24 * time.Hour)
	states := make([]arr.EpisodeState, 0, timelineMaxEpisodeLines+5)
	for i := 1; i <= timelineMaxEpisodeLines+5; i++ {
		airsAt := aired.AddDate(0, 0, i)
		states = append(states, arr.EpisodeState{Number: i, Title: fmt.Sprintf("Ep %d", i), AirsAt: &airsAt})
	}
	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 1,
		map[int][]arr.EpisodeState{1: states}, nil, futuramaNow)

	if !strings.Contains(text, "…and 5 more episode(s).") {
		t.Fatalf("long season was not bounded:\n%s", text)
	}
	if strings.Contains(text, fmt.Sprintf("- E%02d ", timelineMaxEpisodeLines+1)) {
		t.Fatalf("bounded rendering leaked past the cap:\n%s", text)
	}
}

// TestEpisodeTimelineForAnUnknownSeason answers a season the library does not
// have in words, not with an empty timeline.
func TestEpisodeTimelineForAnUnknownSeason(t *testing.T) {
	text := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 12,
		map[int][]arr.EpisodeState{11: futuramaS11States(t)}, futuramaS11Files(t), futuramaNow)
	if text != "Futurama has no season 12 in Sonarr." {
		t.Fatalf("unknown season = %q", text)
	}
	empty := renderEpisodeTimeline(futuramaSeriesRecord(), 615, 0, map[int][]arr.EpisodeState{}, nil, futuramaNow)
	if empty != "Futurama has no episodes in Sonarr yet." {
		t.Fatalf("empty series = %q", empty)
	}
}

// --- the tool end to end ---

// TestGetEpisodeTimelineJoinsEpisodesAndFiles proves the tool actually reads
// both endpoints and joins them. The import stamp lives on the FILE, not the
// episode, and a missing join silently disarms the whole finding — the timeline
// would render every line and conclude nothing.
func TestGetEpisodeTimelineJoinsEpisodesAndFiles(t *testing.T) {
	// Anchored to the real clock: the tool resolves "aired" from time.Now().
	now := time.Now().UTC()
	imported := now.Add(-13 * 24 * time.Hour).UTC().Format(time.RFC3339)
	episodes := []map[string]any{
		{"id": 1101, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 1, "title": "Beef",
			"airDateUtc": now.Add(2 * 24 * time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5101},
		{"id": 1102, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 2, "title": "The Cure for Boredom",
			"airDateUtc": now.Add(2*24*time.Hour + time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5102},
		{"id": 1103, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 3, "title": "How the West Was 1010001",
			"airDateUtc": now.Add(7 * 24 * time.Hour).Format(time.RFC3339), "hasFile": false},
	}
	files := []map[string]any{
		{"id": 5101, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported,
			"sceneName": "Futurama.S11E01.Beef.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM",
			"quality":   map[string]any{"quality": map[string]any{"name": "WEBDL-1080p"}}},
		{"id": 5102, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported,
			"sceneName": "Futurama.S11E02.The.Cure.for.Boredom.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM",
			"quality":   map[string]any{"quality": map[string]any{"name": "WEBDL-1080p"}}},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: episodes, files: files}
	arrServer := fake.start()
	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_episode_timeline",
		json.RawMessage(`{"media_type":"tv","tmdb_id":615,"season_number":11}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_episode_timeline: %v", err)
	}

	if !strings.Contains(result.Text, "file imported "+imported) {
		t.Fatalf("timeline did not join the file's import time:\n%s", result.Text)
	}
	for _, want := range []string{
		"(NOT YET AIRED)",
		"[WEBDL-1080p]",
		"Futurama.S11E01.Beef.1080p.DSNP.WEB-DL.DDP5.1.H.264.Dual-CM",
		`→ next: propose_action delete_media_files {"media_type": "tv", "tmdb_id": 615, "season": 11, "episodes": [1, 2], "blocklist": true}`,
		"Do not propose a separate search afterwards.",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("timeline missing %q:\n%s", want, result.Text)
		}
	}
	// The whole repair is that one call, end to end through the tool — not a
	// delete the agent is expected to chase with a search of its own.
	if got := strings.Count(result.Text, "propose_action"); got != 1 {
		t.Fatalf("tool prescribed %d calls, want exactly 1:\n%s", got, result.Text)
	}
	for _, unwanted := range []string{"trigger_search", "aired_only"} {
		if strings.Contains(result.Text, unwanted) {
			t.Fatalf("tool output still names %q:\n%s", unwanted, result.Text)
		}
	}
	// The whole series is read, then rendered for the season in scope.
	if !containsRequest(fake, "/api/v3/episode?seriesId=28") || !containsRequest(fake, "/api/v3/episodefile?seriesId=28") {
		t.Fatalf("tool did not read both endpoints: %+v", fake.rec.all())
	}
}

// TestGetEpisodeTimelineUnscopedThroughTheTool proves the season-less call
// reaches the rollup path with real data behind it.
func TestGetEpisodeTimelineUnscopedThroughTheTool(t *testing.T) {
	now := time.Now().UTC()
	imported := now.Add(-13 * 24 * time.Hour).UTC().Format(time.RFC3339)
	episodes := []map[string]any{
		{"id": 1001, "seriesId": 28, "seasonNumber": 10, "episodeNumber": 1, "title": "Long ago",
			"airDateUtc": now.Add(-400 * 24 * time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5001},
		{"id": 1101, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 1, "title": "Beef",
			"airDateUtc": now.Add(2 * 24 * time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5101},
		{"id": 1102, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 2, "title": "The Cure for Boredom",
			"airDateUtc": now.Add(2*24*time.Hour + time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5102},
	}
	files := []map[string]any{
		{"id": 5001, "seriesId": 28, "seasonNumber": 10, "dateAdded": now.Add(-399 * 24 * time.Hour).Format(time.RFC3339)},
		{"id": 5101, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported},
		{"id": 5102, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: episodes, files: files}
	arrServer := fake.start()
	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": arrServer.URL})

	result, err := server.ExecuteTool(context.Background(), "get_episode_timeline",
		json.RawMessage(`{"media_type":"tv","tmdb_id":615}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_episode_timeline: %v", err)
	}
	for _, want := range []string{
		"Futurama — 2 season(s).",
		"- Season 10: 1 episode(s), 1 aired, 1 with files",
		"- Season 11: 2 episode(s), 0 aired, 2 with files — 2 file(s) for episodes that have NOT aired",
		"Futurama season 11 —",
		`"season": 11`,
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("unscoped timeline missing %q:\n%s", want, result.Text)
		}
	}
}

// TestGetEpisodeTimelineAnswersInPlainLanguage: every case the tool cannot
// serve is an answer, not an error. An error would make the agent retry or give
// up; a sentence tells it what to do instead.
func TestGetEpisodeTimelineAnswersInPlainLanguage(t *testing.T) {
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: futuramaS11Episodes()}
	arrServer := fake.start()

	empty := &sonarrFileFake{t: t, series: []map[string]any{}}
	emptyServer := empty.start()

	cases := []struct {
		name  string
		urls  map[string]string
		input string
		want  string
	}{
		{
			name:  "movies have no episode timeline",
			urls:  map[string]string{"sonarr": arrServer.URL},
			input: `{"media_type":"movie","tmdb_id":550}`,
			want:  "An episode timeline exists only for TV.",
		},
		{
			name:  "books have no episode timeline either",
			urls:  map[string]string{"sonarr": arrServer.URL},
			input: `{"media_type":"book"}`,
			want:  "An episode timeline exists only for TV.",
		},
		{
			name:  "sonarr not configured",
			urls:  map[string]string{},
			input: `{"media_type":"tv","tmdb_id":615}`,
			want:  "Sonarr is not configured.",
		},
		{
			name:  "no identity to scope to",
			urls:  map[string]string{"sonarr": arrServer.URL},
			input: `{"media_type":"tv"}`,
			want:  "get_episode_timeline needs a series: pass tmdb_id.",
		},
		{
			name:  "series is not in the library",
			urls:  map[string]string{"sonarr": emptyServer.URL},
			input: `{"media_type":"tv","tmdb_id":615}`,
			want:  "That series is not in the Sonarr library.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := newDefaultInstanceToolServer(t, tc.urls)
			result, err := server.ExecuteTool(context.Background(), "get_episode_timeline",
				json.RawMessage(tc.input), adminCallContext())
			if err != nil {
				t.Fatalf("get_episode_timeline returned an error instead of an answer: %v", err)
			}
			if result.Text != tc.want {
				t.Fatalf("answer = %q, want %q", result.Text, tc.want)
			}
		})
	}
}

// --- the typed verdict the runner closes on ---

// seasonCleanVerification is the server's own answer to "is the impossible
// content still there", carried out of band so the conclusion gate never has to
// believe the model's reading of the prose above it. Everything below is about
// when it refuses to answer: this verdict can close an issue, and a wrong
// "clean" closes one that was never repaired.

// repairedS11States is season 11 after the fix: the same ten episodes, none of
// them holding a file any more.
func repairedS11States(t *testing.T) []arr.EpisodeState {
	t.Helper()
	states := futuramaS11States(t)
	for i := range states {
		states[i].HasFile = false
		states[i].FileID = 0
	}
	return states
}

func TestSeasonCleanVerificationReportsWhetherTheFindingStillTrips(t *testing.T) {
	files := futuramaS11Files(t)

	still := seasonCleanVerification(map[int][]arr.EpisodeState{11: futuramaS11States(t)}, files, 11, futuramaNow)
	if still == nil {
		t.Fatal("a season in scope produced no verdict")
	}
	if still.Kind != VerificationSeasonClean {
		t.Errorf("kind = %q, want %q", still.Kind, VerificationSeasonClean)
	}
	if !still.ExactScope {
		t.Error("a season-scoped read is exact scope; without it the runner ignores the verdict")
	}
	if !still.TargetPresent {
		t.Error("nine impossible files still on disk read as repaired")
	}

	repaired := seasonCleanVerification(map[int][]arr.EpisodeState{11: repairedS11States(t)}, files, 11, futuramaNow)
	if repaired == nil {
		t.Fatal("a repaired season in scope produced no verdict")
	}
	if repaired.Kind != VerificationSeasonClean || !repaired.ExactScope {
		t.Errorf("repaired verdict = %+v, want the same kind and scope", repaired)
	}
	if repaired.TargetPresent {
		t.Error("a season holding no impossible files still reports the target present")
	}
}

// TestSeasonCleanVerificationOnlyAnswersForOneSeason. A series-wide read rolls
// up every season, and a rollup cannot say whether THIS incident's season is
// clean — it would answer a question nobody asked, on evidence about other
// seasons. No verdict at all leaves the issue exactly where a run that proved
// nothing should leave it.
func TestSeasonCleanVerificationOnlyAnswersForOneSeason(t *testing.T) {
	bySeason := map[int][]arr.EpisodeState{11: futuramaS11States(t)}
	files := futuramaS11Files(t)

	for _, season := range []int{0, -1} {
		if v := seasonCleanVerification(bySeason, files, season, futuramaNow); v != nil {
			t.Fatalf("unscoped read (season %d) produced verdict %+v, want none", season, v)
		}
	}
}

// TestSeasonCleanVerificationRefusesASeasonSonarrDoesNotHold is the sharpest
// edge of the whole mechanism: an ABSENT season holds no impossible files, so
// anything that read absence as cleanliness would close the incident the moment
// the agent asked about the wrong season number.
func TestSeasonCleanVerificationRefusesASeasonSonarrDoesNotHold(t *testing.T) {
	files := futuramaS11Files(t)

	if v := seasonCleanVerification(map[int][]arr.EpisodeState{11: futuramaS11States(t)}, files, 12, futuramaNow); v != nil {
		t.Fatalf("a season the library does not hold produced verdict %+v, want none", v)
	}
	if v := seasonCleanVerification(map[int][]arr.EpisodeState{12: {}}, files, 12, futuramaNow); v != nil {
		t.Fatalf("an empty season produced verdict %+v, want none", v)
	}
	if v := seasonCleanVerification(map[int][]arr.EpisodeState{}, nil, 11, futuramaNow); v != nil {
		t.Fatalf("a series with no episodes produced verdict %+v, want none", v)
	}
}

// TestSeasonCleanVerificationFollowsTheDetectorNotItsOwnRule: the verdict is
// the detector's PreAirFill, threshold and all. One file left behind therefore
// reads as clean here — which is exactly why the service's own recovery proof
// re-reads the season and requires that NOTHING unaired still holds a file,
// rather than trusting this flag alone.
func TestSeasonCleanVerificationFollowsTheDetectorNotItsOwnRule(t *testing.T) {
	unaired := futuramaNow.Add(48 * time.Hour)
	nearMiss := []arr.EpisodeState{
		{Number: 1, Title: "Aired", AirsAt: ptrTime(futuramaNow.Add(-72 * time.Hour)), HasFile: true, FileID: 8001},
		{Number: 2, Title: "Unaired, has a file", AirsAt: &unaired, HasFile: true, FileID: 8002},
	}
	files := map[int]sonarr.EpisodeFile{
		8001: {ID: 8001, DateAdded: ptrTime(futuramaNow.Add(-71 * time.Hour))},
		8002: {ID: 8002, DateAdded: ptrTime(futuramaImportedAt)},
	}

	v := seasonCleanVerification(map[int][]arr.EpisodeState{5: nearMiss}, files, 5, futuramaNow)
	if v == nil {
		t.Fatal("a season in scope produced no verdict")
	}
	if v.TargetPresent {
		t.Error("one unaired episode holding a file tripped the finding; the detector says it is a near miss")
	}
}

// TestGetEpisodeTimelineEmitsTheVerdictThroughTheTool proves the verification
// actually rides out on the tool result the runner inspects, not just out of
// the helper.
func TestGetEpisodeTimelineEmitsTheVerdictThroughTheTool(t *testing.T) {
	now := time.Now().UTC()
	imported := now.Add(-13 * 24 * time.Hour).UTC().Format(time.RFC3339)
	episodes := []map[string]any{
		{"id": 1101, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 1, "title": "Beef",
			"airDateUtc": now.Add(2 * 24 * time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5101},
		{"id": 1102, "seriesId": 28, "seasonNumber": 11, "episodeNumber": 2, "title": "The Cure for Boredom",
			"airDateUtc": now.Add(2*24*time.Hour + time.Hour).Format(time.RFC3339), "hasFile": true, "episodeFileId": 5102},
	}
	files := []map[string]any{
		{"id": 5101, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported},
		{"id": 5102, "seriesId": 28, "seasonNumber": 11, "dateAdded": imported},
	}
	fake := &sonarrFileFake{t: t, series: futuramaSeries(), episodes: episodes, files: files}
	server := newDefaultInstanceToolServer(t, map[string]string{"sonarr": fake.start().URL})

	scoped, err := server.ExecuteTool(context.Background(), "get_episode_timeline",
		json.RawMessage(`{"media_type":"tv","tmdb_id":615,"season_number":11}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_episode_timeline: %v", err)
	}
	if scoped.Verification == nil {
		t.Fatal("a season-scoped read carried no verification")
	}
	if scoped.Verification.Kind != VerificationSeasonClean || !scoped.Verification.ExactScope || !scoped.Verification.TargetPresent {
		t.Fatalf("verification = %+v, want season_clean / exact / present", scoped.Verification)
	}

	unscoped, err := server.ExecuteTool(context.Background(), "get_episode_timeline",
		json.RawMessage(`{"media_type":"tv","tmdb_id":615}`), adminCallContext())
	if err != nil {
		t.Fatalf("get_episode_timeline unscoped: %v", err)
	}
	if unscoped.Verification != nil {
		t.Fatalf("a series-wide read carried verification %+v, want none", unscoped.Verification)
	}
}

func ptrTime(v time.Time) *time.Time { return &v }

func containsRequest(f *sonarrFileFake, uri string) bool {
	for _, call := range f.rec.all() {
		if call.URI == uri {
			return true
		}
	}
	return false
}
