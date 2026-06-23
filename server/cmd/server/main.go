package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/api"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/cache"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/credentials"
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/downloads"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/push"
	"github.com/windoze95/cantinarr-server/internal/remediation"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/secrets"
	"github.com/windoze95/cantinarr-server/internal/tautulli"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Ensure DB directory exists
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		log.Fatalf("Failed to create DB directory: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Secrets-at-rest cipher: env key > key file next to the DB
	encKey, err := secrets.LoadKey(cfg.EncryptionKeyFile)
	if err != nil {
		log.Fatalf("Failed to resolve encryption key: %v", err)
	}
	cipher, err := secrets.NewCipher(encKey)
	if err != nil {
		log.Fatalf("Failed to initialize secrets cipher: %v", err)
	}
	if err := secrets.VerifyKeyIdentity(database, cipher); err != nil {
		log.Fatalf("Encryption key check failed: %v", err)
	}
	secretSettings := append([]string{"jwt_secret", "push_api_key"}, credentials.AllKeys...)
	if n, err := secrets.EncryptExisting(database, cipher, secretSettings); err != nil {
		log.Fatalf("Failed to encrypt existing secrets: %v", err)
	} else if n > 0 {
		log.Printf("Encrypted %d existing secret value(s) at rest", n)
	}

	// Resolve JWT secret: env var > DB > generate and persist
	if cfg.JWTSecret == "" {
		cfg.JWTSecret, err = ensureJWTSecret(database, cipher)
		if err != nil {
			log.Fatalf("Failed to resolve JWT secret: %v", err)
		}
	}

	// Credentials registry (lazy-creates TMDB/Trakt clients from DB)
	creds := credentials.NewRegistry(database, cipher)
	credHandler := credentials.NewHandler(creds)

	// Auth
	authService := auth.NewService(database, cfg.JWTSecret, auth.WebAuthnConfig{
		ExtraOrigins:            cfg.WebAuthnExtraOrigins,
		AppleAppIDs:             cfg.AppleAppIDs,
		AndroidCertFingerprints: cfg.AndroidCertFingerprints,
	})
	if err := authService.MigrateSetupState(); err != nil {
		log.Fatalf("Failed to migrate setup state: %v", err)
	}
	authHandler := auth.NewHandler(authService)

	// Bridge (uses credentials registry for TMDB/Trakt clients)
	bridge := tmdb.NewBridge(creds, database)

	// Instance store and registry
	instanceStore := instance.NewStore(database, cipher)
	registry := instance.NewRegistry(instanceStore)
	instanceHandler := instance.NewHandler(instanceStore, registry)

	// Downloads handler (SABnzbd / qBittorrent / NZBGet / Transmission queue management)
	downloadsHandler := downloads.NewHandler(instanceStore, registry)

	// Tautulli handler (Plex monitoring)
	tautulliHandler := tautulli.NewHandler(instanceStore, registry)

	// Server-lifetime context: drives the WebSocket hub and the push manager's
	// background enrollment retry.
	ctx := context.Background()
	logger := slog.Default()

	// Push notifications via the self-hosted gateway. A single push.Manager owns
	// the lazily-built gateway client: the key is resolved (explicit env key > a
	// key auto-enrolled on a previous start > self-enroll) on first use, and a
	// gateway that was down at boot — or tokens registered while push was off —
	// self-heal without a restart. The manager is nil (and push disabled) only
	// when no gateway URL is set; this keeps the hub's content notifier and the
	// request composite off exactly as before.
	var pushManager *push.Manager
	if cfg.PushGatewayURL != "" {
		pushManager = push.NewManager(database, cipher, cfg.PushGatewayURL, cfg.PushAPIKey, cfg.PushEnrollToken, cfg.ServerName, logger)
		// Try once now (non-blocking) and keep retrying in the background until
		// the gateway is reachable; both are no-ops once enrolled.
		go pushManager.Ensure(ctx)
		pushManager.StartRetry(ctx)
		log.Printf("Push notifications enabled via %s", cfg.PushGatewayURL)
	} else {
		log.Println("Push notifications disabled (CANTINARR_PUSH_GATEWAY_URL unset)")
	}
	pushHandler := push.NewHandler(database, pushManager, logger)

	// One push notifier drives both the request-decision/pending fan-out and the
	// new-content (movie/episode available) pushes. It is built only when push is
	// configured (gateway URL set) so the hub's content notifier and the request
	// composite stay off otherwise; it no-ops on its own while the gateway is
	// unreachable (manager.Client() == nil).
	var pushNotifier *push.Notifier
	if pushManager != nil {
		pushNotifier = push.NewNotifier(database, pushManager, logger)
	}

	// WebSocket hub (built before the request service so request approvals and
	// denials can push realtime events to the requester). The push notifier is
	// passed so download completions also fan out to opted-in devices; passing
	// an untyped nil keeps new-content pushes off when push is disabled.
	var contentNotifier ws.ContentNotifier
	if pushNotifier != nil {
		contentNotifier = pushNotifier
	}
	// The auto-dispatch opener is wired after the remediation service exists (it
	// depends on the notifier composite, which depends on this hub). The hub's
	// poll loop is therefore started LATER, once SetIssueOpener has run.
	wsHub := ws.NewHub(authService, registry, instanceStore, contentNotifier, nil)

	// Notifier composite. Request decisions and issue events fan out to both the
	// WebSocket hub (live clients) and the push gateway (offline devices). The
	// push notifier is only added to the fan-out when push is configured. The
	// concrete *push.Composite satisfies both request.Notifier and
	// remediation.Notifier, so the same fan-out drives both.
	var notifier *push.Composite
	if pushNotifier != nil {
		notifier = push.NewComposite(wsHub, pushNotifier)
	} else {
		notifier = push.NewComposite(wsHub)
	}
	requestService := request.NewService(database, registry, bridge, notifier)
	requestHandler := request.NewHandler(requestService)

	// Remediation (issue reporting) service + handler. Records/threads issues, runs
	// the read-only agent, and (Wave 5) accepts auto-dispatched issues from the
	// poller. AutoDispatch ships OFF; the opener re-checks the live toggle per call.
	remediationService := remediation.NewService(database, registry, bridge, notifier)
	remediationHandler := remediation.NewHandler(remediationService)

	// Proxy handler
	proxyHandler := proxy.NewHandler(instanceStore)

	// MCP tool server + AI handler
	toolServer := mcp.NewToolServer(creds, requestService, registry, bridge)
	aiHandler := ai.NewHandler(creds, toolServer)

	// Remediation read-only agent (Wave 2). The agent investigates one issue
	// read-only and posts a diagnosis; it has NO path to mutate the *arr (the
	// Runner's hardcoded read-tool allow-list is the enforcement boundary). Inject
	// the remediation write surface into the tool server so the agent-only tools
	// (post_issue_message / conclude_issue) can record findings, then start a
	// small bounded worker pool that drains enqueued investigation jobs.
	toolServer.SetIssueStore(remediationService)
	remediationProcToken := newProcToken()
	remediationRunner := remediation.NewRunner(database, remediationService, toolServer, creds, remediationProcToken)
	remediationService.StartWorkers(ctx, remediationRunner, 2)
	// Reply-TTL sweep (Wave 4): periodically close awaiting_user issues whose
	// reporter never answered the agent's clarifying question within the window.
	remediationService.StartReplyTTLSweeper(ctx)

	// Auto-dispatch (Wave 5, highest-risk trigger, ships OFF). The hub's poller
	// hands stuck/blocked downloads to this opener, which re-checks the live
	// Enabled && AutoDispatch toggles, opens a deduped issue, and enqueues the
	// Runner off the poll goroutine. Wire it before starting the hub's poll loop
	// (SetIssueOpener is not safe to call once Run is polling). Passing the opener
	// only here keeps a server with the feature unwired from ever fetching the
	// detailed queue.
	wsHub.SetIssueOpener(remediation.NewAutoDispatcher(remediationService))
	go wsHub.Run(ctx)

	// Discover handler (always created — checks credentials at request time)
	apiCache := cache.New()
	defer apiCache.Close()
	discoverHandler := discover.NewHandler(creds, apiCache)

	// Router
	router := api.NewRouter(cfg, authHandler, authService, requestHandler, remediationService, remediationHandler, proxyHandler, wsHub, aiHandler, discoverHandler, instanceHandler, instanceStore, downloadsHandler, tautulliHandler, creds, credHandler, toolServer, pushHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Cantinarr server starting on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// newProcToken returns a random per-process token stamped on each agent_runs row
// so a future watchdog can distinguish a run crashed mid-investigation (its token
// != the current process token) from one parked by design. Best-effort: a random
// failure falls back to a constant, which only weakens crash detection, not
// correctness.
func newProcToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "proc"
	}
	return hex.EncodeToString(b)
}

// ensureJWTSecret loads the JWT secret from the settings table, or generates
// and persists a new one. This ensures tokens survive server restarts.
func ensureJWTSecret(database *sql.DB, cipher *secrets.Cipher) (string, error) {
	var stored string
	err := database.QueryRow("SELECT value FROM settings WHERE key = 'jwt_secret'").Scan(&stored)
	if err == nil {
		return cipher.Decrypt(stored)
	}

	// Generate a new secret
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	secret := hex.EncodeToString(b)

	enc, err := cipher.Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("encrypt secret: %w", err)
	}
	_, err = database.Exec("INSERT INTO settings (key, value) VALUES ('jwt_secret', ?)", enc)
	if err != nil {
		return "", fmt.Errorf("persist secret: %w", err)
	}

	log.Println("Generated and persisted JWT secret")
	return secret, nil
}
