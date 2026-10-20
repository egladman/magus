package textindex

import (
	"bytes"
	"fmt"
)

// ReadFunc supplies a file's bytes. Injected rather than assumed so a caller can serve
// from a cache, an archive, or memory, and so tests need no filesystem.
type ReadFunc func(path string) ([]byte, error)

// Match is one occurrence: the file, the 1-based line and column, and that whole line.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

// Scan searches paths for pattern, in path order then line order. A file read fails on
// is skipped: the file set is routinely a live working tree, where a file can vanish
// between the walk and the read.
//
// fold lowercases both sides. It allocates a copy per file, which is why it is a
// parameter rather than the default.
func Scan(paths []string, read ReadFunc, pattern string, fold bool) ([]Match, error) {
	if pattern == "" {
		return nil, fmt.Errorf("textindex: an empty pattern matches everything, say what you are looking for")
	}
	pat := []byte(pattern)
	if fold {
		pat = bytes.ToLower(pat)
	}
	var out []Match
	for _, p := range paths {
		body, err := read(p)
		if err != nil {
			continue
		}
		hay := body
		if fold {
			hay = bytes.ToLower(body)
		}
		for _, m := range matchesIn(p, hay, pat) {
			// The line is reported from the ORIGINAL bytes: a reader acting on this needs
			// the text as it is written, not as it was folded for comparison.
			if fold {
				m.Text = lineAt(body, m.Line)
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// lineAt returns the 1-based line of body, without its newline.
func lineAt(body []byte, line int) string {
	start := 0
	for n := 1; n < line; n++ {
		nl := bytes.IndexByte(body[start:], '\n')
		if nl < 0 {
			return ""
		}
		start += nl + 1
	}
	end := bytes.IndexByte(body[start:], '\n')
	if end < 0 {
		return string(body[start:])
	}
	return string(body[start : start+end])
}

// matchesIn walks one file's occurrences, carrying the line number forward rather than
// recounting newlines from the start for each hit.
func matchesIn(path string, body, pat []byte) []Match {
	var out []Match
	line, lineStart, from := 1, 0, 0
	for {
		rel := bytes.Index(body[from:], pat)
		if rel < 0 {
			return out
		}
		at := from + rel
		for nl := bytes.IndexByte(body[lineStart:at], '\n'); nl >= 0; nl = bytes.IndexByte(body[lineStart:at], '\n') {
			line++
			lineStart += nl + 1
		}
		end := bytes.IndexByte(body[lineStart:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += lineStart
		}
		out = append(out, Match{
			Path: path,
			Line: line,
			Col:  at - lineStart + 1,
			Text: string(body[lineStart:end]),
		})
		from = at + len(pat)
		if from > len(body)-len(pat) {
			return out
		}
	}
}
