package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyHarnessAddsOnlyMagusHookAlongsideUserHooks(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	const original = "{\n  \"other\": {\"keep\": true},\n  \"hooks\": {\n    \"before\": [{\"match\": \"run\", \"commands\": [{\"type\": \"command\", \"command\": \"my-own-hook\"}]}]\n  }\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "my-own-hook")
	assert.Contains(t, string(body), "magus-hook-command.sh")
	assert.Contains(t, string(body), `"other": {`)

	second, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	assert.False(t, second.Changed, "a second apply must be idempotent")
}

func TestApplyHarnessRefusesMalformedOrWrongShapeJSON(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":[]}`), 0o644))

	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an object")
}

func TestApplyHarnessPreservesCompetingAndLargeUserValues(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
  "large": 9007199254740993,
  "hooks": {"before": [{"match": "run", "commands": [{"type":"command", "command":"my-own-hook", "statusMessage":"my custom status"}]}]}
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "9007199254740993")
	assert.Contains(t, string(body), "my custom status")
	assert.Contains(t, string(body), "magus-hook-command.sh")
}

func TestApplyHarnessCanWireReadObserver(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "reader.json"), []byte(`{
  "schema_version": 2,
  "id": "reader",
  "display": {"name": "Reader"},
  "config": {"path": "reader/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "full"},
  "managed_entries": [{
    "path": ["hooks", "PreToolUse"],
    "entries": [{"matcher": "Read", "hooks": [{"type": "command", "command": "sh magus-hook-observe.sh"}]}]
  }]
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "reader"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(filepath.Join(root, "reader/hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus-hook-observe.sh")

	result, err := VerifyHarness(context.Background(), root, "reader")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)
	assert.True(t, result.Guarded)
}

func TestApplyHarnessRejectsDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	const body = `{"hooks":{},"hooks":{}}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate JSON object key")
	got, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, body, string(got), "a rejected document must not be rewritten")
}

func TestApplyHarnessDryRunPlansWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host", DryRun: true})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	assert.True(t, update.Planned)
	_, err = os.Stat(filepath.Join(root, "test-host", "hooks.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestApplyHarnessRejectsBoundLease(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host", ActingLease: "job-123"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bound job")
}

func TestWorkspaceHarnessLoads(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	d, source, err := LoadHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "test-host", d.ID)
	assert.Equal(t, filepath.Join(root, "harnesses", "test-host.json"), source)
}

func TestVerifyHarnessReportsCoverageRatherThanGuessing(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	result, err := VerifyHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)

	_, err = ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	// The wired commands now have to actually answer, not merely be present: give
	// them something real to run.
	writeStubGuardScript(t, root, "magus-hook-command.sh", "deny")
	writeStubGuardScript(t, root, "magus-hook-path.sh", "advise")
	result, err = VerifyHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)
}

func TestApplyHarnessFlatEntriesAndConfigDefaults(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flat.json"), []byte(`{
  "schema_version": 2,
  "id": "flat",
  "display": {"name": "Flat"},
  "config": {"path": "flat/hooks.json"},
  "config_defaults": {"version": 1},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [
    {"path": ["hooks", "beforeShell"], "entries": [{"command": "sh cursor-hook.sh"}]},
    {"path": ["hooks", "afterTool"], "entries": [{"matcher": "Write", "command": "sh cursor-hook.sh"}]},
    {"path": ["hooks", "sessionStop"], "entries": [{"command": "sh magus-checkpoint.sh"}]}
  ]
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "flat"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(filepath.Join(root, "flat/hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"version": 1`)
	assert.Contains(t, string(body), "cursor-hook.sh")
	assert.Contains(t, string(body), `"beforeShell"`)
	assert.NotContains(t, string(body), `"hooks": [`)

	// cursor-hook.sh's reply dialect is self-contained (see probeEventFor), so the
	// probe only checks that it answers something; magus-checkpoint.sh renders no
	// verdict at all and is never probed.
	writeStubGuardScript(t, root, "cursor-hook.sh", "ok")
	result, err := VerifyHarness(context.Background(), root, "flat")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)

	second, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "flat"})
	require.NoError(t, err)
	assert.False(t, second.Changed)
}

