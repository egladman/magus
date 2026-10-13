package guard

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/trail"
)

// hookBudget bounds one hook judgment, from the moment magus starts judging to its verdict.
// It answers well inside the ten seconds a shipped wiring waits on a hook: the rest is
// process start and exit, which a loaded machine stretches too.
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

// dryRunBudget bounds a dry-run judgment. No host waits on it, but a fork or a verb's own
// edit must not hang on a rule that loops.
const dryRunBudget = 2 * time.Minute

type dryRunKey struct{}

// withDryRunDeadline bounds ctx to dryRunBudget and marks it a dry run, so a lapse is worded
// as a call not graded rather than one the host would have cut off.
func withDryRunDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, dryRunBudget)
	return context.WithValue(ctx, dryRunKey{}, true), cancel
}

func isDryRun(ctx context.Context) bool {
	dry, _ := ctx.Value(dryRunKey{}).(bool)
	return dry
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
//
// A load that does not stop at the deadline is waited for, so a late answer still spares
// what only a loaded policy can tell apart; a hook bounds the whole judgment, the load
// included, with JudgeWithin.
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

// awaitDeadline runs work on a goroutine of its own and waits for it until ctx is done,
// then answers ctx's error unless work answered by then. work is left running past that.
func awaitDeadline[T any](ctx context.Context, work func() (T, error)) (T, error) {
	type answer struct {
		value T
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := work()
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		return a.value, a.err
	case <-ctx.Done():
	}
	select {
	case a := <-done:
		return a.value, a.err
	default:
		var zero T
		return zero, ctx.Err()
	}
}

// JudgeWithin runs judge, a hook judgment for req, and answers by the hook's deadline on
// ctx plus judgeReserve whether or not judge has: past that it answers unjudged, recorded
// on the trail with the judgment lapsed, and leaves judge running for the process exit to
// reclaim. A dry run has no host waiting on it and is judged to the end.
//
// Judge bounds each workspace rule phase, but not everything else on its path: a rule that
// opens the workspace, the host's hook timeout read off its harness, a lock held by another
// process. A host that cuts a hook off runs the call with no rule applied and tells the
// agent nothing, so a late verdict is worth less than an unjudged one on time.
//
// base resolves the trail's location and names the judging binary when judge has not
// answered; judge loads the rest itself, inside the bound.
func JudgeWithin(ctx context.Context, base Dependencies, req Request, judge func(context.Context) Verdict) Verdict {
	if req.DryRun {
		return judge(ctx)
	}
	ctx, cancel := WithHookDeadline(ctx)
	defer cancel()
	if begunJudgment(ctx).path == "" {
		ctx = BeginJudgment(ctx, base.CacheDir, req)
	}
	deadline, _ := ctx.Deadline()
	start := time.Now()
	done := make(chan Verdict, 1)
	go func() { done <- judge(ctx) }()
	timer := time.NewTimer(time.Until(deadline) + judgeReserve)
	defer timer.Stop()
	select {
	case v := <-done:
		return v
	case <-timer.C:
		return unjudged(context.WithoutCancel(ctx), base, req, time.Since(start))
	}
}

// phaseAnswer is the trail's name for the bound JudgeWithin holds a whole judgment to.
const phaseAnswer = "answer"

// unjudgedNote tells a session that one of its calls went through with no rule judging it.
var unjudgedNote = "This guard check did not finish within " + hookBudget.String() +
	", so no rule judged this call; it ran because it only reads."

// unjudged is the verdict for a hook call whose judgment did not answer within the bound,
// in the stance a lapsed workspace rule phase takes: a call that changes state is denied
// and one that only reads passes with a note. A call with nothing to judge passes as it
// would have. It ends the call's judgment mark, since the host got its answer in time.
func unjudged(ctx context.Context, base Dependencies, req Request, elapsed time.Duration) Verdict {
	pass := Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
	who := hookAttribution{Host: req.Host, Form: req.Form, Session: req.Session, Agent: req.Agent, Transcript: req.Transcript, Event: req.Event, Window: req.Window}
	input, isPath, callDir := req.Input, req.IsPath, ""
	if filepath.IsAbs(req.Dir) {
		callDir = req.Dir
		ctx = hookContextAt(ctx, base, req.Dir)
	}
	call := judgedCall{seam: seamCommand, what: "this command", lease: req.Lease}
	readOnly := false
	env, isEnvelope := decodeHookEnvelope(req.Input)
	if isEnvelope {
		ctx = hookContextAt(ctx, base, env.Cwd)
		if callDir == "" && filepath.IsAbs(env.Cwd) {
			callDir = env.Cwd
		}
		who.Session = cmp.Or(who.Session, env.Who.Session)
		who.Transcript = cmp.Or(who.Transcript, env.Who.Transcript)
		who.Event = cmp.Or(who.Event, env.Who.Event)
		who.Agent = cmp.Or(who.Agent, env.Who.Agent)
		input, isPath, readOnly = env.Value, isPath || env.IsPath, env.ReadOnlyTool
	}
	begunJudgment(ctx).end(time.Now(), noHookTimeout)
	if req.Observe || req.Message || input == "" || (isEnvelope && (env.NothingToJudge || env.LoadedSkill != "")) {
		return pass
	}
	tool := hookToolCommand
	switch {
	case isPath:
		tool = hookToolWrite
		call = judgedCall{seam: seamWrite, changes: true, what: "this file write", lease: req.Lease}
	case env.IsSpawn || env.IsContinue:
		call = judgedCall{seam: seamSpawn, changes: true, what: "a subagent spawn", lease: req.Lease}
	default:
		d := effectiveDialect(base.ShellDialect)
		call.changes = !readOnly && !readOnlyLine(input, d) && !recoveryLine(input, d, req.Lease)
	}
	verdict := pass
	verdict.Lease = req.Lease
	if call.changes {
		lapse := ruleLapse{budget: hookBudget, said: lapseWording{"the guard's judgment", "finish"}}
		verdict.Decision, verdict.Reason, verdict.Rule = decisionDeny, lapseReason(call, lapse, false), string(advisoryUnfinished)
	} else {
		verdict = applyRuleFailureNote(verdict, unjudgedNote, advisoryUnfinished)
	}
	ctx = context.WithValue(ctx, phaseLogKey{}, &phaseLog{
		start: time.Now().Add(-elapsed),
		done:  []trail.Phase{timedPhase(phaseAnswer, elapsed, hookBudget, true)},
	})
	appendHookActivity(ctx, hookLocation(ctx, base), input, who, tool, req.Lease, "", "", "", verdict, workspaceRuleRecord{}, base.judging(callDir))
	return verdict
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
// of time. dryRun words it for a person grading the call ahead of time: not graded.
func lapseReason(call judgedCall, l ruleLapse, dryRun bool) string {
	if dryRun {
		return "magus did not finish grading " + call.what + ": " + l.what() + ", so it is not graded. " +
			"A dry run is bounded at " + dryRunBudget.String() + " and a hook at " + hookBudget.String() + ", so a hook would deny the call as well."
	}
	return "magus ran out of time judging " + call.what + ": " + l.what() + ". " +
		"A call that changes state is denied rather than let through unjudged; retry it. " +
		"The guard answers within " + hookBudget.String() + ", inside the host's hook timeout, " +
		"because a hook the host cuts off is a call it runs with no rule applied."
}
