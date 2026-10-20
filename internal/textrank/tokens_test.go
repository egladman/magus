package textrank

import (
	"reflect"
	"testing"
)

func TestSplitIdentifier(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"camel", "leafScoreLower", []string{"leaf", "score", "lower"}},
		{"pascal", "LeafScoreLower", []string{"leaf", "score", "lower"}},
		{"leading acronym", "HTTPServer", []string{"http", "server"}},
		{"inner acronym", "parseJSONBody", []string{"parse", "json", "body"}},
		{"trailing acronym", "parseJSON", []string{"parse", "json"}},
		{"all caps", "URL", []string{"url"}},
		{"digits stay on the word before", "sha256Sum", []string{"sha256", "sum"}},
		{"digits then acronym", "HTTP2Server", []string{"http2", "server"}},
		{"digits inside lowercase", "sha256sum", []string{"sha256sum"}},
		{"snake", "snake_case_name", []string{"snake", "case", "name"}},
		{"kebab", "kebab-case-name", []string{"kebab", "case", "name"}},
		{"dotted", "pkg.Name.Method", []string{"pkg", "name", "method"}},
		{"slashed", "internal/textrank/fuseAll", []string{"internal", "textrank", "fuse", "all"}},
		{"mixed separators", "my_pkg-v2/readFile.go", []string{"my", "pkg", "v2", "read", "file", "go"}},
		{"empty parts dropped", "__a--b..c//", []string{"a", "b", "c"}},
		{"single word", "word", []string{"word"}},
		{"empty", "", nil},
		{"separators only", "_-./", nil},
		{"unicode letters", "größeÜberSicht", []string{"größe", "über", "sicht"}},
		{"unicode acronym", "ÉCOLEPrimaire", []string{"école", "primaire"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitIdentifier(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitIdentifier(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"call", "leafScoreLower(x)", []string{"leafscorelower", "leaf", "score", "lower", "x"}},
		{"snake keeps whole word", "snake_case", []string{"snake_case", "snake", "case"}},
		{"plain words", "Find the File", []string{"find", "the", "file"}},
		{"acronym", "HTTPServer", []string{"httpserver", "http", "server"}},
		{"digits", "sha256Sum", []string{"sha256sum", "sha256", "sum"}},
		{"dotted path is separators", "pkg.readFile", []string{"pkg", "readfile", "read", "file"}},
		{"slashed path", "internal/textrank", []string{"internal", "textrank"}},
		{"kebab is separators", "read-file", []string{"read", "file"}},
		{"leading underscore", "_foo", []string{"_foo"}},
		{"punctuation", "a, b; (c)!", []string{"a", "b", "c"}},
		{"buzz error operator", "fun f() > void !> any", []string{"fun", "f", "void", "!>", "any"}},
		{"error operator between words", "int!>str", []string{"int", "!>", "str"}},
		{"error operator once in a run", "!!>>", []string{"!>"}},
		{"other operators dropped", "a != b -> c => d >! e", []string{"a", "b", "c", "d", "e"}},
		{"no stemming", "running files", []string{"running", "files"}},
		{"unicode", "größeÜber naïve", []string{"größeüber", "größe", "über", "naïve"}},
		{"empty", "", nil},
		{"whitespace only", " \t\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokens(tt.in)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Tokens(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
