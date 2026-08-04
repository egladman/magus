package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiffKeyInputsIdentical verifies two equal snapshots produce no lines.
func TestDiffKeyInputsIdentical(t *testing.T) {
	ki := &KeyInputs{
		KeyVersion:      keyVersion,
		SpellDefVersion: "abc123",
		Charms:          []string{"rw"},
		Sources:         []SourceInput{{Path: "a.go", Hash: "h1"}},
		EnvAllow:        []EnvInput{{Name: "CI", Set: true, ValueHash: "fp1"}},
		ToolVersions:    []string{"go:1.25.0"},
	}
	kiCopy := *ki
	got := DiffKeyInputs(ki, &kiCopy)
	assert.Empty(t, got, "identical snapshots must produce no diff lines")
}

// TestDiffKeyInputsKeyVersion verifies a magus-upgrade keyVersion bump is reported.
func TestDiffKeyInputsKeyVersion(t *testing.T) {
	prev := &KeyInputs{KeyVersion: 2}
	cur := &KeyInputs{KeyVersion: 3}
	got := DiffKeyInputs(prev, cur)
	require.Len(t, got, 1)
	assert.Equal(t, "keyVersion: 2 -> 3 (magus upgrade; invalidates every entry)", got[0])
}

// TestDiffKeyInputsSpellDefVersion verifies a binary/spell definition change is reported
// with shortened hashes.
func TestDiffKeyInputsSpellDefVersion(t *testing.T) {
	prev := &KeyInputs{SpellDefVersion: "aaaaaaaaaaaaaaaa"}
	cur := &KeyInputs{SpellDefVersion: "bbbbbbbbbbbbbbbb"}
	got := DiffKeyInputs(prev, cur)
	require.Len(t, got, 1)
	assert.Equal(t, "spellDefVersion: aaaaaaaa -> bbbbbbbb (spell/op definitions changed, likely a magus upgrade)", got[0])
}

// TestDiffKeyInputsCharms verifies added and removed charms are both reported.
func TestDiffKeyInputsCharms(t *testing.T) {
	prev := &KeyInputs{Charms: []string{"rw", "verbose"}}
	cur := &KeyInputs{Charms: []string{"rw", "ci"}}
	got := DiffKeyInputs(prev, cur)
	assert.ElementsMatch(t, []string{"charm added: ci", "charm removed: verbose"}, got)
}

// TestDiffKeyInputsArgs verifies ExtraArgs is compared order-sensitively (unlike
// charms, args are never sorted before hashing - see hashStep).
func TestDiffKeyInputsArgs(t *testing.T) {
	prev := &KeyInputs{ExtraArgs: []string{"-run", "X"}}
	cur := &KeyInputs{ExtraArgs: []string{"X", "-run"}}
	got := DiffKeyInputs(prev, cur)
	require.Len(t, got, 1)
	assert.Equal(t, `args changed: [-run X] -> [X -run]`, got[0])
}

// TestDiffKeyInputsSources verifies added, removed, and content/exec-bit-changed
// source files are each reported by path only, never by hash.
func TestDiffKeyInputsSources(t *testing.T) {
	prev := &KeyInputs{Sources: []SourceInput{
		{Path: "a.go", Hash: "h1"},
		{Path: "b.go", Hash: "h2"},
		{Path: "removed.go", Hash: "h3"},
	}}
	cur := &KeyInputs{Sources: []SourceInput{
		{Path: "a.go", Hash: "h1"},         // unchanged
		{Path: "b.go", Hash: "h2-changed"}, // content changed
		{Path: "added.go", Hash: "h4"},     // new
	}}
	got := DiffKeyInputs(prev, cur)
	assert.ElementsMatch(t, []string{
		"src changed: b.go",
		"src added: added.go",
		"src removed: removed.go",
	}, got)
	for _, line := range got {
		assert.NotContains(t, line, "h1")
		assert.NotContains(t, line, "h2")
		assert.NotContains(t, line, "h3")
		assert.NotContains(t, line, "h4")
	}
}

// TestDiffKeyInputsSourcesExecBit verifies a chmod +x with unchanged content is
// still reported as a source change (hashStep folds the executable bit into
// the "src:" line precisely so this is not silently ignored).
func TestDiffKeyInputsSourcesExecBit(t *testing.T) {
	prev := &KeyInputs{Sources: []SourceInput{{Path: "run.sh", Hash: "h1", Exec: false}}}
	cur := &KeyInputs{Sources: []SourceInput{{Path: "run.sh", Hash: "h1", Exec: true}}}
	got := DiffKeyInputs(prev, cur)
	assert.Equal(t, []string{"src changed: run.sh"}, got)
}

