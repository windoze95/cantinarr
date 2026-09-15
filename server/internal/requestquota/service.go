// Package requestquota accounts for accepted request units. Callers resolve
// provider state before opening a write transaction, then save intent and its
// charges in that same transaction. Reads and errors never imply unlimited.
package requestquota

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

type Key struct {
	MediaType  string `json:"media_type"`
	BookFormat string `json:"book_format,omitempty"`
}

var Keys = []Key{{MediaType: "movie"}, {MediaType: "tv"}, {MediaType: "book", BookFormat: "ebook"}, {MediaType: "book", BookFormat: "audiobook"}, {MediaType: "music"}}

func (k Key) Valid() bool {
	for _, known := range Keys {
		if known == k {
			return true
		}
	}
	return false
}

func (k Key) Label() string {
	switch k.MediaType {
	case "movie":
		return "Movies"
	case "tv":
		return "TV seasons"
	case "music":
		return "Albums"
	case "book":
		if k.BookFormat == "ebook" {
			return "eBooks"
		}
		return "Audiobooks"
	}
	return "Requests"
}

type Rule struct {
	Key
	Count      *int `json:"count"` // null means unlimited; zero refuses new work
	WindowDays int  `json:"window_days"`
	Inherit    bool `json:"inherit,omitempty"`
}

func (r Rule) Validate(override bool) error {
	if !r.Key.Valid() || (r.Inherit && !override) {
		return errors.New("invalid request allowance")
	}
	if r.Inherit {
		return nil
	}
	if r.WindowDays != 1 && r.WindowDays != 7 && r.WindowDays != 30 {
		return errors.New("allowance window must be 1, 7, or 30 days")
	}
	if r.Count != nil && (*r.Count < 0 || *r.Count > 1000000) {
		return errors.New("allowance count must be between 0 and 1000000, or null for unlimited")
	}
	return nil
}

type Allowance struct {
	Rule
	Source             string     `json:"source"`
	Used               int        `json:"used"`
	Remaining          *int       `json:"remaining"`
	NextReplenishesAt  *time.Time `json:"next_replenishes_at,omitempty"`
	FullyReplenishesAt *time.Time `json:"fully_replenishes_at,omitempty"`
	RequestedUnits     int        `json:"requested_units,omitempty"`
	expires            []time.Time
}

type View struct {
	Exempt       bool        `json:"exempt"`
	AsOf         time.Time   `json:"as_of"`
	NextChangeAt *time.Time  `json:"next_change_at,omitempty"`
	Allowances   []Allowance `json:"allowances"`
}

type Preview struct {
	View
	Fits            bool       `json:"fits"`
	EarliestFitsAt  *time.Time `json:"earliest_fits_at,omitempty"`
	ReduceSelection bool       `json:"reduce_selection"`
	Seasons         []int      `json:"seasons,omitempty"`
}

type Exceeded struct {
	Code            string      `json:"code"`
	Message         string      `json:"error"`
	Allowances      []Allowance `json:"allowances"`
	EarliestFitsAt  *time.Time  `json:"earliest_fits_at,omitempty"`
	ReduceSelection bool        `json:"reduce_selection"`
}

func (e *Exceeded) Error() string { return e.Message }

type Service struct {
	DB  *sql.DB
	Now func() time.Time
}

func New(db *sql.DB) *Service { return &Service{DB: db, Now: time.Now} }

