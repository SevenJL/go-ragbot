// Package rag is the orchestrator: it ingests documents and answers messages
// by routing them through skills, plugins, retrieval and the LLM.
package rag

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strings"
	"time"

	"ragbot/internal/config"
	"ragbot/internal/core"
	"ragbot/internal/document"
	"ragbot/internal/embedding"
	"ragbot/internal/llm"
	"ragbot/internal/plugin"
	"ragbot/internal/session"
	"ragbot/internal/skill"
	"ragbot/internal/vectorstore"
)

type Engine struct {
	cfg      config.RAGConfig
	embedder embedding.Embedder
	store    vectorstore.Store
	llm      llm.LLM
	plugins  *plugin.Manager
	skills   *skill.Manager
	sessions *session.Store
}

func New(
	cfg config.RAGConfig,
	emb embedding.Embedder,
	store vectorstore.Store,
	model llm.LLM,
	plugins *plugin.Manager,
	skills *skill.Manager,
	sessions *session.Store,
) *Engine {
	return &Engine{
		cfg:      cfg,
		embedder: emb,
		store:    store,
		llm:      model,
		plugins:  plugins,
		skills:   skills,
		sessions: sessions,
	}
}

func (e *Engine) Plugins() *plugin.Manager { return e.plugins }
func (e *Engine) Skills() *skill.Manager   { return e.skills }
func (e *Engine) Store() vectorstore.Store { return e.store }
func (e *Engine) Sessions() *session.Store { return e.sessions }

// PruneSessions removes idle sessions older than idleTimeout. Sessions with an
// active multi-turn skill in progress are kept regardless of age.
func (e *Engine) PruneSessions(idleTimeout time.Duration) int {
	return e.sessions.Cleanup(idleTimeout)
}

// Ingest loads, chunks, embeds and stores a document. Returns doc id + chunk count.
func (e *Engine) Ingest(ctx context.Context, filename string, data []byte) (string, int, error) {
	text, err := document.LoadText(filename, data)
	if err != nil {
		return "", 0, fmt.Errorf("load %s: %w", filename, err)
	}
	parts := document.Chunk(text, e.cfg.ChunkSize, e.cfg.ChunkOverlap)
	if len(parts) == 0 {
		return "", 0, fmt.Errorf("no extractable text in %s", filename)
	}

	docID := docIDFor(filename, data)
	vecs, err := e.embedder.Embed(ctx, parts)
	if err != nil {
		return "", 0, fmt.Errorf("embed: %w", err)
	}
	chunks := make([]core.Chunk, len(parts))
	for i, p := range parts {
		chunks[i] = core.Chunk{
			ID:        fmt.Sprintf("%s#%d", docID, i),
			DocID:     docID,
			Source:    filename,
			Index:     i,
			Text:      p,
			Embedding: vecs[i],
		}
	}
	if err := e.store.Add(ctx, chunks); err != nil {
		return "", 0, fmt.Errorf("store: %w", err)
	}
	return docID, len(chunks), nil
}

// AnswerResult is the structured outcome of handling a message.
type AnswerResult struct {
	Answer    string                `json:"answer"`
	Source    string                `json:"source"` // "skill" | "plugin" | "rag"
	SkillName string                `json:"skill_name,omitempty"`
	Retrieved []core.RetrievedChunk `json:"retrieved,omitempty"`
}

