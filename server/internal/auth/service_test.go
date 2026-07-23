package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/windoze95/cantinarr-server/internal/db"
)

func setupTestService(t *testing.T) *Service {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	svc := NewService(database, "test-secret-key")
	if err := svc.EnsureAdmin("testpass123"); err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	return svc
}

func TestNativePasskeyStatusFromRequest(t *testing.T) {
	svc := NewService(nil, "test-secret-key", WebAuthnConfig{
		AppleAppIDs:             []string{"TEAMID.codes.julian.cantinarr"},
		AndroidCertFingerprints: []string{"AA:BB"},
	})

	req := httptest.NewRequest("GET", "https://example.com/api/auth/status", nil)
	status := svc.nativePasskeyStatusFromRequest(req)
	if !status.AppleConfigured || !status.AndroidConfigured || !status.WindowsOriginTrusted {
		t.Fatalf("status = %+v, want all native passkey surfaces configured", status)
	}

	req = httptest.NewRequest("GET", "https://127.0.0.1/api/auth/status", nil)
	status = svc.nativePasskeyStatusFromRequest(req)
	if status.AppleConfigured || status.AndroidConfigured {
		t.Fatalf("status = %+v, want Apple and Android unavailable on IP hosts", status)
	}

	req = httptest.NewRequest("GET", "https://example.com:8585/api/auth/status", nil)
	status = svc.nativePasskeyStatusFromRequest(req)
	if status.WindowsOriginTrusted {
		t.Fatalf("WindowsOriginTrusted = true for non-default port without extra origin")
	}

	svc = NewService(nil, "test-secret-key", WebAuthnConfig{
		ExtraOrigins: []string{"https://example.com"},
	})
	status = svc.nativePasskeyStatusFromRequest(req)
	if !status.WindowsOriginTrusted {
		t.Fatalf("WindowsOriginTrusted = false with matching extra origin")
	}

	req = httptest.NewRequest("GET", "http://example.com/api/auth/status", nil)
	status = svc.nativePasskeyStatusFromRequest(req)
	if status.AppleConfigured || status.AndroidConfigured || status.WindowsOriginTrusted {
		t.Fatalf("status = %+v, want native passkeys unavailable over insecure origin", status)
	}
}

func TestListUsers_ReportsDeviceAndInviteState(t *testing.T) {
	svc := setupTestService(t)

	// admin logs in -> gets an active device
	if _, err := svc.Login("admin", "testpass123", "", ""); err != nil {
		t.Fatalf("login: %v", err)
	}

	// Invite a new user via connect link (unredeemed -> pending invite)
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}

	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}

	byName := map[string]UserSummary{}
	for _, u := range users {
		byName[u.Username] = u
	}

	admin := byName["admin"]
	if admin.Role != RoleAdmin || admin.DeviceCount != 1 || !admin.HasPassword || admin.HasPendingInvite ||
		!admin.PasswordEnabled || !admin.PasskeyEnabled {
		t.Fatalf("unexpected admin summary: %+v", admin)
	}

	// New invite users default to no password/passkey ability — just a session.
	guest := byName["guest"]
	if guest.Role != RoleUser || guest.DeviceCount != 0 || guest.HasPassword || !guest.HasPendingInvite ||
		guest.PasswordEnabled || guest.PasskeyEnabled {
		t.Fatalf("unexpected guest summary: %+v", guest)
	}
}

func TestSharedAIAccessDefaultsAndAdminToggle(t *testing.T) {
	svc := setupTestService(t)
	if _, err := svc.CreateConnectToken(1, "guest-ai", "http://example.com"); err != nil {
		t.Fatal(err)
	}
	users, err := svc.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	var admin, guest *UserSummary
	for i := range users {
		switch users[i].Username {
		case "admin":
			admin = &users[i]
		case "guest-ai":
			guest = &users[i]
		}
	}
	if admin == nil || !admin.AISharedEnabled {
		t.Fatalf("initial admin shared access = %#v, want enabled", admin)
	}
	if guest == nil || guest.AISharedEnabled {
		t.Fatalf("new invited user shared access = %#v, want disabled", guest)
	}
	updated, err := svc.SetUserAISharedAccess(guest.ID, true)
	if err != nil || !updated.AISharedEnabled {
		t.Fatalf("enable shared AI = %#v, %v", updated, err)
	}
	if _, err := svc.SetUserAISharedAccess(99999, true); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing user error = %v, want ErrUserNotFound", err)
	}
}