// Begin acquires SQLite's write lock BEFORE any policy, usage or intent read.
// Using the first write in a deferred transaction is equivalent to BEGIN
// IMMEDIATE, while retaining database/sql's rollback and connection ownership.
func Begin(db *sql.DB) (*sql.Tx, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE request_quota_lock SET id=id WHERE id=1`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func Role(q Queryer, userID int64) (string, error) {
	var role string
	err := q.QueryRow(`SELECT role FROM users WHERE id=?`, userID).Scan(&role)
	return role, err
}

func (s *Service) Read(q Queryer, userID int64) (*View, error) {
	role, err := Role(q, userID)
	if err != nil {
		return nil, err
	}
	now := s.Now().UTC()
	v := &View{Exempt: role == "admin", AsOf: now, Allowances: []Allowance{}}
	for _, key := range Keys {
		a := Allowance{Rule: Rule{Key: key, WindowDays: 7}, Source: "default"}
		err = q.QueryRow(`SELECT count,window_days FROM request_quota_defaults WHERE media_type=? AND book_format=?`, key.MediaType, key.BookFormat).Scan(&a.Count, &a.WindowDays)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		err = q.QueryRow(`SELECT count,window_days FROM user_request_quotas WHERE user_id=? AND media_type=? AND book_format=?`, userID, key.MediaType, key.BookFormat).Scan(&a.Count, &a.WindowDays)
		if err == nil {
			a.Source = "override"
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err = a.Rule.Validate(false); err != nil {
			return nil, err
		}
		window := time.Duration(a.WindowDays) * 24 * time.Hour
		rows, err := q.Query(`SELECT charged_at FROM request_quota_charges WHERE user_id=? AND media_type=? AND book_format=? AND charged_at>? AND refunded_at IS NULL AND reset_id IS NULL ORDER BY charged_at,id`, userID, key.MediaType, key.BookFormat, now.Add(-window).UnixNano())
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var charged int64
			if err = rows.Scan(&charged); err != nil {
				rows.Close()
				return nil, err
			}
			a.expires = append(a.expires, time.Unix(0, charged).UTC().Add(window))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		a.Used = len(a.expires)
		if a.Count != nil && !v.Exempt {
			remaining := max(0, *a.Count-a.Used)
			a.Remaining = &remaining
		}
		if len(a.expires) > 0 {
			if v.NextChangeAt == nil || a.expires[0].Before(*v.NextChangeAt) {
				v.NextChangeAt = &a.expires[0]
			}
			if a.Count != nil && *a.Count > 0 && !v.Exempt {
				a.NextReplenishesAt = &a.expires[max(0, a.Used-*a.Count)]
			}
			a.FullyReplenishesAt = &a.expires[len(a.expires)-1]
		}
		v.Allowances = append(v.Allowances, a)
	}
	return v, nil
}

// Unit identifies an acquisition, not a title search result or quality choice.
// RequestID binds the item to immutable charge ownership. Free is verified
// provider no-op or accepted legacy work, never a fallback on a failed read.
type Unit struct {
	Key
	RequestID  int64
	InstanceID string
	UnitKey    string
	Work       string // TV pilot and full season share a charge but have different work
	Free       bool
	chargeID   int64
	existing   bool
}

func (s *Service) Price(q Queryer, userID int64, units []Unit) (*Preview, []Unit, error) {
	v, err := s.Read(q, userID)
	if err != nil {
		return nil, nil, err
	}
	p := &Preview{View: *v, Fits: true}
	priced := []Unit{}
	seen := map[string]bool{}
	for _, u := range units {
		if !u.Key.Valid() || u.UnitKey == "" {
			return nil, nil, errors.New("invalid allowance unit")
		}
		identity := fmt.Sprintf("%d:%s:%s:%s:%s", u.RequestID, u.MediaType, u.BookFormat, u.InstanceID, u.UnitKey)
		if seen[identity] {
			continue
		}
		seen[identity] = true
		var prior sql.NullInt64
		var priorWork string
		err = q.QueryRow(`SELECT charge_id,work FROM request_quota_items WHERE request_id=? AND user_id=? AND media_type=? AND book_format=? AND unit_key=? AND released_at IS NULL`, u.RequestID, userID, u.MediaType, u.BookFormat, u.UnitKey).Scan(&prior, &priorWork)
		if err == nil {
			if priorWork != "pilot" || u.Work != "season" {
				u.chargeID, u.existing = prior.Int64, true
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
		for i := range p.Allowances {
			a := &p.Allowances[i]
			if a.Key != u.Key {
				continue
			}
			if !u.Free && !u.existing {
				// Pending identical work remains accepted after its charge ages
				// out. A pilot only covers an expansion in the current window.
				err = q.QueryRow(`SELECT c.id FROM request_quota_charges c WHERE c.user_id=? AND c.media_type=? AND c.book_format=? AND c.instance_id=? AND c.unit_key=? AND c.refunded_at IS NULL AND (c.charged_at>? OR EXISTS(SELECT 1 FROM request_quota_items i JOIN request_log r ON r.id=i.request_id WHERE i.charge_id=c.id AND i.released_at IS NULL AND r.status='pending' AND (i.work=? OR i.work='season'))) ORDER BY c.charged_at DESC LIMIT 1`, userID, u.MediaType, u.BookFormat, u.InstanceID, u.UnitKey, v.AsOf.Add(-time.Duration(a.WindowDays)*24*time.Hour).UnixNano(), u.Work).Scan(&u.chargeID)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, nil, err
				}
				if u.chargeID == 0 {
					a.RequestedUnits++
				}
			}
			break
		}
		priced = append(priced, u)
	}
	for _, a := range p.Allowances {
		if v.Exempt || a.Count == nil || a.RequestedUnits == 0 || a.Used+a.RequestedUnits <= *a.Count {
			continue
		}
		p.Fits = false
		if a.RequestedUnits > *a.Count {
			p.ReduceSelection = true
			continue
		}
		when := a.expires[a.Used+a.RequestedUnits-*a.Count-1]
		if p.EarliestFitsAt == nil || p.EarliestFitsAt.Before(when) {
			p.EarliestFitsAt = &when
		}
	}
	if p.ReduceSelection {
		p.EarliestFitsAt = nil
	}
	return p, priced, nil
}

func (p *Preview) Failure() *Exceeded {
	if p.Fits {
		return nil
	}
	e := &Exceeded{Code: "request_quota_exceeded", Message: "This selection exceeds your request allowance.", EarliestFitsAt: p.EarliestFitsAt, ReduceSelection: p.ReduceSelection, Allowances: []Allowance{}}
	messages := []string{}
	for _, a := range p.Allowances {
		if a.Count != nil && a.RequestedUnits > 0 && a.Used+a.RequestedUnits > *a.Count {
			e.Allowances = append(e.Allowances, a)
			messages = append(messages, fmt.Sprintf("%s: %d requested, %d remaining.", a.Label(), a.RequestedUnits, max(0, *a.Count-a.Used)))
		}
	}
	e.Message = strings.Join(messages, " ")
	if e.ReduceSelection {
		e.Message += " Reduce the selection to fit the configured limit."
	} else {
		e.Message += " Reduce the selection or wait for allowance to return."
	}
	return e
}

func (s *Service) Accept(tx *sql.Tx, userID int64, units []Unit) (*Preview, error) {
	p, priced, err := s.Price(tx, userID, units)
	if err != nil {
		return nil, err
	}
	if e := p.Failure(); e != nil {
		return p, e
	}
	for _, u := range priced {
		if u.existing {
			continue
		}
		if u.RequestID <= 0 {
			return nil, errors.New("allowance charge requires saved intent")
		}
		if !u.Free && u.chargeID == 0 {
			res, err := tx.Exec(`INSERT INTO request_quota_charges(user_id,media_type,book_format,instance_id,unit_key,charged_at) VALUES (?,?,?,?,?,?)`, userID, u.MediaType, u.BookFormat, u.InstanceID, u.UnitKey, p.AsOf.UnixNano())
			if err != nil {
				return nil, err
			}
			u.chargeID, err = res.LastInsertId()
			if err != nil {
				return nil, err
			}
		}
		var charge any
		if u.chargeID > 0 {
			charge = u.chargeID
		}
		// New subscribers inherit the delivery fact, never the charge owner.
		_, err = tx.Exec(`INSERT INTO request_quota_items(request_id,user_id,media_type,book_format,unit_key,work,charge_id,delivery_started_at) VALUES (?,?,?,?,?,?,?,COALESCE((SELECT delivery_started_at FROM request_dispatch WHERE request_id=? AND format=?),0)) ON CONFLICT(request_id,user_id,media_type,book_format,unit_key) DO UPDATE SET charge_id=excluded.charge_id,work=excluded.work,delivery_started_at=MAX(request_quota_items.delivery_started_at,excluded.delivery_started_at),released_at=NULL,release_reason=NULL`, u.RequestID, userID, u.MediaType, u.BookFormat, u.UnitKey, u.Work, charge, u.RequestID, u.BookFormat)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Release preserves the item and charge history. A charge is refunded only
// when no accepted work still uses it and no linked delivery could have begun.
func (s *Service) Release(tx *sql.Tx, requestID, userID int64, format, unitKey, reason string) error {
	now := s.Now().UTC().UnixNano()
	_, err := tx.Exec(`UPDATE request_quota_items SET released_at=?,release_reason=? WHERE request_id=? AND (?=0 OR user_id=?) AND (?='*' OR book_format=?) AND (?='' OR unit_key=?) AND released_at IS NULL`, now, reason, requestID, userID, userID, format, format, unitKey, unitKey)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE request_quota_charges SET refunded_at=?,refund_reason=? WHERE refunded_at IS NULL AND id IN (SELECT charge_id FROM request_quota_items WHERE request_id=?) AND NOT EXISTS(SELECT 1 FROM request_quota_items i WHERE i.charge_id=request_quota_charges.id AND (i.released_at IS NULL OR i.delivery_started_at>0))`, now, reason, requestID)
	return err
}

