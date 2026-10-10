package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registerHarnessSpell decodes a JSON descriptor body — the same shape a
// harnesses/<id>.json compat file used to carry — into a HarnessDescriptor and
// registers it as a fake harness spell for id, restoring the previous loader when
// the test ends. LoadHarness has no JSON fallback any more, and PlanHarness and
// VerifyHarness operate on whatever LoadHarness resolves without
// caring which source supplied it, so every fixture body below is unchanged from
// the JSON-descriptor era; only how a test hands it to LoadHarness moved. body is
// decoded, not validated, so a fixture that is deliberately invalid (an escaping
// config path, a command that does not invoke magus) still reaches
// validateHarnessDescriptor through LoadHarness exactly as it did as a file.
func registerHarnessSpell(t *testing.T, id, body string) {
	t.Helper()
	var d HarnessDescriptor
	require.NoError(t, decodeHarnessJSON([]byte(body), &d))
	prev := harnessSpellLoader
	t.Cleanup(func() { harnessSpellLoader = prev })
	harnessSpellLoader = func(_ context.Context, wantID string) (HarnessDescriptor, string, bool, error) {
		if wantID != id {
			return HarnessDescriptor{}, "", false, nil
		}
		return d, "spell:" + id, true, nil
	}
}

// mergeHarness plans id and merges the plan into the files with the semantics of the printed
// command, standing in for the person who runs it. TestHarnessMergeCommandLeavesTheFileCurrent
// pins that the command and this agree.
func mergeHarness(t *testing.T, root, id string) types.HarnessPlan {
	t.Helper()
	plan, err := PlanHarness(context.Background(), root, id)
	require.NoError(t, err)
	for rel, file := range plan.Files {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		if file.Content != "" {
			require.NoError(t, os.WriteFile(path, []byte(file.Content), 0o644))
			continue
		}
		doc := map[string]any{}
		if file.Exists {
			body, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NoError(t, decodeHarnessJSON(body, &doc))
		}
		encoded, err := json.MarshalIndent(mergeFragment(doc, file.Fragment), "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o644))
	}
	return plan
}

// requireCurrent plans id again and requires that nothing is left to merge.
func requireCurrent(t *testing.T, root, id string) {
	t.Helper()
	plan, err := PlanHarness(context.Background(), root, id)
	require.NoError(t, err)
	assert.True(t, plan.Current(), "a merged plan leaves nothing to merge: %v", plan.Files)
	assert.Empty(t, plan.Merge)
}

func TestPlanHarnessAddsOnlyMagusHookAlongsideUserHooks(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	const original = "{\n  \"other\": {\"keep\": true},\n  \"hooks\": {\n    \"before\": [{\"match\": \"run\", \"commands\": [{\"type\": \"command\", \"command\": \"my-own-hook\"}]}]\n  }\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	plan, err := PlanHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, string(got), "planning writes nothing")

	own := map[string]any{"match": "run", "commands": []any{map[string]any{"type": "command", "command": "my-own-hook"}}}
	command := map[string]any{"match": "run", "commands": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-command.buzz", "statusMessage": "magus guard: checking command"}}}
	pathEntry := map[string]any{"match": "write", "commands": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-path.buzz", "timeout": float64(10)}}}
	assert.Equal(t, map[string]types.HarnessFile{
		"test-host/hooks.json": {
			Exists:   true,
			Fragment: map[string]any{"hooks": map[string]any{"before": []any{own, command, pathEntry}}},
			Changes: []types.HarnessChange{
				{Op: types.HarnessAdd, Key: "hooks.before", Value: command},
				{Op: types.HarnessAdd, Key: "hooks.before", Value: pathEntry},
			},
		},
	}, normalizeFiles(t, plan.Files))

	mergeHarness(t, root, "test-host")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "my-own-hook")
	assert.Contains(t, string(body), `"other": {`)
	requireCurrent(t, root, "test-host")

	current, err := PlanHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	encoded, err := json.Marshal(current.Wired)
	require.NoError(t, err)
	var wired []types.HarnessWired
	require.NoError(t, json.Unmarshal(encoded, &wired))
	assert.Equal(t, []types.HarnessWired{{File: "test-host/hooks.json", Key: "hooks.before", Entries: []map[string]any{command, pathEntry}}}, wired,
		"a current harness still names what it wires, and only the managed entries")
}

// normalizeFiles round-trips files through JSON, the form `describe harness -o json` prints,
// so a whole-value comparison does not depend on which Go types the descriptor decoded into.
func normalizeFiles(t *testing.T, files map[string]types.HarnessFile) map[string]types.HarnessFile {
	t.Helper()
	encoded, err := json.Marshal(files)
	require.NoError(t, err)
	var out map[string]types.HarnessFile
	require.NoError(t, json.Unmarshal(encoded, &out))
	return out
}

func TestPlanHarnessRefusesMalformedOrWrongShapeJSON(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":[]}`), 0o644))

	_, err := PlanHarness(context.Background(), root, "test-host")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an object")
}

func TestPlanHarnessPreservesCompetingAndLargeUserValues(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
  "large": 9007199254740993,
  "hooks": {"before": [{"match": "run", "commands": [{"type":"command", "command":"my-own-hook", "statusMessage":"my custom status"}]}]}
}`), 0o644))

	plan := mergeHarness(t, root, "test-host")
	assert.False(t, plan.Current())
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "9007199254740993")
	assert.Contains(t, string(body), "my custom status")
	assert.Contains(t, string(body), "magus-command.buzz")
}

func TestPlanHarnessCanWireReadObserver(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "reader", `{
  "schema_version": 2,
  "id": "reader",
  "display": {"name": "Reader"},
  "config": {"path": "reader/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "full"},
  "managed_entries": [{
    "path": ["hooks", "PreToolUse"],
    "entries": [{"matcher": "Read", "hooks": [{"type": "command", "command": "./magus buzz -s magus-observe.buzz"}]}]
  }]
}`)

	plan := mergeHarness(t, root, "reader")
	assert.False(t, plan.Current())

	body, err := os.ReadFile(filepath.Join(root, "reader/hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus-observe.buzz")

	result, err := VerifyHarness(untimedProbes(), root, "reader")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)
	assert.True(t, result.Guarded)
}

func TestPlanHarnessRejectsDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	const body = `{"hooks":{},"hooks":{}}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	_, err := PlanHarness(context.Background(), root, "test-host")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate JSON object key")
}

// TestPlanHarnessForAMissingFileCreatesIt pins the first-time case: the plan writes nothing,
// and its command creates the directory and the file from the fragment alone.
func TestPlanHarnessForAMissingFileCreatesIt(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	plan, err := PlanHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "test-host"))
	assert.True(t, os.IsNotExist(err), "planning writes nothing")
	require.Contains(t, plan.Files, "test-host/hooks.json")
	assert.False(t, plan.Files["test-host/hooks.json"].Exists)
	assert.Equal(t,
		"magus describe harness test-host -o json | magus buzz -e "+posixQuote(harnessMergeScript),
		plan.Merge)
}

