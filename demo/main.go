package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

// ─── Configuration ──────────────────────────────────────

const (
	jwtSecret  = "demo-jwt-secret-cantinarr"
	demoPort   = 8484
	demoInvite = "DEMO42"
	serverName = "Cantinarr Demo"
)

// ─── Simple password hashing (demo only) ────────────────

func hashPassword(password string) string {
	h := sha256.Sum256([]byte("cantinarr-demo-salt:" + password))
	return hex.EncodeToString(h[:])
}

func checkPassword(hash, password string) bool {
	return hash == hashPassword(password)
}

// ─── User store ─────────────────────────────────────────

type demoUser struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type userStore struct {
	mu     sync.RWMutex
	users  map[string]*demoUser
	nextID int64
}

func newUserStore() *userStore {
	s := &userStore{users: make(map[string]*demoUser), nextID: 3}
	s.users["admin"] = &demoUser{ID: 1, Username: "admin", PasswordHash: hashPassword("demo"), Role: "admin", CreatedAt: time.Now()}
	s.users["user"] = &demoUser{ID: 2, Username: "user", PasswordHash: hashPassword("demo"), Role: "user", CreatedAt: time.Now()}
	return s
}

func (s *userStore) get(username string) *demoUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.users[username]
}

func (s *userStore) getByID(id int64) *demoUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *userStore) create(username, password, role string) (*demoUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.users[username]; exists {
		return nil, fmt.Errorf("username already taken")
	}
	u := &demoUser{ID: s.nextID, Username: username, PasswordHash: hashPassword(password), Role: role, CreatedAt: time.Now()}
	s.users[username] = u
	s.nextID++
	return u, nil
}

// ─── JWT ────────────────────────────────────────────────

