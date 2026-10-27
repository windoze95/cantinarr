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
		`<link rel="icon" href="/static/favicon.png?v=4" type="image/png">`,
		`<img src="/static/lockup.svg" alt="Cantinarr" width="147" height="40" style="border-radius:0" />`,
		`<h1 id="demo-title">Try Cantinarr.</h1>`,
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
		path        string
		contentType string
		want        []byte
	}{
		{path: "/static/lockup.svg", contentType: "image/svg+xml", want: demoLockup},
		{path: "/static/logo.png", contentType: "image/png", want: demoLogo},
		{path: "/static/favicon.png", contentType: "image/png", want: demoFavicon},
		{path: "/static/fonts/schibsted-var.woff2", contentType: "font/woff2", want: demoFont},
		{path: "/static/fonts/SchibstedGrotesk-OFL.txt", contentType: "text/plain; charset=utf-8", want: demoFontLicense},
	} {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			res := httptest.NewRecorder()

			buildRouter().ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", test.path, res.Code, http.StatusOK)
			}
			if got := res.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("GET %s Content-Type = %q, want %q", test.path, got, test.contentType)
			}
			if !bytes.Equal(res.Body.Bytes(), test.want) {
				t.Fatalf("GET %s did not return the embedded brand asset", test.path)
			}
		})
	}
}
