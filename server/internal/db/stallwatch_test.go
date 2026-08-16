package db

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStallTestWatchdog builds a watchdog against a real database with short
// timings, and captures what it would have logged.
func newStallTestWatchdog(t *testing.T) (*StallWatchdog, *[]string) {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "stall.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	var lines []string
	w := NewStallWatchdog(database)
	w.probeTimeout = 50 * time.Millisecond
	w.reminderEvery = time.Minute
	w.logf = func(format string, args ...interface{}) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	w.dump = func() string { return "goroutine 1 [running]:\nfake.Holder(...)" }
	return w, &lines
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

// TestStallWatchdogNamesAWedgedPool drives the real failure: something takes
// the single connection and does not give it back. Before this existed the
// server went silent here — no crash, no error, no further log line — and an
// operator saw only an app that had stopped working and a container claiming to
// be healthy.
func TestStallWatchdogNamesAWedgedPool(t *testing.T) {
	w, lines := newStallTestWatchdog(t)
	ctx := context.Background()
	start := time.Now()

	// Healthy: the probe gets the connection and nothing is said. A watchdog
	// that chatters while things are fine is one nobody reads.
	w.check(ctx, start)
	if len(*lines) != 0 {
		t.Fatalf("healthy probe logged %v, want silence", *lines)
	}

	// Take the only connection and hold it, exactly as a caller blocked while
	// holding an open cursor does.
	tx, err := w.db.Begin()
	if err != nil {
		t.Fatalf("begin holding transaction: %v", err)
	}
	defer tx.Rollback()

	// One failure is not a stall — a single slow moment must not cry wolf.
	w.check(ctx, start.Add(10*time.Second))
	if len(*lines) != 0 {
		t.Fatalf("a single failed probe logged %v, want it to wait for confirmation", *lines)
	}

	// The second consecutive failure is.
	w.check(ctx, start.Add(20*time.Second))
	if len(*lines) != 1 {
		t.Fatalf("logged %d lines, want exactly one stall report: %v", len(*lines), *lines)
	}
	report := (*lines)[0]
	if !strings.Contains(report, "STALLED") {
		t.Fatalf("stall report = %q, want it to name the condition", report)
	}
	// The pool counters that corroborate it: the connection is held.
	if !strings.Contains(report, "in_use=1") || !strings.Contains(report, "max_open=1") {
		t.Fatalf("stall report = %q, want the pool evidence", report)
	}
	// And the holder. Without this the log only repeats what the operator
	// already knows from the app being down.
	if !strings.Contains(report, "fake.Holder") {
		t.Fatalf("stall report = %q, want the goroutine dump naming the holder", report)
	}

	// An ongoing stall is not re-reported every probe...
	w.check(ctx, start.Add(30*time.Second))
	if len(*lines) != 1 {
		t.Fatalf("logged %v, want an ongoing stall to stay quiet between reminders", *lines)
	}
	// ...but does not go silent for the whole outage either: a reader landing
	// in the log later must see it was still stuck, not just that it once was.
	w.check(ctx, start.Add(2*time.Minute))
	if len(*lines) != 2 || !strings.Contains((*lines)[1], "still stalled") {
		t.Fatalf("logged %v, want a reminder while stalled", *lines)
	}

	// Releasing the connection clears it, with the duration — the only trace
	// that the outage happened at all once the server is working again.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("release holding transaction: %v", err)
	}
	w.check(ctx, start.Add(3*time.Minute))
	if len(*lines) != 3 {
		t.Fatalf("logged %v, want a recovery line", *lines)
	}
	// Measured from when the stall was declared (start+20s), not from the first
	// failed probe: the reported duration is the window the server was known to
	// be wedged, which is what an operator matches against their outage.
	if !strings.Contains((*lines)[2], "recovered after 2m40s") {
		t.Fatalf("recovery line = %q, want the stall duration since it was declared", (*lines)[2])
	}

	// Back to healthy and silent.
	w.check(ctx, start.Add(4*time.Minute))
	if len(*lines) != 3 {
		t.Fatalf("logged %v, want silence once recovered", *lines)
	}
}

// TestStallWatchdogIgnoresShutdown keeps the watchdog from writing an alarming
// last line on the way out: a cancelled context is the server stopping, not the
// pool wedging.
func TestStallWatchdogIgnoresShutdown(t *testing.T) {
	w, lines := newStallTestWatchdog(t)
	tx, err := w.db.Begin()
	if err != nil {
		t.Fatalf("begin holding transaction: %v", err)
	}
	defer tx.Rollback()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < w.strikes+1; i++ {
		w.check(ctx, time.Now())
	}
	if len(*lines) != 0 {
		t.Fatalf("logged %v during shutdown, want silence", *lines)
	}
}

// TestStallWatchdogLeavesRealQueriesAlone is the promise that made this the
// safe option over query timeouts: only the probe carries a deadline, so a slow
// query still finishes instead of acquiring a new way to fail.
func TestStallWatchdogLeavesRealQueriesAlone(t *testing.T) {
	w, lines := newStallTestWatchdog(t)
	ctx := context.Background()

	// Probe while the connection is held, then release and run an ordinary
	// query with no context of its own — it must succeed untouched.
	tx, err := w.db.Begin()
	if err != nil {
		t.Fatalf("begin holding transaction: %v", err)
	}
	w.check(ctx, time.Now())
	w.check(ctx, time.Now())
	if err := tx.Rollback(); err != nil {
		t.Fatalf("release holding transaction: %v", err)
	}

	var n int
	if err := w.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("ordinary query after a stall report: %v, want it unaffected", err)
	}
	if joined(*lines) == "" {
		t.Fatal("expected the stall to have been reported")
	}
}
