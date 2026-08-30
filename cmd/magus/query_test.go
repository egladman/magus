package main

import (
	"bytes"
	"context"
	"fmt"
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
// showOutputIdentity prints straight to os.Stdout (matching the rest of `magus query`'s
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

// TestSplitQueryNegations pins the shield that makes the grammar's documented
// negation reachable from the CLI: a dash token the query flag set does not know
// is a search term, never a flag error, while real flags, their values, "="
// spellings, and the "--" tail keep today's parse.
func TestSplitQueryNegations(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	cases := []struct {
		name      string
		args      []string
		kept      []string
		negations []string
	}{
		{"field negation", []string{"docker", "-kind:op"}, []string{"docker"}, []string{"-kind:op"}},
		{"bare negation", []string{"cache", "-remote"}, []string{"cache"}, []string{"-remote"}},
		{"registered flag survives", []string{"docker", "-o", "json"}, []string{"-o", "json", "docker"}, nil},
		{"value flag keeps its value", []string{"--url", "-kind:op", "docker"}, []string{"--url", "-kind:op", "docker"}, nil},
		{"equals spelling stays a flag error", []string{"-kind=op"}, []string{"-kind=op"}, nil},
		{"double dash tail untouched", []string{"a", "--", "-kind:op"}, []string{"a", "--", "-kind:op"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, negations := splitQueryNegations(tc.args)
			assert.Equal(t, tc.kept, kept)
			assert.Equal(t, tc.negations, negations)
		})
	}
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
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

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

// TestShowOutputIdentity_RevisionRendering drives showOutputIdentity end to end in a real git
// workspace: a target's descriptor is stamped with the revision HEAD was at when it
// ran (CurrentRevision, resolved once by executeStages), and --identity must render it -
// silently when it still matches HEAD, and with a "recorded at X, you are on Y" line
// once a later commit moves HEAD away from it.
func TestShowOutputIdentity_RevisionRendering(t *testing.T) {
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
		require.NoError(t, showOutputIdentity(ctx, m, ref, OutputOptions{}))
	})
	assert.Contains(t, out, "rev:     "+firstRev[:12], "the descriptor records the revision HEAD was at when the target ran")
	assert.NotContains(t, out, "recorded at", "HEAD has not moved yet, so nothing to call out")

	// Move HEAD to a second commit without re-running the target: the stored
	// descriptor still names firstRev, but the workspace is no longer there.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644))
	runGit(t, dir, "add", "other.txt")
	runGit(t, dir, "commit", "-m", "second")
	secondRev := strings.TrimSpace(string(mustOutput(t, exec.Command("git", "-C", dir, "rev-parse", "HEAD"))))
	require.NotEqual(t, firstRev, secondRev)

	out = captureStdout(t, func() {
		require.NoError(t, showOutputIdentity(ctx, m, ref, OutputOptions{}))
	})
	assert.Contains(t, out,
		"recorded at "+firstRev[:12]+", you are on "+secondRev[:12]+".",
	)
}

// TestPrintIdentifyRefSuggestion_MultipleMatches covers printIdentifyRefSuggestion's
// default case (len(matches) > 1): "any of:" plus every match's command, not just
// one. A ref match is a content-hash prefix, so forcing a real collision takes many
// distinct targets rather than a hand-picked one - this computes the ACTUAL live
// keys for 32 targets and uses whichever first hex digit two of them really land
// on (a hardcoded prefix would be asserting on a guess, not on printIdentifyRefSuggestion's
// behavior). A collision is CERTAIN rather than likely: a first hex digit has 16
// values and this keys 32 targets, so pigeonhole forces at least one pair. Keep the
// count above 16 if you change it - dropping to a "probably enough" number trades a
// guarantee for a flake.
func TestPrintIdentifyRefSuggestion_MultipleMatches(t *testing.T) {
	const spellName = "zzz-query-multi-spell"
	targets := make([]string, 32)
	for i := range targets {
		targets[i] = fmt.Sprintf("build%02d", i)
	}
	s := spells.NewSpell(spellName, spells.WithTargets(targets...))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	m, err := magus.Open(context.Background(), root, magus.WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()

	byFirstDigit := map[byte][]string{}
	fullRef := map[string]string{}
	for _, target := range targets {
		key, _, err := m.ComputeTargetKey(ctx, ".", target, nil)
		require.NoErrorf(t, err, "ComputeTargetKey(%s)", target)
		ref := cache.PortableRef(key)
		fullRef[target] = ref
		digit := ref[len(cache.RefPrefix)]
		byFirstDigit[digit] = append(byFirstDigit[digit], target)
	}
	var collidingTargets []string
	for _, group := range byFirstDigit {
		if len(group) >= 2 {
			collidingTargets = group
			break
		}
	}
	require.GreaterOrEqualf(t, len(collidingTargets), 2,
		"test setup: expected at least one same-first-hex-digit pair among %d targets", len(targets))
	prefix := fullRef[collidingTargets[0]][:len(cache.RefPrefix)+1]

	out := captureStderr(t, func() {
		printIdentifyRefSuggestion(ctx, m, prefix)
	})
	assert.Contains(t, out, "Nothing has produced it here, but this workspace would print it for any of:")
	for _, target := range collidingTargets {
		assert.Contains(t, out, "magus run "+target, "expected the colliding target %q in the suggestion", target)
	}
}

// TestPrintIdentifyRefSuggestion_SkipsOnIdentifyRefError covers the other uncovered
// branch: when m.IdentifyRef itself errors (types.ErrNoCache on an Inspect
// workspace, the one case IdentifyRef propagates rather than swallowing - see its
// doc), printIdentifyRefSuggestion must print nothing at all, not even the
// unconditional "share it with --publish" hint that follows every other path. A
// best-effort suggestion must never partially render around an error it hit.
func TestPrintIdentifyRefSuggestion_SkipsOnIdentifyRefError(t *testing.T) {
	const spellName = "zzz-query-nocache-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spellName))
	ws, err := magus.Inspect(context.Background(), root, magus.WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Inspect")
	m, ok := ws.(*magus.Magus)
	require.True(t, ok, "magus.Inspect returns a *magus.Magus under types.WorkspaceRepository")

	_, identifyErr := m.IdentifyRef(context.Background(), "out123456789012")
	require.Error(t, identifyErr, "test setup: an Inspect (cache-free) workspace must fail to key")

	out := captureStderr(t, func() {
		printIdentifyRefSuggestion(context.Background(), m, "out123456789012")
	})
	assert.Empty(t, out, "an IdentifyRef error must skip the suggestion entirely, including the trailing --publish hint")
}

// mustOutput runs cmd and fails the test on error, surfacing combined output.
func mustOutput(t *testing.T, cmd *exec.Cmd) []byte {
	t.Helper()
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%v: %s", cmd.Args, out)
	return out
}
