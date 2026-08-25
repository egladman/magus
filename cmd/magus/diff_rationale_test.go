package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompatUntil(t *testing.T) {
	cases := []struct{ in, want string }{
		{"compat(until: no store still serves ed25519 envelopes): sigAlg is", "no store still serves ed25519 envelopes"},
		{"compat(until: console reads the new keys)", "console reads the new keys"},
		// A condition that wraps onto the next line is truncated rather than dropped: half
		// of it still says what KIND of thing would retire the code.
		{"compat(until: no install still carries the pre-rename file - the", "no install still carries the pre-rename file - the"},
		{"compat(until: )", ""},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, compatUntil(c.in), c.in)
	}
}

func TestCollectRationale(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	write("a.go", "package a\n\n// compat(until: no store holds v1 descriptors): keep the branch.\nfunc F() {}\n")
	write("b.go", "package b\n\nfunc G() {}\n")
	write("gen/c.go", "package gen\n\n// compat(until: whatever): generated.\n")

	rev := types.Diff{Files: []types.DiffFile{
		{Path: "a.go", Role: types.DiffRoleSource},
		{Path: "b.go", Role: types.DiffRoleSource},
		{Path: "gen/c.go", Role: types.DiffRoleOutput},
	}}

	got := collectRationale(root, rev)
	require.Len(t, got, 1)
	assert.Equal(t, "a.go", got[0].Path)
	assert.Equal(t, 3, got[0].Line)
	assert.Equal(t, "no store holds v1 descriptors", got[0].Until)
}

func TestPreflightRationaleLines(t *testing.T) {
	t.Run("empty says nothing was marked, not nothing was checked", func(t *testing.T) {
		lines := preflightRationaleLines(nil)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "no compat(until:) marker")
	})

	t.Run("names the file, the line, and the condition", func(t *testing.T) {
		lines := preflightRationaleLines([]rationaleHit{{Path: "a.go", Line: 12, Until: "no store holds v1"}})
		assert.Contains(t, lines[0], "1 compat(until:) marker")
		assert.Equal(t, "      a.go:12 until no store holds v1", lines[1])
	})

	// The marker appears as a bare token in this tool's own source, in doc comments naming
	// the convention, and in lint patterns. Matching those would put the reporting code at
	// the top of its own report.
	t.Run("a bare mention of the marker is not a decision", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"),
			[]byte("package a\n\nconst marker = \"compat(until:\"\n// the compat(until:) convention\n"), 0o644))

		got := collectRationale(root, types.Diff{
			Files: []types.DiffFile{{Path: "a.go", Role: types.DiffRoleSource}},
		})
		assert.Empty(t, got)
	})

	t.Run("the list is capped", func(t *testing.T) {
		var hits []rationaleHit
		for i := range rationaleShown + 3 {
			hits = append(hits, rationaleHit{Path: "a.go", Line: i, Until: "x"})
		}
		lines := preflightRationaleLines(hits)
		assert.Contains(t, lines[rationaleShown+1], "and 3 more")
	})
}
