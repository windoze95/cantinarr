package request

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
)

// tv4KEpisodes builds a two-season show whose every aired episode has the
// given file id, plus an unaired episode that has none.
func tv4KEpisodes(fileIDs ...int) string {
	rows := []string{}
	for i, id := range fileIDs {
		hasFile := id > 0
		rows = append(rows, fmt.Sprintf(`{"id":%d,"seriesId":7,"seasonNumber":%d,"episodeNumber":%d,"hasFile":%t,"episodeFileId":%d,"monitored":true,"airDateUtc":"2020-01-01T00:00:00Z"}`,
			i+1, 1+i/2, 1+i%2, hasFile, id))
	}
	rows = append(rows, `{"id":99,"seriesId":7,"seasonNumber":2,"episodeNumber":3,"hasFile":false,"episodeFileId":0,"monitored":true,"airDateUtc":"2100-01-01T00:00:00Z"}`)
	return "[" + strings.Join(rows, ",") + "]"
}

// tv4KFiles serves one Sonarr 4.0.20-shaped file record per "id:resolution"
// pair; an empty resolution is a file Sonarr never analysed.
func tv4KFiles(pairs ...string) string {
	rows := []string{}
	for _, pair := range pairs {
		id, resolution, _ := strings.Cut(pair, ":")
		mediaInfo := "null"
		if resolution != "" {
			mediaInfo = fmt.Sprintf(`{"resolution":%q}`, resolution)
		}
		rows = append(rows, fmt.Sprintf(`{"id":%s,"seriesId":7,"quality":{"quality":{"name":"WEBDL-2160p"}},"mediaInfo":%s}`, id, mediaInfo))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func TestTVStatus4KRequiresEveryCountedFileMeasured(t *testing.T) {
	f := &fakeSonarrTV{
		libraryJSON: `[{"id":7,"title":"Chernobyl","tvdbId":999,"monitored":true,"seasons":[
			{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":true}]}]`,
		episodesJSON:     map[string]string{"": tv4KEpisodes(501, 502, 503, 503)},
		episodeFilesJSON: tv4KFiles("501:3840x2160", "502:3840x1600", "503:3840x2160", "400:1920x1080"),
	}
	srv := newFakeSonarrServer(t, f)
	s, uid := newHistoryTestService(t, "", srv.URL, "")
	installTVFixture(t, s, f, 300)
	seedTvdbCache(t, s, 300, 999)

	read := func(query string) (int, StatusResponse, string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/requests/300/status?media_type=tv&include_instance_statuses=false&"+query, nil)
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("tmdb_id", "300")
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
		r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid}))
		w := httptest.NewRecorder()
		NewHandler(s).GetStatus(w, r)
		var status StatusResponse
		_ = json.Unmarshal(w.Body.Bytes(), &status)
		return w.Code, status, w.Body.String()
	}

	// With the admin switch off, an app asking still gets nothing, and
	// Sonarr's files are never read.
	enabled := false
	s.SetCover4KBadges(func() bool { return enabled })
	if code, st, body := read("include_4k=true"); code != http.StatusOK || st.Status != StatusAvailable || st.Is4K || strings.Contains(body, "is_4k") || f.episodeFileReads != 0 {
		t.Fatalf("switch off: %d %s (file reads %d)", code, body, f.episodeFileReads)
	}
	enabled = true

	// Without the opt-in nothing extra is read or claimed.
	if code, st, body := read(""); code != http.StatusOK || st.Status != StatusAvailable || st.Is4K || strings.Contains(body, "is_4k") || f.episodeFileReads != 0 {
		t.Fatalf("default read: %d %s (file reads %d)", code, body, f.episodeFileReads)
	}
	if code, _, _ := read("include_4k=maybe"); code != http.StatusBadRequest {
		t.Fatalf("bad include_4k = %d, want 400", code)
	}

	// Every counted file measures 4K (a wide 3840x1600 film counts, one file
	// holding two episodes counts once, and the 1080p record no episode
	// points at is ignored).
	if _, st, body := read("include_4k=true"); !st.Is4K || !strings.Contains(body, `"is_4k":true`) {
		t.Fatalf("complete 4K show: %s", body)
	}
	if _, st, _ := read("include_4k=true"); !st.Is4K || f.episodeFileReads != 1 {
		t.Fatalf("unchanged file set re-read Sonarr: is4K=%v reads=%d", st.Is4K, f.episodeFileReads)
	}

	// Replacing one file with an HD copy gives it a new id, so the cached
	// answer for the old set is not reused.
	f.episodesJSON[""] = tv4KEpisodes(501, 502, 503, 504)
	f.episodeFilesJSON = tv4KFiles("501:3840x2160", "502:3840x2160", "503:3840x2160", "504:1920x1080")
	if _, st, _ := read("include_4k=true"); st.Is4K || f.episodeFileReads != 2 {
		t.Fatalf("mixed show claimed 4K or reused the old answer: is4K=%v reads=%d", st.Is4K, f.episodeFileReads)
	}

	// A file Sonarr never analysed (its quality name alone says 2160p) is
	// unknown, and unknown is not 4K.
	f.episodesJSON[""] = tv4KEpisodes(501, 502, 503, 505)
	f.episodeFilesJSON = tv4KFiles("501:3840x2160", "502:3840x2160", "503:3840x2160", "505:")
	if _, st, _ := read("include_4k=true"); st.Is4K {
		t.Fatal("unanalysed file counted as 4K")
	}

	// A failed file read omits the claim and is not remembered.
	f.episodesJSON[""] = tv4KEpisodes(501, 502, 503, 506)
	f.episodeFilesJSON = tv4KFiles("501:3840x2160", "502:3840x2160", "503:3840x2160", "506:3840x2160")
	f.episodeFilesFail = true
	reads := f.episodeFileReads
	if code, st, body := read("include_4k=true"); code != http.StatusOK || st.Status != StatusAvailable || st.Is4K {
		t.Fatalf("failed file read changed the status or claimed 4K: %d %s", code, body)
	}
	f.episodeFilesFail = false
	if _, st, _ := read("include_4k=true"); !st.Is4K || f.episodeFileReads != reads+2 {
		t.Fatalf("recovered read: is4K=%v reads=%d", st.Is4K, f.episodeFileReads-reads)
	}

	// A show with an aired episode missing is partial: no claim, no read.
	f.episodesJSON[""] = tv4KEpisodes(501, 502, 503, 0)
	reads = f.episodeFileReads
	if _, st, _ := read("include_4k=true"); st.Status != StatusPartial || st.Is4K || f.episodeFileReads != reads {
		t.Fatalf("partial show: status=%s is4K=%v reads=%d", st.Status, st.Is4K, f.episodeFileReads-reads)
	}
}

// Movies are answered on the device from the Radarr list, so a movie read
// never grows a TV-only Sonarr call.
func TestMovieStatusIgnoresInclude4K(t *testing.T) {
	radarr := jsonServer(t, map[string]string{
		"/api/v3/movie": `[{"id":1,"tmdbId":603,"title":"The Matrix","hasFile":true,"monitored":true}]`,
		"/api/v3/queue": `{"records":[]}`,
	})
	s, uid := newHistoryTestService(t, radarr.URL, "", "")
	s.SetCover4KBadges(func() bool { return true })
	r := httptest.NewRequest(http.MethodGet, "/api/requests/603/status?media_type=movie&include_4k=true", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("tmdb_id", "603")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
	r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: uid}))
	w := httptest.NewRecorder()
	NewHandler(s).GetStatus(w, r)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "is_4k") {
		t.Fatalf("movie status: %d %s", w.Code, w.Body.String())
	}
}
