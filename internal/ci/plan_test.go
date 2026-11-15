package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestFailedResultsSuite(t *testing.T) { suite.Run(t, new(FailedResultsSuite)) }

// FailedResultsSuite pins the failures a run closes with, across the
// verify passes of a ci run under rw.
type FailedResultsSuite struct{ suite.Suite }

func resultEvent(project, target, status string) journal.Event {
	return journal.Event{Kind: journal.KindResult, Project: project, Target: target, Status: status}
}

func (s *FailedResultsSuite) TestClearedFailureIsNotPrinted() {
	c := &FailedResults{}
	c.add(resultEvent(".", "tidy", journal.StatusFail))
	c.add(resultEvent("docs", "lint", journal.StatusFail))
	c.add(resultEvent(".", "tidy", journal.StatusPass))

	s.Equal([]journal.Event{resultEvent("docs", "lint", journal.StatusFail)}, c.List())
}

func (s *FailedResultsSuite) TestCachedPassClearsAFailure() {
	c := &FailedResults{}
	c.add(resultEvent(".", "tidy", journal.StatusFail))
	c.add(resultEvent(".", "tidy", journal.StatusCached))

	s.Empty(c.List())
}

func (s *FailedResultsSuite) TestAFailureInEveryPassIsPrintedOnce() {
	c := &FailedResults{}
	c.add(resultEvent(".", "broken", journal.StatusFail))
	c.add(resultEvent(".", "broken", journal.StatusFail))

	s.Equal([]journal.Event{resultEvent(".", "broken", journal.StatusFail)}, c.List())
}

func (s *FailedResultsSuite) TestFailureAfterAPassIsKept() {
	c := &FailedResults{}
	c.add(resultEvent(".", "tidy", journal.StatusPass))
	c.add(resultEvent(".", "tidy", journal.StatusFail))

	s.Equal([]journal.Event{resultEvent(".", "tidy", journal.StatusFail)}, c.List())
}

func TestReadAffectedPlanPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		null  bool
		want  []string
	}{
		{
			name:  "newline paths are sorted and deduplicated",
			input: "z/file.go\na file.go\n\nz/file.go\n",
			want:  []string{"a file.go", "z/file.go"},
		},
		{
			name:  "nul batches collapse into one plan",
			input: "z/file.go\x00\x00a file.go\x00z/file.go\x00",
			null:  true,
			want:  []string{"a file.go", "z/file.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ReadAffectedPlanPaths(strings.NewReader(tt.input), tt.null)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestPlanOutputsCoverWorkflowKeys pins the plan's job outputs to the workflow that
// reads them. The two halves used to agree only because a magusfile function listed
// the same names by hand, which is not agreement anything can check: a merge reverted
// that function, ci.yaml went on gating two jobs on an `inherit` output nothing wrote,
// and every run in between read the empty string and skipped the feature silently.
func TestPlanOutputsCoverWorkflowKeys(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yaml"))
	require.NoError(t, err)

	outs, err := PlanOutputs(PlanOutput{})
	require.NoError(t, err)
	produced := make(map[string]bool, len(outs))
	for _, o := range outs {
		produced[o.Name] = true
	}

	read := regexp.MustCompile(`steps\.plan\.outputs\.([a-z_]+)`).FindAllStringSubmatch(string(body), -1)
	require.NotEmpty(t, read, "ci.yaml no longer reads plan step outputs; repoint this test at what does")
	for _, m := range read {
		assert.True(t, produced[m[1]],
			"ci.yaml publishes plan output %q that `magus affected --plan` does not emit", m[1])
	}
}

func TestPlanOutputsStaySingleLineAndReportInheritance(t *testing.T) {
	t.Parallel()

	out := PlanOutput{Count: 1, MaxParallel: 1, Matrix: []PlanShard{{Shard: "1", Projects: ". docs", JobName: ". docs", Digest: "d"}}}
	got, err := PlanOutputs(out)
	require.NoError(t, err)

	byName := make(map[string]string, len(got))
	for _, o := range got {
		// A newline here forges an entry in the line-oriented file a provider
		// appends this to, rather than merely looking wrong.
		assert.NotContains(t, o.Value, "\n", "output %q spans lines", o.Name)
		byName[o.Name] = o.Value
	}
	assert.JSONEq(t, `{"include":[{"shard":"1","projects":". docs","job_name":". docs","digest":"d","duties":""}]}`, byName["matrix"])
	assert.Equal(t, "false", byName["inherit"])

	out.Inherit = &PlanInherit{Run: "42", Commit: "abc123"}
	got, err = PlanOutputs(out)
	require.NoError(t, err)
	assert.Contains(t, got, PlanPublish{Name: "inherit", Value: "true"})
}

func TestPlanSummaryMarkdownPrefersTheInheritanceReport(t *testing.T) {
	t.Parallel()

	out := PlanOutput{Count: 2, MaxParallel: 2, Matrix: []PlanShard{
		{Shard: "1a", Projects: "."},
		{Shard: "1b", Projects: ". console"},
	}}
	table := PlanSummaryMarkdown(out)
	assert.Contains(t, table, "## CI plan: 2 shards\n")
	assert.Contains(t, table, "| `1a` | magus |")
	assert.Contains(t, table, "| `1b` | magus console |")

	out.Inherit = &PlanInherit{Run: "42", Commit: "abc123"}
	inherited := PlanSummaryMarkdown(out)
	assert.Contains(t, inherited, "### Inherited verdict")
	assert.Contains(t, inherited, "Inherited run: 42 at commit `abc123`.")
	assert.NotContains(t, inherited, "CI plan")
}

// TestPlanSummaryMarkdownNamesStagesAndFallbacks pins the whole summary of a
// two-stage plan with a fallback, the shape a reader meets most.
func TestPlanSummaryMarkdownNamesStagesAndFallbacks(t *testing.T) {
	t.Parallel()

	out := PlanOutput{
		Count: 2, MaxParallel: 2,
		StageZero: []PlanShard{{Shard: "0a", Projects: "."}},
		Matrix:    []PlanShard{{Shard: "1a", Projects: "cmd/magus"}, {Shard: "1b", Projects: "console docs"}},
		Fallbacks: []types.Fallback{{
			What: "No memory check", Why: "magus.yaml sets no `ci.machine`",
			Effect: "No shard is checked against a runner's memory.",
		}},
	}
	want := "## CI plan: 3 shards in 2 stages\n\n" +
		"Stage 0 computes, once, what two or more stage 1 shards share. " +
		"Each stage 1 shard then replays it from the cache.\n\n" +
		"| Shard | Projects |\n| --- | --- |\n" +
		"| `0a` | magus |\n| `1a` | cmd/magus |\n| `1b` | console docs |\n\n" +
		"Up to 2 shards of stage 1 run at once. " +
		"Each shard's job starts with its ID, and a red one uploads its logs as `magus-logs-<ID>`.\n\n" +
		"### The plan used defaults\n\n" +
		"An input was missing, so the plan chose for it. Each row says what that costs this run.\n\n" +
		"| Default | Because | What it means |\n| --- | --- | --- |\n" +
		"| No memory check | magus.yaml sets no `ci.machine` | No shard is checked against a runner's memory. |\n\n" +
		"<details><summary>Why these projects</summary>\n\n" +
		"Run `magus affected ci --explain` locally to see which changed path reached each project. " +
		"[Affected CI guide](https://eli.gladman.cc/magus/guides/affected/), " +
		"[CI target guide](https://eli.gladman.cc/magus/concepts/targets/ci/)\n\n</details>\n"
	assert.Equal(t, want, PlanSummaryMarkdown(out))
}

// TestPlanRiskOnATrivialChange: a trivial change plans no shard, and the summary is
// the per-path proof rather than an empty table.
func TestPlanRiskOnATrivialChange(t *testing.T) {
	t.Parallel()

	rep := types.TierReport{Base: "0123456789abcdef", Tier: check.RiskTrivial, Gate: []types.TierGateStep{},
		Evidence: []check.TierEvidence{{Path: "notes/v1.md", Class: "prose", Tier: check.RiskTrivial, Why: "nothing in ci's chain reads it"}}}
	risk := NewPlanRisk(rep)
	assert.Equal(t, &PlanRisk{Tier: check.RiskTrivial, Base: "0123456789abcdef", Paths: rep.Evidence}, risk)
	summary := "### Gate sized trivial\n\n" +
		"No shard runs: the change against `01234567` tiers trivial, so no step of the gate can observe it.\n\n" +
		"| Changed path | Tier | Class | Decided by |\n| --- | --- | --- | --- |\n" +
		"| `notes/v1.md` | trivial | prose | nothing in ci's chain reads it |\n\n" +
		"To run the full gate anyway, run `magus affected ci --no-redundancy-check`.\n"

	out := PlanOutput{Matrix: []PlanShard{}, Risk: risk}
	assert.Equal(t, summary, PlanSummaryMarkdown(out))
	got, err := PlanOutputs(out)
	require.NoError(t, err)
	assert.Contains(t, got, PlanPublish{Name: "count", Value: "0"}, "the workflow's count guard skips every shard")
	assert.Contains(t, got, PlanPublish{Name: "inherit", Value: "false"})
}

func TestSavedPlanJSONContainsTypedInputsWithoutMarkdown(t *testing.T) {
	t.Parallel()

	for _, out := range []PlanOutput{
		{Target: "ci", Matrix: []PlanShard{{Shard: "1", Projects: "."}}},
		{Target: "ci", Inherit: &PlanInherit{Run: "42", Commit: "abc123"}},
		{Target: "ci", Risk: &PlanRisk{Tier: check.RiskTrivial, Paths: []check.TierEvidence{{Path: "notes/v1.md", Tier: check.RiskTrivial, Why: "no gate step reads it"}}}},
	} {
		body, err := json.Marshal(out)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "summary")
		assert.NotContains(t, string(body), "# CI plan")
	}
}
