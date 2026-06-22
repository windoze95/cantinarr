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

	// Push notifications via the self-hosted gateway. Disabled (client nil) when
	// either the gateway URL or API key is unset; the handler and notifier are
	// still built (nil-safe) so wiring stays uniform. Built before the hub and
	// request service so both can dispatch pushes.
	var pushClient *push.Client
	if cfg.PushGatewayURL != "" {
		// Resolve the gateway key: explicit env key, else a key auto-enrolled on a
		// previous start, else self-enroll now. Failure is non-fatal — push stays
		// off this run and is retried on the next start.
		apiKey, err := ensurePushAPIKey(database, cipher, cfg)
		if err != nil {
			log.Printf("Push notifications disabled: %v", err)
		} else {
			pushClient = push.NewClient(cfg.PushGatewayURL, apiKey)
			log.Printf("Push notifications enabled via %s", cfg.PushGatewayURL)
		}
	} else {
		log.Println("Push notifications disabled (CANTINARR_PUSH_GATEWAY_URL unset)")
	}
	logger := slog.Default()
	pushHandler := push.NewHandler(database, pushClient, logger)

	// One push notifier drives both the request-decision/pending fan-out and the
	// new-content (movie/episode available) pushes. nil when push is disabled so
	// the hub's content notifier and the request composite both no-op.
	var pushNotifier *push.Notifier
	if pushClient != nil {
		pushNotifier = push.NewNotifier(database, pushClient, logger)
	}

	// WebSocket hub (built before the request service so request approvals and
	// denials can push realtime events to the requester). The push notifier is
	// passed so download completions also fan out to opted-in devices; passing
	// an untyped nil keeps new-content pushes off when push is disabled.
	var contentNotifier ws.ContentNotifier
	if pushNotifier != nil {
		contentNotifier = pushNotifier
	}
	wsHub := ws.NewHub(authService, registry, instanceStore, contentNotifier)
	go wsHub.Run(context.Background())

	// Request service. Request decisions fan out to both the WebSocket hub
	// (live clients) and the push gateway (offline devices). The push notifier
	// is only added to the fan-out when push is configured.
	var notifier request.Notifier
	if pushNotifier != nil {
		notifier = push.NewComposite(wsHub, pushNotifier)
	} else {
		notifier = push.NewComposite(wsHub)
	}
	requestService := request.NewService(database, registry, bridge, notifier)
	requestHandler := request.NewHandler(requestService)

	// Proxy handler
	proxyHandler := proxy.NewHandler(instanceStore)

	// MCP tool server + AI handler
	toolServer := mcp.NewToolServer(creds, requestService, registry, bridge)
	aiHandler := ai.NewHandler(creds, toolServer)

	// Discover handler (always created — checks credentials at request time)
	apiCache := cache.New()
	defer apiCache.Close()
	discoverHandler := discover.NewHandler(creds, apiCache)

	// Router
	router := api.NewRouter(cfg, authHandler, authService, requestHandler, proxyHandler, wsHub, aiHandler, discoverHandler, instanceHandler, instanceStore, downloadsHandler, tautulliHandler, creds, credHandler, toolServer, pushHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Cantinarr server starting on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
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

// ensurePushAPIKey resolves the push-gateway API key when a gateway URL is set:
// an explicit CANTINARR_PUSH_API_KEY wins; otherwise a key auto-enrolled on a
// previous start is loaded from the settings table; otherwise the server self-
// enrolls with the gateway once and persists the issued key (encrypted at rest,
// like the JWT secret). This gives self-hosters push with zero manual key
// handling. To force re-enrollment, delete the 'push_api_key' settings row.
func ensurePushAPIKey(database *sql.DB, cipher *secrets.Cipher, cfg *config.Config) (string, error) {
	if cfg.PushAPIKey != "" {
		return cfg.PushAPIKey, nil // explicit operator override; not persisted
	}

	var stored string
	if err := database.QueryRow("SELECT value FROM settings WHERE key = 'push_api_key'").Scan(&stored); err == nil {
		return cipher.Decrypt(stored)
	}

	// No key yet: self-enroll with the gateway and persist the issued key.
	name := cfg.ServerName
	if name == "" {
		name = "Cantinarr"
	}
	res, err := push.Enroll(cfg.PushGatewayURL, name, cfg.PushEnrollToken)
	if err != nil {
		return "", fmt.Errorf("auto-enroll with push gateway: %w", err)
	}
	enc, err := cipher.Encrypt(res.APIKey)
	if err != nil {
		return "", fmt.Errorf("encrypt push key: %w", err)
	}
	if _, err := database.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('push_api_key', ?)", enc); err != nil {
		return "", fmt.Errorf("persist push key: %w", err)
	}
	log.Printf("Auto-enrolled with push gateway %s (tenant %s); key persisted", cfg.PushGatewayURL, res.TenantID)
	return res.APIKey, nil
}
