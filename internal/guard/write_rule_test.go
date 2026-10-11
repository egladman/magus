package guard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
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
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Write("magus.yaml", "{}\n")
	root = w.Root()
	cacheDir = t.TempDir()
	return WithLocation(t.Context(), cacheDir, root, root), root, cacheDir
}

// committedPolicyFixture is writeFixture as a git repository whose root magusfile is
// committed, so nothing in its policy is pending.
func committedPolicyFixture(t *testing.T) (*testkit.Repo, context.Context) {
	t.Helper()
	testkit.Isolate(t)
	repo := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": "import \"magus\";\n"})
	return repo, WithLocation(t.Context(), t.TempDir(), repo.Root(), repo.Root())
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

// The file-write rules carry the payload's subagent id as the shell-command rules do.
func TestWriteRuleSeesTheHostsSubagentID(t *testing.T) {
	ctx, root, _ := writeFixture(t)
	probe := &writeRuleProbe{}
	Judge(ctx, Dependencies{WriteRule: probe.rule()}, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
		"session_id": "s1", "agent_id": "a534fcfe", "hook_event_name": "PreToolUse", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": filepath.Join(root, "notes.md"), "content": "x\n"},
	})})
	require.Len(t, probe.asked, 1)
	assert.Equal(t, "a534fcfe", probe.asked[0].Agent)
	assert.Empty(t, probe.asked[0].Parent)
}

func canonicalDir(dir string) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// Strengthen only, for file writes too: a deny blocks, and a read is never asked.
func TestWriteRuleStrengthensOnly(t *testing.T) {
	ctx, root, cacheDir := writeFixture(t)
	probe := &writeRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "Add a fragment instead."}}
	deps := Dependencies{WriteRule: probe.rule()}

	v := Judge(ctx, deps, Request{Input: filepath.Join(root, "CHANGELOG.md"), IsPath: true, Host: "claude-code"})
	assert.Equal(t, verdictWithRule("deny", workspaceWriteRule), unworded(v))
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

// A write to a file the guard loads goes through, advised, when its judgment runs out of
// time while the approved policy stands, its rule having answered or no policy change
// pending: denying it would leave a looping committed rule with no way to be fixed. Any
// other write is still denied, and so is a policy write whose approved side did not answer
// while a change is pending, since an agent can slow that side on purpose.
func TestLapsedJudgmentAdvisesAWriteToAPolicySource(t *testing.T) {
	approved := func(context.Context) (workspace.WriteRule, error) { return (&writeRuleProbe{}).rule(), nil }
	lapsedLoad := Dependencies{
		LoadFailure:       errors.New("magusfile: context deadline exceeded"),
		RuleLoad:          trail.Phase{Name: phaseLoadRules, ElapsedMs: 6600, BudgetMs: 6500, Lapsed: true},
		ApprovedWriteRule: approved,
	}
	const spared = "lets it through, because the guard loads this file itself"

	t.Run("the root magusfile", func(t *testing.T) {
		ctx, root, _ := writeFixture(t)
		v := Judge(ctx, lapsedLoad, Request{Input: filepath.Join(root, "magusfile.buzz"), IsPath: true, Host: "claude-code", Session: "s1"})
		assert.Equal(t, verdictWithRule("advise", string(advisoryWriteRuleFailed)), unworded(v))
		assert.Contains(t, v.Context, spared)
	})

	t.Run("a source the last loaded policy read", func(t *testing.T) {
		ctx, root, cacheDir := writeFixture(t)
		policy := filepath.Join(root, "hack", "policy", "guard.buzz")
		RecordPolicy(t.Context(), cacheDir, root, PolicyState{Digest: "d1", WriteRule: true, Files: []string{filepath.Join(root, "magusfile.buzz"), policy}}, false)
		v := Judge(ctx, lapsedLoad, Request{Input: policy, IsPath: true, Host: "claude-code", Session: "s1"})
		assert.Equal(t, "advise", v.Decision)
		assert.Contains(t, v.Context, spared)
	})

	t.Run("a source this call's policy read, while its rule loops", func(t *testing.T) {
		ctx, root, _ := writeFixture(t)
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		loops := func(ctx context.Context, _ types.WriteRequest, _ hint.Gate) (types.GuardVerdict, error) {
			<-ctx.Done()
			return types.GuardVerdict{}, ctx.Err()
		}
		deps := Dependencies{
			WriteRule:         loops,
			ApprovedWriteRule: approved,
			Policy: func() PolicyState {
				return PolicyState{Digest: "d1", WriteRule: true, Files: []string{"hack/policy/guard.buzz"}}
			},
		}
		v := Judge(ctx, deps, Request{Input: filepath.Join(root, "hack", "policy", "guard.buzz"), IsPath: true, Host: "claude-code", Session: "s1"})
		assert.Equal(t, "advise", v.Decision, "a relative source is the workspace's")
		assert.Contains(t, v.Context, spared)

		other := Judge(ctx, deps, Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, Host: "claude-code", Session: "s1"})
		assert.Equal(t, "deny", other.Decision, "any other write is still denied")
	})

	t.Run("a policy source whose approved side did not answer", func(t *testing.T) {
		slowed := func(ctx context.Context) (workspace.WriteRule, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		unanswered := map[string]Dependencies{
			"slowed":      {LoadFailure: lapsedLoad.LoadFailure, RuleLoad: lapsedLoad.RuleLoad, ApprovedWriteRule: slowed},
			"none to ask": {LoadFailure: lapsedLoad.LoadFailure, RuleLoad: lapsedLoad.RuleLoad},
		}
		judge := func(ctx context.Context, deps Dependencies, root string) Verdict {
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			return Judge(ctx, deps, Request{Input: filepath.Join(root, "magusfile.buzz"), IsPath: true, Host: "claude-code", Session: "s1"})
		}

		repo, ctx := committedPolicyFixture(t)
		for name, deps := range unanswered {
			v := judge(ctx, deps, repo.Root())
			assert.Equal(t, "advise", v.Decision, "%s: no policy change is pending, so the committed rule stands", name)
			assert.Contains(t, v.Context, spared, name)
		}

		_, outside, _ := writeFixture(t)
		for name, deps := range unanswered {
			v := judge(WithLocation(t.Context(), t.TempDir(), outside, outside), deps, outside)
			assert.Equal(t, "advise", v.Decision, "%s: in no repository the working tree is the whole policy", name)
		}

		for pending, file := range map[string][2]string{
			"the root magusfile edited":   {"magusfile.buzz", "import \"magus\";\n// edited\n"},
			"a new Buzz source":           {"hack/policy/new.buzz", "import \"magus\";\n"},
			"the workspace config edited": {"magus.yaml", "{\"version\": 1}\n"},
		} {
			repo, ctx := committedPolicyFixture(t)
			repo.Write(file[0], file[1])
			for name, deps := range unanswered {
				v := judge(ctx, deps, repo.Root())
				assert.Equal(t, "deny", v.Decision, "%s, %s", pending, name)
				assert.Contains(t, v.Reason, "magus ran out of time judging this file write", "%s, %s", pending, name)
			}
		}
	})

	t.Run("any other file", func(t *testing.T) {
		ctx, root, _ := writeFixture(t)
		v := Judge(ctx, lapsedLoad, Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, Host: "claude-code", Session: "s1"})
		assert.Equal(t, "deny", v.Decision)
		assert.Contains(t, v.Reason, "magus ran out of time judging this file write")
	})
}
