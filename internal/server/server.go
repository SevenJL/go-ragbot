// Package server exposes the engine over HTTP with a security-hardened
// middleware chain and serves an embedded web console.
package server

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"ragbot/internal/audit"
	"ragbot/internal/auth"
	"ragbot/internal/core"
	"ragbot/internal/middleware"
	"ragbot/internal/rag"
	"ragbot/internal/skill"
)

//go:embed index.html
var indexHTML []byte

// ServerConfig holds options for the HTTP layer.
type ServerConfig struct {
	APIKey       string // legacy shared API key (backward compat)
	JWTSecret    string // JWT signing secret; empty = JWT auth disabled
	JWTTTL       time.Duration // token lifetime; 0 = 24h
	CORS         middleware.CORSConfig
	RateLimitRPS float64 // requests per second per IP; 0 = no limit
	RateBurst    int     // max burst; 0 = 2× RPS
	AuditLogPath string  // path to audit log file; empty = stdout only
}

// Server holds the engine and HTTP configuration.
type Server struct {
	engine      *rag.Engine
	mux         *http.ServeMux
	cfg         ServerConfig
	rateLimiter *middleware.RateLimiter
	audit       *audit.Logger
	metrics     *Metrics
	jwtIssuer   *auth.Issuer
}

// JWTIssuer returns the JWT issuer for external token management (e.g., /api/v1/auth/token).
func (s *Server) JWTIssuer() *auth.Issuer { return s.jwtIssuer }

// New creates a Server with sensible defaults.
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
	al, err := audit.NewLogger(cfg.AuditLogPath)
	if err != nil {
		log.Printf("server: audit log disabled: %v", err)
		al, _ = audit.NewLogger("") // fallback to stdout-only
	}

	s := &Server{engine: engine, mux: http.NewServeMux(), cfg: cfg, audit: al,
		metrics: NewMetrics(
			func() int { return engine.Sessions().Count() },
			func() int { return engine.Store().Count() },
		),
	}
	// Initialize JWT issuer if secret is configured.
	if cfg.JWTSecret != "" {
		ttl := cfg.JWTTTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		s.jwtIssuer = auth.NewIssuer(cfg.JWTSecret, ttl)
	}
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

// Audit returns the audit logger for external use (e.g. graceful shutdown).
func (s *Server) Audit() *audit.Logger { return s.audit }

// Handler returns the full middleware chain:
//
//	Recovery → RequestID → JWT(optional) → TenantID → SecurityHeaders → CORS → RateLimit → Logging → MaxBytes(1MB)
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux

	// Request body limit (1 MB for general API calls; upload has its own limit).
	h = middleware.MaxBytes(1 << 20)(h)

	// Structured request logging (innermost to capture accurate timing).
	h = s.withStructuredLogging(h)

	// Rate limiting (if configured).
	if s.rateLimiter != nil {
		h = s.rateLimiter.Limit(h)
	}

	// CORS.
	h = middleware.CORS(s.cfg.CORS)(h)

	// Security headers.
	h = middleware.SecurityHeaders(h)

	// Multi-tenancy — extract tenant ID from header.
	h = middleware.TenantID(h)

	// JWT optional auth — extracts and verifies token if present.
	if s.jwtIssuer != nil {
		h = auth.OptionalAuth(s.jwtIssuer)(h)
	}

	// Request ID injection.
	h = middleware.RequestID(h)

	// Panic recovery (outermost so it catches panics from everything below).
	h = middleware.Recovery(h)

	return h
}

