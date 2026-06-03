// Package core holds the shared data types used across the whole project.
// It must not import any other internal package so that it can be imported
// freely without creating import cycles.
package core

// Message is a single chat turn passed to an LLM.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// Chunk is a single piece of a document after splitting, optionally with an
// embedding vector attached.
type Chunk struct {
	ID        string    `json:"id"`
	DocID     string    `json:"doc_id"`
	Source    string    `json:"source"` // original file name / path
	Index     int       `json:"index"`  // position within the document
	Text      string    `json:"text"`
	Embedding []float64 `json:"embedding,omitempty"`
}

// RetrievedChunk is a Chunk plus the similarity score from a search.
type RetrievedChunk struct {
	Chunk
	Score float64 `json:"score"`
}

// DocInfo is a light summary of a stored document.
type DocInfo struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Chunks int    `json:"chunks"`
}
