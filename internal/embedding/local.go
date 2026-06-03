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
//   - latin/alphanumeric word unigrams, lower-cased
//   - latin word bigrams for phrase-level matching
//   - latin character trigrams (robust against spelling typos and word forms)
//   - CJK character unigrams and bigrams (good for Chinese without a tokenizer)
//
// Feature frequency is dampened with sqrt so common words don't dominate.
func features(text string) []string {
	text = strings.ToLower(text)
	var feats []string
	var word strings.Builder
	var cjk []rune
	var words []string // track consecutive words for bigrams

	flushWord := func() {
		if word.Len() > 0 {
			w := "w:" + word.String()
			feats = append(feats, w)
			// Character trigrams for the word help match similar words.
			runes := []rune(word.String())
			for i := 0; i+2 < len(runes); i++ {
				feats = append(feats, "t:"+string(runes[i:i+3]))
			}
			words = append(words, w)
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
		// CJK runs also reset the word-bigram window.
		if len(cjk) > 0 {
			words = nil
		}
		cjk = cjk[:0]
	}
	flushBigrams := func() {
		for i := 0; i+1 < len(words); i++ {
			feats = append(feats, "p:"+words[i]+"|"+words[i+1])
		}
		words = nil
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
			flushBigrams()
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushBigrams()
	flushCJK()

	// Dampen frequency: replace repeated features with sqrt-counted copies.
	return dampenFreq(feats)
}

// dampenFreq reduces the impact of high-frequency features by taking sqrt of
// each feature's occurrence count. This follows the sub-linear TF scaling used
// in BM25 / TF-IDF, keeping common words from dominating the sparse vector.
func dampenFreq(feats []string) []string {
	counts := map[string]int{}
	for _, f := range feats {
		counts[f]++
	}
	written := map[string]int{}
	out := make([]string, 0, len(feats))
	for _, f := range feats {
		c := counts[f]
		limit := int(math.Sqrt(float64(c)))
		if limit < 1 {
			limit = 1
		}
		if written[f] < limit {
			out = append(out, f)
			written[f]++
		}
	}
	return out
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // Extension A
		(r >= 0x3040 && r <= 0x30FF) // Hiragana + Katakana
}
