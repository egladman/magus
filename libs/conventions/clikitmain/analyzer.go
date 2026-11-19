// Package clikitmain reports a main package that parses its command line by
// hand: a flag defined or parsed through the standard flag package, or a read
// of os.Args. A program declares its command as data instead, so every
// command's flags, help, version and exit statuses behave alike, and the
// linted module's hint names the library that runs the declaration.
//
// The mains that predate the rule sit in the linted module's analyzers.yaml
// under its clikitmain key, as a count per file, and a count may only shrink:
// a file past its count reports every finding, and a file under it reports the
// entry stale until the count matches. An exit excuses a file for good, with a
// reason. A //nolint naming the linter is itself reported.
package clikitmain

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const name = "clikitmain"

// policyFile is the policy file's name at the root of the linted module.
const policyFile = "analyzers.yaml"

const message = "%s parses the command line by hand: declare the command as data and run it through the command library"

const overMessage = "%s holds %d hand-parsed command-line reads, past the %d the baseline allows: " +
	"declare the command as data and run it through the command library"

const staleMessage = "the baseline allows %s %d hand-parsed command-line reads and it holds %d: " +
	"lower %s.baseline in %s to %d so the list only shrinks"

const goneMessage = "the baseline lists %s, which no longer parses the command line by hand: " +
	"delete it from %s.baseline in %s so the list only shrinks"

const nolintMessage = "a //nolint cannot exempt " + name + ": declare the command as data"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the module the analyzer lints. Its root holds the policy file
	// and the policy's paths are relative to it. A plugin run fills it from the
	// linted tree's go.mod; an empty Module reads the policy file from the
	// working directory and checks no path against the tree.
	Module string `json:"module"`
}

type exit struct {
	Path   string `yaml:"path"`
	Reason string `yaml:"reason"`
}

// policy is the clikitmain key of the module's analyzers.yaml:
//
//	clikitmain:
//	  settings:
//	    hint: <appended to every diagnostic>
//	  baseline:
//	    <file>: <count>
//	  exits:
//	    - {path: <file>, reason: <why>}
type policy struct {
	Settings struct {
		Hint string `yaml:"hint"`
	} `yaml:"settings"`
	Baseline map[string]int `yaml:"baseline"`
	Exits    []exit         `yaml:"exits"`
}

type loaded struct {
	module   string
	hint     string
	baseline map[string]int
	exits    map[string]bool
}

// New returns the analyzer configured by opts.
//
// A module with no analyzers.yaml, or one with no clikitmain key, excuses
// nothing. New errors on an unreadable or malformed file, an unknown field, an
// entry naming a file that does not exist, a count under one, a file both in
// the baseline and the exits, or an exit with no reason.
func New(opts Options) (*analysis.Analyzer, error) {
	l, err := load(opts)
	if err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: name,
		Doc:  "report a main package that parses its command line by hand",
		Run: func(pass *analysis.Pass) (any, error) {
			run(pass, l)
			return nil, nil
		},
	}, nil
}

func load(opts Options) (loaded, error) {
	root := ""
	dir := "."
	if opts.Module != "" {
		var err error
		if root, err = source.Root(name, opts.Module); err != nil {
			return loaded{}, err
		}
		dir = root
	}
	var p policy
	if err := source.PolicyKey(filepath.Join(dir, policyFile), name, &p); err != nil {
		return loaded{}, fmt.Errorf("%s: policy %s: %w", name, policyFile, err)
	}
	l := loaded{module: opts.Module, hint: p.Settings.Hint, baseline: map[string]int{}, exits: map[string]bool{}}
	var errs []error
	for file, n := range p.Baseline {
		if n < 1 {
			errs = append(errs, fmt.Errorf("%s: policy %s: %s.baseline gives %s %d: a file with no hand-parsed reads leaves the baseline", name, policyFile, name, file, n))
		}
		errs = append(errs, exists(root, file, "baseline"))
		l.baseline[file] = n
	}
	for _, x := range p.Exits {
		switch {
		case strings.TrimSpace(x.Reason) == "":
			errs = append(errs, fmt.Errorf("%s: policy %s: %s.exits names %s with no reason: an exit says why", name, policyFile, name, x.Path))
		case l.exits[x.Path]:
			errs = append(errs, fmt.Errorf("%s: policy %s lists %s twice in %s.exits", name, policyFile, x.Path, name))
		}
		if _, ok := l.baseline[x.Path]; ok {
			errs = append(errs, fmt.Errorf("%s: policy %s lists %s in both %s.baseline and %s.exits", name, policyFile, x.Path, name, name))
		}
		errs = append(errs, exists(root, x.Path, "exits"))
		l.exits[x.Path] = true
	}
	if err := errors.Join(errs...); err != nil {
		return loaded{}, err
	}
	return l, nil
}

