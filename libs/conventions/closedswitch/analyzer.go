// Package closedswitch reports a switch over a closed set that does not name
// every member, or that keeps a default arm. A closed set is a named type with
// two or more declared constants; naming each member makes a new member fail at
// every switch that forgot it, where a default arm would take a quiet path.
//
// The rule binds every author, and the tree does not meet it everywhere yet, so
// the exhaustive-switch key of the analyzers.yaml at the root of the module that
// holds the file records what is allowed. Its baseline holds, per function, how
// many violating switches it carries today; the count may only fall. Its exits
// list switches a person accepted one by one, each with a reason. There is no
// //nolint exit.
package closedswitch

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

// maxNamed bounds the members a message spells out; an opcode set runs to
// dozens.
const maxNamed = 6

// Section is the exhaustive-switch key of a project's analyzers.yaml. Other
// keys in the file belong to other analyzers and are ignored.
const Section = "exhaustive-switch"

// policyFile is the policy file's name at the root of the module that holds a
// file.
const policyFile = "analyzers.yaml"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module limits the check to types declared inside it, and makes each file
	// answer to the analyzers.yaml of the nearest module at or above it, with
	// paths relative to that module's root. Empty checks every type and reads
	// the one analyzers.yaml in the working directory, with paths taken from the
	// package import path.
	Module string `json:"module"`
}

// Settings is the exhaustive-switch key of a project's analyzers.yaml:
//
//	exhaustive-switch:
//	  baseline:
//	    - {path: <file>, symbol: <function>, count: <switches>}
//	  exits:
//	    - {path: <file>, symbol: <function>, type: <set>, reason: <why>}
type Settings struct {
	// Baseline lists the functions that may still hold violating switches.
	Baseline []Count `yaml:"baseline"`
	// Exits lists the switches a person accepted.
	Exits []Accepted `yaml:"exits"`
}

// Count is one baseline row: the violating switches Symbol in Path may hold.
// Count defaults to 1.
type Count struct {
	Path   string `yaml:"path"`
	Symbol string `yaml:"symbol"`
	Count  int    `yaml:"count"`
}

// Accepted is one exit: the switch over Type in function Symbol of Path.
type Accepted struct {
	Path   string `yaml:"path"`
	Symbol string `yaml:"symbol"`
	Type   string `yaml:"type"`
	Reason string `yaml:"reason"`
}

// New returns the analyzer configured by opts. A module with no analyzers.yaml,
// or one with no exhaustive-switch key, allows nothing. A run errors on an
// unreadable or malformed file, an unknown field, a row naming a file that does
// not exist, or an exit with no reason.
func New(opts Options) (*analysis.Analyzer, error) {
	cache := &policies{byDir: map[string]loaded{}, check: opts.Module != ""}
	return &analysis.Analyzer{
		Name: "closedswitch",
		Doc:  "report a switch over a closed set that misses a member or keeps a default arm",
		Run: func(pass *analysis.Pass) (any, error) {
			return nil, run(pass, opts, cache)
		},
	}, nil
}

// loaded is one project's settings and where they came from.
type loaded struct {
	settings Settings
	display  string
	err      error
}

// policies reads each project's file once; packages run concurrently.
type policies struct {
	mu    sync.Mutex
	byDir map[string]loaded
	// check is whether a row's file must exist under the project: an
	// import-path run has no tree to check it against.
	check bool
}

func (p *policies) at(dir string) loaded {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.byDir[dir]; ok {
		return l
	}
	l := load(dir, p.check)
	p.byDir[dir] = l
	return l
}

func load(dir string, check bool) loaded {
	path := filepath.Join(dir, policyFile)
	l := loaded{display: path}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil {
			l.display = filepath.ToSlash(rel)
		}
	}
	settings, err := decode(path)
	if err != nil {
		l.err = fmt.Errorf("closedswitch: %s: %w", l.display, err)
		return l
	}
	for i := range settings.Baseline {
		if settings.Baseline[i].Count == 0 {
			settings.Baseline[i].Count = 1
		}
	}
	for _, e := range settings.Exits {
		if strings.TrimSpace(e.Reason) == "" {
			l.err = fmt.Errorf("closedswitch: %s: exit %s in %s has no reason: a person writes why the switch stays", l.display, e.Symbol, e.Path)
			return l
		}
	}
	if check {
		rows := make([]string, 0, len(settings.Baseline)+len(settings.Exits))
		for _, c := range settings.Baseline {
			rows = append(rows, c.Path)
		}
		for _, e := range settings.Exits {
			rows = append(rows, e.Path)
		}
		for _, row := range rows {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(row))); err != nil {
				l.err = fmt.Errorf("closedswitch: %s: %s is not a file under %s: it moved, so delete or fix the row", l.display, row, dir)
				return l
			}
		}
	}
	l.settings = settings
	return l
}

// decode reads the exhaustive-switch key strictly; a missing file or key is
// the empty policy.
func decode(path string) (Settings, error) {
	var settings Settings
	err := source.PolicyKey(path, Section, &settings)
	return settings, err
}

// owner returns the directory a file's policy and paths are relative to: the
// nearest directory at or above the file with a go.mod.
func owner(file string) (string, bool) {
	for d := filepath.Dir(file); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, true
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}

