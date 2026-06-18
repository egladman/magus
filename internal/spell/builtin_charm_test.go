package spell

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuiltinCharmsUnchanged pins every bundled spell's charm patches after the
// migration from hand-written positional JSON Pointers to magus/charm
// constructors: the resolved RFC 6902 ops must be byte-identical to the originals,
// so the rewrite is provably behavior-preserving (e.g. after(args,"run",["--fix"])
// still lands --fix at /3; set(args,"--check","--write") still replaces /2).
func TestBuiltinCharmsUnchanged(t *testing.T) {
	specs := Builtins()

	charm := func(t *testing.T, spell, op, ch string) []PatchOp {
		t.Helper()
		sp, ok := specs[spell]
		require.Truef(t, ok, "spell %q missing", spell)
		o, ok := sp.Ops[op]
		require.Truef(t, ok, "%s op %q missing", spell, op)
		c, ok := o.Charms[ch]
		require.Truef(t, ok, "%s op %q charm %q missing", spell, op, ch)
		return c.Ops
	}

	cases := []struct {
		spell, op, charm string
		want             []PatchOp
	}{
		// go
		{"go", "go-fmt", "rw", []PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
		{"go", "golangci-lint", "debug", []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		{"go", "golangci-lint", "rw", []PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
		{"go", "go-test", "cd", []PatchOp{
			{Op: "add", Path: "/-", Value: "-covermode=atomic"},
			{Op: "add", Path: "/-", Value: "-coverprofile=coverage.out"},
		}},
		{"go", "go-mod-tidy", "rw", []PatchOp{{Op: "remove", Path: "/2"}}},
		// py
		{"py", "pytest", "debug", []PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		{"py", "ruff-check", "rw", []PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
		{"py", "ruff-check", "gha", []PatchOp{{Op: "add", Path: "/3", Value: "--output-format=github"}}},
		{"py", "ruff-format", "rw", []PatchOp{{Op: "remove", Path: "/3"}}},
		// ts
		{"ts", "prettier", "rw", []PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
		{"ts", "vitest", "gha", []PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
		// md
		{"md", "prettier", "rw", []PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
		// buf
		{"buf", "buf-lint", "gha", []PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
		{"buf", "buf-format", "rw", []PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}},
	}

	for _, c := range cases {
		t.Run(c.spell+"/"+c.op+"/"+c.charm, func(t *testing.T) {
			assert.Equal(t, c.want, charm(t, c.spell, c.op, c.charm))
		})
	}
}
