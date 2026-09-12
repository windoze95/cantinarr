package mediaaccess

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/appletv"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

func TestAppleTVTitleRequiresCanonicalMetadataAndCurrentRecipientAccess(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			user := e.user("viewer")
			server := e.jellyfin("Library", instance.MediaServerConfig{PublicAddress: "https://watch.example.com"})
			e.grant(user, server)
			if _, err := e.svc.insertAccount(accountRow{UserID: user, InstanceID: server, RemoteUserID: "viewer"}, false); err != nil {
				t.Fatal(err)
			}
			finder := &finderProvider{fakeProvider: newFakeProvider()}
			finder.find = func(remoteID string, query mediaserver.ItemQuery) (mediaserver.Item, error) {
				if remoteID != "viewer" || query.MediaType != kind || query.TMDBID != 12 || query.Year != 2020 || query.Title != "Canonical title" {
					t.Errorf("wrong identity or untrusted lookup hints: %+v", query)
				}
				if kind == "tv" && query.TVDBID != 34 {
					t.Error("canonical TVDB identity missing")
				}
				return mediaserver.Item{ID: "copy", WebPath: "/web/#/details?id=copy"}, nil
			}
			e.providers[server] = finder
			h := NewHandler(e.svc, nil)
			getter := watchMetadataGetter(func(path string, params url.Values) ([]byte, error) {
				if path != "/"+kind+"/12" || params.Get("append_to_response") != "external_ids" {
					t.Fatalf("metadata request %s %v", path, params)
				}
				return []byte(`{"id":12,"title":"Canonical title","name":"Canonical title","release_date":"2020-01-01","first_air_date":"2020-01-01","external_ids":{"tvdb_id":34}}`), nil
			})
			h.SetWatchContentPolicy(nil, func() contentpolicy.RawGetter { return getter })
			if err := h.AuthorizeAppleTVTitle(context.Background(), user, kind, 12); err != nil {
				t.Fatal(err)
			}
			if _, err := e.db.Exec(`DELETE FROM user_instance_grants WHERE user_id=?`, user); err != nil {
				t.Fatal(err)
			}
			if err := h.AuthorizeAppleTVTitle(context.Background(), user, kind, 12); !errors.Is(err, appletv.ErrTitleUnavailable) {
				t.Fatalf("revoked media grant allowed TV title: %v", err)
			}
		})
	}
}

func TestAppleTVTitleLookupDistinguishesUnreadableFromAbsent(t *testing.T) {
	e := newEnv(t)
	user := e.user("viewer")
	server := e.jellyfin("Library", instance.MediaServerConfig{PublicAddress: "https://watch.example.com"})
	e.grant(user, server)
	if _, err := e.svc.insertAccount(accountRow{UserID: user, InstanceID: server, RemoteUserID: "viewer"}, false); err != nil {
		t.Fatal(err)
	}
	finder := &finderProvider{fakeProvider: newFakeProvider()}
	e.providers[server] = finder
	h := NewHandler(e.svc, nil)
	for _, tc := range []struct {
		raw                string
		findErr, errorWant error
	}{
		{`{"id":12}`, mediaserver.ErrItemNotFound, appletv.ErrTitleUnavailable},
		{`{"id":12}`, mediaserver.ErrItemUnverified, appletv.ErrLookupUnavailable},
		{`{"id":12}`, errors.New("private-host"), appletv.ErrLookupUnavailable},
		{`{"id":99}`, nil, appletv.ErrLookupUnavailable},
		{`not json`, nil, appletv.ErrLookupUnavailable},
	} {
		h.SetWatchContentPolicy(nil, func() contentpolicy.RawGetter {
			return watchMetadataGetter(func(string, url.Values) ([]byte, error) { return []byte(tc.raw), nil })
		})
		finder.find = func(string, mediaserver.ItemQuery) (mediaserver.Item, error) { return mediaserver.Item{}, tc.findErr }
		if err := h.AuthorizeAppleTVTitle(context.Background(), user, "tv", 12); !errors.Is(err, tc.errorWant) {
			t.Fatalf("lookup %s: %v want %v", tc.raw, err, tc.errorWant)
		}
	}
	h.SetWatchContentPolicy(nil, func() contentpolicy.RawGetter { return nil })
	if err := h.AuthorizeAppleTVTitle(context.Background(), user, "tv", 12); !errors.Is(err, appletv.ErrLookupUnavailable) {
		t.Fatal(err)
	}
}
