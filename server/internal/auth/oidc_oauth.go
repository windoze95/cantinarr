package auth

import (
	"net/http"
	"net/url"
	"strings"
)

var oidcOAuthFields = []string{"response_type", "client_id", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "resource"}

func oidcOAuthValues(in url.Values) url.Values {
	out := url.Values{}
	for _, key := range oidcOAuthFields {
		out.Set(key, in.Get(key))
	}
	return out
}
func (h *OAuthHandler) BeginOIDC(w http.ResponseWriter, r *http.Request) {
	var req oidcBeginRequest
	if !oidcDecode(w, r, &req) {
		return
	}
	if req.Client != "mcp" {
		oidcHTTPError(w, ErrOIDCFlow)
		return
	}
	req.OAuth = oidcOAuthValues(req.OAuth)
	check := r.Clone(r.Context())
	check.Form = req.OAuth
	if _, err := h.validateAuthorizeRequest(check); err != nil {
		oidcHTTPError(w, err)
		return
	}
	result, err := h.service.beginOIDC(req, "mcp", nil)
	if err != nil {
		oidcHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *OAuthHandler) authorizeOIDC(r *http.Request, client *OAuthClient) (string, error) {
	s := h.service
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	f := s.oidcFlows
	f.mu.Lock()
	f.prune()
	key := hashToken(r.Form.Get("oidc_consent"))
	consent, ok := f.consents[key]
	if !ok || oidcOAuthValues(r.Form).Encode() != oidcOAuthValues(consent.OAuth).Encode() {
		f.mu.Unlock()
		return "", ErrOIDCFlow
	}
	delete(f.consents, key)
	f.mu.Unlock()
	c, err := s.oidcConfiguration()
	if err != nil {
		return "", err
	}
	if !c.Enabled || c.fingerprint != consent.Fingerprint {
		return "", ErrOIDCFlow
	}
	var linked bool
	if err = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM oidc_identities WHERE user_id=? AND issuer=?)", consent.UserID, consent.Issuer).Scan(&linked); err != nil || !linked {
		return "", ErrOIDCFlow
	}
	return s.createOAuthAuthorizationCode(client, consent.UserID, r.Form.Get("redirect_uri"), r.Form.Get("code_challenge"), h.requestedMCPResource(r), normalizeOAuthScope(r.Form.Get("scope")), "oidc", consent.Issuer)
}
func (h *OAuthHandler) oidcTemplateSettings() (string, string, string) {
	c, err := h.service.oidcConfiguration()
	if err != nil {
		return "", "", err.Error()
	}
	if !c.Enabled {
		return "", "", ""
	}
	note := ""
	if c.SSOOnly {
		note = "Single sign-on is required. Local sign-in is available for administrator recovery."
	}
	return c.Label, strings.TrimSuffix(c.CallbackURL, "/api/auth/oidc/callback"), note
}
