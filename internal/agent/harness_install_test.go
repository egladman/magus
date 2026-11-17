package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
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
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), tier.SkillVariantShort), 0o644))

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
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), tier.SkillVariantShort), 0o644))
	out = testkit.CaptureStdout(t, func() {
		_, _, err := catalog.installHarnessSkillPath(context.Background(), root, dest, types.SkillFormFull, HarnessInstallOptions{DryRun: true, Text: true})
		require.NoError(t, err)
	})
	assert.DirExists(t, orphan)
	assert.Contains(t, out, fmt.Sprintf("would remove skill this binary no longer ships: %s\n", filepath.Join(dest, "magus-retired")))
}

// TestDescribeHarnessPrintsEachChangeAndTheMergeCommand pins the text a person reads before
// installing: every file, every entry it gains or loses, and the command.
// Describe prints that command and never runs it.
func TestDescribeHarnessPrintsEachChangeAndTheMergeCommand(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, WriteHarnessPlan(&out, types.HarnessInstallation{
		ID: "claude-code",
		Files: map[string]types.HarnessFile{
			".claude/settings.json": {
				Exists:   true,
				Fragment: map[string]any{"hooks": map[string]any{"Stop": []any{}}},
				Changes: []types.HarnessChange{
					{Op: types.HarnessAdd, Key: "hooks.Stop", Value: map[string]any{"command": "magus buzz -s magus-checkpoint.buzz"}},
					{Op: types.HarnessRetire, Key: "hooks.Stop", Value: map[string]any{"command": "magus buzz -s old/magus-checkpoint.buzz"}},
				},
			},
			".codex/rules/magus.rules": {Content: "rule\n", Changes: []types.HarnessChange{{Op: types.HarnessWrite}}},
		},
		Merge:   "merge-command",
		MCPHint: "register it",
	}))
	assert.Equal(t, `claude-code harness: 2 file(s) to merge
  .claude/settings.json (exists)
    add hooks.Stop: {"command":"magus buzz -s magus-checkpoint.buzz"}
    retire hooks.Stop: {"command":"magus buzz -s old/magus-checkpoint.buzz"}
  .codex/rules/magus.rules (missing)
    write the whole file
apply the installation:
  merge-command
mcp claude-code (user-owned; Magus does not write host MCP config):
register it
`, out.String())

	out.Reset()
	require.NoError(t, WriteHarnessPlan(&out, types.HarnessInstallation{ID: "cursor"}))
	assert.Equal(t, "cursor harness: current\n", out.String())

	out.Reset()
	require.NoError(t, WriteHarnessPlan(&out, types.HarnessInstallation{
		ID: "claude-code",
		Wired: []types.HarnessWired{{File: ".claude/settings.json", Key: "hooks.PreToolUse", Entries: []map[string]any{
			{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-command.buzz"}}},
			{"hooks": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-observe.buzz"}}},
		}}},
	}))
	assert.Equal(t, `claude-code harness: current
wired in .claude/settings.json hooks.PreToolUse:
  Bash: ./magus buzz -s magus-command.buzz
  any: ./magus buzz -s magus-observe.buzz
`, out.String(), "a current harness still prints the commands it wires")
}

func TestVerificationFailureNamesTheFirstFailingPart(t *testing.T) {
	assert.NoError(t, VerificationFailure(HarnessVerification{ID: "x", Status: HarnessVerified}))
	assert.NoError(t, VerificationFailure(HarnessVerification{ID: "x", Status: HarnessSkillsOnly}), "a host that wires no guard has nothing uncovered")

	err := VerificationFailure(HarnessVerification{ID: "x", Status: HarnessSkillsOnly, Prompt: HarnessCheck{Status: HarnessUncovered, Reason: "no prompt"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "approval prompt for x")
}
