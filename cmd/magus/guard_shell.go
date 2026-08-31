package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/project"
	"mvdan.cc/sh/v3/syntax"
)

// The command surface of `magus session hook`: the rules that judge a shell line,
// minus the two large pieces that earned their own files. Tokenizing is in
// guard_shellparse.go and the git rules are in guard_git.go.
//
// evaluateBashGuard is a pure function of the command line and is tested as one, so
// a rule that has to read live workspace state lives beside its own reader instead
// (guard_gate.go). The path surface is guard_write.go.

// bashGuardVerdict classifies one Bash command line. Deny blocks the call with a
// reason the model sees; Context lets it proceed and injects a reminder.
//
// Kind names an advisory that is held to one firing per session (guard_advisory.go).
// It is empty for the advisories that correct the command in front of the reader, where
// a second firing reports a second mistake rather than repeating a standing fact, and it
// is always empty on a deny: a refusal explains itself every time it refuses.
type bashGuardVerdict struct {
	Deny    string
	Context string
	Kind    advisoryKind
}

// cmdPos anchors a pattern to a COMMAND position - line start or just after a
// shell separator - so a pattern cannot match its own name appearing as text.
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

// guardChainedRunRe matches a second `magus run` on the same line.
//
// Targets COMPOSE through ctx.needs, so a chain is usually one invocation that already did
// the whole thing: in this workspace `lint` needs `format` needs `generate`, which makes
// `magus run generate . ; magus run format . ; magus run lint .` three workspace loads to
// produce what the third one produces alone.
//
// It matches the SHAPE rather than parsed commands because the mistake is the chaining, and
// every spelling of it - ; && || - is the same mistake.
// `affected` counts as a second one: the gate runs the whole pipeline over everything the diff
// reaches, so building a project immediately before it is asking for the same work twice. That
// spelling slipped past the first version of this rule, which only looked for `run`, and the
// author of the rule then made exactly that mistake within the hour.
var guardChainedRunRe = regexp.MustCompile(
	cmdPos + `(?:\./)?magus\s+(?:run|affected)\s[^;&|]*[;&|]+\s*(?:\./)?magus\s+(?:run|affected)\s`)

// guardToolMatch is one command spell operation Magus can run on the caller's
// behalf. It is derived from the registered spell catalog, never a hand-kept
// list in the guard: adding a spell operation automatically teaches the hook
// which raw command it replaces.
type guardToolMatch struct {
	spell     string
	operation string
}

// guardTextFilters are the shell commands whose purpose is to trim, slice, or
// search text. Piping magus into one is always a missing output flag.
//
// `jq` and `magus` are deliberately absent. Both consume a CONTRACT rather than
// scraping a layout - `jq` over `-o json`, and magus-into-magus over `--stdin` -
// which is composition, the opposite of the antipattern. `tee` is absent too: it
// duplicates a stream without trimming it.
var guardTextFilters = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"head": true, "tail": true, "awk": true, "sed": true,
	"cut": true, "sort": true, "uniq": true, "wc": true, "column": true,
}

// magusPipedToFilter reports a magus command whose output is being trimmed by a
// shell text filter. Denied rather than advised: as an advisory it fired
// repeatedly and was read straight past, the same trained-reflex result the
// raw-tool advisory produced.
//
// `magus query output <ref>` is the ONE exemption - it returns a raw captured
// log with no schema for magus to project, so searching it is a real need. Every
// other verb emits a structured record that -o shapes exactly.
func magusPipedToFilter(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		pipe, ok := n.(*syntax.BinaryCmd)
		if !ok || pipe.Op != syntax.Pipe {
			return true
		}
		if trimmableMagus(lastOfPipeline(pipe.X)) && isTextFilter(firstOfPipeline(pipe.Y)) {
			found = true
		}
		return true
	})
	return found
}

// magusRedirected reports a magus command whose stdout or stderr is being sent
// to a file, to /dev/null, or folded together with 2>&1.
//
// Denied for the pipe rule's reason. `--silent > /dev/null 2>&1` is the worst
// case: silent mode stays quiet UNTIL something fails, then prints the likely
// diagnostics and the full-log path, and the redirect discards exactly that.
//
// `magus query output <ref>` is exempt, as with the pipe rule. Note --tee is NOT
// the escape hatch a reader might assume - it mirrors STRUCTURED output only -
// so the message points at the persisted log instead.
func magusRedirected(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		stmt, ok := n.(*syntax.Stmt)
		if !ok || len(stmt.Redirs) == 0 || !trimmableMagus(stmtCommands(stmt)) {
			return true
		}
		for _, r := range stmt.Redirs {
			switch r.Op {
			// Output redirects only. A HEREDOC or an input redirect feeds magus
			// rather than hiding what it said, so neither is this rule's business.
			case syntax.RdrOut, syntax.AppOut, syntax.DplOut, syntax.RdrAll, syntax.AppAll:
				found = true
			}
		}
		return true
	})
	return found
}

// throwawayDirRe matches a path under a temp root, or any path with a scratchpad
// segment - the places a COPY of a workspace gets made rather than checked out.
var throwawayDirRe = regexp.MustCompile(`^(/private)?/(tmp|var/folders)/|/scratchpad(/|$)`)

// assignmentRe recovers a `NAME=value` made earlier on the same line.
var assignmentRe = regexp.MustCompile(`(?:^|[;&|]\s*|\s)([A-Za-z_]\w*)=("?)([^"'\s;&|]+)`)