func TestWorkspaceHarnessLoads(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	d, source, err := LoadHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "test-host", d.ID)
	assert.Equal(t, "spell:test-host", source)
}

func TestVerifyHarnessReportsCoverageRatherThanGuessing(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	result, err := VerifyHarness(untimedProbes(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)

	mergeHarness(t, root, "test-host")
	// The wired commands now have to actually answer, not merely be present: give
	// them something real to run.
	writeStubGuardScript(t, root, "magus-command.buzz", "deny")
	writeStubGuardScript(t, root, "magus-path.buzz", "advise")
	result, err = VerifyHarness(untimedProbes(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)
}

func TestPlanHarnessFlatEntriesAndConfigDefaults(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "flat", `{
  "schema_version": 2,
  "id": "flat",
  "display": {"name": "Flat"},
  "config": {"path": "flat/hooks.json"},
  "config_defaults": {"version": 1},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [
    {"path": ["hooks", "beforeShell"], "entries": [{"command": "./magus buzz -s cursor-hook.buzz"}]},
    {"path": ["hooks", "afterTool"], "entries": [{"matcher": "Write", "command": "./magus buzz -s cursor-hook.buzz"}]},
    {"path": ["hooks", "sessionStop"], "entries": [{"command": "./magus buzz -s magus-checkpoint.buzz"}]}
  ]
}`)

	plan := mergeHarness(t, root, "flat")
	assert.Equal(t, types.HarnessChange{Op: types.HarnessSet, Key: "version", Value: float64(1)}, normalizeFiles(t, plan.Files)["flat/hooks.json"].Changes[0])

	body, err := os.ReadFile(filepath.Join(root, "flat/hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"version": 1`)
	assert.Contains(t, string(body), "cursor-hook.buzz")
	assert.Contains(t, string(body), `"beforeShell"`)
	assert.NotContains(t, string(body), `"hooks": [`)

	// cursor-hook.buzz's reply dialect is self-contained (see probeEvent), so the
	// probe only checks that it answers something; magus-checkpoint.buzz renders no
	// verdict at all and is never probed.
	writeStubGuardScript(t, root, "cursor-hook.buzz", "ok")
	result, err := VerifyHarness(untimedProbes(), root, "flat")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status, result.Reason)

	requireCurrent(t, root, "flat")
}

// An object default reaches inside the person's object: the variable lands beside their
// own, and a value they set for it is the one the host keeps.
func TestPlanHarnessSetsAnObjectDefaultKeyByKey(t *testing.T) {
	descriptor := `{
  "schema_version": 2,
  "id": "envhost",
  "display": {"name": "Env Host"},
  "config": {"path": "envhost/settings.json"},
  "config_defaults": {"env": {"MAGUS_LOG_AUDIENCE": "agent"}},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [
    {"path": ["hooks"], "entries": [{"command": "magus buzz -s magus-command.buzz"}]}
  ]
}`
	for _, tc := range []struct {
		name, existing string
		changes        []types.HarnessChange
		env            map[string]any
	}{
		{
			name:     "no env object",
			existing: `{"hooks": [{"command": "magus buzz -s magus-command.buzz"}]}`,
			changes:  []types.HarnessChange{{Op: types.HarnessSet, Key: "env", Value: map[string]any{"MAGUS_LOG_AUDIENCE": "agent"}}},
			env:      map[string]any{"MAGUS_LOG_AUDIENCE": "agent"},
		},
		{
			name:     "env without the variable",
			existing: `{"env": {"FOO": "1"}, "hooks": [{"command": "magus buzz -s magus-command.buzz"}]}`,
			changes:  []types.HarnessChange{{Op: types.HarnessSet, Key: "env.MAGUS_LOG_AUDIENCE", Value: "agent"}},
			env:      map[string]any{"FOO": "1", "MAGUS_LOG_AUDIENCE": "agent"},
		},
		{
			name:     "the person chose human",
			existing: `{"env": {"MAGUS_LOG_AUDIENCE": "human"}, "hooks": [{"command": "magus buzz -s magus-command.buzz"}]}`,
			env:      map[string]any{"MAGUS_LOG_AUDIENCE": "human"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			registerHarnessSpell(t, "envhost", descriptor)
			path := filepath.Join(root, "envhost", "settings.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(tc.existing), 0o644))

			plan := mergeHarness(t, root, "envhost")
			assert.Equal(t, tc.changes, normalizeFiles(t, plan.Files)["envhost/settings.json"].Changes)

			body, err := os.ReadFile(path)
			require.NoError(t, err)
			doc := map[string]any{}
			require.NoError(t, decodeHarnessJSON(body, &doc))
			assert.Equal(t, tc.env, doc["env"])
			requireCurrent(t, root, "envhost")
		})
	}
}

func TestPlanHarnessOwnsManagedEntries(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "managed", `{
  "schema_version": 2,
  "id": "managed",
  "display": {"name": "Managed"},
  "config": {"path": "managed/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "full"},
  "managed_entries": [
    {
      "path": ["hooks", "PreToolUse"],
      "entries": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./magus buzz -s magus-command.buzz"}]}]
    },
    {
      "path": ["hooks", "Stop"],
      "entries": [{"hooks": [{"type": "command", "command": "./magus buzz -s magus-checkpoint.buzz"}]}]
    }
  ]
}`)

	plan := mergeHarness(t, root, "managed")
	assert.False(t, plan.Current())

	// magus-checkpoint.buzz renders no verdict and is never probed; magus-command.buzz
	// is, so it needs something real behind it now.
	writeStubGuardScript(t, root, "magus-command.buzz", "deny")
	result, err := VerifyHarness(untimedProbes(), root, "managed")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, result.Status)

	requireCurrent(t, root, "managed")
}

func TestPlanHarnessReplacesSameIdentityInPlace(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
  "hooks": {
    "before": [{
      "match": "run",
      "commands": [{"type": "command", "command": "./magus buzz -s magus-command.buzz", "statusMessage": "stale status"}]
    }]
  }
}`), 0o644))

	plan := mergeHarness(t, root, "test-host")
	assert.Equal(t, types.HarnessReplace, plan.Files["test-host/hooks.json"].Changes[0].Op)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus guard: checking command")
	assert.NotContains(t, string(body), "stale status")
	assert.Equal(t, 1, strings.Count(string(body), "magus-command.buzz"), "identity match must replace, not append")
}

// TestPlanHarnessRetiresTheEntriesTheOldDescriptorWrote pins the upgrade path.
//
// managedIdentityKey is built from the COMMAND, so a descriptor that rewrites its
// commands matches nothing already in the config, and a merge that only appended
// would leave the old wiring beside the new. Both then fire: every tool call judged
// twice, recorded twice on the activity trail, and answered in part by the version
// the tree just replaced.
//
// The user's own hook survives. Only an entry naming a SHIPPED template came from a
// descriptor, and therefore only one of those may be retired.
func TestPlanHarnessRetiresTheEntriesTheOldDescriptorWrote(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
  "hooks": {
    "before": [
      {"match": "run", "commands": [{"type": "command", "command": "./magus buzz -s old/magus-command.buzz"}]},
      {"match": "write", "commands": [{"type": "command", "command": "./magus buzz -s old/magus-path.buzz"}]},
      {"match": "run", "commands": [{"type": "command", "command": "magus session notify"}]},
      {"match": "run", "commands": [{"type": "command", "command": "my-own-hook"}]}
    ]
  }
}`), 0o644))

	plan := mergeHarness(t, root, "test-host")
	var retired []any
	for _, change := range plan.Files["test-host/hooks.json"].Changes {
		if change.Op == types.HarnessRetire {
			retired = append(retired, change.Value)
		}
	}
	assert.Len(t, retired, 2, "the plan names each entry it retires")

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	doc := string(body)
	assert.NotContains(t, doc, "old/magus-command.buzz", "the previous descriptor's entry is retired, not kept beside the new one")
	assert.NotContains(t, doc, "old/magus-path.buzz")
	assert.Equal(t, 1, strings.Count(doc, "magus-command.buzz"), "exactly one command entry")
	assert.Equal(t, 1, strings.Count(doc, "magus-path.buzz"), "exactly one path entry")
	assert.Contains(t, doc, "magus session notify", "a reader's own magus hook is theirs to keep")
	assert.Contains(t, doc, "my-own-hook")

	requireCurrent(t, root, "test-host")
}

