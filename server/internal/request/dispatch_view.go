package request

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func dispatchMessage(state string) string {
	switch state {
	case "approval":
		return "Waiting for approval. Your request is saved."
	case "queued", "processing", "retry":
		return "Your request is saved. Delivery to the library continues in the background."
	case "needs_match":
		return "This saved request needs attention. Search your Chaptarr library for the book."
	case "attention":
		return "This saved request needs attention before delivery can continue."
	case "waiting_library":
		return bookAuthorImportingMessage
	case "cancelled":
		return "Request cancelled."
	case "complete":
		return "The library accepted this request."
	}
	return "This request is saved."
}

// Messages explain the action a requester can take without exposing service
// URLs, credentials, or untrusted upstream error bodies.
func deliveryMessage(state, code string) string {
	switch code {
	case "tv_match_changed":
		return "The TV match changed. An admin must review the affected request in TV matches before delivery can continue."
	case "tv_match_paused", "tv_seasons_unmapped", "tv_match_ambiguous", "tv_metadata_unavailable":
		return (&tvMatchError{code: code}).Error()
	case "tv_target_missing":
		return "This request has no verified TV target. An admin must review its TV match."
	case "tv_metadata_refresh":
		return "Your request is saved. Waiting for the library to finish adding the series before selecting the episode."
	case "tv_pilot_unavailable":
		return "Your pilot request is saved. Waiting for episode 1 of the selected season to appear in the library."
	}
	if code == "catalog_retired" {
		return "Needs attention. Open Library requests can no longer be matched. Cancel this request and search your Chaptarr library for the book."
	}
	if state != "attention" {
		return dispatchMessage(state)
	}
	switch code {
	case "catalog_access_denied", "service_unavailable":
		return "The library connection or setup needs an administrator's attention. Your request is saved."
	case "access_unavailable", "instance_missing":
		return "Access to the selected library changed. Your request is saved."
	case "import_failed":
		return "The library could not finish importing this author. You can try again."
	case "import_cancelled":
		return "The library import was cancelled. Your request is saved."
	case "import_identity_ambiguous", "library_identity_unresolved":
		return "An administrator needs to resolve conflicting library records. Your request is saved."
	case "retry_limit":
		return "Automatic retries paused after repeated failures. Your request is saved; you can try again."
	}
	return dispatchMessage(state)
}

