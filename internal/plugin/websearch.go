package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// WebSearchPlugin implements FallbackProvider: when the knowledge base returns
// no good hits, the engine calls Fallback to fetch web context. It supports a
// Tavily-style JSON API, and a "mock" provider for offline development.
type WebSearchPlugin struct {
	base
	provider string
	apiKey   string
	endpoint string
	client   *http.Client
}

func NewWebSearchPlugin(enabled bool, provider, apiKey, endpoint string) *WebSearchPlugin {
	if provider == "" {
		provider = "mock"
	}
	if endpoint == "" {
		endpoint = "https://api.tavily.com/search"
	}
	p := &WebSearchPlugin{
		provider: provider,
		apiKey:   apiKey,
		endpoint: endpoint,
		client:   &http.Client{Timeout: 20 * time.Second},
	}
	p.SetEnabled(enabled)
	return p
}

func (p *WebSearchPlugin) Name() string { return "websearch" }
func (p *WebSearchPlugin) Description() string {
	return "知识库无结果时联网搜索补充上下文"
}

// BeforeRAG / AfterRAG are no-ops; this plugin works via the Fallback hook.
func (p *WebSearchPlugin) BeforeRAG(ctx context.Context, query string) (*Result, error) {
	return nil, nil
}
func (p *WebSearchPlugin) AfterRAG(ctx context.Context, query, answer string) (*Result, error) {
	return nil, nil
}

// Fallback returns extra context text gathered from the web.
func (p *WebSearchPlugin) Fallback(ctx context.Context, query string) (string, error) {
	if p.provider == "mock" || p.apiKey == "" {
		return mockFallback(query), nil
	}
	return p.tavily(ctx, query)
}

// mockFallback produces a deterministic but helpful placeholder that explains
// what a real web search would return. It extracts a short subject from the
// query so the placeholder feels tailored rather than generic.
func mockFallback(query string) string {
	subject := pickSubject(query)
	var b strings.Builder
	b.WriteString("【联网搜索（mock）】\n")
	b.WriteString("未配置真实搜索 API（plugins.websearch.api_key 为空）。\n")
	b.WriteString("如在生产中使用，这里会返回与「")
	b.WriteString(subject)
	b.WriteString("」相关的网络信息。\n\n")
	b.WriteString("建议操作：\n")
	b.WriteString("  1. 在 config.json 的 plugins.websearch 中填写 Tavily API Key\n")
	b.WriteString("  2. 或切换 provider 为其他搜索后端\n")
	b.WriteString("  3. 也可以上传包含该主题的 PDF/TXT/MD 文档到本地知识库\n")
	b.WriteString("\n【补充说明】知识库中未检索到与「")
	b.WriteString(subject)
	b.WriteString("」直接匹配的片段。请尝试换个提问方式，或补充相关文档。")
	return b.String()
}

// pickSubject extracts a short representative phrase from the query.
func pickSubject(query string) string {
	// Try to use the first 50 runes; cut at the first CJK sentence break.
	runes := []rune(strings.TrimSpace(query))
	maxLen := len(runes)
	if maxLen > 50 {
		maxLen = 50
	}
	cut := maxLen
	for i, r := range runes[:maxLen] {
		switch r {
		case '。', '！', '？', '!', '?', '\n', '；', ';':
			cut = i + 1
			break
		}
	}
	if cut > 0 {
		return strings.TrimSpace(string(runes[:cut]))
	}
	return strings.TrimSpace(query)
}

type tavilyReq struct {
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
	SearchDepth   string `json:"search_depth"`
	IncludeAnswer bool   `json:"include_answer"`
}

type tavilyResp struct {
	Answer  string `json:"answer"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func (p *WebSearchPlugin) tavily(ctx context.Context, query string) (string, error) {
	body, _ := json.Marshal(tavilyReq{
		Query:         query,
		MaxResults:    3,
		SearchDepth:   "basic",
		IncludeAnswer: true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("websearch http %d: %s", resp.StatusCode, string(raw))
	}
	var tr tavilyResp
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("【联网搜索结果】\n")
	if tr.Answer != "" {
		b.WriteString("摘要：" + tr.Answer + "\n")
	}
	for i, r := range tr.Results {
		fmt.Fprintf(&b, "%d. %s (%s)\n%s\n", i+1, r.Title, r.URL, r.Content)
	}
	return b.String(), nil
}
