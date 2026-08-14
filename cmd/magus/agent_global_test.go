package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInstallTargetHonorsGlobal pins the fix for a flag that never worked:
// --global documents "allow absolute destination paths", the command let one
// past its own guard, and Catalog.WriteSkillTree then refused it
// unconditionally - so the error told the caller to pass the flag they had just
// passed.
//
// The split is what makes it work without weakening the catalog: an absolute
// destination becomes the directory it names plus the leaf inside it, so the
// catalog's containment check still has something real to check.
func TestInstallTargetHonorsGlobal(t *testing.T) {
	base, leaf := installTarget(".", "/tmp/skills", true)
	assert.Equal(t, filepath.Dir("/tmp/skills"), base, "an absolute dest supplies its own base")
	assert.Equal(t, "skills", leaf)
	assert.Equal(t, filepath.Clean("/tmp/skills"), filepath.Join(base, leaf),
		"the split must rejoin to the path the caller asked for")
}

// TestInstallTargetLeavesRelativeAlone: without --global, and for a relative
// destination with it, the pair is unchanged - the catalog keeps enforcing
// containment against the repo dir.
func TestInstallTargetLeavesRelativeAlone(t *testing.T) {
	base, leaf := installTarget("repo", ".claude/skills", false)
	assert.Equal(t, "repo", base)
	assert.Equal(t, ".claude/skills", leaf)

	base, leaf = installTarget("repo", ".claude/skills", true)
	assert.Equal(t, "repo", base, "--global does not relocate a relative destination")
	assert.Equal(t, ".claude/skills", leaf)

	// A traversal is left intact so the catalog still refuses it; --global must
	// not become a way to escape the tree.
	base, leaf = installTarget("repo", "../../outside", true)
	assert.Equal(t, "repo", base)
	assert.Equal(t, "../../outside", leaf)
}
