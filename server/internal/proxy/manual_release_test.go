package proxy

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestManualReleaseReferencesExpireAndStayWithTheirSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var refs manualReleaseReferences
		scope := manualReleaseScope{1, "sonarr-1", "http://tv.invalid"}
		const guid = "/get?id=12&token=test-value"
		row := map[string]any{"guid": guid, "indexerId": json.Number("2")}
		if err := refs.protectSearch(scope, []any{row}); err != nil {
			t.Fatal(err)
		}
		ref := row["guid"].(string)
		for _, other := range []manualReleaseScope{
			{2, scope.instanceID, scope.instanceURL},
			{scope.userID, scope.instanceID, "http://changed.invalid"},
		} {
			if _, ok := refs.resolve(other, ref, 2); ok {
				t.Fatal("resolved a selection for another user or destination")
			}
		}
		time.Sleep(manualReleaseTTL - time.Second)
		if got, ok := refs.resolve(scope, ref, 2); !ok || got != guid {
			t.Fatal("original selection expired early")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if _, ok := refs.resolve(scope, ref, 2); ok || refs.count != 0 || refs.guidBytes != 0 {
			t.Fatal("expired selection was still usable or retained in memory")
		}
	})
}

func TestManualReleaseReferenceLimits(t *testing.T) {
	var refs manualReleaseReferences
	scope := manualReleaseScope{1, "radarr-1", "http://movies.invalid"}
	rows := make([]any, maxReleaseRefs)
	for i := range rows {
		rows[i] = map[string]any{"guid": "/get?token=test", "indexerId": json.Number("2")}
	}
	if err := refs.protectSearch(scope, rows); err != nil {
		t.Fatal(err)
	}
	old := rows[0].(map[string]any)["guid"].(string)
	row := map[string]any{"guid": "/get?token=new", "indexerId": json.Number("2")}
	if err := refs.protectSearch(scope, []any{row}); err != nil {
		t.Fatal(err)
	}
	if _, ok := refs.resolve(scope, old, 2); ok || refs.count != 1 {
		t.Fatal("oldest search was not evicted at the entry limit")
	}
	newRef := row["guid"].(string)
	// A search that cannot fit must not evict otherwise usable selections.
	row["guid"] = "/get?token=" + strings.Repeat("x", maxReleaseGUIDBytes)
	if err := refs.protectSearch(scope, []any{row}); err != errReleaseSearchTooLarge {
		t.Fatalf("oversized GUID: %v", err)
	}
	if guid, ok := refs.resolve(scope, newRef, 2); !ok || guid != "/get?token=new" {
		t.Fatal("failed search displaced a usable selection")
	}
}
