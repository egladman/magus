package bench

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit/script"
)

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