// TestPlanHarnessRetiresOnlyEntriesMarkedAsMagus pins ownership to the two marks a
// descriptor renders, a shipped template's path or types.HarnessOwnedMarker, and nothing a
// command merely says. A marked `magus shell` entry whose command changed is retired
// rather than kept beside its replacement, and so is a second copy of a declared entry. A
// hook of the person's own stays where it was, whether it runs magus or only mentions it.
func TestPlanHarnessRetiresOnlyEntriesMarkedAsMagus(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "env-host", `{
  "schema_version": 2,
  "id": "env-host",
  "display": {"name": "Env Host"},
  "config": {"path": "env-host/hooks.json"},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [{
    "path": ["hooks", "start"],
    "entries": [
      {"match": "start", "commands": [{"type": "command", "command": "magus shell --agent-name env-host # magus:harness"}]},
      {"match": "compact", "commands": [{"type": "command", "command": "magus buzz -s magus-rehydrate.buzz"}]}
    ]
  }]
}`)
	path := filepath.Join(root, "env-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{
  "hooks": {
    "start": [
      {"match": "start", "commands": [{"type": "command", "command": "magus shell -o json # magus:harness"}]},
      {"match": "start", "commands": [{"type": "command", "command": "echo \"remember to rebuild magus\""}]},
      {"match": "start", "commands": [{"type": "command", "command": "magus session notify"}]},
      {"match": "start", "commands": [{"type": "command", "command": "my-own-hook"}]},
      {"match": "compact", "commands": [{"type": "command", "command": "magus buzz -s magus-rehydrate.buzz"}]},
      {"match": "compact", "commands": [{"type": "command", "command": "magus buzz -s magus-rehydrate.buzz"}]}
    ]
  }
}`), 0o644))

	plan := mergeHarness(t, root, "env-host")
	stale := map[string]any{"match": "start", "commands": []any{map[string]any{"type": "command", "command": "magus shell -o json # magus:harness"}}}
	wanted := map[string]any{"match": "start", "commands": []any{map[string]any{"type": "command", "command": "magus shell --agent-name env-host # magus:harness"}}}
	rehydrate := map[string]any{"match": "compact", "commands": []any{map[string]any{"type": "command", "command": "magus buzz -s magus-rehydrate.buzz"}}}
	assert.Equal(t, []types.HarnessChange{
		{Op: types.HarnessAdd, Key: "hooks.start", Value: wanted},
		{Op: types.HarnessRetire, Key: "hooks.start", Value: stale},
		{Op: types.HarnessRetire, Key: "hooks.start", Value: rehydrate},
	}, normalizeFiles(t, plan.Files)["env-host/hooks.json"].Changes)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		Hooks struct {
			Start []map[string]any `json:"start"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	var commands []string
	for _, entry := range doc.Hooks.Start {
		_, cmds := EntryCommands(entry)
		commands = append(commands, cmds...)
	}
	assert.Equal(t, []string{
		`echo "remember to rebuild magus"`,
		"magus session notify",
		"my-own-hook",
		"magus buzz -s magus-rehydrate.buzz",
		"magus shell --agent-name env-host # magus:harness",
	}, commands, "the declared entries once each, and the person's own where they were")

	requireCurrent(t, root, "env-host")
}

