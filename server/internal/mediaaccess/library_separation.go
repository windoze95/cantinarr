package mediaaccess

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/windoze95/cantinarr-server/internal/audiobookshelf"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

var (
	ErrSeparationState   = errors.New("Audiobookshelf library separation is not in a resumable state.")
	ErrSeparationPlan    = errors.New("Audiobookshelf library separation preview is stale; preview the libraries again.")
	ErrSeparationPolicy  = errors.New("A managed Audiobookshelf account has a custom tag policy; review it before continuing.")
	ErrSeparationLibrary = errors.New("Select every Audiobookshelf book library before separating access.")
)

type absSeparationClient interface {
	Libraries(context.Context) ([]mediaserver.Library, error)
	BookInventory(context.Context, string) ([]audiobookshelf.BookInventoryItem, error)
	SetItemTag(context.Context, string, string, bool) error
	SetItemTagAllowlist(context.Context, string, []string) error
	GetUser(context.Context, string) (mediaserver.RemoteUser, error)
	TagAccessPolicy(context.Context, string) (audiobookshelf.TagAccessPolicy, error)
}

type ABSSeparationLibraryStatus struct {
	LibraryID       string `json:"library_id"`
	State           string `json:"state"`
	InventoryCount  int    `json:"inventory_count"`
	SharedCount     int    `json:"shared_count"`
	RequestedCount  int    `json:"requested_count"`
	UnresolvedCount int    `json:"unresolved_count"`
	PendingItems    int    `json:"pending_items"`
	FailedItems     int    `json:"failed_items"`
	LastError       string `json:"last_error,omitempty"`
}

type ABSSeparationView struct {
	InstanceID        string                       `json:"instance_id"`
	State             string                       `json:"state"`
	PlanHash          string                       `json:"plan_hash,omitempty"`
	CanApply          bool                         `json:"can_apply"`
	UnmatchedRequests []absBookRequestIdentity     `json:"unmatched_requests,omitempty"`
	Libraries         []ABSSeparationLibraryStatus `json:"libraries"`
}

type absSeparationPreviewRequest struct {
	LibraryIDs []string `json:"library_ids"`
}

type absSeparationApplyRequest struct {
	PlanHash          string `json:"plan_hash"`
	ConfirmUnresolved bool   `json:"confirm_unresolved"`
}

func absProvider(s *Service, inst *instance.Instance) (absSeparationClient, error) {
	provider, err := s.providers(inst)
	if err != nil {
		return nil, err
	}
	client, ok := provider.(absSeparationClient)
	if !ok {
		return nil, ErrNotMediaServer
	}
	return client, nil
}

// PreviewABSLibrarySeparation is read-only against ABS. It persists a reviewed
// plan and item receipts so apply can be resumed without re-inferring history.
func (s *Service) PreviewABSLibrarySeparation(ctx context.Context, instanceID string, libraryIDs []string, adminID int64) (ABSSeparationView, error) {
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return ABSSeparationView{}, err
	}
	if inst.ServiceType != "audiobookshelf" || inst.MediaServerConfigInvalid {
		return ABSSeparationView{}, ErrNotMediaServer
	}
	client, err := absProvider(s, inst)
	if err != nil {
		return ABSSeparationView{}, ErrUpstream
	}
	libraries, err := client.Libraries(ctx)
	if err != nil {
		return ABSSeparationView{}, ErrUpstream
	}
	bookLibraryIDs := make([]string, 0)
	for _, lib := range libraries {
		if lib.CollectionType == "book" {
			bookLibraryIDs = append(bookLibraryIDs, lib.ID)
		}
	}
	slices.Sort(bookLibraryIDs)
	requestedLibraries := slices.Clone(libraryIDs)
	slices.Sort(requestedLibraries)
	requestedLibraries = slices.Compact(requestedLibraries)
	if len(bookLibraryIDs) == 0 || !slices.Equal(bookLibraryIDs, requestedLibraries) {
		return ABSSeparationView{}, ErrSeparationLibrary
	}
	identities, err := loadABSBookRequestIdentities(ctx, s.db, s.store)
	if err != nil {
		return ABSSeparationView{}, err
	}
	plans := make([]absBookTagPlan, 0, len(bookLibraryIDs))
	for _, libraryID := range bookLibraryIDs {
		inventory, e := client.BookInventory(ctx, libraryID)
		if e != nil {
			return ABSSeparationView{}, ErrUpstream
		}
		plans = append(plans, buildABSBookTagPlan(instanceID, libraryID, inventory, identities))
	}
	if err = s.persistABSSeparationPreview(ctx, plans, adminID); err != nil {
		return ABSSeparationView{}, err
	}
	return s.ABSLibrarySeparationStatus(instanceID)
}

