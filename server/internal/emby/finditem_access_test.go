package emby

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// An admin key can still read a disabled user's items on both real servers.
// These cases require the account read, not just a user-scoped item query.
func TestFindItemRequiresCurrentEnabledAccount(t *testing.T) {
	for _, scenario := range []string{"disabled", "deleted", "wrong identity", "missing policy", "missing disabled flag", "invalid disabled flag", "invalid policy", "disabled during lookup", "deleted during lookup", "permissions changed", "policy order changed", "unreachable", "unreachable after lookup"} {
		t.Run(scenario, func(t *testing.T) {
			reads := 0
			lib := &fakeLibrary{t: t, items: []map[string]any{item("match", map[string]any{"Tmdb": "10378"})}}
			lib.user = func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("lookup wrote to provider: %s", r.Method)
				}
				reads++
				policy := any(map[string]any{"IsDisabled": false, "EnabledFolders": []string{"movies"}})
				id := "reader"
				switch scenario {
				case "disabled":
					policy = map[string]any{"IsDisabled": true}
				case "deleted":
					w.WriteHeader(http.StatusNotFound)
					return
				case "wrong identity":
					id = "someone-else"
				case "missing policy":
					policy = nil
				case "missing disabled flag":
					policy = map[string]any{"EnabledFolders": []string{"movies"}}
				case "invalid disabled flag":
					policy = map[string]any{"IsDisabled": "false"}
				case "invalid policy":
					policy = "unreadable"
				case "disabled during lookup":
					if reads > 1 {
						policy = map[string]any{"IsDisabled": true}
					}
				case "deleted during lookup":
					if reads > 1 {
						w.WriteHeader(http.StatusNotFound)
						return
					}
				case "permissions changed":
					if reads > 1 {
						policy = map[string]any{"IsDisabled": false, "EnabledFolders": []string{"different"}}
					}
				case "policy order changed":
					if reads > 1 {
						_, _ = w.Write([]byte(`{"Policy":{"IsDisabled":false,"EnabledFolders":["movies"]},"Id":"reader"}`))
						return
					}
				case "unreachable":
					w.WriteHeader(http.StatusBadGateway)
					return
				case "unreachable after lookup":
					if reads > 1 {
						w.WriteHeader(http.StatusBadGateway)
						return
					}
				}
				writeJSON(t, w, map[string]any{"Id": id, "Policy": policy})
			}
			c := testClient(t, lib.handler())
			got, err := c.FindItem(context.Background(), "reader", mediaserver.ItemQuery{MediaType: "movie", TMDBID: 10378, Year: 2008})
			if scenario == "policy order changed" {
				if err != nil || got.ID != "match" || reads != 2 {
					t.Fatalf("equivalent policy rejected: %+v, %v, reads=%d", got, err, reads)
				}
				return
			}
			if got.ID != "" || got.WebPath != "" {
				t.Fatalf("unverified account received item: %+v", got)
			}
			if scenario == "unreachable" || scenario == "unreachable after lookup" {
				if err == nil || errors.Is(err, mediaserver.ErrItemNotFound) || errors.Is(err, mediaserver.ErrItemUnverified) {
					t.Fatalf("outage reported as absence/account refusal: %v", err)
				}
			} else if !errors.Is(err, mediaserver.ErrItemUnverified) {
				t.Fatalf("err=%v, want unverified", err)
			}
			switch scenario {
			case "disabled during lookup", "deleted during lookup", "permissions changed", "unreachable after lookup":
				if len(lib.queries) != 1 || reads != 2 {
					t.Fatalf("post-lookup verification missing: queries=%d reads=%d", len(lib.queries), reads)
				}
			default:
				if len(lib.queries) != 0 {
					t.Fatal("ineligible account reached catalog")
				}
			}
		})
	}
}
