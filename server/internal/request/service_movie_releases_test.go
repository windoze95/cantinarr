package request

import (
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/radarr"
)

// TestMovieStatusCarriesReleaseDates pins the release dates a movie's status
// response hands the detail screen: they come from the Radarr record the status
// call already reads, so a title that reads "Requested" can say it simply isn't
// out yet instead of looking like a stalled download.
func TestMovieStatusCarriesReleaseDates(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Dune: Part Three","tmdbId":550,"year":2026,
			"hasFile":false,"monitored":true,
			"inCinemas":"2026-07-03T00:00:00Z","digitalRelease":"2026-09-12T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Status != StatusRequested {
		t.Fatalf("status = %q, want %q", resp.Status, StatusRequested)
	}
	if resp.Releases == nil {
		t.Fatal("releases = nil, want the movie's dates")
	}
	if resp.Releases.InCinemas != "2026-07-03" {
		t.Errorf("in_cinemas = %q, want 2026-07-03", resp.Releases.InCinemas)
	}
	if resp.Releases.Digital != "2026-09-12" {
		t.Errorf("digital = %q, want 2026-09-12", resp.Releases.Digital)
	}
}

// TestMovieReleaseDatesDoNotShiftZones is the regression pin for the trap that
// dogs every arr date: a movie release is a calendar date with no meaningful
// time-of-day, so converting it moves it onto the neighbouring day.
//
// The dates are given a location deliberately far from UTC, which is what makes
// the test bite on a UTC CI box: inserting either .UTC() or .Local() into the
// projection drags this midnight back to the 2nd and the 11th and fails here.
// (The process-wide time.Local is emphatically not moved to achieve that — it
// races with every httptest server the rest of the package leaves running.)
func TestMovieReleaseDatesDoNotShiftZones(t *testing.T) {
	tz := time.FixedZone("UTC+13", 13*60*60)
	cinemas := time.Date(2026, 7, 3, 0, 0, 0, 0, tz)
	digital := time.Date(2026, 9, 12, 0, 0, 0, 0, tz)

	got := movieReleases(&radarr.Movie{InCinemas: &cinemas, DigitalRelease: &digital})
	if got == nil {
		t.Fatal("releases = nil, want the movie's dates")
	}
	if got.InCinemas != "2026-07-03" || got.Digital != "2026-09-12" {
		t.Errorf("dates = %q/%q, want 2026-07-03/2026-09-12 (a zone conversion slipped in)",
			got.InCinemas, got.Digital)
	}
}

// TestMovieStatusReleasesOmittedWhenUnknown keeps the field absent rather than
// empty when Radarr knows neither date, so a client never renders a blank
// release line.
func TestMovieStatusReleasesOmittedWhenUnknown(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Fight Club","tmdbId":550,"hasFile":false,"monitored":true}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Releases != nil {
		t.Errorf("releases = %+v, want nil when neither date is known", resp.Releases)
	}
}

// TestMovieStatusReleasesOnlyPartiallyKnown covers the common pre-theatrical
// shape: a cinema date is set and the digital date has not been announced.
func TestMovieStatusReleasesOnlyPartiallyKnown(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Fight Club","tmdbId":550,"hasFile":false,"monitored":true,
			"inCinemas":"2026-07-03T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Releases == nil || resp.Releases.InCinemas != "2026-07-03" {
		t.Fatalf("releases = %+v, want the cinema date alone", resp.Releases)
	}
	if resp.Releases.Digital != "" {
		t.Errorf("digital = %q, want empty (not announced)", resp.Releases.Digital)
	}
}

// TestMovieStatusReleasesRideAvailableTitles keeps the dates on an available
// movie: a file can land before the digital date, and deciding whether a
// milestone still matters is the client's call, not the server's.
func TestMovieStatusReleasesRideAvailableTitles(t *testing.T) {
	f := &fakeRadarr{
		libraryJSON: `[{"id":1,"title":"Fight Club","tmdbId":550,"hasFile":true,"monitored":true,
			"digitalRelease":"2026-09-12T00:00:00Z"}]`,
	}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Status != StatusAvailable {
		t.Fatalf("status = %q, want %q", resp.Status, StatusAvailable)
	}
	if resp.Releases == nil || resp.Releases.Digital != "2026-09-12" {
		t.Errorf("releases = %+v, want the digital date to ride along", resp.Releases)
	}
}

// TestMovieStatusUnaddedTitleHasNoReleases pins the scope: dates come from the
// arr record, so a title nobody has added carries none.
func TestMovieStatusUnaddedTitleHasNoReleases(t *testing.T) {
	f := &fakeRadarr{}
	srv := newFakeRadarrServer(t, f)
	s, uid := newHistoryTestService(t, srv.URL, "", "")

	resp, err := s.GetUserStatus(uid, 550, "movie", "")
	if err != nil {
		t.Fatalf("GetUserStatus: %v", err)
	}
	if resp.Status != StatusUnavailable {
		t.Fatalf("status = %q, want %q", resp.Status, StatusUnavailable)
	}
	if resp.Releases != nil {
		t.Errorf("releases = %+v, want nil for a title not in the library", resp.Releases)
	}
}
