package guard

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
	"mvdan.cc/sh/v3/syntax"
)

// The command surface of `magus shell`: the rules that judge a shell line,
// minus the two large pieces that earned their own files. Tokenizing is in
// internal/guard/parse.go and the git rules are in internal/guard/vcs.go.
//
// Evaluate is a pure function of its inputs (the command line, plus the
// hint translator the caller built), and is tested as one, so a rule that has to read
// live workspace state lives beside its own reader instead (internal/guard/lease.go). The
// path surface is internal/guard/write.go.
//
// HOW LONG A DENY MAY BE: three lines. The first is the replacement command; the rest
// are facts the reader cannot discover by trying again. That is the whole budget.
//
// It is a budget because a refusal is read under interruption, by someone who wanted to
// run something else, and length is what makes it skimmed instead of read. Everything
// these messages used to carry and no longer do was true and still cost more than it
// returned: why magus is better, what the guard also catches, which skill to load, what
// is still allowed. A reader who needs the argument can find the rule; a reader who needs
// the command needs it in the first line. An advisory (Context) may run longer, since
// nothing was blocked and the reader chose to keep going.

// ShellVerdict classifies one shell command line. Deny blocks the call with a
// reason the model sees; Context lets it proceed and injects a reminder.
//
// Kind names an advisory that is held to one firing per session (internal/guard/advisory.go).
// It is empty for the advisories that correct the command in front of the reader, where
// a second firing reports a second mistake rather than repeating a standing fact, and it
// is always empty on a deny: a refusal explains itself every time it refuses, and Judge
// alone decides how briefly (denial.go).
//
// Brief is what Kind ships on a repeat firing, and it is empty for a kind that
// should go quiet instead. It names the command and nothing else, because a repeat
// is read by someone who already declined the full text once.
//
// Rule NAMES the rule, on either arm. It is set on every deny, and an advisory sets it
// when it wants to be nameable WITHOUT being held: naming and holding are separable, and
// conflating them is how enrolling three VCS advisories to give them names quietly turned
// them from every-time into once-per-session. An advisory that sets Kind is named by it
// and needs no Rule; one that sets Rule alone speaks every time and still reports itself.
type ShellVerdict struct {
	Deny    string
	Context string
	Kind    hint.MarkerKind
	Brief   string
	Rule    denyRule
}

// RuleName is the name this verdict reports, preferring the marker kind when it has one:
// a held advisory's kind IS its name, and a rule set beside it would be a second spelling
// of the same thing.
func (v ShellVerdict) advisoryName() string {
	if v.Kind != "" {
		return string(v.Kind)
	}
	return string(v.Rule.Name)
}

// RuleName names the rule that refused, or "" when nothing did. The rule's ARGUMENT is
// not reported: it renders the resolved argv, which is the content a replay path exists
// to keep out of a store.
func (v ShellVerdict) RuleName() string { return string(v.Rule.Name) }

// AdvisoryKind names the advisory that matched, or "" when none did.
func (v ShellVerdict) AdvisoryKind() string { return string(v.Kind) }

// denyRuleName identifies WHICH guard rule refused a command, the deny arm's
// counterpart to hint.MarkerKind. Deny prose is for the reader; the rule is what a
// test compares, so rewording a reason never churns a test.
type denyRuleName string

const (
	denyRuleNotesAuthor       denyRuleName = "notes-author"
	denyRuleAgentSignOff      denyRuleName = "agent-sign-off"
	denyRuleSedInPlace        denyRuleName = "sed-in-place"
	denyRuleBusyWait          denyRuleName = "busy-wait"
	denyRuleProcessPoll       denyRuleName = "process-poll"
	denyRuleCaptureFilter     denyRuleName = "capture-filter"
	denyRuleMergeSideCheckout denyRuleName = "merge-side-checkout"
	denyRuleScriptedRewrite   denyRuleName = "scripted-rewrite"
	denyRuleRawTool           denyRuleName = "raw-tool"
	denyRuleThrowawayCopy     denyRuleName = "throwaway-copy"
	denyRuleSiblingCheckout   denyRuleName = "sibling-checkout"
	denyRuleOutputPipe        denyRuleName = "output-pipe"
	denyRuleOutputRedirect    denyRuleName = "output-redirect"
	denyRuleWholeTree         denyRuleName = "whole-tree"
	denyRuleSharedStash       denyRuleName = "shared-stash"
	denyRuleWorktreeRemove    denyRuleName = "worktree-remove"
	denyRuleStageAll          denyRuleName = "stage-all"
	denyRuleCacheDirWrite     denyRuleName = "cache-dir-write"
	denyRuleCd                denyRuleName = "cd"
	denyRuleSymbolSearch      denyRuleName = "symbol-search"
	denyRuleExitStatusEcho    denyRuleName = "exit-status-echo"
	denyRuleCredentialVerb    denyRuleName = "credential-verb" //nolint:gosec // a rule's name, not a credential

	denyRuleBacktickSubstitution denyRuleName = "backtick-substitution"
	denyRuleFilterWithoutInput   denyRuleName = "filter-without-input"

	denyRuleInterpreterRewrite denyRuleName = "interpreter-rewrite"
	denyRuleUnknownEnv         denyRuleName = "unknown-env"

	// Not shell rules: one fires on a SPAWN and one on a FILE WRITE, and neither reaches a
	// shell parser. They live in this block because denyRuleName is the one namespace every
	// verdict's Rule field is drawn from, and the catalog test reads this block to find what
	// must be documented. See internal/guard/spawn.go and internal/guard/buzz.go.
	denySpawnUnbriefed   denyRuleName = "spawn-unbriefed"
	denyBuzzUnbriefed    denyRuleName = "buzz-unbriefed"
	denyRuleBriefCommand denyRuleName = "brief-command"
	// Upgraded from the push-gate ADVISORY when the run log proves no green gate covers
	// this commit; see internal/guard/push.go.
	denyRulePushUngated denyRuleName = "push-ungated"
	// Both surfaces, like cache-dir-write: a file write and a shell line; see
	// internal/guard/credential.go.
	denyRuleTokenState denyRuleName = "token-state"
)

// denyRule is the rule plus what it fired on, so a rule that renders a verb or a
// resolved argv is still compared exactly rather than by substring.
type denyRule struct {
	Name denyRuleName
	Arg  string // the op, verb, or resolved argv; empty when the rule takes none
}

// cmdPos anchors a pattern to a COMMAND position (line start or just after a
// shell separator), so a pattern cannot match its own name appearing as text.
// `go test` and `git add -A` show up constantly in test data and commit messages.
//
// Deliberately NOT applied to the whole-tree VCS patterns: those deny work that
// cannot be recovered, so a rare false positive there is the safe direction.
//
// A separator preceded by a BACKSLASH is an escape inside a quoted argument, not
// a separator (commonly a grep alternation). RE2 has no lookbehind, so the
// preceding char is consumed by a negated class; `^` keeps the start-of-string
// case.
const cmdPos = `(?:^|[^\\][;&|(]\s*|\s&&\s*|\s\|\|\s*|` + "`" + `)\s*`

// chainedRunRe matches a second `magus run` on the same line.
//
// Targets COMPOSE through ctx.needs, so a chain is usually one invocation that already did
// the whole thing: in this workspace `lint` needs `format` needs `generate`, which makes
// `magus run generate . ; magus run format . ; magus run lint .` three workspace loads to
// produce what the third one produces alone.
//
// It matches the SHAPE rather than parsed commands because the mistake is the chaining, and
// every spelling of it (; && ||) is the same mistake.
// `affected` counts as a second one: the gate runs the whole pipeline over everything the diff
// reaches, so building a project immediately before it is asking for the same work twice. That
// spelling slipped past the first version of this rule, which only looked for `run`, and the
// author of the rule then made exactly that mistake within the hour.
var chainedRunRe = regexp.MustCompile(
	cmdPos + `(?:\./)?magus\s+(?:run|affected)\s[^;&|]*[;&|]+\s*(?:\./)?magus\s+(?:run|affected)\s`)

// toolMatch is one command spell operation Magus can run on the caller's
// behalf. It is derived from the registered spell catalog, never a hand-kept
// list in the guard: adding a spell operation automatically teaches the hook
// which raw command it replaces.
type toolMatch struct {
	spell     string
	operation string
	// rewrites reports that the rendering matched was the rw one, so the verdict can
	// name the charm that makes the write legal instead of routing to a target that
	// would refuse to do it.
	rewrites bool
}

// textFilters are the shell commands whose purpose is to trim, slice, or
// search text. Piping magus into one is always a missing output flag.
//
// `jq` and `magus` are deliberately absent. Both consume a CONTRACT rather than
// scraping a layout (`jq` over `-o json`, and magus-into-magus over `--stdin`),
// which is composition, the opposite of the antipattern. `tee` is absent too: it
// duplicates a stream without trimming it.
var textFilters = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"head": true, "tail": true, "awk": true, "sed": true,
	"cut": true, "sort": true, "uniq": true, "wc": true, "column": true,
}

// magusPipedToFilter reports a magus command whose output is being trimmed by a
// shell text filter. Denied rather than advised: as an advisory it fired
// repeatedly and was read straight past, the same trained-reflex result the
// raw-tool advisory produced.
//
// `magus query output <ref>` is the ONE exemption: it returns a raw captured
// log with no schema for magus to project, so searching it is a real need. Every
// other verb emits a structured record that -o shapes exactly.
// It returns the magus VERB and the FILTER rather than a bool, for the reason
// firstRawToolDenied gives below: a reader told only that a pipe was denied has to work
// out which half offended and what to do instead, and a correction becomes a hunt. The
// verb lets the message name the command in front of them; the filter says what they were
// actually trying to do, which is what decides the answer. `| head` wants less output and
// `| grep` wants fewer rows, and those are different levers.
func magusPipedToFilter(command string, d Dialect) (verb, filter string, ok bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return "", "", false
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		if ok {
			return false
		}
		pipe, isPipe := n.(*syntax.BinaryCmd)
		if !isPipe || pipe.Op != syntax.Pipe {
			return true
		}
		left := lastOfPipeline(pipe.X, d)
		if !trimmableMagus(left) {
			return true
		}
		name, isFilter := firstTextFilter(firstOfPipeline(pipe.Y, d))
		if !isFilter {
			return true
		}
		verb, filter, ok = magusVerb(left), name, true
		return false
	})
	return verb, filter, ok
}

