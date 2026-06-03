// Package config loads runtime configuration from a JSON file.
// JSON (stdlib) is used instead of YAML so the project has zero external
// dependencies; swap in gopkg.in/yaml.v3 if you prefer YAML.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type ServerConfig struct {
	Addr   string `json:"addr"`
	APIKey string `json:"api_key"`
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
	b, err := os.ReadFile(path)
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
	return &c, nil
}

func (c *Config) expandEnv() {
	c.Server.Addr = os.ExpandEnv(c.Server.Addr)
	c.Server.APIKey = os.ExpandEnv(c.Server.APIKey)

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

// Enabled reports whether name is present in list.
func Enabled(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}
