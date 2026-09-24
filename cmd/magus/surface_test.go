package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
)

// TestCLICommandHeadsAreRealSubcommands guards against the drift that shipped a
// hint for a command that no longer exists. Every canonical command referenced
// from user-facing output (hint.AllCommands) must have a head token that dispatchSub
// actually routes: knownSubcommands is that switch's own accept-list. Rename or
// remove a subcommand and forget to update hint's command registry, and this fails.
func TestCLICommandHeadsAreRealSubcommands(t *testing.T) {
	for _, c := range hint.AllCommands {
		if !slices.Contains(knownSubcommands, c.Head()) {
			t.Errorf("hint command %q references head subcommand %q, which is not in knownSubcommands %v",
				c, c.Head(), knownSubcommands)
		}
	}
}

// TestCLICommandPathsResolve checks the whole path, not just the head token the test
// above walks. That gap shipped three remedies naming command chains the dispatcher
// rejects: `magus config mcp token print` (the operator token moved to `config token`),
// `magus notes new <name>`, and a bare `magus ci`. Each had a real head, so nothing failed.
//
// Resolution walks internal/cli's tree, the same hand-maintained mirror
// TestManpageCoversEverySubcommand pins to knownSubcommands at the top level. A mirror is
// the strongest target available: most families dispatch on a bare switch with no
// accept-list to read, so an assertion against the router itself needs those switches
// restructured first. It is only worth as much as the mirror is complete, which is what
// the two accept-list tests below enforce for the families that have one.
func TestCLICommandPathsResolve(t *testing.T) {
	for _, c := range hint.AllCommands {
		tokens := strings.Fields(strings.TrimPrefix(c.String(), "magus "))
		if err := resolveCommandPath(tokens); err != nil {
			t.Errorf("hint command %q: %v", c, err)
		}
	}
}

// resolveCommandPath reports whether tokens name a routable subcommand chain.
func resolveCommandPath(tokens []string) error {
	children := cli.All
	for i, tok := range tokens {
		idx := slices.IndexFunc(children, func(c cli.Command) bool { return c.Name == tok })
		if idx < 0 {
			parent := "magus " + strings.Join(tokens[:i], " ")
			return fmt.Errorf("%q is not a subcommand of %q - fix the hint, or add the entry to internal/cli/registry.go",
				tok, strings.TrimSpace(parent))
		}
		children = children[idx].Children
	}
	return nil
}

// TestCLICommandGraphLeavesAreRealSubcommands ties the graph-family hints to
// graphCmd's own accept-list. `graph` routes its second token positionally, so a
// hint like `magus graph export` is only valid while graphSubs still lists
// "export". This is the strongest guard the hand-rolled dispatch allows: graphSubs
// is the introspectable accept-list, not the switch itself, so it only catches
// drift if graphSubs stays in sync with graphCmd's switch (which hint.Nearest
// already depends on).
func TestCLICommandGraphLeavesAreRealSubcommands(t *testing.T) {
	for _, c := range []hint.Command{hint.GraphExport, hint.GraphStats} {
		if c.Head() != "graph" {
			t.Fatalf("expected a graph-family command, got %q", c)
		}
		if !slices.Contains(graphSubs, c.Leaf()) {
			t.Errorf("hint command %q references graph subcommand %q, which is not in graphSubs %v",
				c, c.Leaf(), graphSubs)
		}
	}
}

// TestCLICommandLsNounsAreDocumented ties the registry's ls children to lsNouns,
// lsCmd's own accept-list. `magus ls targets` shipped routable but undocumented, so
// TestCLICommandPathsResolve had to carry a carve-out for it; comparing the two lists
// is what keeps the mirror from falling behind the router again.
func TestCLICommandLsNounsAreDocumented(t *testing.T) {
	documented := slices.Sorted(slices.Values(childNames(t, "ls")))
	routed := slices.Sorted(slices.Values(lsNouns))
	if !slices.Equal(documented, routed) {
		t.Errorf("internal/cli documents ls nouns %v, lsCmd routes %v", documented, routed)
	}
}

// TestCLIDescribeNounsAreAccepted holds the same line for describe, in the direction
// that can mislead a reader: a documented noun describeAlias does not accept is a man
// page for a command that exits 2. The reverse is deliberately not asserted: the alias
// map carries both spellings of every noun, and documenting each twice would say the
// same thing on two rows.
func TestCLIDescribeNounsAreAccepted(t *testing.T) {
	for _, name := range childNames(t, "describe") {
		if describeAlias[name] == "" {
			t.Errorf("internal/cli documents `magus describe %s`, which describeAlias does not accept", name)
		}
	}
}