func (s *Server) routes() {
	// API v1.
	s.mux.HandleFunc("/api/v1/health", s.handleHealth)
	s.mux.HandleFunc("/api/v1/chat", s.withAPIAuth(s.handleChat))
	s.mux.HandleFunc("/api/v1/upload", s.withAPIAuth(s.handleUpload))
	s.mux.HandleFunc("/api/v1/docs", s.withAPIAuth(s.handleDocs))
	s.mux.HandleFunc("/api/v1/plugins", s.withAPIAuth(s.handlePlugins))
	s.mux.HandleFunc("/api/v1/plugins/toggle", s.withAPIAuth(s.handlePluginToggle))
	s.mux.HandleFunc("/api/v1/skills", s.withAPIAuth(s.handleSkills))
	// Backup & restore.
	s.mux.HandleFunc("/api/v1/export", s.withAPIAuth(s.handleExport))
	s.mux.HandleFunc("/api/v1/import", s.withAPIAuth(s.handleImport))
	// Metrics (Prometheus).
	s.mux.HandleFunc("/api/v1/metrics", s.metrics.Handler())
	// Auth (JWT token endpoints).
	s.mux.HandleFunc("/api/v1/auth/token", s.handleAuthToken)

	// Backward-compatible /api/ paths redirect to v1.
	legacy := map[string]string{
		"/api/health":         "/api/v1/health",
		"/api/chat":           "/api/v1/chat",
		"/api/upload":         "/api/v1/upload",
		"/api/docs":           "/api/v1/docs",
		"/api/plugins":        "/api/v1/plugins",
		"/api/plugins/toggle": "/api/v1/plugins/toggle",
		"/api/skills":         "/api/v1/skills",
		"/api/export":         "/api/v1/export",
		"/api/import":         "/api/v1/import",
		"/api/metrics":        "/api/v1/metrics",
	}
	for old, new := range legacy {
		target := new
		s.mux.HandleFunc(old, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})
	}

	// Web console (last so it catches / only).
	s.mux.HandleFunc("/", s.handleIndex)
}

// ---------------------------------------------------------------------------
// actors — extract a meaningful actor from a request for audit purposes.
// ---------------------------------------------------------------------------

func (s *Server) actorFromRequest(r *http.Request) string {
	// Use JWT subject if authenticated.
	if claims := auth.GetClaims(r.Context()); claims != nil {
		return claims.Sub
	}
	// Fall back to session ID or request ID.
	if sid := r.URL.Query().Get("session_id"); sid != "" {
		return sid
	}
	return middleware.GetRequestID(r.Context())
}

// ---------------------------------------------------------------------------
// logging
// ---------------------------------------------------------------------------

func (s *Server) withStructuredLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.metrics.RecordRequest()
		start := time.Now()
		lw := &logWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		dur := time.Since(start).Round(time.Microsecond)
		reqID := middleware.GetRequestID(r.Context())
		log.Printf(`{"req_id":"%s","method":"%s","path":"%s","status":%d,"duration_ms":%.3f}`,
			reqID, r.Method, r.URL.Path, lw.status, float64(dur.Microseconds())/1000)
	})
}

type logWriter struct {
	http.ResponseWriter
	status int
}

func (lw *logWriter) WriteHeader(code int) {
	lw.status = code
	lw.ResponseWriter.WriteHeader(code)
}

// ---------------------------------------------------------------------------
// auth
// ---------------------------------------------------------------------------

func (s *Server) withAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow if JWT is valid (claims already in context from OptionalAuth).
		if claims := auth.GetClaims(r.Context()); claims != nil {
			next(w, r)
			return
		}
		// Allow if legacy API key is valid.
		if s.cfg.APIKey != "" && validAPIKey(r, s.cfg.APIKey) {
			next(w, r)
			return
		}
		// Allow if auth is not configured at all (dev mode).
		if s.cfg.APIKey == "" && s.jwtIssuer == nil {
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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// allowedUploadMIME reports whether a MIME type is accepted for upload.
func allowedUploadMIME(mime string) bool {
	switch mime {
	case "text/plain",
		"text/markdown",
		"text/x-markdown",
		"application/pdf",
		"application/octet-stream", // catch-all for unknown
		"":
		return true
	}
	// Accept any text/* or application/* type broadly.
	if strings.HasPrefix(mime, "text/") || strings.HasPrefix(mime, "application/") {
		return true
	}
	return false
}

// detectMIME tries to sniff the MIME type from the file header and filename.
func detectMIME(filename string, header multipart.FileHeader) string {
	// Try the Content-Type from the form.
	if ct := header.Header.Get("Content-Type"); ct != "" {
		return strings.ToLower(strings.TrimSpace(ct))
	}
	// Infer from extension.
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt", ".text":
		return "text/plain"
	default:
		return ""
	}
}