// firstTextFilter names the filter the output was piped into.
func firstTextFilter(cmds []hint.Invocation) (string, bool) {
	for _, c := range cmds {
		if textFilters[c.Name] {
			return c.Name, true
		}
	}
	return "", false
}

// magusVerb is the subcommand path a magus invocation names, at most two words
// ("agent install", "affected ci"), so a message can quote the command the reader ran
// rather than the word "magus".
func magusVerb(cmds []hint.Invocation) string {
	for _, c := range cmds {
		if !strings.HasSuffix(c.Name, "magus") {
			continue
		}
		var words []string
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-") {
				break
			}
			if words = append(words, a); len(words) == 2 {
				break
			}
		}
		return strings.Join(words, " ")
	}
	return ""
}

// magusRedirected reports a magus command whose stdout or stderr is being sent
// to a file, to /dev/null, or folded together with 2>&1.
//
// Denied for the pipe rule's reason. `--silent > /dev/null 2>&1` is the worst
// case: silent mode stays quiet UNTIL something fails, then prints the likely
// diagnostics and the full-log path, and the redirect discards exactly that.
//
// `magus query output <ref>` is exempt, as with the pipe rule. Note --tee is NOT
// the escape hatch a reader might assume (it mirrors STRUCTURED output only),
// so the message points at the persisted log instead.
// It returns the magus VERB and the redirect DESTINATION for the reason the pipe rule
// returns its filter: what the redirect was aiming at is what decides the answer.
// `> /dev/null` wants silence, a named file wants the output kept, and magus has a
// different lever for each.
func magusRedirected(command string, d Dialect) (verb, dest string, ok bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return "", "", false
	}
	syntax.Walk(f, func(n syntax.Node) bool {
		if ok {
			return false
		}
		stmt, isStmt := n.(*syntax.Stmt)
		if !isStmt || len(stmt.Redirs) == 0 || !trimmableMagus(stmtCommands(stmt, d)) {
			return true
		}
		for _, r := range stmt.Redirs {
			// Output redirects only. A HEREDOC or an input redirect feeds magus
			// rather than hiding what it said, so neither is this rule's business.
			if !writesToFile(r.Op) {
				continue
			}
			verb, dest, ok = magusVerb(stmtCommands(stmt, d)), redirectTarget(r), true
			return false
		}
		return true
	})
	return verb, dest, ok
}

// redirectTarget names where the output was being sent, for the message to quote. The
// word is the reader's own, so an unprintable or computed target degrades to the operator
// rather than to a guess.
func redirectTarget(r *syntax.Redirect) string {
	if r.Word == nil {
		return r.Op.String()
	}
	lit := r.Word.Lit()
	if lit == "" {
		return r.Op.String()
	}
	return r.Op.String() + lit
}

// capturePathRe matches the files that hold magus console output verbatim:
// the host's task capture for a backgrounded command (`<id>.output`, whatever
// directory the host keeps it in) and a persisted run log.
//
// It is the same shape the busy-wait rule is pinned against, which polls a
// capture by grepping it.
var capturePathRe = regexp.MustCompile(`(?:^|/)(?:[^/]+\.output|\.magus/logs/[0-9a-f]+\.log)$`)

// captureFilterFires reports a text filter aimed at one of those files.
//
// The capture is magus output one step removed, so the pipe rule's reasoning
// reaches it: the filter drops the output ref and the inspect line that sit two
// lines under the `cause:` an agent greps for. Measured twice in one session,
// with nothing on the line the pipe rule could recognize as magus.
//
// `cat <capture>` alone is not a filter and stays allowed, which is why the
// pipeline arm asks what the SOURCE of the pipe named rather than only what each
// command was pointed at.
func captureFilterFires(cmds []hint.Invocation, command string, d Dialect) bool {
	if slices.ContainsFunc(cmds, func(c hint.Invocation) bool {
		return textFilters[c.Name] && namesCapture(c)
	}) {
		return true
	}
	f, err := parseFile(command, d)
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		pipe, ok := n.(*syntax.BinaryCmd)
		if !ok || pipe.Op != syntax.Pipe {
			return true
		}
		if slices.ContainsFunc(lastOfPipeline(pipe.X, d), namesCapture) && isTextFilter(firstOfPipeline(pipe.Y, d)) {
			found = true
		}
		return true
	})
	return found
}

// namesCapture reports whether a command was pointed at a capture. Every
// argument is checked rather than the operands alone, because each filter spells
// its value-taking flags differently and no flag value looks like this path.
func namesCapture(c hint.Invocation) bool {
	return slices.ContainsFunc(c.Args, capturePathRe.MatchString)
}

// backtickSubstFires reports a backtick command substitution anywhere on the line. Only the
// parse can tell: a backtick inside single quotes or a quoted heredoc is text, and one
// inside double quotes is a command.
func backtickSubstFires(command string, d Dialect) bool {
	f, err := parseFile(command, d)
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if cs, ok := n.(*syntax.CmdSubst); ok && cs.Backquotes {
			found = true
		}
		return !found
	})
	return found
}

// throwawayDirRe matches a path under a temp root, or any path with a scratchpad
// segment: the places a COPY of a workspace gets made rather than checked out.
var throwawayDirRe = regexp.MustCompile(`^(/private)?/(tmp|var/folders)/|/scratchpad(/|$)`)

// assignmentRe recovers a `NAME=value` made earlier on the same line.
var assignmentRe = regexp.MustCompile(`(?:^|[;&|]\s*|\s)([A-Za-z_]\w*)=("?)([^"'\s;&|]+)`)

// magusCdTargets returns the directories a line relocates to before running
// magus, resolving same-line variable assignments: the observed shape chains a
// whole pipeline onto one, so a literal `cd /tmp/...` would be missed.
//
// Empty when the line does not RUN magus, so a rule built on this cannot fire on
// one that merely names a directory.
func magusCdTargets(command string, d Dialect) []string {
	if !mentionsMagusCommand(command, d) {
		return nil
	}
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	vars := map[string]string{}
	for _, m := range assignmentRe.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[3]
	}
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || len(call.Args) < 2 || literalWord(call.Args[0].Parts) != "cd" {
			return true
		}
		out = append(out, expandGuardVars(rawWord(command, call.Args[1]), vars))
		return true
	})
	return out
}

// shellUsesCd reports whether the line runs the cd builtin ahead of a magus command, the
// shape the catalog names: magus is CWD-relative, so a cd there is how the right command
// lands on the wrong project. Parsed commands are preferred so `bash -c 'cd ...'` and a
// subshell `(cd ... && ...)` are seen the same way; the regex is only the
// unparseable-line fallback.
//
// A cd with no magus command AFTER it passes, alone on its line or ahead of ordinary
// work (`cd dir && go test`, `cd dir; ls`): it relocates nothing this rule is about, and
// on a host whose shell persists across calls a bare cd is how a session moves into its
// own checkout.
func shellUsesCd(cmds []hint.Invocation, parsed bool, command string) bool {
	if parsed {
		cdAt := slices.IndexFunc(cmds, isCdInvocation)
		if cdAt < 0 {
			return false
		}
		return slices.ContainsFunc(cmds[cdAt+1:], isMagusInvocation)
	}
	return cdCmdRe.MatchString(command) && magusMentionRe.MatchString(command)
}

func isCdInvocation(c hint.Invocation) bool {
	return c.Name == "cd" || filepath.Base(c.Name) == "cd"
}

// isMagusInvocation reports a command that runs magus. The base name is compared exactly,
// not by suffix, so `./magus` and an absolute path both count while `notmagus` does not.
func isMagusInvocation(c hint.Invocation) bool {
	return filepath.Base(c.Name) == "magus"
}

// rawWord returns a word's SOURCE text, quotes stripped.
//
// The parser renders `$WT` as empty, since its value is unknowable without running
// anything, and that is the one form expandGuardVars can still resolve from a
// same-line assignment. So the raw text is what it needs, not the rendered word.
func rawWord(command string, w *syntax.Word) string {
	start, end := int(w.Pos().Offset()), int(w.End().Offset())
	if start < 0 || end > len(command) || start >= end {
		return ""
	}
	return strings.Trim(command[start:end], `"'`)
}

// magusInThrowawayCopy reports a magus command being run from a COPY of a
// workspace in a temp or scratchpad directory.
//
// Denied because of what it produces: a verdict about a tree nobody will ship. A
// gate that passes in a stale duplicate is worse than no gate. It also splits the
// cache, strands generated files inside the copy, and duplicates spell sources
// (MGS1002).
//
// A genuinely different workspace is `--root <path>`, which keeps one cache.
//
// A temp path announces itself by name, so this rule stays pure. The other
// instance of the same mistake (a sibling checkout of this repository) can only
// be recognized by reading the filesystem, so it lives in internal/guard/checkout.go and
// shares magusCdTargets rather than growing a second cd scanner.
func magusInThrowawayCopy(command string, d Dialect) bool {
	return slices.ContainsFunc(magusCdTargets(command, d), throwawayDirRe.MatchString)
}

// expandGuardVars substitutes $NAME and ${NAME} from assignments made earlier on
// the same line. Anything it cannot resolve is left as written, so an unknown
// variable simply fails to match rather than matching everything.
func expandGuardVars(s string, vars map[string]string) string {
	for name, val := range vars {
		s = strings.ReplaceAll(s, "${"+name+"}", val)
		s = strings.ReplaceAll(s, "$"+name, val)
	}
	return s
}