type demoClaims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func generateTokens(u *demoUser) (string, string, error) {
	now := time.Now()
	access := jwt.NewWithClaims(jwt.SigningMethodHS256, &demoClaims{
		UserID: u.ID, Username: u.Username, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})
	accessToken, err := access.SignedString([]byte(jwtSecret))
	if err != nil {
		return "", "", err
	}
	refresh := jwt.NewWithClaims(jwt.SigningMethodHS256, &demoClaims{
		UserID: u.ID, Username: u.Username, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(30 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	})
	refreshToken, err := refresh.SignedString([]byte(jwtSecret))
	if err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

func validateToken(tokenStr string) (*demoClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &demoClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*demoClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// ─── Auth middleware ────────────────────────────────────

type contextKey string

const claimsKey contextKey = "claims"

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization"})
			return
		}
		claims, err := validateToken(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		ctx := r.Context()
		ctx = contextWithClaims(ctx, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func contextWithClaims(ctx interface{ Value(any) any }, claims *demoClaims) interface {
	Value(any) any
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
} {
	return &claimsContext{parent: ctx.(interface {
		Value(any) any
		Deadline() (time.Time, bool)
		Done() <-chan struct{}
		Err() error
	}), claims: claims}
}

type claimsContext struct {
	parent interface {
		Value(any) any
		Deadline() (time.Time, bool)
		Done() <-chan struct{}
		Err() error
	}
	claims *demoClaims
}

func (c *claimsContext) Value(key any) any {
	if key == claimsKey {
		return c.claims
	}
	return c.parent.Value(key)
}
func (c *claimsContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *claimsContext) Done() <-chan struct{}        { return c.parent.Done() }
func (c *claimsContext) Err() error                  { return c.parent.Err() }

func claimsFromContext(ctx interface{ Value(any) any }) *demoClaims {
	if v, ok := ctx.Value(claimsKey).(*demoClaims); ok {
		return v
	}
	return nil
}

// ─── Request store ──────────────────────────────────────

type requestEntry struct {
	TmdbID      int       `json:"tmdb_id"`
	MediaType   string    `json:"media_type"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Progress    float64   `json:"progress"`
	UserID      int64     `json:"user_id"`
	RequestedAt time.Time `json:"requested_at"`
}

type requestStore struct {
	mu       sync.RWMutex
	requests map[string]*requestEntry
}

func newRequestStore() *requestStore {
	s := &requestStore{requests: make(map[string]*requestEntry)}
	s.requests["19:movie"] = &requestEntry{TmdbID: 19, MediaType: "movie", Title: "Metropolis", Status: "available", Progress: 1.0, UserID: 1, RequestedAt: time.Now().Add(-48 * time.Hour)}
	s.requests["961:movie"] = &requestEntry{TmdbID: 961, MediaType: "movie", Title: "The General", Status: "available", Progress: 1.0, UserID: 1, RequestedAt: time.Now().Add(-24 * time.Hour)}
	s.requests["90001:tv"] = &requestEntry{TmdbID: 90001, MediaType: "tv", Title: "Sherlock Holmes Adventures", Status: "available", Progress: 1.0, UserID: 1, RequestedAt: time.Now().Add(-72 * time.Hour)}
	return s
}

func (s *requestStore) key(tmdbID int, mediaType string) string {
	return fmt.Sprintf("%d:%s", tmdbID, mediaType)
}

func (s *requestStore) get(tmdbID int, mediaType string) *requestEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.requests[s.key(tmdbID, mediaType)]
}

func (s *requestStore) set(entry *requestEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[s.key(entry.TmdbID, entry.MediaType)] = entry
}

func (s *requestStore) listForUser(userID int64) []requestEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []requestEntry
	for _, r := range s.requests {
		if r.UserID == userID {
			result = append(result, *r)
		}
	}
	return result
}

// ─── WebSocket hub ──────────────────────────────────────

type wsClient struct {
	conn   *websocket.Conn
	send   chan []byte
	userID int64
}

type wsHub struct {
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	mu         sync.RWMutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func newWSHub() *wsHub {
	return &wsHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
	}
}

func (h *wsHub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- msg:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *wsHub) broadcastEvent(eventType string, data map[string]interface{}) {
	msg, err := json.Marshal(map[string]interface{}{
		"type": eventType,
		"data": data,
	})
	if err != nil {
		return
	}
	h.broadcast <- msg
}

func (c *wsClient) readPump() {
	defer func() { c.conn.Close() }()
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (c *wsClient) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() { ticker.Stop(); c.conn.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ─── JSON helpers ───────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeJSONOK(w http.ResponseWriter, v interface{}) {
	writeJSON(w, http.StatusOK, v)
}

func queryInt(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func tmdbListResponse(results []interface{}, page int) TMDBListResponse {
	if results == nil {
		results = []interface{}{}
	}
	return TMDBListResponse{
		Page: page, Results: results, TotalPages: 1, TotalResults: len(results),
	}
}

func yearFromDate(date string) int {
	if len(date) >= 4 {
		y, _ := strconv.Atoi(date[:4])
		return y
	}
	return 0
}

// ─── Main ───────────────────────────────────────────────

func main() {
	users := newUserStore()
	requests := newRequestStore()
	hub := newWSHub()
	go hub.run()

	r := chi.NewRouter()

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

		// ── WebSocket ──
		r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
			protocols := websocket.Subprotocols(r)
			if len(protocols) < 2 || protocols[0] != "Bearer" {
				http.Error(w, "missing auth", http.StatusUnauthorized)
				return
			}
			claims, err := validateToken(protocols[1])
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Printf("ws upgrade: %v", err)
				return
			}
			client := &wsClient{conn: conn, send: make(chan []byte, 256), userID: claims.UserID}
			hub.register <- client
			go client.writePump()
			go client.readPump()
		})

		// ── Health ──
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			writeJSONOK(w, map[string]string{"status": "ok"})
		})

		// ── Auth ──
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", loginHandler(users))
			r.Post("/register", registerHandler(users))
			r.Post("/refresh", refreshHandler(users))

			r.Group(func(r chi.Router) {
				r.Use(authMiddleware)
				r.Get("/me", meHandler(users))
				r.Group(func(r chi.Router) {
					r.Use(adminMiddleware)
					r.Post("/invite", inviteHandler())
				})
			})
		})

		// ── Config ──
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Get("/config", func(w http.ResponseWriter, _ *http.Request) {
				writeJSONOK(w, map[string]interface{}{
					"server_name": serverName,
					"services": map[string]bool{
						"radarr": true, "sonarr": true, "ai": true, "tmdb": true, "trakt": true,
					},
				})
			})
		})

		// ── Requests ──
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Post("/requests", createRequestHandler(requests, hub))
			r.Get("/requests", listRequestsHandler(requests))
			r.Get("/requests/{tmdb_id}/status", requestStatusHandler(requests))
		})

		// ── Discovery & Media ──
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			registerDiscoveryRoutes(r)
			registerTraktRoutes(r)
		})

		// ── AI Chat ──
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Post("/ai/chat", aiChatHandler())
			r.Get("/ai/available", func(w http.ResponseWriter, _ *http.Request) {
				writeJSONOK(w, map[string]bool{"available": true})
			})
		})

		// ── Arr proxy ──
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Use(adminMiddleware)
			registerArrRoutes(r, requests)
		})
	})

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(landingHTML))
	})

	addr := fmt.Sprintf(":%d", demoPort)
	log.Printf("Cantinarr Demo Server starting on %s", addr)
	log.Printf("  Admin login: admin / demo")
	log.Printf("  User login:  user / demo")
	log.Printf("  Invite code: %s", demoInvite)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// ─── Auth handlers ──────────────────────────────────────

func tokenResponse(u *demoUser) (map[string]interface{}, error) {
	at, rt, err := generateTokens(u)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"access_token":  at,
		"refresh_token": rt,
		"user": map[string]interface{}{
			"id": u.ID, "username": u.Username, "role": u.Role, "created_at": u.CreatedAt,
		},
	}, nil
}

func loginHandler(users *userStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		u := users.get(req.Username)
		if u == nil || !checkPassword(u.PasswordHash, req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		resp, err := tokenResponse(u)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token error"})
			return
		}
		writeJSONOK(w, resp)
	}
}

func registerHandler(users *userStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username   string `json:"username"`
			Password   string `json:"password"`
			InviteCode string `json:"invite_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Username == "" || req.Password == "" || req.InviteCode == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username, password, and invite_code required"})
			return
		}
		if req.InviteCode != demoInvite {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid invite code required"})
			return
		}
		u, err := users.create(req.Username, req.Password, "user")
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		resp, err := tokenResponse(u)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token error"})
			return
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

func refreshHandler(users *userStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		claims, err := validateToken(req.RefreshToken)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid refresh token"})
			return
		}
		u := users.getByID(claims.UserID)
		if u == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user not found"})
			return
		}
		resp, err := tokenResponse(u)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token error"})
			return
		}
		writeJSONOK(w, resp)
	}
}

