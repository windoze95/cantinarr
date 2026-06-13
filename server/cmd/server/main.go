package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
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
	secretSettings := append([]string{"jwt_secret"}, credentials.AllKeys...)
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
	authService := auth.NewService(database, cfg.JWTSecret)
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

	// Request service
	requestService := request.NewService(database, registry, bridge)
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

	// WebSocket hub
	wsHub := ws.NewHub(authService, registry, instanceStore)
	go wsHub.Run(context.Background())

	// Router
	router := api.NewRouter(cfg, authHandler, authService, requestHandler, proxyHandler, wsHub, aiHandler, discoverHandler, instanceHandler, instanceStore, downloadsHandler, tautulliHandler, creds, credHandler, toolServer)

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
