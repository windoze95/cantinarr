package discordnotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	projectdb "github.com/windoze95/cantinarr-server/internal/db"
)

const viewerDiscord = "123456789012345678"
const adminDiscord = "234567890123456789"
const roleDiscord = "345678901234567890"

type fakeSource struct {
	views        map[int64]Availability
	blocked      map[int64]bool
	keys         map[int64]string
	unverifiable map[int64]bool
	reads        int
	err          error
}

func (f *fakeSource) DiscordAvailability(_ context.Context, id int64) (Availability, error) {
	f.reads++
	if f.unverifiable[id] {
		return Availability{}, fmt.Errorf("%w: legacy selection", ErrUnverifiable)
	}
	return f.views[id], f.err
}
func (f *fakeSource) DiscordAuthorize(_ context.Context, id int64, _ Subject) (bool, error) {
	return !f.blocked[id], f.err
}
func (f *fakeSource) DiscordObservationKey(_ context.Context, id int64) (string, error) {
	return f.keys[id], nil
}

func execSQL(t *testing.T, s *Service, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}
func allEvents(t *testing.T, s *Service) {
	t.Helper()
	events := map[string]bool{}
	for _, kind := range EventKinds {
		events[kind] = true
	}
	on := true
	if err := s.SaveUpdate(Update{Enabled: true, Webhook: testWebhook, Events: &events, EnableMentions: &on}, false); err != nil {
		t.Fatal(err)
	}
}
func optIn(t *testing.T, s *Service, id int64, discordID string) {
	t.Helper()
	p, err := s.preferences(id)
	if err != nil {
		t.Fatal(err)
	}
	p.Enabled = true
	p.DiscordIDs = []string{discordID}
	p.Events = p.AllowedEvents
	if err = s.savePreferences(id, p); err != nil {
		t.Fatal(err)
	}
}
func capture(s *Service) *[]map[string]any {
	messages := []map[string]any{}
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return nil, err
		}
		messages = append(messages, p)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"123456789012345678"}`))}, nil
	})
	return &messages
}

func TestLifecycleEventsAreDistinctAndMentionsRecheckConsent(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	execSQL(t, s, `INSERT INTO users(id,username,password_hash,role) VALUES(2,'admin','','admin')`)
	optIn(t, s, 1, viewerDiscord)
	optIn(t, s, 2, adminDiscord)
	id := seed(t, s, "movie")
	execSQL(t, s, `UPDATE request_log SET approved_by=2 WHERE id=?`, id)
	s.RequestCreated(id, true)
	s.NotifyUser(1, "request_decision", map[string]any{"request_id": id, "decision": "approved"})
	s.NotifyUser(1, "request_decision", map[string]any{"request_id": id, "decision": "approved"})
	execSQL(t, s, `INSERT INTO request_dispatch(request_id,state,code,last_attempt_at,attempts) VALUES(?,'attention','retry_limit',100,5)`, id)
	s.NotifyUser(1, "request_updated", map[string]any{"request_id": id, "delivery_state": "attention"})
	messages := capture(s)
	for range 3 {
		if err := s.deliverOne(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(*messages) != 3 {
		t.Fatalf("got %d events", len(*messages))
	}
	for i, want := range []string{adminDiscord, viewerDiscord, adminDiscord} {
		got := (*messages)[i]["allowed_mentions"].(map[string]any)["users"].([]any)
		if !reflect.DeepEqual(got, []any{want}) {
			t.Fatalf("audience %d: %v", i, got)
		}
	}
	// A personal mute after enqueue must win at dispatch.
	s.NotifyUser(1, "request_decision", map[string]any{"request_id": id, "decision": "denied"})
	if err := s.savePreferences(1, Preferences{DiscordIDs: []string{viewerDiscord}, Events: map[string]bool{RequestDenied: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if content := (*messages)[3]["content"]; content != "" {
		t.Fatal(content)
	}
	got, err := s.Get()
	if err != nil || len(got.Recent) != 4 {
		t.Fatalf("receipts: %+v %v", got, err)
	}
}

func TestMentionsRejectInjectionAndSuppressActorAndDemotedAdmin(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	execSQL(t, s, `INSERT INTO users(id,username,password_hash,role) VALUES(2,'admin','','admin')`)
	optIn(t, s, 2, adminDiscord)
	for _, bad := range []string{"@everyone", "<@123456789012345678>", "123abc", "123"} {
		if err := s.savePreferences(1, Preferences{Enabled: true, DiscordIDs: []string{bad}}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if err := s.savePreferences(1, Preferences{Events: map[string]bool{RequestPending: true}}); err == nil {
		t.Fatal("requester opted into admin event")
	}
	ids, err := s.userMentions([]int64{2, 2}, RequestPending, 2)
	if err != nil || len(ids) != 0 {
		t.Fatalf("self mention: %v %v", ids, err)
	}
	execSQL(t, s, `UPDATE users SET role='user' WHERE id=2`)
	ids, err = s.userMentions([]int64{2}, RequestPending, 0)
	if err != nil || len(ids) != 0 {
		t.Fatalf("demoted admin: %v %v", ids, err)
	}
	p, err := s.preferences(2)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.savePreferences(2, p); err != nil {
		t.Fatalf("demoted account cannot save visible preferences: %v", err)
	}
}

func TestAvailabilityBaselinesBatchingRestartAndNoUpgradeReplay(t *testing.T) {
	s := fixture(t)
	old := seed(t, s, "tv")
	allEvents(t, s)
	id := seed(t, s, "tv")
	other := seed(t, s, "tv")
	makeView := func(id int64, keys ...string) Availability {
		v := Availability{Subject: Subject{RequestID: id, Title: "A show", MediaType: "tv", TmdbID: 550, InstanceID: "sonarr-a"}}
		for _, key := range keys {
			v.Units = append(v.Units, Unit{Key: key, Label: key})
		}
		return v
	}
	f := &fakeSource{views: map[int64]Availability{old: makeView(old, "old"), id: makeView(id), other: makeView(other)}}
	s.SetSource(f)
	c, _ := s.readConfig(s.db)
	for _, id := range []int64{old, id, other} {
		if err := s.recordAvailability(c, id, f.views[id], ""); err != nil {
			t.Fatal(err)
		}
	}
	f.views[id] = makeView(id, "S01E01")
	if err := s.recordAvailability(c, id, f.views[id], ""); err != nil {
		t.Fatal(err)
	}
	start := s.now()
	s.now = func() time.Time { return start.Add(45 * time.Second) }
	f.views[id] = makeView(id, "S01E01", "S01E02")
	f.views[other] = makeView(other, "S01E02")
	for _, id := range []int64{id, other} {
		if err := s.recordAvailability(c, id, f.views[id], ""); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var due int64
	if err := s.db.QueryRow(`SELECT COUNT(*),MIN(next_attempt_at) FROM discord_notifications`).Scan(&count, &due); err != nil {
		t.Fatal(err)
	}
	if count != 1 || due != start.Unix()+60 {
		t.Fatalf("batch count=%d due=%d", count, due)
	}
	// Removing one request cannot delete a batch owed to another requester.
	execSQL(t, s, `DELETE FROM request_log WHERE id=?`, id)
	restarted := NewService(s.db, s.cipher, nil)
	restarted.SetSource(f)
	restarted.now = func() time.Time { return start.Add(time.Minute) }
	messages := capture(restarted)
	if err := restarted.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*messages) != 1 {
		t.Fatalf("restart deliveries %d", len(*messages))
	}
	for range 2 {
		if err := restarted.recordAvailability(c, other, f.views[other], ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM discord_notifications`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade replayed %d %v", count, err)
	}
	if err := s.Save(false, "", false, nil); err != nil {
		t.Fatal(err)
	}
	f.views[other] = makeView(other, "S01E02", "muted")
	if err := s.Save(true, "", false, nil); err != nil {
		t.Fatal(err)
	}
	c, _ = s.readConfig(s.db)
	if err := s.recordAvailability(c, other, f.views[other], ""); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM discord_notifications`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("mute replayed %d %v", count, err)
	}
}

func TestSharedBookAudienceUsesRequestedFormatAndCurrentAccess(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	execSQL(t, s, `INSERT INTO users(id,username,password_hash) VALUES(2,'reader2',''),(3,'reader3','')`)
	for id, discordID := range map[int64]string{1: viewerDiscord, 2: adminDiscord, 3: roleDiscord} {
		optIn(t, s, id, discordID)
	}
	id := seed(t, s, "book")
	execSQL(t, s, `UPDATE request_log SET book_format='ebook' WHERE id=?`, id)
	execSQL(t, s, `INSERT INTO book_request_waiters(request_id,user_id,book_format) VALUES(?,2,'audiobook'),(?,3,'both')`, id, id)
	s.SetSource(&fakeSource{blocked: map[int64]bool{3: true}})
	a, err := loadRequestAlert(s.db, id)
	if err != nil {
		t.Fatal(err)
	}
	for format, want := range map[string]int64{"ebook": 1, "audiobook": 2} {
		ids, err := s.requestRecipients(context.Background(), a, []Unit{{Key: format, Format: format}})
		if err != nil || !reflect.DeepEqual(ids, []int64{want}) {
			t.Fatalf("%s: %v %v", format, ids, err)
		}
	}
}

func TestAuthorizationOutageDoesNotConsumeHTTPAttemptsOrBlockOtherEvents(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	id := seed(t, s, "movie")
	execSQL(t, s, `UPDATE request_log SET approved_by=1 WHERE id=?`, id)
	s.SetSource(&fakeSource{err: errors.New("offline")})
	s.NotifyUser(1, "request_decision", map[string]any{"request_id": id, "decision": "approved"})
	messages := capture(s)
	for range 6 {
		if err := s.deliverOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		next := s.now().Add(31 * time.Second)
		s.now = func() time.Time { return next }
	}
	d := status(t, s, id)
	c, _ := s.readConfig(s.db)
	if len(*messages) != 0 || d.Status != "pending" || d.Attempts != 0 || c.NotBefore != 0 {
		t.Fatalf("outage: %+v cooldown=%d calls=%d", d, c.NotBefore, len(*messages))
	}
	s.source = &fakeSource{blocked: map[int64]bool{1: true}}
	if err := s.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status(t, s, id).Status != "cancelled" {
		t.Fatal("revoked grant did not cancel")
	}
}

func TestReportsUseFixedSummariesAndIgnoreSystemIssues(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	execSQL(t, s, `INSERT INTO issues(id,title,tmdb_id,media_type,reporter_id,source) VALUES(1,'A title',550,'movie',1,'user'),(2,'Secret diagnostic',0,'movie',1,'system')`)
	for _, kind := range []string{IssueCreated, IssueComment, IssueResolved, IssueReopened} {
		s.ReportChanged(kind, 1, 1, 10)
		s.ReportChanged(kind, 1, 1, 10)
		s.ReportChanged(kind, 2, 1, 10)
	}
	messages := capture(s)
	for range 4 {
		if err := s.deliverOne(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(*messages) != 4 {
		t.Fatalf("reports: %d", len(*messages))
	}
	for _, p := range *messages {
		b, _ := json.Marshal(p)
		if strings.Contains(string(b), "diagnostic") || !strings.Contains(string(b), "/issues/1") {
			t.Fatal(string(b))
		}
	}
}

func TestLegacyQueueMigrationRetainsReceiptsAndAllowsMultipleEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	database, err := projectdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO users(id,username,password_hash) VALUES(1,'user','');
	INSERT INTO request_log(id,user_id,tmdb_id,media_type,title) VALUES(1,1,550,'movie','A title');
	DROP TABLE discord_notifications;
	CREATE TABLE discord_notifications(id INTEGER PRIMARY KEY AUTOINCREMENT,request_id INTEGER NOT NULL UNIQUE REFERENCES request_log(id) ON DELETE CASCADE,revision INTEGER NOT NULL,payload TEXT NOT NULL,status TEXT NOT NULL DEFAULT 'pending',detail TEXT NOT NULL DEFAULT 'Waiting to send.',attempts INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,next_attempt_at INTEGER NOT NULL DEFAULT 0);
	CREATE INDEX discord_notifications_due ON discord_notifications(status,next_attempt_at);
	INSERT INTO discord_notifications(request_id,revision,payload,status,created_at,updated_at) VALUES(1,2,'{"requires_approval":true}','sent',100,101);`)
	if err != nil {
		t.Fatal(err)
	}
	database.Close()
	database, err = projectdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var key, status string
	if err = database.QueryRow(`SELECT event_key,status FROM discord_notifications`).Scan(&key, &status); err != nil || key != "created:1" || status != "sent" {
		t.Fatalf("%s %s %v", key, status, err)
	}
	if _, err = database.Exec(`INSERT INTO discord_notifications(request_id,event_key,revision,payload,created_at,updated_at) VALUES(1,'approved:1',2,'{}',102,102)`); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationOptionsRoleGateAndDeepLinks(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	role, thread, name := roleDiscord, "456789012345678901", "My server"
	roles := map[string]bool{RequestPending: true}
	if err := s.SaveUpdate(Update{Enabled: true, RoleID: &role, RoleEvents: &roles, ThreadID: &thread, Username: &name}, false); err != nil {
		t.Fatal(err)
	}
	id := seed(t, s, "movie")
	s.RequestCreated(id, true)
	c, _ := s.readConfig(s.db)
	a, _ := loadRequestAlert(s.db, id)
	a.Kind = RequestPending
	p, err := s.prepareMessage(context.Background(), a, c, "https://cantinarr.example/base")
	if err != nil {
		t.Fatal(err)
	}
	if p["content"] != "<@&"+role+">" || !strings.Contains(webhookDestination(c), "thread_id="+thread) {
		t.Fatal(p, webhookDestination(c))
	}
	off := false
	if err = s.SaveUpdate(Update{Enabled: true, EnableMentions: &off}, false); err != nil {
		t.Fatal(err)
	}
	c, _ = s.readConfig(s.db)
	p, err = s.prepareMessage(context.Background(), a, c, "")
	if err != nil || p["content"] != "" {
		t.Fatal(p, err)
	}
	for _, kind := range []string{"book", "music"} {
		a = requestAlert{Kind: RequestApproved, MediaType: kind, Subject: Subject{ForeignID: "id/with space", InstanceID: "library one"}}
		link := eventLink(a, "https://cantinarr.example/base")
		if !strings.Contains(link, "id%2Fwith%20space") || strings.Contains(link, "%252F") || !strings.Contains(link, "instance_id=library+one") {
			t.Fatal(link)
		}
	}
	// An old client still preserves thread and category choices.
	if err = s.Save(true, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get()
	if got.ThreadID != thread || !got.Events[RequestAvailable] {
		t.Fatal(fmt.Sprint(got))
	}
}

func TestLargeMentionAudienceIsSplitWithIndependentReceipts(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	for id := int64(2); id <= 81; id++ {
		execSQL(t, s, `INSERT INTO users(id,username,password_hash,role) VALUES(?,?,'','admin')`, id, fmt.Sprintf("admin-%d", id))
		optIn(t, s, id, fmt.Sprintf("123456789012345%03d", id))
	}
	id := seed(t, s, "movie")
	s.RequestCreated(id, true)
	messages := capture(s)
	if err := s.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*messages) != 0 {
		t.Fatal("oversized message was sent")
	}
	for range 2 {
		if err := s.deliverOne(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, p := range *messages {
		if len(p["content"].(string)) > 2000 {
			t.Fatal("message too long")
		}
		for _, id := range p["allowed_mentions"].(map[string]any)["users"].([]any) {
			if seen[id.(string)] {
				t.Fatal("duplicate ping")
			}
			seen[id.(string)] = true
		}
	}
	if len(*messages) != 2 || len(seen) != 80 {
		t.Fatalf("messages=%d recipients=%d", len(*messages), len(seen))
	}
}

// pin gives a seeded request the owning library the availability scan requires.
func pin(t *testing.T, s *Service, id int64) {
	t.Helper()
	execSQL(t, s, `INSERT OR IGNORE INTO service_instances(id,service_type,name,url,api_key) VALUES('sonarr-a','sonarr','Shows','http://sonarr:8989','')`)
	execSQL(t, s, `UPDATE request_log SET instance_id='sonarr-a' WHERE id=?`, id)
}

func TestUnchangedObservationKeySkipsFullReadAndWrite(t *testing.T) {
	s := fixture(t)
	id := seed(t, s, "tv")
	pin(t, s, id)
	allEvents(t, s)
	view := Availability{Subject: Subject{RequestID: id, Title: "A show", MediaType: "tv", TmdbID: 550, InstanceID: "sonarr-a"}, Units: []Unit{{Key: "e1", Label: "S01E01"}}}
	f := &fakeSource{views: map[int64]Availability{id: view}, keys: map[int64]string{id: "k1"}}
	s.SetSource(f)
	changes := func() (n int64) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	scan := func(wantReads int) {
		t.Helper()
		if err := s.scanAvailability(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.reads != wantReads {
			t.Fatalf("reads=%d want %d", f.reads, wantReads)
		}
	}
	scan(1)
	before := changes()
	scan(1) // unchanged library: no provider read and no receipt write
	if changes() != before {
		t.Fatal("unchanged sweep wrote a receipt")
	}
	f.keys[id] = ""
	scan(2) // keyless reads always run; the new key is stored once
	before = changes()
	scan(3)
	if changes() != before {
		t.Fatal("unchanged keyless read rewrote its receipt")
	}
	f.keys[id] = "k2"
	scan(4)
	var key string
	if err := s.db.QueryRow(`SELECT observed_key FROM discord_availability WHERE request_id=?`, id).Scan(&key); err != nil || key != "k2" {
		t.Fatalf("stored key %q %v", key, err)
	}
}

func TestUnverifiableRequestsStayQuietAndQueuedPartsDrop(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	legacy, queued := seed(t, s, "tv"), seed(t, s, "tv")
	pin(t, s, legacy)
	pin(t, s, queued)
	view := Availability{Subject: Subject{RequestID: queued, Title: "A show", MediaType: "tv", TmdbID: 550, InstanceID: "sonarr-a"}, Units: []Unit{{Key: "e1", Label: "S01E01"}}}
	f := &fakeSource{views: map[int64]Availability{queued: view}, unverifiable: map[int64]bool{legacy: true}}
	s.SetSource(f)
	if err := s.scanAvailability(context.Background()); err != nil {
		t.Fatalf("an unverifiable request raised the library warning: %v", err)
	}
	var receipts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM discord_availability WHERE request_id=?`, legacy).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("unverifiable request recorded a baseline: %d %v", receipts, err)
	}
	// A queued part whose selection became unverifiable is dropped, not retried.
	f.unverifiable[queued] = true
	start := s.now()
	s.now = func() time.Time { return start.Add(61 * time.Second) }
	messages := capture(s)
	if err := s.deliverOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.db.QueryRow(`SELECT status FROM discord_notifications WHERE json_extract(payload,'$.kind')=?`, RequestAvailable).Scan(&state); err != nil || state != "cancelled" || len(*messages) != 0 {
		t.Fatalf("queued part: %q %v sent=%d", state, err, len(*messages))
	}
}

func TestRepairFirstObservationIsSilentBaseline(t *testing.T) {
	s := fixture(t)
	allEvents(t, s)
	id := seed(t, s, "tv")
	pin(t, s, id)
	view := Availability{Subject: Subject{RequestID: id, Title: "A show", MediaType: "tv", TmdbID: 550, InstanceID: "sonarr-a"}, Units: []Unit{{Key: "e1", Label: "S01E01"}}, Baseline: true}
	c, _ := s.readConfig(s.db)
	count := func() (n int) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM discord_notifications`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := s.recordAvailability(c, id, view, ""); err != nil || count() != 0 {
		t.Fatalf("repair re-announced existing work: %v", err)
	}
	view.Units = append(view.Units, Unit{Key: "e2", Label: "S01E02"})
	if err := s.recordAvailability(c, id, view, ""); err != nil || count() != 1 {
		t.Fatalf("a new episode after the repair baseline: %v", err)
	}
}

func TestDisplayNameRejectsWordsDiscordRefuses(t *testing.T) {
	s := fixture(t)
	for _, name := range []string{"Discord alerts", "clyde", "Media DISCORD"} {
		if err := s.SaveUpdate(Update{Enabled: true, Webhook: testWebhook, Username: &name}, false); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	name := "Home media"
	if err := s.SaveUpdate(Update{Enabled: true, Webhook: testWebhook, Username: &name}, false); err != nil {
		t.Fatal(err)
	}
}

func TestLinksTargetHashRoutes(t *testing.T) {
	for _, tc := range []struct {
		a    requestAlert
		want string
	}{
		{requestAlert{Kind: RequestPending, MediaType: "movie"}, "https://cantinarr.example/base/#/approvals"},
		{requestAlert{Kind: IssueComment, IssueID: 7, MediaType: "tv"}, "https://cantinarr.example/base/#/issues/7"},
		{requestAlert{Kind: RequestAvailable, MediaType: "movie", Subject: Subject{TmdbID: 550, InstanceID: "radarr-a"}}, "https://cantinarr.example/base/#/detail/movie/550?instance_id=radarr-a"},
		{requestAlert{Kind: RequestApproved, MediaType: "book", Subject: Subject{ForeignID: "id/with space", InstanceID: "books"}}, "https://cantinarr.example/base/#/detail/book/id%2Fwith%20space?instance_id=books&source=chaptarr"},
		{requestAlert{Kind: RequestAvailable, MediaType: "music", Subject: Subject{ForeignID: "album-1"}}, "https://cantinarr.example/base/#/detail/album/album-1"},
	} {
		for _, external := range []string{"https://cantinarr.example/base", "https://cantinarr.example/base/"} {
			if got := eventLink(tc.a, external); got != tc.want {
				t.Errorf("%s: got %s want %s", external, got, tc.want)
			}
		}
	}
}
