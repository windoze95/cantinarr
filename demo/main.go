// main.go — router assembly, auth middleware, landing page, and process
// startup for the Cantinarr demo server (demo.cantinarr.com). Part of the
// frozen Stage A contract (see contract.md).
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

const demoPort = 8484

// demoServerURL is the advertised base URL (DEMO_SERVER_URL env; production
// sets https://demo.cantinarr.com). Read once at startup, before the router
// serves traffic.
var demoServerURL = fmt.Sprintf("http://localhost:%d", demoPort)

//go:embed assets/landing.html
var demoLandingTemplate string

// demoLandingRendered is the landing page with DEMO_SERVER_URL substituted.
var demoLandingRendered string

func main() {
	if v := os.Getenv("DEMO_SERVER_URL"); v != "" {
		demoServerURL = v
	}
	demoLandingRendered = strings.NewReplacer(
		"__DEMO_SERVER_URL_QUERY__", url.QueryEscape(demoServerURL),
		"__DEMO_SERVER_URL__", demoServerURL,
	).Replace(demoLandingTemplate)

	wsStartHub()
	startSimulations()

	r := buildRouter()

	addr := fmt.Sprintf(":%d", demoPort)
	log.Printf("Cantinarr Demo Server starting on %s", addr)
	log.Printf("  Admin login: admin / demo")
	log.Printf("  User login:  user / demo")
	log.Printf("  Demo connect link: cantinarr://connect?token=%s&server=%s",
		demoConnectTokenStr, url.QueryEscape(demoServerURL))
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// startSimulations wires the domain tickers. The downloads_queue snapshot
// loop is the only standing ticker: request/book download simulations start
// on demand (startDownloadSimulation / bookStartFormatSimulation) and the
// Tautulli drift is applied per activity poll.
func startSimulations() {
	dlStartDownloadsTicker()
}

func buildRouter() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// Permissive CORS — deliberate divergence from the real server (which
	// ships none) so browser-hosted app builds can point at the demo.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders: []string{"Link"},
		MaxAge:         300,
	}))

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.SetHeader("Content-Type", "application/json"))

		// Endpoints with their own auth transport (or none).
		registerWS(r) // GET /ws — subprotocol auth
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		registerAuth(r)       // /auth/* — public + its own requireAuth subgroup
		registerMediaFiles(r) // /media-files/* — applies requireAuth itself so
		// the self-authorizing GET|HEAD /media-files/download/{ticket} stays public.

		// Everything else requires a session. Domain register functions
		// receive this authenticated router and gate admin surfaces
		// themselves with requireAdmin (see contract.md for mount rules).
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			registerConfig(r)
			registerUsersAdmin(r)
			registerExternalAddress(r)
			registerMediaAccess(r)
			registerNotifications(r)
			registerRequests(r)
			registerRequestsAdmin(r)
			registerBooks(r)
			registerDiscover(r)
			registerTrakt(r)
			registerAI(r)
			registerAIAdmin(r)
			registerIssues(r)
			registerRemediation(r)
			registerProposals(r)
			registerInstances(r)
			registerDownloads(r)
			registerTautulli(r)
		})
	})

	// Landing page: any unmatched non-API GET serves the reviewer HTML.
	// Unmatched API paths (and non-GETs) get the standard JSON 404.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && !strings.HasPrefix(req.URL.Path, "/api") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(demoLandingRendered))
			return
		}
		writeErr(w, http.StatusNotFound, "not found")
	})

	return r
}

// ─── Auth middleware & context ──────────────────────────

type ctxUserKey struct{}

// requireAuth validates the Bearer access JWT and stores the *DemoUser in the
// request context (read it back with userFrom). All error bodies are JSON —
// deliberate divergence from the real server's text/plain http.Error sites.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			writeErr(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		if !strings.HasPrefix(header, "Bearer ") {
			writeErr(w, http.StatusUnauthorized, "invalid authorization format")
			return
		}
		claims, err := parseAccessClaims(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		// Revoking a device kills its live access tokens immediately.
		if claims.DeviceID != "" && deviceRevoked(claims.DeviceID) {
			writeErr(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		u := userByID(claims.UserID)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireAdmin gates a route on the admin role. Mount it AFTER requireAuth
// (e.g. r.With(requireAdmin).Get(...)).
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if u.Role != roleAdmin {
			writeErr(w, http.StatusForbidden, "permission denied")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// userFrom returns the authenticated *DemoUser placed in the context by
// requireAuth; nil on unauthenticated requests.
func userFrom(r *http.Request) *DemoUser {
	u, _ := r.Context().Value(ctxUserKey{}).(*DemoUser)
	return u
}
