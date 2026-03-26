package api

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	CtxAgentID contextKey = "agent_id"
	CtxScopes  contextKey = "scopes"
)

func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.NoAuth {
			ctx := context.WithValue(r.Context(), CtxAgentID, "agent:local")
			ctx = context.WithValue(ctx, CtxScopes, []string{"*"})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid Authorization header")
			return
		}
		rawKey := strings.TrimPrefix(header, "Bearer ")

		agentID, scopes, err := s.AuthStore.ValidateKey(rawKey)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
			return
		}

		ctx := context.WithValue(r.Context(), CtxAgentID, agentID)
		ctx = context.WithValue(ctx, CtxScopes, scopes)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireScope(required string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopes, _ := r.Context().Value(CtxScopes).([]string)
			if !auth_checkScope(scopes, required) {
				WriteError(w, http.StatusForbidden, "FORBIDDEN", "Insufficient permissions: requires "+required)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// auth_checkScope is a local copy to avoid import cycle — mirrors auth.CheckScope.
func auth_checkScope(granted []string, required string) bool {
	for _, s := range granted {
		if s == "*" {
			return true
		}
		if s == required {
			return true
		}
		if strings.HasSuffix(s, ":*") {
			prefix := strings.TrimSuffix(s, "*")
			if strings.HasPrefix(required, prefix) {
				return true
			}
		}
	}
	return false
}

func GetAgentID(r *http.Request) string {
	v, _ := r.Context().Value(CtxAgentID).(string)
	return v
}

func GetScopes(r *http.Request) []string {
	v, _ := r.Context().Value(CtxScopes).([]string)
	return v
}
