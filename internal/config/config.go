// Package config loads runtime configuration from a JSON file.
// JSON (stdlib) is used instead of YAML so the project has zero external
// dependencies; swap in gopkg.in/yaml.v3 if you prefer YAML.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ServerConfig struct {
	Addr          string `json:"addr"`
	APIKey        string `json:"api_key"`
	JWTSecret     string `json:"jwt_secret"`
	JWTTTL        string `json:"jwt_ttl"`
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
}

type LLMConfig struct {
	Provider string `json:"provider"` // "openai" (OpenAI-compatible) | "mock"
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

type EmbeddingConfig struct {
	Provider string `json:"provider"` // "local" | "openai"
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	Dim      int    `json:"dim"` // dimension for the local embedder
}

type RAGConfig struct {
	ChunkSize    int     `json:"chunk_size"`
	ChunkOverlap int     `json:"chunk_overlap"`
	TopK         int     `json:"top_k"`
	MinScore     float64 `json:"min_score"` // below this a hit is treated as "no result"
	StorePath    string  `json:"store_path"`
}

type WebSearchConfig struct {
	Provider string `json:"provider"` // "tavily" | "mock"
	APIKey   string `json:"api_key"`
	Endpoint string `json:"endpoint"`
}

type PluginsConfig struct {
	Enabled   []string        `json:"enabled"`
	WebSearch WebSearchConfig `json:"websearch"`
}

type EmailConfig struct {
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

type WeatherConfig struct {
	Provider string `json:"provider"` // "mock" | "open-meteo"
	APIKey   string `json:"api_key"`
}

type SkillsConfig struct {
	Enabled []string      `json:"enabled"`
	Email   EmailConfig   `json:"email"`
	Weather WeatherConfig `json:"weather"`
}

type Config struct {
	Server    ServerConfig    `json:"server"`
	LLM       LLMConfig       `json:"llm"`
	Embedding EmbeddingConfig `json:"embedding"`
	RAG       RAGConfig       `json:"rag"`
	Plugins   PluginsConfig   `json:"plugins"`
	Skills    SkillsConfig    `json:"skills"`
}

// Load reads and parses the config file, applying sensible defaults.
func Load(path string) (*Config, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	b, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.expandEnv()
	c.applyDefaults()
	c.resolveRelativePaths(filepath.Dir(resolved))
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}
	return &c, nil
}

func resolvePath(path string) (string, error) {
	if path == "" {
		path = "config.json"
	}
	if _, err := os.Stat(path); err == nil {
		return filepath.Abs(path)
	} else if !os.IsNotExist(err) {
		return "", err
	} else if path != "config.json" || filepath.IsAbs(path) || filepath.Dir(path) != "." {
		return "", err
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(wd, path)
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Abs(candidate)
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return "", os.ErrNotExist
}

func (c *Config) expandEnv() {
	c.Server.Addr = os.ExpandEnv(c.Server.Addr)
	c.Server.APIKey = os.ExpandEnv(c.Server.APIKey)
	c.Server.JWTSecret = os.ExpandEnv(c.Server.JWTSecret)
	c.Server.JWTTTL = os.ExpandEnv(c.Server.JWTTTL)
	c.Server.AdminUsername = os.ExpandEnv(c.Server.AdminUsername)
	c.Server.AdminPassword = os.ExpandEnv(c.Server.AdminPassword)

	c.LLM.Provider = os.ExpandEnv(c.LLM.Provider)
	c.LLM.BaseURL = os.ExpandEnv(c.LLM.BaseURL)
	c.LLM.APIKey = os.ExpandEnv(c.LLM.APIKey)
	c.LLM.Model = os.ExpandEnv(c.LLM.Model)

	c.Embedding.Provider = os.ExpandEnv(c.Embedding.Provider)
	c.Embedding.BaseURL = os.ExpandEnv(c.Embedding.BaseURL)
	c.Embedding.APIKey = os.ExpandEnv(c.Embedding.APIKey)
	c.Embedding.Model = os.ExpandEnv(c.Embedding.Model)

	c.RAG.StorePath = os.ExpandEnv(c.RAG.StorePath)

	for i := range c.Plugins.Enabled {
		c.Plugins.Enabled[i] = os.ExpandEnv(c.Plugins.Enabled[i])
	}
	c.Plugins.WebSearch.Provider = os.ExpandEnv(c.Plugins.WebSearch.Provider)
	c.Plugins.WebSearch.APIKey = os.ExpandEnv(c.Plugins.WebSearch.APIKey)
	c.Plugins.WebSearch.Endpoint = os.ExpandEnv(c.Plugins.WebSearch.Endpoint)

	for i := range c.Skills.Enabled {
		c.Skills.Enabled[i] = os.ExpandEnv(c.Skills.Enabled[i])
	}
	c.Skills.Email.SMTPHost = os.ExpandEnv(c.Skills.Email.SMTPHost)
	c.Skills.Email.Username = os.ExpandEnv(c.Skills.Email.Username)
	c.Skills.Email.Password = os.ExpandEnv(c.Skills.Email.Password)
	c.Skills.Email.From = os.ExpandEnv(c.Skills.Email.From)
	c.Skills.Weather.Provider = os.ExpandEnv(c.Skills.Weather.Provider)
	c.Skills.Weather.APIKey = os.ExpandEnv(c.Skills.Weather.APIKey)
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Embedding.Dim <= 0 {
		c.Embedding.Dim = 256
	}
	if c.RAG.ChunkSize <= 0 {
		c.RAG.ChunkSize = 500
	}
	if c.RAG.ChunkOverlap < 0 {
		c.RAG.ChunkOverlap = 0
	}
	if c.RAG.TopK <= 0 {
		c.RAG.TopK = 4
	}
	if c.RAG.StorePath == "" {
		c.RAG.StorePath = "data/vectorstore.json"
	}
}

func (c *Config) resolveRelativePaths(baseDir string) {
	if c.RAG.StorePath != "" && !filepath.IsAbs(c.RAG.StorePath) {
		c.RAG.StorePath = filepath.Join(baseDir, c.RAG.StorePath)
	}
}

// Validate checks that the loaded configuration is internally consistent.
func (c *Config) Validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server.addr must not be empty")
	}
	if c.RAG.ChunkSize <= 0 {
		return fmt.Errorf("rag.chunk_size must be > 0")
	}
	if c.RAG.ChunkOverlap < 0 {
		return fmt.Errorf("rag.chunk_overlap must be >= 0")
	}
	if c.RAG.ChunkOverlap >= c.RAG.ChunkSize {
		return fmt.Errorf("rag.chunk_overlap (%d) must be < chunk_size (%d)", c.RAG.ChunkOverlap, c.RAG.ChunkSize)
	}
	if c.RAG.TopK <= 0 {
		return fmt.Errorf("rag.top_k must be > 0")
	}
	if c.RAG.MinScore < 0 || c.RAG.MinScore > 1 {
		return fmt.Errorf("rag.min_score must be in [0, 1]")
	}
	if c.RAG.StorePath == "" {
		return fmt.Errorf("rag.store_path must not be empty")
	}
	if c.Embedding.Dim <= 0 && c.Embedding.Provider == "local" {
		return fmt.Errorf("embedding.dim must be > 0 for local provider")
	}
	// Validate LLM provider.
	switch c.LLM.Provider {
	case "", "mock", "openai", "deepseek", "zhipu", "qwen", "compatible":
		// ok
	default:
		return fmt.Errorf("unknown llm.provider: %s", c.LLM.Provider)
	}
	// Validate embedding provider.
	switch c.Embedding.Provider {
	case "", "local", "openai", "compatible":
		// ok
	default:
		return fmt.Errorf("unknown embedding.provider: %s", c.Embedding.Provider)
	}
	return nil
}

// Enabled reports whether name is present in list.
func Enabled(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
