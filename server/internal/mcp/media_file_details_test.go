package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// The read that makes "wrong audio" falsifiable: files the arr analyzed render
// their audio languages and subtitles; files it has not analyzed say so
// explicitly — blindness, never silent absence.
func TestGetMediaFileDetailsRendersMediaInfoAndBlindness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/series":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 3, "title": "Probe Show", "tvdbId": 777, "tmdbId": 555}})
		case r.URL.Path == "/api/v3/episode":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 31, "seriesId": 3, "seasonNumber": 1, "episodeNumber": 1, "hasFile": true, "episodeFileId": 91},
				{"id": 32, "seriesId": 3, "seasonNumber": 1, "episodeNumber": 2, "hasFile": true, "episodeFileId": 92},
			})
		case r.URL.Path == "/api/v3/episodefile":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 91, "seriesId": 3, "seasonNumber": 1, "relativePath": "s01e01.mkv", "size": 1000000000, "sceneName": "Probe.S01E01",
					"mediaInfo": map[string]any{"resolution": "1080p", "videoCodec": "x265", "audioCodec": "EAC3", "audioChannels": 5.1, "audioLanguages": "eng/jpn", "subtitles": "eng", "runTime": "42:00"}},
				{"id": 92, "seriesId": 3, "seasonNumber": 1, "relativePath": "s01e02.mkv", "size": 900000000, "sceneName": "Probe.S01E02"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, cerr := secrets.NewCipher(bytes.Repeat([]byte{0x11}, 32))
	if cerr != nil {
		t.Fatalf("cipher: %v", cerr)
	}
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{ID: "sonarr-probe", ServiceType: "sonarr", Name: "TV", URL: srv.URL, APIKey: "k"}); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	server := NewToolServer(nil, nil, instance.NewRegistry(store), nil)

	res, err := server.getMediaFileDetails(json.RawMessage(`{"media_type":"tv","tmdb_id":555,"season_number":1}`), "sonarr-probe")
	if err != nil {
		t.Fatalf("getMediaFileDetails: %v", err)
	}
	if !strings.Contains(res.Text, "eng/jpn") || !strings.Contains(res.Text, "subs [eng]") {
		t.Fatalf("analyzed file's audio/subs missing from:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "blindness, not absence") {
		t.Fatalf("unanalyzed file must declare blindness:\n%s", res.Text)
	}
}
