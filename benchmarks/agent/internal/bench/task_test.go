package bench

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit/script"
)

func bugfixTask(id string) Task {
	return Task{
		Suite:      SuiteHistoryGo,
		ID:         id,
		Kind:       TaskBugfix,
		Tag:        TagCapability,
		Created:    "2026-09-30T12:00:00Z",
		Repo:       "egladman/magus",
		License:    "MIT",
		BaseCommit: "aaaa",
		FixCommit:  "bbbb",
		Prompt:     "fix the cache key race",
		Patch:      "diff --git a/x.go b/x.go\n",
		TestPatch:  "diff --git a/x_test.go b/x_test.go\n",
		FailToPass: []string{"TestCacheSuite/TestKeyRace"},
		Packages:   []string{"github.com/egladman/magus/internal/cache"},
	}
}

func (s *benchSuite) TestSealSetsTheDigestOfTheRecordWithoutItsSHA() {
	t := bugfixTask("fix-cache-key-race")
	s.Require().NoError(t.Seal())
	s.True(strings.HasPrefix(t.SHA, "sha256:"))
	again, err := t.Digest()
	s.Require().NoError(err)
	s.Equal(t.SHA, again)
}

func (s *benchSuite) TestDigestChangesWhenThePromptIsReworded() {
	a := bugfixTask("fix-cache-key-race")
	b := a
	b.Prompt += "."
	da, err := a.Digest()
	s.Require().NoError(err)
	db, err := b.Digest()
	s.Require().NoError(err)
	s.NotEqual(da, db)
}

func (s *benchSuite) TestValidateNamesEveryMissingFieldForTheKind() {
	t := bugfixTask("fix-cache-key-race")
	t.TestPatch = ""
	t.FailToPass = nil
	t.Tag = ""
	err := t.Validate()
	s.Require().Error(err)
	s.ErrorContains(err, "test_patch is empty")
	s.ErrorContains(err, "fail_to_pass is empty")
	s.ErrorContains(err, `tag ""`)
}

func (s *benchSuite) TestValidateWantsAnAnswerKeyForInterrogation() {
	t := bugfixTask("who-imports-guard")
	t.Kind = TaskInterrogation
	s.ErrorContains(t.Validate(), "answer.items is empty")
	t.Answer = &AnswerKey{Items: []string{"a"}, Oracle: "go list -json ./..."}
	s.NoError(t.Validate())
}

func (s *benchSuite) TestValidateRefusesAnUnknownKind() {
	t := bugfixTask("x")
	t.Kind = "refactor"
	s.ErrorContains(t.Validate(), `kind "refactor"`)
}

func (s *benchSuite) TestWriteThenLoadReturnsTheTasksInIDOrder() {
	path := filepath.Join(s.T().TempDir(), "history-go.jsonl")
	s.Require().NoError(WriteTasks(path, []Task{bugfixTask("b"), bugfixTask("a")}))
	got, err := LoadTasks(path)
	s.Require().NoError(err)
	s.Require().Len(got, 2)
	s.Equal("a", got[0].ID)
	s.Equal("b", got[1].ID)
	s.NotEmpty(got[0].SHA)
}

func (s *benchSuite) TestWriteRefusesARepeatedID() {
	path := filepath.Join(s.T().TempDir(), "history-go.jsonl")
	s.ErrorContains(WriteTasks(path, []Task{bugfixTask("a"), bugfixTask("a")}), `task "a" appears twice`)
	s.NoFileExists(path)
}

func (s *benchSuite) TestLoadRefusesARecordEditedAfterItWasSealed() {
	path := filepath.Join(s.T().TempDir(), "history-go.jsonl")
	s.Require().NoError(WriteTasks(path, []Task{bugfixTask("a")}))
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	edited := strings.Replace(string(raw), "fix the cache key race", "fix the cache key", 1)
	s.Require().NoError(os.WriteFile(path, []byte(edited), 0o644))
	_, err = LoadTasks(path)
	s.ErrorContains(err, "but digests to")
}

func (s *benchSuite) TestLoadRefusesARepeatedID() {
	path := filepath.Join(s.T().TempDir(), "history-go.jsonl")
	s.Require().NoError(WriteTasks(path, []Task{bugfixTask("a")}))
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(path, append(raw, raw...), 0o644))
	_, err = LoadTasks(path)
	s.ErrorContains(err, `task "a" repeats line 1`)
}

func (s *benchSuite) TestLoadRefusesAnUnfinishedLastLine() {
	path := filepath.Join(s.T().TempDir(), "history-go.jsonl")
	s.Require().NoError(WriteTasks(path, []Task{bugfixTask("a")}))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	s.Require().NoError(err)
	_, err = f.WriteString(`{"suite":`)
	s.Require().NoError(err)
	s.Require().NoError(f.Close())
	_, err = LoadTasks(path)
	s.ErrorContains(err, "no newline closes")
}

