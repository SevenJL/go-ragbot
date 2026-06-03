package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerWritesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}

	l.DocUpload("test-user", "doc.pdf", "abc123", 5, nil)
	l.DocDelete("test-user", "abc123", nil)
	l.SkillRegister("test-user", "my-skill", 3, nil)
	l.PluginToggle("test-user", "websearch", false)
	l.ChatQuery("test-user", "s1", "rag", 4)
	l.Export("test-user")

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Should contain all six actions.
	for _, want := range []string{
		`"action":"doc.upload"`,
		`"action":"doc.delete"`,
		`"action":"skill.register"`,
		`"action":"plugin.toggle"`,
		`"action":"chat.query"`,
		`"action":"system.export"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in audit log, got:\n%s", want, content)
		}
	}
}

func TestLoggerStdoutOnly(t *testing.T) {
	l, err := NewLogger("")
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic or error.
	l.DocUpload("u", "f.txt", "id1", 1, nil)
	l.PluginToggle("u", "p", true)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
