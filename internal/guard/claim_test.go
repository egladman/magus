package guard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const claimedGo = `package run

func A() {
	a()
}

func B() {
	b()
}

func C() {
	c()
}
`

// claimFixture is a git checkout holding run.go under the golang driver, with a plan where
// own-a claims run.go#A and the writer, own-b, claims run.go#B.
func claimFixture(t *testing.T, extra ...types.Job) (context.Context, string) {
	t.Helper()
	leases := []types.Job{claimLease("own-a", "run.go#A"), claimLease("own-b", "run.go#B")}
	ctx, root := fleetFixture(t, append(leases, extra...)...)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.go diff=golang\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "run.go"), []byte(claimedGo), 0o644))
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return ctx, root
}

func claimLease(id string, paths ...string) types.Job {
	return types.Job{
		ID: id, Criteria: "work on " + id, WritePaths: paths, State: types.StateRunning,
		Checkpoint: "rev", ReportedBase: "rev", BaseVerdict: types.BaseMatch, Registered: 1,
	}
}

func TestGradeClaimedDeclarations(t *testing.T) {
	ctx, root := claimFixture(t, claimLease("own-file", "other.go", "run.go"))
	run := filepath.Join(root, "run.go")
	edit := func(oldText, newText string) writeFields { return writeFields{OldText: oldText, NewText: newText} }
	for _, tc := range []struct {
		name   string
		lease  string
		fields writeFields
		denied bool
	}{
		{name: "inside its own claim", lease: "own-b", fields: edit("b()", "b2()")},
		{name: "into another job's claim", lease: "own-b", fields: edit("a()", "a2()"), denied: true},
		{name: "into a declaration nobody claims", lease: "own-b", fields: edit("c()", "c2()")},
		{name: "above the first declaration", lease: "own-b", fields: edit("package run\n", "package run\n\nimport \"fmt\"\n")},
		{name: "a new function after its own", lease: "own-b", fields: edit("\tb()\n}\n", "\tb()\n}\n\nfunc B2() {\n}\n")},
		{name: "every edit of a sequence is placed", lease: "own-b", fields: writeFields{Edits: []textEdit{
			{OldText: "b()", NewText: "b2()"}, {OldText: "a()", NewText: "a2()"},
		}}, denied: true},
		{name: "a whole-file write keeps the path verdict", lease: "own-b", fields: writeFields{Content: "package run\n"}},
		{name: "an edit that does not apply keeps the path verdict", lease: "own-b", fields: edit("absent()", "x()")},
		{name: "an ambiguous edit keeps the path verdict", lease: "own-b", fields: edit("()", "(x)")},
		{name: "a writer holding the whole file", lease: "own-file", fields: edit("a()", "a2()")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := gradeLeasedEdit(ctx, Dependencies{}, tc.lease, run, tc.fields)
			if !tc.denied {
				assert.Empty(t, got.Decision, got.Reason)
				return
			}
			require.Equal(t, "deny", got.Decision)
			assert.Equal(t, string(denyRuleClaimedDeclaration), got.Rule)
			assert.Contains(t, got.Reason, "which lease own-a (work on own-a) claims as run.go#A")
			assert.Contains(t, got.Reason, "This edit changes func A() {")
			assert.Contains(t, got.Reason, "`magus job exit own-a`")
		})
	}
}

// The real hook envelope, as Claude Code sends it for an edit, reaches the declaration rule
// through Judge.
func TestJudgeDeniesAnEditIntoAnotherJobsDeclaration(t *testing.T) {
	ctx, root := claimFixture(t)
	run := filepath.Join(root, "run.go")
	judge := func(toolInput map[string]any) Verdict {
		return Judge(ctx, Dependencies{}, Request{Host: "claude-code", Lease: "own-b", Input: hookJSON(t, map[string]any{
			"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Edit", "cwd": root,
			"tool_input": toolInput,
		})})
	}

	v := judge(map[string]any{"file_path": run, "old_string": "a()", "new_string": "a2()"})
	require.Equal(t, "deny", v.Decision)
	assert.Equal(t, string(denyRuleClaimedDeclaration), v.Rule)
	assert.Contains(t, v.Reason, "claims as run.go#A")

	multi := judge(map[string]any{"file_path": run, "edits": []any{
		map[string]any{"old_string": "b()", "new_string": "b2()"},
		map[string]any{"old_string": "a()", "new_string": "a2()", "replace_all": true},
	}})
	assert.Equal(t, "deny", multi.Decision, "every edit of a list is placed")

	assert.NotEqual(t, "deny", judge(map[string]any{"file_path": run, "old_string": "b()", "new_string": "b2()"}).Decision)
	assert.NotEqual(t, "deny", judge(map[string]any{"file_path": run, "content": "package run\n"}).Decision,
		"a whole-file write carries no edit to place, and the writer's claim covers the file")
}

func TestApplyEdits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		fields writeFields
		want   string
		ok     bool
	}{
		{"one replacement", writeFields{OldText: "b", NewText: "B"}, "aBca", true},
		{"no edit at all", writeFields{Content: "x"}, "", false},
		{"absent text", writeFields{OldText: "z", NewText: "Z"}, "", false},
		{"ambiguous text", writeFields{OldText: "a", NewText: "A"}, "", false},
		{"ambiguous text replaced everywhere", writeFields{OldText: "a", NewText: "A", ReplaceAll: true}, "AbcA", true},
		{"a sequence, each on the result of the last", writeFields{Edits: []textEdit{{OldText: "b", NewText: "bb"}, {OldText: "bb", NewText: "X"}}}, "aXca", true},
		{"a sequence with one that fails", writeFields{Edits: []textEdit{{OldText: "b", NewText: "B"}, {OldText: "z", NewText: "Z"}}}, "", false},
	} {
		got, ok := applyEdits("abca", tc.fields)
		assert.Equal(t, tc.ok, ok, tc.name)
		if tc.ok {
			assert.Equal(t, tc.want, got, tc.name)
		}
	}
}
