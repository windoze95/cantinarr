package mcpserver

import (
	"context"
	"net/http"

	"github.com/windoze95/cantinarr-server/internal/auth"
)

type contextKey string

const (
	userIDKey contextKey = "mcp_user_id"
	roleKey   contextKey = "mcp_user_role"
)

// AuthContextFunc bridges chi's auth context into mcp-go's context.
// It reads Claims set by auth.AuthMiddleware on r.Context() and injects
// the userID and role into the context that mcp-go passes to tool handlers.
func AuthContextFunc(ctx context.Context, r *http.Request) context.Context {
	claims := auth.GetClaims(r.Context())
	if claims != nil {
		ctx = context.WithValue(ctx, userIDKey, claims.UserID)
		ctx = context.WithValue(ctx, roleKey, claims.Role)
	}
	return ctx
}

// GetUserIDFromContext extracts the userID set by AuthContextFunc.
func GetUserIDFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(userIDKey).(int64)
	return id
}

// GetRoleFromContext extracts the user role set by AuthContextFunc.
// Returns empty string when no role is available.
func GetRoleFromContext(ctx context.Context) string {
	role, _ := ctx.Value(roleKey).(string)
	return role
}
