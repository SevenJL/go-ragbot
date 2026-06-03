package vectorstore

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"ragbot/internal/core"
)

// Memory is a simple in-process vector store backed by a JSON file. It is the
// lightweight local alternative to Chroma/FAISS requested in the brief; for a
// production deployment swap this implementation for a Chroma HTTP client
// behind the same Store interface.
type Memory struct {
	mu     sync.RWMutex
	path   string
	chunks []core.Chunk
}

// NewMemory creates the store and loads any persisted data at path.
func NewMemory(path string) (*Memory, error) {
	m := &Memory{path: path}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Memory) load() error {
	if m.path == "" {
		return nil
	}
	b, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &m.chunks)
}

func (m *Memory) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Memory) saveLocked() error {
	if m.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(m.chunks)
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, b, 0o644)
}

func (m *Memory) Add(ctx context.Context, chunks []core.Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, chunks...)
	return m.saveLocked()
}

func (m *Memory) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.chunks)
}

func (m *Memory) Search(ctx context.Context, query []float64, topK int) ([]core.RetrievedChunk, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scored := make([]core.RetrievedChunk, 0, len(m.chunks))
	for _, c := range m.chunks {
		scored = append(scored, core.RetrievedChunk{
			Chunk: c,
			Score: cosine(query, c.Embedding),
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	if topK > len(scored) {
		topK = len(scored)
	}
	// Strip embeddings from the returned copies to keep payloads small.
	out := make([]core.RetrievedChunk, topK)
	for i := 0; i < topK; i++ {
		rc := scored[i]
		rc.Embedding = nil
		out[i] = rc
	}
	return out, nil
}

func (m *Memory) Docs() []core.DocInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	counts := map[string]*core.DocInfo{}
	var order []string
	for _, c := range m.chunks {
		di, ok := counts[c.DocID]
		if !ok {
			di = &core.DocInfo{ID: c.DocID, Source: c.Source}
			counts[c.DocID] = di
			order = append(order, c.DocID)
		}
		di.Chunks++
	}
	out := make([]core.DocInfo, 0, len(order))
	for _, id := range order {
		out = append(out, *counts[id])
	}
	return out
}

func (m *Memory) Delete(docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.chunks[:0]
	for _, c := range m.chunks {
		if c.DocID != docID {
			kept = append(kept, c)
		}
	}
	m.chunks = kept
	return m.saveLocked()
}

// cosine returns the cosine similarity of a and b. Vectors are assumed (but
// not required) to be normalised; we normalise defensively.
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
