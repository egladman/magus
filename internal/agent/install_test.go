package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shippedCatalog is the catalog this binary embeds, the one install writes.
func shippedCatalog() *Catalog { return Default("magus.knowledge/v1alpha6") }

// plantSkillDirs stages the three things a skills directory holds beside a shipped
// install: a skill a past magus stamped and this one dropped, a hand-authored skill,
// and an empty directory.
func plantSkillDirs(t *testing.T, dir, dest string) (retired, handAuthored, empty string) {
	t.Helper()
	catalog := shippedCatalog()
	retired = filepath.Join(dir, dest, "magus-retired")
	require.NoError(t, os.MkdirAll(retired, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(retired, "SKILL.md"),
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), typesagent.SkillVariantShort), 0o644))

	handAuthored = filepath.Join(dir, dest, "magus-local-development")
	require.NoError(t, os.MkdirAll(handAuthored, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(handAuthored, "SKILL.md"), []byte("---\nname: magus-local-development\n---\n\nmine\n"), 0o644))

	empty = filepath.Join(dir, dest, "magus-empty")
	require.NoError(t, os.MkdirAll(empty, 0o755))
	return retired, handAuthored, empty
}

func TestInstallSkillTreePrunesOnlyWhatMagusStamped(t *testing.T) {
	catalog := shippedCatalog()
	dir := t.TempDir()
	const dest = ".claude/skills"
	retired, handAuthored, empty := plantSkillDirs(t, dir, dest)

	written, changed, removed, err := catalog.InstallSkillTree(dir, dest, true, false, types.SkillFormFull)
	require.NoError(t, err)

	assert.Equal(t, []string{filepath.Join(dest, "magus-retired")}, removed)
	assert.NotEmpty(t, written)
	assert.Equal(t, written, changed, "a fresh tree is all changes")
	assert.NoDirExists(t, retired)
	assert.DirExists(t, handAuthored, "a skill without the stamp is not magus's to delete")
	assert.DirExists(t, empty, "an empty directory proves nothing about who wrote it")
	assert.FileExists(t, filepath.Join(dir, dest, "magus-query", "SKILL.md"))
}

func TestInstallSkillTreeDryRunRemovesNothing(t *testing.T) {
	catalog := shippedCatalog()
	dir := t.TempDir()
	const dest = ".claude/skills"
	retired, _, _ := plantSkillDirs(t, dir, dest)

	written, changed, removed, err := catalog.InstallSkillTree(dir, dest, true, true, types.SkillFormFull)
	require.NoError(t, err)

	assert.Equal(t, []string{filepath.Join(dest, "magus-retired")}, removed)
	assert.NotEmpty(t, written)
	assert.Empty(t, changed)
	assert.DirExists(t, retired)
	assert.NoDirExists(t, filepath.Join(dir, dest, "magus-query"), "a dry run writes nothing")
}

// TestSkillTargetHonorsGlobal pins the fix for a flag that never worked:
// --global documents "allow absolute destination paths", the command let one
// past its own guard, and Catalog.WriteSkillTree then refused it
// unconditionally, so the error told the caller to pass the flag they had just
// passed.
//
// The split is what makes it work without weakening the catalog: an absolute
// destination becomes the directory it names plus the leaf inside it, so the
// catalog's containment check still has something real to check.
func TestSkillTargetHonorsGlobal(t *testing.T) {
	base, leaf := SkillTarget(".", "/tmp/skills", true)
	assert.Equal(t, filepath.Dir("/tmp/skills"), base, "an absolute dest supplies its own base")
	assert.Equal(t, "skills", leaf)
	assert.Equal(t, filepath.Clean("/tmp/skills"), filepath.Join(base, leaf),
		"the split must rejoin to the path the caller asked for")
}

// TestSkillTargetLeavesRelativeAlone: without --global, and for a relative
// destination with it, the pair is unchanged: the catalog keeps enforcing
// containment against the repo dir.
func TestSkillTargetLeavesRelativeAlone(t *testing.T) {
	base, leaf := SkillTarget("repo", ".claude/skills", false)
	assert.Equal(t, "repo", base)
	assert.Equal(t, ".claude/skills", leaf)

	base, leaf = SkillTarget("repo", ".claude/skills", true)
	assert.Equal(t, "repo", base, "--global does not relocate a relative destination")
	assert.Equal(t, ".claude/skills", leaf)

	// A traversal is left intact so the catalog still refuses it; --global must
	// not become a way to escape the tree.
	base, leaf = SkillTarget("repo", "../../outside", true)
	assert.Equal(t, "repo", base)
	assert.Equal(t, "../../outside", leaf)
}

func TestDeclaresRoutingIndexRefusesAnyDirThatIsNotTheRoot(t *testing.T) {
	outputs := func(context.Context, string) ([]types.Glob, error) {
		return []types.Glob{{Pattern: "MAGUS.md"}}, nil
	}
	dir := t.TempDir()
	assert.True(t, DeclaresRoutingIndex(t.Context(), dir, func(abs string) (string, error) { return abs, nil }, outputs))
	assert.False(t, DeclaresRoutingIndex(t.Context(), dir, func(string) (string, error) { return filepath.Dir(dir), nil }, outputs), "a root above dir would point the link at a file beside nothing")
	assert.False(t, DeclaresRoutingIndex(t.Context(), dir, func(string) (string, error) { return "", os.ErrNotExist }, outputs))
	assert.False(t, DeclaresRoutingIndex(t.Context(), dir, func(abs string) (string, error) { return abs, nil },
		func(context.Context, string) ([]types.Glob, error) { return nil, os.ErrInvalid }), "a workspace that does not load declares nothing")
}
