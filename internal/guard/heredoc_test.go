package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeredocWritesReadsOnlyALiteralBodyIntoAFile(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		line string
		want []heredocWrite
	}{
		{"cat > notes.md <<'EOF'\nhello\nEOF", []heredocWrite{{"notes.md", "hello\n"}}},
		{"cat >> notes.md <<EOF\nplain text\nEOF", []heredocWrite{{"notes.md", "plain text\n"}}},
		{"tee -a a.go b.go >/dev/null <<'EOF'\npackage a\nEOF", []heredocWrite{{"a.go", "package a\n"}, {"b.go", "package a\n"}}},
		// A home-relative target is kept as the shell will place it, never as a path here.
		{"cat >> ~/.claude/p/RESUME.md <<'EOF'\nx\nEOF", []heredocWrite{{"~/.claude/p/RESUME.md", "x\n"}}},
		{"cat >> $HOME/.claude/p/RESUME.md <<'EOF'\nx\nEOF", []heredocWrite{{"~/.claude/p/RESUME.md", "x\n"}}},
		{"cat >> \"${HOME}/.claude/p/RESUME.md\" <<'EOF'\nx\nEOF", []heredocWrite{{"~/.claude/p/RESUME.md", "x\n"}}},
		{"cat >> $DIR/RESUME.md <<'EOF'\nx\nEOF", nil},
		// The body expands a parameter, so nobody can read it before it runs.
		{"cat > notes.md <<EOF\nhome is $HOME\nEOF", nil},
		// A heredoc feeding a command's stdin is no write.
		{"cat <<'EOF' | ./magus job apply -f -\nid: x\nEOF", nil},
		{"cat <<'EOF' >/dev/null\nx\nEOF", nil},
		{"cat 2> err.log <<'EOF'\nx\nEOF", nil},
		{"grep x > out.txt <<'EOF'\nx\nEOF", nil},
	} {
		assert.Equal(t, tt.want, heredocWrites(tt.line, DialectBash), tt.line)
	}
}

// The comment and proofread rules are workspace write rules: a heredoc into a file reaches
// them with its body, as the host's write tool would.
func TestHeredocWriteMeetsTheWriteRules(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	var seen []types.WriteRequest
	deps := Dependencies{WriteRule: func(_ context.Context, req types.WriteRequest, _ hint.Gate) (types.GuardVerdict, error) {
		seen = append(seen, req)
		if strings.Contains(req.Content, "forbidden") {
			return types.GuardVerdict{Decision: types.GuardDeny, Reason: "that word is not allowed here"}, nil
		}
		return types.GuardVerdict{Decision: types.GuardAllow}, nil
	}}
	judge := func(line string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Session: "heredoc", Host: "claude-code", Dir: root})
	}

	v := judge("cat > notes.md <<'EOF'\na forbidden word\nEOF")
	require.Equal(t, "deny", v.Decision, v.Context)
	assert.Equal(t, string(denyRuleHeredocWrite), v.Rule)
	assert.Contains(t, v.Reason, "that word is not allowed here")
	require.NotEmpty(t, seen)
	assert.Equal(t, filepath.Join(root, "notes.md"), seen[0].Path)
	assert.Equal(t, "a forbidden word\n", seen[0].Content)

	assert.NotEqual(t, "deny", judge("cat > notes.md <<'EOF'\nan allowed word\nEOF").Decision)
	assert.NotEqual(t, "deny", judge("cat > notes.md <<EOF\n$forbidden\nEOF").Decision, "an expanding body is not judged")

	// The root appending to its plan under ~/.claude writes the home's file, not ./~/.claude.
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	for _, line := range []string{
		"cat >> ~/.claude/plans/p/RESUME.md <<'EOF'\nan allowed word\nEOF",
		"cat >> $HOME/.claude/plans/p/RESUME.md <<'EOF'\nan allowed word\nEOF",
	} {
		seen = nil
		judge(line)
		for _, req := range seen {
			assert.Equal(t, filepath.Join(home, ".claude", "plans", "p", "RESUME.md"), req.Path, line)
		}
	}
}

// Every file a patch writes is judged, not only its first.
func TestEveryPathOfAPatchIsJudged(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	deps := Dependencies{WriteRule: func(_ context.Context, req types.WriteRequest, _ hint.Gate) (types.GuardVerdict, error) {
		if filepath.Base(req.Path) == "second.go" {
			return types.GuardVerdict{Decision: types.GuardDeny, Reason: "second.go is off limits"}, nil
		}
		return types.GuardVerdict{Decision: types.GuardAllow}, nil
	}}
	patch := "*** Begin Patch\n*** Add File: first.go\n+package a\n*** Update File: second.go\n@@\n-x\n+y\n*** End Patch\n"
	v := Judge(ctx, deps, Request{Host: "codex", Input: hookJSON(t, map[string]any{
		"session_id": "patch", "cwd": root, "hook_event_name": "PreToolUse", "tool_name": "apply_patch",
		"tool_input": map[string]any{"command": patch},
	})})
	require.Equal(t, "deny", v.Decision, v.Context)
	assert.Contains(t, v.Reason, "second.go is off limits")
	assert.Contains(t, v.Reason, "second.go: ", "the refusal names the file it is about")
}
