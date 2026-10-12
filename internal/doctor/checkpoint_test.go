package doctor

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// doctorHarnessDescriptorJSON is the "test-host" fixture body, in the same shape a
// harnesses/test-host.json compat file used to carry.
const doctorHarnessDescriptorJSON = `{
  "schema_version": 2,
  "id": "test-host",
  "display": {"name": "Test Host"},
  "config": {"path": "host/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "managed_entries": [{
    "path": ["hooks", "before"],
    "entries": [{"match":"run", "commands":[{"type":"command","command":"sh magus-command-stand-in.sh"}]}]
  }]
}`

// writeDoctorHarness registers "test-host" as a fake harness spell (there is no
// harnesses/*.json compat directory left to scan) and stages the guard stub and
// fake ./magus every caller of it needs. Callers must pass "test-host" as one of
// the wired ids the check under test resolves against; nothing discovers it
// automatically any more.
func writeDoctorHarness(t *testing.T, w *testkit.Workspace) {
	t.Helper()
	var descriptor agent.HarnessDescriptor
	require.NoError(t, json.Unmarshal([]byte(doctorHarnessDescriptorJSON), &descriptor))
	fixture.WireHarness(t, descriptor)
	// VerifyHarness now actually runs the wired command (see internal/agent's
	// harness_probe.go), rather than trusting that "sh magus-command-stand-in.sh" is
	// present in the config. This stub is what makes it answer for real: every
	// caller of guardedHarnessConfig() references this exact script name.
	w.WriteExecutable("magus-command-stand-in.sh", "#!/bin/sh\ncat >/dev/null\nprintf 'deny'\n")
	// The probe also checks for AN executable magus before running anything (see
	// checkProbeEnvironment), purely as a presence gate: it never actually
	// invokes this file for a "sh magus-command-stand-in.sh" entry, which resolves
	// against root on its own. Written only if a caller (a guard probe stub) has not
	// already planted a specific one here.
	if _, err := os.Stat(w.Path("magus")); os.IsNotExist(err) {
		w.WriteExecutable("magus", "#!/bin/sh\nexit 0\n")
	}
}

// harnessProbeCtx gives the harness probe VerifyHarness runs the same headroom
// testProbeBudget gives the guard probe, for the same reason.
func harnessProbeCtx() context.Context {
	return agent.ContextWithProbeTimeout(context.Background(), testProbeBudget)
}

func writeCheckpointHarness(t *testing.T, w *testkit.Workspace, body string) {
	t.Helper()
	writeDoctorHarness(t, w)
	w.Write("host/hooks.json", body)
}

func guardedHarnessConfig() string {
	return `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"sh magus-command-stand-in.sh"}]}]}}`
}

// The case this check exists for: a host wired for the guard months ago, judging
// correctly, and silently recording nothing about where work stopped.
func TestCheckpointWiringAdvisesAGuardedHostThatRecordsNothing(t *testing.T) {
	w := testkit.NewWorkspace(t)
	writeCheckpointHarness(t, w, guardedHarnessConfig())

	got := checkCheckpointWiring(harnessProbeCtx(), w.Root(), "test-host")

	assert.Equal(t, types.CheckAdvice, got.Status)
	assert.Contains(t, got.Message, "none recording a checkpoint")
}

func TestCheckpointWiringPassesOnAHostThatRecordsOne(t *testing.T) {
	w := testkit.NewWorkspace(t)
	body := strings.TrimSuffix(guardedHarnessConfig(), "}") + `,"checkpoint":"magus session checkpoint"}`
	writeCheckpointHarness(t, w, body)

	got := checkCheckpointWiring(harnessProbeCtx(), w.Root(), "test-host")

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "1 of 1")
}

// A plugin that calls the command directly counts: the shipped script exists so a host
// does not have to reimplement the binary lookup, not as the only way in.
func TestCheckpointWiringAcceptsTheCommandWithoutTheTemplate(t *testing.T) {
	w := testkit.NewWorkspace(t)
	body := strings.TrimSuffix(guardedHarnessConfig(), "}") + `,"checkpoint":"magus session checkpoint"}`
	writeCheckpointHarness(t, w, body)

	got := checkCheckpointWiring(harnessProbeCtx(), w.Root(), "test-host")

	assert.Equal(t, types.CheckOK, got.Status)
}

// Nothing wired at all is guard-wiring's finding. Repeating it here would be a second
// advisory about one absence, which is how a report trains people to skim it.
func TestCheckpointWiringStaysQuietWithNoHostAtAll(t *testing.T) {
	got := checkCheckpointWiring(harnessProbeCtx(), t.TempDir())

	assert.Equal(t, types.Check{Status: types.CheckOK, Evidence: types.EvidenceUnknown}, withoutWording(got))
	assert.Contains(t, got.Message, "skipped")
}

func TestCheckpointWiringReportsGuardedHostMissingManagedCheckpoint(t *testing.T) {
	w := testkit.NewWorkspace(t)
	writeDoctorHarness(t, w)
	w.Write("host/hooks.json", guardedHarnessConfig())

	got := checkCheckpointWiring(harnessProbeCtx(), w.Root(), "test-host")

	assert.Equal(t, types.CheckAdvice, got.Status)
	assert.Contains(t, got.Message, "none recording a checkpoint")
}

// A config that never mentions magus is somebody else's, and reading it as an unrecording
// magus host would advise every repository with a Claude Code settings file.
func TestCheckpointWiringFollowsNamedGuardScripts(t *testing.T) {
	w := testkit.NewWorkspace(t)
	writeDoctorHarness(t, w)
	w.Write("host/hooks.json", `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"sh magus-command-stand-in.sh"}]}],"stop":[{"commands":[{"type":"command","command":"magus buzz -s docs/agents/cursor-hook.buzz -- --agent-name cursor"}]}]}}`)
	// The call as the shipped Cursor glue spells it: an argv list, never the words
	// "session checkpoint" side by side.
	w.Write("docs/agents/cursor-hook.buzz", `proc\exec(bin, args: ["session", "checkpoint", "--agent-name", agent], opts: {"quiet": true});`+"\n")

	got := checkCheckpointWiring(harnessProbeCtx(), w.Root(), "test-host")

	assert.Equal(t, types.CheckOK, got.Status, got.Message)
	assert.Contains(t, got.Message, "1 of 1")
}

func TestCheckpointWiringIgnoresAConfigThatIsNotMagus(t *testing.T) {
	w := testkit.NewWorkspace(t)
	writeDoctorHarness(t, w)
	w.Write("host/hooks.json", `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./scripts/lint.sh"}]}]}}`)

	got := checkCheckpointWiring(harnessProbeCtx(), w.Root(), "test-host")

	require.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "skipped")
}
