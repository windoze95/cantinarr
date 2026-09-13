package request

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
)

func TestTVCardStatusSelectedLibraryAndAuthorization(t *testing.T) {
	s, uid, admin, lab := newCorrectionLab(t)
	primary := s.effectiveArrInstanceID(uid, "tv")
	var siblingReads atomic.Int32
	sibling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siblingReads.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer sibling.Close()
	_, err := s.db.Exec(`INSERT INTO service_instances(id,service_type,name,url,api_key) SELECT 'tv-sibling',service_type,'Sibling',?,api_key FROM service_instances WHERE id=?`, sibling.URL, primary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMediaRequest(uid, tvRequest(299939, "all")); err != nil {
		t.Fatal(err)
	}
	read := func(userID int64, id, query string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/requests/"+id+"/status?media_type=tv&"+query, nil)
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("tmdb_id", id)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
		if userID > 0 {
			r = r.WithContext(context.WithValue(r.Context(), auth.ClaimsKey, &auth.Claims{UserID: userID}))
		}
		w := httptest.NewRecorder()
		NewHandler(s).GetStatus(w, r)
		return w
	}
	for _, userID := range []int64{uid, admin} {
		w := read(userID, "299939", "instance_id="+primary+"&include_instance_statuses=false")
		var status StatusResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &status) != nil {
			t.Fatalf("card status: %d %s", w.Code, w.Body.String())
		}
		if status.Status != StatusRequested || status.Match.TVDBID != 389492 || status.Seasons[0].SeasonNumber != 1 || len(status.InstanceStatuses) != 0 {
			t.Fatalf("card projection: %+v", status)
		}
	}
	if siblingReads.Load() != 0 {
		t.Fatal("card status queried a sibling")
	}
	if w := read(admin, "299939", "instance_id="+primary); w.Code != http.StatusOK || siblingReads.Load() == 0 {
		t.Fatal("legacy/default status no longer reads sibling libraries")
	}
	if w := read(uid, "113988", "instance_id="+primary+"&include_instance_statuses=false"); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	} else {
		var status StatusResponse
		_ = json.Unmarshal(w.Body.Bytes(), &status)
		if status.Status != StatusUnavailable {
			t.Fatalf("Lizzie monitoring leaked into Dahmer: %+v", status)
		}
	}
	for _, tc := range []struct {
		uid   int64
		query string
		want  int
	}{
		{0, "include_instance_statuses=false", http.StatusUnauthorized},
		{uid, "instance_id=tv-sibling&include_instance_statuses=false", http.StatusForbidden},
		{uid, "include_instance_statuses=garbage", http.StatusBadRequest},
	} {
		if w := read(tc.uid, "299939", tc.query); w.Code != tc.want {
			t.Fatalf("%s: got %d want %d", tc.query, w.Code, tc.want)
		}
	}
	policies := contentpolicy.New(s.db, func() contentpolicy.RawGetter { return &ratingsTMDB{} }, nil)
	s.SetContentPolicy(policies)
	if err := policies.Store.Set(uid, contentpolicy.Policy{MaxMovieRating: "PG", MaxTVRating: "TV-PG", RatingRegion: "US", BlockUnrated: true}); err != nil {
		t.Fatal(err)
	}
	w := read(uid, "299939", "instance_id="+primary+"&include_instance_statuses=false")
	var hidden StatusResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &hidden) != nil || hidden.Match != nil || hidden.Status != StatusUnavailable {
		t.Fatalf("card bypassed content policy: %d %s", w.Code, w.Body.String())
	}
	lab.mu.Lock()
	mutations := lab.mutations
	lab.episodesDown = true
	lab.mu.Unlock()
	w = read(admin, "299939", "instance_id="+primary+"&include_instance_statuses=false")
	var unknown StatusResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &unknown) != nil || unknown.StatusKnown == nil || *unknown.StatusKnown {
		t.Fatalf("unreadable episodes claimed absence: %s", w.Body.String())
	}
	lab.mu.Lock()
	defer lab.mu.Unlock()
	if lab.mutations != mutations {
		t.Fatal("status mutated the library")
	}
}
