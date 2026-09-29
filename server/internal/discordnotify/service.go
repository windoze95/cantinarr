package discordnotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const settingsKey = "discord_notifications"

type configuration struct {
	Options
	IncludeAutoApproved bool   `json:"include_auto_approved"`
	Enabled             bool   `json:"enabled"`
	Webhook             string `json:"webhook_url"`
	Revision            int64  `json:"revision"`
	NotBefore           int64  `json:"not_before"`
	AvailabilityEpoch   int64  `json:"availability_epoch"`
	AvailabilityFloor   int64  `json:"availability_floor"`
}

type Settings struct {
	Options
	IncludeAutoApproved bool       `json:"include_auto_approved"`
	Enabled             bool       `json:"enabled"`
	HasWebhook          bool       `json:"has_webhook"`
	Recent              []Delivery `json:"recent"`
	Error               string     `json:"error,omitempty"`
}

// Service owns the encrypted destination and durable delivery queue. It has
// no dependency on native push or on the request package.
type Service struct {
	db                *sql.DB
	cipher            *secrets.Cipher
	client            *http.Client
	externalURL       func() string
	wake              chan struct{}
	deliveryMu        sync.Mutex // one sender; settings saves wait for an in-flight send
	errorMu           sync.Mutex
	enqueueError      string
	now               func() time.Time
	source            Source
	observeWake       chan struct{}
	observationMu     sync.Mutex
	availabilityReads map[int64]time.Time // successful full reads in this process, under observationMu
	availabilityError string
}

func NewService(db *sql.DB, cipher *secrets.Cipher, externalURL func() string) *Service {
	return &Service{db: db, cipher: cipher, client: newClient(), externalURL: externalURL, wake: make(chan struct{}, 1), observeWake: make(chan struct{}, 1), now: time.Now}
}

// SetSource is called before Start. All provider and content-policy reads stay
// owned by the request service, rather than a second Discord-specific resolver.
func (s *Service) SetSource(source Source) { s.source = source }

func (s *Service) WakeAvailability() {
	select {
	case s.observeWake <- struct{}{}:
	default:
	}
}

type reader interface{ QueryRow(string, ...any) *sql.Row }

func (s *Service) readConfig(db reader) (configuration, error) {
	var c configuration
	var stored string
	err := db.QueryRow("SELECT value FROM settings WHERE key=?", settingsKey).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		c.defaults()
		return c, nil
	}
	if err != nil || s.cipher == nil || !secrets.IsEncrypted(stored) {
		return c, errors.New("the Discord settings could not be read")
	}
	plain, err := s.cipher.Decrypt(stored)
	if err != nil || json.Unmarshal([]byte(plain), &c) != nil {
		return configuration{}, errors.New("the Discord settings could not be decrypted")
	}
	if c.Webhook != "" {
		c.Webhook, err = normalizeWebhook(c.Webhook)
		if err != nil {
			return configuration{}, err
		}
	}
	if c.Enabled && c.Webhook == "" {
		return configuration{}, errors.New("the Discord webhook is missing")
	}
	c.defaults()
	return c, nil
}

func (s *Service) writeConfig(tx *sql.Tx, c configuration) error {
	if s.cipher == nil {
		return errors.New("encryption is required to save the Discord webhook")
	}
	plain, _ := json.Marshal(c)
	stored, err := s.cipher.Encrypt(string(plain))
	if err != nil {
		return errors.New("the Discord settings could not be encrypted")
	}
	_, err = tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", settingsKey, stored)
	return err
}

