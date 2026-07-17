package tautulli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

const testAPIKey = "TAUTULLI_KEY_SENTINEL"

// successEnvelope wraps a data payload in Tautulli's response envelope.
func successEnvelope(data string) string {
	return `{"response":{"result":"success","message":null,"data":` + data + `}}`
}

// TestCallSendsCmdAndAPIKey pins Tautulli's command-style dialect: every call
// is a GET to {base}/api/v2?apikey=...&cmd=..., with any trailing slash on the
// configured base URL trimmed.
func TestCallSendsCmdAndAPIKey(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, successEnvelope(`{"pms_name":"Cantina Plex","pms_version":"1.41.0"}`))
	}))
	t.Cleanup(srv.Close)

	info, err := NewClient(srv.URL+"/", testAPIKey).GetServerInfo()
	if err != nil {
		t.Fatalf("GetServerInfo: %v", err)
	}
	if info.PMSName != "Cantina Plex" || info.PMSVersion != "1.41.0" {
		t.Errorf("server info = %+v, want pms_name/pms_version mapped", info)
	}
	if gotPath != "/api/v2" {
		t.Errorf("path = %s, want /api/v2 (trailing base slash must be trimmed)", gotPath)
	}
	if gotQuery.Get("apikey") != testAPIKey {
		t.Errorf("apikey = %q, want %q", gotQuery.Get("apikey"), testAPIKey)
	}
	if gotQuery.Get("cmd") != "get_server_info" {
		t.Errorf("cmd = %q, want get_server_info", gotQuery.Get("cmd"))
	}
}

