package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
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

// TestHarnessChangeRefusesABoundJobFromEitherSource pins that a harness change is refused
// under the checkout's binding as well as under the claim the process was launched with.
// Only the claim used to count, so a worker `magus job exec` bound, with no BAGGAGE, could
// rewire the hooks that grade it.
func TestHarnessChangeRefusesABoundJobFromEitherSource(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("version: 1\n"), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)

	assert.Empty(t, harnessActingLease(context.Background(), root), "an unbound caller rewires its own hosts")

	claimed := proc.WithLease(context.Background(), "fleet/claimed")
	assert.Equal(t, "fleet/claimed", harnessActingLease(claimed, root), "the claim")

	require.NoError(t, job.BindLease(cacheDir, "fleet/bound"))
	assert.Equal(t, "fleet/bound", harnessActingLease(context.Background(), root), "the binding, with no claim")
	assert.Equal(t, "fleet/bound", harnessActingLease(claimed, root), "the binding over a different claim")

	err = agentHarnessInstallCmd(context.Background(), root, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bound job "fleet/bound"`)
}
