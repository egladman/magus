package guard

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"mvdan.cc/sh/v3/syntax"
)

// A spawn brief that teaches a command the guard denies. The worker copies commands from
// its brief as written, so a denied one is refused in every worker the brief reaches, or
// worse, teaches each of them a workaround. Measured 2026-09-24: 33 briefs seeded 462
// prefixes of a retired MAGUS_NO_WAIT variable the day it was removed.
//
// Only a command the brief presents as one is graded: a fenced shell block, or an inline
// code span. A line that names a command in order to forbid it ("never `git add -A`") is
// passed over, because it teaches the opposite.

// briefFenceRe matches a fenced block and its info string.
var briefFenceRe = regexp.MustCompile("(?ms)^[ \t]*```([A-Za-z]*)[^\n]*\n((?:.*?\n)??)[ \t]*```")

// briefSpanRe matches an inline code span on one line.
var briefSpanRe = regexp.MustCompile("`([^`\n]+)`")

// shellFence reports an info string that marks a block as shell: a shell's name, the sh
// family's tags, or a console transcript. An unlabeled block counts too: that is how most
// briefs write one.
func shellFence(lang string) bool {
	return lang == "" || lang == "console" || shells[lang] || strings.HasPrefix(lang, "sh")
}

// briefNegationRe matches the words that turn a named command into one NOT to run. It is
// read over the whole line a span sits on, or the line that introduces a block.
var briefNegationRe = regexp.MustCompile(`(?i)\b(?:never|not|don't|do not|no longer|avoid|instead of|rather than|deny|denies|denied|refuse[sd]?|forbid(?:den)?|banned|retired|removed|stop|without)\b|n't\b`)

// briefPlaceholderRe matches a `<placeholder>`, which a shell would read as a redirect.
var briefPlaceholderRe = regexp.MustCompile(`<[A-Za-z][^<>\n]*>`)

// briefInput is a spawn's brief and the description it was spawned under.
type briefInput struct {
	text  string
	title string
	// isolated is a spawn whose worker runs in a worktree of its own.
	isolated bool
	// bound is a continuation to a worker already bound to a live row. worker-check-only
	// grades that worker's own runs, so its message is not graded against the row.
	bound bool
	// format is the brief's format and formatFrom where it came from (job.ResolveBriefFormat).
	// Zero is markdown with nothing to name.
	format     types.BriefFormat
	formatFrom string
}

// lazyProducers finds, once and on demand, whether a target regenerates a row's write
// paths. The lookup loads the workspace, so a caller that never needs it pays nothing.
type lazyProducers func() func(target, project string) bool

// noProducers is the lookup for a row whose write paths nothing regenerates.
func noProducers() func(target, project string) bool {
	return func(string, string) bool { return false }
}

// judgeBriefCommand refuses a brief that teaches a denied command, naming the command and
// the rule. Otherwise it passes, and the verdict may carry Context saying why a magus run
// in the brief was not held to a check.
func judgeBriefCommand(deps Dependencies, brief briefInput) ShellVerdict {
	for _, cmd := range briefCommands(brief.text) {
		cmd = withoutBootstrap(cmd)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		v := Evaluate(deps, briefPlaceholderRe.ReplaceAllString(cmd, "X"))
		if v.Deny == "" {
			continue
		}
		first, _, _ := strings.Cut(v.Deny, "\n")
		return ShellVerdict{
			Deny: "this brief teaches `" + elideCommand(cmd) + "`, a command the guard denies [" + v.RuleName() + "]; fix the brief.",
			Why: "The guard's verdict on it: " + first + "\n" +
				"A worker runs a brief's commands as written, so every worker it reaches is refused, or learns a way around the refusal. A line that names it to forbid it (\"never ...\") is not graded.",
			Rule: denyRule{Name: denyRuleBriefCommand, Arg: v.RuleName()},
		}
	}
	if v := judgeBriefPlainCommands(brief); v.Deny != "" {
		return v
	}
	named := string(brief.format) + ", " + brief.formatFrom
	if brief.format == types.BriefFormatBuzz {
		if v := judgeBriefBuzz(context.Background(), brief.text, magusDecls(), named); v.Deny != "" {
			return v
		}
	}
	v := ShellVerdict{}
	if !brief.bound {
		v = judgeBriefOffCheck(deps, brief)
	}
	if brief.formatFrom != "" && brief.formatFrom != job.BriefFormatUnset && v.Deny == "" {
		v.Context = strings.TrimSpace("brief format: " + named + ". " + v.Context)
		if v.Rule.Name == "" {
			v.Rule = denyRule{Name: denyRuleBriefCommand, Arg: "brief-format"}
		}
	}
	return v
}

