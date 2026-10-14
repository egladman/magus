package guard

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// hookBudget bounds one hook judgment, from the moment magus starts judging to its verdict.
// It answers well inside the ten seconds a shipped wiring waits on a hook: the rest is
// process start and exit, which a loaded machine stretches too.
const hookBudget = 7 * time.Second

// inspectBudget bounds the wait for one workspace open, half the hook's budget so the rest
// of the judgment still answers in time.
const inspectBudget = hookBudget / 2

// judgeReserve is held back from every phase for wording the verdict and writing the trail
// row once the phases are done.
const judgeReserve = 500 * time.Millisecond

// The phases of a judgment, as the trail row names them. phaseAnswer is the bound
// JudgeWithin holds the whole judgment to.
const (
	phaseLoadRules       = "load_rules"
	phaseApprovedResolve = "approved_resolve"
	phaseApprovedRule    = "approved_rule"
	phaseWorktreeRule    = "worktree_rule"
	phaseInspect         = "inspect"
	phaseJobRows         = "job_rows"
	phaseJudge           = "judge"
	phaseAnswer          = "answer"
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
	log := judgmentPhases(ctx)
	log.begin(phaseLoadRules)
	budget, _ := phaseShares{phaseLoadRules: 1}.take(ctx, phaseLoadRules)
	start := time.Now()
	got, err := load()
	log.idle()
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

// boundedPhase runs call as phase of the judgment on ctx. The wait ends judgeReserve before
// ctx does and never outlasts inspectBudget; a wait that ends first answers its error, and
// once phase has lapsed in a judgment every later call answers context.DeadlineExceeded at
// once, since each waiting its own budget on the same stall would add up past the deadline.
// call runs without ctx's cancel, so a memoized answer is never cached as cancelled. The
// phase is recorded lapsed when it answered an error after its wait ran out.
func boundedPhase[T any](ctx context.Context, phase string, call func(context.Context) (T, error)) (T, error) {
	log := judgmentPhases(ctx)
	if log.lapsed(phase) {
		var zero T
		return zero, context.DeadlineExceeded
	}
	log.begin(phase)
	budget := inspectBudget
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, max(time.Until(deadline)-judgeReserve, 0))
	}
	wait, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	start := time.Now()
	detached := context.WithoutCancel(ctx)
	v, err := awaitDeadline(wait, func() (T, error) { return call(detached) })
	log.record(timedPhase(phase, time.Since(start), budget, err != nil && wait.Err() != nil))
	return v, err
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
// base resolves the trail's location, names the judging binary and carries the spell
// catalog when judge has not answered; judge loads the rest itself, inside the bound.
func JudgeWithin(ctx context.Context, base Dependencies, req Request, judge func(context.Context) types.GuardJudgment) types.GuardJudgment {
	if req.DryRun {
		return judge(ctx)
	}
	ctx, cancel := WithHookDeadline(ctx)
	defer cancel()
	if begunJudgment(ctx).path == "" {
		ctx = BeginJudgment(ctx, base.CacheDir, req)
	}
	log := &phaseLog{start: time.Now()}
	ctx = context.WithValue(ctx, phaseLogKey{}, log)
	deadline, _ := ctx.Deadline()
	done := make(chan types.GuardJudgment, 1)
	go func() { done <- judge(ctx) }()
	timer := time.NewTimer(time.Until(deadline) + judgeReserve)
	defer timer.Stop()
	select {
	case v := <-done:
		begunJudgment(ctx).answeredInTime()
		return v
	case <-timer.C:
		return unjudged(context.WithoutCancel(ctx), base, req, log)
	}
}

// lapsesBeforeDegrade is how many judgments in a row a session's calls that change state are
// denied for lapsing. Past it they pass with a notice until one judgment answers in time: a
// guard slower than its budget on every call, on a loaded machine or against a daemon from
// another build, otherwise denies every edit and spawn the session makes.
const lapsesBeforeDegrade = 2

// lapseStage is where a session stands after some judgments in a row ran out of time.
type lapseStage int

const (
	// lapseDenies: a call that changes state is denied.
	lapseDenies lapseStage = iota
	// lapseDegrades: the first call past lapsesBeforeDegrade passes with a notice naming
	// the cause and what to check.
	lapseDegrades
	// lapseStillDegraded: later calls pass with a notice saying only that the guard is
	// still not judging.
	lapseStillDegraded
)

