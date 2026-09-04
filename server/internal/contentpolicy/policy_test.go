package contentpolicy

import (
	"context"
	"strings"
	"testing"
)

func evaluatorFor(t *testing.T, p Policy) *Evaluator {
	t.Helper()
	env := newTestEnv(t)
	ev, err := env.svc.EvaluatorFor(context.Background(), &p)
	if err != nil {
		t.Fatalf("EvaluatorFor: %v", err)
	}
	return ev
}

func TestEvaluatorAllowsByOrderGenreAdultAndUnrated(t *testing.T) {
	ev := evaluatorFor(t, usPolicy())
	rated := func(c string) Rating { return Rating{Certification: c, Known: true} }

	cases := []struct {
		name  string
		media string
		r     Rating
		adult bool
		genre []int
		want  bool
	}{
		{"G under PG", MediaMovie, rated("G"), false, nil, true},
		{"PG at the cap", MediaMovie, rated("PG"), false, nil, true},
		{"lower-case pg-13 above the cap", MediaMovie, rated("pg-13"), false, nil, false},
		{"R above the cap", MediaMovie, rated("R"), false, nil, false},
		{"NR is unrated and hidden", MediaMovie, rated("NR"), false, nil, false},
		{"unknown cert is unrated and hidden", MediaMovie, rated("12A"), false, nil, false},
		{"no rating is hidden", MediaMovie, Rating{}, false, nil, false},
		{"adult beats a G rating", MediaMovie, rated("G"), true, nil, false},
		{"hidden genre beats a G rating", MediaMovie, rated("G"), false, []int{18, 27}, false},
		{"other genres pass", MediaMovie, rated("G"), false, []int{18, 35}, true},
		{"TV-Y under TV-PG", MediaTV, rated("TV-Y"), false, nil, true},
		{"TV-14 above TV-PG", MediaTV, rated("TV-14"), false, nil, false},
		{"tv genre block", MediaTV, rated("TV-G"), false, []int{10768}, false},
		{"movie genre does not block tv", MediaTV, rated("TV-G"), false, []int{27}, true},
		{"unknown media type", "book", rated("G"), false, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ev.Allows(tc.media, tc.r, tc.adult, tc.genre); got != tc.want {
				t.Fatalf("Allows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluatorUnratedFollowsBlockUnrated(t *testing.T) {
	p := usPolicy()
	p.BlockUnrated = false
	ev := evaluatorFor(t, p)
	if !ev.Allows(MediaMovie, Rating{}, false, nil) {
		t.Fatal("unrated title should pass when block_unrated is off")
	}
	if !ev.Allows(MediaMovie, Rating{Certification: "NR", Known: true}, false, nil) {
		t.Fatal("NR should pass when block_unrated is off")
	}
	if ev.Allows(MediaMovie, Rating{Certification: "R", Known: true}, false, nil) {
		t.Fatal("R must stay hidden regardless of block_unrated")
	}
	if ev.Allows(MediaMovie, Rating{}, true, nil) {
		t.Fatal("adult must stay hidden regardless of block_unrated")
	}
}

func TestEvaluatorEmptyCapBlocksEveryRatedTitle(t *testing.T) {
	p := usPolicy()
	p.MaxMovieRating = ""
	ev := evaluatorFor(t, p)
	if ev.Allows(MediaMovie, Rating{Certification: "G", Known: true}, false, nil) {
		t.Fatal("a cap the scheme does not know must not allow a rated title")
	}
}

func TestAllowsArrRecordRegionThenUSThenUnrated(t *testing.T) {
	// GB policy: caps at 12A for movies. A Radarr left on its US default
	// writes "PG-13"; that is read in the US scheme against the US
	// spelling of the cap only when the cap resolves there, which "12A"
	// does not, so it counts as unrated.
	p := Policy{MaxMovieRating: "12A", MaxTVRating: "12", RatingRegion: "GB", BlockUnrated: true, BlockedMovieGenres: []int{878}}
	ev := evaluatorFor(t, p)

	if !ev.AllowsArrRecord(MediaMovie, "PG", nil) {
		t.Fatal("GB PG is under 12A")
	}
	if ev.AllowsArrRecord(MediaMovie, "15", nil) {
		t.Fatal("GB 15 is above 12A")
	}
	if ev.AllowsArrRecord(MediaMovie, "PG-13", nil) {
		t.Fatal("a US-only certification cannot be ranked against a GB cap and counts as unrated")
	}
	if ev.AllowsArrRecord(MediaMovie, "", nil) {
		t.Fatal("no certification is unrated and hidden")
	}
	if ev.AllowsArrRecord(MediaMovie, "PG", []string{"Sci-Fi & Fantasy"}) {
		t.Fatal("Sci-Fi is an alias of the hidden Science Fiction genre")
	}
	if ev.AllowsArrRecord(MediaMovie, "PG", []string{"Science Fiction"}) {
		t.Fatal("the hidden genre by its own name")
	}
	if !ev.AllowsArrRecord(MediaMovie, "PG", []string{"Fantasy"}) {
		t.Fatal("a sibling genre is not hidden")
	}

	// US policy: the arr's US strings resolve in the region scheme directly.
	us := evaluatorFor(t, usPolicy())
	if !us.AllowsArrRecord(MediaMovie, "pg", nil) || us.AllowsArrRecord(MediaMovie, "R", nil) {
		t.Fatal("US certifications rank in the US scheme")
	}
	if !us.AllowsArrRecord(MediaTV, "TV-G", []string{"Drama"}) || us.AllowsArrRecord(MediaTV, "TV-MA", nil) {
		t.Fatal("Sonarr TV certifications rank in the US scheme")
	}
	if us.AllowsArrRecord(MediaTV, "TV-G", []string{"War & Politics"}) {
		t.Fatal("hidden TV genre by name")
	}
	if us.AllowsArrRecord("book", "", nil) {
		t.Fatal("unknown media type is never allowed")
	}
}

func TestDescribeNamesLimitsAndHiddenGenres(t *testing.T) {
	ev := evaluatorFor(t, usPolicy())
	got := ev.Describe()
	for _, want := range []string{"movies up to PG", "shows up to TV-PG", "US ratings", "unrated titles hidden", "Horror (movies)", "War & Politics (shows)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Describe = %q, missing %q", got, want)
		}
	}
}

func TestGenreAliasesSplitCompoundNames(t *testing.T) {
	got := genreAliases("Sci-Fi & Fantasy")
	joined := strings.Join(got, "|")
	for _, want := range []string{"sci-fi & fantasy", "sci-fi", "fantasy", "science fiction"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("aliases %v missing %q", got, want)
		}
	}
	if aliases := genreAliases("Kids"); !strings.Contains(strings.Join(aliases, "|"), "children") {
		t.Fatalf("Kids aliases %v missing children", aliases)
	}
}

func TestNormalizeGenreIDsSortsDedupesAndDropsJunk(t *testing.T) {
	got := normalizeGenreIDs([]int{27, 0, -1, 18, 27})
	if len(got) != 2 || got[0] != 18 || got[1] != 27 {
		t.Fatalf("normalizeGenreIDs = %v", got)
	}
	if decodeGenres("not json") == nil || len(decodeGenres("not json")) != 0 {
		t.Fatal("bad JSON decodes to an empty, non-nil list")
	}
	if encodeGenres(nil) != "[]" {
		t.Fatalf("nil encodes as %q, want []", encodeGenres(nil))
	}
}
