package guard

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

const (
	// gateRepeatWindow is how far back a repeat is counted. Long enough to cover a
	// working session, short enough that yesterday's runs say nothing about today's.
	gateRepeatWindow = 2 * time.Hour

	// gateRepeatMinRuns is how many runs inside the window before saying anything.
	// The second is the first that could have been a narrower target.
	gateRepeatMinRuns = 2

	// gateRepeatMinSpent is how much those runs must have COST before it is worth
	// mentioning. Frequency alone is not waste: a repeat gate over an unchanged tree
	// is mostly cache hits and finishes in seconds, which is the cache working. What
	// is worth a word is a gate that keeps genuinely re-running.
	gateRepeatMinSpent = 5 * time.Minute
)

// adviseRepeatGate reports what the CI gate has already cost in this window, or ""
// when it has not run enough to be worth saying.
//
// The gate is the most expensive target by construction, because it runs everything
// the diff reaches, and it is the one an iterating caller reaches for out of habit.
//
// It reports rather than refuses, and deliberately does NOT judge whether a given
// run was justified. Nothing available separates a wasteful repeat from a legitimate
// re-run after a fix: the tree is dirty at the same revision either way. What is
// checkable is the accumulating cost, and stating it is what changes behavior.
//
// Read from the RUN LOG, not from forecast history. The history is user-global and
// keyed by workspace-relative project path, so every worktree records its root under
// "." in one file. This machine runs dozens, so a sibling checkout's gate would be
// counted and reported as having run "here". The run log is per-workspace, one file
// per invocation, carrying the argv and real wall clock, so the count is a fact
// rather than a reconstruction from per-spell samples.
func adviseRepeatGate(runsDir string, now time.Time) (full advice, brief string) {
	runs, spent := recentGateRuns(runsDir, now)
	if runs < gateRepeatMinRuns || spent < gateRepeatMinSpent {
		return advice{}, ""
	}
	return gateRepeatAdvice(runs, spent), gateRepeatBrief(runs, spent)
}

