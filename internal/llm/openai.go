package llm

import (
	"bufio"
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

// streamChunk is one SSE data payload from a streaming chat completion.
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// StreamChat sends a streaming chat completion request. Each content delta is
// passed to onChunk as it arrives. Returns the full accumulated text.
func (o *OpenAI) StreamChat(ctx context.Context, messages []core.Message, onChunk func(delta string) error) (string, error) {
	body, err := json.Marshal(chatRequest{Model: o.model, Messages: messages, Stream: true})
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
	req.Header.Set("Accept", "text/event-stream")

	// Use a longer timeout for streaming; the response stays open.
	streamClient := &http.Client{Timeout: 180 * time.Second}
	resp, err := streamClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("llm stream http %d: %s", resp.StatusCode, string(raw))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// Some providers send larger lines; bump buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var sc streamChunk
		if err := json.Unmarshal([]byte(payload), &sc); err != nil {
			// Skip unparseable lines (some providers send keep-alive comments).
			continue
		}
		for _, ch := range sc.Choices {
			if ch.Delta.Content != "" {
				full.WriteString(ch.Delta.Content)
				if onChunk != nil {
					if err := onChunk(ch.Delta.Content); err != nil {
						return full.String(), err
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), fmt.Errorf("llm stream read: %w", err)
	}
	return strings.TrimSpace(full.String()), nil
}
