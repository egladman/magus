package secretfields

import (
	"errors"
	"go/ast"
	"go/types"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s holds a credential as %s, which every fmt verb, log and encoder prints: " +
	"make it a self-redacting secret type, or rename it so its last word says what it holds"

// Options configures the analyzer returned by [New].
type Options struct {
	// Words name a credential. A name is a secret when its last camel-case words
	// spell one, case-insensitively: "registryToken" is, "PasswordRef" is not.
	Words []string `json:"words"`

	// Hint is appended to every diagnostic: the repository's own secret type.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring on an empty word list.
func New(opts Options) (*analysis.Analyzer, error) {
	if len(opts.Words) == 0 {
		return nil, errors.New("secretfields: words is empty, so nothing would be reported")
	}
	words := make([][]string, 0, len(opts.Words))
	for _, w := range opts.Words {
		parts := camelWords(w)
		if len(parts) == 0 {
			return nil, errors.New("secretfields: a word is empty")
		}
		words = append(words, parts)
	}
	msg := message
	if opts.Hint != "" {
		msg += "; " + opts.Hint
	}
	return &analysis.Analyzer{
		Name: "secretfields",
		Doc: "report a struct field or named result whose name ends in a credential word and whose type " +
			"is string, []byte or a container of them; test files hold fixtures and are not checked",
		Run: func(pass *analysis.Pass) (any, error) { return nil, run(pass, words, msg) },
	}, nil
}

func run(pass *analysis.Pass, words [][]string, msg string) error {
	if pass.TypesInfo == nil {
		return errors.New("secretfields: the pass carries no type information, so no field can be checked")
	}
	check := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, f := range fields.List {
			t := pass.TypesInfo.TypeOf(f.Type)
			if t == nil || !plaintext(t) {
				continue
			}
			for _, name := range f.Names {
				if isSecretName(name.Name, words) {
					pass.Reportf(name.Pos(), msg, name.Name, types.TypeString(t, types.RelativeTo(pass.Pkg)))
				}
			}
		}
	}
	for _, f := range pass.Files {
		if source.IsTest(pass, f) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.StructType:
				check(n.Fields)
			case *ast.FuncType:
				check(n.Results)
			}
			return true
		})
	}
	return nil
}

// plaintext reports whether t is string or []byte, or a slice, array, map or pointer
// reaching one. A named type passes: an enum over string is not a credential, and a
// self-redacting secret type is the fix.
func plaintext(t types.Type) bool {
	switch t := types.Unalias(t).(type) {
	case *types.Basic:
		return t.Kind() == types.String
	case *types.Slice:
		if b, ok := types.Unalias(t.Elem()).(*types.Basic); ok && b.Kind() == types.Byte {
			return true
		}
		return plaintext(t.Elem())
	case *types.Array:
		return plaintext(t.Elem())
	case *types.Pointer:
		return plaintext(t.Elem())
	case *types.Map:
		return plaintext(t.Key()) || plaintext(t.Elem())
	}
	return false
}

func isSecretName(name string, words [][]string) bool {
	parts := camelWords(name)
	for _, w := range words {
		if len(w) > len(parts) {
			continue
		}
		tail := parts[len(parts)-len(w):]
		match := true
		for i := range w {
			if !strings.EqualFold(tail[i], w[i]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// camelWords splits a Go name into its words: "registryToken" is registry, Token;
// "APIKey" is API, Key; "signing_key" is signing, key.
func camelWords(name string) []string {
	var words []string
	runes := []rune(name)
	start := 0
	flush := func(end int) {
		if end > start {
			words = append(words, string(runes[start:end]))
		}
		start = end
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '_' {
			flush(i)
			start = i + 1
			continue
		}
		if i == start || !unicode.IsUpper(r) {
			continue
		}
		prev := runes[i-1]
		nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
			flush(i)
		}
	}
	flush(len(runes))
	return words
}
