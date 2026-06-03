package vectorstore

import (
	"context"
	"path/filepath"
	"testing"

	"ragbot/internal/core"
)

func TestMemoryStorePersistsSearchesAndDeletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	ctx := context.Background()

	store, err := NewMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []core.Chunk{
		{ID: "doc1#0", DocID: "doc1", Source: "a.txt", Text: "alpha", Embedding: []float64{1, 0}},
		{ID: "doc2#0", DocID: "doc2", Source: "b.txt", Text: "beta", Embedding: []float64{0, 1}},
	}
	if err := store.Add(ctx, chunks); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 2 {
		t.Fatalf("count = %d", reloaded.Count())
	}
	hits, err := reloaded.Search(ctx, []float64{1, 0}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].DocID != "doc1" {
		t.Fatalf("hits = %#v", hits)
	}
	if hits[0].Embedding != nil {
		t.Fatal("expected returned embedding to be stripped")
	}

	if err := reloaded.Delete("doc1"); err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 1 {
		t.Fatalf("count after delete = %d", reloaded.Count())
	}
}