func TestCarriesOwnedMarkerOnlyAsATrailingComment(t *testing.T) {
	assert.True(t, carriesOwnedMarker("magus shell # magus:harness"))
	assert.True(t, carriesOwnedMarker("magus shell\t# magus:harness  "))
	assert.False(t, carriesOwnedMarker("magus shell# magus:harness"), "no blank before it: part of a word the shell runs")
	assert.False(t, carriesOwnedMarker("echo '# magus:harness'"), "quoted, so the shell prints it")
	assert.False(t, carriesOwnedMarker("magus shell # magus:harness; rm -rf out"), "a comment ends the line, so the marker does too")
}

func TestHarnessDescriptorRejectsEscapingPathAndNonMagusCommand(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "bad", `{
  "schema_version": 2,
  "id": "bad",
  "display": {"name": "Bad"},
  "config": {"path": "../outside.json"},
  "managed_entries": [{
    "path": ["hooks"],
    "entries": [{"match":"run", "commands":[{"type":"command", "command":"bypass"}]}]
  }]
}`)
	_, _, err := LoadHarness(context.Background(), root, "bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace-relative")

	registerHarnessSpell(t, "bypass", `{
  "schema_version": 2,
  "id": "bypass",
  "display": {"name": "Bypass"},
  "config": {"path": "bypass/hooks.json"},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [{
    "path": ["hooks"],
    "entries": [{"command": "bypass # magus:harness"}]
  }]
}`)
	_, _, err = LoadHarness(context.Background(), root, "bypass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not invoke magus")
}

// TestHarnessDescriptorRefusesAnUnmarkedEntry pins the other half of explicit ownership: a
// declared entry that runs no shipped template and carries no marker would be one a later
// merge could not retire, so the descriptor is refused with the entry named and the fix.
func TestHarnessDescriptorRefusesAnUnmarkedEntry(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "unmarked", `{
  "schema_version": 2,
  "id": "unmarked",
  "display": {"name": "Unmarked"},
  "config": {"path": "unmarked/hooks.json"},
  "skills": {"paths": [], "form": "short"},
  "managed_entries": [{
    "path": ["hooks", "before"],
    "entries": [
      {"match": "run", "commands": [{"type": "command", "command": "magus buzz -s magus-command.buzz"}]},
      {"match": "start", "commands": [{"type": "command", "command": "magus shell -o json"}]}
    ]
  }]
}`)
	_, _, err := LoadHarness(context.Background(), root, "unmarked")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `managed_entries[0]: entries[1] (matcher "start", commands ["magus shell -o json"])`)
	assert.Contains(t, err.Error(), `end its command with " # magus:harness"`)
}

// TestSkillsOnlyHarnessReportsSkillsOnlyNotVerified pins the opencode.json defect:
// a descriptor with no config.path at all wires no guard, and reporting that as
// HarnessVerified (the behavior this test used to assert) is the single most
// misleading verdict this status could give: every deny and advise rule reads
// as enforced when nothing here can enforce anything. It must read as its own,
// distinct status instead.
func TestSkillsOnlyHarnessReportsSkillsOnlyNotVerified(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "skills-only", `{
  "schema_version": 2,
  "id": "skills-only",
  "display": {"name": "Skills Only"},
  "skills": {"paths": [".agents/skills"], "form": "both"}
}`)

	result, err := VerifyHarness(untimedProbes(), root, "skills-only")
	require.NoError(t, err)
	assert.Equal(t, HarnessSkillsOnly, result.Status)
	assert.NotEqual(t, HarnessVerified, result.Status)
	assert.False(t, result.Guarded)
	assert.Empty(t, result.Path)
	assert.NotEmpty(t, result.Reason)

	requireCurrent(t, root, "skills-only")
}

