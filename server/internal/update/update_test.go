package update

import "testing"

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