func (s *benchSuite) TestLoadRefusesAnEmptyFile() {
	path := filepath.Join(s.T().TempDir(), "history-go.jsonl")
	s.Require().NoError(os.WriteFile(path, nil, 0o644))
	_, err := LoadTasks(path)
	s.ErrorContains(err, "holds no tasks")
}

func (s *benchSuite) TestCutCloneHoldsNothingPastTheBaseCommit() {
	source := filepath.Join(s.T().TempDir(), "source")
	s.gitIn(source, "init", "--quiet", "--initial-branch=main")
	var commits []string
	for _, msg := range []string{"one", "two", "three"} {
		s.Require().NoError(os.WriteFile(filepath.Join(source, "f"), []byte(msg), 0o644))
		s.gitIn(source, "add", "f")
		s.gitIn(source, "commit", "--quiet", "-m", msg)
		commits = append(commits, s.gitIn(source, "rev-parse", "HEAD"))
	}
	s.gitIn(source, "tag", "v1", commits[2])

	dest := filepath.Join(s.T().TempDir(), "trial")
	t := bugfixTask("x")
	t.BaseCommit = commits[1]
	s.Require().NoError(t.CutClone(context.Background(), source, dest))

	s.Equal(commits[1], s.gitIn(dest, "rev-parse", "HEAD"))
	s.Equal("main", s.gitIn(dest, "branch", "--show-current"))
	s.Equal("two\none", s.gitIn(dest, "log", "--format=%s", "--all"))
	s.Empty(s.gitIn(dest, "remote"))
	s.Empty(s.gitIn(dest, "tag"))
	s.Empty(s.gitIn(dest, "status", "--porcelain"))
	s.Error(exec.Command("git", "-C", dest, "cat-file", "-e", commits[2]).Run())
}

func (s *benchSuite) gitIn(dir string, args ...string) string {
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	return strings.TrimSpace(string(out))
}

// TestAgentEval runs every testdata/agenteval task with a real claude, once per skill
// variant and repetition, and appends one row per run to MAGUS_AGENT_EVAL_RESULTS, an
// absolute path (default magus-agenteval.jsonl in the temp dir). It spends tokens, so it
// skips unless MAGUS_AGENT_EVAL=1. The scripts run the magus on PATH, so a build of the
// tree under test comes first on PATH; it fails when none is there.
//
// MAGUS_AGENT_EVAL_VARIANTS is a comma-separated list of skill forms or absolute skills
// directories (default full,short); MAGUS_AGENT_EVAL_REPS is the runs per variant
// (default 1). The test runs in this package's directory, so it refuses a relative path
// rather than resolve it there.
func (s *benchSuite) TestAgentEval() {
	t := s.T()
	if os.Getenv("MAGUS_AGENT_EVAL") != "1" {
		t.Skip("MAGUS_AGENT_EVAL=1 runs the agent eval, which spends tokens")
	}
	_, err := exec.LookPath("magus")
	s.Require().NoError(err, "the eval scripts run the magus on PATH")
	var variants []string
	for _, v := range strings.Split(cmp.Or(os.Getenv("MAGUS_AGENT_EVAL_VARIANTS"), "full,short"), ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if strings.ContainsRune(v, filepath.Separator) && !filepath.IsAbs(v) {
			t.Fatalf("MAGUS_AGENT_EVAL_VARIANTS: %q: a skills directory must be an absolute path", v)
		}
		variants = append(variants, v)
	}
	s.Require().NotEmpty(variants, "MAGUS_AGENT_EVAL_VARIANTS names no variant")
	reps, err := strconv.Atoi(cmp.Or(os.Getenv("MAGUS_AGENT_EVAL_REPS"), "1"))
	s.Require().NoError(err, "MAGUS_AGENT_EVAL_REPS")
	if reps <= 0 {
		t.Fatalf("MAGUS_AGENT_EVAL_REPS: %d: want at least 1", reps)
	}
	results := cmp.Or(os.Getenv("MAGUS_AGENT_EVAL_RESULTS"), filepath.Join(os.TempDir(), "magus-agenteval.jsonl"))
	if !filepath.IsAbs(results) {
		t.Fatalf("MAGUS_AGENT_EVAL_RESULTS: %q: want an absolute path", results)
	}
	t.Logf("results: %s", results)

	passEnv := script.WithSetup(func(e *script.Env) error {
		for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_EXECPATH"} {
			if v := os.Getenv(name); v != "" {
				e.Setenv(name, v)
			}
		}
		return nil
	})
	for _, variant := range variants {
		for rep := 1; rep <= reps; rep++ {
			t.Run(fmt.Sprintf("%s/%d", filepath.Base(variant), rep), func(t *testing.T) {
				script.Run(t, filepath.Join("testdata", "agenteval"),
					passEnv,
					script.WithSetup(script.Skills(variant)),
					script.WithAgent(script.Recorder{Variant: variant, Repetition: rep, File: results}))
			})
		}
	}
}
