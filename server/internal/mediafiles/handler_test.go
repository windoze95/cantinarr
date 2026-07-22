package mediafiles

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediapath"
)

type fakeInstanceStore struct {
	instances   map[string]*instance.Instance
	allowed     bool
	getErr      error
	accessErr   error
	accessCalls []accessCall
	roles       map[int64]string
	deleted     map[int64]bool
	userErr     error
}

type accessCall struct {
	userID      int64
	instanceID  string
	serviceType string
}

func (s *fakeInstanceStore) Get(id string) (*instance.Instance, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	inst := s.instances[id]
	if inst == nil {
		return nil, nil
	}
	copy := *inst
	return &copy, nil
}

func (s *fakeInstanceStore) UserCanAccessInstance(userID int64, instanceID, serviceType string) (bool, error) {
	s.accessCalls = append(s.accessCalls, accessCall{
		userID:      userID,
		instanceID:  instanceID,
		serviceType: serviceType,
	})
	if s.accessErr != nil {
		return false, s.accessErr
	}
	return s.allowed, nil
}

func (s *fakeInstanceStore) GetUser(userID int64) (*auth.User, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	if s.deleted[userID] {
		return nil, sql.ErrNoRows
	}
	role := s.roles[userID]
	if role == "" {
		role = auth.RoleUser
		if userID == 1 {
			role = auth.RoleAdmin
		}
	}
	return &auth.User{ID: userID, Role: role}, nil
}

type fakeMetadataResolver struct {
	paths map[int]string
	err   error
	calls []resolveCall
}

type resolveCall struct {
	instanceID  string
	serviceType string
	fileID      int
}

func (r *fakeMetadataResolver) Resolve(instanceID, serviceType string, fileID int) (fileMetadata, error) {
	r.calls = append(r.calls, resolveCall{instanceID: instanceID, serviceType: serviceType, fileID: fileID})
	if r.err != nil {
		return fileMetadata{}, r.err
	}
	return fileMetadata{Path: r.paths[fileID]}, nil
}

func TestHandlerDownloadSupportsReusableHeadAndRange(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Movies", "example.mp4")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("0123456789")
	if err := os.WriteFile(mediaPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"radarr-main": {ID: "radarr-main", ServiceType: "radarr"},
		},
		allowed: true,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{7: mediaPath}}
	h := newTestHandler(t, store, resolver, []string{root})
	fixedNow := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return fixedNow }

	response, body := issueTicket(t, h, &auth.Claims{UserID: 42, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":7}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, body = %s", response.Code, response.Body.String())
	}
	if body.Filename != "example.mp4" || body.SizeBytes != int64(len(content)) {
		t.Fatalf("ticket metadata = %#v", body)
	}
	if !body.ExpiresAt.Equal(fixedNow.Add(defaultTicketTTL)) {
		t.Fatalf("expires_at = %v", body.ExpiresAt)
	}
	if strings.Contains(response.Body.String(), mediaPath) || strings.Contains(response.Body.String(), "radarr-main") {
		t.Fatalf("ticket response leaked server identity: %s", response.Body.String())
	}
	token := strings.TrimPrefix(body.URL, "/api/media-files/download/")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != ticketRandomBytes {
		t.Fatalf("ticket token = %q, decoded bytes = %d, err = %v", token, len(decoded), err)
	}

	head := downloadRequest(h, http.MethodHead, body.URL, "")
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, body = %q", head.Code, head.Body.String())
	}
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD body = %q, want empty", head.Body.String())
	}
	if head.Header().Get("Content-Length") != strconv.Itoa(len(content)) {
		t.Fatalf("HEAD Content-Length = %q", head.Header().Get("Content-Length"))
	}
	assertDownloadHeaders(t, head, "example.mp4")

	ranged := downloadRequest(h, http.MethodGet, body.URL, "bytes=2-5")
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "2345" {
		t.Fatalf("Range response = %d %q", ranged.Code, ranged.Body.String())
	}
	if ranged.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", ranged.Header().Get("Content-Range"))
	}
	if ranged.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q", ranged.Header().Get("Accept-Ranges"))
	}
	assertDownloadHeaders(t, ranged, "example.mp4")

	full := downloadRequest(h, http.MethodGet, body.URL, "")
	if full.Code != http.StatusOK || !bytes.Equal(full.Body.Bytes(), content) {
		t.Fatalf("full response = %d %q", full.Code, full.Body.String())
	}
	assertDownloadHeaders(t, full, "example.mp4")

	invalidRange := downloadRequest(h, http.MethodGet, body.URL, "bytes=99-")
	if invalidRange.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("invalid Range status = %d, body = %q", invalidRange.Code, invalidRange.Body.String())
	}
	assertDownloadHeaders(t, invalidRange, "")
	if got := invalidRange.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("error Content-Disposition = %q, want empty", got)
	}

	if len(resolver.calls) != 5 {
		t.Fatalf("metadata resolves = %d, want issue plus every HEAD/GET", len(resolver.calls))
	}
	if len(store.accessCalls) != 5 {
		t.Fatalf("access checks = %d, want issue plus every HEAD/GET", len(store.accessCalls))
	}
}

