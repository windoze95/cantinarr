package appletv

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func (h *Handler) lock(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy[id] {
		return false
	}
	h.busy[id] = true
	return true
}

func (h *Handler) unlock(id string) { h.mu.Lock(); delete(h.busy, id); h.mu.Unlock() }

func (h *Handler) check(w http.ResponseWriter, r *http.Request) {
	_, d, err := h.target(r.Context(), chi.URLParam(r, "tvID"), true)
	if err != nil {
		fail(w, err)
		return
	}
	if !h.lock(d.ID) {
		fail(w, problem("busy"))
		return
	}
	defer h.unlock(d.ID)
	ctx, cancel := context.WithTimeout(r.Context(), operationLifetime)
	defer cancel()
	credentials, err := h.cipher.Decrypt(d.credentials)
	if err != nil {
		fail(w, problem("needs_pairing"))
		return
	}
	conversation, err := h.start(ctx, Command{Action: "check", Address: d.Address, Identifier: d.Identifier, Credentials: credentials})
	if err != nil {
		fail(w, err)
		return
	}
	defer conversation.Close()
	reply, err := conversation.Receive()
	if err == nil && reply.State != "checked" {
		err = problem("unavailable")
	}
	if err == nil {
		_, _, err = h.current(r, d, true)
	}
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]string{"state": "checked"})
}

func (h *Handler) current(r *http.Request, before storedDevice, adminOnly bool) (caller, storedDevice, error) {
	c, d, err := h.target(r.Context(), before.ID, adminOnly)
	if err == nil && d.revision != before.revision {
		err = problem("not_available")
	}
	return c, d, err
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request) {
	c, d, err := h.target(r.Context(), chi.URLParam(r, "tvID"), false)
	if err != nil {
		fail(w, err)
		return
	}
	var title titleRequest
	if !decode(w, r, &title) {
		return
	}
	if (title.MediaType != "movie" && title.MediaType != "tv") || title.TMDBID <= 0 {
		fail(w, problem("invalid_request"))
		return
	}
	if !h.lock(d.ID) {
		fail(w, problem("busy"))
		return
	}
	defer h.unlock(d.ID)
	h.mu.Lock()
	delete(h.confirmations, d.ID)
	h.mu.Unlock()
	if err = h.dispatch(r, c, d, title, "open", time.Time{}); err != nil {
		fail(w, err)
		return
	}
	pending := confirmation{id: randomID(), userID: c.userID, deviceID: c.deviceID, revision: d.revision, title: title, deadline: time.Now().Add(confirmationLifetime)}
	h.mu.Lock()
	h.confirmations[d.ID] = pending
	h.mu.Unlock()
	respond(w, 200, map[string]any{"state": "sent", "confirmation_id": pending.id, "confirmation_expires_at": pending.deadline.UTC()})
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	c, d, err := h.target(r.Context(), chi.URLParam(r, "tvID"), false)
	if err != nil {
		fail(w, err)
		return
	}
	var body struct {
		ID string `json:"confirmation_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !h.lock(d.ID) {
		fail(w, problem("busy"))
		return
	}
	defer h.unlock(d.ID)
	h.mu.Lock()
	pending, ok := h.confirmations[d.ID]
	if ok && pending.id == body.ID && pending.userID == c.userID && pending.deviceID == c.deviceID {
		delete(h.confirmations, d.ID)
	}
	h.mu.Unlock()
	if !ok || body.ID == "" || pending.id != body.ID || pending.userID != c.userID || pending.deviceID != c.deviceID || pending.revision != d.revision || time.Now().After(pending.deadline) {
		fail(w, problem("confirmation_expired"))
		return
	}
	// This is a one-use, short-lived handoff confirmation, never a general
	// remote-control endpoint or an automatic Select after every launch.
	if err = h.dispatch(r, c, d, pending.title, "select", pending.deadline); err != nil {
		fail(w, err)
		return
	}
	respond(w, 200, map[string]string{"state": "sent"})
}

func (h *Handler) dispatch(r *http.Request, c caller, d storedDevice, title titleRequest, action string, notAfter time.Time) error {
	ctx, cancel := context.WithTimeout(r.Context(), operationLifetime)
	defer cancel()
	r = r.WithContext(ctx)
	if h.title == nil {
		return ErrLookupUnavailable
	}
	if err := h.title(ctx, c.userID, title.MediaType, title.TMDBID); err != nil {
		return err
	}
	credentials, err := h.cipher.Decrypt(d.credentials)
	if err != nil {
		return problem("needs_pairing")
	}
	conversation, err := h.start(ctx, Command{Action: action, Address: d.Address, Identifier: d.Identifier, Credentials: credentials, MediaType: title.MediaType, TMDBID: title.TMDBID})
	if err != nil {
		return err
	}
	defer conversation.Close()
	reply, err := conversation.Receive()
	if err != nil {
		return err
	}
	if reply.State != "ready" {
		return problem("unavailable")
	}
	if _, _, err = h.current(r, d, false); err != nil {
		return err
	}
	if err = h.title(ctx, c.userID, title.MediaType, title.TMDBID); err != nil {
		return err
	}
	if _, _, err = h.current(r, d, false); err != nil {
		return err
	}
	if !notAfter.IsZero() && !time.Now().Before(notAfter) {
		return problem("confirmation_expired")
	}
	if ctx.Err() != nil {
		return problem("timeout")
	}
	if err = conversation.Send(map[string]bool{"commit": true}); err != nil {
		return err
	}
	reply, err = conversation.Receive()
	if err != nil {
		return err
	}
	if reply.State != "sent" {
		return problem("launch_failed")
	}
	return nil
}
