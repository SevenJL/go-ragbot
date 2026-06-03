// Package middleware provides HTTP middleware for security, observability,
// and resilience. All middleware composes with the standard net/http chain.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// contextKey is an unexported type to prevent collisions in context values.
type contextKey string

const (
	RequestIDKey contextKey = "request_id"
)

// ---------------------------------------------------------------------------
// RequestID — injects a unique ID into every request for tracing through logs.
// ---------------------------------------------------------------------------

// RequestID reads an existing X-Request-ID header or generates a new one,
// attaches it to the request context, and sets the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), RequestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GetRequestID extracts the request ID from a context, or returns "".
func GetRequestID(ctx context.Context) string {
	if v, ok := ctx.Value(RequestIDKey).(string); ok {
		return v
	}
	return ""
}

// ---------------------------------------------------------------------------
// Recovery — catches panics in handlers and returns 500 instead of crashing.
// ---------------------------------------------------------------------------

// Recovery returns middleware that recovers from panics, logs the stack
// trace, and responds with HTTP 500.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				reqID := GetRequestID(r.Context())
				log.Printf("[RECOVERY] req=%s panic=%v\n%s", reqID, rec, string(stack))
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// SecurityHeaders — adds common security-hardening response headers.
// ---------------------------------------------------------------------------

// SecurityHeaders injects a set of recommended security headers:
//
//	X-Content-Type-Options: nosniff
//	X-Frame-Options: DENY
//	X-XSS-Protection: 0           (deprecated; CSP is the modern approach)
//	Referrer-Policy: strict-origin-when-cross-origin
//	Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'
//	Strict-Transport-Security: max-age=63072000; includeSubDomains (only when request is HTTPS)
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// CSP: allow inline style/script for the embedded web console.
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		// HSTS only on HTTPS connections.
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// CORS — permissive for local dev; configurable for production.
// ---------------------------------------------------------------------------

// CORSConfig controls cross-origin behaviour.
type CORSConfig struct {
	AllowedOrigins []string // e.g. ["https://app.example.com"]; empty = allow all (*)
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         int // seconds for preflight cache
}

// DefaultCORS returns a permissive dev-friendly CORS config.
func DefaultCORS() CORSConfig {
	return CORSConfig{
		AllowedOrigins: nil, // allow all origins in dev
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "X-API-Key", "X-Request-ID"},
		MaxAge:         86400,
	}
}

// CORS returns middleware that handles cross-origin requests per cfg.
func CORS(cfg CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowOrigin := ""
			if len(cfg.AllowedOrigins) == 0 {
				allowOrigin = "*"
			} else {
				for _, o := range cfg.AllowedOrigins {
					if strings.EqualFold(o, origin) || o == "*" {
						allowOrigin = o
						break
					}
				}
			}
			if allowOrigin == "" && origin != "" {
				// Not an allowed origin; reject the CORS request.
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowedMethods, ", "))
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowedHeaders, ", "))
			w.Header().Set("Access-Control-Max-Age", itoa(cfg.MaxAge))
			if allowOrigin != "*" {
				w.Header().Set("Vary", "Origin")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ---------------------------------------------------------------------------
// RateLimiter — per-IP token-bucket rate limiter.
// ---------------------------------------------------------------------------

// RateLimiter is a simple per-IP token-bucket rate limiter. It is safe for
// concurrent use. Burst allows short spikes; rate defines the sustained refill
// rate in requests per second.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	rate     float64 // tokens per second
	burst    int     // max tokens
	cleanup  time.Time
}

type tokenBucket struct {
	tokens   float64
	lastFill time.Time
}

// NewRateLimiter creates a per-IP rate limiter. rate is requests-per-second,
// burst is the maximum burst size. Uses a periodic sweep to evict idle entries.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	if rate <= 0 {
		rate = 10
	}
	if burst <= 0 {
		burst = 20
	}
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		cleanup: time.Now(),
	}
}

// Allow reports whether a request from ip should be permitted.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	// Sweep stale entries every 5 minutes.
	if now.Sub(rl.cleanup) > 5*time.Minute {
		for k, b := range rl.buckets {
			if now.Sub(b.lastFill) > 10*time.Minute {
				delete(rl.buckets, k)
			}
		}
		rl.cleanup = now
	}

	b, ok := rl.buckets[ip]
	if !ok {
		b = &tokenBucket{tokens: float64(rl.burst), lastFill: now}
		rl.buckets[ip] = b
	}

	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastFill).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastFill = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Limit returns HTTP middleware that applies the rate limiter per client IP.
func (rl *RateLimiter) Limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !rl.Allow(ip) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the client IP from headers, falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	// Check common proxy headers in order of trust.
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i >= 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	if fwd := r.Header.Get("X-Real-IP"); fwd != "" {
		return strings.TrimSpace(fwd)
	}
	// Fall back to RemoteAddr; strip port.
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

// ---------------------------------------------------------------------------
// MaxBytes — limits request body size.
// ---------------------------------------------------------------------------

// MaxBytes returns middleware that limits the request body to n bytes for
// methods that typically carry a body (POST, PUT, PATCH).
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch:
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// Timeout — per-request timeout wrapper.
// ---------------------------------------------------------------------------

// Timeout returns middleware that cancels the request context after d.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ---------------------------------------------------------------------------
// Compose — applies a chain of middleware (first argument is outermost).
// ---------------------------------------------------------------------------

// Chain applies middlewares in order: Chain(m1, m2, m3)(h) = m1(m2(m3(h))).
// The first argument is the outermost (runs first on entry, last on exit).
func Chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}