// denyRepeatGate refuses the gate from its third run in the window when the newest
// verdict this branch recorded is green and nothing in the tree changed after it, or
// refuses nothing. The advisory above states the cost, and callers gated past it again and
// again. A gate over a tree it already passed can only pass again, so the refusal costs the
// caller nothing it needed.
//
// "Changed" is read off version control: every file that differs from the green gate's
// commit, committed or not, compared by modification time against the verdict. A file
// edited before the gate ran was in the tree it passed; one it cannot stat counts as a
// change. Anything unreadable (no run log, no store, no version control) refuses nothing.
func denyRepeatGate(ctx context.Context, deps Dependencies, _, command string) denial {
	if !commandRunsGate(command) {
		return denial{}
	}
	at := hookLocation(ctx, deps)
	runs, _ := recentGateRuns(workspaceRunsDir(at.cacheDir), time.Now())
	if runs < gateRepeatDenyRuns {
		return denial{}
	}
	green, ok := greenGateUnchanged(ctx, deps, at.workspace)
	if !ok {
		return denial{}
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` passed on this branch %s ago and nothing changed since: gate after your next change.",
			types.TargetCI, time.Since(green.At).Round(time.Second)),
		Why: fmt.Sprintf("The gate has run %d times here in the last %s, and the newest verdict, a pass at %s, covers the tree as it stands: no file that differs from that commit was written after it. "+
			"A gate over a tree it already passed only repeats the pass. %s.",
			runs, gateRepeatWindow, green.Commit, gateCadence),
	}
}

// gateRepeatDenyRuns is how many gate runs inside the window refuse the next over an
// unchanged green tree: the third run is the first refused.
const gateRepeatDenyRuns = 2

// greenGateUnchanged is the newest gate verdict this branch recorded, when it passed and
// no file that differs from its commit was written after it.
func greenGateUnchanged(ctx context.Context, deps Dependencies, workspace string) (sessions.GateRecord, bool) {
	if workspace == "" {
		return sessions.GateRecord{}, false
	}
	res, err := vcs.Resolve(ctx, workspace, "", deps.VCS)
	if err != nil || res.VCS == nil {
		return sessions.GateRecord{}, false
	}
	ref, err := res.VCS.Ref(ctx, workspace)
	if err != nil || ref == "" {
		return sessions.GateRecord{}, false
	}
	dir, err := sessions.Dir(workspace)
	if err != nil {
		return sessions.GateRecord{}, false
	}
	fold, err := sessions.ReadGateResults(dir)
	if err != nil {
		return sessions.GateRecord{}, false
	}
	green, ok := sessions.LatestGate(fold, ref, string(types.TargetCI))
	if !ok || green.Outcome != sessions.OutcomePass || green.Commit == "" {
		return sessions.GateRecord{}, false
	}
	changed, err := res.VCS.ChangedFiles(ctx, workspace, green.Commit)
	if err != nil {
		return sessions.GateRecord{}, false
	}
	for _, rel := range changed {
		info, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil || info.ModTime().After(green.At) {
			return sessions.GateRecord{}, false
		}
	}
	return green, true
}

// workspaceRunsDir is where this workspace logs its invocations, or "" when there is none
// to read.
//
// An unresolved cache dir answers "" rather than falling back to the literal name: that
// name is relative, and joining it against the hook PROCESS's directory is the
// cross-checkout mistake hookLocationAt exists to prevent. No runs dir means no
// count, which is the honest answer.
func workspaceRunsDir(cacheDir string) string {
	if cacheDir == "" {
		return ""
	}
	dir := filepath.Join(cacheDir, cache.RunsDir)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}

// recentGateRuns counts the gate's invocations inside the window and sums their wall
// clock.
//
// One log file is one invocation, so there is nothing to cluster and no way to count
// the same run twice.
func recentGateRuns(runsDir string, now time.Time) (runs int, spent time.Duration) {
	cutoff := now.Add(-gateRepeatWindow)
	recentRunLogs(runsDir, cutoff, func(started journal.Event, modified time.Time, _ *bufio.Reader) {
		start := time.UnixMilli(started.Ts)
		if !isGateCommand(started.Command.Arguments) || start.Before(cutoff) {
			return
		}
		runs++
		// The log's last write is the run's end; a run still in flight measures as
		// however far it has got, which is the honest reading of "spent so far".
		if d := modified.Sub(start); d > 0 {
			spent += d
		}
	})
	return runs, spent
}

// commandRunsGate reports whether a shell line would invoke the CI gate.
//
// Through the parser rather than a pattern, for the reason ParseCommands exists:
// it resolves quoting, peels wrappers, and hands back each command a line would
// actually run, so `mise exec -- ./magus affected ci` reads the same as the bare form
// and a `ci` inside a quoted argument reads as neither.
func commandRunsGate(command string) bool {
	cmds, ok := ParseCommands(command)
	if !ok {
		return false
	}
	for _, c := range cmds {
		if c.Name == "magus" && isGateCommand(c.Args) {
			return true
		}
	}
	return false
}

// gateReadFlags are the flags that turn a gate invocation into a REPORT about it: the
// shard plan, the affected set, why a project is in it, the command that would run.
//
// None of them runs a target, so none of them is what either caller is asking about: the
// deny exists because seven workers ran the whole pipeline at once, and the advisory
// reports accumulated wall clock. Measured by the structural breadcrumb test, which is how
// this was found: `magus affected ci --plan` is the command magus itself serves after an
// affected listing, and the deny refused magus's own suggestion to every worker.
var gateReadFlags = []string{"--plan", "--impact", "--explain", "--dry-run"}

// isGateCommand reports whether an argv RUNS the CI gate.
//
// An argv, so there is no shell quoting left to peel and no way for a trailing
// `-run ci` argument to a test command to masquerade as the gate. The two callers
// both supply one: a recorded invocation's arguments, and a parsed command line.
func isGateCommand(args []string) bool {
	for i, a := range args {
		if a != "run" && a != "affected" {
			continue
		}
		if slices.ContainsFunc(args[i+1:], func(f string) bool { return slices.Contains(gateReadFlags, f) }) {
			return false
		}
		for _, rest := range args[i+1:] {
			// Everything past `--` belongs to the underlying tool, where a bare `ci`
			// is a test filter rather than this workspace's gate.
			if rest == "--" {
				return false
			}
			if rest == "" || rest[0] == '-' {
				continue
			}
			// Every bare word, not just the first: magus accepts flags before the
			// target, and a flag's VALUE is a bare word too (`--timeout 5m ci`).
			// ParseTarget strips the charm suffix and normalizes, so `ci:rw` and `CI`
			// both resolve; a project path or a duration simply does not match.
			if t, err := types.ParseTarget(rest); err == nil && t.Name == types.TargetCI {
				return true
			}
		}
		return false
	}
	return false
}

// gateRepeatAdvice names no target but the gate itself: a workspace calls its
// narrower targets whatever it likes, so the advisory points at the command that
// lists them rather than guessing.

// gateCadence names the cost, the command that sizes the risk, and leaves the trade to
// the caller.
//
// Not a threshold, and the earlier drafts that were one are the reason. A count alone
// changes nothing: this fired eight times in one session at a caller who gated ten times
// in two hours and narrowed nothing. "Once per branch, when the change is complete" fails
// the other way, since complete is a judgment nobody can check. And a hard rule -- more
// than one project, or the root -- is a refusal dressed as advice: this tier cannot
// enforce anything, and the caller knows things magus does not, like whether the change
// is load-bearing or what a red pull request costs today.
//
// What magus can contribute is the size of the blast radius, which is what --plan prints.
// The caller weighs that against the wall clock above.
var gateCadence = "`" + hint.Affected.With(string(types.TargetCI), "--plan") + "` sizes the risk"

// gateCadenceWhy is the trade --plan informs, kept behind the advisory's ref.
var gateCadenceWhy = "One project is usually answered by that project's own `" + string(types.TargetCI) +
	"`, while the root or several projects is the gate earning it."

func gateRepeatAdvice(runs int, spent time.Duration) advice {
	return advice{
		Say: fmt.Sprintf("magus workspace: the `%s` gate has run %d times here in the last %s, about %s of wall clock; %s first.",
			types.TargetCI, runs, gateRepeatWindow, spent.Round(time.Second), gateCadence),
		Why: gateCadenceWhy,
	}
}

// gateRepeatBrief is the repeat form, and it keeps the running cost rather than going
// quiet: the count and the wall clock are the whole argument, and they are the part that
// has changed since the caller last read the full text.
func gateRepeatBrief(runs int, spent time.Duration) string {
	return fmt.Sprintf("magus workspace: `%s` has run %d times here, about %s of wall clock; %s.",
		types.TargetCI, runs, spent.Round(time.Second), gateCadence)
}

// denyLeaseScopedGate is what a bound lease's magus runs meet: the gate refusal first, then
// worker-check-only for every other target. One entry because roleScopedCommandRules lists
// the rules it stands down by name.
func denyLeaseScopedGate(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if d := denyLeaseGate(ctx, deps, actingLease, command); d.refused() {
		return d
	}
	return denyWorkerCheckOnly(ctx, deps, actingLease, command)
}

// denyLeaseGate refuses the gate to a lease that was handed a narrower check, and
// refuses nothing for everybody else.
//
// The gate runs ONCE per branch, in the orchestrator's tree, after every unit lands. A
// delegated worker's `validation` is the narrow target it was assigned, and until this
// rule the field declared that and enforced nothing: seven workers each ran the whole
// pipeline concurrently on one machine because every brief ended with it (2026-09-09).
// Gate redundancy cannot catch that, since it keys on identical tree content and seven
// worktrees are seven trees.
//
// Same seatbelt contract gradeLeasedWrite documents. A caller naming no lease, a lease
// with no live row and an unreadable ledger all pass: a boundary nobody declared is not a
// narrow one. A person running their own gate names no lease and never reaches this rule.
//
// A bound row that declares NO check is refused too. It used to pass, on the reading that
// an empty field is an undeclared boundary, but a bound worker is a worker either way and
// the gate is still the orchestrator's; the empty field says nobody wrote down what this
// unit should run, which is a reason to ask rather than a licence to run everything.
func denyLeaseGate(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" || !commandRunsGate(command) {
		return denial{}
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || actingIntegrator(ctx, deps, me) {
		return denial{}
	}
	const gated = "The checkout's integrator, " + integratorIs + ", gates once, in its own tree, after every unit lands."
	if source, names := LeaseGateSource(me); names {
		return denial{
			Say: fmt.Sprintf("magus workspace: `%s` is the integrator's gate, and lease %s names it as its check; a job's check is never the gate.",
				elideCommand(command), me.ID),
			Why: fmt.Sprintf("%s Lease %s's %s names `%s`, and this caller is not the checkout's integrator. If this unit integrates the checkout, the orchestrator spawns it as `<parent>/%s %s` in that checkout; otherwise %s\n%s",
				gated, me.ID, source, types.TargetCI, integrateRole, me.ID, leaseActorClause("give this row the narrowest target covering its paths"), leaseActorWhy),
		}
	}
	checks := rowChecks(me)
	if len(checks) == 0 && me.Validation == "" {
		return denial{
			Say: fmt.Sprintf("magus workspace: `%s` is the integrator's gate, and lease %s declares no check; run the narrowest target covering your paths.",
				elideCommand(command), me.ID),
			Why: gated + " Report what that target said, and that this row names no check: " + leaseActorClause("record a check on this row") + "\n" + leaseActorWhy,
		}
	}
	check := me.Validation
	if len(checks) > 0 {
		check = checkCommand(checks[0])
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` is the integrator's gate; run your check: `%s`.", elideCommand(command), check),
		Why: fmt.Sprintf("%s `%s` runs the `%s` gate, and the check on lease %s's row, %q, does not name it. If the unit needs the gate, %s\n%s",
			gated, elideCommand(command), types.TargetCI, me.ID, check, leaseActorClause("widen that check"), leaseActorWhy),
	}
}

// denyRuleWorkerCheckOnly names the refusal of any target but a bound row's own check.
const denyRuleWorkerCheckOnly denyRuleName = "worker-check-only"

// denyWorkerCheckOnly refuses a bound worker every `magus run` and `magus affected` but its
// row's check, a slice of the work that check composes, a dependency install, and the
// targets declaring an output in its write paths, or refuses nothing. Two worktrees share
// no cache key, so a second run of some other target only doubles the load.
//
// A slice passes because it is the iteration the check exists to end in: `go-test . --
// -run X` under a `test .` check runs a part of the check's own work, and refusing it sent
// workers to a raw `go test` that nothing graded at all. An install passes because a fresh
// worktree can run nothing, its check included, until its dependencies are there. Every
// other run it lets through, the check included, is held to the write paths under the rw
// charm. rowJudge decides all of it, so a brief is refused exactly when its worker would be.
//
// Silent where there is nothing to hold the run to: no lease, no live row, the checkout's
// integrator, which builds, regenerates and gates there, or a row declaring no check at
// all, which denyLeaseGate already answers for the gate. Every other verb, and a run that only reports (--plan,
// --dry-run, --graph, --help), passes.
func denyWorkerCheckOnly(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return denial{}
	}
	var runs []targetRun
	for _, c := range cmds {
		if r, ok := parseTargetRun(c); ok {
			runs = append(runs, r)
		}
	}
	if len(runs) == 0 {
		return denial{}
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || actingIntegrator(ctx, deps, me) {
		return denial{}
	}
	checks := rowChecks(me)
	if len(checks) == 0 {
		return denial{}
	}
	at := hookLocation(ctx, deps)
	judge := newRowJudge(ctx, deps, me, at, lazyWorkspace(ctx, deps, at.workspace))
	for _, r := range runs {
		v := judge.verdict(r)
		switch {
		case v.unchecked != nil:
			return uncheckedDenial(v, me, checks, deps.DefaultCharms)
		case v.op:
			return opRewriteDenial(v, me, deps.DefaultCharms)
		case len(v.outside) > 0:
			return rewriteDenial(v, me, deps.DefaultCharms)
		case v.permitted:
			continue
		}
		r = v.run
		commands := make([]string, len(checks))
		for i, c := range checks {
			commands[i] = "`" + checkCommand(c) + "`"
		}
		if buildsCheckoutBinary(ctx, judge.workspace, r) {
			return denial{
				Say: fmt.Sprintf("magus workspace: `%s` builds ./magus, the binary judging lease %s's calls; %s.", r.target, me.ID, workerBuildVerdict),
				Why: onePerBase + "\n" +
					"When this unit's own changes have to reach that binary, the checkout's integrator builds ./magus at this branch's commit and the orchestrator places it here with `" +
					strings.Join(placementArgv(me.ID), " ") + "`. Until then the unit is held to " + commands[0] + ", and a need for the new binary goes in your result's unresolved risks.",
			}
		}
		return denial{
			Say: fmt.Sprintf("magus workspace: `%s` is not lease %s's check; run %s.", r.target, me.ID, commands[0]),
			Why: fmt.Sprintf("Lease %s's checks: %s. The checkout's integrator, "+integratorIs+", runs every other target serially, in its own tree, after it integrates the units.\n"+
				"`%s` runs `%s`, which is neither that check nor a target declaring an output in lease %s's write paths. Another worktree's run of it shares no cache key with this one, so it replays nothing here and holds the machine. Name the target in your result's unresolved risks if the unit needs it.",
				me.ID, strings.Join(commands, ", "), elideCommand(command), r.target, me.ID),
		}
	}
	return denial{}
}

// lazyWorkspace opens the workspace at root on its first call and keeps it, or the error
// opening it. It is the only opener a judgment uses: one open costs seconds inside the
// hook's deadline.
func lazyWorkspace(ctx context.Context, deps Dependencies, root string) func() (types.WorkspaceRepository, error) {
	return sync.OnceValues(func() (types.WorkspaceRepository, error) {
		ws, err := deps.inspect(ctx, root)
		if err == nil && ws == nil {
			err = fmt.Errorf("opening the workspace at %s found none", root)
		}
		return ws, err
	})
}

// rowJudge judges the runs a row's worker would make, for the worker's own call and for a
// brief teaching them. Every lookup reads the one workspace it is handed, on first use.
type rowJudge struct {
	deps   Dependencies
	row    types.Job
	checks []types.LeaseCheck
	// root is the workspace, and dir the directory the runs are made in.
	root, dir string
	workspace func() (types.WorkspaceRepository, error)
	graph     func() (types.TargetGraphOutput, error)
	produces  lazyProducers
	composes  lazyProducers
}

func newRowJudge(ctx context.Context, deps Dependencies, row types.Job, at location, workspace func() (types.WorkspaceRepository, error)) rowJudge {
	checks := rowChecks(row)
	graph := lazyTargetGraph(ctx, workspace)
	return rowJudge{
		deps: deps, row: row, checks: checks, root: at.workspace, dir: at.dir, workspace: workspace, graph: graph,
		produces: sync.OnceValue(func() func(target, project string) bool {
			return leaseProducers(ctx, workspace, row.WritePaths)
		}),
		composes: sync.OnceValue(func() func(target, project string) bool {
			return checksCompose(graph, checks)
		}),
	}
}

// lazyTargetGraph reads the workspace's static target graph on its first call and keeps
// it, or the error reading it: the graph parses every magusfile, so a judgment reads it
// once.
func lazyTargetGraph(ctx context.Context, workspace func() (types.WorkspaceRepository, error)) func() (types.TargetGraphOutput, error) {
	return sync.OnceValues(func() (types.TargetGraphOutput, error) {
		ws, err := workspace()
		if err != nil {
			return types.TargetGraphOutput{}, err
		}
		return ws.TargetGraph(ctx)
	})
}

// runVerdict is how a row's rule judges one run its worker would make.
type runVerdict struct {
	// run has the projects it selects resolved.
	run targetRun
	// permitted is the row's check, a slice of what the check composes, a target declaring
	// an output in the write paths, or a dependency install.
	permitted bool
	// outside are the globs a permitted run writes past the write paths under the rw charm.
	outside []string
	// op is a permitted run of a spell op on its own under an rw charm the op honors: no
	// declaration says what it writes. composers are the magusfile targets calling it in
	// the projects the run selects.
	op        bool
	composers []string
	// unchecked is why the run could not be judged: the workspace it is read against did
	// not load.
	unchecked error
}

// verdict judges r. A permitted run under the rw charm, named on its target or carried by
// default_charms, is held to the write paths; a target whose chain declares no write
// passes, since nothing says what it touches. A spell op run on its own under an rw charm
// it honors is refused: an op declares no writes, so it would always pass.
func (j rowJudge) verdict(r targetRun) runVerdict {
	r, err := r.fromDir(j.root, j.dir, j.workspace)
	v := runVerdict{run: r}
	switch {
	case err != nil:
		v.unchecked = err
	case r.allowed(j.checks, j.produces) || r.composedByCheck(j.composes):
		v.permitted = true
		if r.verb != "run" || !r.rewrites(j.deps.DefaultCharms) {
			break
		}
		ws, err := j.workspace()
		if err != nil {
			v.unchecked = fmt.Errorf("reading what %s writes: %w", r.target, err)
			break
		}
		if r.runsOp(ws) && opHonorsRewrite(j.deps.spells(), r.target) {
			v.op, v.composers = true, r.composers(j.graph)
			break
		}
		v.outside = r.writesOutside(ws, j.row.WritePaths)
	case r.installs(j.deps):
		v.permitted = true
	}
	return v
}

// rewriteDenial words the refusal of a run the rw charm would have write past the row's
// write paths.
func rewriteDenial(v runVerdict, me types.Job, defaults []string) denial {
	r, next := v.run, v.run.readOnly(defaults)
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` rewrites files outside lease %s's write paths; run `%s`.", r.target, me.ID, next),
		Why: fmt.Sprintf("Under the rw charm `%s` writes `%s`, which lease %s's write paths (%s) do not cover.\n"+
			"`%s` reports the same drift without writing it. The checkout's integrator, "+integratorIs+", rewrites the rest after it integrates the units; name the files in your result's unresolved risks if the unit needs them.",
			r.target, strings.Join(v.outside, "`, `"), me.ID, strings.Join(me.WritePaths, ", "), next),
	}
}

// opRewriteDenial words the refusal of a spell op run on its own under the rw charm. It
// names a magusfile target composing the op, whose declarations the rule judges, or the
// run without the charm when none does.
func opRewriteDenial(v runVerdict, me types.Job, defaults []string) denial {
	r, next := v.run, v.opNext(defaults)
	why := fmt.Sprintf("A rewrite is held to lease %s's write paths by what its target declares writing, and a spell op run on its own declares nothing: what `%s` rewrites is known only once MGS3001 reports it, after the write.\n", me.ID, r.target)
	if len(v.composers) > 0 {
		why += fmt.Sprintf("`%s` calls it in the projects the run selects, and that run is judged by its declarations.", strings.Join(v.composers, "`, `"))
	} else {
		why += "No magusfile target calls it in the projects the run selects. `" + next + "` reports the same drift without writing it; the checkout's integrator, " + integratorIs + ", rewrites the rest after it integrates the units."
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` runs a spell op under the rw charm, and no declaration says what it writes; run `%s`.", r.target, next),
		Why: why,
	}
}

// opNext is the run a refused op run is sent to: its first composer, with the run's
// charms and projects, or the run without the rw charm when nothing composes it. The
// forwarded arguments are the op's own, so a composer's run drops them.
func (v runVerdict) opNext(defaults []string) string {
	r := v.run
	if len(v.composers) == 0 {
		return r.readOnly(defaults)
	}
	_, t, _ := r.parse()
	name := v.composers[0]
	if len(t.Charms) > 0 {
		name += ":" + strings.Join(t.Charms, ",")
	}
	args := append([]string{name}, r.projects...)
	if r.noDefaultCharms {
		args = append(args, "--no-default-charms")
	}
	return hint.Run.With(args...)
}

// uncheckedDenial words the refusal of a run the rule could not judge. It refuses rather
// than pass the run unchecked: no silent default stands in for the write paths. The next
// run is judged without the workspace: a permitted run without the rw charm, or the check,
// which names its project.
func uncheckedDenial(v runVerdict, me types.Job, checks []types.LeaseCheck, defaults []string) denial {
	next := checkCommand(checks[0])
	if v.permitted {
		next = v.run.readOnly(defaults)
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` could not be judged against lease %s's write paths: the workspace did not load; run `%s`.", v.run.target, me.ID, next),
		Why: fmt.Sprintf("%v.\nWithout the workspace's declarations nothing says which project `%s` selects or what it writes, so it is refused rather than run unchecked. Once the workspace loads, the run is judged as usual.",
			v.unchecked, v.run.target),
	}
}

// denyWorkerRawTool refuses a bound worker a raw tool a spell op covers, naming the check
// it may run instead, or refuses nothing. raw-tool only advises by default, so without this
// a worker refused a target ran the same tool raw, outside the cache, the sandbox and this
// rule, and the refusal taught the bypass. Silent where denyWorkerCheckOnly is.
func denyWorkerRawTool(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	raw, ok := firstRawToolDenied(deps, command)
	if !ok {
		return denial{}
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok || actingIntegrator(ctx, deps, me) {
		return denial{}
	}
	checks := rowChecks(me)
	if len(checks) == 0 {
		return denial{}
	}
	match, _ := rawToolMatch(deps, raw)
	op := match.spell + "::" + match.operation
	why := "A bound worker runs its work through targets, where the cache, the sandbox and the rules that hold it to its row all apply."
	project := cmp.Or(checks[0].Project, ".")
	if checksCompose(lazyTargetGraph(ctx, lazyWorkspace(ctx, deps, hookLocation(ctx, deps).workspace)), checks)(op, project) {
		why += " The check composes " + op + ", so a narrower run of it passes too: `" + hint.Run.With(op, project) + "`, with the tool's own flags after `--`."
	} else {
		why += " If the unit needs " + op + ", name it in your result's unresolved risks."
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` runs %s outside magus; run lease %s's check: `%s`.",
			elideCommand(resolvedCommand(raw)), op, me.ID, checkCommand(checks[0])),
		Why: why,
	}
}

// targetRun is one `magus run` or `magus affected` a command line makes, read far enough to
// hold it to a row's check.
type targetRun struct {
	verb     string
	target   string
	projects []string
	// cwdProject is the project a run naming none selects from its directory, when one
	// holds that directory (see fromDir).
	cwdProject string
	// forwarded are the arguments after `--`, passed to the target's tools.
	forwarded []string
	// noDefaultCharms is --no-default-charms, which keeps the workspace's default_charms
	// off this run.
	noDefaultCharms bool
}

// runReportFlags turn a run into a report about one: nothing executes, so nothing contends.
var runReportFlags = []string{"plan", "impact", "explain", "dry-run", "graph", "help", "h"}

// parseTargetRun reads a magus invocation as a target run, reporting false for any other
// verb, for a run that only reports, and for one whose target the line does not name.
//
// A flag's arity comes from the verb's own registry entry before the global set, so the
// value of `--timeout 5m` or `--skip docs` is never read as a project.
func parseTargetRun(c hint.Invocation) (targetRun, bool) {
	if path.Base(c.Name) != "magus" {
		return targetRun{}, false
	}
	var r targetRun
	skip := false
	for i, a := range c.Args {
		switch {
		case skip:
			skip = false
		case a == "--":
			r.forwarded = c.Args[i+1:]
			return r, r.target != ""
		case len(a) > 1 && a[0] == '-':
			name, value, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if slices.Contains(runReportFlags, name) {
				return targetRun{}, false
			}
			if name == "no-default-charms" {
				r.noDefaultCharms = !joined || value != "false"
			}
			skip = !joined && verbFlagTakesValue(r.verb, name)
		case r.verb == "":
			if a != "run" && a != "affected" {
				return targetRun{}, false
			}
			r.verb = a
		case r.target == "":
			r.target = a
		default:
			r.projects = append(r.projects, a)
		}
	}
	return r, r.target != ""
}

// verbFlagTakesValue reports whether a flag consumes the next word, asking verb's own flags
// in the registry the CLI parses with before magus's global set.
func verbFlagTakesValue(verb, name string) bool {
	for _, cmd := range commands.Root.Children {
		if cmd.Name != verb {
			continue
		}
		if i := slices.IndexFunc(cmd.Flags, func(f cmdline.Flag) bool { return f.Name == name }); i >= 0 {
			return cmd.Flags[i].Kind != cmdline.Bool
		}
	}
	takes, _ := MagusFlagTakesValue(name)
	return takes
}

// allowed reports whether a bound worker may make this run: its row's check, or a target
// declaring an output in its write paths. A rebuild of magus's own binary is never on the
// list: there is one binary per base, the integrator builds it and a copy is placed in each
// worker checkout, so a worker's own build is a second binary for the same base.
//
// `affected` is never a check: it picks its projects from the diff, and a check names one.
func (r targetRun) allowed(checks []types.LeaseCheck, produces lazyProducers) bool {
	projects := r.selection()
	if r.verb == "run" && slices.ContainsFunc(checks, func(c types.LeaseCheck) bool {
		return sameTarget(c.Target, r.target) && !slices.ContainsFunc(projects, func(p string) bool { return path.Clean(p) != path.Clean(c.Project) })
	}) {
		return true
	}
	if r.verb != "run" {
		return false
	}
	writes := produces()
	return !slices.ContainsFunc(projects, func(p string) bool { return !writes(r.target, p) })
}

// selection is the projects a run selects: the ones it names, else the project fromDir
// found holding its directory, else every project, which is what `magus run` picks from
// the root or from a directory no project holds.
func (r targetRun) selection() []string {
	switch {
	case len(r.projects) > 0:
		return r.projects
	case r.cwdProject != "":
		return []string{r.cwdProject}
	}
	return []string{"/"}
}

// fromDir resolves the project a run naming none selects from dir, the innermost project
// holding it, as `magus run` does. It opens the workspace only for a dir below root, and
// errs when that open fails, since the run's projects are then unknown.
func (r targetRun) fromDir(root, dir string, workspace func() (types.WorkspaceRepository, error)) (targetRun, error) {
	if len(r.projects) > 0 || r.verb != "run" || dir == "" || filepath.Clean(dir) == filepath.Clean(root) {
		return r, nil
	}
	ws, err := workspace()
	if err != nil {
		return r, fmt.Errorf("finding the project %s selects from %s: %w", r.target, dir, err)
	}
	if p, ok := ws.Where(dir); ok {
		r.cwdProject = p.Path
	}
	return r, nil
}

// composedByCheck reports a run of a target one of the row's checks composes, on every
// project it selects: part of the check's own work, however its forwarded arguments narrow it.
func (r targetRun) composedByCheck(composes lazyProducers) bool {
	if r.verb != "run" {
		return false
	}
	composed := composes()
	return !slices.ContainsFunc(r.selection(), func(p string) bool { return !composed(r.target, p) })
}

// runsOp reports a run of a spell op rather than a magusfile target: one naming a spell
// other than the magusfile's, or a target the magusfile of a project it selects does not
// export. Over every project it is an op when no project exports it.
func (r targetRun) runsOp(ws types.WorkspaceReader) bool {
	if spell, _ := splitSpell(r.target); spell != "" && spell != types.MagusfileSpellName {
		return true
	}
	name := targetName(r.target)
	exports := func(p *types.Project) bool { return p != nil && slices.Contains(p.MagusfileTargets, name) }
	if slices.Contains(r.selection(), "/") {
		return !slices.ContainsFunc(ws.All(), exports)
	}
	return slices.ContainsFunc(r.selection(), func(p string) bool { return !exports(ws.Get(path.Clean(p))) })
}

// opHonorsRewrite reports whether the rw charm changes target's op: a catalog op declaring
// it, or an op the catalog does not hold, since nothing then says the charm is ignored. An
// op declaring no rw runs its argv unpatched, so default_charms [rw] write nothing through
// it.
func opHonorsRewrite(catalog []*spells.Spell, target string) bool {
	spell, _ := splitSpell(target)
	name := targetName(target)
	found := false
	for _, s := range catalog {
		if spell != "" && s.Name() != spell {
			continue
		}
		if _, ok := s.Op(name); !ok {
			continue
		}
		if slices.Contains(s.Charms(name), types.CharmReadWrite) {
			return true
		}
		found = true
	}
	return !found
}

// composers are the magusfile targets, in the projects the run selects, whose body calls
// the op the run names. A graph that cannot be read names none.
func (r targetRun) composers(graph func() (types.TargetGraphOutput, error)) []string {
	g, err := graph()
	if err != nil {
		return nil
	}
	op := targetName(r.target)
	selected := r.selection()
	var names []string
	for _, p := range g.Projects {
		dir := path.Clean(cmp.Or(p.Path, "."))
		if !slices.Contains(selected, "/") && !slices.ContainsFunc(selected, func(s string) bool { return path.Clean(s) == dir }) {
			continue
		}
		for _, n := range p.Nodes {
			calls := slices.ContainsFunc(n.Spells, func(u types.TargetSpellUse) bool {
				return slices.ContainsFunc(u.Ops, func(o string) bool { return targetName(o) == op })
			})
			if name := targetName(n.Name); calls && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// parse splits the run's target into its spell filter and the target with its charms.
func (r targetRun) parse() (spell string, t types.Target, err error) {
	spell, name := splitSpell(r.target)
	t, err = types.ParseTarget(name)
	return spell, t, err
}

// rewrites reports a run under the rw charm: named on its target, or carried by defaults,
// the workspace's default_charms, on a `magus run` without --no-default-charms.
func (r targetRun) rewrites(defaults []string) bool {
	_, t, err := r.parse()
	if err != nil {
		return false
	}
	if slices.Contains(t.Charms, types.CharmReadWrite) {
		return true
	}
	return r.verb == "run" && !r.noDefaultCharms && defaultsRewrite(defaults)
}

// defaultsRewrite reports default_charms carrying the rw charm.
func defaultsRewrite(defaults []string) bool {
	return slices.ContainsFunc(defaults, func(c string) bool {
		return types.NormalizeCharm(c) == types.CharmReadWrite
	})
}

// readOnly is the run's command without the rw charm, keeping its other charms, its
// projects and its forwarded arguments. It adds --no-default-charms only when defaults
// would put rw back, so a default charm that does not write stays on.
func (r targetRun) readOnly(defaults []string) string {
	spell, t, _ := r.parse()
	name := t.Name
	if kept := slices.DeleteFunc(slices.Clone(t.Charms), func(c string) bool { return c == types.CharmReadWrite }); len(kept) > 0 {
		name += ":" + strings.Join(kept, ",")
	}
	if spell != "" {
		name = spell + "::" + name
	}
	args := append([]string{name}, r.projects...)
	if r.noDefaultCharms || defaultsRewrite(defaults) {
		args = append(args, "--no-default-charms")
	}
	if len(r.forwarded) > 0 {
		args = append(append(args, "--"), r.forwarded...)
	}
	return hint.Run.With(args...)
}

// writesOutside reads what a run under the rw charm writes from the workspace's
// declarations, in every project the run selects: its target's ctx.writesFiles and
// ctx.modifiesExistingFiles globs, and the ctx.modifiesExistingFiles globs of each step
// its ctx.needs chain reaches in that project. It returns the rooted globs no write path
// covers.
//
// A needed step's outputs and another project's steps are left out: they build what the
// run consumes, and another project's ci settles its own formatting.
func (r targetRun) writesOutside(ws types.WorkspaceReader, writePaths []string) (outside []string) {
	var projects []*types.Project
	if slices.Contains(r.selection(), "/") {
		projects = ws.All()
	} else {
		for _, p := range r.selection() {
			if found := ws.Get(path.Clean(p)); found != nil {
				projects = append(projects, found)
			}
		}
	}
	name := targetName(r.target)
	for _, p := range projects {
		var globs []types.Glob
		for _, ref := range p.TargetOutputs[name] {
			globs = append(globs, ref.Rooted(p.Path))
		}
		for _, ref := range p.TargetUpdates[name] {
			globs = append(globs, ref.Rooted(p.Path))
		}
		// A nil lookup keeps the chain in p.
		for _, g := range append(globs, types.ChainUpdates(p, name, nil)...) {
			if !leaseCoversGlob(writePaths, g.Pattern) && !slices.Contains(outside, g.Pattern) {
				outside = append(outside, g.Pattern)
			}
		}
	}
	return outside
}

// leaseCoversGlob reports whether writePaths cover every path glob can match.
//
// declarationCovering matches a declaration against the glob's own text, which holds for
// `*`: a declared `docs/gen/*` matches the output `docs/gen/*.svg` and covers it. It fails
// for `**`, which a declared `docs/*` would match as one segment while the output reaches
// every depth. A glob holding `**` is covered only by a declaration spelling the same
// glob, one claiming a whole directory its literal prefix lies in, or a declared
// `<dir>/**/<tail>` whose dir holds that prefix and whose tail ends the glob.
func leaseCoversGlob(writePaths []string, glob string) bool {
	if !strings.Contains(glob, "**") {
		_, covered, _ := declarationCovering(writePaths, glob)
		return covered
	}
	var prefix []string
	for _, segment := range strings.Split(glob, "/") {
		if types.IsGlobMeta(segment) {
			break
		}
		prefix = append(prefix, segment)
	}
	literal := strings.Join(prefix, "/")
	under := func(dir string) bool { return literal == dir || strings.HasPrefix(literal, dir+"/") }
	return slices.ContainsFunc(writePaths, func(raw string) bool {
		file, _ := types.SplitClaim(raw)
		declared := path.Clean(file)
		if declared == glob || declared == "**" {
			return true
		}
		if literal == "" {
			return false
		}
		if dir, tail, ok := strings.Cut(declared, "/**/"); ok && !types.IsGlobMeta(dir) && !strings.Contains(tail, "**") {
			return under(dir) && strings.HasSuffix(glob, "/"+tail)
		}
		dir := strings.TrimSuffix(declared, "/**")
		if dir == "." || dir == "/" || types.IsGlobMeta(dir) {
			return false
		}
		return under(dir)
	})
}

// installs reports a run of a dependency install: the workspace's `install` target, or a
// spell op of the install kind, with no charm. A charm is refused because `update` is one,
// and re-resolving a lockfile writes outside every worker's paths.
func (r targetRun) installs(deps Dependencies) bool {
	spell, target := splitSpell(r.target)
	if r.verb != "run" || strings.Contains(target, ":") {
		return false
	}
	if spell == "" && targetName(target) == "install" {
		return true
	}
	for _, s := range deps.spells() {
		if spell != "" && s.Name() != spell {
			continue
		}
		if op, ok := s.Op(targetName(target)); ok && op.Kind == spells.OpKindInstall {
			return true
		}
	}
	return false
}

// checksCompose reports which targets the checks compose: every target a check's target
// reaches through ctx.needs, in its own project or another, and every spell op those
// targets call, read from the workspace's static target graph. A workspace that cannot
// be read composes nothing, which leaves each run to the check alone.
func checksCompose(targetGraph func() (types.TargetGraphOutput, error), checks []types.LeaseCheck) func(target, project string) bool {
	graph, err := targetGraph()
	if err != nil {
		return func(string, string) bool { return false }
	}
	type key struct{ target, project string }
	nodes := map[key]types.TargetGraphNode{}
	for _, p := range graph.Projects {
		for _, n := range p.Nodes {
			nodes[key{targetName(n.Name), path.Clean(cmp.Or(p.Path, "."))}] = n
		}
	}
	composed := map[key]bool{}
	var walk func(k key)
	walk = func(k key) {
		if composed[k] {
			return
		}
		composed[k] = true
		n, ok := nodes[k]
		if !ok {
			return
		}
		for _, d := range n.Dependencies {
			walk(key{targetName(d), k.project})
		}
		for _, d := range n.CrossDependencies {
			walk(key{targetName(d.Target), path.Clean(d.Project)})
		}
		for _, use := range n.Spells {
			for _, op := range use.Ops {
				composed[key{targetName(op), k.project}] = true
			}
		}
	}
	for _, c := range checks {
		walk(key{targetName(c.Target), path.Clean(cmp.Or(c.Project, "."))})
	}
	return func(target, project string) bool { return composed[key{targetName(target), path.Clean(project)}] }
}

// checkoutBinary is the binary the hooks and the guard run, by the name they find it by
// at the checkout's root.
const checkoutBinary = "magus"

// buildsCheckoutBinary reports a run of a target the workspace declares as the producer
// of ./magus: only a workspace that builds magus itself declares one.
func buildsCheckoutBinary(ctx context.Context, workspace func() (types.WorkspaceRepository, error), r targetRun) bool {
	if r.verb != "run" {
		return false
	}
	ws, err := workspace()
	if err != nil {
		return false
	}
	files, err := ws.ClassifyFiles(ctx, []string{checkoutBinary})
	if err != nil {
		return false
	}
	selected := r.selection()
	for _, f := range files {
		if path.Clean(f.Path) != checkoutBinary {
			continue
		}
		for _, c := range f.Claims {
			if c.Role == "output" && targetName(c.Target) == targetName(r.target) &&
				(slices.Contains(selected, "/") || slices.Contains(selected, path.Clean(cmp.Or(c.Project, ".")))) {
				return true
			}
		}
	}
	return false
}

// rowChecks are the runs a row names as its checks: the check record and every check goal.
func rowChecks(row types.Job) []types.LeaseCheck {
	var out []types.LeaseCheck
	for _, g := range row.EffectiveGoals() {
		if g.Kind == types.GoalKindCheck && g.Check.Target != "" {
			out = append(out, g.Check)
		}
	}
	if len(out) > 0 || row.Validation == "" {
		return out
	}
	// compat: see types.ParseLeaseRunLine
	if c, err := types.ParseLeaseRunLine(row.Validation); err == nil {
		out = append(out, c)
	}
	return out
}

// sameTarget reports whether a run names a check's target. The charm is ignored, since
// `lint:rw` is the same work written back; a spell filter is compared only when both name
// one, as internal/job.ranCheck does.
func sameTarget(check, ran string) bool {
	cs, ct := splitSpell(check)
	rs, rt := splitSpell(ran)
	return targetName(ct) == targetName(rt) && (cs == "" || rs == "" || cs == rs)
}

func splitSpell(s string) (spell, target string) {
	spell, target, ok := strings.Cut(s, "::")
	if !ok {
		return "", s
	}
	return spell, target
}

// targetName is a target's normalized name, charm and spell filter dropped.
func targetName(s string) string {
	_, t := splitSpell(s)
	name, _, _ := strings.Cut(t, ":")
	return types.Normalize(name)
}

// leaseProducers reports which targets declare an output among writePaths, answered from
// the workspace's own declarations.
//
// A workspace that cannot be read falls back to this repository's producer spelling, a
// `-generate` name or a charm such as `:rw`: a rule the guard cannot evaluate must not
// block a tool call. A run under the rw charm is still refused by rowJudge, which reads
// the same workspace to hold it to the write paths.
func leaseProducers(ctx context.Context, workspace func() (types.WorkspaceRepository, error), writePaths []string) func(target, project string) bool {
	looksGenerating := func(target, _ string) bool {
		name := targetName(target)
		_, t := splitSpell(target)
		return name == "generate" || strings.HasSuffix(name, "-generate") || strings.Contains(t, ":")
	}
	ws, err := workspace()
	if err != nil {
		return looksGenerating
	}
	paths := make([]string, 0, len(writePaths))
	for _, p := range writePaths {
		file, _ := types.SplitClaim(p)
		paths = append(paths, file)
	}
	files, err := ws.ClassifyFiles(ctx, paths)
	if err != nil {
		return looksGenerating
	}
	declared := map[[2]string]bool{}
	for _, f := range files {
		for _, c := range f.Claims {
			if c.Role == "output" && c.Target != "" {
				declared[[2]string{targetName(c.Target), path.Clean(cmp.Or(c.Project, "."))}] = true
			}
		}
	}
	return func(target, project string) bool {
		return declared[[2]string{targetName(target), path.Clean(project)}]
	}
}

// checkCommand renders a check as the command that runs it, quoted for a shell.
func checkCommand(c types.LeaseCheck) string {
	args := []string{c.Target, cmp.Or(c.Project, ".")}
	if c.NoDefaultCharms {
		args = append(args, "--no-default-charms")
	}
	if len(c.Args) > 0 {
		args = append(args, "--")
		for _, a := range c.Args {
			if q, err := syntax.Quote(a, syntax.LangBash); err == nil {
				a = q
			}
			args = append(args, a)
		}
	}
	return hint.Run.With(args...)
}

// actingLiveLease reads the acting lease's own live row, reporting none whenever the
// ledger cannot answer. An unreadable ledger and an id nobody declared are one silence
// here: both leave nothing to judge against, and a rule the guard cannot evaluate must
// not block a tool call.
func actingLiveLease(ctx context.Context, deps Dependencies, actingLease string) (types.Job, bool) {
	standing := actingLeaseStanding(ctx, deps, actingLease)
	if !standing.declared || !standing.state.Live() {
		return types.Job{}, false
	}
	return standing.row, true
}

// leaseStanding is where the acting lease's row stands in the ledger: whether the ledger
// could be read at all, whether it declares the id, the state it recorded, and the row.
//
// Three answers, not two. "The ledger says nothing" and "the ledger could not be read" are
// the difference between an id nobody declared and a rule the guard cannot evaluate, and
// only the first is the caller's mistake.
type leaseStanding struct {
	readable bool
	declared bool
	state    types.JobState
	row      types.Job
	// rows is the whole store, for a rule that reads the job tree around row.
	rows []types.Job
	// endedBinding is the job the caller was bound to when its own record is a tombstone:
	// the job store ended the binding when that job's checkout was removed (see
	// job.Binding). "" for any other record.
	endedBinding string
}

// terminal reports a row that is declared and has stopped running, so its rules are inert.
func (s leaseStanding) terminal() bool {
	return s.declared && !s.state.Live()
}

// inFlight reports a row whose work is still to come: declared or running. A holder may
// take another job only once its own is not, since walking away mid-flight would leave
// its next write graded under a job nobody handed it. Exited is not in flight although it
// is live: nobody is required to ever wait on it, and a caller held to it would be stuck.
func (s leaseStanding) inFlight() bool {
	return s.declared && (s.state == types.StateDeclared || s.state == types.StateRunning)
}

// actingLeaseStanding looks the acting lease up across EVERY row, terminal ones included.
//
// Terminal ones included, because "does this id mean anything here" is what separates a
// typo from a plan that has already finished, and the two cases want opposite verdicts: a
// dead id looked exactly like a guarded session.
func actingLeaseStanding(ctx context.Context, deps Dependencies, actingLease string) leaseStanding {
	if !types.ValidJobID(actingLease) {
		return leaseStanding{}
	}
	location := hookLocation(ctx, deps)
	if location.cacheDir == "" {
		return leaseStanding{}
	}
	leases, err := leaseRows(ctx, location)
	if err != nil {
		return leaseStanding{}
	}
	for _, u := range leases {
		if u.ID == actingLease {
			return leaseStanding{readable: true, declared: true, state: u.State, row: u, rows: leases}
		}
	}
	return leaseStanding{readable: true, rows: leases}
}

// denyUndeclaredLease refuses to grade a call for an id this workspace's ledger does not
// carry, and refuses nothing whenever it does carry one or cannot say.
//
// An id that names no row buys SILENT un-enrolled treatment: every lease-scoped rule reads
// it, finds nothing, and passes, so a worker whose orchestrator typo'd the id runs
// unguarded and looks exactly like a guarded one. It denies rather than advises because the
// caller asserted a binding that does not exist.
//
// Not the INVALID-id case, which stays an advisory (adviseInvalidLease): an id magus cannot
// parse is one it cannot look up either, and blocking a tool call over unparsable metadata
// is the failure the fail-open contract is written against.
//
// It refuses WORK and never the remedy. command is the shell line for a shell-command call
// and "" for a file write, where every call is work by definition. A rule that refused
// the line it prints locked a checkout out of its own repair: the plan moved house once and
// the bound worker could not read the plan, print a schema, or bind again, because each of
// those is a command and every command was refused.
//
// A tombstoned binding is refused the same way, whatever the ledger holds: its job's row
// or checkout is gone, and reading the caller as unbound would grade it as the orchestrator.
func denyUndeclaredLease(standing leaseStanding, actingLease, command string) denial {
	if standing.endedBinding == "" && (!standing.readable || standing.declared) {
		return denial{}
	}
	if command != "" && undeclaredLeaseRepairs(command) {
		return denial{}
	}
	if standing.endedBinding != "" {
		return denial{
			Say: fmt.Sprintf("magus workspace: your binding to job %s ended when its row or its checkout was removed; `%s` binds you again.",
				standing.endedBinding, hint.JobExec.With("<job>")),
			Why: "A caller whose binding ended is refused rather than read as unbound, because an unbound caller is graded as the orchestrator, which no write path holds. " +
				"Bind from a checkout that exists.\n" +
				"Reading the tree, printing a schema or a usage line, and the job verbs themselves still run.",
		}
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: lease %s is not declared; `%s` lists the ids.", actingLease, hint.LsJobs.String()),
		Why: "Every lease-scoped rule reads that row, so a call naming a row this workspace's ledger does not carry is graded by nothing at all. That is the shape a typo'd id takes: an agent that believes it is inside a boundary, running outside every one.\n" +
			"Reading the tree, printing a schema or a usage line, and the job verbs themselves still run, so you can find the id you were handed and bind to it.",
	}
}

// undeclaredLeaseRepairs reports a shell line that only looks at this checkout or repairs
// the binding, so an unenrolled session can find out what it is bound to and fix it.
//
// EVERY invocation on the line has to qualify. One reader chained onto a build is a build,
// and the cheap way to get work past this rule would be to put `ls &&` in front of it.
func undeclaredLeaseRepairs(command string) bool {
	cmds, ok := ParseCommands(command)
	if !ok || len(cmds) == 0 {
		return false
	}
	for _, c := range cmds {
		if !repairInvocation(c) {
			return false
		}
	}
	// A reader whose output a redirect writes into a file is a writer.
	return !slices.ContainsFunc(redirectTargets(command, 0, DialectBash), func(target string) bool { return target != os.DevNull })
}

// repairInvocation judges one invocation. magus answers for itself: a usage line or a
// schema touches no store, and the verbs that read the plan or take a job are the remedy.
// Everything else is the short reader set a person orients with, which is deliberately
// narrower than cacheDirReaders (git and magus are wholesale readers there, and `git
// commit` is not a way to find out what you are bound to). sed and find each have a
// spelling that writes, so each is read past it.
func repairInvocation(c hint.Invocation) bool {
	if path.Base(c.Name) == "magus" {
		if slices.ContainsFunc(c.Args, func(a string) bool { return a == "--schema" || a == "--help" || a == "-h" }) {
			return true
		}
		words := magusSubcommandWords(c.Args)
		return len(words) > 0 && undeclaredLeaseVerbs[words[0]] && !renamesSymbol(c.Args)
	}
	if c.Name == "git" {
		g := parseGit(c.Args)
		return g.alias == "" && gitReadVerbs[g.sub]
	}
	switch name := path.Base(c.Name); name {
	case "sed":
		return sedOnlyPrints(c.Args)
	case "find":
		return onlyReads(name, "", c.Args)
	default:
		return undeclaredLeaseReaders[name]
	}
}

// sedPrintScript is a sed script that only prints by line number: `1,5p`, `$p`, `12q`,
// `3=`, joined by `;`. It is the shape read-navigation hands out.
var sedPrintScript = regexp.MustCompile(`^\s*(?:(?:\d+|\$)(?:\s*,\s*(?:\d+|\$))?\s*[pq=]\s*(?:;\s*|$))+$`)

// sedOnlyPrints reports a sed that reads its files and only prints line ranges of them.
// Anything it cannot read that far is refused: a script file, an in-place flag, an
// unknown flag, and any script but a line-range print, since `w`, `W` and GNU's `e` write
// files and run commands from inside the script.
func sedOnlyPrints(args []string) bool {
	var scripts []string
	bare := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--expression" || len(a) > 1 && a[0] == '-' && a[len(a)-1] == 'e' && strings.Trim(a[1:len(a)-1], "nErsuz") == "":
			if i+1 >= len(args) {
				return false
			}
			i++
			scripts = append(scripts, args[i])
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
		case a == "--quiet" || a == "--silent" || a == "--regexp-extended" || a == "--separate":
		case len(a) > 1 && a[0] == '-' && strings.Trim(a[1:], "nErsuz") == "":
		case len(a) > 0 && a[0] == '-':
			return false
		case len(scripts) == 0 && !bare:
			bare = true
			scripts = append(scripts, a)
		}
	}
	if len(scripts) == 0 {
		return false
	}
	for _, s := range scripts {
		if !sedPrintScript.MatchString(s) {
			return false
		}
	}
	return true
}

// The three sets repairInvocation reads. The magus verbs are the ones that answer "what am
// I bound to and what does this workspace hold"; the job verbs are in it because taking a
// job is how a checkout stops being unenrolled.
var (
	undeclaredLeaseVerbs = map[string]bool{
		"ledger": true, "job": true, "session": true, "ls": true, "describe": true,
		"query": true, "explain": true, "path": true, "refs": true, "where": true,
		"status": true, "doctor": true, "help": true, "version": true,
	}
	gitReadVerbs = map[string]bool{
		"status": true, "log": true, "diff": true, "show": true, "branch": true, "remote": true,
	}
	undeclaredLeaseReaders = map[string]bool{
		"cat": true, "head": true, "tail": true, "ls": true, "grep": true, "rg": true,
		"stat": true, "wc": true, "pwd": true, "echo": true, "nl": true, "cut": true,
		"diff": true, "cmp": true, "basename": true, "dirname": true, "realpath": true,
		"readlink": true, "file": true, "du": true, "tree": true,
	}
)

// adviseInvalidLease says that an id magus cannot parse was treated as naming no lease.
//
// Treated as absent rather than rejected: an id magus cannot parse is one it cannot look
// up either, and erroring would block the tool call over metadata. Said for EVERY kind of call,
// from Judge: it used to be produced inside gradeLeasedWrite, so a shell-command call
// under a typo'd id ran fully un-enrolled with no notice at all.
func adviseInvalidLease(actingLease string) advice {
	if actingLease == "" || types.ValidJobID(actingLease) {
		return advice{}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: %s=%q is not a valid lease id: fix it and re-run, as this call was graded as naming no lease.", envHookLease, actingLease),
		Why: fmt.Sprintf("A lease id is at most %d characters of A-Za-z0-9-_./:. Once it parses, the guard grades the call against your lease's declared boundary.", types.MaxJobIDLen),
	}
}

// adviseTerminalLease says that a declared row has stopped running, or "" for a live one.
//
// The rules keyed on the row are inert from that moment (liveLeases drops it), which is
// correct and invisible: the verdicts look identical to a session nobody leased. One line
// per session is what makes the difference readable.
func adviseTerminalLease(standing leaseStanding, actingLease string) advice {
	if !standing.terminal() {
		return advice{}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: lease %s is in state %s; its rules are inert for this session.", actingLease, standing.state),
		Why: "A terminal row has no boundary left to grade against, so the lease-scoped denials are not running. If work is still in flight under that id, the orchestrator owns reopening it.",
	}
}

// denyOverdueLease refuses a write graded under a live lease past its deadline, or returns
// "" for one with no deadline or time left. A taken row past its deadline is ended by the
// next sweep of the store, so this refuses writes under one nobody took, which only
// `magus job prune` ends, and under a taken one read before that sweep.
func denyOverdueLease(me types.Job, now int64) string {
	if !me.Overdue(now) {
		return ""
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: lease %s passed its deadline; stop writing and report what you have.", me.ID),
		Why: fmt.Sprintf("Lease %s was forked with a timeout, and its deadline %s passed %s ago, so the guard denies every write graded under it. "+
			"The row is still %s: the orchestrator ends it with `%s` or re-forks it with a new --timeout.",
			me.ID, time.Unix(me.Deadline, 0).UTC().Format(time.RFC3339),
			time.Duration(now-me.Deadline)*time.Second, me.State, hint.JobExit.With(me.ID)),
	}.full()
}

// denyLeaseScopedRebind refuses, under a bound lease, every command that would rewrite
// WHO the caller is or what its row says.
//
// The store refuses these too, on the ACTING LEASE id; registered_by is provenance
// recorded beside it and is read by no rule. This rule is the half that arrives BEFORE the
// call, carrying the reason: an agent that reads a denial naming the tool reaches for the
// tool. Being told first costs one verdict; finding out from a store error costs a turn
// and teaches nothing about why.
//
// A READ is untouched. `magus describe job` prints the row a worker is bound to, and
// `magus ls jobs` prints the plan; refusing those would deny a worker the ability to find
// out what it is bound to, which is the opposite of what this rule is for.
//
// Unbound callers are untouched too, for the reason every lease rule here gives: an
// orchestrator and a person at a terminal both name no lease, and they are the ones who
// write rows.
//
// A child of the caller's own lease, and `job wait` on one of its descendants, pass: the
// store grades a child against its parent (see childForkRebind for when it cannot).
func denyLeaseScopedRebind(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	f, err := parseFile(command, DialectBash)
	if err != nil {
		return denial{}
	}
	// The magus the line runs resolves its store's actor as job.RunLease does: the
	// checkout's record, else the BAGGAGE it starts with, which is the line's own when it
	// sets one and the session's otherwise, the environment this hook shares.
	claim := cmp.Or(lineBaggageLease(f), trail.LeaseFromEnv())
	h := holder{
		id:       actingLease,
		standing: sync.OnceValue(func() leaseStanding { return actingLeaseStanding(ctx, deps, actingLease) }),
		storeLease: func() string {
			lease, _ := job.ActingLease(hookLocation(ctx, deps).jobs(), claim)
			return lease
		},
	}
	var what rebind
	syntax.Walk(f, func(n syntax.Node) bool {
		st, ok := n.(*syntax.Stmt)
		if !ok || what.refuses() {
			return !what.refuses()
		}
		for _, c := range stmtCommands(st, DialectBash) {
			if what = leaseRebind(c, stdinRecord(st), h); what.refuses() {
				return false
			}
		}
		return true
	})
	if !what.refuses() {
		return denial{}
	}
	fix := what.fix
	if fix == "" {
		fix = leaseActorClause(what.ask)
	}
	why := "This call acts under lease " + actingLease + ". An agent that can move the rows it is graded against is graded against a boundary nobody handed it from the next call on, which is the one thing the ledger exists to make visible."
	if what.why != "" {
		why = what.why + "\n" + why
	}
	if what.fix == "" {
		why += "\n" + leaseActorWhy
	}
	return denial{Say: "magus workspace: `" + elideCommand(command) + "` " + what.problem + "; " + fix, Why: why}
}

// lineBaggageLease is the lease the last BAGGAGE assignment in f names, exported,
// assigned or prefixed to a command, "" when f sets none. A prefix counts for the whole
// line, not only the command it precedes: it can only make the line's store write as a
// lease, which the store then grades.
func lineBaggageLease(f *syntax.File) string {
	lease := ""
	take := func(a *syntax.Assign) {
		if a == nil || a.Name == nil || a.Name.Value != trail.EnvBaggage || a.Value == nil {
			return
		}
		if l := trail.LeaseFromBaggage(literalWord(a.Value.Parts)); l != "" {
			lease = l
		}
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.DeclClause:
			for _, a := range x.Args {
				take(a)
			}
		case *syntax.CallExpr:
			for _, a := range x.Assigns {
				take(a)
			}
		}
		return true
	})
	return lease
}

// rebind is what the rebind rule refuses a bound caller's command, worded as the verdict
// it ends in: "`<command>` <problem>; <fix>".
type rebind struct {
	// problem names what the command would do to the ledger.
	problem string
	// ask is what the orchestrator would do instead, for leaseActorClause.
	ask string
	// fix is the caller's own correction, when there is one; it replaces the ask.
	fix string
	// why is the detail behind this case.
	why string
}

func (r rebind) refuses() bool { return r.problem != "" }

// denyLeaseScopedHarness refuses, under a lease, the command that rewrites a harness's
// skill trees. The skills are what steers a worker, so a worker that could rewrite them
// could rewrite its own instructions. magus writes no host hook wiring at all: `magus
// describe harness` prints it for a person to merge.
//
// `magus agent harness install` refuses itself too, but a CLI process knows only the
// checkout's binding and its own BAGGAGE claim. The guard also knows the calling subagent
// and the host session, so a worker attributed by either is refused here.
func denyLeaseScopedHarness(_ context.Context, _ Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return denial{}
	}
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h") || magusFlag(c.Args, "help") {
			continue
		}
		words := magusSubcommandWords(c.Args)
		if hint.AgentHarnessInstall.MatchedBy(words) {
			return denial{
				Say: fmt.Sprintf("magus workspace: `%s` would rewrite the skills that steer lease %s's calls; %s",
					elideCommand(command), actingLease, leaseActorClause("rewrite a harness's skills")),
				Why: leaseActorWhy,
			}
		}
	}
	return denial{}
}

