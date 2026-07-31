package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/instance"
)

func testExternalSettingChange(t *testing.T, resourceID string) newSettingChange {
	t.Helper()
	beforeRaw := json.RawMessage(`{"name":"HD","score":0,"server_only":"history-secret"}`)
	afterRaw := json.RawMessage(`{"name":"HD","score":25}`)
	beforeHash, err := canonicalJSONHash(beforeRaw)
	if err != nil {
		t.Fatalf("hash before snapshot: %v", err)
	}
	afterHash, err := canonicalJSONHash(afterRaw)
	if err != nil {
		t.Fatalf("hash after snapshot: %v", err)
	}
	return newSettingChange{
		ActorUserID:     42,
		ActorDeviceID:   "device-42",
		Source:          "ai_chat",
		ServiceType:     "radarr",
		InstanceID:      "instance-1",
		InstanceName:    "Main Movies",
		ResourceType:    "quality_profile",
		ResourceID:      resourceID,
		ResourceName:    "HD",
		Operation:       "update",
		Summary:         settingChangeSummary("quality_profile", "update", "HD"),
		Changes:         []SettingFieldChange{{Key: "custom_format_score:4", Label: "Custom format: x265", Before: "0", After: "+25"}},
		BeforeRaw:       beforeRaw,
		AfterRaw:        afterRaw,
		BeforeHash:      beforeHash,
		AfterHash:       afterHash,
		DependencyHash:  sha256.Sum256([]byte("dependencies")),
		InstanceBinding: instance.ArrSettingsFingerprint(sha256.Sum256([]byte("instance-binding"))),
	}
}

func applyProfileChangeForHistoryTest(t *testing.T, server *ToolServer, reference, turnID, trustedText string) ExternalSettingChange {
	t.Helper()
	result, err := server.ExecuteTool(
		context.Background(),
		"apply_profile_change",
		json.RawMessage(`{"change_reference":"`+reference+`"}`),
		profileToolCallContext(turnID, trustedText),
	)
	if err != nil {
		t.Fatalf("apply profile change: %v", err)
	}
	if !strings.Contains(result.Text, "Applied the requested change") {
		t.Fatalf("apply result = %q", result.Text)
	}
	changes, err := server.settingsChanges.list(1, 0)
	if err != nil || len(changes) != 1 {
		t.Fatalf("applied history = %#v, %v", changes, err)
	}
	return changes[0]
}