func TestVerifyHarnessRejectsConfigThatDoesNotInvokeMagus(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "empty", `{
  "schema_version": 2,
  "id": "empty",
  "display": {"name": "Empty"},
  "config": {"path": "empty/hooks.json"},
  "skills": {"paths": [], "form": "short"}
}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "empty", "hooks.json"), []byte(`{"hooks":[]}`), 0o644))

	result, err := VerifyHarness(untimedProbes(), root, "empty")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.Status)
	assert.Contains(t, result.Reason, "does not invoke magus")

	_, err = PlanHarness(context.Background(), root, "empty")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not invoke magus")
}

func TestInvokesMagusRejectsGenericGuardScripts(t *testing.T) {
	assert.False(t, invokesMagus("sh host-guard.sh"))
	assert.False(t, invokesMagus("echo shell"))
	assert.True(t, invokesMagus("magus buzz -s docs/guides/integrations/agents/cursor-hook.buzz"))
	assert.True(t, invokesMagus("magus shell -o json"))
	assert.True(t, invokesMagus("./magus session notify"))
}

// TestKnownHarnessesReturnsWiredIDsDedupedAndSorted replaces the old union-with-JSON
// test: wired is the only source of an id now that harnesses/*.json is gone, so an
// id that nobody wired is simply not known, spell or not.
func TestKnownHarnessesReturnsWiredIDsDedupedAndSorted(t *testing.T) {
	ids, err := KnownHarnesses(context.Background())
	require.NoError(t, err)
	assert.Empty(t, ids, "nothing is known without an explicit wired id")

	// nil / no wired args: still empty. Distinct from a blank entry in wired.
	ids, err = KnownHarnesses(context.Background(), nil...)
	require.NoError(t, err)
	assert.Empty(t, ids)

	ids, err = KnownHarnesses(context.Background(), "cursor", "test-host", "codex", "cursor")
	require.NoError(t, err)
	assert.Equal(t, []string{"codex", "cursor", "test-host"}, ids)
}

func TestKnownHarnessesRejectsEmptyWiredID(t *testing.T) {
	_, err := KnownHarnesses(context.Background(), "cursor", "", "codex")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")

	_, err = KnownHarnesses(context.Background(), "  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestLoadHarnessSpellOnlyWhenWired(t *testing.T) {
	root := t.TempDir()

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
				Entries: []map[string]any{{"command": "magus shell # magus:harness"}},
			}},
		}, "spell:test-host", true, nil
	}

	// Unset context: registered spell still wins (legacy / tests).
	d, src, err := LoadHarness(context.Background(), root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "spell:test-host", src)
	assert.Equal(t, "spell/hooks.json", d.Config.Path)

	// Wired without this id: the spell is not even tried, and there is no JSON
	// fallback left to catch it, so the id is simply unknown.
	ctx := ContextWithWiredHarnesses(context.Background(), []string{"cursor"})
	_, _, err = LoadHarness(ctx, root, "test-host")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no harness named "test-host"`)

	// Wired including this id: spell wins.
	ctx = ContextWithWiredHarnesses(context.Background(), []string{"test-host"})
	d, src, err = LoadHarness(ctx, root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "spell:test-host", src)
	assert.Equal(t, "spell/hooks.json", d.Config.Path)
}

// writeStubGuardScript registers output as the answer to the glue file called name,
// and puts a stub magus at root/magus that drains stdin and prints the answer
// registered for whichever glue file its argv names. It stands in for magus running
// the shipped glue, so VerifyHarness's probe (harness_probe.go) has something real
// to execute when a harness wires `./magus buzz -s <glue>`. Tests that only exercise
// PlanHarness's own merge mechanics do not need this; only a test that calls
// VerifyHarness against a command probeHarnessCommands recognizes as guard-shaped
// does. See harness_probe_test.go for the probe's own tests.
func writeStubGuardScript(t *testing.T, root, name, output string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "stub-"+name), []byte(output), 0o644))
	const stub = "#!/bin/sh\ncat >/dev/null\nfor a in \"$@\"; do [ -f \"stub-$a\" ] && exec cat \"stub-$a\"; done\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte(stub), 0o755))
}

// writeTestHarness registers the "test-host" fixture as a fake harness spell. root
// is accepted only so every existing call site (this package and catalog_test.go)
// need not change; the descriptor no longer lives under it.
func writeTestHarness(t *testing.T, root string) {
	t.Helper()
	registerHarnessSpell(t, "test-host", `{
  "schema_version": 2,
  "id": "test-host",
  "display": {"name": "Test Host", "color": "#111111"},
  "config": {"path": "test-host/hooks.json"},
  "skills": {"paths": [".agents/skills"], "form": "full"},
  "managed_entries": [{
    "path": ["hooks", "before"],
    "entries": [
      {"match": "run", "commands": [{"type": "command", "command": "./magus buzz -s magus-command.buzz", "statusMessage": "magus guard: checking command"}]},
      {"match": "write", "commands": [{"type": "command", "command": "./magus buzz -s magus-path.buzz", "timeout": 10}]}
    ]
  }]
}`)
}

const promptRules = "prefix_rule(pattern = [\"git\", \"push\"], decision = \"prompt\")\n"

// writePromptHarness registers a skills-only descriptor that keeps two host-native approval
// prompts: a whole rules file, and one key inside a JSON config the person also edits.
func writePromptHarness(t *testing.T, root string) {
	t.Helper()
	registerHarnessSpell(t, "prompter", `{
  "schema_version": 2,
  "id": "prompter",
  "display": {"name": "Prompter"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "prompts": [
    {"path": ".codex/rules/magus.rules", "content": `+strconvQuote(promptRules)+`},
    {"path": "opencode.json", "key": ["permission", "bash", "git push *"], "value": "ask"}
  ]
}`)
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestPlanHarnessPlansNativePrompts pins that the plan puts the host's own approval prompt in
// place and leaves the rest of a shared config alone, and that verify then reports it.
func TestPlanHarnessPlansNativePrompts(t *testing.T) {
	root := t.TempDir()
	writePromptHarness(t, root)
	config := filepath.Join(root, "opencode.json")
	require.NoError(t, os.WriteFile(config, []byte(`{"model": "m", "permission": {"edit": "ask"}}`), 0o644))

	before, err := VerifyHarness(untimedProbes(), root, "prompter")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, before.PromptStatus)
	assert.Contains(t, before.PromptReason, ".codex/rules/magus.rules")

	plan := mergeHarness(t, root, "prompter")
	assert.Equal(t, map[string]types.HarnessFile{
		".codex/rules/magus.rules": {Content: promptRules, Changes: []types.HarnessChange{{Op: types.HarnessWrite}}},
		"opencode.json": {
			Exists:   true,
			Fragment: map[string]any{"permission": map[string]any{"bash": map[string]any{"git push *": "ask"}}},
			Changes:  []types.HarnessChange{{Op: types.HarnessSet, Key: "permission.bash.git push *", Value: "ask"}},
		},
	}, plan.Files)

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

	requireCurrent(t, root, "prompter")

	after, err := VerifyHarness(untimedProbes(), root, "prompter")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, after.PromptStatus)
	assert.Empty(t, after.PromptReason)
}