// mentionsMagusCommand reports whether the line actually RUNS magus, so the
// throwaway-copy rule cannot fire on a line that merely names a temp path.
func mentionsMagusCommand(command string, d Dialect) bool {
	cmds, parsed := ParseCommandsDialect(command, d)
	if !parsed {
		return false
	}
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool { return c.Name == "magus" })
}

// trimmableMagus reports a magus invocation whose output the pipe and redirect
// rules should catch: a structured record with a `-o` shape a text filter has no
// business reaching for.
//
// Two exemptions carry the same reasoning: `magus query output <ref>` (a raw
// captured log with no schema to project) and `magus refs <pattern> --text` (a
// raw grep replacement whose whole purpose is being piped or redirected). Every
// OTHER refs invocation is a symbol lookup that renders a structured record
// `-o` already shapes, so only the --text spelling is let through.
//
// Both checks go through magusInvokes rather than anchoring on Args[0]/[1]: magus
// accepts its global flags before the verb, and a position anchor missed the exemption
// whenever one was there (`magus --root <dir> query output <ref>`).
func trimmableMagus(cmds []hint.Invocation) bool {
	for _, c := range cmds {
		if c.Name != "magus" {
			continue
		}
		one := []hint.Invocation{c}
		if magusInvokes(one, "query", "output") {
			continue
		}
		if magusInvokes(one, "refs") && slices.ContainsFunc(c.Args, isRefsTextFlag) {
			continue
		}
		return true
	}
	return false
}

// isRefsTextFlag matches refs' --text flag, spelled either --text or -text (Go's
// flag package treats a single and a double dash the same), bare or with a value
// (--text=true).
func isRefsTextFlag(a string) bool {
	return a == "-text" || a == "--text" || strings.HasPrefix(a, "-text=") || strings.HasPrefix(a, "--text=")
}

func isTextFilter(cmds []hint.Invocation) bool {
	return slices.ContainsFunc(cmds, func(c hint.Invocation) bool { return textFilters[c.Name] })
}

// firstRawToolDenied parses the line and returns the FIRST command it would run
// that magus already covers. A line that does not parse skips this rule.
//
// It returns the command rather than a bool so the verdict can name it: told only
// that `bash -c '...'` was denied, a reader has to work out which part offended,
// and a denial becomes a wrapper hunt instead of a correction.
func firstRawToolDenied(deps Dependencies, command string) (hint.Invocation, bool) {
	cmds, ok := ParseCommands(command)
	if !ok {
		return hint.Invocation{}, false
	}
	for _, c := range cmds {
		if _, ok := rawToolMatch(deps, c); ok {
			return c, true
		}
	}
	return hint.Invocation{}, false
}

// resolvedCommand renders a parsed command back as argv text: what the shell
// would actually run, wrappers and quoting stripped. It is both what explainDeny
// shows the reader and what the raw-tool rule carries as its Arg, so the two can
// never disagree about which command was judged.
func resolvedCommand(c hint.Invocation) string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// explainDeny prefixes a rule's reason with the resolved command that tripped
// it, and says so explicitly when that differs from what was typed, which is
// the whole point of peeling wrappers, made visible instead of implied.
//
// It does not repeat that re-wrapping will not help: runGuardContext's tail
// already says the guard reads the command being RUN, and this prefix is
// prepended to it.
//
// Both spellings are ELIDED, because the reader wrote the command and is looking at
// it: the prefix identifies which one was judged, it does not quote it back. A line
// carrying a heredoc replayed the whole body here, which made the refusal longer than
// the script that tripped it.
func explainDeny(typed string, c hint.Invocation, reason string) string {
	resolved := resolvedCommand(c)
	var b strings.Builder
	b.WriteString("magus guard denied `" + elideCommand(resolved) + "`")
	if strings.TrimSpace(typed) != resolved {
		b.WriteString(" (what `" + elideCommand(strings.TrimSpace(typed)) + "` resolves to)")
	}
	b.WriteString(".\n\n")
	b.WriteString(reason)
	return b.String()
}

// maxEchoedCommand bounds a command quoted back to its author, in bytes.
const maxEchoedCommand = 120

// elideCommand shortens a command for quoting back, keeping the head (the verb and
// its first arguments, which is what names it) and collapsing the rest to one line.
func elideCommand(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) <= maxEchoedCommand {
		return s
	}
	return strings.TrimSpace(s[:maxEchoedCommand]) + " ..."
}

// rawToolDenied reports whether one resolved command has a registered spell-op
// equivalent. It intentionally allows a tool that Magus does not expose; a
// guard may funnel an available capability, never remove one.
func rawToolDenied(deps Dependencies, c hint.Invocation) bool {
	_, ok := rawToolMatch(deps, c)
	return ok
}

// rawToolMatch finds the operation whose rendered base command matches c. Both the
// ordinary and rw renderings participate, so the verdict can name the charm the caller
// needs. The catalog is read for every process, so a newly registered spell operation
// requires no guard edit.
//
// A rendering that carries a SUBCOMMAND is matched on it, which is what keeps `go env`
// available in a workspace whose spells render `go test`. A rendering that carries none is
// a single-purpose program, and then every spelling of it is the covered one: a read-only
// form (`gofmt -l`, `govulncheck ./...`, `shellcheck`) bypasses the cache, the sandbox and
// the affected set exactly as the rewriting form does, and the exemption those used to
// have is what let a whole tool family run raw.
//
// `--version` still passes. It asks the binary what it is rather than running it over the
// tree, and a guard funnels a capability rather than removing one.
func rawToolMatch(deps Dependencies, c hint.Invocation) (toolMatch, bool) {
	for _, a := range c.Args {
		if a == "--version" || a == "-version" || a == "-V" {
			return toolMatch{}, false
		}
	}
	// Read once: every rendering that reaches the comparison below has already been
	// checked to name this same program, so the invocation reads the same way for all of
	// them. Computed inside the loops it cost a scan per spell, per operation, per charm.
	have := afterGlobalFlags(c.Name, c.Args)
	for _, spell := range deps.spells() {
		for _, operation := range spell.Targets() {
			// Installs are advised, never denied: see installAdvised.
			if op, ok := spell.Op(operation); ok && op.Kind == spells.OpKindInstall {
				continue
			}
			for _, charms := range [][]string{nil, {"rw"}} {
				program, args, ok, err := spell.RenderCommand(operation, charms)
				if err != nil || !ok || program == "" || filepath.Base(program) != c.Name {
					continue
				}
				prefix := commandPrefix(program, args)
				if len(prefix) > 0 && (len(have) < len(prefix) || !slices.Equal(have[:len(prefix)], prefix)) {
					continue
				}
				return toolMatch{spell: spell.Name(), operation: operation, rewrites: len(charms) > 0}, true
			}
		}
	}
	return toolMatch{}, false
}

// installAdvised reports a command that is some spell's declared install run bare, such
// as `pnpm install` or `npm ci`. Every manifest's installs are read, not the install op's
// rendering, which names only the first. An operand after the install's own words names
// packages (`pnpm install <pkg>`), which is a dependency edit, not an install.
func installAdvised(deps Dependencies, c hint.Invocation) bool {
	have := afterGlobalFlags(c.Name, c.Args)
	for _, spell := range deps.spells() {
		for _, man := range spell.Manifests() {
			for _, in := range man.Installs {
				if filepath.Base(in.Command.Bin) != c.Name {
					continue
				}
				prefix := commandPrefix(in.Command.Bin, in.Command.Args)
				if len(prefix) == 0 || len(have) < len(prefix) || !slices.Equal(have[:len(prefix)], prefix) {
					continue
				}
				if !slices.ContainsFunc(have[len(prefix):], func(a string) bool { return !strings.HasPrefix(a, "-") }) {
					return true
				}
			}
		}
	}
	return false
}

// commandPrefix extracts the semantic subcommand from an operation's rendered argv,
// preserving compound verbs (`go mod tidy`, `go tool govulncheck`). It is empty when the
// rendering names no subcommand, which rawToolMatch reads as a single-purpose program.
//
// It consumes a valued global flag's OPERAND as well as its name: leaving the operand in
// place makes it read as the subcommand, and a nil answer here claims the program has NO
// subcommands, which covers every spelling of it.
//
// An unknown flag is SKIPPED here and ends the read on the invocation side. The asymmetry
// is the point: this argv is magus's own rendering, where a misread costs a rule that
// matches nothing, while there it would invent a deny.
func commandPrefix(program string, args []string) []string {
	valued := valuedGlobalFlags[filepath.Base(program)]
	first := 0
	for first < len(args) && strings.HasPrefix(args[first], "-") {
		if valued[args[first]] {
			first++
		}
		first++
	}
	// >=, not ==: an argv ending on a valued flag (["-C"]) advances past the end.
	if first >= len(args) || !subcommandWord(args[first]) {
		return nil
	}
	prefix := []string{args[first]}
	if (args[first] == "mod" || args[first] == "tool") && first+1 < len(args) && subcommandWord(args[first+1]) {
		prefix = append(prefix, args[first+1])
	}
	return prefix
}

// valuedGlobalFlags names, per program, the flags that precede a subcommand and take a
// SEPARATE operand. Per program because the word after one is otherwise indistinguishable
// from the subcommand, and reading it wrong invents a deny rather than missing one.
//
// `npm -w <pkg> test` and its siblings still walk past their denies. The gap is left open
// deliberately: a wrong entry here is worse than a missing one.
var valuedGlobalFlags = map[string]map[string]bool{
	"go": {"-C": true, "--C": true},
}

