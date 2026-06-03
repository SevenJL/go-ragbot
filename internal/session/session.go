// Package session keeps per-conversation state: chat history plus any active
// multi-turn skill.
package session

import (
	"sync"

	"ragbot/internal/core"
)

// Session is the mutable state for one conversation.
type Session struct {
	mu sync.Mutex

	ID string

	// History is the running chat transcript (used for LLM context).
	History []core.Message

	// Active skill state. ActiveSkill is "" when no skill is running.
	ActiveSkill string
	SkillStep   int
	SkillData   map[string]string
}

// Lock serializes mutations to a single conversation. The engine holds this
// for one full answer turn so multi-turn skills cannot interleave steps.
func (s *Session) Lock() { s.mu.Lock() }

func (s *Session) Unlock() { s.mu.Unlock() }

// StartSkill initialises skill state on the session.
func (s *Session) StartSkill(name string) {
	s.ActiveSkill = name
	s.SkillStep = 0
	s.SkillData = map[string]string{}
}

// EndSkill clears skill state (returns the session to RAG mode).
func (s *Session) EndSkill() {
	s.ActiveSkill = ""
	s.SkillStep = 0
	s.SkillData = nil
}

func (s *Session) AddMessage(role, content string) {
	s.History = append(s.History, core.Message{Role: role, Content: content})
	// keep history bounded
	const maxTurns = 20
	if len(s.History) > maxTurns {
		s.History = s.History[len(s.History)-maxTurns:]
	}
}

// Store is a thread-safe registry of sessions.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewStore() *Store {
	return &Store{sessions: map[string]*Session{}}
}

// Get returns the session for id, creating it if needed.
func (st *Store) Get(id string) *Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sessions[id]
	if !ok {
		s = &Session{ID: id}
		st.sessions[id] = s
	}
	return s
}
