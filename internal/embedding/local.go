package embedding

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Local is a dependency-free, deterministic embedder. It hashes text features
// (lower-cased latin words + CJK character bigrams) into a fixed-dimension
// vector and L2-normalises it. This is NOT as good as a real model such as
// BAAI/bge-small-zh, but it requires no network or weights and gives usable
// lexical-semantic similarity for local development and demos.
//
// To use a real model, set embedding.provider="openai" and point base_url at
// an OpenAI-compatible embeddings endpoint (see openai.go).
type Local struct {
	dim int
}

func NewLocal(dim int) *Local {
	if dim <= 0 {
		dim = 256
	}
	return &Local{dim: dim}
}

func (l *Local) Name() string { return "local-hash" }
func (l *Local) Dim() int     { return l.dim }

func (l *Local) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i, t := range texts {
		out[i] = l.embedOne(t)
	}
	return out, nil
}

func (l *Local) embedOne(text string) []float64 {
	vec := make([]float64, l.dim)
	for _, feat := range features(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(feat))
		sum := h.Sum32()
		idx := int(sum % uint32(l.dim))
		// signed hashing trick reduces collisions cancelling out information.
		if (sum>>31)&1 == 1 {
			vec[idx] -= 1
		} else {
			vec[idx] += 1
		}
	}
	// L2 normalise so cosine similarity == dot product.
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

// features extracts lexical features from text:
//   - latin/alphanumeric tokens, lower-cased
//   - CJK character unigrams and bigrams (good for Chinese without a tokenizer)
func features(text string) []string {
	text = strings.ToLower(text)
	var feats []string
	var word strings.Builder
	var cjk []rune

	flushWord := func() {
		if word.Len() > 0 {
			feats = append(feats, "w:"+word.String())
			word.Reset()
		}
	}
	flushCJK := func() {
		for i, r := range cjk {
			feats = append(feats, "c:"+string(r))
			if i+1 < len(cjk) {
				feats = append(feats, "b:"+string(cjk[i])+string(cjk[i+1]))
			}
		}
		cjk = cjk[:0]
	}

	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			word.WriteRune(r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return feats
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // Extension A
		(r >= 0x3040 && r <= 0x30FF) // Hiragana + Katakana
}
