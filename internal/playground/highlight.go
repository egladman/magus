package playground

import (
	"unicode"
	"unicode/utf8"

	"github.com/egladman/gopherbuzz/token"
)

// Span is a classified run of source text for the editor's highlight overlay.
// Class is a short CSS-class suffix ("kw","str","num","com","punct") or "" for
// plain text (identifiers, whitespace). Text is the exact source covered, so
// concatenating every Span.Text reproduces the input verbatim.
type Span struct {
	Class string
	Text  string
}

// Highlight scans Buzz source into classified spans for the editor overlay.
//
// Unlike token.Tokenize (the parser's lexer) this is a display-only scanner: it
// never errors, it keeps every byte — including whitespace and comments, which
// the parser discards — and it tracks raw source extents rather than semantic
// values. That total-coverage property is what lets the highlighted layer line
// up character-for-character with the textarea. Keyword classification reuses
// the canonical set via token.IsKeyword, so it stays in sync with the language.
func Highlight(src string) []Span {
	var spans []Span
	emit := func(class, text string) {
		if text == "" {
			return
		}
		// Coalesce adjacent plain runs (whitespace + identifiers) to keep the
		// rendered node count down.
		if class == "" && len(spans) > 0 && spans[len(spans)-1].Class == "" {
			spans[len(spans)-1].Text += text
			return
		}
		spans = append(spans, Span{Class: class, Text: text})
	}

	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			j := i + 2
			for j < n && src[j] != '\n' {
				j++
			}
			emit("com", src[i:j])
			i = j

		case c == '/' && i+1 < n && src[i+1] == '*':
			j := i + 2
			for j < n && !(src[j] == '*' && j+1 < n && src[j+1] == '/') {
				j++
			}
			if j < n {
				j += 2 // consume the closing */
			}
			emit("com", src[i:j])
			i = j

		case c == '"':
			j := i + 1
			for j < n {
				if src[j] == '\\' && j+1 < n {
					j += 2
					continue
				}
				if src[j] == '"' {
					j++
					break
				}
				j++
			}
			emit("str", src[i:j])
			i = j

		case c >= '0' && c <= '9':
			j := i + 1
			for j < n {
				if src[j] >= '0' && src[j] <= '9' {
					j++
					continue
				}
				// A '.' is part of the number only when a digit follows; '..' is
				// the range operator, not a decimal point.
				if src[j] == '.' && j+1 < n && src[j+1] >= '0' && src[j+1] <= '9' {
					j += 2
					continue
				}
				break
			}
			emit("num", src[i:j])
			i = j

		case isIdentStart(rune(c)) || c >= utf8.RuneSelf:
			j := i
			for j < n {
				r, size := utf8.DecodeRuneInString(src[j:])
				if !isIdentPart(r) {
					break
				}
				j += size
			}
			word := src[i:j]
			if token.IsKeyword(word) {
				emit("kw", word)
			} else {
				emit("", word)
			}
			i = j

		case isPunct(c):
			emit("punct", src[i:i+1])
			i++

		default:
			emit("", src[i:i+1])
			i++
		}
	}
	return spans
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func isPunct(c byte) bool {
	switch c {
	case '(', ')', '{', '}', '[', ']', ',', ';', ':', '.',
		'=', '?', '!', '+', '-', '*', '/', '%', '<', '>', '&', '\\':
		return true
	}
	return false
}
