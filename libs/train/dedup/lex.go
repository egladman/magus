// Package dedup finds the files of one language that repeat each other: exact copies
// by the sha256 of their normalized bytes, near copies by MinHash over token shingles.
// A pair it returns becomes a link in the split's group deal, so both files land on one
// side. Everything here is deterministic: no clock, no map-order dependence in a result.
package dedup

import (
	"go/scanner"
	"go/token"
	"strings"

	buzztoken "github.com/egladman/magus/libs/gopherbuzz/token"
)

// Languages the dedup and the split know, by the names sources.jsonl uses.
const (
	Buzz       = "buzz"
	Go         = "go"
	TypeScript = "typescript"
)

// Tokens is src's token texts in its own language's lexer, comments and whitespace left
// out. A Buzz file that fails to lex answers an error: the caller decides what an
// unreadable file is worth.
func Tokens(language, src string) ([]string, error) {
	switch language {
	case Buzz:
		toks, err := buzztoken.Tokenize(src)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(toks))
		for _, t := range toks {
			if t.Kind == buzztoken.EOF {
				continue
			}
			out = append(out, t.Kind.String()+"\x00"+t.Val)
		}
		return out, nil
	case Go:
		return goTokens(src), nil
	case TypeScript:
		lex := LexTS(src)
		out := make([]string, 0, len(lex))
		for _, l := range lex {
			out = append(out, l.Text(src))
		}
		return out, nil
	}
	return nil, &UnknownLanguageError{Language: language}
}

// UnknownLanguageError is Tokens' refusal of a language it has no lexer for.
type UnknownLanguageError struct{ Language string }

func (e *UnknownLanguageError) Error() string {
	return "dedup: no lexer for language " + e.Language
}

func goTokens(src string) []string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	// A scan error leaves the token as ILLEGAL; the rest of the file still lexes.
	s.Init(file, []byte(src), func(token.Position, string) {}, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		if lit == "" {
			lit = tok.String()
		}
		out = append(out, lit)
	}
}

// Lexeme is one TypeScript token: its kind and its byte range in the source.
type Lexeme struct {
	Kind       LexKind
	Start, End int
}

// Text is the lexeme's source text.
func (l Lexeme) Text(src string) string { return src[l.Start:l.End] }

// LexKind classifies a TypeScript lexeme.
type LexKind byte

const (
	LexIdent LexKind = iota
	LexNumber
	LexString
	LexTemplate
	LexPunct
)

// LexTS is a TypeScript lexer good enough for shingling and bracket matching: identifiers,
// numbers, strings, template literals, and single-rune punctuation. Comments and
// whitespace are dropped. A regular-expression literal lexes as punctuation and
// identifiers; a quote inside one can swallow the next line, which the shingles tolerate.
func LexTS(src string) []Lexeme {
	var out []Lexeme
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '"' || c == '\'':
			start := i
			i++
			for i < n && src[i] != c && src[i] != '\n' {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			i = min(i+1, n)
			out = append(out, Lexeme{LexString, start, i})
		case c == '`':
			start := i
			i = templateEnd(src, i+1)
			out = append(out, Lexeme{LexTemplate, start, i})
		case isIdentStart(c):
			start := i
			for i < n && (isIdentStart(src[i]) || src[i] >= '0' && src[i] <= '9') {
				i++
			}
			out = append(out, Lexeme{LexIdent, start, i})
		case c >= '0' && c <= '9':
			start := i
			for i < n && (isIdentStart(src[i]) || src[i] >= '0' && src[i] <= '9' || src[i] == '.') {
				i++
			}
			out = append(out, Lexeme{LexNumber, start, i})
		default:
			out = append(out, Lexeme{LexPunct, i, i + 1})
			i++
		}
	}
	return out
}

// templateEnd returns the offset after the backquote that closes the template literal whose
// body starts at i, skipping nested ${...} expressions and their own templates.
func templateEnd(src string, i int) int {
	n := len(src)
	for i < n {
		switch src[i] {
		case '\\':
			i += 2
		case '`':
			return i + 1
		case '$':
			if i+1 < n && src[i+1] == '{' {
				depth := 1
				i += 2
				for i < n && depth > 0 {
					switch src[i] {
					case '{':
						depth++
					case '}':
						depth--
					case '`':
						i = templateEnd(src, i+1)
						continue
					}
					i++
				}
				continue
			}
			i++
		default:
			i++
		}
	}
	return n
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
