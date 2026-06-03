package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchTavilyUsesBearerAuth(t *testing.T) {
	var authHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answer": "summary",
			"results": []map[string]string{{
				"title":   "Result",
				"url":     "https://example.com",
				"content": "content",
			}},
		})
	}))
	defer ts.Close()

	p := NewWebSearchPlugin(true, "tavily", "test-key", ts.URL)
	out, err := p.Fallback(context.Background(), "query")
	if err != nil {
		t.Fatal(err)
	}
	if authHeader != "Bearer test-key" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	if !strings.Contains(out, "summary") || !strings.Contains(out, "https://example.com") {
		t.Fatalf("fallback output = %q", out)
	}
}

func TestWebSearchMockWithoutAPIKey(t *testing.T) {
	p := NewWebSearchPlugin(true, "tavily", "", "")
	out, err := p.Fallback(context.Background(), "NebulaQuartz")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "NebulaQuartz") {
		t.Fatalf("fallback output = %q", out)
	}
}
