package document

import (
	"strings"
	"unicode/utf8"
)

// Chunk splits text into overlapping windows of roughly size runes, preferring
// to break on paragraph/sentence boundaries. size and overlap are measured in
// runes (so Chinese text is handled sensibly, not by bytes).
func Chunk(text string, size, overlap int) []string {
	text = normalizeWhitespace(text)
	if text == "" {
		return nil
	}
	if size <= 0 {
		size = 500
	}
	if overlap < 0 || overlap >= size {
		overlap = size / 5
	}

	// Split into semantic units first, then greedily pack into windows.
	units := splitUnits(text)

	var chunks []string
	var cur strings.Builder
	curLen := 0

	flush := func() {
		if curLen > 0 {
			chunks = append(chunks, strings.TrimSpace(cur.String()))
		}
		cur.Reset()
		curLen = 0
	}

	for _, u := range units {
		ul := utf8.RuneCountInString(u)
		if ul > size {
			// A single huge unit: hard-split it.
			flush()
			for _, piece := range hardSplit(u, size, overlap) {
				chunks = append(chunks, piece)
			}
			continue
		}
		if curLen+ul > size && curLen > 0 {
			flush()
			// seed the next window with a tail overlap from the previous chunk
			if overlap > 0 && len(chunks) > 0 {
				tail := tailRunes(chunks[len(chunks)-1], overlap)
				cur.WriteString(tail)
				cur.WriteString(" ")
				curLen = utf8.RuneCountInString(tail) + 1
			}
		}
		cur.WriteString(u)
		cur.WriteString(" ")
		curLen += ul + 1
	}
	flush()
	return chunks
}

// splitUnits breaks text into paragraphs, then sentences.
func splitUnits(text string) []string {
	var units []string
	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		units = append(units, splitSentences(para)...)
	}
	return units
}

// splitSentences splits on common Chinese and English sentence terminators,
// keeping the terminator with its sentence.
func splitSentences(s string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		cur.WriteRune(r)
		switch r {
		case '。', '！', '？', '!', '?', '；', ';', '\n':
			if strings.TrimSpace(cur.String()) != "" {
				out = append(out, strings.TrimSpace(cur.String()))
			}
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

func hardSplit(s string, size, overlap int) []string {
	runes := []rune(s)
	step := size - overlap
	if step <= 0 {
		step = size
	}
	var out []string
	for i := 0; i < len(runes); i += step {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, strings.TrimSpace(string(runes[i:end])))
		if end == len(runes) {
			break
		}
	}
	return out
}

func tailRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

func normalizeWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// collapse 3+ newlines to a paragraph break
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}
