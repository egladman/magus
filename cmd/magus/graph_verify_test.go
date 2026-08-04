package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphVerifyStrictOnMissingInstall pins absence as a --strict failure. A CI
// drift gate that exits 0 because the artifact it verifies is not there guards
// only the repos that already installed it: dropping the install, or never
// landing it, is exactly the drift the gate exists to catch.
func TestGraphVerifyStrictOnMissingInstall(t *testing.T) {
	assertExit1 := func(t *testing.T, err error) {
		t.Helper()
		require.Error(t, err)
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		assert.Equal(t, 1, silent.exitCode)
	}

	t.Run("missing install is informational without --strict", func(t *testing.T) {
		dir := t.TempDir()
		assert.NoError(t, graphVerify(context.Background(), dir, []string{"--dir", dir}))
	})

	t.Run("missing install fails under --strict", func(t *testing.T) {
		dir := t.TempDir()
		assertExit1(t, graphVerify(context.Background(), dir, []string{"--strict", "--dir", dir}))
	})

	t.Run("current install passes under --strict", func(t *testing.T) {
		dir := t.TempDir()
		_, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, agent.VariantFull)
		require.NoError(t, err)
		assert.NoError(t, graphVerify(context.Background(), dir, []string{"--strict", "--dir", dir}))
	})

	t.Run("stale install fails under --strict", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude/skills/magus-query"), 0o755))
		stale := "---\nname: x\n---\nbody\n<!-- agent-skill-version: 0; knowledge-schema-version: 1 -->\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude/skills/magus-query/SKILL.md"), []byte(stale), 0o644))
		assertExit1(t, graphVerify(context.Background(), dir, []string{"--strict", "--dir", dir}))
	})
}