// TestPlanHarnessRefusesAPromptThePersonOverrode pins that no plan overwrites a value someone
// chose, and that it says which one: the prompt is missing either way, and silence would
// leave every ungated push refused with no clue why.
func TestPlanHarnessRefusesAPromptThePersonOverrode(t *testing.T) {
	root := t.TempDir()
	writePromptHarness(t, root)
	config := filepath.Join(root, "opencode.json")
	const chosen = `{"permission": {"bash": {"git push *": "allow"}}}`
	require.NoError(t, os.WriteFile(config, []byte(chosen), 0o644))

	_, err := PlanHarness(context.Background(), root, "prompter")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission.bash.git push *")

	result, err := VerifyHarness(untimedProbes(), root, "prompter")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, result.PromptStatus)
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

// Conformance gates for the agent-host integration files, against the hosts' OWN
// schemas where a host publishes one.
//
// The hook gates in guard_test.go assert that these files exist, are
// embedded in their page, and declare a stance for every decision. None of them
// asks the question a user cares about first: would the host actually LOAD this?
// The agents magusfile named that gap in its own words: "the event SHAPES in the
// fixtures are recorded from each host's documentation, so a host silently renaming
// a field is still invisible here". This is what closes it. A schema is the one
// artifact that catches a rename, because it was written by the host.
//
// Two directions are graded. The CONFIG direction takes every hooks config magus
// ships or embeds and validates it against the host's config schema. The OUTPUT
// direction, in guard_test.go, takes the JSON the shipped sh templates print, rendered
// from the template bodies in those files so it is the real bytes and not a copy, and
// validates it against the host's hook-stdout schema.
//
// Nothing here reaches the network. testdata/hosts holds vendored copies and
// records the provenance of each; hack/dev/host-schemas.buzz is what refreshes them.

const hostSchemaDir = repoRoot + "/testdata/hosts"

// hostConfigSchema names the schema every hooks config a host reads is graded against.
// A host absent from this map is a host whose config nothing checks, which is the
// state this file exists to end.
var hostConfigSchema = map[string]string{
	"claude-code": "claude-code/settings.schema.json",
	"codex":       "codex/hooks.schema.json",
	"cursor":      "cursor/hooks.schema.json",
}

// hostConfigFile names the config files magus SHIPS for a host, as opposed to the
// blocks its pages embed. Cursor has none: its page tells a reader to write the file.
var hostConfigFile = map[string][]string{
	"claude-code": {dogfoodedHookConfig},
	"codex":       {filepath.Join(hookTemplateDir, "codex-hooks.json")},
	"cursor":      {repoRoot + "/.cursor/hooks.json"},
}

// upstreamConfigDir holds configs the HOST's own people wrote, vendored from
// github.com/cursor/plugins (the advisor, ralph-loop and continual-learning plugins).
//
// Every other input to these schemas is something magus produced, and a schema graded
// only against its author's own output cannot fail: wrong in the same direction as the
// thing it grades, it passes forever. These are the independent half. They exercise
// fields magus never writes -- `loop_limit`, including its null form -- and they are the
// only evidence here that the schema matches what Cursor actually loads rather than what
// magus happens to emit.
//
// Refresh them when Cursor's plugin repository moves; a rejection here is a finding about
// OUR schema, never about their config.
const upstreamConfigDir = hostSchemaDir + "/cursor/upstream-configs"

// hostGuidePage names the page whose embedded JSON configures each host.
var hostGuidePage = map[string]string{
	"claude-code": "claude-code.md",
	"codex":       "codex.md",
	"cursor":      "cursor.md",
}

// jsonCodeBlock matches a fenced json block on a guide page.
var jsonCodeBlock = regexp.MustCompile("(?ms)^```json\r?\n(.*?)^```")

// loadHostSchema reads a vendored schema and prepares it for validation.
func loadHostSchema(t *testing.T, rel string) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hostSchemaDir, rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(raw, &schema), "%s must parse as a JSON Schema", rel)
	// nil loader on purpose: a schema that grew a remote $ref would fail here rather
	// than turn every run of this test into an HTTP request.
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err, "%s must resolve with no loader; a remote $ref would make this test fetch", rel)
	return resolved
}

// decodeJSON unmarshals into the shape jsonschema-go validates.
func decodeJSON(t *testing.T, label, body string) any {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal([]byte(body), &doc), "%s must be valid JSON", label)
	return doc
}

// hooksConfigBlocks returns the fenced json blocks on a page that configure hooks. A
// page may carry JSON for something else (an MCP registration, say), so the selector is
// the presence of a top-level "hooks" key rather than the fence.
func hooksConfigBlocks(t *testing.T, page string) []string {
	t.Helper()
	body, err := os.ReadFile(page)
	require.NoError(t, err, "read %s", page)
	var configs []string
	for _, match := range jsonCodeBlock.FindAllStringSubmatch(string(body), -1) {
		doc, ok := decodeJSON(t, page, match[1]).(map[string]any)
		if !ok {
			continue
		}
		if _, isConfig := doc["hooks"]; isConfig {
			configs = append(configs, match[1])
		}
	}
	return configs
}

// TestShippedHookConfigsValidateAgainstTheirHostSchema grades every hooks config magus
// ships or publishes against the schema its host reads.
//
// The page blocks are covered alongside the files because they are what most readers
// install: a reader copies the block, not the repository's own settings.
func TestShippedHookConfigsValidateAgainstTheirHostSchema(t *testing.T) {
	for host, schemaFile := range hostConfigSchema {
		t.Run(host, func(t *testing.T) {
			schema := loadHostSchema(t, schemaFile)

			var graded int
			for _, file := range hostConfigFile[host] {
				body, err := os.ReadFile(file)
				require.NoError(t, err, "read %s", file)
				require.NoError(t, schema.Validate(decodeJSON(t, file, string(body))),
					"%s is not a config %s would load, per %s", file, host, schemaFile)
				graded++
			}

			page := filepath.Join(hookTemplateDir, hostGuidePage[host])
			for i, block := range hooksConfigBlocks(t, page) {
				require.NoError(t, schema.Validate(decodeJSON(t, page, block)),
					"%s json block %d is not a config %s would load, per %s", page, i, host, schemaFile)
				graded++
			}

			assert.Positive(t, graded,
				"nothing was graded for %s: either its page stopped embedding a hooks config or the\n"+
					"fence stopped saying json, and either way this gate went quiet rather than red", host)
		})
	}
}

