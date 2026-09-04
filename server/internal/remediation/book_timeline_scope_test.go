package remediation

import (
	"encoding/json"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/mcp"
)

// The scrub in scopeReadToolInput deletes book_id like every other identity
// key; without a re-injection case the tool was allow-listed but unreachable —
// every call refused with "needs the issue's book_id". This pins the round
// trip: the issue's own book id goes back in, and a model-supplied one never
// survives.
func TestBookTimelineIsScopedToTheIssuesBook(t *testing.T) {
	issue := &Issue{MediaType: "book", BookID: 42, AuthorID: 7, InstanceID: "books-a"}
	scoped, err := scopeReadToolInput(issue, "get_book_timeline",
		json.RawMessage(`{"media_type":"movie","book_id":999,"author_id":999}`))
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(scoped, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["media_type"] != "book" || got["book_id"] != float64(42) {
		t.Fatalf("scoped input = %v, want the issue's own book identity", got)
	}
	if _, present := got["author_id"]; present {
		t.Errorf("author_id = %v survived scoping; the tool takes only book_id", got["author_id"])
	}
}

func TestBookTimelineIsOnTheAgentsReadAllowList(t *testing.T) {
	if !readToolAllowSet["get_book_timeline"] {
		t.Fatal("the agent cannot call get_book_timeline")
	}
	// It is also the read that proves a book repair against the record's own
	// receipts, so it has to satisfy the conclusion gate's "you looked at live
	// state" check the way get_episode_timeline does for TV.
	if !isVerificationRead("get_book_timeline", &mcp.ToolResult{Text: "Book: The Wrong Tome\nFiles (1):"}) {
		t.Fatal("get_book_timeline does not count as a verification read")
	}
	if isVerificationRead("get_book_timeline", &mcp.ToolResult{Text: "Chaptarr is not configured."}) {
		t.Fatal("an unconfigured-service reply counted as verification")
	}
}

// get_media_file_details had the same unreachable-when-scrubbed shape: it
// requires tmdb_id, which the scrub deleted with no case to put it back.
func TestMediaFileDetailsIsScopedToTheIssuesTitle(t *testing.T) {
	issue := &Issue{MediaType: "tv", TmdbID: 615, TvdbID: 73871, SeasonNumber: 11}
	scoped, err := scopeReadToolInput(issue, "get_media_file_details",
		json.RawMessage(`{"media_type":"movie","tmdb_id":999,"season_number":4}`))
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(scoped, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["media_type"] != "tv" || got["tmdb_id"] != float64(615) || got["season_number"] != float64(11) {
		t.Fatalf("scoped input = %v, want the issue's own identity", got)
	}
}

// The music arm of get_media_file_details is keyed by album_id, which the
// scrub deletes like every other identity key; the re-injection is what makes
// the tool reachable on a music issue at all.
func TestMediaFileDetailsIsScopedToTheIssuesAlbum(t *testing.T) {
	issue := &Issue{MediaType: "music", AuthorID: 3, BookID: 77}
	scoped, err := scopeReadToolInput(issue, "get_media_file_details",
		json.RawMessage(`{"media_type":"movie","tmdb_id":999,"album_id":5}`))
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(scoped, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["media_type"] != "music" || got["album_id"] != float64(77) {
		t.Fatalf("scoped input = %v, want the issue's own album", got)
	}
	if _, leaked := got["tmdb_id"]; leaked {
		t.Fatalf("a model-supplied tmdb_id survived the scrub: %v", got)
	}
}
