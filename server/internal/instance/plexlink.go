package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/plex"
)

// plexLinkTTL is how long an approved PIN link waits for the admin to save
// the instance. plex.tv's own PIN expires sooner than this; the token it
// yields is kept here, in memory only, for the rest of the window.
const plexLinkTTL = 15 * time.Minute

// plexLink is one PIN link: the client identifier the PIN was created under
// and, once the admin approves it in their browser, the token and account.
type plexLink struct {
	clientID string
	token    string
	account  string
	expires  time.Time
}

// plexLinks holds links by PIN id. Tokens never leave the process: the app
// refers to a link by its PIN id, and the create/update handlers resolve it.
type plexLinks struct {
	mu    sync.Mutex
	byPin map[int64]*plexLink
}

func newPlexLinks() *plexLinks {
	return &plexLinks{byPin: map[int64]*plexLink{}}
}

func (l *plexLinks) put(pin int64, link *plexLink) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	l.byPin[pin] = link
}

func (l *plexLinks) get(pin int64) *plexLink {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune()
	return l.byPin[pin]
}

func (l *plexLinks) prune() {
	now := time.Now()
	for pin, link := range l.byPin {
		if now.After(link.expires) {
			delete(l.byPin, pin)
		}
	}
}

// SetPlexBaseURL points the link flow at another plex.tv (tests, a lab proxy).
func (h *Handler) SetPlexBaseURL(baseURL string) {
	h.plexBaseURL = strings.TrimRight(baseURL, "/")
}

// PlexLinkBegin starts a PIN link: POST /instances/plex/link/begin ->
// {pin_id, code, url}. The admin opens url, approves, and the app polls
// PlexLinkCheck with pin_id.
func (h *Handler) PlexLinkBegin(w http.ResponseWriter, r *http.Request) {
	clientID := uuid.NewString()
	client := plex.NewClientAt(h.plexBaseURL)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	pin, err := client.CreatePin(ctx, clientID)
	if err != nil {
		http.Error(w, `{"error":"could not reach plex.tv"}`, http.StatusBadGateway)
		return
	}
	h.plexLinks.put(pin.ID, &plexLink{clientID: clientID, expires: time.Now().Add(plexLinkTTL)})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"pin_id": pin.ID,
		"code":   pin.Code,
		"url":    client.AuthURL(clientID, pin.Code),
	})
}

// PlexLinkCheck polls a PIN link: POST /instances/plex/link/check {pin_id}
// -> {linked, account}. Once approved, the token is verified and held for
// the instance save; linked=false means "still waiting".
func (h *Handler) PlexLinkCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PinID int64 `json:"pin_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PinID == 0 {
		http.Error(w, `{"error":"pin_id required"}`, http.StatusBadRequest)
		return
	}
	link := h.plexLinks.get(body.PinID)
	if link == nil {
		http.Error(w, `{"error":"the Plex link has expired; link the account again"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if link.token != "" {
		json.NewEncoder(w).Encode(map[string]any{"linked": true, "account": link.account})
		return
	}
	client := plex.NewClientAt(h.plexBaseURL)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	pin, err := client.CheckPin(ctx, link.clientID, body.PinID)
	if err != nil {
		http.Error(w, `{"error":"could not reach plex.tv"}`, http.StatusBadGateway)
		return
	}
	if pin.AuthToken == "" {
		json.NewEncoder(w).Encode(map[string]any{"linked": false})
		return
	}
	account, err := client.GetUser(ctx, link.clientID, pin.AuthToken)
	if err != nil {
		http.Error(w, `{"error":"plex.tv did not accept the approved link; try again"}`, http.StatusBadGateway)
		return
	}
	link.token, link.account = pin.AuthToken, account.Username
	json.NewEncoder(w).Encode(map[string]any{"linked": true, "account": account.Username})
}

type plexServerResponse struct {
	Name              string `json:"name"`
	MachineIdentifier string `json:"machine_identifier"`
}

// PlexServers lists the owned Plex Media Servers of a linked account, for
// the editor's server picker: POST /instances/plex/servers with the same
// candidate body as TestConnection (a plex_link_pin, or an id whose stored
// token is used).
func (h *Handler) PlexServers(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.resolveTestInstance(w, r)
	if !ok {
		return
	}
	if inst.ServiceType != "plex" {
		http.Error(w, `{"error":"service_type must be plex"}`, http.StatusBadRequest)
		return
	}
	client := plex.NewClientAt(inst.URL)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	servers, err := client.ListServers(ctx, inst.MediaServerConfig.ClientID, inst.APIKey)
	if err != nil {
		http.Error(w, `{"error":"could not list the account's servers; relink the Plex account"}`, http.StatusBadGateway)
		return
	}
	out := make([]plexServerResponse, 0, len(servers))
	for _, s := range servers {
		out = append(out, plexServerResponse{Name: s.Name, MachineIdentifier: s.ClientIdentifier})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"servers": out})
}

// applyPlexLink resolves a Plex instance's credential before the shared
// validation runs: the url defaults to plex.tv, and the token comes from the
// named PIN link, else the stored instance, else an api_key pasted by hand
// (which gets a fresh client identifier). Returns the client identifier the
// token belongs with. Non-Plex instances pass through untouched.
func (h *Handler) applyPlexLink(inst *Instance, pin int64, existing *Instance) (string, error) {
	if inst.ServiceType != "plex" {
		return "", nil
	}
	if inst.URL == "" {
		inst.URL = plex.BaseURL
	}
	if pin != 0 {
		link := h.plexLinks.get(pin)
		if link == nil {
			return "", fmt.Errorf("the Plex link has expired; link the account again")
		}
		if link.token == "" {
			return "", fmt.Errorf("the Plex link is not approved yet")
		}
		inst.APIKey = link.token
		return link.clientID, nil
	}
	if existing != nil && existing.ServiceType == "plex" && inst.APIKey == existing.APIKey {
		return existing.MediaServerConfig.ClientID, nil
	}
	if inst.APIKey != "" {
		return uuid.NewString(), nil
	}
	return "", fmt.Errorf("link a Plex account first")
}

// applyPlexConfig finishes a Plex instance's config after the shared config
// step: the client identifier the token belongs with, and the server the
// instance shares — an instance with no server selected can invite nobody.
func applyPlexConfig(inst *Instance, clientID string) error {
	if inst.ServiceType != "plex" {
		return nil
	}
	if clientID != "" {
		inst.MediaServerConfig.ClientID = clientID
	}
	if inst.MediaServerConfig.MachineIdentifier == "" {
		return fmt.Errorf("pick the Plex server to share (media_server_config.machine_identifier)")
	}
	return nil
}