// AUTH-027: Interactive dispatch rechecks the current role, device, and grant.
func TestAuthorizeInteractiveToolCallRechecksRoleDeviceAndSharedGrant(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	adminLogin, err := svc.Login("admin", "testpass123", "Admin Device", "admin-device")
	if err != nil {
		t.Fatalf("login admin: %v", err)
	}
	role, err := svc.AuthorizeInteractiveToolCall(ctx, adminLogin.User.ID, adminLogin.DeviceID, true)
	if err != nil || role != RoleAdmin {
		t.Fatalf("authorize shared admin = role %q, err %v", role, err)
	}

	if _, err := svc.SetUserAISharedAccess(adminLogin.User.ID, false); err != nil {
		t.Fatalf("revoke admin shared access: %v", err)
	}
	if _, err := svc.AuthorizeInteractiveToolCall(ctx, adminLogin.User.ID, adminLogin.DeviceID, true); !errors.Is(err, ErrSharedAIAccessRevoked) {
		t.Fatalf("revoked admin shared grant error = %v, want ErrSharedAIAccessRevoked", err)
	}
	role, err = svc.AuthorizeInteractiveToolCall(ctx, adminLogin.User.ID, adminLogin.DeviceID, false)
	if err != nil || role != RoleAdmin {
		t.Fatalf("personal admin after shared revoke = role %q, err %v", role, err)
	}
	if _, err := svc.SetUserAISharedAccess(adminLogin.User.ID, true); err != nil {
		t.Fatalf("restore admin shared access: %v", err)
	}

	connect, err := svc.CreateConnectToken(adminLogin.User.ID, "tool-user", "http://example.com")
	if err != nil {
		t.Fatalf("create user connect token: %v", err)
	}
	connectURL, err := url.Parse(connect.Link)
	if err != nil {
		t.Fatalf("parse connect link: %v", err)
	}
	userLogin, err := svc.RedeemConnectToken(connectURL.Query().Get("token"), "User Device", "user-device")
	if err != nil {
		t.Fatalf("redeem user connect token: %v", err)
	}
	role, err = svc.AuthorizeInteractiveToolCall(ctx, userLogin.User.ID, userLogin.DeviceID, false)
	if err != nil || role != RoleUser {
		t.Fatalf("authorize personal user = role %q, err %v", role, err)
	}
	if _, err := svc.AuthorizeInteractiveToolCall(ctx, userLogin.User.ID, userLogin.DeviceID, true); !errors.Is(err, ErrSharedAIAccessRevoked) {
		t.Fatalf("ungranted user shared error = %v, want ErrSharedAIAccessRevoked", err)
	}
	if _, err := svc.SetUserAISharedAccess(userLogin.User.ID, true); err != nil {
		t.Fatalf("grant user shared access: %v", err)
	}
	role, err = svc.AuthorizeInteractiveToolCall(ctx, userLogin.User.ID, userLogin.DeviceID, true)
	if err != nil || role != RoleUser {
		t.Fatalf("authorize granted shared user = role %q, err %v", role, err)
	}

	// Role comes from the same live authorization query, not the access-token
	// snapshot that started the model turn.
	if _, err := svc.db.Exec("UPDATE users SET role = ? WHERE id = ?", RoleUser, adminLogin.User.ID); err != nil {
		t.Fatalf("demote admin in database: %v", err)
	}
	role, err = svc.AuthorizeInteractiveToolCall(ctx, adminLogin.User.ID, adminLogin.DeviceID, true)
	if err != nil || role != RoleUser {
		t.Fatalf("authorize after demotion = role %q, err %v", role, err)
	}
	if err := svc.AuthorizePermission(ctx, adminLogin.User.ID, adminLogin.DeviceID, PermissionCredentialsManage); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("credential permission after demotion = %v, want ErrPermissionDenied", err)
	}
	if err := svc.AuthorizePermission(ctx, adminLogin.User.ID, adminLogin.DeviceID, PermissionAIChat); err != nil {
		t.Fatalf("user AI permission after demotion = %v", err)
	}

	if _, err := svc.AuthorizeInteractiveToolCall(ctx, userLogin.User.ID, adminLogin.DeviceID, false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("cross-user device error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.AuthorizeInteractiveToolCall(ctx, userLogin.User.ID, "", false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing device error = %v, want ErrInvalidCredentials", err)
	}
	if err := svc.RevokeDevice(userLogin.DeviceID); err != nil {
		t.Fatalf("revoke user device: %v", err)
	}
	if _, err := svc.AuthorizeInteractiveToolCall(ctx, userLogin.User.ID, userLogin.DeviceID, false); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device error = %v, want ErrDeviceRevoked", err)
	}
	if err := svc.AuthorizePermission(ctx, userLogin.User.ID, userLogin.DeviceID, PermissionAIChat); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device permission error = %v, want ErrDeviceRevoked", err)
	}
}

// inviteGuest creates a connect-link "guest" user (password/passkey disabled by
// default) and returns its ID.
func inviteGuest(t *testing.T, svc *Service) int64 {
	t.Helper()
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}
	return guestID
}

func enableMethod(t *testing.T, svc *Service, userID int64, password, passkey *bool) {
	t.Helper()
	if _, err := svc.SetUserAuthMethods(userID, password, passkey); err != nil {
		t.Fatalf("set auth methods: %v", err)
	}
}

func TestUpdateUserRole_PromoteAndDemote(t *testing.T) {
	svc := setupTestService(t)
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}

	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}

	updated, err := svc.UpdateUserRole(guestID, RoleAdmin)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if updated.Role != RoleAdmin {
		t.Fatalf("role = %q, want %q", updated.Role, RoleAdmin)
	}

	if _, err := svc.UpdateUserRole(guestID, "superuser"); err != ErrInvalidRole {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}

	if _, err := svc.UpdateUserRole(guestID, RoleUser); err != nil {
		t.Fatalf("demote: %v", err)
	}
}

