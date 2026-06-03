package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ragbot/internal/config"
	"ragbot/internal/core"
)

// OpenAI is a client for any OpenAI Chat-Completions-compatible endpoint.
// This covers OpenAI itself plus DeepSeek, Zhipu (智谱), Qwen (通义千问)
// open platforms which all expose /chat/completions.
type OpenAI struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewOpenAI(cfg config.LLMConfig) *OpenAI {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &OpenAI{
		baseURL: base,
		apiKey:  cfg.APIKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *OpenAI) Name() string { return "openai(" + o.model + ")" }

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []core.Message `json:"messages"`
	Stream   bool           `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message core.Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o *OpenAI) Chat(ctx context.Context, messages []core.Message) (string, error) {
	body, err := json.Marshal(chatRequest{Model: o.model, Messages: messages})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, string(raw))
	}
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", fmt.Errorf("decode llm response: %w", err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), nil
}