func (s *Service) persistABSSeparationPreview(ctx context.Context, plans []absBookTagPlan, adminID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, plan := range plans {
		var state string
		err = tx.QueryRowContext(ctx, `SELECT state FROM abs_library_separation_runs WHERE instance_id=? AND library_id=?`, plan.InstanceID, plan.LibraryID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && state != "previewed" {
			return ErrSeparationState
		}
		data, e := json.Marshal(plan)
		if e != nil {
			return e
		}
		hash := sha256.Sum256(data)
		planHash := hex.EncodeToString(hash[:])
		_, err = tx.ExecContext(ctx, `INSERT INTO abs_library_separation_runs
 (instance_id,library_id,state,inventory_count,shared_count,requested_count,unresolved_count,plan_hash,plan_json,last_error,created_by,updated_at)
 VALUES(?,?,'previewed',?,?,?,?,?,?, '',?,CURRENT_TIMESTAMP)
 ON CONFLICT(instance_id,library_id) DO UPDATE SET state='previewed',inventory_count=excluded.inventory_count,
 shared_count=excluded.shared_count,requested_count=excluded.requested_count,unresolved_count=excluded.unresolved_count,
 plan_hash=excluded.plan_hash,plan_json=excluded.plan_json,last_error='',created_by=excluded.created_by,updated_at=CURRENT_TIMESTAMP`,
			plan.InstanceID, plan.LibraryID, plan.InventoryCount, plan.SharedCount, plan.RequestedCount,
			plan.UnresolvedCount, planHash, string(data), adminID)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM abs_library_separation_items WHERE instance_id=? AND library_id=?`, plan.InstanceID, plan.LibraryID); err != nil {
			return err
		}
		for _, item := range plan.Items {
			users, e := json.Marshal(item.UserIDs)
			if e != nil {
				return e
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO abs_library_separation_items(instance_id,library_id,item_id,classification,requester_user_ids,state)
 VALUES(?,?,?,?,?,'pending')`, plan.InstanceID, plan.LibraryID, item.ItemID, item.Class, string(users)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func aggregateABSPlanHash(plans []absBookTagPlan) string {
	sort.Slice(plans, func(i, j int) bool { return plans[i].LibraryID < plans[j].LibraryID })
	var data strings.Builder
	for _, plan := range plans {
		b, _ := json.Marshal(plan)
		h := sha256.Sum256(b)
		data.WriteString(plan.LibraryID)
		data.WriteByte(':')
		data.WriteString(hex.EncodeToString(h[:]))
		data.WriteByte('\n')
	}
	h := sha256.Sum256([]byte(data.String()))
	return hex.EncodeToString(h[:])
}

func (s *Service) loadABSSeparationPlans(ctx context.Context, instanceID string) ([]absBookTagPlan, []ABSSeparationLibraryStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT library_id,state,inventory_count,shared_count,requested_count,unresolved_count,plan_json,last_error,
 (SELECT COUNT(*) FROM abs_library_separation_items i WHERE i.instance_id=r.instance_id AND i.library_id=r.library_id AND i.state='pending'),
 (SELECT COUNT(*) FROM abs_library_separation_items i WHERE i.instance_id=r.instance_id AND i.library_id=r.library_id AND i.state='failed')
 FROM abs_library_separation_runs r WHERE instance_id=? ORDER BY library_id`, instanceID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	plans := []absBookTagPlan{}
	statuses := []ABSSeparationLibraryStatus{}
	for rows.Next() {
		var p absBookTagPlan
		var status ABSSeparationLibraryStatus
		var raw string
		if err = rows.Scan(&status.LibraryID, &status.State, &status.InventoryCount, &status.SharedCount, &status.RequestedCount, &status.UnresolvedCount, &raw, &status.LastError, &status.PendingItems, &status.FailedItems); err != nil {
			return nil, nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, nil, errors.New("stored Audiobookshelf separation plan is invalid")
		}
		p.LibraryID = status.LibraryID
		plans = append(plans, p)
		statuses = append(statuses, status)
	}
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	return plans, statuses, nil
}

func (s *Service) ABSLibrarySeparationStatus(instanceID string) (ABSSeparationView, error) {
	if _, err := s.mediaServerInstance(instanceID); err != nil {
		return ABSSeparationView{}, err
	}
	plans, statuses, err := s.loadABSSeparationPlans(context.Background(), instanceID)
	if err != nil {
		return ABSSeparationView{}, err
	}
	view := ABSSeparationView{InstanceID: instanceID, Libraries: statuses, CanApply: len(plans) > 0}
	if len(plans) == 0 {
		view.State = "not_started"
		return view, nil
	}
	view.PlanHash = aggregateABSPlanHash(plans)
	view.State = "previewed"
	for _, p := range plans {
		if len(view.UnmatchedRequests) == 0 {
			view.UnmatchedRequests = append(view.UnmatchedRequests, p.UnmatchedRequest...)
		}
	}
	for _, status := range statuses {
		if status.State == "enabled" {
			view.State = "enabled"
		} else if status.State == "applying" || status.State == "error" || status.State == "ready" {
			if view.State != "enabled" {
				view.State = status.State
			}
		}
		if status.PendingItems > 0 || status.FailedItems > 0 || status.State == "error" {
			view.CanApply = true
		}
	}
	if view.State == "enabled" {
		view.CanApply = false
	}
	return view, nil
}

// ApplyABSLibrarySeparation applies a reviewed, exact-ID plan to every book
// library before enabling any managed user policy. Item tag writes are
// idempotent and recorded per item; a retry resumes the same plan.
func (s *Service) ApplyABSLibrarySeparation(ctx context.Context, instanceID, planHash string, confirmUnresolved bool) error {
	s.separationMu.Lock()
	defer s.separationMu.Unlock()
	inst, err := s.mediaServerInstance(instanceID)
	if err != nil {
		return err
	}
	if inst.ServiceType != "audiobookshelf" || inst.MediaServerConfigInvalid {
		return ErrNotMediaServer
	}
	client, err := absProvider(s, inst)
	if err != nil {
		return ErrUpstream
	}
	plans, _, err := s.loadABSSeparationPlans(ctx, instanceID)
	if err != nil || len(plans) == 0 {
		return ErrSeparationPlan
	}
	if aggregateABSPlanHash(plans) != planHash {
		return ErrSeparationPlan
	}
	libraries, err := client.Libraries(ctx)
	if err != nil {
		return ErrUpstream
	}
	currentBookLibraries := []string{}
	for _, library := range libraries {
		if library.CollectionType == "book" {
			currentBookLibraries = append(currentBookLibraries, library.ID)
		}
	}
	plannedBookLibraries := make([]string, 0, len(plans))
	for _, plan := range plans {
		plannedBookLibraries = append(plannedBookLibraries, plan.LibraryID)
	}
	slices.Sort(currentBookLibraries)
	slices.Sort(plannedBookLibraries)
	if !slices.Equal(currentBookLibraries, plannedBookLibraries) {
		return ErrSeparationPlan
	}
	for _, plan := range plans {
		if len(plan.UnmatchedRequest) > 0 && !confirmUnresolved {
			return ErrSeparationPlan
		}
		inventory, e := client.BookInventory(ctx, plan.LibraryID)
		if e != nil || !sameABSInventory(inventory, plan.Items) {
			return ErrSeparationPlan
		}
		var state string
		if e = s.db.QueryRowContext(ctx, `SELECT state FROM abs_library_separation_runs WHERE instance_id=? AND library_id=?`, instanceID, plan.LibraryID).Scan(&state); e != nil {
			return ErrSeparationPlan
		}
		if state == "enabled" {
			continue
		}
		if state != "previewed" && state != "applying" && state != "ready" && state != "error" {
			return ErrSeparationState
		}
	}
	for _, plan := range plans {
		if _, err = s.db.ExecContext(ctx, `UPDATE abs_library_separation_runs SET state='applying',last_error='',updated_at=CURRENT_TIMESTAMP WHERE instance_id=? AND library_id=? AND state!='enabled'`, instanceID, plan.LibraryID); err != nil {
			return err
		}
		for _, item := range plan.Items {
			var state string
			if err = s.db.QueryRowContext(ctx, `SELECT state FROM abs_library_separation_items WHERE instance_id=? AND library_id=? AND item_id=?`, instanceID, plan.LibraryID, item.ItemID).Scan(&state); err != nil {
				return err
			}
			if state == "applied" {
				continue
			}
			for _, tag := range item.TagsToAdd {
				if err = client.SetItemTag(ctx, item.ItemID, tag, true); err != nil {
					return s.failABSSeparationItem(ctx, instanceID, plan.LibraryID, item.ItemID, err)
				}
			}
			for _, tag := range item.TagsToDrop {
				if err = client.SetItemTag(ctx, item.ItemID, tag, false); err != nil {
					return s.failABSSeparationItem(ctx, instanceID, plan.LibraryID, item.ItemID, err)
				}
			}
			if _, err = s.db.ExecContext(ctx, `UPDATE abs_library_separation_items SET state='applied',last_error='',updated_at=CURRENT_TIMESTAMP WHERE instance_id=? AND library_id=? AND item_id=?`, instanceID, plan.LibraryID, item.ItemID); err != nil {
				return err
			}
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE abs_library_separation_runs SET state='ready',updated_at=CURRENT_TIMESTAMP WHERE instance_id=? AND library_id=?`, instanceID, plan.LibraryID); err != nil {
			return err
		}
	}
	if err = s.enableABSSeparatedAccounts(ctx, inst, client); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE abs_library_separation_runs SET state='enabled',last_error='',updated_at=CURRENT_TIMESTAMP WHERE instance_id=?`, instanceID)
	return err
}

func sameABSInventory(inventory []audiobookshelf.BookInventoryItem, planItems []absBookTagPlanItem) bool {
	if len(inventory) != len(planItems) {
		return false
	}
	want := make(map[string]string, len(planItems))
	for _, item := range planItems {
		want[item.ItemID] = item.SnapshotHash
	}
	for _, item := range inventory {
		if hash, ok := want[item.ID]; !ok || hash != absInventoryItemHash(item) {
			return false
		}
	}
	return true
}

func (s *Service) failABSSeparationItem(ctx context.Context, instanceID, libraryID, itemID string, cause error) error {
	_, _ = s.db.ExecContext(ctx, `UPDATE abs_library_separation_items SET state='failed',last_error='Audiobookshelf item tag update failed',updated_at=CURRENT_TIMESTAMP WHERE instance_id=? AND library_id=? AND item_id=?`, instanceID, libraryID, itemID)
	_, _ = s.db.ExecContext(ctx, `UPDATE abs_library_separation_runs SET state='error',last_error='Audiobookshelf item tag update failed',updated_at=CURRENT_TIMESTAMP WHERE instance_id=? AND library_id=?`, instanceID, libraryID)
	s.logger.Warn("mediaaccess: ABS item tagging failed", "instance_id", instanceID, "library_id", libraryID, "item_id", itemID, "err", cause)
	return ErrUpstream
}

type absManagedTagAccount struct {
	userID int64
	remote string
	role   string
}

func (s *Service) enableABSSeparatedAccounts(ctx context.Context, inst *instance.Instance, client absSeparationClient) error {
	rows, err := s.db.QueryContext(ctx, `SELECT a.user_id,a.remote_user_id,u.role FROM user_media_server_accounts a
	 JOIN users u ON u.id=a.user_id WHERE a.instance_id=? AND a.manage_access=1 ORDER BY a.user_id`, inst.ID)
	if err != nil {
		return err
	}
	accounts := []absManagedTagAccount{}
	for rows.Next() {
		var account absManagedTagAccount
		if err = rows.Scan(&account.userID, &account.remote, &account.role); err != nil {
			rows.Close()
			return err
		}
		accounts = append(accounts, account)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type update struct {
		account absManagedTagAccount
		tags    []string
	}
	updates := make([]update, 0, len(accounts))
	for _, account := range accounts {
		if account.role == auth.RoleAdmin {
			continue
		}
		policy, e := client.TagAccessPolicy(ctx, account.remote)
		if e != nil {
			return ErrUpstream
		}
		if policy.IsAdministrator {
			continue
		}
		tags := []string{absSharedLibraryTag, ABSUserTag(account.userID)}
		slices.Sort(tags)
		alreadyConfigured := !policy.AccessAllTags && !policy.DenySelected && slices.Equal(policy.SelectedTags, tags)
		if !alreadyConfigured && (!policy.AccessAllTags || policy.DenySelected) {
			return ErrSeparationPolicy
		}
		updates = append(updates, update{account: account, tags: tags})
	}
	for _, change := range updates {
		unlock := s.lock(change.account.userID, inst.ID)
		err = client.SetItemTagAllowlist(ctx, change.account.remote, change.tags)
		unlock()
		if err != nil {
			return ErrUpstream
		}
	}
	return nil
}

func (s *Service) hasEnabledABSSeparation(instanceID string) (bool, error) {
	var enabled bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM abs_library_separation_runs WHERE instance_id=? AND state='enabled')`, instanceID).Scan(&enabled)
	return enabled, err
}