func TestIssueTicketEnforcesAccessAndDownloadRechecksGrant(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "book.epub")
	if err := os.WriteFile(mediaPath, []byte("book"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"books-1": {ID: "books-1", ServiceType: "chaptarr"},
		},
		allowed: false,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{19: mediaPath}}
	h := newTestHandler(t, store, resolver, []string{root})

	denied, _ := issueTicket(t, h, &auth.Claims{UserID: 8, Role: auth.RoleUser}, `{"instance_id":"books-1","file_id":19}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, body = %s", denied.Code, denied.Body.String())
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver called before access decision: %#v", resolver.calls)
	}
	if got := store.accessCalls[0]; got.userID != 8 || got.instanceID != "books-1" || got.serviceType != "chaptarr" {
		t.Fatalf("access check = %#v", got)
	}

	store.userErr = errors.New("database at /private/auth.db failed")
	unavailable, _ := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-1","file_id":19}`)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("user lookup failure status = %d, body = %s", unavailable.Code, unavailable.Body.String())
	}
	if strings.Contains(unavailable.Body.String(), "/private/auth.db") {
		t.Fatalf("user lookup failure leaked detail: %s", unavailable.Body.String())
	}
	store.userErr = nil

	admin, adminBody := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-1","file_id":19}`)
	if admin.Code != http.StatusCreated {
		t.Fatalf("admin status = %d, body = %s", admin.Code, admin.Body.String())
	}
	if len(store.accessCalls) != 1 {
		t.Fatalf("admin unexpectedly used requester grant check")
	}
	store.userErr = errors.New("temporary user lookup failure")
	lookupFailure := downloadRequest(h, http.MethodGet, adminBody.URL, "")
	if lookupFailure.Code != http.StatusServiceUnavailable {
		t.Fatalf("download user lookup failure = %d, body = %s", lookupFailure.Code, lookupFailure.Body.String())
	}
	store.userErr = nil

	// Ticket authorization uses the current account role, not the role snapshot
	// in the JWT or at issuance. An admin demotion therefore revokes access to a
	// non-effective instance immediately.
	store.roles = map[int64]string{1: auth.RoleUser}
	demotedResolves := len(resolver.calls)
	demoted := downloadRequest(h, http.MethodGet, adminBody.URL, "")
	if demoted.Code != http.StatusNotFound {
		t.Fatalf("demoted admin download status = %d, body = %s", demoted.Code, demoted.Body.String())
	}
	if len(resolver.calls) != demotedResolves {
		t.Fatal("demoted admin ticket resolved upstream metadata before current-role denial")
	}
	staleAdminClaims, _ := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-1","file_id":19}`)
	if staleAdminClaims.Code != http.StatusForbidden {
		t.Fatalf("stale admin claims issue status = %d, body = %s", staleAdminClaims.Code, staleAdminClaims.Body.String())
	}

	store.allowed = true
	requester, requesterBody := issueTicket(t, h, &auth.Claims{UserID: 8, Role: auth.RoleUser}, `{"instance_id":"books-1","file_id":19}`)
	if requester.Code != http.StatusCreated {
		t.Fatalf("requester status = %d, body = %s", requester.Code, requester.Body.String())
	}
	resolvesBeforeRevocation := len(resolver.calls)
	store.allowed = false
	download := downloadRequest(h, http.MethodGet, requesterBody.URL, "")
	if download.Code != http.StatusNotFound {
		t.Fatalf("revoked download status = %d, body = %s", download.Code, download.Body.String())
	}
	if len(resolver.calls) != resolvesBeforeRevocation {
		t.Fatal("revoked ticket resolved upstream metadata before rejecting access")
	}

	store.allowed = true
	store.deleted = map[int64]bool{8: true}
	deleted := downloadRequest(h, http.MethodHead, requesterBody.URL, "")
	if deleted.Code != http.StatusNotFound || deleted.Body.Len() != 0 {
		t.Fatalf("deleted-user HEAD = %d %q", deleted.Code, deleted.Body.String())
	}
	if len(resolver.calls) != resolvesBeforeRevocation {
		t.Fatal("deleted user's ticket resolved upstream metadata")
	}
}

