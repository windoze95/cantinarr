package instance

import (
	"bytes"
	"context"
	"database/sql"
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
	"github.com/windoze95/cantinarr-server/internal/hardcover"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

type testHardcoverOAuth struct {
	poll      func(context.Context, string) (hardcover.OAuthTokens, error)
	refresh   func(context.Context, string) (hardcover.OAuthTokens, error)
	polls     atomic.Int32
	refreshes atomic.Int32
}

func (p *testHardcoverOAuth) Begin(context.Context) (hardcover.DeviceCode, error) {
	return hardcover.DeviceCode{DeviceCode: "secret-device-code", UserCode: "ABCD-EFGH", VerificationURI: "https://hardcover.app/link", ExpiresIn: 900, Interval: 5}, nil
}
func (p *testHardcoverOAuth) Poll(ctx context.Context, code string) (hardcover.OAuthTokens, error) {
	p.polls.Add(1)
	return p.poll(ctx, code)
}
func (p *testHardcoverOAuth) Refresh(ctx context.Context, token string) (hardcover.OAuthTokens, error) {
	p.refreshes.Add(1)
	return p.refresh(ctx, token)
}

func oauthEnv(t *testing.T) (*Store, *HardcoverManager, *testHardcoverOAuth, string, *time.Time) {
	t.Helper()
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	p := &testHardcoverOAuth{}
	p.poll = func(context.Context, string) (hardcover.OAuthTokens, error) {
		return hardcover.OAuthTokens{AccessToken: "secret-access", RefreshToken: "secret-refresh", ExpiresAt: now.Add(time.Hour)}, nil
	}
	p.refresh = func(context.Context, string) (hardcover.OAuthTokens, error) {
		return hardcover.OAuthTokens{AccessToken: "rotated-access", RefreshToken: "rotated-refresh", ExpiresAt: now.Add(time.Hour)}, nil
	}
	m := newHardcoverManager(s)
	m.now = func() time.Time { return now }
	m.provider = p
	m.verify = func(context.Context, string) error { return nil }
	return s, m, p, id, &now
}
func connectOAuth(t *testing.T, m *HardcoverManager, id string, now *time.Time) HardcoverDeviceResult {
	t.Helper()
	flow, err := m.Begin(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	result, err := m.Check(context.Background(), 1, id, flow.FlowID)
	if err != nil || result.Status != "connected" {
		t.Fatalf("connect: %v %v", result, err)
	}
	return result
}

func TestHardcoverOAuthPersistenceAndIsolation(t *testing.T) {
	s, m, _, id, now := oauthEnv(t)
	if err := s.SetHardcoverToken(id, "previous-token"); err != nil {
		t.Fatal(err)
	}
	flow, err := m.Begin(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if token, _ := s.HardcoverToken(id); token != "previous-token" {
		t.Fatal("begin replaced existing credential")
	}
	*now = now.Add(5 * time.Second)
	connected, err := m.Check(context.Background(), 1, id, flow.FlowID)
	if err != nil || connected.Status != "connected" {
		t.Fatalf("connect: %v %v", connected, err)
	}
	var stored string
	if err := s.db.QueryRow("SELECT credentials FROM hardcover_connections").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "enc:v1:") || strings.Contains(stored, "secret") {
		t.Fatal("credential not encrypted")
	}
	if token, _ := s.HardcoverToken(id); token != "" {
		t.Fatal("OAuth left an active API token")
	}
	state, err := s.HardcoverState(id)
	if err != nil || state.Method != "oauth" || !state.Configured || state.Reconnect {
		t.Fatalf("state %v %v", state, err)
	}
	wire, _ := json.Marshal(connected)
	for _, secret := range []string{"secret-device-code", "secret-access", "secret-refresh"} {
		if bytes.Contains(wire, []byte(secret)) {
			t.Fatal("flow response leaked a credential")
		}
	}
	// A restarted manager has no device flows but reads the encrypted connection.
	restarted := newHardcoverManager(NewStore(s.db, s.cipher))
	restarted.now = m.now
	if _, err := restarted.Check(context.Background(), 1, id, flow.FlowID); !errors.Is(err, errHardcoverFlowMissing) {
		t.Fatal("flow survived restart")
	}
	if token, err := restarted.Token(context.Background(), id, ""); err != nil || token != "secret-access" {
		t.Fatalf("stored connection did not survive: %v", err)
	}
	other := mkInstance(t, s, "chaptarr", "Other")
	if err := s.applyHardcoverConnection(context.Background(), id, state.ConnectionID, HardcoverApplyTarget{InstanceID: other}); err != nil {
		t.Fatal(err)
	}
	replacement := connectOAuth(t, m, id, now)
	if replacement.ConnectionID == state.ConnectionID {
		t.Fatal("replacement modified shared credential in place")
	}
	sibling, _ := s.HardcoverState(other)
	if sibling.ConnectionID != state.ConnectionID {
		t.Fatal("replacement changed sibling")
	}
	if err := s.ClearHardcoverToken(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.hardcoverCredential(replacement.ConnectionID); err != sql.ErrNoRows {
		t.Fatal("orphan replacement credential survived")
	}
	if _, err := s.hardcoverCredential(sibling.ConnectionID); err != nil {
		t.Fatal("disconnect deleted shared credential")
	}
	if err := s.Delete(other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.hardcoverCredential(sibling.ConnectionID); err != sql.ErrNoRows {
		t.Fatal("last instance deletion left credentials")
	}
}

func TestHardcoverDevicePollingAndTerminalStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		status   string
		interval int64
	}{
		{"pending", hardcover.ErrPending, "pending", 5}, {"slow down", hardcover.ErrSlowDown, "pending", 10},
		{"denied", hardcover.ErrDenied, "denied", 5}, {"expired", hardcover.ErrExpired, "expired", 5},
		{"transient", hardcover.ErrProvider, "pending", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, m, p, id, now := oauthEnv(t)
			_ = s.SetHardcoverToken(id, "old")
			p.poll = func(context.Context, string) (hardcover.OAuthTokens, error) { return hardcover.OAuthTokens{}, tc.err }
			flow, err := m.Begin(context.Background(), 1, id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Check(context.Background(), 2, id, flow.FlowID); !errors.Is(err, errHardcoverFlowMissing) {
				t.Fatal("wrong admin could poll")
			}
			if _, err := m.Cancel(2, id, flow.FlowID); !errors.Is(err, errHardcoverFlowMissing) {
				t.Fatal("wrong admin could cancel")
			}
			if _, err := m.Check(context.Background(), 1, "other", flow.FlowID); !errors.Is(err, errHardcoverFlowMissing) {
				t.Fatal("wrong instance could poll")
			}
			_, _ = m.Check(context.Background(), 1, id, flow.FlowID)
			if p.polls.Load() != 0 {
				t.Fatal("polled before interval")
			}
			*now = now.Add(5 * time.Second)
			result, err := m.Check(context.Background(), 1, id, flow.FlowID)
			if err != nil || result.Status != tc.status || result.Interval != tc.interval {
				t.Fatalf("result %v %v", result, err)
			}
			_, _ = m.Check(context.Background(), 1, id, flow.FlowID)
			if p.polls.Load() != 1 {
				t.Fatal("rapid checks reached provider")
			}
			if token, _ := s.HardcoverToken(id); token != "old" {
				t.Fatal("unsuccessful flow replaced token")
			}
			if tc.status == "pending" {
				*now = now.Add(20 * time.Minute)
				result, err = m.Check(context.Background(), 1, id, flow.FlowID)
				if err != nil || result.Status != "expired" || p.polls.Load() != 1 {
					t.Fatal("expired flow reached provider")
				}
			}
		})
	}
}

func TestHardcoverCancelledOrSupersededFlowCannotCommit(t *testing.T) {
	for _, action := range []string{"cancel", "disconnect", "new flow", "token replacement", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, m, p, id, now := oauthEnv(t)
			started, release := make(chan struct{}), make(chan struct{})
			p.poll = func(context.Context, string) (hardcover.OAuthTokens, error) {
				close(started)
				<-release
				return hardcover.OAuthTokens{AccessToken: "late", RefreshToken: "late-refresh", ExpiresAt: now.Add(time.Hour)}, nil
			}
			f, err := m.Begin(context.Background(), 1, id)
			if err != nil {
				t.Fatal(err)
			}
			*now = now.Add(5 * time.Second)
			done := make(chan HardcoverDeviceResult, 1)
			go func() { r, _ := m.Check(context.Background(), 1, id, f.FlowID); done <- r }()
			<-started
			switch action {
			case "cancel":
				_, err = m.Cancel(1, id, f.FlowID)
			case "disconnect":
				err = s.ClearHardcoverToken(id)
			case "new flow":
				_, err = m.Begin(context.Background(), 2, id)
			case "token replacement":
				err = s.SetHardcoverToken(id, "newer")
			case "delete":
				err = s.Delete(id)
			}
			if err != nil {
				t.Fatal(err)
			}
			close(release)
			result := <-done
			if result.Status == "connected" {
				t.Fatal("late provider reply replaced newer intent")
			}
			var count int
			_ = s.db.QueryRow("SELECT COUNT(*) FROM hardcover_connections").Scan(&count)
			if count != 0 {
				t.Fatal("stale flow created credential")
			}
		})
	}
}