// Called under the existing user/server account lock. Cantinarr administrators
// are intentionally left with their current global ABS visibility.
func (s *Service) syncManagedABSUserTagPolicy(ctx context.Context, inst *instance.Instance, provider mediaserver.Provider, userID int64, remoteID string) error {
	if inst.ServiceType != "audiobookshelf" {
		return nil
	}
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, userID).Scan(&role); err != nil {
		return err
	}
	if role == auth.RoleAdmin {
		return nil
	}
	enabled, err := s.hasEnabledABSSeparation(inst.ID)
	if err != nil || !enabled {
		return err
	}
	client, ok := provider.(absSeparationClient)
	if !ok {
		return ErrUpstream
	}
	policy, err := client.TagAccessPolicy(ctx, remoteID)
	if err != nil {
		return err
	}
	if policy.IsAdministrator {
		return nil
	}
	tags := []string{absSharedLibraryTag, ABSUserTag(userID)}
	slices.Sort(tags)
	if !policy.AccessAllTags && !policy.DenySelected && slices.Equal(policy.SelectedTags, tags) {
		return nil
	}
	if !policy.AccessAllTags || policy.DenySelected {
		return ErrSeparationPolicy
	}
	return client.SetItemTagAllowlist(ctx, remoteID, tags)
}

func (h *Handler) ABSLibrarySeparationStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := h.svc.ABSLibrarySeparationStatus(chi.URLParam(r, "instanceID"))
	h.writeABSSeparation(w, view, err)
}

