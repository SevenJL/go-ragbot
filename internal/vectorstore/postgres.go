// Package vectorstore: PostgreSQL adapter.
//
// This file provides a production-ready Store backed by PostgreSQL with the
// pgvector extension. It uses database/sql — the caller is responsible for
// importing and registering a PostgreSQL driver (e.g. github.com/lib/pq or
// github.com/jackc/pgx/v5/stdlib).
//
// Required schema:
//
//	CREATE EXTENSION IF NOT EXISTS vector;
//	CREATE TABLE IF NOT EXISTS chunks (
//	  id        TEXT PRIMARY KEY,
//	  doc_id    TEXT NOT NULL,
//	  source    TEXT NOT NULL,
//	  idx       INTEGER NOT NULL DEFAULT 0,
//	  text      TEXT NOT NULL,
//	  embedding vector(1536)
//	);
//	CREATE INDEX IF NOT EXISTS chunks_doc_id_idx ON chunks(doc_id);
//	CREATE INDEX IF NOT EXISTS chunks_embedding_idx ON chunks
//	  USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
//
// Usage:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"  // in your main.go
//	db, _ := sql.Open("pgx", "postgres://...")
//	store, _ := vectorstore.NewPostgresStore(db)
package vectorstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"ragbot/internal/core"
)

// PostgresStore implements Store on top of PostgreSQL + pgvector.
// The caller provides an already-open *sql.DB with a registered pgvector-compatible driver.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore wraps an existing *sql.DB and ensures the schema exists.
func NewPostgresStore(db *sql.DB) (*PostgresStore, error) {
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	ps := &PostgresStore{db: db}
	if err := ps.migrate(); err != nil {
		return nil, err
	}
	return ps, nil
}

func (ps *PostgresStore) migrate() error {
	_, err := ps.db.Exec(`
		CREATE EXTENSION IF NOT EXISTS vector;
		CREATE TABLE IF NOT EXISTS chunks (
			id        TEXT PRIMARY KEY,
			doc_id    TEXT NOT NULL,
			source    TEXT NOT NULL,
			idx       INTEGER NOT NULL DEFAULT 0,
			text      TEXT NOT NULL,
			embedding vector(1536)
		);
		CREATE INDEX IF NOT EXISTS chunks_doc_id_idx ON chunks(doc_id);
	`)
	return err
}

func (ps *PostgresStore) Add(ctx context.Context, chunks []core.Chunk) error {
	tx, err := ps.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (id, doc_id, source, idx, text, embedding)
		VALUES ($1, $2, $3, $4, $5, $6::vector)
		ON CONFLICT (id) DO UPDATE SET text=$5, embedding=$6::vector
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		embJSON, _ := json.Marshal(c.Embedding)
		if _, err := stmt.ExecContext(ctx, c.ID, c.DocID, c.Source, c.Index, c.Text, string(embJSON)); err != nil {
			return fmt.Errorf("postgres: insert %s: %w", c.ID, err)
		}
	}
	return tx.Commit()
}

func (ps *PostgresStore) Search(ctx context.Context, query []float64, topK int) ([]core.RetrievedChunk, error) {
	queryJSON, _ := json.Marshal(query)
	rows, err := ps.db.QueryContext(ctx, `
		SELECT id, doc_id, source, idx, text,
		       1.0 - (embedding <=> $1::vector) AS score
		FROM chunks
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`, string(queryJSON), topK)
	if err != nil {
		return nil, fmt.Errorf("postgres: search: %w", err)
	}
	defer rows.Close()
	return scanChunksPostgres(rows)
}

func (ps *PostgresStore) SearchHybrid(ctx context.Context, queryVec []float64, queryText string, topK int) ([]core.RetrievedChunk, error) {
	queryJSON, _ := json.Marshal(queryVec)
	// Combine pgvector cosine-distance with simple text ILIKE boost.
	rows, err := ps.db.QueryContext(ctx, `
		SELECT id, doc_id, source, idx, text,
		       (1.0 - (embedding <=> $1::vector))
		       + CASE WHEN text ILIKE '%' || $2 || '%' THEN 0.15 ELSE 0 END AS score
		FROM chunks
		ORDER BY score DESC
		LIMIT $3
	`, string(queryJSON), queryText, topK)
	if err != nil {
		return nil, fmt.Errorf("postgres: hybrid search: %w", err)
	}
	defer rows.Close()

	var results []core.RetrievedChunk
	for rows.Next() {
		var rc core.RetrievedChunk
		if err := rows.Scan(&rc.ID, &rc.DocID, &rc.Source, &rc.Index, &rc.Text, &rc.Score); err != nil {
			return nil, err
		}
		results = append(results, rc)
	}
	return results, rows.Err()
}

func (ps *PostgresStore) Docs() []core.DocInfo {
	rows, err := ps.db.Query(`SELECT doc_id, source, COUNT(*) FROM chunks GROUP BY doc_id, source ORDER BY source`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []core.DocInfo
	for rows.Next() {
		var di core.DocInfo
		if err := rows.Scan(&di.ID, &di.Source, &di.Chunks); err != nil {
			continue
		}
		out = append(out, di)
	}
	return out
}

func (ps *PostgresStore) Delete(docID string) error {
	_, err := ps.db.Exec(`DELETE FROM chunks WHERE doc_id = $1`, docID)
	return err
}

func (ps *PostgresStore) Save() error { return nil }

func (ps *PostgresStore) Count() int {
	var n int
	_ = ps.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&n)
	return n
}

func (ps *PostgresStore) AllChunks() []core.Chunk {
	rows, err := ps.db.Query(`SELECT id, doc_id, source, idx, text, embedding::text FROM chunks`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []core.Chunk
	for rows.Next() {
		var c core.Chunk
		var embStr string
		if err := rows.Scan(&c.ID, &c.DocID, &c.Source, &c.Index, &c.Text, &embStr); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(embStr), &c.Embedding)
		out = append(out, c)
	}
	return out
}

func (ps *PostgresStore) Replace(chunks []core.Chunk) error {
	tx, err := ps.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM chunks`); err != nil {
		return err
	}
	for _, c := range chunks {
		embJSON, _ := json.Marshal(c.Embedding)
		if _, err := tx.Exec(
			`INSERT INTO chunks (id, doc_id, source, idx, text, embedding) VALUES ($1,$2,$3,$4,$5,$6::vector)`,
			c.ID, c.DocID, c.Source, c.Index, c.Text, string(embJSON),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (ps *PostgresStore) Close() error { return ps.db.Close() }

func scanChunksPostgres(rows *sql.Rows) ([]core.RetrievedChunk, error) {
	var out []core.RetrievedChunk
	for rows.Next() {
		var rc core.RetrievedChunk
		var score sql.NullFloat64
		if err := rows.Scan(&rc.ID, &rc.DocID, &rc.Source, &rc.Index, &rc.Text, &score); err != nil {
			return nil, err
		}
		rc.Score = score.Float64
		if math.IsNaN(rc.Score) {
			rc.Score = 0
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

// BuildEmbeddingLiteral converts []float64 to pgvector literal string: '[0.1,0.2,0.3]'.
func BuildEmbeddingLiteral(vec []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%f", v)
	}
	b.WriteByte(']')
	return b.String()
}
