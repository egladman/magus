package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what it wrote.
// showOutputMeta prints straight to os.Stdout (matching the rest of `magus query`'s
// text-format rendering), so a real fd swap is the only way to observe it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	prev := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = prev
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	return buf.String()
}

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

// TestShowOutputMeta_RevisionRendering drives showOutputMeta end to end in a real git
// workspace: a target's descriptor is stamped with the revision HEAD was at when it
// ran (CurrentRevision, resolved once by executeStages), and --meta must render it -
// silently when it still matches HEAD, and with a "produced at X, you are on Y" line
// once a later commit moves HEAD away from it.
func TestShowOutputMeta_RevisionRendering(t *testing.T) {
	dir := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte(""), 0o644))
	runGit(t, dir, "add", "magusfile.buzz")
	runGit(t, dir, "commit", "-m", "seed")
	firstRev := strings.TrimSpace(string(mustOutput(t, exec.Command("git", "-C", dir, "rev-parse", "HEAD"))))

	const spellName = "zzz-query-meta-revision-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) { return nil, nil }))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), dir, magus.WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	require.NoError(t, m.Run(ctx, []types.Target{{Path: ".", Name: "build"}}))

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	ref := cache.PortableRef(key)

	out := captureStdout(t, func() {
		require.NoError(t, showOutputMeta(ctx, m, ref, OutputOptions{}))
	})
	assert.Contains(t, out, "rev:     "+firstRev[:12], "the descriptor records the revision HEAD was at when the target ran")
	assert.NotContains(t, out, "produced at", "HEAD has not moved yet, so nothing to call out")

	// Move HEAD to a second commit without re-running the target: the stored
	// descriptor still names firstRev, but the workspace is no longer there.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644))
	runGit(t, dir, "add", "other.txt")
	runGit(t, dir, "commit", "-m", "second")
	secondRev := strings.TrimSpace(string(mustOutput(t, exec.Command("git", "-C", dir, "rev-parse", "HEAD"))))
	require.NotEqual(t, firstRev, secondRev)

	out = captureStdout(t, func() {
		require.NoError(t, showOutputMeta(ctx, m, ref, OutputOptions{}))
	})
	assert.Contains(t, out,
		"produced at "+firstRev[:12]+", you are on "+secondRev[:12]+"; check out that commit first to reproduce it.",
	)
}

// mustOutput runs cmd and fails the test on error, surfacing combined output.
func mustOutput(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%v: %s", cmd.Args, out)
	return out
}
