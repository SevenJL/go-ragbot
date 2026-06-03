// Package skill implements stateful, multi-turn task flows (e.g. send email,
// query weather). A skill owns the conversation while active and signals when
// it is finished so the engine can return to RAG mode.
package skill

import (
	"context"
	"strings"
	"sync"

	"ragbot/internal/session"
)

// Skill is a multi-turn task flow.
type Skill interface {
	Name() string
	Description() string

	// Match reports whether a free-text message should trigger this skill,
	// e.g. the user says "我要发邮件". Triggering is also possible
	// programmatically (e.g. via LLM intent detection) by calling Start.
	Match(input string) bool

	// Start begins the flow and returns the first prompt to the user.
	Start(ctx context.Context, sess *session.Session) (string, error)

	// Handle consumes one user message for the active flow and returns the
	// reply plus done=true when the flow has completed.
	Handle(ctx context.Context, sess *session.Session, input string) (reply string, done bool, err error)
}

// Manager holds the registered skills.
type Manager struct {
	skills   []Skill
	mu       sync.RWMutex
}

func NewManager() *Manager { return &Manager{} }

func (m *Manager) Register(s Skill) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skills = append(m.skills, s)
}

// Unregister removes a skill by name. Returns false if the skill was not found.
// Dynamic skills created at runtime can be removed; built-in skills registered
// at startup will be re-added on the next restart.
func (m *Manager) Unregister(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.skills {
		if s.Name() == name {
			m.skills = append(m.skills[:i], m.skills[i+1:]...)
			return true
		}
	}
	return false
}

func (m *Manager) All() []Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Skill, len(m.skills))
	copy(out, m.skills)
	return out
}

func (m *Manager) Get(name string) Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.skills {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// MatchTrigger returns the first skill whose Match accepts the input.
func (m *Manager) MatchTrigger(input string) Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.skills {
		if s.Match(input) {
			return s
		}
	}
	return nil
}

// containsAny is a small helper shared by skills for keyword matching.
func containsAny(s string, kws []string) bool {
	ls := strings.ToLower(s)
	for _, k := range kws {
		if strings.Contains(ls, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// isCancel reports whether the user wants to abort the current skill.
func isCancel(s string) bool {
	return containsAny(s, []string{"取消", "退出", "算了", "不发了", "cancel", "quit", "exit"})
}
