package mediaaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

type bookFinderProvider struct {
	*fakeProvider
	find func(string, mediaserver.BookQuery) ([]mediaserver.BookItem, error)
}

func (p *bookFinderProvider) FindBooks(_ context.Context, id string, q mediaserver.BookQuery) ([]mediaserver.BookItem, error) {
	return p.find(id, q)
}

func TestListenLinksEligibilityAndRevalidation(t *testing.T) {
	for _, scenario := range []string{"found", "unlinked", "no metadata", "offline", "ungranted", "revoke", "unlink", "repoint", "rotate key", "public address changed", "not available"} {
		t.Run(scenario, func(t *testing.T) {
			e := newEnv(t)
			uid := e.user("reader")
			id := e.mediaServer("audiobookshelf", "Books", instance.MediaServerConfig{PublicAddress: "https://books.example/base"})
			if scenario != "ungranted" {
				e.grantType(uid, "audiobookshelf", id)
			}
			if scenario != "unlinked" {
				if _, err := e.svc.insertAccount(accountRow{UserID: uid, InstanceID: id, RemoteUserID: "reader-1", RemoteUsername: "reader"}, false); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			e.providers[id] = &bookFinderProvider{fakeProvider: newFakeProvider(), find: func(remote string, q mediaserver.BookQuery) ([]mediaserver.BookItem, error) {
				calls++
				if remote != "reader-1" {
					t.Errorf("wrong linked account %s", remote)
				}
				switch scenario {
				case "no metadata":
					return nil, mediaserver.ErrItemUnverified
				case "offline":
					return nil, errors.New("offline")
				case "revoke":
					e.grantType(uid, "audiobookshelf")
				case "unlink":
					_, _ = e.svc.deleteAccount(uid, id)
				case "repoint", "rotate key", "public address changed":
					inst, err := e.store.Get(id)
					if err != nil {
						t.Error(err)
						return nil, err
					}
					if scenario == "repoint" {
						inst.URL = "http://different.internal"
					}
					if scenario == "rotate key" {
						inst.APIKey = "changed-secret"
					}
					if scenario == "public address changed" {
						inst.MediaServerConfig.PublicAddress = "https://different.example"
					}
					if err := e.store.Update(inst); err != nil {
						t.Error(err)
					}
				}
				return []mediaserver.BookItem{{ID: "copy-1", Title: "Book", LibraryName: "Main"}, {ID: "copy-2", Title: "Book", LibraryName: "Other"}}, nil
			}}
			links, err := e.svc.ListenLinks(context.Background(), uid, mediaserver.BookQuery{Available: scenario != "not available", ASINs: []string{"B012345678"}})
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "ungranted", "revoke", "unlink", "repoint", "rotate key", "public address changed", "not available":
				if len(links) != 0 {
					t.Fatalf("ineligible links: %+v", links)
				}
			case "found":
				if len(links) != 1 || links[0].State != WatchFound || len(links[0].Items) != 2 || links[0].Items[0].URL != "https://books.example/base/item/copy-1" || links[0].FallbackURL != "" {
					t.Fatalf("links: %+v", links)
				}
			default:
				if len(links) != 1 || len(links[0].Items) != 0 || links[0].FallbackURL != "https://books.example/base" {
					t.Fatalf("fallback: %+v", links)
				}
				want := WatchUnverified
				if scenario == "offline" {
					want = WatchUnreachable
				}
				if links[0].State != want {
					t.Fatalf("state=%s want %s", links[0].State, want)
				}
			}
			if (scenario == "ungranted" || scenario == "unlinked" || scenario == "not available") && calls != 0 {
				t.Fatalf("ineligible upstream call: %d", calls)
			}
			data, _ := json.Marshal(links)
			if strings.Contains(string(data), "internal") || strings.Contains(string(data), testInstanceKey) {
				t.Fatal("private connection data exposed")
			}
		})
	}
}

type listeningSource struct {
	denied         bool
	changeRevision bool
	revisionReads  int
	calls          int
}

func (s *listeningSource) ListeningLibraryRevision(int64, string) (instance.ArrSettingsFingerprint, error) {
	var r instance.ArrSettingsFingerprint
	s.revisionReads++
	if s.denied {
		return r, mediaserver.ErrBookAccess
	}
	if s.changeRevision && s.revisionReads > 1 {
		r[0] = 1
	}
	return r, nil
}
func (s *listeningSource) ResolveListeningBook(context.Context, int64, string, string) (mediaserver.BookQuery, error) {
	s.calls++
	return mediaserver.BookQuery{Available: true}, nil
}

func TestListenHandlerRequiresLibraryAuthorization(t *testing.T) {
	e := newEnv(t)
	uid := e.user("reader")
	h := NewHandler(e.svc, nil)
	source := &listeningSource{}
	h.SetListeningBooks(source)
	for _, tt := range []struct {
		query  string
		userID int64
		want   int
	}{
		{"?instance_id=books&foreign_book_id=hc:1", 0, 401},
		{"?foreign_book_id=hc:1", uid, 400},
		{"?instance_id=books", uid, 400},
		{"?instance_id=books&foreign_book_id=hc:1", uid, 200},
	} {
		rec := serve(http.HandlerFunc(h.Listen), "GET", "/api/media-servers/listen"+tt.query, tt.userID, "")
		if rec.Code != tt.want {
			t.Fatalf("%s: %d %s", tt.query, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("listen response cached")
		}
	}
	source.denied = true
	before := source.calls
	if r := serve(http.HandlerFunc(h.Listen), "GET", "/api/media-servers/listen?instance_id=secret&foreign_book_id=hc:1", uid, ""); r.Code != 403 || source.calls != before {
		t.Fatalf("unauthorized source resolved: %d", r.Code)
	}
	source.denied = false
	source.changeRevision = true
	source.revisionReads = 0
	if r := serve(http.HandlerFunc(h.Listen), "GET", "/api/media-servers/listen?instance_id=books&foreign_book_id=hc:1", uid, ""); r.Code != 503 {
		t.Fatalf("repoint survived: %d", r.Code)
	}
}
