package ci

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

// PlanOutput is the provider-neutral JSON shape from `magus affected --plan`.
type PlanOutput struct {
	// Target is what every shard runs, so `magus run --stdin` needs nothing beside the
	// document to know what to run.
	Target string `json:"target"`
	// Charms and Args are what the target's body was evaluated under. A shard
	// run under others is refused, since its steps would differ.
	Charms []string `json:"charms,omitempty"`
	Args   []string `json:"args,omitempty"`
	// Machine is the class the shards were packed for; Fallbacks says each
	// default the plan fell back to.
	Machine     *types.Machine   `json:"machine,omitempty"`
	Fallbacks   []types.Fallback `json:"fallbacks,omitempty"`
	Count       int              `json:"count"`
	MaxParallel int              `json:"max_parallel"`
	Source      string           `json:"source"`
	Matrix      []PlanShard      `json:"matrix"`
	// StageZero runs before every shard of Matrix and computes, once, the
	// prerequisites two or more of them share. Absent when none is shared.
	// It stays out of Outputs, so a plan without it renders as before;
	// `magus run --stdin --shard <id>` runs one of its shards. Count and
	// MaxParallel describe Matrix alone.
	StageZero []PlanShard `json:"stage_zero,omitempty"`
	// Affected is the whole closure the matrix was cut from, sorted, and UnboundedBy says
	// why it is not a proof when it is not. Together they are the answer a merge queue's
	// affected hook expects, so `affected <target> --plan --stdin` feeds one as it stands.
	// Affected stays the closure even when an inherited verdict empties the matrix.
	Affected    []string `json:"affected"`
	UnboundedBy string   `json:"unbounded_by,omitempty"`
	// Unmeasured are the units and prerequisites the shards were
	// weighed by the workspace fallback for, having no history.
	Unmeasured []types.Unit `json:"unmeasured,omitempty"`
	// Unreached are the affected projects' units no changed path reaches
	// through declared reads. Each replays from the cache, so no shard runs it.
	Unreached []types.Unit `json:"unreached,omitempty"`
	// Inherit is present only when the plan inherited a green run's verdict;
	// see PlanInherit. The matrix beside it is then empty on purpose.
	Inherit *PlanInherit `json:"inherit,omitempty"`
	// Risk is present only when the change tiers trivial; see PlanRisk. The matrix
	// beside it is then empty on purpose.
	Risk *PlanRisk `json:"risk,omitempty"`
	// Detail is keyed by shard id and present only under --detail.
	//
	// A SIBLING of Matrix rather than fields on its entries, and that is a hard
	// constraint rather than a preference: the matrix is consumed as a GitHub Actions
	// job matrix (`fromJSON(needs.plan.outputs.matrix)`), where every key in an entry
	// becomes a job DIMENSION. Adding spells or write globs there would multiply the
	// job count or fail the workflow outright, so the detail hangs beside it and
	// the matrix keeps the exact two keys the workflow dereferences.
	Detail map[string]ShardDetail `json:"detail,omitempty"`
	// Outputs is the complete set of name/value pairs a CI provider publishes as
	// job outputs, in a stable order. A provider target forwards the list; it does
	// not decide the membership.
	//
	// The membership is the part that has to live here. It used to live in this
	// repo's magusfile, which hand-listed three names and so silently dropped the
	// fourth when a bad merge reverted that function: ci.yaml went on reading an
	// `inherit` output nothing wrote, read the empty string, and skipped a feature
	// for every run in between. Nothing failed, because a hand-written translator
	// agreeing with a workflow is not a thing either side can check. A plan that
	// states its own outputs makes the next field arrive everywhere at once.
	Outputs []PlanPublish `json:"outputs"`
	// Summary is filled only for `-o template`; the JSON plan carries the typed
	// inputs above, not Markdown.
	Summary string `json:"summary,omitempty"`
}

type PlanShard struct {
	Shard    string `json:"shard"    yaml:"shard"`
	Projects string `json:"projects" yaml:"projects"`
	// JobName is Projects shortened for a CI job name.
	JobName string `json:"job_name" yaml:"job_name"`
	// Digest is a digest of Units, the same for the same units on any plan, so a
	// provider keys a per-shard cache by it.
	Digest string `json:"digest" yaml:"digest"`
	// Duties are the workspace-wide steps this shard runs, space-separated.
	Duties string `json:"duties" yaml:"duties"`
	// Units are what `magus run --stdin --shard` runs for this shard.
	Units []types.Unit `json:"units,omitempty" yaml:"units,omitempty"`
}

