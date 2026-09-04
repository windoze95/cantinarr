package request

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// ratingsTMDB answers the kids-account rating lookups from a table; every
// other path is an unknown title.
type ratingsTMDB struct {
	movies map[int]string
	down   bool
}

func (f *ratingsTMDB) DoGetRaw(path string, _ url.Values) ([]byte, error) {
	if f.down {
		return nil, errors.New("connection refused")
	}
	var id int
	if _, err := fmt.Sscanf(path, "/movie/%d/release_dates", &id); err == nil {
		if cert, ok := f.movies[id]; ok {
			return []byte(fmt.Sprintf(`{"id":%d,"results":[{"iso_3166_1":"US","release_dates":[{"certification":%q,"type":3}]}]}`, id, cert)), nil
		}
	}
	return nil, &statusErr{code: http.StatusNotFound}
}

type statusErr struct{ code int }

func (e *statusErr) Error() string   { return fmt.Sprintf("TMDB API returned status %d", e.code) }
func (e *statusErr) HTTPStatus() int { return e.code }

type kidsRequestEnv struct {
	svc   *Service
	tmdb  *ratingsTMDB
	kid   int64
	adult int64
	admin int64
}

func newKidsRequestEnv(t *testing.T) *kidsRequestEnv {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	svc := NewService(database, instance.NewRegistry(store), nil, nil)
	fake := &ratingsTMDB{movies: map[int]string{1: "G", 2: "R"}}
	policies := contentpolicy.New(database, func() contentpolicy.RawGetter { return fake }, nil)
	svc.SetContentPolicy(policies)

	insert := func(name, role string) int64 {
		res, err := database.Exec("INSERT INTO users (username, password_hash, role) VALUES (?, '', ?)", name, role)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	env := &kidsRequestEnv{svc: svc, tmdb: fake, kid: insert("kid", "user"), adult: insert("adult", "user"), admin: insert("admin", "admin")}
	if err := policies.Store.Set(env.kid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int64{env.kid, env.adult} {
		for _, row := range []struct {
			tmdb  int
			title string
		}{{1, "Gentle"}, {2, "Grown"}} {
			if _, err := database.Exec("INSERT INTO request_log (user_id, tmdb_id, media_type, title, status) VALUES (?, ?, 'movie', ?, 'pending')", uid, row.tmdb, row.title); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := database.Exec("INSERT INTO request_log (user_id, tmdb_id, foreign_id, media_type, title, status, book_format) VALUES (?, 0, 'gr:1', 'book', 'A Book', 'pending', 'ebook')", uid); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func TestCreateMediaRequestRefusesBlockedTitlesForKidsAccounts(t *testing.T) {
	e := newKidsRequestEnv(t)
	_, err := e.svc.CreateMediaRequest(e.kid, &CreateRequest{TmdbID: 2, MediaType: "movie", Title: "Grown"})
	if !errors.Is(err, ErrTitleNotAvailable) {
		t.Fatalf("kid requesting an R title: %v, want ErrTitleNotAvailable", err)
	}
	// An allowed title passes the gate; with no Radarr configured the request
	// then fails further along, on something that is not the gate.
	if _, err := e.svc.CreateMediaRequest(e.kid, &CreateRequest{TmdbID: 1, MediaType: "movie", Title: "Gentle"}); errors.Is(err, ErrTitleNotAvailable) || errors.Is(err, ErrContentPolicyUnavailable) {
		t.Fatalf("kid requesting a G title hit the gate: %v", err)
	}
	if _, err := e.svc.CreateMediaRequest(e.adult, &CreateRequest{TmdbID: 2, MediaType: "movie", Title: "Grown"}); errors.Is(err, ErrTitleNotAvailable) {
		t.Fatalf("adult requesting an R title hit the gate: %v", err)
	}
	if _, err := e.svc.CreateMediaRequest(e.admin, &CreateRequest{TmdbID: 2, MediaType: "movie", Title: "Grown"}); errors.Is(err, ErrTitleNotAvailable) {
		t.Fatalf("admin requesting an R title hit the gate: %v", err)
	}
	// An unrated title is hidden while block_unrated is on.
	if _, err := e.svc.CreateMediaRequest(e.kid, &CreateRequest{TmdbID: 99, MediaType: "movie", Title: "Nobody"}); !errors.Is(err, ErrTitleNotAvailable) {
		t.Fatalf("kid requesting an unrated title: %v", err)
	}
	// Books are not gated.
	if _, err := e.svc.CreateMediaRequest(e.kid, &CreateRequest{ForeignID: "gr:9", MediaType: "book", Title: "A Book", BookFormat: "ebook"}); errors.Is(err, ErrTitleNotAvailable) || errors.Is(err, ErrContentPolicyUnavailable) {
		t.Fatalf("kid requesting a book hit the gate: %v", err)
	}
	// A rating that cannot be read refuses the request.
	e.tmdb.down = true
	if _, err := e.svc.CreateMediaRequest(e.kid, &CreateRequest{TmdbID: 7, MediaType: "movie", Title: "Unknown"}); !errors.Is(err, ErrContentPolicyUnavailable) {
		t.Fatalf("kid requesting while ratings are down: %v, want ErrContentPolicyUnavailable", err)
	}
}

func TestGetUserStatusReadsUnavailableForBlockedTitles(t *testing.T) {
	e := newKidsRequestEnv(t)
	allowed, err := e.svc.GetUserStatus(e.kid, 1, "movie", "")
	if err != nil || allowed.Status != StatusPending {
		t.Fatalf("kid status of an allowed pending title = %+v, %v", allowed, err)
	}
	blocked, err := e.svc.GetUserStatus(e.kid, 2, "movie", "")
	if err != nil || blocked.Status != StatusUnavailable || blocked.StatusKnown == nil || !*blocked.StatusKnown {
		t.Fatalf("kid status of a blocked pending title = %+v, %v", blocked, err)
	}
	adult, err := e.svc.GetUserStatus(e.adult, 2, "movie", "")
	if err != nil || adult.Status != StatusPending {
		t.Fatalf("adult status = %+v, %v", adult, err)
	}
	e.tmdb.down = true
	if _, err := e.svc.GetUserStatus(e.kid, 7, "movie", ""); !errors.Is(err, ErrContentPolicyUnavailable) {
		t.Fatalf("kid status while ratings are down: %v", err)
	}
}

func TestGetRequestsHidesBlockedRowsForKidsAccounts(t *testing.T) {
	e := newKidsRequestEnv(t)
	rows, err := e.svc.GetRequests(e.kid)
	if err != nil {
		t.Fatalf("GetRequests: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("kid history = %d rows (%+v), want the G movie and the book", len(rows), rows)
	}
	for _, row := range rows {
		if row.MediaType == "movie" && row.TmdbID != 1 {
			t.Fatalf("kid history carries the hidden title: %+v", row)
		}
	}
	adult, err := e.svc.GetRequests(e.adult)
	if err != nil || len(adult) != 3 {
		t.Fatalf("adult history = %d rows, %v", len(adult), err)
	}
	e.tmdb.down = true
	// The G title is cached from the first read; a fresh title that cannot
	// be rated fails the whole read rather than leaving a silent gap.
	if _, err := e.svc.db.Exec("INSERT INTO request_log (user_id, tmdb_id, media_type, title, status) VALUES (?, 8, 'movie', 'Fresh', 'pending')", e.kid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetRequests(e.kid); !errors.Is(err, ErrContentPolicyUnavailable) {
		t.Fatalf("kid history while ratings are down: %v", err)
	}
}

func TestRequestErrorStatusMapsContentPolicySentinels(t *testing.T) {
	if got := requestErrorStatus(ErrTitleNotAvailable); got != http.StatusNotFound {
		t.Fatalf("ErrTitleNotAvailable -> %d, want 404", got)
	}
	if got := requestErrorStatus(ErrContentPolicyUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("ErrContentPolicyUnavailable -> %d, want 503", got)
	}
}