// holder is what the rebind rule reads about the bound caller, each part fetched only on
// the paths that need it.
type holder struct {
	id       string
	standing func() leaseStanding
	// storeLease is the lease the job store writes as from this checkout, "" for none.
	storeLease func() string
}

// leaseRebind names what a parsed command would do to the ledger when it is one a bound
// caller may not do, or nothing for everything else. stdin is the record the command's
// statement feeds it, "" when the line carries none the guard can read.
//
// A client script's magus\job write is graded here alongside the CLI ones because it is
// the SAME write through a different transport, and a rule that held on one channel would
// move the traffic rather than stop it.
func leaseRebind(c hint.Invocation, stdin string, h holder) rebind {
	if c.Name == hint.ToolClient.String() {
		return jobToolRebind(mcpParams(c.Args), h)
	}
	if path.Base(c.Name) != "magus" {
		return rebind{}
	}
	// A usage line and a schema are reads: neither reaches a store, and the rule below
	// reads the subcommand without them, so `job wait --schema` was refused to the one
	// caller who needs the shape of the result it has to file.
	if slices.ContainsFunc(c.Args, func(a string) bool { return a == "--schema" || a == "--help" || a == "-h" }) {
		return rebind{}
	}
	words := magusSubcommandWords(c.Args)
	if len(words) < 2 {
		return rebind{}
	}
	// A holder must always be able to READ the rubric it is graded against. These print the
	// contract and touch no row, and the schema is the same for every row, so refusing them
	// protects nothing and guarantees results written from memory of another vocabulary by
	// the people most expected to file good ones.
	if hasFlag(c.Args, 0, "schema") || hasFlag(c.Args, 0, "help") {
		return rebind{}
	}
	switch {
	// Taking the job the caller already holds is the bootstrap run twice, and records the
	// base it landed on. Taking another is the escape this rule closes, but only while the
	// held job is in flight: once it has exited or ended, or names no row at all, the next
	// exec is how the caller moves on (see bindOnExec).
	case hint.JobExec.MatchedBy(words) && len(words) > 2:
		if held := h.standing(); !held.inFlight() || execOperand(c.Args) == held.row.ID {
			return rebind{}
		}
		return rebind{problem: "would take the lease on another job while lease " + h.id + " is in flight", ask: "hand that job out"}
	case words[0] == hint.JobWait.Head() && words[1] == hint.JobWait.Leaf():
		if len(words) > 2 && descendsFrom(h.standing(), words[2]) {
			return rebind{}
		}
		return rebind{problem: "would verify a job outside lease " + h.id + "'s tree", ask: "verify it"}
	case words[0] == hint.JobFork.Head() && words[1] == hint.JobFork.Leaf():
		return childForkRebind(cliFork(c.Args, stdin), h, "declare a job", "fork it")
	}
	return rebind{}
}

