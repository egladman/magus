package benchtasks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/benchmarks/agent/internal/bench"
)

type historySuite struct {
	suite.Suite
}

func TestHistorySuite(t *testing.T) {
	suite.Run(t, new(historySuite))
}

func (s *historySuite) TestClassifyFindsABugfix() {
	c := classify([]string{"internal/cache/key.go", "internal/cache/key_test.go", "docs/x.md"}, nil)
	s.Equal(change{kind: bench.TaskBugfix, sources: []string{"internal/cache/key.go"}, tests: []string{"internal/cache/key_test.go"}}, c)
}

func (s *historySuite) TestClassifyFindsARegeneration() {
	c := classify([]string{"internal/config/config.go", "internal/config/gen/flags.go"}, nil)
	s.Equal(change{kind: bench.TaskRegeneration, sources: []string{"internal/config/config.go"}, generated: []string{"internal/config/gen/flags.go"}}, c)
}

func (s *historySuite) TestClassifyLeavesNestedModulesOutOfTheRootPackages() {
	c := classify([]string{"libs/xdg/xdg.go", "libs/xdg/xdg_test.go", "run.go"}, []string{"libs/xdg"})
	s.Equal(change{kind: bench.TaskInterrogation, sources: []string{"run.go"}}, c)
}

func (s *historySuite) TestClassifyTreatsTestdataAsTests() {
	c := classify([]string{"a/b.go", "a/testdata/in.txt"}, nil)
	s.Equal(change{kind: bench.TaskBugfix, sources: []string{"a/b.go"}, tests: []string{"a/testdata/in.txt"}}, c)
}

func (s *historySuite) TestClassifyRefusesAWideCommitAsABugfix() {
	var files []string
	for i := range maxBugfixFiles {
		files = append(files, fmt.Sprintf("p/f%d.go", i))
	}
	c := classify(append(files, "p/f_test.go"), nil)
	s.Equal(bench.TaskInterrogation, c.kind)
}

func (s *historySuite) TestClassifyIgnoresADocsOnlyCommit() {
	s.Empty(classify([]string{"docs/a.md"}, nil).kind)
}

func (s *historySuite) TestSlugCutsAtADash() {
	s.Equal("fix-the-cache-key-race-v2", slug("Fix the cache key race (v2)!"))
	long := slug("select the in-process Go indexer by the go-packages symbol format")
	s.LessOrEqual(len(long), 48)
	s.False(strings.HasSuffix(long, "-"))
}

func (s *historySuite) TestPromptDropsTrailers() {
	s.Equal("fix it\n\nbody", prompt("fix it\n\nbody\nCo-Authored-By: x <y>\nSigned-off-by: z\n"))
}

func (s *historySuite) TestTestEnvDropsSessionVariables() {
	s.Equal([]string{"PATH=/bin"}, testEnv([]string{"PATH=/bin", "MAGUS_JOB=x", "CLAUDECODE=1"}))
}

func (s *historySuite) TestDependentsDirectAndTransitive() {
	listed := []listedPackage{
		{ImportPath: "m/a", Imports: []string{"m/t"}, Deps: []string{"fmt", "m/t"}},
		{ImportPath: "m/b", Imports: []string{"m/a"}, Deps: []string{"m/a", "m/t"}},
		{ImportPath: "m/t", Deps: []string{"fmt"}},
	}
	s.Equal([]string{"m/a"}, dependents(listed, "m/t", false))
	s.Equal([]string{"m/a", "m/b"}, dependents(listed, "m/t", true))
}

func (s *historySuite) TestHistoryNamesEveryMissingFlag() {
	s.ErrorContains(writeHistory(context.Background(), historyOptions{repo: "."}), "--after, --ref, -o required")
}

func (s *historySuite) TestCutScratchCutsAtANonTipCommitOfARelativeRepo() {
	repo := s.T().TempDir()
	s.git(repo, "init", "--quiet", "--initial-branch=main")
	var shas []string
	for _, msg := range []string{"one", "two", "three"} {
		s.Require().NoError(os.WriteFile(filepath.Join(repo, "f"), []byte(msg), 0o644))
		s.git(repo, "add", "f")
		s.git(repo, "commit", "--quiet", "-m", msg)
		shas = append(shas, s.git(repo, "rev-parse", "HEAD"))
	}
	s.T().Chdir(repo)

	dest := filepath.Join(s.T().TempDir(), "scratch")
	s.Require().NoError(cutScratch(context.Background(), ".", shas[1], dest))

	s.Equal(shas[1], s.git(dest, "rev-parse", "HEAD"))
}

func (s *historySuite) TestCutScratchFailsLoudlyOnAMissingCommit() {
	repo := s.T().TempDir()
	s.git(repo, "init", "--quiet", "--initial-branch=main")
	dest := filepath.Join(s.T().TempDir(), "scratch")
	err := cutScratch(context.Background(), repo, strings.Repeat("a", 40), dest)
	s.ErrorContains(err, "cut a clone")
}

func (s *historySuite) git(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	return strings.TrimSpace(string(out))
}
