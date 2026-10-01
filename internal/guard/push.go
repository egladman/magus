package guard

import (
	"bufio"
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"mvdan.cc/sh/v3/syntax"
)

// Refusing a push that no green gate covers.
//
// This is the one rule here that is a CONTRACT rather than a nudge, and the distinction
// is the reason it exists. The push advisory it replaces fired on every `git push`
// unconditionally: it never read the run log, so it told a caller who had just gated and a
// caller who had never gated exactly the same thing. A notice that cannot tell those two
// apart is not a reminder, it is a toll, and the measured response to a toll is to stop
// reading it.
//
// What makes a refusal legitimate here is that magus can PROVE the finding. The run log
// records, per invocation, the argv that started it, the per-target results, and the
// version string the binary was built from -- which carries the commit. So "no green `ci`
// has run at this commit" is a fact read off disk, not a suspicion. That is the line the
// doctrine draws: refuse what magus can prove, advise everything else.

// gateCoverage is what the run log says about the gate at one commit.
//
// ONE value, not a set of booleans. The states are mutually exclusive, and the first
// draft encoded them as three flags, which made illegal combinations expressible
// (unknown-and-passed) and immediately produced the bug they invite: the zero value read
// as "no gate ran" when it meant "nothing was asked", and the rule denied every push in a
// fresh clone. A reader also cannot tell from a bool pair which combinations are real.
type gateCoverage int

const (
	// gateUnknown is the question not being ASKABLE: no run log to read, or no commit to
	// match against. The zero value, and it must never deny: it is the state of a fresh
	// clone, a host with no VCS, and every test fixture.
	gateUnknown gateCoverage = iota
	// gateAbsent is a readable run log holding no gate run at this commit.
	gateAbsent
	// gateFailed is a gate run at this commit that FINISHED and did not pass.
	gateFailed
	// gateIncomplete is a gate run at this commit that started and wrote no finished
	// event: still running, or killed. The log cannot tell those apart -- both are simply
	// a missing record -- so this names the superset rather than claiming the one it
	// cannot prove. Asking the project lock would distinguish them, which is machinery
	// this rule does not need: neither is coverage.
	gateIncomplete
	// gatePassed is a gate run at this commit that finished with every target passing.
	gatePassed
)

// gateCoverageAt reads the run log for gate invocations built from commit.
//
// COMMIT, not tree state. An exact tree match is available -- the checkpoint digest would
// give it -- and it is the wrong threshold: it invalidates on the first comment typo after
// a green gate, which is the case the cadence explicitly allows and the case a caller
// would route around the rule to get. A commit with no green gate at all is the failure
// worth refusing, and it is the one people actually ship.
//
// Empty commit, or no run log, reports nothing known: this rule stands down rather than
// refusing on an absence it cannot account for.
func gateCoverageAt(runsDir, commit string) gateCoverage {
	if runsDir == "" || !matchableRevision(commit) {
		return gateUnknown
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return gateUnknown
	}
	out := gateAbsent
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch readGateOutcome(filepath.Join(runsDir, e.Name()), commit) {
		case gatePassed:
			return gatePassed
		// A failure outranks an incomplete run when both are present: it is the more
		// definite thing to tell the caller, and the message differs.
		case gateFailed:
			out = gateFailed
		case gateIncomplete:
			if out == gateAbsent {
				out = gateIncomplete
			}
		}
	}
	return out
}