func TestIssueTicketRejectsPathsOutsideRootAndDoesNotLeakDetails(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "media")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "outside-secret.mkv")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	prefixSibling := filepath.Join(parent, "media-other")
	if err := os.MkdirAll(prefixSibling, 0700); err != nil {
		t.Fatal(err)
	}
	prefixPath := filepath.Join(prefixSibling, "prefix-secret.mkv")
	if err := os.WriteFile(prefixPath, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(root, "escape.mkv")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	directoryPath := filepath.Join(root, "not-a-file")
	if err := os.Mkdir(directoryPath, 0700); err != nil {
		t.Fatal(err)
	}
	insideTarget := filepath.Join(root, "inside-target.mkv")
	if err := os.WriteFile(insideTarget, []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	insideSymlink := filepath.Join(root, "inside-link.mkv")
	if err := os.Symlink(filepath.Base(insideTarget), insideSymlink); err != nil {
		t.Fatal(err)
	}

	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"sonarr-main": {ID: "sonarr-main", ServiceType: "sonarr"},
		},
		allowed: true,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{}}
	h := newTestHandler(t, store, resolver, []string{root})
	resolver.paths[6] = insideSymlink
	inside, insideBody := issueTicket(t, h, &auth.Claims{UserID: 11, Role: auth.RoleUser}, `{"instance_id":"sonarr-main","file_id":6}`)
	if inside.Code != http.StatusCreated {
		t.Fatalf("in-root symlink status = %d, body = %s", inside.Code, inside.Body.String())
	}
	insideDownload := downloadRequest(h, http.MethodGet, insideBody.URL, "")
	if insideDownload.Code != http.StatusOK || insideDownload.Body.String() != "inside" {
		t.Fatalf("in-root symlink download = %d %q", insideDownload.Code, insideDownload.Body.String())
	}

	paths := []struct {
		path   string
		status int
	}{
		{path: outsidePath, status: http.StatusConflict},
		{path: prefixPath, status: http.StatusConflict},
		{path: symlinkPath, status: http.StatusNotFound},
		{path: directoryPath, status: http.StatusNotFound},
		{path: "relative/file.mkv", status: http.StatusConflict},
	}
	for index, testCase := range paths {
		fileID := index + 1
		resolver.paths[fileID] = testCase.path
		response, _ := issueTicket(t, h, &auth.Claims{UserID: 11, Role: auth.RoleUser}, `{"instance_id":"sonarr-main","file_id":`+strconv.Itoa(fileID)+`}`)
		if response.Code != testCase.status {
			t.Errorf("path %q status = %d, want %d; body = %s", testCase.path, response.Code, testCase.status, response.Body.String())
		}
		if strings.Contains(response.Body.String(), testCase.path) || strings.Contains(response.Body.String(), "secret") {
			t.Errorf("path %q leaked in response %q", testCase.path, response.Body.String())
		}
	}

	resolver.err = errors.New("upstream http://sonarr.internal:8989 exposed /private/library")
	upstream, _ := issueTicket(t, h, &auth.Claims{UserID: 11, Role: auth.RoleUser}, `{"instance_id":"sonarr-main","file_id":99}`)
	if upstream.Code != http.StatusBadGateway {
		t.Fatalf("upstream status = %d, body = %s", upstream.Code, upstream.Body.String())
	}
	if strings.Contains(upstream.Body.String(), "sonarr.internal") || strings.Contains(upstream.Body.String(), "/private/library") {
		t.Fatalf("upstream details leaked: %s", upstream.Body.String())
	}

	resolver.err = nil
	callsBeforeClientPath := len(resolver.calls)
	clientPath, _ := issueTicket(t, h, &auth.Claims{UserID: 11, Role: auth.RoleUser}, `{"instance_id":"sonarr-main","file_id":99,"path":"`+outsidePath+`"}`)
	if clientPath.Code != http.StatusBadRequest {
		t.Fatalf("client path status = %d, body = %s", clientPath.Code, clientPath.Body.String())
	}
	if len(resolver.calls) != callsBeforeClientPath {
		t.Fatal("request containing a client-supplied path reached metadata resolver")
	}
}

