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
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/request"
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

	// Resolve JWT secret: env var > DB > generate and persist
	if cfg.JWTSecret == "" {
		cfg.JWTSecret, err = ensureJWTSecret(database)
		if err != nil {
			log.Fatalf("Failed to resolve JWT secret: %v", err)
		}
	}

	// Credentials registry (lazy-creates TMDB/Trakt clients from DB)
	creds := credentials.NewRegistry(database)
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
	instanceStore := instance.NewStore(database)
	registry := instance.NewRegistry(instanceStore)
	instanceHandler := instance.NewHandler(instanceStore, registry)

	// Request service
	requestService := request.NewService(database, registry, bridge)
	requestHandler := request.NewHandler(requestService)

	// Proxy handler
	proxyHandler := proxy.NewHandler(instanceStore)

	// MCP tool server + AI handler
	toolServer := mcp.NewToolServer(creds, requestService, registry)
	aiHandler := ai.NewHandler(creds, toolServer)

	// Discover handler (always created — checks credentials at request time)
	apiCache := cache.New()
	defer apiCache.Close()
	discoverHandler := discover.NewHandler(creds, apiCache)

	// WebSocket hub
	wsHub := ws.NewHub(authService, registry, instanceStore)
	go wsHub.Run(context.Background())

	// Router
	router := api.NewRouter(cfg, authHandler, authService, requestHandler, proxyHandler, wsHub, aiHandler, discoverHandler, instanceHandler, instanceStore, creds, credHandler, toolServer)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Cantinarr server starting on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// ensureJWTSecret loads the JWT secret from the settings table, or generates
// and persists a new one. This ensures tokens survive server restarts.
func ensureJWTSecret(database *sql.DB) (string, error) {
	var secret string
	err := database.QueryRow("SELECT value FROM settings WHERE key = 'jwt_secret'").Scan(&secret)
	if err == nil {
		return secret, nil
	}

	// Generate a new secret
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	secret = hex.EncodeToString(b)

	_, err = database.Exec("INSERT INTO settings (key, value) VALUES ('jwt_secret', ?)", secret)
	if err != nil {
		return "", fmt.Errorf("persist secret: %w", err)
	}

	log.Println("Generated and persisted JWT secret")
	return secret, nil
}