// TestDiffKeyInputsEnv verifies env var additions, removals, and value changes are
// reported by name only - never the fingerprint or the value.
func TestDiffKeyInputsEnv(t *testing.T) {
	prev := &KeyInputs{EnvAllow: []EnvInput{
		{Name: "CI", Set: true, ValueHash: "fp1"},
		{Name: "STALE", Set: true, ValueHash: "fp2"},
	}}
	cur := &KeyInputs{EnvAllow: []EnvInput{
		{Name: "CI", Set: true, ValueHash: "fp1-new"}, // value changed
		{Name: "FRESH", Set: true, ValueHash: "fp3"},  // added
	}}
	got := DiffKeyInputs(prev, cur)
	assert.ElementsMatch(t, []string{
		"env changed: CI",
		"env added: FRESH",
		"env removed: STALE",
	}, got)
}

// TestDiffKeyInputsEnvUnsetToSet verifies a variable flipping from unset to set
// (or back) is reported as changed even when no ValueHash was ever recorded.
func TestDiffKeyInputsEnvUnsetToSet(t *testing.T) {
	prev := &KeyInputs{EnvAllow: []EnvInput{{Name: "FLAG", Set: false}}}
	cur := &KeyInputs{EnvAllow: []EnvInput{{Name: "FLAG", Set: true, ValueHash: "fp1"}}}
	got := DiffKeyInputs(prev, cur)
	assert.Equal(t, []string{"env changed: FLAG"}, got)
}

// TestDiffKeyInputsExecOverrides verifies added/removed exec overrides are reported
// verbatim (their values come from the magusfile, not process env, so they carry
// no secret-exposure risk).
func TestDiffKeyInputsExecOverrides(t *testing.T) {
	prev := &KeyInputs{ExecOverrides: []string{"cwd:old"}}
	cur := &KeyInputs{ExecOverrides: []string{"cwd:new"}}
	got := DiffKeyInputs(prev, cur)
	assert.ElementsMatch(t, []string{
		"exec override added: cwd:new",
		"exec override removed: cwd:old",
	}, got)
}

// TestDiffKeyInputsDepsByProject verifies dependency changes are attributed to the
// specific upstream project path when DepsByProject was captured.
func TestDiffKeyInputsDepsByProject(t *testing.T) {
	prev := &KeyInputs{DepsByProject: map[string]string{
		"libs/a": "hash-a-old",
		"libs/b": "hash-b",
	}}
	cur := &KeyInputs{DepsByProject: map[string]string{
		"libs/a": "hash-a-new",
		"libs/c": "hash-c",
	}}
	got := DiffKeyInputs(prev, cur)
	assert.ElementsMatch(t, []string{
		"dep changed: libs/a",
		"dep added: libs/c",
		"dep removed: libs/b",
	}, got)
}

// TestDiffKeyInputsDepsFallback verifies that without DepsByProject attribution
// (an old manifest, or a target with no DependsOn), a coarse count is still
// reported rather than silently dropping a real dependency change.
func TestDiffKeyInputsDepsFallback(t *testing.T) {
	prev := &KeyInputs{Deps: []string{"h1"}}
	cur := &KeyInputs{Deps: []string{"h1", "h2"}}
	got := DiffKeyInputs(prev, cur)
	require.Len(t, got, 1)
	assert.Equal(t, "deps changed: 1 -> 2 entries (no per-project attribution captured)", got[0])
}

// TestDiffKeyInputsToolVersions verifies tool version bumps are reported per
// spell/tool key with old -> new versions, and distinguishes the 2-part
// ("spell:version") from the 3-part ("spell:tool:version") Step.ToolVersions shape.
func TestDiffKeyInputsToolVersions(t *testing.T) {
	prev := &KeyInputs{ToolVersions: []string{"go:1.25.0", "docker:buildx:0.11.0"}}
	cur := &KeyInputs{ToolVersions: []string{"go:1.25.5", "docker:buildx:0.11.0", "node:20.0.0"}}
	got := DiffKeyInputs(prev, cur)
	assert.ElementsMatch(t, []string{
		"tool version changed: go: 1.25.0 -> 1.25.5",
		"tool added: node (20.0.0)",
	}, got)
}

// TestDiffKeyInputsNoObservedDifference documents the honest failure mode: when
// every captured component matches, DiffKeyInputs reports nothing - the miss is
// real but not explained by anything this version of magus records.
func TestDiffKeyInputsNoObservedDifference(t *testing.T) {
	ki := &KeyInputs{KeyVersion: keyVersion, Sources: []SourceInput{{Path: "a.go", Hash: "h1"}}}
	got := DiffKeyInputs(ki, ki)
	assert.Empty(t, got)
}

