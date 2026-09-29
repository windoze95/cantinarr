package chaptarr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/transporterr"
)

func TestEnsureAuthorFormatPreservesFullResourceAndCurrentChoices(t *testing.T) {
	author := map[string]json.RawMessage{}
	_ = json.Unmarshal([]byte(`{"id":7,"audiobookQualityProfileId":99,"audiobookMetadataProfileId":3,"audiobookRootFolderPath":"/custom/audio","ebookQualityProfileId":88,"ebookMetadataProfileId":87,"ebookRootFolderPath":"/custom/ebooks","ebookMonitorFuture":true,"audiobookMonitorFuture":false,"tags":[4,9],"unknownSetting":{"id":9007199254740993},"addOptions":{"monitor":"all"}}`), &author)
	original := make(map[string]json.RawMessage)
	for k, v := range author {
		original[k] = v
	}
	writes := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/author/7" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		if r.Method == http.MethodPut {
			writes++
			author = nil
			if err := json.NewDecoder(r.Body).Decode(&author); err != nil {
				t.Error(err)
			}
		}
		_ = json.NewEncoder(w).Encode(author)
	}))
	defer upstream.Close()
	client := NewClient(upstream.URL, "key")
	for range 2 {
		updated, err := client.EnsureAuthorFormatConfig(7, "audiobook", 2, 1, "/default/audio", map[int]bool{3: true})
		if err != nil || updated.AudiobookMetadataProfileID != 1 || updated.AudiobookQualityProfileID != 99 || updated.AudiobookRootFolderPath != "/custom/audio" {
			t.Fatalf("configuration: %+v %v", updated, err)
		}
	}
	if writes != 1 {
		t.Fatalf("repeat unnecessarily changed author: %d writes", writes)
	}
	delete(original, "addOptions")
	original["audiobookMetadataProfileId"] = json.RawMessage(`1`)
	if !reflect.DeepEqual(author, original) {
		t.Fatalf("unrelated settings changed: %s", mustMarshalAuthor(t, author))
	}
}

func mustMarshalAuthor(t *testing.T, author map[string]json.RawMessage) []byte {
	t.Helper()
	data, err := json.Marshal(author)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEnsureAuthorFormatDoesNotWriteAfterFailedReadOrRevokedAccess(t *testing.T) {
	for _, failedRead := range []bool{false, true} {
		t.Run(fmt.Sprint(failedRead), func(t *testing.T) {
			writes := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
				}
				if failedRead {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				fmt.Fprint(w, `{"id":7}`)
			}))
			defer upstream.Close()
			denied := errors.New("access revoked")
			guardCalls := 0
			client := NewClient(upstream.URL, "key").WithMutationGuard(func() error {
				guardCalls++
				return denied
			})
			_, err := client.EnsureAuthorFormatConfig(7, "ebook", 1, 2, "/ebooks", nil)
			if err == nil || writes != 0 {
				t.Fatalf("unprotected update: writes=%d err=%v", writes, err)
			}
			if failedRead {
				if retry, _ := transporterr.Retry(err); !retry {
					t.Fatalf("upstream failure lost retry classification: %v", err)
				}
			} else if guardCalls != 1 {
				t.Fatalf("mutation guard was not checked: calls=%d err=%v", guardCalls, err)
			}
		})
	}
}

func TestAuthorConfigurationReadsRetainHTTPRetryEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusServiceUnavailable)
		// A decodable body must not turn a failed read into empty configuration.
		if r.URL.Path == "/api/v1/author/7" {
			fmt.Fprint(w, `{}`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	}))
	defer upstream.Close()
	client := NewClient(upstream.URL, "key")
	checks := map[string]func() error{
		"author":   func() error { _, err := client.GetAuthor(7); return err },
		"quality":  func() error { _, err := client.GetQualityProfiles(); return err },
		"metadata": func() error { _, err := client.GetMetadataProfiles(); return err },
		"roots":    func() error { _, err := client.GetRootFolders(); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			err := check()
			retry, after := transporterr.Retry(err)
			if !retry || after != 2*time.Minute {
				t.Fatalf("read lost retry evidence: retry=%v after=%v err=%v", retry, after, err)
			}
		})
	}
}
