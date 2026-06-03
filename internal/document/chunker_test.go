package document

import "testing"

func TestChunkSplitsAndOverlaps(t *testing.T) {
	chunks := Chunk("alpha beta gamma delta epsilon zeta eta theta", 18, 5)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d: %#v", len(chunks), chunks)
	}
	if chunks[0] == "" || chunks[1] == "" {
		t.Fatalf("empty chunk: %#v", chunks)
	}
}

func TestLoadMarkdownKeepsLinkText(t *testing.T) {
	text, err := LoadText("guide.md", []byte("# Title\nSee [docs](https://example.com) and **bold** text."))
	if err != nil {
		t.Fatal(err)
	}
	if text == "" || text == "# Title" {
		t.Fatalf("unexpected markdown text = %q", text)
	}
}