// TestCursorSchemaAcceptsCursorsOwnConfigs grades our Cursor schema against configs
// Cursor's own people wrote, which is the only input here magus did not produce.
//
// The sibling test above proves the schema accepts what magus writes. That is compatible
// with the schema being wrong, because magus writes a narrow subset: a rejection of a real
// config is invisible to it. This is the half that catches a schema too strict to load
// what the host actually loads -- the direction a hand transcription fails in, since a
// reader transcribing a validator records the branches they happened to read.
func TestCursorSchemaAcceptsCursorsOwnConfigs(t *testing.T) {
	schema := loadHostSchema(t, hostConfigSchema["cursor"])

	entries, err := os.ReadDir(upstreamConfigDir)
	require.NoError(t, err, "read %s", upstreamConfigDir)
	require.NotEmpty(t, entries,
		"no upstream configs vendored: this gate went quiet rather than red, which is the\n"+
			"failure it exists to prevent")

	for _, entry := range entries {
		file := filepath.Join(upstreamConfigDir, entry.Name())
		body, err := os.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		require.NoError(t, schema.Validate(decodeJSON(t, file, string(body))),
			"%s is a config Cursor ships and our schema rejects it, so the schema is wrong", file)
	}
}

// TestClaudeCodeAndCursorSchemasRejectAnUnknownHookEvent proves the config schemas bite.
//
// A gate that only ever sees valid input cannot tell a strict schema from an empty one,
// and an empty one is exactly what a mis-parsed schema degrades into.
func TestClaudeCodeAndCursorSchemasRejectAnUnknownHookEvent(t *testing.T) {
	cases := map[string]string{
		"claude-code": `{"hooks":{"PreToolUseTypo":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
		"cursor":      `{"version":1,"hooks":{"beforeShellExecutionTypo":[{"command":"true"}]}}`,
	}
	for host, body := range cases {
		t.Run(host, func(t *testing.T) {
			schema := loadHostSchema(t, hostConfigSchema[host])
			assert.Error(t, schema.Validate(decodeJSON(t, host, body)),
				"%s's schema accepted an event name that host has never heard of, so it would\n"+
					"accept a typo in a shipped config too", host)
		})
	}
}

// TestCodexHookEventsAreNamedByItsSchema closes the one hole in Codex's published schema.
//
// Its `hooks` object takes additionalProperties, so `PreToolUseTypo` validates and the
// hook simply never fires, which is the silent failure this whole file exists to
// catch. The schema still NAMES every event Codex supports, so the check magus makes is
// every event it ships is one of those.
func TestCodexHookEventsAreNamedByItsSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(hostSchemaDir, hostConfigSchema["codex"]))
	require.NoError(t, err)

	var schema struct {
		Properties struct {
			Hooks struct {
				Properties map[string]any `json:"properties"`
			} `json:"hooks"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	known := schema.Properties.Hooks.Properties
	require.NotEmpty(t, known, "the codex schema must name the events it supports")
	require.NotContains(t, known, "PreToolUseTypo",
		"the event list read out of the schema is the wrong one if a made-up name is in it")

	for _, file := range hostConfigFile["codex"] {
		body, err := os.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		var config struct {
			Hooks map[string]any `json:"hooks"`
		}
		require.NoError(t, json.Unmarshal(body, &config))
		require.NotEmpty(t, config.Hooks, "%s registers no hooks", file)
		for event := range config.Hooks {
			assert.Contains(t, known, event,
				"%s registers %q, which the Codex hooks schema does not name. Codex accepts an\n"+
					"unknown event without complaint and then never fires it, so this is the only\n"+
					"place a rename or a typo can surface.", file, event)
		}
	}
}

// harnessSpellName reads the one identity a harness spell declares, its mgs_getName().
func harnessSpellName(t *testing.T, id string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot, "spells", "harness", id, "spell.buzz"))
	require.NoError(t, err)
	m := regexp.MustCompile(`export fun mgs_getName\(\) > str \{ return "([^"]+)"; \}`).FindSubmatch(body)
	require.NotNil(t, m, "spells/harness/%s declares no mgs_getName", id)
	return string(m[1])
}

// namedGlue matches a hook command that reaches glue which reads its host from the argv.
// rehydrate reads none, and is left out on purpose.
var namedGlue = regexp.MustCompile(`magus-(command|path|observe|checkpoint)\.buzz|cursor-hook\.buzz`)

