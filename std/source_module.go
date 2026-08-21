package std

import (
	"fmt"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// SourceModule is a stdlib module written in BUZZ rather than in Go.
//
// WHY THIS EXISTS. Every other module here is a Go implementation with a
// generated Buzz trampoline, which means extending the standard library requires
// writing Go, rebuilding the binary, and shipping a release. That is the right
// trade for anything on a hot path - a Go binding beats an equivalent Buzz
// implementation by 9-13x on string work and 2.2x even with no boundary to cross
// (internal/interp/bindings/hostvsbuzz_bench_test.go) - and the wrong one for
// everything else:
//
//   - POLICY that should be readable by the people it governs. tools/toolchain.buzz
//     says so in its own header: "NOTHING HERE IS IN THE MAGUS BINARY, and that is
//     the point ... it lives in the workspace so its trust decisions are yours to
//     read and revise."
//   - CONTRIBUTIONS from someone who writes magusfiles and not Go.
//   - ITERATION without a rebuild.
//
// HOW IT WORKS, and why it needs almost nothing. gopherbuzz already executes a
// declaration module that carries no native value - that is how `magus/spell`
// gives spells their Command/Target types, and how upstream's own assert/suite/
// testing modules are shipped. Real Buzz source, executed on import, exported
// functions and object types, fully type-checked. The language side was already
// finished; what was missing was any way for magus to REGISTER one, which meant
// nothing downstream could see it.
//
// THE DESCRIPTOR IS DERIVED, never hand-written. Parsing the source for its
// exported functions costs one parse at init and makes drift impossible; a Go
// descriptor sitting beside the .buzz would be a second source of truth that
// silently disagrees the first time someone edits one and not the other.
type SourceModule struct {
	// Name is the identifier the module binds as, exactly like Module.Name.
	Name string
	// Path is the import spelling when it differs from Name.
	Path string
	// Doc is the one-line module summary for `magus describe modules`.
	Doc string
	// Source is the Buzz implementation, normally a go:embed of a .buzz file.
	Source string
}

// sourceModules is the registry of Buzz-implemented modules, kept beside the Go
// one so All() can report both as a single surface.
var sourceModules = map[string]SourceModule{}

// RegisterSource adds a Buzz-implemented module to the registry.
//
// It PARSES the source and panics on a failure or on a name collision with a Go
// module, at init, for the same reason Register validates a Go Impl there: a
// stdlib module that does not load is a programmer error, and discovering it when
// a magusfile imports it makes it look like the magusfile's fault.
func RegisterSource(m SourceModule) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := modules[m.Name]; exists {
		panic(fmt.Sprintf("std: %q is already registered as a Go module", m.Name))
	}
	if _, exists := sourceModules[m.Name]; exists {
		panic(fmt.Sprintf("std: duplicate source module registration: %q", m.Name))
	}
	if _, err := describeSource(m); err != nil {
		panic(fmt.Sprintf("std: source module %q: %s", m.Name, err))
	}
	sourceModules[m.Name] = m
}

// AllSource returns a snapshot of every registered SourceModule.
func AllSource() []SourceModule {
	mu.Lock()
	defer mu.Unlock()
	out := make([]SourceModule, 0, len(sourceModules))
	for _, m := range sourceModules {
		out = append(out, m)
	}
	return out
}

// GetSource returns the SourceModule registered under name.
func GetSource(name string) (SourceModule, bool) {
	mu.Lock()
	defer mu.Unlock()
	m, ok := sourceModules[name]
	return m, ok
}

// ImportPath returns the path a magusfile imports this module by.
func (m SourceModule) ImportPath() string {
	if m.Path != "" {
		return m.Path
	}
	return m.Name
}

// describeSource parses a source module and derives its method list from the
// exported functions the Buzz source declares.
//
// It reads the same three things a Go descriptor states by hand - the name, the
// doc comment and the signature - straight off the AST, so the two can never
// disagree. An `extern` is skipped: it is a forward declaration of something the
// module does not implement.
//
// KNOWN LIMITS, stated because they are visible in the output rather than
// theoretical. ast.ObjectDecl and ast.EnumDecl carry no Doc field (only FunDecl
// does), so an exported type appears without its comment. And the parser consumes
// the `!>` error-set annotation without storing it, so a raising function cannot
// be reported as one.
func describeSource(m SourceModule) ([]Method, error) {
	prog, err := buzz.ParseEmbedded(m.Source)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var out []Method
	for _, stmt := range prog.Stmts {
		fn, ok := stmt.(*ast.FunDecl)
		if !ok || !fn.IsExported || fn.IsExtern {
			continue
		}
		out = append(out, Method{
			// The Buzz source already writes the name the way a caller types it, so
			// it is taken verbatim rather than round-tripped through snake_case.
			Name:     fn.Name,
			BuzzName: fn.Name,
			Doc:      docSummary(fn.Doc),
			Args:     sourceArgs(fn),
			Returns:  sourceReturns(fn),
		})
	}
	return out, nil
}

// sourceArgs renders a Buzz function's parameters as descriptor Args.
//
// The TYPE is carried as the annotation TEXT rather than mapped onto a TypeTag.
// Buzz's type language is wider than the tag set - a function type, a nested map,
// a declared object - so mapping would either lose information or reject a
// legitimate signature. Nothing generates marshaling code from these (the module
// IS Buzz; there is no boundary to cross), so the text is the honest
// representation and the one describe/docs should show.
func sourceArgs(fn *ast.FunDecl) []Arg {
	args := make([]Arg, 0, len(fn.Params))
	for i, p := range fn.Params {
		a := Arg{Name: p, Type: TypeAny}
		if i < len(fn.ParamAnnots) && fn.ParamAnnots[i] != "" {
			a.Object = fn.ParamAnnots[i]
		}
		if i < len(fn.ParamDefaults) && fn.ParamDefaults[i] != nil {
			a.Optional = true
		}
		args = append(args, a)
	}
	return args
}

// sourceReturns renders a Buzz function's return annotation as descriptor Rets.
// A void or unannotated function returns nothing.
func sourceReturns(fn *ast.FunDecl) []Ret {
	if fn.RetAnnot == "" || fn.RetAnnot == "void" {
		return nil
	}
	return []Ret{{Type: TypeAny, Object: fn.RetAnnot}}
}

// docSummary reduces a Buzz doc block to the one line describe and the docs site
// render, which is a single sentence for every Go module and must be for these too.
//
// The lexer has already stripped the leading `//` from each line, so a `///` doc
// arrives with ONE slash left on every line. Trimming a run of slashes rather
// than a fixed marker handles both comment styles without caring which the author
// used.
func docSummary(doc string) string {
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "/"))
		if line != "" {
			return line
		}
	}
	return ""
}

// SourceModulesAsModules projects every registered SourceModule into the Module
// shape, so a consumer that renders modules - the docs generator, the langservice
// manifest - handles both kinds through one code path instead of growing a second
// branch that drifts.
//
// The Methods are derived from the Buzz source; Impl is nil on every one, which is
// the only observable difference and matters to nothing downstream: codegen reads
// std.All(), never this.
func SourceModulesAsModules() []Module {
	src := AllSource()
	out := make([]Module, 0, len(src))
	for _, sm := range src {
		methods, err := describeSource(sm)
		if err != nil {
			methods = nil // unreachable: RegisterSource parses at init
		}
		out = append(out, Module{Name: sm.Name, Path: sm.Path, Doc: sm.Doc, Methods: methods})
	}
	return out
}