// gateVerdictAt is the verdict the gate recorded for commit in the repository's session
// store, or gateUnknown when there is none to read.
//
// Consulted before the run log, because the store is keyed by the commit a gate ran AT,
// while a run log names only the commit its binary was BUILT from. A gate run by a binary
// one commit behind HEAD, which is every gate after a commit that was not rebuilt, reads
// as absent in the log while the redundancy check (MGS3010) refuses to run it again: two
// rules each blocking the only way past the other.
func gateVerdictAt(workspace, commit string) gateCoverage {
	if workspace == "" || !matchableRevision(commit) {
		return gateUnknown
	}
	dir, err := sessions.Dir(workspace)
	if err != nil {
		return gateUnknown
	}
	// Gate verdicts only, through the store's per-kind fold cache: a push used to decode
	// every invocation in the store to find one record.
	fold, err := sessions.ReadGateResults(dir)
	if err != nil {
		return gateUnknown
	}
	rec, ok := sessions.GateAt(fold, commit, string(types.TargetCI))
	switch {
	case !ok:
		return gateUnknown
	case rec.Outcome == sessions.OutcomePass:
		return gatePassed
	default:
		return gateFailed
	}
}

// gatePassStatus is the overall outcome a finished gate writes. Journal statuses are
// plain strings on the wire (see journal.Invocation.Status), so this names the one value
// that counts rather than comparing a literal at the call site.
const gatePassStatus = "pass"

// readGateOutcome reports whether one run log is a gate invocation built from commit that
// finished passing, and whether it was a gate invocation at this commit at all.
//
// Folded through journal.InvocationFromEvents rather than by reading the two lifecycle
// events here: that function already knows which event carries the command, the version
// and the status, and a second reading of the same stream is a second thing to keep true.
// It also supplies the interrupted case for free -- a run killed at the terminal writes no
// finished event, so Status stays empty and reads as not-green, which is correct.
//
// The file is bounded before it is parsed. A run log holds every line a build emitted, so
// the biggest here run to megabytes, and this is called once per log on a push.
func readGateOutcome(path, commit string) gateCoverage {
	f, err := os.Open(path)
	if err != nil {
		return gateUnknown
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), gateLogLineMax)
	var events []journal.Event
	for sc.Scan() {
		var ev journal.Event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		// The started event is the first line, so a log that is not this commit's gate
		// is abandoned on line one instead of being read to the end.
		if ev.Kind == journal.KindStarted {
			if ev.Command == nil || !isGateCommand(ev.Command.Arguments) || !builtFrom(ev.MagusVersion, commit) {
				return gateUnknown
			}
		}
		if ev.Kind == journal.KindStarted || ev.Kind == journal.KindFinished {
			events = append(events, ev)
		}
	}
	if len(events) == 0 {
		return gateUnknown
	}
	switch journal.InvocationFromEvents("", events).Status {
	case gatePassStatus:
		return gatePassed
	// No status at all means no finished event: the run was killed, or is still going.
	case "":
		return gateIncomplete
	default:
		return gateFailed
	}
}

// gateLogLineMax bounds one line of a run log. A build's captured output lands here, so a
// line can be long; past this it is not a journal event any reader here understands.
const gateLogLineMax = 4 << 20

// minRevisionPrefix is the shortest abbreviation a revision is matched by. git's describe
// never abbreviates below it, and hg, Sapling and jj abbreviate to 12.
const minRevisionPrefix = 7