func TestTicketDeduplicationBoundsAndExpiry(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "file.bin")
	if err := os.WriteFile(mediaPath, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"radarr-main": {ID: "radarr-main", ServiceType: "radarr"},
		},
		allowed: true,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{7: mediaPath, 8: mediaPath, 9: mediaPath}}
	h := newTestHandler(t, store, resolver, []string{root})
	h.maxTickets = 1
	h.maxTicketsPerUser = 1
	now := time.Date(2026, time.July, 22, 13, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return now }

	first, firstBody := issueTicket(t, h, &auth.Claims{UserID: 21, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":7}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	repeated, repeatedBody := issueTicket(t, h, &auth.Claims{UserID: 21, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":7}`)
	if repeated.Code != http.StatusCreated || repeatedBody.URL != firstBody.URL || !repeatedBody.ExpiresAt.Equal(firstBody.ExpiresAt) {
		t.Fatalf("deduplicated ticket = %d %#v, first = %#v", repeated.Code, repeatedBody, firstBody)
	}
	if len(h.tickets) != 1 {
		t.Fatalf("tickets = %d, want one deduplicated capability", len(h.tickets))
	}

	perUser, _ := issueTicket(t, h, &auth.Claims{UserID: 21, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":8}`)
	if perUser.Code != http.StatusTooManyRequests {
		t.Fatalf("per-user bound status = %d, body = %s", perUser.Code, perUser.Body.String())
	}
	global, _ := issueTicket(t, h, &auth.Claims{UserID: 22, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":9}`)
	if global.Code != http.StatusServiceUnavailable {
		t.Fatalf("global bound status = %d, body = %s", global.Code, global.Body.String())
	}

	now = firstBody.ExpiresAt
	expired := downloadRequest(h, http.MethodGet, firstBody.URL, "")
	if expired.Code != http.StatusNotFound {
		t.Fatalf("expired ticket status = %d, body = %s", expired.Code, expired.Body.String())
	}
	if len(h.tickets) != 0 {
		t.Fatalf("expired tickets retained: %d", len(h.tickets))
	}
	afterExpiry, _ := issueTicket(t, h, &auth.Claims{UserID: 22, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":9}`)
	if afterExpiry.Code != http.StatusCreated {
		t.Fatalf("ticket after expiry status = %d, body = %s", afterExpiry.Code, afterExpiry.Body.String())
	}
}

func TestLexicalRootAliasAndActiveContentRemainDownloadOnly(t *testing.T) {
	target := t.TempDir()
	alias := filepath.Join(t.TempDir(), "library-alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(alias, "active.html")
	if err := os.WriteFile(activePath, []byte(`<script>alert("same origin")</script>`), 0600); err != nil {
		t.Fatal(err)
	}
	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"radarr-main": {ID: "radarr-main", ServiceType: "radarr"},
		},
		allowed: true,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{10: activePath}}
	h := newTestHandler(t, store, resolver, []string{alias})

	issued, body := issueTicket(t, h, &auth.Claims{UserID: 31, Role: auth.RoleUser}, `{"instance_id":"radarr-main","file_id":10}`)
	if issued.Code != http.StatusCreated {
		t.Fatalf("lexical alias issue status = %d, body = %s", issued.Code, issued.Body.String())
	}
	download := downloadRequest(h, http.MethodGet, body.URL, "")
	if download.Code != http.StatusOK {
		t.Fatalf("active-content download status = %d, body = %s", download.Code, download.Body.String())
	}
	assertDownloadHeaders(t, download, "active.html")
	if got := download.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("active-content Content-Type = %q", got)
	}
}

func TestPerInstanceMappingsDisambiguateTheSameArrPath(t *testing.T) {
	root := t.TempDir()
	firstRoot := filepath.Join(root, "first-library")
	secondRoot := filepath.Join(root, "second-library")
	for _, library := range []struct {
		root    string
		content string
	}{
		{root: firstRoot, content: "first instance"},
		{root: secondRoot, content: "second instance"},
	} {
		if err := os.MkdirAll(library.root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(library.root, "same-name.epub"), []byte(library.content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"books-first": {
				ID:                "books-first",
				ServiceType:       "chaptarr",
				MediaDownloadMode: instance.MediaDownloadModeMapped,
				MediaPathMappings: []mediapath.Mapping{{ArrPath: "/ebooks", CantinarrPath: firstRoot}},
			},
			"books-second": {
				ID:                "books-second",
				ServiceType:       "chaptarr",
				MediaDownloadMode: instance.MediaDownloadModeMapped,
				MediaPathMappings: []mediapath.Mapping{{ArrPath: "/ebooks", CantinarrPath: secondRoot}},
			},
		},
		allowed: true,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{
		1: "/ebooks/same-name.epub",
		2: "/ebooks/same-name.epub",
	}}
	h := newTestHandler(t, store, resolver, []string{root})

	first, firstTicket := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-first","file_id":1}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first issue status = %d, body = %s", first.Code, first.Body.String())
	}
	second, secondTicket := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-second","file_id":2}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("second issue status = %d, body = %s", second.Code, second.Body.String())
	}

	firstDownload := downloadRequest(h, http.MethodGet, firstTicket.URL, "")
	if firstDownload.Code != http.StatusOK || firstDownload.Body.String() != "first instance" {
		t.Fatalf("first download = %d %q", firstDownload.Code, firstDownload.Body.String())
	}
	secondDownload := downloadRequest(h, http.MethodGet, secondTicket.URL, "")
	if secondDownload.Code != http.StatusOK || secondDownload.Body.String() != "second instance" {
		t.Fatalf("second download = %d %q", secondDownload.Code, secondDownload.Body.String())
	}
}

