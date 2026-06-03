package llm

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"ragbot/internal/core"
)

// Mock is an offline LLM that synthesises a plausible answer from the retrieved
// context and conversation history. It lets the whole pipeline run end-to-end
// without any API key, which is handy for development and tests.
//
// Unlike a trivial echo, this mock:
//   - extracts key sentences from the retrieved context that contain query terms,
//   - quotes them as "sources",
//   - and assembles a short, structured answer.
type Mock struct{}

func NewMock() *Mock { return &Mock{} }

func (m *Mock) Name() string { return "mock" }

func (m *Mock) Chat(ctx context.Context, messages []core.Message) (string, error) {
	var lastUser, system string
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			lastUser = msg.Content
		case "system":
			system = msg.Content
		}
	}

	// Extract context snippets that look like knowledge-base fragments.
	contextSentences := extractRelevantSentences(system, lastUser)

	var b strings.Builder
	b.WriteString("【Mock LLM 回复】以下是基于检索上下文的模拟回答。配置真实 LLM 后可获得更准确答案。\n\n")

	// Build a plausible answer from the retrieved context.
	if len(contextSentences) > 0 {
		b.WriteString("根据知识库检索结果：\n\n")
		for i, s := range contextSentences {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, s)
		}
		b.WriteString("\n**总结**：以上信息表明，")
		// pick the first sentence as the core answer clue
		clue := contextSentences[0]
		clue = strings.TrimRight(clue, "。！？.!?;；,，")
		b.WriteString(clue)
		b.WriteString("。")
	} else {
		b.WriteString("知识库中暂未找到与「")
		b.WriteString(strings.TrimSpace(lastUser))
		b.WriteString("」直接相关的资料。\n\n")
		b.WriteString("建议：\n  1. 上传相关文档以补充知识库\n")
		b.WriteString("  2. 启用 websearch 插件获取联网信息\n")
		b.WriteString("  3. 配置真实 LLM 以利用其自身知识")
	}

	b.WriteString("\n\n---\n你的问题是：")
	b.WriteString(strings.TrimSpace(lastUser))
	return b.String(), nil
}

// extractRelevantSentences picks sentences from the system prompt's context
// block that are semantically related to the query (by simple term overlap).
func extractRelevantSentences(system, query string) []string {
	// Locate the context block marker.
	const marker = "【上下文】"
	idx := strings.Index(system, marker)
	if idx < 0 {
		return nil
	}
	ctxText := system[idx+len(marker):]

	// Build a set of meaningful query tokens for matching.
	queryTokens := tokenSet(query)

	// Split context into sentences and score each by token overlap.
	type scored struct {
		text  string
		score int
	}
	var candidates []scored
	for _, s := range splitSentences(ctxText) {
		s = strings.TrimSpace(s)
		// Skip metadata / empty lines.
		if s == "" || strings.HasPrefix(s, "（") || strings.HasPrefix(s, "(") {
			continue
		}
		s = strings.TrimPrefix(s, "[补充资料]")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ctxTokens := tokenSet(s)
		overlap := 0
		for t := range queryTokens {
			if ctxTokens[t] {
				overlap++
			}
		}
		if overlap > 0 {
			candidates = append(candidates, scored{text: s, score: overlap})
		}
	}

	// Return top 3, sorted by overlap score.
	if len(candidates) > 3 {
		// Simple bubble for small n.
		for i := 0; i < len(candidates); i++ {
			for j := i + 1; j < len(candidates); j++ {
				if candidates[j].score > candidates[i].score {
					candidates[i], candidates[j] = candidates[j], candidates[i]
				}
			}
		}
		candidates = candidates[:3]
	}
	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.text
	}
	return out
}

// tokenSet returns a set of meaningful tokens from s.
func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	runes := []rune(strings.ToLower(s))
	for i := 0; i < len(runes); {
		r := runes[i]
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			// Collect a word token.
			start := i
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
				i++
			}
			tok := string(runes[start:i])
			if len([]rune(tok)) >= 2 { // skip single-char tokens (noisy)
				set[tok] = true
			}
		} else if r >= 0x4E00 && r <= 0x9FFF {
			// CJK: each character is a token, plus bigrams.
			set[string(r)] = true
			if i+1 < len(runes) && runes[i+1] >= 0x4E00 && runes[i+1] <= 0x9FFF {
				set[string(runes[i:i+2])] = true
			}
			i++
		} else {
			i++
		}
	}
	return set
}

// splitSentences splits text on common sentence boundaries.
func splitSentences(text string) []string {
	var out []string
	start := 0
	runes := []rune(text)
	for i, r := range runes {
		switch r {
		case '。', '！', '？', '!', '?', '\n':
			if s := strings.TrimSpace(string(runes[start : i+1])); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(string(runes[start:])); s != "" {
		out = append(out, s)
	}
	return out
}