// descendsFrom reports whether id sits below the held row in the parent chain.
func descendsFrom(held leaseStanding, id string) bool {
	return held.declared && held.state.Live() &&
		slices.ContainsFunc(types.JobDescendants(held.rows, held.row.ID), func(r types.Job) bool { return r.ID == id })
}

// childFork is the row a fork declares, as far as childForkRebind reads it.
type childFork struct {
	id, parent string
	readOnly   bool
	writePaths bool
	// bounded is any declaration beyond the lineage, the prose and read_only: paths, a
	// target check, gates, a state. Each is something the store grades against the parent.
	// A check naming only a script is not one: it grants nothing, and it is the one way a
	// read-only scout's claim can pass `job wait`.
	bounded bool
}

// childForkRebind names what a bound caller's fork would do when the guard refuses it, or
// nothing for a new child of its own lease that is left to the store. verb names the
// write in the refusal, and ask is what the orchestrator would do instead.
//
// The store grades the child's boundary against the parent's (internal/job/authorize.go
// authorizeChild), but only when it writes AS that lease, which it learns from this
// checkout's record. A worker the hook identified in a checkout bound to nobody writes to
// an unbound store that grades nothing, so there only a read-only child declaring no
// boundary, at most a script check, passes: it can widen nothing.
//
// A child that is neither read-only nor handed write paths is refused on both paths: an
// empty write set scopes nothing (gradeAgainstOwnLease), and the store's subset test
// passes it.
func childForkRebind(f childFork, h holder, verb, ask string) rebind {
	held := h.standing()
	switch {
	case !held.declared || !held.state.Live():
		return rebind{problem: "would " + verb + ", and lease " + h.id + " is not live to hand one out", ask: ask}
	case f.id == "":
		return rebind{problem: "would " + verb + " the guard cannot read", fix: "give the child an id and --parent " + h.id + ", or its record in a quoted heredoc."}
	case f.id == h.id:
		return rebind{problem: "would rewrite the job it holds", ask: "change it"}
	case f.parent != h.id:
		return rebind{problem: "is outside lease " + h.id + "'s tree", ask: ask,
			why: fmt.Sprintf("It would %s outside its own tree: a row a worker creates must name %s as its parent, and this one names %q.", verb, h.id, f.parent)}
	case slices.ContainsFunc(held.rows, func(r types.Job) bool { return r.ID == f.id }):
		return rebind{problem: "would " + verb + " over " + f.id + ", which already exists", ask: "change it",
			why: "A worker writes no row but its own and the children it hands out."}
	case !f.readOnly && !f.writePaths && (held.row.ReadOnly || len(held.row.WritePaths) > 0):
		return rebind{problem: "would " + verb + " that can write anywhere", fix: "fork it --read-only or with --write-paths inside your own.",
			why: "A child that is not read-only and names no write paths is scoped by nothing."}
	case (!f.readOnly || f.bounded) && h.storeLease() != h.id:
		return rebind{problem: "would " + verb + " nothing would grade", fix: "fork it --read-only, declaring no paths or gates, and at most a script check.",
			why: fmt.Sprintf("This checkout's job store writes as %q, not as %s, so it checks no child against your row.", h.storeLease(), h.id)}
	}
	return rebind{}
}

