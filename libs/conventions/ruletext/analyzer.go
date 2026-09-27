// Package ruletext reports guard rule text written into the guard's CLI half.
//
// Every reason and advisory the guard produces opens with one prefix, so a
// literal carrying it is a rule wherever it sits. A rule in package main works,
// and is invisible to both the rule suite and the replay path that re-grades a
// recorded command, because neither can import it.
package ruletext

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "guard rule text %s in a file that owns flags, stdin and rendering only: move the rule " +
	"into the guard's own package, where its rule suite and its replay path can both reach it"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Files] is relative to.
	Module string `json:"module"`

	// Files may not carry rule text.
	Files source.Globs `json:"files"`

	// Prefix opens every reason and advisory the guard produces.
	Prefix string `json:"prefix"`

	// Hint is appended to every diagnostic: the repository's own remedy, which
	// may name the guard's package and the commands that replay it.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring on a malformed glob or
// an empty prefix, which would report every string.
func New(opts Options) (*analysis.Analyzer, error) {
	if err := opts.Files.Validate("ruletext"); err != nil {
		return nil, err
	}
	if opts.Prefix == "" {
		return nil, errors.New("ruletext: prefix is empty")
	}
	if err := source.InModule("ruletext", opts.Module, func(root string) error {
		return opts.Files.RequireMatches("ruletext", "files", root)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "ruletext",
		Doc:  "report guard rule text outside the guard package",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		if rel, ok := source.Rel(pass, opts.Module, f); !ok || !opts.Files.Match(rel) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(v, opts.Prefix) {
				pass.Reportf(lit.Pos(), "%s", source.Hint(fmt.Sprintf(message, lit.Value), opts.Hint))
			}
			return true
		})
	}
	return nil
}
