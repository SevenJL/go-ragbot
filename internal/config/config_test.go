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
	wantStore := filepath.Join(filepath.Dir(path), "data", "vectorstore.json")
	if cfg.RAG.StorePath != wantStore {
		t.Fatalf("default store path = %q", cfg.RAG.StorePath)
	}
}

func TestLoadFindsDefaultConfigInParentDirectory(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
		"server": { "addr": ":18080" },
		"llm": { "provider": "mock" },
		"embedding": { "provider": "local" },
		"rag": { "store_path": "data/test-vectorstore.json" },
		"plugins": { "enabled": [] },
		"skills": { "enabled": [] }
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("config.json")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Addr != ":18080" {
		t.Fatalf("addr = %q", cfg.Server.Addr)
	}
	wantStore := filepath.Join(root, "data", "test-vectorstore.json")
	if cfg.RAG.StorePath != wantStore {
		t.Fatalf("store path = %q, want %q", cfg.RAG.StorePath, wantStore)
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

func TestValidateRejectsInvalidChunkSize(t *testing.T) {
	cfg := Config{
		Server:    ServerConfig{Addr: ":8080"},
		LLM:       LLMConfig{Provider: "mock"},
		Embedding: EmbeddingConfig{Provider: "local", Dim: 256},
		RAG:       RAGConfig{ChunkSize: 0, ChunkOverlap: 0, TopK: 4, MinScore: 0.1, StorePath: "data/v.json"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected chunk_size=0 to fail validation")
	}
}

func TestValidateRejectsBadMinScore(t *testing.T) {
	cfg := Config{
		Server:    ServerConfig{Addr: ":8080"},
		LLM:       LLMConfig{Provider: "mock"},
		Embedding: EmbeddingConfig{Provider: "local", Dim: 256},
		RAG:       RAGConfig{ChunkSize: 500, ChunkOverlap: 0, TopK: 4, MinScore: 1.5, StorePath: "data/v.json"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected min_score=1.5 to fail validation")
	}
}

func TestValidateRejectsUnknownLLMProvider(t *testing.T) {
	cfg := Config{
		Server:    ServerConfig{Addr: ":8080"},
		LLM:       LLMConfig{Provider: "unknown-provider"},
		Embedding: EmbeddingConfig{Provider: "local", Dim: 256},
		RAG:       RAGConfig{ChunkSize: 500, ChunkOverlap: 0, TopK: 4, MinScore: 0.1, StorePath: "data/v.json"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unknown provider to fail validation")
	}
}
