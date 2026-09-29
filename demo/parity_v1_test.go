package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"testing"
)

func TestV1AssignmentsKeepAccessSeparateFromRouting(t *testing.T) {
	stateMu.Lock()
	u := demoUsers[2]
	grants := append([]string{}, u.InstanceGrants[serviceRadarr]...)
	preferred := u.DefaultInstances[serviceRadarr]
	stateMu.Unlock()
	defer func() {
		stateMu.Lock()
		u.InstanceGrants[serviceRadarr] = grants
		if preferred == "" {
			delete(u.DefaultInstances, serviceRadarr)
		} else {
			u.DefaultInstances[serviceRadarr] = preferred
		}
		stateMu.Unlock()
	}()

	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	user := demoTestLogin(t, router, "user")
	path := "/api/instances/" + instRadarr + "/assignments"

	res := demoTestRequest(t, router, http.MethodPatch, path, user,
		[]byte(`{"action":"remove","user_ids":[2]}`))
	if res.Code != http.StatusForbidden {
		t.Fatalf("requester changed assignments: %d %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodPatch, path, admin,
		[]byte(`{"action":"remove","user_ids":[2]}`))
	if res.Code != http.StatusOK {
		t.Fatalf("remove assignment: %d %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodGet, "/api/config", user, nil)
	var config struct {
		Instances []struct {
			ID             string `json:"id"`
			Assigned       bool   `json:"assigned"`
			RequestDefault bool   `json:"request_default"`
		} `json:"instances"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &config) != nil {
		t.Fatalf("config after removal: %d %s", res.Code, res.Body.String())
	}
	seen4K := false
	for _, inst := range config.Instances {
		if inst.ID == instRadarr {
			t.Fatal("removed Radarr assignment remained visible")
		}
		if inst.ID == instRadarr4K {
			seen4K = inst.Assigned && inst.RequestDefault
		}
	}
	if !seen4K {
		t.Fatal("remaining Radarr assignment did not become request default")
	}
	res = demoTestRequest(t, router, http.MethodGet,
		"/api/requests/961/status?media_type=movie&instance_id="+instRadarr, user, nil)
	if res.Code != http.StatusForbidden {
		t.Fatalf("unassigned library status = %d: %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodPatch, path, admin,
		[]byte(`{"action":"add","user_ids":[2]}`))
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"assigned":true`)) {
		t.Fatalf("reassign library: %d %s", res.Code, res.Body.String())
	}
}

func TestV1AutomaticAssignmentRunsOnceAtAccountCreation(t *testing.T) {
	stateMu.Lock()
	inst := lockedInstanceByID(instRadarr)
	before := inst.AutoAddUsers
	inst.AutoAddUsers = false
	stateMu.Unlock()
	defer func() {
		stateMu.Lock()
		inst.AutoAddUsers = before
		stateMu.Unlock()
	}()
	u := createInvitedUser("v1-auto-assignment-test")
	defer func() { _ = deleteUser(u.ID) }()
	if userAssignedInstance(u, instRadarr) || !userAssignedInstance(u, instSonarr) {
		t.Fatal("new account ignored per-instance automatic assignment settings")
	}
	stateMu.Lock()
	inst.AutoAddUsers = true
	stateMu.Unlock()
	if userAssignedInstance(u, instRadarr) {
		t.Fatal("existing account was assigned again after an instance setting changed")
	}
}

func TestV1DiscordInvalidSaveIsAtomic(t *testing.T) {
	admsMu.Lock()
	before := admsDiscord
	admsMu.Unlock()
	defer func() {
		admsMu.Lock()
		admsDiscord = before
		admsMu.Unlock()
	}()

	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	path := "/api/admin/discord-notifications"
	res := demoTestRequest(t, router, http.MethodDelete, path, admin, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("clear Discord settings: %d %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodPut, path, admin,
		[]byte(`{"enabled":true,"webhook_url":"https://discord.com/api/webhooks/123/token","role_id":"bad"}`))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid Discord save = %d: %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodGet, path, admin, nil)
	var settings struct {
		Enabled    bool   `json:"enabled"`
		HasWebhook bool   `json:"has_webhook"`
		RoleID     string `json:"role_id"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &settings) != nil ||
		settings.Enabled || settings.HasWebhook || settings.RoleID != "" {
		t.Fatalf("invalid save changed Discord settings: %d %s", res.Code, res.Body.String())
	}
}

func TestV1TagRetryRechecksEligibility(t *testing.T) {
	stateMu.Lock()
	inst := lockedInstanceByID(instSonarr)
	before := inst.TagRequests
	inst.TagRequests = false
	stateMu.Unlock()
	defer func() {
		stateMu.Lock()
		inst.TagRequests = before
		stateMu.Unlock()
	}()

	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	res := demoTestRequest(t, router, http.MethodPost, "/api/admin/requests/3/tags/retry", admin, nil)
	if res.Code != http.StatusConflict {
		t.Fatalf("ineligible tag retry = %d: %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodGet, "/api/admin/requests/history?q=Sherlock", admin, nil)
	var history struct {
		Requests []struct {
			ID  int64 `json:"id"`
			Tag struct {
				Status   string `json:"status"`
				CanRetry bool   `json:"can_retry"`
			} `json:"requester_tagging"`
		} `json:"requests"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &history) != nil || len(history.Requests) == 0 ||
		history.Requests[0].ID != 3 || history.Requests[0].Tag.Status != "failed" || history.Requests[0].Tag.CanRetry {
		t.Fatalf("failed tag was changed by rejected retry: %d %s", res.Code, res.Body.String())
	}
}

// A child test process keeps the simulated download and its timer out of the
// rest of the suite while exercising the actual request and status handlers.
func TestV1MissingBookFormatFlow(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestV1MissingBookFormatChild$")
	command.Env = append(os.Environ(), "CANTINARR_DEMO_BOOK_CHILD=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("book request flow: %v\n%s", err, output)
	}
}

func TestV1MissingBookFormatChild(t *testing.T) {
	if os.Getenv("CANTINARR_DEMO_BOOK_CHILD") != "1" {
		t.Skip("run by TestV1MissingBookFormatFlow")
	}
	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	res := demoTestRequest(t, router, http.MethodPost, "/api/requests", admin,
		[]byte(`{"media_type":"book","foreign_id":"18490","book_format":"audiobook","title":"Frankenstein","instance_id":"`+instChaptarr+`"}`))
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"audiobook":"requested"`)) {
		t.Fatalf("request missing audiobook format: %d %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodGet,
		"/api/requests/book-status?foreign_id=18490&instance_id="+instChaptarr, admin, nil)
	var status struct {
		BookFormats map[string]string `json:"book_formats"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &status) != nil ||
		status.BookFormats[bookFormatEbook] != statusAvailable ||
		status.BookFormats[bookFormatAudiobook] == statusUnavailable {
		t.Fatalf("missing-format status lost existing ebook: %d %s", res.Code, res.Body.String())
	}
	res = demoTestRequest(t, router, http.MethodGet,
		"/api/requests/delivery-status?media_type=book&foreign_id=18490&instance_id="+instChaptarr, admin, nil)
	var delivery struct {
		RequestID int64 `json:"request_id"`
		Delivery  []struct {
			RequestID int64  `json:"request_id"`
			Format    string `json:"format"`
			State     string `json:"state"`
		} `json:"delivery"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &delivery) != nil ||
		delivery.RequestID <= 0 || len(delivery.Delivery) != 1 ||
		delivery.Delivery[0].RequestID != delivery.RequestID ||
		delivery.Delivery[0].Format != bookFormatAudiobook ||
		delivery.Delivery[0].State != "queued" {
		t.Fatalf("new audiobook request lacks saved delivery receipt: %d %s", res.Code, res.Body.String())
	}
}

func TestV1WaitingDeliveryReceipt(t *testing.T) {
	router := buildRouter()
	admin := demoTestLogin(t, router, "admin")
	res := demoTestRequest(t, router, http.MethodGet, "/api/admin/requests/waiting", admin, nil)
	res = demoTestRequest(t, router, http.MethodGet, "/api/admin/requests/waiting", admin, nil)
	var waiting []struct {
		ID       int64 `json:"id"`
		Delivery []struct {
			State string `json:"state"`
		} `json:"delivery"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &waiting) != nil {
		t.Fatalf("saved delivery list: %d %s", res.Code, res.Body.String())
	}
	found := false
	for _, row := range waiting {
		if row.ID == 6 {
			found = len(row.Delivery) == 1 && row.Delivery[0].State == "working"
		}
	}
	if !found {
		t.Fatalf("waiting book lacks saved delivery state: %s", res.Body.String())
	}
}