// TestComputeKeyInputsMatchesHashStepSources verifies ComputeKeyInputs walks and
// hashes the same source set hashStep would hash for the same Step, so the two
// can never disagree about what changed.
func TestComputeKeyInputsMatchesHashStepSources(t *testing.T) {
	root := t.TempDir()
	writeMain(t, root, "package main")
	c := newBareCache(t)
	c.mtimes.load(t.Context())

	step := makeStep(root)
	ki, err := c.ComputeKeyInputs(context.Background(), &step)
	require.NoError(t, err)
	require.Len(t, ki.Sources, 1)
	assert.Equal(t, "test/pkg/main.go", ki.Sources[0].Path)

	wantHash, _, err := c.hashFiles(context.Background(), []relAbs{{
		rel: "test/pkg/main.go",
		abs: filepath.Join(root, "test", "pkg", "main.go"),
	}})
	require.NoError(t, err)
	assert.Equal(t, wantHash[0], ki.Sources[0].Hash)
	assert.Equal(t, keyVersion, ki.KeyVersion)
}

// TestComputeKeyInputsEnvValueNeverStored verifies that a set env var contributes
// a fingerprint, never its literal value, to the captured KeyInputs - a manifest
// must not become a new place secrets come to rest on disk.
func TestComputeKeyInputsEnvValueNeverStored(t *testing.T) {
	root := t.TempDir()
	c := newBareCache(t)
	c.mtimes.load(t.Context())

	const secretValue = "sk-super-secret-token-value"
	t.Setenv("MAGUS_TEST_SECRET_ENV", secretValue)

	step := Step{
		ProjectPath:   "test/pkg",
		WorkspaceRoot: root,
		EnvAllow:      []string{"MAGUS_TEST_SECRET_ENV"},
	}
	ki, err := c.ComputeKeyInputs(context.Background(), &step)
	require.NoError(t, err)
	require.Len(t, ki.EnvAllow, 1)
	assert.Equal(t, "MAGUS_TEST_SECRET_ENV", ki.EnvAllow[0].Name)
	assert.True(t, ki.EnvAllow[0].Set)
	assert.NotEmpty(t, ki.EnvAllow[0].ValueHash)
	assert.NotEqual(t, secretValue, ki.EnvAllow[0].ValueHash)
	assert.NotContains(t, ki.EnvAllow[0].ValueHash, secretValue)
}

// TestComputeKeyInputsDepsByProject verifies DepsByProject resolves each
// DependsOn path against that project's own most recent entry.
func TestComputeKeyInputsDepsByProject(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	upstream := Step{ProjectPath: "libs/a", WorkspaceRoot: root, Target: "build"}
	res, err := c.Run(t.Context(), upstream, func(context.Context) error { return nil })
	require.NoError(t, err)

	downstream := Step{
		ProjectPath:   "svc",
		WorkspaceRoot: root,
		Target:        "build",
		DependsOn:     []string{"libs/a"},
	}
	ki, err := c.ComputeKeyInputs(t.Context(), &downstream)
	require.NoError(t, err)
	require.NotNil(t, ki.DepsByProject)
	assert.Equal(t, res.Hash, ki.DepsByProject["libs/a"])
}

// TestComputeKeyInputsDepsByProjectMissing verifies a DependsOn entry with no
// cache entry yet is simply absent from DepsByProject, not an error.
func TestComputeKeyInputsDepsByProjectMissing(t *testing.T) {
	root, _, c := newMutableCache(t)
	step := Step{
		ProjectPath:   "svc",
		WorkspaceRoot: root,
		Target:        "build",
		DependsOn:     []string{"libs/never-built"},
	}
	ki, err := c.ComputeKeyInputs(t.Context(), &step)
	require.NoError(t, err)
	_, ok := ki.DepsByProject["libs/never-built"]
	assert.False(t, ok)
}

// TestSnapshotPersistsKeyInputs verifies a real Run through the mutable cache
// stores KeyInputs on the manifest, and that a second run with a changed source
// file produces a diff pinpointing exactly that file.
func TestSnapshotPersistsKeyInputs(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	out := touchOut(t, root)

	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}

	_, err := c.Run(t.Context(), step, func(context.Context) error {
		return os.WriteFile(out, []byte("v1"), 0o644)
	})
	require.NoError(t, err)

	m1, _, err := c.LastEntry("test/pkg")
	require.NoError(t, err)
	require.NotNil(t, m1.KeyInputs, "manifest must carry captured KeyInputs")

	// Change the source so the second run is a genuine miss.
	writeMain(t, root, "package main // v2")
	_, err = c.Run(t.Context(), step, func(context.Context) error {
		return os.WriteFile(out, []byte("v2"), 0o644)
	})
	require.NoError(t, err)

	m2, err := c.ComputeKeyInputs(t.Context(), &step)
	require.NoError(t, err)

	got := DiffKeyInputs(m1.KeyInputs, m2)
	assert.Equal(t, []string{"src changed: test/pkg/main.go"}, got)
}
