package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"ragbot/internal/auth"
	"ragbot/internal/config"
	"ragbot/internal/embedding"
	"ragbot/internal/llm"
	"ragbot/internal/middleware"
	"ragbot/internal/plugin"
	"ragbot/internal/rag"
	"ragbot/internal/session"
	"ragbot/internal/skill"
	"ragbot/internal/vectorstore"
)

func testServer(t *testing.T, apiKey string) *Server {
	t.Helper()
	return testServerWithConfig(t, ServerConfig{
		APIKey:       apiKey,
		CORS:         middleware.DefaultCORS(),
		RateLimitRPS: 10,
		RateBurst:    30,
	})
}

func testServerWithConfig(t *testing.T, srvCfg ServerConfig) *Server {
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
	if srvCfg.CORS.AllowedMethods == nil {
		srvCfg.CORS = middleware.DefaultCORS()
	}
	return NewWithConfig(engine, srvCfg)
}

func TestAPIAuthDisabledByDefault(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAPIAuthRequiresBearerOrAPIKey(t *testing.T) {
	srv := testServer(t, "secret")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/plugins", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer status = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"session_id":"s","message":"calculate 2+2"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req.WithContext(context.Background()))
	if rec.Code != http.StatusOK {
		t.Fatalf("x-api-key status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestJWTTokenRejectsDefaultAdminPassword(t *testing.T) {
	srv := testServerWithConfig(t, ServerConfig{
		JWTSecret:     "jwt-secret",
		JWTTTL:        time.Hour,
		AdminUser:     "admin",
		AdminPassword: "configured-secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(`{"username":"admin","password":"admin"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("default credential status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminOnlyRoutesRejectUserJWT(t *testing.T) {
	srv := testServerWithConfig(t, ServerConfig{
		JWTSecret:     "jwt-secret",
		JWTTTL:        time.Hour,
		AdminUser:     "admin",
		AdminPassword: "configured-secret",
	})
	tok, err := auth.NewIssuer("jwt-secret", time.Hour).IssueForTenant("alice", auth.RoleUser, "alice", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	req.Header.Set("Authorization", "Bearer "+tok.Raw)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("user export status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestConfiguredJWTAdminCanAccessAdminRoute(t *testing.T) {
	srv := testServerWithConfig(t, ServerConfig{
		JWTSecret:     "jwt-secret",
		JWTTTL:        time.Hour,
		AdminUser:     "admin",
		AdminPassword: "configured-secret",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(`{"username":"admin","password":"configured-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("token status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken == "" {
		t.Fatal("missing access token")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	req.Header.Set("Authorization", "Bearer "+body.AccessToken)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin export status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminRoutesRemainOpenInDevMode(t *testing.T) {
	srv := testServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("dev export status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health body = %s", rec.Body.String())
	}
}

func TestLegacyAPIPathsRedirect(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("legacy redirect status = %d, want 301", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing X-Content-Type-Options header")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing X-Frame-Options header")
	}
}

func TestCORSHeaders(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing Access-Control-Allow-Origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestRequestIDHeader(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	id := rec.Header().Get("X-Request-ID")
	if len(id) != 16 {
		t.Fatalf("expected 16-char hex request ID, got %q", id)
	}
}

func TestNotFoundForUnknownPath(t *testing.T) {
	srv := testServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown path, got %d", rec.Code)
	}
}

func TestEmbeddedAssetServed(t *testing.T) {
	srv := testServer(t, "")
	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(indexRec, indexReq)

	if indexRec.Code != http.StatusOK {
		t.Fatalf("index status = %d, body = %s", indexRec.Code, indexRec.Body.String())
	}

	match := regexp.MustCompile(`/assets/[^"]+\.css`).FindString(indexRec.Body.String())
	if match == "" {
		t.Fatalf("css asset not found in index: %s", indexRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, match, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("asset content-type = %q", ct)
	}
}
