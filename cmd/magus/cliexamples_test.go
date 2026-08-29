package main

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/config"
)

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
// top-level command was found at all - false means args names something
// this registry does not know, which is worth failing on rather than
// silently skipping.
//
// The full path, not just the deepest command, matters: a navigational child
// (query's "output" and "invocation", for one) can declare no Flags of its
// own while the real dispatcher parses the whole family's flags in one
// flag.FlagSet at the PARENT (queryCmd calls cmdParse once, then matches
// "output"/"invocation" positionally against the already-parsed result) - so
// a flag documented as "output <ref>: ..." lives on queryCommand.Flags, not
// on the "output" child. Binding every node on the path, root to leaf, is
// the one strategy that is correct for both shapes without hardcoding which
// dispatcher scopes flags where.
//
// A GLOBAL flag may precede the command word ("magus --daemon-address <addr>
// server start" is a documented example), so globalFS - already bound with
// the config and display flags, before any node's own - is consulted to skip
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
// the flag set its own resolved command actually binds - the same three
// binders cmdParse composes (config flags, display flags, the command's own
// declared Flags) - so a bogus or misspelled flag in a documented example is
// a build-time failure instead of something a reader discovers by pasting it.
//
// It reuses parseGuardCommands (guard_shellparse.go) for the shell parsing:
// the same mvdan.cc/sh AST walk the write-guard already trusts to find every
// command a shell line would actually run, including inside command
// substitutions ($(...)) and pipelines, and to correctly EXCLUDE redirects
// (many examples end in "> file.json", which is not an argument to magus).
//
// This catches a structurally invalid example (undefined flag, wrong value
// type) - not a semantically wrong one, like a valid flag holding a value
// magus rejects at runtime (the events --type diagnostic.emitted class of
// bug); that needs running the command, which a registry-only test cannot do
// without a live workspace.
//
// knownBrokenExamples lists example commands that fail this dry-parse for a
// reason already tracked as a bug in the CLI ITSELF, not in the registry
// data: `magus query docker -kind:op`, the registry's own documented
// negation syntax, is rejected by the real binary today with "flag provided
// but not defined: -kind:op" - stdlib flag.Parse treats any bare "-token" as
// an attempted flag, and queryCmd never shields a leading-dash SEARCH TERM
// from that before parsing. Fixing query's term/flag disambiguation is a
// cmd/magus/query.go change outside this audit's file ownership; it is
// tracked separately rather than silently dropped or worked around by
// changing the example to demonstrate something else.
var knownBrokenExamples = map[string]bool{
	"magus query docker -kind:op": true,
}

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
				cmds, ok := parseGuardCommands(ex.Command)
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
					if err := fs.Parse(reorderFlagsFirst(fs, gc.Args)); err != nil {
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
