// Package llm abstracts the chat-completion backend.
package llm

import (
	"context"
	"fmt"

	"ragbot/internal/config"
	"ragbot/internal/core"
)

// LLM is implemented by every chat backend.
type LLM interface {
	// Chat sends a list of messages and returns the assistant reply text.
	Chat(ctx context.Context, messages []core.Message) (string, error)
	Name() string
}

// New builds an LLM from config.
func New(cfg config.LLMConfig) (LLM, error) {
	switch cfg.Provider {
	case "", "mock":
		return NewMock(), nil
	case "openai", "deepseek", "zhipu", "qwen", "compatible":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("llm provider %q requires an api_key", cfg.Provider)
		}
		return NewOpenAI(cfg), nil
	default:
		return nil, fmt.Errorf("unknown llm provider: %s", cfg.Provider)
	}
}
