package session

import (
	"sync"
	"testing"
	"time"
)

func TestStoreCreatesSessionOnGet(t *testing.T) {
	st := NewStore()
	s := st.Get("s1")
	if s.ID != "s1" {
		t.Fatalf("id = %q", s.ID)
	}
	if st.Count() != 1 {
		t.Fatalf("count = %d", st.Count())
	}
}

func TestStoreReturnsSameSession(t *testing.T) {
	st := NewStore()
	a := st.Get("same")
	b := st.Get("same")
	if a != b {
		t.Fatal("expected same pointer")
	}
}

func TestSessionAddMessageBoundsHistory(t *testing.T) {
	s := &Session{ID: "test"}
	for i := 0; i < 25; i++ {
		s.AddMessage("user", "msg")
	}
	if len(s.History) > 20 {
		t.Fatalf("history too long: %d", len(s.History))
	}
	if s.History[0].Content != "msg" {
		t.Fatalf("oldest kept should be msg, got %q", s.History[0].Content)
	}
}

func TestSessionSkillLifecycle(t *testing.T) {
	s := &Session{ID: "test"}
	if s.ActiveSkill != "" {
		t.Fatal("expected no active skill initially")
	}

	s.StartSkill("weather")
	if s.ActiveSkill != "weather" {
		t.Fatalf("active skill = %q", s.ActiveSkill)
	}
	if s.SkillStep != 0 {
		t.Fatalf("skill step = %d", s.SkillStep)
	}
	if s.SkillData == nil {
		t.Fatal("skill data should be non-nil")
	}

	s.SkillStep = 2
	s.SkillData["city"] = "Beijing"
	s.EndSkill()
	if s.ActiveSkill != "" {
		t.Fatal("skill should be cleared after EndSkill")
	}
	if s.SkillData != nil {
		t.Fatal("skill data should be nil after EndSkill")
	}
}

func TestSessionLockSerialisesAccess(t *testing.T) {
	s := &Session{ID: "test"}
	var wg sync.WaitGroup
	wg.Add(2)
	var last time.Time
	go func() {
		defer wg.Done()
		s.Lock()
		time.Sleep(10 * time.Millisecond)
		s.AddMessage("user", "first")
		s.Unlock()
	}()
	go func() {
		defer wg.Done()
		s.Lock()
		time.Sleep(10 * time.Millisecond)
		s.AddMessage("user", "second")
		s.Unlock()
	}()
	wg.Wait()
	_ = last
	if len(s.History) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(s.History))
	}
}

func TestSessionLastAccessUpdatedOnLock(t *testing.T) {
	s := &Session{ID: "test"}
	before := time.Now()
	// Small sleep to ensure time advances.
	time.Sleep(time.Millisecond)
	s.Lock()
	s.Unlock()
	if s.LastAccess.Before(before) {
		t.Fatal("LastAccess should be updated on Lock")
	}
}

func TestCleanupPrunesIdleSessions(t *testing.T) {
	st := NewStore()
	a := st.Get("active")
	a.Lock()
	a.AddMessage("user", "hello")
	a.Unlock()

	b := st.Get("idle")
	b.Lock()
	b.Unlock()

	// Set idle session's LastAccess to far in the past.
	b.LastAccess = time.Now().Add(-2 * time.Hour)

	// Active session has a skill, should survive.
	a.Lock()
	a.StartSkill("weather")
	a.Unlock()

	pruned := st.Cleanup(1 * time.Hour)
	if pruned < 1 {
		t.Fatalf("expected at least 1 pruned, got %d", pruned)
	}
	// The active session with a skill should survive.
	if st.Count() < 1 {
		t.Fatal("expected active sessions to survive")
	}
	// Verify idle is gone.
	if _, ok := st.sessions["idle"]; ok {
		t.Fatal("idle session should have been pruned")
	}
}

func TestCleanupKeepsSessionWithActiveSkill(t *testing.T) {
	st := NewStore()
	s := st.Get("busy")
	s.Lock()
	s.StartSkill("email")
	s.Unlock()

	pruned := st.Cleanup(1 * time.Nanosecond)
	// The "busy" session has an active skill, so it should survive.
	if _, ok := st.sessions["busy"]; !ok {
		t.Fatal("session with active skill should survive cleanup")
	}
	_ = pruned
}
