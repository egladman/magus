package job

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/job"
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
	// A guard that ran out of time grading the check defers it too.
	Deferred string
}

// CheckGrader judges row's check the way the guard will judge row's holder running it in
// the checkout at root, against that workspace's rules. The guard lives above this
// package, so the CLI supplies the implementation. One grader serves every checkout a
// door opens stores for, so it must be safe for concurrent use.
type CheckGrader func(ctx context.Context, root string, row job.Job) CheckVerdict

// checkGrader is the store's grader. A store whose Location names none grades every check
// as [noGrader] does, so a missing grader is a problem the fork reports, never a pass.
func (s *Store) checkGrader() CheckGrader {
	if s.grader == nil {
		return noGrader
	}
	return s.grader
}

// noGraderReason is why a check graded with no grader reached no verdict.
const noGraderReason = "the job store was opened with no check grader (Location.Grader), so nothing judges whether the holder may run it"

// noGrader is the grade of a store with no grader: no verdict, said as NotGraded.
func noGrader(context.Context, string, job.Job) CheckVerdict {
	return CheckVerdict{NotGraded: noGraderReason}
}

// WithGrader returns a Store over s's file that grades a declared check with g in place of
// s's own: a fork wraps the grader it validates with in kept verdicts
// that live as long as that fork. The view takes its own mutex, as [Store.As] does.
func (s *Store) WithGrader(g CheckGrader) *Store {
	v := s.view()
	v.grader = g
	return v
}

// KeptVerdicts keeps the verdicts a grader reached, under a key the caller derives from
// everything the verdict reads, for a caller that grades the same checks again and again
// against a tree it holds still. It keeps no deferred verdict: a deferral is the clock's or
// the binary's, and the next grade may reach one. Safe for concurrent use.
type KeptVerdicts struct {
	mu       sync.Mutex
	verdicts map[string]CheckVerdict
	judged   int
}

// NewKeptVerdicts is an empty KeptVerdicts.
func NewKeptVerdicts() *KeptVerdicts {
	return &KeptVerdicts{verdicts: map[string]CheckVerdict{}}
}

// Grade answers the verdict kept under key, else the one judge reaches, kept unless it is
// deferred. An empty key keeps nothing and always judges. Two grades of one key at once may
// both judge.
func (m *KeptVerdicts) Grade(key string, judge func() CheckVerdict) CheckVerdict {
	if key != "" {
		m.mu.Lock()
		v, ok := m.verdicts[key]
		m.mu.Unlock()
		if ok {
			return v
		}
	}
	v := judge()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.judged++
	if key != "" && v.Deferred == "" {
		m.verdicts[key] = v
	}
	return v
}

// Judged is how many grades reached judge.
func (m *KeptVerdicts) Judged() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.judged
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

// deniedCheckProblems is every check of row, its own or a check postcondition's, the guard would deny
// its holder, as JobCheckUnrunnable. The holder runs every check under the lease, so a
// denied one leaves the job unable to verify itself and ends it unverified. Each check is
// graded as the row with that check, and a check two postconditions share is graded once. A check
// the grader reached no verdict on is one JobUngraded problem, and grading stops there: why
// no verdict was reached is the workspace's or the binary's, and holds for every check. A
// deferred grade ends the grading the same way but is no problem: the row is stored. A
// Store whose Location names no grader grades every check as [noGrader], one JobUngraded
// problem, and a job with no check finds nothing.
func deniedCheckProblems(ctx context.Context, store *Store, row job.Job) types.JobProblems {
	if store == nil {
		return nil
	}
	grader := store.checkGrader()
	var (
		graded []types.LeaseCheck
		ps     types.JobProblems
	)
	checks, err := effectiveChecks(row)
	if err != nil {
		return types.JobProblems{unreadableCheckProblem(err)}
	}
	for _, gc := range checks {
		postcondition, check := gc.Postcondition, gc.Check
		if postcondition.Kind != job.PostconditionKindCheck || check.Target == "" && check.Script == "" {
			continue
		}
		if slices.ContainsFunc(graded, func(c types.LeaseCheck) bool { return sameLeaseCheck(&c, &check) }) {
			continue
		}
		graded = append(graded, check)
		pending := row
		pending.Check = postcondition.Check
		verdict := grader(ctx, store.root, pending)
		which := "check"
		if postcondition.ID != job.PrimaryPostconditionID {
			which = fmt.Sprintf("postcondition %s's check", postcondition.ID)
		}
		if verdict.Deferred != "" {
			return ps
		}
		if verdict.NotGraded != "" {
			p := ungradedCheckProblem(row.ID, which, check, verdict.NotGraded)
			p.Field = postconditionCheckField(row, postcondition)
			if verdict.NotGraded == noGraderReason {
				p.Fix = "open the job store with a Location that names its Grader, as magus's own doors do"
			}
			return append(ps, p)
		}
		if !verdict.Denied {
			continue
		}
		p := deniedCheckProblem(row, which, check, verdict)
		p.Field = postconditionCheckField(row, postcondition)
		ps = append(ps, p)
	}
	return ps
}

