// Package update reports whether a newer Cantinarr release is available.
//
// It compares the running server version against the latest published GitHub
// release. The check is best-effort and never blocks the request path: callers
// get the last cached result immediately, and a stale cache triggers a
// background refresh. The checker makes no outbound request at all unless the
// running version is a real semver tag, so dev, "latest", and PR builds never
// contact GitHub.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// repoSlug is the GitHub "owner/repo" whose releases are checked.
const repoSlug = "windoze95/cantinarr"

const (
	// checkInterval is how long a successful result is cached.
	checkInterval = 12 * time.Hour
	// errorBackoff is how long to wait before retrying after a failed check, so
	// a GitHub outage can't turn every /api/config call into a fetch attempt.
	errorBackoff = 1 * time.Hour
)

// Status is the update state surfaced to admin clients.
type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
}

// Checker holds the running version and a cached view of the latest release.
// The zero value is not usable; construct one with NewChecker.
type Checker struct {
	current  string
	disabled bool
	client   *http.Client

	mu        sync.Mutex
	cached    Status
	nextCheck time.Time
	checking  bool
}

// NewChecker returns a checker for the given running version. It is disabled
// (never contacts GitHub) when disable is true or when current is not a
// comparable semver — there is nothing to compare a "dev"/"latest"/PR build to.
func NewChecker(current string, disable bool) *Checker {
	_, ok := parseVersion(current)
	return &Checker{
		current:  current,
		disabled: disable || !ok,
		client:   &http.Client{Timeout: 15 * time.Second},
		cached:   Status{Current: current},
	}
}

// Status returns the latest known update status without blocking. When the
// cached result is stale it kicks off a background refresh and returns the
// current (possibly stale) snapshot.
func (c *Checker) Status() Status {
	if c == nil {
		return Status{}
	}
	if c.disabled {
		return Status{Current: c.current}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checking && time.Now().After(c.nextCheck) {
		c.checking = true
		go c.refresh()
	}
	return c.cached
}

// refresh fetches the latest release and updates the cache. It runs in its own
// goroutine and swallows all errors (best-effort): on failure the previous
// cache is kept and the next attempt is delayed by errorBackoff.
func (c *Checker) refresh() {
	tag, url, err := c.fetchLatest()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.checking = false
	if err != nil {
		c.nextCheck = time.Now().Add(errorBackoff)
		return
	}
	c.cached = Status{
		Current:   c.current,
		Latest:    strings.TrimPrefix(tag, "v"),
		Available: isNewer(c.current, tag),
		URL:       url,
	}
	c.nextCheck = time.Now().Add(checkInterval)
}

// fetchLatest asks GitHub for the latest published (non-prerelease) release.
func (c *Checker) fetchLatest() (tag, url string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	endpoint := "https://api.github.com/repos/" + repoSlug + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	// GitHub requires a User-Agent and recommends an explicit API version.
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cantinarr-update-check")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github releases/latest: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}
	if body.TagName == "" {
		return "", "", fmt.Errorf("github releases/latest: empty tag_name")
	}
	return body.TagName, body.HTMLURL, nil
}

type semver struct{ major, minor, patch int }

// parseVersion parses a lenient "vMAJOR.MINOR.PATCH" (the v is optional, and
// any -prerelease/+build suffix is ignored). It returns ok=false for anything
// non-numeric like "dev", "latest", or "pr-42".
func parseVersion(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return semver{}, false
	}
	var out semver
	for i, part := range strings.Split(s, ".") {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return semver{}, false
		}
		switch i {
		case 0:
			out.major = n
		case 1:
			out.minor = n
		case 2:
			out.patch = n
		}
	}
	return out, true
}

// isNewer reports whether latest is a strictly greater semver than current. If
// either side is not comparable, it returns false (never nag on ambiguity).
func isNewer(current, latest string) bool {
	c, ok1 := parseVersion(current)
	l, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	if l.major != c.major {
		return l.major > c.major
	}
	if l.minor != c.minor {
		return l.minor > c.minor
	}
	return l.patch > c.patch
}
