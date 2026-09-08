package doctor

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The case this check exists for: a host wired for the guard months ago, judging
// correctly, and silently recording nothing about where work stopped.
func TestCheckpointWiringAdvisesAGuardedHostThatRecordsNothing(t *testing.T) {
	root := t.TempDir()
	plant(t, root, ".claude/settings.json", `{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-guard-command.sh"}]}]}}`)

	got := checkCheckpointWiring(root, "")

	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "none recording a checkpoint")
}

func TestCheckpointWiringPassesOnAHostThatRecordsOne(t *testing.T) {
	root := t.TempDir()
	plant(t, root, ".claude/settings.json",
		`{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-guard-command.sh"}]}],`+
			`"Stop":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-checkpoint.sh"}]}]}}`)

	got := checkCheckpointWiring(root, "")

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "1 of 1")
}

// A plugin that calls the command directly counts: the shipped script exists so a host
// does not have to reimplement the binary lookup, not as the only way in.
func TestCheckpointWiringAcceptsTheCommandWithoutTheTemplate(t *testing.T) {
	root := t.TempDir()
	plant(t, root, ".opencode/plugins/magus.ts", `export const hook = () => run("magus", ["session", "checkpoint", "--agent-name", "opencode"])`)

	got := checkCheckpointWiring(root, "")

	assert.Equal(t, types.DoctorOK, got.Status)
}

// Nothing wired at all is guard-wiring's finding. Repeating it here would be a second
// advisory about one absence, which is how a report trains people to skim it.
func TestCheckpointWiringStaysQuietWithNoHostAtAll(t *testing.T) {
	got := checkCheckpointWiring(t.TempDir(), "")

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	assert.Contains(t, got.Message, "skipped")
}

// A config that never mentions magus is somebody else's, and reading it as an unrecording
// magus host would advise every repository with a Claude Code settings file.
func TestCheckpointWiringIgnoresAConfigThatIsNotMagus(t *testing.T) {
	root := t.TempDir()
	plant(t, root, ".claude/settings.json", `{"hooks":{"PreToolUse":[{"hooks":[{"command":"./scripts/lint.sh"}]}]}}`)

	got := checkCheckpointWiring(root, "")

	require.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "skipped")
}
