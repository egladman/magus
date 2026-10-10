package stderrprint

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

const writeMessage = "this writes to os.Stderr directly, past the log display: -q, -s, redaction and " +
	"-o jsonl never see it; log it as a record, or print help from a usage function"

const handFormat = "this hands os.Stderr to %s, which writes to it past the log display: -q, -s, " +
	"redaction and -o jsonl never see it; log it as a record, or print help from a usage function"

const cleanFormat = "%s writes nothing to os.Stderr now; delete its stderrprint allow entry"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [AllowEntry.File] is relative to.
	Module string `json:"module"`

	// UsagePattern matches the name of a function that prints help. A write
	// inside one, or inside a closure it holds, is help.
	UsagePattern string `json:"usage-pattern"`

	// UsageFields name the fields a help function is assigned to, such as
	// flag.FlagSet's Usage. The closure or function assigned to one is help.
	UsageFields []string `json:"usage-fields"`

	// CalleePackages are import path prefixes whose functions write wherever
	// they are told. Handing os.Stderr to one is a write, unless the callee is
	// help.
	CalleePackages []string `json:"callee-packages"`

	// Display names the callees that are the display itself, which is handed
	// the terminal to draw on or probe: an import path, which covers its
	// packages, or a function as "<import path>.<name>".
	Display []string `json:"display"`

	// Allow exempts a file: the display itself, or a file staged for the move.
	// An entry naming one file without a pattern is reported once that file
	// writes nothing, so a staged entry cannot outlive its findings.
	Allow []AllowEntry `json:"allow"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// AllowEntry exempts one file.
type AllowEntry struct {
	// File is a [source.Globs] pattern over module-relative paths.
	File string `json:"file"`

	// Reason says why the file may write to os.Stderr.
	Reason string `json:"reason"`
}

// New returns the analyzer configured by opts. It reports a write to os.Stderr from
// fmt.Fprint, Fprintf, Fprintln, io.WriteString or the Write methods, and os.Stderr
// handed to a function of [Options.CalleePackages], anywhere but help and allowed
// files: results belong on stdout, and every other stderr line on the log display,
// where the verbosity flags, redaction and -o jsonl reach it.
//
// New errors on a malformed usage pattern or glob, when nothing could ever be help, and
// on an allow entry with no reason or no file it matches.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.UsagePattern == "" && len(opts.UsageFields) == 0 {
		return nil, errors.New("stderrprint: usage-pattern and usage-fields are both empty; no help could print")
	}
	var usage *regexp.Regexp
	if opts.UsagePattern != "" {
		re, err := regexp.Compile(opts.UsagePattern)
		if err != nil {
			return nil, fmt.Errorf("stderrprint: usage-pattern: %w", err)
		}
		usage = re
	}
	allowed := make(source.Globs, 0, len(opts.Allow))
	for _, a := range opts.Allow {
		if a.File == "" || a.Reason == "" {
			return nil, fmt.Errorf("stderrprint: allow entry %+v needs both file and reason", a)
		}
		allowed = append(allowed, a.File)
	}
	if err := allowed.Validate("stderrprint"); err != nil {
		return nil, err
	}
	if err := source.InModule("stderrprint", opts.Module, func(root string) error {
		return allowed.RequireMatches("stderrprint", "allow", root)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "stderrprint",
		Doc:  "report a write to os.Stderr outside a usage function and the display",
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, run(pass, opts, usage, allowed)
		},
	}, nil
}

func run(pass *analysis.Pass, opts Options, usage *regexp.Regexp, allowed source.Globs) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	w := walker{pass: pass, opts: opts, usage: usage, help: usageFuncs(pass, files, opts.UsageFields)}
	for _, f := range files {
		rel, ok := source.Rel(pass, opts.Module, f)
		if !ok || source.IsTest(pass, f) {
			continue
		}
		exempt, found := allowed.Match(rel), 0
		w.report = func(pos token.Pos, msg string) {
			found++
			if !exempt {
				pass.Reportf(pos, "%s", source.Hint(msg, opts.Hint))
			}
		}
		w.file(f)
		if found == 0 && slices.Contains(allowed, rel) {
			pass.Reportf(f.Package, cleanFormat, rel)
		}
	}
	return nil
}

// usageFuncs is every package function assigned to a usage field, by assignment or in
// a composite literal, so `fs.Usage = printFlags` makes printFlags help.
func usageFuncs(pass *analysis.Pass, files []*ast.File, fields []string) map[types.Object]bool {
	help := map[types.Object]bool{}
	note := func(e ast.Expr) {
		if id, ok := ast.Unparen(e).(*ast.Ident); ok {
			if fn, ok := pass.TypesInfo.Uses[id].(*types.Func); ok {
				help[fn] = true
			}
		}
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range n.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && i < len(n.Rhs) && slices.Contains(fields, sel.Sel.Name) {
						note(n.Rhs[i])
					}
				}
			case *ast.KeyValueExpr:
				if key, ok := n.Key.(*ast.Ident); ok && slices.Contains(fields, key.Name) {
					note(n.Value)
				}
			}
			return true
		})
	}
	return help
}

type walker struct {
	pass  *analysis.Pass
	opts  Options
	usage *regexp.Regexp
	// help holds the functions assigned to a usage field.
	help   map[types.Object]bool
	report func(token.Pos, string)
}

// file reports every write in f outside help.
func (w walker) file(f *ast.File) {
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Body == nil || w.isUsageName(decl.Name.Name) || w.help[w.pass.TypesInfo.Defs[decl.Name]] {
				continue
			}
			w.walk(decl.Body)
		case *ast.GenDecl:
			w.walk(decl)
		}
	}
}

func (w walker) isUsageName(name string) bool {
	return w.usage != nil && w.usage.MatchString(name)
}

// walk reports every write in n, skipping the closures that print help.
func (w walker) walk(n ast.Node) {
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if help := w.helpClosure(n); help != nil {
				for _, e := range slices.Concat(n.Lhs, n.Rhs) {
					if e != help {
						w.walk(e)
					}
				}
				return false
			}
		case *ast.KeyValueExpr:
			if _, lit := n.Value.(*ast.FuncLit); lit && w.usageTarget(n.Key) {
				return false
			}
		case *ast.ValueSpec:
			if len(n.Values) != len(n.Names) {
				break
			}
			for i, name := range n.Names {
				if _, lit := n.Values[i].(*ast.FuncLit); lit && w.isUsageName(name.Name) {
					return false
				}
			}
		case *ast.CallExpr:
			if w.writesStderr(n) {
				w.report(n.Pos(), writeMessage)
			} else if name := w.handsStderr(n); name != "" {
				w.report(n.Pos(), fmt.Sprintf(handFormat, name))
			}
		}
		return true
	})
}

// helpClosure is the closure n assigns to a usage target, or nil.
func (w walker) helpClosure(n *ast.AssignStmt) ast.Expr {
	if len(n.Lhs) != len(n.Rhs) {
		return nil
	}
	for i, lhs := range n.Lhs {
		if _, lit := n.Rhs[i].(*ast.FuncLit); lit && w.usageTarget(lhs) {
			return n.Rhs[i]
		}
	}
	return nil
}

// usageTarget reports whether a closure assigned to e prints help: e is a usage field,
// or a variable named like a usage function.
func (w walker) usageTarget(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.SelectorExpr:
		return slices.Contains(w.opts.UsageFields, e.Sel.Name)
	case *ast.Ident:
		return slices.Contains(w.opts.UsageFields, e.Name) || w.isUsageName(e.Name)
	}
	return false
}

// writesStderr reports whether call is one of the writes to os.Stderr itself.
func (w walker) writesStderr(call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(w.pass.TypesInfo, call).(*types.Func)
	if !ok {
		return false
	}
	switch fn.FullName() {
	case "fmt.Fprint", "fmt.Fprintf", "fmt.Fprintln", "io.WriteString":
		return len(call.Args) > 0 && w.isStderr(call.Args[0])
	case "(*os.File).Write", "(*os.File).WriteString":
		sel, ok := call.Fun.(*ast.SelectorExpr)
		return ok && w.isStderr(sel.X)
	}
	return false
}

// handsStderr names the callee when call passes it os.Stderr and the callee belongs to
// [Options.CalleePackages] without being help or the display; otherwise it is "".
func (w walker) handsStderr(call *ast.CallExpr) string {
	fn, ok := typeutil.Callee(w.pass.TypesInfo, call).(*types.Func)
	if !ok || fn.Pkg() == nil || w.isUsageName(fn.Name()) || w.help[fn] {
		return ""
	}
	path := fn.Pkg().Path()
	under := func(p string) bool { return path == p || strings.HasPrefix(path, p+"/") }
	if !slices.ContainsFunc(w.opts.CalleePackages, under) || slices.ContainsFunc(w.opts.Display, func(d string) bool {
		return under(d) || d == path+"."+fn.Name()
	}) {
		return ""
	}
	if slices.ContainsFunc(call.Args, w.isStderr) {
		return fn.Name()
	}
	return ""
}

func (w walker) isStderr(e ast.Expr) bool {
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	v, ok := w.pass.TypesInfo.Uses[sel.Sel].(*types.Var)
	return ok && v.Pkg() != nil && v.Pkg().Path() == "os" && v.Name() == "Stderr"
}