// matchableRevision reports whether id is a content hash abbreviation long enough to match
// by prefix: git, hg and Sapling node hashes and jj commit ids are all hex. Anything else
// cannot be matched against a recorded gate, so the rule stands down on it rather than
// matching by accident: an hg local revision number like 42 prefixes every node that starts
// with those digits, and a jj change id is spelled in k-z.
func matchableRevision(id string) bool {
	if len(id) < minRevisionPrefix {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// builtFrom reports whether a magus version string names this commit.
//
// `git describe` renders it as v0.4.3-97-g4a1c0cc84, optionally with -dirty, so the
// commit appears as a g-prefixed abbreviation. Compared by PREFIX in both directions
// because the two sides abbreviate to different lengths: the describe output picks its
// own, and the caller passes whatever `vcs.Commit().short` gave it.
func builtFrom(version, commit string) bool {
	if version == "" || !matchableRevision(commit) {
		return false
	}
	for _, field := range strings.Split(version, "-") {
		abbrev, ok := strings.CutPrefix(field, "g")
		if !ok || !matchableRevision(abbrev) {
			continue
		}
		if strings.HasPrefix(commit, abbrev) || strings.HasPrefix(abbrev, commit) {
			return true
		}
	}
	return false
}

// askUnrendered is appended when the verdict would be ask and the caller did not declare
// that it renders one. Such a hook predates the decision, renders it as nothing, and its
// host reads nothing as allow, so the push is refused instead of published unasked.
const askUnrendered = "This hook predates approval prompts, so it cannot ask the person and the push is refused instead. " +
	"Merge what `magus describe harness` prints or refresh the hook template from the magus docs, then push again; or push from your own terminal."

// gradePushWithoutGate is the verdict for a push at commit when no green gate covers it:
// "ask" for a session no job lease binds, "deny" for one a lease binds, and "" when a gate
// covers the commit or the question could not be asked.
//
// An unbound session is the orchestrator, and publishing work in progress is a call it
// makes with the person. magus cannot tell a deliberate push from an oversight, so the
// host's own approval prompt decides. A marker the agent types is not consent.
//
// A bound session is a worker. Approving its push would publish from a boundary that does
// not own the branch, so nobody is asked.
func gradePushWithoutGate(cover gateCoverage, commit, lease string) (decision, reason string) {
	var state string
	switch cover {
	// Unknown passes. This rule refuses on evidence or not at all: no run log, no VCS, or a
	// host magus cannot read the revision from all mean the question was never asked, and
	// a refusal there is one nobody can clear.
	case gateUnknown, gatePassed:
		return "", ""
	case gateAbsent:
		state = "no `" + string(types.TargetCI) + "` run is recorded at this commit"
	case gateFailed:
		state = "the `" + string(types.TargetCI) + "` run at this commit failed"
	case gateIncomplete:
		state = "the `" + string(types.TargetCI) + "` run at this commit never finished, so it is still going or it was killed"
	}
	head := "pushing " + commit + ", which no passing gate covers: " + state + ".\n"
	gate := "`" + hint.Affected.With(string(types.TargetCI)) + "` runs the gate, and CI runs the identical command on the identical tree, so a red push pays twice for one answer."
	if lease != "" {
		return "deny", head + "This session is bound to job " + lease + ", and workers do not publish: report the commit to the orchestrator that holds the branch.\n" + gate
	}
	return "ask", head + "Approving publishes it as it stands. " + gate
}

// pushCoverage is what the gate record says about the revision the push on command
// publishes, read in the checkout that push runs in, and that revision. gateUnknown when
// the line relocates the push somewhere this cannot name, so the push keeps its advisory:
// refusing it would grade a tree nobody proved it publishes.
func pushCoverage(ctx context.Context, deps Dependencies, hook location, command string, d Dialect, cwd string) (gateCoverage, string) {
	push, located := locatePush(command, d, cwd)
	if !located {
		return gateUnknown, ""
	}
	at := hook
	if push.relocated {
		at = hookLocationAt(deps, push.dir)
	}
	commit := deps.revision(ctx, push.dir, push.rev)
	cover := gateVerdictAt(at.workspace, commit)
	if cover == gateUnknown {
		cover = gateCoverageAt(workspaceRunsDir(at.cacheDir), commit)
	}
	return cover, commit
}

// pushSite is where a push runs and what it publishes.
type pushSite struct {
	// dir is the directory the push runs in: absolute when relocated, otherwise the
	// hook's own, "" for the process's working directory.
	dir       string
	relocated bool
	// rev is the source of the single refspec a git push names, "" for the checkout's
	// current revision.
	rev string
}

// locatePush reads where the first push on command runs, starting from cwd and following
// a cd earlier on the line and the backend's relocation flags (git -C, hg -R, jj -R).
// False when the line pushes nothing this can find, or when the directory is not knowable
// without running anything: a variable, a substitution, a cd whose success decides what
// runs next, or a relocation inside a wrapper's script.
func locatePush(command string, d Dialect, cwd string) (pushSite, bool) {
	site, _, located := locateFirst(command, d, cwd, isPush)
	return site, located
}

// locateCheckout is where the checkout a command rule is handed stands: the first push's,
// or on a line that pushes nothing, the first commit's. A push that is found but cannot be
// located stays unknown rather than falling back, since the push rules read that checkout.
func locateCheckout(command string, d Dialect, cwd string) (pushSite, bool) {
	site, found, located := locateFirst(command, d, cwd, isPush)
	if found {
		return site, located
	}
	site, _, located = locateFirst(command, d, cwd, recordsCommit)
	return pushSite{dir: site.dir, relocated: site.relocated}, located
}

// locateFirst locates the first invocation on command that match accepts. found reports
// one was seen, located that its directory is known without running anything.
func locateFirst(command string, d Dialect, cwd string, match func(hint.Invocation) bool) (site pushSite, found, located bool) {
	calls, ok := locateCalls(command, d, cwd, match, true)
	if !ok || len(calls) == 0 {
		return pushSite{}, false, false
	}
	c := calls[0]
	switch {
	case c.unfollowed:
		return pushSite{}, true, false
	case c.args == nil:
		if !c.at.known || vcsRelocates(c.inv.Name, c.inv.Args) {
			return pushSite{}, true, false
		}
		site = pushSite{dir: c.at.dir, relocated: c.at.moved}
	default:
		if site, ok = pushFrom(c.inv.Name, c.args, c.at); !ok {
			return pushSite{}, true, false
		}
	}
	if site.relocated && !filepath.IsAbs(site.dir) {
		abs, err := filepath.Abs(site.dir)
		if err != nil {
			return pushSite{}, true, false
		}
		site.dir = abs
	}
	return site, true, true
}

// recordsCommit reports a command that records a commit message: git, hg and sl commit
// (hg and sl also spell it ci), and jj commit and describe (describe also spelled desc).
func recordsCommit(c hint.Invocation) bool {
	switch c.Name {
	case "git", "hg", "sl", "jj":
	default:
		return false
	}
	sub, _ := vcsSubcommand(c)
	switch c.Name {
	case "git":
		return sub == "commit"
	case "jj":
		return sub == "commit" || sub == "describe" || sub == "desc"
	}
	return sub == "commit" || sub == "ci"
}

// shellDir is the working directory a shell line has reached at one point in it.
type shellDir struct {
	dir   string
	moved bool
	known bool
}

// locatedCall is one invocation a callWalk matched, and the directory the shell had
// reached when it ran.
type locatedCall struct {
	inv hint.Invocation
	at  shellDir
	// args are the call's words after the program, nil when the invocation was reached
	// through a wrapper, whose arguments arrive only as rendered text.
	args []*syntax.Word
	// unfollowed is set for a call inside a conditional, function or loop other than a
	// literalLoop, which the walk finds but does not locate.
	unfollowed bool
}

// locateCalls returns the invocations on command that match, in the order the shell
// runs them, each with the directory it runs in, starting from cwd. first stops at the
// first match. False when the line does not parse.
func locateCalls(command string, d Dialect, cwd string, match func(hint.Invocation) bool, first bool) ([]locatedCall, bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return nil, false
	}
	w := callWalk{d: d, match: match, first: first, vars: map[string]string{}}
	// A function defined on the line may rebind a loop variable from inside the body.
	syntax.Walk(f, func(n syntax.Node) bool {
		_, fn := n.(*syntax.FuncDecl)
		w.funcs = w.funcs || fn
		return !w.funcs
	})
	w.stmts(f.Stmts, shellDir{dir: cwd, known: true})
	return w.calls, true
}

// callWalk follows a line's statements in the order the shell runs them, carrying the
// working directory, and records each matching invocation.
type callWalk struct {
	d     Dialect
	match func(hint.Invocation) bool
	first bool
	calls []locatedCall
	// vars binds each variable of the literal loops the walk is inside to this
	// iteration's word.
	vars  map[string]string
	funcs bool
}

func (w *callWalk) done() bool { return w.first && len(w.calls) > 0 }

func (w *callWalk) stmts(list []*syntax.Stmt, at shellDir) shellDir {
	for _, s := range list {
		if w.done() {
			break
		}
		at = w.stmt(s, at)
	}
	return at
}

func (w *callWalk) stmt(s *syntax.Stmt, at shellDir) shellDir {
	if s == nil || s.Cmd == nil || w.done() {
		return at
	}
	if s.Background {
		w.cmd(s.Cmd, at)
		return at
	}
	return w.cmd(s.Cmd, at)
}

func (w *callWalk) cmd(c syntax.Command, at shellDir) shellDir {
	switch c := c.(type) {
	case *syntax.CallExpr:
		return w.call(c, at)
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.AndStmt:
			return w.stmt(c.Y, w.stmt(c.X, at))
		case syntax.OrStmt:
			// The right side runs only when the left failed, so a cd on the left leaves it,
			// and everything after, in a directory that depends on that failure.
			if after := w.stmt(c.X, at); after != at {
				at.known = false
			}
			w.stmt(c.Y, at)
			return at
		default:
			// Each side of a pipeline runs in its own subshell.
			w.stmt(c.X, at)
			w.stmt(c.Y, at)
			return at
		}
	case *syntax.Subshell:
		w.stmts(c.Stmts, at)
		return at
	case *syntax.Block:
		return w.stmts(c.Stmts, at)
	case *syntax.ForClause:
		if name, words, ok := literalLoop(c); ok && !w.funcs {
			for _, word := range words {
				if w.done() {
					break
				}
				w.vars[name] = word
				at = w.stmts(c.Do, at)
			}
			// The shell leaves the variable set, but an unbound one only refuses more.
			delete(w.vars, name)
			return at
		}
	}
	// Conditionals, other loops and functions are not followed: a match inside one is found
	// but not located, and a cd inside one leaves the directory unknown.
	syntax.Walk(c, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && !w.done() {
			for _, inv := range peelWrappers(literalWords(w.bound(call.Args)), w.d) {
				switch {
				case w.match(inv):
					w.calls = append(w.calls, locatedCall{inv: inv, at: at, unfollowed: true})
				case changesDir(inv):
					at.known = false
				}
			}
		}
		return !w.done()
	})
	return at
}