func childNames(t *testing.T, command string) []string {
	t.Helper()
	idx := slices.IndexFunc(cli.All, func(c cli.Command) bool { return c.Name == command })
	if idx < 0 {
		t.Fatalf("no cli.All entry for %q", command)
	}
	names := make([]string, 0, len(cli.All[idx].Children))
	for _, child := range cli.All[idx].Children {
		names = append(names, child.Name)
	}
	return names
}

// TestCLICommandServerLeavesAreRealSubcommands ties the server-family hints to the
// tokens serverCmd routes on. serverCmd switches directly on hint.*.Leaf(), so
// the accepted form and the hint already share one source of truth; this asserts
// they remain the exact set start/stop/job/reload, catching a stray edit that renames
// one side only.
func TestCLICommandServerLeavesAreRealSubcommands(t *testing.T) {
	got := []string{hint.ServerStart.Leaf(), hint.ServerStop.Leaf(), hint.ServerStatus.Leaf(), hint.ServerReload.Leaf()}
	want := []string{"start", "stop", "status", "reload"}
	if !slices.Equal(got, want) {
		t.Errorf("server leaves = %v, want %v", got, want)
	}
	for _, c := range []hint.Command{hint.ServerStart, hint.ServerStop, hint.ServerStatus, hint.ServerReload} {
		if c.Head() != "server" {
			t.Errorf("hint command %q is not a server-family command", c)
		}
	}
}

// TestCLICommandQueryOutputForm locks the query-output hint to the form queryCmd
// accepts. queryCmd matches its output positional against hint.QueryOutput.Leaf(),
// so the hint (`magus query output <ref>`) and the accepted form cannot disagree:
// the exact bug that shipped `magus query <ref>`. This asserts the shape stays
// two-token (a bare `magus query` would reopen that gap) and renders as expected.
//
// Limit: dispatch is hand-rolled positional matching, not an introspectable
// command tree, so this cannot execute the router without a workspace. It guards
// the constant's shape and its rendering; the tie to acceptance is structural
// (queryCmd reads the same Leaf()), enforced at compile/review time rather than here.
func TestCLICommandQueryOutputForm(t *testing.T) {
	if hint.QueryOutput.Head() != "query" {
		t.Errorf("QueryOutput head = %q, want %q", hint.QueryOutput.Head(), "query")
	}
	if hint.QueryOutput.Leaf() != "output" {
		t.Errorf("QueryOutput leaf = %q, want %q", hint.QueryOutput.Leaf(), "output")
	}
	if got, want := hint.QueryOutput.With("out1a2b3c"), "magus query output out1a2b3c"; got != want {
		t.Errorf("QueryOutput.With(ref) = %q, want %q", got, want)
	}
	if got, want := hint.QueryOutput.With("out1a2b3c", "--open"), "magus query output out1a2b3c --open"; got != want {
		t.Errorf("QueryOutput.With(ref, --open) = %q, want %q", got, want)
	}
}

