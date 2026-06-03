package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHandlesBOMEnvAndDefaults(t *testing.T) {
	t.Setenv("RAGBOT_TEST_API_KEY", "secret-token")
	t.Setenv("RAGBOT_TEST_MODEL", "real-model")

	path := filepath.Join(t.TempDir(), "config.json")
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{
		"server": { "api_key": "${RAGBOT_TEST_API_KEY}" },
		"llm": { "provider": "mock", "model": "$RAGBOT_TEST_MODEL" },
		"embedding": { "provider": "local" },
		"rag": {},
		"plugins": { "enabled": ["time"] },
		"skills": { "enabled": [] }
	}`)...)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Fatalf("default addr = %q", cfg.Server.Addr)
	}
	if cfg.Server.APIKey != "secret-token" {
		t.Fatalf("api key was not expanded: %q", cfg.Server.APIKey)
	}
	if cfg.LLM.Model != "real-model" {
		t.Fatalf("model was not expanded: %q", cfg.LLM.Model)
	}
	if cfg.Embedding.Dim != 256 {
		t.Fatalf("default embedding dim = %d", cfg.Embedding.Dim)
	}
	if cfg.RAG.StorePath != "data/vectorstore.json" {
		t.Fatalf("default store path = %q", cfg.RAG.StorePath)
	}
}

func TestEnabled(t *testing.T) {
	if !Enabled([]string{"time", "calculator"}, "calculator") {
		t.Fatal("expected calculator to be enabled")
	}
	if Enabled([]string{"time"}, "weather") {
		t.Fatal("did not expect weather to be enabled")
	}
}
