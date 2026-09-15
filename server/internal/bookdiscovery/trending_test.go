package bookdiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

// fakeSource stands in for Hardcover: it records every call and the token it
// was handed, and answers a fixed list or error.
type fakeSource struct {
	mu     sync.Mutex
	calls  atomic.Int32
	tokens []string
	limits []int
	books  []hardcover.Book
	err    error
	block  chan struct{} // when non-nil, Trending waits on it (coalescing test)
}

func (f *fakeSource) Trending(_ context.Context, token string, limit int) ([]hardcover.Book, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.tokens = append(f.tokens, token)
	f.limits = append(f.limits, limit)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return f.books, f.err
}

type env struct {
	store    *instance.Store
	source   *fakeSource
	h        *TrendingHandler
	granted  string // chaptarr instance requester 1 holds
	other    string // chaptarr instance nobody but the admin may read
	notBooks string // a radarr instance
}

func newEnv(t *testing.T) *env {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, user := range []struct{ name, role string }{{"requester", "user"}, {"ungranted", "user"}, {"admin", "admin"}} {
		if _, err := database.Exec("INSERT INTO users(username,password_hash,role) VALUES (?,'',?)", user.name, user.role); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := instance.NewStore(database, cipher)
	create := func(kind, name string) string {
		inst := &instance.Instance{ServiceType: kind, Name: name, URL: "http://not-contacted.invalid", APIKey: "k"}
		if err := store.Create(inst); err != nil {
			t.Fatal(err)
		}
		return inst.ID
	}
	e := &env{store: store, source: &fakeSource{}, granted: create("chaptarr", "Books"), other: create("chaptarr", "Other"), notBooks: create("radarr", "Movies")}
	if err := store.SetUserGrants(1, map[string][]string{"chaptarr": {e.granted}}); err != nil {
		t.Fatal(err)
	}
	e.source.books = []hardcover.Book{{ID: 446681, ForeignID: "hc:446681", Title: "Dungeon Crawler Carl", Authors: []string{"Matt Dinniman"}, ISBN13s: []string{"9798228815889"}}}
	e.h = NewTrendingHandler(store, e.source)
	return e
}

func (e *env) get(t *testing.T, userID int64, role, instanceID string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/discover/books/trending"
	if instanceID != "" {
		url += "?instance_id=" + instanceID
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if role != "" {
		req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID, Role: role}))
	}
	rec := httptest.NewRecorder()
	e.h.Trending(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestTrendingNotConnectedSaysSoWithoutDialingHardcover(t *testing.T) {
	e := newEnv(t)
	rec := e.get(t, 1, "user", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["connected"] != false || body["instance_id"] != e.granted {
		t.Fatalf("body = %v, want connected:false on the granted instance", body)
	}
	if books, ok := body["books"].([]any); !ok || len(books) != 0 {
		t.Fatalf("books = %v, want an empty list (not null)", body["books"])
	}
	if e.source.calls.Load() != 0 {
		t.Fatal("Hardcover must not be dialed without a token")
	}
}

func TestTrendingUsesTheInstanceTokenAndCaches(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetHardcoverToken(e.granted, "tok-books"); err != nil {
		t.Fatal(err)
	}
	rec := e.get(t, 1, "user", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["connected"] != true {
		t.Fatalf("connected = %v", body["connected"])
	}
	books := body["books"].([]any)
	if len(books) != 1 || books[0].(map[string]any)["foreign_id"] != "hc:446681" {
		t.Fatalf("books = %v", books)
	}
	if strings.Contains(rec.Body.String(), "tok-books") {
		t.Fatal("response leaks the token")
	}
	if got := e.source.tokens; len(got) != 1 || got[0] != "tok-books" {
		t.Fatalf("hardcover saw tokens %v, want the instance's", got)
	}
	if e.source.limits[0] != TrendingLimit || TrendingLimit != 50 {
		t.Fatalf("limit = %d, want %d (50)", e.source.limits[0], TrendingLimit)
	}

	// Second read within the TTL is served from cache; after the TTL it refetches.
	e.get(t, 1, "user", "")
	if e.source.calls.Load() != 1 {
		t.Fatalf("calls after cached read = %d, want 1", e.source.calls.Load())
	}
	base := time.Now()
	e.h.now = func() time.Time { return base.Add(trendingTTL + time.Second) }
	e.get(t, 1, "user", "")
	if e.source.calls.Load() != 2 {
		t.Fatalf("calls after TTL = %d, want 2", e.source.calls.Load())
	}

	// A token change drops the cache immediately.
	e.h.Invalidate(e.granted)
	e.get(t, 1, "user", "")
	if e.source.calls.Load() != 3 {
		t.Fatalf("calls after invalidate = %d, want 3", e.source.calls.Load())
	}
}

func TestTrendingCoalescesConcurrentMisses(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetHardcoverToken(e.granted, "tok"); err != nil {
		t.Fatal(err)
	}
	e.source.block = make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rec := e.get(t, 1, "user", ""); rec.Code != 200 {
				t.Errorf("status = %d", rec.Code)
			}
		}()
	}
	// Let every goroutine reach the handler before releasing Hardcover.
	deadline := time.Now().Add(2 * time.Second)
	for e.source.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(e.source.block)
	wg.Wait()
	if e.source.calls.Load() != 1 {
		t.Fatalf("hardcover calls = %d, want 1 for five concurrent readers", e.source.calls.Load())
	}
}

func TestTrendingAuthorization(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{e.granted, e.other} {
		if err := e.store.SetHardcoverToken(id, "tok-"+id); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name     string
		user     int64
		role     string
		instance string
		want     int
	}{
		{"anonymous", 0, "", "", 401},
		{"requester default instance", 1, "user", "", 200},
		{"requester explicit granted instance", 1, "user", e.granted, 200},
		{"requester sibling instance", 1, "user", e.other, 403},
		{"requester radarr instance", 1, "user", e.notBooks, 403},
		{"ungranted requester", 2, "user", "", 403},
		{"ungranted requester names an instance", 2, "user", e.granted, 403},
		{"admin explicit instance", 3, "admin", e.other, 200},
		{"admin radarr instance", 3, "admin", e.notBooks, 403},
		{"admin unknown instance", 3, "admin", "chaptarr-nope", 403},
	}
	for _, tc := range cases {
		rec := e.get(t, tc.user, tc.role, tc.instance)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	// Each instance was read with its own token, never the sibling's.
	for _, tok := range e.source.tokens {
		if tok != "tok-"+e.granted && tok != "tok-"+e.other {
			t.Fatalf("unexpected token %q", tok)
		}
	}
	if rec := e.get(t, 1, "user", e.other); strings.Contains(rec.Body.String(), "connected") {
		t.Fatalf("a requester must not learn whether a sibling instance is connected: %s", rec.Body.String())
	}
}

func TestTrendingHardcoverFailuresAreBlindnessNotAbsence(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetHardcoverToken(e.granted, "tok"); err != nil {
		t.Fatal(err)
	}
	e.source.err = errors.New("dial tcp: timeout")
	rec := e.get(t, 1, "user", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "reach Hardcover") {
		t.Fatalf("unreachable = %d %s, want 502", rec.Code, rec.Body.String())
	}
	e.source.err = hardcover.ErrUnauthorized
	rec = e.get(t, 1, "user", "")
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no longer accepts") {
		t.Fatalf("revoked token = %d %s, want 502 naming the token", rec.Code, rec.Body.String())
	}
	// Failures are never cached: the next read tries again.
	e.source.err = nil
	rec = e.get(t, 1, "user", "")
	if rec.Code != 200 {
		t.Fatalf("recovery = %d %s", rec.Code, rec.Body.String())
	}
}

type trendingSourceFunc func(context.Context, string, int) ([]hardcover.Book, error)

func (f trendingSourceFunc) Trending(ctx context.Context, token string, limit int) ([]hardcover.Book, error) {
	return f(ctx, token, limit)
}

func TestTrendingResolvesCredentialsOnlyOnMiss(t *testing.T) {
	e := newEnv(t)
	if err := e.store.SetHardcoverToken(e.granted, "stored"); err != nil {
		t.Fatal(err)
	}
	resolutions := 0
	e.h.SetCredentialResolver(func(context.Context, string, string) (string, error) { resolutions++; return "resolved", nil })
	for i := 0; i < 3; i++ {
		if rec := e.get(t, 1, "user", e.granted); rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
	}
	if resolutions != 1 || e.source.calls.Load() != 1 {
		t.Fatalf("cache hit renewed credential: %d %d", resolutions, e.source.calls.Load())
	}
	e.h.Invalidate(e.granted)
	_ = e.get(t, 1, "user", e.granted)
	if resolutions != 2 {
		t.Fatal("cache invalidation did not resolve again")
	}
}

func TestTrendingOldInflightCannotRepopulateAfterConnectionChange(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "disconnect"}[disconnect], func(t *testing.T) {
			e := newEnv(t)
			_ = e.store.SetHardcoverToken(e.granted, "old")
			started, release := make(chan struct{}), make(chan struct{})
			e.h.source = trendingSourceFunc(func(_ context.Context, token string, _ int) ([]hardcover.Book, error) {
				if token == "old" {
					close(started)
					<-release
				}
				return []hardcover.Book{{Title: token}}, nil
			})
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { result <- e.get(t, 1, "user", e.granted) }()
			<-started
			if disconnect {
				_ = e.store.ClearHardcoverToken(e.granted)
			} else {
				_ = e.store.SetHardcoverToken(e.granted, "new")
			}
			e.h.Invalidate(e.granted)
			close(release)
			rec := <-result
			if rec.Code != 200 || strings.Contains(rec.Body.String(), `"title":"old"`) {
				t.Fatalf("stale result: %s", rec.Body.String())
			}
			if disconnect && !strings.Contains(rec.Body.String(), `"connected":false`) {
				t.Fatal("disconnect kept feed connected")
			}
			rec = e.get(t, 1, "user", e.granted)
			if strings.Contains(rec.Body.String(), `"title":"old"`) {
				t.Fatal("old inflight repopulated cache")
			}
		})
	}
}

