package ai

import (
	"strings"
	"testing"
)

func TestDynamicContextAddsTheKidsAccountLine(t *testing.T) {
	plain := dynamicContext(ChatContext{Username: "kid", Role: "user"})
	if strings.Contains(plain, "kids account") {
		t.Fatalf("an unrestricted account got the kids line: %q", plain)
	}
	kids := dynamicContext(ChatContext{Username: "kid", Role: "user", KidsAccount: true, ContentLimits: "movies up to PG and shows up to TV-PG (US ratings); unrated titles hidden"})
	for _, want := range []string{
		"This is a kids account limited to movies up to PG and shows up to TV-PG (US ratings); unrated titles hidden.",
		"Only discuss, recommend, or request titles within those limits.",
		"never work around that",
		"even from memory",
		"age-appropriate",
	} {
		if !strings.Contains(kids, want) {
			t.Fatalf("kids line %q is missing %q", kids, want)
		}
	}
	bare := dynamicContext(ChatContext{KidsAccount: true})
	if !strings.Contains(bare, "This is a kids account.") {
		t.Fatalf("kids line without limits = %q", bare)
	}
}
