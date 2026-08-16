package remediation

import "errors"

// batchApproveMaxIDs bounds one approve-batch request. The one-proposed-per-
// issue invariant keeps the real queue near the open awaiting-approval count,
// so this is a defensive ceiling on serial arr I/O per request, not a product
// limit an admin should ever meet.
const batchApproveMaxIDs = 100

// BatchApproveRequest is the POST /api/admin/agent-actions/approve-batch body:
// the explicit action ids the admin reviewed. There is deliberately no
// "approve everything currently proposed" form — a proposal that parks while
// the batch is in flight must never be approved sight-unseen.
type BatchApproveRequest struct {
	IDs []int64 `json:"ids"`
}

// BatchApproveResponse carries one verdict per requested id (duplicates
// collapsed to their first occurrence), in request order.
type BatchApproveResponse struct {
	Results []BatchApprovalItem `json:"results"`
}

// BatchApprovalItem is one id's outcome within an approve-batch request.
// Status is the durable action status after the attempt ("executed", "failed",
// "outcome_unknown", "superseded", ...) or one of two batch-only verdicts:
// "skipped" — a decision, closure, or arr-recovery race owned the proposal
// first and nothing was executed — and "error" — the id could not be decided
// at all. Detail carries the human-readable reason for anything that did not
// execute cleanly.
type BatchApprovalItem struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// ApproveActions approves a list of proposed actions through the same
// per-item decision core as ApproveAction — one CAS claim, both arr-recovery
// preflights, at-most-once dispatch — sequentially, so a batch never runs arr
// mutations in parallel (sweepAutoApprovals sets that precedent). One item's
// conflict or failure never stops the rest: the admin approved the whole
// list, and each proposal's outcome is independent. The loop runs to
// completion even if the requesting client disconnects, so "approve all"
// never half-finishes because an app was backgrounded; every outcome is
// durable and the client reconciles by re-reading the queue.
//
// No overrides and no remember: editing params or arming a standing
// auto-approval rule are deliberate per-proposal decisions that keep their
// own single-action flow. Destructive kinds are refused per-item for the same
// reason (see the D6 skip below): file deletion keeps its single-card confirm.
func (s *Service) ApproveActions(adminID int64, ids []int64) []BatchApprovalItem {
	results := make([]BatchApprovalItem, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		// D6: a destructive fix never rides a batch. delete_media_files is the
		// one kind that destroys files on disk, and its single-card confirm
		// (which repeats the exact episodes and says it cannot be undone) is
		// the decision surface it deserves — a batch dialog's per-card warnings
		// are exactly the ones nobody read. Structural, not UI copy: the server
		// refuses it here whatever client sent the list.
		var kind string
		if err := s.db.QueryRow("SELECT kind FROM agent_actions WHERE id = ?", id).Scan(&kind); err == nil &&
			ActionKind(kind) == ActionDeleteMediaFiles {
			results = append(results, BatchApprovalItem{
				ID: id, Status: "skipped",
				Detail: "Deleting imported files needs its own approval — open this fix and approve it individually.",
			})
			continue
		}
		act, err := s.ApproveAction(adminID, id, nil)
		switch {
		case errors.Is(err, ErrActionDecisionConflict):
			// Nothing executed: the arr began recovering, media state changed,
			// or another decision won. The proposal's durable state is whatever
			// the winner left; the queue view shows it on the next read.
			results = append(results, BatchApprovalItem{ID: id, Status: "skipped", Detail: err.Error()})
		case err != nil:
			results = append(results, BatchApprovalItem{ID: id, Status: "error", Detail: err.Error()})
		default:
			item := BatchApprovalItem{ID: id, Status: act.Status}
			// Surface the stored (already-redacted) outcome text for anything
			// that did not execute cleanly, so the batch response is
			// self-sufficient without a follow-up read per item.
			if act.Status != ActionExecuted && act.ResultText != nil {
				item.Detail = *act.ResultText
			}
			results = append(results, item)
		}
	}
	return results
}
