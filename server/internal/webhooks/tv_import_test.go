package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/push"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/trakt"
)

type tvImportMetadata struct{ client *tmdb.Client }

func (m tvImportMetadata) TMDB() *tmdb.Client   { return m.client }
func (m tvImportMetadata) Trakt() *trakt.Client { return nil }

func TestTVWebhookCorrectedIdentityAudienceAndOverlap(t *testing.T) {
	parent := &sonarr.Series{ID: 42, TmdbID: 113988, TvdbID: 389492, Title: "Monster parent",
		Seasons: []sonarr.SeasonResource{{SeasonNumber: 1}, {SeasonNumber: 2}, {SeasonNumber: 3}, {SeasonNumber: 4}}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v3/series/42":
			_ = json.NewEncoder(w).Encode(parent)
		case r.URL.Path == "/api/v3/series/lookup":
			_ = json.NewEncoder(w).Encode([]*sonarr.Series{parent})
		case r.URL.Path == "/api/v3/episode":
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/content_ratings"):
			// Parent would pass the child's cap. Only the corrected story is MA.
			rating := "TV-MA"
			if strings.Contains(r.URL.Path, "/113988/") {
				rating = "TV-G"
			}
			fmt.Fprintf(w, `{"results":[{"iso_3166_1":"US","rating":%q}]}`, rating)
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			var id int
			_, _ = fmt.Sscanf(r.URL.Path, "/tv/%d", &id)
			fmt.Fprintf(w, `{"id":%d,"name":"Corrected story %d","seasons":[{"season_number":1}],"genres":[]}`, id, id)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	f := newFixture(t, upstream.URL, upstream.URL)
	metadata := tmdb.NewClientWithBaseURL("fixture", upstream.URL)
	svc := request.NewService(f.database, f.handler.registry, tmdb.NewBridge(tvImportMetadata{metadata}, f.database), nil)
	f.handler.tvImports = svc
	other := &instance.Instance{ServiceType: "sonarr", Name: "Other TV", URL: upstream.URL, APIKey: "fixture"}
	if err := f.store.Create(other); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE service_instances SET is_default=1 WHERE id=?`, f.sonarrID)
	exec(`INSERT INTO users(id,username,password_hash,role) VALUES(1,'admin','','admin'),(2,'reader','','user'),(3,'other-reader','','user'),(4,'child','','user')`)
	exec(`INSERT INTO notification_prefs(user_id,content_upgraded) VALUES(1,1),(2,1),(3,1),(4,1)`)
	for _, id := range []int{2, 4} {
		exec(`INSERT INTO user_instance_grants(user_id,instance_id) VALUES(?,?)`, id, f.sonarrID)
	}
	exec(`INSERT INTO user_default_instances(user_id,service_type,instance_id) VALUES(3,'sonarr',?)`, other.ID)
	exec(`INSERT INTO user_content_policies(user_id,max_movie_rating,max_tv_rating) VALUES(4,'PG','TV-PG')`)
	sends := make(chan map[string]any, 10)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/notifications" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sends <- body
		}
		fmt.Fprint(w, `{"sent":1,"failed":0}`)
	}))
	defer gateway.Close()
	mgr := push.NewManager(f.database, nil, gateway.URL, "fixture", "", "Test", nil)
	if mgr.Ensure(context.Background()) == nil {
		t.Fatal("gateway did not enroll")
	}
	notifier := push.NewNotifier(f.database, mgr, nil)
	notifier.SetContentPolicy(contentpolicy.New(f.database, func() contentpolicy.RawGetter { return metadata }, nil))
	f.handler.content = notifier
	post := func(library string, season int, upgrade bool) {
		t.Helper()
		token, err := f.store.WebhookToken(library)
		if err != nil {
			t.Fatal(err)
		}
		payload := fmt.Sprintf(`{"eventType":"Download","isUpgrade":%t,"series":{"id":42,"tvdbId":389492,"tmdbId":113988,"title":"Monster parent"},"episodes":[{"id":%d,"seasonNumber":%d,"episodeNumber":1}]}`, upgrade, season*10, season)
		if response := f.post(t, "/api/webhooks/arr/"+library, payload, basicWebhookAuth(token)); response.Code != 200 {
			t.Fatal(response.Code)
		}
	}
	check := func(tmdbID int, library, category string, users string) {
		t.Helper()
		select {
		case body := <-sends:
			data := body["data"].(map[string]any)
			if data["tmdb_id"] != float64(tmdbID) || data["instance_id"] != library || data["type"] != category {
				t.Fatalf("destination = %+v", data)
			}
			text := body["notification"].(map[string]any)["body"].(string)
			if !strings.Contains(text, fmt.Sprintf("Corrected story %d", tmdbID)) {
				t.Fatalf("wrong story copy: %s", text)
			}
			ids := []string{}
			for _, id := range body["to"].(map[string]any)["user_ids"].([]any) {
				ids = append(ids, fmt.Sprint(id))
			}
			sort.Strings(ids)
			if strings.Join(ids, ",") != users {
				t.Fatalf("recipients = %v, want %s", ids, users)
			}
			collapse := body["options"].(map[string]any)["collapse_id"].(string)
			if !strings.Contains(collapse, library) {
				t.Fatalf("collapse omitted library: %s", collapse)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no notification")
		}
	}
	post(f.sonarrID, 2, false)
	check(225634, f.sonarrID, "new_episode", "1,2")
	post(f.sonarrID, 2, false) // per-file/duplicate webhook
	// The poller resolves identical import evidence through the same service.
	client, _ := f.handler.registry.GetSonarrClient(f.sonarrID)
	titles, err := svc.ResolveTVImports(client, parent, []sonarr.ImportedEpisode{{SeasonNumber: 2}})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range titles {
		notifier.NotifyNewEpisode(title.Title, title.TmdbID, f.sonarrID)
	}
	post(f.sonarrID, 3, true)
	check(286801, f.sonarrID, "content_upgraded", "1")
	// An upgrade's silent broadcast claim absorbs a later poll witness.
	notifier.NotifyNewEpisode("Corrected story 286801", 286801, f.sonarrID)
	post(other.ID, 2, false)
	check(225634, other.ID, "new_episode", "1,3")
	select {
	case body := <-sends:
		t.Fatalf("duplicate notification: %+v", body)
	case <-time.After(50 * time.Millisecond):
	}
	var count int
	if err := f.database.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("imports depended on request records: %d, %v", count, err)
	}
	if len(f.requests.instanceIDs) != 4 {
		t.Fatalf("library refresh stopped: %v", f.requests.instanceIDs)
	}
}

func TestTVWebhookMissingSeasonNeverFallsBackToParent(t *testing.T) {
	f := newFixture(t, "http://127.0.0.1:1", "http://127.0.0.1:1")
	f.handler.tvImports = request.NewService(f.database, f.handler.registry, nil, nil)
	for _, upgrade := range []bool{false, true} {
		payload := fmt.Sprintf(`{"eventType":"Download","isUpgrade":%t,"series":{"id":42,"tvdbId":389492,"tmdbId":113988,"title":"Monster parent"}}`, upgrade)
		if res := f.post(t, "/api/webhooks/arr/"+f.sonarrID, payload, basicWebhookAuth(f.sonarrTok)); res.Code != 200 {
			t.Fatal(res.Code)
		}
	}
	if len(f.content.episodes)+len(f.content.upgradedEpisodes) != 0 {
		t.Fatal("missing seasons announced parent")
	}
	if len(f.requests.instanceIDs) != 2 || len(f.hub.events) == 0 {
		t.Fatal("rejected notification stopped library refresh")
	}
}
