package discordnotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/httpx"
	"github.com/windoze95/cantinarr-server/internal/httpx/httpxtest"
	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const testWebhook = "https://discord.com/api/webhooks/123456/secret_test_token"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T) *Service {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cipher, err := secrets.NewCipher(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(database, cipher, func() string { return "https://cantinarr.example" })
	s.now = func() time.Time { return time.Unix(1800000000, 0) }
	if _, err = database.Exec(`INSERT INTO users(id,username,password_hash) VALUES(1,'requester','')`); err != nil {
		t.Fatal(err)
	}
	return s
}
func seed(t *testing.T, s *Service, kind string) int64 {
	t.Helper()
	r, err := s.db.Exec(`INSERT INTO request_log(user_id,tmdb_id,media_type,title,book_format) VALUES(1,550,?,'A title','both')`, kind)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id
}
func enable(t *testing.T, s *Service) {
	t.Helper()
	if err := s.Save(true, testWebhook, false); err != nil {
		t.Fatal(err)
	}
}
func status(t *testing.T, s *Service, id int64) Delivery {
	t.Helper()
	out, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Recent {
		if d.RequestID == id {
			return d
		}
	}
	t.Fatalf("no receipt for %d: %+v", id, out)
	return Delivery{}
}
func transport(s *Service, code int, body string, calls *int) {
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		*calls++
		return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func TestSettingsSecretAndSafeTest(t *testing.T) {
	s := fixture(t)
	out, err := s.Get()
	if err != nil || out.Enabled || out.HasWebhook {
		t.Fatalf("fresh: %+v %v", out, err)
	}
	if err = s.Save(true, "", false); err == nil {
		t.Fatal("enabled without URL")
	}
	calls := 0
	transport(s, 200, `{"id":"message"}`, &calls)
	before := 0
	s.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&before)
	d, err := s.Test(context.Background(), testWebhook)
	if err != nil || d.Status != "sent" {
		t.Fatalf("test: %+v %v", d, err)
	}
	after := 0
	s.db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&after)
	if before != after {
		t.Fatal("draft test saved settings")
	}
	enable(t, s)
	var stored string
	s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, settingsKey).Scan(&stored)
	if !secrets.IsEncrypted(stored) || strings.Contains(stored, "secret_test_token") {
		t.Fatal("plaintext webhook stored")
	}
	if err = s.Save(false, "", false); err != nil {
		t.Fatal(err)
	}
	out, _ = s.Get()
	encoded, _ := json.Marshal(out)
	if out.Enabled || !out.HasWebhook || strings.Contains(string(encoded), "secret_test_token") {
		t.Fatalf("settings: %s", encoded)
	}
	if _, err = s.Test(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(false, "", true); err != nil {
		t.Fatal(err)
	}
	out, _ = s.Get()
	if out.HasWebhook || out.Enabled {
		t.Fatal("remove did not clear settings")
	}
}

func TestWebhookValidation(t *testing.T) {
	for _, raw := range []string{testWebhook, strings.Replace(testWebhook, "/api/", "/api/v10/", 1), strings.Replace(testWebhook, "discord.com", "discordapp.com", 1)} {
		if _, err := normalizeWebhook(raw); err != nil {
			t.Errorf("valid webhook rejected: %v", err)
		}
	}
	for _, raw := range []string{"", strings.Replace(testWebhook, "https:", "http:", 1), strings.Replace(testWebhook, "discord.com", "discord.com.evil.test", 1), strings.Replace(testWebhook, "discord.com", "user@discord.com", 1), strings.Replace(testWebhook, "discord.com", "discord.com:444", 1), testWebhook + "?thread_id=1", testWebhook + "#fragment", testWebhook + "/github", testWebhook + "%2fextra", "https://127.0.0.1/api/webhooks/1/token"} {
		if _, err := normalizeWebhook(raw); err == nil {
			t.Error("invalid webhook accepted")
		}
	}
}

