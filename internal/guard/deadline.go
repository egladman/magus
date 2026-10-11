package guard

import (
	"context"
	"errors"
	"time"

	"github.com/egladman/magus/internal/trail"
)

// HookBudget bounds one hook judgment, from the moment magus starts judging to its verdict.
// The shipped host wiring kills a hook at ten seconds and runs the call unjudged, with no
// trail row and no word to the agent, so the guard answers well inside that: the rest is
// process start and exit, which a loaded machine stretches too.
const HookBudget = 7 * time.Second

// judgeReserve is held back from every phase for wording the verdict and writing the trail
// row once the phases are done.
const judgeReserve = 500 * time.Millisecond

// resolveShares is the approved resolve's weight against a rule call's one. Resolving runs a
// VCS status and loads the committed magusfile a second time; a rule call is one function.
const resolveShares = 2

// The phases of a judgment, as the trail row names them.
const (
	phaseLoadRules       = "load_rules"
	phaseApprovedResolve = "approved_resolve"
	phaseApprovedRule    = "approved_rule"
	phaseWorktreeRule    = "worktree_rule"
	phaseJudge           = "judge"
)

// WithHookDeadline bounds ctx to HookBudget from now. A deadline ctx already carries that
// falls earlier stands.
func WithHookDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, HookBudget)
}

// phaseBudget is what a phase holding shares of the left shares still to run may spend of
// ctx's remaining time, less judgeReserve, so a phase that answers early leaves its time to
// the ones after it. Zero or less when nothing is left. With no deadline on ctx it is the
// same proportion of HookBudget.
func phaseBudget(ctx context.Context, shares, left int) time.Duration {
	remaining := HookBudget
	if deadline, ok := ctx.Deadline(); ok {
		remaining = time.Until(deadline) - judgeReserve
	}
	return remaining * time.Duration(shares) / time.Duration(max(left, shares, 1))
}

// TimeRuleLoad runs load, which reads the workspace's guard rules, and times it against what
// remains of ctx's deadline. It is lapsed only when load failed after the deadline passed.
//
// load runs under ctx itself rather than a share of it: the loaded session keeps the
// context it was loaded with, and a share that ended would end every rule call after it. Its
// budget is therefore all that remains, and the phases after it divide what it leaves. With
// no deadline on ctx it has no budget and never lapses.
func TimeRuleLoad(ctx context.Context, load func() error) trail.Phase {
	var budget time.Duration
	if _, ok := ctx.Deadline(); ok {
		budget = phaseBudget(ctx, 1, 1)
	}
	start := time.Now()
	err := load()
	return trail.Phase{
		Name:      phaseLoadRules,
		ElapsedMs: time.Since(start).Milliseconds(),
		BudgetMs:  budget.Milliseconds(),
		Lapsed:    err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
}

// judgeClock collects the phases of one judgment for its trail row. A judgment runs its
// phases one at a time, so it takes no lock.
type judgeClock struct {
	start  time.Time
	phases []trail.Phase
}

type clockKey struct{}

// withJudgeClock starts timing a judgment, with the rule load its caller timed, if any, as
// the first phase.
func withJudgeClock(ctx context.Context, load trail.Phase) context.Context {
	c := &judgeClock{start: time.Now()}
	if load.Name != "" {
		c.phases = append(c.phases, load)
	}
	return context.WithValue(ctx, clockKey{}, c)
}

// clockFrom is the judgment's clock, nil outside Judge.
func clockFrom(ctx context.Context) *judgeClock {
	c, _ := ctx.Value(clockKey{}).(*judgeClock)
	return c
}

// record adds one phase. A nil clock records nothing.
func (c *judgeClock) record(name string, elapsed, budget time.Duration, lapsed bool) {
	if c == nil {
		return
	}
	c.phases = append(c.phases, trail.Phase{Name: name, ElapsedMs: elapsed.Milliseconds(), BudgetMs: max(budget, 0).Milliseconds(), Lapsed: lapsed})
}

// trail is every recorded phase, then the whole judgment so far. Nil for a nil clock.
func (c *judgeClock) trail() []trail.Phase {
	if c == nil {
		return nil
	}
	return append(c.phases[:len(c.phases):len(c.phases)], trail.Phase{Name: phaseJudge, ElapsedMs: time.Since(c.start).Milliseconds()})
}

// ruleLapse is a phase of a judgment that ran out of its share of the deadline.
type ruleLapse struct {
	// side is the side the phase belongs to, as Event.DecidedBy names it.
	side string
	// what says what ran out, and of how much: "the approved magus\guard.command rule did
	// not answer within 2.1s".
	what string
}

// lapseClause words a phase that ran out of budget: subject did not verb within it, or had
// none left at all.
func lapseClause(subject, verb string, budget time.Duration) string {
	if budget <= 0 {
		return subject + " had no time left before the guard's deadline"
	}
	return subject + " did not " + verb + " within " + roundBudget(budget).String()
}

// roundBudget rounds a budget for a reader: tenths of a second from one second up.
func roundBudget(d time.Duration) time.Duration {
	if d >= time.Second {
		return d.Round(100 * time.Millisecond)
	}
	return d.Round(time.Millisecond)
}

// lapseDenial is the deny for a call that changes state when a phase of its judgment ran out
// of time.
func lapseDenial(call unloadedCall, l ruleLapse) string {
	return "magus ran out of time judging " + call.what + ": " + l.what + ". " +
		"A call that changes state is denied rather than let through unjudged; retry it. " +
		"The guard answers within " + HookBudget.String() + ", inside the host's hook timeout, " +
		"because a hook the host cuts off is a call it runs with no rule applied."
}