func (s *Service) Get() (Settings, error) {
	c, err := s.readConfig(s.db)
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Options: c.Options, Enabled: c.Enabled, IncludeAutoApproved: c.IncludeAutoApproved, HasWebhook: c.Webhook != "", Recent: []Delivery{}}
	rows, err := s.db.Query(`SELECT COALESCE(request_id,0),status,detail,attempts,updated_at,next_attempt_at,payload FROM discord_notifications ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Delivery
		var payload string
		if err := rows.Scan(&d.RequestID, &d.Status, &d.Detail, &d.Attempts, &d.UpdatedAt, &d.NextAttemptAt, &payload); err != nil {
			return out, err
		}
		var a requestAlert
		if json.Unmarshal([]byte(payload), &a) == nil {
			d.Event = eventKind(a)
			d.IssueID = a.IssueID
		}
		out.Recent = append(out.Recent, d)
	}
	s.errorMu.Lock()
	out.Error = s.enqueueError
	if out.Error == "" {
		out.Error = s.availabilityError
	}
	s.errorMu.Unlock()
	return out, rows.Err()
}

// Save preserves the write-only URL when omitted/blank. Remove is explicit.
func (s *Service) Save(enabled bool, webhook string, remove bool, includeAutoApproved *bool) error {
	return s.SaveUpdate(Update{Enabled: enabled, Webhook: webhook, IncludeAutoApproved: includeAutoApproved}, remove)
}

func (s *Service) SaveUpdate(u Update, remove bool) error {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := s.readConfig(tx)
	if err != nil {
		return err
	}
	previous := c
	previous.Events = maps.Clone(c.Events)
	if err := c.apply(u); err != nil {
		return err
	}
	enabled, webhook := u.Enabled, u.Webhook
	if remove {
		c.Webhook = ""
		enabled = false
	} else if webhook != "" {
		c.Webhook, err = normalizeWebhook(webhook)
		if err != nil {
			return err
		}
	}
	if enabled && c.Webhook == "" {
		return errors.New("add a webhook before enabling Discord notifications")
	}
	c.Enabled = enabled
	if previous.Enabled != c.Enabled || previous.Webhook != c.Webhook || previous.ThreadID != c.ThreadID {
		c.Revision++
		if previous.Webhook != c.Webhook {
			c.NotBefore = 0
		}
		if _, err = tx.Exec(`UPDATE discord_notifications SET status='cancelled',detail='Cancelled because the Discord configuration changed.',next_attempt_at=0,updated_at=? WHERE status='pending'`, s.now().Unix()); err != nil {
			return err
		}
	}
	for _, kind := range EventKinds {
		if c.Events[kind] {
			continue
		}
		if _, err = tx.Exec(`UPDATE discord_notifications SET status='cancelled',detail='This event category was turned off.',next_attempt_at=0,updated_at=? WHERE status='pending' AND COALESCE(json_extract(payload,'$.kind'),CASE WHEN json_extract(payload,'$.requires_approval')=1 THEN 'request_pending' ELSE 'request_auto_approved' END)=?`, s.now().Unix(), kind); err != nil {
			return err
		}
	}
	if c.Enabled && c.Events[RequestAvailable] && (!previous.Enabled || !previous.Events[RequestAvailable] || previous.Webhook != c.Webhook || previous.ThreadID != c.ThreadID) {
		c.AvailabilityEpoch++
		if err = tx.QueryRow(`SELECT COALESCE(MAX(id),0) FROM request_log`).Scan(&c.AvailabilityFloor); err != nil {
			return err
		}
	}
	if err = s.writeConfig(tx, c); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.WakeAvailability()
	return nil
}

// RequestCreated is called only after a genuinely new request was saved.
// Enqueue failure is visible to admins, but cannot undo an accepted request.
func (s *Service) RequestCreated(id int64, approval bool) {
	defer s.WakeAvailability()
	if err := s.enqueue(id, approval); err != nil {
		s.errorMu.Lock()
		s.enqueueError = "A request alert could not be queued. Check the server's database and logs."
		s.errorMu.Unlock()
		slog.Error("Discord request alert could not be queued", "request_id", id)
	}
}

func (s *Service) enqueue(id int64, approval bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := s.readConfig(tx)
	if err != nil {
		return err
	}
	kind := RequestAutoApproved
	if approval {
		kind = RequestPending
	}
	if !c.Enabled || !c.Events[kind] {
		return nil
	}
	a, err := loadRequestAlert(tx, id)
	if err != nil {
		return err
	}
	a.Kind, a.RequiresApproval = kind, approval
	a.ActorID = a.UserID
	data, _ := json.Marshal(a)
	now := s.now().Unix()
	_, err = tx.Exec(`INSERT INTO discord_notifications(request_id,event_key,revision,payload,status,created_at,updated_at,next_attempt_at) VALUES(?,?,?,?,'pending',?,?,?) ON CONFLICT(event_key) DO NOTHING`, id, fmt.Sprintf("created:%d", id), c.Revision, string(data), now, now, max(now, c.NotBefore))
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Service) Start(ctx context.Context) {
	go s.observeAvailability(ctx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if err := s.deliverOne(ctx); err != nil {
				slog.Error("Discord delivery queue could not be processed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
		}
	}()
}

func (s *Service) deliverOne(ctx context.Context) error {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	now := s.now().Unix()
	// A claim lasts longer than the HTTP timeout. A crashed sender may have
	// posted, so its expired claim is unconfirmed, never eligible for replay.
	if _, err := s.db.Exec(`UPDATE discord_notifications SET status='unconfirmed',detail='Delivery was interrupted and could not be confirmed; it will not be sent again automatically.',updated_at=?,next_attempt_at=0 WHERE status='sending' AND updated_at<=?`, now, now-30); err != nil {
		return err
	}
	c, err := s.readConfig(s.db)
	if err != nil || !c.Enabled {
		return err
	}
	if _, err = s.db.Exec(`UPDATE discord_notifications SET status='failed',detail='The Discord delivery retry limit was reached.',updated_at=?,next_attempt_at=0 WHERE status='pending' AND (attempts>=5 OR created_at<=?)`, now, now-86400); err != nil {
		return err
	}
	if now < c.NotBefore {
		return nil
	}
	var id, created, requestID int64
	var attempts int
	var payload string
	// Claim atomically; UPDATE RETURNING prevents two workers sending a row.
	err = s.db.QueryRow(`UPDATE discord_notifications SET status='sending',attempts=attempts+1,updated_at=?,next_attempt_at=0 WHERE id=(SELECT id FROM discord_notifications WHERE status='pending' AND revision=? AND next_attempt_at<=? ORDER BY id LIMIT 1) AND status='pending' RETURNING id,payload,attempts,created_at,COALESCE(request_id,0)`, now, c.Revision, now).Scan(&id, &payload, &attempts, &created, &requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var alert requestAlert
	result := sendResult{Status: "failed", Detail: "The saved notification could not be read."}
	preflight := false
	if json.Unmarshal([]byte(payload), &alert) == nil {
		if alert.Subject.RequestID == 0 {
			alert.Subject.RequestID = requestID
		}
		if !c.Events[eventKind(alert)] {
			_, err := s.db.Exec(`UPDATE discord_notifications SET status='cancelled',detail='This event category was turned off.',updated_at=? WHERE id=?`, now, id)
			return err
		}
		external := ""
		if s.externalURL != nil {
			external = s.externalURL()
		}
		payload, prepareErr := s.prepareMessage(ctx, alert, c, external)
		if prepareErr != nil {
			// No HTTP request was made. Unlike an ambiguous Discord response,
			// an unavailable authorization/library read can safely be retried.
			result = sendResult{Status: "pending", Detail: "Waiting to verify current library access and availability.", RetryAfter: 30 * time.Second}
			preflight = true
			attempts--
		} else if payload == nil {
			result = sendResult{Status: "cancelled", Detail: "The notification no longer has an eligible request or recipient."}
		} else {
			ids := payload["allowed_mentions"].(map[string]any)["users"].([]string)
			if len(ids) > 75 {
				return s.splitMentions(c, id, alert, ids)
			}
			result = send(ctx, s.client, webhookDestination(c), payload)
		}
	}
	next := int64(0)
	if result.Status == "pending" {
		next = retryAt(s.now(), result.RetryAfter)
		if attempts >= 5 || next >= created+86400 {
			result.Status = "failed"
			result.Detail = "The Discord delivery retry limit was reached."
		}
		// Persist the destination-wide cooldown, including when this event
		// exhausts its own budget. Later requests must respect the same limit.
		if !preflight {
			if err = s.cooldown(c.Webhook, next); err != nil {
				return err
			}
		}
	}
	if result.Status != "pending" {
		next = 0
	}
	_, err = s.db.Exec(`UPDATE discord_notifications SET status=?,detail=?,updated_at=?,next_attempt_at=?,attempts=? WHERE id=? AND status='sending'`, result.Status, result.Detail, s.now().Unix(), next, attempts, id)
	return err
}

// Discord limits message length as well as explicit mentions. Large audiences
// get separately receipted chunks, each rechecking consent before its send.
func (s *Service) splitMentions(c configuration, id int64, a requestAlert, ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for start := 0; start < len(ids); start += 75 {
		a.MentionIDs = ids[start:min(start+75, len(ids))]
		a.SuppressRole = start > 0
		if err = s.insertEvent(tx, c, fmt.Sprintf("mentions:%d:%d", id, start), a, s.now().Unix()); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE discord_notifications SET status='split',detail='The audience was divided into smaller deliveries.',attempts=attempts-1,next_attempt_at=0,updated_at=? WHERE id=? AND status='sending'`, s.now().Unix(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) cooldown(webhook string, until int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := s.readConfig(tx)
	if err != nil {
		return err
	}
	if c.Webhook == webhook {
		c.NotBefore = max(c.NotBefore, until)
		if err = s.writeConfig(tx, c); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) Test(ctx context.Context, webhook string) (Delivery, error) {
	return s.TestUpdate(ctx, Update{Webhook: webhook})
}

func (s *Service) TestUpdate(ctx context.Context, u Update) (Delivery, error) {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	c, err := s.readConfig(s.db)
	if err != nil {
		return Delivery{}, err
	}
	if err = c.apply(u); err != nil {
		return Delivery{}, err
	}
	webhook, savedWebhook := u.Webhook, c.Webhook
	if webhook == "" {
		webhook = c.Webhook
	}
	webhook, err = normalizeWebhook(webhook)
	if err != nil {
		return Delivery{}, err
	}
	if webhook == c.Webhook && s.now().Unix() < c.NotBefore {
		return Delivery{Status: "pending", Detail: "Discord asked us to wait. Try the test again later.", NextAttemptAt: c.NotBefore}, nil
	}
	c.Webhook = webhook
	result := send(ctx, s.client, webhookDestination(c), withAppearance(c, map[string]any{"content": "Cantinarr test notification. Enabled request and report events will appear here.", "allowed_mentions": map[string]any{"parse": []string{}}, "tts": false}))
	d := Delivery{Status: result.Status, Detail: result.Detail, Attempts: 1, UpdatedAt: s.now().Unix()}
	if result.Status == "pending" {
		d.NextAttemptAt = retryAt(s.now(), result.RetryAfter)
		if webhook == savedWebhook {
			err = s.cooldown(webhook, d.NextAttemptAt)
		}
	}
	return d, err
}

// The queue clock uses whole seconds. Round up so a sub-second send time
// cannot shorten the wait Discord requested.
func retryAt(now time.Time, delay time.Duration) int64 {
	at := now.Add(delay)
	seconds := at.Unix()
	if at.Nanosecond() != 0 {
		seconds++
	}
	return seconds
}
