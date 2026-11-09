package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestPathClean(t *testing.T) {
	got, err := PathClean(context.Background(), "a/b/../c/./d")
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("a/c/d"), got)
}

func TestPathRel(t *testing.T) {
	got, err := PathRel(context.Background(), "a/b", "a/b/c/d")
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("c/d"), got)
}

func TestPathIsAbs(t *testing.T) {
	abs, _ := PathAbs(context.Background(), "x")
	got, err := PathIsAbs(context.Background(), abs)
	require.NoError(t, err)
	assert.True(t, got, "is_abs(%q) should be true", abs)

	rel, _ := PathIsAbs(context.Background(), "x/y")
	assert.False(t, rel, "is_abs of a relative path should be false")
}

func TestPathAbsUsesContextCwd(t *testing.T) {
	dir := t.TempDir()
	ctx := WithCurrentWorkingDirectory(context.Background(), dir)

	got, err := PathAbs(ctx, "x")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "x"), got, "path.abs should resolve against the context cwd, not the process cwd")
}

func TestPathExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	ctx := context.Background()

	got, err := PathExpandUser(ctx, "~/proj")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "proj"), got)

	bare, _ := PathExpandUser(ctx, "~")
	assert.Equal(t, home, bare)

	// A non-~ path and another user's ~ are left untouched.
	for _, in := range []string{"/abs/path", "rel/path", "~other/x"} {
		got, _ := PathExpandUser(ctx, in)
		assert.Equal(t, in, got, "expand_user(%q) should be unchanged", in)
	}
}

type PathJoinWithinSuite struct{ suite.Suite }

func TestPathJoinWithinSuite(t *testing.T) { suite.Run(t, new(PathJoinWithinSuite)) }

func (s *PathJoinWithinSuite) TestJoinCleansTheResult() {
	got, err := PathJoin(context.Background(), []string{"a", "b/../c", "d"})
	s.Require().NoError(err)
	s.Equal(filepath.FromSlash("a/c/d"), got)
}

func (s *PathJoinWithinSuite) TestJoinSkipsEmptyParts() {
	got, err := PathJoin(context.Background(), []string{"", "a", "", "b"})
	s.Require().NoError(err)
	s.Equal(filepath.FromSlash("a/b"), got)
}

func (s *PathJoinWithinSuite) TestJoinNoPartsIsEmpty() {
	got, err := PathJoin(context.Background(), []string{})
	s.Require().NoError(err)
	s.Equal("", got)
}

func (s *PathJoinWithinSuite) TestWithinChild() {
	got, err := PathWithin(context.Background(), "a/b", "a/b/c")
	s.Require().NoError(err)
	s.True(got)
}

func (s *PathJoinWithinSuite) TestWithinSelf() {
	got, err := PathWithin(context.Background(), "a/b", "a/b/")
	s.Require().NoError(err)
	s.True(got)
}

func (s *PathJoinWithinSuite) TestWithinSiblingSharingAPrefixIsOutside() {
	got, err := PathWithin(context.Background(), "a/b", "a/bc")
	s.Require().NoError(err)
	s.False(got)
}

func (s *PathJoinWithinSuite) TestWithinParentIsOutside() {
	got, err := PathWithin(context.Background(), "a/b", "a")
	s.Require().NoError(err)
	s.False(got)
}

func (s *PathJoinWithinSuite) TestWithinDotHoldsEveryRelativePath() {
	got, err := PathWithin(context.Background(), ".", "x/y")
	s.Require().NoError(err)
	s.True(got)
}

func (s *PathJoinWithinSuite) TestWithinDotDotNamedDirectoryIsInside() {
	got, err := PathWithin(context.Background(), "a", "a/..b")
	s.Require().NoError(err)
	s.True(got)
}

func (s *PathJoinWithinSuite) TestWithinAbsoluteAgainstRelativeIsOutside() {
	got, err := PathWithin(context.Background(), "a", string(filepath.Separator)+"a")
	s.Require().NoError(err)
	s.False(got)
}

type PathDirBaseSuite struct{ suite.Suite }

func TestPathDirBaseSuite(t *testing.T) { suite.Run(t, new(PathDirBaseSuite)) }

func (s *PathDirBaseSuite) TestDirOfANestedPath() {
	got, err := PathDir(context.Background(), "a/b/c.go")
	s.Require().NoError(err)
	s.Equal(filepath.FromSlash("a/b"), got)
}

func (s *PathDirBaseSuite) TestDirOfABareNameIsDot() {
	got, err := PathDir(context.Background(), "c.go")
	s.Require().NoError(err)
	s.Equal(".", got)
}

func (s *PathDirBaseSuite) TestDirOfARootChildIsRoot() {
	got, err := PathDir(context.Background(), "/x")
	s.Require().NoError(err)
	s.Equal(string(filepath.Separator), got)
}

func (s *PathDirBaseSuite) TestBaseOfANestedPath() {
	got, err := PathBase(context.Background(), "a/b/c.go")
	s.Require().NoError(err)
	s.Equal("c.go", got)
}

func (s *PathDirBaseSuite) TestBaseDropsTrailingSeparators() {
	got, err := PathBase(context.Background(), "a/b/")
	s.Require().NoError(err)
	s.Equal("b", got)
}
