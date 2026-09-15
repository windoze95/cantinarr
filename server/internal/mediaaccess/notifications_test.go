package mediaaccess

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

func TestAccountServerGrantNotifications(t *testing.T) {
	for _, service := range []string{"jellyfin", "emby", "audiobookshelf"} {
		t.Run(service, func(t *testing.T) {
			e := newEnv(t)
			n := e.notifier()
			alice, bob := e.user("alice"), e.user("bob")
			server := e.mediaServer(service, "Home", instance.MediaServerConfig{})
			sibling := e.mediaServer(service, "Away", instance.MediaServerConfig{})
			e.grantType(alice, service, server, server)
			if len(n.user) != 1 || n.user[0].userID != alice || n.user[0].event != eventMediaServerAccess {
				t.Fatalf("wrong recipient or duplicate notification: %+v", n.user)
			}
			want := map[string]interface{}{"instance_id": server, "server_name": "Home", "service_type": service, "access_state": "granted"}
			if !reflect.DeepEqual(n.user[0].data, want) {
				t.Fatalf("access data = %+v, want %+v", n.user[0].data, want)
			}
			// Both grant editors must compare committed rows, not the submitted
			// user list. A repeated save, a removal, and a failed write are silent.
			if err := e.store.SetInstanceGrantUsers(server, []int64{alice, alice}); err != nil {
				t.Fatal(err)
			}
			e.grantType(alice, service, server)
			if err := e.store.SetInstanceGrantUsers(server, []int64{bob, 999999}); err == nil {
				t.Fatal("unknown user did not roll back grant replacement")
			}
			if len(n.user) != 1 {
				t.Fatalf("unchanged or rolled-back grants notified: %+v", n.user)
			}
			if err := e.store.SetInstanceGrantUsers(server, []int64{bob}); err != nil {
				t.Fatal(err)
			}
			if len(n.user) != 2 || n.user[1].userID != bob {
				t.Fatalf("replacement notified wrong users: %+v", n.user)
			}
			e.grantType(bob, service, server, sibling)
			if len(n.user) != 3 || n.user[2].data["instance_id"] != sibling {
				t.Fatalf("adding a sibling repeated existing access: %+v", n.user)
			}
			e.grantType(bob, service)
			e.grantType(bob, service, server)
			if len(n.user) != 4 || n.user[3].data["instance_id"] != server {
				t.Fatalf("re-grant should notify once: %+v", n.user)
			}
			// Creating the account completes the guide the grant already linked
			// to; it must not repeat that alert.
			if _, err := e.svc.CreateAccount(context.Background(), bob, server, "strong-password"); err != nil {
				t.Fatal(err)
			}
			e.svc.OnGrantsChanged([]int64{bob})
			if len(n.user) != 4 || len(n.admin) != 0 {
				t.Fatalf("account setup/reconciliation repeated the grant alert: %+v", n)
			}
			// Starting the service with existing grants never announces them.
			restarted := NewService(e.db, e.store, e.svc.providers, nil)
			restarted.SetNotifier(n)
			restarted.background = func(fn func()) { fn() }
			e.store.SetGrantAddedObserver(restarted.OnGrantAdded)
			restarted.OnGrantsChanged([]int64{bob})
			e.grantType(bob, service, server)
			if len(n.user) != 4 {
				t.Fatal("restart or unchanged grant replayed existing access")
			}
		})
	}
}

func TestAdminAccountLinkNotifiesNewGrant(t *testing.T) {
	for _, service := range []string{"jellyfin", "emby", "audiobookshelf"} {
		t.Run(service, func(t *testing.T) {
			e := newEnv(t)
			n := e.notifier()
			alice := e.user("alice")
			server := e.mediaServer(service, "Home", instance.MediaServerConfig{})
			remote := e.provider.addUser("alice", false, false)
			if _, err := e.svc.LinkAccount(context.Background(), alice, server, remote); err != nil {
				t.Fatal(err)
			}
			if n.userEvents(alice, eventMediaServerAccess) != 1 {
				t.Fatalf("admin link did not notify new access: %+v", n.user)
			}
		})
	}
}

func TestPlexGrantWaitsForSuccessfulShare(t *testing.T) {
	e := newEnv(t)
	n := e.notifier()
	alice := e.user("alice")
	server := e.mediaServer("plex", "Home", instance.MediaServerConfig{})
	provider := newFakeInviteProvider()
	e.providers[server] = provider
	e.grantType(alice, "plex", server)
	e.svc.OnGrantsChanged([]int64{alice})
	if len(n.user) != 0 {
		t.Fatal("grant alone claimed a Plex invitation")
	}
	e.setEmail(alice, "alice@example.com")
	provider.createErr = errors.New("offline")
	e.svc.OnGrantsChanged([]int64{alice})
	if len(n.user) != 0 {
		t.Fatal("failed share sent an access notification")
	}
	provider.createErr = nil
	e.svc.OnGrantsChanged([]int64{alice})
	e.svc.OnGrantsChanged([]int64{alice})
	if len(n.user) != 1 || n.user[0].data["access_state"] != "invite_pending" || n.user[0].data["service_type"] != "plex" {
		t.Fatalf("invite should notify once with acceptance steps: %+v", n.user)
	}
}