// magusCdTargets returns the directories a line relocates to before running
// magus, resolving same-line variable assignments: the observed shape chains a
// whole pipeline onto one, so a literal `cd /tmp/...` would be missed.
//
// Empty when the line does not RUN magus, so a rule built on this cannot fire on
// one that merely names a directory.
func magusCdTargets(command string) []string {
	if !mentionsMagusCommand(command) {
		return nil
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
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
// instance of the same mistake - a sibling checkout of this repository - can only
// be recognized by reading the filesystem, so it lives in guard_checkout.go and
// shares magusCdTargets rather than growing a second cd scanner.
func magusInThrowawayCopy(command string) bool {
	return slices.ContainsFunc(magusCdTargets(command), throwawayDirRe.MatchString)
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
func mentionsMagusCommand(command string) bool {
	cmds, parsed := parseGuardCommands(command)
	if !parsed {
		return false
	}
	return slices.ContainsFunc(cmds, func(c guardCommand) bool { return c.Name == "magus" })
}

func trimmableMagus(cmds []guardCommand) bool {
	for _, c := range cmds {
		if c.Name != "magus" {
			continue
		}
		if len(c.Args) >= 2 && c.Args[0] == "query" && c.Args[1] == "output" {
			continue
		}
		return true
	}
	return false
}

func isTextFilter(cmds []guardCommand) bool {
	return slices.ContainsFunc(cmds, func(c guardCommand) bool { return guardTextFilters[c.Name] })
}

// firstRawToolDenied parses the line and returns the FIRST command it would run
// that magus already covers. A line that does not parse skips this rule.
//
// It returns the command rather than a bool so the verdict can name it: told only
// that `bash -c '...'` was denied, a reader has to work out which part offended,
// and a denial becomes a wrapper hunt instead of a correction.
func firstRawToolDenied(command string) (guardCommand, bool) {
	cmds, ok := parseGuardCommands(command)
	if !ok {
		return guardCommand{}, false
	}
	for _, c := range cmds {
		if _, ok := rawToolMatch(c); ok {
			return c, true
		}
	}
	return guardCommand{}, false
}

// explainDeny prefixes a rule's reason with the resolved command that tripped
// it, and says so explicitly when that differs from what was typed - which is
// the whole point of peeling wrappers, made visible instead of implied.
//
// It does not repeat that re-wrapping will not help: runGuardContext's tail
// already says the guard reads the command being RUN, and this prefix is
// prepended to it.
func explainDeny(typed string, c guardCommand, reason string) string {
	resolved := strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
	var b strings.Builder
	b.WriteString("magus guard denied `" + resolved + "`")
	if strings.TrimSpace(typed) != resolved {
		b.WriteString(" (what `" + strings.TrimSpace(typed) + "` resolves to once wrappers and quoting are stripped)")
	}
	b.WriteString(".\n\n")
	b.WriteString(reason)
	return b.String()
}

// rawToolDenied reports whether one resolved command has a registered spell-op
// equivalent. It intentionally allows a tool that Magus does not expose; a
// guard may funnel an available capability, never remove one.
func rawToolDenied(c guardCommand) bool {
	_, ok := rawToolMatch(c)
	return ok
}

// rawToolMatch finds the operation whose rendered base command and semantic
// subcommand match c. Both the ordinary and rw renderings participate: a spell
// can expose a read-only check (`gofmt -l`) and a rewriting form (`gofmt -w`)
// without the guard confusing the two. The catalog is read for every process,
// so a newly registered spell operation requires no guard edit.
func rawToolMatch(c guardCommand) (guardToolMatch, bool) {
	for _, a := range c.Args {
		if a == "--version" || a == "-version" || a == "-V" {
			return guardToolMatch{}, false
		}
	}
	for _, spell := range project.DefaultSpellRegistry().All() {
		for _, operation := range spell.Targets() {
			for _, charms := range [][]string{nil, []string{"rw"}} {
				program, args, ok, err := spell.RenderCommand(operation, charms)
				if err != nil || !ok || program == "" || filepath.Base(program) != c.Name {
					continue
				}
				prefix := guardCommandPrefix(args)
				if len(prefix) == 0 || len(c.Args) < len(prefix) || !slices.Equal(c.Args[:len(prefix)], prefix) {
					continue
				}
				return guardToolMatch{spell: spell.Name(), operation: operation}, true
			}
		}
	}
	return guardToolMatch{}, false
}

// guardCommandPrefix extracts the semantic command portion from an operation's
// rendered argv. It preserves compound verbs (`go mod tidy`, `go tool
// govulncheck`) and uses write-mode flags as a verb when an operation has no
// subcommand (`gofmt -w`). Other leading flags describe a read-only rendering
// and therefore do not create a raw-tool deny.
func guardCommandPrefix(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			break
		}
		if arg == "-w" || arg == "--write" || arg == "--fix" {
			return []string{arg}
		}
	}
	first := 0
	for first < len(args) && strings.HasPrefix(args[first], "-") {
		first++
	}
	if first == len(args) || args[first] == "." || strings.HasPrefix(args[first], "./") {
		return nil
	}
	prefix := []string{args[first]}
	if (args[first] == "mod" || args[first] == "tool") && first+1 < len(args) && !strings.HasPrefix(args[first+1], "-") {
		prefix = append(prefix, args[first+1])
	}
	return prefix
}

// The guard patterns. [^&|;]* keeps a flag search inside one segment of a
// compound command, so `git reset && tool --hard-mode` does not false-positive.
//
// These remain ONLY as the unparsable-line fallback: gitGuard above is the
// primary path, and it reads an AST instead of the raw text.
var (
	guardStashRe = regexp.MustCompile(`\bgit\s+stash\b`)
	// Reading a stash is safe; RESTORING one is not, and the parsed rule denies the bare
	// restore forms. Listing pop/apply/drop/branch as safe here left the destructive
	// spellings with no verdict at all on a line that does not parse, which is the one
	// place an over-eager deny is the right answer.
	guardStashSafeRe = regexp.MustCompile(`\bgit\s+stash\s+(list|show)\b`)
	guardResetRe     = regexp.MustCompile(`\bgit\s+reset\b[^&|;]*--hard`)
	guardCheckoutRe  = regexp.MustCompile(`\bgit\s+checkout\s+(--\s+)?\.(\s|$)`)
	guardRestoreRe   = regexp.MustCompile(`\bgit\s+restore\b[^&|;]*\s\.(\s|$)`)
	guardCleanRe     = regexp.MustCompile(`\bgit\s+clean\b[^&|;]*\s-\w*[fdxX]`)
	guardStageRe     = regexp.MustCompile(`\bgit\s+(commit|add)\b`)
	// `git add -A` / `git add .` / `git add --all` / `git add -u`: stage-everything
	// forms. Split out from guardStageRe because these DENY - see evaluateBashGuard.
	guardStageAllRe = regexp.MustCompile(cmdPos + `git\s+add\s+(-A\b|--all\b|-u\b|--update\b|\.(\s|$))`)
	// Push, NOT commit. Committing in a half-finished state is ordinary and
	// sometimes necessary; a gate there would fire constantly and be tuned out.
	// Publishing is where the work stops being yours alone, so that is where the
	// reminder earns its place - and it stays an advise, because a push can
	// legitimately carry a work-in-progress branch.
	guardPushRe = regexp.MustCompile(`\bgit\s+push\b`)
	// A SCOPED revert: `git checkout -- <paths>` / `git restore <paths>`. The
	// whole-tree forms above already deny; this one is legitimate often enough
	// that it only advises, but it is the shape of the most common wrong reflex
	// an agent has about generated files.
	// `git checkout ... -- <paths>` needs the `--` separator to be a revert at
	// all; without it the argument is a branch (`git checkout main`, `-b foo`),
	// which is not this rule's business. `git restore` targets worktree files by
	// definition, so its bare form counts.
	guardScopedRevertRe = regexp.MustCompile(`\bgit\s+checkout\b[^&|;]*\s--\s|\bgit\s+restore\b`)
	// `cd <dir> && magus ...`: magus is CWD-relative, so this is the shape of
	// running the right command against the wrong project. Every magus command
	// that acts on a project takes it as an explicit argument, so the cd is
	// almost always avoidable - and when it is not (a DIFFERENT workspace), the
	// answer is --root, not a cd.
	guardCdMagusRe = regexp.MustCompile(`\bcd\s+\S+\s*(&&|;)\s*(\S*/)?magus\s`)

	// guardNotesWriteRe matches an invocation that would AUTHOR a note. It is the
	// unparsable-line fallback for magusInvokes below, the way gitGuardFallback is for
	// gitGuard: anchoring the verb to the program misses every global flag in between.
	//
	// The path rule already refuses an agent write into a notes store, but it only ever
	// sees file writes - and `magus notes edit` reading piped prose is a command, not a
	// file write, so it would sail past a boundary that is supposed to be about WHO is
	// writing rather than which surface they used. This closes that, so the rule holds
	// however the write is spelled.
	guardNotesWriteRe = regexp.MustCompile(`\bmagus\s+notes\s+edit\b`)

	// guardReadAckRe matches an invocation that would mint a read receipt.
	//
	// A receipt is a claim that a PERSON read something, and it is the only fact in a
	// review no analysis can supply. An agent that can mint one turns the whole measure
	// into a formality it satisfies on the way past - and it would, because stamping the
	// changeset is the obvious tidy-up at the end of a task.
	//
	// The guard is the right place precisely because of what it sees: it is wired into
	// agent hosts, so every command reaching it came from an agent by construction. A
	// person at a terminal never meets this rule.
	// The unparsable-line fallback for magusInvokes, as above.
	guardReadAckRe = regexp.MustCompile(`\bmagus\s+diff\b[^&|;]*\s--ack\b`)

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
	// applies an exact replacement, and reports what changed - which is the same operation
	// without the portability trap or the blind write.
	guardSedInPlaceRe = regexp.MustCompile(`\bsed\b[^|;&]*\s(-[a-zA-Z]*i[a-zA-Z]*\b|--in-place\b)`)

	// A scripted in-place rewrite: an inline interpreter that runs a REGEX SUBSTITUTION
	// and writes the result back. It is the same edit `sed -i` is refused for, reached by
	// a route the sed rule cannot see, and it is how a rename escapes the graph in
	// practice - `sed -i` is denied, so the next thing to hand is a python one-liner.
	//
	// Deliberately narrow. An interpreter that merely WRITES a file is ordinary authoring
	// and must stay available; what is refused is substitute-then-write, because that is
	// the shape that cannot tell a symbol from a word that looks like one. A rewrite of
	// prose or a config value is caught too - the false positive costs one explanation,
	// while the false negative silently rewrote a dependency's identifier.
	guardScriptedRewriteRe = regexp.MustCompile(`\b(python3?|perl|ruby|node)\b[\s\S]*\b(re\.subn?|str\.replace|\.replace\()[\s\S]*\.write\(|\b(perl|ruby)\s+-[a-zA-Z]*i[a-zA-Z]*\b`)

	// A repo-wide code search. This does NOT claim the agent asked the wrong
	// question - a hook cannot know that - only that a whole-tree text search has
	// a better tool here, because the graph answers from DECLARED sources while a
	// grep hit is a guess. Deliberately narrow: a recursive grep, a bare ripgrep
	// (effectively always repo-wide), or a find-by-name. A plain `grep pattern
	// file` is reading one file and is left alone.
	guardCodeSearchRe = regexp.MustCompile(`\bgrep\s+-[a-zA-Z]*[rR]|\brg\s|\bag\s|\bfind\s+[^|&;]*-name\b`)
	// bareIdentRe recognizes a search pattern that is a single identifier, so the advisory can
	// route it to `magus refs` (the occurrence-precise symbol answer) rather than a free-text query.
	bareIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)
	// diagnosticCodeRe and buzzOpRe route a pattern to `magus query` instead of `magus refs`,
	// because refs resolves only compiled-language symbols. Measured against real session history:
	// a grep for MGS2011 (a diagnostic) or mgs_listManifests (a Buzz spell op) has a graph answer,
	// but it is a diagnostic/function node that query finds and refs misses. See the guard doc's
	// adoption section for the measurement.
	diagnosticCodeRe = regexp.MustCompile(`^MGS[0-9]{4}$`)
	buzzOpRe         = regexp.MustCompile(`^mgs_[A-Za-z0-9_]+$`)
	// guardDocSearchRe fires when a read or search command names a markdown file - an agent
	// looking for something IN prose. Markdown headings are indexed as doc-section nodes, so
	// the answer is a section query, not a whole-file scan. Matches on ".md" so it fires in
	// any repo, not just one that keeps docs under a magus convention.
	guardDocSearchRe = regexp.MustCompile(`\b(cat|bat|head|tail|less|more|grep|egrep|fgrep|rg|ag)\b[^|&;]*\.md\b`)

	// `magus ... && echo "TESTS GREEN"`. The exit status already carries that, which
	// is what an exit status is for; the echo adds a line that is true by
	// construction and tells a reader nothing the command did not.
	guardEchoOnSuccessRe = regexp.MustCompile(`(?:^|[;&|]\s*)(\S*/)?magus\s[^&|;]*&&\s*echo\b`)
	// Read off the raw line, not the parsed command: the wrapper peeling that lets
	// `time go test` be judged as `go test` would erase the very token this rule is
	// about.
	guardTimedMagusRe = regexp.MustCompile(`(?:^|[;&|]\s*)time\s+(\S*/)?magus\s`)
	// `timeout 300 magus run ci .`, read off the raw line for the same reason as the
	// rule above: `timeout` is a peeled wrapper. Narrowed to run and affected, the
	// only two subcommands carrying --timeout - naming it on `magus graph build`
	// would advise a flag that does not exist there.
	guardTimeoutMagusRe = regexp.MustCompile(`(?:^|[;&|]\s*)timeout\s+[^;&|]*?\s(\S*/)?magus\s+(?:run|affected)\b`)

	// A magus invocation whose own output is truncated or filtered by the shell.
	// magus has output flags for this; a pipe throws away the parts the agent
	// then has to guess at. jq is deliberately absent: it composes with -o json
	// rather than fighting it.
)

