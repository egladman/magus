package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// writeProbeableHarness installs a descriptor named id whose one managed command
// is command, wired at a path this test controls, and applies it, so
// VerifyHarness has a real config to read and probeHarnessCommands has a real
// command string to run.
func writeProbeableHarness(t *testing.T, root, id, command string) {
	t.Helper()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644))
	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: id})
	require.NoError(t, err)
}

// writeFakeMagusBinary satisfies checkProbeEnvironment's presence check without
// needing a real one: it is only ever stat'd, never executed, because every
// command these tests probe is a self-contained relative script.
func writeFakeMagusBinary(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

// TestVerifyHarnessProbeCatchesABrokenWiredCommand is defect 2's pinning test: a
// config that carries the declared "sh magus-command.sh" entry byte-for-byte
// (so the OLD presence-comparison VerifyHarness said "verified") but whose script
// does not exist at that path: the exact shape of the incident that motivated this
// change, a wired command that looks right and does nothing. This must FAIL against
// the presence-only implementation: that code never executes anything, so it would
// report HarnessVerified here as it did for the config that locked a session out.
func TestVerifyHarnessProbeCatchesABrokenWiredCommand(t *testing.T) {
	root := t.TempDir()
	writeFakeMagusBinary(t, root)
	writeProbeableHarness(t, root, "broken", "sh magus-command.sh")
	// Deliberately never written: the script the config names does not exist.

	result, err := VerifyHarness(context.Background(), root, "broken")
	require.NoError(t, err)
	assert.NotEqual(t, HarnessVerified, result.Status, "a wired command that cannot even run must never read as verified")
	assert.Equal(t, HarnessUncovered, result.Status)
	assert.Contains(t, result.Reason, "magus-command.sh")
}

// TestVerifyHarnessProbeAcceptsAWorkingWiredCommand is the positive twin: the same
// shape, but the script is really there and really answers "deny" for the
// universal whole-tree-stash event. Only this earns HarnessVerified.
func TestVerifyHarnessProbeAcceptsAWorkingWiredCommand(t *testing.T) {
	root := t.TempDir()
	writeFakeMagusBinary(t, root)
	writeStubGuardScript(t, root, "magus-command.sh", "deny")
	writeProbeableHarness(t, root, "working", "sh magus-command.sh")

	result, err := VerifyHarness(context.Background(), root, "working")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)
	assert.True(t, result.Guarded)
}

// TestVerifyHarnessProbeAnswersWithAWrongDecisionIsUncovered covers a command that
// runs, and answers, but not with a decision a working guard would ever render for
// this event: the case a raw "it produced non-empty output" check would have
// missed.
func TestVerifyHarnessProbeAnswersWithAWrongDecisionIsUncovered(t *testing.T) {
	root := t.TempDir()
	writeFakeMagusBinary(t, root)
	writeStubGuardScript(t, root, "magus-command.sh", "pass")
	writeProbeableHarness(t, root, "wrongdecision", "sh magus-command.sh")

	result, err := VerifyHarness(context.Background(), root, "wrongdecision")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)
	assert.Contains(t, result.Reason, `"pass"`)
}

// TestVerifyHarnessProbeReportsMissingJQAsUnprobed pins the "cannot run" contract:
// a probe that cannot execute at all must say exactly why, distinct from both a
// working guard and a broken one. PATH is emptied so exec.LookPath("jq") fails
// exactly like a machine that never installed it, per checkProbeEnvironment.
func TestVerifyHarnessProbeReportsMissingJQAsUnprobed(t *testing.T) {
	root := t.TempDir()
	writeFakeMagusBinary(t, root)
	writeStubGuardScript(t, root, "magus-command.sh", "deny")
	writeProbeableHarness(t, root, "nojq", "sh magus-command.sh")

	// Resolved and copied BEFORE PATH is overridden below.
	shOnlyPATH := buildMinimalPATH(t, "sh")
	t.Setenv("PATH", shOnlyPATH)

	result, err := VerifyHarness(context.Background(), root, "nojq")
	require.NoError(t, err)
	assert.Equal(t, HarnessUnprobed, result.Status)
	assert.Contains(t, result.Reason, "jq")
}

// TestVerifyHarnessProbeReportsNoMagusBinaryAsUnprobed is the third named "cannot
// run" case: sh and jq resolve, but no magus binary does anywhere checkProbeEnvironment
// looks (root, then PATH).
func TestVerifyHarnessProbeReportsNoMagusBinaryAsUnprobed(t *testing.T) {
	root := t.TempDir()
	// No writeFakeMagusBinary this time: root/magus is deliberately absent.
	writeStubGuardScript(t, root, "magus-command.sh", "deny")
	writeProbeableHarness(t, root, "nomagus", "sh magus-command.sh")

	shAndJQOnlyPATH := buildMinimalPATH(t, "sh", "jq")
	t.Setenv("PATH", shAndJQOnlyPATH)

	result, err := VerifyHarness(context.Background(), root, "nomagus")
	require.NoError(t, err)
	assert.Equal(t, HarnessUnprobed, result.Status)
	assert.Contains(t, result.Reason, "no magus binary")
}

// TestVerifyHarnessProbeSkipsLifecycleScripts confirms a harness whose only
// managed command is a lifecycle hook (checkpoint/rehydrate/observe), which
// never renders a verdict, is still reported verified from presence alone: there
// is nothing for the probe to run, and that is not a gap.
// Both forms of both wrappers, because the skip used to match the .sh suffix alone:
// porting checkpoint and rehydrate to Buzz made every apply --id claude-code write a
// config its own verify then called uncovered, on the grounds that a recorder had
// rendered no verdict.
func TestVerifyHarnessProbeSkipsLifecycleScripts(t *testing.T) {
	for _, command := range []string{
		"sh magus-checkpoint.sh",
		"sh magus-rehydrate.sh",
		"magus buzz -s magus-checkpoint.buzz",
		"magus buzz -s magus-rehydrate.buzz",
	} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeFakeMagusBinary(t, root)
			writeProbeableHarness(t, root, "lifecycle", command)
			// No stub script written at all: if this were probed, it would fail.

			result, err := VerifyHarness(context.Background(), root, "lifecycle")
			require.NoError(t, err)
			assert.Equal(t, HarnessVerified, result.Status)
		})
	}
}

// buildMinimalPATH resolves each name against the CURRENT (real) PATH, so call it
// before t.Setenv("PATH", ...) replaces it, copies each into a fresh directory,
// and returns that directory. A test then sets PATH to exactly this, giving a
// deterministic PATH that carries some real tools (sh, jq) but not others
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