// afterGlobalFlags drops the options a tool takes BEFORE its subcommand, so a rule matches
// the command rather than one spelling of it: `go -C <dir> test ./...` is `go test ./...`.
// It answers nil when it cannot tell.
//
// An UNKNOWN flag ends the read, because skipping it cannot distinguish a lone flag from
// one whose operand follows: `npx --package nx some-bin` would present `nx` as the
// subcommand and earn a deny nobody invoked. Missing a deny beats inventing one.
//
// A `--flag=value` carries its operand, so nothing follows it, but the value still has to
// be READ: `-C=../sibling` otherwise spells its way around the escape check.
func afterGlobalFlags(program string, args []string) []string {
	valued := valuedGlobalFlags[filepath.Base(program)]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			// go also takes -C as the first flag AFTER its subcommand, into the same
			// directory, so both spellings have to reach one verdict.
			if dir, ok := valuedOperand(valued, args[i+1:]); ok && escapesWorkspace(dir) {
				return nil
			}
			return args[i:]
		}
		if name, value, joined := strings.Cut(arg, "="); joined {
			if valued[name] && escapesWorkspace(value) {
				return nil
			}
			continue
		}
		if !valued[arg] {
			return nil
		}
		// The operand of a valued flag, and the one case where reading it decides more
		// than where to resume: a directory outside this workspace means the invocation
		// is pointed at another tree, which no target here can run for the caller.
		if i+1 >= len(args) || escapesWorkspace(args[i+1]) {
			return nil
		}
		i++
	}
	return nil
}

// valuedOperand reads the operand of a valued flag leading args, joined (`-C=dir`) or
// separate (`-C dir`).
func valuedOperand(valued map[string]bool, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if name, value, joined := strings.Cut(args[0], "="); joined && valued[name] {
		return value, true
	}
	if valued[args[0]] && len(args) > 1 {
		return args[1], true
	}
	return "", false
}

