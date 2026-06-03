package embedding

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
)

// OpenAIEmbedder calls an OpenAI-compatible /embeddings endpoint. Many
// providers expose this, and you can also self-host bge-small-zh behind a
// compatible gateway (e.g. via an embeddings server) and point base_url here.
type OpenAIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	client  *http.Client
}

func NewOpenAI(cfg config.EmbeddingConfig) *OpenAIEmbedder {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &OpenAIEmbedder{
		baseURL: base,
		apiKey:  cfg.APIKey,
		model:   model,
		dim:     cfg.Dim, // filled in after first call if 0
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *OpenAIEmbedder) Name() string { return "openai-embed(" + e.model + ")" }
func (e *OpenAIEmbedder) Dim() int     { return e.dim }

type embedReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResp struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	body, err := json.Marshal(embedReq{Model: e.model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed http %d: %s", resp.StatusCode, string(raw))
	}
	var er embedResp
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if er.Error != nil {
		return nil, fmt.Errorf("embed error: %s", er.Error.Message)
	}
	out := make([][]float64, len(er.Data))
	for i, d := range er.Data {
		out[i] = d.Embedding
		if e.dim == 0 {
			e.dim = len(d.Embedding)
		}
	}
	return out, nil
}
