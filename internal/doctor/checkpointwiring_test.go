package doctor

import (
	"os"
	"path/filepath"
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
  "pre_tool_use": {
    "path": ["hooks", "before"],
    "matcher_key": "match",
    "hooks_key": "commands",
    "response_template": "{{toJson .}}",
    "entries": [{"matcher":"run", "hook":{"type":"command"}}]
  }
}`), 0o644))
}

func writeCheckpointHarness(t *testing.T, root, body string) {
	t.Helper()
	writeDoctorHarness(t, root)
	plant(t, root, "host/hooks.json", body)
}

const guardedHarnessConfig = `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"magus agent hook --host test-host"}]}]}}`

// The case this check exists for: a host wired for the guard months ago, judging
// correctly, and silently recording nothing about where work stopped.
func TestCheckpointWiringAdvisesAGuardedHostThatRecordsNothing(t *testing.T) {
	root := t.TempDir()
	writeCheckpointHarness(t, root, guardedHarnessConfig)

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "none recording a checkpoint")
}

func TestCheckpointWiringPassesOnAHostThatRecordsOne(t *testing.T) {
	root := t.TempDir()
	writeCheckpointHarness(t, root, guardedHarnessConfig[:len(guardedHarnessConfig)-1]+`,"checkpoint":"magus session checkpoint"}`)

	got := checkCheckpointWiring(root)

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "1 of 1")
}

// A plugin that calls the command directly counts: the shipped script exists so a host
// does not have to reimplement the binary lookup, not as the only way in.
func TestCheckpointWiringAcceptsTheCommandWithoutTheTemplate(t *testing.T) {
	root := t.TempDir()
	writeCheckpointHarness(t, root, guardedHarnessConfig[:len(guardedHarnessConfig)-1]+`,"checkpoint":"magus session checkpoint"}`)

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

// A config that never mentions magus is somebody else's, and reading it as an unrecording
// magus host would advise every repository with a Claude Code settings file.
func TestCheckpointWiringIgnoresAConfigThatIsNotMagus(t *testing.T) {
	root := t.TempDir()
	writeDoctorHarness(t, root)
	plant(t, root, "host/hooks.json", `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./scripts/lint.sh"}]}]}}`)

	got := checkCheckpointWiring(root)

	require.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "skipped")
}
