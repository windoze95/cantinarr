package update

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		name            string
		current, latest string
		want            bool
	}{
		{"patch bump", "1.2.3", "1.2.4", true},
		{"minor bump", "1.2.3", "1.3.0", true},
		{"major bump", "1.2.3", "2.0.0", true},
		{"same version", "1.2.3", "1.2.3", false},
		{"older patch", "1.2.3", "1.2.2", false},
		{"older minor", "1.2.3", "1.1.9", false},
		{"v prefix tolerated", "v1.2.3", "v1.2.4", true},
		{"mixed prefix", "1.2.3", "v1.2.4", true},
		{"prerelease suffix ignored", "1.2.3", "1.2.4-rc1", true},
		{"two-component latest", "1.2.0", "1.3", true},
		{"current not comparable", "dev", "1.2.3", false},
		{"latest not comparable", "1.2.3", "latest", false},
		{"latest tag build", "latest", "1.2.3", false},
		{"pr build", "pr-42", "1.2.3", false},
		{"empty current", "", "1.2.3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNewer(tc.current, tc.latest); got != tc.want {
				t.Fatalf("isNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestNewCheckerDisabledForNonSemver(t *testing.T) {
	c := NewChecker("dev", false)
	if !c.disabled {
		t.Fatal("checker should be disabled for a non-semver running version")
	}
	st := c.Status()
	if st.Available {
		t.Fatal("a dev build must never report an update available")
	}
	if st.Current != "dev" {
		t.Fatalf("Status().Current = %q, want %q", st.Current, "dev")
	}
}

func TestNewCheckerDisabledExplicitly(t *testing.T) {
	c := NewChecker("1.2.3", true)
	if !c.disabled {
		t.Fatal("checker should be disabled when disable=true")
	}
	if got := c.Status(); got.Available {
		t.Fatal("a disabled checker must never report an update available")
	}
}

// testChecker returns an enabled checker pointed at a fake GitHub API.
func testChecker(t *testing.T, current string, handler http.Handler) *Checker {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewChecker(current, false)
	c.apiBase = srv.URL
	return c
}

func TestFetchLatestParsesRelease(t *testing.T) {
	c := testChecker(t, "1.2.3", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/windoze95/cantinarr/releases/latest" {
			t.Errorf("path = %q, want /repos/windoze95/cantinarr/releases/latest", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got != "cantinarr-update-check" {
			t.Errorf("User-Agent = %q, want cantinarr-update-check", got)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want application/vnd.github+json", got)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://github.com/windoze95/cantinarr/releases/tag/v1.3.0"}`))
	}))

	tag, url, err := c.fetchLatest()
	if err != nil {
		t.Fatalf("fetchLatest: %v", err)
	}
	if tag != "v1.3.0" {
		t.Fatalf("tag = %q, want v1.3.0", tag)
	}
	if url != "https://github.com/windoze95/cantinarr/releases/tag/v1.3.0" {
		t.Fatalf("url = %q, want the release page", url)
	}
}

func TestRefreshGatesAvailabilityOnSemver(t *testing.T) {
	release := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://example.test/release"}`))
	})

	older := testChecker(t, "1.2.3", release)
	older.refresh()
	st := older.Status()
	if !st.Available {
		t.Fatal("1.2.3 -> v1.3.0 should report an update available")
	}
	if st.Latest != "1.3.0" {
		t.Fatalf("Latest = %q, want the v prefix trimmed to 1.3.0", st.Latest)
	}
	if st.Current != "1.2.3" || st.URL != "https://example.test/release" {
		t.Fatalf("status = %+v", st)
	}

	current := testChecker(t, "1.3.0", release)
	current.refresh()
	if st := current.Status(); st.Available {
		t.Fatalf("1.3.0 -> v1.3.0 must not report an update: %+v", st)
	}
}

func TestRefreshKeepsZeroStatusOnMalformedJSON(t *testing.T) {
	c := testChecker(t, "1.2.3", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name": not-json`))
	}))

	c.refresh()

	st := c.Status()
	if st.Available || st.Latest != "" {
		t.Fatalf("failed refresh changed the cached status: %+v", st)
	}
	if st.Current != "1.2.3" {
		t.Fatalf("Current = %q, want 1.2.3", st.Current)
	}
	until := time.Until(c.nextCheck)
	if until <= 0 || until > errorBackoff {
		t.Fatalf("next check in %v, want the %v error backoff (not the %v success interval)", until, errorBackoff, checkInterval)
	}
}

func TestRefreshKeepsPreviousResultOnHTTPError(t *testing.T) {
	var failing atomic.Bool
	c := testChecker(t, "1.2.3", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "rate limited", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.3.0","html_url":"https://example.test/release"}`))
	}))

	c.refresh()
	if st := c.Status(); !st.Available || st.Latest != "1.3.0" {
		t.Fatalf("seed refresh did not populate the cache: %+v", st)
	}

	failing.Store(true)
	c.refresh()

	st := c.Status()
	if !st.Available || st.Latest != "1.3.0" {
		t.Fatalf("failed refresh dropped the previous result: %+v", st)
	}
	until := time.Until(c.nextCheck)
	if until <= 0 || until > errorBackoff {
		t.Fatalf("next check in %v, want the %v error backoff", until, errorBackoff)
	}
}

func TestFetchLatestRejectsEmptyTag(t *testing.T) {
	c := testChecker(t, "1.2.3", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"","html_url":"https://example.test"}`))
	}))

	if _, _, err := c.fetchLatest(); err == nil {
		t.Fatal("fetchLatest accepted a release without a tag")
	} else if !strings.Contains(err.Error(), "empty tag_name") {
		t.Fatalf("error = %v, want empty tag_name message", err)
	}
}

func TestDisabledCheckerNeverContactsServer(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewChecker("dev", false) // non-semver build: disabled
	c.apiBase = srv.URL
	if st := c.Status(); st.Available {
		t.Fatalf("disabled checker reported an update: %+v", st)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("disabled checker made %d requests, want 0", got)
	}
}

func TestParseVersion(t *testing.T) {
	if _, ok := parseVersion("1.2.3"); !ok {
		t.Fatal("1.2.3 should parse")
	}
	if _, ok := parseVersion("garbage"); ok {
		t.Fatal("garbage should not parse")
	}
	v, ok := parseVersion("v2.5")
	if !ok || v.major != 2 || v.minor != 5 || v.patch != 0 {
		t.Fatalf("v2.5 parsed as %+v ok=%v", v, ok)
	}
}
