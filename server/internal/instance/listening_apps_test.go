package instance

import (
	"encoding/json"
	"testing"
)

func TestListeningAppDefaultsValidateCloneAndRoundTrip(t *testing.T) {
	h := &Handler{}
	inst := &Instance{ServiceType: "audiobookshelf"}
	apps := ListeningApps{IOS: "shelfplayer", Android: "theshelf"}
	if err := h.applyMediaServerConfig(inst, &MediaServerConfig{ListeningApps: &apps}, nil); err != nil {
		t.Fatal(err)
	}
	apps.IOS = "browser"
	if inst.MediaServerConfig.ListeningApps.IOS != "shelfplayer" {
		t.Fatal("configuration aliases submitted data")
	}
	copy := inst.MediaServerConfig.clone()
	copy.ListeningApps.Android = "browser"
	if inst.MediaServerConfig.ListeningApps.Android != "theshelf" {
		t.Fatal("configuration clone aliases saved data")
	}
	raw, err := encodeMediaServerConfig(inst)
	var saved MediaServerConfig
	if err != nil || json.Unmarshal([]byte(raw), &saved) != nil || saved.ListeningApps == nil || saved.ListeningApps.IOS != "shelfplayer" {
		t.Fatalf("round trip: %s %v", raw, err)
	}
	updated := &Instance{ServiceType: "audiobookshelf"}
	if err := h.applyMediaServerConfig(updated, nil, inst); err != nil || updated.MediaServerConfig.ListeningApps.Android != "theshelf" {
		t.Fatal("unrelated edits lost defaults")
	}
	for _, kind := range []string{"jellyfin", "emby", "plex", "radarr"} {
		if err := h.applyMediaServerConfig(&Instance{ServiceType: kind}, &saved, nil); err == nil {
			t.Fatalf("accepted audiobook defaults for %s", kind)
		}
	}
	for _, bad := range []ListeningApps{{IOS: "theshelf"}, {Android: "shelfplayer"}, {IOS: "https://example.com"}, {Android: "other"}} {
		if err := h.applyMediaServerConfig(updated, &MediaServerConfig{ListeningApps: &bad}, inst); err == nil {
			t.Fatalf("accepted unsupported app %+v", bad)
		}
	}
}
