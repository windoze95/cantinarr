package instance

import (
	"testing"
	"time"
)

// seedQueueWitness records a queue-membership row for an instance, as the
// WebSocket poller does on every successful poll.
func seedQueueWitness(t *testing.T, s *Store, instanceID string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO arr_queue_witness (instance_id, service_type, observed_at, media_ids)
		 VALUES (?, 'radarr', ?, '[42]')`, instanceID, time.Now().UTC(),
	); err != nil {
		t.Fatalf("seed queue witness: %v", err)
	}
}

func hasQueueWitness(t *testing.T, s *Store, instanceID string) bool {
	t.Helper()
	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM arr_queue_witness WHERE instance_id = ?`, instanceID,
	).Scan(&count); err != nil {
		t.Fatalf("count queue witness: %v", err)
	}
	return count > 0
}

// TestUpdateClearsQueueWitnessWhenURLChanges pins the freshness story for the
// stored witness. The recorded ids are arr-internal record ids, so repointing an
// instance at a different server makes them meaningless — and diffing against
// them could announce the wrong titles.
func TestUpdateClearsQueueWitnessWhenURLChanges(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "radarr", "Movies")

	seedQueueWitness(t, s, id)
	inst, err := s.Get(id)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}
	inst.Name = "Movies (renamed)"
	if err := s.Update(inst); err != nil {
		t.Fatalf("update instance: %v", err)
	}
	if !hasQueueWitness(t, s, id) {
		t.Error("an edit that left the URL alone dropped the witness")
	}

	inst.URL = "http://radarr-2:7878"
	if err := s.Update(inst); err != nil {
		t.Fatalf("update instance url: %v", err)
	}
	if hasQueueWitness(t, s, id) {
		t.Error("repointing the instance kept a witness recorded against the old server")
	}
}

// TestDeleteClearsQueueWitness keeps a removed instance from leaving a row
// behind.
func TestDeleteClearsQueueWitness(t *testing.T) {
	s := newTestStore(t)
	id := mkInstance(t, s, "chaptarr", "Books")
	seedQueueWitness(t, s, id)

	if err := s.Delete(id); err != nil {
		t.Fatalf("delete instance: %v", err)
	}
	if hasQueueWitness(t, s, id) {
		t.Error("deleted instance left its queue witness behind")
	}
}
