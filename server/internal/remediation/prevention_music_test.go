package remediation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/arr"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// A recurring music problem is a recurring problem: the notice names the
// Lidarr library like any other (never its id, never its host) and quotes the
// live setting it is about. Both halves of that came from service lists that
// once stopped at Chaptarr, so a Lidarr instance read as "unknown" and fell
// back to the raw id with no live block at all.
func TestPreventionNoticeCoversALidarrInstance(t *testing.T) {
	const lidarrID = "lidarr-test"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/indexer" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name": "Orpheus", "protocol": "torrent", "enableRss": true, "priority": 25,
			"fields": []map[string]any{
				{"name": "apiKey", "value": "SECRET-XYZ"},
				{"name": "minimumSeeders", "value": 0},
			},
		}})
	}))
	t.Cleanup(fake.Close)

	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	store := instance.NewStore(database, cipher)
	if err := store.Create(&instance.Instance{ID: lidarrID, ServiceType: "lidarr", Name: "Music", URL: fake.URL, APIKey: "key"}); err != nil {
		t.Fatalf("create lidarr instance: %v", err)
	}
	svc := NewService(database, instance.NewRegistry(store), nil, &fakeNotifier{})
	seedPattern(t, svc, lidarrID, arr.ProblemDownloadStalled, "music", preventionBase, 3, 2, 3)

	sweepPreventionAt(t, svc, preventionBase)

	issue := soleNoticeIssue(t, svc)
	if want := fmt.Sprintf("%q keeps happening on Music", arr.ProblemDownloadStalled); issue.Title != want {
		t.Fatalf("title = %q, want %q", issue.Title, want)
	}
	if !strings.Contains(issue.Detail, "min seeders 0") || !strings.Contains(issue.Detail, "Orpheus") {
		t.Fatalf("notice does not quote the live Lidarr indexer:\n%s", issue.Detail)
	}
	if strings.Contains(issue.Title+issue.Detail+issue.Resolution, "SECRET-XYZ") ||
		strings.Contains(issue.Title+issue.Detail+issue.Resolution, fake.URL) {
		t.Fatalf("a secret or the instance host reached the notice:\n%s", issue.Detail)
	}
}
