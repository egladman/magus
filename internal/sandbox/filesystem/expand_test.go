package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandUserRule_AbsPath(t *testing.T) {
	r, err := ExpandUserRule("/tmp/mydir", true, false)
	require.NoError(t, err)
	// user-allow paths default to Exec=true.
	assert.Equal(t, Rule{Path: "/tmp/mydir", Read: true, Write: false, Exec: true}, r)
}

func TestExpandUserRule_EnvVar(t *testing.T) {
	t.Setenv("TEST_EXPAND_PATH", "/var/test")
	r, err := ExpandUserRule("$TEST_EXPAND_PATH", true, false)
	require.NoError(t, err)
	assert.Equal(t, "/var/test", r.Path)
}

// TestExpandUserRuleAlwaysGrantsExec pins the documented default: a magus.yaml
// sandbox.allow entry is executable whatever read/write the caller asked for,
// so a toolchain directory ($GOPATH/bin, $CARGO_HOME/bin) stays runnable. It is
// a deliberate widening, which is exactly why it should fail loudly if changed.
func TestExpandUserRuleAlwaysGrantsExec(t *testing.T) {
	for _, tc := range []struct{ read, write bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		r, err := ExpandUserRule("/tmp/some-tooldir", tc.read, tc.write)
		require.NoError(t, err)
		assert.Truef(t, r.Exec, "read=%v write=%v must still grant Exec", tc.read, tc.write)
		assert.Equal(t, tc.read, r.Read)
		assert.Equal(t, tc.write, r.Write)
	}
}

// TestExpandUserRuleExpandsTilde covers the ~ branch. Joined against the real home
// so the rule compares equal to paths the checker later normalizes.
func TestExpandUserRuleExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	r, err := ExpandUserRule("~/tools", true, false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "tools"), r.Path)
	assert.NotContains(t, r.Path, "~", "the tilde must not survive into the rule")
}

// TestExpandUserRuleResolvesSymlinksWhenThePathExists is what keeps a user rule
// comparable with a checked path: checkAccess resolves symlinks, so a rule that
// did not would match nothing. The unresolved form must NOT come back.
func TestExpandUserRuleResolvesSymlinksWhenThePathExists(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.Symlink(target, link))

	r, err := ExpandUserRule(link, true, false)
	require.NoError(t, err)

	resolvedTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	assert.Equal(t, resolvedTarget, r.Path, "an existing symlinked rule path resolves to its target")

	// And the resulting rule actually grants the file it should.
	rs := Ruleset{Rules: []Rule{r}}
	assert.NoError(t, rs.CheckRead(filepath.Join(link, "f.txt")))
}

// TestExpandUserRuleKeepsLexicalFormWhenMissing: a rule may name a directory a
// later step creates, so a non-existent path is kept rather than rejected.
func TestExpandUserRuleKeepsLexicalFormWhenMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created-yet")
	r, err := ExpandUserRule(missing, true, true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(missing), r.Path)
}
