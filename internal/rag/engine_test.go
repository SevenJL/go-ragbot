package rag

import (
	"context"
	"strings"
	"sync"
	"testing"

	"ragbot/internal/config"
	"ragbot/internal/embedding"
	"ragbot/internal/llm"
	"ragbot/internal/plugin"
	"ragbot/internal/session"
	"ragbot/internal/skill"
	"ragbot/internal/vectorstore"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	store, err := vectorstore.NewMemory("")
	if err != nil {
		t.Fatal(err)
	}
	pm := plugin.NewManager()
	pm.Register(plugin.NewCalculatorPlugin(true))
	sm := skill.NewManager()
	sm.Register(skill.NewWeatherSkill(config.WeatherConfig{Provider: "mock"}))
	return New(
		config.RAGConfig{ChunkSize: 200, ChunkOverlap: 20, TopK: 3, MinScore: 0.1},
		embedding.NewLocal(128),
		store,
		llm.NewMock(),
		pm,
		sm,
		session.NewStore(),
	)
}

func TestEngineIngestAndRetrieve(t *testing.T) {
	ctx := context.Background()
	engine := newTestEngine(t)
	_, chunks, err := engine.Ingest(ctx, "project.txt", []byte("NebulaQuartz launch window is 2042-09-17. Owner is Ada Lab."))
	if err != nil {
		t.Fatal(err)
	}
	if chunks == 0 {
		t.Fatal("expected chunks")
	}

	res, err := engine.Answer(ctx, "s1", "What is the NebulaQuartz launch window?")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "rag" {
		t.Fatalf("source = %q", res.Source)
	}
	if len(res.Retrieved) == 0 {
		t.Fatal("expected retrieved chunks")
	}
	if !strings.Contains(res.Retrieved[0].Text, "2042-09-17") {
		t.Fatalf("retrieved = %#v", res.Retrieved)
	}
}

func TestEngineSerializesSameSessionSkillTurns(t *testing.T) {
	ctx := context.Background()
	engine := newTestEngine(t)

	start, err := engine.Answer(ctx, "same-session", "weather")
	if err != nil {
		t.Fatal(err)
	}
	if start.SkillName != "weather" {
		t.Fatalf("start = %#v", start)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	for _, msg := range []string{"Shanghai", "today"} {
		msg := msg
		go func() {
			defer wg.Done()
			_, err := engine.Answer(ctx, "same-session", msg)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if engine.Sessions().Get("same-session").ActiveSkill != "" {
		t.Fatal("expected weather skill to finish without interleaved state")
	}
}
