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
	"github.com/windoze95/cantinarr-server/internal/db"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/radarr"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/sonarr"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
	"github.com/windoze95/cantinarr-server/internal/trakt"
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

	// Auth
	authService := auth.NewService(database, cfg.JWTSecret)
	if err := authService.EnsureAdmin(cfg.AdminPassword); err != nil {
		log.Fatalf("Failed to ensure admin: %v", err)
	}
	authHandler := auth.NewHandler(authService)

	// TMDB (optional)
	var tmdbClient *tmdb.Client
	if cfg.TMDBEnabled() {
		tmdbClient = tmdb.NewClient(cfg.TMDBAccessToken)
	}

	// Trakt (optional)
	var traktClient *trakt.Client
	if cfg.TraktEnabled() {
		traktClient = trakt.NewClient(cfg.TraktClientID)
	}

	// Bridge
	bridge := tmdb.NewBridge(tmdbClient, traktClient, database)

	// Instance store and registry
	instanceStore := instance.NewStore(database)
	seedInstancesFromEnv(instanceStore, cfg)
	registry := instance.NewRegistry(instanceStore)
	instanceHandler := instance.NewHandler(instanceStore, registry)

	// Legacy direct clients (for backward compat, also used as fallback)
	var radarrClient *radarr.Client
	if cfg.RadarrEnabled() {
		radarrClient = radarr.NewClient(cfg.RadarrURL, cfg.RadarrKey)
	}
	var sonarrClient *sonarr.Client
	if cfg.SonarrEnabled() {
		sonarrClient = sonarr.NewClient(cfg.SonarrURL, cfg.SonarrKey)
	}

	// Request service
	requestService := request.NewService(database, registry, radarrClient, sonarrClient, bridge)
	requestHandler := request.NewHandler(requestService)

	// Proxy handler
	proxyHandler := proxy.NewHandler(cfg.RadarrURL, cfg.RadarrKey, cfg.SonarrURL, cfg.SonarrKey, instanceStore)

	// MCP tool server
	toolServer := mcp.NewToolServer(tmdbClient, requestService, registry, radarrClient, sonarrClient)

	// AI service
	var aiService *ai.Service
	if cfg.AIEnabled() {
		aiService = ai.NewService(cfg.AnthropicKey, toolServer)
	}
	aiHandler := ai.NewHandler(aiService)

	// Discover handler (caching proxy for TMDB/Trakt)
	apiCache := cache.New()
	defer apiCache.Close()
	var discoverHandler *discover.Handler
	if tmdbClient != nil {
		discoverHandler = discover.NewHandler(tmdbClient, traktClient, apiCache, cfg)
	}

	// WebSocket hub
	wsHub := ws.NewHub(authService, registry, instanceStore, radarrClient, sonarrClient, cfg.AllowedOrigins)
	go wsHub.Run(context.Background())

	// Router
	router := api.NewRouter(cfg, authHandler, authService, requestHandler, proxyHandler, wsHub, aiHandler, discoverHandler, instanceHandler, instanceStore)

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

// seedInstancesFromEnv creates default service instances from environment variables
// if the service_instances table is empty and env vars are set.
func seedInstancesFromEnv(store *instance.Store, cfg *config.Config) {
	all, err := store.ListAll()
	if err != nil {
		log.Printf("Warning: could not check existing instances: %v", err)
		return
	}
	if len(all) > 0 {
		return // Already have instances, don't seed
	}

	if cfg.RadarrEnabled() {
		inst := &instance.Instance{
			ID:          "radarr-default",
			ServiceType: "radarr",
			Name:        "Movies",
			URL:         cfg.RadarrURL,
			APIKey:      cfg.RadarrKey,
			IsDefault:   true,
			SortOrder:   0,
		}
		if err := store.Create(inst); err != nil {
			log.Printf("Warning: failed to seed Radarr instance: %v", err)
		} else {
			log.Println("Seeded default Radarr instance from env vars")
		}
	}

	if cfg.SonarrEnabled() {
		inst := &instance.Instance{
			ID:          "sonarr-default",
			ServiceType: "sonarr",
			Name:        "TV Shows",
			URL:         cfg.SonarrURL,
			APIKey:      cfg.SonarrKey,
			IsDefault:   true,
			SortOrder:   0,
		}
		if err := store.Create(inst); err != nil {
			log.Printf("Warning: failed to seed Sonarr instance: %v", err)
		} else {
			log.Println("Seeded default Sonarr instance from env vars")
		}
	}
}
