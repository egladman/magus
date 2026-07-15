package spell

import (
	"testing"

	"github.com/egladman/magus/types"

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

	charm := func(t *testing.T, spell, op, ch string) []types.PatchOp {
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
		want             []types.PatchOp
	}{
		// go
		{"go", "go-fmt", "rw", []types.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}},
		{"go", "golangci-lint", "debug", []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		{"go", "golangci-lint", "rw", []types.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
		{"go", "go-test", "cd", []types.PatchOp{
			{Op: "add", Path: "/-", Value: "-covermode=atomic"},
			{Op: "add", Path: "/-", Value: "-coverprofile=coverage.out"},
		}},
		{"go", "go-mod-tidy", "rw", []types.PatchOp{{Op: "remove", Path: "/2"}}},
		// py
		{"py", "pytest", "debug", []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}},
		{"py", "ruff-check", "rw", []types.PatchOp{{Op: "add", Path: "/3", Value: "--fix"}}},
		{"py", "ruff-check", "gha", []types.PatchOp{{Op: "add", Path: "/3", Value: "--output-format=github"}}},
		{"py", "ruff-format", "rw", []types.PatchOp{{Op: "remove", Path: "/3"}}},
		// ts
		{"ts", "prettier", "rw", []types.PatchOp{{Op: "replace", Path: "/2", Value: "--write"}}},
		{"ts", "vitest", "gha", []types.PatchOp{{Op: "add", Path: "/-", Value: "--reporter=github-actions"}}},
		{"ts", "eslint", "rw", []types.PatchOp{{Op: "add", Path: "/2", Value: "--fix"}}},
		{"ts", "eslint", "gha", []types.PatchOp{{Op: "add", Path: "/2", Value: "--format=unix"}}},
		{"ts", "biome-check", "rw", []types.PatchOp{{Op: "add", Path: "/3", Value: "--write"}}},
		{"ts", "biome-check", "gha", []types.PatchOp{{Op: "add", Path: "/3", Value: "--reporter=github"}}},
		{"ts", "biome-format", "rw", []types.PatchOp{{Op: "add", Path: "/3", Value: "--write"}}},
		// md
		{"md", "prettier", "rw", []types.PatchOp{{Op: "replace", Path: "/0", Value: "--write"}}},
		// buf
		{"buf", "buf-lint", "gha", []types.PatchOp{{Op: "add", Path: "/-", Value: "--error-format=github-actions"}}},
		{"buf", "buf-format", "rw", []types.PatchOp{{Op: "replace", Path: "/1", Value: "-w"}}},
		// rs — compound charm (two drops): the constructor concat must still yield
		// remove /2 then remove /1, in that order.
		{"rs", "cargo-fmt", "rw", []types.PatchOp{{Op: "remove", Path: "/2"}, {Op: "remove", Path: "/1"}}},
	}

	for _, c := range cases {
		t.Run(c.spell+"/"+c.op+"/"+c.charm, func(t *testing.T) {
			assert.Equal(t, c.want, charm(t, c.spell, c.op, c.charm))
		})
	}
}

// TestTSRequiredGlobsSupersetOfClaimed guards D1/D2: mgs_listRequiredGlobs must
// cover every module-variant extension and lockfile format mgs_listClaimedGlobs
// claims, so editing a .mts/.cts/.mjs/.cjs file or bumping a yarn/bun lockfile
// marks the project affected instead of silently missing it.
func TestTSRequiredGlobsSupersetOfClaimed(t *testing.T) {
	ts, ok := Builtins()["ts"]
	require.True(t, ok, "ts spell missing")

	for _, want := range []string{
		"**/*.mts", "**/*.cts", "**/*.mjs", "**/*.cjs", "yarn.lock", "bun.lockb", "tsconfig*.json",
	} {
		assert.Containsf(t, ts.Needs, want, "ts required globs missing %q", want)
	}
}
