package websocket

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// witnessRow is one instance's restored queue membership: the arr record ids
// that were in its download queue at the last successful poll, plus when that
// poll happened.
type witnessRow struct {
	serviceType string
	ids         []int
	observedAt  time.Time
}

// queueWitness persists the poller's queue-departure memory.
//
// The poller detects a completion by diffing the arr download queue against the
// previous poll's membership. That membership used to live only in RAM, so a
// restart re-seeded from empty and every completion that landed while the
// process was down was silently never witnessed — permanently, since the arrs
// fire their webhooks once, and books only have one on instances where the
// admin configured instant updates.
//
// Only set membership is ever read back. Nothing here is treated as truth about
// the arr: a departure is re-verified live against the arr before anything is
// announced.
type queueWitness struct {
	db *sql.DB
}

// newQueueWitness returns nil when there is no database, which leaves the
// poller on its previous in-memory-only behavior.
func newQueueWitness(database *sql.DB) *queueWitness {
	if database == nil {
		return nil
	}
	return &queueWitness{db: database}
}

// load returns the stored membership per instance id, dropping rows too old to
// act on. A dropped row is left on disk (the next successful poll overwrites
// it); the instance simply re-seeds as it would on a first boot.
func (w *queueWitness) load(now time.Time, staleAfter time.Duration) (map[string]witnessRow, error) {
	if w == nil {
		return nil, nil
	}
	rows, err := w.db.Query(`SELECT instance_id, service_type, observed_at, media_ids FROM arr_queue_witness`)
	if err != nil {
		return nil, fmt.Errorf("query queue witness: %w", err)
	}
	defer rows.Close()

	out := make(map[string]witnessRow)
	for rows.Next() {
		var (
			instanceID  string
			serviceType string
			observedAt  time.Time
			mediaIDs    string
		)
		if err := rows.Scan(&instanceID, &serviceType, &observedAt, &mediaIDs); err != nil {
			return nil, fmt.Errorf("scan queue witness: %w", err)
		}
		// A snapshot from long ago describes a queue that has since turned over;
		// diffing against it would announce content the user has already found.
		if now.Sub(observedAt) > staleAfter {
			continue
		}
		// Clock skew: a row stamped in the future would never age out.
		if observedAt.After(now.Add(time.Second)) {
			continue
		}
		var ids []int
		if err := json.Unmarshal([]byte(mediaIDs), &ids); err != nil {
			// A row we cannot parse is not a reason to lose every other
			// instance's witness; it re-seeds on the next poll.
			continue
		}
		out[instanceID] = witnessRow{serviceType: serviceType, ids: ids, observedAt: observedAt}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate queue witness: %w", err)
	}
	return out, nil
}

// save records this instance's current queue membership, replacing the previous
// row. One row per instance, so the table cannot grow past the instance count.
func (w *queueWitness) save(instanceID, serviceType string, ids []int, now time.Time) error {
	if w == nil {
		return nil
	}
	sort.Ints(ids)
	encoded, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encode queue witness: %w", err)
	}
	_, err = w.db.Exec(`
        INSERT INTO arr_queue_witness (instance_id, service_type, observed_at, media_ids)
        VALUES (?, ?, ?, ?)
        ON CONFLICT(instance_id) DO UPDATE SET
            service_type = excluded.service_type,
            observed_at  = excluded.observed_at,
            media_ids    = excluded.media_ids`,
		instanceID, serviceType, now.UTC(), string(encoded))
	if err != nil {
		return fmt.Errorf("save queue witness: %w", err)
	}
	return nil
}
