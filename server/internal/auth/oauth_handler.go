package auth

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type OAuthHandler struct {
	service *Service
	issuer  string
}

func NewOAuthHandler(service *Service, issuer string) *OAuthHandler {
	return &OAuthHandler{service: service, issuer: strings.TrimRight(issuer, "/")}
}

func (h *OAuthHandler) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	writeOAuthJSON(w, http.StatusOK, map[string]any{
		"resource":                 h.mcpResourceURL(r),
		"resource_name":            "Cantinarr MCP",
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{defaultOAuthScope},
	})
}

func (h *OAuthHandler) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	metadata := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{defaultOAuthScope},
	}
	if h.issuer != "" {
		metadata["authorization_response_iss_parameter_supported"] = true
	}
	writeOAuthJSON(w, http.StatusOK, metadata)
}

func (h *OAuthHandler) RegisterClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		Scope                   string   `json:"scope"`
		ApplicationType         string   `json:"application_type"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid registration request")
		return
	}
	applicationType, ok := oauthApplicationType(req.ApplicationType)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "application_type must be native or web")
		return
	}
	if req.ApplicationType != "" && applicationType == "web" && hasLoopbackHTTPRedirect(req.RedirectURIs) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "web application_type requires HTTPS redirect_uris")
		return
	}
	client, err := h.service.RegisterOAuthClient(req.ClientName, req.RedirectURIs)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris must be HTTPS or localhost HTTP")
		return
	}

	writeOAuthJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.ClientName,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                client.GrantTypes,
		"response_types":             client.ResponseTypes,
		"token_endpoint_auth_method": "none",
		"scope":                      client.Scope,
		"application_type":           applicationType,
	})
}

func (h *OAuthHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		h.renderAuthorizeForm(w, r, "")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	client, err := h.validateAuthorizeRequest(r)
	if err != nil {
		h.renderAuthorizeForm(w, r, oauthErrorText(err))
		return
	}

	var code string
	if r.Form.Get("plex_consent") != "" {
		code, err = h.authorizeExternal(r, client, "plex")
	} else if r.Form.Get("oidc_consent") != "" {
		code, err = h.authorizeOIDC(r, client)
	} else {

		var user *User
		user, err = h.service.AuthenticatePassword(r.Form.Get("username"), r.Form.Get("password"))
		if err != nil {
			h.renderAuthorizeForm(w, r, "Invalid username or password.")
			return
		}

		resource := h.requestedMCPResource(r)
		scope := normalizeOAuthScope(r.Form.Get("scope"))
		code, err = h.service.CreateOAuthAuthorizationCode(
			client,
			user.ID,
			r.Form.Get("redirect_uri"),
			r.Form.Get("code_challenge"),
			resource,
			scope,
		)
	}
	if err != nil {
		h.renderAuthorizeForm(w, r, oauthErrorText(err))
		return
	}

	redirectURI, err := oauthCodeRedirect(r.Form.Get("redirect_uri"), code, r.Form.Get("state"), h.issuer)
	if err != nil {
		http.Error(w, "invalid redirect uri", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, redirectURI, http.StatusFound)
}

func (h *OAuthHandler) Token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}

	clientID := r.Form.Get("client_id")
	if clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "client_id is required")
		return
	}
	resource := h.requestedMCPResource(r)

	var (
		resp *OAuthTokenResponse
		err  error
	)
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		resp, err = h.service.ExchangeOAuthAuthorizationCode(
			clientID,
			r.Form.Get("code"),
			r.Form.Get("redirect_uri"),
			r.Form.Get("code_verifier"),
			resource,
		)
	case "refresh_token":
		resp, err = h.service.RefreshOAuthToken(
			clientID,
			r.Form.Get("refresh_token"),
			resource,
		)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	if err != nil {
		writeOAuthTokenError(w, err)
		return
	}

	writeOAuthJSON(w, http.StatusOK, resp)
}

func (h *OAuthHandler) BeginOAuthPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	options, sessionID, err := h.service.BeginPasskeyLogin(r)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "failed to begin passkey login")
		return
	}

	writeOAuthJSON(w, http.StatusOK, map[string]interface{}{
		"options":    options,
		"session_id": sessionID,
	})
}

func (h *OAuthHandler) FinishOAuthPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	sessionID := r.Form.Get("session_id")
	if sessionID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "session_id is required")
		return
	}
	client, err := h.validateAuthorizeRequest(r)
	if err != nil {
		writeOAuthTokenError(w, err)
		return
	}

	waUser, err := h.service.finishPasskeyLoginUser(sessionID, r)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "access_denied", "passkey authentication failed")
		return
	}

	resource := h.requestedMCPResource(r)
	scope := normalizeOAuthScope(r.Form.Get("scope"))
	code, err := h.service.CreateOAuthAuthorizationCode(
		client,
		waUser.user.ID,
		r.Form.Get("redirect_uri"),
		r.Form.Get("code_challenge"),
		resource,
		scope,
	)
	if err != nil {
		writeOAuthTokenError(w, err)
		return
	}

	redirectURI, err := oauthCodeRedirect(r.Form.Get("redirect_uri"), code, r.Form.Get("state"), h.issuer)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid redirect_uri")
		return
	}
	writeOAuthJSON(w, http.StatusOK, map[string]string{"redirect_uri": redirectURI})
}

func (h *OAuthHandler) MCPAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			h.writeMCPUnauthorized(w, r)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			h.writeMCPUnauthorized(w, r)
			return
		}

		claims, user, err := h.service.AuthenticateTokenForAudience(token, h.mcpResourceURL(r))
		if err != nil {
			h.writeMCPUnauthorized(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		ctx = context.WithValue(ctx, UserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *OAuthHandler) PasskeySetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	returnTo := r.URL.Query().Get("return")
	base := h.baseURL(r)
	appURL := "cantinarr://passkeys?server=" + url.QueryEscape(base)
	if returnTo != "" {
		appURL += "&return=" + url.QueryEscape(returnTo)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = passkeySetupTemplate.Execute(w, map[string]string{
		"AppURL":     appURL,
		"BrowserURL": base + "/settings/passkeys/new",
		"ReturnURL":  returnTo,
	})
}

func (h *OAuthHandler) PasskeyCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = passkeyCreateTemplate.Execute(w, map[string]string{
		"Token": token,
	})
}

func (h *OAuthHandler) validateAuthorizeRequest(r *http.Request) (*OAuthClient, error) {
	if r.Form.Get("response_type") != "code" {
		return nil, errors.New("response_type must be code")
	}
	client, err := h.service.GetOAuthClient(r.Form.Get("client_id"))
	if err != nil {
		return nil, err
	}
	if !clientAllowsRedirect(client, r.Form.Get("redirect_uri")) {
		return nil, ErrOAuthInvalidRedirectURI
	}
	if r.Form.Get("code_challenge_method") != "S256" {
		return nil, ErrOAuthInvalidPKCE
	}
	if r.Form.Get("code_challenge") == "" {
		return nil, ErrOAuthInvalidPKCE
	}
	if !h.validRequestedMCPResource(r) {
		return nil, ErrOAuthInvalidResource
	}
	return client, nil
}

func (h *OAuthHandler) renderAuthorizeForm(w http.ResponseWriter, r *http.Request, message string) {
	oidcHeaders(w)
	if r.Method == http.MethodGet {
		_ = r.ParseForm()
	}
	if _, err := h.validateAuthorizeRequest(r); err != nil && message == "" {
		message = oauthErrorText(err)
	}
	label, origin, note := h.oidcTemplateSettings()
	plexLabel := ""
	if c, err := h.service.plexConfiguration(); err == nil && c.Enabled {
		plexLabel = "Continue with Plex"
		if c.ssoOnly != "false" {
			plexLabel = "Continue with Plex (administrator recovery)"
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = authorizeTemplate.Execute(w, map[string]string{
		"SSOLabel": label, "SSOOrigin": origin, "SSONote": note, "PlexLabel": plexLabel,
		"Message":             message,
		"ResponseType":        r.Form.Get("response_type"),
		"ClientID":            r.Form.Get("client_id"),
		"RedirectURI":         r.Form.Get("redirect_uri"),
		"Scope":               normalizeOAuthScope(r.Form.Get("scope")),
		"State":               r.Form.Get("state"),
		"CodeChallenge":       r.Form.Get("code_challenge"),
		"CodeChallengeMethod": r.Form.Get("code_challenge_method"),
		"Resource":            h.requestedMCPResource(r),
		"PasskeySetupURL":     h.passkeySetupURL(r),
	})
}

//go:embed plex_pkce.js
var plexPKCEScript string

var authorizeTemplate = newAuthPageTemplate("authorize", `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Cantinarr MCP Authorization</title>
  {{template "auth-style"}}
