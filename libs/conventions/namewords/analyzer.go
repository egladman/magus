// Package namewords holds function and method names to the words they may use:
// a name says what it returns or does, so it never ends in a preposition such as
// Of, For or To, and never shortens a word to a form such as Src or Cfg.
//
// Only a declared function or method is judged; variables, parameters, fields
// and types are not. A test, benchmark, fuzz or example entry point reads as a
// sentence and is skipped, as is a generated file and a method that satisfies a
// standard library interface, whose name the interface chose (io.WriterTo's
// WriteTo).
package namewords

import (
	"errors"
	"go/ast"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const (
	suffixMessage = "%s ends in the word %q: name it for what it returns or does"
	shortMessage  = "%s uses the short form %q: spell the word out"
)

// Options configures the analyzer returned by [New]. Words match a name's
// camel-case or underscore-separated words regardless of case.
type Options struct {
	// Suffixes are the words a name must not end in. A one-word name is never
	// judged against them: it has no last word to drop.
	Suffixes []string `json:"suffixes"`

	// ShortForms are the abbreviations a name must not use as any of its words.
	ShortForms []string `json:"short-forms"`
}

// New returns the analyzer configured by opts, erroring when both word lists
// are empty.
func New(opts Options) (*analysis.Analyzer, error) {
	if len(opts.Suffixes) == 0 && len(opts.ShortForms) == 0 {
		return nil, errors.New("namewords: suffixes and short-forms are both empty")
	}
	return &analysis.Analyzer{
		Name: "namewords",
		Doc:  "report function and method names ending in a preposition or using a short form",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	var stdlib map[string][]*types.Interface
	for _, f := range files {
		if ast.IsGenerated(f) {
			continue
		}
		test := source.IsTest(pass, f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || test && fn.Recv == nil && isEntryPoint(fn.Name.Name) {
				continue
			}
			suffix, short := judge(fn.Name.Name, opts)
			if suffix == "" && short == "" {
				continue
			}
			if fn.Recv != nil {
				if stdlib == nil {
					stdlib = stdlibMethods(pass.Pkg)
				}
				if satisfiesAny(pass, fn, stdlib[fn.Name.Name]) {
					continue
				}
			}
			if suffix != "" {
				pass.Reportf(fn.Name.Pos(), suffixMessage, fn.Name.Name, suffix)
			}
			if short != "" {
				pass.Reportf(fn.Name.Pos(), shortMessage, fn.Name.Name, short)
			}
		}
	}
	return nil
}

// judge returns the configured suffix name ends in and the first short form it
// uses, each spelled as the name spells it, or "" for none.
func judge(name string, opts Options) (suffix, short string) {
	words := splitWords(name)
	if len(words) > 1 {
		last := words[len(words)-1]
		for _, s := range opts.Suffixes {
			if strings.EqualFold(last, s) {
				suffix = last
				break
			}
		}
	}
	for _, w := range words {
		for _, s := range opts.ShortForms {
			if strings.EqualFold(w, s) {
				return suffix, w
			}
		}
	}
	return suffix, ""
}

// splitWords splits name at underscores, camel-case boundaries and runs of
// digits. An acronym stays one word up to the capital that opens the next:
// URLFor is URL, For.
func splitWords(name string) []string {
	var words []string
	for _, part := range strings.Split(name, "_") {
		runes := []rune(part)
		start := 0
		for i := 1; i < len(runes); i++ {
			prev, cur := runes[i-1], runes[i]
			lowerToUpper := !unicode.IsUpper(prev) && unicode.IsUpper(cur)
			acronymEnd := unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			digits := unicode.IsDigit(prev) != unicode.IsDigit(cur)
			if lowerToUpper || acronymEnd || digits {
				words = append(words, string(runes[start:i]))
				start = i
			}
		}
		if start < len(runes) {
			words = append(words, string(runes[start:]))
		}
	}
	return words
}

// isEntryPoint reports whether name is one the go tool runs from a test file:
// the prefix alone, or followed by anything but a lowercase letter.
func isEntryPoint(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(rest); rest == "" || !unicode.IsLower(r) {
			return true
		}
	}
	return false
}

// stdlibMethods maps a method name to the exported interfaces declaring it, in
// every standard library package pkg reaches through its imports.
func stdlibMethods(pkg *types.Package) map[string][]*types.Interface {
	methods := map[string][]*types.Interface{}
	seen := map[*types.Package]bool{}
	var visit func(p *types.Package)
	visit = func(p *types.Package) {
		if seen[p] {
			return
		}
		seen[p] = true
		for _, imp := range p.Imports() {
			visit(imp)
		}
		if p == pkg || !isStdlib(p.Path()) {
			return
		}
		scope := p.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || !obj.Exported() {
				continue
			}
			iface, ok := obj.Type().Underlying().(*types.Interface)
			if !ok {
				continue
			}
			for i := range iface.NumMethods() {
				m := iface.Method(i).Name()
				methods[m] = append(methods[m], iface)
			}
		}
	}
	visit(pkg)
	return methods
}

// isStdlib reports whether path names a standard library package: its first
// element has no dot, as every module path's host does.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// satisfiesAny reports whether fn's receiver type, or a pointer to it,
// implements one of ifaces.
func satisfiesAny(pass *analysis.Pass, fn *ast.FuncDecl, ifaces []*types.Interface) bool {
	if len(ifaces) == 0 {
		return false
	}
	obj, ok := pass.TypesInfo.Defs[fn.Name].(*types.Func)
	if !ok {
		// A file this platform's build excludes has no types, so the method
		// name alone decides; the platform that compiles it checks the rest.
		return true
	}
	recv := obj.Type().(*types.Signature).Recv().Type()
	candidates := []types.Type{recv}
	if _, ptr := recv.(*types.Pointer); !ptr {
		candidates = append(candidates, types.NewPointer(recv))
	}
	for _, iface := range ifaces {
		for _, t := range candidates {
			if types.Implements(t, iface) {
				return true
			}
		}
	}
	return false
}