// unreadableCheckProblem is a row whose check does not read as a lease check, which no
// store keeps: the record is refused rather than graded as checking nothing.
func unreadableCheckProblem(err error) types.JobProblem {
	return types.JobProblem{Code: types.JobRecordInvalid, Field: "check", Message: err.Error(),
		Fix: "write the check as {target, project, args, no_default_charms} or {script, args, test}"}
}

// ungradedCheckProblem words a check of row id the guard reached no verdict on, quoting why.
func ungradedCheckProblem(id, which string, check types.LeaseCheck, why string) types.JobProblem {
	return types.JobProblem{
		Code: types.JobUngraded,
		Message: fmt.Sprintf("%s's %s `%s` could not be graded against the guard: %s,"+
			" so nothing says its worker may run it", id, which, CheckCommand(check), strings.Join(strings.Fields(why), " ")),
		Fix: fmt.Sprintf("a magus older than this tree cannot judge it, so fork with one built at this checkout's HEAD or later; if that one refuses too, `%s` names what it cannot load", hint.Doctor),
	}
}

// replacementCheckFix is what a denied check of row becomes.
// The compiling check grades only Go, so another row gets its own kind.
func replacementCheckFix(row job.Job) string {
	writes := func(ext string) (string, bool) {
		i := slices.IndexFunc(row.Paths.Write, func(p string) bool { return path.Ext(p) == ext })
		if i < 0 {
			return "", false
		}
		return row.Paths.Write[i], true
	}
	_, goSource := writes(".go")
	script, buzz := writes(".buzz")
	switch {
	case !goSource && buzz:
		return fmt.Sprintf(`declare the script check that tests what it writes, with "check": {"script": %q, "test": true}`, script)
	case !goSource && len(row.Paths.Write) > 0:
		return fmt.Sprintf("declare a check that grades what it writes, with `magus job fork %s --check '<target> <project>'`", row.ID)
	}
	compiling := CompilingCheck()
	recommended := fmt.Sprintf("%s %s", compiling.Target, compiling.Project)
	return fmt.Sprintf("declare a target that compiles without building the binary, such as `%s`,"+
		" with `magus job fork %s --check '%s'`", recommended, row.ID, recommended)
}

// deniedCheckProblem words the problem with row's check. A target a person runs
// can never be a worker's check, so that problem asks for another.
// Any other deny gets [replacementCheckFix].
func deniedCheckProblem(row job.Job, which string, check types.LeaseCheck, verdict CheckVerdict) types.JobProblem {
	id := row.ID
	reason := strings.Join(strings.Fields(verdict.Reason), " ")
	if verdict.Rule == humanOnlyRule {
		return types.JobProblem{
			Code: types.JobCheckUnrunnable,
			Message: fmt.Sprintf("%s's %s `%s` is a target a person runs (%s): %s,"+
				" and a worker is refused it whoever asks, so it can never verify the job",
				id, which, CheckCommand(check), humanOnlyRule, reason),
			Fix: fmt.Sprintf("declare a check its worker may run in its place, with `magus job fork %s --check '<target> <project>'`", id),
		}
	}
	rule := ""
	if verdict.Rule != "" {
		rule = fmt.Sprintf(" (%s)", verdict.Rule)
	}
	return types.JobProblem{
		Code: types.JobCheckUnrunnable,
		Message: fmt.Sprintf("%s's %s `%s` would be denied to its worker by the guard%s: %s,"+
			" and the holder runs the check under its lease, so a denied one can never verify the job",
			id, which, CheckCommand(check), rule, reason),
		Fix: replacementCheckFix(row),
	}
}
