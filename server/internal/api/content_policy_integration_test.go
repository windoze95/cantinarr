package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Kids accounts through the real router: the admin routes, the refusal to
// make an admin a child in either direction, and the flags the app reads.
func TestContentPolicyRoutesAndProfileFlags(t *testing.T) {
	harness := newRBACRouterHarness(t, false)
	policyPath := func(id int64) string {
		return "/api/admin/users/" + strconv.FormatInt(id, 10) + "/content-policy"
	}
	body := `{"max_movie_rating":"PG","max_tv_rating":"TV-PG","rating_region":"US","block_unrated":true,"blocked_movie_genres":[27],"blocked_tv_genres":[]}`

	// The requester cannot set their own limits; the matrix test covers the
	// 403 generally, this pins the exact route.
	if rec := serveRBACRequestWithBody(harness.router, http.MethodPut, policyPath(harness.requesterID), harness.requesterToken, body); rec.Code != http.StatusForbidden {
		t.Fatalf("requester PUT: %d %s", rec.Code, rec.Body.String())
	}

	// Before: not a kids account, and the profile says so.
	if rec := serveRBACRequest(harness.router, http.MethodGet, policyPath(harness.requesterID), harness.adminToken); rec.Code != http.StatusNotFound {
		t.Fatalf("GET before: %d %s", rec.Code, rec.Body.String())
	}
	me := serveRBACRequest(harness.router, http.MethodGet, "/api/auth/me", harness.requesterToken)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"child":false`) || !strings.Contains(me.Body.String(), `"content_limits":null`) {
		t.Fatalf("me before: %d %s", me.Code, me.Body.String())
	}

	// The certification schemes the editor offers; TMDB is unconfigured in
	// this harness, so the built-in US scheme answers and says so.
	certs := serveRBACRequest(harness.router, http.MethodGet, "/api/admin/certifications", harness.adminToken)
	if certs.Code != http.StatusOK || !strings.Contains(certs.Body.String(), `"source":"builtin"`) || !strings.Contains(certs.Body.String(), `"certification":"TV-PG"`) {
		t.Fatalf("certifications: %d %s", certs.Code, certs.Body.String())
	}

	if rec := serveRBACRequestWithBody(harness.router, http.MethodPut, policyPath(harness.requesterID), harness.adminToken, body); rec.Code != http.StatusOK {
		t.Fatalf("admin PUT: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveRBACRequestWithBody(harness.router, http.MethodPut, policyPath(harness.adminID), harness.adminToken, body); rec.Code != http.StatusConflict {
		t.Fatalf("PUT on an admin: %d %s", rec.Code, rec.Body.String())
	}

	// After: the profile, the token response, and the admin list all say child.
	me = serveRBACRequest(harness.router, http.MethodGet, "/api/auth/me", harness.requesterToken)
	var profile struct {
		Child         bool `json:"child"`
		ContentLimits *struct {
			MaxMovieRating string `json:"max_movie_rating"`
			MaxTVRating    string `json:"max_tv_rating"`
			RatingRegion   string `json:"rating_region"`
		} `json:"content_limits"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if !profile.Child || profile.ContentLimits == nil || profile.ContentLimits.MaxMovieRating != "PG" || profile.ContentLimits.MaxTVRating != "TV-PG" || profile.ContentLimits.RatingRegion != "US" {
		t.Fatalf("me after: %s", me.Body.String())
	}
	list := serveRBACRequest(harness.router, http.MethodGet, "/api/admin/users", harness.adminToken)
	var users []struct {
		ID    int64 `json:"id"`
		Child bool  `json:"child"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &users); err != nil {
		t.Fatalf("users list: %v (%s)", err, list.Body.String())
	}
	for _, u := range users {
		if (u.ID == harness.requesterID) != u.Child {
			t.Fatalf("users list child flags: %s", list.Body.String())
		}
	}
	// A refreshed session carries the same flag: the token response marshals
	// the user with it.
	refresh := serveRBACRequest(harness.router, http.MethodPost, "/api/auth/refresh", harness.requesterToken)
	if refresh.Code == http.StatusOK && !strings.Contains(refresh.Body.String(), `"child":true`) {
		t.Fatalf("refresh does not carry child: %s", refresh.Body.String())
	}

	// A kids account is not promoted in place.
	promote := serveRBACRequestWithBody(harness.router, http.MethodPatch, "/api/admin/users/"+strconv.FormatInt(harness.requesterID, 10), harness.adminToken, `{"role":"admin"}`)
	if promote.Code != http.StatusConflict || !strings.Contains(promote.Body.String(), "turn off the kids account first") {
		t.Fatalf("promote child: %d %s", promote.Code, promote.Body.String())
	}

	if rec := serveRBACRequest(harness.router, http.MethodDelete, policyPath(harness.requesterID), harness.adminToken); rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveRBACRequest(harness.router, http.MethodGet, policyPath(harness.requesterID), harness.adminToken); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete: %d", rec.Code)
	}
	promote = serveRBACRequestWithBody(harness.router, http.MethodPatch, "/api/admin/users/"+strconv.FormatInt(harness.requesterID, 10), harness.adminToken, `{"role":"admin"}`)
	if promote.Code != http.StatusOK {
		t.Fatalf("promote after clearing: %d %s", promote.Code, promote.Body.String())
	}
}
