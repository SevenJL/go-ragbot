// Package embedding turns text into vectors.
package embedding

import (
	"context"
	"fmt"

	"ragbot/internal/config"
)

// Embedder converts text into fixed-length vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
	Dim() int
	Name() string
}

// New builds an Embedder from config.
func New(cfg config.EmbeddingConfig) (Embedder, error) {
	switch cfg.Provider {
	case "", "local":
		return NewLocal(cfg.Dim), nil
	case "openai", "compatible":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("embedding provider %q requires an api_key", cfg.Provider)
		}
		return NewOpenAI(cfg), nil
	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", cfg.Provider)
	}
}
