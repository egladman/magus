package guard

import (
	"context"
	"errors"
	"time"

	"github.com/egladman/magus/internal/trail"
)

// hostHookTimeout is how long the shipped host wiring waits on a hook before it kills the
// hook and runs the call unjudged, with no trail row and no word to the agent.
const hostHookTimeout = 10 * time.Second

// hookBudget bounds one hook judgment, from the moment magus starts judging to its verdict.
// It answers well inside hostHookTimeout: the rest is process start and exit, which a
// loaded machine stretches too.
const hookBudget = 7 * time.Second

// judgeReserve is held back from every phase for wording the verdict and writing the trail
// row once the phases are done.
const judgeReserve = 500 * time.Millisecond

// The phases of a judgment, as the trail row names them.
const (
	phaseLoadRules       = "load_rules"
	phaseApprovedResolve = "approved_resolve"
	phaseApprovedRule    = "approved_rule"
	phaseWorktreeRule    = "worktree_rule"
	phaseJudge           = "judge"
)

// WithHookDeadline bounds ctx to the hook's budget from now, seven seconds against the
// host's ten. A deadline ctx already carries that falls earlier stands.
func WithHookDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, hookBudget)
}

// phaseShares is how a judgment divides its deadline: each phase still to run holds its
// shares, and a phase that starts spends its shares' part of what remains, so one that
// answers early leaves its time to the ones after it.
type phaseShares map[string]int

// take removes phase from the table and returns what it may spend of ctx's remaining time,
// less judgeReserve: zero when nothing is left, never less. bounded is false when ctx
// carries no deadline, and the phase then runs to completion.
func (p phaseShares) take(ctx context.Context, phase string) (budget time.Duration, bounded bool) {
	shares := p[phase]
	delete(p, phase)
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, false
	}
	total := shares
	for _, s := range p {
		total += s
	}
	remaining := time.Until(deadline) - judgeReserve
	return max(remaining*time.Duration(shares)/time.Duration(max(total, 1)), 0), true
}

// TimeRuleLoad runs load, which reads the workspace's guard rules, and times it against
// what remains of ctx's deadline. The phase is lapsed only when load failed after the
// deadline passed.
//
// load runs under ctx itself rather than a share of it: the loaded session keeps the
// context it was loaded with, and a share that ended would end every rule call after it.
// Its budget is therefore all that remains, and the phases after it divide what it leaves.
// With no deadline on ctx it has no budget and never lapses.
func TimeRuleLoad[T any](ctx context.Context, load func() (T, error)) (T, trail.Phase, error) {
	budget, _ := phaseShares{phaseLoadRules: 1}.take(ctx, phaseLoadRules)
	start := time.Now()
	got, err := load()
	return got, trail.Phase{
		Name:      phaseLoadRules,
		ElapsedMs: time.Since(start).Milliseconds(),
		BudgetMs:  budget.Milliseconds(),
		Lapsed:    err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded),
	}, err
}

// phaseLog collects the phases of one judgment for its trail row. A judgment runs its
// phases one at a time, so it takes no lock.
type phaseLog struct {
	start time.Time
	done  []trail.Phase
}

type phaseLogKey struct{}

// withPhaseLog starts timing a judgment, with the rule load its caller timed, if any, as
// the first phase.
func withPhaseLog(ctx context.Context, load trail.Phase) context.Context {
	l := &phaseLog{start: time.Now()}
	if load.Name != "" {
		l.done = append(l.done, load)
	}
	return context.WithValue(ctx, phaseLogKey{}, l)
}

// judgmentPhases is the judgment's phase log, nil outside Judge.
func judgmentPhases(ctx context.Context) *phaseLog {
	l, _ := ctx.Value(phaseLogKey{}).(*phaseLog)
	return l
}

// record adds one phase. A nil log records nothing.
func (l *phaseLog) record(p trail.Phase) {
	if l == nil {
		return
	}
	l.done = append(l.done, p)
}

// phases is every recorded phase, then the whole judgment so far. Nil for a nil log.
func (l *phaseLog) phases() []trail.Phase {
	if l == nil {
		return nil
	}
	return append(l.done[:len(l.done):len(l.done)], trail.Phase{Name: phaseJudge, ElapsedMs: time.Since(l.start).Milliseconds()})
}

// timedPhase is one phase's trail entry.
func timedPhase(name string, elapsed, budget time.Duration, lapsed bool) trail.Phase {
	return trail.Phase{Name: name, ElapsedMs: elapsed.Milliseconds(), BudgetMs: budget.Milliseconds(), Lapsed: lapsed}
}

// ruleLapse is a phase of a judgment that ran out of its share of the deadline.
type ruleLapse struct {
	// side is the side the phase belongs to, as Event.DecidedBy names it.
	side   string
	budget time.Duration
	// said words the phase for a reader who has not been told the side: "the approved
	// magus\guard.command rule" did not "answer". failed words it under the side's own
	// failure line: "the rule" did not "answer".
	said, failed lapseWording
}

// lapseWording is a phase as one sentence names it: subject did not verb within the budget.
type lapseWording struct {
	subject, verb string
}

// clause words the phase against budget, or as having had none left at all.
func (w lapseWording) clause(budget time.Duration) string {
	if budget <= 0 {
		return w.subject + " had no time left before the guard's deadline"
	}
	return w.subject + " did not " + w.verb + " within " + roundBudget(budget).String()
}

// what says what ran out, and of how much: "the approved magus\guard.command rule did not
// answer within 2.1s".
func (l ruleLapse) what() string { return l.said.clause(l.budget) }

// failure is the side's failure line: "the rule did not answer within 2.1s".
func (l ruleLapse) failure() string { return l.failed.clause(l.budget) }

// roundBudget rounds a budget for a reader: tenths of a second from one second up.
func roundBudget(d time.Duration) time.Duration {
	if d >= time.Second {
		return d.Round(100 * time.Millisecond)
	}
	return d.Round(time.Millisecond)
}

// lapseReason is the deny for a call that changes state when a phase of its judgment ran out
// of time.
func lapseReason(call judgedCall, l ruleLapse) string {
	return "magus ran out of time judging " + call.what + ": " + l.what() + ". " +
		"A call that changes state is denied rather than let through unjudged; retry it. " +
		"The guard answers within " + hookBudget.String() + ", inside the host's hook timeout, " +
		"because a hook the host cuts off is a call it runs with no rule applied."
}
