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

// The music arm: Lidarr's audio analysis renders per track file, and a file
// Lidarr has not analyzed says so — the same blindness rule as movies and TV.
func TestGetMediaFileDetailsRendersAlbumAudioAndBlindness(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/album/77":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "title": "Probe Album", "artistId": 3})
		case r.URL.Path == "/api/v1/trackfile":
			if r.URL.Query().Get("albumId") != "77" {
				t.Errorf("trackfile query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 901, "albumId": 77, "path": "/music/Probe/01 - Opener.flac", "size": 31000000, "dateAdded": "2026-08-30T10:00:00Z",
					"quality":   map[string]any{"quality": map[string]any{"name": "FLAC"}},
					"mediaInfo": map[string]any{"audioChannels": 2, "audioBitRate": "912 kbps", "audioCodec": "FLAC", "audioBits": "16bit", "audioSampleRate": "44.1kHz"}},
				{"id": 902, "albumId": 77, "path": "/music/Probe/02 - Closer.mp3", "size": 8000000,
					"quality": map[string]any{"quality": map[string]any{"name": "MP3-320"}}},
				{"id": 903, "albumId": 78, "path": "/music/Other/01.flac", "size": 1},
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
	cipher, cerr := secrets.NewCipher(bytes.Repeat([]byte{0x12}, 32))
	if cerr != nil {
		t.Fatalf("cipher: %v", cerr)
	}
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{ID: "lidarr-probe", ServiceType: "lidarr", Name: "Music", URL: srv.URL, APIKey: "k"}); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	server := NewToolServer(nil, nil, instance.NewRegistry(store), nil)

	res, err := server.getMediaFileDetails(json.RawMessage(`{"media_type":"music","album_id":77}`), "lidarr-probe")
	if err != nil {
		t.Fatalf("getMediaFileDetails: %v", err)
	}
	for _, want := range []string{
		"Probe Album — 2 track file(s):",
		"- 01 - Opener.flac (31.0 MB, FLAC, imported 2026-08-30)",
		"audio FLAC · 16bit · 44.1kHz · 2.0ch · 912 kbps",
		"- 02 - Closer.mp3 (8.0 MB, MP3-320)",
		"blindness, not absence",
	} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("album file details missing %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "/music/Probe") || strings.Contains(res.Text, "Other") {
		t.Fatalf("full paths or another album's file leaked:\n%s", res.Text)
	}

	// The scope guard: music is keyed by album_id, never by a title id.
	refused, err := server.getMediaFileDetails(json.RawMessage(`{"media_type":"music","tmdb_id":77}`), "lidarr-probe")
	if err != nil {
		t.Fatalf("getMediaFileDetails without album_id: %v", err)
	}
	if !strings.Contains(refused.Text, "album_id") {
		t.Fatalf("missing album_id was not refused: %q", refused.Text)
	}
}
