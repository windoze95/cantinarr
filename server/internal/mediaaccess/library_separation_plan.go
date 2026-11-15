package mediaaccess

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/audiobookshelf"
	"github.com/windoze95/cantinarr-server/internal/chaptarr"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

const absSharedLibraryTag = "cantinarr-shared"

// ABSUserTag deliberately contains only Cantinarr's stable numeric user ID.
// Usernames change and can expose identity when tags are visible to clients.
func ABSUserTag(userID int64) string {
	if userID <= 0 {
		return ""
	}
	return fmt.Sprintf("cantinarr-user-%d", userID)
}

type absBookRequestIdentity struct {
	RequestID  int64
	InstanceID string
	Format     string
	RecordID   int
	ForeignID  string
	Title      string
	UserIDs    []int64
	ASINs      []string
	ISBNs      []string
}

type absBookTagPlanItem struct {
	ItemID       string   `json:"item_id"`
	SnapshotHash string   `json:"snapshot_hash"`
	Class        string   `json:"classification"`
	UserIDs      []int64  `json:"requester_user_ids"`
	TagsToAdd    []string `json:"tags_to_add"`
	TagsToDrop   []string `json:"tags_to_remove"`
}

type absBookTagPlan struct {
	InstanceID       string                   `json:"instance_id"`
	LibraryID        string                   `json:"library_id"`
	InventoryCount   int                      `json:"inventory_count"`
	SharedCount      int                      `json:"shared_count"`
	RequestedCount   int                      `json:"requested_count"`
	UnresolvedCount  int                      `json:"unresolved_count"`
	UnmatchedRequest []absBookRequestIdentity `json:"unmatched_requests,omitempty"`
	Items            []absBookTagPlanItem     `json:"items"`
}

// buildABSBookTagPlan classifies only by exact ISBN/ASIN identities from a
// completed Chaptarr dispatch. It never falls back to title/author matching.
func buildABSBookTagPlan(instanceID, libraryID string, inventory []audiobookshelf.BookInventoryItem, identities []absBookRequestIdentity) absBookTagPlan {
	plan := absBookTagPlan{InstanceID: instanceID, LibraryID: libraryID, InventoryCount: len(inventory)}
	requesters := make(map[string][]int64)
	for _, identity := range identities {
		if len(identity.UserIDs) == 0 || (identity.Format != "ebook" && identity.Format != "audiobook") {
			plan.UnmatchedRequest = append(plan.UnmatchedRequest, identity)
			continue
		}
		matched := false
		asins := make(map[string]bool, len(identity.ASINs))
		isbns := make(map[string]bool, len(identity.ISBNs))
		for _, asin := range identity.ASINs {
			asin = strings.ToUpper(strings.TrimSpace(asin))
			if len(asin) == 10 {
				asins[asin] = true
			}
		}
		for _, isbn := range normalizeISBNSet(identity.ISBNs...) {
			isbns[isbn] = true
		}
		for _, item := range inventory {
			if !absFormatSupports(item, identity.Format) {
				continue
			}
			asin := strings.ToUpper(strings.TrimSpace(item.ASIN))
			isbn := chaptarr.NormalizeISBN(item.ISBN)
			if !asins[asin] && (isbn == "" || !isbns[isbn]) {
				continue
			}
			requesters[item.ID] = mergeUserIDs(requesters[item.ID], identity.UserIDs...)
			matched = true
		}
		if !matched {
			plan.UnmatchedRequest = append(plan.UnmatchedRequest, identity)
		}
	}
	plan.UnresolvedCount = len(plan.UnmatchedRequest)
	for _, item := range inventory {
		users := requesters[item.ID]
		entry := absBookTagPlanItem{ItemID: item.ID, SnapshotHash: absInventoryItemHash(item), UserIDs: users}
		if len(users) == 0 {
			entry.Class = "shared"
			entry.TagsToAdd = []string{absSharedLibraryTag}
			plan.SharedCount++
		} else {
			entry.Class = "requested"
			entry.TagsToDrop = []string{absSharedLibraryTag}
			for _, userID := range users {
				entry.TagsToAdd = append(entry.TagsToAdd, ABSUserTag(userID))
			}
			slices.Sort(entry.TagsToAdd)
			plan.RequestedCount++
		}
		for _, tag := range item.Tags {
			if strings.HasPrefix(tag, "cantinarr-user-") && !slices.Contains(entry.TagsToAdd, tag) {
				entry.TagsToDrop = append(entry.TagsToDrop, tag)
			}
		}
		slices.Sort(entry.TagsToDrop)
		entry.TagsToDrop = slices.Compact(entry.TagsToDrop)
		plan.Items = append(plan.Items, entry)
	}
	slices.SortFunc(plan.Items, func(a, b absBookTagPlanItem) int { return strings.Compare(a.ItemID, b.ItemID) })
	slices.SortFunc(plan.UnmatchedRequest, func(a, b absBookRequestIdentity) int {
		if a.RequestID < b.RequestID {
			return -1
		}
		if a.RequestID > b.RequestID {
			return 1
		}
		return strings.Compare(a.Format, b.Format)
	})
	return plan
}