func TestApplyHarnessOwnsManagedEntries(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "managed.json"), []byte(`{
  "schema_version": 2,
  "id": "managed",
  "display": {"name": "Managed"},
  "config": {"path": "managed/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "full"},
  "managed_entries": [
    {
      "path": ["hooks", "PreToolUse"],
      "entries": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "sh magus-hook-command.sh"}]}]
    },
    {
      "path": ["hooks", "Stop"],
      "entries": [{"hooks": [{"type": "command", "command": "sh magus-checkpoint.sh"}]}]
    }
  ]
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "managed"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	// magus-checkpoint.sh renders no verdict and is never probed; magus-hook-command.sh
	// is, so it needs something real behind it now.
	writeStubGuardScript(t, root, "magus-hook-command.sh", "deny")
	result, err := VerifyHarness(context.Background(), root, "managed")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)

	second, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "managed"})
	require.NoError(t, err)
	assert.False(t, second.Changed)
}

func TestApplyHarnessReplacesSameIdentityInPlace(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
  "hooks": {
    "before": [{
      "match": "run",
      "commands": [{"type": "command", "command": "sh magus-hook-command.sh", "statusMessage": "stale status"}]
    }]
  }
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus guard: checking command")
	assert.NotContains(t, string(body), "stale status")
	assert.Equal(t, 1, strings.Count(string(body), "magus-hook-command.sh"), "identity match must replace, not append")
}

func TestHarnessDescriptorRejectsEscapingPathAndNonMagusCommand(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{
  "schema_version": 2,
  "id": "bad",
  "display": {"name": "Bad"},
  "config": {"path": "../outside.json"},
  "managed_entries": [{
    "path": ["hooks"],
    "entries": [{"match":"run", "commands":[{"type":"command", "command":"bypass"}]}]
  }]
}`), 0o644))
	_, _, err := LoadHarness(context.Background(), root, "bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace-relative")

	require.NoError(t, os.Remove(filepath.Join(dir, "bad.json")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bypass.json"), []byte(`{
  "schema_version": 2,
  "id": "bypass",
  "display": {"name": "Bypass"},
  "config": {"path": "bypass/hooks.json"},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [{
    "path": ["hooks"],
    "entries": [{"command": "bypass"}]
  }]
}`), 0o644))
	_, _, err = LoadHarness(context.Background(), root, "bypass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not invoke magus")
}

// TestSkillsOnlyHarnessReportsSkillsOnlyNotVerified pins the opencode.json defect:
// a descriptor with no config.path at all wires no guard, and reporting that as
// HarnessVerified (the behavior this test used to assert) is the single most
// misleading verdict this surface could give: every deny and advise rule reads
// as enforced when nothing here can enforce anything. It must read as its own,
// distinct status instead.
func TestSkillsOnlyHarnessReportsSkillsOnlyNotVerified(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skills-only.json"), []byte(`{
  "schema_version": 2,
  "id": "skills-only",
  "display": {"name": "Skills Only"},
  "skills": {"paths": [".agents/skills"], "form": "both"}
}`), 0o644))

	result, err := VerifyHarness(context.Background(), root, "skills-only")
	require.NoError(t, err)
	assert.Equal(t, HarnessSkillsOnly, result.Status)
	assert.NotEqual(t, HarnessVerified, result.Status)
	assert.False(t, result.Guarded)
	assert.Empty(t, result.Path)
	assert.NotEmpty(t, result.Reason)

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "skills-only"})
	require.NoError(t, err)
	assert.False(t, update.Changed)
}

func TestVerifyHarnessRejectsConfigThatDoesNotInvokeMagus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.json"), []byte(`{
  "schema_version": 2,
  "id": "empty",
  "display": {"name": "Empty"},
  "config": {"path": "empty/hooks.json"},
  "skills": {"paths": [], "form": "short"}
}`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "empty", "hooks.json"), []byte(`{"hooks":[]}`), 0o644))

	result, err := VerifyHarness(context.Background(), root, "empty")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)
	assert.Contains(t, result.Reason, "does not invoke magus")

	_, err = ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "empty"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not invoke magus")
}

func TestInvokesMagusRejectsGenericGuardScripts(t *testing.T) {
	assert.False(t, invokesMagus("sh host-guard.sh"))
	assert.False(t, invokesMagus("echo shell"))
	assert.True(t, invokesMagus("sh docs/guides/integrations/agents/cursor-hook.sh"))
	assert.True(t, invokesMagus("magus shell -o json"))
	assert.True(t, invokesMagus("./magus session notify"))
}

func TestKnownHarnessesUnionsWiredSpellNames(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)

	ids, err := KnownHarnesses(context.Background(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"test-host"}, ids)

	// nil / no wired args: JSON only. Distinct from a blank entry in wired.
	ids, err = KnownHarnesses(context.Background(), root, nil...)
	require.NoError(t, err)
	assert.Equal(t, []string{"test-host"}, ids)

	ids, err = KnownHarnesses(context.Background(), root, "cursor", "test-host", "codex")
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "cursor", "test-host"}, ids)
}

func TestKnownHarnessesRejectsEmptyWiredID(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)

	_, err := KnownHarnesses(context.Background(), root, "cursor", "", "codex")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")

	_, err = KnownHarnesses(context.Background(), root, "  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestLoadHarnessSpellOnlyWhenWired(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)

	prev := harnessSpellLoader
	t.Cleanup(func() { harnessSpellLoader = prev })
	harnessSpellLoader = func(ctx context.Context, id string) (HarnessDescriptor, string, bool, error) {
		if id != "test-host" {
			return HarnessDescriptor{}, "", false, nil
		}
		return HarnessDescriptor{
			SchemaVersion: harnessSchemaVersion,
			ID:            "test-host",
			Display:       HarnessDisplay{Name: "Spell"},
			Config:        HarnessConfig{Path: "spell/hooks.json"},
			Skills:        HarnessSkills{Paths: []string{".agents/skills"}, Form: FormFull},
			ManagedEntries: []HarnessEntries{{
				Path:    []string{"hooks", "before"},
				Entries: []map[string]any{{"command": "magus shell"}},
			}},
		}, "spell:test-host", true, nil
	}

	// Unset context: registered spell still wins (legacy / tests).
	d, src, err := LoadHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "spell:test-host", src)
	assert.Equal(t, "spell/hooks.json", d.Config.Path)

	// Wired without this id: fall through to JSON.
	ctx := ContextWithWiredHarnesses(context.Background(), []string{"cursor"})
	d, src, err = LoadHarness(ctx, root, "test-host")
	require.NoError(t, err)
	assert.NotEqual(t, "spell:test-host", src)
	assert.Equal(t, "test-host/hooks.json", d.Config.Path)

	// Wired including this id: spell wins.
	ctx = ContextWithWiredHarnesses(context.Background(), []string{"test-host"})
	d, src, err = LoadHarness(ctx, root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "spell:test-host", src)
	assert.Equal(t, "spell/hooks.json", d.Config.Path)
}

// TestOneBadDescriptorDisqualifiesOnlyItself bounds the blast radius of a stray
// file. A single unparsable descriptor used to fail the whole load, so one .json
// in a user config dir turned harness support off for every host on the machine.
// It is skipped now, but never quietly: it is reported by name with its error,
// and a lookup that misses names it too, because a typo and a malformed file
// would otherwise read the same.
func TestOneBadDescriptorDisqualifiesOnlyItself(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	writeTestHarness(t, root)
	broken := filepath.Join(root, "harnesses", "broken.json")
	require.NoError(t, os.WriteFile(broken, []byte(`{"schema_version": 2, "id":`), 0o644))

	d, source, err := LoadHarness(context.Background(), root, "test-host")
	require.NoError(t, err, "a malformed neighbour must not disqualify a valid descriptor")
	assert.Equal(t, "test-host", d.ID)
	assert.Equal(t, filepath.Join(root, "harnesses", "test-host.json"), source)

	ids, err := KnownHarnesses(context.Background(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"test-host"}, ids)

	problems, err := HarnessProblems(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	assert.Equal(t, broken, problems[0].Source)
	assert.Contains(t, problems[0].Reason, "parse harness descriptor")

	_, _, err = LoadHarness(context.Background(), root, "absent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken.json", "a miss must name what disqualified itself")

	// A descriptor that parses but fails validation knows its own id, so the miss
	// answers with the reason rather than with "no harness named".
	require.NoError(t, os.WriteFile(filepath.Join(root, "harnesses", "escaping.json"), []byte(`{
  "schema_version": 2,
  "id": "escaping",
  "display": {"name": "Escaping"},
  "config": {"path": "../outside.json"}
}`), 0o644))
	_, _, err = LoadHarness(context.Background(), root, "escaping")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace-relative")
	ids, err = KnownHarnesses(context.Background(), root)
	require.NoError(t, err)
	assert.Equal(t, []string{"test-host"}, ids)
}

// writeStubGuardScript drops a trivial POSIX sh script at root/name that drains
// stdin and prints output verbatim, standing in for a shipped guard script so
// VerifyHarness's probe (harness_probe.go) has something real to execute. Tests
// that only exercise ApplyHarness's own file-merge mechanics do not need this;
// only a test that calls VerifyHarness against a command probeHarnessCommands
// recognizes as guard-shaped does, now that presence alone no longer earns
// HarnessVerified. See harness_probe_test.go for the probe's own tests.
func writeStubGuardScript(t *testing.T, root, name, output string) {
	t.Helper()
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '" + output + "'\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(script), 0o755))
}

func writeTestHarness(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test-host.json"), []byte(`{
  "schema_version": 2,
  "id": "test-host",
  "display": {"name": "Test Host", "color": "#111111"},
  "config": {"path": "test-host/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "full"},
  "managed_entries": [{
    "path": ["hooks", "before"],
    "entries": [
      {"match": "run", "commands": [{"type": "command", "command": "sh magus-hook-command.sh", "statusMessage": "magus guard: checking command"}]},
      {"match": "write", "commands": [{"type": "command", "command": "sh magus-hook-path.sh", "timeout": 10}]}
    ]
  }]
}`), 0o644))
}

// TestRemoveHarnessDeletesOnlyItsOwnEntriesAndDefaults pins defect 1: before this,
// there was no inverse of `harness apply` at all, so a descriptor that wires a
// broken guard command could lock an agent out of editing the very file that is
// denying it, with no command to recover short of hand-editing host config from
// outside the session. This is the test apply's own existing suite never needed
// and remove's whole reason to exist: it must undo EXACTLY what apply wrote, and
// nothing a person added beside it.
func TestRemoveHarnessDeletesOnlyItsOwnEntriesAndDefaults(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flat.json"), []byte(`{
  "schema_version": 2,
  "id": "flat",
  "display": {"name": "Flat"},
  "config": {"path": "flat/hooks.json"},
  "config_defaults": {"version": 1},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [
    {"path": ["hooks", "beforeShell"], "entries": [{"command": "sh cursor-hook.sh"}]}
  ]
}`), 0o644))

	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "flat"})
	require.NoError(t, err)

	path := filepath.Join(root, "flat", "hooks.json")
	// A person's own key beside magus's, added after apply ran. Neither belongs
	// to this descriptor, and neither may be touched by remove.
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var config map[string]any
	require.NoError(t, json.Unmarshal(body, &config))
	config["mine"] = map[string]any{"kept": true}
	config["userVersion"] = 7
	encoded, err := json.MarshalIndent(config, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o644))

	update, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "flat"})
	require.NoError(t, err)
	assert.True(t, update.Removed)
	assert.True(t, update.Changed)
	assert.False(t, update.Planned)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(after), "cursor-hook.sh", "magus's own managed entry must be gone")
	assert.NotContains(t, string(after), `"version": 1`, "the config_default this descriptor wrote must be gone")
	assert.Contains(t, string(after), `"kept": true`, "a user's own key must survive")
	assert.Contains(t, string(after), `"userVersion": 7`, "a user's own key must survive")

	// Verify now reports uncovered: apply's own fragments are gone.
	result, err := VerifyHarness(context.Background(), root, "flat")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)

	// Idempotent: nothing left of ours to remove a second time.
	second, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "flat"})
	require.NoError(t, err)
	assert.False(t, second.Changed)
}

// TestRemoveHarnessLeavesAUserModifiedConfigDefaultAlone: a config_defaults key
// only belongs to remove when the file still holds exactly the value apply wrote.
// A value the user changed since is theirs now.
func TestRemoveHarnessLeavesAUserModifiedConfigDefaultAlone(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flat.json"), []byte(`{
  "schema_version": 2,
  "id": "flat",
  "display": {"name": "Flat"},
  "config": {"path": "flat/hooks.json"},
  "config_defaults": {"version": 1},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [
    {"path": ["hooks", "beforeShell"], "entries": [{"command": "sh cursor-hook.sh"}]}
  ]
}`), 0o644))
	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "flat"})
	require.NoError(t, err)

	path := filepath.Join(root, "flat", "hooks.json")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	// A user bumped "version" themselves after apply ran.
	changed := strings.Replace(string(body), `"version": 1`, `"version": 2`, 1)
	require.NoError(t, os.WriteFile(path, []byte(changed), 0o644))

	_, err = RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "flat"})
	require.NoError(t, err)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(after), `"version": 2`, "a value the user changed since apply is theirs, not ours to delete")
}

// TestRemoveHarnessRecognizesAHandEditedManagedEntryByIdentity mirrors apply's own
// "same identity, different body" replace-in-place rule (sameManagedIdentity):
// remove must still recognize an entry as OURS after a person tweaked its timeout
// or statusMessage, or a stale edited copy of magus's hook survives every remove.
func TestRemoveHarnessRecognizesAHandEditedManagedEntryByIdentity(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(body), "magus guard: checking command", "a person's own status text", 1)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o644))

	update, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(after), "magus-hook-command.sh")
}

// TestRemoveHarnessOnSkillsOnlyDescriptorIsANoOp: apply never writes a fragment for
// a skills-only descriptor (empty config.path), so remove has nothing to undo.
func TestRemoveHarnessOnSkillsOnlyDescriptorIsANoOp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skills-only.json"), []byte(`{
  "schema_version": 2,
  "id": "skills-only",
  "display": {"name": "Skills Only"},
  "skills": {"paths": [".agents/skills"], "form": "both"}
}`), 0o644))

	update, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "skills-only"})
	require.NoError(t, err)
	assert.True(t, update.Removed)
	assert.False(t, update.Changed)
	assert.Empty(t, update.Path)
}

// TestRemoveHarnessOnMissingConfigIsANoOp: nothing was ever applied, so there is
// no file to touch and no error to raise.
func TestRemoveHarnessOnMissingConfigIsANoOp(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)

	update, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	assert.False(t, update.Changed)
	_, statErr := os.Stat(filepath.Join(root, "test-host", "hooks.json"))
	assert.True(t, os.IsNotExist(statErr))
}

// TestRemoveHarnessRejectsBoundLease mirrors ApplyHarness's own rule: a bound job
// cannot rewire a host harness, remove included.
func TestRemoveHarnessRejectsBoundLease(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	_, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "test-host", ActingLease: "job-123"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bound job")
}

// TestRemoveHarnessDryRunPlansWithoutWriting mirrors ApplyHarness's own --dry-run
// contract: report what would change, touch nothing.
func TestRemoveHarnessDryRunPlansWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "test-host"})
	require.NoError(t, err)
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	update, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "test-host", DryRun: true})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	assert.True(t, update.Planned)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a dry run must not touch the file")
}

const promptRules = "prefix_rule(pattern = [\"git\", \"push\"], decision = \"prompt\")\n"

// writePromptHarness installs a skills-only descriptor that keeps two host-native approval
// prompts: a whole rules file, and one key inside a JSON config the person also edits.
func writePromptHarness(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompter.json"), []byte(`{
  "schema_version": 2,
  "id": "prompter",
  "display": {"name": "Prompter"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "prompts": [
    {"path": ".codex/rules/magus.rules", "content": `+strconvQuote(promptRules)+`},
    {"path": "opencode.json", "key": ["permission", "bash", "git push *"], "value": "ask"}
  ]
}`), 0o644))
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestApplyHarnessWritesNativePrompts pins that apply puts the host's own approval prompt in
// place and leaves the rest of a shared config alone, and that verify then reports it.
func TestApplyHarnessWritesNativePrompts(t *testing.T) {
	root := t.TempDir()
	writePromptHarness(t, root)
	config := filepath.Join(root, "opencode.json")
	require.NoError(t, os.WriteFile(config, []byte(`{"model": "m", "permission": {"edit": "ask"}}`), 0o644))

	before, err := VerifyHarness(context.Background(), root, "prompter")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, before.PromptStatus)
	assert.Contains(t, before.PromptReason, ".codex/rules/magus.rules")

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "prompter"})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	assert.Equal(t, []string{
		filepath.Join(root, ".codex/rules/magus.rules"),
		filepath.Join(root, "opencode.json"),
	}, update.Prompts)

	rules, err := os.ReadFile(filepath.Join(root, ".codex/rules/magus.rules"))
	require.NoError(t, err)
	assert.Equal(t, promptRules, string(rules))
	var got map[string]any
	body, err := os.ReadFile(config)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, map[string]any{
		"model":      "m",
		"permission": map[string]any{"edit": "ask", "bash": map[string]any{"git push *": "ask"}},
	}, got)

	again, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "prompter"})
	require.NoError(t, err)
	assert.False(t, again.Changed, "a second apply must be idempotent")

	after, err := VerifyHarness(context.Background(), root, "prompter")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, after.PromptStatus)
	assert.Empty(t, after.PromptReason)
}

// TestApplyHarnessRefusesAPromptThePersonOverrode pins that apply does not overwrite a value
// someone chose, and says which one: the prompt is missing either way, and silence would
// leave every ungated push refused with no clue why.
func TestApplyHarnessRefusesAPromptThePersonOverrode(t *testing.T) {
	root := t.TempDir()
	writePromptHarness(t, root)
	config := filepath.Join(root, "opencode.json")
	const chosen = `{"permission": {"bash": {"git push *": "allow"}}}`
	require.NoError(t, os.WriteFile(config, []byte(chosen), 0o644))

	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "prompter"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission.bash.git push *")
	body, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, chosen, string(body))

	result, err := VerifyHarness(context.Background(), root, "prompter")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.PromptStatus)
}

// TestRemoveHarnessDeletesItsPrompts pins remove as apply's inverse for prompts too.
func TestRemoveHarnessDeletesItsPrompts(t *testing.T) {
	root := t.TempDir()
	writePromptHarness(t, root)
	config := filepath.Join(root, "opencode.json")
	require.NoError(t, os.WriteFile(config, []byte(`{"model": "m"}`), 0o644))
	_, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "prompter"})
	require.NoError(t, err)

	update, err := RemoveHarness(context.Background(), HarnessRemoveOptions{Root: root, ID: "prompter"})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	assert.NoFileExists(t, filepath.Join(root, ".codex/rules/magus.rules"))
	body, err := os.ReadFile(config)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, map[string]any{"model": "m"}, got)
}

// TestHarnessDescriptorRejectsAMalformedPrompt pins that a prompt names exactly one of a file
// body or a JSON key, inside the workspace.
func TestHarnessDescriptorRejectsAMalformedPrompt(t *testing.T) {
	base := HarnessDescriptor{SchemaVersion: harnessSchemaVersion, ID: "p", Display: HarnessDisplay{Name: "P"}, Skills: HarnessSkills{Form: "both"}}
	for name, prompt := range map[string]HarnessPrompt{
		"no path":      {Content: "x"},
		"escapes":      {Path: "../x", Content: "x"},
		"neither body": {Path: "x"},
		"both bodies":  {Path: "x", Content: "x", Key: []string{"k"}, Value: "ask"},
		"key no value": {Path: "x", Key: []string{"k"}},
	} {
		d := base
		d.Prompts = []HarnessPrompt{prompt}
		assert.Error(t, validateHarnessDescriptor(d), name)
	}
}
