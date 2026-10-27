package mediaserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func inventoryDecode(t *testing.T, out any, value any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, out); e != nil {
		t.Fatal(e)
	}
}
func jsonMovie(id string, tmdb int) map[string]any {
	return map[string]any{"Id": id, "Type": "Movie", "ProviderIds": map[string]string{"Tmdb": strconv.Itoa(tmdb)}, "MediaSources": []any{map[string]any{"MediaStreams": []any{map[string]any{"Type": "Video", "Width": 3840, "Height": 1600}}}}}
}
func TestJSONInventoryPaginationAndNativeRecipientQueries(t *testing.T) {
	for _, emby := range []bool{false, true} {
		t.Run(fmt.Sprint(emby), func(t *testing.T) {
			reads, policies := 0, 0
			j := JSONInventory{UserPath: emby, Libraries: func(context.Context) ([]Library, error) {
				return []Library{{ID: "native", CollectionType: "movies"}}, nil
			}, Access: func(context.Context, string) (map[string]any, error) {
				policies++
				return map[string]any{"IsDisabled": false}, nil
			}}
			j.Read = func(ctx context.Context, path string, out any) error {
				reads++
				u, _ := url.Parse(path)
				q := u.Query()
				if emby && u.Path != "/Users/recipient/Items" || !emby && q.Get("UserId") != "recipient" {
					t.Fatalf("wrong recipient scope: %s", path)
				}
				if q.Get("ParentId") != "native" || q.Get("Limit") != "200" || q.Get("EnableTotalRecordCount") != "true" {
					t.Fatalf("unbounded query: %s", path)
				}
				start, _ := strconv.Atoi(q.Get("StartIndex"))
				rows := []any{}
				for n := start; n < 201 && n < start+200; n++ {
					rows = append(rows, jsonMovie(strconv.Itoa(n+1), n+1))
				}
				inventoryDecode(t, out, map[string]any{"Items": rows, "TotalRecordCount": 201})
				return nil
			}
			items, e := j.Run(context.Background(), []string{"native"}, "recipient", nil, nil, time.Time{}, false)
			if e != nil || len(items) != 201 || reads != 2 || policies != 2 || !items[0].Movie || !items[0].Is4K {
				t.Fatalf("items=%d reads=%d policy=%d err=%v", len(items), reads, policies, e)
			}
		})
	}
}
func TestJSONInventoryRejectsIncompleteCatalogAndPolicyChanges(t *testing.T) {
	for _, kind := range []string{"missing total", "short page", "duplicate IDs", "changed total", "policy changed", "unreachable"} {
		t.Run(kind, func(t *testing.T) {
			reads, policies := 0, 0
			j := JSONInventory{Libraries: func(context.Context) ([]Library, error) {
				return []Library{{ID: "movies", CollectionType: "movies"}}, nil
			}, Access: func(context.Context, string) (map[string]any, error) {
				policies++
				return map[string]any{"IsDisabled": kind == "policy changed" && policies > 1}, nil
			}}
			j.Read = func(ctx context.Context, path string, out any) error {
				reads++
				if kind == "unreachable" {
					return errors.New("offline")
				}
				rows := []any{jsonMovie("one", 1)}
				total := any(1)
				switch kind {
				case "missing total":
					total = nil
				case "short page":
					total = 2
					if reads > 1 {
						rows = nil
					}
				case "duplicate IDs":
					total = 2
					rows = append(rows, jsonMovie("one", 1))
				case "changed total":
					total = 2
					if reads > 1 {
						total = 3
					}
				}
				inventoryDecode(t, out, map[string]any{"Items": rows, "TotalRecordCount": total})
				return nil
			}
			items, e := j.Run(context.Background(), []string{"movies"}, "recipient", nil, nil, time.Time{}, true)
			if e == nil || len(items) != 0 {
				t.Fatalf("bad read produced evidence: %d %v", len(items), e)
			}
		})
	}
}
func TestJSONInventoryVerifiesCandidateAndPlayableEpisodeCoverage(t *testing.T) {
	reads := 0
	j := JSONInventory{Libraries: func(context.Context) ([]Library, error) {
		return []Library{{ID: "tv", CollectionType: "tvshows"}, {ID: "movies", CollectionType: "movies"}}, nil
	}, Access: func(context.Context, string) (map[string]any, error) { return map[string]any{"IsDisabled": false}, nil }}
	j.Read = func(ctx context.Context, path string, out any) error {
		reads++
		u, _ := url.Parse(path)
		q := u.Query()
		var rows []any
		if q.Get("ParentId") == "tv" {
			if q.Get("Ids") != "show" || q.Get("SearchTerm") != "" || q.Get("Years") != "" {
				t.Fatalf("candidate ignored: %s", path)
			}
			rows = []any{map[string]any{"Id": "show", "Type": "Series", "ProviderIds": map[string]string{"Tmdb": "10", "Tvdb": "20"}}}
		} else if q.Get("ParentId") == "show" {
			ep := jsonMovie("ep", 111)
			ep["Type"] = "Episode"
			ep["SeriesId"] = "show"
			ep["IndexNumber"] = 1
			ep["IndexNumberEnd"] = 2
			ep["ParentIndexNumber"] = 3
			ep["ProviderIds"] = map[string]string{"Tmdb": "111", "Tvdb": "222"}
			virtual := jsonMovie("placeholder", 333)
			virtual["Type"] = "Episode"
			virtual["IndexNumber"] = 3
			virtual["ParentIndexNumber"] = 3
			virtual["IsVirtualItem"] = true
			rows = []any{ep, virtual}
		} else {
			t.Fatalf("unrelated catalog traversal: %s", path)
		}
		inventoryDecode(t, out, map[string]any{"Items": rows, "TotalRecordCount": len(rows)})
		return nil
	}
	q := ItemQuery{MediaType: "tv", TMDBID: 10, TVDBID: 20}
	items, e := j.Run(context.Background(), []string{"tv", "movies"}, "recipient", &q, []InventoryItem{{LibraryID: "tv", ItemID: "show"}}, time.Time{}, false)
	if e != nil || len(items) != 1 || len(items[0].Episodes) != 2 || reads != 2 {
		t.Fatalf("items=%+v err=%v", items, e)
	}
	ep := items[0].Episodes[0]
	if ep.TVDBID != 222 || ep.TMDBID != 111 || ep.SeriesTVDBID != 20 || ep.Numbering != "unknown" || items[0].Episodes[1].TVDBID != 0 {
		t.Fatalf("invented episode identity: %+v", items[0].Episodes)
	}
}
func TestJSONInventoryIncrementalEpisodeRefreshAndEmptyMovie(t *testing.T) {
	j := JSONInventory{Libraries: func(context.Context) ([]Library, error) { return []Library{{ID: "tv", CollectionType: "tvshows"}}, nil }}
	reads := 0
	j.Read = func(ctx context.Context, path string, out any) error {
		reads++
		u, _ := url.Parse(path)
		q := u.Query()
		rows := []any{}
		switch reads {
		case 1:
			if q.Get("MinDateLastSaved") == "" || q.Get("IncludeItemTypes") != "Movie,Series,Episode" {
				t.Fatal("incremental cursor missing")
			}
			rows = append(rows, map[string]any{"Id": "changed-ep", "Type": "Episode", "SeriesId": "show"})
		case 2:
			if q.Get("Ids") != "show" || q.Get("MinDateLastSaved") != "" {
				t.Fatal("parent not freshly resolved")
			}
			rows = append(rows, map[string]any{"Id": "show", "Type": "Series", "ProviderIds": map[string]string{"Tmdb": "10"}})
		case 3:
			if q.Get("ParentId") != "show" {
				t.Fatal("full show refresh missing")
			}
		default:
			t.Fatalf("unexpected read: %s", path)
		}
		inventoryDecode(t, out, map[string]any{"Items": rows, "TotalRecordCount": len(rows)})
		return nil
	}
	items, e := j.Run(context.Background(), []string{"tv"}, "", nil, nil, time.Now().Add(-time.Minute), false)
	if e != nil || len(items) != 1 || len(items[0].Episodes) != 0 || reads != 3 {
		t.Fatalf("incremental=%+v err=%v", items, e)
	}
	v := inventoryVideo{Type: "Movie"}
	if playable, _ := v.playable(); playable {
		t.Fatal("empty movie marked available")
	}
}
func TestInventoryIdentityRejectsConflicts(t *testing.T) {
	q := ItemQuery{MediaType: "tv", TMDBID: 1, TVDBID: 2}
	for _, item := range []InventoryItem{{MediaType: "tv", TMDBID: 1, TVDBID: 99}, {MediaType: "tv", TMDBID: 99, TVDBID: 2}, {MediaType: "movie", TMDBID: 1}, {MediaType: "tv"}} {
		if InventoryMatches(item, q) {
			t.Fatalf("false match: %+v", item)
		}
	}
}

func TestJSONInventoryRetainsDistinctVersionEvidence(t *testing.T) {
	var video inventoryVideo
	inventoryDecode(t, &video, map[string]any{"Id": "movie", "Type": "Movie", "MediaSources": []any{
		map[string]any{"Id": "source-hd", "Path": "/private/one.mkv", "MediaStreams": []any{map[string]any{"Type": "Video", "Width": 1920, "Height": 1080}}},
		map[string]any{"Id": "source-uhd", "Path": "/private/two.mkv", "MediaStreams": []any{map[string]any{"Type": "Video", "Width": 3840, "Height": 1600}}},
	}})
	copies := video.copies()
	if len(copies) != 2 || copies[0].SourceID != "source-hd" || copies[0].Is4K || !copies[1].Is4K || copies[1].Width != 3840 || copies[1].Height != 1600 {
		t.Fatalf("copies=%+v", copies)
	}
	raw, _ := json.Marshal(copies)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "Path") {
		t.Fatal("file paths escaped inventory DTO")
	}
}
