package job

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// CheckVerdict is a [CheckGrader]'s answer. Denied is false for a check the guard lets the
// holder run, and for one the grader could not judge: a grader that cannot reach a verdict
// says why in NotGraded, which the fork reports as JobUngraded rather than as a deny, since
// no verdict on the check was reached, and never as an accept.
type CheckVerdict struct {
	Denied bool
	// Rule names the rule that refused, as the guard prints it.
	Rule string
	// Reason is the guard's own wording, quoted back to the person forking.
	Reason string
	// NotGraded says why no verdict on the check was reached, empty when one was.
	NotGraded string
	// Deferred says why the grade waits for the checkout, empty when it does not: the
	// forking binary could not load the workspace's rules, which a binary built at the
	// checkout may. The fork stores the row, and the grader has already said so on stderr.
	Deferred string
}

// CheckGrader judges row's check the way the guard will judge row's holder running it in
// the checkout at root, against that workspace's rules. The guard lives above this
// package, so the CLI supplies the implementation. One grader serves every checkout a
// door opens stores for, so it must be safe for concurrent use.
type CheckGrader func(ctx context.Context, root string, row types.Job) CheckVerdict

var (
	installedGraderMu sync.RWMutex
	installedGrader   CheckGrader
)

// SetCheckGrader installs the grader a Store whose Location names none grades a declared
// check with. The composition root installs one, because the grader runs the guard's
// rules and the guard imports the doors that open stores; with none installed such a
// Store grades nothing. It returns the grader it replaced, for a test that swaps in its
// own to put back. Safe for concurrent use: a fork running meanwhile grades with the old
// grader or the new one.
func SetCheckGrader(g CheckGrader) (previous CheckGrader) {
	installedGraderMu.Lock()
	defer installedGraderMu.Unlock()
	previous, installedGrader = installedGrader, g
	return previous
}

// checkGrader is the store's own grader, else the installed one.
func (s *Store) checkGrader() CheckGrader {
	if s.grader != nil {
		return s.grader
	}
	return installedCheckGrader()
}

// installedCheckGrader is the grader [SetCheckGrader] installed, nil when none is.
func installedCheckGrader() CheckGrader {
	installedGraderMu.RLock()
	defer installedGraderMu.RUnlock()
	return installedGrader
}

// CompilingCheck is the check a refusal recommends: it compiles the workspace without
// linking its binary, so a worker the guard keeps from building can still run it. The
// grader also judges it to tell a deny of the declared check from a deny of every command
// the worker runs.
func CompilingCheck() types.LeaseCheck {
	return types.LeaseCheck{Target: "go::go-vet", Project: "."}
}

// humanOnlyRule is the guard rule that refuses a run of a target the workspace declares a
// person runs.
const humanOnlyRule = "human-only"

// deniedCheckProblems is every check of row, its own or a check goal's, the guard would deny
// its holder, as JobCheckUnrunnable. The holder runs every check under the lease, so a
// denied one leaves the job unable to verify itself and ends it unverified. Each check is
// graded as the row with that check, and a check two goals share is graded once. A check
// the grader reached no verdict on is one JobUngraded problem, and grading stops there: why
// no verdict was reached is the workspace's or the binary's, and holds for every check. A
// deferred grade ends the grading the same way but is no problem: the row is stored. A
// Store with no grader of its own grades with the one [SetCheckGrader] installed; with
// neither, and for a job with no check, nothing is found.
func deniedCheckProblems(ctx context.Context, store *Store, row types.Job) types.JobProblems {
	if store == nil {
		return nil
	}
	grader := store.checkGrader()
	if grader == nil {
		return nil
	}
	var (
		graded []types.LeaseCheck
		ps     types.JobProblems
	)
	for _, goal := range row.EffectiveGoals() {
		if goal.Kind != types.GoalKindCheck || goal.Check.Target == "" && goal.Check.Script == "" {
			continue
		}
		if slices.ContainsFunc(graded, func(c types.LeaseCheck) bool { return types.SameLeaseCheck(&c, &goal.Check) }) {
			continue
		}
		graded = append(graded, goal.Check)
		pending := row
		pending.Check = &goal.Check
		verdict := grader(ctx, store.root, pending)
		which := "check"
		if goal.ID != types.PrimaryGoalID {
			which = "goal " + goal.ID + "'s check"
		}
		if verdict.Deferred != "" {
			return ps
		}
		if verdict.NotGraded != "" {
			p := ungradedCheckProblem(row.ID, which, goal.Check, verdict.NotGraded)
			p.Field = goalCheckField(row, goal)
			return append(ps, p)
		}
		if !verdict.Denied {
			continue
		}
		p := deniedCheckProblem(row.ID, which, goal.Check, verdict)
		p.Field = goalCheckField(row, goal)
		ps = append(ps, p)
	}
	return ps
}

// ungradedCheckProblem words a check of row id the guard reached no verdict on, quoting why.
func ungradedCheckProblem(id, which string, check types.LeaseCheck, why string) types.JobProblem {
	return types.JobProblem{
		Code: types.JobUngraded,
		Message: fmt.Sprintf("%s's %s `%s` could not be graded against the guard: %s,"+
			" so nothing says its worker may run it", id, which, check.String(), strings.Join(strings.Fields(why), " ")),
		Fix: fmt.Sprintf("make the guard load in this checkout (`%s` names what it cannot load), then fork again", hint.Doctor),
	}
}

// deniedCheckProblem words the problem with row id's check, which names it in the row. A
// target a person runs is no check a worker could ever run, so that problem names the
// rule's reason and asks for another check; any other deny recommends the compiling check,
// which the guard lets a worker it keeps from building run.
func deniedCheckProblem(id, which string, check types.LeaseCheck, verdict CheckVerdict) types.JobProblem {
	reason := strings.Join(strings.Fields(verdict.Reason), " ")
	if verdict.Rule == humanOnlyRule {
		return types.JobProblem{
			Code: types.JobCheckUnrunnable,
			Message: fmt.Sprintf("%s's %s `%s` is a target a person runs (%s): %s,"+
				" and a worker is refused it whoever asks, so it can never verify the job",
				id, which, check.String(), humanOnlyRule, reason),
			Fix: fmt.Sprintf("declare a check its worker may run in its place, with `magus job fork %s --check '<target> <project>'`", id),
		}
	}
	rule := ""
	if verdict.Rule != "" {
		rule = " (" + verdict.Rule + ")"
	}
	compiling := CompilingCheck()
	recommended := compiling.Target + " " + compiling.Project
	return types.JobProblem{
		Code: types.JobCheckUnrunnable,
		Message: fmt.Sprintf("%s's %s `%s` would be denied to its worker by the guard%s: %s,"+
			" and the holder runs the check under its lease, so a denied one can never verify the job",
			id, which, check.String(), rule, reason),
		Fix: fmt.Sprintf("declare a target that compiles without building the binary, such as `%s`,"+
			" with `magus job fork %s --check '%s'`", recommended, id, recommended),
	}
}
