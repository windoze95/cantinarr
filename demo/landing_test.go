package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
		`<link rel="icon" href="/static/favicon.png?v=2" type="image/png">`,
		`<img class="brand-mark" src="/static/logo.png?v=2" alt="" width="30" height="30">`,
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

func TestLandingPageServesBrandAssets(t *testing.T) {
	for _, test := range []struct {
		path string
		want []byte
	}{
		{path: "/static/logo.png", want: demoLogo},
		{path: "/static/favicon.png", want: demoFavicon},
	} {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			res := httptest.NewRecorder()

			buildRouter().ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", test.path, res.Code, http.StatusOK)
			}
			if got := res.Header().Get("Content-Type"); got != "image/png" {
				t.Fatalf("GET %s Content-Type = %q, want image/png", test.path, got)
			}
			if !bytes.Equal(res.Body.Bytes(), test.want) {
				t.Fatalf("GET %s did not return the embedded brand asset", test.path)
			}
		})
	}
}
