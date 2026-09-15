package auth

import (
	"net/http"
	"net/url"
)

func (s *Service) createExternalConsent(user User, method, issuer string, plexID int64, fingerprint string, oauth url.Values) (any, error) {
	ticket, key, expires, err := newCompletionTicket()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	f := s.oidcFlows
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prune()
	if f.full() {
		return nil, ErrAuthUnavailable
	}
	f.consents[key] = oidcConsent{UserID: user.ID, Method: method, Issuer: issuer, PlexID: plexID, OAuth: oidcOAuthValues(oauth), Expires: expires, Fingerprint: fingerprint}
	return map[string]string{"status": "authenticated", "consent": ticket, "username": user.Username}, nil
}
func (s *Service) clearPlexConsents() {
	f := s.oidcFlows
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, c := range f.consents {
		if c.Method == "plex" {
			delete(f.consents, key)
		}
	}
}
func (h *OAuthHandler) authorizeExternal(r *http.Request, client *OAuthClient, method string) (string, error) {
	s := h.service
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	f := s.oidcFlows
	f.mu.Lock()
	f.prune()
	key := hashToken(r.Form.Get(method + "_consent"))
	consent, ok := f.consents[key]
	if !ok || consent.Method != method || oidcOAuthValues(r.Form).Encode() != oidcOAuthValues(consent.OAuth).Encode() {
		f.mu.Unlock()
		return "", ErrOIDCFlow
	}
	delete(f.consents, key)
	f.mu.Unlock()
	if method == "plex" {
		c, err := s.plexConfiguration()
		if err != nil {
			return "", err
		}
		if !c.Enabled || c.raw != consent.Fingerprint {
			return "", ErrPlexFlow
		}
		if err = requirePlexIdentity(s.db, consent.UserID, consent.PlexID); err != nil {
			return "", err
		}
	} else {
		c, err := s.oidcConfiguration()
		if err != nil {
			return "", err
		}
		if !c.Enabled || c.fingerprint != consent.Fingerprint {
			return "", ErrOIDCFlow
		}
		var linked bool
		if s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM oidc_identities WHERE user_id=? AND issuer=?)", consent.UserID, consent.Issuer).Scan(&linked) != nil || !linked {
			return "", ErrOIDCFlow
		}
	}
	return s.createOAuthAuthorizationCode(client, consent.UserID, r.Form.Get("redirect_uri"), r.Form.Get("code_challenge"), h.requestedMCPResource(r), normalizeOAuthScope(r.Form.Get("scope")), method, consent.Issuer, consent.PlexID)
}
