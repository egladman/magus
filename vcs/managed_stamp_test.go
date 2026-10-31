package vcs

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// DirtyStampSuite has a dirty build re-init a hook a clean build wrote.
type DirtyStampSuite struct {
	suite.Suite
	dir      string
	hook     string
	logs     *bytes.Buffer
	previous *slog.Logger
}

func TestDirtyStampSuite(t *testing.T) { suite.Run(t, new(DirtyStampSuite)) }

func (s *DirtyStampSuite) SetupTest() {
	s.dir = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"}).Root()
	s.hook = filepath.Join(s.dir, ".git", "hooks", "post-commit")
	s.logs = &bytes.Buffer{}
	s.previous = slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(s.logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

func (s *DirtyStampSuite) TearDownTest() { slog.SetDefault(s.previous) }

func (s *DirtyStampSuite) build(version string) context.Context {
	return types.WithMagusBuild(s.T().Context(), types.MagusBuild{
		Version: version, Commit: "aaaaaaaaaaaa", Date: "2026-10-31T00:00:00Z",
	})
}

func (s *DirtyStampSuite) install(version, command string) []string {
	installed, err := gitVCS{}.InstallDriftHook(s.build(version), s.dir, command)
	s.Require().NoError(err)
	return installed
}

func (s *DirtyStampSuite) read() string {
	data, err := os.ReadFile(s.hook)
	s.Require().NoError(err)
	return string(data)
}

func (s *DirtyStampSuite) TestUnchangedBodyFromADirtyStampWritesNothing() {
	s.install("v0.4.3-5-gaaaaaaa", "magus job run check-drift")
	before := s.read()

	installed := s.install("v0.4.3-5-gaaaaaaa-dirty", "magus job run check-drift")

	s.Empty(installed)
	s.Equal(before, s.read())
	s.Empty(s.logs.String())
}

func (s *DirtyStampSuite) TestChangedBodyFromADirtyStampSaysItReplacedACleanSection() {
	s.install("v0.4.3-5-gaaaaaaa", "magus job run check-drift")

	installed := s.install("v0.4.3-5-gaaaaaaa-dirty", "./magus job run check-drift")

	s.Equal([]string{"post-commit", "pre-push"}, installed)
	s.Contains(s.logs.String(), "post-commit")
	s.Contains(s.logs.String(), "dirty")
}

func (s *DirtyStampSuite) TestChangedBodyFromACleanStampSaysNothing() {
	s.install("v0.4.3-5-gaaaaaaa", "magus job run check-drift")

	s.install("v0.4.3-6-gaaaaaaa", "./magus job run check-drift")

	s.Empty(s.logs.String())
}