func (w *callWalk) call(c *syntax.CallExpr, at shellDir) shellDir {
	args := w.bound(c.Args)
	words := literalWords(args)
	if len(words) == 0 {
		return at
	}
	invs := peelWrappers(words, w.d)
	if len(invs) == 1 && invs[0].Name == path.Base(words[0]) {
		switch inv := invs[0]; {
		case isCdInvocation(inv):
			return cdInto(args[1:], at)
		case changesDir(inv):
			at.known = false
		case w.match(inv):
			w.calls = append(w.calls, locatedCall{inv: inv, at: at, args: args[1:]})
		}
		return at
	}
	for _, inv := range invs {
		switch {
		case changesDir(inv):
			at.known = false
		case w.match(inv):
			w.calls = append(w.calls, locatedCall{inv: inv, at: at})
			if w.done() {
				return at
			}
		}
	}
	return at
}

// bound is words with each plain $NAME or ${NAME} of a bound loop variable replaced by its
// word, so the literal readers downstream see the value this iteration passes.
func (w *callWalk) bound(words []*syntax.Word) []*syntax.Word {
	if len(w.vars) == 0 {
		return words
	}
	out := make([]*syntax.Word, len(words))
	for i, word := range words {
		out[i] = &syntax.Word{Parts: w.boundParts(word.Parts)}
	}
	return out
}

