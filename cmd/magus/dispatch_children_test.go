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

	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/interactive/clihint"
)

// helpAliases are case labels every hand-rolled dispatcher below repeats to
// route its own -h/--help/help handling. They are not children of anything and
// are stripped before comparing a dispatcher's routed set against the registry.
var helpAliases = []string{"-h", "--help", "help"}

// dispatcherCases parses funcName out of file (relative to this test file's
// directory) and returns the string-literal case labels of its first
// top-level switch statement, in source order, with helpAliases removed.
//
// This is TestDispatchSubCoversKnownSubcommands's technique (main.go's
// dispatchSub vs surface.go's knownSubcommands) applied one level down: a
// per-command dispatcher that routes a child with no matching entry in that
// command's registry.Children is invisible to man pages, completions, and
// --help, which is exactly the class of gap item 1 of the 2026-08 doctrine
// audit found (graph build/diff, config token print/revoke/status, config mcp
// connector ls, notes capture/promote, self refresh/registry all reached the
// dispatcher with no registry entry).
//
// Only dispatchers whose switch compares against plain string literals are
// covered here; a few (query, buzz, man) route on an if-chain instead of a
// switch and are asserted separately or left to the man-page drift tests.
func dispatcherCases(t *testing.T, file, funcName string) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(thisFile), file)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == funcName {
			fn = d
			break
		}
	}
	if fn == nil {
		t.Fatalf("%s: no %s function found - the dispatcher moved or was renamed", path, funcName)
	}

	var sw *ast.SwitchStmt
	for _, stmt := range fn.Body.List {
		if s, ok := stmt.(*ast.SwitchStmt); ok {
			sw = s
			break
		}
	}
	if sw == nil {
		t.Fatalf("%s: %s has no top-level switch - the parse is wrong, not the switch (route it through dispatcherCases's callers list instead)", path, funcName)
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
			if err != nil || slices.Contains(helpAliases, s) {
				continue
			}
			cases = append(cases, s)
		}
	}
	return cases
}

// registryChildren walks cli.All down a dotted command path (e.g. "config",
// "mcp", "connector") and returns the names of that command's declared
// Children.
func registryChildren(t *testing.T, path ...string) []string {
	t.Helper()
	cmds := cli.All
	var cur cli.Command
	found := false
	for i, name := range path {
		found = false
		for _, c := range cmds {
			if c.Name == name {
				cur = c
				cmds = c.Children
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("registry has no command %q at path %v", name, path[:i+1])
		}
	}
	if !found {
		t.Fatalf("empty path")
	}
	names := make([]string, 0, len(cur.Children))
	for _, c := range cur.Children {
		names = append(names, c.Name)
	}
	return names
}

// TestDispatcherChildrenAreDeclared is the one-level-down twin of
// TestDispatchSubCoversKnownSubcommands: every child a per-command dispatcher
// switches on must have a matching entry in that command's registry.Children,
// and vice versa. exempt lists case labels that are deliberately NOT registry
// children - old names kept alive only to print "this moved" - which would
// teach the reader nothing new by appearing in Children too.
func TestDispatcherChildrenAreDeclared(t *testing.T) {
	tests := []struct {
		command string   // registry path, for the failure message
		path    []string // dotted command path into cli.All
		file    string
		fn      string
		exempt  []string
	}{
		{"graph", []string{"graph"}, "graph.go", "graphCmd", []string{
			"verify", // moved: hard-redirects to `magus doctor`'s agent-skills check
		}},
		{"config", []string{"config"}, "config.go", "configCmd", nil},
		{"config history", []string{"config", "history"}, "config_history.go", "configHistoryCmd", nil},
		{"config cache", []string{"config", "cache"}, "config_cache.go", "configCacheCmd", nil},
		{"config mcp", []string{"config", "mcp"}, "config_mcp.go", "configMCPCmd", []string{
			"token", // moved: hard-redirects to `magus config token` (not an MCP credential)
		}},
		{"config mcp connector", []string{"config", "mcp", "connector"}, "config_mcp.go", "configMCPConnector", []string{
			"list", // renamed to ls in v0.4.0
		}},
		{"config console", []string{"config", "console"}, "config_console.go", "configConsoleCmd", nil},
		{"config console token", []string{"config", "console", "token"}, "config_console.go", "configConsoleToken", nil},
		{"config token", []string{"config", "token"}, "config_token.go", "configToken", nil},
		{"notes", []string{"notes"}, "notes.go", "notesCmd", []string{
			"put", // deliberately absent: a note is written by a person, not a program
		}},
		{"self", []string{"self"}, "self.go", "selfCmd", nil},
		{"vcs", []string{"vcs"}, "vcs.go", "vcsCmd", nil},
		{"agent", []string{"agent"}, "agent.go", "agentCmd", nil},
		{"memory", []string{"memory"}, "memory.go", "memoryCmd", []string{
			"list", // renamed to ls in v0.4.0
		}},
		{"session", []string{"session"}, "sessions.go", "sessionCmd", nil},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := dispatcherCases(t, tt.file, tt.fn)
			var routed []string
			for _, c := range got {
				if slices.Contains(tt.exempt, c) {
					continue
				}
				routed = append(routed, c)
			}
			slices.Sort(routed)

			want := registryChildren(t, tt.path...)
			slices.Sort(want)

			if !slices.Equal(routed, want) {
				t.Errorf("%s dispatcher (%s.%s) routes %v (after exempting %v)\nregistry Children for %q = %v\n"+
					"a case the dispatcher routes must have an entry in the command's Children, and vice versa",
					tt.command, tt.file, tt.fn, routed, tt.exempt, tt.command, want)
			}
		})
	}
}

// TestServerDispatcherChildrenAreDeclared covers `magus server` separately:
// serverCmd's switch compares against clihint.ServerStart.Leaf() and friends
// rather than string literals (see server.go), so dispatcherCases's literal
// extraction finds nothing there. clihint.All already carries the same four
// leaves for the hint-drift test in surface_test.go; this asserts they also
// match the registry's declared server Children, which is the check that
// would have caught server missing reload and job (item 1 of the 2026-08
// doctrine audit).
func TestServerDispatcherChildrenAreDeclared(t *testing.T) {
	got := []string{
		clihint.ServerStart.Leaf(),
		clihint.ServerStop.Leaf(),
		clihint.ServerJob.Leaf(),
		clihint.ServerReload.Leaf(),
	}
	slices.Sort(got)

	want := registryChildren(t, "server")
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("serverCmd routes %v (via clihint)\nregistry Children for \"server\" = %v", got, want)
	}
}