// TestGetActivityToleratesStringNumbers pins the flexInt mapping: Tautulli
// encodes many numeric fields as strings (sometimes empty or junk), and the
// client must map every session anyway.
func TestGetActivityToleratesStringNumbers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("cmd"); got != "get_activity" {
			t.Errorf("cmd = %q, want get_activity", got)
		}
		_, _ = io.WriteString(w, successEnvelope(`{
			"stream_count": "2",
			"total_bandwidth": 12000,
			"sessions": [
				{"user":"julian","title":"Heat","full_title":"Heat (1995)","player":"Living Room TV","product":"Plex for Apple TV","state":"playing","progress_percent":"42","quality_profile":"1080p","transcode_decision":"direct play","bandwidth":"8000.9"},
				{"user":"dex","title":"Andor","full_title":"Andor - S02E03","player":"phone","product":"Plex for iOS","state":"paused","progress_percent":"","quality_profile":"720p","transcode_decision":"transcode","bandwidth":null}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	activity, err := NewClient(srv.URL, testAPIKey).GetActivity()
	if err != nil {
		t.Fatalf("GetActivity: %v", err)
	}
	if activity.StreamCount != 2 || activity.TotalBandwidth != 12000 {
		t.Errorf("counts = (%d, %d), want (2, 12000)", activity.StreamCount, activity.TotalBandwidth)
	}
	if len(activity.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(activity.Sessions))
	}
	first := activity.Sessions[0]
	if first.User != "julian" || first.State != "playing" || first.ProgressPercent != 42 || first.Bandwidth != 8000 {
		t.Errorf("session[0] = %+v, want string-encoded numbers parsed (bandwidth truncated to 8000)", first)
	}
	if first.QualityProfile != "1080p" || first.TranscodeDecision != "direct play" {
		t.Errorf("session[0] quality/transcode = (%q, %q)", first.QualityProfile, first.TranscodeDecision)
	}
	second := activity.Sessions[1]
	if second.ProgressPercent != 0 || second.Bandwidth != 0 {
		t.Errorf("session[1] = %+v, want empty/null numbers mapped to 0", second)
	}
}

func TestFlexIntToleratesTautulliNumberEncodings(t *testing.T) {
	cases := map[string]flexInt{
		`42`:       42,
		`"42"`:     42,
		`"8000.9"`: 8000,
		`""`:       0,
		`null`:     0,
		`"n/a"`:    0,
	}
	for raw, want := range cases {
		var got flexInt
		if err := got.UnmarshalJSON([]byte(raw)); err != nil {
			t.Errorf("UnmarshalJSON(%s) error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("UnmarshalJSON(%s) = %d, want %d", raw, got, want)
		}
	}
}

// TestGetHistoryPassesLengthAndUnwrapsRows pins the nested get_history shape
// (rows live under data.data) and the length parameter contract: sent when
// positive, omitted entirely for 0 (server default).
func TestGetHistoryPassesLengthAndUnwrapsRows(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, successEnvelope(`{"data":[
			{"user":"julian","full_title":"Heat (1995)","date":"1720000000","duration":3600,"percent_complete":"87","player":"TV","platform":"tvOS"}
		]}`))
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.URL, testAPIKey)

	rows, err := client.GetHistory(25)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if gotQuery.Get("cmd") != "get_history" || gotQuery.Get("length") != "25" {
		t.Errorf("query = %v, want cmd=get_history length=25", gotQuery)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.User != "julian" || row.FullTitle != "Heat (1995)" || row.Date != 1720000000 ||
		row.Duration != 3600 || row.PercentComplete != 87 || row.Platform != "tvOS" {
		t.Errorf("row = %+v, want all fields mapped", row)
	}

	if _, err := client.GetHistory(0); err != nil {
		t.Fatalf("GetHistory(0): %v", err)
	}
	if gotQuery.Has("length") {
		t.Errorf("GetHistory(0) sent length=%q, want the parameter omitted", gotQuery.Get("length"))
	}
}

func TestGetHomeStatsPassesTimeRange(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = io.WriteString(w, successEnvelope(`[
			{"stat_id":"top_movies","rows":[{"title":"Heat","total_plays":"9"}]},
			{"stat_id":"top_users","rows":[{"user":"julian","friendly_name":"Julian","total_plays":4}]}
		]`))
	}))
	t.Cleanup(srv.Close)

	stats, err := NewClient(srv.URL, testAPIKey).GetHomeStats(7)
	if err != nil {
		t.Fatalf("GetHomeStats: %v", err)
	}
	if gotQuery.Get("cmd") != "get_home_stats" || gotQuery.Get("time_range") != "7" {
		t.Errorf("query = %v, want cmd=get_home_stats time_range=7", gotQuery)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %d, want 2", len(stats))
	}
	if stats[0].StatID != "top_movies" || len(stats[0].Rows) != 1 || stats[0].Rows[0].TotalPlays != 9 {
		t.Errorf("stats[0] = %+v, want top_movies with 9 plays", stats[0])
	}
	if stats[1].Rows[0].FriendlyName != "Julian" || stats[1].Rows[0].User != "julian" {
		t.Errorf("stats[1] row = %+v, want user identities mapped", stats[1].Rows[0])
	}
}

// TestNon2xxStatusIsAnError pins that HTTP-level failures surface only the
// status code — never the response body, which a hostile or misconfigured
// upstream could use to echo the API key back.
func TestNon2xxStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error while handling "+r.URL.Query().Get("apikey"), http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, testAPIKey).GetActivity()
	if err == nil {
		t.Fatal("GetActivity accepted a 500 response")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %v, want the status surfaced", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error echoed the API key: %v", err)
	}
}

func TestMalformedBodyIsADecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>login page, not an API</html>")
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, testAPIKey).GetServerInfo()
	if err == nil {
		t.Fatal("GetServerInfo accepted a non-JSON body")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("error = %v, want a decode-response error", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error echoed the API key: %v", err)
	}
}

func TestMismatchedDataShapeIsADecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, successEnvelope(`"not an object"`))
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, testAPIKey).GetActivity()
	if err == nil {
		t.Fatal("GetActivity accepted a mismatched data payload")
	}
	if !strings.Contains(err.Error(), "decode data") {
		t.Errorf("error = %v, want a decode-data error", err)
	}
}

// TestErrorEnvelopeSurfacesMessage pins Tautulli's HTTP-200-but-failed
// envelope: result != success must fail with the server's message, falling
// back to "unknown error" when the message is empty.
func TestErrorEnvelopeSurfacesMessage(t *testing.T) {
	message := `"Invalid apikey"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"response":{"result":"error","message":`+message+`,"data":null}}`)
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.URL, testAPIKey)

	_, err := client.GetActivity()
	if err == nil {
		t.Fatal("GetActivity accepted an error envelope")
	}
	if !strings.Contains(err.Error(), "tautulli get_activity: Invalid apikey") {
		t.Errorf("error = %v, want the envelope message surfaced with the cmd", err)
	}

	message = `""`
	_, err = client.GetActivity()
	if err == nil || !strings.Contains(err.Error(), "unknown error") {
		t.Errorf("error = %v, want the unknown-error fallback for an empty message", err)
	}
}

// TestClientDoesNotFollowRedirects pins the redirect guard: the client is
// built with CheckRedirect returning ErrUseLastResponse, so a redirecting
// upstream is treated as a plain non-2xx failure and the redirect target is
// never fetched (the apikey is not replayed to an attacker-chosen location).
func TestClientDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		_, _ = io.WriteString(w, successEnvelope(`{}`))
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v2", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	_, err := NewClient(redirector.URL, testAPIKey).GetServerInfo()
	if err == nil {
		t.Fatal("GetServerInfo accepted a redirect response")
	}
	if !strings.Contains(err.Error(), "status 302") {
		t.Errorf("error = %v, want the redirect surfaced as status 302", err)
	}
	if followed.Load() != 0 {
		t.Fatalf("client followed the redirect %d times, want 0", followed.Load())
	}
}

// TestTransportErrorRedactsAPIKey pins the redaction path: a transport-level
// *url.Error embeds the full request URL, apikey query parameter included, so
// the client must scrub the secret before the error can reach logs or API
// responses.
func TestTransportErrorRedactsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // guarantee a connection-refused transport error

	_, err := NewClient(addr, testAPIKey).GetActivity()
	if err == nil {
		t.Fatal("GetActivity succeeded against a closed server")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("transport error leaked the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("error = %v, want the apikey replaced with [redacted]", err)
	}
}
