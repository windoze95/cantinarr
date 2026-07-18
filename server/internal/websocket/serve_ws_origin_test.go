package websocket

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialWithOrigin is dial with an explicit Origin request header ("" sends
// none, which is what native dart:io clients do).
func (e *wsTestEnv) dialWithOrigin(t *testing.T, token, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(e.server.URL, "http")
	dialer := websocket.Dialer{Subprotocols: []string{"Bearer", token}, HandshakeTimeout: 5 * time.Second}
	var header http.Header
	if origin != "" {
		header = http.Header{"Origin": []string{origin}}
	}
	return dialer.Dial(wsURL, header)
}

// TestServeWSOriginPolicy pins ServeWS's Origin posture (#231). The hub's
// upgrader is the zero-value gorilla Upgrader, so gorilla's default
// CheckOrigin applies: a request with no Origin header (native clients) or
// whose Origin host equals the request Host (the web build, served
// same-origin) upgrades; any other Origin is refused with 403 at the upgrade
// step — even when the caller presents a valid token, so a cross-site page in
// a victim's browser can never open the socket. Overriding CheckOrigin (or
// swapping websocket libraries) is a deliberate change that must flip this
// test.
func TestServeWSOriginPolicy(t *testing.T) {
	env := newWSTestEnv(t)
	token := env.userToken(t, "alice", "hw-alice").AccessToken
	sameOrigin := "http://" + strings.TrimPrefix(env.server.URL, "http://")

	t.Run("absent origin accepted (native clients)", func(t *testing.T) {
		conn, resp, err := env.dialWithOrigin(t, token, "")
		if err != nil {
			t.Fatalf("dial without Origin: %v", err)
		}
		defer conn.Close()
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("status = %d, want 101", resp.StatusCode)
		}
		env.waitForClients(t, 1)
	})
	env.waitForClients(t, 0)

	t.Run("same-origin accepted (web build)", func(t *testing.T) {
		conn, resp, err := env.dialWithOrigin(t, token, sameOrigin)
		if err != nil {
			t.Fatalf("dial with same-origin %q: %v", sameOrigin, err)
		}
		defer conn.Close()
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("status = %d, want 101", resp.StatusCode)
		}
		env.waitForClients(t, 1)
	})
	env.waitForClients(t, 0)

	t.Run("cross-origin refused even with a valid token", func(t *testing.T) {
		conn, resp, err := env.dialWithOrigin(t, token, "https://attacker.example")
		if err == nil {
			conn.Close()
			t.Fatal("cross-origin handshake succeeded, want rejection")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("handshake response = %+v, want HTTP 403", resp)
		}
		if got := len(env.clientsSnapshot()); got != 0 {
			t.Fatalf("cross-origin handshake registered %d client(s), want 0", got)
		}
	})
}