// escapesWorkspace reports a path that names something outside the workspace. Textual: it
// touches no filesystem. Cleaned first because a raw prefix test reads `./..` and
// `a/../..` as in-tree.
func escapesWorkspace(path string) bool {
	clean := filepath.Clean(path)
	return filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// subcommandWord reports an argv word that names a subcommand rather than a path or a
// pattern: a bare word with no separator, glob or extension. `./...`, `.` and
// `scripts/x.sh` are operands a rendering points the tool AT, not verbs it selects.
func subcommandWord(arg string) bool {
	return arg != "" && !strings.HasPrefix(arg, "-") && !strings.ContainsAny(arg, "/*?.")
}

// The guard patterns. [^&|;]* keeps a flag search inside one segment of a
// compound command, so `git reset && tool --hard-mode` does not false-positive.
//
// These remain ONLY as the unparsable-line fallback: gitGuard above is the
// primary path, and it reads an AST instead of the raw text.
var (
	stashRe = regexp.MustCompile(`\bgit\s+stash\b`)
	// Reading a stash is safe; RESTORING one is not, and the parsed rule denies the bare
	// restore forms. Listing pop/apply/drop/branch as safe here left the destructive
	// spellings with no verdict at all on a line that does not parse, which is the one
	// place an over-eager deny is the right answer.
	stashSafeRe = regexp.MustCompile(`\bgit\s+stash\s+(list|show)\b`)
	resetRe     = regexp.MustCompile(`\bgit\s+reset\b[^&|;]*--hard`)
	checkoutRe  = regexp.MustCompile(`\bgit\s+checkout\s+(--\s+)?\.(\s|$)`)
	restoreRe   = regexp.MustCompile(`\bgit\s+restore\b[^&|;]*\s\.(\s|$)`)
	cleanRe     = regexp.MustCompile(`\bgit\s+clean\b[^&|;]*\s-\w*[fdxX]`)
	stageRe     = regexp.MustCompile(`\bgit\s+(commit|add)\b`)
	// `git add -A` / `git add .` / `git add --all` / `git add -u`: stage-everything
	// forms. Split out from stageRe because these DENY; see Evaluate.
	stageAllRe = regexp.MustCompile(cmdPos + `git\s+add\s+(-A\b|--all\b|-u\b|--update\b|\.(\s|$))`)
	// Push, NOT commit. Committing in a half-finished state is ordinary and
	// sometimes necessary; a gate there would fire constantly and be tuned out.
	// Publishing is where the work stops being yours alone, so that is where the
	// reminder earns its place, and it stays an advise, because a push can
	// legitimately carry a work-in-progress branch.
	// Every backend's push, with any global options before the subcommand (git -C <dir>,
	// hg -R <repo>, jj -R <repo>): a relocated push the fallback misses publishes unasked.
	pushRe = regexp.MustCompile(`\b(?:git|hg|sl|jj(?:\s+-\S+(?:\s+[^\s-]\S*)?)*\s+git)(?:\s+-\S+(?:\s+[^\s-]\S*)?)*\s+push\b`)
	// A SCOPED revert: `git checkout -- <paths>` / `git restore <paths>`. The
	// whole-tree forms above already deny; this one is legitimate often enough
	// that it only advises, but it is the shape of the most common wrong reflex
	// an agent has about generated files.
	// `git checkout ... -- <paths>` needs the `--` separator to be a revert at
	// all; without it the argument is a branch (`git checkout main`, `-b foo`),
	// which is not this rule's business. `git restore` targets worktree files by
	// definition, so its bare form counts.
	scopedRevertRe = regexp.MustCompile(`\bgit\s+checkout\b[^&|;]*\s--\s|\bgit\s+restore\b`)
	// Unparseable-line fallback for shellUsesCd. Anchored at a command position
	// so a `cd` inside a commit message or a quoted string does not trip it.
	cdCmdRe = regexp.MustCompile(cmdPos + `cd\b`)
	// The other half of that fallback: shellUsesCd also requires a magus command
	// somewhere on the line, and without a parse tree "somewhere" is all an unparseable
	// line can promise. A path segment ending in "magus" counts, the way isMagusInvocation
	// counts `./magus`.
	magusMentionRe = regexp.MustCompile(cmdPos + `(?:\S*/)?magus\b`)

	// notesWriteRe matches an invocation that would AUTHOR a note. It is the
	// unparsable-line fallback for notesWriteFires below, the way gitGuardFallback is for
	// gitGuard: anchoring the verb to the program misses every global flag in between.
	//
	// The path rule refuses an agent write into a notes store, but it sees file writes
	// only, and these verbs write through magus. It also resolves the SHARED store alone,
	// so `capture`, which defaults to the private one, has no other rule that sees it.
	notesWriteRe = regexp.MustCompile(`\bmagus\s+notes\s+(edit|capture|promote)\b`)

	// agentSignOffRe matches an invocation of either verb this package folds into one
	// rule: minting a read receipt, or closing an attention request.
	//
	// A receipt is a claim that a PERSON read something, and a disposed request is a
	// claim that a PERSON answered it; both are the only fact in their measure that no
	// analysis can supply. An agent able to record either turns the measure into a
	// formality it satisfies on the way past, and it would, because stamping the
	// changeset or clearing its own block is the obvious tidy-up at the end of a task.
	//
	// The guard is the right place precisely because of what it sees: it is wired into
	// agent hosts, so every command reaching it came from an agent by construction. A
	// person at a terminal never meets this rule.
	// The unparsable-line fallback for magusInvokes, as above.
	agentSignOffRe = regexp.MustCompile(`\bmagus\s+diff\b[^&|;]*\s--ack\b|\bmagus\s+session\s+dispose\b`)

	// An IN-PLACE stream edit. Reading with sed is untouched; only -i is refused.
	//
	// The flag is not portable and the two spellings silently destroy each other's work:
	// GNU takes `sed -i 's/x/y/' f`, BSD/macOS reads that same line's `s/x/y/` as the
	// BACKUP SUFFIX and then has no script, while the portable `sed -i '' ...` makes GNU
	// treat `''` as the script and edit nothing. A command that works on the author's
	// machine mangles the file on the next one, and it does it by writing, so the damage is
	// already on disk when it is noticed.
	//
	// Every host driving this guard has a structured editor tool that reads the file,
	// applies an exact replacement, and reports what changed, which is the same operation
	// without the portability trap or the blind write.
	sedInPlaceRe = regexp.MustCompile(`\bsed\b[^|;&]*\s(-[a-zA-Z]*i[a-zA-Z]*\b|--in-place\b)`)

	// A scripted in-place rewrite: an inline interpreter that runs a REGEX SUBSTITUTION
	// and writes the result back. It is the same edit `sed -i` is refused for, reached by
	// a route the sed rule cannot see, and it is how a rename escapes the graph in
	// practice: `sed -i` is denied, so the next thing to hand is a python one-liner.
	//
	// Deliberately narrow. An interpreter that merely WRITES a file is ordinary authoring
	// and must stay available; what is refused is substitute-then-write, because that is
	// the shape that cannot tell a symbol from a word that looks like one. A rewrite of
	// prose or a config value is caught too: the false positive costs one explanation,
	// while the false negative silently rewrote a dependency's identifier.
	scriptedRewriteRe = regexp.MustCompile(`\b(python3?|perl|ruby|node)\b[\s\S]*\b(re\.subn?|str\.replace|\.replace\()[\s\S]*\.write\(|\b(perl|ruby)\s+-[a-zA-Z]*i[a-zA-Z]*\b`)

	// sourceReadRe is the fallback when the argv parse fails: an unbounded dump of a
	// source file. Keep the extension list in sync with sourceExt in parse.go.
	sourceReadRe = regexp.MustCompile(`\b(cat|bat|head|tail|less|more)\b[^|&;]*\.(go|buzz|ts|tsx|js|jsx|rs|py|c|h|cc|cpp|java|kt|swift|rb|cs|zig)\b`)

	// `magus ... && echo "TESTS GREEN"`. The exit status already carries that, which
	// is what an exit status is for; the echo adds a line that is true by
	// construction and tells a reader nothing the command did not.
	echoOnSuccessRe = regexp.MustCompile(`(?:^|[;&|]\s*)(\S*/)?magus\s[^&|;]*&&\s*echo\b`)
	// Read off the raw line, not the parsed command: the wrapper peeling that lets
	// `time go test` be judged as `go test` would erase the very token this rule is
	// about.
	timedMagusRe = regexp.MustCompile(`(?:^|[;&|]\s*)time\s+(\S*/)?magus\s`)
	// `timeout 300 magus run ci .`, read off the raw line for the same reason as the
	// rule above: `timeout` is a peeled wrapper. Narrowed to run and affected, the
	// only two subcommands carrying --timeout: naming it on `magus graph build`
	// would advise a flag that does not exist there.
	timeoutMagusRe = regexp.MustCompile(`(?:^|[;&|]\s*)timeout\s+[^;&|]*?\s(\S*/)?magus\s+(?:run|affected)\b`)

	// A magus invocation whose own output is truncated or filtered by the shell.
	// magus has output flags for this; a pipe throws away the parts the agent
	// then has to guess at. jq is deliberately absent: it composes with -o json
	// rather than fighting it.
)

// Rendered through hint rather than spelled out, so a subcommand rename is a compile
// error here instead of a verdict that names a command nobody can run. That makes these
// vars rather than consts: a Command renders through a method call.
var (
	vcsGuardContext = "magus workspace: classify the dirty tree first with `" + hint.DescribeFile.With("$(git diff --name-only)") + "`, then stage the reviewed paths explicitly: `git add -- <paths>`.\n" +
		"role=output paths are generated: never hand-edit them, and commit them with the source change that moved them. Load the magus-vcs-hygiene skill for the commit checklist if not already loaded."
	// The tail of runGuardContextFor, which supplies the replacements. This half
	// carries only the WHY and the anti-retry line.
	//
	// DENIED, not advised: every tool matched here has an exact magus equivalent,
	// so the deny costs nothing, and an advisory loses to a trained reflex. As an
	// advisory it changed behavior zero times over a long session and left the Go
	// build cache poisoned by uninstrumented raw runs.
	runGuardContext = "A wrapper, a `VAR=value` prefix or `bash -c` reaches the same verdict."
	// Reverting regenerated output is the wrong default. An agent that did not
	// hand-edit a gen/ file concludes it is not "its" change and discards it,
	// but a generate target rewriting its declared outputs is the system working,
	// and those outputs belong in the same commit as the source that moved them.
	// The honest test is whether the SOURCE changed, not whether the agent typed
	// into the output.
	revertGuardContext = "magus workspace: classify before reverting with `" + hint.DescribeFile.With("<paths>") + "`, and do not revert a file just because you did not hand-edit it.\n" +
		"A role=output path moved by a source change is correct: it belongs in the SAME commit as that source, and reverting it is what makes CI fail on drift. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged. That drift is environmental, and worth reporting rather than discarding. Load the magus-vcs-hygiene skill if not already loaded."
	// DENY, not advise. Advise was tuned out: agents kept prefixing `cd <dir> &&`
	// on every shell call, which relocates later commands on the line and re-fires
	// shell chpwd hooks (mise among them) that can fail with an empty command.
	// Magus takes the project as an argument; a host that needs a one-shot
	// directory change has a working_directory / cwd field that does not rewrite
	// the command line. A DIFFERENT workspace is `--root <path>`, not a cd.
	denyCd = "Do not `cd`. The project is an argument, written bare: `" + hint.Run.With("<target>", "libs/foo") + "`. A different workspace is `--root <path>`; `" + hint.Where.With("<name>") + "` resolves a fuzzy name.\n" +
		"Your shell tool has a working_directory (or cwd) field: set that. A `cd` prefix relocates every later command on the line."

	// Shared by every advisory that routes to refs. A not-indexed verdict is the one
	// answer a reader can misread as "absent" and fall back to grep on, so whichever
	// advisory sent them to refs owes them this sentence.
	searchColdIndexRouting = "If refs reports a project not-indexed, that verdict is \"unknown, not absent\": run `" + hint.GraphBuild.String() + "` and ask again rather than falling back to a text match."

	// A precedent hunt is a search for one distinctive name, and it is the search the
	// graph answers best: refs lists verified sites, so the reader lands on working code
	// instead of assembling it from grep hits. Measured over 1,499 sessions: 42% of new
	// files were preceded by one of these, 71% in subagent sessions, where only 12.9%
	// reached for a magus verb at all.
	precedentSearchAdvice = "this workspace has a knowledge graph, and it answers \"what already does this\" directly:\n" +
		"  EVERY VERIFIED SITE, checked against the tree:  " + hint.Refs.With("%s", "--occurrences") + "\n" +
		"  DOES IT EXIST, and what kind of thing is it:  " + hint.Query.With("%s") + "\n" +
		"A text match finds the name. refs finds the USES, generated and cross-language ones included, which is what a precedent hunt is actually asking for. An empty result means it was text rather than a symbol, and grep is right after all. " + searchColdIndexRouting

	// Shared with Cursor's Read synthesis: an unbounded source dump before refs/query.
	sourceReadAdvice = "this workspace has SCIP symbol indexes; an unbounded source read wastes the context they already answered. Before dumping a whole file to find a definition or call site:\n" +
		"  CODE SYMBOL (defined / used where):  `" + hint.Refs.With("<symbol>") + "`  (add `--occurrences` for edit-precise ranges)\n" +
		"  DOMAIN ENTITY (projects, targets, spells, ops):  `" + hint.Query.With("\"<terms>\"") + "`\n" +
		"Then read ONLY the path+line range refs returned. If you already have that range from refs, a bounded read is fine. " + searchColdIndexRouting

	sourceReadBrief = "magus workspace: `" + hint.Refs.With("<sym>") + "` before reading whole source files."

	// `ci` is the one target name magus ENFORCES (docs/recommendations.md), so it is
	// the one literal a shipped verdict may carry; every other target name is
	// workspace vocabulary and routes through discovery.
	pushGuardContext = "magus workspace: run the gate before publishing if you have not since your last change. `" + hint.Affected.With("ci") + "` runs it over every project the diff reaches, including ones you never edited.\n" +
		"Already ran it, or pushing deliberate work-in-progress? Push. Load the magus-run skill if not already loaded."

	denyAgentSignOff = "A read receipt records that a PERSON read a change, and disposing an attention request records that a PERSON answered it. Only a person can record either, so every spelling of both is refused.\n" +
		"Report what is unread instead: `" + hint.Diff.With("--impact") + "` names every changed file carrying no receipt (`" + hint.Diff.With("-o", "json") + "` puts read_state on each one). Say you cannot ack and hand back the unread list.\n" +
		"Waiting on a request instead: say you are waiting on its id and hand it back; `" + hint.SessionDispose.With("<id>") + "` is a person's to run."

	denyCredentialVerb = "Use the token you were given. Minting, printing, rotating or revoking a credential is the person's to do: `" + hint.ConfigMCPConnectorCreate.With("--name", "<client>") + "` mints one holding mcp=write, and they run it for you.\n" +
		"A session that mints a token, or holds a console link's code, holds a grant nobody handed it; the operator token also reaches token management, so whoever reads it can mint any grant."

	denyNotesAuthor = "Use `" + hint.MemoryPut.With("<name>") + "`: the agent-writable store, where every entry cites a ref a later reader can re-run.\n" +
		"Notes are human-authored by design, so every spelling of the write is denied: `capture` files a transcript as a note, `promote` writes into the SHARED store, and both put a person's name on prose they never read.\n" +
		"If it genuinely belongs in the notes, say so and let the person run it."

	// LEADS with the editor tool, because that is the answer for most of what trips this:
	// changing a string, a literal or a few lines in one file. The rename branch is named
	// second and marked as a branch.
	//
	// It used to lead with `refs --occurrences` and the `.Sum` argument, which is advice
	// about renaming a SYMBOL across a tree. Read while replacing a literal in one test
	// file, it names a tool that takes a symbol the reader does not have, and a correct
	// deny whose remediation does not fit is one the reader learns to route around.
	denyScriptedRewrite = "Use your editor tool: it reads the file first and reports what it changed.\n" +
		"Renaming a symbol across the tree instead? `" + hint.Refs.With("<symbol>", "--occurrences") + "` gives verified, column-precise sites to edit; run `" + hint.GraphBuild.String() + "` first if it reports not-indexed, which means unknown rather than absent.\n" +
		"A regex writes before anyone reads a diff, and it cannot tell your `.Sum` from the OTel SDK's. Creating a new file, or writing under a scratch path, is untouched."

	denySedInPlace = "Use your editor tool: it reads the file first and reports what it changed. Whole-tree mechanical edit? `" + hint.Refs.With("<symbol>", "--occurrences") + "` gives column-precise sites.\n" +
		"`sed -i` is also not portable: GNU reads `sed -i 's/x/y/' f` as an edit, macOS reads that script as the BACKUP SUFFIX. Reading with sed is untouched."

	denyBusyWait = "Do not poll for work you started; you are told when it finishes. Start it and do something else.\n" +
		"Past the tool timeout this loop is BACKGROUNDED rather than killed, and keeps polling a condition a failed run never prints.\n" +
		"Waiting on something outside this machine is what your host's monitor surface is for."

	denyProcessPoll = "Use `" + hint.Status.With("--watch=15s") + "`: it reads the project lock continuously (holder PID, command, age).\n" +
		"`pgrep`, `pidof` and `ps` invent an unbounded poll that answers what the lock message already said."

	// LEADS with the replacement, like the pipe and redirect messages it extends,
	// and spells out the block because the reader cannot lose what they can see.
	denyCaptureFilter = "Read that file whole (`cat`, or your editor tool), or give the run a contract up front: `-o jsonl --tee <file>`, then `jq` over that.\n" +
		"A failure prints `cause:` and `output: out<hex>` two lines apart, so `grep cause:` keeps the symptom and drops the ref `" + hint.QueryOutput.With("<ref>") + "` reads the whole log from.\n" +
		"A range print (`sed -n '1,200p'`) is a filter too: it cuts by POSITION. Reading the whole file stays allowed."

	denyBacktickSubstitution = "Write a command substitution as `$(...)`, and put a literal backtick in single quotes, as in grep -n '```' README.md.\n" +
		"Inside double quotes a backtick RUNS a command: it pairs with the next backtick anywhere on the line, and everything between them, file operands and pipes included, becomes that command."

	// Named for what the agent should do instead, not for what it did wrong: the
	// exact safe replacement is the actionable part. `git add -A` is the single command
	// most likely to turn a focused change into an unreviewable one: it sweeps every
	// regenerated output and every unrelated formatting fix a target just wrote into
	// a commit about something else. Measured: one such call put 69 files (a whole
	// regenerated docs site plus five untouched source files) into a commit about
	// four collection methods.
	denyStageAll = "Stage through the workspace: `" + hint.VCSAdd.String() + "` keeps a source change with the outputs it produced and REPORTS anything undeclared instead of sweeping it in; `" + hint.VCSAdd.With("--dry-run") + "` stages nothing.\n" +
		"A hand-picked `git add -- <paths>` is still fine. Targets write declared outputs as they run, so the tree is routinely dirty with files you did not edit."

	// An ADVISORY, not a deny: two genuinely independent targets in one line is real work
	// (`magus run build api ; magus run test docs`), and only the dependency graph knows
	// which case this is. What the guard can see is that the chain is worth questioning.
	adviseChainedRun = "Run the LAST target and let its dependencies pull the rest in. Targets compose through ctx.needs, so a chain is usually ONE invocation: here `lint` needs `format` needs `generate`, and `" + hint.Run.With("lint", ".") + "` alone runs all three in order.\n" +
		"Check what a target already pulls in before chaining: `" + hint.Run.With("<target>", "<project>", "--dry-run") + "` prints the plan without executing it.\n" +
		"`" + hint.Affected.With("ci") + "` counts as one of these: it runs the whole pipeline over everything the diff reaches, so a build immediately before it does that work twice, and the second run can trip MGS4007 on an output the first one left behind.\n" +
		"What it does NOT do is regenerate. A workspace whose default charms the gate strips (`--no-default-charms`) turns the composed `generate` into a drift GATE, so stale outputs fail it rather than being rewritten. Regenerate first, in ONE invocation across every affected project: `" + hint.Affected.With("generate:rw") + "`.\n" +
		"Each extra invocation reloads the workspace and re-evaluates every magusfile. And `" + hint.Run.String() + "` takes one TARGET and many PROJECTS (`" + hint.Run.With("build", "api", "web") + "`), so two targets never belong in one call either."

	// Both messages LEAD with the replacement, per this file's rule: the agent
	// reached for a filter because it wanted one specific thing, so the actionable
	// correction is the flag that returns that thing, not the prohibition.
	// The exit-status fact is the half a reader cannot discover by trying again: the pipe
	// SUCCEEDS, so a failing gate reads as exit 0 and nothing ever says so.
	pipeExitNote      = "A pipe also takes the exit status from the last stage, so a failing magus reads as exit 0."
	throwawayCopyDeny = "Run from the workspace and name the project: `" + hint.Run.With("<target>", "<project>") + "`. A different workspace is `--root <path>`; a pristine tree is a throwaway `git worktree`, not a copy.\n" +
		"A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits."
)

// pipeDeny answers the question the filter was asking, about the command that was run.
//
// The menu this replaced listed -o name, -o json and -o template on every pipe, whatever
// the reader piped or which command they piped it from. It was wrong as often as it was
// right: `magus agent install | head` emits advisory lines with no record to project, so
// every option offered was inapplicable, and a reader who tries one and gets nothing
// learns the advice is noise. Three lines nobody reads are worse than one that lands.
func pipeDeny(verb, filter string) string {
	// The verb is quoted WITHOUT the binary name: a compiled-in verdict that spells
	// `magus run lint` reads as an instruction, and lint/test/build/generate are this
	// repository's target names rather than magus vocabulary, so in most workspaces that
	// instruction names nothing. Quoting the reader's own verb identifies the command
	// without minting a command line to copy.
	lead := "`" + verb + " | " + filter + "`: magus answers this without the pipe.\n"
	if verb == "" {
		lead = "magus answers this without the pipe.\n"
	}
	switch filter {
	case "head", "tail", "less", "more":
		// Fewer LINES. -s is the only lever every command has, because it suppresses
		// progress rather than projecting a record the command may not have.
		return lead + "`-s` stays quiet until something fails, then prints the diagnostics and the log path.\n" + pipeExitNote
	case "wc":
		return lead + "`-o name` prints one id per line, which is what a count of them reads.\n" + pipeExitNote
	case "jq":
		return lead + "`-o json` IS the record, and `-o json --tee <file>` writes it where jq can read it.\n" + pipeExitNote
	case "grep", "egrep", "fgrep", "rg", "ag":
		return lead + "`-o name` for the ids alone, `-o json` for the whole record, `-o template='{{.field}}'` for one field; a bare `-o template` lists the fields this command has.\n" + pipeExitNote
	default:
		return lead + "`-o name`, `-o json`, or `-o template='{{.field}}'` project the record; `-s` silences progress instead.\n" + pipeExitNote
	}
}

// redirectDeny answers what the redirect was for, about the command that was run.
//
// There is no legitimate shape of this against magus, which is what makes one tailored
// answer possible where the pipe rule needed several. Silencing and keeping are the only
// two intents, magus has a lever for each, and both leave the full log on disk either way.
func redirectDeny(verb, dest string) string {
	lead := "`" + verb + " " + dest + "`: "
	if verb == "" {
		lead = "redirecting magus output: "
	}
	answer := "`-o json --tee <file>` keeps the STRUCTURED output, never console text, which is not a format anything should parse."
	if strings.HasSuffix(dest, "/dev/null") {
		answer = "`--silent` says nothing until something fails, then prints the diagnostics this would have discarded."
	}
	return lead + answer + mintedLogNote(verb)
}

// filterWithoutInputDeny names the tool, since on a pipeline the reader cannot otherwise
// tell which stage was left without input.
func filterWithoutInputDeny(tool string) string {
	lead := "Give `" + tool + "` its input: name a file, pipe into it, or redirect one with `<`."
	switch {
	case stdinReaders[tool].operands == operandsNeverInput || stdinReaders[tool].operands == operandsAreCommand:
		lead = "`" + tool + "` reads only stdin, and its operands are never input: pipe into it or redirect a file with `<`."
	case tool == "grep" || tool == "egrep" || tool == "fgrep":
		lead += " A recursive grep names its path too (`" + tool + " -r <pattern> .`): macOS's BSD grep reads stdin without one."
	}
	return lead + "\n" +
		"As written it reads the shell's own stdin, which nothing on this line feeds: where the harness holds it open, the call hangs past the tool timeout and keeps waiting in the background."
}

// mintedLogNote names where the output already lives, and ONLY for a verb that mints one.
//
// A target run persists its whole log and prints a ref for it, so capturing the console is
// redundant there and saying so is the point. Every other verb mints nothing: `magus ls`
// has no ref and no run log, so offering `query output <ref>` would send the reader after
// an id that does not exist. That is the same failure the flat menu made on the pipe rule,
// and it is worth more care here because the suggestion LOOKS specific.
func mintedLogNote(verb string) string {
	head, _, _ := strings.Cut(verb, " ")
	if head != "run" && head != "affected" && head != "x" {
		return ""
	}
	return "\nThe run already wrote its full log: `" + hint.QueryOutput.With("<ref>") + "` reads it back, and a failure prints the .magus/logs/ path itself."
}

var (

	// ADVISE, never deny: reading the revision is legitimate, and checkpoint is a
	// strict SUPERSET rather than a substitute, so there is nothing to block. That
	// also rules out the third deny trigger, which needs an exact equivalent.
	checkpointGuardContext = "magus workspace: `" + hint.VCSCheckpoint.String() + "` identifies the working state (`-o name` prints `<revision>` clean, `<revision>+<digest>` dirty), so a later reader knows what the work was looking at. It PRINTS; the value reaches a store only when you register it with a lease or record it with `magus session checkpoint`.\n" +
		"A revision alone cannot identify a DIRTY tree: two workers on the same commit with different uncommitted work read as identical, and the patch digest is what separates them. checkpoint RESOLVES AND RECORDS with no tag, no stash, no ref, and no file, so one nobody keeps has cost nothing."

	// ADVISE, never deny: re-resolving dependencies is legitimate work with no
	// exact magus equivalent to route to, so the third deny trigger does not apply.
	// It is here because update is under-discoverable (a reserved charm nothing
	// prompts for), and a lockfile refreshed outside magus is a write the cache and
	// the affected set never saw.
	//
	// The covering TARGET is not named and cannot be: update is magus vocabulary,
	// but which target carries the dependency work is the workspace's.
	//
	// Shared with the raw-tool deny, which appends it when the denied command is
	// also a re-resolution (`go mod tidy` is both), so the charm is named whichever
	// rule answers first.
	updateAdvice = "Run the covering target with the update charm (`" + hint.Run.With("<target>:update", "<project>") + "`) so the dependency rewrite happens inside magus, cached and visible to affected tracking. `" + hint.DescribeTargets.String() + "` lists what this workspace defines.\n" +
		"update is the reserved charm for moving PINNED UPSTREAM state forward, the way rw covers derived output: reproducible from a clean checkout is rw, dependent on what a registry or a vulnerability feed serves today is update. ci strips both, so a gate verifies the committed lockfile rather than refreshing it."
	updateGuardContext = "magus workspace: " + updateAdvice

	// Advice, not a deny: a raw install is correct, only uncached. "install" is the
	// project's own top-level target (magusfile convention, not a spell op name: a
	// spell's install op is named per binary, pnpm-install/go-mod-download/...).
	installGuardContext = "magus workspace: `" + hint.Run.With("install", "<project>...") + "` runs the same install, keyed on the lockfile, and replays it when nothing changed."

	// Advice, not a deny: it wastes a line, it does not break anything.
	echoOnSuccessAdvice = "Drop the `&& echo ...` and read the exit status: it already says the command passed, and a message that prints only on success adds nothing."

	// Advise, not deny: timing a command is legitimate, and the point is that magus
	// already answered the question better than the shell can.
	timedMagusAdvice = "magus times itself: drop `-s` and it prints each target's duration and a `(cached, 320ms)` or `(ran, 5m28s)` verdict. `time` around a silent run measures the wall clock magus already reported, and hides which targets replayed, which is usually the thing being asked."

	// Advise, not deny: bounding a run is legitimate, and no deny trigger applies;
	// nothing is unrecoverable, nothing is written, and the equivalent is close but
	// not exact.
	timeoutMagusAdvice = "magus has its own: `" + hint.Run.With("<target>", "<project>", "--timeout", "5m") + "` (and the same flag on `" + hint.Affected.String() + "`). It cancels the run rather than signaling the process, so the error names the target (`run ci: timed out after 5m`) and it logs elapsed/remaining heartbeats while the run is still going.\n" +
		"An external `timeout` sees one opaque process: it cannot say which target was still running, and the SIGTERM lands wherever the run happened to be."
)

// denySharedStash refuses an unqualified stash restore. The verb it names in the
// reason is also the rule's Arg, so the two cannot describe different commands.
func denySharedStash(verb string) ShellVerdict {
	return ShellVerdict{
		Deny: "Name the entry you meant: read `git stash list`, then `git stash " + verb + " stash@{N}`.\n" +
			"Bare `git stash " + verb + "` acts on stash@{0}, and the stash stack belongs to the REPOSITORY rather than your worktree: the top entry is often another checkout's work, and " + verb + " applies or destroys it.",
		Rule: denyRule{Name: denyRuleSharedStash, Arg: verb},
	}
}

// denyWholeTree refuses an operation that discards the whole tree, op naming which
// one: in the reason and, for the same reason as above, in the rule.
func denyWholeTree(op string) ShellVerdict {
	return ShellVerdict{
		Deny: "Verify in place. No magus run needs a clean tree: `" + hint.Run.With("<target>", "<project>") + "`, or `" + hint.Affected.With("ci") + "` for everything the diff reaches. If you truly need a pristine tree, use " + scratchCheckout(op) + ".\n" +
			"whole-tree " + op + " destroys uncommitted and untracked work, including a concurrent agent's. See the magus-vcs-hygiene skill.",
		Rule: denyRule{Name: denyRuleWholeTree, Arg: op},
	}
}

// scratchCheckoutFor names the disposable checkout for the backend op belongs to. This
// line used to say "a throwaway git worktree" unconditionally, which is advice an hg or
// sl user cannot follow, and the guard now denies their commands too, so it would be the
// first thing they were told and it would be wrong.
//
// Only git gets a verb, because only git has one for this. Naming `hg share` would point
// at an extension that may not be enabled; a clone is what always works.
func scratchCheckout(op string) string {
	if strings.HasPrefix(op, "git ") {
		return "a throwaway `git worktree add`"
	}
	return "a throwaway clone"
}

// magusInvokes reports whether any resolved command runs magus carrying all of the given
// words among its arguments.
//
// Position-free among magus's OWN arguments on purpose: magus accepts its global flags
// before the verb, so `magus --root . notes edit x` and `magus -o json diff --ack` are the
// same invocations the anchored patterns are written for, and both walked past them. Tokens
// after a bare `--` are passed through to a spell's tool, not read by magus, so they are
// excluded: otherwise `magus run go::go-test . -- notes edit` reads as note-authoring.
func magusInvokes(cmds []hint.Invocation, words ...string) bool {
	for _, c := range cmds {
		if c.Name != "magus" {
			continue
		}
		args := c.Args
		if i := slices.Index(args, "--"); i >= 0 {
			args = args[:i]
		}
		if !slices.ContainsFunc(words, func(w string) bool { return !slices.Contains(args, w) }) {
			return true
		}
	}
	return false
}

// ruleFires asks a parsed rule when the line parsed, and its pattern when it did not.
//
// The fallback is for a line the shell parser rejects, not a second opinion: a rule that
// consulted both would keep every false positive the pattern has, which is the whole reason
// these moved. An unparseable line is rare and cannot be judged structurally at all, so
// there the pattern is the only answer available.
func ruleFires(cmds []hint.Invocation, parsed bool, command string,
	rule func([]hint.Invocation) bool, fallback *regexp.Regexp,
) bool {
	if parsed {
		return rule(cmds)
	}
	return fallback.MatchString(command)
}

// notesWriteVerbs are the `magus notes` subcommands that AUTHOR a note; `ls`, `get` and
// `verify` read and stay allowed.
//
// `promote` is listed even though it already refuses an unmodified body: an agent editing
// the draft it wrote satisfies that check, so the refusal cannot stand in for this rule.
var notesWriteVerbs = []string{"edit", "capture", "promote"}

// notesWriteFires is magusRuleFires over a set of verbs, which it cannot reuse because
// magusInvokes requires every word it is handed and these verbs are alternatives.
func notesWriteFires(cmds []hint.Invocation, parsed bool, command string) bool {
	if !parsed {
		return notesWriteRe.MatchString(command)
	}
	return slices.ContainsFunc(notesWriteVerbs, func(verb string) bool {
		return magusInvokes(cmds, "notes", verb)
	})
}

// agentSignOffFires is magusRuleFires over two shapes it cannot express as one word set:
// minting a read receipt (`diff --ack`) and closing an attention request (`session
// dispose`) are different verbs recording different acts, but both record that a PERSON
// did something, so one rule and one deny cover both rather than a third rule per verb.
func agentSignOffFires(cmds []hint.Invocation, parsed bool, command string) bool {
	if !parsed {
		return agentSignOffRe.MatchString(command)
	}
	return magusInvokes(cmds, "diff", "--ack") || magusInvokes(cmds, "session", "dispose")
}

// precedentIdentRe is the identifier shape the transcript mining classified as a
// precedent hunt: CamelCase, or snake_case with a real separator. A run of lowercase
// letters is as likely to be prose, and routing that to refs spends the session's one
// firing on a miss.
var precedentIdentRe = regexp.MustCompile(`^(?:[A-Za-z0-9]*[a-z0-9][A-Z][A-Za-z0-9]*|[a-z0-9]+(?:_[a-z0-9]+)+)$`)

// precedentIdentMin is the length floor from the same classifier. Short names collide
// with ordinary words often enough that the graph answer would be wrong as often as it
// was useful.
const precedentIdentMin = 6

// precedentIdent returns the identifier a line is hunting for, or empty when the line is
// not a precedent hunt.
//
// hint.Classify draws the line that matters: a grep reading a pipeline's output is
// ClassRead, and 26% of grep invocations in the mining were that shape (`go test | grep
// FAIL`). Routing those to the graph would fire on every test run and teach the reader to
// skip the whole family.
func precedentIdent(cmds []hint.Invocation) string {
	for _, c := range cmds {
		if !hint.IsSearchTool(c.Name) {
			continue
		}
		if hint.Classify(hint.Invocation{Name: c.Name, Args: c.Args}) != hint.ClassSearchSource {
			continue
		}
		for _, a := range c.Args {
			if strings.HasPrefix(a, "-") || len(a) < precedentIdentMin {
				continue
			}
			if precedentIdentRe.MatchString(a) {
				return a
			}
		}
	}
	return ""
}

// Evaluate applies the guard rules in severity order, against the facts deps supplies
// (the spell catalog the raw-tool rule matches against, among them).
//
// magus denies on three independent triggers and explains everything else:
//
//  1. it cannot be UNDONE: the whole-tree git rules;
//  2. it WRITES INTO THE WORKING TREE: codegen, formatters with -w/--fix,
//     dependency files, build output landing on a tracked path;
//  3. it has an EXACT WORKING EQUIVALENT: raw `go test` against `magus run test`.
//
// Trigger 2 is the firm one: reading through the wrong tool costs a cache hit,
// writing through it corrupts the workspace's account of itself. Trigger 3 is
// denied not because the command is dangerous but because the replacement is
// complete, which makes the deny free.
//
// A deny is only legitimate once the replacement it names actually works: the
// reverted grep deny removed a capability magus had nothing to route to. Do not
// add one without checking that path end to end.
func Evaluate(deps Dependencies, command string) ShellVerdict {
	d := effectiveDialect(deps.ShellDialect)
	builtin := evaluateRules(deps, command, d)
	// A built-in advisory is about this workspace, so a line that only touches paths
	// outside it has nothing to be advised about. Denies are exempt: each one decides for
	// itself whether a path outside the tree changes its answer.
	if builtin.Deny == "" && builtin.Context != "" && deps.scope.lineOutside(command, d) {
		builtin = ShellVerdict{}
	}
	v := strengthenWithWorkspace(builtin, matchWorkspaceShell(deps.ShellRules, command, d))
	// A deny refuses the WHOLE line, and the reason only ever discusses the one construct
	// that earned it. On a line holding several commands that reads as a partial refusal:
	// the writer fixes the named command and assumes the others ran. They did not.
	//
	// Measured 2026-09-08: five times in one session an edit was chained ahead of a magus
	// call carrying a redirect, the redirect denied, and the edit silently never happened,
	// each caught only when a later step failed for an unrelated-looking reason. The reason
	// text was correct and complete about the redirect every time. What it never said was
	// how much else went with it.
	if v.Deny != "" {
		v.Deny += nothingRanNote(command, d)
	}
	return v
}

// nothingRanNote is the line a deny carries when it refused several commands at once, or ""
// for a single one, where the refusal already says it did not run.
func nothingRanNote(command string, d Dialect) string {
	if cmds, parsed := ParseCommandsDialect(command, d); parsed && len(cmds) > 1 {
		return fmt.Sprintf("\nnothing ran (%d commands)", len(cmds))
	}
	return ""
}

func evaluateRules(deps Dependencies, command string, d Dialect) ShellVerdict {
	// The program rules judge PARSED commands; the rest read the line as written,
	// because they are about its SHAPE (a pipe, a redirect, a cd before a magus
	// call) rather than which program runs.
	//
	// A matched git rule that only ADVISES is held, not returned: returning here
	// let a trailing `git commit` downgrade a deny to an advisory. Deny always
	// outranks advise, whichever rule saw the line first.
	cmds, parsed := ParseCommandsDialect(command, d)
	// Authoring a note is refused before anything else, because it is the one rule whose
	// whole point is that it holds on EVERY surface: the path rule sees file writes, and
	// these verbs are commands.
	if notesWriteFires(cmds, parsed, command) {
		return ShellVerdict{Deny: denyNotesAuthor, Rule: denyRule{Name: denyRuleNotesAuthor}}
	}
	// Beside the notes rule and for the same reason: both refuse an agent AUTHORING a
	// human's statement, and both have to hold however the command is spelled.
	if agentSignOffFires(cmds, parsed, command) {
		return ShellVerdict{Deny: denyAgentSignOff, Rule: denyRule{Name: denyRuleAgentSignOff}}
	}
	// A credential rule, so it holds however the line is spelled, before any rule about shape.
	if credentialVerbFires(cmds, parsed, command) {
		return ShellVerdict{Deny: denyCredentialVerb, Rule: denyRule{Name: denyRuleCredentialVerb}}
	}
	if ruleFires(cmds, parsed, command, sedInPlaceFires, sedInPlaceRe) {
		return ShellVerdict{Deny: denySedInPlace, Rule: denyRule{Name: denyRuleSedInPlace}}
	}
	if busyWaitFires(command, d) {
		return ShellVerdict{Deny: denyBusyWait, Rule: denyRule{Name: denyRuleBusyWait}}
	}
	// Beside busy-wait: both invent a waiter for work magus already tracks. This one is
	// the process-table form (pgrep/ps/pidof); that one is the sleep-loop form.
	if parsed && processPollFires(cmds) {
		return ShellVerdict{Deny: denyProcessPoll, Rule: denyRule{Name: denyRuleProcessPoll}}
	}
	// Beside busy-wait too: each is a line that hangs past the tool timeout and goes on
	// waiting in the background. The backtick is judged first because a stray one is how a
	// filter loses its file operand in the first place.
	if backtickSubstFires(command, d) {
		return ShellVerdict{Deny: denyBacktickSubstitution, Rule: denyRule{Name: denyRuleBacktickSubstitution}}
	}
	if tool, ok := unfedReader(command, d); ok {
		return ShellVerdict{Deny: filterWithoutInputDeny(tool), Rule: denyRule{Name: denyRuleFilterWithoutInput, Arg: tool}}
	}
	// Beside busy-wait for the other half of the same story: that rule refuses WAITING on
	// a task capture, this one refuses trimming it once it arrives. It has to sit above
	// the search advisories, which would otherwise answer for the grep and say nothing
	// about what it was cutting away.
	if parsed && captureFilterFires(cmds, command, d) {
		return ShellVerdict{Deny: denyCaptureFilter, Rule: denyRule{Name: denyRuleCaptureFilter}}
	}
	if scriptedRewriteFires(command, d, deps.scope) {
		return ShellVerdict{Deny: denyScriptedRewrite, Rule: denyRule{Name: denyRuleScriptedRewrite}}
	}
	var advisory ShellVerdict
	// Held rather than returned, like the git advisories below: a deny found later on the
	// same line outranks it.
	if chainedRunRe.MatchString(command) {
		advisory = ShellVerdict{Context: adviseChainedRun, Rule: denyRule{Name: advisoryChainedRun}}
		// Narrowed to the combined-run form when every magus run/affected invocation on
		// the line names the same target: charms included. A chain of genuinely
		// different targets stays chained-run's text, and its own domain.
		if parsed {
			if text, ok := splitRunLineAdvice(cmds); ok {
				advisory = ShellVerdict{Context: text, Rule: denyRule{Name: denyRuleName(advisorySplitRun)}}
			}
		}
	}
	if parsed {
		if v, matched := gitGuard(cmds); matched {
			if v.Deny != "" {
				return v
			}
			advisory = v
		}
		// Same bar, the other backends. Only the parsed path: their verbs have no unanchored
		// fallback because clean, update, revert, goto, restore and abandon are ordinary English
		// that a commit message or a skill body would carry, and matching prose is the
		// false positive gitGuard's own doc says the AST exists to avoid.
		if v, matched := nonGitVCSGuard(cmds); matched {
			if v.Deny != "" {
				return v
			}
			advisory = v
		}
	} else if v, matched := gitGuardFallback(command); matched {
		if v.Deny != "" {
			return v
		}
		advisory = v
	}

	rawToolCmd, rawToolDeny := firstRawToolDenied(deps, command)
	pipedVerb, pipedFilter, pipedToFilter := magusPipedToFilter(command, d)
	redirVerb, redirDest, redirected := magusRedirected(command, d)
	switch {
	// Throwaway before the general cd deny: the same line matches both, and the
	// throwaway reason is the one that says why THAT relocation is wrong.
	case magusInThrowawayCopy(command, d):
		return ShellVerdict{Deny: throwawayCopyDeny, Rule: denyRule{Name: denyRuleThrowawayCopy}}
	case shellUsesCd(cmds, parsed, command):
		return ShellVerdict{Deny: denyCd, Rule: denyRule{Name: denyRuleCd}}
	case rawToolDeny:
		match, _ := rawToolMatch(deps, rawToolCmd)
		reason := runGuardAdvice(match)
		// `go mod tidy` is both a covered spell op and a dependency re-resolution.
		// The deny answers first, so it is the only text the reader gets, and
		// routing into magus without naming the charm that makes the write legal
		// sends them to a target that would refuse to do it.
		//
		// The WHOLE line is scanned, not just the denied command: `go test ./... &&
		// npm update` denies on the first half, and the reader was never told the
		// second half rewrites a lockfile: the deny is the only text they get.
		if isDependencyMutation(rawToolCmd) || slices.ContainsFunc(cmds, isDependencyMutation) {
			reason += "\n" + updateAdvice
		}
		return ShellVerdict{
			Deny: explainDeny(command, rawToolCmd, reason),
			Rule: denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(rawToolCmd)},
		}
	case pipedToFilter:
		return ShellVerdict{Deny: pipeDeny(pipedVerb, pipedFilter), Rule: denyRule{Name: denyRuleOutputPipe}}
	case redirected:
		return ShellVerdict{Deny: redirectDeny(redirVerb, redirDest), Rule: denyRule{Name: denyRuleOutputRedirect}}
	}
	if name, msg, ok := misconfiguredMagusEnv(command, d); ok {
		return ShellVerdict{Deny: denyMisconfiguredMagusEnv(msg), Rule: denyRule{Name: denyRuleUnknownEnv, Arg: name}}
	}
	// Below the rules that name the command itself: on `go test ./...; echo $?` the raw
	// tool is the correction worth reading first.
	if echo := exitStatusEchoFires(command, d); echo != exitEchoNone {
		return ShellVerdict{Deny: denyExitStatusEchoFor(echo), Rule: denyRule{Name: denyRuleExitStatusEcho}}
	}
	switch {
	case parsed && slices.ContainsFunc(cmds, isDependencyMutation):
		return ShellVerdict{Context: updateGuardContext}
	case parsed && slices.ContainsFunc(cmds, func(c hint.Invocation) bool { return installAdvised(deps, c) }):
		return ShellVerdict{Context: installGuardContext}
	case ruleFires(cmds, parsed, command, sourceReadFires, sourceReadRe):
		return ShellVerdict{Context: sourceReadAdvice, Kind: advisorySourceRead, Brief: sourceReadBrief}
	}
	if v, ok := searchVerdict(deps, cmds); ok {
		return v
	}
	switch {
	case echoOnSuccessRe.MatchString(command):
		return ShellVerdict{Context: echoOnSuccessAdvice}
	case timedMagusRe.MatchString(command):
		return ShellVerdict{Context: timedMagusAdvice}
	case timeoutMagusRe.MatchString(command):
		return ShellVerdict{Context: timeoutMagusAdvice}
	}
	// Nothing denied, so a held git advisory is the answer after all.
	return advisory
}