func (w *callWalk) boundParts(parts []syntax.WordPart) []syntax.WordPart {
	out := make([]syntax.WordPart, len(parts))
	for i, part := range parts {
		out[i] = part
		switch p := part.(type) {
		case *syntax.ParamExp:
			if !plainParam(p) {
				continue
			}
			if v, ok := w.vars[p.Param.Value]; ok {
				out[i] = &syntax.Lit{Value: v}
			}
		case *syntax.DblQuoted:
			out[i] = &syntax.DblQuoted{Dollar: p.Dollar, Parts: w.boundParts(p.Parts)}
		}
	}
	return out
}

// plainParam reports $NAME or ${NAME} with no operator, whose value is the variable's own.
func plainParam(p *syntax.ParamExp) bool {
	return p.Param != nil && p.Flags == nil && !p.Excl && !p.Length && !p.Width && !p.IsSet &&
		p.NestedParam == nil && p.Index == nil && len(p.Modifiers) == 0 &&
		p.Slice == nil && p.Repl == nil && p.Names == 0 && p.Exp == nil
}

// literalLoop is the variable and words of a `for NAME in WORDS` loop the walk unrolls.
// False unless every word is literal and expands unchanged wherever the body reads it
// unquoted (no glob, brace, tilde, escape or blank), and nothing in the body can rebind
// NAME or skip the rest of an iteration.
func literalLoop(c *syntax.ForClause) (string, []string, bool) {
	iter, ok := c.Loop.(*syntax.WordIter)
	if !ok || c.Select || !iter.InPos.IsValid() {
		return "", nil, false
	}
	name := iter.Name.Value
	words := make([]string, 0, len(iter.Items))
	for _, item := range iter.Items {
		v, ok := literalArg(item.Parts)
		if !ok || v == "" || strings.ContainsAny(v, "*?[]{}~\\ \t\n") {
			return "", nil, false
		}
		words = append(words, v)
	}
	escapes := false
	for _, s := range c.Do {
		syntax.Walk(s, func(n syntax.Node) bool {
			switch n := n.(type) {
			case *syntax.Assign:
				escapes = n.Name != nil && n.Name.Value == name
			case *syntax.WordIter:
				escapes = n.Name.Value == name
			case *syntax.DeclClause, *syntax.LetClause, *syntax.ArithmCmd, *syntax.ArithmExp:
				escapes = true
			case *syntax.CallExpr:
				escapes = rebindsOrLeaves(literalWords(n.Args))
			}
			return !escapes
		})
		if escapes {
			return "", nil, false
		}
	}
	return name, words, true
}

