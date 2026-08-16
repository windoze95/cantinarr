package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"runtime"
	"time"
)

// The pool holds exactly one connection (SQLite is single-writer), so every
// query in the process takes its turn through a single door. Code that holds
// that connection while waiting on something which also needs it never gives it
// back, and nothing in this codebase sets a query timeout — so the wait is
// forever. The process does not crash, does not error, and writes no further
// log line, because a goroutine blocked on a connection is not doing anything
// worth logging. Users see an app that has stopped working; the operator sees a
// container that insists it is healthy. A restart clears it and teaches nobody
// anything. That is exactly how the PR #352 deadlock stayed unexplained.
//
// This watchdog does not fix that. It ends the silence: it notices from the
// outside that nothing can get through, and prints who is holding the door.
//
// It must not run through the database. Every other health signal this server
// has — system issues, admin pushes — is written to or read from the very
// connection that is stuck, so during this failure they are the one thing that
// cannot report it. The log is the only channel left, which is why this reports
// there and nowhere else.
const (
	// stallProbeInterval is how often the door is tried. Cheap: a healthy probe
	// holds the connection for microseconds.
	stallProbeInterval = 10 * time.Second
	// stallProbeTimeout is how long a probe waits for the connection before
	// giving up. Only the probe carries this deadline; real queries are left
	// exactly as they were, so this can neither fail a slow query nor change
	// any behavior a user can see.
	stallProbeTimeout = 5 * time.Second
	// stallStrikes is how many consecutive failed probes declare a stall. One
	// is not enough: a single slow moment under load is not a wedged pool, and
	// a watchdog that cries wolf gets ignored precisely when it is right.
	stallStrikes = 2
	// stallReminderEvery re-states an ongoing stall, so an operator reading the
	// log later sees how long it went on instead of one old line.
	stallReminderEvery = 5 * time.Minute
	// stallDumpLimit caps the goroutine dump. Enough to name the holder without
	// filling a log volume during an outage.
	stallDumpLimit = 64 << 10
)

// StallWatchdog reports, to the log only, that the single database connection
// has been unavailable long enough that the server is effectively wedged.
type StallWatchdog struct {
	db            *sql.DB
	interval      time.Duration
	probeTimeout  time.Duration
	strikes       int
	reminderEvery time.Duration

	// logf and dump are injected so the behavior can be tested without timers
	// or a real stack dump.
	logf func(format string, args ...interface{})
	dump func() string

	consecutiveFailures int
	stalledSince        time.Time
	lastReminder        time.Time
}

// NewStallWatchdog builds a watchdog with production settings.
func NewStallWatchdog(database *sql.DB) *StallWatchdog {
	return &StallWatchdog{
		db:            database,
		interval:      stallProbeInterval,
		probeTimeout:  stallProbeTimeout,
		strikes:       stallStrikes,
		reminderEvery: stallReminderEvery,
		logf:          log.Printf,
		dump:          goroutineDump,
	}
}

// Start runs the watchdog until ctx is cancelled.
func (w *StallWatchdog) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				w.check(ctx, now)
			}
		}
	}()
}

// check performs one probe and reports any transition. Exported behavior lives
// here rather than in the ticker loop so tests can drive it directly.
func (w *StallWatchdog) check(ctx context.Context, now time.Time) {
	probeCtx, cancel := context.WithTimeout(ctx, w.probeTimeout)
	defer cancel()

	// Acquiring the connection is the whole test; SELECT 1 is just the cheapest
	// way to ask for it. database/sql applies the context while WAITING for a
	// connection, so a held pool fails here without ever reaching SQLite.
	var one int
	err := w.db.QueryRowContext(probeCtx, "SELECT 1").Scan(&one)
	if err == nil {
		w.recovered(now)
		return
	}
	// A cancelled parent means the server is shutting down, not stalling.
	if ctx.Err() != nil {
		return
	}
	// Any other error (a real query failure) is somebody else's problem: it is
	// visible to whoever ran it. Only "could not get the connection at all"
	// means the door is stuck.
	if !errors.Is(err, context.DeadlineExceeded) {
		return
	}

	w.consecutiveFailures++
	if w.consecutiveFailures < w.strikes {
		return
	}
	if w.stalledSince.IsZero() {
		w.stalledSince = now
		w.lastReminder = now
		w.logf(
			"db: STALLED — no query could obtain the single connection for %s. "+
				"The server is running but effectively wedged; requests will hang until it clears. %s\n%s",
			w.probeTimeout*time.Duration(w.strikes), w.stats(), w.dump(),
		)
		return
	}
	if now.Sub(w.lastReminder) >= w.reminderEvery {
		w.lastReminder = now
		w.logf("db: still stalled after %s. %s", now.Sub(w.stalledSince).Round(time.Second), w.stats())
	}
}

// recovered clears a stall, reporting how long it lasted. A stall that ends by
// itself still has to be named: it is the only trace that the outage happened
// at all, and the next one is easier to place with a duration to match against.
func (w *StallWatchdog) recovered(now time.Time) {
	w.consecutiveFailures = 0
	if w.stalledSince.IsZero() {
		return
	}
	w.logf("db: recovered after %s stalled.", now.Sub(w.stalledSince).Round(time.Second))
	w.stalledSince = time.Time{}
	w.lastReminder = time.Time{}
}

// stats renders the pool counters that corroborate the diagnosis. in_use=1 with
// idle=0 is the connection being held; the cumulative wait counters show how
// much of the process has already queued behind it. (database/sql exposes no
// "currently waiting" gauge, so those two are totals since start — read them as
// a rate between reminders, not as a queue depth.)
func (w *StallWatchdog) stats() string {
	s := w.db.Stats()
	return fmt.Sprintf(
		"pool: in_use=%d idle=%d open=%d max_open=%d waits_since_start=%d wait_time_since_start=%s",
		s.InUse, s.Idle, s.OpenConnections, s.MaxOpenConnections,
		s.WaitCount, s.WaitDuration.Round(time.Millisecond),
	)
}

// goroutineDump names the holder. Without it the log says only that the server
// is stuck, which an operator already knows from the app being down; with it
// the stuck call site is in the log, which is the difference between an
// afternoon of bisecting and a fix. Stopping the world briefly to collect it is
// free here — nothing was making progress anyway.
func goroutineDump() string {
	buf := make([]byte, stallDumpLimit)
	n := runtime.Stack(buf, true)
	if n == stallDumpLimit {
		return string(buf[:n]) + "\n… goroutine dump truncated"
	}
	return string(buf[:n])
}