// denyRuleBriefPlainCommands is a brief for an isolated worker that teaches a compound command.
const denyRuleBriefPlainCommands denyRuleName = "brief-plain-commands"

// judgeBriefPlainCommands refuses a brief for a worker in its own worktree that teaches a
// compound command. A host that isolates a worker refuses the commands it cannot prove stay
// inside the worktree, and a worker copies the style of the commands its brief shows. A
// brief need not say how to write commands: the host's refusal already teaches that. Briefs
// for a shared checkout pass.
func judgeBriefPlainCommands(brief briefInput) ShellVerdict {
	if !brief.isolated {
		return ShellVerdict{}
	}
	why := "A host that isolates a worker in its own worktree refuses any command it cannot prove stays inside it, and a substitution, a loop or an exec is one it cannot; each refusal costs the worker a turn. " +
		"A worker writes its commands the way its brief shows them."
	for _, cmd := range briefCommands(brief.text) {
		if compoundCommand(cmd) {
			return ShellVerdict{
				Deny: "this brief teaches `" + elideCommand(cmd) + "`, which a worker in its own worktree is refused as too complex: split it into plain commands.",
				Why:  why,
				Rule: denyRule{Name: denyRuleBriefPlainCommands},
			}
		}
	}
	return ShellVerdict{}
}

// compoundCommand reports a command line holding a substitution, a loop or an exec: the
// shapes an isolating host cannot follow.
func compoundCommand(cmd string) bool {
	f, err := parseFile(cmd, DialectBash)
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.WhileClause, *syntax.ForClause:
			found = true
		case *syntax.CallExpr:
			if words := literalWords(n.Args); len(words) > 0 && words[0] == "exec" {
				found = true
			}
		}
		return !found
	})
	return found
}

// briefJobRe finds the job a brief hands its worker: the lease it exports, the job it takes,
// or the JOB ID line every worker prompt carries.
var briefJobRe = regexp.MustCompile(`(?:magus\.lease=|\bjob exec\s+|(?i:\bjob id:)\s*)([A-Za-z0-9._/:-]+)`)

// briefProseRunRe matches the magus that opens a run named in running text rather than in a
// code span.
var briefProseRunRe = regexp.MustCompile(`(?:^|[^\w./-])(?:\./)?magus\s+`)

// briefTargetPhraseRe matches "run the lint target" and "run `lint` target".
var briefTargetPhraseRe = regexp.MustCompile("(?i)\\brun\\s+(?:the\\s+)?`?([A-Za-z0-9_:-]+)`?\\s+target\\b")

// judgeBriefOffCheck refuses a brief that tells the worker of a live row to run a magus target
// other than that row's check and the targets declaring its write paths, quoting the line.
// The spawn path hands the grader no context, so it reads the store itself.
//
// The rows are the one the brief names and the one the spawn title names (in the
// `<parent>/<role> <job>` form), each held to its own check. A brief that reaches no live row
// passes, since there is no check to hold it to, and so does a run the row allows or a line
// that forbids it; the verdict's Context then says which, so a pass is never silent about why
// a magus run in the brief was not graded. A spawn titled `<parent>/integrate <job>` passes
// too: its holder integrates the checkout, which runs whatever target lands the work.
func judgeBriefOffCheck(deps Dependencies, brief briefInput) ShellVerdict {
	runs := briefRuns(brief.text)
	if !slices.ContainsFunc(runs, func(r briefRun) bool { return !r.negated }) {
		return ShellVerdict{}
	}
	if head, _, ok := strings.Cut(brief.title, " "); ok && path.Base(head) == integrateRole {
		return briefUngraded("the spawn's role is "+integrateRole+", and the checkout's integrator runs any target there", runs)
	}
	ctx := context.Background()
	at := hookLocationAt(deps, deps.scope.root)
	if at.cacheDir == "" {
		return ShellVerdict{}
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return ShellVerdict{}
	}
	ids := briefJobIDs(brief.text)
	var graded []types.Job
	if row, ok := briefRow(ids, rows); ok {
		graded = append(graded, row)
	}
	if row, ok := spawnTitleJob(ctx, at, brief.title); ok && gradableRow(row) && !slices.ContainsFunc(graded, func(g types.Job) bool { return g.ID == row.ID }) {
		graded = append(graded, row)
		ids = append(ids, row.ID)
	}
	if len(graded) == 0 {
		return briefUngraded(whyNoBriefRow(ids, rows), runs)
	}
	workspace := lazyWorkspace(ctx, deps, at.workspace)
	if v := briefHumanOnly(ctx, deps, at.workspace, workspace, graded[0], runs); v.Deny != "" {
		return v
	}
	return gradeBriefRuns(ctx, deps, at, workspace, graded, runs)
}

