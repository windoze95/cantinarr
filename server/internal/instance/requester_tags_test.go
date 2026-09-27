package instance

import (
	"encoding/json"
	"testing"
)

func TestRequesterTagSettingDefaultsAndOldClientEdits(t *testing.T) {
	store := newTestStore(t)
	for _, kind := range []string{"radarr", "sonarr", "chaptarr", "lidarr", "plex"} {
		t.Run(kind, func(t *testing.T) {
			id := mkInstance(t, store, kind, kind)
			i, err := store.Get(id)
			if err != nil || i.TagRequests {
				t.Fatalf("default=%+v err=%v", i, err)
			}
			i.TagRequests = true
			err = store.Update(i)
			if kind == "plex" {
				if err == nil {
					t.Fatal("unsupported type accepted tags")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			existing, err := store.Get(id)
			if err != nil || !existing.TagRequests {
				t.Fatalf("stored=%+v err=%v", existing, err)
			}
			var oldClient instanceRequest
			if err := json.Unmarshal([]byte(`{"name":"Edited name"}`), &oldClient); err != nil {
				t.Fatal(err)
			}
			oldClient.Instance.ServiceType = kind
			if err := applyTagRequests(&oldClient.Instance, oldClient.TagRequests, existing); err != nil || !oldClient.Instance.TagRequests {
				t.Fatal("old client lost tag setting")
			}
			var disable instanceRequest
			if err := json.Unmarshal([]byte(`{"tag_requests":false}`), &disable); err != nil {
				t.Fatal(err)
			}
			disable.Instance.ServiceType = kind
			if err := applyTagRequests(&disable.Instance, disable.TagRequests, existing); err != nil || disable.Instance.TagRequests {
				t.Fatal("explicit false did not disable")
			}
		})
	}
}
