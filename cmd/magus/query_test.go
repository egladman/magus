package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// captureStderr redirects os.Stderr for the duration of fn and returns what it wrote.
// reportRefLookupError and its suggestion helper write straight to os.Stderr (matching
// every other CLI error path in this package), so a real fd swap is the only way to
// observe them without changing that convention.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	prev := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = prev
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	return buf.String()
}

// TestReportRefLookupError_NoDoubledConsulted guards the RefNotFoundError rendering
// bug: Error() already reads `...; consulted: local cache`, so the wrapper must not
// append a second "(consulted: ...)". m is nil here on purpose - this branch exercises
// the not-exist rendering in isolation, and a nil Magus must skip the suggestion rather
// than panic (the txtar coverage exercises the suggestion with a real workspace).
func TestReportRefLookupError_NoDoubledConsulted(t *testing.T) {
	err := &cache.RefNotFoundError{Ref: "outdeadbeef0000", Stores: []string{"local cache"}}

	out := captureStderr(t, func() {
		got := reportRefLookupError(context.Background(), nil, "outdeadbeef0000", err)
		require.Error(t, got)
	})

	assert.Contains(t, out, `no stored output for ref "outdeadbeef0000"; consulted: local cache`)
	assert.NotContains(t, out, "consulted: local cache (consulted", "consulted: ... must render exactly once")
	assert.Equal(t, 1, strings.Count(out, "consulted:"), "consulted: must appear exactly once: %q", out)
}

// newQueryTestWorkspace opens a real (cache-backed) single-project workspace bound to a
// spell providing target "build" - the matched-ref suggestion needs IdentifyRef, which
// needs a live cache (ComputeTargetKey returns types.ErrNoCache on an Inspect workspace).
func newQueryTestWorkspace(t *testing.T) *magus.Magus {
	t.Helper()
	const spellName = "zzz-query-ref-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), root, magus.WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestReportRefLookupError_MatchedRefSuggestsRunCommand covers the case the txtar
// scripts cannot express: IdentifyRef needs a real cache-backed workspace and a ref
// that is a live target's PREDICTED key, which a static fixture file cannot pin (the
// key is a content hash). Here the ref is computed via ComputeTargetKey exactly like a
// real run would mint it, then looked up before anything ever produced it - the
// not-exist path must invert it back to a `magus run build` suggestion, with the root
// project omitted since the target lives at ".".
func TestReportRefLookupError_MatchedRefSuggestsRunCommand(t *testing.T) {
	m := newQueryTestWorkspace(t)
	ctx := context.Background()

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	ref := cache.PortableRef(key)

	_, _, lookupErr := m.OutputByRef(ref)
	require.Error(t, lookupErr, "ref must not already be stored")

	out := captureStderr(t, func() {
		got := reportRefLookupError(ctx, m, ref, lookupErr)
		require.Error(t, got)
	})

	assert.Contains(t, out, "Nothing has produced it here, but this workspace would print it for:")
	assert.Contains(t, out, "magus run build\n", "root project (\".\") must be omitted from the suggested command")
	assert.NotContains(t, out, "magus run build .", "root project must not be spelled out as \".\"")
}
