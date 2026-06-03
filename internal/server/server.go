// Package server exposes the engine over HTTP with a security-hardened
// middleware chain and serves an embedded web console.
package server

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ragbot/internal/middleware"
	"ragbot/internal/rag"
	"ragbot/internal/skill"
)

//go:embed index.html
var indexHTML []byte

// ServerConfig holds options for the HTTP layer.
type ServerConfig struct {
	APIKey      string
	CORS        middleware.CORSConfig
	RateLimitRPS float64 // requests per second per IP; 0 = no limit
	RateBurst   int     // max burst; 0 = 2× RPS
}

// Server holds the engine and HTTP configuration.
type Server struct {
	engine      *rag.Engine
	mux         *http.ServeMux
	cfg         ServerConfig
	rateLimiter *middleware.RateLimiter
}

// New creates a Server. apiKey is "" for no auth.
func New(engine *rag.Engine, apiKey string) *Server {
	return NewWithConfig(engine, ServerConfig{
		APIKey:       apiKey,
		CORS:         middleware.DefaultCORS(),
		RateLimitRPS: 10,
		RateBurst:    30,
	})
}

// NewWithConfig creates a Server with the full middleware configuration.
func NewWithConfig(engine *rag.Engine, cfg ServerConfig) *Server {
	s := &Server{engine: engine, mux: http.NewServeMux(), cfg: cfg}
	if cfg.RateLimitRPS > 0 {
		burst := cfg.RateBurst
		if burst <= 0 {
			burst = int(cfg.RateLimitRPS * 3)
		}
		s.rateLimiter = middleware.NewRateLimiter(cfg.RateLimitRPS, burst)
	}
	s.routes()
	return s
}

// Handler returns the full middleware chain:
//
//	Recovery → RequestID → SecurityHeaders → CORS → RateLimit → Logging → MaxBytes(1MB)
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux

	// Request body limit (1 MB for general API calls; upload has its own limit).
	h = middleware.MaxBytes(1 << 20)(h)

	// Structured request logging (innermost to capture accurate timing).
	h = withStructuredLogging(h)

	// Rate limiting (if configured).
	if s.rateLimiter != nil {
		h = s.rateLimiter.Limit(h)
	}

	// CORS.
	h = middleware.CORS(s.cfg.CORS)(h)

	// Security headers.
	h = middleware.SecurityHeaders(h)

	// Request ID injection.
	h = middleware.RequestID(h)

	// Panic recovery (outermost so it catches panics from everything below).
	h = middleware.Recovery(h)

	return h
}

func (s *Server) routes() {
	// API v1 — all endpoints are versioned.
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/chat", s.withAPIAuth(s.handleChat))
	s.mux.HandleFunc("/api/v1/upload", s.withAPIAuth(s.handleUpload))
	s.mux.HandleFunc("/api/v1/docs", s.withAPIAuth(s.handleDocs))
	s.mux.HandleFunc("/api/v1/plugins", s.withAPIAuth(s.handlePlugins))
	s.mux.HandleFunc("/api/v1/plugins/toggle", s.withAPIAuth(s.handlePluginToggle))
	s.mux.HandleFunc("/api/v1/skills", s.withAPIAuth(s.handleSkills))

	// Backward-compatible /api/ paths redirect to v1.
	legacy := map[string]string{
		"/api/health":        "/api/v1/health",
		"/api/chat":          "/api/v1/chat",
		"/api/upload":        "/api/v1/upload",
		"/api/docs":          "/api/v1/docs",
		"/api/plugins":       "/api/v1/plugins",
		"/api/plugins/toggle": "/api/v1/plugins/toggle",
		"/api/skills":        "/api/v1/skills",
	}
	for old, new := range legacy {
		target := new // capture
		s.mux.HandleFunc(old, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})
	}

	// Web console (last so it catches / only).
	s.mux.HandleFunc("/", s.handleIndex)
}

// withStructuredLogging logs requests as JSON with request ID and duration.
func withStructuredLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &logWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		dur := time.Since(start).Round(time.Microsecond)
		reqID := middleware.GetRequestID(r.Context())
		log.Printf(`{"req_id":"%s","method":"%s","path":"%s","status":%d,"duration_ms":%.3f}`,
			reqID, r.Method, r.URL.Path, lw.status, float64(dur.Microseconds())/1000)
	})
}

