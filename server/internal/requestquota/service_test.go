package requestquota

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/db"
)

func intp(n int) *int { return &n }

func quotaFixture(t *testing.T) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quotas.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err = database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES (1,'requester','','user'),(2,'administrator','','admin'),(3,'child','','user'); INSERT INTO user_content_policies(user_id,max_movie_rating,max_tv_rating) VALUES(3,'PG','TV-PG')`); err != nil {
		t.Fatal(err)
	}
	s := New(database)
	s.Now = func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 123, time.UTC) }
	return s, path
}

func accept(t *testing.T, s *Service, userID int64, key Key, unit string) (int64, error) {
	t.Helper()
	tx, err := Begin(s.DB)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO request_log(user_id,tmdb_id,media_type,title,status) VALUES (?,1,?,'Test','pending')`, userID, key.MediaType)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`INSERT INTO request_dispatch(request_id,format,state) VALUES (?,?,'approval')`, id, key.BookFormat); err != nil {
		return 0, err
	}
	if _, err = s.Accept(tx, userID, []Unit{{Key: key, RequestID: id, InstanceID: "library", UnitKey: unit}}); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func view(t *testing.T, s *Service, userID int64) *View {
	t.Helper()
	v, err := s.Read(s.DB, userID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRollingWindowsInheritanceAndPolicyChanges(t *testing.T) {
	s, _ := quotaFixture(t)
	movie := Keys[0]
	if _, err := accept(t, s, 1, movie, "first"); err != nil {
		t.Fatal(err)
	}
	v := view(t, s, 1)
	if v.Allowances[0].Used != 1 || v.Allowances[0].Remaining != nil {
		t.Fatalf("unlimited activity not recorded: %+v", v)
	}
	if err := s.Save(2, 0, []Rule{{Key: movie, Count: intp(1), WindowDays: 1}}); err != nil {
		t.Fatal(err)
	}
	v = view(t, s, 1)
	if *v.Allowances[0].Remaining != 0 || v.Allowances[0].Source != "default" {
		t.Fatal(v)
	}
	charged := s.Now()
	s.Now = func() time.Time { return charged.Add(24*time.Hour - time.Nanosecond) }
	if _, err := accept(t, s, 1, movie, "second"); err == nil {
		t.Fatal("allowed before the exact rolling boundary")
	}
	s.Now = func() time.Time { return charged.Add(24 * time.Hour) }
	if got := view(t, s, 1).Allowances[0]; got.Used != 0 || got.NextReplenishesAt != nil {
		t.Fatalf("boundary: %+v", got)
	}
	if _, err := accept(t, s, 1, movie, "second"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(2, 0, []Rule{{Key: movie, Count: intp(1), WindowDays: 30}}); err != nil {
		t.Fatal(err)
	}
	if got := view(t, s, 1).Allowances[0]; got.Used != 2 || *got.Remaining != 0 {
		t.Fatal(got)
	}
	// Complete-rule override, unlimited exception, then inheritance.
	if err := s.Save(2, 1, []Rule{{Key: movie, Count: intp(4), WindowDays: 7}}); err != nil {
		t.Fatal(err)
	}
	if got := view(t, s, 1).Allowances[0]; got.Source != "override" || *got.Remaining != 2 || got.WindowDays != 7 {
		t.Fatal(got)
	}
	if err := s.Save(2, 1, []Rule{{Key: movie, WindowDays: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := view(t, s, 1).Allowances[0]; got.Count != nil || got.Remaining != nil || got.Source != "override" {
		t.Fatal(got)
	}
	if err := s.Save(2, 1, []Rule{{Key: movie, Inherit: true}}); err != nil {
		t.Fatal(err)
	}
	if got := view(t, s, 1).Allowances[0]; got.Source != "default" || got.WindowDays != 30 {
		t.Fatal(got)
	}
}

func TestAdminExemptionKidsAndFailures(t *testing.T) {
	s, _ := quotaFixture(t)
	for _, k := range Keys {
		if err := s.Save(2, 0, []Rule{{Key: k, Count: intp(0), WindowDays: 7}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, uid := range []int64{1, 3} {
		for _, k := range Keys {
			if _, err := accept(t, s, uid, k, "new"); err == nil {
				t.Fatalf("user %d bypassed %v", uid, k)
			}
		}
	}
	for _, k := range Keys {
		if _, err := accept(t, s, 2, k, "new"); err != nil {
			t.Fatal(err)
		}
	}
	if !view(t, s, 2).Exempt {
		t.Fatal("admin not exempt")
	}
	if err := s.Save(1, 1, []Rule{{Key: Keys[0], WindowDays: 7}}); err == nil {
		t.Fatal("requester changed allowance")
	}
	if _, err := s.Reset(1, 1, []Key{Keys[0]}); err == nil {
		t.Fatal("requester reset allowance")
	}
	if _, err := s.Read(s.DB, 999); err == nil {
		t.Fatal("missing user became unlimited")
	}
	if _, err := s.DB.Exec(`ALTER TABLE request_quota_charges RENAME TO unavailable_charges`); err != nil {
		t.Fatal(err)
	}
	if _, err := accept(t, s, 1, Keys[0], "blind"); err == nil {
		t.Fatal("failed accounting became unlimited")
	}
}

func TestTwoIndependentConnectionsSpendFinalUnitOnce(t *testing.T) {
	s, path := quotaFixture(t)
	other, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	second := New(other)
	second.Now = s.Now
	if err = s.Save(2, 0, []Rule{{Key: Keys[0], Count: intp(1), WindowDays: 7}}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, svc := range []*Service{s, second} {
		wg.Add(1)
		go func(i int, svc *Service) {
			defer wg.Done()
			<-start
			_, err := accept(t, svc, 1, Keys[0], string(rune('a'+i)))
			results <- err
		}(i, svc)
	}
	close(start)
	wg.Wait()
	close(results)
	success, refused := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			var exceeded *Exceeded
			if !errors.As(err, &exceeded) {
				t.Fatal(err)
			}
			refused++
		}
	}
	if success != 1 || refused != 1 {
		t.Fatalf("accepted=%d refused=%d", success, refused)
	}
	for _, table := range []string{"request_log", "request_dispatch", "request_quota_charges"} {
		var n int
		if err = s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s=%d %v", table, n, err)
		}
	}
}

func TestPreviewEarliestFitAndAtomicFormats(t *testing.T) {
	s, _ := quotaFixture(t)
	for _, k := range []Key{Keys[2], Keys[3]} {
		if err := s.Save(2, 0, []Rule{{Key: k, Count: intp(2), WindowDays: 7}}); err != nil {
			t.Fatal(err)
		}
	}
	first := s.Now()
	if _, err := accept(t, s, 1, Keys[3], "one"); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return first.Add(time.Hour) }
	if _, err := accept(t, s, 1, Keys[3], "two"); err != nil {
		t.Fatal(err)
	}
	units := []Unit{{Key: Keys[2], InstanceID: "library", UnitKey: "both"}, {Key: Keys[3], InstanceID: "library", UnitKey: "both"}}
	p, _, err := s.Price(s.DB, 1, units)
	if err != nil {
		t.Fatal(err)
	}
	if p.Fits || p.ReduceSelection || p.EarliestFitsAt == nil || !p.EarliestFitsAt.Equal(first.Add(7*24*time.Hour)) {
		t.Fatalf("preview=%+v", p)
	}
	tx, err := Begin(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Accept(tx, 1, units); err == nil {
		t.Fatal("partially accepted both formats")
	}
	tx.Rollback()
	if view(t, s, 1).Allowances[2].Used != 0 {
		t.Fatal("ebook charged despite audiobook refusal")
	}
	units = append(units, Unit{Key: Keys[3], InstanceID: "library", UnitKey: "third"}, Unit{Key: Keys[3], InstanceID: "library", UnitKey: "fourth"})
	p, _, err = s.Price(s.DB, 1, units)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReduceSelection || p.EarliestFitsAt != nil {
		t.Fatal(p)
	}
}

func TestChargeOwnershipRefundsAndAuditedReset(t *testing.T) {
	s, _ := quotaFixture(t)
	id, err := accept(t, s, 1, Keys[2], "book")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := Begin(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Accept(tx, 3, []Unit{{Key: Keys[2], RequestID: id, InstanceID: "library", UnitKey: "book"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE request_log SET user_id=3 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = s.Release(tx, id, 1, "ebook", "", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if view(t, s, 1).Allowances[2].Used != 0 || view(t, s, 3).Allowances[2].Used != 1 {
		t.Fatal("ownership or subscription transferred charges")
	}
	if _, err = s.DB.Exec(`UPDATE request_quota_items SET delivery_started_at=123 WHERE request_id=? AND user_id=3`, id); err != nil {
		t.Fatal(err)
	}
	tx, err = Begin(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Release(tx, id, 3, "ebook", "", "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if view(t, s, 3).Allowances[2].Used != 1 {
		t.Fatal("refunded delivery that could have begun")
	}
	if _, err = accept(t, s, 3, Keys[3], "audio"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reset(2, 3, []Key{Keys[2]}); err != nil {
		t.Fatal(err)
	}
	v := view(t, s, 3)
	if v.Allowances[2].Used != 0 || v.Allowances[3].Used != 1 {
		t.Fatal("reset affected unselected allowance")
	}
	var admin, user, n int
	var restored string
	if err = s.DB.QueryRow(`SELECT admin_id,user_id,restored_units FROM request_quota_resets`).Scan(&admin, &user, &restored); err != nil || admin != 2 || user != 3 || restored == "" {
		t.Fatalf("audit %d %d %s %v", admin, user, restored, err)
	}
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM request_log`).Scan(&n); err != nil || n != 2 {
		t.Fatal("reset erased history")
	}
	var refund sql.NullInt64
	if err = s.DB.QueryRow(`SELECT refunded_at FROM request_quota_charges WHERE user_id=3 AND book_format='ebook'`).Scan(&refund); err != nil || refund.Valid {
		t.Fatal("reset rewrote refund history")
	}
}

func TestReducedLimitReportsFirstUsableReplenishmentAndKeepsLibraryPool(t *testing.T) {
	s, _ := quotaFixture(t)
	start := s.Now()
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i) * time.Hour)
		s.Now = func() time.Time { return at }
		if _, err := accept(t, s, 1, Keys[0], fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Save(2, 0, []Rule{{Key: Keys[0], Count: intp(1), WindowDays: 1}}); err != nil {
		t.Fatal(err)
	}
	v := view(t, s, 1)
	a := v.Allowances[0]
	if a.NextReplenishesAt == nil || !a.NextReplenishesAt.Equal(start.Add(26*time.Hour)) || !v.NextChangeAt.Equal(start.Add(24*time.Hour)) {
		t.Fatalf("over-limit replenishment: %+v", a)
	}
	tx, err := Begin(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p, _, err := s.Price(tx, 1, []Unit{{Key: Keys[0], InstanceID: "another-library", UnitKey: "0"}})
	if err != nil || p.Fits || p.Allowances[0].RequestedUnits != 1 {
		t.Fatalf("library escaped shared pool: %+v %v", p, err)
	}
}
