package middleware

import (
	"context"
	"net/http"

	"ragbot/internal/core"
)

// TenantID extracts the tenant ID from the X-Tenant-ID header and attaches
// it to the request context. Falls back to "default" if no header is present.
func TenantID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get("X-Tenant-ID")
		if tid == "" {
			tid = "default"
		}
		ctx := context.WithValue(r.Context(), core.TenantCtxKey, tid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