// forkFlagsUnbounded are the `job fork` flags, and the magus\job.put opts, that declare no
// boundary: the lineage, the prose, the timing, read_only and the checkout the child works in.
var forkFlagsUnbounded = map[string]bool{
	"parent": true, "criteria": true, "model": true, "timeout": true, "checkpoint": true, "checkout": true,
	"read-only": true, "read_only": true, "depends-on": true, "depends_on": true,
	"stdin": true, "op": true, "id": true,
}

// cliFork reads the child a `magus job fork` argv declares. Under --stdin the flags are
// ignored and the row is the record, which the guard reads only from a heredoc or a
// here-string on the same statement; a record it cannot read declares no id.
func cliFork(args []string, stdin string) childFork {
	operands, flags := verbArgv(args, hint.JobFork)
	if flags["stdin"] != "" && flags["stdin"] != "false" {
		row, err := job.DecodeDeclaration(strings.NewReader(stdin))
		if err != nil {
			return childFork{}
		}
		scriptOnly := row.Check != nil && row.Check.Target == "" && row.Check.Script != ""
		return childFork{
			id: row.ID, parent: row.Parent, readOnly: row.ReadOnly, writePaths: len(row.WritePaths) > 0,
			bounded: len(row.WritePaths)+len(row.ReadPaths)+len(row.DenyPaths)+len(row.Goals) > 0 ||
				(row.Check != nil && !scriptOnly) || row.Validation != "" || (row.State != "" && row.State != types.StateDeclared),
		}
	}
	f := childFork{parent: flags["parent"], writePaths: flags["write-paths"] != ""}
	if len(operands) == 1 {
		f.id = operands[0]
	}
	if v, ok := flags["read-only"]; ok {
		f.readOnly = v != "false"
	}
	for name := range flags {
		if _, own := verbFlag(hint.JobFork, name); own && !forkFlagsUnbounded[name] {
			f.bounded = true
		}
	}
	return f
}

// verbArgv splits a magus argv into the operands after its two-word verb and the flags it
// carries, a bool flag reading "true". Arity comes from the registry the CLI parses with;
// a flag it does not know takes no value, and the CLI refuses that argv anyway.
func verbArgv(args []string, verb hint.Command) (operands []string, flags map[string]string) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			operands = append(operands, a)
			continue
		}
		name, value, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
		takes, _ := MagusFlagTakesValue(name)
		if own, ok := verbFlag(verb, name); ok {
			takes = own.Kind != cmdline.Bool
		}
		switch {
		case joined:
		case takes && i+1 < len(args):
			i++
			value = args[i]
		default:
			value = "true"
		}
		flags[name] = value
	}
	return operands[min(2, len(operands)):], flags
}

// verbFlag looks name up among verb's own flags in the registry the CLI parses with.
func verbFlag(verb hint.Command, name string) (cmdline.Flag, bool) {
	for _, group := range commands.Root.Children {
		if group.Name != verb.Head() {
			continue
		}
		for _, sub := range group.Children {
			if sub.Name != verb.Leaf() {
				continue
			}
			if i := slices.IndexFunc(sub.Flags, func(f cmdline.Flag) bool { return f.Name == name }); i >= 0 {
				return sub.Flags[i], true
			}
		}
	}
	return cmdline.Flag{}, false
}

// stdinRecord is the text a statement feeds its command from a heredoc or a here-string,
// or "" when it feeds none or the text holds an expansion only the shell can resolve.
func stdinRecord(st *syntax.Stmt) string {
	for _, r := range st.Redirs {
		w := r.Hdoc
		if r.Op == syntax.WordHdoc {
			w = r.Word
		}
		if w == nil {
			continue
		}
		if !literalParts(w.Parts) {
			return ""
		}
		return literalWord(w.Parts)
	}
	return ""
}

