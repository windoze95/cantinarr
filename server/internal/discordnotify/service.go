package discordnotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/secrets"
)

const settingsKey = "discord_notifications"

type configuration struct {
	IncludeAutoApproved bool   `json:"include_auto_approved"`
	Enabled             bool   `json:"enabled"`
	Webhook             string `json:"webhook_url"`
	Revision            int64  `json:"revision"`
	NotBefore           int64  `json:"not_before"`
}

type Settings struct {
	IncludeAutoApproved bool       `json:"include_auto_approved"`
	Enabled             bool       `json:"enabled"`
	HasWebhook          bool       `json:"has_webhook"`
	Recent              []Delivery `json:"recent"`
	Error               string     `json:"error,omitempty"`
}

// Service owns the encrypted destination and durable delivery queue. It has
// no dependency on native push or on the request package.
type Service struct {
	db           *sql.DB
	cipher       *secrets.Cipher
	client       *http.Client
	externalURL  func() string
	wake         chan struct{}
	deliveryMu   sync.Mutex // one sender; settings saves wait for an in-flight send
	errorMu      sync.Mutex
	enqueueError string
	now          func() time.Time
}

func NewService(db *sql.DB, cipher *secrets.Cipher, externalURL func() string) *Service {
	return &Service{db: db, cipher: cipher, client: newClient(), externalURL: externalURL, wake: make(chan struct{}, 1), now: time.Now}
}

type reader interface{ QueryRow(string, ...any) *sql.Row }

func (s *Service) readConfig(db reader) (configuration, error) {
	var c configuration
	var stored string
	err := db.QueryRow("SELECT value FROM settings WHERE key=?", settingsKey).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
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
	out := Settings{Enabled: c.Enabled, IncludeAutoApproved: c.IncludeAutoApproved, HasWebhook: c.Webhook != "", Recent: []Delivery{}}
	rows, err := s.db.Query(`SELECT request_id,status,detail,attempts,updated_at,next_attempt_at FROM discord_notifications ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.RequestID, &d.Status, &d.Detail, &d.Attempts, &d.UpdatedAt, &d.NextAttemptAt); err != nil {
			return out, err
		}
		out.Recent = append(out.Recent, d)
	}
	s.errorMu.Lock()
	out.Error = s.enqueueError
	s.errorMu.Unlock()
	return out, rows.Err()
}

// Save preserves the write-only URL when omitted/blank. Remove is explicit.
func (s *Service) Save(enabled bool, webhook string, remove bool, includeAutoApproved *bool) error {
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
	if includeAutoApproved != nil {
		c.IncludeAutoApproved = *includeAutoApproved
	}
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
	if previous.Enabled != c.Enabled || previous.Webhook != c.Webhook {
		c.Revision++
		if previous.Webhook != c.Webhook {
			c.NotBefore = 0
		}
		if _, err = tx.Exec(`UPDATE discord_notifications SET status='cancelled',detail='Cancelled because the Discord configuration changed.',next_attempt_at=0,updated_at=? WHERE status='pending'`, s.now().Unix()); err != nil {
			return err
		}
	}
	if previous.IncludeAutoApproved && !c.IncludeAutoApproved {
		if _, err = tx.Exec(`UPDATE discord_notifications SET status='cancelled',detail='Automatically approved request alerts were turned off.',next_attempt_at=0,updated_at=? WHERE status='pending' AND json_extract(payload,'$.requires_approval')=0`, s.now().Unix()); err != nil {
			return err
		}
	}
	if err = s.writeConfig(tx, c); err != nil {
		return err
	}
	return tx.Commit()
}

// RequestCreated is called only after a genuinely new request was saved.
// Enqueue failure is visible to admins, but cannot undo an accepted request.
func (s *Service) RequestCreated(id int64, approval bool) {
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
	if !c.Enabled || (!approval && !c.IncludeAutoApproved) {
		return nil
	}
	a := requestAlert{RequiresApproval: approval}
	err = tx.QueryRow(`SELECT r.title,r.media_type,u.username,COALESCE(r.book_format,'') FROM request_log r JOIN users u ON u.id=r.user_id WHERE r.id=?`, id).Scan(&a.Title, &a.MediaType, &a.Username, &a.BookFormat)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(a)
	now := s.now().Unix()
	_, err = tx.Exec(`INSERT INTO discord_notifications(request_id,revision,payload,status,created_at,updated_at,next_attempt_at) VALUES(?,?,?,'pending',?,?,?) ON CONFLICT(request_id) DO NOTHING`, id, c.Revision, string(data), now, now, max(now, c.NotBefore))
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
	var id, created int64
	var attempts int
	var payload string
	// Claim atomically; UPDATE RETURNING prevents two workers sending a row.
	err = s.db.QueryRow(`UPDATE discord_notifications SET status='sending',attempts=attempts+1,updated_at=?,next_attempt_at=0 WHERE id=(SELECT id FROM discord_notifications WHERE status='pending' AND revision=? AND next_attempt_at<=? ORDER BY id LIMIT 1) AND status='pending' RETURNING id,payload,attempts,created_at`, now, c.Revision, now).Scan(&id, &payload, &attempts, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var alert requestAlert
	result := sendResult{Status: "failed", Detail: "The saved request alert could not be read."}
	if json.Unmarshal([]byte(payload), &alert) == nil {
		if !alert.RequiresApproval && !c.IncludeAutoApproved {
			_, err := s.db.Exec(`UPDATE discord_notifications SET status='cancelled',detail='Automatically approved request alerts were turned off.',updated_at=? WHERE id=?`, now, id)
			return err
		}
		external := ""
		if s.externalURL != nil {
			external = s.externalURL()
		}
		result = send(ctx, s.client, c.Webhook, message(alert, external))
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
		if err = s.cooldown(c.Webhook, next); err != nil {
			return err
		}
	}
	if result.Status != "pending" {
		next = 0
	}
	_, err = s.db.Exec(`UPDATE discord_notifications SET status=?,detail=?,updated_at=?,next_attempt_at=? WHERE id=? AND status='sending'`, result.Status, result.Detail, s.now().Unix(), next, id)
	return err
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
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	c, err := s.readConfig(s.db)
	if err != nil {
		return Delivery{}, err
	}
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
	result := send(ctx, s.client, webhook, map[string]any{"content": "Cantinarr test notification. New media requests will appear here when Discord notifications are enabled.", "allowed_mentions": map[string]any{"parse": []string{}}, "tts": false})
	d := Delivery{Status: result.Status, Detail: result.Detail, Attempts: 1, UpdatedAt: s.now().Unix()}
	if result.Status == "pending" {
		d.NextAttemptAt = retryAt(s.now(), result.RetryAfter)
		if webhook == c.Webhook {
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
