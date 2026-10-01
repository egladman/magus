package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// untimedProbes lifts the probe deadline: the stand-ins these tests wire answer or
// exit at once, so the shipped 10s measures only how loaded the machine is, and go
// test's own timeout still catches a real hang.
func untimedProbes() context.Context {
	return ContextWithProbeTimeout(context.Background(), time.Hour)
}

// TestProbeFixturesMatchTheSharedGuardArchive keeps the probe's embedded synthetic
// events byte-identical to the ones cmd/magus/testdata/script/guard_templates.txtar
// already uses to pin the SAME guard behavior (a whole-tree `git stash` denies, an
// AGENTS.md write advises) end to end against the real shipped templates. The probe
// cannot read that archive at runtime (it is test-only source and does not ship
// in the binary), so this is what "reuse the fixtures" means here: one file
// section copied, and a test that fails the moment it drifts from the copy.
func TestProbeFixturesMatchTheSharedGuardArchive(t *testing.T) {
	archive, err := txtar.ParseFile(filepath.Join("..", "..", "cmd", "magus", "testdata", "script", "guard_templates.txtar"))
	require.NoError(t, err, "read the shared guard-template fixture archive")

	want := map[string]string{
		"deny-command.json":      probeDenyCommandEvent,
		"advise-path.json":       probeAdvisePathEvent,
		"cursor-shell-deny.json": probeCursorShellDenyEvent,
	}
	found := map[string]bool{}
	for _, f := range archive.Files {
		if got, ok := want[f.Name]; ok {
			found[f.Name] = true
			assert.Equal(t, got, string(bytesTrimNewline(f.Data)), "probe fixture %q drifted from the shared archive", f.Name)
		}
	}
	for name := range want {
		assert.True(t, found[name], "shared archive no longer carries %q; the probe's copy has nothing to stay in sync with", name)
	}
}

func bytesTrimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// writeProbeableHarness registers a descriptor named id whose one managed command
// is command, wired at a path this test controls, and merges its plan, so
// VerifyHarness has a real config to read and probeHarnessCommands has a real
// command string to run.
func writeProbeableHarness(t *testing.T, root, id, command string) {
	t.Helper()
	body := `{
  "schema_version": 2,
  "id": "` + id + `",
  "display": {"name": "` + id + `"},
  "config": {"path": "` + id + `/hooks.json"},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [{
    "path": ["hooks", "before"],
    "entries": [{"match": "run", "commands": [{"type": "command", "command": "` + command + `"}]}]
  }]
}`
	registerHarnessSpell(t, id, body)
	mergeHarness(t, root, id)
}

// probeCommand is the shape a harness wires the command glue in: the workspace's
// binary running the Buzz file. The binary these tests put at root/magus is a stub,
// so what answers is whatever the test registered for the glue.
const probeCommand = "./magus buzz -s magus-command.buzz"

// writeFakeMagusBinary satisfies checkProbeEnvironment's presence check with a
// binary that answers nothing at all.
func writeFakeMagusBinary(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

// TestVerifyHarnessProbeCatchesABrokenWiredCommand is defect 2's pinning test: a
// config that carries the declared command glue entry byte-for-byte (so the OLD
// presence-comparison VerifyHarness said "verified") but whose command answers
// nothing: the exact shape of the incident that motivated this change, a wired
// command that looks right and does nothing. This must FAIL against the
// presence-only implementation: that code never executes anything, so it would
// report HarnessVerified here as it did for the config that locked a session out.
func TestVerifyHarnessProbeCatchesABrokenWiredCommand(t *testing.T) {
	root := t.TempDir()
	writeFakeMagusBinary(t, root)
	writeProbeableHarness(t, root, "broken", probeCommand)

	result, err := VerifyHarness(untimedProbes(), root, "broken")
	require.NoError(t, err)
	assert.NotEqual(t, HarnessVerified, result.Status, "a wired command that answers nothing must never read as verified")
	assert.Equal(t, HarnessUncovered, result.Status)
	assert.Contains(t, result.Reason, "magus-command.buzz")
}

// TestVerifyHarnessProbeAcceptsAWorkingWiredCommand is the positive twin: the same
// shape, but the script is really there and really answers "deny" for the
// universal whole-tree-stash event. Only this earns HarnessVerified.
func TestVerifyHarnessProbeAcceptsAWorkingWiredCommand(t *testing.T) {
	root := t.TempDir()
	writeStubGuardScript(t, root, "magus-command.buzz", "deny")
	writeProbeableHarness(t, root, "working", probeCommand)

	result, err := VerifyHarness(untimedProbes(), root, "working")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)
	assert.True(t, result.Guarded)
}

