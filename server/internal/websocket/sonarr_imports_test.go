package websocket

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/trakt"
)

type importMetadata struct{ client *tmdb.Client }

func (m importMetadata) TMDB() *tmdb.Client   { return m.client }
func (m importMetadata) Trakt() *trakt.Client { return nil }

func newTVImportLab(t *testing.T) (*Hub, *videoBackend, *sonarr.Client, *recordingContent, *sql.DB, *request.Service) {
	t.Helper()
	database := witnessDB(t)
	seasons := []sonarr.SeasonResource{}
	episodes := []sonarr.Episode{}
	for n := 1; n <= 16; n++ {
		seasons = append(seasons, sonarr.SeasonResource{SeasonNumber: n})
		episodes = append(episodes, sonarr.Episode{ID: n * 10, SeriesID: 6, SeasonNumber: n, EpisodeNumber: 1, HasFile: true})
	}
	parent := sonarr.Series{ID: 6, TmdbID: 113988, TvdbID: 389492, Title: "Monster parent", Seasons: seasons}
	parentJSON, _ := json.Marshal(parent)
	episodeJSON, _ := json.Marshal(episodes)
	b := &videoBackend{apiPrefix: "/api/v3", queue: `[{"id":1,"seriesId":6,"status":"downloading","size":100,"sizeleft":50}]`,
		series: map[int]string{6: string(parentJSON)}, episodes: string(episodeJSON)}
	base := b.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/tv/"))
			fmt.Fprintf(w, `{"id":%d,"name":"Story %d","seasons":[{"season_number":1}]}`, id, id)
		case r.URL.Path == "/api/v3/series/lookup":
			_ = json.NewEncoder(w).Encode([]sonarr.Series{parent})
		default:
			base(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	svc := request.NewService(database, nil, tmdb.NewBridge(importMetadata{tmdb.NewClientWithBaseURL("fixture", srv.URL)}, database), nil)
	content := &recordingContent{}
	h := NewHub(nil, nil, nil, database, content, nil)
	h.SetTVImportResolver(svc)
	return h, b, sonarr.NewClient(srv.URL, "fixture"), content, database, svc
}

func tvReceipt(season int, when time.Time) string {
	return fmt.Sprintf(`{"id":%d,"seriesId":6,"episodeId":%d,"eventType":"downloadFolderImported","date":%q}`, season, season*10, when.UTC().Format(time.RFC3339Nano))
}

func TestCorrectedTVPollUsesActualImportsWhileParentKeepsDownloading(t *testing.T) {
	h, b, client, content, _, _ := newTVImportLab(t)
	h.pollSonarrInstance("tv-a", client)
	// One episode of S2 lands while S3 still downloads on the same parent.
	b.set(&b.history, "["+tvReceipt(2, time.Now())+"]")
	h.pollSonarrInstance("tv-a", client)
	if got := content.episodeCalls(); !reflect.DeepEqual(got, []string{"Story 225634|225634"}) {
		t.Fatalf("concurrent/partial import = %v", got)
	}
	// Another library gets its own witness and identical corrected destination.
	h.pollSonarrInstance("tv-b", client)
	b.set(&b.history, "["+tvReceipt(3, time.Now())+"]")
	h.pollSonarrInstance("tv-b", client)
	if got := content.episodeCalls(); len(got) != 2 || got[1] != "Story 286801|286801" {
		t.Fatalf("second library = %v", got)
	}
	// Removing the remaining download proves no new import, despite old files.
	b.set(&b.queue, `[]`)
	b.set(&b.history, `[]`)
	h.pollSonarrInstance("tv-a", client)
	if got := content.episodeCalls(); len(got) != 2 {
		t.Fatalf("removed download announced: %v", got)
	}
}

func TestCorrectedTVPollGroupsPacksAndUpgradesByStory(t *testing.T) {
	h, b, client, content, _, _ := newTVImportLab(t)
	h.pollSonarrInstance("tv-a", client)
	when := time.Now()
	// Two S2 imports, only one delete: mixed/new. S3's sole import is an
	// upgrade. S1 and S4 remain independent new stories on this same parent.
	receipts := []string{tvReceipt(1, when), tvReceipt(2, when), tvReceipt(2, when), tvReceipt(3, when), tvReceipt(4, when)}
	b.set(&b.history, "["+strings.Join(receipts, ",")+"]")
	b.set(&b.deleteHistory, fmt.Sprintf(`[{"episodeId":20,"eventType":"episodeFileDeleted","date":%q,"data":{"reason":"Upgrade"}},{"episodeId":30,"eventType":"episodeFileDeleted","date":%q,"data":{"reason":"Upgrade"}}]`, when.Format(time.RFC3339Nano), when.Format(time.RFC3339Nano)))
	h.pollSonarrInstance("tv-a", client)
	if got := content.episodeCalls(); !reflect.DeepEqual(got, []string{"Story 113988|113988", "Story 225634|225634", "Story 299939|299939"}) {
		t.Fatalf("new stories = %v", got)
	}
	if got := content.upgradedEpisodeCalls(); !reflect.DeepEqual(got, []string{"Story 286801|286801"}) {
		t.Fatalf("upgraded stories = %v", got)
	}
}

func TestCorrectedTVImportRecoveryGuards(t *testing.T) {
	for _, mode := range []string{"restart", "first boot", "legacy upgrade", "legacy upgrade new import", "stale", "history overflow", "unverifiable episode", "title cap", "gateway hold"} {
		t.Run(mode, func(t *testing.T) {
			h, b, client, content, database, svc := newTVImportLab(t)
			if mode != "first boot" {
				h.pollSonarrInstance("tv-a", client)
			}
			age := 30 * time.Minute
			if mode == "stale" {
				age = 7 * time.Hour
			}
			if _, err := database.Exec(`UPDATE arr_queue_witness SET observed_at=?`, time.Now().Add(-age).UTC()); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "legacy upgrade") {
				if _, err := database.Exec(`UPDATE arr_queue_witness SET media_ids='[6]'`); err != nil {
					t.Fatal(err)
				}
			}
			b.set(&b.queue, `[]`)
			receipts := []string{tvReceipt(2, time.Now().Add(-time.Minute))}
			if mode == "title cap" {
				receipts = nil
				for n := 5; n <= 16; n++ {
					if _, err := database.Exec(`INSERT INTO tv_match_overrides(tmdb_id,mode,tvdb_id,season_map,revision) VALUES(?,'custom',389492,?,1)`, 1000+n, fmt.Sprintf(`{"1":%d}`, n)); err != nil {
						t.Fatal(err)
					}
					receipts = append(receipts, tvReceipt(n, time.Now().Add(-time.Minute)))
				}
			}
			b.set(&b.history, "["+strings.Join(receipts, ",")+"]")
			if mode == "history overflow" {
				b.historyTotal = 201
			}
			if mode == "unverifiable episode" {
				b.set(&b.episodes, `[]`)
			}
			h = NewHub(nil, nil, nil, database, content, nil)
			h.SetTVImportResolver(svc)
			var gate *gatedTVContent
			if mode == "gateway hold" {
				gate = &gatedTVContent{recordingContent: content}
				h.content = gate
			}
			h.restoreQueueWitness()
			if mode == "legacy upgrade new import" {
				b.set(&b.history, "["+tvReceipt(2, time.Now().Add(-time.Minute))+","+tvReceipt(3, time.Now())+"]")
			}
			h.pollSonarrInstance("tv-a", client)
			want := 0
			if mode == "restart" || mode == "legacy upgrade new import" {
				want = 1
			}
			if got := content.episodeCalls(); len(got) != want {
				t.Fatalf("%s alerts = %v", mode, got)
			}
			if mode == "legacy upgrade new import" && content.episodeCalls()[0] != "Story 286801|286801" {
				t.Fatal("legacy snapshot suppressed a new import or replayed the old story")
			}
			if gate != nil {
				gate.ready = true
				h.pollSonarrInstance("tv-a", client)
				if len(content.episodeCalls()) != 1 {
					t.Fatal("held import was lost")
				}
			}
			h = NewHub(nil, nil, nil, database, content, nil)
			h.SetTVImportResolver(svc)
			h.restoreQueueWitness()
			before := len(content.episodeCalls())
			h.pollSonarrInstance("tv-a", client)
			if len(content.episodeCalls()) != before {
				t.Fatal("replayed historical alert")
			}
		})
	}
}

type gatedTVContent struct {
	*recordingContent
	ready bool
}

func (g *gatedTVContent) ContentReady() bool { return g.ready }
