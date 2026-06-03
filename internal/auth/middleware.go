package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// contextKey for storing claims in request context.
type claimsKeyType struct{}

var claimsKey = claimsKeyType{}

// GetClaims extracts verified JWT claims from the request context, or nil.
func GetClaims(ctx context.Context) *Claims {
	if c, ok := ctx.Value(claimsKey).(*Claims); ok {
		return c
	}
	return nil
}

// RequireAuth is HTTP middleware that verifies the JWT in the Authorization
// header and attaches claims to the request context. If requiredRole is
// non-empty, only tokens with that role (or admin) pass through.
func RequireAuth(issuer *Issuer, requiredRole Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token == "" {
				writeAuthErr(w, "missing Authorization header")
				return
			}
			claims, err := issuer.Verify(token)
			if err != nil {
				writeAuthErr(w, "invalid token: "+err.Error())
				return
			}
			if requiredRole != "" && !HasRole(claims, requiredRole) {
				writeAuthErr(w, "insufficient permissions")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuth extracts and verifies the JWT if present, but does not reject
// requests without a token. Claims are available via GetClaims(ctx) if valid.
func OptionalAuth(issuer *Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			if token != "" {
				if claims, err := issuer.Verify(token); err == nil {
					ctx := context.WithValue(r.Context(), claimsKey, claims)
					r = r.WithContext(ctx)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearer(r *http.Request) string {
	// Also support the legacy X-API-Key header as a bearer token for backward compat.
	auth := r.Header.Get("Authorization")
	if auth == "" {
		auth = r.Header.Get("X-API-Key")
		if auth != "" {
			auth = "Bearer " + auth
		}
	}
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[len("Bearer "):])
	}
	return ""
}

func writeAuthErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="ragbot"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// GetSubject returns a human-readable actor string from the request context.
// It prefers JWT claims subject, then legacy API key detection, then request ID.
func GetSubject(ctx context.Context, legacyKey string) string {
	if c := GetClaims(ctx); c != nil {
		return c.Sub
	}
	if legacyKey != "" {
		return "apikey"
	}
	return "anonymous"
}