// TestManpageCoversEverySubcommand guards the drift that shipped `magus man` with pages for
// 21 of 30 subcommands, vcs among them. internal/cli/registry.go is a hand-maintained
// mirror of surface.go, and api_test.go cannot catch divergence: it locks api.lock against
// API(), which is generated from the same registry.
func TestManpageCoversEverySubcommand(t *testing.T) {
	// `magus help` prints what `magus` prints; man(1) has no page for it.
	const notDocumented = "help"

	documented := make(map[string]bool, len(cli.All))
	for _, c := range cli.All {
		documented[c.Name] = true
	}

	for _, s := range subcommands {
		if s.Name == notDocumented {
			continue
		}
		if !documented[s.Name] {
			t.Errorf("subcommand %q has no cli.All entry, so `magus man` installs no page for it "+
				"and the docs site renders none - add one to internal/cli/registry.go", s.Name)
		}
	}

	// A page for a command the dispatcher no longer routes is worse than a missing one:
	// manpage/magus-churn.1 is a committed artifact of exactly that.
	for _, c := range cli.All {
		if !slices.Contains(knownSubcommands, c.Name) {
			t.Errorf("cli.All documents %q, which knownSubcommands does not route - "+
				"remove the entry, or restore the subcommand", c.Name)
		}
	}
}

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
// children (old names kept alive only to print "this moved"), which would
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
		{"queue", []string{"queue"}, "queue.go", "runQueue", []string{"-h", "--help", "help"}},
		{"spell", []string{"spell"}, "spell.go", "spellCmd", nil},
		{"agent", []string{"agent"}, "agent.go", "agentCmd", nil},
		{"memory", []string{"memory"}, "memory.go", "memoryCmd", []string{
			"list", // renamed to ls in v0.4.0
		}},
		{"session", []string{"session"}, "session.go", "sessionCmd", []string{
			"hook", // moved: hard-redirects to `magus shell`, which is not session-scoped
		}},
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
// serverCmd's switch compares against hint.ServerStart.Leaf() and friends
// rather than string literals (see server.go), so dispatcherCases's literal
// extraction finds nothing there. hint.AllCommands already carries the same four
// leaves for the hint-drift test in surface_test.go; this asserts they also
// match the registry's declared server Children, which is the check that
// would have caught server missing reload and job (item 1 of the 2026-08
// doctrine audit).
func TestServerDispatcherChildrenAreDeclared(t *testing.T) {
	got := []string{
		hint.ServerStart.Leaf(),
		hint.ServerStop.Leaf(),
		hint.ServerStatus.Leaf(),
		hint.ServerReload.Leaf(),
	}
	slices.Sort(got)

	want := registryChildren(t, "server")
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("serverCmd routes %v (via hint)\nregistry Children for \"server\" = %v", got, want)
	}
}

// walkRegistryCommands visits every node in the recursive registry tree
// (internal/cli.All plus every Children entry, at any depth), calling visit
// with the dotted path of command words leading to it.
func walkRegistryCommands(cmds []cli.Command, prefix []string, visit func(path []string, c cli.Command)) {
	for _, c := range cmds {
		path := append(append([]string{}, prefix...), c.Name)
		visit(path, c)
		if len(c.Children) > 0 {
			walkRegistryCommands(c.Children, path, visit)
		}
	}
}