// htmlEscape escapes HTML special characters to prevent XSS in rendered output.
// Note: the frontend uses textContent (safe), but this provides defense-in-depth
// for any future client that renders via innerHTML.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// chat
// ---------------------------------------------------------------------------

type chatReq struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
	Stream    bool   `json:"stream"` // request SSE streaming response
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

	// Streaming also accepted via query param for EventSource compatibility.
	if !req.Stream {
		req.Stream = r.URL.Query().Get("stream") == "true"
	}

	if req.Stream {
		s.handleChatStream(w, r, req)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	s.metrics.RecordChatQuery()

	res, err := s.engine.Answer(ctx, req.SessionID, req.Message)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	sanitizeRetrieved(res)

	nRetrieved := 0
	if res.Retrieved != nil {
		nRetrieved = len(res.Retrieved)
	}
	s.audit.ChatQuery(s.actorFromRequest(r), req.SessionID, res.Source, nRetrieved)

	writeJSON(w, http.StatusOK, res)
}

// handleChatStream writes an SSE (Server-Sent Events) stream.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request, req chatReq) {
	s.metrics.RecordChatQuery()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	// Stream content chunks; metadata is sent after completion.
	res, err := s.engine.StreamAnswer(ctx, req.SessionID, req.Message, func(delta string) error {
		fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(delta, "\n", " "))
		flusher.Flush()
		return nil
	})

	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
		return
	}

	// Send metadata after streaming completes.
	if res != nil {
		sanitizeRetrieved(res)
		meta, _ := json.Marshal(map[string]any{
			"source":     res.Source,
			"skill_name": res.SkillName,
			"retrieved":  res.Retrieved,
		})
		fmt.Fprintf(w, "event: meta\ndata: %s\n\n", meta)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	nRetrieved := 0
	if res != nil && res.Retrieved != nil {
		nRetrieved = len(res.Retrieved)
	}
	source := ""
	if res != nil {
		source = res.Source
	}
	s.audit.ChatQuery(s.actorFromRequest(r), req.SessionID, source, nRetrieved)
}

// ---------------------------------------------------------------------------
// auth — JWT token endpoint
// ---------------------------------------------------------------------------

type tokenReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleAuthToken issues a JWT for valid credentials. Accepts legacy API key
// as password when JWT is not configured, for backward compat.
func (s *Server) handleAuthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req tokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}

	// If JWT issuer is configured, check admin credentials.
	if s.jwtIssuer != nil {
		if !s.validateCredentials(req.Username, req.Password) {
			writeErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		role := s.getUserRole(req.Username)
		tok, err := s.jwtIssuer.Issue(req.Username, role, "")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "token issue failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": tok.Raw,
			"token_type":   "Bearer",
			"expires_in":   int64(s.cfg.JWTTTL.Seconds()),
			"role":         string(role),
		})
		return
	}

	// Fallback: treat legacy API key as a bearer token.
	if s.cfg.APIKey != "" && req.Password == s.cfg.APIKey {
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": s.cfg.APIKey,
			"token_type":   "Bearer",
			"role":         "admin",
		})
		return
	}
	writeErr(w, http.StatusUnauthorized, "invalid credentials")
}

// validateCredentials checks username/password against environment config.
// In production this would check against a database or OAuth provider.
func (s *Server) validateCredentials(username, password string) bool {
	if username == "" || password == "" {
		return false
	}
	// Accept legacy API key as admin password for backward compat.
	if s.cfg.APIKey != "" && password == s.cfg.APIKey {
		return true
	}
	// For demo purposes, accept a simple admin credential.
	if username == "admin" && password == "admin" {
		return true
	}
	return false
}

