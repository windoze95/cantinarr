package mediaaccess

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// This fixture composes real account lifecycle operations with title lookup.
// Provider HTTP tests separately prove that lookup checks remote permissions.
type lifecycleFinder struct {
	mediaserver.Provider
	calls int
}

func (p *lifecycleFinder) Kind() mediaserver.Kind { return mediaserver.KindOf(p.Provider) }

func (p *lifecycleFinder) FindItem(ctx context.Context, id string, _ mediaserver.ItemQuery) (mediaserver.Item, error) {
	p.calls++
	u, err := p.GetUser(ctx, id)
	if errors.Is(err, mediaserver.ErrUserNotFound) || (err == nil && (u.IsDisabled || u.Pending)) {
		return mediaserver.Item{}, mediaserver.ErrItemUnverified
	}
	if err != nil {
		return mediaserver.Item{}, err
	}
	return mediaserver.Item{ID: "item", WebPath: "/item/item"}, nil
}

func (p *lifecycleFinder) FindBooks(ctx context.Context, id string, _ mediaserver.BookQuery) ([]mediaserver.BookItem, error) {
	item, err := p.FindItem(ctx, id, mediaserver.ItemQuery{})
	if err != nil {
		return nil, err
	}
	return []mediaserver.BookItem{{ID: item.ID, Title: "Book"}}, nil
}

func TestTitleLinksFollowLiveAccessAfterManagementStops(t *testing.T) {
	for _, kind := range []string{"audiobookshelf", "jellyfin", "emby", "plex"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			uid := e.user("reader")
			id := e.mediaServer(kind, "Library", instance.MediaServerConfig{PublicAddress: "https://media.example"})
			remote := e.provider.addUser("reader", false, false)
			p := &lifecycleFinder{Provider: e.provider}
			invite := newFakeInviteProvider()
			if kind == "plex" {
				remote = "reader@example.com"
				invite.share(remote, false)
				p.Provider = invite
			}
			e.providers[id] = p
			if _, err := e.svc.LinkAccount(ctx, uid, id, remote, true); err != nil {
				t.Fatal(err)
			}
			found := func() bool {
				t.Helper()
				if kind == "audiobookshelf" {
					links, err := e.svc.ListenLinks(ctx, uid, mediaserver.BookQuery{Available: true, ASINs: []string{"B012345678"}})
					if err != nil {
						t.Fatal(err)
					}
					return len(links) == 1 && links[0].State == WatchFound && len(links[0].Items) == 1
				}
				links, err := e.svc.WatchLinks(ctx, uid, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008})
				if err != nil {
					t.Fatal(err)
				}
				return len(links) == 1 && links[0].State == WatchFound && links[0].URL != ""
			}
			if !found() {
				t.Fatal("baseline exact link missing")
			}
			e.grantType(uid, kind)
			e.svc.OnGrantsChanged([]int64{uid})
			if !e.row(uid, id).DisabledAt.Valid {
				t.Fatal("revoke did not record disable")
			}
			before := p.calls
			if found() || p.calls != before {
				t.Fatal("revoked grant reached lookup")
			}
			if _, err := e.svc.SetManagement(ctx, uid, id, false); err != nil {
				t.Fatal(err)
			}
			e.grantType(uid, kind, id)
			e.svc.OnGrantsChanged([]int64{uid})
			if found() {
				t.Fatal("regrant restored a stopped account")
			}
			if kind == "plex" {
				invite.share(remote, true)
				if found() {
					t.Fatal("pending share produced an exact link")
				}
				invite.accept(remote)
			} else if err := e.provider.SetDisabled(ctx, remote, false); err != nil {
				t.Fatal(err)
			}
			row := e.row(uid, id)
			writes, removals, invites := e.provider.disables, invite.removals, invite.invites
			if !found() {
				t.Fatal("live restored access still blocked by historical disable stamp")
			}
			e.svc.SweepAccountDrift(ctx)
			if !reflect.DeepEqual(row, e.row(uid, id)) || e.provider.disables != writes || invite.removals != removals || invite.invites != invites || len(e.provider.libraryWrites) != 0 || len(invite.libraryWrites) != 0 {
				t.Fatal("lookup or maintenance mutated stopped account")
			}
			if kind == "plex" {
				invite.vanish(remote)
			} else if err := e.provider.SetDisabled(ctx, remote, true); err != nil {
				t.Fatal(err)
			}
			if found() {
				t.Fatal("directly removed access retained exact link")
			}
			if err := e.svc.UnlinkAccount(uid, id); err != nil {
				t.Fatal(err)
			}
			before = p.calls
			if found() || p.calls != before {
				t.Fatal("unlinked account reached lookup")
			}
		})
	}
}

func TestWatchLinksRevalidateLocalAccessAfterLookup(t *testing.T) {
	for _, kind := range []string{"jellyfin", "emby", "plex"} {
		for _, change := range []string{"grant", "unlink", "identity", "server", "key", "public address", "libraries"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				e := newEnv(t)
				uid := e.user("viewer")
				id := e.mediaServer(kind, "Video", instance.MediaServerConfig{PublicAddress: "https://media.example"})
				e.grantType(uid, kind, id)
				if _, err := e.svc.insertAccount(accountRow{UserID: uid, InstanceID: id, RemoteUserID: "viewer"}, false); err != nil {
					t.Fatal(err)
				}
				e.providers[id] = &finderProvider{fakeProvider: e.provider, find: func(string, mediaserver.ItemQuery) (mediaserver.Item, error) {
					switch change {
					case "grant":
						e.grantType(uid, kind)
					case "unlink":
						if err := e.svc.UnlinkAccount(uid, id); err != nil {
							t.Error(err)
						}
					case "identity":
						if _, err := e.db.Exec("UPDATE user_media_server_accounts SET remote_user_id = ? WHERE user_id = ? AND instance_id = ?", "other", uid, id); err != nil {
							t.Error(err)
						}
					default:
						inst, err := e.store.Get(id)
						if err != nil {
							return mediaserver.Item{}, err
						}
						switch change {
						case "server":
							inst.URL = "http://other.internal"
						case "key":
							inst.APIKey = "different-key"
						case "public address":
							inst.MediaServerConfig.PublicAddress = "https://other.example"
						case "libraries":
							inst.MediaServerConfig.LibraryIDs = []string{"other"}
						}
						if err := e.store.Update(inst); err != nil {
							return mediaserver.Item{}, err
						}
					}
					return mediaserver.Item{ID: "item", WebPath: "/item/item"}, nil
				}}
				links, err := e.svc.WatchLinks(context.Background(), uid, mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008})
				if err != nil || len(links) != 0 {
					t.Fatalf("stale result escaped: %+v, %v", links, err)
				}
			})
		}
	}
}