// stageFor is the stage inARow lapses in a row put a session at, this one included. Zero,
// a session whose lapses are not counted, denies.
func stageFor(inARow int) lapseStage {
	switch {
	case inARow <= lapsesBeforeDegrade:
		return lapseDenies
	case inARow == lapsesBeforeDegrade+1:
		return lapseDegrades
	}
	return lapseStillDegraded
}

// unjudgedNote tells a session that one of its calls went through with no rule judging it.
func unjudgedNote() string {
	return "This guard check did not finish within " + hookBudget.String() +
		", so no rule judged this call; it ran because it only reads."
}

// unjudged is the verdict for a hook call whose judgment did not answer within the bound,
// in the stance a lapsed workspace rule phase takes: a call that changes state is denied
// and one that only reads passes with a note. A call with nothing to judge passes as it
// would have. It ends the call's judgment mark, since the host got its answer in time.
//
// The built-in rules that read only the call run first (lapsedRules), and what they refuse
// is refused however the call is graded below.
//
// Past lapsesBeforeDegrade lapses in a row in the session, a call that changes state passes
// too, with a notice naming the phase that stalled, unless it pushes; the first such notice
// says why and what to check, the rest say only that the guard is still not judging. The
// trail row records log's finished phases, the one underway, and the bound.
func unjudged(ctx context.Context, base Dependencies, req Request, log *phaseLog) types.GuardJudgment {
	elapsed := time.Since(log.start)
	pass := types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
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
	mark := begunJudgment(ctx)
	mark.end()
	if req.Observe || req.Message || input == "" || (isEnvelope && (env.NothingToJudge || env.LoadedSkill != "")) {
		return pass
	}
	stalled, isStalled := log.stalled()
	finished := log.finished()
	tool := hookToolCommand
	pushes := false
	switch {
	case isPath:
		tool = hookToolWrite
		call = judgedCall{seam: seamWrite, changes: true, what: "this file write", lease: req.Lease}
	case env.IsSpawn || env.IsContinue:
		call = judgedCall{seam: seamSpawn, changes: true, what: "a subagent spawn", lease: req.Lease}
	default:
		d := effectiveDialect(base.ShellDialect)
		call.changes = !readOnly && !readOnlyLine(input, d) && !recoveryLine(input, d, req.Lease)
		cmds, _ := ParseCommandsDialect(input, d)
		pushes = slices.ContainsFunc(cmds, isPush)
	}
	verdict := pass
	verdict.Lease = req.Lease
	inARow := mark.recordLapse()
	builtins := lapsedRules(ctx, base, log, req, env, who, input, isPath, hookLocation(ctx, base).cacheDir)
	switch {
	case builtins.Deny != "":
		verdict.Decision, verdict.Reason, verdict.Rule = decisionDeny, builtins.Deny, builtins.RuleName()
	case call.changes && (stageFor(inARow) == lapseDenies || pushes):
		// A push is never degraded: the gate it needs proven is in the run log, which only
		// a judgment that finishes reads.
		lapse := ruleLapse{budget: hookBudget, said: lapseWording{"the guard's judgment", "finish"}}
		verdict.Decision, verdict.Reason, verdict.Rule = decisionDeny, lapseReason(call, lapse, false), string(advisoryUnfinished)
	case call.changes:
		where := stallSite(stalled, isStalled, finished)
		verdict = applyRuleFailureNote(verdict, degradedNote(call, where, inARow), advisoryUnfinished)
	default:
		verdict = applyRuleFailureNote(verdict, unjudgedNote(), advisoryUnfinished)
	}
	row := &phaseLog{start: log.start, done: finished}
	if isStalled {
		row.done = append(row.done, stalled)
	}
	row.done = append(row.done, timedPhase(phaseAnswer, elapsed, hookBudget, true))
	ctx = context.WithValue(ctx, phaseLogKey{}, row)
	appendHookActivity(ctx, hookLocation(ctx, base), input, who, tool, req.Lease, "", "", "", verdict, workspaceRuleRecord{}, base.judging(callDir))
	return verdict
}

// stallSite words where a lapsed judgment stopped: in the phase underway, else after the
// last one it finished.
func stallSite(stalled trail.Phase, isStalled bool, finished []trail.Phase) string {
	switch {
	case isStalled:
		return "in its " + stalled.Name + " phase, " + roundBudget(time.Duration(stalled.ElapsedMs)*time.Millisecond).String() + " in"
	case len(finished) > 0:
		return "after its " + finished[len(finished)-1].Name + " phase"
	}
	return "before its first phase"
}

