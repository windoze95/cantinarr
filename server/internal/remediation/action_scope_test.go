package remediation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/db"
)

// ISS-041: Book remediation rejects title-level actions without durable identity.
func TestBookIssueRejectsActionsWithoutDurableBookIdentity(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	res, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, instance_id, download_id, arr_queue_id)
		 VALUES ('auto', 'open', 'book', 0, 'Book', 'chaptarr-1', 'download-1', 77)`,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	unsafe := []struct {
		kind   ActionKind
		params string
	}{
		{ActionGrabRelease, `{"media_type":"book","guid":"g","indexer_id":1,"queue_id_to_replace":77,"release_title":"Book.Release","size":1,"protocol":"usenet","indexer":"Test"}`},
		{ActionTriggerSearch, `{"media_type":"book","book_id":123}`},
		{ActionRescan, `{"media_type":"book","author_id":456}`},
	}
	for _, tc := range unsafe {
		canonical, err := validateActionParams(tc.kind, json.RawMessage(tc.params))
		if err != nil {
			t.Fatalf("validate %s params: %v", tc.kind, err)
		}
		err = validateActionScopeWith(database, issueID, tc.kind, canonical)
		if err == nil || !strings.Contains(err.Error(), "authoritative book id") {
			t.Errorf("%s scope error = %v", tc.kind, err)
		}
	}

	canonical, err := validateActionParams(ActionRemediateQueue,
		json.RawMessage(`{"media_type":"book","queue_id":77,"action":"remove"}`))
	if err != nil {
		t.Fatalf("validate queue params: %v", err)
	}
	if err := validateActionScopeWith(database, issueID, ActionRemediateQueue, canonical); err != nil {
		t.Fatalf("exact book queue action rejected: %v", err)
	}
}

func TestTVDBOnlyIssueCannotAcceptModelSelectedTMDBMutation(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	res, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, tvdb_id, title)
		 VALUES ('user', 'open', 'tv', 0, 4242, 'TVDB-only show')`,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	for _, tc := range []struct {
		kind   ActionKind
		params string
	}{
		{ActionTriggerSearch, `{"media_type":"tv","tmdb_id":999}`},
		{ActionRescan, `{"media_type":"tv","tmdb_id":999}`},
	} {
		canonical, err := validateActionParams(tc.kind, json.RawMessage(tc.params))
		if err != nil {
			t.Fatalf("validate %s params: %v", tc.kind, err)
		}
		err = validateActionScopeWith(database, issueID, tc.kind, canonical)
		if err == nil || !strings.Contains(err.Error(), "no authoritative tmdb_id") {
			t.Errorf("%s scope error = %v", tc.kind, err)
		}
	}
}

func TestEpisodeIssueRequiresEpisodeScopedTriggerSearch(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	res, err := database.Exec(
		`INSERT INTO issues (source, status, media_type, tmdb_id, title, season_number, episode_number)
		 VALUES ('user', 'open', 'tv', 42, 'Show', 2, 7)`,
	)
	if err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issueID, _ := res.LastInsertId()

	for _, tc := range []struct {
		params string
		ok     bool
	}{
		{`{"media_type":"tv","tmdb_id":42,"season":2,"episode":7}`, true},
		{`{"media_type":"tv","tmdb_id":42,"season":2}`, false},
		{`{"media_type":"tv","tmdb_id":42,"season":2,"episode":8}`, false},
	} {
		canonical, err := validateActionParams(ActionTriggerSearch, json.RawMessage(tc.params))
		if err == nil {
			err = validateActionScopeWith(database, issueID, ActionTriggerSearch, canonical)
		}
		if tc.ok && err != nil {
			t.Errorf("exact episode search rejected: %v", err)
		}
		if !tc.ok && err == nil {
			t.Errorf("broader/wrong episode search accepted: %s", tc.params)
		}
	}
}

func TestTriggerSearchRejectsNegativeTVSeason(t *testing.T) {
	if _, err := validateActionParams(ActionTriggerSearch,
		json.RawMessage(`{"media_type":"tv","tmdb_id":42,"season":-1}`)); err == nil || !strings.Contains(err.Error(), "season") {
		t.Fatalf("negative TV season validation error = %v", err)
	}
	if _, err := validateActionParams(ActionTriggerSearch,
		json.RawMessage(`{"media_type":"tv","tmdb_id":42,"season":0,"episode":1}`)); err != nil {
		t.Fatalf("exact S00 special rejected: %v", err)
	}
}
