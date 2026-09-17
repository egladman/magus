package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessInstallReportsWhatItWroteAndPruned pins the report. This install
// force-writes and prunes without a flag, and it did both with no output at all:
// 30 directories rewritten and 11 removed, and nothing said so. A delete a person
// cannot see is how they lose a skill they thought they had.
func TestHarnessInstallReportsWhatItWroteAndPruned(t *testing.T) {
	root := t.TempDir()
	const dest = ".agents/skills"

	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	require.NoError(t, installHarnessSkillPath(context.Background(), root, dest, agent.FormFull, false))
	assert.Contains(t, log.String(), "agent harness install: wrote")

	// An orphan from an earlier release: magus stamped it, so this install prunes it.
	orphan := filepath.Join(root, dest, "magus-retired")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		agentSkills.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), agent.VariantShort), 0o644))

	log.Reset()
	require.NoError(t, installHarnessSkillPath(context.Background(), root, dest, agent.FormFull, false))
	assert.NoDirExists(t, orphan)
	assert.Contains(t, log.String(), "agent harness install: removed skill this binary no longer ships")
	assert.Contains(t, log.String(), filepath.Join(dest, "magus-retired"))

	// A dry run names the same deletion and performs none of it.
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		agentSkills.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), agent.VariantShort), 0o644))
	log.Reset()
	require.NoError(t, installHarnessSkillPath(context.Background(), root, dest, agent.FormFull, true))
	assert.DirExists(t, orphan)
	assert.Contains(t, log.String(), "agent harness install: would remove skill this binary no longer ships")
}
