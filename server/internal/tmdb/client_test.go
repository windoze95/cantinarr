package tmdb

import "testing"

func TestBalancedTrendingResultsInterleavesMoviesAndTV(t *testing.T) {
	movies := []SearchResult{
		{ID: 1, Title: "Movie 1"},
		{ID: 2, Title: "Movie 2"},
		{ID: 3, Title: "Movie 3"},
		{ID: 4, Title: "Movie 4"},
		{ID: 5, Title: "Movie 5"},
		{ID: 6, Title: "Movie 6"},
	}
	tv := []SearchResult{
		{ID: 101, Name: "Show 1"},
		{ID: 102, Name: "Show 2"},
		{ID: 103, Name: "Show 3"},
		{ID: 104, Name: "Show 4"},
		{ID: 105, Name: "Show 5"},
		{ID: 106, Name: "Show 6"},
	}

	got := balancedTrendingResults(movies, tv, 10)

	if len(got) != 10 {
		t.Fatalf("expected 10 results, got %d", len(got))
	}
	for i, item := range got {
		want := "movie"
		if i%2 == 1 {
			want = "tv"
		}
		if item.MediaType != want {
			t.Fatalf("result %d media type = %q, want %q", i, item.MediaType, want)
		}
	}
}

func TestBalancedTrendingResultsFillsFromAvailableCategory(t *testing.T) {
	movies := []SearchResult{{ID: 1, Title: "Movie 1"}}
	tv := []SearchResult{
		{ID: 101, Name: "Show 1"},
		{ID: 102, Name: "Show 2"},
		{ID: 103, Name: "Show 3"},
	}

	got := balancedTrendingResults(movies, tv, 4)

	if len(got) != 4 {
		t.Fatalf("expected 4 results, got %d", len(got))
	}
	if got[0].MediaType != "movie" {
		t.Fatalf("first result media type = %q, want movie", got[0].MediaType)
	}
	for i := 1; i < len(got); i++ {
		if got[i].MediaType != "tv" {
			t.Fatalf("result %d media type = %q, want tv", i, got[i].MediaType)
		}
	}
}

func TestNormalizeTrendingMediaTypeDefaultsToAll(t *testing.T) {
	if got := normalizeTrendingMediaType(""); got != "all" {
		t.Fatalf("empty media type = %q, want all", got)
	}
	if got := normalizeTrendingMediaType("shows"); got != "tv" {
		t.Fatalf("shows media type = %q, want tv", got)
	}
}