// gradeBriefRuns holds each of runs to each graded row's check, judging a run where its
// line moves the worker; a worker starts at its checkout's root, wherever the spawn was
// made from.
func gradeBriefRuns(ctx context.Context, deps Dependencies, at location, workspace func() (types.WorkspaceRepository, error), graded []types.Job, runs []briefRun) ShellVerdict {
	at.dir = at.workspace
	workerCtx := context.WithValue(ctx, locationKey{}, at)
	workerDeps := deps
	workerDeps.callDir = at.workspace
	workspaces := map[string]func() (types.WorkspaceRepository, error){at.workspace: workspace}
	var notes []string
	for _, row := range graded {
		judges := map[string]rowJudge{}
		for _, r := range runs {
			placed := placedLocation(workerCtx, workerDeps, r.line)
			judge, ok := judges[placed.workspace]
			if !ok {
				if workspaces[placed.workspace] == nil {
					workspaces[placed.workspace] = lazyWorkspace(ctx, deps, placed.workspace)
				}
				judge = newRowJudge(ctx, deps, row, placed, workspaces[placed.workspace])
				judges[placed.workspace] = judge
			}
			one := []briefRun{r}
			if off, v, found := briefOffCheck(one, judge); found {
				return ShellVerdict{
					Deny: briefOffCheckDeny(row, off, v),
					Why:  briefOffCheckWhy(row, v, deps.DefaultCharms),
					Rule: denyRule{Name: denyRuleBriefCommand, Arg: string(denyRuleWorkerCheckOnly)},
				}
			}
			notes = append(notes, briefPassNotes(one, judge)...)
		}
	}
	return briefPassVerdict(notes)
}

// briefHumanOnly refuses a brief telling row's worker to run a line that reaches a
// human_only target, which human-only would refuse the worker whatever its check.
func briefHumanOnly(ctx context.Context, deps Dependencies, root string, workspace func() (types.WorkspaceRepository, error), row types.Job, runs []briefRun) ShellVerdict {
	for _, r := range runs {
		if r.negated {
			continue
		}
		pairs := humanOnlyReach(ctx, deps, root, workspace, []targetRun{r.run})
		if len(pairs) == 0 {
			continue
		}
		return ShellVerdict{
			Deny: fmt.Sprintf("this brief tells job %s's worker to run `%s`, which a person runs [%s]: fix the brief.\nThe line: %q",
				row.ID, pairs[0].ref(), denyRuleHumanOnly, elideCommand(strings.TrimSpace(r.line))),
			Why:  "The workspace declares it human_only: " + pairs[0].reason + "\nA leased worker is refused it whoever asks.",
			Rule: denyRule{Name: denyRuleBriefCommand, Arg: string(denyRuleHumanOnly)},
		}
	}
	return ShellVerdict{}
}

// gradableRow is a live row that names a check: one a brief's runs can be held to.
func gradableRow(row types.Job) bool {
	return row.State.Live() && len(rowChecks(row)) > 0
}

