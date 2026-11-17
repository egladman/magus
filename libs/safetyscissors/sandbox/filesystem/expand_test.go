package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ExpandSuite struct{ suite.Suite }

func TestExpand(t *testing.T) { suite.Run(t, new(ExpandSuite)) }

func env(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

func (s *ExpandSuite) requireMode(mode string, want Rule) {
	got, err := ModeRule(mode)
	s.Require().NoError(err, mode)
	s.Equal(want, got, mode)
}

func (s *ExpandSuite) TestTheEmptyModeIsReadOnly() { s.requireMode("", Rule{Read: true}) }

func (s *ExpandSuite) TestRoGrantsRead() { s.requireMode("ro", Rule{Read: true}) }

func (s *ExpandSuite) TestRwGrantsReadAndWrite() { s.requireMode("rw", Rule{Read: true, Write: true}) }

func (s *ExpandSuite) TestRxGrantsReadAndExec() { s.requireMode("rx", Rule{Read: true, Exec: true}) }

func (s *ExpandSuite) TestRwxGrantsEverything() {
	s.requireMode("rwx", Rule{Read: true, Write: true, Exec: true})
}

func (s *ExpandSuite) TestModeRuleRefusesATypo() {
	for _, typo := range []string{"RW", "wr", "x", "r", "rox"} {
		_, err := ModeRule(typo)
		s.ErrorContains(err, "unknown mode", typo)
	}
}

func (s *ExpandSuite) TestExpandUserRuleResolvesVariablesAndHome() {
	t := s.T()
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
func (s *ExpandSuite) TestExpandUserRuleRefusesAnUnsetOrEmptyVariable() {
	t := s.T()
	for _, raw := range []string{"$UNSET/", "${UNSET}/cache", "$EMPTY/x"} {
		_, err := ExpandUserRule(raw, "rw", "/home/u", env(map[string]string{"EMPTY": ""}))
		assert.ErrorIs(t, err, ErrUnsetVariable, raw)
	}
}

func (s *ExpandSuite) TestExpandUserRuleRefusesRelativeAndHomelessPaths() {
	t := s.T()
	_, err := ExpandUserRule("relative/dir", "ro", "/home/u", env(nil))
	assert.ErrorContains(t, err, "must be absolute")
	_, err = ExpandUserRule("~/x", "ro", "", env(nil))
	assert.ErrorContains(t, err, "home directory")
	_, err = ExpandUserRule("/abs", "RW", "", env(nil))
	assert.ErrorContains(t, err, "unknown mode")
}

// A rule path is resolved like a checked path, or the two never compare equal.
func (s *ExpandSuite) TestExpandUserRuleResolvesSymlinksWhenThePathExists() {
	t := s.T()
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.Symlink(target, link))

	r, err := ExpandUserRule(link, "ro", "", env(nil))
	require.NoError(t, err)
	assert.Equal(t, ResolveRulePath(target), r.Path)
	require.NoError(t, Ruleset{Rules: []Rule{r}}.Check(filepath.Join(link, "f.txt"), Read))
}

// A rule may name a directory a later step creates.
func (s *ExpandSuite) TestExpandUserRuleKeepsAMissingPath() {
	t := s.T()
	missing := filepath.Join(t.TempDir(), "not-created-yet")
	r, err := ExpandUserRule(missing, "rw", "", env(nil))
	require.NoError(t, err)
	assert.Equal(t, ResolveRulePath(missing), r.Path)
}