// Answer handles one user message within a session.
func (e *Engine) Answer(ctx context.Context, sessionID, message string) (*AnswerResult, error) {
	sess := e.sessions.Get(sessionID)
	sess.Lock()
	defer sess.Unlock()

	sess.AddMessage("user", message)

	// 1) If a skill is already running, route the message to it.
	if sess.ActiveSkill != "" {
		sk := e.skills.Get(sess.ActiveSkill)
		if sk == nil { // skill disappeared; reset
			sess.EndSkill()
		} else {
			reply, _, err := sk.Handle(ctx, sess, message)
			if err != nil {
				return nil, err
			}
			sess.AddMessage("assistant", reply)
			return &AnswerResult{Answer: reply, Source: "skill", SkillName: sk.Name()}, nil
		}
	}

	// 2) Skill trigger detection (keyword based; LLM intent could plug in here).
	if sk := e.skills.MatchTrigger(message); sk != nil {
		reply, err := sk.Start(ctx, sess)
		if err != nil {
			return nil, err
		}
		sess.AddMessage("assistant", reply)
		return &AnswerResult{Answer: reply, Source: "skill", SkillName: sk.Name()}, nil
	}

	// 3) Plugin BeforeRAG short-circuit (time, calculator...).
	if r, err := e.plugins.RunBeforeRAG(ctx, message); err != nil {
		return nil, err
	} else if r != nil && r.Handled {
		ans, err := e.plugins.RunAfterRAG(ctx, message, r.Answer)
		if err != nil {
			return nil, err
		}
		sess.AddMessage("assistant", ans)
		return &AnswerResult{Answer: ans, Source: "plugin"}, nil
	}

	// 4) Retrieval.
	retrieved, err := e.retrieve(ctx, message)
	if err != nil {
		return nil, err
	}

	// 5) If nothing useful, ask fallback providers (e.g. websearch) for context.
	var extra string
	if !hasGoodHit(retrieved, e.cfg.MinScore) {
		extra = e.plugins.Fallbacks(ctx, message)
	}

	// 6) Build prompt + call LLM.
	messages := e.buildPrompt(sess, message, retrieved, extra)
	answer, err := e.llm.Chat(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	// 7) Plugin AfterRAG post-processing.
	answer, err = e.plugins.RunAfterRAG(ctx, message, answer)
	if err != nil {
		return nil, err
	}

	sess.AddMessage("assistant", answer)
	return &AnswerResult{Answer: answer, Source: "rag", Retrieved: retrieved}, nil
}

func (e *Engine) retrieve(ctx context.Context, query string) ([]core.RetrievedChunk, error) {
	if e.store.Count() == 0 {
		return nil, nil
	}
	vecs, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	return e.store.Search(ctx, vecs[0], e.cfg.TopK)
}

func (e *Engine) buildPrompt(sess *session.Session, query string, retrieved []core.RetrievedChunk, extra string) []core.Message {
	var ctxBuf strings.Builder
	for i, r := range retrieved {
		if r.Score < e.cfg.MinScore {
			continue
		}
		fmt.Fprintf(&ctxBuf, "[片段%d 来源:%s 相似度:%.3f]\n%s\n\n", i+1, r.Source, r.Score, r.Text)
	}
	if extra != "" {
		ctxBuf.WriteString("[补充资料]\n")
		ctxBuf.WriteString(extra)
		ctxBuf.WriteString("\n")
	}

	ctxText := ctxBuf.String()
	system := "你是一个基于知识库的问答助手。请优先依据下面提供的【上下文】回答用户问题，" +
		"上下文中没有的内容不要编造；如果上下文不足以回答，请明确说明并给出你能提供的最相关信息。\n\n" +
		"【上下文】\n"
	if strings.TrimSpace(ctxText) == "" {
		system += "（本次没有检索到相关资料）\n"
	} else {
		system += ctxText
	}

	msgs := []core.Message{{Role: "system", Content: system}}

	// include a little prior history (excluding the just-added user msg, which
	// we append explicitly as the final turn)
	hist := sess.History
	if len(hist) > 0 {
		hist = hist[:len(hist)-1]
	}
	start := 0
	if len(hist) > 6 {
		start = len(hist) - 6
	}
	msgs = append(msgs, hist[start:]...)
	msgs = append(msgs, core.Message{Role: "user", Content: query})
	return msgs
}

func hasGoodHit(rs []core.RetrievedChunk, minScore float64) bool {
	for _, r := range rs {
		if r.Score >= minScore {
			return true
		}
	}
	return false
}

func docIDFor(filename string, data []byte) string {
	h := sha1.New()
	h.Write([]byte(filename))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}
