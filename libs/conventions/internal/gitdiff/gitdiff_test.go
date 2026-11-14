package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ChangesSuite struct {
	suite.Suite
}

func TestChanges(t *testing.T) {
	suite.Run(t, new(ChangesSuite))
}

func (s *ChangesSuite) TestHunksSpanTheWorkingCopyLines() {
	diff := "@@ -3,0 +4,2 @@\n+a\n+b\n@@ -9,2 +11 @@\n@@ -20,3 +21,0 @@\n"
	s.Equal([]Span{{4, 5}, {11, 11}, {21, 22}}, hunks(diff))
}

func (s *ChangesSuite) TestOverlapsMeetsAClosedRange() {
	spans := []Span{{4, 5}}
	s.True(Overlaps(spans, 5, 9))
	s.False(Overlaps(spans, 6, 9))
}

const sample = "one\ntwo\nthree\nfour\n"

// repository makes a repository whose main branch holds files, then checks out a
// branch for the test's own commits.
func (s *ChangesSuite) repository(files map[string]string) string {
	dir := s.T().TempDir()
	s.git(dir, "init", "-q", "-b", "main")
	s.git(dir, "config", "user.email", "t@example.com")
	s.git(dir, "config", "user.name", "t")
	for name, text := range files {
		s.write(dir, name, text)
	}
	s.git(dir, "add", "-A")
	s.git(dir, "commit", "-q", "-m", "base")
	s.git(dir, "checkout", "-q", "-b", "work")
	return dir
}

func (s *ChangesSuite) write(dir, name, text string) {
	path := filepath.Join(dir, name)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(text), 0o644))
}

func (s *ChangesSuite) git(dir string, args ...string) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	s.Require().NoError(err, string(out))
}

func (s *ChangesSuite) commitAll(dir string) {
	s.git(dir, "add", "-A")
	s.git(dir, "commit", "-q", "-m", "work")
}

func (s *ChangesSuite) TestUnchangedRenameHasNoChangedSpans() {
	dir := s.repository(map[string]string{"pkg/old.go": sample})
	s.git(dir, "mv", "pkg/old.go", "pkg/new.go")
	s.commitAll(dir)

	spans, base, err := New("main").File(filepath.Join(dir, "pkg", "new.go"))
	s.Require().NoError(err)
	s.Empty(spans)
	s.Equal(sample, string(base))
}

func (s *ChangesSuite) TestRenameWithOneEditedLineHasExactlyThatSpan() {
	dir := s.repository(map[string]string{"pkg/old.go": sample})
	s.git(dir, "mv", "pkg/old.go", "pkg/new.go")
	s.write(dir, "pkg/new.go", "one\ntwo\nTHREE\nfour\n")
	s.commitAll(dir)

	spans, err := New("main").Lines(filepath.Join(dir, "pkg", "new.go"))
	s.Require().NoError(err)
	s.Equal([]Span{{3, 3}}, spans)
}

func (s *ChangesSuite) TestRenameAcrossDirectoriesFollowsTheOldPath() {
	dir := s.repository(map[string]string{"a/old.go": sample})
	s.Require().NoError(os.MkdirAll(filepath.Join(dir, "b"), 0o755))
	s.git(dir, "mv", "a/old.go", "b/new.go")
	s.commitAll(dir)

	spans, err := New("main").Lines(filepath.Join(dir, "b", "new.go"))
	s.Require().NoError(err)
	s.Empty(spans)
}

func (s *ChangesSuite) TestUnrenamedFileReportsItsEditedLines() {
	dir := s.repository(map[string]string{"pkg/keep.go": sample})
	s.write(dir, "pkg/keep.go", "one\ntwo\nthree\nfour\nfive\n")
	s.commitAll(dir)

	spans, base, err := New("main").File(filepath.Join(dir, "pkg", "keep.go"))
	s.Require().NoError(err)
	s.Equal([]Span{{5, 5}}, spans)
	s.Equal(sample, string(base))
}

func (s *ChangesSuite) TestNewFileIsWhollyChanged() {
	dir := s.repository(map[string]string{"pkg/keep.go": sample})
	s.write(dir, "pkg/fresh.go", "a\nb\n")
	s.commitAll(dir)

	spans, base, err := New("main").File(filepath.Join(dir, "pkg", "fresh.go"))
	s.Require().NoError(err)
	s.Equal([]Span{{1, 3}}, spans)
	s.Nil(base)
}