func TestHardcoverDeviceRetainsIssuedPairUntilVerificationAndStorageSucceed(t *testing.T) {
	s, m, p, id, now := oauthEnv(t)
	verifyFails := true
	m.verify = func(context.Context, string) error {
		if verifyFails {
			return hardcover.ErrProvider
		}
		return nil
	}
	flow, _ := m.Begin(context.Background(), 1, id)
	*now = now.Add(5 * time.Second)
	result, _ := m.Check(context.Background(), 1, id, flow.FlowID)
	if result.Status != "pending" {
		t.Fatal(result)
	}
	verifyFails = false
	// A real SQLite write failure occurs after verification, without mocking the store.
	_, err := s.db.Exec(`CREATE TRIGGER fail_hardcover_insert BEFORE INSERT ON hardcover_connections BEGIN SELECT RAISE(FAIL,'busy'); END`)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	result, _ = m.Check(context.Background(), 1, id, flow.FlowID)
	if result.Status != "pending" {
		t.Fatal(result)
	}
	_, _ = s.db.Exec("DROP TRIGGER fail_hardcover_insert")
	*now = now.Add(5 * time.Second)
	result, _ = m.Check(context.Background(), 1, id, flow.FlowID)
	if result.Status != "connected" || p.polls.Load() != 1 {
		t.Fatalf("issued device pair replayed or lost: %v %d", result, p.polls.Load())
	}
}

