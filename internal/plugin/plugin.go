// Package plugin provides pluggable hooks around the RAG pipeline.
//
// Each plugin implements a unified interface with two hooks mapped to the
// brief's before_rag(query) / after_rag(answer, context):
//
//	BeforeRAG(query)            -> may short-circuit and answer directly
//	AfterRAG(query, answer)     -> may post-process / rewrite the answer
//
// A plugin may additionally implement FallbackProvider to supply extra context
// when the knowledge base returns no good hits (used by WebSearchPlugin).
//
// Plugins can be enabled/disabled at runtime and are loaded according to the
// config file.
package plugin

import (
	"context"
	"sync"
)

// Result is returned from the plugin hooks.
type Result struct {
	// Handled, when true on a BeforeRAG result, short-circuits the pipeline
	// and returns Answer directly without doing retrieval or calling the LLM.
	Handled bool
	// Answer is the text to return (BeforeRAG short-circuit) or the rewritten
	// answer (AfterRAG).
	Answer string
}

// Plugin is the unified interface every plugin implements.
type Plugin interface {
	Name() string
	Description() string
	IsEnabled() bool
	SetEnabled(bool)

	// BeforeRAG runs before retrieval. Return Handled=true to short-circuit.
	BeforeRAG(ctx context.Context, query string) (*Result, error)
	// AfterRAG runs after the LLM produced answer. Return Handled=true to
	// replace the answer with Result.Answer.
	AfterRAG(ctx context.Context, query, answer string) (*Result, error)
}

// FallbackProvider is an optional capability: when RAG finds nothing useful,
// the engine asks every enabled FallbackProvider for extra context.
type FallbackProvider interface {
	Fallback(ctx context.Context, query string) (extraContext string, err error)
}

// base is embedded by concrete plugins to provide the enable/disable plumbing.
type base struct {
	mu      sync.RWMutex
	enabled bool
}

func (b *base) IsEnabled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.enabled
}

func (b *base) SetEnabled(v bool) {
	b.mu.Lock()
	b.enabled = v
	b.mu.Unlock()
}

// Manager holds the ordered set of plugins.
type Manager struct {
	plugins []Plugin
}

func NewManager() *Manager { return &Manager{} }

func (m *Manager) Register(p Plugin) { m.plugins = append(m.plugins, p) }

func (m *Manager) All() []Plugin { return m.plugins }

func (m *Manager) Get(name string) Plugin {
	for _, p := range m.plugins {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// RunBeforeRAG runs each enabled plugin's BeforeRAG; the first that reports
// Handled wins and short-circuits.
func (m *Manager) RunBeforeRAG(ctx context.Context, query string) (*Result, error) {
	for _, p := range m.plugins {
		if !p.IsEnabled() {
			continue
		}
		r, err := p.BeforeRAG(ctx, query)
		if err != nil {
			return nil, err
		}
		if r != nil && r.Handled {
			return r, nil
		}
	}
	return nil, nil
}

// RunAfterRAG lets enabled plugins rewrite the answer in registration order.
func (m *Manager) RunAfterRAG(ctx context.Context, query, answer string) (string, error) {
	for _, p := range m.plugins {
		if !p.IsEnabled() {
			continue
		}
		r, err := p.AfterRAG(ctx, query, answer)
		if err != nil {
			return answer, err
		}
		if r != nil && r.Handled {
			answer = r.Answer
		}
	}
	return answer, nil
}

// Fallbacks gathers extra context from enabled FallbackProviders.
func (m *Manager) Fallbacks(ctx context.Context, query string) string {
	var parts []string
	for _, p := range m.plugins {
		if !p.IsEnabled() {
			continue
		}
		if fp, ok := p.(FallbackProvider); ok {
			if extra, err := fp.Fallback(ctx, query); err == nil && extra != "" {
				parts = append(parts, extra)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	out := ""
	for _, p := range parts {
		out += p + "\n"
	}
	return out
}
