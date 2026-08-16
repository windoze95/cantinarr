package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
)

type contextKey string

const (
	ClaimsKey contextKey = "claims"
	UserKey   contextKey = "user"
)

func GetClaims(ctx context.Context) *Claims {
	claims, _ := ctx.Value(ClaimsKey).(*Claims)
	return claims
}

func GetUserFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(UserKey).(*User)
	return user
}

func (s *Service) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// Every 401 names its cause and source in the log: a client dying on
			// silent 401s is otherwise undiagnosable from the server side. Only
			// the reason and remote address are logged — never token material.
			log.Printf("auth: 401 %s %s from %s: no authorization header", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			log.Printf("auth: 401 %s %s from %s: malformed authorization header", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
			return
		}

		claims, user, err := s.AuthenticateToken(token)
		if err != nil {
			// A fault while evaluating the token (DB error) must not read as a
			// rejection: clients treat 401 as "session dead". Answer 503 so
			// they retry with the same credentials.
			if errors.Is(err, ErrAuthUnavailable) {
				log.Printf("auth: 503 %s %s from %s (session kept, client retries): %v", r.Method, r.URL.Path, r.RemoteAddr, err)
				http.Error(w, `{"error":"temporarily unavailable, retry shortly"}`, http.StatusServiceUnavailable)
				return
			}
			// The validation error text describes the failure class (expired,
			// bad signature, revoked device) and never contains the token.
			log.Printf("auth: 401 %s %s from %s: %v", r.Method, r.URL.Path, r.RemoteAddr, err)
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ClaimsKey, claims)
		ctx = context.WithValue(ctx, UserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequirePermission(permission Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			if !HasPermission(claims.Role, permission) {
				http.Error(w, `{"error":"permission denied"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func AdminMiddleware(next http.Handler) http.Handler {
	return RequirePermission(PermissionAdmin)(next)
}