func meHandler(users *userStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFromContext(r.Context())
		u := users.getByID(claims.UserID)
		if u == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user not found"})
			return
		}
		writeJSONOK(w, map[string]interface{}{"id": u.ID, "username": u.Username, "role": u.Role})
	}
}

func inviteHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"code": demoInvite, "expires_at": time.Now().Add(7 * 24 * time.Hour),
		})
	}
}

// ─── Request handlers ───────────────────────────────────

func createRequestHandler(requests *requestStore, hub *wsHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFromContext(r.Context())
		var req struct {
			TmdbID    int    `json:"tmdb_id"`
			MediaType string `json:"media_type"`
			Title     string `json:"title"`
			TvdbID    int    `json:"tvdb_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.TmdbID == 0 || req.MediaType == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tmdb_id and media_type required"})
			return
		}

		if existing := requests.get(req.TmdbID, req.MediaType); existing != nil {
			writeJSONOK(w, map[string]interface{}{"success": true, "status": existing.Status, "title": existing.Title})
			return
		}

		title := req.Title
		if title == "" {
			if req.MediaType == "movie" {
				title = movieTitle(req.TmdbID)
			} else {
				title = tvTitle(req.TmdbID)
			}
		}

		entry := &requestEntry{
			TmdbID: req.TmdbID, MediaType: req.MediaType, Title: title,
			Status: "requested", Progress: 0, UserID: claims.UserID, RequestedAt: time.Now(),
		}
		requests.set(entry)
		go simulateDownload(requests, hub, entry.TmdbID, entry.MediaType)

		writeJSONOK(w, map[string]interface{}{"success": true, "status": "requested", "title": title})
	}
}

func listRequestsHandler(requests *requestStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := claimsFromContext(r.Context())
		list := requests.listForUser(claims.UserID)
		if list == nil {
			list = []requestEntry{}
		}
		result := make([]map[string]interface{}, len(list))
		for i, req := range list {
			result[i] = map[string]interface{}{
				"tmdb_id": req.TmdbID, "media_type": req.MediaType,
				"title": req.Title, "status": req.Status, "requested_at": req.RequestedAt,
			}
		}
		writeJSONOK(w, result)
	}
}

func requestStatusHandler(requests *requestStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tmdbID, err := strconv.Atoi(chi.URLParam(r, "tmdb_id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tmdb_id"})
			return
		}
		mediaType := r.URL.Query().Get("media_type")
		if mediaType == "" {
			mediaType = "movie"
		}
		entry := requests.get(tmdbID, mediaType)
		if entry == nil {
			writeJSONOK(w, map[string]interface{}{"status": "unavailable", "progress": 0.0})
			return
		}
		writeJSONOK(w, map[string]interface{}{"status": entry.Status, "progress": entry.Progress})
	}
}

// ─── Discovery routes ───────────────────────────────────

func registerDiscoveryRoutes(r chi.Router) {
	r.Get("/discover/trending", func(w http.ResponseWriter, r *http.Request) {
		writeJSONOK(w, tmdbListResponse(allMediaResults(), queryInt(r, "page", 1)))
	})
	r.Get("/discover/movies/popular", func(w http.ResponseWriter, r *http.Request) {
		writeJSONOK(w, tmdbListResponse(moviesAsResults(movies), queryInt(r, "page", 1)))
	})
	r.Get("/discover/tv/popular", func(w http.ResponseWriter, r *http.Request) {
		writeJSONOK(w, tmdbListResponse(tvAsResults(tvShows), queryInt(r, "page", 1)))
	})
	r.Get("/discover/movies/top-rated", func(w http.ResponseWriter, r *http.Request) {
		sorted := make([]movieEntry, len(movies))
		copy(sorted, movies)
		for i := 0; i < len(sorted)-1; i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].tmdb.VoteAverage > sorted[i].tmdb.VoteAverage {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		writeJSONOK(w, tmdbListResponse(moviesAsResults(sorted), queryInt(r, "page", 1)))
	})
	r.Get("/discover/movies/upcoming", func(w http.ResponseWriter, r *http.Request) {
		writeJSONOK(w, tmdbListResponse(moviesAsResults(movies[:min(6, len(movies))]), queryInt(r, "page", 1)))
	})
	r.Get("/discover/movies/now-playing", func(w http.ResponseWriter, r *http.Request) {
		start := min(6, len(movies))
		end := min(12, len(movies))
		writeJSONOK(w, tmdbListResponse(moviesAsResults(movies[start:end]), queryInt(r, "page", 1)))
	})
	r.Get("/discover/movies", func(w http.ResponseWriter, r *http.Request) {
		genreFilter := r.URL.Query().Get("with_genres")
		filtered := filterMoviesByGenre(genreFilter)
		writeJSONOK(w, tmdbListResponse(moviesAsResults(filtered), queryInt(r, "page", 1)))
	})
	r.Get("/discover/tv", func(w http.ResponseWriter, r *http.Request) {
		genreFilter := r.URL.Query().Get("with_genres")
		filtered := filterTVByGenre(genreFilter)
		writeJSONOK(w, tmdbListResponse(tvAsResults(filtered), queryInt(r, "page", 1)))
	})
	r.Get("/search", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if query == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query parameter required"})
			return
		}
		var results []interface{}
		for _, m := range searchMovies(query) {
			mv := m.tmdb
			mv.MediaType = "movie"
			results = append(results, mv)
		}
		for _, t := range searchTV(query) {
			tv := t.tmdb
			tv.MediaType = "tv"
			results = append(results, tv)
		}
		for _, p := range searchPersons(query) {
			pe := p.person
			pe.MediaType = "person"
			results = append(results, pe)
		}
		writeJSONOK(w, tmdbListResponse(results, queryInt(r, "page", 1)))
	})

	// Media details
	r.Get("/media/movie/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		if m := movieByID(id); m != nil {
			writeJSONOK(w, m.detail)
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "movie not found"})
		}
	})
	r.Get("/media/tv/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		if t := tvByID(id); t != nil {
			writeJSONOK(w, t.detail)
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tv show not found"})
		}
	})
	r.Get("/media/movie/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		var results []movieEntry
		for _, m := range movies {
			if m.tmdb.ID != id {
				results = append(results, m)
				if len(results) >= 6 {
					break
				}
			}
		}
		writeJSONOK(w, tmdbListResponse(moviesAsResults(results), queryInt(r, "page", 1)))
	})
	r.Get("/media/tv/{id}/recommendations", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		var results []tvEntry
		for _, t := range tvShows {
			if t.tmdb.ID != id {
				results = append(results, t)
			}
		}
		writeJSONOK(w, tmdbListResponse(tvAsResults(results), queryInt(r, "page", 1)))
	})
	r.Get("/media/movie/{id}/similar", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		var results []movieEntry
		for _, m := range movies {
			if m.tmdb.ID != id {
				results = append(results, m)
				if len(results) >= 6 {
					break
				}
			}
		}
		writeJSONOK(w, tmdbListResponse(moviesAsResults(results), queryInt(r, "page", 1)))
	})
	r.Get("/media/tv/{id}/similar", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		var results []tvEntry
		for _, t := range tvShows {
			if t.tmdb.ID != id {
				results = append(results, t)
			}
		}
		writeJSONOK(w, tmdbListResponse(tvAsResults(results), queryInt(r, "page", 1)))
	})
	r.Get("/media/person/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		if p := personByID(id); p != nil {
			writeJSONOK(w, p.person)
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "person not found"})
		}
	})
	r.Get("/media/person/{id}/credits", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(chi.URLParam(r, "id"))
		if p := personByID(id); p != nil {
			writeJSONOK(w, p.credits)
		} else {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "person not found"})
		}
	})

	// Genres & providers
	r.Get("/genres/movie", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONOK(w, map[string]interface{}{"genres": movieGenres})
	})
	r.Get("/genres/tv", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONOK(w, map[string]interface{}{"genres": tvGenres})
	})
	r.Get("/providers/movie", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONOK(w, map[string]interface{}{
			"results": []map[string]interface{}{
				{"provider_id": 1, "provider_name": "Public Domain Streaming", "logo_path": nil, "display_priority": 1},
				{"provider_id": 2, "provider_name": "Classic Cinema Channel", "logo_path": nil, "display_priority": 2},
				{"provider_id": 3, "provider_name": "Archive Films", "logo_path": nil, "display_priority": 3},
			},
		})
	})
}

func filterMoviesByGenre(genreFilter string) []movieEntry {
	if genreFilter == "" {
		return movies
	}
	gid, _ := strconv.Atoi(genreFilter)
	var filtered []movieEntry
	for _, m := range movies {
		for _, g := range m.tmdb.GenreIDs {
			if g == gid {
				filtered = append(filtered, m)
				break
			}
		}
	}
	return filtered
}

func filterTVByGenre(genreFilter string) []tvEntry {
	if genreFilter == "" {
		return tvShows
	}
	gid, _ := strconv.Atoi(genreFilter)
	var filtered []tvEntry
	for _, t := range tvShows {
		for _, g := range t.tmdb.GenreIDs {
			if g == gid {
				filtered = append(filtered, t)
				break
			}
		}
	}
	return filtered
}

// ─── Trakt routes ───────────────────────────────────────

func registerTraktRoutes(r chi.Router) {
	r.Get("/trakt/trending", func(w http.ResponseWriter, r *http.Request) {
		typ := r.URL.Query().Get("type")
		if typ == "" || typ == "movies" {
			var results []TraktTrendingMovie
			for i, m := range movies {
				results = append(results, TraktTrendingMovie{
					Watchers: 1000 - i*50,
					Movie:    TraktMovie{Title: m.tmdb.Title, Year: yearFromDate(m.tmdb.ReleaseDate), IDs: TraktIDs{Trakt: m.tmdb.ID * 10, TMDB: m.tmdb.ID, IMDB: m.detail.ImdbID}},
				})
			}
			writeJSONOK(w, results)
		} else {
			var results []TraktTrendingShow
			for i, t := range tvShows {
				results = append(results, TraktTrendingShow{
					Watchers: 800 - i*40,
					Show:     TraktShow{Title: t.tmdb.Name, Year: yearFromDate(t.tmdb.FirstAirDate), IDs: TraktIDs{Trakt: t.tmdb.ID * 10, TMDB: t.tmdb.ID, TVDB: *t.detail.ExternalIDs.TvdbID}},
				})
			}
			writeJSONOK(w, results)
		}
	})

	r.Get("/trakt/popular", func(w http.ResponseWriter, r *http.Request) {
		typ := r.URL.Query().Get("type")
		if typ == "" || typ == "movies" {
			var results []TraktMovie
			for _, m := range movies {
				results = append(results, TraktMovie{Title: m.tmdb.Title, Year: yearFromDate(m.tmdb.ReleaseDate), IDs: TraktIDs{Trakt: m.tmdb.ID * 10, TMDB: m.tmdb.ID, IMDB: m.detail.ImdbID}})
			}
			writeJSONOK(w, results)
		} else {
			var results []TraktShow
			for _, t := range tvShows {
				results = append(results, TraktShow{Title: t.tmdb.Name, Year: yearFromDate(t.tmdb.FirstAirDate), IDs: TraktIDs{Trakt: t.tmdb.ID * 10, TMDB: t.tmdb.ID, TVDB: *t.detail.ExternalIDs.TvdbID}})
			}
			writeJSONOK(w, results)
		}
	})

	r.Get("/trakt/lists", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONOK(w, traktLists())
	})

	r.Get("/trakt/lists/{user}/{slug}/items", func(w http.ResponseWriter, r *http.Request) {
		slug := chi.URLParam(r, "slug")
		now := time.Now().Format(time.RFC3339)
		var movieIDs []int
		switch slug {
		case "classic-horror-essentials":
			movieIDs = []int{987, 653, 234, 27573, 17057, 42832}
		case "silent-film-gems":
			movieIDs = []int{653, 961, 19, 1942, 234, 27573}
		default:
			movieIDs = []int{25862, 44208, 1480, 43861}
		}
		var items []TraktListItem
		for rank, id := range movieIDs {
			if m := movieByID(id); m != nil {
				items = append(items, TraktListItem{
					Rank: rank + 1, ID: id, ListedAt: now, Type: "movie",
					Movie: &TraktMovie{Title: m.tmdb.Title, Year: yearFromDate(m.tmdb.ReleaseDate), IDs: TraktIDs{TMDB: id, IMDB: m.detail.ImdbID}},
				})
			}
		}
		writeJSONOK(w, items)
	})

	r.Get("/trakt/calendar", func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now()
		var items []TraktCalendarItem
		for i, t := range tvShows {
			item := TraktCalendarItem{
				FirstAired: now.AddDate(0, 0, i).Format(time.RFC3339),
				Show:       TraktShow{Title: t.tmdb.Name, Year: yearFromDate(t.tmdb.FirstAirDate), IDs: TraktIDs{Trakt: t.tmdb.ID * 10, TMDB: t.tmdb.ID, TVDB: *t.detail.ExternalIDs.TvdbID}},
			}
			item.Episode.Season = 1
			item.Episode.Number = i + 1
			item.Episode.Title = fmt.Sprintf("Episode %d", i+1)
			item.Episode.IDs = TraktIDs{Trakt: t.tmdb.ID*100 + i + 1, TMDB: t.tmdb.ID}
			items = append(items, item)
		}
		writeJSONOK(w, items)
	})

	r.Get("/trakt/anticipated", func(w http.ResponseWriter, r *http.Request) {
		typ := r.URL.Query().Get("type")
		if typ == "" || typ == "movies" {
			var items []TraktAnticipatedItem
			for i, m := range movies {
				if i >= 10 {
					break
				}
				mv := TraktMovie{Title: m.tmdb.Title, Year: yearFromDate(m.tmdb.ReleaseDate), IDs: TraktIDs{Trakt: m.tmdb.ID * 10, TMDB: m.tmdb.ID, IMDB: m.detail.ImdbID}}
				items = append(items, TraktAnticipatedItem{ListCount: 500 - i*30, Movie: &mv})
			}
			writeJSONOK(w, items)
		} else {
			var items []TraktAnticipatedItem
			for i, t := range tvShows {
				sh := TraktShow{Title: t.tmdb.Name, Year: yearFromDate(t.tmdb.FirstAirDate), IDs: TraktIDs{Trakt: t.tmdb.ID * 10, TMDB: t.tmdb.ID, TVDB: *t.detail.ExternalIDs.TvdbID}}
				items = append(items, TraktAnticipatedItem{ListCount: 400 - i*25, Show: &sh})
			}
			writeJSONOK(w, items)
		}
	})

	r.Get("/trakt/recommendations", func(w http.ResponseWriter, r *http.Request) {
		typ := r.URL.Query().Get("type")
		if typ == "" || typ == "movies" {
			var results []TraktMovie
			for _, m := range movies[:min(8, len(movies))] {
				results = append(results, TraktMovie{Title: m.tmdb.Title, Year: yearFromDate(m.tmdb.ReleaseDate), IDs: TraktIDs{Trakt: m.tmdb.ID * 10, TMDB: m.tmdb.ID, IMDB: m.detail.ImdbID}})
			}
			writeJSONOK(w, results)
		} else {
			var results []TraktShow
			for _, t := range tvShows {
				results = append(results, TraktShow{Title: t.tmdb.Name, Year: yearFromDate(t.tmdb.FirstAirDate), IDs: TraktIDs{Trakt: t.tmdb.ID * 10, TMDB: t.tmdb.ID, TVDB: *t.detail.ExternalIDs.TvdbID}})
			}
			writeJSONOK(w, results)
		}
	})
}

// ─── AI chat handler ────────────────────────────────────

func aiChatHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if len(req.Messages) == 0 {
			http.Error(w, `{"error":"messages required"}`, http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		response := aiResponses[rand.Intn(len(aiResponses))]
		words := strings.Fields(response)
		for i, word := range words {
			chunk := word
			if i < len(words)-1 {
				chunk += " "
			}
			data, _ := json.Marshal(map[string]string{"text": chunk})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// ─── Arr proxy routes ───────────────────────────────────

func registerArrRoutes(r chi.Router, requests *requestStore) {
	r.Get("/radarr/*", func(w http.ResponseWriter, r *http.Request) {
		path := chi.URLParam(r, "*")
		switch {
		case strings.HasSuffix(path, "qualityprofile"):
			writeJSONOK(w, []map[string]interface{}{{"id": 1, "name": "HD-1080p"}})
		case strings.HasSuffix(path, "rootfolder"):
			writeJSONOK(w, []map[string]interface{}{{"id": 1, "path": "/movies", "freeSpace": 500000000000}})
		case strings.HasSuffix(path, "queue"):
			writeJSONOK(w, map[string]interface{}{"records": []interface{}{}})
		case strings.HasSuffix(path, "movie"):
			var radarrMovies []map[string]interface{}
			for _, m := range movies[:min(8, len(movies))] {
				entry := requests.get(m.tmdb.ID, "movie")
				radarrMovies = append(radarrMovies, map[string]interface{}{
					"id": m.tmdb.ID, "title": m.tmdb.Title, "tmdbId": m.tmdb.ID,
					"year": yearFromDate(m.tmdb.ReleaseDate), "hasFile": entry != nil && entry.Status == "available",
					"monitored": true, "isAvailable": true, "rootFolderPath": "/movies",
				})
			}
			writeJSONOK(w, radarrMovies)
		default:
			writeJSONOK(w, map[string]interface{}{})
		}
	})

	r.Get("/sonarr/*", func(w http.ResponseWriter, r *http.Request) {
		path := chi.URLParam(r, "*")
		switch {
		case strings.HasSuffix(path, "qualityprofile"):
			writeJSONOK(w, []map[string]interface{}{{"id": 1, "name": "HD-1080p"}})
		case strings.HasSuffix(path, "rootfolder"):
			writeJSONOK(w, []map[string]interface{}{{"id": 1, "path": "/tv", "freeSpace": 500000000000}})
		case strings.HasSuffix(path, "queue"):
			writeJSONOK(w, map[string]interface{}{"records": []interface{}{}})
		case strings.HasSuffix(path, "series"):
			var series []map[string]interface{}
			for _, t := range tvShows[:min(3, len(tvShows))] {
				series = append(series, map[string]interface{}{
					"id": t.tmdb.ID, "title": t.tmdb.Name, "tvdbId": *t.detail.ExternalIDs.TvdbID,
					"tmdbId": t.tmdb.ID, "year": yearFromDate(t.tmdb.FirstAirDate), "monitored": true,
					"statistics": map[string]interface{}{
						"episodeFileCount": t.detail.NumberOfEpisodes, "episodeCount": t.detail.NumberOfEpisodes, "percentOfEpisodes": 100.0,
					},
				})
			}
			writeJSONOK(w, series)
		default:
			writeJSONOK(w, map[string]interface{}{})
		}
	})

	noopHandler := func(w http.ResponseWriter, _ *http.Request) {
		writeJSONOK(w, map[string]interface{}{"success": true})
	}
	r.Post("/radarr/*", noopHandler)
	r.Delete("/radarr/*", noopHandler)
	r.Post("/sonarr/*", noopHandler)
	r.Delete("/sonarr/*", noopHandler)
}

// ─── Download simulation ────────────────────────────────

func simulateDownload(store *requestStore, hub *wsHub, tmdbID int, mediaType string) {
	time.Sleep(10 * time.Second)

	entry := store.get(tmdbID, mediaType)
	if entry == nil {
		return
	}

	entry.Status = "downloading"
	entry.Progress = 0
	store.set(entry)
	hub.broadcastEvent("download_progress", map[string]interface{}{
		"tmdb_id": tmdbID, "media_type": mediaType, "progress": 0.0, "status": "downloading",
	})

	steps := 20
	for i := 1; i <= steps; i++ {
		time.Sleep(1500 * time.Millisecond)
		progress := float64(i) / float64(steps)
		entry.Progress = progress
		store.set(entry)
		hub.broadcastEvent("download_progress", map[string]interface{}{
			"tmdb_id": tmdbID, "media_type": mediaType, "progress": progress, "status": "downloading",
		})
	}

	entry.Status = "available"
	entry.Progress = 1.0
	store.set(entry)
	hub.broadcastEvent("request_status_changed", map[string]interface{}{
		"tmdb_id": tmdbID, "media_type": mediaType, "status": "available",
	})
}

// ─── Landing page ───────────────────────────────────────

const landingHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Cantinarr Demo Server</title>
<style>
  *{margin:0;padding:0;box-sizing:border-box}
  body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#1a1a2e;color:#e0e0e0;min-height:100vh;display:flex;align-items:center;justify-content:center}
  .container{max-width:600px;padding:2rem;text-align:center}
  h1{color:#ffd700;font-size:2.5rem;margin-bottom:.5rem}
  .subtitle{color:#888;margin-bottom:2rem}
  .card{background:#16213e;border-radius:12px;padding:1.5rem;margin:1rem 0;text-align:left}
  .card h3{color:#ffd700;margin-bottom:.5rem}
  code{background:#0f3460;padding:.2rem .5rem;border-radius:4px;font-size:.9rem;color:#e94560}
  .credentials{display:grid;grid-template-columns:1fr 1fr;gap:1rem}
  .cred{background:#0f3460;border-radius:8px;padding:1rem;text-align:center}
  .cred .label{color:#888;font-size:.8rem;text-transform:uppercase}
  .cred .value{color:#ffd700;font-size:1.2rem;margin-top:.25rem}
  .footer{margin-top:2rem;color:#555;font-size:.85rem}
</style>
</head>
<body>
<div class="container">
  <h1>Cantinarr</h1>
  <p class="subtitle">Demo Server</p>
  <div class="card">
    <h3>Connect Your App</h3>
    <p>Point your Cantinarr app to this server's URL to explore the demo with public domain content.</p>
  </div>
  <div class="credentials">
    <div class="cred"><div class="label">Admin Login</div><div class="value">admin / demo</div></div>
    <div class="cred"><div class="label">User Login</div><div class="value">user / demo</div></div>
  </div>
  <div class="card">
    <h3>Invite Code</h3>
    <p>Use code <code>DEMO42</code> to register new accounts.</p>
  </div>
  <div class="card">
    <h3>Features Enabled</h3>
    <p>TMDB Discovery, Radarr, Sonarr, Trakt, AI Assistant &mdash; all simulated with public domain films.</p>
  </div>
  <p class="footer">All content is public domain. This is a demo server for distribution review.</p>
</div>
</body>
</html>`

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