// place returns the policy and the slash path of f inside its project. An
// empty Module has no tree to walk: the working directory owns every file and
// the package import path names it.
func place(pass *analysis.Pass, opts Options, cache *policies, f *ast.File) (loaded, string, bool) {
	if opts.Module == "" {
		rel, ok := source.Rel(pass, "", f)
		return cache.at("."), rel, ok
	}
	if _, ok := source.Rel(pass, opts.Module, f); !ok {
		return loaded{}, "", false
	}
	name := source.Name(pass, f)
	dir, ok := owner(name)
	if !ok {
		return loaded{}, "", false
	}
	rel, err := filepath.Rel(dir, name)
	if err != nil {
		return loaded{}, "", false
	}
	return cache.at(dir), filepath.ToSlash(rel), true
}

type violation struct {
	pos     token.Pos
	message string
}

func run(pass *analysis.Pass, opts Options, cache *policies) error {
	for _, f := range pass.Files {
		if source.IsTest(pass, f) {
			continue
		}
		policy, rel, ok := place(pass, opts, cache, f)
		if !ok {
			continue
		}
		if policy.err != nil {
			return policy.err
		}
		settings := policy.settings
		found := map[string][]violation{}
		decls := map[string]token.Pos{}
		usedExit := map[int]bool{}
		for _, decl := range f.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}
			decls[fn.Name.Name] = fn.Pos()
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sw, isSwitch := n.(*ast.SwitchStmt)
				if !isSwitch || sw.Tag == nil {
					return true
				}
				s, ok := closedSet(pass, opts, sw.Tag)
				if !ok {
					return true
				}
				if i := slices.IndexFunc(settings.Exits, func(a Accepted) bool {
					return a.Path == rel && a.Symbol == fn.Name.Name && a.Type == s.name
				}); i >= 0 {
					usedExit[i] = true
					return true
				}
				if msg := judge(pass, sw, s); msg != "" {
					found[fn.Name.Name] = append(found[fn.Name.Name], violation{sw.Pos(), msg})
				}
				return true
			})
		}
		for i, a := range settings.Exits {
			if a.Path == rel && !usedExit[i] {
				pass.Reportf(f.Package, "exit names a switch over %s in %s that this file does not hold: delete the exit from %s",
					a.Type, a.Symbol, policy.display)
			}
		}
		allowed := map[string]int{}
		for _, c := range settings.Baseline {
			if c.Path == rel {
				allowed[c.Symbol] += c.Count
			}
		}
		for symbol, vs := range found {
			if len(vs) > allowed[symbol] {
				for _, v := range vs {
					pass.Reportf(v.pos, "%s; %s holds %d such switches and the %s baseline records %d: fix this one, since the baseline only shrinks",
						v.message, symbol, len(vs), Section, allowed[symbol])
				}
			}
		}
		for symbol, n := range allowed {
			if held := len(found[symbol]); held < n {
				pos, ok := decls[symbol]
				if !ok {
					pos = f.Package
				}
				pass.Reportf(pos, "the %s baseline in %s records %d switches for %s in %s, which holds %d: lower the count to %d, or delete the row at 0; the baseline only shrinks",
					Section, policy.display, n, symbol, rel, held, held)
			}
		}
	}
	return nil
}

type set struct {
	name    string
	members []member
}

// member is one distinct value of a set, named by its first declaration in
// sorted order.
type member struct {
	name  string
	value string
}

func closedSet(pass *analysis.Pass, opts Options, tag ast.Expr) (set, bool) {
	named, isNamed := types.Unalias(pass.TypesInfo.TypeOf(tag)).(*types.Named)
	if !isNamed || named.Obj().Pkg() == nil {
		return set{}, false
	}
	if _, basic := named.Underlying().(*types.Basic); !basic {
		return set{}, false
	}
	pkg := named.Obj().Pkg()
	if opts.Module != "" && pkg.Path() != opts.Module && !strings.HasPrefix(pkg.Path(), opts.Module+"/") {
		return set{}, false
	}
	var members []member
	seen := map[string]bool{}
	for _, n := range pkg.Scope().Names() {
		c, isConst := pkg.Scope().Lookup(n).(*types.Const)
		if !isConst || !types.Identical(c.Type(), named) {
			continue
		}
		v := c.Val().ExactString()
		if !seen[v] {
			seen[v] = true
			members = append(members, member{n, v})
		}
	}
	if len(members) < 2 {
		return set{}, false
	}
	return set{named.Obj().Name(), members}, true
}

// judge returns the finding for sw, or "" when it names every member and has
// no default arm.
func judge(pass *analysis.Pass, sw *ast.SwitchStmt, s set) string {
	named := map[string]bool{}
	hasDefault := false
	for _, stmt := range sw.Body.List {
		clause, isClause := stmt.(*ast.CaseClause)
		if !isClause {
			continue
		}
		if clause.List == nil {
			hasDefault = true
		}
		for _, e := range clause.List {
			if v := pass.TypesInfo.Types[e].Value; v != nil && v.Kind() != constant.Unknown {
				named[v.ExactString()] = true
			}
		}
	}
	var missing []string
	for _, m := range s.members {
		if !named[m.value] {
			missing = append(missing, m.name)
		}
	}
	switch {
	case len(missing) > 0 && hasDefault:
		return fmt.Sprintf("switch over %s misses %s and keeps a default arm: add %s and delete the default arm", s.name, list(missing), cases(missing))
	case len(missing) > 0:
		return fmt.Sprintf("switch over %s misses %s: add %s", s.name, list(missing), cases(missing))
	case hasDefault:
		return fmt.Sprintf("switch over %s names every member and keeps a default arm: delete the default arm, so a new member fails here", s.name)
	}
	return ""
}

func list(names []string) string {
	if len(names) <= maxNamed {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxNamed], ", "), len(names)-maxNamed)
}

func cases(names []string) string {
	if len(names) <= maxNamed {
		return "`case " + strings.Join(names, ", ") + ":`"
	}
	return "a case for each of them"
}
