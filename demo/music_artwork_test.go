package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"testing"
)

func TestPublicDomainAlbumArtworkThroughBothAPIs(t *testing.T) {
	router := buildRouter()
	user := demoTestLogin(t, router, "user")
	data, err := publicDomainMusicFS.ReadFile("assets/music/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog []struct {
		ID          int
		Title       string
		CoverSHA256 string `json:"cover_sha256"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 4 {
		t.Fatalf("got %d public-domain albums, want 4", len(catalog))
	}
	for _, album := range catalog {
		t.Run(album.Title, func(t *testing.T) {
			paths := []string{
				"/api/discover/music/artwork/" + lidAlbumFID(album.ID) + "?instance_id=lidarr-4d5e6f7a",
				fmt.Sprintf("/api/instances/lidarr-4d5e6f7a/api/v1/mediacover/album/%d/cover.jpg", album.ID),
			}
			for _, path := range paths {
				res := demoTestRequest(t, router, http.MethodGet, path, user, nil)
				if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "image/png" {
					t.Fatalf("%s: status %d, type %q", path, res.Code, res.Header().Get("Content-Type"))
				}
				if got := fmt.Sprintf("%x", sha256.Sum256(res.Body.Bytes())); got != album.CoverSHA256 {
					t.Fatalf("%s: artwork does not match the recorded original", path)
				}
				if _, err := png.Decode(bytes.NewReader(res.Body.Bytes())); err != nil {
					t.Fatalf("%s: invalid PNG: %v", path, err)
				}
				if res := demoTestRequest(t, router, http.MethodGet, path, "", nil); res.Code != http.StatusUnauthorized {
					t.Fatalf("%s: unauthenticated artwork status %d", path, res.Code)
				}
			}
		})
	}
}
