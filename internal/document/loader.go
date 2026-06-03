// Package document loads raw text from uploaded files and splits it into chunks.
package document

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// LoadText extracts plain text from a file given its name (for type detection)
// and raw bytes. Supported: .txt, .md/.markdown, .pdf.
func LoadText(filename string, data []byte) (string, error) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return loadPDF(data)
	case strings.HasSuffix(lower, ".md"), strings.HasSuffix(lower, ".markdown"):
		return stripMarkdown(string(data)), nil
	case strings.HasSuffix(lower, ".txt"), strings.HasSuffix(lower, ".text"):
		return string(data), nil
	default:
		// Be permissive: treat unknown types as UTF-8 text.
		return string(data), nil
	}
}

// stripMarkdown removes the most intrusive markup so retrieval focuses on prose.
func stripMarkdown(s string) string {
	// images ![alt](url) and links [text](url) -> keep visible text
	imgRe := regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	s = imgRe.ReplaceAllString(s, "")
	linkRe := regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	s = linkRe.ReplaceAllString(s, "$1")
	// code fences, heading hashes, emphasis markers
	s = strings.ReplaceAll(s, "```", "")
	hdrRe := regexp.MustCompile(`(?m)^#{1,6}\s*`)
	s = hdrRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return s
}

// loadPDF extracts text. It first tries the `pdftotext` CLI (poppler-utils),
// which is robust, then falls back to a minimal pure-Go extractor for simple
// PDFs so the project still works if poppler is absent.
func loadPDF(data []byte) (string, error) {
	if txt, err := pdfViaPdftotext(data); err == nil && strings.TrimSpace(txt) != "" {
		return txt, nil
	}
	txt := pdfNaive(data)
	if strings.TrimSpace(txt) == "" {
		return "", fmt.Errorf("could not extract text from PDF (install poppler-utils for better results)")
	}
	return txt, nil
}

func pdfViaPdftotext(data []byte) (string, error) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		return "", err
	}
	// "-" for stdin and stdout.
	cmd := exec.Command("pdftotext", "-q", "-", "-")
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// pdfNaive is a best-effort extractor: it inflates FlateDecode streams and
// pulls text out of (...) Tj / [...] TJ operators. It handles many simple,
// text-based PDFs but is not a full parser.
func pdfNaive(data []byte) string {
	var b strings.Builder
	streamRe := regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
	for _, m := range streamRe.FindAllSubmatch(data, -1) {
		raw := m[1]
		if inflated, err := inflate(raw); err == nil {
			extractTextOps(inflated, &b)
		} else {
			extractTextOps(raw, &b)
		}
	}
	if b.Len() == 0 {
		// last resort: scan whole file for text-show operators
		extractTextOps(data, &b)
	}
	return b.String()
}

func inflate(p []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(p))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

var (
	tjRe  = regexp.MustCompile(`\(((?:\\.|[^\\)])*)\)\s*Tj`)
	tjArr = regexp.MustCompile(`\[((?:\\.|[^\]])*)\]\s*TJ`)
	strRe = regexp.MustCompile(`\(((?:\\.|[^\\)])*)\)`)
)

func extractTextOps(content []byte, b *strings.Builder) {
	for _, m := range tjRe.FindAllSubmatch(content, -1) {
		b.WriteString(unescapePDF(string(m[1])))
		b.WriteString(" ")
	}
	for _, m := range tjArr.FindAllSubmatch(content, -1) {
		for _, s := range strRe.FindAllSubmatch(m[1], -1) {
			b.WriteString(unescapePDF(string(s[1])))
		}
		b.WriteString(" ")
	}
}

func unescapePDF(s string) string {
	return strings.NewReplacer(
		`\(`, "(", `\)`, ")", `\\`, `\`,
		`\n`, "\n", `\r`, "\r", `\t`, "\t",
	).Replace(s)
}
