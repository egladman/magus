package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessInstallReportsWhatItWroteAndPruned pins the report. This install
// force-writes and prunes without a flag, and it did both with no output at all:
// 30 directories rewritten and 11 removed, and nothing said so. A delete a person
// cannot see is how they lose a skill they thought they had.
func TestHarnessInstallReportsWhatItWroteAndPruned(t *testing.T) {
	catalog := shippedCatalog()
	root := t.TempDir()
	const dest = ".agents/skills"

	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	_, _, err := catalog.installHarnessSkillPath(context.Background(), root, dest, types.SkillFormFull, HarnessInstallOptions{Text: true})
	require.NoError(t, err)
	assert.Contains(t, log.String(), `msg=wrote component="agent harness install"`)

	// An orphan from an earlier release: magus stamped it, so this install prunes it.
	orphan := filepath.Join(root, dest, "magus-retired")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), typesagent.SkillVariantShort), 0o644))

	log.Reset()
	out := testkit.CaptureStdout(t, func() {
		_, _, err := catalog.installHarnessSkillPath(context.Background(), root, dest, types.SkillFormFull, HarnessInstallOptions{Text: true})
		require.NoError(t, err)
	})
	assert.NoDirExists(t, orphan)
	assert.Equal(t, fmt.Sprintf("removed skill this binary no longer ships: %s\n", filepath.Join(dest, "magus-retired")), out)

	// A dry run names the same deletion and performs none of it.
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), typesagent.SkillVariantShort), 0o644))
	out = testkit.CaptureStdout(t, func() {
		_, _, err := catalog.installHarnessSkillPath(context.Background(), root, dest, types.SkillFormFull, HarnessInstallOptions{DryRun: true, Text: true})
		require.NoError(t, err)
	})
	assert.DirExists(t, orphan)
	assert.Contains(t, out, fmt.Sprintf("would remove skill this binary no longer ships: %s\n", filepath.Join(dest, "magus-retired")))
}
