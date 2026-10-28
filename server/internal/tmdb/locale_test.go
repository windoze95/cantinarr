package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestRequestLocaleCannotMutateSharedClientOrDuplicateQuery(t *testing.T) {
	c := testHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q["language"]) != 1 {
			t.Errorf("duplicate language %v", q["language"])
		}
		fmt.Fprint(w, q.Get("language"))
	}))
	params := url.Values{"language": {"ko", "de"}, "page": {"2"}}
	raw, err := c.DoGetRawContext(context.Background(), "/movie/1", params, "fr-FR")
	if err != nil || string(raw) != "fr-FR" {
		t.Fatalf("French %s %v", raw, err)
	}
	if len(params["language"]) != 2 || params.Get("language") != "ko" {
		t.Fatal("mutated caller params")
	}
	raw, err = c.DoGetRaw("/movie/1", nil)
	if err != nil || string(raw) != "en-US" {
		t.Fatalf("legacy %s %v", raw, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.DoGetRawContext(ctx, "/movie/1", nil, "fr-FR"); err == nil {
		t.Fatal("ignored canceled request")
	}
}
