package instance

import (
	"reflect"
	"testing"
)

func TestAssignmentsControlAllAutomationAccess(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			s := newTestStore(t)
			user := createUser(t, s, "reader")
			other := createUser(t, s, "other")
			global := mkDefaultInstance(t, s, service, "Z global")
			a := mkInstance(t, s, service, "A library")
			b := mkInstance(t, s, service, "B library")
			check := func(want string, ids ...string) {
				t.Helper()
				got, err := s.EffectiveDefaultInstanceID(user, service)
				if err != nil || got != want {
					t.Fatalf("default=%q,%v want %q", got, err, want)
				}
				visible, err := s.VisibleInstanceIDs(user, service)
				if err != nil || !reflect.DeepEqual(visible, ids) {
					t.Fatalf("visible=%v,%v want %v", visible, err, ids)
				}
			}
			if id, err := s.EffectiveDefaultInstanceID(user, service); err != nil || id != "" {
				t.Fatalf("default granted access: %q,%v", id, err)
			}
			if err := s.SetUserDefault(user, service, global); err == nil {
				t.Fatal("unassigned preference accepted")
			}
			if err := s.ChangeAssignments(b, []int64{user}, true); err != nil {
				t.Fatal(err)
			}
			check(b, b)
			if err := s.ChangeAssignments(a, []int64{user}, true); err != nil {
				t.Fatal(err)
			}
			check(a, a, b)
			if err := s.SetUserDefault(user, service, b); err != nil {
				t.Fatal(err)
			}
			check(b, a, b)
			if err := s.ChangeAssignments(global, []int64{user, other}, true); err != nil {
				t.Fatal(err)
			}
			check(b, a, b, global)
			if err := s.ClearUserDefault(user, service); err != nil {
				t.Fatal(err)
			}
			check(global, a, b, global)
			if err := s.SetUserDefault(user, service, b); err != nil {
				t.Fatal(err)
			}
			if err := s.ChangeAssignments(b, []int64{user}, false); err != nil {
				t.Fatal(err)
			}
			check(global, a, global)
			if _, pinned, _ := s.GetUserDefault(user, service); pinned {
				t.Fatal("revoked preference survived")
			}
			if err := s.ChangeAssignments(global, []int64{user, 9999}, false); err == nil {
				t.Fatal("unknown user accepted")
			}
			check(global, a, global)
			if err := s.ChangeAssignments(global, []int64{user}, false); err != nil {
				t.Fatal(err)
			}
			check(a, a)
			if ok, _ := s.UserHasInstanceAccess(other, global); !ok {
				t.Fatal("bulk removal touched unselected user")
			}
			if err := s.ChangeAssignments(a, []int64{user}, false); err != nil {
				t.Fatal(err)
			}
			if id, err := s.EffectiveDefaultInstanceID(user, service); err != nil || id != "" {
				t.Fatalf("last revocation: %q,%v", id, err)
			}
		})
	}
}

func TestAdministratorPersonalDefaultsRequireAssignments(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		t.Run(service, func(t *testing.T) {
			s := newTestStore(t)
			admin := createUser(t, s, "admin")
			if _, err := s.db.Exec("UPDATE users SET role='admin' WHERE id=?", admin); err != nil {
				t.Fatal(err)
			}
			global := mkDefaultInstance(t, s, service, "Global")
			personal := mkInstance(t, s, service, "Personal")
			check := func(want string) {
				t.Helper()
				id, err := s.AssignedDefaultInstanceID(admin, service)
				if err != nil || id != want {
					t.Fatalf("personal default=%q,%v want %q", id, err, want)
				}
			}
			check("")
			if id, err := s.EffectiveDefaultInstanceID(admin, service); err != nil || id != global {
				t.Fatal("admin management routing lost", id, err)
			}
			if err := s.SetUserDefault(admin, service, global); err == nil {
				t.Fatal("unassigned personal preference accepted")
			}
			if err := s.ChangeAssignments(personal, []int64{admin}, true); err != nil {
				t.Fatal(err)
			}
			check(personal)
			if err := s.ChangeAssignments(global, []int64{admin}, true); err != nil {
				t.Fatal(err)
			}
			check(global)
			if err := s.SetUserDefault(admin, service, personal); err != nil {
				t.Fatal(err)
			}
			check(personal)
			if err := s.SetUserGrants(admin, map[string][]string{service: {global}}); err != nil {
				t.Fatal(err)
			}
			check(global)
			if _, pinned, err := s.GetUserDefault(admin, service); err != nil || pinned {
				t.Fatal("revoked admin preference survived", err)
			}
			if err := s.ChangeAssignments(global, []int64{admin}, false); err != nil {
				t.Fatal(err)
			}
			check("")
		})
	}
}

func TestAutoAddSettingPreservesOmissionAndScope(t *testing.T) {
	for _, service := range []string{"radarr", "sonarr", "chaptarr", "lidarr"} {
		s := newTestStore(t)
		inst := &Instance{ServiceType: service, Name: service, URL: "http://example", APIKey: "key"}
		if err := applyAutoAddUsers(inst, nil, nil); err != nil || !inst.AutoAddUsers {
			t.Fatalf("new default %s: %+v %v", service, inst, err)
		}
		if err := s.Create(inst); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(inst.ID)
		if err != nil || !got.AutoAddUsers {
			t.Fatalf("read: %+v,%v", got, err)
		}
		edit := &Instance{ServiceType: service}
		if err := applyAutoAddUsers(edit, nil, got); err != nil || !edit.AutoAddUsers {
			t.Fatal("omitted edit changed setting")
		}
		off := false
		if err := applyAutoAddUsers(edit, &off, got); err != nil || edit.AutoAddUsers {
			t.Fatal("explicit off ignored")
		}
	}
	on := true
	if err := applyAutoAddUsers(&Instance{ServiceType: "plex"}, &on, nil); err == nil {
		t.Fatal("media server auto-add accepted")
	}
}
