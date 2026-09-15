package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	assert.Contains(t, string(body), "magus-guard-command.sh")
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
	assert.Contains(t, string(body), "magus-guard-command.sh")
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
    "entries": [{"matcher": "Read", "hooks": [{"type": "command", "command": "sh magus-guard-observe.sh"}]}]
  }]
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "reader"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(filepath.Join(root, "reader/hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus-guard-observe.sh")

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
    {"path": ["hooks", "beforeShell"], "entries": [{"command": "sh cursor-guard.sh"}]},
    {"path": ["hooks", "afterTool"], "entries": [{"matcher": "Write", "command": "sh cursor-guard.sh"}]},
    {"path": ["hooks", "sessionStop"], "entries": [{"command": "sh magus-checkpoint.sh"}]}
  ]
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "flat"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(filepath.Join(root, "flat/hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"version": 1`)
	assert.Contains(t, string(body), "cursor-guard.sh")
	assert.Contains(t, string(body), `"beforeShell"`)
	assert.NotContains(t, string(body), `"hooks": [`)

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
      "entries": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "sh magus-guard-command.sh"}]}]
    },
    {
      "path": ["hooks", "Stop"],
      "entries": [{"hooks": [{"type": "command", "command": "magus session checkpoint"}]}]
    }
  ]
}`), 0o644))

	update, err := ApplyHarness(context.Background(), HarnessApplyOptions{Root: root, ID: "managed"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

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
      "commands": [{"type": "command", "command": "sh magus-guard-command.sh", "statusMessage": "stale status"}]
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
	assert.Equal(t, 1, strings.Count(string(body), "magus-guard-command.sh"), "identity match must replace, not append")
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

func TestSkillsOnlyHarnessVerifiesWithoutAConfig(t *testing.T) {
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
	assert.Equal(t, HarnessVerified, result.Status)
	assert.False(t, result.Guarded)
	assert.Empty(t, result.Path)

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
	assert.False(t, invokesMagus("echo session hook"))
	assert.True(t, invokesMagus("sh docs/guides/integrations/agents/cursor-guard.sh"))
	assert.True(t, invokesMagus("magus session hook -o json"))
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
				Entries: []map[string]any{{"command": "magus session hook"}},
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
      {"match": "run", "commands": [{"type": "command", "command": "sh magus-guard-command.sh", "statusMessage": "magus guard: checking command"}]},
      {"match": "write", "commands": [{"type": "command", "command": "sh magus-guard-path.sh", "timeout": 10}]}
    ]
  }]
}`), 0o644))
}
