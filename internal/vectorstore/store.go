// Package vectorstore stores chunk embeddings and does similarity search.
package vectorstore

import (
	"context"

	"ragbot/internal/core"
)

// Store persists chunks with embeddings and supports top-k similarity search.
type Store interface {
	// Add inserts chunks (which must already carry embeddings).
	Add(ctx context.Context, chunks []core.Chunk) error
	// Search returns the topK most similar chunks to query by cosine similarity.
	Search(ctx context.Context, query []float64, topK int) ([]core.RetrievedChunk, error)
	// Docs lists stored documents.
	Docs() []core.DocInfo
	// Delete removes every chunk belonging to docID.
	Delete(docID string) error
	// Save persists the store to disk.
	Save() error
	// Count returns the number of stored chunks.
	Count() int
	// AllChunks returns every stored chunk for export/backup.
	AllChunks() []core.Chunk
	// Replace atomically replaces all stored chunks (used for import).
	Replace(chunks []core.Chunk) error
	// SearchHybrid performs both vector (cosine) and keyword (TF-IDF) search,
	// merging results with Reciprocal Rank Fusion. Falls back to pure vector
	// search if keywordWeight is 0.
	SearchHybrid(ctx context.Context, queryVec []float64, queryText string, topK int) ([]core.RetrievedChunk, error)
}