const (
	vcsGuardContext = "magus workspace: classify the dirty tree first with `magus describe file $(git diff --name-only)`, then stage the reviewed paths explicitly: `git add -- <paths>`.\n" +
		"role=output paths are generated: never hand-edit them, and commit them with the source change that moved them. Load the magus-vcs-hygiene skill for the commit checklist if not already loaded."
	// The tail of runGuardContextFor, which supplies the replacements. This half
	// carries only the WHY and the anti-retry line.
	//
	// DENIED, not advised: every tool matched here has an exact magus equivalent,
	// so the deny costs nothing, and an advisory loses to a trained reflex. As an
	// advisory it changed behavior zero times over a long session and left the Go
	// build cache poisoned by uninstrumented raw runs.
	runGuardContext = "magus covers this exactly and adds cache, sandbox, and affected tracking, so the deny costs you nothing. A raw WRITE (codegen, a formatter with -w/--write/--fix, go mod tidy, build output on a tracked path) also leaves the owning target reporting drift it did not cause, and that half has no exceptions.\n" +
		"The guard reads the command being RUN, so a launcher, a `VAR=value` prefix, or `bash -c '...'` reaches the same verdict. Run the magus command directly. Load the magus-run skill if not already loaded."
	// Reverting regenerated output is the wrong default. An agent that did not
	// hand-edit a gen/ file concludes it is not "its" change and discards it -
	// but a generate target rewriting its declared outputs is the system working,
	// and those outputs belong in the same commit as the source that moved them.
	// The honest test is whether the SOURCE changed, not whether the agent typed
	// into the output.
	revertGuardContext = "magus workspace: classify before reverting with `magus describe file <paths>`, and do not revert a file just because you did not hand-edit it.\n" +
		"A role=output path moved by a source change is correct: it belongs in the SAME commit as that source, and reverting it is what makes CI fail on drift. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged. That drift is environmental, and worth reporting rather than discarding. Load the magus-vcs-hygiene skill if not already loaded."
	// ADVISE, not deny. Denying was tried and reverted: magus has no raw-text
	// search to fall back on, so "where does this string appear" has no magus
	// answer and the deny removed a capability. The advisory still applies the
	// pressure without making the agent unable to work.
	//
	// The reason must ROUTE, not scold: `magus query` indexes DOMAIN entities and
	// returns 0 for a code symbol, while `magus refs` indexes CODE symbols. An
	// agent that tries `magus query someFunc`, gets 0, and concludes the graph is
	// useless is the failure this text exists to prevent.
	// Names the mechanism, because the fix is not "remember where you are" - it is
	// that the project is an argument and never needs to be implied by the CWD.
	cwdGuardContext = "magus workspace: pass the project instead of cd-ing to it (`magus run <target> <project>`, `magus describe project <path>`) so the command means the same thing from anywhere. `magus where <name>` resolves a name to its path.\n" +
		"magus is CWD-relative, so a `cd` first is how the right command lands on the wrong project; project paths are workspace-relative and written bare (`libs/foo`). Only a DIFFERENT workspace needs relocating, and that is `--root <path>`, not a cd."

	searchGuardReason = "this workspace has a knowledge graph, and a text match misses the generated, indirect, and cross-language references it knows about. Pick by what you are asking:\n" +
		"  CODE SYMBOL (defined / used where):  magus refs <symbol>\n" +
		"  DOMAIN ENTITY (projects, targets, spells, ops, docs, diagnostics):  magus query \"<terms>\"  with kind=<k> project=<p> relation=<r> matchers, kind!=<k> to exclude, id=~<re> for a regex\n" +
		"  ONE node's edges, provenance, blast radius:  magus explain <node>\n" +
		"  HOW two things connect:  magus path <a> <b>\n" +
		"`magus query <symbol>` returns 0 for a code symbol, which is refs's job. If refs reports a project not-indexed, that verdict is \"unknown, not absent\": run `magus graph build` and ask again rather than falling back to a text match. Searching raw text in CODE (a string literal, a comment, a config value) has no magus replacement: carry on with grep. Markdown PROSE does now: `magus query kind=docsection \"<terms>\"` returns the section that covers it. Load the magus-query skill for the full grammar."

	docSearchAdvice = "this workspace indexes every markdown heading as a doc section, so prose is queryable, not only greppable. `magus query kind=docsection \"<terms>\"` returns the heading whose section covers your terms, as a `path#anchor` pointer you can read on its own instead of scanning the whole file; add `project=<p>` to scope it and `magus explain <section>` to see what it links to.\n" +
		"Reading one specific file you already know the path of? Read it. This is for when you are LOOKING for where something is explained: the section query lands you on the passage instead of the page. Load the magus-query skill for the grammar."

	// `ci` is the one target name magus ENFORCES (docs/recommendations.md), so it is
	// the one literal a shipped verdict may carry; every other target name is
	// workspace vocabulary and routes through discovery.
	pushGuardContext = "magus workspace: run the gate before publishing if you have not since your last change. `magus affected ci` runs it over every project the diff reaches, including ones you never edited.\n" +
		"Already ran it, or pushing deliberate work-in-progress? Push. Load the magus-run skill if not already loaded."

	denyReadAck = "A read receipt records that a PERSON read a change, so only a person can record one.\n" +
		"This is not a permission you are missing - there is no spelling of it an agent may use, and an agent stamping the changeset would make the measure mean nothing for everybody, including the human relying on it.\n" +
		"Report what is unread instead: `magus diff --impact` names every changed file carrying no receipt, and `magus diff -o json` puts read_state on each file for a caller to branch on.\n" +
		"If you were asked to mark the change reviewed, say that you cannot and hand back the unread list."

	denyNotesAuthor = "Recording a DECISION ABOUT THIS WORKSPACE is what `magus memory put <name>` is for: the agent-writable store, where every entry cites a ref a later reader can re-run.\n" +
		"Notes are human-authored by design: a note is the one thing in the knowledge graph nothing here corroborates later, so its only provenance is the person who wrote it and signed the commit. That is why it is denied however the write is spelled.\n" +
		"If the content genuinely belongs in the notes, say so and let the person run it themselves."

	denyScriptedRewrite = "A scripted substitute-and-write is the same edit `sed -i` is denied for, by another route. Use your editor tool for a few sites; for a whole-tree rename use the graph:\n" +
		"  1. `magus graph build` FIRST if `magus refs` says a project is not-indexed: a cold index answers \"unknown, not absent\", and taking that for \"no matches\" is how a rename misses half its sites.\n" +
		"  2. `magus refs <symbol> --occurrences` gives column-precise, verified sites, per file.\n" +
		"  3. Edit those sites. Let the compiler enumerate what moved; do not widen the pattern until it goes quiet.\n" +
		"A regex cannot tell YOUR symbol from a dependency's symbol of the same name: a `\\.Sum\\b` rewrite aimed at one proto field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`, and the damage is written before any diff is read. The graph knows which is which; a pattern never can.\n\n" +
		"Rewriting raw TEXT (prose, a config value, a string literal) has no graph equivalent: say so and use your editor tool."

	denySedInPlace = "Use your editor tool instead: it reads the file, applies an exact replacement, and reports what changed. For a whole-tree mechanical edit, `magus refs <symbol> --occurrences` gives column-precise sites rather than a pattern that also matches the comment about it.\n" +
		"`sed -i` is not portable and the two spellings destroy each other's work: GNU reads `sed -i 's/x/y/' f` as an edit, BSD and macOS read that same script as the BACKUP SUFFIX, and `sed -i '' ...` makes GNU edit nothing. So it mangles the file on the next machine, by WRITING, before anyone reads a diff. Reading with sed is untouched."

	// Named for what the agent should do instead, not for what it did wrong: the
	// exact safe replacement is the actionable part. `git add -A` is the single command
	// most likely to turn a focused change into an unreviewable one: it sweeps every
	// regenerated output and every unrelated formatting fix a target just wrote into
	// a commit about something else. Measured: one such call put 69 files - a whole
	// regenerated docs site plus five untouched source files - into a commit about
	// four collection methods.
	denyStageAll = "Classify the dirty tree first: `magus describe file $(git diff --name-only)`. Then stage only the reviewed paths with `git add -- <paths>`, and confirm the selection with `git diff --cached --stat` before committing.\n" +
		"A magus target writes its declared outputs as it runs, so the tree is routinely dirty with files you did not edit; `git add -A` sweeps those and build residue into the commit with no signal that it happened. There is deliberately no `magus vcs` wrapper; load the magus-vcs-hygiene skill if not already loaded."

	// An ADVISORY, not a deny: two genuinely independent targets in one line is real work
	// (`magus run build api ; magus run test docs`), and only the dependency graph knows
	// which case this is. What the guard can see is that the chain is worth questioning.
	adviseChainedRun = "Run the LAST target and let its dependencies pull the rest in. Targets compose through ctx.needs, so a chain is usually ONE invocation: here `lint` needs `format` needs `generate`, and `magus run lint .` alone runs all three in order.\n" +
		"Check what a target already pulls in before chaining: `magus run <target> <project> --dry-run` prints the plan without executing it.\n" +
		"`magus affected ci` counts as one of these: it runs the whole pipeline over everything the diff reaches, so a build immediately before it does that work twice - and the second run can trip MGS4007 on an output the first one left behind.\n" +
		"Each extra invocation reloads the workspace and re-evaluates every magusfile. And `magus run` takes one TARGET and many PROJECTS (`magus run build api web`), so two targets never belong in one call either."

	// Both messages LEAD with the replacement, per this file's rule: the agent
	// reached for a filter because it wanted one specific thing, so the actionable
	// correction is the flag that returns that thing, not the prohibition.
	outputPipeDeny = "Ask magus for the field instead of filtering its output:\n" +
		"  -o name                      the ids/names, one per line\n" +
		"  -o json                      the full record\n" +
		"  -o template=<go-template>    one field, e.g. -o template='{{.Ref}}'\n" +
		"A pipe also replaces the exit status with the last stage's, so a failing gate reads as exit 0.\n" +
		outputGuardTail
	outputRedirectDeny = "magus already wrote the log; you do not need to capture it:\n" +
		"  magus query output <ref>     the failing target's full captured log (this one may be redirected)\n" +
		"  .magus/logs/<hash>.log       the path, printed by the failure itself\n" +
		"  -o json --tee <file>         mirror structured output to a file (never console text)\n" +
		"--silent prints the diagnostics and the log path on failure, and a redirect throws exactly that away.\n" +
		outputGuardTail
	throwawayCopyDeny = "Run from the workspace and name the project: `magus run <target> <project>`. A different workspace is `--root <path>`; a pristine tree is a throwaway `git worktree`, not a copy.\n" +
		"A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits."
	outputGuardTail = "The one exception is `magus query output <ref>`: a raw captured log has no schema to project."

	// ADVISE, never deny: reading the revision is legitimate, and checkpoint is a
	// strict SUPERSET rather than a substitute, so there is nothing to block. That
	// also rules out the third deny trigger, which needs an exact equivalent.
	checkpointGuardContext = "magus workspace: `magus vcs checkpoint` identifies the working state (`-o name` prints `<revision>` clean, `<revision>+<digest>` dirty) and records it on the activity trail, so a later reader knows what the work was looking at.\n" +
		"A revision alone cannot identify a DIRTY tree: two workers on the same commit with different uncommitted work read as identical, and the patch digest is what separates them. checkpoint RESOLVES AND RECORDS with no tag, no stash, no ref, and no file, so one nobody keeps has cost nothing."

	// ADVISE, never deny: re-resolving dependencies is legitimate work with no
	// exact magus equivalent to route to, so the third deny trigger does not apply.
	// It is here because relock is under-discoverable - a reserved charm nothing
	// prompts for - and a lockfile refreshed outside magus is a write the cache and
	// the affected set never saw.
	//
	// The covering TARGET is not named and cannot be: relock is magus vocabulary,
	// but which target carries the dependency work is the workspace's.
	//
	// Shared with the raw-tool deny, which appends it when the denied command is
	// also a re-resolution (`go mod tidy` is both), so the charm is named whichever
	// rule answers first.
	relockAdvice = "Run the covering target with the relock charm (`magus run <target>:relock <project>`) so the dependency rewrite happens inside magus, cached and visible to affected tracking. `magus describe targets` lists what this workspace defines.\n" +
		"relock is the reserved charm for rewriting DEPENDENCY state, the way rw covers derived output: reproducible from a clean checkout is rw, dependent on what a registry serves today is relock. ci strips both, so a gate verifies the committed lockfile rather than refreshing it."
	relockGuardContext = "magus workspace: " + relockAdvice

	// Advice, not a deny: it wastes a line, it does not break anything.
	echoOnSuccessAdvice = "Drop the `&& echo ...` and read the exit status: it already says the command passed, and a message that prints only on success adds nothing."

	// Advise, not deny: timing a command is legitimate, and the point is that magus
	// already answered the question better than the shell can.
	timedMagusAdvice = "magus times itself: drop `-s` and it prints each target's duration and a `(cached, 320ms)` or `(ran, 5m28s)` verdict. `time` around a silent run measures the wall clock magus already reported, and hides which targets replayed, which is usually the thing being asked."

	// Advise, not deny: bounding a run is legitimate, and no deny trigger applies -
	// nothing is unrecoverable, nothing is written, and the equivalent is close but
	// not exact.
	timeoutMagusAdvice = "magus has its own: `magus run <target> <project> --timeout 5m` (and the same flag on `magus affected`). It cancels the run rather than signaling the process, so the error names the target (`run ci: timed out after 5m`) and it logs elapsed/remaining heartbeats while the run is still going.\n" +
		"An external `timeout` sees one opaque process: it cannot say which target was still running, and the SIGTERM lands wherever the run happened to be."
)