// literalParts reports whether a word holds only text the shell passes through unchanged.
func literalParts(parts []syntax.WordPart) bool {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			if !literalParts(p.Parts) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// jobToolRebind judges one magus\job write from a client script against the job the
// caller holds, naming what the call would do when it is something a holder may not.
//
// Four carve-outs, and each is somebody else's job rather than a hole:
//
//   - `register` on the caller's OWN job records the base it landed on. That is the
//     holder's own procedure, demanded by the checkpoint denial in gradeAgainstOwnLease,
//     and denying it here would leave a holder unable to write anywhere at all. On
//     another job it is refused only while the held one is in flight, as the CLI form is.
//   - `put` on the caller's own job that only SHRINKS its write paths gives ground
//     back. It passes to the store, which owns whether a given shrink is legitimate; the
//     guard's job is the direction, and giving up a path cannot widen a role.
//   - `put` of a new row naming the caller's job as `parent` is a child, graded by
//     the store against its parent (childForkRebind).
//   - a read (list, and every member that writes nothing) is not a write.
//
// Shrinking is verbatim membership, not glob containment: every declaration in the call
// must already be one the job carries, and there must be fewer of them. A cleverer pattern
// that happens to cover less is not something this rule will try to prove.
func jobToolRebind(params map[string]string, h holder) rebind {
	op, id := params["op"], params["id"]
	if op == jobOpClear {
		return rebind{problem: "would drop every job", ask: "clear the plan"}
	}
	if op != jobOpPut && op != jobOpRegister && op != jobOpUnread {
		return rebind{}
	}
	standing := h.standing()
	if !standing.readable || standing.terminal() {
		// The store could not answer, or the job has already finished and every rule
		// keyed on it is inert. Naming somebody else's job here would be a reason the
		// caller can check and find false, in the same verdict that tells them these
		// rules are not running.
		return rebind{}
	}
	if op == jobOpUnread {
		return rebind{problem: "would run a client script the guard cannot read, which may write any job", ask: "run it"}
	}
	if op == jobOpRegister && (id == standing.row.ID || !standing.inFlight()) {
		return rebind{}
	}
	if _, entering := params["enter"]; entering && op == jobOpPut {
		// The store refuses anything but an entry beside `enter`, and grades the rest.
		if id != standing.row.ID && mayHandOut(standing.rows, standing.row.ID, id) {
			return rebind{}
		}
		return rebind{problem: "would enter a job not forked beneath the one it holds", ask: "enter it"}
	}
	if op == jobOpPut && id != standing.row.ID {
		return childForkRebind(mcpFork(params), h, "write another job", "write it")
	}
	if id == "" || id != standing.row.ID {
		return rebind{problem: "would write another job", ask: "write it"}
	}
	row := standing.row
	if shrinksWritePaths(params, row) {
		return rebind{}
	}
	return rebind{problem: "would rewrite the job it holds", ask: "change it"}
}

// mcpFork reads the child a magus\job.put declares.
func mcpFork(params map[string]string) childFork {
	f := childFork{
		id: strings.TrimSpace(params["id"]), parent: strings.TrimSpace(params["parent"]),
		readOnly:   params["read_only"] == "true",
		writePaths: params[writePathsParam] != "",
	}
	for key := range params {
		f.bounded = f.bounded || !forkFlagsUnbounded[key]
	}
	return f
}

// shrinksWritePaths reports whether a put changes nothing but the row's write paths, and
// only by removing entries it already carries.
//
// An ALLOWLIST over what the call carried, not a scan of the keys the guard happens to
// know: a key outside the three below is a rewrite of something else on the row whatever
// it holds. Reading a list of known keys instead would clear every key added to the
// ledger's merge until somebody remembered to add it in two places.
func shrinksWritePaths(params map[string]string, row types.Job) bool {
	declared, present := "", false
	for key, value := range params {
		switch key {
		case "op", "id":
		case writePathsParam:
			declared, present = value, true
		default:
			return false
		}
	}
	proposed := strings.Split(declared, ",")
	if !present || len(proposed) >= len(row.WritePaths) {
		return false
	}
	for _, decl := range proposed {
		if !slices.Contains(row.WritePaths, strings.TrimSpace(decl)) {
			return false
		}
	}
	return true
}

// integrateRole is the spawn-title role of the worker that integrates its checkout.
const integrateRole = "integrate"

// integratorIs names the checkout's integrator in a verdict.
const integratorIs = "the subagent spawned `<parent>/" + integrateRole + " <job>` for a row taken in that checkout"

// integrates reports whether a caller spawned as spawned, acting under row, integrates the
// checkout at workspace: the title is `<parent>/integrate <job>` naming row, row is live,
// and row was taken in that checkout. The integrator builds, regenerates and gates there;
// the root has no spawn title and a worker spawned under a change role in its own worktree
// has another role, so neither does.
//
// The role, not the row's check: a job's check is never the gate, so the check cannot say
// who integrates. One build at a time per checkout is the project lock's, which refuses a
// second run of a project the checkout is already running and names its holder.
func integrates(row types.Job, spawned, workspace string) bool {
	head, _, ok := strings.Cut(spawned, " ")
	if !ok || path.Base(head) != integrateRole || !row.State.Live() || row.CheckoutRoot == "" || workspace == "" {
		return false
	}
	if titled, ok := spawnTitleRow([]types.Job{row}, spawned); !ok || titled.ID != row.ID {
		return false
	}
	return samePath(row.CheckoutRoot, workspace)
}

// actingIntegrator reports whether the caller of this judgment, acting under me, integrates
// the checkout the call runs in.
func actingIntegrator(ctx context.Context, deps Dependencies, me types.Job) bool {
	return integrates(me, spawnedAs(deps.facts, deps.caller.Agent), hookLocation(ctx, deps).workspace)
}

// LeaseGateSource reports which of a row's declarations names the gate, so a refusal can
// quote the offending one, and false for a row naming none.
//
// It exists because the refusal quoted row.Validation whatever had actually matched, and a
// row can now name the gate from any of several places: told that its check `go-test api`
// names `ci`, a reader goes looking for a bug in a line that is fine while the goal that
// really did it goes unmentioned.
func LeaseGateSource(row types.Job) (string, bool) {
	// EVERY goal is asked, and the primary check no longer answers for the row. Returning
	// on row.Check alone meant a job whose check was some narrow target stopped the walk
	// before its goals were read, so a narrow check beside a `ci` check goal took the gate
	// through the door the check rule holds shut.
	for _, gate := range row.EffectiveGoals() {
		if gate.Kind != types.GoalKindCheck {
			continue
		}
		t, err := types.ParseTarget(gate.Check.Target)
		if err == nil && t.Name == types.TargetCI {
			if gate.ID == types.PrimaryGoalID {
				return gate.Check.String(), true
			}
			return fmt.Sprintf("goal %q (%s)", gate.ID, gate.Check.String()), true
		}
	}
	if validationNamesGate(row.Validation) {
		return row.Validation, true
	}
	return "", false
}

// compat(until: no ledger row is written without a check record): rows predating
// types.LeaseCheck carry only the rendered validation line, so it is read as words. `ci`,
// `magus run ci` and `affected ci` are all things a person writes there, and a stray `ci`
// elsewhere in the field reads as ownership and clears the deny, which is the direction
// this rule fails in on purpose. Observe it is safe to drop when every row
// `magus ledger list -o json` prints carries a `check` object.
func validationNamesGate(validation string) bool {
	for _, word := range strings.Fields(validation) {
		if t, err := types.ParseTarget(word); err == nil && t.Name == types.TargetCI {
			return true
		}
	}
	return false
}

// denyLeaseScopedVCS refuses a worker lease the version-control operations the
// orchestrator owns. The orchestrator lands every unit from the worker's tree, so a
// worker that pushes, stashes, reverts or rewrites history edits the state it is being
// integrated from, and a whole-tree revert destroys a sibling's uncommitted work. A commit,
// a cherry-pick or an applied patch is the exception (see workerLands): one on the worker's
// own branch, in the checkout its lease was taken in, adds to that tree without moving
// anything the orchestrator reads. So is an init that creates a repository outside the
// checkout, and the verbs after it on the same line inside that repository.
//
// The command is parsed before the ledger is read: every tool call under a bound lease
// reaches this rule, and most of them are not version control.
func denyLeaseScopedVCS(ctx context.Context, deps Dependencies, actingLease, command string) string {
	if actingLease == "" || helpOnlyLine(command, DialectBash) {
		return ""
	}
	cmds, ok := ParseCommands(command)
	if !ok || !slices.ContainsFunc(cmds, func(c hint.Invocation) bool { return workerVCSOp(c) != "" }) {
		return ""
	}
	me, ok := actingLiveLease(ctx, deps, actingLease)
	if !ok {
		return ""
	}
	role := workerRole(me, deps.caller)
	if role == "" {
		return ""
	}
	// A line that creates a repository is located call by call, whatever else it runs:
	// the verbs after the init may belong to the repository it made.
	creates := slices.ContainsFunc(cmds, isInit)
	if !creates {
		for _, c := range cmds {
			if op := workerVCSOp(c); op != "" && !workerLands(c) {
				if cwd, _ := deps.workingDir(); isWorktreeRemove(c) && removesOwnCheckout(command, cwd, me) {
					continue
				}
				if addsCheckout(c) {
					return checkoutAddDenial("`"+command+"`", me, role)
				}
				return workerVCSDenial("`"+command+"`", vcsRefusal{op: op, kind: vcsOpOrchestrated}, me, role)
			}
		}
	}
	matches := workerLands
	if creates {
		matches = func(c hint.Invocation) bool { return workerVCSOp(c) != "" }
	}
	d := effectiveDialect(deps.ShellDialect)
	cwd, _ := deps.workingDir()
	calls, ok := locateCalls(command, d, cwd, matches, false)
	if !ok {
		unparsed := vcsRefusal{op: "commit", kind: vcsOpLands, why: "The line does not parse, so where it commits cannot be read."}
		return workerVCSDenial("`"+command+"`", unparsed, me, role)
	}
	envDir, envWhy := gitDirAssigned(command, d)
	var fresh []string
	for _, c := range calls {
		site, unlocated := shellCommitSite(c)
		if isInit(c.inv) && unlocated == "" {
			unlocated = nonLiteralInit(c)
		}
		if c.inv.Name == "git" && unlocated == "" {
			switch {
			case envWhy != "":
				unlocated = envWhy
			case envDir != "" && filepath.IsAbs(envDir):
				site.gitDir = envDir
			case envDir != "":
				site.gitDir = filepath.Join(site.dir, envDir)
			}
		}
		if inFreshRepo(c.inv, site, unlocated, fresh) {
			continue
		}
		r := workerVCSRefusal(ctx, deps.VCS, me, c.inv, site, unlocated)
		if r.refused() {
			return workerVCSDenial("`"+command+"`", r, me, role)
		}
		if r.kind == vcsOpInit {
			fresh = append(fresh, r.dest)
		}
	}
	return ""
}

// nonLiteralInit says why a shell line's init cannot be placed, "" when every word of it is
// literal. The directory operand and the options that move the repository are read off
// those words, so one the shell expands, or an init reached through a wrapper whose
// arguments arrive as rendered text, could create the repository anywhere.
func nonLiteralInit(c locatedCall) string {
	if c.args == nil {
		return "It runs through a wrapper, so where it commits cannot be read."
	}
	for _, w := range c.args {
		lit, ok := literalArg(w.Parts)
		if !ok || strings.ContainsAny(lit, "*?[{") {
			return "A word of it is not one literal string, so where it commits cannot be read."
		}
	}
	return ""
}

func init() { std.RegisterVCSLeaseGate(vcsCmdRefusal) } //nolint:gochecknoinits // std cannot import guard; every binary that links the guard registers the vcs lease gate

// vcsCmdRefusal is lease-vcs for vcs\cmd, which a Buzz process runs under row in dir with
// env, the environment the child starts with. The process knows no session or subagent, so
// row is graded as a worker: the answer the shell rule gives a caller that names neither.
func vcsCmdRefusal(ctx context.Context, row types.Job, backend string, args []string, dir string, env []string) error {
	inv := hint.Invocation{Name: backend, Args: args}
	site, unlocated := argvSite(inv, dir)
	relocated := false
	if backend == "git" && unlocated == "" {
		site, unlocated, relocated = gitEnvSite(site, env, row.CheckoutRoot)
	}
	if unlocated == "" && !relocated && backend == "git" && !isInit(inv) && actsInPlace(inv) &&
		inOtherRepository(site, row.CheckoutRoot) && workTreeAt(ctx, site.dir, row.CheckoutRoot) {
		return nil
	}
	r := workerVCSRefusal(ctx, types.VCSOptions{}, row, inv, site, unlocated)
	if !r.refused() {
		return nil
	}
	return errors.New(workerVCSDenial(fmt.Sprintf("`vcs\\cmd(%q)`", args), r, row, workerRole(row, job.Caller{})))
}

// gitLocationVars are the environment variables that move git's repository, work tree or
// index off the directory it runs in.
var gitLocationVars = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"}

// gitEnvSite applies env, the environment git starts with, to site. GIT_DIR names the git
// directory as --git-dir does, unless the arguments already named one; any other of
// gitLocationVars leaves the commit unplaced. relocated reports that env set any of them,
// which rules out the scratch-repository exemption: that exemption reads the repository
// off the directory alone. A GIT_INDEX_FILE is the exception when both it and site lie
// outside checkout and its git directory: a scratch index there moves no file of the job's.
func gitEnvSite(site vcsSite, env []string, checkout string) (_ vcsSite, unlocated string, relocated bool) {
	for _, name := range gitLocationVars {
		value, ok := envValue(env, name)
		if !ok {
			continue
		}
		if name == "GIT_INDEX_FILE" && scratchIndex(site.dir, value, checkout) {
			continue
		}
		relocated = true
		switch {
		case name != "GIT_DIR":
			return vcsSite{}, "Its environment sets " + name + ", so where it commits cannot be read.", true
		case site.gitDir != "":
		case value == "" || strings.HasPrefix(value, "~"):
			return vcsSite{}, "The GIT_DIR its environment sets is not one literal path, so where it commits cannot be read.", true
		case filepath.IsAbs(value):
			site.gitDir = value
		default:
			site.gitDir = filepath.Join(site.dir, value)
		}
	}
	return site, "", relocated
}

// scratchIndex reports whether index, a GIT_INDEX_FILE value, names a literal path that,
// like dir, lies outside checkout and outside the git directory its checkouts share.
// False when checkout is unset or its git directory cannot be read.
func scratchIndex(dir, index, checkout string) bool {
	if checkout == "" || index == "" || strings.HasPrefix(index, "~") {
		return false
	}
	if !filepath.IsAbs(index) {
		index = filepath.Join(dir, index)
	}
	if within(dir, checkout) || within(index, checkout) {
		return false
	}
	_, common, ok := gitCheckout(checkout)
	return ok && !within(index, common)
}

// envValue is name's value in env as exec hands it to a child: the last entry wins, and
// Windows matches names without regard to case.
func envValue(env []string, name string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		k, v, ok := strings.Cut(env[i], "=")
		if ok && (k == name || runtime.GOOS == "windows" && strings.EqualFold(k, name)) {
			return v, true
		}
	}
	return "", false
}

// inOtherRepository reports whether a git call at site stands wholly outside checkout, in a
// repository of its own, such as a script's scratch repository. A site inside checkout
// never does, whatever repository it names: a submodule or nested repository there holds
// the job's files. With a git directory named, git takes the current directory as the
// work tree, so that directory must be the git directory's own checkout. False when either
// side cannot be read, so the refusal decides.
func inOtherRepository(site vcsSite, checkout string) bool {
	if checkout == "" || within(site.dir, checkout) {
		return false
	}
	if site.gitDir != "" {
		root, ok := gitDirCheckout(site.gitDir)
		if !ok || !samePath(site.dir, root) {
			return false
		}
	}
	_, common, ok := gitCheckout(site.dir)
	if !ok {
		return false
	}
	_, own, ok := gitCheckout(checkout)
	return ok && !samePath(common, own)
}

// actsInPlace reports whether a git argv acts only on the repository it runs in, so that
// standing in another repository is enough to keep it off the job's. A push writes to a
// remote, which may be the job's repository; inline config can move the work tree
// (core.worktree) or define an alias that runs anything.
func actsInPlace(inv hint.Invocation) bool {
	g := parseGit(inv.Args)
	return g.at >= 0 && g.sub != "push" && !g.configured
}

// workTreeAt reports whether git, run in dir, takes dir's own checkout as its work tree
// and that work tree lies outside checkout. git is asked rather than the directory read:
// the repository's config, or one it includes, can set core.worktree to the job's checkout.
func workTreeAt(ctx context.Context, dir, checkout string) bool {
	root, _, ok := gitCheckout(dir)
	if !ok {
		return false
	}
	res, err := vcs.Resolve(ctx, dir, "", types.VCSOptions{Name: "git"})
	if err != nil || res.VCS == nil {
		return false
	}
	top, err := res.VCS.Root(ctx, dir)
	return err == nil && samePath(top, root) && !within(top, checkout)
}

// workerRole says why row, acted under by caller, is a worker rather than the root, ""
// for the root. A row with a parent is some job's descendant; a subagent's session holds
// a worker's lease whatever its row says; and a caller that names no session cannot show
// it is the orchestrator, so it is graded as a worker. Only an identified root session
// holding a parentless row is the root.
func workerRole(row types.Job, caller job.Caller) string {
	switch {
	case row.Parent != "":
		return "a worker under " + row.Parent + " in this workspace's ledger"
	case caller.Agent != "":
		return "held by a subagent, which makes it a worker"
	case !caller.Identified():
		return "held by a caller that names no session, which is graded as a worker"
	}
	return ""
}

// workerVCSDenial words a lease-vcs refusal. what is the refused call as the reader typed
// it.
func workerVCSDenial(what string, r vcsRefusal, row types.Job, role string) string {
	checkout := cmp.Or(row.CheckoutRoot, "the checkout its lease was taken in")
	switch r.kind {
	case vcsOpInit:
		return fmt.Sprintf("magus workspace: a worker creates a repository only outside its checkout, %s.\n"+
			"%s runs `%s`, and lease %s is %s. %s "+
			leaseActorClause("create it"),
			checkout, what, r.op, row.ID, role, r.why)
	case vcsOpLands:
		return landDenial(what, r.op, row, role, "magus workspace: a worker commits only on its own branch in its own checkout, "+checkout+".", r.why)
	}
	return landDenial(what, r.op, row, role,
		"magus workspace: `"+r.op+"` stays with the orchestrator, not a worker; report your worktree path, branch and `git status --short`.",
		"The orchestrator lands the work from there. "+
			"A worker reads the repository, stages, switches or creates a branch and fetches; every other version-control verb stays with the orchestrator.")
}

// landDenial is workerVCSDenial for every call but an init: lead, then why, then what a
// worker may land and where.
func landDenial(what, op string, row types.Job, role, lead, why string) string {
	checkout := cmp.Or(row.CheckoutRoot, "the checkout its lease was taken in")
	return fmt.Sprintf("%s\n%s runs `%s`, and lease %s is %s. %s "+
		"A worker may commit, with any backend, cherry-pick or apply a patch, on its own branch in %s; every other version-control mutation changes the tree the orchestrator integrates from, and a whole-tree revert destroys a sibling's uncommitted work. "+
		leaseActorClause("land it"),
		lead, what, op, row.ID, role, why, checkout)
}

// vcsOpKind is how lease-vcs grades and words a version-control mutation.
type vcsOpKind int

const (
	// vcsOpOrchestrated is a mutation that stays with the orchestrator, refused with no why.
	vcsOpOrchestrated vcsOpKind = iota
	// vcsOpLands is a commit, a git cherry-pick or a git apply (see workerLands), refused
	// with a why unless it lands on the worker's own branch in its own checkout.
	vcsOpLands
	// vcsOpInit creates a repository, refused with a why unless it lies wholly outside the
	// worker's checkout.
	vcsOpInit
)

