package request

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type parentTagLab struct {
	parent      map[string]any
	tags        []map[string]any
	writes      int
	fail        bool
	wrongParent bool
	onTagRead   func()
	commands    []map[string]any
	onArtistTag func()
	commandFail bool
	onCommands  func()
}

func newParentTagLab(t *testing.T, kind string) (*parentTagLab, string) {
	t.Helper()
	collection, key := "artist", "foreignArtistId"
	if kind == "book" {
		collection, key = "author", "foreignAuthorId"
	}
	l := &parentTagLab{parent: map[string]any{"id": 10, key: "parent-id", "tags": []int{7}, "ebookTags": []int{7}, "audiobookTags": []int{9}, "monitored": true, "path": "/media/parent", "ebookRootFolderPath": "/books", "audiobookRootFolderPath": "/audio", "ebookQualityProfileId": 1, "audiobookQualityProfileId": 2, "ebookMonitored": false, "audiobookMonitored": true, "ebookMetadataProfileId": 3, "audiobookMonitorNewItems": "new"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/command":
			if l.onCommands != nil {
				l.onCommands()
			}
			if l.commandFail {
				w.WriteHeader(503)
			} else if l.commands == nil {
				write([]any{})
			} else {
				write(l.commands)
			}
		case "GET /api/v1/album", "GET /api/v1/book":
			if kind == "music" {
				write([]any{map[string]any{"id": 1, "foreignAlbumId": "album-id", "artistId": 10}})
			} else {
				write([]any{map[string]any{"id": 1, "foreignBookId": "hc:242987", "authorId": 10, "mediaType": "ebook"}, map[string]any{"id": 2, "foreignBookId": "hc:242987", "authorId": 10, "mediaType": "audiobook"}})
			}
		case "GET /api/v1/book/1", "GET /api/v1/book/2":
			id, format := 1, "ebook"
			if strings.HasSuffix(r.URL.Path, "/2") {
				id, format = 2, "audiobook"
			}
			parent := 10
			if l.wrongParent {
				parent = 99
			}
			write(map[string]any{"id": id, "foreignBookId": "hc:242987", "authorId": parent, "mediaType": format})
		case "GET /api/v1/album/1":
			parent := 10
			if l.wrongParent {
				parent = 99
			}
			write(map[string]any{"id": 1, "foreignAlbumId": "album-id", "artistId": parent})
		case "GET /api/v1/" + collection + "/10":
			write(l.parent)
		case "GET /api/v1/tag":
			if l.onTagRead != nil {
				l.onTagRead()
			}
			if l.fail {
				w.WriteHeader(503)
				return
			}
			write(l.tags)
		case "POST /api/v1/tag":
			var tag map[string]any
			json.NewDecoder(r.Body).Decode(&tag)
			tag["id"] = 100 + len(l.tags)
			l.tags = append(l.tags, tag)
			l.writes++
			write(tag)
		case "PUT /api/v1/artist/editor":
			var body struct {
				IDs   []int  `json:"artistIds"`
				Tags  []int  `json:"tags"`
				Apply string `json:"applyTags"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if !reflect.DeepEqual(body.IDs, []int{10}) || body.Apply != "add" {
				t.Errorf("unsafe artist edit: %+v", body)
			}
			l.parent["tags"] = append(l.parent["tags"].([]int), body.Tags...)
			l.writes++
			if l.onArtistTag != nil {
				l.onArtistTag()
			}
			w.WriteHeader(202)
		case "PUT /api/v1/author/10":
			var body map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&body)
			for k, v := range body {
				if k == "ebookTags" || k == "audiobookTags" {
					var tags []int
					json.Unmarshal(v, &tags)
					l.parent[k] = tags
					continue
				}
				if !slices.Contains([]string{"id", "path", "monitored", "ebookRootFolderPath", "audiobookRootFolderPath", "ebookQualityProfileId", "audiobookQualityProfileId", "addOptions", "lastSelectedMediaType"}, k) {
					t.Errorf("unnecessary author field %s", k)
				}
				want, _ := json.Marshal(l.parent[k])
				if string(v) != string(want) {
					t.Errorf("author setting changed: %s", k)
				}
			}
			l.writes++
			w.WriteHeader(202)
		default:
			t.Errorf("unexpected native call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	return l, server.URL
}

func parentTagService(t *testing.T, kind, url string) (*Service, int64, string, *instance.Store) {
	t.Helper()
	var s *Service
	var uid int64
	var id string
	if kind == "book" {
		s, uid = newChaptarrBookTestService(t, url)
		s.db.QueryRow(`SELECT id FROM service_instances`).Scan(&id)
	} else {
		s, uid, id = newLidarrMusicTestService(t, url)
	}
	cipher, _ := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	store := instance.NewStore(s.db, cipher)
	setRequesterTagging(t, store, id, true)
	requireApproval(t, s)
	return s, uid, id, store
}

func parentRequest(t *testing.T, s *Service, uid int64, kind, id, format string) int64 {
	t.Helper()
	foreign := "album-id"
	if kind == "book" {
		foreign = "hc:242987"
	}
	out, err := s.CreateMediaRequest(uid, &CreateRequest{MediaType: kind, ForeignID: foreign, Title: "Selected", InstanceID: id, BookFormat: format})
	if err != nil {
		t.Fatal(err)
	}
	return out.RequestID
}

func completeParentFormat(t *testing.T, s *Service, id int64, format string) {
	t.Helper()
	record := 1
	if format == "audiobook" {
		record = 2
	}
	if _, err := s.db.Exec(`UPDATE request_dispatch SET state='complete',book_record_id=? WHERE request_id=? AND format=?`, record, id, format); err != nil {
		t.Fatal(err)
	}
}

func TestRequesterTagBookSharedFormatsAndRetry(t *testing.T) {
	l, url := newParentTagLab(t, "book")
	s, uid, id, store := parentTagService(t, "book", url)
	admin := createTestAdmin(t, s)
	result, _ := s.db.Exec(`INSERT INTO users(username,password_hash,role) VALUES('second','','user')`)
	second, _ := result.LastInsertId()
	if err := store.SetUserGrants(second, map[string][]string{"chaptarr": {id}}); err != nil {
		t.Fatal(err)
	}
	store.SetUserDefault(second, "chaptarr", id)
	rid := parentRequest(t, s, uid, "book", id, "both")
	if parentRequest(t, s, second, "book", id, "ebook") != rid {
		t.Fatal("book work not shared")
	}
	parentRequest(t, s, second, "book", id, "ebook")
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs`).Scan(&count)
	if count != 3 {
		t.Fatalf("want 3 subscriber/format jobs, got %d", count)
	}
	completeParentFormat(t, s, rid, "ebook")
	s.SweepRequesterTags(context.Background())
	s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE state='applied'`).Scan(&count)
	if count != 2 {
		t.Fatalf("want two ebook receipts, got %d", count)
	}
	if !reflect.DeepEqual(l.parent["audiobookTags"], []int{9}) || !reflect.DeepEqual(l.parent["ebookTags"], []int{7, 100, 101}) {
		t.Fatalf("wrong format tags %+v", l.parent)
	}
	tx, _ := s.db.Begin()
	status, err := loadRequesterTagStatus(context.Background(), tx, rid)
	tx.Rollback()
	if err != nil || status.Status != "partial" || len(status.Recipients) != 3 {
		t.Fatalf("history lost partial recipients: %+v %v", status, err)
	}
	completeParentFormat(t, s, rid, "audiobook")
	l.fail = true
	s.SweepRequesterTags(context.Background())
	s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE state='retrying'`).Scan(&count)
	if count != 1 {
		t.Fatal("audio tag failure not isolated")
	}
	l.fail = false
	beforeWrites := l.writes
	if w := retryTagRequest(s, admin, rid); w.Code != 202 {
		t.Fatalf("retry failed %d %s", w.Code, w.Body.String())
	}
	s = NewService(s.db, s.registry, nil, nil)
	s.SweepRequesterTags(context.Background())
	s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE state='applied'`).Scan(&count)
	if count != 3 || l.writes != beforeWrites+1 {
		t.Fatal("restart/retry recreated tags or lost receipts")
	}
	if !reflect.DeepEqual(l.parent["audiobookTags"], []int{9, 100}) {
		t.Fatalf("wrong audio requester: %v", l.parent["audiobookTags"])
	}
}

func TestRequesterTagParentGuardsAndIdentity(t *testing.T) {
	for _, kind := range []string{"book", "music"} {
		for _, failure := range []string{"none", "revoked", "rebound", "disabled"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				l, url := newParentTagLab(t, kind)
				s, uid, id, store := parentTagService(t, kind, url)
				format := ""
				if kind == "book" {
					format = "ebook"
				}
				rid := parentRequest(t, s, uid, kind, id, format)
				completeParentFormat(t, s, rid, format)
				l.onTagRead = func() {
					switch failure {
					case "revoked":
						s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=?`, uid)
					case "rebound":
						l.wrongParent = true
					case "disabled":
						setRequesterTagging(t, store, id, false)
					}
				}
				s.SweepRequesterTags(context.Background())
				if failure == "none" {
					assertTagState(t, s, rid, "applied")
					if l.writes != 2 {
						t.Fatal("tag was not created and applied")
					}
				} else {
					if l.writes != 0 {
						t.Fatalf("%s allowed %d writes", failure, l.writes)
					}
					var state string
					s.db.QueryRow(`SELECT state FROM request_tag_jobs`).Scan(&state)
					if state != "failed" && state != "cancelled" {
						t.Fatalf("unexpected state %s", state)
					}
				}
			})
		}
	}
}