// getUserRole returns the role for a username. Default: "user".
// Admin users: any user authenticated with the legacy API key.
func (s *Server) getUserRole(username string) auth.Role {
	if username == "admin" {
		return auth.RoleAdmin
	}
	return auth.RoleUser
}

func sanitizeRetrieved(res *rag.AnswerResult) {
	if res == nil || res.Retrieved == nil {
		return
	}
	for i := range res.Retrieved {
		res.Retrieved[i].Text = htmlEscape(res.Retrieved[i].Text)
	}
}

// ---------------------------------------------------------------------------
// upload
// ---------------------------------------------------------------------------

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

	// Validate MIME type.
	mime := detectMIME(hdr.Filename, *hdr)
	if mime != "" && !allowedUploadMIME(mime) {
		writeErr(w, http.StatusUnsupportedMediaType, fmt.Sprintf("unsupported file type: %s", mime))
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "read file: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	s.metrics.RecordDocUpload()

	docID, n, err := s.engine.Ingest(ctx, hdr.Filename, data)
	s.audit.DocUpload(s.actorFromRequest(r), hdr.Filename, docID, n, err)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"doc_id": docID, "filename": hdr.Filename, "chunks": n,
	})
}

// ---------------------------------------------------------------------------
// docs — GET (list), POST (update), DELETE
// ---------------------------------------------------------------------------

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.engine.Store().Docs())
	case http.MethodPost:
		s.handleDocUpdate(w, r)
	case http.MethodDelete:
		s.handleDocDelete(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET, POST or DELETE")
	}
}

func (s *Server) handleDocUpdate(w http.ResponseWriter, r *http.Request) {
	docID := r.URL.Query().Get("id")
	if docID == "" {
		writeErr(w, http.StatusBadRequest, "missing id query parameter")
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

	_, n, err := s.engine.UpdateDoc(ctx, docID, hdr.Filename, data)
	s.audit.DocUpdate(s.actorFromRequest(r), hdr.Filename, docID, n, err)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"doc_id": docID, "filename": hdr.Filename, "chunks": n,
	})
}

func (s *Server) handleDocDelete(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing id")
		return
	}
	err := s.engine.Store().Delete(id)
	s.audit.DocDelete(s.actorFromRequest(r), id, err)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// ---------------------------------------------------------------------------
// export / import
// ---------------------------------------------------------------------------

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	chunks := s.engine.ExportAll()
	s.audit.Export(s.actorFromRequest(r))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=ragbot-export.json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(chunks)
}

type importReq struct {
	Chunks []core.Chunk `json:"chunks"`
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req importReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if len(req.Chunks) == 0 {
		writeErr(w, http.StatusBadRequest, "empty chunks array")
		return
	}
	// Validate chunk structure.
	for i, c := range req.Chunks {
		if c.ID == "" || c.DocID == "" || c.Text == "" {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("chunk %d missing required field (id/doc_id/text)", i))
			return
		}
	}

	err := s.engine.ImportAll(req.Chunks)
	s.audit.Import(s.actorFromRequest(r), len(req.Chunks), err)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "import failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imported_chunks": len(req.Chunks), "total_chunks": s.engine.Store().Count(),
	})
}

// ---------------------------------------------------------------------------
// plugins
// ---------------------------------------------------------------------------

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
	s.audit.PluginToggle(s.actorFromRequest(r), req.Name, req.Enabled)
	writeJSON(w, http.StatusOK, pluginView{p.Name(), p.Description(), p.IsEnabled()})
}

// ---------------------------------------------------------------------------
// skills
// ---------------------------------------------------------------------------

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
	s.audit.SkillRegister(s.actorFromRequest(r), def.Name, len(def.Steps), err)
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
	ok := s.engine.Skills().Unregister(name)
	err := fmt.Errorf("not found")
	if ok {
		err = nil
	}
	s.audit.SkillUnregister(s.actorFromRequest(r), name, err)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "failed to unregister")
		return
	}
	log.Printf("skills: unregistered dynamic skill %q", name)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}