// vcsRefusal is lease-vcs's answer for one call.
type vcsRefusal struct {
	// op names the mutation as the deny words it, "" for a call that mutates nothing.
	op   string
	kind vcsOpKind
	// why is the reason a call that lands or an init is refused, "" when it is not.
	why string
	// dest is the work tree an init that is not refused creates.
	dest string
}

func (r vcsRefusal) refused() bool {
	return r.op != "" && (r.kind == vcsOpOrchestrated || r.why != "")
}

// vcsSite is where a version-control call acts: the directory it runs in once every
// relocation it names is applied, or, when it names its repository by a git directory,
// that directory.
type vcsSite struct {
	dir    string
	gitDir string
}

// workerVCSRefusal is the one decision behind lease-vcs, shared by the shell rule and
// vcs\cmd so the two cannot disagree: whether a worker lease may run inv at site.
// unlocated is why site could not be read, which refuses a commit, since a commit that
// cannot be placed cannot be shown to be in the worker's own checkout.
func workerVCSRefusal(ctx context.Context, opts types.VCSOptions, row types.Job, inv hint.Invocation, site vcsSite, unlocated string) vcsRefusal {
	op := workerVCSOp(inv)
	switch {
	case op == "":
		return vcsRefusal{}
	case isInit(inv):
		return initRefusal(row, inv, site, unlocated)
	case !workerLands(inv):
		return vcsRefusal{op: op, kind: vcsOpOrchestrated}
	case unlocated != "":
		return vcsRefusal{op: op, kind: vcsOpLands, why: unlocated}
	}
	return vcsRefusal{op: op, kind: vcsOpLands, why: commitRefusal(ctx, opts, row, inv.Name, site)}
}

// workerLands reports a call that lands work on the branch its checkout is on: a commit, a
// git cherry-pick, or a git apply. All three are graded where the commit is, its own
// branch in its own checkout, because a cherry-pick and `git diff | git apply -3 && git
// commit -C` land the same change, and a rule that answered them differently taught
// whichever spelling it let through.
func workerLands(inv hint.Invocation) bool {
	if recordsCommit(inv) {
		return true
	}
	sub, _ := vcsSubcommand(inv)
	return inv.Name == "git" && (sub == "cherry-pick" || sub == "apply")
}

// initArgs returns the words after an init's subcommand and reports whether inv is one:
// git, hg and sl `init`, and `jj git init`.
func initArgs(inv hint.Invocation) (args []string, ok bool) {
	sub, rest := vcsSubcommand(inv)
	switch inv.Name {
	case "git", "hg", "sl":
		return rest, sub == "init"
	case "jj":
		if sub != "git" {
			return nil, false
		}
		nested, nestedRest := vcsSubcommand(hint.Invocation{Name: inv.Name, Args: rest})
		return nestedRest, nested == "init"
	}
	return nil, false
}

func isInit(inv hint.Invocation) bool {
	_, ok := initArgs(inv)
	return ok
}

// initOp names an init as workerVCSDenial words it.
func initOp(inv hint.Invocation) string {
	if inv.Name == "jj" {
		return "jj git init"
	}
	return inv.Name + " init"
}

// initOption describes one option of an init: whether it takes a value, as the next word
// or after `=`, and whether that value is a directory the repository's files land in.
type initOption struct{ valued, dir bool }

// vcsBackend names a version-control program as a command line runs it.
type vcsBackend string

const (
	backendGit vcsBackend = "git"
	backendHg  vcsBackend = "hg"
	backendSl  vcsBackend = "sl"
	backendJJ  vcsBackend = "jj"
)

// initOptions are the options each backend's init takes. One outside the table refuses
// the init: git reads unique prefixes of its long options, so an unlisted spelling may be
// one that relocates the repository.
var initOptions = map[vcsBackend]map[string]initOption{
	backendGit: {
		"-q": {}, "--quiet": {}, "--bare": {}, "--shared": {}, "--template": {valued: true},
		"-b": {valued: true}, "--initial-branch": {valued: true}, "--object-format": {valued: true},
		"--ref-format": {valued: true}, "--separate-git-dir": {valued: true, dir: true},
	},
	backendHg: {"--git": {}, "--no-git": {}},
	backendSl: {"--git": {}, "--no-git": {}},
	backendJJ: {"--colocate": {}, "--no-colocate": {}, "--git-repo": {valued: true, dir: true}},
}

// initOperands reads an init's words into the one directory operand it names, "" for none,
// and the directories its options point the repository's files at. unread is why the words
// cannot be read, worded for the deny; "" when they were.
func initOperands(backend vcsBackend, args []string) (operand string, dirs []string, unread string) {
	var operands []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			operands = append(operands, a)
			continue
		}
		name, value, joined := strings.Cut(a, "=")
		opt, known := initOptions[backend][name]
		if !known {
			return "", nil, "Its option " + name + " is not one init is known to take, so where it creates the repository cannot be read."
		}
		if opt.valued && !joined {
			if i++; i >= len(args) {
				return "", nil, "Its option " + name + " has no value."
			}
			value = args[i]
		}
		if opt.dir {
			dirs = append(dirs, value)
		}
	}
	if len(operands) > 1 {
		return "", nil, "It names more than one directory."
	}
	if len(operands) == 1 {
		operand = operands[0]
	}
	return operand, dirs, ""
}

// initRefusal is lease-vcs's answer for a worker's init: refused with why unless it creates
// its repository wholly outside the lease's checkout, in a directory no checkout of the
// job's repository holds, and then dest is that repository's work tree. unlocated is why
// site could not be read.
//
// Everything that decides where the files land is read: the directory operand, -C, a git
// directory named by --git-dir, GIT_DIR or --separate-git-dir, and jj's --git-repo. One that
// cannot be read refuses the init, as does any that points back into the checkout.
func initRefusal(row types.Job, inv hint.Invocation, site vcsSite, unlocated string) vcsRefusal {
	refuse := func(why string) vcsRefusal { return vcsRefusal{op: initOp(inv), kind: vcsOpInit, why: why} }
	if unlocated != "" {
		return refuse(strings.ReplaceAll(unlocated, "commits", "initializes"))
	}
	if row.CheckoutRoot == "" {
		return refuse("The lease records no checkout_root, so it cannot be shown that the repository lies outside the worker's checkout; `magus job exec` stamps one.")
	}
	if inv.Name == "git" {
		if parseGit(inv.Args).configured {
			return refuse("It sets inline config, which can move where the repository lands.")
		}
	} else {
		if slices.ContainsFunc(inv.Args, func(a string) bool { return relocatesDir(inv.Name, a) }) {
			return refuse("It names another directory to run in, so where it creates the repository cannot be read.")
		}
		if parseVCS(inv.Name, inv.Args).alias != "" {
			return refuse("It defines an alias or loads config, which can change what init does.")
		}
	}
	args, _ := initArgs(inv)
	operand, dirs, unread := initOperands(vcsBackend(inv.Name), args)
	if unread != "" {
		return refuse(unread)
	}
	dest := site.dir
	if operand != "" {
		if strings.HasPrefix(operand, "~") || strings.Contains(operand, "://") {
			return refuse("The directory it names is not one local literal path, so where it creates the repository cannot be read.")
		}
		dest = resolveFrom(site.dir, operand)
	}
	dest, err := filepath.Abs(dest)
	if err != nil {
		return refuse("Where it creates the repository cannot be read.")
	}
	dirs = append(dirs, dest)
	if site.gitDir != "" {
		dirs = append(dirs, site.gitDir)
	}
	for _, d := range dirs {
		if !filepath.IsAbs(d) || strings.HasPrefix(d, "~") {
			return refuse("A directory it names is not an absolute literal path, so where it creates the repository cannot be read.")
		}
		if why := checkoutOverlap(row.CheckoutRoot, d); why != "" {
			return refuse(why)
		}
	}
	return vcsRefusal{op: initOp(inv), kind: vcsOpInit, dest: dest}
}

// relocatesDir reports whether arg is hg, sl or jj's option moving the program off its
// working directory, or an abbreviation hg and sl accept of one.
func relocatesDir(backend, arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	return slices.ContainsFunc(relocatingFlags[backend], func(flag string) bool {
		switch {
		case len(flag) == 2:
			return strings.HasPrefix(arg, flag)
		case backend == "jj":
			return name == flag
		}
		return name == flag || strings.HasPrefix(name, "--") && len(name) > 3 && strings.HasPrefix(flag, name)
	})
}

// checkoutOverlap says why dir may not hold a repository a worker creates: it lies in the
// lease's checkout, holds it, or belongs to a checkout of the same repository, one that
// shares the checkout's common git directory. "" when dir is clear of all three.
func checkoutOverlap(checkout, dir string) (why string) {
	switch {
	case within(dir, checkout):
		return fmt.Sprintf("%s lies in the lease's checkout, %s.", dir, checkout)
	case within(checkout, dir):
		return fmt.Sprintf("%s holds the lease's checkout, %s.", dir, checkout)
	}
	if _, common, ok := gitCheckout(dir); ok {
		if _, own, ok := gitCheckout(checkout); ok && samePath(common, own) {
			return fmt.Sprintf("%s lies in a checkout of the repository the lease works in.", dir)
		}
	}
	return ""
}

// inFreshRepo reports whether a worker may run inv at site because site is inside a
// repository an earlier init on the same line created, listed in fresh. The call must act
// in that repository alone: not push, not name config the worker could point at the
// checkout, and not be a verb that writes to a path its operands name.
func inFreshRepo(inv hint.Invocation, site vcsSite, unlocated string, fresh []string) bool {
	if unlocated != "" || !slices.ContainsFunc(fresh, func(f string) bool {
		return within(site.dir, f) && (site.gitDir == "" || within(site.gitDir, f))
	}) || isPush(inv) || isInit(inv) {
		return false
	}
	if inv.Name != "git" {
		return parseVCS(inv.Name, inv.Args).alias == ""
	}
	g := parseGit(inv.Args)
	switch {
	case g.configured:
		return false
	case slices.Contains([]string{"clone", "worktree", "archive", "format-patch", "bundle", "submodule", "checkout-index"}, g.sub):
		return false
	case g.sub == "config":
		return !slices.ContainsFunc(g.rest, func(a string) bool {
			a = strings.ToLower(a)
			return strings.Contains(a, "worktree") || strings.HasPrefix(a, "--file") || strings.HasPrefix(a, "--blob") ||
				a == "--global" || a == "--system" || a == "-f"
		})
	}
	return !slices.ContainsFunc(g.rest, func(a string) bool { return strings.HasPrefix(a, "--output") })
}

// commitRefusal says why a worker's commit at site is refused, "" when it is in the
// lease's own checkout, a secondary one, on a named branch other than the base.
func commitRefusal(ctx context.Context, opts types.VCSOptions, row types.Job, backend string, site vcsSite) string {
	if row.CheckoutRoot == "" {
		return "The lease records no checkout_root, so no checkout is the worker's own; `magus job exec` stamps one."
	}
	root, ok := siteCheckout(ctx, opts, backend, site)
	if !ok {
		return "Which checkout it commits in cannot be read."
	}
	if !samePath(root, row.CheckoutRoot) {
		return fmt.Sprintf("It commits in %s, and the lease was taken in %s.", root, row.CheckoutRoot)
	}
	opts.Name = backend
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil || res.VCS == nil {
		return "Version control is disabled or unresolvable for " + root + ", so its branch cannot be read."
	}
	if !res.VCS.IsSecondaryCheckout(root) {
		return root + " is the repository's primary checkout, the one the orchestrator integrates in."
	}
	ref, err := res.VCS.Ref(ctx, root)
	switch {
	case err != nil || ref == "":
		return root + " has no branch checked out; " + switchToJobBranch(backend, row.ID)
	case isBaseBranch(ref, res.Base):
		return fmt.Sprintf("%s is on %s, the base branch.", root, ref)
	}
	return ""
}

// isBaseBranch reports whether ref names the branch base tracks: base itself, or its last
// segment for a remote-tracking base such as origin/main.
func isBaseBranch(ref, base string) bool {
	return ref == base || ref == path.Base(base)
}

// switchToJobBranch is the line that puts a detached checkout on the job's branch, named
// after the job as the orchestrator names it, or creates that branch when it does not exist.
// A detached HEAD is never a place to commit: the orchestrator lands a branch, and a commit
// on no branch is one it cannot name.
func switchToJobBranch(backend, job string) string {
	if backend != "git" {
		return "check out a branch named " + job + " first."
	}
	return fmt.Sprintf("run `git switch %s`, or `git switch -c %s` when no such branch exists.", job, job)
}

// siteCheckout resolves site to the root of the checkout it stands in.
func siteCheckout(ctx context.Context, opts types.VCSOptions, backend string, site vcsSite) (string, bool) {
	if site.gitDir != "" {
		return gitDirCheckout(site.gitDir)
	}
	if backend == "git" {
		root, _, ok := gitCheckout(site.dir)
		return root, ok
	}
	opts.Name = backend
	res, err := vcs.Resolve(ctx, site.dir, "", opts)
	if err != nil || res.VCS == nil {
		return "", false
	}
	root, err := res.VCS.Root(ctx, site.dir)
	return root, err == nil && root != ""
}

// gitDirCheckout is the checkout a git directory belongs to: the parent of a `.git`
// directory, or the checkout a linked worktree's administrative directory names in its
// gitdir file. False for any other directory, such as a bare repository, which has no
// checkout to be the worker's.
func gitDirCheckout(gitDir string) (string, bool) {
	gitDir = realPath(gitDir)
	if filepath.Base(gitDir) == ".git" {
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			return filepath.Dir(gitDir), true
		}
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(data))
	if !filepath.IsAbs(p) || filepath.Base(p) != ".git" {
		return "", false
	}
	return filepath.Dir(p), true
}

// shellCommitSite places a commit a shell line runs, or says why it cannot be placed.
func shellCommitSite(c locatedCall) (vcsSite, string) {
	switch {
	case c.unfollowed:
		return vcsSite{}, "It runs inside a conditional, a loop or a function, so where it commits cannot be read without running it."
	case !c.at.known:
		return vcsSite{}, "A cd before it does not name one literal directory, so where it commits cannot be read."
	case c.args == nil:
		// Reached through a wrapper, whose arguments arrive only as rendered text: a
		// variable renders empty there, so a relocation cannot be read off them.
		if vcsRelocates(c.inv.Name, c.inv.Args) {
			return vcsSite{}, "It runs through a wrapper and names another repository, so where it commits cannot be read."
		}
		return vcsSite{dir: c.at.dir}, ""
	}
	if c.inv.Name == "git" {
		lits := make([]string, 0, len(c.args))
		for _, w := range c.args {
			lit, ok := literalArg(w.Parts)
			if !ok {
				break
			}
			lits = append(lits, lit)
		}
		if g := parseGit(lits); g.at < 0 {
			return vcsSite{}, "A word git reads before its subcommand is not literal, so where it commits cannot be read."
		}
		return argvSite(hint.Invocation{Name: c.inv.Name, Args: lits}, c.at.dir)
	}
	site, ok := pushFrom(c.inv.Name, c.args, c.at)
	if !ok {
		return vcsSite{}, "The repository it names is not one literal path, so where it commits cannot be read."
	}
	return vcsSite{dir: site.dir}, ""
}