// PlanPublish is one CI job output. Value is always single-line: providers write
// these into line-oriented files ($GITHUB_OUTPUT), where an embedded newline is a
// forged entry rather than a formatting bug. PlanOutputs keeps that true at the
// source, so no provider needs escaping logic to be safe.
type PlanPublish struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PlanInherit is the --plan inheritance block: the delta since the branch's
// last green CI run classified entirely low-risk, so that run's verdict stands
// and the emitted matrix is empty. Never a silent skip: every changed path rides
// along with its class and the fact that classified it.
type PlanInherit struct {
	Run    string            `json:"run"`
	Commit string            `json:"commit"`
	Paths  []planInheritPath `json:"paths"`
}

// NewPlanInherit is f as the plan document carries it.
func NewPlanInherit(f *InheritFinding) *PlanInherit {
	pi := &PlanInherit{Run: f.Run, Commit: f.Commit}
	for _, e := range f.Report.Evidence {
		pi.Paths = append(pi.Paths, planInheritPath{Path: e.Path, Tier: string(e.Tier), Class: e.Class, Why: e.Why})
	}
	return pi
}

type planInheritPath struct {
	Path  string `json:"path"`
	Tier  string `json:"tier"`
	Class string `json:"class"`
	Why   string `json:"why"`
}

// PlanRisk is the --plan block of a change that tiers trivial against the plan's base:
// no gate step can observe it, so the matrix is empty, and every changed path rides
// along with its tier and what decided it.
type PlanRisk struct {
	Tier  check.Tier           `json:"tier"`
	Base  string               `json:"base"`
	Paths []check.TierEvidence `json:"paths"`
}

func NewPlanRisk(rep types.TierReport) *PlanRisk {
	return &PlanRisk{Tier: rep.Tier, Base: rep.Base, Paths: rep.Evidence}
}

func planRiskSummaryMarkdown(r *PlanRisk) string {
	var b strings.Builder
	fmt.Fprintf(&b, `### Gate sized %s

`, string(r.Tier))
	fmt.Fprintf(&b, "No shard runs: the change against `%s` tiers %s, so no step of the gate can observe it.\n\n", shortRev(r.Base), string(r.Tier))
	if len(r.Paths) == 0 {
		b.WriteString("No paths changed.\n")
	} else {
		b.WriteString("| Changed path | Tier | Class | Decided by |\n| --- | --- | --- | --- |\n")
		for _, e := range r.Paths {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", e.Path, string(e.Tier), e.Class, e.Why)
		}
	}
	b.WriteString("\nTo run the full gate anyway, run `magus affected ci --no-redundancy-check`.\n")
	return b.String()
}

// PlanOutputs renders the plan's CI job outputs. The matrix is wrapped in the
// `include` key a job matrix expects, so a provider forwards the value verbatim
// instead of knowing that shape.
//
// Adding a name here reaches every provider at once, which is the whole point of
// the field; the workflow keys that consume these live in .github/workflows/ci.yaml
// and TestPlanOutputsCoverWorkflowKeys pins the two together.
func PlanOutputs(out PlanOutput) ([]PlanPublish, error) {
	// Units stay in the plan document: every matrix key is a job dimension.
	include := slices.Clone(out.Matrix)
	for i := range include {
		include[i].Units = nil
	}
	matrix, err := codec.Marshal(map[string]any{"include": include})
	if err != nil {
		return nil, fmt.Errorf("magus affected --plan: encode matrix: %w", err)
	}
	return []PlanPublish{
		{Name: "matrix", Value: string(matrix)},
		{Name: "count", Value: strconv.Itoa(out.Count)},
		{Name: "max_parallel", Value: strconv.Itoa(out.MaxParallel)},
		{Name: "inherit", Value: strconv.FormatBool(out.Inherit != nil)},
	}, nil
}

