package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func TestModeRuleSpellsEachGrant(t *testing.T) {
	for mode, want := range map[string]Rule{
		"":    {Read: true},
		"ro":  {Read: true},
		"rw":  {Read: true, Write: true},
		"rx":  {Read: true, Exec: true},
		"rwx": {Read: true, Write: true, Exec: true},
	} {
		got, err := ModeRule(mode)
		require.NoError(t, err, mode)
		assert.Equal(t, want, got, mode)
	}
	for _, typo := range []string{"RW", "wr", "x", "r", "rox"} {
		_, err := ModeRule(typo)
		assert.ErrorContains(t, err, "unknown mode", typo)
	}
}

func TestExpandUserRuleResolvesVariablesAndHome(t *testing.T) {
	dir := ResolveRulePath(t.TempDir())
	r, err := ExpandUserRule("$TOOLS/bin", "rx", "", env(map[string]string{"TOOLS": dir}))
	require.NoError(t, err)
	assert.Equal(t, Rule{Path: filepath.Join(dir, "bin"), Read: true, Exec: true}, r)

	r, err = ExpandUserRule("${TOOLS}/x", "rw", "", env(map[string]string{"TOOLS": dir}))
	require.NoError(t, err)
	assert.Equal(t, Rule{Path: filepath.Join(dir, "x"), Read: true, Write: true}, r)

	r, err = ExpandUserRule("~/tools", "ro", dir, env(nil))
	require.NoError(t, err)
	assert.Equal(t, Rule{Path: filepath.Join(dir, "tools"), Read: true}, r)
}

// "$UNSET/" would expand to "/" and grant the whole filesystem.
func TestExpandUserRuleRefusesAnUnsetOrEmptyVariable(t *testing.T) {
	for _, raw := range []string{"$UNSET/", "${UNSET}/cache", "$EMPTY/x"} {
		_, err := ExpandUserRule(raw, "rw", "/home/u", env(map[string]string{"EMPTY": ""}))
		assert.ErrorIs(t, err, ErrUnsetVariable, raw)
	}
}

func TestExpandUserRuleRefusesRelativeAndHomelessPaths(t *testing.T) {
	_, err := ExpandUserRule("relative/dir", "ro", "/home/u", env(nil))
	assert.ErrorContains(t, err, "must be absolute")
	_, err = ExpandUserRule("~/x", "ro", "", env(nil))
	assert.ErrorContains(t, err, "home directory")
	_, err = ExpandUserRule("/abs", "RW", "", env(nil))
	assert.ErrorContains(t, err, "unknown mode")
}

// A rule path is resolved like a checked path, or the two never compare equal.
func TestExpandUserRuleResolvesSymlinksWhenThePathExists(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.Symlink(target, link))

	r, err := ExpandUserRule(link, "ro", "", env(nil))
	require.NoError(t, err)
	assert.Equal(t, ResolveRulePath(target), r.Path)
	assert.NoError(t, Ruleset{Rules: []Rule{r}}.Check(filepath.Join(link, "f.txt"), Read))
}

// A rule may name a directory a later step creates.
func TestExpandUserRuleKeepsAMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created-yet")
	r, err := ExpandUserRule(missing, "rw", "", env(nil))
	require.NoError(t, err)
	assert.Equal(t, ResolveRulePath(missing), r.Path)
}