// runGuardContextFor leads with the TOP-LEVEL TARGET, and names the spell op
// only as the arg-passthrough escape hatch.
//
// The target is not named, because it cannot be: the guard ships in a binary and
// a workspace calls its targets whatever it likes, so a literal `magus run test`
// would be this repository's vocabulary asserted over someone else's. The op IS
// named, since it resolved from the spell catalog rather than from a convention.
func runGuardAdvice(match toolMatch) string {
	return fmt.Sprintf("Run it through magus: `"+hint.Run.With("<target>"+charmSuffix(match), "<project>")+"`; `"+hint.DescribeTargets.With("-o", "name")+"` lists this workspace's targets.\n"+
		"Tool flags go after `--`: `"+hint.Run.With("%s::%s", "[<project>]", "--", "<tool-args>")+"`.\n%s",
		match.spell, match.operation, runGuardContext)
}

// charmSuffix spells the rewrite charm into the suggested target, because the same target
// answers both forms and routing a rewrite at the checking one sends it at a target that
// would refuse to do it. The charm is named rather than the target: a workspace calls its
// targets whatever it likes, and `rw` is magus's own vocabulary. A check needs no suffix,
// which is why only the rewrite arm spends a word on it.
func charmSuffix(match toolMatch) string {
	if match.rewrites {
		return ":rw"
	}
	return ""
}