func TestHardcoverDeviceRetainsIssuedPairAcrossStateReadFailure(t *testing.T) {
	s, m, p, id, now := oauthEnv(t)
	database := s.db
	unavailable, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = unavailable.Close()
	defer func() { s.db = database }()
	p.poll = func(context.Context, string) (hardcover.OAuthTokens, error) {
		// The provider consumes the code just before local storage goes away.
		s.db = unavailable
		return hardcover.OAuthTokens{AccessToken: "issued-once", RefreshToken: "issued-refresh", ExpiresAt: now.Add(time.Hour)}, nil
	}
	flow, err := m.Begin(context.Background(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	if _, err := m.Check(context.Background(), 1, id, flow.FlowID); !errors.Is(err, errHardcoverStorage) {
		t.Fatalf("expected temporary storage failure: %v", err)
	}
	s.db = database
	*now = now.Add(5 * time.Second)
	result, err := m.Check(context.Background(), 1, id, flow.FlowID)
	if err != nil || result.Status != "connected" || p.polls.Load() != 1 {
		t.Fatalf("issued pair lost or code replayed: status=%s polls=%d error=%v", result.Status, p.polls.Load(), err)
	}
}

func TestHardcoverSharedRefreshSerializationAndRotationRecovery(t *testing.T) {
	s, m, p, id, now := oauthEnv(t)
	connected := connectOAuth(t, m, id, now)
	other := mkInstance(t, s, "chaptarr", "Other")
	if err := s.applyHardcoverConnection(context.Background(), id, connected.ConnectionID, HardcoverApplyTarget{InstanceID: other}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Hour)
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := id
			if i%2 == 0 {
				target = other
			}
			token, err := m.Token(context.Background(), target, "")
			if err != nil || token != "rotated-access" {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if failures.Load() != 0 || p.refreshes.Load() != 1 {
		t.Fatalf("concurrent refreshes=%d failures=%d", p.refreshes.Load(), failures.Load())
	}
	// Provider rotation succeeds but persistence temporarily fails. The next
	// call must save the exact successor, never replay its consumed predecessor.
	*now = now.Add(2 * time.Hour)
	p.refresh = func(_ context.Context, token string) (hardcover.OAuthTokens, error) {
		if token != "rotated-refresh" {
			t.Error("replayed predecessor")
		}
		return hardcover.OAuthTokens{AccessToken: "successor", RefreshToken: "successor-refresh", ExpiresAt: now.Add(time.Hour)}, nil
	}
	fail := true
	m.persist = func(id string, c hardcoverCredential) error {
		if fail {
			return errors.New("disk unavailable")
		}
		return s.updateHardcoverCredential(id, c)
	}
	if _, err := m.Token(context.Background(), id, ""); !errors.Is(err, errHardcoverStorage) {
		t.Fatal(err)
	}
	if _, err := m.Token(context.Background(), other, ""); !errors.Is(err, errHardcoverStorage) {
		t.Fatal(err)
	}
	fail = false
	if token, err := m.Token(context.Background(), other, ""); err != nil || token != "successor" {
		t.Fatalf("recovery: %v", err)
	}
	if p.refreshes.Load() != 2 {
		t.Fatal("recovery repeated the provider refresh")
	}
	c, err := s.hardcoverCredential(connected.ConnectionID)
	if err != nil || c.Tokens.RefreshToken != "successor-refresh" {
		t.Fatal("successor not stored")
	}
	// Missing replacement refresh token preserves the last valid one.
	*now = now.Add(2 * time.Hour)
	p.refresh = func(context.Context, string) (hardcover.OAuthTokens, error) {
		return hardcover.OAuthTokens{AccessToken: "access-only", ExpiresAt: now.Add(time.Hour)}, nil
	}
	if _, err := m.Token(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	c, _ = s.hardcoverCredential(connected.ConnectionID)
	if c.Tokens.RefreshToken != "successor-refresh" {
		t.Fatal("omitted refresh token erased credential")
	}
}

func TestHardcoverRefreshFailuresPreserveCredentials(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(map[bool]string{false: "temporary", true: "invalid grant"}[permanent], func(t *testing.T) {
			s, m, p, id, now := oauthEnv(t)
			connected := connectOAuth(t, m, id, now)
			*now = now.Add(2 * time.Hour)
			p.refresh = func(context.Context, string) (hardcover.OAuthTokens, error) {
				if permanent {
					return hardcover.OAuthTokens{}, hardcover.ErrInvalidGrant
				}
				return hardcover.OAuthTokens{}, hardcover.ErrProvider
			}
			_, err := m.Token(context.Background(), id, "")
			if permanent && !errors.Is(err, errHardcoverReconnect) {
				t.Fatal(err)
			}
			if !permanent && !errors.Is(err, hardcover.ErrProvider) {
				t.Fatal(err)
			}
			c, err := s.hardcoverCredential(connected.ConnectionID)
			if err != nil || c.Reconnect != permanent || c.Tokens.RefreshToken != "secret-refresh" {
				t.Fatalf("credential lost: %v", err)
			}
			_, _ = m.Token(context.Background(), id, "")
			if p.refreshes.Load() != 1 {
				t.Fatal("retried invalid grant or ignored transient backoff")
			}
		})
	}
}

func TestHardcoverWrongKeyDoesNotReplaceOAuthCredential(t *testing.T) {
	s, m, p, id, now := oauthEnv(t)
	connected := connectOAuth(t, m, id, now)
	var before, after string
	_ = s.db.QueryRow("SELECT credentials FROM hardcover_connections WHERE id=?", connected.ConnectionID).Scan(&before)
	wrong, _ := secrets.NewCipher(bytes.Repeat([]byte{99}, 32))
	s.cipher = wrong
	if _, err := m.Token(context.Background(), id, ""); err == nil {
		t.Fatal("wrong key accepted")
	}
	_ = s.db.QueryRow("SELECT credentials FROM hardcover_connections WHERE id=?", connected.ConnectionID).Scan(&after)
	if before != after || p.refreshes.Load() != 0 {
		t.Fatal("wrong key altered credential")
	}
}

func TestHardcoverApplyPartialResultsAndExpectedSource(t *testing.T) {
	s, m, _, id, now := oauthEnv(t)
	connected := connectOAuth(t, m, id, now)
	good := mkInstance(t, s, "chaptarr", "Good")
	changed := mkInstance(t, s, "chaptarr", "Changed")
	_ = s.SetHardcoverToken(changed, "newer")
	wrongType := mkInstance(t, s, "radarr", "Movies")
	h := NewHandler(s, nil)
	h.hardcover = m
	body, _ := json.Marshal(map[string]any{"connection_id": connected.ConnectionID, "instances": []HardcoverApplyTarget{{InstanceID: good}, {InstanceID: changed}, {InstanceID: "deleted"}, {InstanceID: wrongType}}})
	rec := do(t, newHardcoverRouter(h), http.MethodPost, "/instances/"+id+"/hardcover/apply", string(body))
	var results struct {
		Results []HardcoverApplyResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil || rec.Code != 200 || len(results.Results) != 4 {
		t.Fatalf("apply: %s", rec.Body.String())
	}
	for i, result := range results.Results {
		if result.Applied != (i == 0) {
			t.Fatal(results)
		}
	}
	if token, _ := s.HardcoverToken(changed); token != "newer" {
		t.Fatal("apply overwrote a concurrent change")
	}
	_ = s.ClearHardcoverToken(id)
	rec = do(t, newHardcoverRouter(h), http.MethodPost, "/instances/"+id+"/hardcover/apply", string(body))
	if rec.Code != http.StatusConflict {
		t.Fatal("stale source accepted")
	}
}

func TestHardcoverRoutesRefuseNonAdmin(t *testing.T) {
	s, m, _, id, _ := oauthEnv(t)
	h := NewHandler(s, nil)
	h.hardcover = m
	for _, path := range []string{"", "/device/begin", "/device/opaque", "/apply"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			req := httptest.NewRequest(method, "/instances/"+id+"/hardcover"+path, strings.NewReader(`{"token":"secret"}`))
			req = req.WithContext(context.WithValue(req.Context(), auth.ClaimsKey, &auth.Claims{UserID: 1, Role: auth.RoleUser}))
			rec := httptest.NewRecorder()
			newHardcoverRouter(h).ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("nonadmin %s %s: %d", method, path, rec.Code)
			}
		}
	}
}
