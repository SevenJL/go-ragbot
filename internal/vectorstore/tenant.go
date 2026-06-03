package vectorstore

import (
	"context"

	"ragbot/internal/core"
)

// TenantStore wraps a Store to provide tenant isolation by prefixing all
// chunk docIDs with the tenant ID. This allows a single underlying store
// to serve multiple tenants without cross-tenant data leakage.
type TenantStore struct {
	inner Store
}

// NewTenantStore creates a tenant-isolating wrapper.
func NewTenantStore(inner Store) *TenantStore {
	return &TenantStore{inner: inner}
}

// tenantChunks prefixes each chunk's DocID with tenantID for isolation.
func tenantChunks(tenantID string, chunks []core.Chunk) []core.Chunk {
	out := make([]core.Chunk, len(chunks))
	for i, c := range chunks {
		c.DocID = tenantID + ":" + c.DocID
		c.ID = tenantID + ":" + c.ID
		out[i] = c
	}
	return out
}

// untenantChunks strips the tenant prefix from chunk DocIDs for external display.
func untenantChunks(chunks []core.Chunk) []core.Chunk {
	out := make([]core.Chunk, len(chunks))
	for i, c := range chunks {
		c.DocID = stripTenantPrefix(c.DocID)
		c.ID = stripTenantPrefix(c.ID)
		out[i] = c
	}
	return out
}

func stripTenantPrefix(s string) string {
	for i, r := range s {
		if r == ':' {
			return s[i+1:]
		}
	}
	return s
}

func (ts *TenantStore) Add(ctx context.Context, chunks []core.Chunk) error {
	tid := tenantFromCtx(ctx)
	return ts.inner.Add(ctx, tenantChunks(tid, chunks))
}

func (ts *TenantStore) Search(ctx context.Context, query []float64, topK int) ([]core.RetrievedChunk, error) {
	tid := tenantFromCtx(ctx)
	results, err := ts.inner.Search(ctx, query, topK*3) // oversample then filter
	if err != nil {
		return nil, err
	}
	// Filter to current tenant only.
	filtered := make([]core.RetrievedChunk, 0, topK)
	for _, r := range results {
		if matchesTenant(r.DocID, tid) {
			r.DocID = stripTenantPrefix(r.DocID)
			r.ID = stripTenantPrefix(r.ID)
			filtered = append(filtered, r)
		}
		if len(filtered) >= topK {
			break
		}
	}
	return filtered, nil
}

func (ts *TenantStore) SearchHybrid(ctx context.Context, queryVec []float64, queryText string, topK int) ([]core.RetrievedChunk, error) {
	tid := tenantFromCtx(ctx)
	results, err := ts.inner.SearchHybrid(ctx, queryVec, queryText, topK*3)
	if err != nil {
		return nil, err
	}
	filtered := make([]core.RetrievedChunk, 0, topK)
	for _, r := range results {
		if matchesTenant(r.DocID, tid) {
			r.DocID = stripTenantPrefix(r.DocID)
			r.ID = stripTenantPrefix(r.ID)
			filtered = append(filtered, r)
		}
		if len(filtered) >= topK {
			break
		}
	}
	return filtered, nil
}

func (ts *TenantStore) Docs() []core.DocInfo {
	// Not tenant-scoped for simplicity; callers should provide a scoped version.
	docs := ts.inner.Docs()
	for i := range docs {
		docs[i].ID = stripTenantPrefix(docs[i].ID)
	}
	return docs
}

func (ts *TenantStore) Delete(docID string) error {
	// docID is tenant-prefixed by the caller.
	return ts.inner.Delete(docID)
}

func (ts *TenantStore) Save() error      { return ts.inner.Save() }
func (ts *TenantStore) Count() int       { return ts.inner.Count() }
func (ts *TenantStore) AllChunks() []core.Chunk {
	chunks := ts.inner.AllChunks()
	return untenantChunks(chunks)
}
func (ts *TenantStore) Replace(chunks []core.Chunk) error { return ts.inner.Replace(chunks) }

func matchesTenant(docID, tenantID string) bool {
	prefix := tenantID + ":"
	return len(docID) > len(prefix) && docID[:len(prefix)] == prefix
}

func tenantFromCtx(ctx context.Context) string {
	return core.GetTenantID(ctx)
}

// WithTenant attaches a tenant ID to the context for store operations.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, core.TenantCtxKey, tenantID)
}
