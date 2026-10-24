package job

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// UnrunGate is a target of the workspace's ci chain whose sources cover files a job may
// write, and which neither the job's check nor any of its goals runs.
type UnrunGate struct {
	// Target and Project are what `magus run <target> <project>` takes.
	Target  string `json:"target"  yaml:"target"`
	Project string `json:"project" yaml:"project"`
	// Source is the target's source glob that matched a written file.
	Source string `json:"source"  yaml:"source"`
	// Goal is the goal that would add the target to the job, ready to put in its goals: a
	// form the guard lets the job's worker run. Zero when IntegratorGates.
	Goal types.Goal `json:"goal"    yaml:"goal"`
	// IntegratorGates marks a target no form of which the guard lets a worker run, such as
	// the build of magus itself. The integrator runs it, so no goal or waiver is asked for.
	IntegratorGates bool `json:"integrator_gates,omitempty" yaml:"integrator_gates,omitempty"`
}

// gradeFunc is the guard's verdict on a check run by the holder of the row being gated.
// Nil grades nothing, which offers every goal as it is.
type gradeFunc func(types.LeaseCheck) CheckVerdict

// gradeWith grades a check as the row with that check, the way [deniedCheckProblems]
// does. A nil grader grades nothing.
func gradeWith(grader CheckGrader, root string, row types.Job) gradeFunc {
	if grader == nil {
		return nil
	}
	return func(c types.LeaseCheck) CheckVerdict {
		pending := row
		pending.Check = &c
		return grader(context.Background(), root, pending)
	}
}

// gradedGoal is goal in the form the guard lets the holder run: as given, else without
// default charms. It reports true, with no goal, when neither form may run. A verdict that
// reached no conclusion (NotGraded) offers the goal as given; [deniedCheckProblems] reports
// the missing grade once for the whole row.
func gradedGoal(goal types.Goal, grade gradeFunc) (types.Goal, bool) {
	if grade == nil {
		return goal, false
	}
	charmless := goal
	charmless.Check.NoDefaultCharms = true
	for _, candidate := range []types.Goal{goal, charmless} {
		if !grade(candidate.Check).Denied {
			return candidate, false
		}
	}
	return types.Goal{}, true
}

// unrunGatesLimit is how many gates a text rendering lists before it counts the rest.
const unrunGatesLimit = 6

// ciTarget is the target the gate is named after, run from the workspace's root project.
const ciTarget = "ci"

// UnrunGates lists the ci chain's targets that read files the row may write and that
// neither its check nor a goal runs, in the order ci runs them. It is a note for the
// orchestrator, never a refusal: a job whose check is `go-test .` passes while the linters
// that would have caught its style defects never ran, and the root meets them at the gate.
//
// A chain member of a check or goal counts as run, because running the target runs what it
// composes. A target another listed target composes is left out for the same reason, and a
// target RegeneratedOutside already lists is left out because the root regenerates it. A
// script check covers nothing: what it runs is not a target.
//
// Each goal is graded by the installed [CheckGrader] before it is offered: the form a
// worker may run, else none, with the gate marked IntegratorGates. A workspace with no root
// project, or one whose root has no ci chain, yields nil.
func UnrunGates(root string, row types.Job, projects []*types.Project, regenerated []RegeneratedOutput) []UnrunGate {
	return unrunGates(root, row, projects, regenerated, gradeWith(installedCheckGrader(), root, row))
}

// unrunGates is [UnrunGates] graded by grade.
func unrunGates(root string, row types.Job, projects []*types.Project, regenerated []RegeneratedOutput, grade gradeFunc) []UnrunGate {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil
	}
	byPath := map[string]*types.Project{}
	for _, p := range projects {
		byPath[p.Path] = p
	}
	lookup := func(path string) *types.Project { return byPath[path] }
	top := byPath["."]
	if top == nil {
		return nil
	}

	run := runTargets(row, lookup)
	var (
		written []string
		walked  bool
		found   []unrunFound
	)
	_ = types.WalkChain(top, ciTarget, lookup, func(v types.ChainVisit) error {
		if v.Depth == 0 || run[v.Key()] {
			return nil
		}
		label := v.Project.Path + ":" + v.Target
		if slices.ContainsFunc(regenerated, func(o RegeneratedOutput) bool { return o.Target == label }) {
			return nil
		}
		if !walked {
			written, walked = writtenFiles(root, row), true
		}
		sources, _ := targetSources(v.Project, v.Target)
		source, _, ok := sourcedBy(sources, written)
		if !ok {
			return nil
		}
		found = append(found, unrunFound{
			gate:  UnrunGate{Target: v.Target, Project: v.Project.Path, Source: source.String()},
			visit: v,
		})
		return nil
	})

	return offeredGates(found, row, lookup, grade)
}