// whyNoBriefRow says why none of ids is a row to hold a brief to.
func whyNoBriefRow(ids []string, rows []types.Job) string {
	if len(ids) == 0 {
		return "the brief names no job (no `magus.lease=<id>`, `job exec <id>` or `JOB ID:` line) and the spawn description is not `<parent>/<role> <job>`"
	}
	var why []string
	for _, id := range ids {
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
		switch {
		case i < 0:
			why = append(why, "job "+id+" is not in the store")
		case !rows[i].State.Live():
			why = append(why, "job "+id+" is not live")
		default:
			why = append(why, "job "+id+" declares no check")
		}
	}
	return strings.Join(why, "; ")
}

// briefUngraded is the pass for a brief whose magus runs met no row, saying so for each.
func briefUngraded(why string, runs []briefRun) ShellVerdict {
	var notes []string
	for _, r := range runs {
		if !r.negated {
			notes = append(notes, briefRunLabel(r)+" is not graded: no row was held to a check; "+why)
		}
	}
	return briefPassVerdict(notes)
}

func briefRunLabel(r briefRun) string {
	return strconv.Quote(elideCommand(strings.TrimSpace(r.line)))
}

// briefPassNotes explains each of a brief's runs that row's grader passed for a reason a reader
// could take for an oversight: the line forbids it, it is described in running text rather
// than taught, or the target regenerates the row's write paths. A run of the row's own check
// needs no words.
func briefPassNotes(runs []briefRun, judge rowJudge) []string {
	var notes []string
	for _, r := range runs {
		line := briefRunLabel(r)
		if r.negated {
			notes = append(notes, line+" is not graded: the line forbids the run")
			continue
		}
		if strings.ContainsAny(r.run.target, "<>$") {
			continue
		}
		if r.prose {
			notes = append(notes, line+" is not graded: it describes a run in running text, and a brief teaches a command in a code span or a shell block")
			continue
		}
		run, err := r.run.fromDir(judge.root, judge.dir, judge.workspace)
		if err != nil || run.allowed(judge.checks, noProducers) {
			continue
		}
		if run.allowed(judge.checks, judge.produces) {
			notes = append(notes, line+" is allowed: it regenerates job "+judge.row.ID+"'s write paths")
		}
	}
	return notes
}

// briefPassVerdict folds notes into the Context of a verdict that does not refuse. At most
// five are told, since a long brief can carry many runs.
func briefPassVerdict(notes []string) ShellVerdict {
	if len(notes) == 0 {
		return ShellVerdict{}
	}
	notes = slices.Compact(slices.Sorted(slices.Values(notes)))
	if len(notes) > 5 {
		notes = append(notes[:5:5], fmt.Sprintf("and %d more", len(notes)-5))
	}
	return ShellVerdict{
		Context: "worker-check-only passed this brief: " + strings.Join(notes, "; ") + ".",
		Rule:    denyRule{Name: denyRuleBriefCommand, Arg: string(denyRuleWorkerCheckOnly)},
	}
}

// briefOffCheckDeny words the refusal, quoting the line the brief would have the worker follow.
func briefOffCheckDeny(row types.Job, off briefRun, v runVerdict) string {
	line, target := off.line, off.run.target
	switch {
	case v.unchecked != nil:
		return fmt.Sprintf("this brief tells job %s's worker to run `%s`, which could not be judged against its write paths: the workspace did not load [%s].\nThe line: %q",
			row.ID, target, denyRuleWorkerCheckOnly, elideCommand(strings.TrimSpace(line)))
	case v.op:
		return fmt.Sprintf("this brief tells job %s's worker to run `%s`, a spell op under the rw charm whose writes nothing declares [%s]; fix the brief.\nThe line: %q",
			row.ID, target, denyRuleWorkerCheckOnly, elideCommand(strings.TrimSpace(line)))
	case len(v.outside) > 0:
		return fmt.Sprintf("this brief tells job %s's worker to run `%s`, which rewrites files outside its write paths [%s]; fix the brief.\nThe line: %q",
			row.ID, target, denyRuleWorkerCheckOnly, elideCommand(strings.TrimSpace(line)))
	}
	checks := rowChecks(row)
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = "`" + c.String() + "`"
	}
	return fmt.Sprintf("this brief tells job %s's worker to run `%s`, not its check %s [%s]; fix the brief.\nThe line: %q\n"+
		"A worker runs only its row's check and the targets that regenerate its write paths; the checkout's integrator, a subagent spawned `<parent>/integrate <job>`, runs the rest serially, in its own tree, after it integrates the units. A line that names it to forbid it (\"never run ...\") is not graded.",
		row.ID, target, strings.Join(names, " or "), denyRuleWorkerCheckOnly, elideCommand(strings.TrimSpace(line)))
}

