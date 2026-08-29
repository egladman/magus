package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
)

// TestDispatchSubCoversKnownSubcommands guards the drift that shipped `mcp` as a case
// dispatchSub routed with no matching entry in surface.go's subcommands: `magus mcp`
// worked when typed, but help, did-you-mean, the man pages, and every completion
// script (all derived from subcommands / knownSubcommands) never mentioned it.
// dispatchSub's switch and knownSubcommands are two separate declarations that can
// drift exactly the way the three copies surface.go's own doc comment already
// describes - this closes that gap mechanically instead of relying on someone
// remembering to update both.
//
// It reads dispatchSub's case labels out of main.go's source with go/parser rather
// than exercising the switch at runtime: running it needs a loaded workspace for
// most cases, which is more than a unit test should require to check that two name
// lists agree.
func TestDispatchSubCoversKnownSubcommands(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	mainPath := filepath.Join(filepath.Dir(thisFile), "main.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", mainPath, err)
	}

	var dispatchFn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "dispatchSub" {
			dispatchFn = fn
			break
		}
	}
	if dispatchFn == nil {
		t.Fatalf("%s: no dispatchSub function found - the parse target moved", mainPath)
	}

	var sw *ast.SwitchStmt
	for _, stmt := range dispatchFn.Body.List {
		if s, ok := stmt.(*ast.SwitchStmt); ok {
			sw = s
			break
		}
	}
	if sw == nil {
		t.Fatal("dispatchSub has no top-level switch - the parse is wrong, not the switch")
	}

	var cases []string
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok || cc.List == nil { // nil List is the default clause
			continue
		}
		for _, expr := range cc.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			cases = append(cases, s)
		}
	}
	if len(cases) == 0 {
		t.Fatal("found no case clauses in dispatchSub's switch - the parse is wrong, not the switch")
	}

	// help and version are routed in runCLI before dispatchSub is ever called (see
	// runCLI's own switch on res.sub), so they belong in knownSubcommands and are
	// deliberately absent from dispatchSub's case set - not a gap to flag.
	got := append(cases, "help", "version")
	slices.Sort(got)

	want := slices.Clone(knownSubcommands)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("dispatchSub's routed cases (+ help, version) = %v\nknownSubcommands (from surface.go) = %v\n"+
			"a case dispatchSub routes must have an entry in surface.go's subcommands, and vice versa", got, want)
	}
}