// rebindsOrLeaves reports a builtin that can set a variable by name or cut an iteration
// short, after which the unrolled body no longer matches what the shell runs.
func rebindsOrLeaves(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "break", "continue", "read", "mapfile", "readarray", "getopts", "unset", "eval", "source", ".",
		"export", "declare", "typeset", "local", "readonly", "let":
		return true
	case "printf":
		return slices.ContainsFunc(words[1:], func(a string) bool { return strings.HasPrefix(a, "-v") })
	}
	return false
}

// changesDir reports a builtin that moves the shell's working directory.
func changesDir(c hint.Invocation) bool {
	return isCdInvocation(c) || c.Name == "pushd" || c.Name == "popd"
}

// cdInto is the directory after `cd` with args, unknown unless they name one literal path.
func cdInto(args []*syntax.Word, at shellDir) shellDir {
	var target string
	operands := 0
	for _, w := range args {
		lit, ok := literalArg(w.Parts)
		switch {
		case !ok:
			at.known = false
			return at
		case operands == 0 && (lit == "-L" || lit == "-P" || lit == "--"):
			continue
		}
		target = lit
		operands++
	}
	// No operand is $HOME, `-` is $OLDPWD, and `~` expands from the environment.
	if operands != 1 || target == "" || target == "-" || strings.HasPrefix(target, "~") {
		at.known = false
		return at
	}
	return moveTo(at, target)
}

// moveTo is at after changing into target. An absolute target is known wherever at was.
func moveTo(at shellDir, target string) shellDir {
	if filepath.IsAbs(target) {
		return shellDir{dir: filepath.Clean(target), moved: true, known: true}
	}
	at.dir, at.moved = filepath.Join(at.dir, target), true
	return at
}