func TestMissingOrRemovedInstanceMappingFailsBeforeMetadataResolution(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "book.epub")
	if err := os.WriteFile(mediaPath, []byte("book"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &fakeInstanceStore{
		instances: map[string]*instance.Instance{
			"books-1": {
				ID:                "books-1",
				ServiceType:       "chaptarr",
				MediaDownloadMode: instance.MediaDownloadModeDisabled,
			},
		},
		allowed: true,
	}
	resolver := &fakeMetadataResolver{paths: map[int]string{
		19: "/ebooks/book.epub",
		20: "/audiobooks/book.m4b",
	}}
	h := newTestHandler(t, store, resolver, []string{root})

	disabled, _ := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-1","file_id":19}`)
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled issue status = %d, body = %s", disabled.Code, disabled.Body.String())
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("disabled instance resolved metadata: %#v", resolver.calls)
	}

	store.instances["books-1"].MediaDownloadMode = instance.MediaDownloadModeMapped
	store.instances["books-1"].MediaPathMappings = []mediapath.Mapping{{
		ArrPath:       "/ebooks",
		CantinarrPath: root,
	}}
	unmapped, _ := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-1","file_id":20}`)
	if unmapped.Code != http.StatusConflict {
		t.Fatalf("unmapped file issue status = %d, body = %s", unmapped.Code, unmapped.Body.String())
	}
	if strings.Contains(unmapped.Body.String(), "/ebooks") ||
		strings.Contains(unmapped.Body.String(), "/audiobooks") ||
		strings.Contains(unmapped.Body.String(), root) {
		t.Fatalf("unmapped file response leaked a path: %s", unmapped.Body.String())
	}
	issued, ticket := issueTicket(t, h, &auth.Claims{UserID: 1, Role: auth.RoleAdmin}, `{"instance_id":"books-1","file_id":19}`)
	if issued.Code != http.StatusCreated {
		t.Fatalf("mapped issue status = %d, body = %s", issued.Code, issued.Body.String())
	}
	resolvesAfterIssue := len(resolver.calls)

	store.instances["books-1"].MediaDownloadMode = instance.MediaDownloadModeDisabled
	store.instances["books-1"].MediaPathMappings = nil
	revoked := downloadRequest(h, http.MethodGet, ticket.URL, "")
	if revoked.Code != http.StatusNotFound {
		t.Fatalf("revoked mapping download status = %d, body = %s", revoked.Code, revoked.Body.String())
	}
	if len(resolver.calls) != resolvesAfterIssue {
		t.Fatal("revoked mapping resolved metadata before rejecting the ticket")
	}
}

