package mediaaccess

import (
	"reflect"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/audiobookshelf"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

func TestABSBookTagPlanUsesExactEditionIdentityAndUnionsRequesters(t *testing.T) {
	inventory := []audiobookshelf.BookInventoryItem{
		{ID: "audio-1", LibraryID: "books", ISBN: "978-0-306-40615-7", Tags: []string{"Classics"}, HasAudio: true},
		{ID: "ebook-1", LibraryID: "books", ASIN: "B012345678", Tags: []string{}, HasEbook: true},
		{ID: "unrelated", LibraryID: "books", Tags: []string{}, HasEbook: true},
	}
	identities := []absBookRequestIdentity{
		{RequestID: 1, Format: "audiobook", ISBNs: []string{"0306406152"}, UserIDs: []int64{7}},
		{RequestID: 2, Format: "audiobook", ISBNs: []string{"9780306406157"}, UserIDs: []int64{8}},
		{RequestID: 3, Format: "ebook", ASINs: []string{"b012345678"}, UserIDs: []int64{7}},
	}
	plan := buildABSBookTagPlan("abs", "books", inventory, identities)
	if plan.InventoryCount != 3 || plan.RequestedCount != 2 || plan.SharedCount != 1 || plan.UnresolvedCount != 0 {
		t.Fatalf("unexpected classification counts: %+v", plan)
	}
	if plan.Items[0].ItemID != "audio-1" || !reflect.DeepEqual(plan.Items[0].UserIDs, []int64{7, 8}) || !reflect.DeepEqual(plan.Items[0].TagsToAdd, []string{"cantinarr-user-7", "cantinarr-user-8"}) || !reflect.DeepEqual(plan.Items[0].TagsToDrop, []string{absSharedLibraryTag}) {
		t.Fatalf("audiobook co-requesters not unioned: %+v", plan.Items[0])
	}
	if plan.Items[1].ItemID != "ebook-1" || !reflect.DeepEqual(plan.Items[1].TagsToAdd, []string{"cantinarr-user-7"}) {
		t.Fatalf("ebook identity was not format-scoped: %+v", plan.Items[1])
	}
	if plan.Items[2].ItemID != "unrelated" || plan.Items[2].Class != "shared" || !reflect.DeepEqual(plan.Items[2].TagsToAdd, []string{absSharedLibraryTag}) {
		t.Fatalf("unrequested title should remain shared: %+v", plan.Items[2])
	}
}

func TestABSBookTagPlanDoesNotMatchByTitleAndReportsUnresolvedRequests(t *testing.T) {
	plan := buildABSBookTagPlan("abs", "books", []audiobookshelf.BookInventoryItem{{
		ID: "same-title", LibraryID: "books", Tags: []string{}, HasEbook: true,
	}}, []absBookRequestIdentity{{RequestID: 4, Format: "ebook", Title: "Same title", UserIDs: []int64{9}}})
	if plan.UnresolvedCount != 1 || plan.RequestedCount != 0 || plan.SharedCount != 1 || plan.UnmatchedRequest[0].RequestID != 4 {
		t.Fatalf("title-only request should be surfaced and not guessed: %+v", plan)
	}
}

func TestABSBookRecordIdentityUsesOnlyMatchingEditionFormat(t *testing.T) {
	book := &chaptarr.Book{Editions: []chaptarr.Edition{
		{Format: "Ebook", ASIN: "B012345678", ISBN13: "9780306406157"},
		{Format: "Audiobook", ASIN: "B876543210", ISBN13: "9781492000000"},
	}}
	identity := absBookRecordIdentity(absBookRequestIdentity{Format: "ebook"}, book)
	if !reflect.DeepEqual(identity.ASINs, []string{"B012345678"}) || !reflect.DeepEqual(identity.ISBNs, []string{"9780306406157"}) {
		t.Fatalf("wrong edition identifiers: %+v", identity)
	}
}

func TestABSUserTagUsesStableOpaqueUserID(t *testing.T) {
	if got := ABSUserTag(43); got != "cantinarr-user-43" {
		t.Fatalf("tag = %q", got)
	}
	if ABSUserTag(0) != "" || ABSUserTag(-1) != "" {
		t.Fatal("invalid user IDs must not produce an access tag")
	}
}
