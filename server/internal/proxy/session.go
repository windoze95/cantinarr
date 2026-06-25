package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// sessionCache holds a forms-auth session cookie per instance. Chaptarr's cover
// images (/MediaCover, /MediaCoverProxy) are served by its web layer, which is
// gated behind a login session rather than the API key, so to fetch them the
// backend logs in with the instance's stored web credentials and replays the
// resulting cookie. The cookie is cached and only re-fetched when it goes stale.
type sessionCache struct {
	mu      sync.Mutex
	cookies map[string]string // instanceID -> Cookie header value
	client  *http.Client
}

func newSessionCache() *sessionCache {
	return &sessionCache{
		cookies: make(map[string]string),
		client: &http.Client{
			Timeout: 15 * time.Second,
			// Capture the login redirect ourselves instead of following it.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (sc *sessionCache) get(instanceID string) string {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.cookies[instanceID]
}

func (sc *sessionCache) set(instanceID, cookie string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.cookies[instanceID] = cookie
}

func (sc *sessionCache) clear(instanceID string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	delete(sc.cookies, instanceID)
}

// login performs a forms login and returns the Cookie header value to replay on
// later requests. A failed login redirects to /login?...loginFailed=true with no
// auth cookie; a success sets the auth cookie(s) on its redirect response.
func (sc *sessionCache) login(baseURL, username, password string) (string, error) {
	form := url.Values{
		"username":   {username},
		"password":   {password},
		"rememberMe": {"true"},
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := sc.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if strings.Contains(resp.Header.Get("Location"), "loginFailed") {
		return "", fmt.Errorf("chaptarr login failed (check the instance's web username/password)")
	}
	var parts []string
	for _, c := range resp.Cookies() {
		parts = append(parts, c.Name+"="+c.Value)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("chaptarr login returned no session cookie")
	}
	return strings.Join(parts, "; "), nil
}

// fetchCover GETs a cover URL using the instance's cached session cookie,
// logging in (or re-logging in) when there's no cookie or the cookie is stale.
// Returns a 200 response whose body the caller must close, else an error.
func (sc *sessionCache) fetchCover(inst *instance.Instance, fullURL string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cookie := sc.get(inst.ID)
		if cookie == "" {
			c, err := sc.login(inst.URL, inst.Username, inst.Password)
			if err != nil {
				return nil, err
			}
			sc.set(inst.ID, c)
			cookie = c
		}

		req, err := http.NewRequest(http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Cookie", cookie)
		resp, err := sc.client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		// Re-login only on an auth bounce (401, or the login redirect); any other
		// status (e.g. 404 for a cover that doesn't exist) is a real failure.
		authBounce := resp.StatusCode == http.StatusUnauthorized ||
			(resp.StatusCode == http.StatusFound &&
				strings.Contains(resp.Header.Get("Location"), "/login"))
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if !authBounce {
			return nil, fmt.Errorf("cover upstream returned status %d", resp.StatusCode)
		}
		sc.clear(inst.ID)
		lastErr = fmt.Errorf("cover unauthorized")
	}
	return nil, lastErr
}

// fetchWithKey GETs a cover via the API-key-authed route (works for /MediaCover
// only); used when no web credentials are configured.
func (sc *sessionCache) fetchWithKey(fullURL, apiKey string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", apiKey)
	resp, err := sc.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("cover upstream returned status %d", resp.StatusCode)
	}
	return resp, nil
}

// sanitizeCoverPath validates that a requested cover path is a Chaptarr cover
// path and nothing else, so the cover endpoint can't be turned into an open
// proxy (with a live session cookie) to arbitrary instance routes.
func sanitizeCoverPath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if strings.Contains(p, "..") {
		return "", false
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", false
	}
	// Both /MediaCover/... and /MediaCoverProxy/... share this prefix.
	if !strings.HasPrefix(strings.ToLower(u.Path), "/mediacover") {
		return "", false
	}
	return p, true
}
