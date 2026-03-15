package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/windoze95/cantinarr-server/internal/ai"
	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/config"
	"github.com/windoze95/cantinarr-server/internal/discover"
	"github.com/windoze95/cantinarr-server/internal/proxy"
	"github.com/windoze95/cantinarr-server/internal/request"
	"github.com/windoze95/cantinarr-server/internal/web"
	ws "github.com/windoze95/cantinarr-server/internal/websocket"
)

func NewRouter(
	cfg *config.Config,
	authHandler *auth.Handler,
	authService *auth.Service,
	requestHandler *request.Handler,
	proxyHandler *proxy.Handler,
	wsHub *ws.Hub,
	aiHandler *ai.Handler,
	discoverHandler *discover.Handler,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.SetHeader("Content-Type", "application/json"))

		// WebSocket (auth handled via subprotocol header)
		r.Get("/ws", wsHub.ServeWS)

		// Health check
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})

		// Auth routes (public)
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", authHandler.Login)
			r.Post("/register", authHandler.Register)
			r.Post("/refresh", authHandler.Refresh)

			// Protected auth routes
			r.Group(func(r chi.Router) {
				r.Use(authService.AuthMiddleware)
				r.Get("/me", authHandler.Me)

				// Admin-only
				r.Group(func(r chi.Router) {
					r.Use(auth.AdminMiddleware)
					r.Post("/invite", authHandler.CreateInvite)
				})
			})
		})

		// Config route (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Get("/config", configHandler(cfg))
		})

		// Request routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Post("/requests", requestHandler.Create)
			r.Get("/requests", requestHandler.List)
			r.Get("/requests/{tmdb_id}/status", requestHandler.GetStatus)
		})

		// Discover / media routes (authenticated, TMDB proxy)
		if discoverHandler != nil {
			r.Group(func(r chi.Router) {
				r.Use(authService.AuthMiddleware)

				// Discover
				r.Get("/discover/trending", discoverHandler.Trending)
				r.Get("/discover/movies/popular", discoverHandler.PopularMovies)
				r.Get("/discover/tv/popular", discoverHandler.PopularTV)
				r.Get("/discover/movies/top-rated", discoverHandler.TopRatedMovies)
				r.Get("/discover/movies/upcoming", discoverHandler.UpcomingMovies)
				r.Get("/discover/movies/now-playing", discoverHandler.NowPlayingMovies)
				r.Get("/discover/movies", discoverHandler.DiscoverMovies)
				r.Get("/discover/tv", discoverHandler.DiscoverTV)

				// Search
				r.Get("/search", discoverHandler.Search)

				// Media details
				r.Get("/media/movie/{id}", discoverHandler.MovieDetail)
				r.Get("/media/tv/{id}", discoverHandler.TVDetail)
				r.Get("/media/movie/{id}/recommendations", discoverHandler.MovieRecommendations)
				r.Get("/media/tv/{id}/recommendations", discoverHandler.TVRecommendations)
				r.Get("/media/movie/{id}/similar", discoverHandler.SimilarMovies)
				r.Get("/media/tv/{id}/similar", discoverHandler.SimilarTV)
				r.Get("/media/person/{id}", discoverHandler.PersonDetail)
				r.Get("/media/person/{id}/credits", discoverHandler.PersonCredits)

				// Genres & providers
				r.Get("/genres/movie", discoverHandler.MovieGenres)
				r.Get("/genres/tv", discoverHandler.TVGenres)
				r.Get("/providers/movie", discoverHandler.MovieWatchProviders)

				// Trakt
				r.Get("/trakt/trending", discoverHandler.TraktTrending)
				r.Get("/trakt/popular", discoverHandler.TraktPopular)
				r.Get("/trakt/lists", discoverHandler.TraktPopularLists)
				r.Get("/trakt/lists/{user}/{slug}/items", discoverHandler.TraktListItems)
				r.Get("/trakt/calendar", discoverHandler.TraktCalendar)
				r.Get("/trakt/anticipated", discoverHandler.TraktAnticipated)
			r.Get("/trakt/recommendations", discoverHandler.TraktRecommendations)
			})
		}

		// AI routes (authenticated)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Post("/ai/chat", aiHandler.Chat)
			r.Get("/ai/available", aiHandler.Available)
		})

		// Arr proxy routes (admin only)
		r.Group(func(r chi.Router) {
			r.Use(authService.AuthMiddleware)
			r.Use(auth.AdminMiddleware)

			r.HandleFunc("/radarr/*", proxyHandler.RadarrProxy())
			r.HandleFunc("/sonarr/*", proxyHandler.SonarrProxy())
		})
	})

	// Serve Flutter web UI at root (catch-all for non-API routes)
	r.NotFound(web.Handler().ServeHTTP)

	return r
}

func configHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server_name": cfg.ServerName,
			"services": map[string]bool{
				"radarr": cfg.RadarrEnabled(),
				"sonarr": cfg.SonarrEnabled(),
				"ai":     cfg.AIEnabled(),
				"tmdb":   cfg.TMDBEnabled(),
				"trakt":  cfg.TraktEnabled(),
			},
		})
	}
}