func TestUpdateUserRole_CannotDemoteLastAdmin(t *testing.T) {
	svc := setupTestService(t)

	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}

	if _, err := svc.UpdateUserRole(adminID, RoleUser); err != ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestDeleteUser_RemovesUserAndDevices(t *testing.T) {
	svc := setupTestService(t)

	// Invite a guest and redeem the link so they have a device + refresh token.
	tok, err := svc.CreateConnectToken(1, "guest", "http://example.com")
	if err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	token := tok.Link[strings.Index(tok.Link, "token=")+len("token=") : strings.Index(tok.Link, "&server=")]
	resp, err := svc.RedeemConnectToken(token, "Guest Phone", "")
	if err != nil {
		t.Fatalf("redeem token: %v", err)
	}

	if err := svc.DeleteUser(1, resp.User.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", resp.User.ID).Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatal("user was not deleted")
	}
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM devices WHERE user_id = ?", resp.User.ID).Scan(&count); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if count != 0 {
		t.Fatal("devices were not cleaned up")
	}
}

func TestDeleteUser_TransfersDurableBookRequestJobs(t *testing.T) {
	svc := setupTestService(t)
	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = 'admin'").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}
	result, err := svc.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('book-job-owner', '', 'user')",
	)
	if err != nil {
		t.Fatalf("create book job owner: %v", err)
	}
	ownerID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read book job owner id: %v", err)
	}
	const instanceID = "delete-user-books"
	if _, err := svc.db.Exec(
		`INSERT INTO service_instances (id, service_type, name, url, api_key)
		 VALUES (?, 'chaptarr', 'Books', 'http://books.invalid', '')`,
		instanceID,
	); err != nil {
		t.Fatalf("create book instance: %v", err)
	}

	type expectedJob struct {
		foreignID, title, format, state, phase, phaseFormat string
		authorID, bookID, acknowledged, attempts            int
		foreignAuthorID, authorName                         string
		ebookStatus, audiobookStatus                        string
		fingerprint, nextAttempt, generation                string
		errorCode, errorText, phaseStarted                  string
	}
	jobs := []expectedJob{
		{
			foreignID: "running-work", title: "Running Work", format: "ebook",
			state: "running", phase: "queued", fingerprint: "010203", attempts: 1,
			nextAttempt: "2030-01-01 01:02:03", generation: "running-generation",
			errorCode: "book_request_retry", errorText: "running detail", phaseStarted: "2029-01-01 01:02:03",
		},
		{
			foreignID: "unknown-work", title: "Unknown Work", format: "audiobook",
			state: "outcome_unknown", phase: "search_inflight", phaseFormat: "audiobook",
			authorID: 41, bookID: 51, acknowledged: 0, attempts: 2,
			foreignAuthorID: "author-unknown", authorName: "Unknown Author",
			ebookStatus: "available", fingerprint: "040506",
			nextAttempt: "2030-02-01 01:02:03", generation: "unknown-generation",
			errorCode: "book_search_unconfirmed", errorText: "unknown detail", phaseStarted: "2029-02-01 01:02:03",
		},
		{
			foreignID: "failed-work", title: "Failed Work", format: "both",
			state: "failed", phase: "converging", phaseFormat: "ebook",
			authorID: 42, bookID: 52, acknowledged: 1, attempts: 3,
			foreignAuthorID: "author-failed", authorName: "Failed Author",
			audiobookStatus: "requested", fingerprint: "070809",
			nextAttempt: "2030-03-01 01:02:03", generation: "failed-generation",
			errorCode: "book_configuration_invalid", errorText: "failed detail", phaseStarted: "2029-03-01 01:02:03",
		},
	}
	for _, job := range jobs {
		if _, err := svc.db.Exec(
			`INSERT INTO book_request_jobs
			 (user_id, instance_id, foreign_id, title, book_format, state, phase,
			  phase_format, author_id, foreign_author_id, author_name, book_id,
			  search_acknowledged, ebook_status, audiobook_status, settings_fingerprint,
			  attempt_count, next_attempt_at, proc_generation, last_error_code,
			  last_error_text, phase_started_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, X'`+job.fingerprint+`', ?, ?, ?, ?, ?, ?)`,
			ownerID, instanceID, job.foreignID, job.title, job.format, job.state, job.phase,
			job.phaseFormat, job.authorID, job.foreignAuthorID, job.authorName, job.bookID,
			job.acknowledged, job.ebookStatus, job.audiobookStatus, job.attempts,
			job.nextAttempt, job.generation, job.errorCode, job.errorText, job.phaseStarted,
		); err != nil {
			t.Fatalf("insert %s job: %v", job.state, err)
		}
	}

	if err := svc.DeleteUser(adminID, ownerID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var users int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM users WHERE id = ?", ownerID).Scan(&users); err != nil {
		t.Fatalf("count deleted user: %v", err)
	}
	if users != 0 {
		t.Fatalf("deleted user count = %d, want 0", users)
	}
	for _, want := range jobs {
		var (
			owner                                               int64
			foreignID, title, format, state, phase, phaseFormat string
			authorID, bookID, acknowledged, attempts            int
			foreignAuthorID, authorName                         string
			ebookStatus, audiobookStatus                        string
			fingerprint, nextAttempt, generation                string
			errorCode, errorText, phaseStarted                  string
		)
		if err := svc.db.QueryRow(
			`SELECT user_id, foreign_id, title, book_format, state, phase, phase_format,
			 author_id, foreign_author_id, author_name, book_id, search_acknowledged,
			 ebook_status, audiobook_status, hex(settings_fingerprint), attempt_count,
			 CAST(next_attempt_at AS TEXT), proc_generation, last_error_code,
			 last_error_text, CAST(phase_started_at AS TEXT)
			 FROM book_request_jobs WHERE instance_id = ? AND foreign_id = ?`,
			instanceID, want.foreignID,
		).Scan(
			&owner, &foreignID, &title, &format, &state, &phase, &phaseFormat,
			&authorID, &foreignAuthorID, &authorName, &bookID, &acknowledged,
			&ebookStatus, &audiobookStatus, &fingerprint, &attempts, &nextAttempt,
			&generation, &errorCode, &errorText, &phaseStarted,
		); err != nil {
			t.Fatalf("load transferred %s job: %v", want.state, err)
		}
		if owner != adminID || foreignID != want.foreignID || title != want.title ||
			format != want.format || state != want.state || phase != want.phase ||
			phaseFormat != want.phaseFormat || authorID != want.authorID ||
			foreignAuthorID != want.foreignAuthorID || authorName != want.authorName ||
			bookID != want.bookID || acknowledged != want.acknowledged ||
			ebookStatus != want.ebookStatus || audiobookStatus != want.audiobookStatus ||
			fingerprint != strings.ToUpper(want.fingerprint) || attempts != want.attempts ||
			nextAttempt != want.nextAttempt || generation != want.generation ||
			errorCode != want.errorCode || errorText != want.errorText ||
			phaseStarted != want.phaseStarted {
			t.Fatalf("transferred %s job changed: owner=%d state=%q phase=%q format=%q code=%q", want.state, owner, state, phase, format, errorCode)
		}
	}
}

