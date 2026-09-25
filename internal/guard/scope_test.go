package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAdvisoriesStayInsideTheWorkspace: observed in the 2026-09-24 audit, advisories fired
// on `find ~/.claude/projects`, a `cat` of a plan file, and a `head` into a scratch file,
// none of which this workspace's graph or rules say anything about. A line whose every
// path lies outside the root is advised nothing; one path inside keeps it in scope.
func TestAdvisoriesStayInsideTheWorkspace(t *testing.T) {
	scope := workspaceScope{root: "/work/repo", home: "/home/me"}
	for _, tt := range []struct {
		command string
		outside bool
	}{
		{"find ~/.claude/projects -name '*.jsonl'", true},
		{"cat /home/me/.claude/plans/audit.md", true},
		{"head -50 /work/other/main.go > /tmp/x/scratch.txt", true},
		{"grep -rn needle ~/.claude/projects | head -20", true},
		{"cat $HOME/notes.go", true},
		{`P=/work/other; cat "$P/main.go"`, true},

		{"cat internal/guard/shell.go", false},
		{"cat /work/repo/internal/guard/shell.go", false},
		{"cat internal/x.go /tmp/y.go", false},
		{"head -50 internal/x.go > /tmp/scratch.txt", false},
		// No path at all works on the call's directory, which is inside.
		{"rg needle", false},
		{"magus run lint .", false},
		// A variable nobody assigned on the line proves nothing.
		{`cat "$WT/main.go"`, false},
		{"cat ../other/main.go", false},
	} {
		assert.Equal(t, tt.outside, scope.lineOutside(tt.command, DialectBash), tt.command)
	}

	deps := testDependencies()
	deps.scope = scope
	assert.Empty(t, Evaluate(deps, "cat /work/other/main.go").Context, "an outside read is advised nothing")
	assert.Contains(t, Evaluate(deps, "cat internal/guard/shell.go").Context, "refs", "the same read inside still is")
	assert.NotEmpty(t, Evaluate(deps, "cd /tmp/copy && magus run lint .").Deny, "a deny is never scoped away")
}

func TestScopeOutside(t *testing.T) {
	rooted := workspaceScope{root: "/work/repo", home: "/home/me"}
	assert.True(t, rooted.outside("/work/other/x"))
	assert.True(t, rooted.outside("~/x"))
	assert.True(t, rooted.outside("/work/repo/../other"))
	assert.False(t, rooted.outside("/work/repo"))
	assert.False(t, rooted.outside("/work/repo/x"))
	assert.False(t, rooted.outside("x"))
	assert.False(t, rooted.outside(unresolved+"/x"))

	// macOS spells /tmp two ways, and the root and the path may arrive under either.
	tmp := workspaceScope{root: "/tmp/ws"}
	assert.False(t, tmp.outside("/private/tmp/ws/x"))
	assert.True(t, tmp.outside("/private/tmp/other/x"))

	// Unrooted, only a temp or scratchpad path is known to be outside.
	bare := workspaceScope{}
	assert.True(t, bare.outside("/tmp/x"))
	assert.True(t, bare.outside("/a/b/scratchpad/x"))
	assert.False(t, bare.outside("/work/other/x"))
	assert.False(t, bare.outside("~/x"))
}
