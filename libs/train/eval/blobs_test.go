package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type blobsSuite struct{ suite.Suite }

func TestBlobsSuite(t *testing.T) { suite.Run(t, new(blobsSuite)) }

func (s *blobsSuite) git(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	return strings.TrimSpace(string(out))
}

func (s *blobsSuite) TestBlobsAndTemporalReadAGitHistory() {
	dir := s.T().TempDir()
	s.git(dir, "init", "-q")
	write := func(name, body string) {
		s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("a.go", "package a\n")
	s.git(dir, "add", ".")
	s.git(dir, "commit", "-qm", "one")
	cutoff := s.git(dir, "rev-parse", "HEAD")
	write("a.go", "package a\n\nvar X = 1\n")
	write("notes.md", "x\n")
	s.git(dir, "add", ".")
	s.git(dir, "commit", "-qm", "two")

	blobs, err := OpenBlobs(dir, "HEAD")
	s.Require().NoError(err)
	sha, data, err := blobs.Read("a.go")
	s.Require().NoError(err)
	s.Equal("package a\n\nvar X = 1\n", string(data))
	s.Equal(gitBlobSHA(data), sha)
	_, _, err = blobs.Read("missing.go")
	s.Error(err)
	s.Require().NoError(blobs.Close())

	changes, commits, err := Temporal(dir, cutoff, "HEAD", hasLanguage)
	s.Require().NoError(err)
	s.Equal(1, commits)
	s.Require().Len(changes, 1)
	s.Equal("a.go", changes[0].Path)
	s.Equal(2, changes[0].Added)
}