func TestSafeFilenameRemovesHeaderControls(t *testing.T) {
	got := safeFilename("folder/evil\r\nX-Evil: injected.epub", 5)
	if got != "evilX-Evil: injected.epub" {
		t.Fatalf("safeFilename() = %q", got)
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": got})
	if strings.ContainsAny(disposition, "\r\n") {
		t.Fatalf("Content-Disposition contains a header control: %q", disposition)
	}
}

func newTestHandler(t *testing.T, store *fakeInstanceStore, resolver metadataResolver, roots []string) *Handler {
	t.Helper()
	// Tests written for the original global-root behavior model a database that
	// has crossed the compatibility migration, where existing arr instances use
	// identity mappings until an admin replaces or disables them.
	for _, inst := range store.instances {
		if inst.MediaDownloadMode == "" {
			inst.MediaDownloadMode = instance.MediaDownloadModeIdentity
		}
	}
	h, err := newHandler(store, store, resolver, roots)
	if err != nil {
		t.Fatalf("newHandler() error = %v", err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return h
}

func issueTicket(t *testing.T, h *Handler, claims *auth.Claims, body string) (*httptest.ResponseRecorder, issueTicketResponse) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/media-files/tickets", strings.NewReader(body))
	if claims != nil {
		ctx := context.WithValue(request.Context(), auth.ClaimsKey, claims)
		request = request.WithContext(ctx)
	}
	response := httptest.NewRecorder()
	h.IssueTicket(response, request)
	var decoded issueTicketResponse
	if response.Code == http.StatusCreated {
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode ticket response: %v; body = %s", err, response.Body.String())
		}
	}
	return response, decoded
}

func downloadRequest(h *Handler, method, url, byteRange string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Get("/api/media-files/download/{ticket}", h.Download)
	router.Head("/api/media-files/download/{ticket}", h.Download)
	request := httptest.NewRequest(method, url, nil)
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertDownloadHeaders(t *testing.T, response *httptest.ResponseRecorder, expectedFilename string) {
	t.Helper()
	for name, want := range map[string]string{
		"Cache-Control":                "private, no-store",
		"Content-Security-Policy":      "sandbox; default-src 'none'",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Pragma":                       "no-cache",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if response.Code < http.StatusBadRequest && response.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if expectedFilename != "" {
		mediaType, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
		if err != nil || mediaType != "attachment" || params["filename"] != expectedFilename {
			t.Errorf("Content-Disposition = %q (type %q params %#v err %v)", response.Header().Get("Content-Disposition"), mediaType, params, err)
		}
	}
}