func TestTrendingWaiterCanCancelWithoutInterruptingFetch(t *testing.T) {
	e := newEnv(t)
	_ = e.store.SetHardcoverToken(e.granted, "token")
	started, release := make(chan struct{}), make(chan struct{})
	e.h.source = trendingSourceFunc(func(context.Context, string, int) ([]hardcover.Book, error) {
		close(started)
		<-release
		return []hardcover.Book{{Title: "ready"}}, nil
	})
	done := make(chan struct{})
	go func() { defer close(done); _, _, _, _ = e.h.trending(context.Background(), e.granted) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := e.h.trending(ctx, e.granted); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	<-done
	if rec := e.get(t, 1, "user", e.granted); rec.Code != 200 {
		t.Fatal("cancelled waiter damaged fetch")
	}
}

func TestTrendingRetriesRejectedOAuthAccessTokenOnce(t *testing.T) {
	e := newEnv(t)
	_ = e.store.SetHardcoverToken(e.granted, "metadata-only")
	resolutions := 0
	e.h.SetCredentialResolver(func(_ context.Context, _ string, rejected string) (string, error) {
		resolutions++
		if rejected == "old" {
			return "new", nil
		}
		return "old", nil
	})
	calls := 0
	e.h.source = trendingSourceFunc(func(_ context.Context, token string, _ int) ([]hardcover.Book, error) {
		calls++
		if token == "old" {
			return nil, hardcover.ErrUnauthorized
		}
		return []hardcover.Book{{Title: "renewed"}}, nil
	})
	if rec := e.get(t, 1, "user", e.granted); rec.Code != 200 || !strings.Contains(rec.Body.String(), "renewed") {
		t.Fatal(rec.Body.String())
	}
	if resolutions != 2 || calls != 2 {
		t.Fatalf("refresh retry: %d %d", resolutions, calls)
	}
}
