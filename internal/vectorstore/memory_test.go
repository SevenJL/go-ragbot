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

func TestMemoryAllChunksAndReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	ctx := context.Background()

	store, err := NewMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []core.Chunk{
		{ID: "a#0", DocID: "a", Source: "a.txt", Text: "first", Embedding: []float64{1}},
		{ID: "b#0", DocID: "b", Source: "b.txt", Text: "second", Embedding: []float64{2}},
	}
	if err := store.Add(ctx, chunks); err != nil {
		t.Fatal(err)
	}

	// AllChunks returns all chunks with embeddings.
	all := store.AllChunks()
	if len(all) != 2 {
		t.Fatalf("AllChunks len = %d", len(all))
	}
	if all[0].Embedding == nil || all[1].Embedding == nil {
		t.Fatal("AllChunks should include embeddings")
	}

	// Replace with new set.
	replacement := []core.Chunk{
		{ID: "c#0", DocID: "c", Source: "c.txt", Text: "third", Embedding: []float64{3}},
	}
	if err := store.Replace(replacement); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 1 {
		t.Fatalf("count after replace = %d", store.Count())
	}

	// Reload and verify.
	reloaded, err := NewMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 1 || reloaded.AllChunks()[0].DocID != "c" {
		t.Fatalf("after reload: count=%d doc=%s", reloaded.Count(), reloaded.AllChunks()[0].DocID)
	}
}