// TestVerifyHarnessProbeIgnoresWhatTheWiredCommandSaysOnStderr pins the stream
// split. The wiring this repository recommends puts a released magus on PATH that
// redirects to the workspace's own binary and says so on stderr, which the host
// never reads; grading it as part of the answer reported a guard that was denying
// correctly as uncovered.
func TestVerifyHarnessProbeIgnoresWhatTheWiredCommandSaysOnStderr(t *testing.T) {
	root := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\necho 'magus: using this workspace ...' >&2\nprintf 'deny'\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte(script), 0o755))
	writeProbeableHarness(t, root, "chatty", probeCommand)

	result, err := VerifyHarness(untimedProbes(), root, "chatty")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status, result.Reason)
}

// TestVerifyHarnessProbeAnswersWithAWrongDecisionIsUncovered covers a command that
// runs, and answers, but not with a decision a working guard would ever render for
// this event: the case a raw "it produced non-empty output" check would have
// missed.
func TestVerifyHarnessProbeAnswersWithAWrongDecisionIsUncovered(t *testing.T) {
	root := t.TempDir()
	writeStubGuardScript(t, root, "magus-command.buzz", "pass")
	writeProbeableHarness(t, root, "wrongdecision", probeCommand)

	result, err := VerifyHarness(untimedProbes(), root, "wrongdecision")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)
	assert.Contains(t, result.Reason, `"pass"`)
}

// TestVerifyHarnessProbeReportsNoMagusBinaryAsUnprobed pins the "cannot run"
// contract: a probe that cannot execute at all must say exactly why, distinct from
// both a working guard and a broken one. sh resolves, but no magus binary does
// anywhere checkProbeEnvironment looks (root, then PATH).
func TestVerifyHarnessProbeReportsNoMagusBinaryAsUnprobed(t *testing.T) {
	root := t.TempDir()
	// root/magus is deliberately absent, and PATH holds sh alone.
	writeProbeableHarness(t, root, "nomagus", "magus buzz -s magus-command.buzz")

	// Resolved and copied BEFORE PATH is overridden below.
	shOnlyPATH := buildMinimalPATH(t, "sh")
	t.Setenv("PATH", shOnlyPATH)

	result, err := VerifyHarness(untimedProbes(), root, "nomagus")
	require.NoError(t, err)
	assert.Equal(t, HarnessUnprobed, result.Status)
	assert.Contains(t, result.Reason, "no magus binary")
}

// TestVerifyHarnessProbeOutlastingItsDeadlineIsUnprobed pins that a probe cut
// short says nothing about the config: under memory pressure a working guard
// misses the deadline too, and uncovered would blame a correct config.
func TestVerifyHarnessProbeOutlastingItsDeadlineIsUnprobed(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\nexec sleep 5\n"), 0o755))
	writeProbeableHarness(t, root, "slow", probeCommand)

	ctx := ContextWithProbeTimeout(context.Background(), 50*time.Millisecond)
	result, err := VerifyHarness(ctx, root, "slow")
	require.NoError(t, err)
	assert.Equal(t, HarnessUnprobed, result.Status, result.Reason)
	assert.Contains(t, result.Reason, "did not finish within 50ms")
	assert.False(t, result.Guarded)
}

// TestVerifyHarnessProbeSkipsLifecycleScripts confirms a harness whose only
// managed command is a lifecycle hook (checkpoint/rehydrate/observe), which
// never renders a verdict, is still reported verified from presence alone: there
// is nothing for the probe to run, and that is not a gap.
func TestVerifyHarnessProbeSkipsLifecycleScripts(t *testing.T) {
	for _, command := range []string{
		"magus buzz -s magus-observe.buzz -- --agent-name x",
		"magus buzz -s magus-checkpoint.buzz -- --agent-name x",
		"magus buzz -s magus-rehydrate.buzz -- --format json",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFakeMagusBinary(t, root)
			writeProbeableHarness(t, root, "lifecycle", command)
			// No stub script written at all: if this were probed, it would fail.

			result, err := VerifyHarness(untimedProbes(), root, "lifecycle")
			require.NoError(t, err)
			assert.Equal(t, HarnessVerified, result.Status)
		})
	}
}

// buildMinimalPATH resolves each name against the CURRENT (real) PATH, so call it
// before t.Setenv("PATH", ...) replaces it, copies each into a fresh directory,
// and returns that directory. A test then sets PATH to exactly this, giving a
// deterministic PATH that carries some real tools (sh) but not others
// (magus), rather than depending on what happens to be installed elsewhere on
// the machine running the suite.
func buildMinimalPATH(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		real, err := exec.LookPath(name)
		require.NoErrorf(t, err, "the test machine has no %q to stand in with", name)
		data, err := os.ReadFile(real)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o755))
	}
	return dir
}
