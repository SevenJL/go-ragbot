package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ragbot/internal/config"
	"ragbot/internal/embedding"
	"ragbot/internal/llm"
	"ragbot/internal/plugin"
	"ragbot/internal/rag"
	"ragbot/internal/session"
	"ragbot/internal/skill"
	"ragbot/internal/vectorstore"
)

func testServer(t *testing.T, apiKey string) *Server {
	t.Helper()
	emb := embedding.NewLocal(32)
	store, err := vectorstore.NewMemory("")
	if err != nil {
		t.Fatal(err)
	}
	pm := plugin.NewManager()
	pm.Register(plugin.NewCalculatorPlugin(true))
	sm := skill.NewManager()
	engine := rag.New(config.RAGConfig{TopK: 2, MinScore: 0.1}, emb, store, llm.NewMock(), pm, sm, session.NewStore())
	return New(engine, apiKey)
}

func TestAPIAuthDisabledByDefault(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIAuthRequiresBearerOrAPIKey(t *testing.T) {
	srv := testServer(t, "secret")

	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"session_id":"s","message":"calculate 2+2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req.WithContext(context.Background()))
	if rec.Code != http.StatusOK {
		t.Fatalf("x-api-key status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
