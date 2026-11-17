package agent

import (
	"bytes"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
