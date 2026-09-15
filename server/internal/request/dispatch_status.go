package request

import (
	"errors"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// Delivery is intent, never an availability snapshot. Live state can cover a
// format while its saved job is still waiting (for example an admin imports it
// directly). Failed reads preserve the saved job and explicitly mark library
// truth unknown. Completed jobs do not claim a file still exists.
func (s *Service) overlayDeliveryTruth(userID int64, mediaType, foreignID string, out *CreateResponse) {
	known := false
	out.StatusKnown = &known
	out.StatusUnknownReason = "library_unavailable"
	if out.CanonicalForeignID != "" {
		foreignID = out.CanonicalForeignID
	}
	if foreignID == "" {
		return
	}
	completed := map[string]bool{}
	for _, d := range out.Delivery {
		completed[d.Format] = d.State == "complete"
		if d.State == "complete" {
			if d.Format == "" {
				out.Status = StatusRequested
			} else {
				out.BookFormats[d.Format] = StatusRequested
			}
		}
	}
	if mediaType == "book" {
		client, instanceID, err := s.resolveChaptarr(userID, out.InstanceID)
		if err != nil || client == nil {
			return
		}
		projection, err := s.liveBookProjectionCached(client, instanceID)
		if err != nil {
			return
		}
		recordIDs := map[string]int{}
		for _, d := range out.Delivery {
			var recordID int
			if s.db.QueryRow(`SELECT book_record_id FROM request_dispatch WHERE request_id=? AND format=?`, d.RequestID, d.Format).Scan(&recordID) == nil && recordID > 0 {
				recordIDs[d.Format] = recordID
			}
		}
		var lookupContext []string
		// An accepted numeric binding survives a native re-key even while the
		// metadata catalog is unavailable. Resolve those records below.
		if len(recordIDs) == 0 && len(out.Delivery) > 0 {
			if r, _, err := s.loadRequest(out.Delivery[0].RequestID); err == nil {
				lookupContext = []string{r.title, r.searchTerm}
			}
		}
		live, canonicalID, err := projection.resolveFormatsWithLookup(client, foreignID, lookupContext)
		if err != nil {
			if errors.Is(err, chaptarr.ErrBookIdentityAmbiguous) {
				out.StatusUnknownReason = "identity_ambiguous"
			}
			if errors.Is(err, ErrBookFormatUnresolved) {
				out.StatusUnknownReason = "format_unresolved"
			}
			return
		}
		if canonicalID != "" && canonicalID != foreignID {
			out.CanonicalForeignID = canonicalID
		}
		known = true
		out.StatusUnknownReason = ""
		if out.BookFormats == nil {
			out.BookFormats = map[string]string{}
		}
		for _, format := range []string{BookFormatEbook, BookFormatAudiobook} {
			status, exists := live[format]
			if !exists {
				if record, ok := projection.recordByID(recordIDs[format]); ok {
					status, exists = record.Status, true
					if record.ForeignID != "" {
						out.CanonicalForeignID = record.ForeignID
					}
				}
			}
			if exists && status != StatusUnavailable {
				out.BookFormats[format] = status
				delete(out.BookFormatWaits, format)
			} else if completed[format] {
				out.BookFormats[format] = StatusUnavailable
			}
		}
		out.Status = collapseBookStatuses(out.BookFormats, out.Status)
	} else {
		client, instanceID, err := s.resolveLidarr(userID, out.InstanceID)
		if err != nil || client == nil {
			return
		}
		projection, err := s.liveMusicProjectionCached(client, instanceID)
		if err != nil {
			return
		}
		known = true
		out.StatusUnknownReason = ""
		status, exists := projection.Statuses[foreignID]
		if !exists {
			var recordID int
			if len(out.Delivery) > 0 {
				s.db.QueryRow(`SELECT book_record_id FROM request_log WHERE id=?`, out.Delivery[0].RequestID).Scan(&recordID)
			}
			if record, ok := projection.recordByID(recordID); ok {
				status, exists = record.Status, true
				if record.ForeignID != "" {
					out.CanonicalForeignID = record.ForeignID
				}
			}
		}
		if exists && status != StatusUnavailable {
			out.Status = status
		} else if completed[""] {
			out.Status = StatusUnavailable
		}
	}
}
