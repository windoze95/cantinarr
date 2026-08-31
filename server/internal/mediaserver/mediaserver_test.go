package mediaserver

import "testing"

func TestValidUsernameMirrorsJellyfinRule(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"alice", true},
		{"Alice Smith", true},
		{"Ana María", true},
		{"a.b_c@d+e", true},
		{"o'brien-2", true},
		{"日本語", true},
		{"", false},
		{" alice", false},
		{"alice ", false},
		{"al\tice", false},
		{"alice/1", false},
		{"alice!", false},
		{"al😀ice", false},
		{"a,b", false},
	}
	for _, tc := range cases {
		if got := ValidUsername(tc.name); got != tc.want {
			t.Errorf("ValidUsername(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
