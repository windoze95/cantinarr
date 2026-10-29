package mediaavailability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
	"slices"
	"time"
)

func (s *Service) Status(id string) (ScanStatus, error) {
	out := ScanStatus{InstanceID: id, State: "disabled"}
	i, e := s.videoInstance(id)
	if e != nil {
		return out, e
	}
	if !scanEnabled(i) {
		return out, nil
	}
	out.State = "idle"
	var success, attempt int64
	var fp string
	e = s.db.QueryRow("SELECT last_success_at,last_attempt_at,item_count,error,config_fingerprint FROM media_availability_scans WHERE instance_id=?", id).Scan(&success, &attempt, &out.ItemCount, &out.Error, &fp)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if success > 0 && fp == fingerprint(i) {
		t := time.Unix(success, 0).UTC()
		out.LastSuccessAt = &t
	} else {
		out.ItemCount = 0
	}
	if attempt > 0 {
		t := time.Unix(attempt, 0).UTC()
		out.LastAttemptAt = &t
	}
	if out.Error != "" {
		out.State = "failed"
	}
	s.mu.Lock()
	if s.running[id] {
		out.State = "running"
	}
	s.mu.Unlock()
	return out, nil
}
func (s *Service) Scan(ctx context.Context, id string) error { return s.scan(ctx, id, false) }

// connectionStamp is read both before I/O and inside the publication
// transaction, so configuration edits cannot publish an obsolete generation.
func connectionStamp(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (string, error) {
	var kind, address, key, config string
	e := q.QueryRow("SELECT service_type,url,api_key,media_server_config FROM service_instances WHERE id=?", id).Scan(&kind, &address, &key, &config)
	b, _ := json.Marshal([]string{kind, address, key, config})
	return string(b), e
}
func (s *Service) scan(ctx context.Context, id string, incremental bool) (err error) {
	i, e := s.videoInstance(id)
	if e != nil {
		return e
	}
	if !scanEnabled(i) {
		return ErrScanDisabled
	}
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return ErrScanRunning
	}
	s.running[id] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.running, id); s.mu.Unlock(); s.notifyChanged() }()
	stamp, e := connectionStamp(s.db, id)
	if e != nil {
		return e
	}
	fp := fingerprint(i)
	currentInstance, e := s.videoInstance(id)
	if e != nil || fingerprint(currentInstance) != fp {
		return ErrUnavailable
	}
	start := s.now()
	_, e = s.db.Exec("INSERT INTO media_availability_scans(instance_id,last_attempt_at) VALUES(?,?) ON CONFLICT(instance_id) DO UPDATE SET last_attempt_at=excluded.last_attempt_at", id, start.Unix())
	if e != nil {
		return e
	}
	defer func() {
		if err != nil {
			_, _ = s.db.Exec("UPDATE media_availability_scans SET error='Library scan could not be completed. Retry after checking the connection.' WHERE instance_id=?", id)
			s.logger.Info("media availability scan incomplete", "instance_id", id, "error", err)
		}
	}()
	p, e := s.providers(i)
	if e != nil {
		return e
	}
	reader, ok := p.(mediaserver.InventoryProvider)
	if !ok {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	var items []mediaserver.InventoryItem
	var last int64
	var oldFP string
	if incremental {
		e = s.db.QueryRow("SELECT last_success_at,config_fingerprint FROM media_availability_scans WHERE instance_id=?", id).Scan(&last, &oldFP)
		if e != nil {
			return e
		}
		incremental = last > 0 && oldFP == fp
	}
	if recent, ok := p.(mediaserver.IncrementalInventory); incremental && ok {
		items, e = recent.InventorySince(ctx, i.MediaServerConfig.AvailabilityScan.LibraryIDs, time.Unix(last, 0).Add(-10*time.Minute))
	} else {
		incremental = false
		items, e = reader.Inventory(ctx, i.MediaServerConfig.AvailabilityScan.LibraryIDs, "", nil)
	}
	if e != nil {
		return e
	}
	if len(items) > 200000 {
		return mediaserver.ErrItemUnverified
	}
	seen := map[string]bool{}
	for _, item := range items {
		key := item.LibraryID + "\x00" + item.ItemID
		if item.ItemID == "" || !slices.Contains(i.MediaServerConfig.AvailabilityScan.LibraryIDs, item.LibraryID) || seen[key] || (item.MediaType != "movie" && item.MediaType != "tv") {
			return mediaserver.ErrItemUnverified
		}
		seen[key] = true
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	current, e := connectionStamp(tx, id)
	if e != nil || current != stamp {
		return ErrUnavailable
	}
	var generation int64
	if e = tx.QueryRow("SELECT generation FROM media_availability_scans WHERE instance_id=?", id).Scan(&generation); e != nil {
		return e
	}
	generation++
	if !incremental {
		if _, e = tx.Exec("DELETE FROM media_availability_items WHERE instance_id=?", id); e != nil {
			return e
		}
	}
	stmt, e := tx.Prepare(`INSERT INTO media_availability_items(instance_id,library_id,item_id,generation,media_type,tmdb_id,tvdb_id,payload) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(instance_id,library_id,item_id) DO UPDATE SET generation=excluded.generation,media_type=excluded.media_type,tmdb_id=excluded.tmdb_id,tvdb_id=excluded.tvdb_id,payload=excluded.payload`)
	if e != nil {
		return e
	}
	defer stmt.Close()
	for _, item := range items {
		raw, e := json.Marshal(item)
		if e != nil {
			return e
		}
		if _, e = stmt.Exec(id, item.LibraryID, item.ItemID, generation, item.MediaType, item.TMDBID, item.TVDBID, string(raw)); e != nil {
			return e
		}
	}
	full := int64(0)
	if !incremental {
		full = start.Unix()
	}
	_, e = tx.Exec(`UPDATE media_availability_scans SET generation=?,config_fingerprint=?,last_success_at=?,last_full_at=CASE WHEN ?>0 THEN ? ELSE last_full_at END,item_count=(SELECT COUNT(*) FROM media_availability_items WHERE instance_id=?),error='' WHERE instance_id=?`, generation, fp, start.Unix(), full, full, id, id)
	if e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.Invalidate(id)
	return nil
}
func (s *Service) Start(ctx context.Context) {
	go func() {
		s.Sweep(ctx)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Sweep(ctx)
			case <-s.wake:
				s.Sweep(ctx)
			}
		}
	}()
}
func (s *Service) Sweep(ctx context.Context) {
	instances, e := s.store.ListAll()
	if e != nil {
		return
	}
	for _, i := range instances {
		if ctx.Err() != nil {
			return
		}
		if !scanEnabled(&i) {
			continue
		}
		var last, full, attempt int64
		var fp string
		e := s.db.QueryRow("SELECT last_success_at,last_full_at,last_attempt_at,config_fingerprint FROM media_availability_scans WHERE instance_id=?", i.ID).Scan(&last, &full, &attempt, &fp)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			continue
		}
		now := s.now()
		changed := fp != fingerprint(&i)
		if !changed && now.Sub(time.Unix(attempt, 0)) < 5*time.Minute {
			continue
		}
		incremental := !changed && last > 0 && now.Sub(time.Unix(full, 0)) < 24*time.Hour
		if err := s.scan(ctx, i.ID, incremental); err != nil && !errors.Is(err, ErrScanRunning) {
			s.logger.Debug("media availability retry deferred", "instance_id", i.ID, "error", fmt.Sprint(err))
		}
	}
}
