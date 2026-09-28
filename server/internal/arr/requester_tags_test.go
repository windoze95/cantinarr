package arr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

func TestRequesterTagsAddWithoutReplacingNativeState(t *testing.T) {
	for _, collection := range []string{"movie", "series"} {
		t.Run(collection, func(t *testing.T) {
			key := "tmdbId"
			if collection == "series" {
				key = "tvdbId"
			}
			tags := []nativeTag{{ID: 90, Label: "cantinarr-42-someone-else"}}
			titleTags := []int{90, 7}
			creates, updates := 0, 0
			var guardErr error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "test-key" {
					t.Error("missing API key")
				}
				title := map[string]any{"id": 31, key: 123, "tags": titleTags, "monitored": true, "qualityProfileId": 8}
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v3/" + collection:
					if r.URL.Query().Get(key) != "123" {
						t.Error("unscoped native lookup")
					}
					json.NewEncoder(w).Encode([]any{title})
				case "GET /api/v3/" + collection + "/31":
					json.NewEncoder(w).Encode(title)
				case "GET /api/v3/tag":
					json.NewEncoder(w).Encode(tags)
				case "POST /api/v3/tag":
					creates++
					var tag nativeTag
					json.NewDecoder(r.Body).Decode(&tag)
					tag.ID = 100
					tags = append(tags, tag)
					// Accepted create, lost response: the retry must find it.
					w.WriteHeader(503)
				case "PUT /api/v3/" + collection + "/editor":
					updates++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					want := map[string]any{collection + "Ids": []any{float64(31)}, "tags": []any{float64(100)}, "applyTags": "add"}
					if !reflect.DeepEqual(body, want) {
						t.Errorf("editor body = %#v; want only additive tags", body)
					}
					titleTags = append(titleTags, 100)
					w.WriteHeader(202)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client := NewRequesterTags(server.Client(), server.URL, "test-key", collection, key)
			guard := func() error { return guardErr }
			for _, label := range []string{"cantinarr-4-first-name", "cantinarr-4-renamed-user"} {
				got, err := client.Apply(context.Background(), 123, "cantinarr-4-", label, guard)
				if err != nil || got != "cantinarr-4-first-name" {
					t.Fatalf("apply = %q, %v", got, err)
				}
			}
			if creates != 1 || updates != 1 || !reflect.DeepEqual(titleTags, []int{90, 7, 100}) {
				t.Fatalf("creates=%d updates=%d tags=%v", creates, updates, titleTags)
			}
			guardErr = errors.New("permission revoked")
			if _, err := client.Apply(context.Background(), 123, "cantinarr-5-", "cantinarr-5-user", guard); !errors.Is(err, guardErr) {
				t.Fatalf("guard error lost: %v", err)
			}
			if creates != 1 || updates != 1 {
				t.Fatal("revoked permission wrote native state")
			}
		})
	}
}

func TestRequesterTagsRefuseUnverifiedTitle(t *testing.T) {
	for _, body := range []string{`[]`, `[{"id":31,"tmdbId":999}]`, `[{"id":31,"tmdbId":123},{"id":32,"tmdbId":123}]`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/api/v3/movie" {
				t.Errorf("unverified identity reached %s %s", r.Method, r.URL)
			}
			w.Write([]byte(body))
		}))
		client := NewRequesterTags(server.Client(), server.URL, "key", "movie", "tmdbId")
		_, err := client.Apply(context.Background(), 123, "cantinarr-4-", "cantinarr-4-user", func() error { return nil })
		server.Close()
		if !errors.Is(err, ErrTagIdentity) {
			t.Fatalf("body %s = %v", body, err)
		}
	}
}

func TestRequesterTagsRetryEvidenceAndRedaction(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "123")
			w.WriteHeader(status)
			w.Write([]byte("private-key http://private-arr:123/internal"))
		}))
		client := NewRequesterTags(server.Client(), server.URL, "private-key", "movie", "tmdbId")
		_, err := client.Apply(context.Background(), 123, "cantinarr-4-", "cantinarr-4-user", func() error { return nil })
		server.Close()
		retry, delay := transporterr.Retry(err)
		if retry != (status >= 500 || status == 429) || delay != 123*time.Second {
			t.Fatalf("status %d: retry=%v delay=%v error=%v", status, retry, delay, err)
		}
		if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), server.URL) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}
