package api

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// safeRequestLogger records failed HTTP requests without routine success noise
// and without ever serializing the query
// string, headers, or body. Query values commonly contain OAuth material and
// older Cantinarr webhook URLs carried bearer credentials there.
func safeRequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		if status < http.StatusBadRequest {
			return
		}
		route := "<unmatched>"
		if routeContext := chi.RouteContext(r.Context()); routeContext != nil && routeContext.RoutePattern() != "" {
			route = routeContext.RoutePattern()
		}
		log.Printf("http: %s %s %d %s", r.Method, route, status, time.Since(started).Round(time.Millisecond))
	})
}
