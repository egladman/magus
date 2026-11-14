package namewords

import (
	"errors"
	"fmt"
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

	// Banned are the words some packages may not use in any declared name.
	Banned []Banned `json:"banned"`
}

// New returns the analyzer configured by opts, erroring when every word list
// is empty or a banned rule is incomplete.
func New(opts Options) (*analysis.Analyzer, error) {
	if len(opts.Suffixes) == 0 && len(opts.ShortForms) == 0 && len(opts.Banned) == 0 {
		return nil, errors.New("namewords: suffixes, short-forms and banned are all empty")
	}
	if err := validateBanned(opts.Banned); err != nil {
		return nil, fmt.Errorf("checking the banned rules: %w", err)
	}
	return &analysis.Analyzer{
		Name: "namewords",
		Doc:  "report function and method names ending in a preposition or using a short form, and names carrying a word their package bans",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	var stdlib map[string][]*types.Interface
	banned := applicable(opts.Banned, pass.Pkg.Path())
	for _, f := range files {
		if ast.IsGenerated(f) {
			continue
		}
		test := source.IsTest(pass, f)
		if len(banned) > 0 {
			checkBanned(pass, f, banned, test)
		}
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

const bannedMessage = "%s carries the word %q: %s"

// Banned keeps a word out of every declared name in some packages, so a word
// reserved for one layer cannot leak into the types above it.
type Banned struct {
	// Packages are import paths. One ending in /... covers every package below
	// it too, and an external test package counts as the package it tests.
	Packages []string `json:"packages"`

	// Words are matched like [Options] words.
	Words []string `json:"words"`

	// Why is shown with each report: what to name the thing instead.
	Why string `json:"why"`
}

func (b Banned) validate() error {
	var errs []error
	if len(b.Packages) == 0 {
		errs = append(errs, errors.New("packages is empty"))
	}
	if len(b.Words) == 0 {
		errs = append(errs, errors.New("words is empty"))
	}
	if b.Why == "" {
		errs = append(errs, errors.New("why is empty: say what to name instead"))
	}
	return errors.Join(errs...)
}

func validateBanned(rules []Banned) error {
	var errs []error
	for i, r := range rules {
		if err := r.validate(); err != nil {
			errs = append(errs, fmt.Errorf("banned[%d]: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

func (b Banned) covers(path string) bool {
	path = strings.TrimSuffix(path, "_test")
	for _, p := range b.Packages {
		if base, below := strings.CutSuffix(p, "/..."); below {
			if path == base || strings.HasPrefix(path, base+"/") {
				return true
			}
		} else if path == p {
			return true
		}
	}
	return false
}

func applicable(rules []Banned, path string) []Banned {
	var out []Banned
	for _, r := range rules {
		if r.covers(path) {
			out = append(out, r)
		}
	}
	return out
}

// bannedWord is the first word of name that b bans, spelled as name spells it.
func (b Banned) bannedWord(name string) string {
	for _, w := range splitWords(name) {
		for _, banned := range b.Words {
			if strings.EqualFold(w, banned) {
				return w
			}
		}
	}
	return ""
}

// checkBanned judges each top-level name of file, each struct field and each
// interface method. A parameter, a local and an embedded field are not judged:
// an embedded field takes its type's name.
func checkBanned(pass *analysis.Pass, file *ast.File, rules []Banned, test bool) {
	report := func(id *ast.Ident) {
		for _, rule := range rules {
			if w := rule.bannedWord(id.Name); w != "" {
				pass.Reportf(id.Pos(), bannedMessage, id.Name, w, rule.Why)
				return
			}
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !test || d.Recv != nil || !isEntryPoint(d.Name.Name) {
				report(d.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					report(s.Name)
					reportMembers(s.Type, report)
				case *ast.ValueSpec:
					for _, name := range s.Names {
						report(name)
					}
				}
			}
		}
	}
}

func reportMembers(expr ast.Expr, report func(*ast.Ident)) {
	ast.Inspect(expr, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok {
			for _, name := range field.Names {
				report(name)
			}
		}
		return true
	})
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
