package discordnotify

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Service) observeAvailability(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if s.source != nil {
			err := s.scanAvailability(ctx)
			s.errorMu.Lock()
			s.availabilityError = ""
			if err != nil && ctx.Err() == nil {
				s.availabilityError = "Some requested content could not be verified. Availability notifications will resume when the library can be read."
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
	// event. TV stays observed because requested seasons can gain new episodes.
	rows, err := s.db.Query(`SELECT r.id FROM request_log r WHERE r.status!='denied' AND r.instance_id IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM discord_availability a WHERE a.request_id=r.id AND a.epoch=? AND (
	 (r.media_type IN ('movie','music') AND json_array_length(a.seen)>0) OR
	 (r.media_type='book' AND json_array_length(a.seen)>=CASE WHEN r.book_format='both' OR COALESCE(r.book_format,'')='' THEN 2 ELSE 1 END))) ORDER BY r.id`, c.AvailabilityEpoch)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	var lastErr error
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return err
		}
		snapshot, e := s.source.DiscordAvailability(ctx, id)
		if e != nil {
			lastErr = e
			continue
		}
		if e = s.recordAvailability(c, id, snapshot); e != nil {
			lastErr = e
		}
	}
	return lastErr
}

func (s *Service) recordAvailability(observed configuration, id int64, snapshot Availability) error {
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
	var raw string
	err = tx.QueryRow(`SELECT epoch,seen FROM discord_availability WHERE request_id=?`, id).Scan(&epoch, &raw)
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
	novel := []Unit{}
	for _, unit := range snapshot.Units {
		if !keys[unit.Key] {
			seen = append(seen, unit)
			keys[unit.Key] = true
			if !initial || id > c.AvailabilityFloor {
				novel = append(novel, unit)
			}
		}
	}
	data, _ := json.Marshal(seen)
	if _, err = tx.Exec(`INSERT INTO discord_availability(request_id,epoch,seen) VALUES(?,?,?) ON CONFLICT(request_id) DO UPDATE SET epoch=excluded.epoch,seen=excluded.seen`, id, c.AvailabilityEpoch, string(data)); err != nil {
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
