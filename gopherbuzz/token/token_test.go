package token_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/gopherbuzz/token"
)

func tokenStr(t token.Token) string {
	switch t.Kind {
	case token.Ident:
		return fmt.Sprintf("ident(%s)", t.Val)
	case token.String:
		return fmt.Sprintf("string(%q)", t.Val)
	case token.Int:
		return fmt.Sprintf("int(%s)", t.Val)
	case token.Float:
		return fmt.Sprintf("float(%s)", t.Val)
	case token.True:
		return "bool(true)"
	case token.False:
		return "bool(false)"
	case token.Null:
		return "null"
	case token.Dot:
		return "."
	case token.EOF:
		return "EOF"
	default:
		return fmt.Sprintf("tok(%d)", t.Kind)
	}
}

func TestLexer_Basic(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{`"hello"`, []string{`string("hello")`, "EOF"}},
		{`42`, []string{"int(42)", "EOF"}},
		{`true false null`, []string{"bool(true)", "bool(false)", "null", "EOF"}},
		{`magus.project.register`, []string{`ident(magus)`, ".", `ident(project)`, ".", `ident(register)`, "EOF"}},
		{`// comment\n42`, []string{"int(42)", "EOF"}},
	}
	for _, tc := range tests {
		src := strings.ReplaceAll(tc.src, `\n`, "\n")
		toks, err := token.Tokenize(src)
		if err != nil {
			t.Errorf("tokenize %q: %v", tc.src, err)
			continue
		}
		var got []string
		for _, tok := range toks {
			got = append(got, tokenStr(tok))
		}
		if len(got) != len(tc.want) {
			t.Errorf("tokenize %q: got %v, want %v", tc.src, got, tc.want)
			continue
		}
		for i, w := range tc.want {
			if got[i] != w {
				t.Errorf("tokenize %q token[%d]: got %q, want %q", tc.src, i, got[i], w)
			}
		}
	}
}

// firstDoc returns the Doc of the first token whose Val (or keyword) matches
// ident, for asserting which declaration a comment block attached to.
func docOfIdent(toks []token.Token, ident string) string {
	for _, t := range toks {
		if t.Kind == token.Ident && t.Val == ident {
			return t.Doc
		}
	}
	return ""
}

func TestLexer_DocComments(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		ident string
		want  string
	}{
		{
			name:  "single line comment attaches to next token",
			src:   "// builds the thing\nbuild",
			ident: "build",
			want:  "builds the thing",
		},
		{
			name:  "contiguous lines join",
			src:   "// line one\n// line two\nbuild",
			ident: "build",
			want:  "line one\nline two",
		},
		{
			name:  "blank line breaks the block",
			src:   "// not a doc\n\nbuild",
			ident: "build",
			want:  "",
		},
		{
			name:  "only the last contiguous block attaches after a gap",
			src:   "// stale\n\n// fresh\nbuild",
			ident: "build",
			want:  "fresh",
		},
		{
			name:  "block comment attaches",
			src:   "/* a block doc */\nbuild",
			ident: "build",
			want:  "a block doc",
		},
		{
			name:  "trailing comment on a line does not attach to the next token",
			src:   "x // trailing\nbuild",
			ident: "build",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, err := token.Tokenize(tt.src)
			if err != nil {
				t.Fatalf("tokenize: %v", err)
			}
			if got := docOfIdent(toks, tt.ident); got != tt.want {
				t.Errorf("doc of %q = %q, want %q", tt.ident, got, tt.want)
			}
		})
	}
}
