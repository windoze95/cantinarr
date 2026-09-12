package instance

import (
	"encoding/json"
	"testing"
)

func TestVideoAppDefaultsValidateCloneAndRoundTrip(t *testing.T) {
	h := &Handler{}
	for _, serviceType := range []string{"plex", "jellyfin", "emby"} {
		t.Run(serviceType, func(t *testing.T) {
			inst := &Instance{ServiceType: serviceType}
			apps := VideoApps{IOS: "infuse"}
			cfg := &MediaServerConfig{VideoApps: &apps, LibraryIDs: []string{"main"}}
			if err := h.applyMediaServerConfig(inst, cfg, nil); err != nil {
				t.Fatal(err)
			}
			apps.IOS = "browser"
			copy := inst.MediaServerConfig.clone()
			copy.VideoApps.IOS = "service"
			if inst.MediaServerConfig.VideoApps.IOS != "infuse" {
				t.Fatal("configuration aliases editable data")
			}
			raw, err := encodeMediaServerConfig(inst)
			var saved MediaServerConfig
			if err != nil || json.Unmarshal([]byte(raw), &saved) != nil || saved.VideoApps == nil || saved.VideoApps.IOS != "infuse" || len(saved.LibraryIDs) != 1 {
				t.Fatalf("round trip: %s %v", raw, err)
			}
			updated := &Instance{ServiceType: serviceType}
			if err := h.applyMediaServerConfig(updated, nil, inst); err != nil || updated.MediaServerConfig.VideoApps.IOS != "infuse" {
				t.Fatal("omitted config lost defaults")
			}
		})
	}
	for _, serviceType := range []string{"audiobookshelf", "radarr"} {
		if err := h.applyMediaServerConfig(&Instance{ServiceType: serviceType}, &MediaServerConfig{VideoApps: &VideoApps{IOS: "infuse"}}, nil); err == nil {
			t.Fatalf("accepted video defaults on %s", serviceType)
		}
	}
	for _, invalid := range []string{"shelfplayer", "plex", "infuse://movie/1", "https://example.com"} {
		if (VideoApps{IOS: invalid}).Validate() == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	if got := (VideoApps{}).WithDefaults(nil); got.IOS != "service" {
		t.Fatalf("legacy default: %+v", got)
	}
	for _, app := range []string{"service", "infuse", "browser"} {
		defaults := &VideoApps{IOS: "infuse"}
		if got := (VideoApps{IOS: app}).WithDefaults(defaults); got.IOS != app {
			t.Fatalf("override lost: %+v", got)
		}
	}
}
