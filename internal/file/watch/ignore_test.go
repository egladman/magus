package watch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuiltinIgnore_VCSMeta(t *testing.T) {
	for _, path := range []string{
		"/work/.git/COMMIT_EDITMSG",
		"/work/node_modules/pkg/index.js",
		"/work/.magus/cache.json",
	} {
		assert.True(t, BuiltinIgnore(path), "BuiltinIgnore(%q) should be true", path)
	}
	for _, path := range []string{
		"/work/src/main.go",
		"/work/README.md",
	} {
		assert.False(t, BuiltinIgnore(path), "BuiltinIgnore(%q) should be false", path)
	}
}

func TestCompose_AnyTrue(t *testing.T) {
	alwaysTrue := func(string) bool { return true }
	alwaysFalse := func(string) bool { return false }

	// OR semantics: true if any predicate returns true
	assert.True(t, Compose(alwaysTrue, alwaysFalse)("x"), "Compose(true,false): OR should return true")
	assert.True(t, Compose(alwaysTrue, alwaysTrue)("x"), "Compose(true,true): OR should return true")
	assert.False(t, Compose(alwaysFalse, alwaysFalse)("x"), "Compose(false,false): OR should return false")
}

func TestCompose_Empty(t *testing.T) {
	none := Compose()
	assert.False(t, none("anything"), "Compose() should return false for any input")
}

// A CHECKOUT UNDER A DOT-DIRECTORY IS STILL A CHECKOUT, and this is the bug that pins it.
//
// BuiltinIgnore walks every segment of the ABSOLUTE path and skips any dot-directory, which
// is right for a tree inside the workspace and catastrophic for one the workspace sits
// inside: an agent worktree at <repo>/.claude/worktrees/<name> has a dot segment above its
// root, so every file in it was ignored and the watcher went permanently silent. Measured
// here: the job feed reported nothing at all in an agent's own worktree.
//
// RelativeIgnore judges a path by where it sits UNDER the root, which is the only thing the
// predicate was ever meant to be asking.
func TestRelativeIgnoreJudgesUnderTheRootNotAboveIt(t *testing.T) {
	const root = "/repo/.claude/worktrees/feature"
	src := root + "/internal/trail/trail.go"

	assert.True(t, BuiltinIgnore(src),
		"the absolute form skips it for a dot segment ABOVE the root, which is the bug")

	rel := RelativeIgnore(root, BuiltinIgnore)
	assert.False(t, rel(src), "a source file under the root is watched wherever the root lives")
	assert.True(t, rel(root+"/node_modules/pkg/index.js"), "a real skip dir under the root is still skipped")
	assert.True(t, rel(root+"/.git/HEAD"), "so is a dot dir under the root")
	assert.True(t, rel(root+"/internal/trail/.DS_Store"), "and so is an editor temporary")
	assert.True(t, rel("/elsewhere/trail.go"), "a path outside the root is not this watcher's business")
}

// THE ROOT MUST NOT BE IGNORED. The recursive walk asks about the root first, so a predicate
// that skips it prunes the whole tree before a single directory is registered: the watcher
// comes up reporting success, in microseconds, watching nothing at all.
//
// Measured while this was broken: over a 33,960-directory agent worktree, "registered in
// 47.5us" and no event ever. With the root admitted, the same walk takes 105.9ms and the
// first write arrives in one batch.
func TestRelativeIgnoreAdmitsTheRootItself(t *testing.T) {
	const root = "/repo/.claude/worktrees/feature"
	rel := RelativeIgnore(root, BuiltinIgnore)

	assert.False(t, rel(root), "ignoring the root prunes the walk before it starts")
	assert.False(t, rel(root+"/"), "and a trailing slash is the same directory")
	assert.False(t, rel(root+"/internal"), "so the directories under it are reached at all")
}
