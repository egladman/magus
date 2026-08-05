package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A doc under a VCS-ignored path must not become a graph node. The concrete case
// this exists for: `magus agent install` writes rendered copies of cmd/magus/skills/
// into .agents/, .opencode/, and .claude/, all three declared generated in
// .gitignore. Indexing them made the committed graph depend on which provider trees
// the person regenerating it happened to have installed, so the drift gate failed
// for everyone else and CI could never reproduce it.
func TestFindDocFiles_SkipsVCSIgnored(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}

	git("init", "-q")
	write(".gitignore", ".agents/skills/\n.claude/skills/*\n!.claude/skills/hand-authored/\n")
	write("README.md", "# readme")
	write("docs/guide.md", "# guide")
	write(".agents/skills/installed/SKILL.md", "# installed copy")
	write(".claude/skills/installed/SKILL.md", "# installed copy")
	write(".claude/skills/hand-authored/SKILL.md", "# hand authored")

	got := findDocFiles(root)

	assert.Contains(t, got, "README.md")
	assert.Contains(t, got, "docs/guide.md")
	// The negation in .gitignore is the whole reason this filters on IGNORED rather
	// than on untracked: hand-authored is uncommitted here too, and must survive.
	assert.Contains(t, got, ".claude/skills/hand-authored/SKILL.md",
		"a re-included path is not ignored and must still be indexed")
	assert.NotContains(t, got, ".agents/skills/installed/SKILL.md")
	assert.NotContains(t, got, ".claude/skills/installed/SKILL.md")
}

// No VCS at all is a normal way to run magus, and it must not silently empty the
// doc set. No answer means index what was found.
func TestFindDocFiles_NoVCSIndexesEverything(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme"), 0o644))

	assert.Equal(t, []string{"README.md"}, findDocFiles(root))
}