// hookCommands collects every "command" string in a host's hook config.
func hookCommands(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc any
	require.NoError(t, json.Unmarshal(body, &doc), "parse %s", path)
	var out []string
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if cmd, ok := v["command"].(string); ok {
				out = append(out, cmd)
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(doc)
	return out
}

// TestShippedHostConfigsNameTheirHost pins the host name onto every glue command a shipped
// config carries, as the harness spell's own mgs_getName() renders it. The glue reads the
// host from that argument and nowhere else, and refuses a call without it (MGS3024), so a
// config that lost it would refuse every call; one naming another host would answer in
// that host's dialect. docs/doctrine.md, "Told, never guessed".
func TestShippedHostConfigsNameTheirHost(t *testing.T) {
	for _, tc := range []struct{ id, config string }{
		{"claude-code", filepath.Join(repoRoot, ".claude", "settings.json")},
		{"codex", filepath.Join(repoRoot, ".codex", "hooks.json")},
		{"codex", filepath.Join(hookTemplateDir, "codex-hooks.json")},
		{"cursor", filepath.Join(repoRoot, ".cursor", "hooks.json")},
	} {
		want := "--agent-name " + harnessSpellName(t, tc.id)
		var glue int
		for _, cmd := range hookCommands(t, tc.config) {
			if !namedGlue.MatchString(cmd) {
				continue
			}
			glue++
			assert.Contains(t, cmd, want, "%s: a glue command must name its host", tc.config)
			assert.Equal(t, 1, strings.Count(cmd, "--agent-name"), "%s: one host per command: %s", tc.config, cmd)
			assert.NotContains(t, cmd, "AGENT_NAME=", "%s: the host rides on argv, never in the environment", tc.config)
		}
		assert.NotZero(t, glue, "%s wires no glue this test recognizes; the matcher stopped matching", tc.config)
	}
}

// TestOpenCodePluginNamesTheSpellsHost ties the one host name magus writes by hand, the
// OpenCode plugin's, to the harness spell that declares it. The plugin is TypeScript that
// magus does not render, so this is what keeps the two from drifting apart.
func TestOpenCodePluginNamesTheSpellsHost(t *testing.T) {
	name := harnessSpellName(t, "opencode")
	body, err := os.ReadFile(filepath.Join(hookTemplateDir, "opencode-plugin.ts"))
	require.NoError(t, err)
	named := regexp.MustCompile(`"--agent-name",\s*"([^"]*)"`).FindAllStringSubmatch(string(body), -1)
	require.NotEmpty(t, named, "the plugin passes no --agent-name literal; the matcher stopped matching")
	for _, m := range named {
		assert.Equal(t, name, m[1], "opencode-plugin.ts names a host its harness spell does not declare")
	}
}

const agentFileBody = "---\ndescription: \"x\"\n---\n\nbody\n"

func writeAgentHarness(t *testing.T) {
	t.Helper()
	registerHarnessSpell(t, "scribe", `{
  "schema_version": 2,
  "id": "scribe",
  "display": {"name": "Scribe"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "agents": [
    {"path": ".scribe/agents/magus-scout.md", "content": `+strconvQuote(agentFileBody)+`}
  ]
}`)
}

// TestPlanHarnessWritesAgentFilesOnceAndThenNothing pins that a subagent file is a whole file
// the descriptor owns: written when the bytes differ, left alone when they match.
func TestPlanHarnessWritesAgentFilesOnceAndThenNothing(t *testing.T) {
	root := t.TempDir()
	writeAgentHarness(t)

	before, err := VerifyHarness(untimedProbes(), root, "scribe")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, before.AgentStatus)
	assert.Contains(t, before.AgentReason, ".scribe/agents/magus-scout.md")

	plan := mergeHarness(t, root, "scribe")
	assert.Equal(t, map[string]types.HarnessFile{
		".scribe/agents/magus-scout.md": {Content: agentFileBody, Changes: []types.HarnessChange{{Op: types.HarnessWrite}}},
	}, plan.Files)

	written, err := os.ReadFile(filepath.Join(root, ".scribe/agents/magus-scout.md"))
	require.NoError(t, err)
	assert.Equal(t, agentFileBody, string(written))
	requireCurrent(t, root, "scribe")

	after, err := VerifyHarness(untimedProbes(), root, "scribe")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, after.AgentStatus)
	assert.Empty(t, after.AgentReason)
}

// A spell that leaves an agent's model to the host names the person's setting once, however
// many agents share it.
func TestPlanHarnessCarriesEachAgentHintOnce(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "scribe", `{
  "schema_version": 2,
  "id": "scribe",
  "display": {"name": "Scribe"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "agents": [
    {"path": ".scribe/agents/a.md", "content": "a\n", "hint": "set scribe.default_model"},
    {"path": ".scribe/agents/b.md", "content": "b\n", "hint": "set scribe.default_model"},
    {"path": ".scribe/agents/c.md", "content": "c\n"}
  ]
}`)
	plan, err := PlanHarness(context.Background(), root, "scribe")
	require.NoError(t, err)
	assert.Equal(t, []string{"set scribe.default_model"}, plan.AgentHints)
}

// TestPlanHarnessRewritesAnEditedAgentFile pins that the descriptor owns the whole file, so a
// local edit is replaced rather than merged.
func TestPlanHarnessRewritesAnEditedAgentFile(t *testing.T) {
	root := t.TempDir()
	writeAgentHarness(t)
	mergeHarness(t, root, "scribe")
	path := filepath.Join(root, ".scribe/agents/magus-scout.md")
	require.NoError(t, os.WriteFile(path, []byte("edited\n"), 0o644))

	plan, err := PlanHarness(context.Background(), root, "scribe")
	require.NoError(t, err)
	file := plan.Files[".scribe/agents/magus-scout.md"]
	assert.True(t, file.Exists)
	assert.Equal(t, agentFileBody, file.Content)
}

func TestHarnessDescriptorWithoutAgentsPlansAndVerifiesNone(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "plain", `{
  "schema_version": 2,
  "id": "plain",
  "display": {"name": "Plain"},
  "skills": {"paths": [".agents/skills"], "form": "both"}
}`)
	requireCurrent(t, root, "plain")
	result, err := VerifyHarness(untimedProbes(), root, "plain")
	require.NoError(t, err)
	assert.Empty(t, result.AgentStatus)
}

func TestHarnessDescriptorRejectsAMalformedAgentFile(t *testing.T) {
	base := HarnessDescriptor{SchemaVersion: harnessSchemaVersion, ID: "p", Display: HarnessDisplay{Name: "P"}, Skills: HarnessSkills{Form: "both"}}
	for name, file := range map[string]HarnessAgentFile{
		"no path":      {Content: "x"},
		"absolute":     {Path: "/etc/agents/a.md", Content: "x"},
		"escapes root": {Path: "../a.md", Content: "x"},
		"no content":   {Path: ".a/agents/a.md"},
	} {
		t.Run(name, func(t *testing.T) {
			d := base
			d.Agents = []HarnessAgentFile{file}
			assert.Error(t, validateHarnessDescriptor(d))
		})
	}
	ok := base
	ok.Agents = []HarnessAgentFile{{Path: ".a/agents/a.md", Content: "x"}}
	assert.NoError(t, validateHarnessDescriptor(ok))
}
