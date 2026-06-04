package vectorstore

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func (m *Memory) Docs(ctx context.Context) []core.DocInfo {
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

func (m *Memory) Delete(ctx context.Context, docID string) error {
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

// idxScore pairs a chunk index with a score for ranking algorithms.
type idxScore struct {
	idx   int
	score float64
}

// AllChunks returns a copy of all stored chunks (embeddings included) for
// export/backup purposes.
func (m *Memory) AllChunks(ctx context.Context) []core.Chunk {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]core.Chunk, len(m.chunks))
	copy(out, m.chunks)
	return out
}

// Replace atomically replaces the entire chunk set and persists. Used for
// bulk import/restore.
func (m *Memory) Replace(ctx context.Context, chunks []core.Chunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = make([]core.Chunk, len(chunks))
	copy(m.chunks, chunks)
	return m.saveLocked()
}

// SearchHybrid performs vector + keyword search with Reciprocal Rank Fusion.
// RRF formula: score = 1/(k + rank_vector) + weight * 1/(k + rank_keyword)
// where k=60 (standard RRF constant) and weight defaults to 0.5.
func (m *Memory) SearchHybrid(ctx context.Context, queryVec []float64, queryText string, topK int) ([]core.RetrievedChunk, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	const rrfK = 60.0
	const keywordWeight = 0.5

	if len(m.chunks) == 0 {
		return nil, nil
	}

	// ---- vector ranking ----
	vecRanked := make([]idxScore, len(m.chunks))
	for i, c := range m.chunks {
		vecRanked[i] = idxScore{idx: i, score: cosine(queryVec, c.Embedding)}
	}
	sort.Slice(vecRanked, func(i, j int) bool { return vecRanked[i].score > vecRanked[j].score })

	// ---- keyword ranking (simple TF-IDF) ----
	kwRanked := rankByTFIDF(m.chunks, queryText)

	// ---- RRF merge ----
	// Build rank maps (1-indexed ranks for RRF).
	vecRank := make(map[int]int, len(vecRanked))
	for rank, vs := range vecRanked {
		vecRank[vs.idx] = rank + 1
	}
	kwRank := make(map[int]int, len(kwRanked))
	for rank, ks := range kwRanked {
		kwRank[ks.idx] = rank + 1
	}

	merged := make([]core.RetrievedChunk, len(m.chunks))
	for i, c := range m.chunks {
		vr, vOk := vecRank[i]
		if !vOk {
			vr = len(m.chunks) + 1
		}
		kr, kOk := kwRank[i]
		if !kOk {
			kr = len(m.chunks) + 1
		}
		rrfScore := 1.0/(rrfK+float64(vr)) + keywordWeight*1.0/(rrfK+float64(kr))
		merged[i] = core.RetrievedChunk{Chunk: c, Score: rrfScore}
	}

	sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })

	if topK > len(merged) {
		topK = len(merged)
	}
	out := make([]core.RetrievedChunk, topK)
	for i := 0; i < topK; i++ {
		rc := merged[i]
		rc.Embedding = nil
		out[i] = rc
	}
	return out, nil
}

// rankByTFIDF scores chunks against query using a simple TF-IDF variant.
func rankByTFIDF(chunks []core.Chunk, query string) []idxScore {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil
	}

	// Compute IDF: log(N / df(t)) for each term.
	docFreq := map[string]int{}
	for _, c := range chunks {
		chunkToks := tokenize(c.Text)
		seen := map[string]bool{}
		for t := range chunkToks {
			if _, ok := seen[t]; !ok {
				seen[t] = true
				docFreq[t]++
			}
		}
	}

	N := float64(len(chunks))
	idf := map[string]float64{}
	for t, df := range docFreq {
		idf[t] = math.Log(1.0 + N/math.Max(float64(df), 1.0))
	}

	// Score each chunk: sum over query terms of TF(chunk, t) * IDF(t).
	scored := make([]idxScore, 0, len(chunks))
	for i, c := range chunks {
		chunkToks := tokenize(c.Text)
		var score float64
		totalToks := len(chunkToks)
		if totalToks == 0 {
			totalToks = 1
		}
		for qt := range tokens {
			tf := float64(chunkToks[qt]) / float64(totalToks)
			score += tf * idf[qt]
		}
		if score > 0 {
			scored = append(scored, idxScore{idx: i, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	return scored
}

// tokenize returns a bag-of-words map from a string. Handles both CJK and
// Latin text.
func tokenize(s string) map[string]int {
	toks := map[string]int{}
	runes := []rune(s)
	for i := 0; i < len(runes); {
		r := runes[i]
		if r >= 0x4E00 && r <= 0x9FFF {
			// CJK: each char is a token, plus bigrams.
			toks[string(r)]++
			if i+1 < len(runes) && runes[i+1] >= 0x4E00 && runes[i+1] <= 0x9FFF {
				toks[string(runes[i:i+2])]++
			}
			i++
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			start := i
			for i < len(runes) && ((runes[i] >= 'a' && runes[i] <= 'z') ||
				(runes[i] >= 'A' && runes[i] <= 'Z') ||
				(runes[i] >= '0' && runes[i] <= '9')) {
				i++
			}
			tok := strings.ToLower(string(runes[start:i]))
			if len(tok) >= 2 {
				toks[tok]++
			}
		} else {
			i++
		}
	}
	return toks
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
