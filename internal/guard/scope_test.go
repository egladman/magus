package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
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

	deps := strict(testDependencies())
	deps.scope = scope
	assert.Empty(t, Evaluate(deps, "cat /work/other/main.go").Context, "an outside read is advised nothing")
	assert.Contains(t, Evaluate(deps, "cat internal/guard/shell.go").Context, "refs", "the same read inside still is")
	assert.NotEmpty(t, Evaluate(deps, "cd /tmp/copy && magus run lint .").Deny, "a deny is never scoped away")
}

// A relative word is the call directory's, after the line's cds: read-navigation and
// symbol-search advised on `cat ../other-wt/main.go` and on a plan read after a cd.
func TestScopePlacesFromTheCallDir(t *testing.T) {
	scope := workspaceScope{root: "/work/repo", dir: "/work/repo", home: "/home/me"}
	assert.True(t, scope.lineOutside("cat ../other-wt/main.go", DialectBash))
	assert.True(t, scope.lineOutside("cd ~/.claude/plans && cat RESUME.md", DialectBash))
	assert.False(t, scope.lineOutside("cd internal && cat x.go", DialectBash))
	assert.False(t, scope.lineOutside("cd $X && cat x.go", DialectBash), "a cd the line does not determine keeps it in scope")
	assert.False(t, scope.lineOutside(`cat "~/x"`, DialectBash), "a quoted ~ is a directory named ~ here")
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

// A lease declaring `**/*.md` matched ~/.claude/.../RESUME.md: the `~` word was joined under
// the checkout as if it were a directory there. A home-relative path is never a checkout's.
func TestWorkspaceRelativePlacesAHomePathOutside(t *testing.T) {
	root := t.TempDir()
	_, inside := workspaceRelative(root, "~/.claude/plans/p/RESUME.md")
	assert.False(t, inside)
	rel, inside := workspaceRelative(root, "docs/RESUME.md")
	assert.True(t, inside)
	assert.Equal(t, "docs/RESUME.md", rel)

	assert.Equal(t, "/home/me/x", expandHome("~/x", "/home/me"))
	assert.Equal(t, "/home/me", expandHome("~", "/home/me"))
	assert.Equal(t, "~/x", expandHome("~/x", ""), "an unknown home leaves the word for the caller to place")
	assert.Equal(t, "a/~/x", expandHome("a/~/x", "/home/me"))
}

// A lease taken in the main checkout matched `**/*.md` against a file in a worktree nested
// under it at .claude/worktrees/<wt>: the other checkout counted as a subtree of this one.
func TestWorkspaceRelativeRefusesANestedCheckout(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".claude", "worktrees", "wt")
	require.NoError(t, os.MkdirAll(filepath.Join(wt, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte(fmt.Sprintf("gitdir: %s\n", filepath.Join(root, ".git", "worktrees", "wt"))), 0o644))

	_, inside := workspaceRelative(root, filepath.Join(wt, "docs", "x.md"))
	assert.False(t, inside, "a nested checkout's file is that checkout's")
	_, inside = workspaceRelative(root, ".claude/worktrees/wt/docs/x.md")
	assert.False(t, inside)
	rel, inside := workspaceRelative(root, ".claude/notes.md")
	assert.True(t, inside, "the directory holding the worktrees is still the root's")
	assert.Equal(t, ".claude/notes.md", rel)
	assert.True(t, workspaceScope{root: root}.outside(filepath.Join(wt, "docs", "x.md")))
}

// placePath places a word as the shell opens it, and refuses one the line does not determine.
func TestPlacePath(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		word, cwd, want string
		ok              bool
	}{
		{"a/b", "/w", "/w/a/b", true},
		{"/abs/x", "", "/abs/x", true},
		{"~/x", "", "/home/me/x", true},
		{"~", "/w", "/home/me", true},
		{"./~/x", "/w", "/w/~/x", true},
		{"~other/x", "/w", "", false},
		{"a/b", "", "", false},
		{unresolved + "$D/x", "/w", "", false},
	} {
		got, ok := placePath(tt.word, tt.cwd, "/home/me")
		assert.Equal(t, tt.ok, ok, tt.word)
		assert.Equal(t, tt.want, got, tt.word)
	}
	_, ok := placePath("~/x", "/w", "")
	assert.False(t, ok, "a ~ with the home unknown is placed nowhere")

	f, err := parseFile(`cd ~/plans && cat a; cd sub && cat b; cd "$X" && cat c; cd /abs && cat d; cd - && cat e`, DialectBash)
	require.NoError(t, err)
	var got []string
	dirs := stmtDirs(f, `cd ~/plans && cat a; cd sub && cat b; cd "$X" && cat c; cd /abs && cat d; cd - && cat e`, "/w", "/home/me")
	for _, st := range f.Stmts {
		bin := st.Cmd.(*syntax.BinaryCmd)
		got = append(got, dirs[bin.Y])
	}
	assert.Equal(t, []string{"/home/me/plans", "/home/me/plans/sub", "", "/abs", ""}, got)
}