// relocatingFlags are, per backend other than git, the options naming the checkout a
// command runs against, accepted anywhere on the line. git's is -C, read by parseGit.
var relocatingFlags = map[string][]string{
	"hg": {"-R", "--repository", "--repo", "--cwd"},
	"sl": {"-R", "--repository", "--repo", "--cwd"},
	"jj": {"-R", "--repository"},
}

// vcsRelocates reports whether rendered args carry a flag moving the program off its
// working directory.
func vcsRelocates(program string, args []string) bool {
	if program == "git" {
		g := parseGit(args)
		return len(g.dirs) > 0 || g.opaque
	}
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(relocatingFlags[program], name) {
			return true
		}
	}
	return false
}

// gitPushFrom is pushFrom for git: where its -C chain leads from at, and the revision the
// refspec names.
func gitPushFrom(args []*syntax.Word, at shellDir) (pushSite, bool) {
	at, g, ok := gitCallAt(args, at)
	if !ok {
		return pushSite{}, false
	}
	return pushSite{dir: at.dir, relocated: at.moved, rev: pushedRev(args[g.at+1:])}, true
}

// gitCallAt is where a git call spelled with args runs, starting from at, and the literal
// prefix of its argv as git reads it. False when a word git reads before its subcommand is
// not literal, since a variable there may be the subcommand itself, or when --git-dir or
// --work-tree name the repository by a path no checkout can be read off.
func gitCallAt(args []*syntax.Word, at shellDir) (shellDir, gitCommand, bool) {
	lits := make([]string, 0, len(args))
	for _, w := range args {
		lit, ok := literalArg(w.Parts)
		if !ok {
			break
		}
		lits = append(lits, lit)
	}
	g := parseGit(lits)
	if g.at < 0 || g.opaque {
		return at, g, false
	}
	for _, dir := range g.dirs {
		if strings.HasPrefix(dir, "~") {
			return at, g, false
		}
		at = moveTo(at, dir)
	}
	return at, g, at.known
}

// pushFrom is where a push spelled with args runs, starting from at, and the revision a
// git push publishes. False when a relocating operand is not one literal path.
func pushFrom(program string, args []*syntax.Word, at shellDir) (pushSite, bool) {
	if program == "git" {
		return gitPushFrom(args, at)
	}
	globals := vcsGlobals[program]
	moving := relocatingFlags[program]
	for i := 0; i < len(args); i++ {
		lit, ok := literalArg(args[i].Parts)
		if !ok {
			continue
		}
		name, value, joined := strings.Cut(lit, "=")
		if !strings.HasPrefix(lit, "-") {
			continue
		}
		if !joined && globals[name] {
			i++
			if i >= len(args) {
				return pushSite{}, false
			}
			value, ok = literalArg(args[i].Parts)
		}
		if !slices.Contains(moving, name) {
			continue
		}
		if !ok || value == "" || strings.HasPrefix(value, "~") {
			return pushSite{}, false
		}
		at = moveTo(at, value)
	}
	if !at.known {
		return pushSite{}, false
	}
	return pushSite{dir: at.dir, relocated: at.moved}, true
}

// pushedRev is the source of the one refspec a `git push` names, or "" for the checkout's
// current revision: no refspec or several, HEAD itself, a deletion, a whole-namespace
// push, or a word that is not literal.
func pushedRev(args []*syntax.Word) string {
	lits := make([]string, 0, len(args))
	for _, w := range args {
		lit, ok := literalArg(w.Parts)
		if !ok {
			return ""
		}
		lits = append(lits, lit)
	}
	for _, flag := range []string{"-d", "--delete", "--all", "--branches", "--mirror", "--tags"} {
		if slices.Contains(lits, flag) {
			return ""
		}
	}
	ops := operands(lits, "o")
	if len(ops) != 2 {
		return ""
	}
	src, _, _ := strings.Cut(strings.TrimPrefix(ops[1], "+"), ":")
	if src == "HEAD" || src == "@" || strings.HasPrefix(src, "-") {
		return ""
	}
	return src
}