// degradedNote tells a session that a call that changes state went through unjudged, the
// inARow-th judgment in a row to run out of time, which stopped where. Only the first past
// lapsesBeforeDegrade names the cause and what to check; the session has it from then on.
func degradedNote(call judgedCall, where string, inARow int) string {
	if stageFor(inARow) == lapseStillDegraded {
		return "The guard is still not finishing in time (" + strconv.Itoa(inARow) + " judgments in a row), so " +
			call.what + " ran unjudged, as the notice before said."
	}
	return "The guard has run out of its " + hookBudget.String() + " budget on " + strconv.Itoa(inARow) +
		" judgments in a row in this session, so " + call.what + " ran with no rule judging it, and so will every call " +
		"until a judgment finishes in time. This one stalled " + where + ". A judgment this slow is a loaded machine or a " +
		"magus built from another commit than the daemon it talks to: compare `magus version` with `magus server status`, " +
		"and check `magus status` for what is running."
}

// phaseLog collects the phases of one judgment for its trail row, the one underway, and the
// dependencies it loaded, which a lapse grades the built-in rules by. The judgment writes
// it while JudgeWithin may read it to word a lapse, hence the lock. Every method is a no-op,
// or answers zero, on a nil log.
type phaseLog struct {
	start time.Time

	mu      sync.Mutex
	done    []trail.Phase
	running string
	since   time.Time
	// deps is what the judgment loaded, nil until it has.
	deps *Dependencies
}

type phaseLogKey struct{}

// withPhaseLog starts timing a judgment, with the rule load its caller timed, if any, as
// the first phase. A log JudgeWithin already put on ctx is the one used, so a lapse can
// name what the judgment had reached.
func withPhaseLog(ctx context.Context, load trail.Phase) context.Context {
	l := judgmentPhases(ctx)
	if l == nil {
		l = &phaseLog{start: time.Now()}
		ctx = context.WithValue(ctx, phaseLogKey{}, l)
	}
	if load.Name != "" {
		l.record(load)
	}
	return ctx
}

// judgmentPhases is the judgment's phase log, nil outside Judge and JudgeWithin.
func judgmentPhases(ctx context.Context) *phaseLog {
	l, _ := ctx.Value(phaseLogKey{}).(*phaseLog)
	return l
}

// begin notes that phase started now.
func (l *phaseLog) begin(phase string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running, l.since = phase, time.Now()
}

// idle notes that no phase is running.
func (l *phaseLog) idle() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running = ""
}

// record adds one finished phase, ending it as the one underway.
func (l *phaseLog) record(p trail.Phase) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.done = append(l.done, p)
	if l.running == p.Name {
		l.running = ""
	}
}

// finished is a copy of the recorded phases.
func (l *phaseLog) finished() []trail.Phase {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.done)
}

// lapsed reports whether a recorded phase named name ran out of time.
func (l *phaseLog) lapsed(name string) bool {
	_, ok := l.lapsedPhase(name)
	return ok
}

// lapsedPhase is the first recorded phase named name that ran out of time.
func (l *phaseLog) lapsedPhase(name string) (trail.Phase, bool) {
	if l == nil {
		return trail.Phase{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	i := slices.IndexFunc(l.done, func(p trail.Phase) bool { return p.Name == name && p.Lapsed })
	if i < 0 {
		return trail.Phase{}, false
	}
	return l.done[i], true
}

// settle records the dependencies the judgment loaded, for a lapse to grade the call by.
func (l *phaseLog) settle(deps Dependencies) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.deps = &deps
}

// settled is what settle recorded, false before the judgment loaded anything.
func (l *phaseLog) settled() (Dependencies, bool) {
	if l == nil {
		return Dependencies{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.deps == nil {
		return Dependencies{}, false
	}
	return *l.deps, true
}

// stalled is the phase underway, lapsed and timed from its start; false when none is.
func (l *phaseLog) stalled() (trail.Phase, bool) {
	if l == nil {
		return trail.Phase{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running == "" {
		return trail.Phase{}, false
	}
	return trail.Phase{Name: l.running, ElapsedMs: time.Since(l.since).Milliseconds(), Lapsed: true}, true
}

// phases is every recorded phase, then the whole judgment so far.
func (l *phaseLog) phases() []trail.Phase {
	if l == nil {
		return nil
	}
	return append(l.finished(), trail.Phase{Name: phaseJudge, ElapsedMs: time.Since(l.start).Milliseconds()})
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
