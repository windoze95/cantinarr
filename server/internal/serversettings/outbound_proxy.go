package serversettings

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/httpx"
)

// KeyOutboundProxyURL is the settings row holding the outbound proxy as one
// credential-bearing URL (scheme://user:pass@host:port), encrypted at rest
// because the password is a secret. It is its own row rather than a field of
// the server_settings blob so the blob stays plaintext and this value alone
// rides the cipher.
const KeyOutboundProxyURL = "outbound_proxy_url"

// OutboundProxy is the admin's outbound proxy as the API and the app see it:
// the address without credentials, the username, and the password, which is
// write-only past this package (the API reports only whether one is stored).
type OutboundProxy struct {
	// URL is scheme://host:port with no userinfo; "" means no proxy.
	URL      string
	Username string
	Password string
}

// Configured reports whether a proxy address is set.
func (p OutboundProxy) Configured() bool { return p.URL != "" }

// ProxyURL composes the credential-bearing URL the transport dials through,
// "" when no proxy is configured.
func (p OutboundProxy) ProxyURL() string {
	if p.URL == "" {
		return ""
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		return ""
	}
	switch {
	case p.Username != "" && p.Password != "":
		u.User = url.UserPassword(p.Username, p.Password)
	case p.Username != "":
		u.User = url.User(p.Username)
	}
	return u.String()
}

// OutboundProxy reads the stored proxy. A row that cannot be decrypted or
// parsed is an error, never an empty proxy: the caller decides whether that is
// fatal (boot) or a 500 (the admin API), but it must not silently turn into
// direct egress.
func (s *Service) OutboundProxy() (OutboundProxy, error) {
	var stored string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", KeyOutboundProxyURL).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboundProxy{}, nil
	}
	if err != nil {
		return OutboundProxy{}, fmt.Errorf("read outbound proxy: %w", err)
	}
	plain := stored
	if s.cipher != nil {
		if plain, err = s.cipher.Decrypt(stored); err != nil {
			return OutboundProxy{}, fmt.Errorf("decrypt outbound proxy: %w", err)
		}
	}
	if plain == "" {
		return OutboundProxy{}, nil
	}
	return parseOutboundProxy(plain)
}

// parseOutboundProxy splits a stored credential-bearing URL back into the
// address and its credentials, re-validating the address so a hand-edited row
// cannot install something the transport would misread.
func parseOutboundProxy(full string) (OutboundProxy, error) {
	u, err := url.Parse(full)
	if err != nil {
		return OutboundProxy{}, fmt.Errorf("parse outbound proxy: %w", err)
	}
	var out OutboundProxy
	if u.User != nil {
		out.Username = u.User.Username()
		out.Password, _ = u.User.Password()
		u.User = nil
	}
	if out.URL, err = normalizeProxyURL(u.String()); err != nil {
		return OutboundProxy{}, fmt.Errorf("stored outbound proxy: %w", err)
	}
	return out, nil
}

// ResolveOutboundProxy validates an admin's submission and merges it with what
// is stored, without saving: both the save and the Test button run it, so the
// proxy that gets tested is exactly the one that would be saved. An empty
// address clears everything. A blank password keeps the stored one when the
// username is unchanged -- the same write-only convention as instance
// credentials -- so an admin can retest or move the address without retyping
// a secret they can no longer see.
func (s *Service) ResolveOutboundProxy(in OutboundProxy) (OutboundProxy, error) {
	address, err := normalizeProxyURL(in.URL)
	if err != nil {
		return OutboundProxy{}, err
	}
	if address == "" {
		return OutboundProxy{}, nil
	}
	out := OutboundProxy{URL: address, Username: strings.TrimSpace(in.Username), Password: in.Password}
	if out.Username == "" {
		out.Password = ""
		return out, nil
	}
	if out.Password == "" {
		stored, err := s.OutboundProxy()
		if err != nil {
			return OutboundProxy{}, err
		}
		if stored.Username == out.Username {
			out.Password = stored.Password
		}
	}
	return out, nil
}

// SetOutboundProxy resolves and stores the proxy, deleting the row when the
// address is cleared, and returns what was stored.
func (s *Service) SetOutboundProxy(in OutboundProxy) (OutboundProxy, error) {
	resolved, err := s.ResolveOutboundProxy(in)
	if err != nil {
		return OutboundProxy{}, err
	}
	if !resolved.Configured() {
		if _, err := s.db.Exec("DELETE FROM settings WHERE key = ?", KeyOutboundProxyURL); err != nil {
			return OutboundProxy{}, fmt.Errorf("clear outbound proxy: %w", err)
		}
		return OutboundProxy{}, nil
	}
	value := resolved.ProxyURL()
	if s.cipher != nil {
		if value, err = s.cipher.Encrypt(value); err != nil {
			return OutboundProxy{}, fmt.Errorf("encrypt outbound proxy: %w", err)
		}
	}
	if _, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", KeyOutboundProxyURL, value); err != nil {
		return OutboundProxy{}, fmt.Errorf("save outbound proxy: %w", err)
	}
	return resolved, nil
}

var errProxyAddress = errors.New("proxy address must be an http, https, socks5, or socks5h URL such as http://proxy:8118, with no path or credentials")

// normalizeProxyURL accepts "" (no proxy) or a bare proxy address: a scheme
// http.Transport can dial through, a host, and nothing else. Credentials go in
// their own fields, never in the address, so the app never has to round-trip
// a password through a text field.
func normalizeProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || !httpx.SupportedProxyScheme(u.Scheme) || u.Hostname() == "" {
		return "", errProxyAddress
	}
	if u.User != nil {
		return "", errors.New("proxy address must not carry credentials; use the username and password fields")
	}
	if u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", errProxyAddress
	}
	u.Path, u.RawPath = "", ""
	return u.String(), nil
}
