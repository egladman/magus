package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeDoctorHarness(t *testing.T, root string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-host.json"), []byte(`{
  "schema_version": 2,
  "id": "test-host",
  "display": {"name": "Test Host"},
  "config": {"path": "host/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "managed_entries": [{
    "path": ["hooks", "before"],
    "entries": [{"match":"run", "commands":[{"type":"command","command":"sh magus-guard-command.sh"}]}]
  }]
}`), 0o644))
	// VerifyHarness now actually runs the wired command (see internal/agent's
	// harness_probe.go), rather than trusting that "sh magus-guard-command.sh" is
	// present in the config. This stub is what makes it answer for real: every
	// caller of guardedHarnessConfig() references this exact script name.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus-guard-command.sh"), []byte("#!/bin/sh\ncat >/dev/null\nprintf 'deny'\n"), 0o755))
	// The probe also checks for AN executable magus before running anything (see
	// checkProbeEnvironment), purely as a presence gate: it never actually
	// invokes this file for a "sh magus-guard-command.sh" entry, which resolves
	// against root on its own. Written only if a caller (writeGuardCanaryStub, for
	// the real canary check) has not already planted a specific one here.
	magusStub := filepath.Join(root, "magus")
	if _, err := os.Stat(magusStub); os.IsNotExist(err) {
		require.NoError(t, os.WriteFile(magusStub, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
}

func writeCheckpointHarness(t *testing.T, root, body string) {
	t.Helper()
	writeDoctorHarness(t, root)
	plant(t, root, "host/hooks.json", body)
}

func guardedHarnessConfig() string {
	return `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"sh magus-guard-command.sh"}]}]}}`
}

// The case this check exists for: a host wired for the guard months ago, judging
// correctly, and silently recording nothing about where work stopped.
func TestCheckpointWiringAdvisesAGuardedHostThatRecordsNothing(t *testing.T) {
	root := t.TempDir()
	writeCheckpointHarness(t, root, guardedHarnessConfig())

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "none recording a checkpoint")
}

func TestCheckpointWiringPassesOnAHostThatRecordsOne(t *testing.T) {
	root := t.TempDir()
	body := strings.TrimSuffix(guardedHarnessConfig(), "}") + `,"checkpoint":"magus session checkpoint"}`
	writeCheckpointHarness(t, root, body)

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "1 of 1")
}

// A plugin that calls the command directly counts: the shipped script exists so a host
// does not have to reimplement the binary lookup, not as the only way in.
func TestCheckpointWiringAcceptsTheCommandWithoutTheTemplate(t *testing.T) {
	root := t.TempDir()
	body := strings.TrimSuffix(guardedHarnessConfig(), "}") + `,"checkpoint":"magus session checkpoint"}`
	writeCheckpointHarness(t, root, body)

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorOK, got.Status)
}

// Nothing wired at all is guard-wiring's finding. Repeating it here would be a second
// advisory about one absence, which is how a report trains people to skim it.
func TestCheckpointWiringStaysQuietWithNoHostAtAll(t *testing.T) {
	got := checkCheckpointWiring(t.TempDir())

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	assert.Contains(t, got.Message, "skipped")
}

func TestCheckpointWiringReportsGuardedHostMissingManagedCheckpoint(t *testing.T) {
	root := t.TempDir()
	writeDoctorHarness(t, root)
	plant(t, root, "host/hooks.json", guardedHarnessConfig())

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "none recording a checkpoint")
}

// A config that never mentions magus is somebody else's, and reading it as an unrecording
// magus host would advise every repository with a Claude Code settings file.
func TestCheckpointWiringFollowsNamedGuardScripts(t *testing.T) {
	root := t.TempDir()
	writeDoctorHarness(t, root)
	plant(t, root, "host/hooks.json", `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"sh magus-guard-command.sh"}]}],"stop":[{"commands":[{"type":"command","command":"sh docs/agents/cursor-guard.sh"}]}]}}`)
	plant(t, root, "docs/agents/cursor-guard.sh", "#!/bin/sh\n$MAGUS session checkpoint --agent-name cursor\n")

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
	assert.Contains(t, got.Message, "1 of 1")
}

func TestCheckpointWiringIgnoresAConfigThatIsNotMagus(t *testing.T) {
	root := t.TempDir()
	writeDoctorHarness(t, root)
	plant(t, root, "host/hooks.json", `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./scripts/lint.sh"}]}]}}`)

	got := checkCheckpointWiring(root)

	require.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "skipped")
}
