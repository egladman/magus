package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func noWorkspaceRules(context.Context, string) ([]WorkspaceShellRule, Dialect) { return nil, "" }

// NewRejudge is the feature the trail cannot have: would TODAY's rules have caught a
// command that ran weeks ago, under whatever rules existed then, or under none at all.
func TestRejudgeJudgesADeniedCommandByTodaysRules(t *testing.T) {
	testkit.Isolate(t)
	rejudge := NewRejudge(t.Context(), t.TempDir(), noWorkspaceRules)

	// A workspace that declares nothing gets raw-tool's default, a deny; the rejudge reads
	// no workspace built-in settings, so a softer workspace is not reflected here.
	program, verdict, rule := rejudge("go build ./...")
	assert.Equal(t, "go", program)
	assert.Equal(t, "deny", verdict)
	assert.Equal(t, "raw-tool", rule)

	program, verdict, rule = rejudge("ls")
	assert.Equal(t, "ls", program)
	assert.Equal(t, "pass", verdict)
	assert.Empty(t, rule)
}

// `magus --root A session load` run from inside checkout B rejudges each command by A's
// shell rules, the workspace whose store it writes.
func TestRejudgeReadsTheLoadedRootsShellRules(t *testing.T) {
	testkit.Isolate(t)
	loaded := t.TempDir()
	var askedRoots []string
	rejudge := NewRejudge(t.Context(), loaded, func(_ context.Context, root string) ([]WorkspaceShellRule, Dialect) {
		askedRoots = append(askedRoots, root)
		return []WorkspaceShellRule{{Name: "no-frobnicate", Decision: "deny", Program: "frobnicate", Reason: "Not here."}}, ""
	})

	program, verdict, rule := rejudge("frobnicate --now")
	_, _, _ = rejudge("frobnicate --later")

	assert.Equal(t, "frobnicate", program)
	assert.Equal(t, "deny", verdict)
	assert.Equal(t, "workspace:no-frobnicate", rule)
	require.Equal(t, []string{loaded}, askedRoots, "the rules are read once, from the root whose store is written")
}

func TestCommandProgramReadsThroughAWrapper(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "go", commandProgram("env GOFLAGS=-mod=mod go build ./..."))
	assert.Equal(t, "ls", commandProgram("ls -la"))
	assert.Empty(t, commandProgram(""))
	// The unparsable fallback must not store a leading assignment as the program: that
	// token is where a pasted credential sits.
	assert.Equal(t, "curl", commandProgram(`TOKEN=hunter2 curl -H "Authorization: Bearer $TOKEN`))
}
