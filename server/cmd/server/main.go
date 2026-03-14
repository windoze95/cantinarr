package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/api"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/mcp"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/db"
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

	// Auth
	authService := auth.NewService(database, cfg.JWTSecret)
	if err := authService.EnsureAdmin(cfg.AdminPassword); err != nil {
		log.Fatalf("Failed to ensure admin: %v", err)
	}
	authHandler := auth.NewHandler(authService)

	// TMDB
	tmdbClient := tmdb.NewClient(cfg.TMDBKey)

	// Trakt (optional)
	var traktClient *trakt.Client
	if cfg.TraktEnabled() {
		traktClient = trakt.NewClient(cfg.TraktClientID)
	}

	// Bridge
	bridge := tmdb.NewBridge(tmdbClient, traktClient, database)

	// Radarr (optional)
	var radarrClient *radarr.Client
	if cfg.RadarrEnabled() {
		radarrClient = radarr.NewClient(cfg.RadarrURL, cfg.RadarrKey)
	}

	// Sonarr (optional)
	var sonarrClient *sonarr.Client
	if cfg.SonarrEnabled() {
		sonarrClient = sonarr.NewClient(cfg.SonarrURL, cfg.SonarrKey)
	}

	// Request service
	requestService := request.NewService(database, radarrClient, sonarrClient, tmdbClient, bridge)
	requestHandler := request.NewHandler(requestService)

	// Proxy handler
	proxyHandler := proxy.NewHandler(cfg.RadarrURL, cfg.RadarrKey, cfg.SonarrURL, cfg.SonarrKey)

	// MCP tool server
	toolServer := mcp.NewToolServer(tmdbClient, requestService, radarrClient, sonarrClient)

	// AI service
	var aiService *ai.Service
	if cfg.AIEnabled() {
		aiService = ai.NewService(cfg.AnthropicKey, toolServer)
	}
	aiHandler := ai.NewHandler(aiService)

	// WebSocket hub
	wsHub := ws.NewHub(authService, radarrClient, sonarrClient)
	go wsHub.Run(context.Background())

	// Router
	router := api.NewRouter(cfg, authHandler, authService, requestHandler, proxyHandler, wsHub, aiHandler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Cantinarr server starting on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