func (s *Service) deliveryStates(id int64) ([]DeliveryState, error) {
	rows, err := s.db.Query(`SELECT format,state,attempts,last_attempt_at,next_attempt_at,code,message FROM request_dispatch WHERE request_id=? ORDER BY format`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryState{}
	for rows.Next() {
		d := DeliveryState{RequestID: id}
		var last, next int64
		if err = rows.Scan(&d.Format, &d.State, &d.Attempts, &last, &next, &d.Code, &d.Message); err != nil {
			return nil, err
		}
		if last > 0 {
			t := time.Unix(last, 0).UTC()
			d.LastAttemptAt = &t
		}
		if next > 0 && (d.State == "queued" || d.State == "retry") {
			t := time.Unix(next, 0).UTC()
			d.NextAttemptAt = &t
		}
		if d.Message == "" {
			d.Message = deliveryMessage(d.State, d.Code)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) deliveryResponse(userID int64, ids []int64, title, instanceID string, ref *CatalogRef) (*CreateResponse, error) {
	out := &CreateResponse{Success: true, Title: title, InstanceID: instanceID, CatalogRef: ref, Status: StatusRequested}
	ids = append([]int64(nil), ids...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	seen := map[string]bool{}
	for _, id := range ids {
		if out.RequestID == 0 {
			out.RequestID = id
		}
		states, err := s.deliveryStates(id)
		if err != nil {
			return nil, err
		}
		var native, stored, park, canonical, savedTitle, mediaType, subscribedFormat string
		var ownerID int64
		var requestedAt time.Time
		if err = s.db.QueryRow(`SELECT COALESCE(foreign_id,''),status,COALESCE(park_reason,''),title,requested_at,user_id,media_type FROM request_log WHERE id=?`, id).Scan(&native, &stored, &park, &savedTitle, &requestedAt, &ownerID, &mediaType); err != nil {
			return nil, err
		}
		// Older native music approvals predate dispatch rows. Reading their
		// saved intent must not depend on a live library or migrate on GET.
		if len(states) == 0 && mediaType == "music" && (stored == StatusPending || stored == StatusDenied) {
			state := "approval"
			if stored == StatusDenied {
				state = "cancelled"
			}
			states = append(states, DeliveryState{RequestID: id, State: state, Message: deliveryMessage(state, "")})
		}
		admin := s.userIsAdmin(userID)
		if mediaType == "book" && !admin {
			if err = s.db.QueryRow(`SELECT book_format FROM book_request_waiters WHERE request_id=? AND user_id=?`, id, userID).Scan(&subscribedFormat); err != nil && ownerID != userID {
				return nil, fmt.Errorf("request is not available to you")
			}
		}
		s.db.QueryRow(`SELECT canonical_foreign_id FROM request_dispatch WHERE request_id=? AND canonical_foreign_id!='' LIMIT 1`, id).Scan(&canonical)
		if out.CanonicalForeignID == "" && canonical != "" {
			out.CanonicalForeignID = canonical
		} else if out.CanonicalForeignID == "" && ref != nil {
			out.CanonicalForeignID = native
		}
		if out.RequestID == id && savedTitle != "" {
			out.Title = strings.TrimSpace(savedTitle)
		}
		for _, d := range states {
			// Callers pass newest requests first. An old cancellation must never
			// replace a later request, and a subscriber only owns their formats.
			if seen[d.Format] || (subscribedFormat != "" && !bookFormatIncludes(subscribedFormat, d.Format)) {
				continue
			}
			seen[d.Format] = true
			d.CanManage = admin || ownerID == userID
			d.CanCancel = d.CanManage || subscribedFormat != ""
			if stored == StatusDenied && d.State != "complete" {
				d.State = "cancelled"
				d.Message = deliveryMessage(d.State, d.Code)
			}
			// The native import worker may have completed or demoted the row.
			if d.State == "waiting_library" && stored != StatusPending {
				d.State = "complete"
				d.Message = deliveryMessage(d.State, d.Code)
			}
			if d.State == "waiting_library" && stored == StatusPending && park == "" {
				d.State = "attention"
				d.Message = "The library import needs attention."
			}
			out.Delivery = append(out.Delivery, d)
			status := StatusRequested
			if d.State == "complete" && d.Code != "" {
				status = d.Code
			}
			if d.State == "approval" {
				status = StatusPending
			}
			if d.State == "cancelled" {
				status = StatusDenied
			}
			if d.Format == "" {
				out.Status = status
			}
			if d.Format != "" {
				if out.BookFormats == nil {
					out.BookFormats = map[string]string{}
				}
				out.BookFormats[d.Format] = status
			}
			if d.State != "complete" && d.State != "cancelled" {
				out.Message = d.Message
			}
			if d.State == "approval" {
				out.Status = StatusPending
			}
			if d.Format != "" && d.State != "complete" && d.State != "cancelled" && d.State != "approval" {
				if out.BookFormatWaits == nil {
					out.BookFormatWaits = map[string]BookFormatWait{}
				}
				reason := d.State
				if reason == "waiting_library" {
					reason = bookParkReasonAuthorImport
				}
				out.BookFormatWaits[d.Format] = BookFormatWait{Reason: reason, WaitingSince: requestedAt, LastAttemptAt: d.LastAttemptAt}
			}
		}
	}
	return out, nil
}

func (s *Service) hasDispatch(id int64) bool {
	var n int
	return s.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch WHERE request_id=?`, id).Scan(&n) == nil && n > 0
}

// Saved delivery is evidence of intent or acceptance, never of current files.
// Availability is read separately from Chaptarr (or overlaid by the default
// delivery-status read for older clients).
func savedDeliveryState(out *CreateResponse) {
	known := false
	out.StatusKnown = &known
	if out.Status == StatusAvailable || out.Status == StatusDownloading {
		out.Status = StatusRequested
	}
	for format, status := range out.BookFormats {
		if status == StatusAvailable || status == StatusDownloading {
			out.BookFormats[format] = StatusRequested
		}
	}
}

func (s *Service) attachPendingDelivery(p *PendingRequest) {
	p.Delivery, _ = s.deliveryStates(p.ID)
	var provider, id string
	if s.db.QueryRow(`SELECT COALESCE(catalog_provider,''),COALESCE(catalog_id,'') FROM request_log WHERE id=?`, p.ID).Scan(&provider, &id) == nil && provider != "" {
		p.CatalogRef = &CatalogRef{Provider: provider, ID: id}
	}
	for _, d := range p.Delivery {
		if d.LastAttemptAt != nil && (p.LastAttemptAt == nil || d.LastAttemptAt.After(*p.LastAttemptAt)) {
			p.LastAttemptAt = d.LastAttemptAt
		}
	}
}

// Administrators can inspect saved intent even after its instance is removed.
// This does not authorize a library read or retry against another instance.
func (s *Service) authorizeDeliveryRead(userID, id int64, r *resolvedRequest) error {
	if s.userIsAdmin(userID) {
		return nil
	}
	var owns int
	if s.db.QueryRow(`SELECT COUNT(*) FROM request_log r WHERE r.id=? AND (r.user_id=? OR EXISTS(SELECT 1 FROM book_request_waiters w WHERE w.request_id=r.id AND w.user_id=?))`, id, userID, userID).Scan(&owns) != nil || owns != 1 {
		return fmt.Errorf("request is not available to you")
	}
	_, err := s.deliveryInstance(userID, r.mediaType, r.instanceID)
	return err
}

func (s *Service) DeliveryByID(userID, id int64, includeLive ...bool) (*CreateResponse, error) {
	r, _, err := s.loadRequest(id)
	if err != nil {
		return nil, err
	}
	if err = s.authorizeDeliveryRead(userID, id, r); err != nil {
		return nil, err
	}
	if r.mediaType == "tv" {
		if err = s.checkContentPolicy(userID, s.userIsAdmin(userID), "tv", r.tmdbID); err != nil {
			return nil, err
		}
		out, err := s.tvDeliveryResponse(userID, id)
		if err != nil {
			return nil, err
		}
		if len(includeLive) == 0 || includeLive[0] {
			live, err := s.tvLiveStatus(userID, r.tmdbID, r.instanceID)
			if err != nil {
				return nil, err
			}
			out.StatusKnown, out.StatusUnknownReason, out.Match = live.StatusKnown, live.StatusUnknownReason, live.Match
			if live.Status == StatusAvailable || live.Status == StatusPartial {
				out.Status = live.Status
			}
		} else {
			savedDeliveryState(out)
		}
		return out, nil
	}
	p := PendingRequest{ID: id}
	s.attachPendingDelivery(&p)
	out, err := s.deliveryResponse(userID, []int64{id}, r.title, r.instanceID, p.CatalogRef)
	if err != nil {
		return nil, err
	}
	if len(includeLive) == 0 || includeLive[0] {
		if r.mediaType == "movie" {
			if err = s.checkContentPolicy(userID, s.userIsAdmin(userID), "movie", r.tmdbID); err != nil {
				return nil, err
			}
			live, e := s.statusFor(userID, r.tmdbID, "movie", r.instanceID)
			known := e == nil && live != nil
			out.StatusKnown = &known
			if !known {
				out.StatusUnknownReason = "library_unavailable"
			} else if live.Status == StatusAvailable || live.Status == StatusDownloading {
				out.Status = live.Status
			}
		} else {
			s.overlayDeliveryTruth(userID, r.mediaType, r.foreignID, out)
		}
	} else {
		savedDeliveryState(out)
	}
	if err = s.authorizeDeliveryRead(userID, id, r); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) DeliveryStatus(userID int64, mediaType, foreignID, instanceID string, ref *CatalogRef, includeLive ...bool) (*CreateResponse, error) {
	// TV identity is TMDB-based, not foreign_id-based. Use its title status or
	// the authorized delivery-by-request-ID endpoint, never an empty-ID group.
	if mediaType != "book" && mediaType != "music" {
		return nil, fmt.Errorf("delivery status requires book or music identity")
	}
	if err := validateCatalogRef(mediaType, ref); err != nil {
		return nil, err
	}
	id, err := s.deliveryInstance(userID, mediaType, instanceID)
	if err != nil {
		return nil, err
	}
	provider, sourceID := "", ""
	if ref != nil {
		provider, sourceID = ref.Provider, ref.ID
		foreignID = ""
	}
	where := `COALESCE(r.foreign_id,'')=?`
	args := []any{mediaType, id, foreignID}
	if ref != nil {
		where = `r.catalog_provider=? AND r.catalog_id=?`
		args = []any{mediaType, id, provider, sourceID}
	}
	if mediaType == "music" {
		where, args, err = musicIdentityWhere(s.db, id, foreignID, provider, sourceID)
		if err != nil {
			return nil, err
		}
		args = append([]any{mediaType, id}, args...)
	}
	args = append(args, userID, userID)
	rows, err := s.db.Query(`SELECT r.id,r.title FROM request_log r WHERE r.media_type=? AND COALESCE(r.instance_id,'')=? AND `+where+` AND (r.user_id=? OR EXISTS(SELECT 1 FROM book_request_waiters bw WHERE bw.request_id=r.id AND bw.user_id=?)) AND (EXISTS(SELECT 1 FROM request_dispatch d WHERE d.request_id=r.id) OR (r.media_type='music' AND r.status='pending')) ORDER BY r.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	title := ""
	for rows.Next() {
		var requestID int64
		var t string
		if err = rows.Scan(&requestID, &t); err != nil {
			break
		}
		ids = append(ids, requestID)
		if title == "" {
			title = t
		}
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	if _, err = s.deliveryInstance(userID, mediaType, id); err != nil {
		return nil, err
	}
	out, err := s.deliveryResponse(userID, ids, title, id, ref)
	if err != nil {
		return nil, err
	}
	if len(includeLive) == 0 || includeLive[0] {
		s.overlayDeliveryTruth(userID, mediaType, foreignID, out)
	} else {
		savedDeliveryState(out)
	}
	if _, err = s.deliveryInstance(userID, mediaType, id); err != nil {
		return nil, err
	}
	return out, nil
}

// activeDeliveryStatus overlays durable intent only while delivery is pending.
// Once delivery completes, the existing status path recomputes live truth.
func (s *Service) activeDeliveryStatus(userID int64, mediaType, foreignID, instanceID string) (*StatusResponse, error) {
	if mediaType != "music" {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM request_dispatch d JOIN request_log r ON r.id=d.request_id WHERE r.media_type=? AND r.foreign_id=? AND r.status='pending' AND d.state NOT IN ('complete','cancelled') AND (r.user_id=? OR EXISTS(SELECT 1 FROM book_request_waiters bw WHERE bw.request_id=r.id AND bw.user_id=?))`, mediaType, foreignID, userID, userID).Scan(&count); err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, nil
		}
	}
	saved, err := s.DeliveryStatus(userID, mediaType, foreignID, instanceID, nil, mediaType != "music")
	if err != nil {
		return nil, err
	}

	active := false
	for _, d := range saved.Delivery {
		active = active || (d.State != "complete" && d.State != "cancelled")
	}
	if !active {
		return nil, nil
	}
	return &StatusResponse{RequestID: saved.RequestID, CatalogRef: saved.CatalogRef, Delivery: saved.Delivery, Status: saved.Status, StatusKnown: saved.StatusKnown, StatusUnknownReason: saved.StatusUnknownReason, BookFormats: saved.BookFormats, BookFormatWaits: saved.BookFormatWaits, CanonicalForeignID: saved.CanonicalForeignID}, nil
}
