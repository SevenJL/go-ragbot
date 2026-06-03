package llm

import (
	"context"
	"strings"

	"ragbot/internal/core"
)

// Mock is an offline LLM that echoes the context it was given. It lets the
// whole pipeline run end-to-end without any API key, which is handy for
// development and tests.
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
	var b strings.Builder
	b.WriteString("【Mock LLM 回复】未配置真实大模型，以下为基于检索上下文的占位回答。\n\n")
	if strings.Contains(system, "上下文") || strings.Contains(system, "context") {
		b.WriteString("（系统已注入检索上下文，真实 LLM 会据此作答）\n\n")
	}
	b.WriteString("你的问题是：")
	b.WriteString(strings.TrimSpace(lastUser))
	return b.String(), nil
}
