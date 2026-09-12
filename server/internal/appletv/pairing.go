package appletv

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func receiveWithin(ctx context.Context, conversation Conversation, timeout time.Duration) (Reply, error) {
	type result struct {
		reply Reply
		err   error
	}
	done := make(chan result, 1)
	go func() { reply, err := conversation.Receive(); done <- result{reply, err} }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case out := <-done:
		return out.reply, out.err
	case <-ctx.Done():
		conversation.Close()
		return Reply{}, problem("timeout")
	case <-timer.C:
		conversation.Close()
		return Reply{}, problem("timeout")
	}
}

func (h *Handler) begin(w http.ResponseWriter, r *http.Request) {
	c, err := h.identity(r.Context(), true)
	if err != nil {
		fail(w, err)
		return
	}
	var body Device
	if !decode(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.ID != "" || !validAddress(body.Address, false) || body.Identifier == "" || len(body.Identifier) > 256 || len(body.Name) > 128 {
		fail(w, problem("invalid_request"))
		return
	}
	h.mu.Lock()
	if len(h.pairings)+h.pairingStarts >= 8 {
		h.mu.Unlock()
		fail(w, problem("busy"))
		return
	}
	h.pairingStarts++
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.pairingStarts--; h.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(context.Background(), pairingLifetime)
	conversation, err := h.start(ctx, Command{Action: "pair", Address: body.Address, Identifier: body.Identifier})
	if err != nil {
		cancel()
		fail(w, err)
		return
	}
	reply, err := receiveWithin(r.Context(), conversation, operationLifetime)
	if err == nil && (reply.State != "pin_required" || reply.Device.Identifier != body.Identifier || !validAddress(reply.Device.Address, false)) {
		err = problem("unavailable")
	}
	if err == nil {
		_, err = h.identity(r.Context(), true)
	}
	if err != nil {
		cancel()
		conversation.Close()
		fail(w, err)
		return
	}
	if body.Name != "" {
		reply.Device.Name = body.Name
	}
	id := randomID()
	deadline, _ := ctx.Deadline()
	session := &pairingSession{userID: c.userID, deviceID: c.deviceID, device: reply.Device, conversation: conversation, cancel: cancel, deadline: deadline}
	h.mu.Lock()
	h.pairings[id] = session
	session.timer = time.AfterFunc(time.Until(deadline), func() { h.dropPairing(id, session) })
	h.mu.Unlock()
	respond(w, 201, map[string]any{"id": id, "state": "pin_required", "device": session.device, "expires_at": session.deadline.UTC()})
}

func (h *Handler) dropPairing(id string, session *pairingSession) {
	h.mu.Lock()
	if h.pairings[id] == session {
		delete(h.pairings, id)
	}
	h.mu.Unlock()
	session.timer.Stop()
	session.cancel()
	session.conversation.Close()
}

func (h *Handler) pairing(r *http.Request) (string, *pairingSession, error) {
	c, err := h.identity(r.Context(), true)
	if err != nil {
		return "", nil, err
	}
	id := chi.URLParam(r, "pairingID")
	h.mu.Lock()
	session := h.pairings[id]
	h.mu.Unlock()
	if session == nil || session.userID != c.userID || session.deviceID != c.deviceID || time.Now().After(session.deadline) {
		return id, nil, problem("pairing_expired")
	}
	return id, session, nil
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	id, session, err := h.pairing(r)
	if err != nil {
		fail(w, err)
		return
	}
	var body struct {
		PIN string `json:"pin"`
	}
	if !decode(w, r, &body) {
		return
	}
	valid := len(body.PIN) == 4
	for _, c := range body.PIN {
		if c < '0' || c > '9' {
			valid = false
		}
	}
	if !valid {
		fail(w, problem("invalid_pin"))
		return
	}
	if !session.mu.TryLock() {
		fail(w, problem("busy"))
		return
	}
	defer session.mu.Unlock()
	defer h.dropPairing(id, session)
	if err = session.conversation.Send(body); err != nil {
		fail(w, err)
		return
	}
	reply, err := receiveWithin(r.Context(), session.conversation, operationLifetime)
	if err == nil && (reply.State != "paired" || reply.Credentials == "" || reply.Device.Identifier != session.device.Identifier) {
		err = problem("pairing_failed")
	}
	if err == nil {
		_, err = h.identity(r.Context(), true)
	}
	if err != nil {
		fail(w, err)
		return
	}
	sealed, err := h.cipher.Encrypt(reply.Credentials)
	if err != nil {
		fail(w, problem("unavailable"))
		return
	}
	d := session.device
	if d.Name == "" {
		d.Name = "Apple TV"
	}
	// Cancellation and expiry must win before the credentials are persisted.
	// Serialize the final short write with removal of this pairing session.
	h.mu.Lock()
	if h.pairings[id] != session || !time.Now().Before(session.deadline) {
		h.mu.Unlock()
		fail(w, problem("pairing_expired"))
		return
	}
	err = h.db.QueryRowContext(r.Context(), `INSERT INTO apple_tv_devices(id,name,address,identifier,credentials) VALUES(?,?,?,?,?)
		ON CONFLICT(identifier) DO UPDATE SET name=excluded.name,address=excluded.address,credentials=excluded.credentials,revision=apple_tv_devices.revision+1 RETURNING id`, randomID(), d.Name, d.Address, d.Identifier, sealed).Scan(&d.ID)
	h.mu.Unlock()
	if err != nil {
		fail(w, problem("unavailable"))
		return
	}
	respond(w, 201, d)
}

func (h *Handler) cancelPairing(w http.ResponseWriter, r *http.Request) {
	id, session, err := h.pairing(r)
	if err != nil {
		fail(w, err)
		return
	}
	h.dropPairing(id, session)
	respond(w, 200, map[string]string{"state": "cancelled"})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	_, d, err := h.target(r.Context(), chi.URLParam(r, "tvID"), true)
	if err != nil {
		fail(w, err)
		return
	}
	var body struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 128 || !validAddress(body.Address, false) {
		fail(w, problem("invalid_request"))
		return
	}
	_, err = h.db.ExecContext(r.Context(), `UPDATE apple_tv_devices SET name=?,address=?,revision=revision+1 WHERE id=?`, body.Name, body.Address, d.ID)
	if err != nil {
		fail(w, problem("unavailable"))
		return
	}
	respond(w, 200, map[string]string{"state": "saved"})
}

func (h *Handler) forget(w http.ResponseWriter, r *http.Request) {
	_, d, err := h.target(r.Context(), chi.URLParam(r, "tvID"), true)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err = h.db.ExecContext(r.Context(), `DELETE FROM apple_tv_devices WHERE id=?`, d.ID); err != nil {
		fail(w, problem("unavailable"))
		return
	}
	h.mu.Lock()
	delete(h.confirmations, d.ID)
	h.mu.Unlock()
	respond(w, 200, map[string]string{"state": "forgotten"})
}

func (h *Handler) grants(w http.ResponseWriter, r *http.Request) {
	_, d, err := h.target(r.Context(), chi.URLParam(r, "tvID"), true)
	if err != nil {
		fail(w, err)
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			UserIDs []int64 `json:"user_ids"`
		}
		if !decode(w, r, &body) {
			return
		}
		if len(body.UserIDs) > 1000 {
			fail(w, problem("invalid_request"))
			return
		}
		tx, err := h.db.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, problem("unavailable"))
			return
		}
		defer tx.Rollback()
		for _, id := range body.UserIDs {
			var adult bool
			if tx.QueryRowContext(r.Context(), `SELECT NOT EXISTS(SELECT 1 FROM user_content_policies WHERE user_id=users.id) FROM users WHERE id=?`, id).Scan(&adult) != nil || !adult {
				fail(w, problem("invalid_request"))
				return
			}
		}
		if _, err = tx.ExecContext(r.Context(), `DELETE FROM apple_tv_grants WHERE tv_id=?`, d.ID); err != nil {
			fail(w, problem("unavailable"))
			return
		}
		for _, id := range body.UserIDs {
			if _, err = tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO apple_tv_grants(tv_id,user_id) VALUES(?,?)`, d.ID, id); err != nil {
				fail(w, problem("unavailable"))
				return
			}
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE apple_tv_devices SET revision=revision+1 WHERE id=?`, d.ID); err != nil {
			fail(w, problem("unavailable"))
			return
		}
		if tx.Commit() != nil {
			fail(w, problem("unavailable"))
			return
		}
		respond(w, 200, map[string]string{"state": "saved"})
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT user_id FROM apple_tv_grants WHERE tv_id=? ORDER BY user_id`, d.ID)
	if err != nil {
		fail(w, problem("unavailable"))
		return
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			fail(w, problem("unavailable"))
			return
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		fail(w, problem("unavailable"))
		return
	}
	respond(w, 200, map[string]any{"user_ids": ids})
}