</head>
<body>
  <main>
    {{template "auth-brand"}}
    <h1>Connect your MCP client</h1>
    <p>Sign in to let your MCP client access Cantinarr with your account’s permissions.</p>
    {{if .Message}}<div class="error" role="alert">{{.Message}}</div>{{end}}
    <form method="post" action="/oauth/authorize">
      <input type="hidden" name="response_type" value="{{.ResponseType}}">
      <input type="hidden" name="client_id" value="{{.ClientID}}">
      <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
      <input type="hidden" name="scope" value="{{.Scope}}">
      <input type="hidden" name="state" value="{{.State}}">
      <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
      <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
      <input type="hidden" name="resource" value="{{.Resource}}">
      {{if .SSOLabel}}<button id="ssoButton" type="button">Continue with {{.SSOLabel}}</button>{{end}}
      {{if .SSONote}}<p>{{.SSONote}}</p>{{end}}
      <input type="hidden" name="oidc_consent" id="oidcConsent">
      <input type="hidden" name="plex_consent" id="plexConsent">
      {{if .PlexLabel}}<button id="plexButton" type="button" class="secondary">{{.PlexLabel}}</button>
      <div id="plexControls" hidden>
        <p>Approve Cantinarr in the Plex browser, then return here.</p>
        <button id="plexReopen" type="button" class="secondary">Reopen Plex</button>
        <button id="plexCheck" type="button" class="secondary">Check now</button>
        <button id="plexCancel" type="button" class="secondary">Cancel Plex sign-in</button>
      </div>{{end}}
      <button id="passkeyButton" type="button" class="secondary">Use passkey</button>
      <a class="button secondary" href="{{.PasskeySetupURL}}">Create a passkey</a>
      <div id="passkeyStatus" class="status" role="status" aria-live="polite"></div>
      <div class="divider">or</div>
      <label for="username">Username</label>
      <input id="username" name="username" autocomplete="username" required>
      <label for="password">Password</label>
      <input id="password" name="password" type="password" autocomplete="current-password" required>
      <button type="submit">Authorize</button>
    </form>
  </main>
  <script>`+plexPKCEScript+`
    const form = document.querySelector('form');
    const passkeyButton = document.getElementById('passkeyButton');
    const passkeyStatus = document.getElementById('passkeyStatus');

    function setStatus(text) {
      passkeyStatus.textContent = text || '';
    }
    function b64urlToBuffer(value) {
      const padded = value.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((value.length + 3) % 4);
      const binary = atob(padded);
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
      return bytes.buffer;
    }
    function bufferToB64url(buffer) {
      const bytes = new Uint8Array(buffer);
      let binary = '';
      for (const byte of bytes) binary += String.fromCharCode(byte);
      return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
    }
    function prepareRequestOptions(options) {
      const publicKey = options.publicKey;
      publicKey.challenge = b64urlToBuffer(publicKey.challenge);
      if (publicKey.allowCredentials) {
        publicKey.allowCredentials = publicKey.allowCredentials.map((cred) => ({
          ...cred,
          id: b64urlToBuffer(cred.id),
        }));
      }
      return { publicKey };
    }
    function assertionToJSON(credential) {
      const response = credential.response;
      const out = {
        id: credential.id,
        rawId: bufferToB64url(credential.rawId),
        type: credential.type,
        response: {
          clientDataJSON: bufferToB64url(response.clientDataJSON),
          authenticatorData: bufferToB64url(response.authenticatorData),
          signature: bufferToB64url(response.signature),
        },
      };
      if (response.userHandle) out.response.userHandle = bufferToB64url(response.userHandle);
      return out;
    }
    function oauthQuery(sessionID) {
      const params = new URLSearchParams(new FormData(form));
      params.set('session_id', sessionID);
      return params.toString();
    }

    const ssoButton = document.getElementById('ssoButton');
    const ssoOrigin = {{.SSOOrigin}};
    const oauthFields = ['response_type','client_id','redirect_uri','scope','state','code_challenge','code_challenge_method','resource'];
    async function ssoJSON(path, data) {
      const response = await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
      const result = await response.json(); if(!response.ok) throw new Error(result.error || 'Sign-in failed'); return result;
    }
    if(ssoButton) ssoButton.addEventListener('click', async () => {
      try {
        const oauth = {}; const query = new URLSearchParams();
        for(const key of oauthFields) {const value=form.elements[key].value; oauth[key]=[value];query.set(key,value);}
        if(location.origin!==ssoOrigin) { location.assign(ssoOrigin+'/oauth/authorize?'+query.toString());return; }
        const verifier=bufferToB64url(crypto.getRandomValues(new Uint8Array(32)));
        const challenge=bufferToB64url(await crypto.subtle.digest('SHA-256',new TextEncoder().encode(verifier)));
        const begun=await ssoJSON('/api/auth/oidc/mcp/begin',{client:'mcp',challenge,oauth});
        sessionStorage.setItem('cantinarr_oidc_mcp_'+begun.flow,verifier);
        location.assign(begun.start_url);
      } catch(error) {setStatus(error.message);}
    });
    (async () => {
      const q=new URLSearchParams(location.search),code=q.get('oidc_code'),flow=q.get('oidc_flow');
      if(!code||!flow)return;
      q.delete('oidc_code');q.delete('oidc_flow');history.replaceState(null,'',location.pathname+'?'+q.toString());
      const key='cantinarr_oidc_mcp_'+flow,verifier=sessionStorage.getItem(key);sessionStorage.removeItem(key);
      if(!verifier){setStatus('This sign-in started in another tab or has expired. Please start again.');return;}
      try {
        const result=await ssoJSON('/api/auth/oidc/exchange',{code,flow,verifier});
        if(!result.consent)throw new Error('Please start sign-in again.');
        document.getElementById('oidcConsent').value=result.consent;
        for(const id of ['username','password']){const field=document.getElementById(id);field.required=false;field.disabled=true;}
        if(ssoButton)ssoButton.disabled=true;passkeyButton.disabled=true;
        setStatus('Signed in as '+result.username+'. Select Authorize to grant this MCP client access.');
      } catch(error) {setStatus(error.message);}
    })();

    const plexButton=document.getElementById('plexButton'),plexKey='cantinarr_plex_mcp';
    let plexPending=null,plexTimer=null,plexChecking=false,plexBeginning=false,plexGeneration=0;
    function plexOAuth() {const oauth={};for(const key of oauthFields)oauth[key]=[form.elements[key].value];return oauth;}
    function sameOAuth(a,b) {return oauthFields.every(key=>JSON.stringify(a[key])===JSON.stringify(b[key]));}
    function plexURL(value) {const u=new URL(value);if(u.origin!=='https://app.plex.tv'||u.pathname!=='/auth'||u.username||u.password)throw new Error('Unexpected Plex address.');return u.href;}
    function plexControls() {document.getElementById('plexControls').hidden=!plexPending;plexButton.textContent=plexPending?'Retry Plex sign-in':{{.PlexLabel}};}
    function clearPlex() {plexPending=null;sessionStorage.removeItem(plexKey);clearInterval(plexTimer);if(plexButton)plexControls();}
    async function cancelPlex() {plexGeneration++;const p=plexPending;clearPlex();if(p)try{await ssoJSON('/api/auth/plex/cancel',{flow:p.flow,verifier:p.verifier});}catch(_){} }
    async function checkPlex() {
      if(!plexPending||plexChecking)return;
      const p=plexPending;
      if(Date.now()>=Date.parse(p.expires_at)){clearPlex();setStatus('Plex sign-in expired. Please try again.');return;}
      plexChecking=true;
      try {
        const proof={flow:p.flow,verifier:p.verifier};
        const checked=await ssoJSON('/api/auth/plex/check',proof);
        if(!plexPending||plexPending.flow!==p.flow||checked.status==='pending')return;
        const result=await ssoJSON('/api/auth/plex/exchange',{...proof,code:checked.code});
        if(!plexPending||plexPending.flow!==p.flow)return;
        if(!result.consent)throw new Error('Please start Plex sign-in again.');
        clearPlex();
        document.getElementById('plexConsent').value=result.consent;
        for(const id of ['username','password']){const field=document.getElementById(id);field.required=false;field.disabled=true;}
        if(ssoButton)ssoButton.disabled=true;plexButton.disabled=true;passkeyButton.disabled=true;
        setStatus('Signed in as '+result.username+'. Select Authorize to grant this MCP client access.');
      }catch(error){clearInterval(plexTimer);setStatus(error.message);}
      finally{plexChecking=false;}
    }
    if(plexButton){
      plexButton.addEventListener('click',async()=>{
        if(plexBeginning)return;plexBeginning=true;plexButton.disabled=true;
        const popup=window.open('about:blank','_blank');if(popup)popup.opener=null;
        try{
          await cancelPlex();
          const generation=plexGeneration;
          const oauth=plexOAuth(),verifier=bufferToB64url(crypto.getRandomValues(new Uint8Array(32)));
          const challenge=await plexChallenge(verifier);
          const begun=await ssoJSON('/api/auth/plex/mcp/begin',{client:'mcp',challenge,oauth});
          const url=plexURL(begun.url);
          if(generation!==plexGeneration){if(popup)popup.close();await ssoJSON('/api/auth/plex/cancel',{flow:begun.flow,verifier});return;}
          plexPending={...begun,url,verifier,oauth};sessionStorage.setItem(plexKey,JSON.stringify(plexPending));plexControls();
          if(popup)popup.location.replace(url);else setStatus('The browser did not open. Select Reopen Plex.');
          plexTimer=setInterval(checkPlex,3000);
        }catch(error){if(popup)popup.close();setStatus(error.message);}
        finally{plexBeginning=false;plexButton.disabled=false;}
      });
      document.getElementById('plexReopen').addEventListener('click',()=>{if(plexPending){const popup=window.open(plexURL(plexPending.url),'_blank','noopener');setStatus('Approve Plex in the browser, then select Check now.');}});
      document.getElementById('plexCheck').addEventListener('click',checkPlex);
      document.getElementById('plexCancel').addEventListener('click',async()=>{await cancelPlex();setStatus('Plex sign-in cancelled.');});
      try{
        const saved=JSON.parse(sessionStorage.getItem(plexKey));
        if(saved&&Date.now()<Date.parse(saved.expires_at)&&sameOAuth(saved.oauth,plexOAuth())){
          plexURL(saved.url);plexPending=saved;plexControls();plexTimer=setInterval(checkPlex,3000);checkPlex();
        }else sessionStorage.removeItem(plexKey);
      }catch(_){sessionStorage.removeItem(plexKey);}
    }

    passkeyButton.addEventListener('click', async () => {
      if (!window.PublicKeyCredential || !navigator.credentials) {
        setStatus('Passkeys are not available in this browser. Create one in the app or another browser.');
        return;
      }
      passkeyButton.disabled = true;
      setStatus('Waiting for passkey...');
      try {
        const begin = await fetch('/oauth/passkey/login/begin', { method: 'POST', credentials: 'same-origin' });
        if (!begin.ok) throw new Error('Could not start passkey login');
        const started = await begin.json();
        const credential = await navigator.credentials.get(prepareRequestOptions(started.options));
        if (!credential) throw new Error('Passkey cancelled');
        const finish = await fetch('/oauth/passkey/login/finish?' + oauthQuery(started.session_id), {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(assertionToJSON(credential)),
        });
        const completed = await finish.json();
        if (!finish.ok) throw new Error(completed.error_description || 'Passkey login failed');
        window.location.href = completed.redirect_uri;
      } catch (err) {
        setStatus(err.message || 'Passkey login failed');
        passkeyButton.disabled = false;
      }
    });
  </script>