// argvSite places a version-control argv that starts in dir, following git's -C chain and
// --git-dir, and the relocating options of hg, sl and jj. A relocation spelled with `~`,
// left empty, or made by git's --work-tree cannot be placed.
func argvSite(inv hint.Invocation, dir string) (vcsSite, string) {
	move := func(target string) bool {
		if target == "" || strings.HasPrefix(target, "~") {
			return false
		}
		if filepath.IsAbs(target) {
			dir = filepath.Clean(target)
		} else {
			dir = filepath.Join(dir, target)
		}
		return true
	}
	const unplaced = "The directory it names is not one literal path, so where it commits cannot be read."
	if inv.Name != "git" {
		for i := 0; i < len(inv.Args); i++ {
			name, value, joined := strings.Cut(inv.Args[i], "=")
			if !slices.Contains(relocatingFlags[inv.Name], name) {
				continue
			}
			if !joined {
				if i++; i >= len(inv.Args) {
					return vcsSite{}, unplaced
				}
				value = inv.Args[i]
			}
			if !move(value) {
				return vcsSite{}, unplaced
			}
		}
		return vcsSite{dir: dir}, ""
	}
	g := parseGit(inv.Args)
	if g.at < 0 {
		return vcsSite{}, "It names no subcommand."
	}
	gitDir := ""
	for i := 0; i < g.at; i++ {
		name, value, joined := strings.Cut(inv.Args[i], "=")
		if joined && (name == "-C" || name == "-c") {
			// git reads these two only in the separate form.
			continue
		}
		if !joined && gitValuedGlobals[name] && i+1 < g.at {
			i++
			value = inv.Args[i]
		}
		switch name {
		case "-C":
			if value != "" && !move(value) {
				return vcsSite{}, unplaced
			}
		case "--work-tree":
			return vcsSite{}, "It names a work tree with --work-tree, which need not be any checkout's directory."
		case "--git-dir":
			if value == "" || strings.HasPrefix(value, "~") {
				return vcsSite{}, unplaced
			}
			gitDir = value
		}
	}
	if gitDir == "" {
		return vcsSite{dir: dir}, ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return vcsSite{dir: dir, gitDir: gitDir}, ""
}

// gitDirAssigned reads the git directory a line's environment names: a GIT_DIR assignment
// as a command prefix, an export, or an `env` word, as written: a relative one resolves
// where git runs. why says the line names a repository in a way that cannot be placed:
// any other of gitLocationVars, a value that is not literal, or more than one.
func gitDirAssigned(command string, d Dialect) (gitDir, why string) {
	f, err := parseFile(command, d)
	if err != nil {
		return "", ""
	}
	var values []string
	unplaced := false
	note := func(name, value string, literal bool) {
		switch name {
		case "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
			unplaced = true
		case "GIT_DIR":
			values = append(values, value)
			unplaced = unplaced || !literal
		}
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Assign:
			if n.Name == nil {
				return true
			}
			value, literal := "", n.Value == nil
			if n.Value != nil {
				value, literal = literalArg(n.Value.Parts)
			}
			note(n.Name.Value, value, literal)
		case *syntax.CallExpr:
			// env's assignments lead its arguments; the first word without `=` is the command.
			if len(n.Args) == 0 || path.Base(literalWord(n.Args[0].Parts)) != "env" {
				return true
			}
			for _, w := range n.Args[1:] {
				lit, ok := literalArg(w.Parts)
				if ok && strings.HasPrefix(lit, "-") {
					continue
				}
				name, value, found := strings.Cut(lit, "=")
				if !ok || !found {
					break
				}
				note(name, value, true)
			}
		}
		return true
	})
	switch {
	case unplaced || len(values) > 1:
		return "", "The line names its repository through " + strings.Join(gitLocationVars, ", ") + " in a way that cannot be read as one checkout."
	case len(values) == 0:
		return "", ""
	case values[0] == "" || strings.HasPrefix(values[0], "~"):
		return "", "The GIT_DIR it names is not one literal path, so where it commits cannot be read."
	}
	return values[0], ""
}

// isPush reports whether a parsed command publishes: git push, hg push, sl push (with or
// without --to) or jj git push, relocated or not. `gh pr create` counts: it pushes nothing
// itself but opens review on the pushed head, so the push gate grades it at the same commit.
func isPush(c hint.Invocation) bool {
	return strings.HasSuffix(vcsMutation(c), " push") || isPRCreate(c)
}

// isPRCreate reports whether c is `gh pr create`, past gh's own flags.
func isPRCreate(c hint.Invocation) bool {
	if filepath.Base(c.Name) != "gh" {
		return false
	}
	var words []string
	for _, a := range c.Args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	return len(words) >= 2 && words[0] == "pr" && words[1] == "create"
}

// vcsSubcommand splits a VCS invocation at its subcommand, past the global options before it.
func vcsSubcommand(c hint.Invocation) (sub string, rest []string) {
	if c.Name == "git" {
		g := parseGit(c.Args)
		return g.sub, g.rest
	}
	v := parseVCS(c.Name, c.Args)
	return v.sub, v.rest
}

// vcsMutation names the version-control operation a parsed command performs when it is one
// lease-vcs judges, or "" for anything else. Global options before the subcommand (git -C
// <dir>, hg -R <repo>, jj -R <repo>) are skipped so a relocated commit is still a commit.
//
// Every backend's PUSH and COMMIT are read: the push gate keys on the push, and a push it
// cannot see publishes without the person being asked; a worker's commit is placed against
// its checkout whichever backend records it. The other mutations are git's alone: the
// Mercurial, Sapling and Jujutsu arms of the guard grade their destructive verbs in
// nonGitVCSGuard, and a name here that promised more would be a rule nothing enforces.
//
// `git stash list` and `git stash show` read the stash rather than moving work onto
// it, so they pass; every other stash form is a mutation.
func vcsMutation(c hint.Invocation) string {
	sub, rest := vcsSubcommand(c)
	// The word an inline alias defines can run any verb, push included.
	if _, other := vcsGlobals[c.Name]; other {
		if v := parseVCS(c.Name, c.Args); v.alias != "" {
			return c.Name + " " + aliasFlag(v.alias)
		}
	}
	switch c.Name {
	case "hg", "sl":
		switch sub {
		case "push":
			return c.Name + " push"
		case "commit", "ci":
			return c.Name + " commit"
		}
		return ""
	case "jj":
		// `git` is a command group, so its own subcommand is read past jj's global options
		// the same way.
		if nested, _ := vcsSubcommand(hint.Invocation{Name: c.Name, Args: rest}); sub == "git" && nested == "push" {
			return "jj git push"
		}
		switch sub {
		case "commit":
			return "jj commit"
		case "describe", "desc":
			return "jj describe"
		}
		return ""
	case "git":
		// The word an inline alias defines can run any of the verbs below.
		if g := parseGit(c.Args); g.alias != "" {
			return "git -c " + g.alias
		}
	default:
		return ""
	}
	switch sub {
	case "commit", "push", "reset", "clean", "revert", "rebase", "merge", "cherry-pick":
		return "git " + sub
	case "stash":
		if len(rest) > 0 && (rest[0] == "list" || rest[0] == "show") {
			return ""
		}
		return "git stash"
	case "worktree":
		if slices.Contains(rest, "remove") {
			return "git worktree remove"
		}
	case "checkout", "restore":
		if slices.Contains(rest, ".") {
			return "git " + sub + " ."
		}
	}
	return ""
}

// denyWriteOutsideLease refuses a shell line that writes outside the acting lease's
// write paths, in the words the file-write rules would have used for the same file.
//
// A shell line is graded because a host reports no PATH for one: without this, a bound
// worker that redirects, tees, sed -i's or cp's into a sibling's tree passes while the
// identical editor-tool write is denied.
//
// It calls the file-write rules' own grader rather than deciding anything itself, so the two
// cannot drift about who owns a path or how the refusal reads, and the extraction that
// finds the written words is the cache-dir rule's, shared for the same reason.
//
// Only a candidate some live lease DECLARED is graded. The extraction offers every word a
// non-reader command was pointed at, which is the safe direction for a boundary as
// specific as the cache dir but not for one as ordinary as a path: grading every word
// would refuse `echo hi` for writing outside the boundary.
func denyWriteOutsideLease(ctx context.Context, deps Dependencies, actingLease, command string) string {
	if actingLease == "" {
		return ""
	}
	location := hookLocation(ctx, deps)
	if location.workspace == "" {
		return ""
	}
	rows, err := leaseRows(ctx, location)
	if err != nil {
		return ""
	}
	live := liveLeases(rows)
	if len(live) == 0 {
		return ""
	}
	dialect := effectiveDialect(deps.ShellDialect)
	redirects := redirectTargets(command, 0, dialect)
	// A relative word names a path under the directory the call runs in, which for a
	// command run from a subdirectory is not the checkout's root, and the line's cds move it.
	checkout := leaseCheckout(ctx, location, actingLease)
	start, home := cmp.Or(deps.callDir, checkout), userHome()
	for _, candidate := range placedWriteCandidates(command, dialect, start, home) {
		// A flag (`-p`, `-c`) or stdin's `-` names no file; a file spelled that way is
		// written `./-p`, which still resolves.
		if strings.HasPrefix(candidate.word, "-") {
			continue
		}
		target, placed := candidate.place(home)
		if !placed {
			continue
		}
		rel, inside := workspaceRelative(checkout, target)
		if !inside {
			// A checkout nested under this one is another checkout, which the file-write
			// rules refuse a lease outright.
			if nestedCheckout(checkout, target) {
				if g := gradeLeasedWrite(ctx, deps, actingLease, target); g.Decision == decisionDeny {
					return g.Reason
				}
			}
			continue
		}
		declared, byCatchAll := declaredPath(live, rel)
		if !declared {
			continue
		}
		// A catch-all covers every word, `print(1)` included, so under one only a
		// redirect target or a word shaped like a file stands in for a path.
		if byCatchAll && !slices.Contains(redirects, candidate.word) && !pathShaped(checkout, rel) {
			continue
		}
		if g := gradeLeasedWrite(ctx, deps, actingLease, target); g.Decision == decisionDeny {
			return g.Reason
		}
	}
	me, enrolled := liveLease(live, actingLease)
	if !enrolled || len(me.WritePaths) == 0 {
		return ""
	}
	for _, operand := range removedOrMovedOperands(command, 0, dialect, start, home) {
		target, placed := operand.place(home)
		if !placed {
			continue
		}
		rel, inside := workspaceRelative(checkout, target)
		if !inside {
			continue
		}
		if outside := reachOutside(checkout, me, rel); outside != "" {
			what := outside + " is"
			if outside != rel {
				what = rel + " holds " + outside + ", which is"
			}
			return denial{
				Say: fmt.Sprintf("magus workspace: %s outside the paths lease %s was given (%s); "+leaseActorClause("widen these write paths"),
					what, me.ID, strings.Join(me.WritePaths, ", ")),
				Why: fmt.Sprintf("A remove or move writes each path it names, and a directory with everything in it, so every one of them must be inside the write paths lease %s (%s) declared and outside its deny paths. "+
					"`%s` shows what the job holds.\n"+leaseActorWhy,
					me.ID, criteriaLine(me), hint.DescribeJob.With(me.ID)),
			}.full()
		}
	}
	return ""
}

// reachOutside is the first path at or in rel that me may not write: one outside its write
// paths, or inside its deny paths. "" when every path there is me's. Of a directory only
// the subdirectories that lead to a write path are walked, and a pattern that cannot be
// read ends the walk silently, as every uncertainty here does.
func reachOutside(checkout string, me types.Job, rel string) string {
	root := filepath.Join(checkout, filepath.FromSlash(rel))
	// Lstat: a remove of a link to a directory removes the link.
	if info, err := os.Lstat(root); err != nil || !info.IsDir() {
		_, denied, errDeny := declarationCovering(me.DenyPaths, rel)
		_, mine, errMine := declarationCovering(me.WritePaths, rel)
		if errDeny != nil || errMine != nil || mine && !denied {
			return ""
		}
		return rel
	}
	outside := ""
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// WalkDir reports an error only for a directory it could not list.
			return filepath.SkipDir
		}
		under, err := filepath.Rel(root, p)
		if err != nil {
			return filepath.SkipAll
		}
		r := path.Join(rel, filepath.ToSlash(under))
		_, denied, errDeny := declarationCovering(me.DenyPaths, r)
		_, mine, errMine := declarationCovering(me.WritePaths, r)
		switch {
		case errDeny != nil || errMine != nil:
			return filepath.SkipAll
		case denied:
		case mine && d.IsDir() && !holdsDeclaredPath(me.DenyPaths, r):
			return filepath.SkipDir
		case mine:
			return nil
		case d.IsDir() && holdsDeclaredPath(me.WritePaths, r):
			return nil
		}
		outside = r
		return filepath.SkipAll
	})
	return outside
}

// holdsDeclaredPath reports whether the directory dir holds a path one of decls could name: it is an
// ancestor of a declaration's literal part, or a declaration starts with a pattern.
func holdsDeclaredPath(decls []string, dir string) bool {
	for _, raw := range decls {
		file, _ := types.SplitClaim(raw)
		var literal []string
		for _, seg := range strings.Split(path.Clean(file), "/") {
			if strings.ContainsAny(seg, "*?[{") {
				break
			}
			literal = append(literal, seg)
		}
		prefix := strings.Join(literal, "/")
		if prefix == "" || dir == "." || strings.HasPrefix(prefix+"/", dir+"/") {
			return true
		}
	}
	return false
}

// declaredPath reports whether any live lease named rel in a boundary, as a path it owns
// or a path it was refused, and whether only a catch-all (`**`) did. A word no plan
// mentions is not treated as a path at all.
func declaredPath(live []types.Job, rel string) (declared, byCatchAll bool) {
	for _, u := range live {
		for _, decls := range [][]string{u.WritePaths, u.DenyPaths} {
			decl, ok, _ := declarationCovering(decls, rel)
			if !ok {
				continue
			}
			if !catchAll(decl) {
				return true, false
			}
			declared = true
		}
	}
	return declared, declared
}

// catchAll reports a declaration that covers every path in the workspace.
func catchAll(decl string) bool {
	file, _ := types.SplitClaim(decl)
	d := path.Clean(file)
	for {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(d, "/**"), "/*")
		if trimmed == d {
			break
		}
		d = trimmed
	}
	return d == "**" || d == "*"
}

// pathShaped reports a word that reads as a file: one that exists, or one spelled in
// file-name characters with a separator or an extension. `hello`, `600` and `print(1)`
// are not; `notes.txt` and `out/log` are. A new bare name (`touch build`) is missed,
// failing open like every other uncertainty here.
func pathShaped(workspace, rel string) bool {
	if _, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(rel))); err == nil {
		return true
	}
	if !strings.ContainsAny(rel, "./") || strings.Trim(rel, "0123456789.") == "" {
		return false
	}
	return !strings.ContainsFunc(rel, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("._-/~@+,%:", r)
	})
}