// absInventoryItemHash binds the reviewed plan to the complete item snapshot,
// including media formats and existing tags. If an item changes after preview,
// apply refuses to use the stale classification and requires a fresh review.
func absInventoryItemHash(item audiobookshelf.BookInventoryItem) string {
	tags := slices.Clone(item.Tags)
	slices.Sort(tags)
	data, _ := json.Marshal(struct {
		ID            string   `json:"id"`
		LibraryID     string   `json:"library_id"`
		ASIN          string   `json:"asin"`
		ISBN          string   `json:"isbn"`
		Tags          []string `json:"tags"`
		HasAudio      bool     `json:"has_audio"`
		HasEbook      bool     `json:"has_ebook"`
		HasMediaFiles bool     `json:"has_media_files"`
	}{item.ID, item.LibraryID, item.ASIN, item.ISBN, tags, item.HasAudio, item.HasEbook, item.HasMediaFiles})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func absBookRecordIdentity(request absBookRequestIdentity, book *chaptarr.Book) absBookRequestIdentity {
	if book == nil {
		return request
	}
	for _, edition := range book.Editions {
		if format := normalizeRequestFormat(edition.Format); format != "" && format != "both" && format != request.Format {
			continue
		}
		if edition.ASIN != "" {
			request.ASINs = append(request.ASINs, strings.ToUpper(strings.TrimSpace(edition.ASIN)))
		}
		request.ISBNs = append(request.ISBNs, edition.ISBN13, edition.ISBN10)
	}
	request.ISBNs = normalizeISBNSet(request.ISBNs...)
	request.ASINs = slices.Compact(request.ASINs)
	return request
}

// loadABSBookRequestIdentities reads only successful Chaptarr book deliveries
// and the people currently subscribed to those formats. A record is resolved
// by its exact native Chaptarr ID/foreign ID; title text is never used.
func loadABSBookRequestIdentities(ctx context.Context, db *sql.DB, store *instance.Store) ([]absBookRequestIdentity, error) {
	rows, err := db.QueryContext(ctx, `SELECT r.id,COALESCE(r.instance_id,''),d.format,d.book_record_id,
	 COALESCE(NULLIF(d.canonical_foreign_id,''),r.foreign_id,''),r.title,r.user_id,COALESCE(r.book_format,'')
 FROM request_log r JOIN request_dispatch d ON d.request_id=r.id
 WHERE r.media_type='book' AND d.state='complete' AND r.status!='denied'
 ORDER BY r.id,d.format`)
	if err != nil {
		return nil, err
	}
	type dispatch struct {
		identity  absBookRequestIdentity
		ownerID   sql.NullInt64
		ownerForm string
	}
	var dispatches []dispatch
	for rows.Next() {
		var d dispatch
		if err = rows.Scan(&d.identity.RequestID, &d.identity.InstanceID, &d.identity.Format,
			&d.identity.RecordID, &d.identity.ForeignID, &d.identity.Title, &d.ownerID, &d.ownerForm); err != nil {
			rows.Close()
			return nil, err
		}
		d.identity.Format = normalizeRequestFormat(d.identity.Format)
		dispatches = append(dispatches, d)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	out := make([]absBookRequestIdentity, 0, len(dispatches))
	type sourceBooks struct {
		valid bool
		byID  map[int]*chaptarr.Book
		byKey map[string][]*chaptarr.Book
	}
	bookCaches := make(map[string]sourceBooks)
	for _, d := range dispatches {
		if d.identity.Format == "" {
			continue
		}
		waiters, e := db.QueryContext(ctx, `SELECT user_id,book_format FROM book_request_waiters WHERE request_id=? ORDER BY user_id`, d.identity.RequestID)
		if e != nil {
			return nil, e
		}
		for waiters.Next() {
			var userID int64
			var formats string
			if e = waiters.Scan(&userID, &formats); e != nil {
				waiters.Close()
				return nil, e
			}
			formats = normalizeRequestFormat(formats)
			if formats == "both" || formats == d.identity.Format {
				d.identity.UserIDs = mergeUserIDs(d.identity.UserIDs, userID)
			}
		}
		e = waiters.Err()
		waiters.Close()
		if e != nil {
			return nil, e
		}
		if len(d.identity.UserIDs) == 0 {
			// Pre-waiter-schema request history has no subscriber row. Retain
			// only the original owner when their recorded format is exact.
			ownerFormat := normalizeRequestFormat(d.ownerForm)
			if d.ownerID.Valid && (ownerFormat == d.identity.Format || ownerFormat == "both") {
				d.identity.UserIDs = []int64{d.ownerID.Int64}
			}
		}
		if len(d.identity.UserIDs) == 0 {
			continue
		}
		if d.identity.InstanceID == "" {
			out = append(out, d.identity)
			continue
		}
		cache, cached := bookCaches[d.identity.InstanceID]
		if !cached {
			source, sourceErr := store.Get(d.identity.InstanceID)
			if sourceErr != nil {
				return nil, sourceErr
			}
			cache = sourceBooks{byID: map[int]*chaptarr.Book{}, byKey: map[string][]*chaptarr.Book{}}
			if source != nil && source.ServiceType == "chaptarr" {
				books, readErr := chaptarr.NewClient(source.URL, source.APIKey).WithContext(ctx).GetAllBooks()
				if readErr != nil {
					return nil, fmt.Errorf("read Chaptarr book records for instance %s: %w", source.ID, readErr)
				}
				cache.valid = true
				for i := range books {
					book := &books[i]
					cache.byID[book.ID] = book
					key := book.ForeignBookID + "\x00" + normalizeRequestFormat(book.MediaType)
					cache.byKey[key] = append(cache.byKey[key], book)
				}
			}
			bookCaches[d.identity.InstanceID] = cache
		}
		if !cache.valid {
			out = append(out, d.identity)
			continue
		}
		var book *chaptarr.Book
		if d.identity.RecordID > 0 {
			book = cache.byID[d.identity.RecordID]
		} else if d.identity.ForeignID != "" {
			matches := cache.byKey[d.identity.ForeignID+"\x00"+d.identity.Format]
			if len(matches) > 1 {
				return nil, errors.New("ambiguous exact Chaptarr book identity")
			}
			if len(matches) == 1 {
				book = matches[0]
			}
		}
		if book == nil || book.ID != d.identity.RecordID && d.identity.RecordID > 0 ||
			book.ForeignBookID != "" && d.identity.ForeignID != "" && book.ForeignBookID != d.identity.ForeignID ||
			book.MediaType != "" && normalizeRequestFormat(book.MediaType) != d.identity.Format {
			out = append(out, d.identity)
			continue
		}
		out = append(out, absBookRecordIdentity(d.identity, book))
	}
	return out, nil
}

func absFormatSupports(item audiobookshelf.BookInventoryItem, format string) bool {
	switch format {
	case "ebook":
		return item.HasEbook
	case "audiobook":
		return item.HasAudio
	default:
		return false
	}
}

func normalizeRequestFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "both" {
		return format
	}
	if format == "ebook" || format == "audiobook" {
		return format
	}
	return ""
}

func mergeUserIDs(current []int64, users ...int64) []int64 {
	seen := make(map[int64]bool, len(current)+len(users))
	for _, id := range current {
		seen[id] = true
	}
	for _, id := range users {
		if id > 0 {
			seen[id] = true
		}
	}
	out := make([]int64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func normalizeISBNSet(values ...string) []string {
	seen := map[string]bool{}
	for _, raw := range values {
		if id := chaptarr.NormalizeISBN(raw); id != "" {
			seen[id] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
