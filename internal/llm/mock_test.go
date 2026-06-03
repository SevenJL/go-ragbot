package llm

import (
	"context"
	"strings"
	"testing"

	"ragbot/internal/core"
)

func TestMockLLMRunsWithoutAPIKey(t *testing.T) {
	m := NewMock()
	if m.Name() != "mock" {
		t.Fatalf("name = %q", m.Name())
	}
	resp, err := m.Chat(context.Background(), []core.Message{
		{Role: "system", Content: "你是一个助手。"},
		{Role: "user", Content: "What is the capital of France?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(resp, "Mock LLM") {
		t.Fatal("missing Mock LLM marker")
	}
}

func TestMockLLMSynthesisesRetrievedContext(t *testing.T) {
	m := NewMock()
	resp, err := m.Chat(context.Background(), []core.Message{
		{
			Role:    "system",
			Content: "你是一个助手。\n\n【上下文】\n[片段1 来源:guide.pdf]NebulaQuartz launch window is 2042-09-17.\n\n[片段2 来源:guide.pdf]Ada Lab leads the project.\n\n",
		},
		{Role: "user", Content: "What is the NebulaQuartz launch window?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "NebulaQuartz") && !strings.Contains(resp, "2042") {
		t.Fatalf("expected mock to reference retrieved context in response: %s", resp)
	}
}

func TestMockLLMWithEmptyContext(t *testing.T) {
	m := NewMock()
	resp, err := m.Chat(context.Background(), []core.Message{
		{Role: "system", Content: "你是一个助手。\n\n【上下文】\n（本次没有检索到相关资料）\n"},
		{Role: "user", Content: "What is the secret formula?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp, "暂未找到") {
		t.Fatalf("expected mock to indicate missing context: %s", resp)
	}
}

func TestExtractRelevantSentences(t *testing.T) {
	system := "你是一个助手。\n\n【上下文】\n[片段1 来源:doc.txt]The sky is blue.\n[片段2 来源:doc.txt]The ocean is deep.\n"
	sentences := extractRelevantSentences(system, "sky")
	if len(sentences) == 0 {
		t.Fatal("expected at least one sentence about sky")
	}
	if !strings.Contains(sentences[0], "sky") {
		t.Fatalf("expected sentence about sky, got %q", sentences[0])
	}
}

func TestTokenSet(t *testing.T) {
	tokens := tokenSet("The quick fox")
	if !tokens["the"] {
		t.Fatal("expected 'the'")
	}
	if !tokens["quick"] {
		t.Fatal("expected 'quick'")
	}
	if !tokens["fox"] {
		t.Fatal("expected 'fox'")
	}
	if len(tokens) < 3 {
		t.Fatalf("token set = %v", tokens)
	}
}

func TestSplitSentences(t *testing.T) {
	parts := splitSentences("Hello world. 你好世界！Is this working?")
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 sentences, got %d: %v", len(parts), parts)
	}
}
