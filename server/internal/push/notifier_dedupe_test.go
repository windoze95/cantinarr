package push

import (
	"database/sql"
	"fmt"
	"testing"
)

// newDedupeNotifier builds a notifier backed by the real schema. The claim
// ledger is a table now, so a bare &Notifier{} would fail open on every call
// and assert nothing.
func newDedupeNotifier(t *testing.T) (*Notifier, *sql.DB) {
	t.Helper()
	database, err := dbOpen(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return NewNotifier(database, nil, nil), database
}

// lapseClaims backdates every stored claim past the suppression window, so a
// test can assert a second send without waiting out contentAlertWindow.
//
// Any test that calls the same NotifyNew*/claimContentAlert twice against one
// database needs this: claims are durable now, so the second call is otherwise
// suppressed exactly as it would be in production.
func lapseClaims(t *testing.T, database *sql.DB) {
	t.Helper()
	mustExec(t, database, `UPDATE content_alert_claims SET claimed_at = datetime('now', '-20 minutes')`)
}

// TestClaimContentAlertDedupes pins the new-content dedupe: the queue-poll
// witness and the arr webhook receiver can both report the same import (and a
// season pack arrives as one webhook per file), but only the first claim in
// the window may send. Distinct content is never suppressed.
func TestClaimContentAlertDedupes(t *testing.T) {
	n, _ := newDedupeNotifier(t)

	if !n.claimContentAlert(CategoryNewEpisode, "tv", "700", "Gappy Show") {
		t.Fatal("first claim must send")
	}
	if n.claimContentAlert(CategoryNewEpisode, "tv", "700", "Gappy Show") {
		t.Error("duplicate claim within the window must be suppressed")
	}
	if !n.claimContentAlert(CategoryNewMovie, "movie", "700", "Gappy Show") {
		t.Error("a different category is different content")
	}
	if !n.claimContentAlert(CategoryNewEpisode, "tv", "0", "Other Show") {
		t.Error("tmdb 0 with a different title is different content")
	}
	if n.claimContentAlert(CategoryNewEpisode, "tv", "0", "Other Show") {
		t.Error("tmdb 0 duplicates of the same title must still dedupe")
	}

	// Books key on foreignBookId plus format: a title's ebook and audiobook
	// are separate records that may import minutes apart, so one format's
	// alert must never swallow the other's.
	if !n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Error("first book claim must send")
	}
	if n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Error("duplicate book format claim must be suppressed")
	}
	if !n.claimContentAlert(CategoryNewBook, "book", "29749107|audiobook", "Ahsoka") {
		t.Error("the sibling format of the same book is different content")
	}
}

// TestClaimContentAlertSurvivesNotifierRestart is why the ledger is durable.
// The queue poller resumes its departure diff across a restart; without a
// persisted claim, a completion the webhook already announced before the
// restart would be announced a second time after it.
func TestClaimContentAlertSurvivesNotifierRestart(t *testing.T) {
	n, database := newDedupeNotifier(t)

	if !n.claimContentAlert(CategoryNewMovie, "movie", "603", "The Matrix") {
		t.Fatal("first claim must send")
	}

	// A new Notifier over the same database is the restart: no in-process
	// state carries over, only the ledger.
	restarted := NewNotifier(database, nil, nil)
	if restarted.claimContentAlert(CategoryNewMovie, "movie", "603", "The Matrix") {
		t.Error("a claim from before the restart must still suppress after it")
	}
}

// TestClaimContentAlertReclaimableAfterWindow pins that durable is not
// permanent. Sends are fire-and-forget, so a claim whose delivery failed must
// become re-claimable once the window lapses — otherwise one transient gateway
// error would silence that title forever.
func TestClaimContentAlertReclaimableAfterWindow(t *testing.T) {
	n, database := newDedupeNotifier(t)

	if !n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Fatal("first claim must send")
	}
	if n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Fatal("duplicate within the window must be suppressed")
	}

	lapseClaims(t, database)

	if !n.claimContentAlert(CategoryNewBook, "book", "29749107|ebook", "Ahsoka") {
		t.Error("a lapsed claim must be re-claimable")
	}
}

// TestClaimContentAlertWithoutLedgerFailsOpen pins the fail-open rule: a
// notifier with no database must still send. A missing ledger may cost a
// duplicate; it must never silence the content-alert surface.
func TestClaimContentAlertWithoutLedgerFailsOpen(t *testing.T) {
	n := NewNotifier(nil, nil, nil)

	if !n.claimContentAlert(CategoryNewMovie, "movie", "603", "The Matrix") {
		t.Fatal("first claim must send without a ledger")
	}
	if !n.claimContentAlert(CategoryNewMovie, "movie", "603", "The Matrix") {
		t.Error("a duplicate must still send when there is no ledger to consult")
	}
}