func TestMessageSafetyAndLinks(t *testing.T) {
	for _, kind := range []string{"movie", "tv", "book", "music"} {
		a := requestAlert{Title: strings.Repeat("[bad](https://evil.test) @everyone\n", 300), Username: "<@123> _name_", MediaType: kind, BookFormat: "both", RequiresApproval: true}
		data := message(a, "https://cantinarr.example/base/")
		embed := data["embeds"].([]any)[0].(map[string]any)
		if embed["url"] != "https://cantinarr.example/base/approvals" {
			t.Fatal(embed["url"])
		}
		if len([]rune(embed["description"].(string))) > 1000 || strings.Contains(embed["description"].(string), "[bad](") {
			t.Fatal("unsafe or oversized text")
		}
		if len(data["allowed_mentions"].(map[string]any)["parse"].([]string)) != 0 {
			t.Fatal("mentions allowed")
		}
		for _, external := range []string{"", "http://u:p@internal", "https://server/?token=secret"} {
			if _, ok := message(a, external)["embeds"].([]any)[0].(map[string]any)["url"]; ok {
				t.Fatal("unexpected outward link")
			}
		}
		a.RequiresApproval = false
		if got := message(a, "https://cantinarr.example")["embeds"].([]any)[0].(map[string]any)["url"]; got != "https://cantinarr.example/" {
			t.Fatal(got)
		}
	}
}

func TestDurableQueueDedupAndConfigurationChanges(t *testing.T) {
	s := fixture(t)
	id := seed(t, s, "movie")
	s.RequestCreated(id, true)
	out, _ := s.Get()
	if len(out.Recent) != 0 {
		t.Fatal("disabled integration queued alert")
	}
	enable(t, s)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.RequestCreated(id, true) }()
	}
	wg.Wait()
	out, _ = s.Get()
	if len(out.Recent) != 1 {
		t.Fatalf("dedupe: %+v", out)
	}
	// New service over the same database proves the queued work is durable.
	restarted := NewService(s.db, s.cipher, s.externalURL)
	restarted.now = s.now
	calls := 0
	transport(restarted, 200, `{"id":"1"}`, &calls)
	if err := restarted.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.deliverOne(context.Background())
	s.RequestCreated(id, true)
	if calls != 1 || status(t, s, id).Status != "sent" {
		t.Fatal("duplicate or unconfirmed send")
	}
	id2 := seed(t, s, "book")
	s.RequestCreated(id2, false)
	if err := s.Save(true, strings.Replace(testWebhook, "123456", "987654", 1), false); err != nil {
		t.Fatal(err)
	}
	if status(t, s, id2).Status != "cancelled" {
		t.Fatal("old destination backlog retained")
	}
	id3 := seed(t, s, "music")
	s.RequestCreated(id3, false)
	s.Save(false, "", false)
	if status(t, s, id3).Status != "cancelled" {
		t.Fatal("disabled destination backlog retained")
	}
	s.Save(true, "", false)
	if status(t, s, id3).Status != "cancelled" {
		t.Fatal("enable replayed history")
	}
}

func TestRateLimitPersistsAndAppliesToWholeDestination(t *testing.T) {
	if got := retryAt(time.Unix(100, 900000000), 3*time.Second); got != 104 {
		t.Fatalf("retry shortened Discord's delay: %d", got)
	}
	s := fixture(t)
	enable(t, s)
	id := seed(t, s, "movie")
	id2 := seed(t, s, "tv")
	s.RequestCreated(id, true)
	s.RequestCreated(id2, false)
	calls := 0
	transport(s, 429, `{"retry_after":2.5}`, &calls)
	if err := s.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := status(t, s, id); got.Status != "pending" || got.Attempts != 1 || got.NextAttemptAt != s.now().Unix()+3 {
		t.Fatalf("rate limit: %+v", got)
	}
	restarted := NewService(s.db, s.cipher, nil)
	restarted.now = s.now
	transport(restarted, 200, `{"id":"2"}`, &calls)
	restarted.deliverOne(context.Background())
	if calls != 1 {
		t.Fatal("destination cooldown lost at restart")
	}
	base := s.now()
	restarted.now = func() time.Time { return base.Add(3 * time.Second) }
	restarted.deliverOne(context.Background())
	restarted.deliverOne(context.Background())
	if calls != 3 || status(t, s, id).Status != "sent" || status(t, s, id2).Status != "sent" {
		t.Fatal("retry did not recover")
	}
}