</body>
</html>`)

var passkeySetupTemplate = newAuthPageTemplate("passkey-setup", `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Create a Cantinarr Passkey</title>
  {{template "auth-style"}}
</head>
<body>
  <main>
    {{template "auth-brand"}}
    <h1>Create a passkey</h1>
    <p>Open Cantinarr to add a passkey to your account, then return to your MCP client and connect again.</p>
    <a class="button" href="{{.AppURL}}">Open Cantinarr App</a>
    <a class="button secondary" href="{{.BrowserURL}}">Continue in Browser</a>
    {{if .ReturnURL}}<a class="button secondary" href="{{.ReturnURL}}">Back to MCP Login</a>{{end}}
  </main>
  <script>
    setTimeout(() => { window.location.href = '{{.AppURL}}'; }, 250);
  </script>
</body>
</html>`)

var passkeyCreateTemplate = newAuthPageTemplate("passkey-create", `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Create a Cantinarr Passkey</title>
  {{template "auth-style"}}
</head>
<body>
  <main>
    {{template "auth-brand"}}
    <h1>Create a passkey</h1>
    <p>Add a passkey to your Cantinarr account, then return to your MCP client and connect again.</p>
    <label for="name">Name</label>
    <input id="name" value="Passkey" autocomplete="off">
    <button id="createButton" type="button">Create Passkey</button>
    <div id="status" class="status" role="status" aria-live="polite"></div>
  </main>
  <script>
    const setupToken = '{{.Token}}';
    const button = document.getElementById('createButton');
    const status = document.getElementById('status');
    const nameInput = document.getElementById('name');
    function setStatus(text, isError = false) {
      status.textContent = text || '';
      status.className = isError ? 'status error' : 'status';
    }
    function b64urlToBuffer(value) {
      const padded = value.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((value.length + 3) % 4);
      const binary = atob(padded);
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
      return bytes.buffer;
    }
    function bufferToB64url(buffer) {
      const bytes = new Uint8Array(buffer);
      let binary = '';
      for (const byte of bytes) binary += String.fromCharCode(byte);
      return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
    }
    function prepareCreationOptions(options) {
      const publicKey = options.publicKey;
      publicKey.challenge = b64urlToBuffer(publicKey.challenge);
      publicKey.user.id = b64urlToBuffer(publicKey.user.id);
      if (publicKey.excludeCredentials) {
        publicKey.excludeCredentials = publicKey.excludeCredentials.map((cred) => ({
          ...cred,
          id: b64urlToBuffer(cred.id),
        }));
      }
      return { publicKey };
    }
    function credentialToJSON(credential) {
      return {
        id: credential.id,
        rawId: bufferToB64url(credential.rawId),
        type: credential.type,
        response: {
          clientDataJSON: bufferToB64url(credential.response.clientDataJSON),
          attestationObject: bufferToB64url(credential.response.attestationObject),
        },
      };
    }
    button.addEventListener('click', async () => {
      if (!window.PublicKeyCredential || !navigator.credentials) {
        setStatus('Passkeys are not available in this browser.', true);
        return;
      }
      button.disabled = true;
      setStatus('Waiting for passkey...');
      try {
        const begin = await fetch('/api/auth/passkey/setup/begin?token=' + encodeURIComponent(setupToken), { method: 'POST' });
        const started = await begin.json();
        if (!begin.ok) throw new Error(started.error || 'Could not start passkey setup');
        const credential = await navigator.credentials.create(prepareCreationOptions(started.options));
        if (!credential) throw new Error('Passkey setup cancelled');
        const finishURL = '/api/auth/passkey/setup/finish?token=' + encodeURIComponent(setupToken) +
          '&session_id=' + encodeURIComponent(started.session_id) +
          '&credential_name=' + encodeURIComponent(nameInput.value || 'Passkey');
        const finish = await fetch(finishURL, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(credentialToJSON(credential)),
        });
        const completed = await finish.json();
        if (!finish.ok) throw new Error(completed.error || 'Passkey setup failed');
        setStatus('Passkey created. Return to your MCP client and connect again.');
      } catch (err) {
        setStatus(err.message || 'Passkey setup failed', true);
        button.disabled = false;
      }
    });
  </script>