// denySharedStash explains why an unqualified stash restore is refused.
func denySharedStash(verb string) string {
	return "Name the entry you meant: read `git stash list`, then `git stash " + verb + " stash@{N}`.\n" +
		"Bare `git stash " + verb + "` acts on stash@{0}, and the stash stack belongs to the REPOSITORY rather than your worktree: the top entry is often another checkout's work, and " + verb + " applies or destroys it."
}

func denyWholeTree(op string) string {
	return "Verify in place. No magus run needs a clean tree: `magus run <target> <project>`, or `magus affected ci` for everything the diff reaches. If you truly need a pristine tree, use a throwaway git worktree.\n" +
		"whole-tree " + op + " destroys uncommitted and untracked work, including a concurrent agent's. See the magus-vcs-hygiene skill."
}

// magusInvokes reports whether any resolved command runs magus carrying all of the given
// words among its arguments.
//
// Position-free among magus's OWN arguments on purpose: magus accepts its global flags
// before the verb, so `magus --root . notes edit x` and `magus -o json diff --ack` are the
// same invocations the anchored patterns are written for, and both walked past them. Tokens
// after a bare `--` are passed through to a spell's tool, not read by magus, so they are
// excluded - otherwise `magus run go::go-test . -- notes edit` reads as note-authoring.
func magusInvokes(cmds []guardCommand, words ...string) bool {
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

// magusRuleFires answers off the resolved argv when the line parses and off the anchored
// pattern when it does not - the same split gitGuard and gitGuardFallback make, and for the
// same reason: a line with no AST to read must still be judged.
func magusRuleFires(cmds []guardCommand, parsed bool, command string, fallback *regexp.Regexp, words ...string) bool {
	if parsed {
		return magusInvokes(cmds, words...)
	}
	return fallback.MatchString(command)
}

// evaluateBashGuard applies the guard rules in severity order.
//
// magus denies on three independent triggers and explains everything else:
//
//  1. it cannot be UNDONE - the whole-tree git rules;
//  2. it WRITES INTO THE WORKING TREE - codegen, formatters with -w/--fix,
//     dependency files, build output landing on a tracked path;
//  3. it has an EXACT WORKING EQUIVALENT - raw `go test` against `magus run test`.
//
// Trigger 2 is the firm one: reading through the wrong tool costs a cache hit,
// writing through it corrupts the workspace's account of itself. Trigger 3 is
// denied not because the command is dangerous but because the replacement is
// complete, which makes the deny free.
//
// A deny is only legitimate once the replacement it names actually works - the
// reverted grep deny removed a capability magus had nothing to route to. Do not
// add one without checking that path end to end.
// translateSearch suggests the magus command most likely to answer the caught search, so the
// advisory hands back something to TRY rather than a principle to weigh - a generic "use the
// graph" loses to muscle memory; `magus refs HandleFoo` does not.
//
// It ROUTES by the pattern's shape, because the right verb differs: a diagnostic code and a
// Buzz op have graph answers that `magus query` finds but `magus refs` (compiled-language
// symbols only) misses - measured against real session history, where routing every identifier
// to refs sent MGS2011 and mgs_* greps to a dead end.
//
// It is deliberately a suggestion, not a promise. grep is TEXTUAL and the graph is SEMANTIC:
// they agree only when the pattern names something the graph models, and a bare word like
// "error" matches the identifier shape without being one. So the wording hedges - an empty
// result means the pattern was text, and grep was the right tool. A grep-accurate translator is
// not worth building; an honest "try this" is. Empty when the pattern cannot be isolated, in
// which case the generic reason still ships.
func translateSearch(cmds []guardCommand) string {
	var pat string
	for _, c := range cmds {
		switch c.Name {
		case "grep", "egrep", "fgrep", "rg", "ag":
			pat = firstSearchPattern(c.Args)
		}
		if pat != "" {
			break
		}
	}
	if pat == "" {
		return ""
	}
	switch {
	case diagnosticCodeRe.MatchString(pat):
		return "`" + pat + "` is a diagnostic code - `magus query " + pat + "` finds it and its docs.\n\n"
	case buzzOpRe.MatchString(pat):
		return "`" + pat + "` reads like a Buzz op - `magus query " + pat + "` finds the spell functions that define it (refs covers compiled-language symbols only).\n\n"
	case bareIdentRe.MatchString(pat):
		return "`" + pat + "` reads like a symbol - try `magus refs " + pat + "` (exact when it is one) or `magus query " + pat + "` for a domain entity. An empty result means it was text, not a symbol, and grep is right.\n\n"
	default:
		return "Try `magus query \"" + pat + "\"` over node ids, labels, and docs. If it misses, the text is not in the graph and grep is the right tool.\n\n"
	}
}

// firstSearchPattern returns a grep/rg pattern: the first operand that is neither a flag nor a
// flag's value. -e/-f (and their long forms) take the next word as the pattern, so that word is
// returned directly rather than skipped as a flag value.
func firstSearchPattern(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-e" || a == "-f" || a == "--regexp" || a == "--file" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

func evaluateBashGuard(command string) bashGuardVerdict {
	// The program rules judge PARSED commands; the rest read the line as written,
	// because they are about its SHAPE - a pipe, a redirect, a cd before a magus
	// call - rather than which program runs.
	//
	// A matched git rule that only ADVISES is held, not returned: returning here
	// let a trailing `git commit` downgrade a deny to an advisory. Deny always
	// outranks advise, whichever rule saw the line first.
	cmds, parsed := parseGuardCommands(command)
	// Authoring a note is refused before anything else, because it is the one rule whose
	// whole point is that it holds on EVERY surface: the path rule sees file writes, and a
	// note authored from piped prose is a command, so only this catches it.
	if magusRuleFires(cmds, parsed, command, guardNotesWriteRe, "notes", "edit") {
		return bashGuardVerdict{Deny: denyNotesAuthor}
	}
	// Beside the notes rule and for the same reason: both refuse an agent AUTHORING a
	// human's statement, and both have to hold however the command is spelled.
	if magusRuleFires(cmds, parsed, command, guardReadAckRe, "diff", "--ack") {
		return bashGuardVerdict{Deny: denyReadAck}
	}
	if guardSedInPlaceRe.MatchString(command) {
		return bashGuardVerdict{Deny: denySedInPlace}
	}
	if guardScriptedRewriteRe.MatchString(command) {
		return bashGuardVerdict{Deny: denyScriptedRewrite}
	}
	var advisory bashGuardVerdict
	// Held rather than returned, like the git advisories below: a deny found later on the
	// same line outranks it.
	if guardChainedRunRe.MatchString(command) {
		advisory = bashGuardVerdict{Context: adviseChainedRun}
	}
	if parsed {
		if v, matched := gitGuard(cmds); matched {
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

	rawToolCmd, rawToolDeny := firstRawToolDenied(command)
	switch {
	case rawToolDeny:
		match, _ := rawToolMatch(rawToolCmd)
		reason := runGuardContextFor(match)
		// `go mod tidy` is both a covered spell op and a dependency re-resolution.
		// The deny answers first, so it is the only text the reader gets, and
		// routing into magus without naming the charm that makes the write legal
		// sends them to a target that would refuse to do it.
		//
		// The WHOLE line is scanned, not just the denied command: `go test ./... &&
		// npm update` denies on the first half, and the reader was never told the
		// second half rewrites a lockfile - the deny is the only text they get.
		if isDependencyMutation(rawToolCmd) || slices.ContainsFunc(cmds, isDependencyMutation) {
			reason += "\n" + relockAdvice
		}
		return bashGuardVerdict{Deny: explainDeny(command, rawToolCmd, reason)}
	case magusInThrowawayCopy(command):
		return bashGuardVerdict{Deny: throwawayCopyDeny}
	case magusPipedToFilter(command):
		return bashGuardVerdict{Deny: outputPipeDeny}
	case magusRedirected(command):
		return bashGuardVerdict{Deny: outputRedirectDeny}
	case parsed && slices.ContainsFunc(cmds, isDependencyMutation):
		return bashGuardVerdict{Context: relockGuardContext}
	case guardCdMagusRe.MatchString(command):
		return bashGuardVerdict{Context: cwdGuardContext}
	case guardDocSearchRe.MatchString(command):
		return bashGuardVerdict{Context: docSearchAdvice, Kind: advisoryDocSearch}
	case guardCodeSearchRe.MatchString(command):
		return bashGuardVerdict{Context: translateSearch(cmds) + searchGuardReason, Kind: advisoryCodeSearch}
	case guardEchoOnSuccessRe.MatchString(command):
		return bashGuardVerdict{Context: echoOnSuccessAdvice}
	case guardTimedMagusRe.MatchString(command):
		return bashGuardVerdict{Context: timedMagusAdvice}
	case guardTimeoutMagusRe.MatchString(command):
		return bashGuardVerdict{Context: timeoutMagusAdvice}
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
func runGuardContextFor(match guardToolMatch) string {
	return fmt.Sprintf("Run it through magus instead: `magus run <target> <project>`. `magus describe targets` lists what this workspace calls its targets (`-o name` for just the names); add `--dry-run` to print the exact command without running it.\n"+
		"Only to pass flags to the tool itself, the one-op form forwards everything after `--`: `magus run %s::%s [<project>] -- <tool-args>`.\n\n%s", match.spell, match.operation, runGuardContext)
}
