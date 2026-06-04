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

	if err := reloaded.Delete(ctx, "doc1"); err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 1 {
		t.Fatalf("count after delete = %d", reloaded.Count())
	}
}

func TestTenantStoreScopesDocsDeleteAndReplace(t *testing.T) {
	base, err := NewMemory("")
	if err != nil {
		t.Fatal(err)
	}
	store := NewTenantStore(base)
	ctxA := WithTenant(context.Background(), "tenant-a")
	ctxB := WithTenant(context.Background(), "tenant-b")

	if err := store.Add(ctxA, []core.Chunk{
		{ID: "doc#0", DocID: "doc", Source: "a.txt", Text: "alpha", Embedding: []float64{1}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctxB, []core.Chunk{
		{ID: "doc#0", DocID: "doc", Source: "b.txt", Text: "beta", Embedding: []float64{2}},
	}); err != nil {
		t.Fatal(err)
	}

	docsA := store.Docs(ctxA)
	if len(docsA) != 1 || docsA[0].Source != "a.txt" || docsA[0].ID != "doc" {
		t.Fatalf("tenant-a docs = %#v", docsA)
	}
	docsB := store.Docs(ctxB)
	if len(docsB) != 1 || docsB[0].Source != "b.txt" || docsB[0].ID != "doc" {
		t.Fatalf("tenant-b docs = %#v", docsB)
	}

	if err := store.Replace(ctxA, []core.Chunk{
		{ID: "tenant-a:new#0", DocID: "tenant-a:new", Source: "new-a.txt", Text: "new alpha", Embedding: []float64{3}},
	}); err != nil {
		t.Fatal(err)
	}
	if docsA = store.Docs(ctxA); len(docsA) != 1 || docsA[0].ID != "new" {
		t.Fatalf("tenant-a docs after replace = %#v", docsA)
	}
	if docsB = store.Docs(ctxB); len(docsB) != 1 || docsB[0].ID != "doc" {
		t.Fatalf("tenant-b docs after tenant-a replace = %#v", docsB)
	}

	if err := store.Delete(ctxB, "doc"); err != nil {
		t.Fatal(err)
	}
	if len(store.Docs(ctxB)) != 0 {
		t.Fatalf("tenant-b docs after delete = %#v", store.Docs(ctxB))
	}
	if len(store.Docs(ctxA)) != 1 {
		t.Fatalf("tenant-a docs after tenant-b delete = %#v", store.Docs(ctxA))
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
	all := store.AllChunks(ctx)
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
	if err := store.Replace(ctx, replacement); err != nil {
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
	reloadedChunks := reloaded.AllChunks(ctx)
	if reloaded.Count() != 1 || reloadedChunks[0].DocID != "c" {
		t.Fatalf("after reload: count=%d doc=%s", reloaded.Count(), reloadedChunks[0].DocID)
	}
}
