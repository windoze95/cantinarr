package musicdiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchSinglesAreOptInAndCreditsKeepIDs(t *testing.T) {
	var queries []string
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query().Get("query"))
		rows := groups(aID, bID)
		rows[1]["primary-type"] = "Single"
		rows[1]["artist-credit"] = []any{map[string]any{"name": "Same artist", "artist": map[string]any{"id": cID, "name": "Same artist"}}}
		jsonResponse(w, map[string]any{"count": 2, "offset": 0, "release-groups": rows})
	})
	body, err := s.Search(context.Background(), "Same title", 1)
	legacy := decodePage(t, body, err)
	body, err = s.Search(context.Background(), "Same title", 1, true)
	all := decodePage(t, body, err)
	if len(legacy.Results) != 1 || len(all.Results) != 2 || all.Results[1].ReleaseType != "Single" || all.Results[1].Artists[0].ForeignID != cID {
		t.Fatalf("lost identities/types: %+v %+v", legacy, all)
	}
	if len(queries) != 2 || strings.Contains(queries[0], "primarytype:single") || !strings.Contains(queries[1], "primarytype:single") {
		t.Fatal(queries)
	}
}

func TestArtistSearchDisambiguationAndExactDiscographyPagination(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "artist") {
			jsonResponse(w, map[string]any{"count": 2, "offset": 0, "artists": []any{map[string]any{"id": aID, "name": "Same name", "disambiguation": "UK band"}, map[string]any{"id": bID, "name": "Same name", "disambiguation": "US singer"}}})
			return
		}
		q := r.URL.Query()
		if q.Get("artist") != bID || q.Get("offset") != "20" || q.Get("query") != "" || q.Get("type") != "album|ep|single" {
			t.Error(q)
		}
		jsonResponse(w, map[string]any{"release-group-count": 41, "release-group-offset": 20, "release-groups": groups(cID)})
	})
	body, err := s.SearchArtists(context.Background(), "Same name", 1)
	if err != nil {
		t.Fatal(err)
	}
	var artists ArtistPage
	if json.Unmarshal(body, &artists) != nil || len(artists.Results) != 2 || artists.Results[0].ForeignID == artists.Results[1].ForeignID || artists.Results[1].Disambiguation != "US singer" {
		t.Fatalf("artist disambiguation lost: %s", body)
	}
	body, err = s.ArtistAlbums(context.Background(), bID, 2)
	page := decodePage(t, body, err)
	if page.NextPage != 3 || page.Results[0].ForeignID != cID {
		t.Fatal(page)
	}
}

func TestSingleDetailsAndUnverifiedCanonicalRejected(t *testing.T) {
	s := testService(t, func(w http.ResponseWriter, r *http.Request) {
		id := aID
		if strings.Contains(r.URL.Path, bID) {
			id = cID
		}
		jsonResponse(w, map[string]any{"id": id, "title": "Single", "primary-type": "Single"})
	})
	body, err := s.Album(context.Background(), aID)
	if err != nil || !strings.Contains(string(body), `"Single"`) {
		t.Fatalf("single rejected: %s %v", body, err)
	}
	if _, err = s.ResolveAlbum(context.Background(), bID, false); err == nil {
		t.Fatal("unverified canonical identity accepted")
	}
}

func TestAbandonedSharedSearchCancelsOnlyAfterLastCaller(t *testing.T) {
	var m memo
	workStarted := make(chan struct{})
	workCancelled := make(chan struct{})
	load := func(ctx context.Context) ([]byte, error) {
		close(workStarted)
		<-ctx.Done()
		close(workCancelled)
		return nil, ctx.Err()
	}
	a, cancelA := context.WithCancel(context.Background())
	b, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()
	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() { defer close(doneA); m.get(a, "search", time.Minute, load) }()
	<-workStarted
	go func() { defer close(doneB); m.get(b, "search", time.Minute, load) }()
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		n := m.pending["search"].callers
		m.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second caller did not join")
		}
		time.Sleep(time.Millisecond)
	}
	cancelA()
	<-doneA
	select {
	case <-workCancelled:
		t.Fatal("one caller cancelled shared work")
	default:
	}
	cancelB()
	<-doneB
	select {
	case <-workCancelled:
	case <-time.After(time.Second):
		t.Fatal("abandoned work still running")
	}
}

func TestInteractiveDeadlineIncludesPacingAndCancelsProvider(t *testing.T) {
	var hits atomic.Int32
	s := testService(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); <-r.Context().Done() })
	s.mb.next = time.Now().Add(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Search(ctx, "paced", 1, true); err == nil {
		t.Fatal("deadline ignored")
	}
	if time.Since(start) > time.Second || hits.Load() != 0 {
		t.Fatal("deadline did not include pacing")
	}
	s.mb.mu.Lock()
	s.mb.next = time.Time{}
	s.mb.mu.Unlock()
	start = time.Now()
	_, err := s.Search(context.Background(), "stalled", 1, true)
	if err == nil || time.Since(start) > 11*time.Second || time.Since(start) < 9*time.Second {
		t.Fatal(fmt.Sprintf("interactive bound: %v %v", time.Since(start), err))
	}
}
