package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Metrics tracks simple in-process counters for Prometheus scraping.
type Metrics struct {
	mu           sync.RWMutex
	startTime    time.Time
	requests     int64
	chatQueries  int64
	docUploads   int64
	activeSess   func() int
	chunkCount   func() int
}

// NewMetrics creates a Metrics collector. The callbacks are polled at scrape time
// to provide live values without tight coupling.
func NewMetrics(activeSessions func() int, chunkCount func() int) *Metrics {
	return &Metrics{
		startTime:   time.Now(),
		activeSess:  activeSessions,
		chunkCount:  chunkCount,
	}
}

// RecordRequest increments the total HTTP request counter.
func (m *Metrics) RecordRequest() {
	m.mu.Lock()
	m.requests++
	m.mu.Unlock()
}

// RecordChatQuery increments the chat query counter.
func (m *Metrics) RecordChatQuery() {
	m.mu.Lock()
	m.chatQueries++
	m.mu.Unlock()
}

// RecordDocUpload increments the document upload counter.
func (m *Metrics) RecordDocUpload() {
	m.mu.Lock()
	m.docUploads++
	m.mu.Unlock()
}

// Handler returns an HTTP handler that outputs Prometheus text format.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		defer m.mu.RUnlock()

		var b strings.Builder
		uptime := time.Since(m.startTime).Seconds()

		writeMetric(&b, "ragbot_uptime_seconds", "gauge", fmt.Sprintf("%.0f", uptime), "Server uptime in seconds")
		writeMetric(&b, "ragbot_requests_total", "counter", fmt.Sprintf("%d", m.requests), "Total HTTP requests")
		writeMetric(&b, "ragbot_chat_queries_total", "counter", fmt.Sprintf("%d", m.chatQueries), "Total chat queries")
		writeMetric(&b, "ragbot_doc_uploads_total", "counter", fmt.Sprintf("%d", m.docUploads), "Total document uploads")
		writeMetric(&b, "ragbot_active_sessions", "gauge", fmt.Sprintf("%d", m.activeSess()), "Active sessions")
		writeMetric(&b, "ragbot_chunk_count", "gauge", fmt.Sprintf("%d", m.chunkCount()), "Stored chunks")

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(b.String()))
	}
}

func writeMetric(b *strings.Builder, name, typ, value, help string) {
	if help != "" {
		b.WriteString("# HELP ")
		b.WriteString(name)
		b.WriteString(" ")
		b.WriteString(help)
		b.WriteString("\n")
	}
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteString(" ")
	b.WriteString(typ)
	b.WriteString("\n")
	b.WriteString(name)
	b.WriteString(" ")
	b.WriteString(value)
	b.WriteString("\n")
}