func restoreSettingChangeThroughAPI(t *testing.T, server *ToolServer, id int64) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewSettingsChangeHandler(server)
	router := chi.NewRouter()
	router.Post("/external-settings-changes/{id}/revert", handler.Revert)
	request := httptest.NewRequest(http.MethodPost, "/external-settings-changes/"+strconv.FormatInt(id, 10)+"/revert", nil)
	request = request.WithContext(context.WithValue(request.Context(), auth.ClaimsKey, &auth.Claims{
		UserID: 77, Role: auth.RoleAdmin, DeviceID: "device-77",
	}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestSettingChangeSummaryNeverClaimsAnUnknownRemoteOutcome(t *testing.T) {
	tests := []struct {
		resourceType string
		operation    string
		want         string
	}{
		{resourceType: "quality_profile", operation: "update", want: `Quality profile update: "Example"`},
		{resourceType: "quality_profile", operation: "revert", want: `Quality profile restore: "Example"`},
		{resourceType: "custom_format", operation: "create", want: `Custom format creation: "Example"`},
		{resourceType: "custom_format", operation: "update", want: `Custom format update: "Example"`},
	}
	for _, test := range tests {
		for _, status := range []string{settingChangeStatusExecuting, settingChangeStatusFailed, settingChangeStatusOutcomeUnknown} {
			if got := settingChangeSummary(test.resourceType, test.operation, "Example"); got != test.want {
				t.Errorf("%s %s %s summary = %q, want %q", status, test.resourceType, test.operation, got, test.want)
			}
		}
	}
}

func TestSettingChangeValidationRequiresRestoreParentLink(t *testing.T) {
	positiveParent := int64(1)
	zeroParent := int64(0)
	tests := []struct {
		name      string
		operation string
		parentID  *int64
		wantError bool
	}{
		{name: "root update", operation: "update"},
		{name: "linked restore", operation: "revert", parentID: &positiveParent},
		{name: "unlinked restore", operation: "revert", wantError: true},
		{name: "invalid restore parent", operation: "revert", parentID: &zeroParent, wantError: true},
		{name: "linked non-restore", operation: "update", parentID: &positiveParent, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			change := testExternalSettingChange(t, "1")
			change.Operation = test.operation
			change.ParentID = test.parentID
			err := validateNewSettingChange(change)
			if (err != nil) != test.wantError {
				t.Fatalf("validateNewSettingChange() error = %v, wantError=%t", err, test.wantError)
			}
		})
	}
}

func TestSettingChangeStorePersistsFinalizedHistoryAndServesBoundedList(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cantinarr.db")
	database, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	store := newSettingChangeStore(database)

	first, err := store.create(testExternalSettingChange(t, "1"))
	if err != nil {
		t.Fatalf("create first change: %v", err)
	}
	if first.ID <= 0 || first.Status != settingChangeStatusExecuting || first.CompletedAt != nil {
		t.Fatalf("new change = %#v", first.ExternalSettingChange)
	}
	verifiedAfter := json.RawMessage(`{"score":25,"name":"HD"}`)
	verifiedHash, err := canonicalJSONHash(verifiedAfter)
	if err != nil {
		t.Fatalf("hash verified result: %v", err)
	}
	verifiedFields := []SettingFieldChange{{Key: "custom_format_score:4", Label: "Custom format: x265", Before: "0", After: "+25"}}
	first, err = store.finishAppliedVerified(first.ID, "1", "HD verified", verifiedFields, verifiedAfter, verifiedHash)
	if err != nil {
		t.Fatalf("finish first change: %v", err)
	}
	if first.Status != settingChangeStatusApplied || first.CompletedAt == nil || first.ResourceName != "HD verified" || string(first.AfterRaw) != string(verifiedAfter) {
		t.Fatalf("finalized first change = %#v raw=%s", first.ExternalSettingChange, first.AfterRaw)
	}

	second, err := store.create(testExternalSettingChange(t, "2"))
	if err != nil {
		t.Fatalf("create second change: %v", err)
	}
	second, err = store.finish(second.ID, settingChangeStatusFailed, "upstream rejected the update")
	if err != nil {
		t.Fatalf("finish second change: %v", err)
	}
	if second.Status != settingChangeStatusFailed || second.CompletedAt == nil || second.ErrorText == "" ||
		second.Summary != `Quality profile update: "HD"` {
		t.Fatalf("finalized second change = %#v", second.ExternalSettingChange)
	}

	page, err := store.list(1, 0)
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(page) != 1 || page[0].ID != second.ID || len(page[0].Changes) != 0 {
		t.Fatalf("first page = %#v", page)
	}
	page, err = store.list(10, second.ID)
	if err != nil {
		t.Fatalf("list next page: %v", err)
	}
	if len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("next page = %#v", page)
	}
	if _, err := database.Exec(`UPDATE external_setting_changes SET changes_json = 'not-json' WHERE id = ?`, second.ID); err != nil {
		t.Fatalf("corrupt detail-only projection: %v", err)
	}
	page, err = store.list(1, 0)
	if err != nil || len(page) != 1 || page[0].ID != second.ID || len(page[0].Changes) != 0 {
		t.Fatalf("metadata-only list after corrupt projection = %#v, %v", page, err)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	reopened, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedStore := newSettingChangeStore(reopened)
	persisted, err := reopenedStore.get(first.ID)
	if err != nil {
		t.Fatalf("get persisted detail: %v", err)
	}
	if persisted.Status != settingChangeStatusApplied || persisted.ActorDeviceID != "device-42" || persisted.AfterHash != hashString(verifiedHash) || string(persisted.AfterRaw) != string(verifiedAfter) || len(persisted.Changes) != 1 {
		t.Fatalf("persisted detail = %#v raw=%s", persisted, persisted.AfterRaw)
	}

	server := NewToolServer(nil, nil, nil, nil)
	server.SetSettingsChangeDatabase(reopened)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/external-settings-changes?limit=1", nil)
	NewSettingsChangeHandler(server).List(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Changes []ExternalSettingChange `json:"changes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Changes) != 1 || response.Changes[0].ID != second.ID {
		t.Fatalf("list response = %#v", response.Changes)
	}
	if strings.Contains(recorder.Body.String(), "history-secret") || strings.Contains(recorder.Body.String(), "before_json") || strings.Contains(recorder.Body.String(), "after_json") {
		t.Fatalf("list leaked server-only snapshots: %s", recorder.Body.String())
	}
}

func TestSettingChangeStoreRecordsOnlyOneSuccessfulRestorePerSource(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "cantinarr.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := newSettingChangeStore(database)

	source, err := store.create(testExternalSettingChange(t, "1"))
	if err != nil {
		t.Fatalf("create source change: %v", err)
	}
	source, err = store.finish(source.ID, settingChangeStatusApplied, "")
	if err != nil {
		t.Fatalf("finish source change: %v", err)
	}
	restore := testExternalSettingChange(t, "1")
	restore.ParentID = &source.ID
	restore.Source = "admin_revert"
	restore.Operation = "revert"
	restore.Summary = settingChangeSummary("quality_profile", "revert", restore.ResourceName)

	type createResult struct {
		change storedSettingChange
		err    error
	}
	start := make(chan struct{})
	results := make(chan createResult, 2)
	for range 2 {
		go func() {
			<-start
			change, err := store.create(restore)
			results <- createResult{change: change, err: err}
		}()
	}
	close(start)
	var first storedSettingChange
	succeeded := 0
	blocked := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			succeeded++
			first = result.change
		case errors.Is(result.err, errSettingChangeAlreadyRestored):
			blocked++
		default:
			t.Fatalf("concurrent restore reservation error = %v", result.err)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatalf("concurrent restore reservations succeeded=%d blocked=%d", succeeded, blocked)
	}
	if _, err := store.finish(first.ID, settingChangeStatusApplied, ""); err != nil {
		t.Fatalf("finish first restore: %v", err)
	}
	if blocked, err := store.hasBlockingRevert(source.ID); err != nil || !blocked {
		t.Fatalf("blocking restore = %t, %v", blocked, err)
	}
	changes, err := store.list(10, 0)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(changes) != 2 || changes[0].ID != first.ID || changes[1].ID != source.ID {
		t.Fatalf("append-only history = %#v", changes)
	}
}

func TestSettingChangeStoreBlocksUncertainRestoreButPermitsFailedRetry(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "cantinarr.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := newSettingChangeStore(database)

	source, err := store.create(testExternalSettingChange(t, "1"))
	if err != nil {
		t.Fatalf("create source change: %v", err)
	}
	source, err = store.finish(source.ID, settingChangeStatusApplied, "")
	if err != nil {
		t.Fatalf("finish source change: %v", err)
	}
	restore := testExternalSettingChange(t, "1")
	restore.ParentID = &source.ID
	restore.Source = "admin_revert"
	restore.Operation = "revert"
	restore.Summary = settingChangeSummary("quality_profile", "revert", restore.ResourceName)

	failed, err := store.create(restore)
	if err != nil {
		t.Fatalf("create failed restore attempt: %v", err)
	}
	if _, err := store.create(restore); !errors.Is(err, errSettingChangeAlreadyRestored) {
		t.Fatalf("restore while first attempt executes error = %v", err)
	}
	if _, err := store.finish(failed.ID, settingChangeStatusFailed, "write was rejected"); err != nil {
		t.Fatalf("finish failed restore attempt: %v", err)
	}
	if blocked, err := store.hasBlockingRevert(source.ID); err != nil || blocked {
		t.Fatalf("failed restore blocked=%t, err=%v", blocked, err)
	}

	unknown, err := store.create(restore)
	if err != nil {
		t.Fatalf("retry after definite failure: %v", err)
	}
	if _, err := store.finish(unknown.ID, settingChangeStatusOutcomeUnknown, "connection ended during confirmation"); err != nil {
		t.Fatalf("finish uncertain restore attempt: %v", err)
	}
	if blocked, err := store.hasBlockingRevert(source.ID); err != nil || !blocked {
		t.Fatalf("uncertain restore blocked=%t, err=%v", blocked, err)
	}
	if _, err := store.create(restore); !errors.Is(err, errSettingChangeAlreadyRestored) {
		t.Fatalf("restore after uncertain outcome error = %v", err)
	}
	changes, err := store.list(10, 0)
	if err != nil {
		t.Fatalf("list append-only attempts: %v", err)
	}
	if len(changes) != 3 || changes[0].ID != unknown.ID || changes[1].ID != failed.ID || changes[2].ID != source.ID {
		t.Fatalf("append-only restore attempts = %#v", changes)
	}
}

func TestSettingChangeStoreRepairsInterruptedExecutionOnStartup(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "cantinarr.db")
	database, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	created, err := newSettingChangeStore(database).create(testExternalSettingChange(t, "1"))
	if err != nil {
		t.Fatalf("create executing change: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := db.Open(databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repaired, err := newSettingChangeStore(reopened).get(created.ID)
	if err != nil {
		t.Fatalf("get repaired change: %v", err)
	}
	if repaired.Status != settingChangeStatusOutcomeUnknown || repaired.CompletedAt == nil || !strings.Contains(repaired.ErrorText, "restarted") ||
		repaired.Summary != `Quality profile update: "HD"` {
		t.Fatalf("repaired change = %#v", repaired.ExternalSettingChange)
	}
	if _, err := newSettingChangeStore(reopened).finish(created.ID, settingChangeStatusApplied, ""); err == nil {
		t.Fatal("startup-repaired change was allowed to become applied")
	}
}

func TestSettingChangeProfileRestoreRefusesDriftThenRestoresExactAppliedImage(t *testing.T) {
	fake := newProfileToolFakeArr()
	server, _, _, inst := newProfileToolIntegrationServerWithStoreForService(t, fake, "radarr")
	reader, resolvedID, _, binding, refusal := server.freshSettingsTargetFor("radarr", inst.ID)
	if refusal != "" || resolvedID != inst.ID {
		t.Fatalf("fresh target id=%q refusal=%q", resolvedID, refusal)
	}
	mutator, ok := reader.(qualityProfileMutator)
	if !ok {
		t.Fatal("fresh Radarr client does not implement qualityProfileMutator")
	}
	before, err := loadProfileSettingsSnapshot(context.Background(), mutator, 1, true)
	if err != nil {
		t.Fatalf("load before snapshot: %v", err)
	}
	afterRaw := json.RawMessage(strings.Replace(settingsProfileHD, `"format":4,"name":"x265","score":0`, `"format":4,"name":"x265","score":25`, 1))
	fake.setProfile(string(afterRaw))
	after, err := loadProfileSettingsSnapshot(context.Background(), mutator, 1, true)
	if err != nil {
		t.Fatalf("load after snapshot: %v", err)
	}
	created, err := server.settingsChanges.create(newSettingChange{
		ActorUserID: 77, ActorDeviceID: "device-77", Source: "ai_chat",
		ServiceType: "radarr", InstanceID: inst.ID, InstanceName: inst.Name,
		ResourceType: "quality_profile", ResourceID: "1", ResourceName: after.ProfileName,
		Operation: "update", Summary: "Set x265 to 25",
		Changes:   []SettingFieldChange{{Key: "custom_format_score:4", Label: "Custom format: x265", Before: "0", After: "+25"}},
		BeforeRaw: before.ProfileRaw, AfterRaw: after.ProfileRaw,
		BeforeHash: before.ProfileHash, AfterHash: after.ProfileHash,
		DependencyHash: profileDependencyHash(after), InstanceBinding: binding,
	})
	if err != nil {
		t.Fatalf("create applied history: %v", err)
	}
	created, err = server.settingsChanges.finish(created.ID, settingChangeStatusApplied, "")
	if err != nil {
		t.Fatalf("finalize applied history: %v", err)
	}

	driftRaw := strings.Replace(settingsProfileHD, `"format":4,"name":"x265","score":0`, `"format":4,"name":"x265","score":50`, 1)
	fake.setProfile(driftRaw)
	detail, err := server.settingChangeDetail(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("detail while drifted: %v", err)
	}
	if detail.CurrentStatus != "different" || detail.CanRevert || len(detail.Changes) != 1 || detail.Changes[0].Current == nil || *detail.Changes[0].Current != "+50" || detail.Changes[0].CurrentState != "different" {
		t.Fatalf("drifted detail = %#v", detail)
	}
	if _, err := server.revertSettingChange(context.Background(), created.ID, profileToolCallContext("revert-drifted", "Restore the previous profile")); !errors.Is(err, errSettingChangeConflict) {
		t.Fatalf("drifted restore error = %v", err)
	}
	if fake.putCount() != 0 {
		t.Fatalf("drifted restore wrote %d times", fake.putCount())
	}

	fake.setProfile(string(afterRaw))
	detail, err = server.settingChangeDetail(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("detail at exact applied image: %v", err)
	}
	if detail.CurrentStatus != "matches_applied" || !detail.CanRevert || detail.Changes[0].Current == nil || *detail.Changes[0].Current != "+25" || detail.Changes[0].CurrentState != "matches_applied" {
		t.Fatalf("matching detail = %#v", detail)
	}

	handler := NewSettingsChangeHandler(server)
	router := chi.NewRouter()
	router.Get("/external-settings-changes/{id}", handler.Get)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/external-settings-changes/"+strconv.FormatInt(created.ID, 10), nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var apiDetail ExternalSettingChange
	if err := json.Unmarshal(recorder.Body.Bytes(), &apiDetail); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if apiDetail.ID != created.ID || apiDetail.CurrentStatus != "matches_applied" || !apiDetail.CanRevert {
		t.Fatalf("API detail = %#v", apiDetail)
	}
	if strings.Contains(recorder.Body.String(), "futureField") || strings.Contains(recorder.Body.String(), "before_json") || strings.Contains(recorder.Body.String(), "after_json") {
		t.Fatalf("detail leaked server-only snapshots: %s", recorder.Body.String())
	}

	reverted, err := server.revertSettingChange(context.Background(), created.ID, profileToolCallContext("revert-exact", "Restore the previous profile"))
	if err != nil {
		t.Fatalf("restore exact applied image: %v", err)
	}
	if fake.putCount() != 1 || reverted.ParentID == nil || *reverted.ParentID != created.ID || reverted.Operation != "revert" || reverted.Status != settingChangeStatusApplied || reverted.CurrentStatus != "matches_applied" || reverted.CanRevert {
		t.Fatalf("reverted change = %#v puts=%d", reverted, fake.putCount())
	}
	fake.mu.Lock()
	restoredRaw := append(json.RawMessage(nil), fake.profile...)
	fake.mu.Unlock()
	restoredHash, err := canonicalProfileJSONHash(restoredRaw)
	if err != nil {
		t.Fatalf("hash restored profile: %v", err)
	}
	if restoredHash != before.ProfileHash {
		t.Fatalf("restored profile differs from before snapshot:\nwant %s\n got %s", before.ProfileRaw, restoredRaw)
	}
	revertedDetail, err := server.settingChangeDetail(context.Background(), reverted.ID)
	if err != nil {
		t.Fatalf("detail for restore record: %v", err)
	}
	if revertedDetail.CurrentStatus != "matches_applied" || revertedDetail.CanRevert || revertedDetail.CurrentError != "" {
		t.Fatalf("restore detail = %#v", revertedDetail)
	}
	if _, err := server.revertSettingChange(context.Background(), reverted.ID, profileToolCallContext("revert-restore", "Restore the restore record")); !errors.Is(err, errSettingChangeNotRestorable) {
		t.Fatalf("restore-record restore error = %v", err)
	}
	response := restoreSettingChangeThroughAPI(t, server, reverted.ID)
	if response.Code != http.StatusConflict ||
		response.Body.String() != "{\"error\":\"This history entry cannot be restored.\"}\n" {
		t.Fatalf("restore-record API status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.putCount() != 1 {
		t.Fatalf("restore-record restore wrote %d extra times", fake.putCount()-1)
	}

	// Even if an administrator later puts the live profile back at this source
	// record's applied image, its successful restore remains one-shot history.
	fake.setProfile(string(afterRaw))
	detail, err = server.settingChangeDetail(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("detail for already-restored source: %v", err)
	}
	if detail.CurrentStatus != "matches_applied" || detail.CanRevert ||
		detail.CurrentError != "This change already has a restore record, so it cannot be restored again." {
		t.Fatalf("already-restored source detail = %#v", detail)
	}
	if _, err := server.revertSettingChange(context.Background(), created.ID, profileToolCallContext("revert-again", "Restore the previous profile again")); !errors.Is(err, errSettingChangeAlreadyRestored) {
		t.Fatalf("second source restore error = %v", err)
	}
	response = restoreSettingChangeThroughAPI(t, server, created.ID)
	if response.Code != http.StatusConflict ||
		response.Body.String() != "{\"error\":\"Previous settings were already restored or a restore requires review.\"}\n" {
		t.Fatalf("already-restored API status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.putCount() != 1 {
		t.Fatalf("second source restore wrote %d extra times", fake.putCount()-1)
	}
}

func TestSettingChangeConcurrentRestoreCreatesOneChildAndOneWrite(t *testing.T) {
	fake := newProfileToolFakeArr()
	firstServer, _, _, _ := newProfileToolIntegrationServerWithStoreForService(t, fake, "radarr")
	reference := previewX265ProfileChange(t, firstServer, "concurrent-restore-apply")
	applied := applyProfileChangeForHistoryTest(t, firstServer, reference, "concurrent-restore-apply", "Set x265 to 25")
	if fake.putCount() != 1 {
		t.Fatalf("apply wrote %d times, want one", fake.putCount())
	}

	// A second ToolServer intentionally has a distinct in-memory mutation lock.
	// The conditional history insert must still reserve this parent atomically.
	secondServer := NewToolServer(nil, nil, firstServer.registry, nil)
	secondServer.SetSettingsChangeDatabase(firstServer.settingsChanges.db)
	secondServer.SetCallAuthorizer(func(context.Context, CallContext) (string, error) {
		return auth.RoleAdmin, nil
	})
	type restoreResult struct {
		change ExternalSettingChange
		err    error
	}
	start := make(chan struct{})
	results := make(chan restoreResult, 2)
	servers := []*ToolServer{firstServer, secondServer}
	for i, server := range servers {
		go func(index int, candidate *ToolServer) {
			<-start
			change, err := candidate.revertSettingChange(
				context.Background(), applied.ID,
				profileToolCallContext(fmt.Sprintf("concurrent-restore-%d", index), "Restore the previous profile"),
			)
			results <- restoreResult{change: change, err: err}
		}(i, server)
	}
	close(start)

	succeeded := 0
	blocked := 0
	var restored ExternalSettingChange
	for range servers {
		result := <-results
		switch {
		case result.err == nil:
			succeeded++
			restored = result.change
		case errors.Is(result.err, errSettingChangeAlreadyRestored), errors.Is(result.err, errSettingChangeConflict):
			blocked++
		default:
			t.Fatalf("concurrent restore error = %v", result.err)
		}
	}
	if succeeded != 1 || blocked != 1 || fake.putCount() != 2 {
		t.Fatalf("concurrent restores succeeded=%d blocked=%d total puts=%d", succeeded, blocked, fake.putCount())
	}
	if restored.ParentID == nil || *restored.ParentID != applied.ID || restored.CanRevert {
		t.Fatalf("successful concurrent restore = %#v", restored)
	}
	changes, err := firstServer.settingsChanges.list(10, 0)
	if err != nil {
		t.Fatalf("list concurrent restore history: %v", err)
	}
	if len(changes) != 2 || changes[0].Operation != "revert" || changes[0].ParentID == nil ||
		*changes[0].ParentID != applied.ID || changes[1].ID != applied.ID {
		t.Fatalf("concurrent restore history = %#v", changes)
	}
}

func TestSettingChangeSonarrDependencyScopeControlsRestore(t *testing.T) {
	t.Run("language score restores while the catalog matches", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _, _, _ := newProfileToolIntegrationServerWithStoreForService(t, fake, "sonarr")
		reference := previewLanguageScoreProfileChange(t, server, "sonarr", "sonarr-language-match")
		applied := applyProfileChangeForHistoryTest(t, server, reference, "sonarr-language-match", "Set Not English to -9000")
		if fake.putCount() != 1 {
			t.Fatalf("apply wrote %d times, want one", fake.putCount())
		}

		detail, err := server.settingChangeDetail(context.Background(), applied.ID)
		if err != nil {
			t.Fatalf("detail at exact applied language state: %v", err)
		}
		if detail.CurrentStatus != "matches_applied" || !detail.CanRevert || detail.CurrentError != "" ||
			len(detail.Changes) != 1 || detail.Changes[0].Key != "custom_format_score:3" ||
			detail.Changes[0].Current == nil || *detail.Changes[0].Current != "-9000" ||
			detail.Changes[0].CurrentState != "matches_applied" {
			t.Fatalf("matching Sonarr language detail = %#v", detail)
		}

		reverted, err := server.revertSettingChange(context.Background(), applied.ID, profileToolCallContext("restore-sonarr-language", "Restore the previous profile"))
		if err != nil {
			t.Fatalf("restore matching Sonarr language profile: %v", err)
		}
		if fake.putCount() != 2 || reverted.ParentID == nil || *reverted.ParentID != applied.ID ||
			reverted.Operation != "revert" || reverted.Status != settingChangeStatusApplied ||
			reverted.CurrentStatus != "matches_applied" || reverted.CanRevert {
			t.Fatalf("Sonarr language restore = %#v puts=%d", reverted, fake.putCount())
		}
		fake.mu.Lock()
		restoredRaw := append(json.RawMessage(nil), fake.profile...)
		fake.mu.Unlock()
		restoredHash, err := canonicalProfileJSONHash(restoredRaw)
		if err != nil {
			t.Fatalf("hash restored Sonarr profile: %v", err)
		}
		beforeHash, err := canonicalProfileJSONHash(json.RawMessage(settingsProfileHD))
		if err != nil {
			t.Fatalf("hash original Sonarr profile: %v", err)
		}
		if restoredHash != beforeHash {
			t.Fatalf("restored Sonarr profile differs from original:\nwant %s\n got %s", settingsProfileHD, restoredRaw)
		}
		revertedDetail, err := server.settingChangeDetail(context.Background(), reverted.ID)
		if err != nil {
			t.Fatalf("detail for Sonarr restore record: %v", err)
		}
		if revertedDetail.CurrentStatus != "matches_applied" || revertedDetail.CanRevert {
			t.Fatalf("Sonarr restore detail = %#v", revertedDetail)
		}
	})

	t.Run("language score refuses real catalog drift", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _, _, _ := newProfileToolIntegrationServerWithStoreForService(t, fake, "sonarr")
		reference := previewLanguageScoreProfileChange(t, server, "sonarr", "sonarr-language-drift")
		applied := applyProfileChangeForHistoryTest(t, server, reference, "sonarr-language-drift", "Set Not English to -9000")
		fake.setLanguages(`[{"id":-1,"name":"Any"},{"id":1,"name":"English (changed)"}]`)

		detail, err := server.settingChangeDetail(context.Background(), applied.ID)
		if err != nil {
			t.Fatalf("detail after Sonarr language catalog drift: %v", err)
		}
		if detail.CurrentStatus != "different" || detail.CanRevert ||
			detail.CurrentError != "Other settings or dependencies on this profile changed after this entry." ||
			len(detail.Changes) != 1 || detail.Changes[0].Current == nil ||
			*detail.Changes[0].Current != "-9000" || detail.Changes[0].CurrentState != "matches_applied" {
			t.Fatalf("drifted Sonarr language detail = %#v", detail)
		}
		if _, err := server.revertSettingChange(context.Background(), applied.ID, profileToolCallContext("restore-sonarr-language-drift", "Restore the previous profile")); !errors.Is(err, errSettingChangeConflict) {
			t.Fatalf("drifted Sonarr language restore error = %v", err)
		}
		if fake.putCount() != 1 {
			t.Fatalf("drifted Sonarr language restore wrote %d times", fake.putCount()-1)
		}
	})

	t.Run("non-language score ignores unrelated catalog drift", func(t *testing.T) {
		fake := newProfileToolFakeArr()
		server, _, _, _ := newProfileToolIntegrationServerWithStoreForService(t, fake, "sonarr")
		reference := previewX265ProfileChangeForService(t, server, "sonarr", "sonarr-non-language")
		applied := applyProfileChangeForHistoryTest(t, server, reference, "sonarr-non-language", "Set x265 to 25")
		fake.setLanguages(`[{"id":-1,"name":"Any"},{"id":1,"name":"English (changed)"}]`)

		detail, err := server.settingChangeDetail(context.Background(), applied.ID)
		if err != nil {
			t.Fatalf("detail after unrelated Sonarr language catalog drift: %v", err)
		}
		if detail.CurrentStatus != "matches_applied" || !detail.CanRevert || detail.CurrentError != "" ||
			len(detail.Changes) != 1 || detail.Changes[0].Current == nil ||
			*detail.Changes[0].Current != "+25" || detail.Changes[0].CurrentState != "matches_applied" {
			t.Fatalf("matching Sonarr non-language detail = %#v", detail)
		}
		if _, err := server.revertSettingChange(context.Background(), applied.ID, profileToolCallContext("restore-sonarr-non-language", "Restore the previous profile")); err != nil {
			t.Fatalf("restore Sonarr non-language profile: %v", err)
		}
		if fake.putCount() != 2 {
			t.Fatalf("Sonarr non-language apply and restore wrote %d times, want two", fake.putCount())
		}
	})
}

func TestSettingChangeCustomFormatDetailComparesLiveStateWithoutOfferingRestore(t *testing.T) {
	var (
		mu      sync.RWMutex
		current = `[{"id":7,"name":"Not English","specifications":[{"name":"Language","implementation":"LanguageSpecification","fields":[{"name":"value","value":1}]}]}]`
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/customformat" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		mu.RLock()
		defer mu.RUnlock()
		_, _ = w.Write([]byte(current))
	}))
	t.Cleanup(upstream.Close)

	inst := &instance.Instance{ServiceType: "radarr", Name: "Main Movies", URL: upstream.URL, APIKey: "key", IsDefault: true}
	server := newSettingsToolServer(t, []*instance.Instance{inst})
	_, resolvedID, _, binding, refusal := server.freshSettingsTargetFor("radarr", inst.ID)
	if refusal != "" || resolvedID != inst.ID {
		t.Fatalf("fresh target id=%q refusal=%q", resolvedID, refusal)
	}
	afterRaw := json.RawMessage(`{"id":7,"name":"Not English","specifications":[{"name":"Language","implementation":"LanguageSpecification","fields":[{"name":"value","value":1}]}]}`)
	beforeRaw := json.RawMessage(`null`)
	beforeHash, _ := canonicalJSONHash(beforeRaw)
	afterHash, _ := canonicalJSONHash(afterRaw)
	fields, err := customFormatSettingFieldChanges(customFormatUpsertPlan{Action: "created", BeforeRaw: beforeRaw, AfterRaw: afterRaw})
	if err != nil {
		t.Fatalf("project created custom format: %v", err)
	}
	created, err := server.settingsChanges.create(newSettingChange{
		ActorUserID: 77, ActorDeviceID: "device-77", Source: "ai_chat",
		ServiceType: "radarr", InstanceID: inst.ID, InstanceName: inst.Name,
		ResourceType: "custom_format", ResourceID: "name:Not English", ResourceName: "Not English",
		Operation: "create", Summary: "Created custom format Not English", Changes: fields,
		BeforeRaw: beforeRaw, AfterRaw: afterRaw, BeforeHash: beforeHash, AfterHash: afterHash,
		InstanceBinding: binding,
	})
	if err != nil {
		t.Fatalf("create custom-format history: %v", err)
	}
	created, err = server.settingsChanges.finishAppliedVerified(created.ID, "7", "Not English", fields, afterRaw, afterHash)
	if err != nil {
		t.Fatalf("finish custom-format history: %v", err)
	}

	detail, err := server.settingChangeDetail(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("matching custom-format detail: %v", err)
	}
	if detail.CurrentStatus != "matches_applied" || detail.CanRevert {
		t.Fatalf("matching custom-format detail = %#v", detail)
	}

	plannedAfter := json.RawMessage(`{"name":"Not English","specifications":[{"name":"Language","implementation":"LanguageSpecification","fields":[{"name":"value","value":1}]}]}`)
	plannedAfterHash, _ := canonicalJSONHash(plannedAfter)
	plannedFields, err := customFormatSettingFieldChanges(customFormatUpsertPlan{Action: "created", BeforeRaw: beforeRaw, AfterRaw: plannedAfter})
	if err != nil {
		t.Fatalf("project interrupted create: %v", err)
	}
	interrupted, err := server.settingsChanges.create(newSettingChange{
		ActorUserID: 77, ActorDeviceID: "device-77", Source: "ai_chat",
		ServiceType: "radarr", InstanceID: inst.ID, InstanceName: inst.Name,
		ResourceType: "custom_format", ResourceID: "name:Not English", ResourceName: "Not English",
		Operation: "create", Summary: "Attempted custom format creation", Changes: plannedFields,
		BeforeRaw: beforeRaw, AfterRaw: plannedAfter, BeforeHash: beforeHash, AfterHash: plannedAfterHash,
		InstanceBinding: binding,
	})
	if err != nil {
		t.Fatalf("create interrupted custom-format history: %v", err)
	}
	interrupted, err = server.settingsChanges.finish(interrupted.ID, settingChangeStatusOutcomeUnknown, "connection ended during confirmation")
	if err != nil {
		t.Fatalf("finish interrupted custom-format history: %v", err)
	}
	interruptedDetail, err := server.settingChangeDetail(context.Background(), interrupted.ID)
	if err != nil {
		t.Fatalf("reconcile interrupted create: %v", err)
	}
	if interruptedDetail.CurrentStatus != "matches_applied" || interruptedDetail.CanRevert || !allCurrentFieldsMatchApplied(interruptedDetail.Changes) {
		t.Fatalf("reconciled interrupted create = %#v", interruptedDetail)
	}

	updateBefore := afterRaw
	updateAfter := json.RawMessage(`{"id":7,"name":"Not English","specifications":[{"name":"Language","implementation":"LanguageSpecification","fields":[{"name":"value","value":2}]}]}`)
	updateBeforeHash, _ := canonicalJSONHash(updateBefore)
	updateAfterHash, _ := canonicalJSONHash(updateAfter)
	updateFields, err := customFormatSettingFieldChanges(customFormatUpsertPlan{Action: "updated", BeforeRaw: updateBefore, AfterRaw: updateAfter})
	if err != nil {
		t.Fatalf("project interrupted update: %v", err)
	}
	interruptedUpdate, err := server.settingsChanges.create(newSettingChange{
		ActorUserID: 77, ActorDeviceID: "device-77", Source: "external_mcp",
		ServiceType: "radarr", InstanceID: inst.ID, InstanceName: inst.Name,
		ResourceType: "custom_format", ResourceID: "7", ResourceName: "Not English",
		Operation: "update", Summary: "Attempted custom format update", Changes: updateFields,
		BeforeRaw: updateBefore, AfterRaw: updateAfter, BeforeHash: updateBeforeHash, AfterHash: updateAfterHash,
		InstanceBinding: binding,
	})
	if err != nil {
		t.Fatalf("create interrupted custom-format update history: %v", err)
	}
	interruptedUpdate, err = server.settingsChanges.finish(interruptedUpdate.ID, settingChangeStatusOutcomeUnknown, "connection ended during confirmation")
	if err != nil {
		t.Fatalf("finish interrupted custom-format update history: %v", err)
	}
	mu.Lock()
	current = `[{"id":7,"name":"Not English","serverOwned":"normalized","specifications":[{"name":"Language","implementation":"LanguageSpecification","serverOwned":"normalized","fields":[{"name":"value","value":2,"serverOwned":"normalized"}]}]}]`
	mu.Unlock()
	interruptedUpdateDetail, err := server.settingChangeDetail(context.Background(), interruptedUpdate.ID)
	if err != nil {
		t.Fatalf("reconcile interrupted update: %v", err)
	}
	if interruptedUpdateDetail.CurrentStatus != "matches_applied" || interruptedUpdateDetail.CanRevert || !allCurrentFieldsMatchApplied(interruptedUpdateDetail.Changes) {
		t.Fatalf("reconciled interrupted update = %#v", interruptedUpdateDetail)
	}

	mu.Lock()
	current = `[{"id":7,"name":"Not English","specifications":[{"name":"Language","implementation":"LanguageSpecification","fields":[{"name":"value","value":2}]}]}]`
	mu.Unlock()
	detail, err = server.settingChangeDetail(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("changed custom-format detail: %v", err)
	}
	if detail.CurrentStatus != "different" || detail.CanRevert {
		t.Fatalf("changed custom-format detail = %#v", detail)
	}
	var matchingRules *SettingFieldChange
	for i := range detail.Changes {
		if detail.Changes[i].Key == "field:specifications" {
			matchingRules = &detail.Changes[i]
			break
		}
	}
	if matchingRules == nil || matchingRules.Current == nil || matchingRules.CurrentState != "different" {
		t.Fatalf("changed matching-rules projection = %#v", matchingRules)
	}

	mu.Lock()
	current = `[]`
	mu.Unlock()
	detail, err = server.settingChangeDetail(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("removed custom-format detail: %v", err)
	}
	if detail.CurrentStatus != "different" || detail.Changes[0].Current == nil || *detail.Changes[0].Current != "Not present" || detail.Changes[0].CurrentState != "matches_before" {
		t.Fatalf("removed custom-format detail = %#v", detail)
	}
}

func TestCustomFormatCurrentFieldStatesUseSemanticValuesNotDisplaySummaries(t *testing.T) {
	fields := []SettingFieldChange{{
		Key: "field:specifications", Label: "Matching rules",
		Before: "Old rules", After: "New rules",
	}}
	beforeRaw := json.RawMessage(`{"id":7,"name":"Format","specifications":[]}`)
	afterRaw := json.RawMessage(`{"id":7,"name":"Format","specifications":[{"name":"one","fields":[{"name":"a","value":1},{"name":"b","value":2}]},{"name":"two","fields":[]}]}`)
	reorderedRaw := json.RawMessage(`{"id":7,"name":"Format","specifications":[{"name":"two","fields":[]},{"name":"one","fields":[{"name":"b","value":2},{"name":"a","value":1}]}]}`)
	_, states, err := customFormatCurrentFieldValues(reorderedRaw, true, fields, beforeRaw, afterRaw, false)
	if err != nil {
		t.Fatalf("compare reordered semantic value: %v", err)
	}
	if states[fields[0].Key] != "matches_applied" {
		t.Fatalf("reordered semantic state = %q", states[fields[0].Key])
	}

	longAfter := strings.Repeat("a", 700)
	longCurrent := strings.Repeat("b", 700)
	afterRaw = json.RawMessage(fmt.Sprintf(`{"id":7,"name":"Format","specifications":[{"name":"rule","fields":[{"name":"value","value":%q}]}]}`, longAfter))
	currentRaw := json.RawMessage(fmt.Sprintf(`{"id":7,"name":"Format","specifications":[{"name":"rule","fields":[{"name":"value","value":%q}]}]}`, longCurrent))
	values, states, err := customFormatCurrentFieldValues(currentRaw, true, fields, beforeRaw, afterRaw, false)
	if err != nil {
		t.Fatalf("compare bounded semantic value: %v", err)
	}
	afterObject, _ := decodeJSONObject(afterRaw)
	if values[fields[0].Key] != customFormatValueDisplay("specifications", afterObject["specifications"]) {
		t.Fatalf("test values no longer share the same bounded display: current=%q after=%q", values[fields[0].Key], customFormatValueDisplay("specifications", afterObject["specifications"]))
	}
	if states[fields[0].Key] != "different" {
		t.Fatalf("same-size changed semantic state = %q", states[fields[0].Key])
	}
}
