package ci

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// planRefusal is an MGS3029 refusal. It exits 2, the misuse status: nothing was attempted.
type planRefusal struct{ error }

func (planRefusal) ExitCode() int { return exit.Usage }

func (e planRefusal) Unwrap() error { return e.error }

// RefusePlan is the MGS3029 refusal a saved plan earns when it cannot run as written.
func RefusePlan(format string, args ...any) error {
	return planRefusal{types.DiagnosticErrorf(types.SavedPlanRefused, format, args...)}
}

// ReadSavedPlan reads a `magus affected --plan` document from r and checks that its
// shards can run as written.
func ReadSavedPlan(r io.Reader) (PlanOutput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return PlanOutput{}, fmt.Errorf("magus run --stdin: %w", err)
	}
	return decodeSavedPlan(data)
}

// decodeSavedPlan decodes a plan document and refuses one whose shards could not be run
// as written. Keys it does not know are ignored, so a plan from a newer magus still runs.
func decodeSavedPlan(data []byte) (PlanOutput, error) {
	var p PlanOutput
	if err := codec.Unmarshal(data, &p); err != nil {
		return PlanOutput{}, RefusePlan("the plan on stdin is not a shard plan: %v", err)
	}
	if p.Target == "" {
		return PlanOutput{}, RefusePlan("the plan on stdin names no target; print it again with `%s`",
			hint.Affected.With("<target>", "--plan"))
	}
	if p.Count != len(p.Matrix) {
		return PlanOutput{}, RefusePlan("the plan on stdin states %d shard(s) but lists %d", p.Count, len(p.Matrix))
	}
	every := slices.Concat(p.Matrix, p.StageZero)
	seen := make(map[string]bool, len(every))
	for _, s := range every {
		switch {
		case s.Shard == "":
			return PlanOutput{}, RefusePlan("the plan on stdin has a shard with no id")
		case seen[s.Shard]:
			return PlanOutput{}, RefusePlan("the plan on stdin lists shard %s twice", s.Shard)
		case len(strings.Fields(s.Projects)) == 0:
			return PlanOutput{}, RefusePlan("the plan on stdin: shard %s has no projects", s.Shard)
		case len(s.Units) == 0:
			return PlanOutput{}, RefusePlan("the plan on stdin: shard %s lists no units; print it again with `%s`",
				s.Shard, hint.Affected.With(p.Target, "--plan"))
		}
		if listed, ran := strings.Join(strings.Fields(s.Projects), " "), strings.Join(types.UnitProjects(s.Units), " "); listed != ran {
			return PlanOutput{}, RefusePlan("the plan on stdin: shard %s lists projects %q, but its units run in %q",
				s.Shard, listed, ran)
		}
		if want := types.UnitsDigest(s.Units); s.Digest != want {
			return PlanOutput{}, RefusePlan("the plan on stdin: shard %s has digest %q, but its units digest to %q",
				s.Shard, s.Digest, want)
		}
		seen[s.Shard] = true
	}
	return p, nil
}

// SavedTargetRef is the plan's target with the charms it was evaluated under.
func SavedTargetRef(p PlanOutput) string {
	if len(p.Charms) == 0 {
		return p.Target
	}
	return fmt.Sprintf("%s:%s", p.Target, strings.Join(p.Charms, ","))
}

// AllPlanShards is the --shard value that runs every shard of a saved plan.
const AllPlanShards = "all"

// SelectPlanShards returns the shards a `run --stdin` invocation runs: the one
// named by shard, of either stage, or every shard for AllPlanShards, stage
// zero first. An empty shard selects only a plan of one shard, so no caller
// runs a whole plan by omission. target must agree with the plan, and
// nShards with the size of the named shard's stage.
func SelectPlanShards(p PlanOutput, target, shard string, nShards int) ([]PlanShard, error) {
	if target != p.Target {
		return nil, RefusePlan("the plan is for %s, not %s; drop the target, or give %s with charms (%s:<charm>)",
			p.Target, target, p.Target, p.Target)
	}
	if stage := PlanStage(p, shard); nShards > 0 && nShards != len(stage) {
		return nil, RefusePlan("--n-shards %d, but the plan has %d shard(s); drop --n-shards, the plan supplies it", nShards, len(stage))
	}
	every := slices.Concat(p.StageZero, p.Matrix)
	if shard == AllPlanShards {
		return every, nil
	}
	if shard == "" {
		if len(every) > 1 {
			ids := make([]string, len(every))
			for i, s := range every {
				ids[i] = s.Shard
			}
			return nil, RefusePlan("the plan has %d shards and no --shard selects one: pass --shard <id> (%s), or --shard %s to run every shard here",
				len(every), strings.Join(ids, ", "), AllPlanShards)
		}
		return every, nil
	}
	ids := make([]string, len(every))
	for i, s := range every {
		if s.Shard == shard {
			return []PlanShard{s}, nil
		}
		ids[i] = s.Shard
	}
	if len(ids) == 0 {
		return nil, RefusePlan("--shard %s: the plan has no shards, so there is nothing to select", shard)
	}
	return nil, RefusePlan("--shard %s: the plan has no such shard (it has %s)", shard, strings.Join(ids, ", "))
}

// PlanStage is the stage holding the shard named id: stage zero when one of
// its shards has that ID, else the matrix.
func PlanStage(p PlanOutput, id string) []PlanShard {
	if slices.ContainsFunc(p.StageZero, func(s PlanShard) bool { return s.Shard == id }) {
		return p.StageZero
	}
	return p.Matrix
}

// PlanProjects is the projects shards run, in plan order, each once.
func PlanProjects(shards []PlanShard) []string {
	projects := make([]string, 0, len(shards))
	seen := map[string]bool{}
	for _, s := range shards {
		for _, p := range strings.Fields(s.Projects) {
			if !seen[p] {
				seen[p] = true
				projects = append(projects, p)
			}
		}
	}
	return projects
}

// PlanTargets is the units shards run, in plan order, each once.
func PlanTargets(shards []PlanShard) []types.Target {
	var targets []types.Target
	seen := map[types.Unit]bool{}
	for _, s := range shards {
		for _, u := range s.Units {
			if !seen[u] {
				seen[u] = true
				targets = append(targets, types.Target{Path: u.Project, Name: u.Target})
			}
		}
	}
	return targets
}

// PlanUnitList is units as space-separated project:target.
func PlanUnitList(units []types.Unit) string {
	return strings.Join(types.UnitNames(units), " ")
}

// SameCharms reports whether a and b hold the same charms in any order.
func SameCharms(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}
