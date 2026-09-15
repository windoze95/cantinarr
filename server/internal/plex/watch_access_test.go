package plex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// PMS can temporarily expose its old library scope while a plex.tv share
// update propagates. Its LAN authentication exception can also broaden a
// stale token's view. The accepted share bounds the searchable libraries.
func TestPlexWatchRespectsCurrentShareLibraries(t *testing.T) {
	for _, scenario := range []string{"unshared library", "missing libraries", "invalid library key", "share removed during lookup", "share pending during lookup", "libraries changed during lookup", "token changed during lookup", "equivalent library order"} {
		t.Run(scenario, func(t *testing.T) {
			f := newWatchFixture(t)
			original := f.share
			changed := original
			beforeSearch := false
			switch scenario {
			case "unshared library":
				f.share = strings.Replace(original, `key="1" shared="1"`, `key="1" shared="0"`, 1)
				beforeSearch = true
			case "missing libraries":
				f.share = `<SharedServer email="alice@example.com" accepted="1" accessToken="share-SECRET"/>`
				beforeSearch = true
			case "invalid library key":
				f.share = strings.Replace(original, `key="1"`, `key="../other"`, 1)
				beforeSearch = true
			case "share removed during lookup":
				changed = ""
			case "share pending during lookup":
				changed = strings.Replace(original, `accepted="1"`, `accepted="0"`, 1)
			case "libraries changed during lookup":
				changed = strings.Replace(original, `key="1" shared="1"`, `key="1" shared="0"`, 1)
			case "token changed during lookup":
				changed = strings.Replace(original, `share-SECRET`, `replacement-SECRET`, 1)
			case "equivalent library order":
				changed = strings.Replace(original, `<Section key="1" shared="1"/><Section key="3" shared="1"/>`, `<Section key="3" shared="1"/><Section key="1" shared="1"/>`, 1)
			}
			if !beforeSearch {
				f.sharePage = func(read int32) string {
					if read == 1 {
						return original
					}
					return changed
				}
			}
			item, err := f.p.FindItem(context.Background(), "alice@example.com", movieWatchQuery())
			if scenario == "equivalent library order" {
				if err != nil || item.ID != "123" || f.shareReads.Load() != 2 {
					t.Fatalf("equivalent share rejected or not rechecked: item=%+v err=%v reads=%d", item, err, f.shareReads.Load())
				}
				return
			}
			if !errors.Is(err, mediaserver.ErrItemUnverified) || item.ID != "" || item.WebPath != "" {
				t.Fatalf("obsolete share produced a link: item=%+v err=%v", item, err)
			}
			if beforeSearch && f.metadataCalls.Load() != 0 {
				t.Fatal("searched a library outside the current share")
			}
			if !beforeSearch && f.shareReads.Load() != 2 {
				t.Fatal("current share was not rechecked after search")
			}
		})
	}
}
