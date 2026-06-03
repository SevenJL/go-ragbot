// Package audit provides a lightweight audit trail for state-changing
// operations. Entries are written as JSON lines to a file and also
// emitted via the standard log for real-time monitoring.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry represents one auditable event.
type Entry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"` // e.g. "doc.upload", "skill.register"
	Actor  string    `json:"actor"`  // session ID, IP, or "system"
	Target string    `json:"target"` // document ID, skill name, plugin name
	Detail string    `json:"detail"` // human-readable summary
	Result string    `json:"result"` // "success" | "error"
}

// Logger writes audit entries to a JSON-lines file.
type Logger struct {
	mu   sync.Mutex
	w    io.WriteCloser
	path string
}

// NewLogger opens (or creates) the audit log file at path. If path is
// empty, audit events are only logged via the standard logger.
func NewLogger(path string) (*Logger, error) {
	l := &Logger{path: path}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("audit: create dir: %w", err)
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, fmt.Errorf("audit: open file: %w", err)
		}
		// Write a BOM-free header marker on new files.
		if st, _ := f.Stat(); st.Size() == 0 {
			_, _ = f.WriteString("# ragbot audit log — JSON lines\n")
		}
		l.w = f
	}
	return l, nil
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		return l.w.Close()
	}
	return nil
}

// Log records an audit entry.
func (l *Logger) Log(action, actor, target, detail, result string) {
	e := Entry{
		Time:   time.Now(),
		Action: action,
		Actor:  actor,
		Target: target,
		Detail: detail,
		Result: result,
	}
	// Always emit to standard log for real-time visibility.
	log.Printf("[AUDIT] action=%s actor=%s target=%s result=%s detail=%s",
		e.Action, e.Actor, e.Target, e.Result, e.Detail)

	if l.path == "" || l.w == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, _ := json.Marshal(e)
	_, _ = l.w.Write(append(data, '\n'))
}

// --- convenience helpers for common actions ---

func (l *Logger) DocUpload(actor, filename, docID string, chunks int, err error) {
	result := "success"
	detail := fmt.Sprintf("file=%s chunks=%d", filename, chunks)
	if err != nil {
		result = "error"
		detail = fmt.Sprintf("file=%s error=%v", filename, err)
	}
	l.Log("doc.upload", actor, docID, detail, result)
}

func (l *Logger) DocDelete(actor, docID string, err error) {
	result := "success"
	detail := "deleted"
	if err != nil {
		result = "error"
		detail = fmt.Sprintf("error=%v", err)
	}
	l.Log("doc.delete", actor, docID, detail, result)
}

func (l *Logger) DocUpdate(actor, filename, docID string, chunks int, err error) {
	result := "success"
	detail := fmt.Sprintf("file=%s chunks=%d", filename, chunks)
	if err != nil {
		result = "error"
		detail = fmt.Sprintf("file=%s error=%v", filename, err)
	}
	l.Log("doc.update", actor, docID, detail, result)
}

func (l *Logger) SkillRegister(actor, name string, steps int, err error) {
	result := "success"
	detail := fmt.Sprintf("steps=%d", steps)
	if err != nil {
		result = "error"
		detail = fmt.Sprintf("error=%v", err)
	}
	l.Log("skill.register", actor, name, detail, result)
}

func (l *Logger) SkillUnregister(actor, name string, err error) {
	result := "success"
	detail := "removed"
	if err != nil {
		result = "error"
		detail = fmt.Sprintf("error=%v", err)
	}
	l.Log("skill.unregister", actor, name, detail, result)
}

func (l *Logger) PluginToggle(actor, name string, enabled bool) {
	detail := fmt.Sprintf("enabled=%v", enabled)
	l.Log("plugin.toggle", actor, name, detail, "success")
}

func (l *Logger) ChatQuery(actor, sessionID, source string, retrieved int) {
	detail := fmt.Sprintf("session=%s source=%s retrieved=%d", sessionID, source, retrieved)
	l.Log("chat.query", actor, sessionID, detail, "success")
}

func (l *Logger) Export(actor string) {
	l.Log("system.export", actor, "vectorstore", "exported", "success")
}

func (l *Logger) Import(actor string, count int, err error) {
	result := "success"
	detail := fmt.Sprintf("chunks=%d", count)
	if err != nil {
		result = "error"
		detail = fmt.Sprintf("error=%v", err)
	}
	l.Log("system.import", actor, "vectorstore", detail, result)
}