func TestDeleteUser_PreservesPendingApprovalAndAnonymizesDecidedHistory(t *testing.T) {
	svc := setupTestService(t)
	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = 'admin'").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}
	result, err := svc.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES ('pending-book-owner', '', 'user')",
	)
	if err != nil {
		t.Fatalf("create pending owner: %v", err)
	}
	ownerID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read pending owner id: %v", err)
	}
	const instanceID = "delete-linked-approval"
	if _, err := svc.db.Exec(
		`INSERT INTO service_instances (id, service_type, name, url, api_key)
		 VALUES (?, 'chaptarr', 'Books', 'http://books.invalid', '')`,
		instanceID,
	); err != nil {
		t.Fatalf("create book instance: %v", err)
	}
	pendingResult, err := svc.db.Exec(
		`INSERT INTO request_log
		 (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status)
		 VALUES (?, 0, 'pending-work', 'audiobook', ?, 'book', 'Pending Work', 'pending')`,
		ownerID, instanceID,
	)
	if err != nil {
		t.Fatalf("create pending request: %v", err)
	}
	pendingID, err := pendingResult.LastInsertId()
	if err != nil {
		t.Fatalf("read pending request id: %v", err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO request_log
		 (user_id, tmdb_id, foreign_id, book_format, instance_id, media_type, title, status, approved_by)
		 VALUES (?, 0, 'old-work', 'ebook', ?, 'book', 'Old Work', 'requested', ?)`,
		ownerID, instanceID, ownerID,
	); err != nil {
		t.Fatalf("create decided history: %v", err)
	}
	if _, err := svc.db.Exec(
		`INSERT INTO book_request_jobs
		 (user_id, request_id, approved_by, instance_id, foreign_id, title,
		  book_format, state, phase, settings_fingerprint)
		 VALUES (?, ?, ?, ?, 'pending-work', 'Pending Work', 'audiobook',
		         'retry_wait', 'seed_inflight', X'010203')`,
		ownerID, pendingID, ownerID, instanceID,
	); err != nil {
		t.Fatalf("create linked approval job: %v", err)
	}

	if err := svc.DeleteUser(adminID, ownerID); err != nil {
		t.Fatalf("delete pending owner: %v", err)
	}
	var pendingOwner int64
	if err := svc.db.QueryRow("SELECT user_id FROM request_log WHERE id = ?", pendingID).Scan(&pendingOwner); err != nil {
		t.Fatalf("load transferred pending request: %v", err)
	}
	if pendingOwner != adminID {
		t.Fatalf("pending request owner = %d, want acting admin %d", pendingOwner, adminID)
	}
	var jobOwner, jobApprover, linkedRequestID int64
	if err := svc.db.QueryRow(
		`SELECT user_id, COALESCE(approved_by, 0), COALESCE(request_id, 0)
		 FROM book_request_jobs WHERE foreign_id = 'pending-work'`,
	).Scan(&jobOwner, &jobApprover, &linkedRequestID); err != nil {
		t.Fatalf("load transferred approval job: %v", err)
	}
	if jobOwner != adminID || jobApprover != 0 || linkedRequestID != pendingID {
		t.Fatalf("transferred approval job = owner %d approver %d request %d", jobOwner, jobApprover, linkedRequestID)
	}
	var historyOwner, historyApprover int64
	if err := svc.db.QueryRow(
		`SELECT COALESCE(user_id, 0), COALESCE(approved_by, 0)
		 FROM request_log WHERE foreign_id = 'old-work'`,
	).Scan(&historyOwner, &historyApprover); err != nil {
		t.Fatalf("load anonymized request history: %v", err)
	}
	if historyOwner != 0 || historyApprover != 0 {
		t.Fatalf("anonymized history = owner %d approver %d, want both null", historyOwner, historyApprover)
	}
}

func TestRedeemConnectToken_DedupesByHardwareID(t *testing.T) {
	svc := setupTestService(t)

	// Redeems a fresh connect link for "guest" and returns the device id. Each
	// connect token is single-use, so a reconnect needs a brand-new link.
	redeem := func(name, hardwareID string) string {
		t.Helper()
		tok, err := svc.CreateConnectToken(1, "guest", "http://example.com")
		if err != nil {
			t.Fatalf("create connect token: %v", err)
		}
		token := tok.Link[strings.Index(tok.Link, "token=")+len("token=") : strings.Index(tok.Link, "&server=")]
		resp, err := svc.RedeemConnectToken(token, name, hardwareID)
		if err != nil {
			t.Fatalf("redeem token: %v", err)
		}
		return resp.DeviceID
	}

	guestDeviceCount := func() int {
		t.Helper()
		devices, err := svc.ListDevices()
		if err != nil {
			t.Fatalf("list devices: %v", err)
		}
		n := 0
		for _, d := range devices {
			if d.Username == "guest" {
				n++
			}
		}
		return n
	}

	// Same physical device reconnects (new link, same hardware id): the row is
	// reused and its name refreshed to the newest — not duplicated.
	first := redeem("iPhone 15", "HW-AAA")
	second := redeem("Apple iPhone 16 Pro Max", "HW-AAA")
	if first != second {
		t.Fatalf("reconnect should reuse device id %q, got %q", first, second)
	}
	if n := guestDeviceCount(); n != 1 {
		t.Fatalf("expected 1 deduped device, got %d", n)
	}

	devices, _ := svc.ListDevices()
	for _, d := range devices {
		if d.ID == first && d.DeviceName != "Apple iPhone 16 Pro Max" {
			t.Fatalf("device name should refresh to newest, got %q", d.DeviceName)
		}
	}

	// A different physical device (distinct hardware id) is its own row.
	redeem("Apple iPad Pro 11", "HW-BBB")
	if n := guestDeviceCount(); n != 2 {
		t.Fatalf("expected 2 devices after a distinct hardware id, got %d", n)
	}

	// No hardware id (e.g. web) never dedupes: each redeem is its own row.
	redeem("Chrome on macOS", "")
	redeem("Chrome on macOS", "")
	if n := guestDeviceCount(); n != 4 {
		t.Fatalf("expected 4 devices (2 deduped + 2 web), got %d", n)
	}
}

func TestLogin_DedupesDeviceByHardwareID(t *testing.T) {
	svc := setupTestService(t)

	adminDeviceCount := func() int {
		t.Helper()
		devices, err := svc.ListDevices()
		if err != nil {
			t.Fatalf("list devices: %v", err)
		}
		n := 0
		for _, d := range devices {
			if d.Username == "admin" {
				n++
			}
		}
		return n
	}

	// Password login routes through the same shared upsert as connect links, so
	// re-logging in on the same physical device reuses its row (newest name)
	// instead of stacking duplicates.
	first, err := svc.Login("admin", "testpass123", "Yana's Mac", "HW-MAC")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	second, err := svc.Login("admin", "testpass123", "Apple MacBook Pro", "HW-MAC")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if first.DeviceID != second.DeviceID {
		t.Fatalf("same-hardware login should reuse device %q, got %q", first.DeviceID, second.DeviceID)
	}
	if n := adminDeviceCount(); n != 1 {
		t.Fatalf("expected 1 deduped admin device, got %d", n)
	}

	// A login with no hardware id (older client) never dedupes.
	if _, err := svc.Login("admin", "testpass123", "Admin", ""); err != nil {
		t.Fatalf("login: %v", err)
	}
	if n := adminDeviceCount(); n != 2 {
		t.Fatalf("expected 2 admin devices after a no-hardware login, got %d", n)
	}
}

func TestDeleteUser_Guards(t *testing.T) {
	svc := setupTestService(t)

	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}

	if err := svc.DeleteUser(adminID, adminID); err != ErrCannotDeleteSelf {
		t.Fatalf("expected ErrCannotDeleteSelf, got %v", err)
	}

	// Promote a second admin so the self-delete guard isn't what's tripping,
	// then ensure the last-admin guard still protects the remaining admin.
	if _, err := svc.CreateConnectToken(adminID, "second", "http://example.com"); err != nil {
		t.Fatalf("create token: %v", err)
	}
	var secondID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "second").Scan(&secondID); err != nil {
		t.Fatalf("load second: %v", err)
	}
	if err := svc.DeleteUser(secondID, adminID); err != ErrLastAdmin {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestSetPassword_EnablesLoginForInviteUser(t *testing.T) {
	svc := setupTestService(t)

	// Invite a user via connect link — they start with no password.
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}

	// An admin must enable password sign-in before the user can set one.
	enabled := true
	enableMethod(t, svc, guestID, &enabled, nil)

	// Before setting a password, neither password login path should work.
	if _, err := svc.Login("guest", "hunter2!", "", ""); err == nil {
		t.Fatal("login should fail before a password is set")
	}
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err == nil {
		t.Fatal("password auth should fail before a password is set")
	}

	if err := svc.SetPassword(guestID, "hunter2!"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// Both the app login and the MCP/OAuth password path should now succeed.
	if _, err := svc.Login("guest", "hunter2!", "", ""); err != nil {
		t.Fatalf("login after set password: %v", err)
	}
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err != nil {
		t.Fatalf("authenticate password after set password: %v", err)
	}

	// The admin user list should now report the account as having a password.
	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	var found bool
	for _, u := range users {
		if u.ID == guestID {
			found = true
			if !u.HasPassword {
				t.Fatalf("guest summary HasPassword = false, want true")
			}
		}
	}
	if !found {
		t.Fatal("guest user missing from list")
	}
}

func TestSetPassword_RejectsTooShort(t *testing.T) {
	svc := setupTestService(t)
	if _, err := svc.CreateConnectToken(1, "guest", "http://example.com"); err != nil {
		t.Fatalf("create connect token: %v", err)
	}
	var guestID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "guest").Scan(&guestID); err != nil {
		t.Fatalf("load guest: %v", err)
	}

	enabled := true
	enableMethod(t, svc, guestID, &enabled, nil)

	if err := svc.SetPassword(guestID, "short"); err != ErrPasswordTooShort {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
	// A too-short password must not have been written.
	if _, err := svc.AuthenticatePassword("guest", "short"); err == nil {
		t.Fatal("too-short password should not have been stored")
	}
}

func TestSetPassword_ReplacesExisting(t *testing.T) {
	svc := setupTestService(t)

	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}

	if err := svc.SetPassword(adminID, "rotated-secret"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// The old password must stop working, the new one must work.
	if _, err := svc.Login("admin", "testpass123", "", ""); err == nil {
		t.Fatal("old password should no longer authenticate")
	}
	if _, err := svc.Login("admin", "rotated-secret", "", ""); err != nil {
		t.Fatalf("login with rotated password: %v", err)
	}
}

func TestSetPassword_UnknownUser(t *testing.T) {
	svc := setupTestService(t)
	if err := svc.SetPassword(999999, "long-enough"); err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestSetPassword_RequiresEnabled(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	// Disabled by default: the user cannot create a password.
	if err := svc.SetPassword(guestID, "hunter2!"); err != ErrPasswordNotAllowed {
		t.Fatalf("expected ErrPasswordNotAllowed, got %v", err)
	}
}

func TestSetPlexEmail_StoresTrimsAndReportsChange(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	changed, err := svc.SetPlexEmail(guestID, "  pirate@example.com ")
	if err != nil {
		t.Fatalf("set plex email: %v", err)
	}
	if !changed {
		t.Fatal("first submission should report changed")
	}

	user, err := svc.GetUser(guestID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.PlexEmail != "pirate@example.com" {
		t.Fatalf("expected trimmed email, got %q", user.PlexEmail)
	}

	// Resubmitting the same address is a no-op so admins aren't re-notified.
	changed, err = svc.SetPlexEmail(guestID, "pirate@example.com")
	if err != nil {
		t.Fatalf("resubmit plex email: %v", err)
	}
	if changed {
		t.Fatal("identical resubmission should not report changed")
	}

	// Simulate an invite having been sent to the first address.
	if _, err := svc.db.Exec("UPDATE users SET plex_invited_at = CURRENT_TIMESTAMP WHERE id = ?", guestID); err != nil {
		t.Fatalf("stamp invited: %v", err)
	}

	// A different address is a change again, shows up in ListUsers, and
	// clears the invited stamp (that invite went to the old email).
	changed, err = svc.SetPlexEmail(guestID, "corsair@example.com")
	if err != nil || !changed {
		t.Fatalf("expected changed update, got changed=%v err=%v", changed, err)
	}
	user, err = svc.GetUser(guestID)
	if err != nil {
		t.Fatalf("get user after change: %v", err)
	}
	if user.PlexInvitedAt != nil {
		t.Fatal("changing the email must clear plex_invited_at")
	}
	users, err := svc.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	found := false
	for _, u := range users {
		if u.ID == guestID {
			found = true
			if u.PlexEmail != "corsair@example.com" {
				t.Fatalf("ListUsers plex email = %q", u.PlexEmail)
			}
		}
	}
	if !found {
		t.Fatal("guest missing from ListUsers")
	}
}

func TestSetPlexEmail_RejectsInvalid(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	for _, bad := range []string{
		"",
		"   ",
		"no-at-sign",
		"@nothing-before",
		"nothing-after@",
		"has space@example.com",
		strings.Repeat("a", 250) + "@example.com",
	} {
		if _, err := svc.SetPlexEmail(guestID, bad); err != ErrInvalidPlexEmail {
			t.Fatalf("email %q: expected ErrInvalidPlexEmail, got %v", bad, err)
		}
	}

	user, err := svc.GetUser(guestID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.PlexEmail != "" {
		t.Fatalf("rejected emails must not be stored, got %q", user.PlexEmail)
	}

	if _, err := svc.SetPlexEmail(999999, "ok@example.com"); err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestSetUserAuthMethods_EnableAndRevokePassword(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	enabled, disabled := true, false
	enableMethod(t, svc, guestID, &enabled, nil)

	if err := svc.SetPassword(guestID, "hunter2!"); err != nil {
		t.Fatalf("set password after enable: %v", err)
	}
	if _, err := svc.Login("guest", "hunter2!", "", ""); err != nil {
		t.Fatalf("login after enable+set: %v", err)
	}

	// Disabling is a real revoke: it clears the password and blocks login.
	enableMethod(t, svc, guestID, &disabled, nil)
	if _, err := svc.Login("guest", "hunter2!", "", ""); err == nil {
		t.Fatal("login should fail after password disabled")
	}
	var hash string
	if err := svc.db.QueryRow("SELECT password_hash FROM users WHERE id = ?", guestID).Scan(&hash); err != nil {
		t.Fatalf("load hash: %v", err)
	}
	if hash != "" {
		t.Fatal("password hash should be cleared on disable")
	}
}

func TestAuthenticatePassword_RequiresEnabled(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	enabled, disabled := true, false
	enableMethod(t, svc, guestID, &enabled, nil)
	if err := svc.SetPassword(guestID, "hunter2!"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// The MCP/OAuth password path works while enabled.
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err != nil {
		t.Fatalf("authenticate password: %v", err)
	}

	// Disabling password revokes MCP password access too.
	enableMethod(t, svc, guestID, &disabled, nil)
	if _, err := svc.AuthenticatePassword("guest", "hunter2!"); err == nil {
		t.Fatal("MCP password auth should fail after disable")
	}
}

func TestBeginPasskeyRegistration_RequiresEnabled(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	req := httptest.NewRequest("POST", "https://example.com/api/auth/passkey/register/begin", nil)
	if _, _, err := svc.BeginPasskeyRegistration(guestID, req); err != ErrPasskeyNotAllowed {
		t.Fatalf("expected ErrPasskeyNotAllowed, got %v", err)
	}

	enabled := true
	enableMethod(t, svc, guestID, nil, &enabled)
	if _, _, err := svc.BeginPasskeyRegistration(guestID, req); err != nil {
		t.Fatalf("registration should begin once enabled: %v", err)
	}
}

func TestSetUserAuthMethods_RevokePasskeyDeletesCredentials(t *testing.T) {
	svc := setupTestService(t)
	guestID := inviteGuest(t, svc)

	// Seed a passkey credential as if the user had registered one.
	if _, err := svc.db.Exec(
		"INSERT INTO webauthn_credentials (id, user_id, public_key, attestation_type, rp_id) VALUES (?, ?, ?, ?, ?)",
		"credid", guestID, []byte("pk"), "none", "example.com",
	); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	enabled, disabled := true, false
	enableMethod(t, svc, guestID, nil, &enabled)
	enableMethod(t, svc, guestID, nil, &disabled)

	var count int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", guestID).Scan(&count); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 0 {
		t.Fatal("passkeys should be deleted on disable")
	}
}

func TestSetUserAuthMethods_CannotModifyAdmin(t *testing.T) {
	svc := setupTestService(t)
	var adminID int64
	if err := svc.db.QueryRow("SELECT id FROM users WHERE username = ?", "admin").Scan(&adminID); err != nil {
		t.Fatalf("load admin: %v", err)
	}
	disabled := false
	if _, err := svc.SetUserAuthMethods(adminID, &disabled, &disabled); err != ErrCannotModifyAdmin {
		t.Fatalf("expected ErrCannotModifyAdmin, got %v", err)
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	h1 := hashToken("my-token-value")
	h2 := hashToken("my-token-value")
	if h1 != h2 {
		t.Fatalf("same input produced different hashes: %s vs %s", h1, h2)
	}

	h3 := hashToken("different-token")
	if h1 == h3 {
		t.Fatal("different inputs produced the same hash")
	}
}

// mintLegacyRefreshJWT reproduces a refresh token exactly as pre-opaque
// versions issued them: an HS256 JWT with a long lifetime, bound to a device.
func mintLegacyRefreshJWT(t *testing.T, svc *Service, user *User, deviceID string, issuedAt time.Time, lifetime time.Duration) string {
	t.Helper()
	claims := &Claims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(issuedAt.Add(lifetime)),
			IssuedAt:  jwt.NewNumericDate(issuedAt),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(svc.jwtSecret)
	if err != nil {
		t.Fatalf("sign legacy refresh token: %v", err)
	}
	return token
}

func TestRefresh_OpaqueTokenIsStableAndReusable(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.HasPrefix(loginResp.RefreshToken, opaqueRefreshPrefix) {
		t.Fatalf("login should issue an opaque refresh token, got %q", loginResp.RefreshToken[:10])
	}

	// Refresh repeatedly with the same token: no rotation means every retry,
	// replayed request, or parallel refresh succeeds and the token is stable.
	for i := 0; i < 3; i++ {
		resp, err := svc.Refresh(loginResp.RefreshToken)
		if err != nil {
			t.Fatalf("refresh #%d: %v", i+1, err)
		}
		if resp.AccessToken == "" {
			t.Fatalf("refresh #%d: missing access token", i+1)
		}
		if resp.RefreshToken != loginResp.RefreshToken {
			t.Fatalf("refresh #%d rotated the token; opaque tokens must be stable", i+1)
		}
		if resp.DeviceID != loginResp.DeviceID {
			t.Fatalf("refresh #%d device = %q, want %q", i+1, resp.DeviceID, loginResp.DeviceID)
		}
		if _, _, err := svc.AuthenticateToken(resp.AccessToken); err != nil {
			t.Fatalf("refresh #%d access token rejected: %v", i+1, err)
		}
	}
}

// TestRefresh_LegacyJWTAmnesty covers the migration contract: any legacy JWT
// refresh token whose device is still authorized is accepted — even when its
// baked-in expiry passed (device idle for months/years) or its store row was
// rotated away or lost (crash before persisting, restore from backup) — and
// the session is upgraded to a never-expiring opaque token.
func TestRefresh_LegacyJWTAmnesty(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	user, err := svc.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	cases := []struct {
		name     string
		issuedAt time.Time
		lifetime time.Duration
	}{
		{"active 365-day token with no store row", time.Now().Add(-time.Hour), 365 * 24 * time.Hour},
		{"expired 30-day token idle for two years", time.Now().Add(-2 * 365 * 24 * time.Hour), 30 * 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := mintLegacyRefreshJWT(t, svc, user, loginResp.DeviceID, tc.issuedAt, tc.lifetime)
			// No refresh_tokens row exists for this token (simulates rotation
			// races, cleanup, and backup restores); amnesty must not care.
			resp, err := svc.Refresh(legacy)
			if err != nil {
				t.Fatalf("legacy refresh: %v", err)
			}
			if !strings.HasPrefix(resp.RefreshToken, opaqueRefreshPrefix) {
				t.Fatal("legacy refresh should migrate to an opaque token")
			}
			if _, err := svc.Refresh(resp.RefreshToken); err != nil {
				t.Fatalf("migrated opaque token rejected: %v", err)
			}
		})
	}
}

// TestRefresh_RejectsNonRefreshTokens locks the amnesty gate: short-lived
// access tokens, audience-bound (OAuth/setup) tokens, device-less tokens, and
// forgeries must never mint a session.
func TestRefresh_RejectsNonRefreshTokens(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	user, err := svc.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	// A real access token (15-minute lifetime) fails the lifetime bar.
	if _, err := svc.Refresh(loginResp.AccessToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("access token as refresh: err = %v, want ErrInvalidCredentials", err)
	}

	// Device-less long-lived JWT.
	deviceless := mintLegacyRefreshJWT(t, svc, user, "", time.Now(), 365*24*time.Hour)
	if _, err := svc.Refresh(deviceless); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("device-less token: err = %v, want ErrInvalidCredentials", err)
	}

	// Audience-bound token (OAuth-style), long-lived and device-bound.
	audClaims := &Claims{
		UserID:   user.ID,
		DeviceID: loginResp.DeviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"https://example.com/mcp"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(365 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	audToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, audClaims).SignedString(svc.jwtSecret)
	if err != nil {
		t.Fatalf("sign audience token: %v", err)
	}
	if _, err := svc.Refresh(audToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("audience-bound token: err = %v, want ErrInvalidCredentials", err)
	}

	// Wrong signature.
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{
		UserID:   user.ID,
		DeviceID: loginResp.DeviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(365 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}).SignedString([]byte("wrong-secret"))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}
	if _, err := svc.Refresh(forged); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("forged token: err = %v, want ErrInvalidCredentials", err)
	}

	// Unknown opaque token.
	if _, err := svc.Refresh(opaqueRefreshPrefix + strings.Repeat("ab", 32)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown opaque token: err = %v, want ErrInvalidCredentials", err)
	}
}

// TestRefresh_TransientFaultIsUnavailableNotRejection pins the 401/503 split:
// when the store cannot be consulted at all, the error must be
// ErrAuthUnavailable (handler → 503, client keeps its session), never a
// rejection that would erase the client's tokens.
func TestRefresh_TransientFaultIsUnavailableNotRejection(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	svc.db.Close()

	_, err = svc.Refresh(loginResp.RefreshToken)
	if !errors.Is(err, ErrAuthUnavailable) {
		t.Fatalf("refresh on closed DB: err = %v, want ErrAuthUnavailable", err)
	}
	if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("transient fault must not read as a rejection: %v", err)
	}
}

func TestGenerateTokens_FailsWhenStoreWriteFails(t *testing.T) {
	svc := setupTestService(t)
	user, err := svc.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	svc.db.Close()

	// A refresh token that was never stored would strand the device on its
	// first refresh, so issuance must fail loudly instead.
	if _, err := svc.generateTokens(user, "some-device"); err == nil {
		t.Fatal("generateTokens must fail when the refresh token cannot be stored")
	}
}

func TestDeviceRevocation_InvalidatesRefreshTokens(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := svc.RevokeDevice(loginResp.DeviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	// The opaque token dies with its deleted store row.
	_, err = svc.Refresh(loginResp.RefreshToken)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("refresh after revocation: err = %v, want ErrInvalidCredentials", err)
	}

	// The legacy amnesty path must not resurrect a revoked device either.
	user, err := svc.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	legacy := mintLegacyRefreshJWT(t, svc, user, loginResp.DeviceID, time.Now(), 365*24*time.Hour)
	if _, err := svc.Refresh(legacy); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("legacy refresh after revocation: err = %v, want ErrDeviceRevoked", err)
	}
}

// TestAuthMiddleware_TransientFaultIs503 pins the middleware side of the
// split: a valid access token evaluated against a broken store must yield
// 503 (retry), not 401 (client wipes its session).
func TestAuthMiddleware_TransientFaultIs503(t *testing.T) {
	svc := setupTestService(t)

	loginResp, err := svc.Login("admin", "testpass123", "", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	handler := svc.AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	svc.db.Close()

	req := httptest.NewRequest("GET", "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.AccessToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
