// Package nameoutput keeps `-o name` on the structured-output destination.
//
// The generic formatter cannot render the name format, so every command answers
// it in its own switch arm, and an arm that prints to stdout directly bypasses
// --tee: the flag is accepted, nothing is written, and nothing says so. Each
// `case outputName:` arm must call an emitter, where an emitter is one of the
// seeds or any package function that calls one. The closure is what keeps a
// command's own helper from needing an allowlist entry.
package nameoutput

import (
	"errors"
	"go/ast"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "this `case %s:` arm must render through %s: printing to stdout directly bypasses --tee, " +
	"which then accepts the flag and writes an empty file; a single value is emitNames([]string{v}), " +
	"a slice of records is emitNamesOf(records, func(r T) string { return r.Field })"

const renamed = "no `case %s:` arm found in %s: the format constant was renamed; update nameoutput's case setting"

// Options configures the analyzer returned by [New].
type Options struct {
	// Package is the import path held to the rule.
	Package string `json:"package"`

	// Case is the identifier a name arm selects.
	Case string `json:"case"`

	// Emitters seed the set of functions that reach the structured destination.
	Emitters []string `json:"emitters"`
}

// New returns the analyzer configured by opts, erroring on a missing field.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Package == "" || opts.Case == "" || len(opts.Emitters) == 0 {
		return nil, errors.New("nameoutput: package, case and emitters are all required")
	}
	return &analysis.Analyzer{
		Name: "nameoutput",
		Doc:  "require every name-format switch arm to render through an emitter",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	if pass.Pkg.Path() != opts.Package {
		return nil
	}
	all, err := source.Files(pass)
	if err != nil {
		return err
	}
	files := slices.DeleteFunc(all, func(f *ast.File) bool { return source.IsTest(pass, f) })
	if len(files) == 0 {
		return nil
	}
	emitters := closure(files, opts.Emitters)
	arms := 0
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok || !picks(clause, opts.Case) {
				return true
			}
			arms++
			if !callsAny(clause.Body, emitters) {
				pass.Reportf(clause.Pos(), message, opts.Case, strings.Join(opts.Emitters, " or "))
			}
			return true
		})
	}
	if arms == 0 {
		pass.Reportf(files[0].Name.Pos(), renamed, opts.Case, opts.Package)
	}
	return nil
}

// picks reports whether the clause selects exactly name, so a shared
// `case outputJSON, outputName:` arm is not read as a name arm.
func picks(clause *ast.CaseClause, name string) bool {
	if len(clause.List) != 1 {
		return false
	}
	id, ok := clause.List[0].(*ast.Ident)
	return ok && id.Name == name
}

// closure grows the seeds by every package function that calls one, until
// nothing changes.
func closure(files []*ast.File, seeds []string) map[string]bool {
	emitters := map[string]bool{}
	for _, s := range seeds {
		emitters[s] = true
	}
	for changed := true; changed; {
		changed = false
		for _, f := range files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || emitters[fn.Name.Name] {
					continue
				}
				if callsAny(fn.Body.List, emitters) {
					emitters[fn.Name.Name] = true
					changed = true
				}
			}
		}
	}
	return emitters
}

// callsAny reports whether any statement calls one of names directly.
func callsAny(stmts []ast.Stmt, names map[string]bool) bool {
	found := false
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if found {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && names[id.Name] {
				found = true
			}
			return !found
		})
	}
	return found
}
