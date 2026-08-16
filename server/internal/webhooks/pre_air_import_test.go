package webhooks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The pre-air gate is the free half of catching a season the service filled
// before it aired: it reads air dates the import event already carries and
// touches no network, so the overwhelmingly normal import — a file for an
// episode that has aired — must cost nothing at all. Everything pinned below is
// about that boundary: what never reaches the witness, and the single call the
// impossible case makes.

type preAirCall struct {
	instanceID   string
	tvdbID       int
	tmdbID       int
	seasonNumber int
	title        string
}

// fakePreAirWitness stands in for *remediation.Service. It records rather than
// counts, because "called once" and "called once with the right season" are
// different assertions and only the second one is worth anything.
type fakePreAirWitness struct {
	mu    sync.Mutex
	calls []preAirCall
}

func (f *fakePreAirWitness) RecordPreAirImport(instanceID string, tvdbID, tmdbID, seasonNumber int, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, preAirCall{
		instanceID:   instanceID,
		tvdbID:       tvdbID,
		tmdbID:       tmdbID,
		seasonNumber: seasonNumber,
		title:        title,
	})
}

func (f *fakePreAirWitness) recorded() []preAirCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]preAirCall(nil), f.calls...)
}

// newArrLibraryStub answers the reads the Download path already made before the
// pre-air gate existed, so a pre-air assertion is never measuring a series read
// that failed for unrelated reasons.
func newArrLibraryStub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/series/28":
			_, _ = w.Write([]byte(`{"id":28,"title":"Futurama","tvdbId":73871,"tmdbId":615,"monitored":true}`))
		case "/api/v3/episode":
			_, _ = w.Write([]byte(`[{"id":1101,"seriesId":28,"seasonNumber":11,"episodeNumber":1,"hasFile":true,"monitored":true}]`))
		case "/api/v3/movie/7":
			_, _ = w.Write([]byte(`{"id":7,"title":"A Movie","tmdbId":600,"hasFile":true,"monitored":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newPreAirFixture wires a handler with the witness attached. The stub arr
// serves both the Sonarr and the Radarr instance so the movie case is exercised
// against a real reply too.
func newPreAirFixture(t *testing.T) (*fixture, *fakePreAirWitness) {
	t.Helper()
	arr := newArrLibraryStub(t)
	f := newFixture(t, arr.URL, arr.URL)
	witness := &fakePreAirWitness{}
	f.handler.SetPreAirImportWitness(witness)
	return f, witness
}

// episodeJSON renders one entry of Sonarr's episodes[] as the service sends it.
// airsIn is relative to now because the gate compares against the real clock.
func episodeJSON(id, number, season int, airsIn time.Duration) string {
	return fmt.Sprintf(`{"id":%d,"episodeNumber":%d,"seasonNumber":%d,"airDateUtc":%q}`,
		id, number, season, time.Now().UTC().Add(airsIn).Format(time.RFC3339))
}

func sonarrDownload(episodes ...string) string {
	body := `{"eventType":"Download","series":{"id":28,"title":"Futurama","tvdbId":73871,"tmdbId":615}`
	if len(episodes) > 0 {
		body += `,"episodes":[`
		for i, ep := range episodes {
			if i > 0 {
				body += ","
			}
			body += ep
		}
		body += `]`
	}
	return body + `}`
}

// TestPreAirGateIgnoresAnOrdinaryImport is the whole cost argument. Every
// episode in this import has aired, which is what almost every import looks
// like, and the witness — the half that reads the library — must never be
// reached. The existing Download side effects are asserted alongside it: the
// gate is an addition to that path, not a replacement for it.
func TestPreAirGateIgnoresAnOrdinaryImport(t *testing.T) {
	f, witness := newPreAirFixture(t)

	rec := f.post(t, "/api/webhooks/arr/"+f.sonarrID, sonarrDownload(
		episodeJSON(1101, 1, 11, -30*24*time.Hour),
		episodeJSON(1102, 2, 11, -23*24*time.Hour),
		episodeJSON(1103, 3, 11, -2*time.Second),
	), basicWebhookAuth(f.sonarrTok))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if calls := witness.recorded(); len(calls) != 0 {
		t.Fatalf("an import of aired episodes reached the witness: %+v", calls)
	}
	// Unchanged behaviour: availability invalidated, status broadcast, alert pushed.
	if len(f.requests.instanceIDs) != 1 || f.requests.instanceIDs[0] != f.sonarrID {
		t.Errorf("digests invalidated = %v, want [%s]", f.requests.instanceIDs, f.sonarrID)
	}
	if len(f.hub.events) != 1 || f.hub.events[0].Type != "request_status_changed" {
		t.Errorf("events = %v, want one request_status_changed", f.eventTypes())
	}
	if len(f.content.episodes) != 1 || f.content.episodes[0] != "Futurama" {
		t.Errorf("episode pushes = %v, want [Futurama]", f.content.episodes)
	}
}

// TestPreAirGateReportsAnImpossibleImport: one episode of the batch has an air
// date still in the future, so a file claiming to be it cannot be it. The
// witness is handed the identity of the SERIES and the SEASON — never the
// episode — because the finding is season-shaped and so is the repair.
func TestPreAirGateReportsAnImpossibleImport(t *testing.T) {
	f, witness := newPreAirFixture(t)

	rec := f.post(t, "/api/webhooks/arr/"+f.sonarrID, sonarrDownload(
		episodeJSON(1101, 1, 11, -30*24*time.Hour),
		episodeJSON(1105, 5, 11, 21*24*time.Hour),
	), basicWebhookAuth(f.sonarrTok))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	calls := witness.recorded()
	if len(calls) != 1 {
		t.Fatalf("witness calls = %+v, want exactly 1", calls)
	}
	want := preAirCall{instanceID: f.sonarrID, tvdbID: 73871, tmdbID: 615, seasonNumber: 11, title: "Futurama"}
	if calls[0] != want {
		t.Fatalf("witness call = %+v, want %+v", calls[0], want)
	}
	// The rest of the Download path still ran; the gate is not an early exit.
	if len(f.hub.events) != 1 || len(f.content.episodes) != 1 {
		t.Errorf("import side effects lost: events=%v pushes=%v", f.eventTypes(), f.content.episodes)
	}
}

// TestPreAirGateOpensOneInvestigationForAPack is the assertion the whole gate
// is shaped around. A season pack imports as one event carrying ten episodes;
// ten reports would mean ten library reads, ten issues, ten agent runs and ten
// approvals for one problem with one repair.
func TestPreAirGateOpensOneInvestigationForAPack(t *testing.T) {
	f, witness := newPreAirFixture(t)

	episodes := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		episodes = append(episodes, episodeJSON(1100+i, i, 11, time.Duration(i)*7*24*time.Hour))
	}
	rec := f.post(t, "/api/webhooks/arr/"+f.sonarrID, sonarrDownload(episodes...), basicWebhookAuth(f.sonarrTok))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	calls := witness.recorded()
	if len(calls) != 1 {
		t.Fatalf("a ten-episode pack produced %d witness calls, want exactly 1: %+v", len(calls), calls)
	}
	if calls[0].seasonNumber != 11 {
		t.Fatalf("season = %d, want 11", calls[0].seasonNumber)
	}
}

// TestPreAirGateStaysSilent walks every payload that must cost nothing. Each
// case still has to answer 200 and run the rest of the Download path — a gate
// that swallowed the callback would be worse than no gate at all.
func TestPreAirGateStaysSilent(t *testing.T) {
	cases := []struct {
		name    string
		radarr  bool
		attach  bool // wire the witness at all
		payload string
	}{
		{
			name:    "an import with no episodes has no air date to judge",
			attach:  true,
			payload: sonarrDownload(),
		},
		{
			name:   "an episode the service carries no air date for is evidence of nothing",
			attach: true,
			payload: `{"eventType":"Download","series":{"id":28,"title":"Futurama","tvdbId":73871,"tmdbId":615},` +
				`"episodes":[{"id":1101,"episodeNumber":1,"seasonNumber":11,"airDateUtc":null}]}`,
		},
		{
			name:   "a movie import is not a season, whatever else the body carries",
			radarr: true,
			attach: true,
			payload: `{"eventType":"Download","movie":{"id":7,"title":"A Movie","tmdbId":600},` +
				`"episodes":[{"id":1,"episodeNumber":1,"seasonNumber":1,"airDateUtc":"2099-01-01T00:00:00Z"}]}`,
		},
		{
			name:    "no witness wired is a no-op, not a panic",
			attach:  false,
			payload: sonarrDownload(episodeJSON(1101, 1, 11, 21*24*time.Hour)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arr := newArrLibraryStub(t)
			f := newFixture(t, arr.URL, arr.URL)
			witness := &fakePreAirWitness{}
			if tc.attach {
				f.handler.SetPreAirImportWitness(witness)
			}
			instanceID, token := f.sonarrID, f.sonarrTok
			if tc.radarr {
				instanceID, token = f.radarrID, f.radarrTok
			}

			rec := f.post(t, "/api/webhooks/arr/"+instanceID, tc.payload, basicWebhookAuth(token))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if calls := witness.recorded(); len(calls) != 0 {
				t.Fatalf("witness was called: %+v", calls)
			}
			// The import was still witnessed the way it always was.
			if len(f.requests.instanceIDs) != 1 {
				t.Errorf("digest invalidations = %v, want exactly one", f.requests.instanceIDs)
			}
			if len(f.hub.events) != 1 || f.hub.events[0].Type != "request_status_changed" {
				t.Errorf("events = %v, want one request_status_changed", f.eventTypes())
			}
		})
	}
}

// TestPreAirGateOnlyLooksAtImports: Sonarr sends the same episodes[] on a Grab,
// hours before anything lands. Diagnosing a season on the strength of a release
// the service has not even imported yet would open an issue about a file that
// does not exist.
func TestPreAirGateOnlyLooksAtImports(t *testing.T) {
	f, witness := newPreAirFixture(t)

	unaired := episodeJSON(1105, 5, 11, 21*24*time.Hour)
	for _, event := range []string{"Grab", "EpisodeFileDelete", "SeriesAdd", "Rename"} {
		body := fmt.Sprintf(
			`{"eventType":%q,"series":{"id":28,"title":"Futurama","tvdbId":73871,"tmdbId":615},"episodes":[%s]}`,
			event, unaired)
		if rec := f.post(t, "/api/webhooks/arr/"+f.sonarrID, body, basicWebhookAuth(f.sonarrTok)); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", event, rec.Code)
		}
	}
	if calls := witness.recorded(); len(calls) != 0 {
		t.Fatalf("a non-import event reached the witness: %+v", calls)
	}
}

func (f *fakePreAirWitness) RecordSuspectImport(instanceID string, tvdbID, tmdbID, seasonNumber, episodeNumber int, title string) {
}