func TestEnqueueDoesNotWaitForDiscord(t *testing.T) {
	s := fixture(t)
	enable(t, s)
	first := seed(t, s, "movie")
	second := seed(t, s, "music")
	s.RequestCreated(first, false)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	s.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"1"}`))}, nil
	})
	go func() { done <- s.deliverOne(context.Background()) }()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sender did not start")
	}
	enqueued := make(chan struct{})
	go func() { s.RequestCreated(second, false); close(enqueued) }()
	select {
	case <-enqueued:
	case <-time.After(5 * time.Second):
		t.Fatal("new request waited for Discord")
	}
	if status(t, s, second).Status != "pending" {
		t.Fatal("second request lost")
	}
}

func TestDeliveryVerdictsDoNotReplayAmbiguousPosts(t *testing.T) {
	for _, tc := range []struct {
		code       int
		body, want string
	}{{200, `{"id":"1"}`, "sent"}, {204, "", "unconfirmed"}, {200, "bad", "unconfirmed"}, {500, "secret_test_token", "unconfirmed"}, {400, "bad", "failed"}, {401, "", "failed"}, {403, "", "failed"}, {404, "", "failed"}, {302, "", "failed"}, {0, "", "unconfirmed"}} {
		t.Run(fmt.Sprint(tc.code, tc.body), func(t *testing.T) {
			s := fixture(t)
			enable(t, s)
			id := seed(t, s, "movie")
			s.RequestCreated(id, true)
			calls := 0
			transport(s, tc.code, tc.body, &calls)
			if tc.code == 0 {
				s.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New(testWebhook) })
			}
			if err := s.deliverOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			s.deliverOne(context.Background())
			got := status(t, s, id)
			if got.Status != tc.want || calls != 1 || strings.Contains(got.Detail, "secret_test_token") {
				t.Fatalf("verdict: %+v calls %d", got, calls)
			}
		})
	}
}

func TestRetryBudgetAndInterruptedClaim(t *testing.T) {
	s := fixture(t)
	enable(t, s)
	id := seed(t, s, "movie")
	s.RequestCreated(id, true)
	s.db.Exec(`UPDATE discord_notifications SET attempts=4 WHERE request_id=?`, id)
	calls := 0
	transport(s, 429, `{"retry_after":1}`, &calls)
	s.deliverOne(context.Background())
	if got := status(t, s, id); got.Status != "failed" || got.Attempts != 5 {
		t.Fatalf("budget: %+v", got)
	}
	id2 := seed(t, s, "movie")
	s.RequestCreated(id2, true)
	s.db.Exec(`UPDATE discord_notifications SET status='sending',updated_at=? WHERE request_id=?`, s.now().Unix()-31, id2)
	s.deliverOne(context.Background())
	if status(t, s, id2).Status != "unconfirmed" || calls != 1 {
		t.Fatal("interrupted post was replayed")
	}
	id3 := seed(t, s, "movie")
	s.RequestCreated(id3, true)
	s.db.Exec(`UPDATE discord_notifications SET created_at=? WHERE request_id=?`, s.now().Unix()-86400, id3)
	s.deliverOne(context.Background())
	if status(t, s, id3).Status != "failed" {
		t.Fatal("expired alert retained")
	}
}

func TestClientUsesProxyAndNeverFollowsRedirects(t *testing.T) {
	proxy := httpxtest.New(t)
	httpx.SetOutboundProxy(proxy.URL())
	t.Cleanup(func() { httpx.SetOutboundProxy(nil) })
	proxy.SetResponse(200, `{"id":"message"}`)
	// The shared fake proxy supports HTTP targets only. Production validation
	// requires HTTPS; this isolated client probe proves its transport class.
	got := send(context.Background(), newClient(), "http://discord.test/api/webhooks/1/token", map[string]any{"content": "test"})
	if got.Status != "sent" || len(proxy.Hits()) != 1 {
		t.Fatalf("proxy: %+v", got)
	}
	httpx.SetOutboundProxy(nil)
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; w.Write([]byte(`{"id":"1"}`)) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	got = send(context.Background(), newClient(), redirect.URL, map[string]any{"content": "test"})
	if targetCalls != 0 || got.Status != "failed" {
		t.Fatal("redirect followed")
	}
}

func TestHandlerNeverReturnsSecretOrTestsRealRequestData(t *testing.T) {
	s := fixture(t)
	enable(t, s)
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(string(data), "requester") || !strings.Contains(string(data), "test notification") || r.URL.Query().Get("wait") != "true" {
			t.Error("unsafe test payload")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"1"}`))}, nil
	})
	for _, method := range []string{"GET", "PUT", "POST", "DELETE"} {
		path := "/api/admin/discord-notifications"
		body := `{"enabled":true}`
		if method == "POST" {
			path += "/test"
			body = `{}`
		}
		w := httptest.NewRecorder()
		s.Handler(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret_test_token") {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"webhook_url":"` + testWebhook + `","oops":1}`, `{} {}`, strings.Repeat("x", 9000)} {
		w := httptest.NewRecorder()
		s.Handler(w, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
		if w.Code != 400 || strings.Contains(w.Body.String(), "secret_test_token") {
			t.Fatal("unsafe malformed-body response")
		}
	}
}
