package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestLandingPageConnectsToConfiguredDemo(t *testing.T) {
	const serverURL = "https://demo.example.test"
	rendered := strings.NewReplacer(
		"__DEMO_SERVER_URL_QUERY__", url.QueryEscape(serverURL),
		"__DEMO_SERVER_URL__", serverURL,
	).Replace(demoLandingTemplate)

	for _, want := range []string{
		`<html lang="en">`,
		`Explore Cantinarr without setting up a <em>server.</em>`,
		`<code>` + serverURL + `</code>`,
		`href="cantinarr://connect?token=` + demoConnectTokenStr + `&amp;server=` + url.QueryEscape(serverURL) + `"`,
		`>Open in Cantinarr</a>`,
		`<dt>Household user</dt>`,
		`<dt>Administrator</dt>`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered landing page is missing %q", want)
		}
	}

	if strings.Contains(rendered, "__DEMO_SERVER_URL") {
		t.Fatal("rendered landing page still contains a server URL placeholder")
	}
	if strings.Count(rendered, demoConnectTokenStr) != 1 {
		t.Fatalf("connect token appears %d times, want exactly one linked credential", strings.Count(rendered, demoConnectTokenStr))
	}
}