// briefJobIDs are the job ids a brief names, in the order it names them.
func briefJobIDs(brief string) []string {
	var ids []string
	for _, m := range briefJobRe.FindAllStringSubmatch(brief, -1) {
		if id := strings.TrimRight(m[1], ".:"); types.ValidJobID(id) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// briefRow is the first live row among ids that names a check: the worker's row a brief
// hands out.
func briefRow(ids []string, rows []types.Job) (types.Job, bool) {
	for _, id := range ids {
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
		if i >= 0 && gradableRow(rows[i]) {
			return rows[i], true
		}
	}
	return types.Job{}, false
}

// briefOffCheck finds the first of a brief's runs that row does not allow its worker, as the
// run and the line that presents it.
//
// Only a run the brief teaches is graded: a shell block's line or a code span. Running text
// describes a run as often as it asks for one ("the guard refused magus run lint"), and no
// parser can tell the two apart, so it is noted and never refused.
//
// Each run is judged by the verdict the worker's own run meets, so a brief is refused
// exactly when its worker would be; v is that verdict.
func briefOffCheck(runs []briefRun, judge rowJudge) (off briefRun, v runVerdict, found bool) {
	for _, r := range runs {
		if r.negated || r.prose || strings.ContainsAny(r.run.target, "<>$") || strings.Contains(r.run.target, "...") {
			continue
		}
		if v := judge.verdict(r.run); v.unchecked != nil || !v.permitted && !readsOwnProjects(v.run, judge.row.WritePaths) || v.op || len(v.outside) > 0 {
			return r, v, true
		}
	}
	return briefRun{}, runVerdict{}, false
}

// briefOffCheckWhy is the rationale for whoever spawns the worker: what to change in the
// brief or the row. A plain off-check run needs none beyond the refusal.
func briefOffCheckWhy(row types.Job, v runVerdict, defaults []string) string {
	switch {
	case v.unchecked != nil:
		return fmt.Sprintf("%v.\nWithout the workspace's declarations nothing says which project the run selects or what it writes, and the worker would be refused it for the same reason.", v.unchecked)
	case v.op:
		return fmt.Sprintf("A spell op run on its own declares nothing it writes, so job %s's write paths cannot hold it.\nTeach `%s` instead.", row.ID, v.opNext(defaults))
	case len(v.outside) > 0:
		return fmt.Sprintf("Under the rw charm `%s` writes `%s`, which job %s's write paths (%s) do not cover.\n"+
			"Narrow the brief's command to `%s`, which reports the same drift without writing it, or widen the row's write paths to cover those globs.",
			v.run.target, strings.Join(v.outside, "`, `"), row.ID, strings.Join(row.WritePaths, ", "), v.run.readOnly(defaults))
	}
	return ""
}

// briefRun is one run a brief presents, with the line presenting it.
type briefRun struct {
	line  string
	run   targetRun
	prose bool
	// negated is a run on a line that forbids it (never, do not, instead of), or in a block
	// introduced by one. It is kept so a pass can say it was not graded, and never refused.
	negated bool
}

// briefRuns are the magus runs a brief presents: each line of a shell block, each inline
// span, each run named in running text, and each "run the X target". A line that negates
// is marked, as briefCommands passes it over.
func briefRuns(brief string) []briefRun {
	var out []briefRun
	fromShell := func(line, text string, negated bool) {
		cmds, ok := ParseCommands(text)
		if !ok {
			return
		}
		for _, c := range cmds {
			if r, ok := parseTargetRun(c); ok {
				out = append(out, briefRun{line: line, run: r, negated: negated})
			}
		}
	}
	lines := strings.Split(brief, "\n")
	for _, m := range briefFenceRe.FindAllStringSubmatchIndex(brief, -1) {
		if !shellFence(strings.ToLower(brief[m[2]:m[3]])) {
			continue
		}
		negated := briefNegationRe.MatchString(lineBefore(lines, strings.Count(brief[:m[0]], "\n")))
		for _, l := range strings.Split(brief[m[4]:m[5]], "\n") {
			l = strings.TrimPrefix(strings.TrimSpace(l), "$ ")
			fromShell(l, l, negated)
		}
	}
	for _, line := range strings.Split(briefFenceRe.ReplaceAllString(brief, ""), "\n") {
		negated := briefNegationRe.MatchString(line)
		for _, m := range briefSpanRe.FindAllStringSubmatch(line, -1) {
			fromShell(line, m[1], negated)
		}
		prose := briefSpanRe.ReplaceAllString(line, " ")
		for _, m := range briefProseRunRe.FindAllStringIndex(prose, -1) {
			if r, ok := proseTargetRun(prose[m[1]:]); ok {
				out = append(out, briefRun{line: line, run: r, prose: true, negated: negated})
			}
		}
		for _, m := range briefTargetPhraseRe.FindAllStringSubmatch(line, -1) {
			out = append(out, briefRun{line: line, run: targetRun{verb: "run", target: m[1]}, prose: true, negated: negated})
		}
	}
	return out
}

// proseTargetRun reads the words after "magus" in running text as a run, keeping at most one
// project and dropping the punctuation the sentence put on the words.
func proseTargetRun(rest string) (targetRun, bool) {
	words := strings.Fields(rest)
	for i, w := range words {
		if w != "." {
			words[i] = strings.TrimRight(w, ".,;:!?)\"'")
		}
	}
	r, ok := parseTargetRun(hint.Invocation{Name: "magus", Args: words})
	if len(r.projects) > 1 {
		r.projects = r.projects[:1]
	}
	return r, ok
}

// briefCommands are the commands a brief presents as ones to run: each shell block whole,
// then each inline span outside the blocks.
func briefCommands(brief string) []string {
	var out []string
	lines := strings.Split(brief, "\n")
	for _, m := range briefFenceRe.FindAllStringSubmatchIndex(brief, -1) {
		lang := strings.ToLower(brief[m[2]:m[3]])
		if !shellFence(lang) {
			continue
		}
		if briefNegationRe.MatchString(lineBefore(lines, strings.Count(brief[:m[0]], "\n"))) {
			continue
		}
		var block []string
		for _, l := range strings.Split(brief[m[4]:m[5]], "\n") {
			block = append(block, strings.TrimPrefix(strings.TrimSpace(l), "$ "))
		}
		out = append(out, strings.Join(block, "\n"))
	}
	for _, line := range strings.Split(briefFenceRe.ReplaceAllString(brief, ""), "\n") {
		if briefNegationRe.MatchString(line) {
			continue
		}
		for _, m := range briefSpanRe.FindAllStringSubmatch(line, -1) {
			// A lone word names a program in prose (`cat`, `grep`); there is no command
			// line in it to teach. An assignment still counts: it is a prefix to copy.
			if f := strings.Fields(m[1]); len(f) == 1 && !strings.Contains(f[0], "=") {
				continue
			}
			out = append(out, m[1])
		}
	}
	return out
}

// withoutBootstrap drops each line that is the bootstrap alone. A brief is judged from the
// spawner's checkout, which has a binary, but the worker runs it in a fresh tree with none,
// where the bootstrap is the one exempt go command. Sharing its line keeps it graded, as it
// is in the worker.
func withoutBootstrap(cmd string) string {
	lines := strings.Split(cmd, "\n")
	kept := lines[:0]
	for _, l := range lines {
		cmds, ok := ParseCommandsDialect(l, DialectBash)
		if ok && len(cmds) == 1 {
			if call, isGo := readGoCall(cmds[0]); isGo && call.chdir == "" && call.bootstrapsMagus() && bootstrapLine(l, DialectBash) {
				continue
			}
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// lineBefore is the last non-blank line above line index i, which is where a block's
// lead-in sentence sits.
func lineBefore(lines []string, i int) string {
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) != "" {
			return lines[j]
		}
	}
	return ""
}
