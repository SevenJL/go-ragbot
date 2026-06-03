// Package server exposes the engine over HTTP and serves a small web console.
package server

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"ragbot/internal/rag"
)

//go:embed index.html
var indexHTML []byte

type Server struct {
	engine *rag.Engine
	mux    *http.ServeMux
	apiKey string
}

func New(engine *rag.Engine, apiKey string) *Server {
	s := &Server{engine: engine, mux: http.NewServeMux(), apiKey: apiKey}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/chat", s.withAPIAuth(s.handleChat))
	s.mux.HandleFunc("/api/upload", s.withAPIAuth(s.handleUpload))
	s.mux.HandleFunc("/api/docs", s.withAPIAuth(s.handleDocs))
	s.mux.HandleFunc("/api/plugins", s.withAPIAuth(s.handlePlugins))
	s.mux.HandleFunc("/api/plugins/toggle", s.withAPIAuth(s.handlePluginToggle))
	s.mux.HandleFunc("/api/skills", s.withAPIAuth(s.handleSkills))
}

func (s *Server) withAPIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.apiKey == "" || validAPIKey(r, s.apiKey) {
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

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	type skillView struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var out []skillView
	for _, sk := range s.engine.Skills().All() {
		out = append(out, skillView{sk.Name(), sk.Description()})
	}
	writeJSON(w, http.StatusOK, out)
}
