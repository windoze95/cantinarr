package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// PublicClientID identifies Cantinarr's registered Mobile, desktop, or CLI
// app. This is public metadata, not a secret. The only allowed scope is the
// public catalog; Chaptarr continues to manage its own metadata credential.
const PublicClientID = "1c18273c-b4d4-4297-aeac-289697c1e525"
const CatalogScope = "read:catalog:data"
const DeviceEndpoint = "https://api.hardcover.app/oauth2/device"
const TokenEndpoint = "https://api.hardcover.app/oauth2/token"

var (
	ErrProvider     = errors.New("Hardcover could not complete the request; try again")
	ErrPending      = errors.New("authorization_pending")
	ErrSlowDown     = errors.New("slow_down")
	ErrDenied       = errors.New("access_denied")
	ErrExpired      = errors.New("expired_token")
	ErrInvalidGrant = errors.New("invalid_grant")
)

// OAuthTokens is server-only and is encrypted as one document at rest.
type OAuthTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

// OAuthClient is independent of the Grok provider. Endpoint overrides are
// test seams; production always uses Hardcover and the public Cantinarr app.
type OAuthClient struct {
	DeviceURL, TokenURL string
	HTTP                *http.Client
	Now                 func() time.Time
}

func NewOAuthClient() *OAuthClient {
	return &OAuthClient{DeviceURL: DeviceEndpoint, TokenURL: TokenEndpoint,
		HTTP: &http.Client{Transport: httpx.External(), Timeout: 20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Now: time.Now}
}

func (c *OAuthClient) post(ctx context.Context, endpoint string, form url.Values, out any) error {
	form.Set("client_id", PublicClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrProvider
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ErrProvider
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ErrProvider
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &problem)
		// Only an explicit protocol rejection is permanent. HTML, 5xx, rate
		// limits and malformed replies never erase a working connection.
		if resp.StatusCode == http.StatusBadRequest {
			switch problem.Error {
			case "authorization_pending":
				return ErrPending
			case "slow_down":
				return ErrSlowDown
			case "access_denied":
				return ErrDenied
			case "expired_token":
				return ErrExpired
			case "invalid_grant":
				return ErrInvalidGrant
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return ErrSlowDown
		}
		return ErrProvider
	}
	if json.Unmarshal(body, out) != nil {
		return ErrProvider
	}
	return nil
}

// TrustedVerificationURI refuses credentials, alternate ports and lookalike
// hosts. The app makes the same check before launching a browser.
func TrustedVerificationURI(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "hardcover.app" && u.User == nil && u.Path == "/link" && u.Fragment == ""
}

func (c *OAuthClient) Begin(ctx context.Context) (DeviceCode, error) {
	var code DeviceCode
	err := c.post(ctx, c.DeviceURL, url.Values{"scope": {CatalogScope}}, &code)
	if err != nil {
		return code, err
	}
	if code.DeviceCode == "" || len(code.DeviceCode) > 4096 || code.UserCode == "" || len(code.UserCode) > 64 || strings.ContainsAny(code.UserCode, "\r\n\t") || !TrustedVerificationURI(code.VerificationURI) || code.ExpiresIn <= 0 || code.ExpiresIn > 86400 || code.Interval < 0 || code.Interval > 86400 {
		return DeviceCode{}, ErrProvider
	}
	if code.Interval == 0 {
		code.Interval = 5
	}
	return code, nil
}

func (c *OAuthClient) token(ctx context.Context, form url.Values) (OAuthTokens, error) {
	var reply struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := c.post(ctx, c.TokenURL, form, &reply); err != nil {
		return OAuthTokens{}, err
	}
	if reply.AccessToken == "" || len(reply.AccessToken) > 16384 || len(reply.RefreshToken) > 16384 || strings.ContainsAny(reply.AccessToken, " \r\n\t") || !strings.EqualFold(reply.TokenType, "bearer") || reply.ExpiresIn <= 0 || reply.ExpiresIn > 366*86400 {
		return OAuthTokens{}, ErrProvider
	}
	if reply.Scope != "" && reply.Scope != CatalogScope {
		return OAuthTokens{}, ErrInsufficientScope
	}
	return OAuthTokens{AccessToken: reply.AccessToken, RefreshToken: reply.RefreshToken, ExpiresAt: c.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)}, nil
}

func (c *OAuthClient) Poll(ctx context.Context, deviceCode string) (OAuthTokens, error) {
	return c.token(ctx, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {deviceCode}})
}
func (c *OAuthClient) Refresh(ctx context.Context, refreshToken string) (OAuthTokens, error) {
	return c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}
