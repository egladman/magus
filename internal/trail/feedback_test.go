package trail

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandShape(t *testing.T) {
	cases := []struct{ command, want string }{
		{"grep -rn foo src", "grep -rn <arg>"},
		{"grep -rn bar lib/a.go lib/b.go", "grep -rn <arg> <path>"},
		{"grep -rn bar lib", "grep -rn <arg>"},
		{"sed -n 1,80p internal/trail/trail.go", "sed -n <n> <path>"},
		{"./magus run go-build . --silent", "magus run go-build <path> --silent"},
		{"git status --short", "git status --short"},
		{"cat a.go b.go | wc -l", "cat <path> | wc -l"},
		{"cd /tmp && ls -la 2>&1 > /dev/null", "cd <path> && ls -la 2>&1 >/dev/null"},
		{"go test -run 'TestX$' ./internal/...", "go test -run <arg> <path>"},
		{"for f in a b; do echo $f; done", "for <loop> ; do echo <arg> ; done"},
		{"TOKEN=abc curl -H \"Authorization: $TOKEN\" https://x", "curl -H <arg> <path>"},
		{"git log --format=%H -3", "git log --format=<arg> -<n>"},
		{"echo 'unterminated", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, CommandShape(tc.command), tc.command)
	}
}

func TestCommandShapes(t *testing.T) {
	assert.Equal(t, []string{"cd <path>", "grep -rn <arg>", "head -<n>"}, CommandShapes("cd /x && grep -rn foo src | head -5"))
	assert.Equal(t, []string{"echo <arg>", "git rev-parse <arg>"}, CommandShapes("echo $(git rev-parse HEAD)"))
	assert.Equal(t, []string{"go test <path> 2>&1"}, CommandShapes("F=1 go test ./... 2>&1"))
	assert.Empty(t, CommandShapes("X=1"), "a bare assignment runs no program")
	assert.Nil(t, CommandShapes("echo 'unterminated"))
}

func TestServedNexts(t *testing.T) {
	reason := "`sed -n 36,42p a.go` prints the declaration whole.\nnext:\n  sed -n 36,42p a.go\n      prints the whole declaration.\n  sed -n 1,9p b.go\n      prints it too.\nsee: https://example.test/rules/grep-reader/"
	assert.Equal(t, []string{"sed -n 36,42p a.go", "sed -n 1,9p b.go"}, servedNexts(reason))
	assert.Empty(t, servedNexts("magus workspace: stay inside ."), "advice with no next block serves nothing")
}

func TestReadFeedbackFoldsOneSessionAcrossCheckouts(t *testing.T) {
	ctx := context.Background()
	main, worktree := t.TempDir(), t.TempDir()
	mainBase, worktreeBase := filepath.Join(main, ".magus"), filepath.Join(worktree, ".magus")

	AppendAgentCommand(ctx, mainBase, AgentCommand{Host: "claude-code", Session: "s1", Agent: "a1", Lease: "job-1", Tool: "shell.command",
		Command: "grep -A5 'func X' a.go", Decision: "deny", Rule: "grep-reader",
		Reason: "reads a body.\nnext:\n  sed -n 1,9p a.go\n      prints it.\nsee: x"})
	AppendAgentCommand(ctx, worktreeBase, AgentCommand{Host: "claude-code", Session: "s1", Agent: "a1", Tool: "shell.command",
		Command: "sed -n 1,9p a.go", Decision: "pass", PreauthorizedBy: "deny-grep-reader"})
	AppendAgentCommand(ctx, mainBase, AgentCommand{Host: "claude-code", Session: "other", Tool: "shell.command", Command: "ls", Decision: "pass"})
	AppendAgentSpawn(ctx, mainBase, AgentSpawn{Host: "claude-code", Session: "s1", Child: "worker", Context: "lease: job-1\ndo it", DeclaredModel: "opus"})
	AppendAgentSpawn(ctx, mainBase, AgentSpawn{Host: "claude-code", Session: "s1", Continue: true, Context: "more"})

	now := time.Now()
	bases := FeedbackBases([]string{main, worktree, t.TempDir()}, ".magus", now.Add(-time.Hour))
	require.Equal(t, 2, len(bases), "a checkout with no trail is not read")

	got, err := ReadFeedback(bases, FeedbackWindow{Session: "s1", Since: now.Add(-time.Hour), Until: now.Add(time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, "claude-code", got.Host)
	assert.ElementsMatch(t, []string{main, worktree}, got.Checkouts)
	require.Len(t, got.Observations, 2)
	deny := got.Observations[0]
	assert.Equal(t, "deny", deny.Decision)
	assert.Equal(t, "grep-reader", deny.Rule)
	assert.Equal(t, "a1", deny.Agent)
	assert.Equal(t, "job-1", deny.Lease)
	assert.Equal(t, []string{"sed -n 1,9p a.go"}, deny.Nexts)
	assert.Equal(t, "grep -A<n> <arg> <path>", deny.Shape)
	assert.Equal(t, "deny-grep-reader", got.Observations[1].PreauthorizedBy)
	require.Len(t, got.Spawns, 1, "a continuation starts no subagent")
	assert.Equal(t, "worker", got.Spawns[0].Child)
	assert.Equal(t, "job-1", got.Spawns[0].Lease)
	assert.Equal(t, "opus", got.Spawns[0].Model)
}

func TestReadFeedbackDefaultsToTheNewestSession(t *testing.T) {
	ctx := context.Background()
	base := filepath.Join(t.TempDir(), ".magus")
	AppendAgentCommand(ctx, base, AgentCommand{Host: "codex", Session: "older", Tool: "shell.command", Command: "ls", Decision: "pass"})
	time.Sleep(2 * time.Millisecond)
	AppendAgentCommand(ctx, base, AgentCommand{Host: "codex", Session: "newer", Tool: "shell.command", Command: "pwd", Decision: "pass"})

	now := time.Now()
	got, err := ReadFeedback([]string{base}, FeedbackWindow{Since: now.Add(-time.Hour), Until: now.Add(time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, "newer", got.Session)
	require.Len(t, got.Observations, 1)
	assert.Equal(t, "pwd", got.Observations[0].Command)
}

func TestReadFeedbackLeavesOutEventsOutsideTheWindow(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".magus")
	AppendAgentCommand(context.Background(), base, AgentCommand{Host: "codex", Session: "s", Tool: "shell.command", Command: "ls", Decision: "pass"})
	future := time.Now().Add(time.Hour)
	got, err := ReadFeedback([]string{base}, FeedbackWindow{Session: "s", Since: future, Until: future.Add(time.Hour)})
	require.NoError(t, err)
	assert.Empty(t, got.Observations)
}

func TestFeedbackBasesSharesAnAbsoluteCache(t *testing.T) {
	shared := t.TempDir()
	assert.Equal(t, []string{shared}, FeedbackBases([]string{"/a", "/b"}, shared, time.Now()))
}

func TestFeedbackBasesSkipsATrailUntouchedInTheWindow(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".magus")
	AppendAgentCommand(context.Background(), base, AgentCommand{Session: "s", Tool: "shell.command", Command: "ls", Decision: "pass"})
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, os.Chtimes(eventsPath(base), old, old))
	assert.Empty(t, FeedbackBases([]string{root}, ".magus", time.Now().Add(-24*time.Hour)))
}