// exists checks that file is under root. An empty root, when no module
// locates the files, checks nothing.
func exists(root, file, list string) error {
	if root == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(file))); err != nil {
		return fmt.Errorf("%s: policy %s: %s.%s names %s, which is not a file under %s: it moved, delete or fix the entry",
			name, policyFile, name, list, file, root)
	}
	return nil
}


func run(pass *analysis.Pass, l loaded) {
	if pass.Pkg.Name() != "main" {
		return
	}
	// pass.Files alone: a call is judged by what it resolves to, which a file
	// the build constraints exclude has no types for.
	for _, f := range pass.Files {
		if source.IsTest(pass, f) || ast.IsGenerated(f) {
			continue
		}
		rel, ok := source.Rel(pass, l.module, f)
		if !ok || l.exits[rel] {
			continue
		}
		reportNolint(pass, l.hint, f)
		reads := handParsed(pass, f)
		allowed, listed := l.baseline[rel]
		switch {
		case !listed:
			for _, r := range reads {
				pass.Reportf(r.pos, "%s", source.Hint(fmt.Sprintf(message, r.what), l.hint))
			}
		case len(reads) > allowed:
			for _, r := range reads {
				pass.Reportf(r.pos, "%s", source.Hint(fmt.Sprintf(overMessage, rel, len(reads), allowed), l.hint))
			}
		case len(reads) == 0:
			pass.Reportf(f.Name.Pos(), "%s", source.Hint(fmt.Sprintf(goneMessage, rel, name, policyFile), l.hint))
		case len(reads) < allowed:
			pass.Reportf(f.Name.Pos(), "%s", source.Hint(fmt.Sprintf(staleMessage, rel, allowed, len(reads), name, policyFile, len(reads)), l.hint))
		}
	}
}

// reportNolint reports at the package clause, which a directive on a
// declaration does not cover.
func reportNolint(pass *analysis.Pass, hint string, f *ast.File) {
	for _, group := range f.Comments {
		for _, c := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			rest, ok := strings.CutPrefix(text, "nolint:")
			if !ok {
				continue
			}
			list, _, _ := strings.Cut(rest, " ")
			if slices.Contains(strings.Split(list, ","), name) {
				pass.Reportf(f.Name.Pos(), "%s", source.Hint(nolintMessage, hint))
				return
			}
		}
	}
}

// flagCalls are the standard flag package's functions and *FlagSet methods
// that define or parse a flag.
var flagCalls = []string{
	"Bool", "BoolFunc", "BoolVar", "Duration", "DurationVar", "Float64", "Float64Var", "Func",
	"Int", "Int64", "Int64Var", "IntVar", "Parse", "String", "StringVar", "TextVar",
	"Uint", "Uint64", "Uint64Var", "UintVar", "Var",
}

type read struct {
	what string
	pos  token.Pos
}

// handParsed lists, in source order, each call that defines or parses a flag
// through the standard flag package and each use of os.Args.
func handParsed(pass *analysis.Pass, f *ast.File) []read {
	var reads []read
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch obj := pass.TypesInfo.Uses[sel.Sel].(type) {
		case *types.Func:
			if obj.Pkg() == nil || obj.Pkg().Path() != "flag" || !slices.Contains(flagCalls, obj.Name()) {
				return true
			}
			what := "flag." + obj.Name()
			if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
				what = "(*flag.FlagSet)." + obj.Name()
			}
			reads = append(reads, read{what: what, pos: sel.Pos()})
		case *types.Var:
			if obj.Pkg() != nil && obj.Pkg().Path() == "os" && obj.Name() == "Args" {
				reads = append(reads, read{what: "os.Args", pos: sel.Pos()})
			}
		}
		return true
	})
	return reads
}
