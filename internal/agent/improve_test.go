package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImprovementCandidateRemainsAProposal(t *testing.T) {
	candidate := improvementCandidateFor(trail.GuardFeedback{
		Rule: "raw-tool", Surface: "shell.command", Denied: 3, Sessions: 1,
		FollowedSessions: 1, Evidence: []string{"test-host:session-1 (3 denials)"},
	})
	assert.Equal(t, "harness descriptor, magus-local-development, or upstream-bug", candidate.Destination)
	assert.Equal(t, "high", candidate.Confidence)
	assert.Contains(t, ImproveDefinition, "cannot prove execution or success")
}

func TestApplyHarnessAddsOnlyMagusHookAlongsideUserHooks(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	const original = "{\n  \"other\": {\"keep\": true},\n  \"hooks\": {\n    \"before\": [{\"match\": \"run\", \"commands\": [{\"type\": \"command\", \"command\": \"my-own-hook\"}]}]\n  }\n}\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))

	update, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "my-own-hook")
	assert.Contains(t, string(body), "magus agent hook --host test-host")
	assert.Contains(t, string(body), "\"other\": {")

	second, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host"})
	require.NoError(t, err)
	assert.False(t, second.Changed, "a second apply must be idempotent")
}

func TestApplyHarnessRefusesMalformedOrWrongShapeJSON(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"hooks":[]}`), 0o644))

	_, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host"})
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
  "hooks": {"before": [{"match": "run", "commands": [{"type":"command", "command":"magus agent hook --host test-host", "statusMessage":"my custom status"}]}]}
}`), 0o644))

	update, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host"})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "9007199254740993")
	assert.Contains(t, string(body), "my custom status")
	assert.Contains(t, string(body), "magus guard: checking command")
}

func TestApplyHarnessRejectsDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	path := filepath.Join(root, "test-host", "hooks.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	const body = `{"hooks":{},"hooks":{}}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	_, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate JSON object key")
	got, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, body, string(got), "a rejected document must not be rewritten")
}

func TestApplyHarnessDryRunPlansWithoutWriting(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	update, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host", DryRun: true})
	require.NoError(t, err)
	assert.True(t, update.Changed)
	assert.True(t, update.Planned)
	_, err = os.Stat(filepath.Join(root, "test-host", "hooks.json"))
	assert.True(t, os.IsNotExist(err))
}

func TestApplyHarnessRejectsBoundLease(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	_, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host", ActingLease: "job-123"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bound job")
}

func TestWorkspaceHarnessLoads(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	d, source, err := LoadHarness(root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "test-host", d.ID)
	assert.Equal(t, filepath.Join(root, "harnesses", "test-host.json"), source)
}

func TestVerifyHarnessReportsCoverageRatherThanGuessing(t *testing.T) {
	root := t.TempDir()
	writeTestHarness(t, root)
	result, err := VerifyHarness(root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "uncovered", result.Status)

	_, err = ApplyHarness(HarnessApplyOptions{Root: root, Host: "test-host"})
	require.NoError(t, err)
	result, err = VerifyHarness(root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, "verified", result.Status)
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
  "pre_tool_use": {
    "path": ["hooks", "PreToolUse"], "matcher_key": "matcher", "hooks_key": "hooks",
    "response_template": "{{toJson .reason}}",
    "entries": [{"matcher": "Bash", "hook": {"type": "command"}}]
  },
  "managed_entries": [{
    "path": ["hooks", "Stop"],
    "entries": [{"hooks": [{"type": "command", "command": "magus session checkpoint"}]}]
  }]
}`), 0o644))

	update, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "managed"})
	require.NoError(t, err)
	assert.True(t, update.Changed)

	result, err := VerifyHarness(root, "managed")
	require.NoError(t, err)
	assert.Equal(t, "verified", result.Status)

	second, err := ApplyHarness(HarnessApplyOptions{Root: root, Host: "managed"})
	require.NoError(t, err)
	assert.False(t, second.Changed)
}

func TestHarnessDescriptorRejectsEscapingPathAndClaimedCommand(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{
  "schema_version": 2,
  "id": "bad",
  "display": {"name": "Bad"},
  "config": {"path": "../outside.json"},
  "pre_tool_use": {
    "path": ["hooks"], "matcher_key": "match", "hooks_key": "commands",
    "response_template": "x",
    "entries": [{"matcher":"run", "hook":{"type":"command", "command":"bypass"}}]
  }
}`), 0o644))
	_, _, err := LoadHarness(root, "bad")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace-relative")
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
  "pre_tool_use": {
    "path": ["hooks", "before"],
    "matcher_key": "match",
    "hooks_key": "commands",
    "response_template": "{{if eq .decision \"deny\"}}{\"blocked\":{{toJson .reason}}}{{else if eq .decision \"advise\"}}{\"context\":{{toJson .context}}}{{end}}",
    "entries": [
      {"matcher": "run", "hook": {"type": "command", "statusMessage": "magus guard: checking command"}},
      {"matcher": "write", "hook": {"type": "command", "timeout": 10}}
    ]
  }
}`), 0o644))
}
