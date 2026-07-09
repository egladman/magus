package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPartitionFlags locks the flag/positional split that both reorderFlagsFirst and
// splitTargetFromArgs depend on. A regression here silently breaks flag placement, so
// every rule (bool flag, value flag, -flag=value, "--" passthrough, unknown flag, and a
// value flag with no trailing value) is pinned.
func TestPartitionFlags(t *testing.T) {
	// A self-contained FlagSet so the cases don't drift with the real global flags:
	// "b" is boolean (no value), "val" takes the next token.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Bool("b", false, "")
		fs.String("val", "", "")
		return fs
	}

	cases := []struct {
		name    string
		args    []string
		flags   []string
		posargs []string
	}{
		{"positional only", []string{"x"}, []string{}, []string{"x"}},
		{"bool flag before positional", []string{"-b", "x"}, []string{"-b"}, []string{"x"}},
		{"bool flag after positional", []string{"x", "-b"}, []string{"-b"}, []string{"x"}},
		{"value flag keeps its value (before)", []string{"-val", "v", "x"}, []string{"-val", "v"}, []string{"x"}},
		{"value flag keeps its value (after)", []string{"x", "-val", "v"}, []string{"-val", "v"}, []string{"x"}},
		{"value is not mistaken for a positional", []string{"-val", "x", "y"}, []string{"-val", "x"}, []string{"y"}},
		{"flag=value is self-contained", []string{"-val=v", "x"}, []string{"-val=v"}, []string{"x"}},
		{"double dash halts, tail preserved", []string{"-b", "x", "--", "-y", "z"}, []string{"-b"}, []string{"x", "--", "-y", "z"}},
		{"unknown flag does not consume next token", []string{"-nope", "x"}, []string{"-nope"}, []string{"x"}},
		{"value flag at end with no value", []string{"x", "-val"}, []string{"-val"}, []string{"x"}},
		{"double-dash long forms too", []string{"--b", "--val", "v", "x"}, []string{"--b", "--val", "v"}, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlags, gotPos := partitionFlags(newFS(), c.args)
			assert.Equal(t, c.flags, gotFlags, "flags")
			assert.Equal(t, c.posargs, gotPos, "positionals")
			// reorderFlagsFirst must remain flags-then-positionals over the same split.
			assert.Equal(t, append(append([]string{}, c.flags...), c.posargs...),
				reorderFlagsFirst(newFS(), c.args), "reorderFlagsFirst")
		})
	}
}

// TestSplitTargetFromArgs is the regression guard for the fix: the target must be found
// regardless of where recognized global/display flags sit (the bug was a flag BEFORE the
// target being mistaken for it, e.g. `magus run --dry-run build`). Uses the real global
// flag set (--dry-run bool, -o value) so it tracks the actual bindings.
func TestSplitTargetFromArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		target string
		rest   []string
		ok     bool
	}{
		{"target only", []string{"build"}, "build", []string{}, true},
		{"bool flag BEFORE target (the fix)", []string{"--dry-run", "build"}, "build", []string{"--dry-run"}, true},
		{"bool flag after target", []string{"build", "--dry-run"}, "build", []string{"--dry-run"}, true},
		{"flag before target, project after", []string{"--dry-run", "build", "web"}, "build", []string{"--dry-run", "web"}, true},
		{"value flag before target keeps value", []string{"-o", "json", "build"}, "build", []string{"-o", "json"}, true},
		{"value flag between target and project", []string{"build", "-o", "json", "web"}, "build", []string{"-o", "json", "web"}, true},
		{"multiple flags around target", []string{"--dry-run", "build", "-o", "json"}, "build", []string{"--dry-run", "-o", "json"}, true},
		{"no args", []string{}, "", nil, false},
		{"flag but no target", []string{"--dry-run"}, "", nil, false},
		{"only a passthrough marker", []string{"--", "x"}, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, rest, ok := splitTargetFromArgs(c.args)
			assert.Equal(t, c.ok, ok, "ok")
			assert.Equal(t, c.target, target, "target")
			assert.Equal(t, c.rest, rest, "rest")
		})
	}
}