// TestClaimContentAlertBreaksStorms pins the burst cap: a mass job announcing
// one distinct title after another — a bulk manual import's webhooks, several
// instances resuming at once — delivers the first contentAlertStormCap alerts
// in the window and suppresses the rest, so a household is never paged once
// per title of a hundred-title job.
func TestClaimContentAlertBreaksStorms(t *testing.T) {
	n, database := newDedupeNotifier(t)

	for i := 1; i <= contentAlertStormCap; i++ {
		if !n.claimContentAlert(CategoryNewMovie, "movie", fmt.Sprint(i), fmt.Sprintf("Movie %d", i)) {
			t.Fatalf("alert %d of %d was suppressed below the cap", i, contentAlertStormCap)
		}
	}
	if n.claimContentAlert(CategoryNewMovie, "movie", "999", "Movie 999") {
		t.Fatal("the alert past the cap must be suppressed")
	}
	// The suppressed title's claim is still recorded, so the burst cannot be
	// re-tried item by item through the other witness of the same import.
	var claimed int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM content_alert_claims WHERE alert_key LIKE '%999%'`).Scan(&claimed); err != nil {
		t.Fatalf("count suppressed claim: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("suppressed alert left %d claim rows, want 1", claimed)
	}

	// Once the window slides past the burst, fresh content alerts again.
	lapseClaims(t, database)
	if !n.claimContentAlert(CategoryNewMovie, "movie", "1000", "Movie 1000") {
		t.Error("an alert after the window lapsed must send")
	}
}

// TestSilentClaimAbsorbsPollerAndSpendsNoBudget pins the upgrade-suppression
// mechanics: a proven upgrade claims the broadcast key without sending, so the
// queue poller's re-witness of the same import finds the claim and stays
// quiet — and none of those silent claims counts toward the broadcast storm
// cap, so a mass upgrade sweep cannot starve a genuine new-content alert.
func TestSilentClaimAbsorbsPollerAndSpendsNoBudget(t *testing.T) {
	n, _ := newDedupeNotifier(t)

	// A whole sweep of silent claims — deliberately more than the cap.
	for i := 1; i <= contentAlertStormCap+5; i++ {
		n.claimContentAlertSilently(CategoryNewMovie, "movie", fmt.Sprint(i), fmt.Sprintf("Movie %d", i))
	}
	// The poller re-witnessing one of those imports is absorbed.
	if n.claimContentAlert(CategoryNewMovie, "movie", "1", "Movie 1") {
		t.Error("a silently claimed key must suppress the poller's broadcast attempt")
	}
	// A genuine new movie still alerts: the silent sweep spent no budget.
	if !n.claimContentAlert(CategoryNewMovie, "movie", "9000", "Actually New") {
		t.Error("a fresh broadcast was suppressed by silent upgrade claims spending the storm budget")
	}
}

// TestUpgradeStormBudgetIsIndependentOfBroadcast pins that delivered upgrade
// alerts spend their own 12-per-window cap: the 13th upgrade in a window is
// suppressed (claim recorded), while broadcast alerts are untouched in both
// directions.
func TestUpgradeStormBudgetIsIndependentOfBroadcast(t *testing.T) {
	n, database := newDedupeNotifier(t)

	for i := 1; i <= contentAlertStormCap; i++ {
		if !n.claimContentAlertScoped(CategoryContentUpgraded, "movie", fmt.Sprint(i), fmt.Sprintf("Movie %d", i), stormScopeUpgrade) {
			t.Fatalf("upgrade alert %d of %d was suppressed below the cap", i, contentAlertStormCap)
		}
	}
	if n.claimContentAlertScoped(CategoryContentUpgraded, "movie", "999", "Movie 999", stormScopeUpgrade) {
		t.Fatal("the upgrade alert past the cap must be suppressed")
	}
	// The suppressed upgrade's claim is still recorded so the other witness
	// cannot re-try it.
	var claimed int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM content_alert_claims WHERE alert_key LIKE 'content_upgraded%999%'`).Scan(&claimed); err != nil {
		t.Fatalf("count suppressed claim: %v", err)
	}
	if claimed != 1 {
		t.Fatalf("suppressed upgrade left %d claim rows, want 1", claimed)
	}
	// A full upgrade window leaves the broadcast budget untouched.
	if !n.claimContentAlert(CategoryNewMovie, "movie", "9000", "Actually New") {
		t.Error("a broadcast alert was suppressed by the upgrade window's spending")
	}
}
