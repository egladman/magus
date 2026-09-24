package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRuleProbe is a workspace write rule that records what it was asked and answers as
// told.
type writeRuleProbe struct {
	asked  []types.WriteRequest
	answer types.GuardVerdict
	err    error
}

func (p *writeRuleProbe) rule() workspace.WriteRule {
	return func(_ context.Context, req types.WriteRequest, _ hint.Gate) (types.GuardVerdict, error) {
		p.asked = append(p.asked, req)
		return p.answer, p.err
	}
}

// writeFixture is a workspace root holding a magus.yaml, so a path under it resolves to it,
// with the trail pinned to a temporary cache dir.
func writeFixture(t *testing.T) (ctx context.Context, root, cacheDir string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	cacheDir = t.TempDir()
	return WithLocation(t.Context(), cacheDir, root, root), root, cacheDir
}

// The rule is handed the text the host says the write carries, read by shape, and the
// workspace the path lands in.
func TestWriteRuleSeesTheWrittenText(t *testing.T) {
	ctx, root, _ := writeFixture(t)
	path := filepath.Join(root, "CHANGELOG.md")
	cases := []struct {
		name  string
		input map[string]any
		want  types.WriteRequest
	}{
		{"an edit", map[string]any{"file_path": path, "old_string": "## [Unreleased]\n", "new_string": "## [Unreleased]\n\n- a line\n"},
			types.WriteRequest{OldText: "## [Unreleased]\n", NewText: "## [Unreleased]\n\n- a line\n"}},
		{"a whole-file write", map[string]any{"file_path": path, "content": "# Changelog\n"},
			types.WriteRequest{Content: "# Changelog\n"}},
		{"a shape magus does not read", map[string]any{"file_path": path, "edits": []any{}},
			types.WriteRequest{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &writeRuleProbe{}
			v := Judge(ctx, Dependencies{WriteRule: probe.rule()}, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": "Edit", "tool_input": tc.input,
			})})
			assert.NotEqual(t, "deny", v.Decision)
			require.Len(t, probe.asked, 1)
			want := tc.want
			want.Host, want.Session, want.Path, want.Workspace, want.Role = "claude-code", "s1", path, root, types.AgentRoleRoot
			got := probe.asked[0]
			got.Workspace = canonicalDir(got.Workspace)
			want.Workspace = canonicalDir(want.Workspace)
			assert.Equal(t, want, got)
		})
	}
}

func canonicalDir(dir string) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// Strengthen only, on the write surface too: a deny blocks, and a read is never asked.
func TestWriteRuleStrengthensOnly(t *testing.T) {
	ctx, root, cacheDir := writeFixture(t)
	probe := &writeRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "Add a fragment instead."}}
	deps := Dependencies{WriteRule: probe.rule()}

	v := Judge(ctx, deps, Request{Input: filepath.Join(root, "CHANGELOG.md"), IsPath: true, Host: "claude-code"})
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, workspaceWriteRule, v.Rule)
	assert.Contains(t, v.Reason, "Add a fragment instead.")
	writes := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.NotEmpty(t, writes)
	assert.Equal(t, decidedByWorktree, writes[0].DecidedBy)

	Judge(ctx, deps, Request{Input: filepath.Join(root, "CHANGELOG.md"), Observe: true, Host: "claude-code"})
	assert.Len(t, probe.asked, 1, "an observed read is not a write")
}

// The committed rule answers when the working tree's is loosened or broken, and a broken
// working-tree rule fails open with the advisory.
func TestWriteRulesKeepTheStricterSide(t *testing.T) {
	ctx, root, _ := writeFixture(t)
	path := filepath.Join(root, "CHANGELOG.md")
	approved := (&writeRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "no"}}).rule()
	v := Judge(ctx, Dependencies{
		WriteRule:         (&writeRuleProbe{}).rule(),
		ApprovedWriteRule: func(context.Context) (workspace.WriteRule, error) { return approved, nil },
	}, Request{Input: path, IsPath: true, Host: "claude-code"})
	assert.Equal(t, "deny", v.Decision)

	ctx, root, _ = writeFixture(t)
	broken := &writeRuleProbe{err: errors.New("magus\\guard.write: the rule raised: boom")}
	v = Judge(ctx, Dependencies{WriteRule: broken.rule()}, Request{Input: filepath.Join(root, "x.go"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", v.Decision)
	assert.Contains(t, v.Context, "Only the built-in rules applied to this write.")
}
