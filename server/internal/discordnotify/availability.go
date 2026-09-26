package discordnotify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Aggregate file statistics can stay unchanged when episode identities change.
// Limit the fast path even when the provider's observation key is unchanged.
const availabilityRefreshInterval = 5 * time.Minute

func (s *Service) observeAvailability(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if s.source != nil {
			err := s.scanAvailability(ctx)
			s.errorMu.Lock()
			s.availabilityError = ""
			if err != nil && ctx.Err() == nil {
				s.availabilityError = "Some requested content could not be checked. A library could not be read, or a TV match needs attention in Request Defaults > TV matches. Missed availability is announced once it can be checked."
			}
			s.errorMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.observeWake:
		}
	}
}

func (s *Service) scanAvailability(ctx context.Context) error {
	s.observationMu.Lock()
	defer s.observationMu.Unlock()
	c, err := s.readConfig(s.db)
	if err != nil || !c.Enabled || !c.Events[RequestAvailable] || s.source == nil {
		return err
	}
	// Movie/album and completed book receipts never owe another availability
	// event. TV stays observed because requested seasons can gain new episodes;
	// its observation key can skip a recently completed full read.
	rows, err := s.db.Query(`SELECT r.id,COALESCE(a.observed_key,'') FROM request_log r
	LEFT JOIN discord_availability a ON a.request_id=r.id AND a.epoch=?
	WHERE r.status!='denied' AND r.instance_id IS NOT NULL AND NOT (a.request_id IS NOT NULL AND (
	 (r.media_type IN ('movie','music') AND json_array_length(a.seen)>0) OR
	 (r.media_type='book' AND json_array_length(a.seen)>=CASE WHEN r.book_format='both' OR COALESCE(r.book_format,'')='' THEN 2 ELSE 1 END))) ORDER BY r.id`, c.AvailabilityEpoch)
	if err != nil {
		return err
	}
	type observed struct {
		id  int64
		key string
	}
	due := []observed{}
	for rows.Next() {
		var o observed
		if err = rows.Scan(&o.id, &o.key); err != nil {
			break
		}
		due = append(due, o)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// Only retain active requests. A restart has no recent full reads, so a
	// persisted key alone never prevents checking the library again.
	reads := make(map[int64]time.Time, len(due))
	for _, o := range due {
		reads[o.id] = s.availabilityReads[o.id]
	}
	s.availabilityReads = reads
	var lastErr error
	for _, o := range due {
		if err = ctx.Err(); err != nil {
			return err
		}
		key, e := s.source.DiscordObservationKey(ctx, o.id)
		if e != nil {
			if !errors.Is(e, ErrUnverifiable) && !errors.Is(e, ErrDeferred) {
				lastErr = e
			}
			continue
		}
		age := s.now().Sub(reads[o.id])
		if key != "" && key == o.key && !reads[o.id].IsZero() && age >= 0 && age < availabilityRefreshInterval {
			continue
		}
		// Never pair a new digest key with an older cached episode snapshot.
		snapshot, e := s.source.DiscordAvailability(ctx, o.id, true)
		if errors.Is(e, ErrUnverifiable) || errors.Is(e, ErrDeferred) {
			continue
		}
		if e != nil {
			lastErr = e
			continue
		}
		if e = s.recordAvailability(c, o.id, snapshot, key); e != nil {
			lastErr = e
		} else {
			reads[o.id] = s.now()
		}
	}
	return lastErr
}

func (s *Service) recordAvailability(observed configuration, id int64, snapshot Availability, key string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := s.readConfig(tx)
	if err != nil || !c.Enabled || !c.Events[RequestAvailable] || c.AvailabilityEpoch != observed.AvailabilityEpoch {
		return err
	}
	var epoch int64
	var raw, stored string
	err = tx.QueryRow(`SELECT epoch,seen,observed_key FROM discord_availability WHERE request_id=?`, id).Scan(&epoch, &raw, &stored)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	initial := err == sql.ErrNoRows || epoch != c.AvailabilityEpoch
	seen := []Unit{}
	if !initial {
		if err = json.Unmarshal([]byte(raw), &seen); err != nil {
			return err
		}
	}
	keys := map[string]bool{}
	for _, unit := range seen {
		keys[unit.Key] = true
	}
	added := false
	novel := []Unit{}
	for _, unit := range snapshot.Units {
		if !keys[unit.Key] {
			seen = append(seen, unit)
			keys[unit.Key] = true
			added = true
			// A new activation's first read, or a repair re-filing existing
			// work, only establishes the baseline.
			if !initial || (id > c.AvailabilityFloor && !snapshot.Baseline) {
				novel = append(novel, unit)
			}
		}
	}
	// An unchanged observation keeps its receipt: no write on every sweep.
	if !initial && !added && stored == key {
		return nil
	}
	data, _ := json.Marshal(seen)
	if _, err = tx.Exec(`INSERT INTO discord_availability(request_id,epoch,seen,observed_key) VALUES(?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET epoch=excluded.epoch,seen=excluded.seen,observed_key=excluded.observed_key`, id, c.AvailabilityEpoch, string(data), key); err != nil {
		return err
	}
	if len(novel) > 0 {
		if err = s.batchAvailability(tx, c, snapshot.Subject, availabilityPart{RequestID: id, Units: novel}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) batchAvailability(tx *sql.Tx, c configuration, subject Subject, part availabilityPart) error {
	identity := fmt.Sprintf("%s:%s:%d:%s", subject.InstanceID, subject.MediaType, subject.TmdbID, subject.ForeignID)
	group := fmt.Sprintf("available:%d:%x:", c.AvailabilityEpoch, sha256.Sum256([]byte(identity)))
	var rowID int64
	var raw string
	now := s.now().Unix()
	// The first arrival owns the fixed 60-second window. Appending an arrival
	// never moves its deadline, including across process restarts.
	err := tx.QueryRow(`SELECT id,payload FROM discord_notifications WHERE event_key LIKE ? AND revision=? AND status='pending' AND attempts=0 AND created_at>? ORDER BY id DESC LIMIT 1`, group+"%", c.Revision, now-60).Scan(&rowID, &raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		var a requestAlert
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return err
		}
		a.Parts = append(a.Parts, part)
		data, _ := json.Marshal(a)
		_, err = tx.Exec(`UPDATE discord_notifications SET payload=?,updated_at=? WHERE id=? AND status='pending'`, string(data), now, rowID)
		return err
	}
	a := requestAlert{Kind: RequestAvailable, Subject: subject, Title: subject.Title, MediaType: subject.MediaType, Parts: []availabilityPart{part}}
	return s.insertEvent(tx, c, fmt.Sprintf("%s%d:%d", group, now, part.RequestID), a, now+60)
}