// logWriter captures the status code written.
type logWriter struct {
	http.ResponseWriter
	status int
}

func (lw *logWriter) WriteHeader(code int) {
	lw.status = code
	lw.ResponseWriter.WriteHeader(code)
}

func (s *Server) withAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" || validAPIKey(r, s.cfg.APIKey) {
			next(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="ragbot"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized")
	}
}

func validAPIKey(r *http.Request, want string) bool {
	got := r.Header.Get("X-API-Key")
	if got == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			got = strings.TrimSpace(auth[len("Bearer "):])
		}
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"version":   "v1",
		"chunks":    s.engine.Store().Count(),
		"sessions":  s.engine.Sessions().Count(),
		"plugins":   len(s.engine.Plugins().All()),
		"skills":    len(s.engine.Skills().All()),
		"embedder":  s.engine.EmbedderName(),
		"llm":       s.engine.LLMName(),
	})
}

type chatReq struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	if req.Message == "" {
		writeErr(w, http.StatusBadRequest, "empty message")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	res, err := s.engine.Answer(ctx, req.SessionID, req.Message)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "parse form: "+err.Error())
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "missing file field: "+err.Error())
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read file: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	docID, n, err := s.engine.Ingest(ctx, hdr.Filename, data)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"doc_id": docID, "filename": hdr.Filename, "chunks": n,
	})
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.engine.Store().Docs())
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeErr(w, http.StatusBadRequest, "missing id")
			return
		}
		if err := s.engine.Store().Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or DELETE")
	}
}

type pluginView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	var out []pluginView
	for _, p := range s.engine.Plugins().All() {
		out = append(out, pluginView{p.Name(), p.Description(), p.IsEnabled()})
	}
	writeJSON(w, http.StatusOK, out)
}

type toggleReq struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func (s *Server) handlePluginToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req toggleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	p := s.engine.Plugins().Get(req.Name)
	if p == nil {
		writeErr(w, http.StatusNotFound, "no such plugin: "+req.Name)
		return
	}
	p.SetEnabled(req.Enabled)
	writeJSON(w, http.StatusOK, pluginView{p.Name(), p.Description(), p.IsEnabled()})
}

// skillView is the JSON representation of a skill returned by the API.
type skillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dynamic     bool   `json:"dynamic"`
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSkills(w, r)
	case http.MethodPost:
		s.registerSkill(w, r)
	case http.MethodDelete:
		s.unregisterSkill(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET, POST or DELETE")
	}
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	var out []skillView
	for _, sk := range s.engine.Skills().All() {
		_, isDynamic := sk.(*skill.ConfigurableSkill)
		out = append(out, skillView{Name: sk.Name(), Description: sk.Description(), Dynamic: isDynamic})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) registerSkill(w http.ResponseWriter, r *http.Request) {
	var def skill.SkillDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if existing := s.engine.Skills().Get(def.Name); existing != nil {
		writeErr(w, http.StatusConflict, "skill '"+def.Name+"' already exists; DELETE it first or use a different name")
		return
	}
	sk, err := skill.NewConfigurableSkill(def)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.engine.Skills().Register(sk)
	log.Printf("skills: registered dynamic skill %q (%d steps)", sk.Name(), len(def.Steps))
	writeJSON(w, http.StatusCreated, skillView{Name: sk.Name(), Description: sk.Description(), Dynamic: true})
}

func (s *Server) unregisterSkill(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "missing 'name' query parameter")
		return
	}
	sk := s.engine.Skills().Get(name)
	if sk == nil {
		writeErr(w, http.StatusNotFound, "no such skill: "+name)
		return
	}
	if _, isDynamic := sk.(*skill.ConfigurableSkill); !isDynamic {
		writeErr(w, http.StatusForbidden, "cannot unregister built-in skill '"+name+"'; only runtime-created skills can be removed")
		return
	}
	if !s.engine.Skills().Unregister(name) {
		writeErr(w, http.StatusInternalServerError, "failed to unregister")
		return
	}
	log.Printf("skills: unregistered dynamic skill %q", name)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}