func (h *Handler) PreviewABSLibrarySeparation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body absSeparationPreviewRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.LibraryIDs == nil {
		h.writeABSSeparation(w, ABSSeparationView{}, ErrSeparationLibrary)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	claims := auth.GetClaims(r.Context())
	adminID := int64(0)
	if claims != nil {
		adminID = claims.UserID
	}
	view, err := h.svc.PreviewABSLibrarySeparation(ctx, chi.URLParam(r, "instanceID"), body.LibraryIDs, adminID)
	h.writeABSSeparation(w, view, err)
}

func (h *Handler) ApplyABSLibrarySeparation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var body absSeparationApplyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.PlanHash == "" {
		h.writeABSSeparation(w, ABSSeparationView{}, ErrSeparationPlan)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	err := h.svc.ApplyABSLibrarySeparation(ctx, chi.URLParam(r, "instanceID"), body.PlanHash, body.ConfirmUnresolved)
	view, statusErr := h.svc.ABSLibrarySeparationStatus(chi.URLParam(r, "instanceID"))
	if err == nil {
		err = statusErr
	}
	h.writeABSSeparation(w, view, err)
}

func (h *Handler) writeABSSeparation(w http.ResponseWriter, view ABSSeparationView, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, view)
	case errors.Is(err, ErrInstanceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance not found"})
	case errors.Is(err, ErrSeparationLibrary):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrSeparationLibrary.Error()})
	case errors.Is(err, ErrSeparationPlan), errors.Is(err, ErrSeparationState), errors.Is(err, ErrSeparationPolicy):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNotMediaServer):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "library separation requires Audiobookshelf"})
	default:
		h.logger.Warn("mediaaccess: ABS library separation", "err", fmt.Sprint(err))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Could not complete Audiobookshelf library separation. Retry shortly."})
	}
}