func TestRequesterTagBookAdmissionNeverBackfillsAndOwnerTransfer(t *testing.T) {
	for _, mode := range []string{"old", "cancel", "rejoin"} {
		t.Run(mode, func(t *testing.T) {
			l, url := newParentTagLab(t, "book")
			s, uid, id, store := parentTagService(t, "book", url)
			if mode == "old" {
				setRequesterTagging(t, store, id, false)
			}
			rid := parentRequest(t, s, uid, "book", id, "ebook")
			setRequesterTagging(t, store, id, true)
			parentRequest(t, s, uid, "book", id, "ebook")
			result, _ := s.db.Exec(`INSERT INTO users(username,password_hash,role) VALUES('second','','user')`)
			second, _ := result.LastInsertId()
			if err := store.SetUserGrants(second, map[string][]string{"chaptarr": {id}}); err != nil {
				t.Fatal(err)
			}
			store.SetUserDefault(second, "chaptarr", id)
			parentRequest(t, s, second, "book", id, "ebook")
			if mode == "cancel" || mode == "rejoin" {
				r, _, _ := s.loadRequest(rid)
				if _, err := s.cancelDelivery(uid, rid, r, false); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "rejoin" {
				s.SweepRequesterTags(context.Background())
				parentRequest(t, s, uid, "book", id, "ebook")
			}
			completeParentFormat(t, s, rid, "ebook")
			s.SweepRequesterTags(context.Background())
			if mode == "rejoin" {
				if len(l.tags) != 2 {
					t.Fatalf("new subscription did not tag both requesters: %+v", l.tags)
				}
				return
			}
			if len(l.tags) != 1 || l.tags[0]["label"] != fmt.Sprintf("cantinarr-%d-second", second) {
				t.Fatalf("misattributed native tags: %+v", l.tags)
			}
			var count int
			s.db.QueryRow(`SELECT COUNT(*) FROM request_tag_jobs WHERE state='applied'`).Scan(&count)
			if count != 1 {
				t.Fatal("unexpected applied audience")
			}
		})
	}
}

func TestRequesterTagMusicWaitsForNativeRefresh(t *testing.T) {
	for _, scenario := range []string{"queued", "started", "global", "bulk", "other-artist", "completed", "unreadable", "before-create", "during-write"} {
		t.Run(scenario, func(t *testing.T) {
			l, url := newParentTagLab(t, "music")
			s, uid, id, store := parentTagService(t, "music", url)
			rid := parentRequest(t, s, uid, "music", id, "")
			completeParentFormat(t, s, rid, "")
			command := map[string]any{"id": 1, "name": "RefreshArtist", "status": "started", "body": map[string]any{"artistIds": []int{10}}}
			l.commands = []map[string]any{command}
			switch scenario {
			case "queued", "completed":
				command["status"] = scenario
			case "global":
				command["body"] = map[string]any{}
			case "bulk":
				command["name"] = "BulkRefreshArtist"
			case "other-artist":
				command["body"] = map[string]any{"artistIds": []int{99}}
			case "unreadable":
				l.commandFail = true
			case "before-create":
				l.commands = nil
				l.onTagRead = func() { l.commands = []map[string]any{command} }
			case "during-write":
				l.commands = nil
				command["status"] = "completed"
				l.onArtistTag = func() { l.commands = []map[string]any{command} }
			}
			s.SweepRequesterTags(context.Background())
			if scenario == "other-artist" || scenario == "completed" {
				assertTagState(t, s, rid, "applied")
				return
			}
			assertTagState(t, s, rid, "retrying")
			if scenario != "during-write" && l.writes != 0 {
				t.Fatal("wrote tags while native refresh was pending or unverified")
			}
			// The native refresh can finish with the artist's original tags.
			// Recover after a Cantinarr service restart, with a second requester.
			l.parent["tags"] = []int{7}
			command["status"] = "completed"
			l.commandFail, l.onArtistTag, l.onTagRead = false, nil, nil
			result, err := s.db.Exec(`INSERT INTO users(username,password_hash,role) VALUES('second','','user')`)
			if err != nil {
				t.Fatal(err)
			}
			second, _ := result.LastInsertId()
			if err := store.SetUserGrants(second, map[string][]string{"lidarr": {id}}); err != nil {
				t.Fatal(err)
			}
			store.SetUserDefault(second, "lidarr", id)
			secondID := parentRequest(t, s, second, "music", id, "")
			completeParentFormat(t, s, secondID, "")
			s.db.Exec(`UPDATE request_tag_jobs SET next_attempt_at=0`)
			s = NewService(s.db, s.registry, nil, nil)
			s.SweepRequesterTags(context.Background())
			assertTagState(t, s, rid, "applied")
			assertTagState(t, s, secondID, "applied")
			if !reflect.DeepEqual(l.parent["tags"], []int{7, 100, 101}) || len(l.tags) != 2 {
				t.Fatalf("lost existing or multiple requester tags: %+v", l.parent["tags"])
			}
		})
	}
}

func TestRequesterTagMusicRechecksGrantAfterRefreshRead(t *testing.T) {
	l, url := newParentTagLab(t, "music")
	s, uid, id, _ := parentTagService(t, "music", url)
	rid := parentRequest(t, s, uid, "music", id, "")
	completeParentFormat(t, s, rid, "")
	reads := 0
	l.onCommands = func() {
		reads++
		if reads == 2 {
			s.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=?`, uid)
		}
	}
	s.SweepRequesterTags(context.Background())
	assertTagState(t, s, rid, "failed")
	if l.writes != 0 {
		t.Fatal("tag write outlived requester's library grant")
	}
}
