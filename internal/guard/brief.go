package guard

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
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

// denyBriefCommand refuses a brief that teaches a denied command, naming the command and
// the rule, or returns the zero verdict.
func denyBriefCommand(deps Dependencies, brief string) ShellVerdict {
	for _, cmd := range briefCommands(brief) {
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
			Deny: "Fix the brief: it teaches `" + elideCommand(cmd) + "`, which the guard denies [" + v.RuleName() + "]. " + first + "\n" +
				"A worker runs a brief's commands as written, so every worker it reaches is refused, or learns a way around the refusal. A line that names it to forbid it (\"never ...\") is not graded.",
			Rule: denyRule{Name: denyRuleBriefCommand, Arg: v.RuleName()},
		}
	}
	return denyBriefOffCheck(deps, brief)
}

// briefJobRe finds the job a brief hands its worker: the lease it exports, the job it takes,
// or the JOB ID line every worker prompt carries.
var briefJobRe = regexp.MustCompile(`(?:magus\.lease=|\bjob exec\s+|(?i:\bjob id:)\s*)([A-Za-z0-9._/:-]+)`)

// briefProseRunRe matches the magus that opens a run named in running text rather than in a
// code span.
var briefProseRunRe = regexp.MustCompile(`(?:^|[^\w./-])(?:\./)?magus\s+`)

// briefTargetPhraseRe matches "run the lint target" and "run `lint` target".
var briefTargetPhraseRe = regexp.MustCompile("(?i)\\brun\\s+(?:the\\s+)?`?([A-Za-z0-9_:-]+)`?\\s+target\\b")

// denyBriefOffCheck refuses a brief that tells the worker of a live row to run a magus target
// other than that row's check and the targets declaring its write paths, quoting the line.
// The spawn path hands the grader no context, so it reads the store itself. A brief naming
// no live job passes: there is no check to hold it to.
func denyBriefOffCheck(deps Dependencies, brief string) ShellVerdict {
	ids := briefJobIDs(brief)
	if len(ids) == 0 {
		return ShellVerdict{}
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
	row, ok := briefRow(ids, rows)
	if !ok {
		return ShellVerdict{}
	}
	produces := sync.OnceValue(func() func(target, project string) bool {
		return leaseProducers(ctx, deps, at.workspace, row.WritePaths)
	})
	targets := sync.OnceValue(func() []types.TargetEntry {
		ws, err := deps.inspect(ctx, at.workspace)
		if err != nil || ws == nil {
			return nil
		}
		entries, _ := ws.ListTargets(ctx)
		return entries
	})
	defines := func(target string) bool {
		return slices.ContainsFunc(targets(), func(e types.TargetEntry) bool { return targetName(e.Name) == targetName(target) })
	}
	line, target, found := briefOffCheck(brief, row, at.workspace != "" && ownSourceRoot(at.workspace), produces, defines)
	if !found {
		return ShellVerdict{}
	}
	return ShellVerdict{
		Deny: briefOffCheckDeny(row, line, target),
		Rule: denyRule{Name: denyRuleBriefCommand, Arg: string(denyRuleWorkerCheckOnly)},
	}
}

// briefOffCheckDeny words the refusal, quoting the line the brief would have the worker follow.
func briefOffCheckDeny(row types.Job, line, target string) string {
	checks := rowChecks(row)
	names := make([]string, len(checks))
	for i, c := range checks {
		names[i] = "`" + c.String() + "`"
	}
	return fmt.Sprintf("Fix the brief: it tells job %s's worker to run `%s`, and that row's check is %s [%s]. The line: %q\n"+
		"A worker runs only its row's check and the targets that regenerate its write paths; the orchestrator runs the rest serially, in its own tree, after it integrates the units. A line that names it to forbid it (\"never run ...\") is not graded.",
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

// briefRow is the first live row among ids that names a check and does not own the gate:
// the worker's row a brief hands out.
func briefRow(ids []string, rows []types.Job) (types.Job, bool) {
	for _, id := range ids {
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
		if i >= 0 && rows[i].State.Live() && !LeaseOwnsGate(rows[i]) && len(rowChecks(rows[i])) > 0 {
			return rows[i], true
		}
	}
	return types.Job{}, false
}

// briefOffCheck finds the first run a brief tells row's worker to make that the row does not
// allow, reporting the line that says so and the target it names.
//
// Running text is read more loosely than a code span. Its word after `run` is a target only
// when the workspace defines one by that name ("magus run takes one target" names none),
// and a run of the check's target passes whatever follows it, since no parser can tell a
// project from the next word of the sentence.
func briefOffCheck(brief string, row types.Job, ownSource bool, produces func() func(target, project string) bool, defines func(target string) bool) (line, target string, found bool) {
	checks := rowChecks(row)
	for _, r := range briefRuns(brief) {
		if strings.ContainsAny(r.run.target, "<>$") || (r.prose && !defines(r.run.target)) {
			continue
		}
		if r.prose && slices.ContainsFunc(checks, func(c types.LeaseCheck) bool { return sameTarget(c.Target, r.run.target) }) {
			continue
		}
		if !r.run.allowed(checks, ownSource, produces) {
			return r.line, r.run.target, true
		}
	}
	return "", "", false
}

// briefRun is one run a brief presents, with the line presenting it.
type briefRun struct {
	line  string
	run   targetRun
	prose bool
}

// briefRuns are the magus runs a brief presents as ones to make: each line of a shell block,
// each inline span, each run named in running text, and each "run the X target". A line
// that negates (never, do not, instead of) is passed over, as briefCommands passes it.
func briefRuns(brief string) []briefRun {
	var out []briefRun
	fromShell := func(line, text string) {
		cmds, ok := ParseCommands(text)
		if !ok {
			return
		}
		for _, c := range cmds {
			if r, ok := parseTargetRun(c); ok {
				out = append(out, briefRun{line: line, run: r})
			}
		}
	}
	lines := strings.Split(brief, "\n")
	for _, m := range briefFenceRe.FindAllStringSubmatchIndex(brief, -1) {
		if !shellFence(strings.ToLower(brief[m[2]:m[3]])) || briefNegationRe.MatchString(lineBefore(lines, strings.Count(brief[:m[0]], "\n"))) {
			continue
		}
		for _, l := range strings.Split(brief[m[4]:m[5]], "\n") {
			l = strings.TrimPrefix(strings.TrimSpace(l), "$ ")
			fromShell(l, l)
		}
	}
	for _, line := range strings.Split(briefFenceRe.ReplaceAllString(brief, ""), "\n") {
		if briefNegationRe.MatchString(line) {
			continue
		}
		for _, m := range briefSpanRe.FindAllStringSubmatch(line, -1) {
			fromShell(line, m[1])
		}
		prose := briefSpanRe.ReplaceAllString(line, " ")
		for _, m := range briefProseRunRe.FindAllStringIndex(prose, -1) {
			if r, ok := proseTargetRun(prose[m[1]:]); ok {
				out = append(out, briefRun{line: line, run: r, prose: true})
			}
		}
		for _, m := range briefTargetPhraseRe.FindAllStringSubmatch(line, -1) {
			out = append(out, briefRun{line: line, run: targetRun{verb: "run", target: m[1]}, prose: true})
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
			if call, isGo := readGoCall(cmds[0]); isGo && call.chdir == "" && call.bootstrapsMagus() {
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