// resolveRegistryPath walks args down the registry tree from the top,
// matching each leading command-word token against a Children name, and
// returns every command reached along the way (root first) plus whether a
// top-level command was found at all: false means args names something
// this registry does not know, which is worth failing on rather than
// silently skipping.
//
// The full path, not just the deepest command, matters: a navigational child
// (query's "output" and "invocation", for one) can declare no Flags of its
// own while the real dispatcher parses the whole family's flags in one
// flag.FlagSet at the PARENT (queryCmd calls cmdParse once, then matches
// "output"/"invocation" positionally against the already-parsed result), so
// a flag documented as "output <ref>: ..." lives on queryCommand.Flags, not
// on the "output" child. Binding every node on the path, root to leaf, is
// the one strategy that is correct for both shapes without hardcoding which
// dispatcher scopes flags where.
//
// A GLOBAL flag may precede the command word ("magus --daemon-address <addr>
// server start" is a documented example), so globalFS (already bound with
// the config and display flags, before any node's own) is consulted to skip
// over those (and their value, if they take one) while searching for the
// next command word, the same interspersed-flag tolerance reorderFlagsFirst
// gives the real CLI. A token that is neither a known global flag nor a
// matching child name stops the walk; per-command flags are never expected
// to precede their own subcommand in these examples.
func resolveRegistryPath(globalFS *flag.FlagSet, args []string) (path []cli.Command, matched bool) {
	cmds := cli.All
	for i := 0; i < len(args); {
		tok := args[i]
		if strings.HasPrefix(tok, "-") {
			name := strings.TrimLeft(tok, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			f := globalFS.Lookup(name)
			if f == nil {
				break
			}
			i++
			if !flagIsBool(f) && !strings.Contains(tok, "=") && i < len(args) {
				i++ // value-taking flag consumes the next token too
			}
			continue
		}
		found := false
		for _, c := range cmds {
			if c.Name == tok {
				path = append(path, c)
				cmds = c.Children
				matched = true
				found = true
				i++
				break
			}
		}
		if !found {
			break
		}
	}
	return path, matched
}

// bindUnlessRegistered copies node's own flags onto fs, skipping any name fs
// already has. A flag documented on the registry can legitimately repeat a
// GLOBAL flag's name (e.g. "config cache prune --dry-run" repeats the
// gen.BindFlags-bound --dry-run purely so the man page's per-command Options
// section shows it in context); flag.FlagSet panics on a duplicate Var call,
// so this is the one safe way to layer several nodes' declared flags onto one
// FlagSet without assuming which level actually owns a given name.
func bindUnlessRegistered(fs *flag.FlagSet, node cli.Command) {
	if !node.HasFlags() {
		return
	}
	tmp := flag.NewFlagSet("tmp", flag.ContinueOnError)
	node.BindFlags(tmp)
	tmp.VisitAll(func(f *flag.Flag) {
		if fs.Lookup(f.Name) == nil {
			fs.Var(f.Value, f.Name, f.Usage)
		}
	})
}

// TestRegistryExamplesParse dry-parses every EXAMPLES entry in the CLI
// registry (internal/cli/registry.go and every command's Children) against
// the flag set its own resolved command actually binds: the same three
// binders cmdParse composes (config flags, display flags, the command's own
// declared Flags), so a bogus or misspelled flag in a documented example is
// a build-time failure instead of something a reader discovers by pasting it.
//
// It reuses guard.ParseCommands (internal/guard/parse.go) for the shell parsing:
// the same mvdan.cc/sh AST walk the write-guard already trusts to find every
// command a shell line would actually run, including inside command
// substitutions ($(...)) and pipelines, and to correctly EXCLUDE redirects
// (many examples end in "> file.json", which is not an argument to magus).
//
// This catches a structurally invalid example (undefined flag, wrong value
// type), not a semantically wrong one, like a valid flag holding a value
// magus rejects at runtime (the events --type diagnostic.emitted class of
// bug); that needs running the command, which a registry-only test cannot do
// without a live workspace.
//
// knownBrokenExamples lists example commands that fail this dry-parse for a
// reason already tracked as a bug in the CLI ITSELF, not in the registry
// data. Empty is the healthy state; an entry is a bug with a tracker, never
// a way to keep an example that demonstrates something broken.
var knownBrokenExamples = map[string]bool{}

func TestRegistryExamplesParse(t *testing.T) {
	// bindDisplayFlags binds onto the process-global display vars, so parsing an
	// example like `--tee build.jsonl` leaks global.tee into every later test in
	// the package, and the next config-view test faithfully tees into the cwd.
	t.Cleanup(snapshotGlobals())
	walkRegistryCommands(cli.All, nil, func(path []string, c cli.Command) {
		for _, ex := range c.Examples {
			t.Run(strings.Join(path, "_")+"/"+ex.Comment, func(t *testing.T) {
				if knownBrokenExamples[ex.Command] {
					t.Skipf("known broken in the CLI itself, tracked separately: %q", ex.Command)
				}
				cmds, ok := guard.ParseCommands(ex.Command)
				if !ok {
					t.Fatalf("example %q does not parse as a shell command line", ex.Command)
				}
				checked := 0
				for _, gc := range cmds {
					if gc.Name != "magus" && gc.Name != "mgs" {
						continue // e.g. `dot`, `jq`, `code` on the far side of a pipe
					}
					checked++
					fs := flag.NewFlagSet(strings.Join(path, " "), flag.ContinueOnError)
					fs.SetOutput(io.Discard)
					gen.BindFlags(fs, &config.Config{})
					bindDisplayFlags(fs)

					cmdPath, matched := resolveRegistryPath(fs, gc.Args)
					if !matched {
						t.Errorf("example %q: %q does not resolve to any top-level registry command",
							ex.Command, strings.Join(gc.Args, " "))
						continue
					}
					for _, node := range cmdPath {
						bindUnlessRegistered(fs, node)
					}
					parseArgs := gc.Args
					if cmdPath[0].Name == "query" {
						// Model query's real entrance: queryCmd shields the grammar's
						// negation terms (-kind:op) from flag parsing before cmdParse.
						parseArgs, _ = splitQueryNegations(parseArgs)
					}
					if err := fs.Parse(reorderFlagsFirst(fs, parseArgs)); err != nil {
						t.Errorf("example %q: %q fails to parse under %q's own flag set: %v",
							ex.Command, strings.Join(gc.Args, " "), strings.Join(path, " "), err)
					}
				}
				if checked == 0 {
					t.Skipf("example %q names no magus/mgs command directly (illustrative shell only)", ex.Command)
				}
			})
		}
	})
}