// offeredGates drops the found gates another found gate composes, and gives each one left
// the goal [gradedGoal] allows.
func offeredGates(found []unrunFound, row types.Job, lookup func(string) *types.Project, grade gradeFunc) []UnrunGate {
	composed := map[string]bool{}
	for _, u := range found {
		_ = types.WalkChain(u.visit.Project, u.visit.Target, lookup, func(m types.ChainVisit) error {
			if m.Depth > 0 {
				composed[m.Key()] = true
			}
			return nil
		})
	}
	taken := map[string]bool{}
	for _, g := range row.EffectiveGoals() {
		taken[g.ID] = true
	}
	var out []UnrunGate
	for _, u := range found {
		if composed[u.visit.Key()] {
			continue
		}
		u.gate.Goal, u.gate.IntegratorGates = gradedGoal(unrunGoal(u.gate.Project, u.gate.Target, taken), grade)
		out = append(out, u.gate)
	}
	return out
}

// unrunFound is a gate with the chain visit that found it.
type unrunFound struct {
	gate  UnrunGate
	visit types.ChainVisit
}

// runTargets is the key of every chain member a check or goal of row runs.
func runTargets(row types.Job, lookup func(string) *types.Project) map[string]bool {
	run := map[string]bool{}
	for _, g := range row.EffectiveGoals() {
		if g.Kind != types.GoalKindCheck || g.Check.Script != "" || g.Check.Target == "" {
			continue
		}
		p := lookup(cmp.Or(g.Check.Project, "."))
		names := []string{g.Check.Target}
		if _, tail, ok := strings.Cut(g.Check.Target, "::"); ok {
			names = append(names, tail)
		}
		for _, name := range names {
			_ = types.WalkChain(p, name, lookup, func(v types.ChainVisit) error {
				run[v.Key()] = true
				return nil
			})
		}
	}
	return run
}

var nonIDChars = regexp.MustCompile(`[^a-z0-9]+`)

// unrunGoal is the goal that runs target in project: the target's name, with the project
// after it when it is not the root, and a counter after that when taken already holds the
// id. Two projects can fold to one id ("a/b" and "a-b"), and a target can share its name
// with a goal the row holds. The id it returns is added to taken.
func unrunGoal(project, target string, taken map[string]bool) types.Goal {
	base := target
	if project != "." {
		base += "-" + project
	}
	base = strings.Trim(nonIDChars.ReplaceAllString(strings.ToLower(base), "-"), "-")
	id := base
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	taken[id] = true
	return types.Goal{ID: id, Check: types.LeaseCheck{Target: target, Project: project}}.Resolve()
}

const unrunHeading = "not run by the check or any goal, and run by ci over your write paths"

// renderUnrunGates writes gates as the block [Terms] renders. Nothing is written for none.
func renderUnrunGates(w io.Writer, gates []UnrunGate) {
	lines := unrunLines(gates)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w, unrunHeading)
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
}

// unrunLines is one line per gate up to unrunGatesLimit, then the goals that add the ones
// listed as the JSON a job's goals take.
func unrunLines(gates []UnrunGate) []string {
	if len(gates) == 0 {
		return nil
	}
	shown := gates[:min(len(gates), unrunGatesLimit)]
	var (
		lines []string
		goals []types.Goal
	)
	for _, g := range shown {
		line := fmt.Sprintf("%s %s (reads %s)", g.Target, g.Project, g.Source)
		if g.IntegratorGates {
			lines = append(lines, line+": the integrator gates it; no worker may run it")
			continue
		}
		lines = append(lines, line)
		goals = append(goals, g.Goal)
	}
	if rest := len(gates) - len(shown); rest > 0 {
		lines = append(lines, fmt.Sprintf("and %d more; -o json lists every one", rest))
	}
	if len(goals) == 0 {
		return lines
	}
	if data, err := json.Marshal(goals); err == nil {
		lines = append(lines, "goals to add: "+string(data))
	}
	return lines
}