</body>
</html>`)

func (h *OAuthHandler) requestedMCPResource(r *http.Request) string {
	resource := r.Form.Get("resource")
	if resource == "" {
		resource = r.URL.Query().Get("resource")
	}
	if resource == "" {
		return h.mcpResourceURL(r)
	}
	return resource
}

func oauthCodeRedirect(rawRedirectURI, code, state, issuer string) (string, error) {
	redirectURL, err := url.Parse(rawRedirectURI)
	if err != nil {
		return "", err
	}
	q := redirectURL.Query()
	q.Set("code", code)
	if issuer != "" {
		q.Set("iss", issuer)
	}
	if state != "" {
		q.Set("state", state)
	}
	redirectURL.RawQuery = q.Encode()
	return redirectURL.String(), nil
}

func oauthApplicationType(applicationType string) (string, bool) {
	switch applicationType {
	case "", "web":
		return "web", true
	case "native":
		return "native", true
	default:
		return "", false
	}
}

func hasLoopbackHTTPRedirect(redirectURIs []string) bool {
	for _, raw := range redirectURIs {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return true
		}
	}
	return false
}

func (h *OAuthHandler) passkeySetupURL(r *http.Request) string {
	returnTo := h.authorizeReturnURL(r)
	values := url.Values{}
	if returnTo != "" {
		values.Set("return", returnTo)
	}
	if encoded := values.Encode(); encoded != "" {
		return h.baseURL(r) + "/passkeys/setup?" + encoded
	}
	return h.baseURL(r) + "/passkeys/setup"
}

func (h *OAuthHandler) authorizeReturnURL(r *http.Request) string {
	values := url.Values{}
	for _, key := range []string{
		"response_type",
		"client_id",
		"redirect_uri",
		"scope",
		"state",
		"code_challenge",
		"code_challenge_method",
		"resource",
	} {
		if value := r.Form.Get(key); value != "" {
			values.Set(key, value)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return h.baseURL(r) + "/oauth/authorize?" + values.Encode()
}

func (h *OAuthHandler) validRequestedMCPResource(r *http.Request) bool {
	return h.requestedMCPResource(r) == h.mcpResourceURL(r)
}

func (h *OAuthHandler) mcpResourceURL(r *http.Request) string {
	return h.baseURL(r) + "/mcp"
}

func (h *OAuthHandler) baseURL(r *http.Request) string {
	if h.issuer != "" {
		return h.issuer
	}
	return publicBaseURL(r)
}

func publicBaseURL(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
		if r.TLS != nil {
			proto = "https"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return strings.TrimRight(proto+"://"+host, "/")
}

func (h *OAuthHandler) writeMCPUnauthorized(w http.ResponseWriter, r *http.Request) {
	metadataURL := h.baseURL(r) + "/.well-known/oauth-protected-resource/mcp"
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, metadataURL))
	writeOAuthError(w, http.StatusUnauthorized, "invalid_token", "authorization required")
}

func writeOAuthTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrOAuthInvalidClient):
		writeOAuthError(w, http.StatusBadRequest, "invalid_client", "invalid client")
	case errors.Is(err, ErrOAuthInvalidRedirectURI):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid redirect_uri")
	case errors.Is(err, ErrOAuthInvalidCode):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid authorization code")
	case errors.Is(err, ErrOAuthInvalidPKCE):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid code_verifier")
	case errors.Is(err, ErrOAuthInvalidRefreshToken):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh_token")
	case errors.Is(err, ErrOAuthInvalidResource):
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "invalid resource")
	default:
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid grant")
	}
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}

func writeOAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthErrorText(err error) string {
	switch {
	case errors.Is(err, ErrOAuthInvalidClient):
		return "The MCP client is not registered."
	case errors.Is(err, ErrOAuthInvalidRedirectURI):
		return "The MCP client used an invalid redirect URI."
	case errors.Is(err, ErrOAuthInvalidPKCE):
		return "The MCP client did not provide a valid PKCE challenge."
	case errors.Is(err, ErrOAuthInvalidResource):
		return "The MCP client requested an invalid resource."
	default:
		if err != nil {
			return err.Error()
		}
		return ""
	}
}