func (s *Service) Defaults(q Queryer) ([]Rule, error) {
	out := []Rule{}
	for _, k := range Keys {
		r := Rule{Key: k, WindowDays: 7}
		err := q.QueryRow(`SELECT count,window_days FROM request_quota_defaults WHERE media_type=? AND book_format=?`, k.MediaType, k.BookFormat).Scan(&r.Count, &r.WindowDays)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Service) Save(adminID, userID int64, rules []Rule) error {
	seen := map[Key]bool{}
	for _, r := range rules {
		if err := r.Validate(userID != 0); err != nil {
			return err
		}
		if seen[r.Key] {
			return errors.New("duplicate allowance rule")
		}
		seen[r.Key] = true
	}
	tx, err := Begin(s.DB)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	role, err := Role(tx, adminID)
	if err != nil {
		return err
	}
	if role != "admin" {
		return errors.New("only admins can change request allowances")
	}
	if userID != 0 {
		if _, err = Role(tx, userID); err != nil {
			return err
		}
	}
	for _, r := range rules {
		if userID == 0 {
			_, err = tx.Exec(`INSERT INTO request_quota_defaults(media_type,book_format,count,window_days) VALUES (?,?,?,?) ON CONFLICT(media_type,book_format) DO UPDATE SET count=excluded.count,window_days=excluded.window_days`, r.MediaType, r.BookFormat, r.Count, r.WindowDays)
		} else if r.Inherit {
			_, err = tx.Exec(`DELETE FROM user_request_quotas WHERE user_id=? AND media_type=? AND book_format=?`, userID, r.MediaType, r.BookFormat)
		} else {
			_, err = tx.Exec(`INSERT INTO user_request_quotas(user_id,media_type,book_format,count,window_days) VALUES (?,?,?,?,?) ON CONFLICT(user_id,media_type,book_format) DO UPDATE SET count=excluded.count,window_days=excluded.window_days`, userID, r.MediaType, r.BookFormat, r.Count, r.WindowDays)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) Reset(adminID, userID int64, keys []Key) (*View, error) {
	if len(keys) == 0 {
		return nil, errors.New("select at least one allowance to reset")
	}
	seen := map[Key]bool{}
	for _, k := range keys {
		if !k.Valid() || seen[k] {
			return nil, errors.New("invalid or repeated allowance")
		}
		seen[k] = true
	}
	tx, err := Begin(s.DB)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	role, err := Role(tx, adminID)
	if err != nil {
		return nil, err
	}
	if role != "admin" {
		return nil, errors.New("only admins can reset request allowances")
	}
	v, err := s.Read(tx, userID)
	if err != nil {
		return nil, err
	}
	restored := []Allowance{}
	for _, a := range v.Allowances {
		if seen[a.Key] {
			restored = append(restored, a)
		}
	}
	kraw, _ := json.Marshal(keys)
	raw, _ := json.Marshal(restored)
	res, err := tx.Exec(`INSERT INTO request_quota_resets(user_id,admin_id,allowances,restored_units,created_at) VALUES (?,?,?,?,?)`, userID, adminID, string(kraw), string(raw), v.AsOf.UnixNano())
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		_, err = tx.Exec(`UPDATE request_quota_charges SET reset_id=? WHERE user_id=? AND media_type=? AND book_format=? AND reset_id IS NULL AND refunded_at IS NULL`, id, userID, k.MediaType, k.BookFormat)
		if err != nil {
			return nil, err
		}
	}
	v, err = s.Read(tx, userID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return v, nil
}
