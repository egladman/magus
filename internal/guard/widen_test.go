package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widenFixture is a running job that released registry.go, which its verification then
// sends it back to.
func widenFixture(t *testing.T) (context.Context, string, types.Job) {
	t.Helper()
	row := types.Job{
		ID:         "cli-ls-files",
		Criteria:   "add vcs ls-files",
		State:      types.StateRunning,
		WritePaths: []string{"cmd/magus/vcs.go", "internal/commands/registry.go"},
		Registered: 1,
	}
	ctx, root := fleetFixture(t, row)
	for _, rel := range []string{"cmd/magus/vcs.go", "internal/commands/registry.go", "README.md"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("package x\n"), 0o644))
	}
	held := job.NewStore(job.Location{
		CacheDir: hookLocation(ctx, Dependencies{}).cacheDir, StateBase: fixture.StateBase(t), Root: root,
		Actor: &job.Actor{Lease: row.ID},
	})
	stored, err := held.Update(ctx, row.ID, func(u *types.Job) { u.WritePaths = []string{"cmd/magus/vcs.go"} })
	require.NoError(t, err)
	require.Len(t, stored.Releases, 1)
	return ctx, root, stored
}

func TestGradeLeasedWriteServesAProvedWidening(t *testing.T) {
	ctx, root, row := widenFixture(t)

	got := gradeLeasedWrite(ctx, Dependencies{}, row.ID, filepath.Join(root, "internal/commands/registry.go"))
	// The reason is checked by substring below, so it is copied across.
	require.Equal(t, writeGrade{Decision: "deny", Reason: got.Reason, Rule: string(denyRuleLeaseWiden), Next: got.Next}, got)
	require.Len(t, got.Next, 1)
	assert.Equal(t, "deny-lease-widen", got.Next[0].ID)
	assert.Contains(t, got.Next[0].Run, "job apply -f - <<<")
	assert.Contains(t, got.Next[0].Run, `"widen":[{"path":"internal/commands/registry.go","because":"released"}]`)
	say, why, _ := strings.Cut(got.Reason, "\n")
	assert.Contains(t, say, "magus proves it in scope (released)")
	assert.NotContains(t, say, "job apply", "the line is served as the next, not worded inline")
	assert.NotContains(t, got.Reason, "ask your orchestrator")
	assert.Contains(t, why, "this attempt's holder released it at")
	assert.LessOrEqual(t, len(say), 1024, "a refusal never grows a context")

	unproved := gradeLeasedWrite(ctx, Dependencies{}, row.ID, filepath.Join(root, "README.md"))
	require.Equal(t, writeGrade{Decision: "deny", Reason: unproved.Reason}, unproved, "a write no proof covers stays lease-write")
	assert.NotContains(t, unproved.Reason, "job apply")
}

// The served line is pre-authorized only if the worker's own guard lets it run.
func TestServedWideningLinePassesTheGuardForTheWorker(t *testing.T) {
	ctx, root, row := widenFixture(t)

	got := gradeLeasedWrite(ctx, Dependencies{}, row.ID, filepath.Join(root, "internal/commands/registry.go"))
	require.Len(t, got.Next, 1)
	line := got.Next[0].Run

	v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: row.ID, Dir: root})
	assert.NotEqual(t, "deny", v.Decision, "%s: %s", line, v.Reason)

	write := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Lease: row.ID, Input: hookJSON(t, map[string]any{
		"session_id": "5d1c", "hook_event_name": "PreToolUse", "tool_name": "Write", "cwd": root,
		"tool_input": map[string]any{"file_path": filepath.Join(root, "internal/commands/registry.go"), "content": "package x\n"},
	})})
	require.Len(t, write.Next, 1, "the refusal serves the apply line to the worker: %+v", write)
	assert.Equal(t, line, write.Next[0].Run)
	write.Next = nil
	assert.Equal(t, verdictWithRule("deny", string(denyRuleLeaseWiden)), unworded(write))
}

func TestLeaseWidenIsCatalogued(t *testing.T) {
	doc, ok := Rule(string(denyRuleLeaseWiden))
	require.True(t, ok)
	assert.Contains(t, doc.Why, "lease-write")
}
