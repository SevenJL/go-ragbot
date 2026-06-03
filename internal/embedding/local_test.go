package embedding

import (
	"context"
	"math"
	"testing"
)

func TestLocalEmbedderHasExpectedDimensions(t *testing.T) {
	e := NewLocal(128)
	if e.Dim() != 128 {
		t.Fatalf("dim = %d", e.Dim())
	}
	if e.Name() != "local-hash" {
		t.Fatalf("name = %q", e.Name())
	}
}

func TestLocalEmbedderDefaultDimensions(t *testing.T) {
	e := NewLocal(0)
	if e.Dim() != 256 {
		t.Fatalf("default dim = %d", e.Dim())
	}
}

func TestLocalEmbedderReturnsVectors(t *testing.T) {
	e := NewLocal(64)
	vecs, err := e.Embed(context.Background(), []string{"hello world", "你好世界"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len = %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 64 {
			t.Fatalf("vecs[%d] dim = %d", i, len(v))
		}
		// L2 norm should be ~1.0 (normalised).
		var norm float64
		for _, x := range v {
			norm += x * x
		}
		norm = math.Sqrt(norm)
		if norm < 0.99 || norm > 1.01 {
			t.Fatalf("vecs[%d] norm = %f, want ~1.0", i, norm)
		}
	}
}

func TestLocalEmbedderSimilarTextIsSimilar(t *testing.T) {
	e := NewLocal(128)
	vecs, err := e.Embed(context.Background(), []string{
		"The quick brown fox jumps over the lazy dog",
		"The quick brown fox jumps over the lazy cat",
		"完全不同的中文文本测试内容",
	})
	if err != nil {
		t.Fatal(err)
	}
	simAB := cosine(vecs[0], vecs[1])
	simAC := cosine(vecs[0], vecs[2])
	if simAB <= simAC {
		t.Fatalf("expected similar texts (%.4f) > dissimilar (%.4f)", simAB, simAC)
	}
}

func TestLocalEmbedderEmptyTextReturnsZeroVector(t *testing.T) {
	e := NewLocal(64)
	vecs, err := e.Embed(context.Background(), []string{""})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range vecs[0] {
		if x != 0 {
			t.Fatalf("expected all-zero vector for empty input, got %v", vecs[0])
		}
	}
}

func TestLocalEmbedderEmptyBatchReturnsEmpty(t *testing.T) {
	e := NewLocal(32)
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 0 {
		t.Fatalf("expected empty, got %d", len(vecs))
	}
}

func TestFeaturesExtractsWordsAndBigrams(t *testing.T) {
	f := features("hello")
	found := false
	for _, feat := range f {
		if feat == "w:hello" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'w:hello' in features, got %v", f)
	}
}

func TestFeaturesExtractsCJKBigrams(t *testing.T) {
	f := features("你好")
	found := false
	for _, feat := range f {
		if feat == "b:你好" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected CJK bigram 'b:你好' in features, got %v", f)
	}
}

func TestFeaturesDampensFrequentWords(t *testing.T) {
	// "a a a a" — word "a" repeated 4 times should be dampened.
	f := features("a a a a")
	count := 0
	for _, feat := range f {
		if feat == "w:a" {
			count++
		}
	}
	// sqrt(4) = 2, so should be at most 2 after dampening.
	if count > 2 {
		t.Fatalf("dampened count = %d, want <= 2", count)
	}
}

func TestIsCJK(t *testing.T) {
	if !isCJK('中') {
		t.Fatal("'中' should be CJK")
	}
	if !isCJK('あ') {
		t.Fatal("'あ' should be CJK (hiragana)")
	}
	if isCJK('a') {
		t.Fatal("'a' should not be CJK")
	}
}

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
