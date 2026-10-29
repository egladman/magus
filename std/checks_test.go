//go:build !wasm

package std

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestCheckReadWriteResolveAgainstTheCwd: a relative path is judged where it lands, the
// project dir on ctx, not the process cwd.
func TestCheckReadWriteResolveAgainstTheCwd(t *testing.T) {
	ws := filesystem.ResolveRulePath(t.TempDir())
	ctx := WithCurrentWorkingDirectory(sandbox.WithPolicy(context.Background(), &sandbox.Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: ws, Read: true}}},
	}), ws)

	require.NoError(t, CheckRead(ctx, "inside.txt"))
	require.ErrorIs(t, CheckWrite(ctx, "inside.txt"), types.PathWriteDenied)
	require.ErrorIs(t, CheckRead(ctx, filepath.Join("..", "outside.txt")), types.PathReadDenied)

	require.NoError(t, CheckWrite(context.Background(), "/anywhere"), "no policy, no check")
}

type EditJudgeSuite struct {
	suite.Suite
	path   string
	graded [][2]string
}

func TestEditJudge(t *testing.T) { suite.Run(t, new(EditJudgeSuite)) }

func (s *EditJudgeSuite) SetupTest() {
	s.path = filepath.Join(filesystem.ResolveRulePath(s.T().TempDir()), "a.txt")
	s.Require().NoError(os.WriteFile(s.path, []byte("old\n"), 0o644))
	s.graded = nil
}

// judged is a ctx whose judge records each grade and refuses "bad".
func (s *EditJudgeSuite) judged() context.Context {
	return WithEditJudge(context.Background(), EditJudge{
		Covers: func(_ context.Context, path string) bool { return path == s.path },
		Grade: func(_ context.Context, _ string, before, after []byte) error {
			s.graded = append(s.graded, [2]string{string(before), string(after)})
			if strings.Contains(string(after), "bad") {
				return errors.New("refused")
			}
			return nil
		},
	})
}

func (s *EditJudgeSuite) contents() string {
	got, err := os.ReadFile(s.path)
	s.Require().NoError(err)
	return string(got)
}

func (s *EditJudgeSuite) TestARefusedWriteLeavesTheFile() {
	s.Require().EqualError(FsWriteFile(s.judged(), s.path, "bad\n"), "refused")
	s.Equal("old\n", s.contents())
}

func (s *EditJudgeSuite) TestTheJudgeSeesWholeBeforeAndAfter() {
	s.Require().NoError(FsWriteFileAtomic(s.judged(), s.path, "new\n"))
	s.Equal([][2]string{{"old\n", "new\n"}}, s.graded)
	s.Equal("new\n", s.contents())
}

func (s *EditJudgeSuite) TestAnAppendIsGradedAsTheFileItLeaves() {
	s.Require().NoError(FsAppendFile(s.judged(), s.path, "more\n"))
	s.Equal([][2]string{{"old\n", "old\nmore\n"}}, s.graded)
}

func (s *EditJudgeSuite) TestACopyIsGradedAsItsSource() {
	src := filepath.Join(filepath.Dir(s.path), "src.txt")
	s.Require().NoError(os.WriteFile(src, []byte("bad\n"), 0o644))
	s.Require().EqualError(FsCopyFile(s.judged(), src, s.path), "refused")
	s.Equal("old\n", s.contents())
}

func (s *EditJudgeSuite) TestAPathTheJudgeDoesNotCoverIsNotGraded() {
	other := filepath.Join(filepath.Dir(s.path), "b.txt")
	s.Require().NoError(FsWriteFile(s.judged(), other, "bad\n"))
	s.Empty(s.graded)
}

func (s *EditJudgeSuite) TestAPolicyWithNoJudgeRefuses() {
	ctx := sandbox.WithPolicy(context.Background(), &sandbox.Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: filepath.Dir(s.path), Read: true, Write: true}}},
	})
	s.Require().ErrorContains(FsWriteFile(ctx, s.path, "new\n"), "no edit judge")
	s.Equal("old\n", s.contents())
}