// PlanSummaryMarkdown renders the job summary: the inheritance report when a green
// run's verdict stands, otherwise the shard table.
//
// An inherited plan carries no shards, so the table would describe an empty fan-out
// and bury the one thing a reader needs, which is why the run it inherited vouched
// for the delta.
func PlanSummaryMarkdown(out PlanOutput) string {
	if out.Inherit != nil {
		evidence := make([]check.TierEvidence, 0, len(out.Inherit.Paths))
		for _, p := range out.Inherit.Paths {
			evidence = append(evidence, check.TierEvidence{Path: p.Path, Tier: check.Tier(p.Tier), Class: p.Class, Why: p.Why})
		}
		return (InheritFinding{
			Run: out.Inherit.Run, Commit: out.Inherit.Commit,
			Report: types.TierReport{Evidence: evidence},
		}).SummaryMarkdown()
	}
	if out.Risk != nil {
		return planRiskSummaryMarkdown(out.Risk)
	}
	var b strings.Builder
	shards := len(out.StageZero) + len(out.Matrix)
	if len(out.StageZero) > 0 {
		fmt.Fprintf(&b, "## CI plan: %s in 2 stages\n\n", shardCount(shards))
		b.WriteString("Stage 0 computes, once, what two or more stage 1 shards share. " +
			"Each stage 1 shard then replays it from the cache.\n\n")
	} else {
		fmt.Fprintf(&b, "## CI plan: %s\n\n", shardCount(shards))
	}
	b.WriteString("| Shard | Projects |\n| --- | --- |\n")
	for _, s := range slices.Concat(out.StageZero, out.Matrix) {
		// The workspace root is "." on the wire and nowhere else; a reader scanning
		// the table for the project that failed is looking for its name.
		projects := s.Projects
		if projects == "." {
			projects = "magus"
		} else if after, ok := strings.CutPrefix(projects, ". "); ok {
			projects = "magus " + after
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", s.Shard, projects)
	}
	fmt.Fprintf(&b, "\nUp to %s of stage 1 run at once. "+
		"Each shard's job starts with its ID, and a red one uploads its logs as `magus-logs-<ID>`.\n",
		shardCount(out.MaxParallel))
	if len(out.Fallbacks) > 0 {
		b.WriteString("\n### The plan used defaults\n\n" +
			"An input was missing, so the plan chose for it. Each row says what that costs this run.\n\n" +
			"| Default | Because | What it means |\n| --- | --- | --- |\n")
		for _, f := range out.Fallbacks {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", f.What, f.Why, f.Effect)
		}
	}
	b.WriteString("\n<details><summary>Why these projects</summary>\n\n" +
		"Run `magus affected ci --explain` locally to see which changed path reached each project. " +
		"[Affected CI guide](https://eli.gladman.cc/magus/guides/affected/), " +
		"[CI target guide](https://eli.gladman.cc/magus/concepts/targets/ci/)\n\n</details>\n")
	return b.String()
}

// NewPlanShard is s as the plan document lists it.
func NewPlanShard(s types.Shard) PlanShard {
	return PlanShard{
		Shard:    s.ID,
		Projects: strings.Join(s.ProjectPaths, " "),
		JobName:  shardName(s.ProjectPaths),
		Digest:   s.Digest,
		Duties:   joinDuties(s.Duties),
		Units:    s.Units,
	}
}

func ReadAffectedPlanPaths(r io.Reader, null bool) ([]string, error) {
	var paths []string
	if null {
		body, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("magus affected --plan: read stdin: %w", err)
		}
		for _, path := range bytes.Split(body, []byte{0}) {
			if len(path) > 0 {
				paths = append(paths, string(path))
			}
		}
	} else {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			if path := scanner.Text(); path != "" {
				paths = append(paths, path)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("magus affected --plan: read stdin: %w", err)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// FailedResults collects the failed target results the invocation's journal records, in
// the order they arrive, so a gate that scrolled its failures away can name them again.
// A ci run under rw verifies more than once, so a later pass clears a
// failure.
type FailedResults struct {
	mu   sync.Mutex
	runs []journal.Run
}

// Handle keeps each target's newest result when it failed; it is a
// recorder.Sink.
func (c *FailedResults) Handle(_ context.Context, r journal.Record) {
	run, ok := r.Run()
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs = slices.DeleteFunc(c.runs, func(f journal.Run) bool {
		return f.Project == run.Project && f.Target == run.Target
	})
	if run.Status == journal.StatusFail {
		c.runs = append(c.runs, run)
	}
}

func (c *FailedResults) List() []journal.Run {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.runs)
}

// gateOutcome is the verdict a finished gate records: session.OutcomePass when runErr
// is nil, sessions.OutcomeUnrun when every failed target failed for a capability this
// host lacks, and session.OutcomeFail otherwise. unrun names each target of an unrun
// gate as "project:target: cause" and is nil for any other outcome. A run that failed
// with no failed target failed: nothing showed a capability was missing.
func gateOutcome(runErr error, failed []journal.Run) (outcome string, unrun []string) {
	if runErr == nil {
		return session.OutcomePass, nil
	}
	for _, e := range failed {
		if !types.CapabilityAbsent(types.DiagnosticCode(e.DiagnosticCode)) {
			return session.OutcomeFail, nil
		}
		cause, _, _ := strings.Cut(strings.TrimSpace(e.Error), "\n")
		unrun = append(unrun, fmt.Sprintf("%s:%s: %s", e.Project, e.Target, cause))
	}
	if len(unrun) == 0 {
		return session.OutcomeFail, nil
	}
	return sessions.OutcomeUnrun, unrun
}
