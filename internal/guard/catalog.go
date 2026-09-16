package guard

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// The rule catalog: what this workspace enforces, as data a reader can list.
//
// Until this existed there was no way to ask the question. A rule announced itself only
// by firing, so the only route to the set was to trip each one, and a verdict naming
// `stage-all` gave a reader a string with nowhere to take it. That is the gap
// docs/doctrine.md calls out under "automation you can interrogate": a verdict magus
// cannot account for is a defect in the same class as a wrong answer.
//
// It is a TABLE rather than a method on each rule because the rules are not objects: they
// are functions returning prose, spread over four files and matched in a switch. A table
// beside the constants is the one shape where a missing entry is visible, and
// TestEveryRuleIsCatalogued is what makes it visible.
//
// Catches is deliberately short and deliberately not the verdict text. The verdict tells
// one caller what to run instead; this tells a reader what the rule is FOR, so a list of
// thirty-eight reads as a set of conventions rather than a wall of remediation.

// RuleDoc is one catalogued rule: its stable name, which tier it lands on, and what it
// fires on.
// The json tags are the -o json and -o template field names, so they follow the
// lowercase convention every other magus output uses rather than the Go spelling.
type RuleDoc struct {
	// Name is the slug a verdict reports and a reader looks up.
	Name string `json:"name"`
	// Decision is the tier: "deny" or "advise". A rule never moves between them without
	// the move being the point of the change, so it is recorded rather than derived.
	Decision string `json:"decision"`
	// Catches says what the rule fires on, in one line, in the reader's terms.
	Catches string `json:"catches"`
	// Why is the reasoning behind the rule, for a reader who wants to disagree with it
	// or to understand why the replacement is better rather than merely different.
	//
	// It is where the rationale the three-line verdict budget displaced lives. Those
	// paragraphs were true and load-bearing and cost more than they returned at the
	// moment of refusal, when the reader is interrupted and wants the command; here they
	// are read by someone who came looking. Empty for a rule whose one line says all of
	// it, which is most of them: a Why that restates Catches is worse than none.
	Why string `json:"why,omitempty"`
}

// denyRuleDocs documents every rule that REFUSES. Ordered by name here only for review;
// Rules sorts what it returns.
var denyRuleDocs = []RuleDoc{
	{Name: string(denyRuleBusyWait), Decision: "deny",
		Catches: "a loop polling for work you started, which announces its own completion",
		Why: "A backgrounded command is tracked and announces its own completion, so starting it and doing something else is strictly better than watching it. " +
			"The loop also has no bound of its own: past the tool timeout it is BACKGROUNDED rather than killed, and goes on polling a condition that may never arrive, because a run that failed early never prints the line being grepped for. " +
			"Several have had to be killed by hand. Waiting on something OUTSIDE this machine, a remote queue or a deploy nobody here started, is what a host's monitor surface is for."},
	{Name: string(denyRuleCacheDirWrite), Decision: "deny", Catches: "a write into this checkout's magus cache dir, which magus alone owns"},
	{Name: string(denyRuleCaptureFilter), Decision: "deny",
		Catches: "a filter over a run capture or log, which cuts the failure block apart",
		Why: "A failure prints five lines together: the target, the cause, an output ref, the command that reads that ref, and the command to reproduce it. " +
			"A filter keeps the one line it matched and drops the rest, so `grep 'cause:'` keeps the symptom and discards the ref that reads the whole log two lines below it. " +
			"A range print (`sed -n '1,200p'`) is a filter too: it cuts by POSITION, and the block sits wherever the run left it. " +
			"Read the file whole, or give the run an output contract up front with `-o jsonl --tee <file>` and query that. Reading the whole file is not a filter and stays allowed."},
	{Name: string(denyRuleCd), Decision: "deny",
		Catches: "a `cd` before a magus command, when the project is an argument",
		Why: "magus is CWD-relative, so a leading `cd` is how the right command lands on the wrong project. " +
			"The project is an argument and is written bare (`magus run build libs/foo`); a DIFFERENT workspace is `--root <path>`, and `magus where <name>` resolves a fuzzy name. " +
			"A `cd` prefix also relocates every later command on the line and re-fires shell chpwd hooks, mise among them, which can fail on an empty command. " +
			"A host shell tool that genuinely needs a different directory for one call has a working_directory field, which does not rewrite the command line."},
	{Name: string(denyRuleCIWatch), Decision: "deny",
		Catches: "a `gh` invocation that BLOCKS until CI finishes, rather than asking once",
		Why: "Watching costs a wake-up per completion and buys nothing, because GREEN CHANGES NOTHING: a person merges, not the watcher. " +
			"Measured in one session: four watches, every one green, every one a turn spent re-reading a verdict that was already true. " +
			"It is also the second half of a duplicate, since a gate already run locally is the same command on the same tree, and waiting for CI to agree pays twice for one answer. " +
			"`gh pr list --state open --json number,mergeable,statusCheckRollup` answers every open pull request in one call. " +
			"Iterating on a run that is already RED is the case worth following, and polling that command serves it too."},
	{Name: string(denyRuleInterpreterRewrite), Decision: "deny",
		Catches: "an inline interpreter rewriting a file this tree already carries",
		Why: "A `python -c` or `node -e` that reads a tracked file, substitutes, and writes it back is an edit nobody reviewed: it lands before a diff exists, and the script that produced it is gone the moment the line ends. " +
			"The editor tool reads the file first and reports what it changed, which is the same edit with a record of itself. " +
			"It fires on the WRITE, not the interpreter: a one-liner that computes something, prints it, or creates a file the tree does not carry is untouched, and so is anything under a scratch path."},
	{Name: string(denySpawnUnbriefed), Decision: "deny",
		Catches: "a subagent spawned before the multi-agent skill loaded",
		Why: "Four decisions a spawn cannot be corrected for later are made before the child starts: which worktree it is cut from, which lease grades its writes, which model it runs, and that git stays with the orchestrator. The magus-multi-agent skill carries all four. " +
			"Measured across 2,147 session transcripts: zero loads, under every name and every variant, while the two skills a hook DEMANDS loaded 415 times. A skill nobody is required to read is a skill nobody reads, and this workspace had already written that down before measuring it again here. " +
			"It grades the session, never the prompt: it asks whether a marker file exists, so a handed-over prompt that merely mentions a denied command is untouched. Load Skill(magus-multi-agent) once and every later spawn in the session passes."},
	{Name: string(denyBuzzUnbriefed), Decision: "deny",
		Catches: "the first write to a .buzz file in a session that has not read the Buzz skill",
		Why: "Buzz is in no model's training data, so what gets written is Go or TypeScript with the serial numbers filed off, and enough of it parses to reach review. " +
			"Six errors in one session, by an agent with this repository open throughout: fs\\glob indexed as strings when it returns [Path]; .append on a list declared without mut; the ternary form, which upstream-strict parsing rejects outside --embedded; archive\\extract, which does not exist; a missing `import \"fs\"`; and .sub sliced by character on BYTE-indexed strings. Reading first supplies every one of them. " +
			"It grades the session, not the file: one Skill(magus-buzz-write) and every later Buzz write passes. Reads are never gated, since reading is how the language gets learned."},
	{Name: string(denyRuleMergeSideCheckout), Decision: "deny",
		Catches: "a checkout of one merge side over a conflicted file, which discards the merge",
		Why: "It reads like \"undo my edit to this file\" and is not: during a merge the working-tree copy IS the merge, and this replaces it wholesale with one side. " +
			"Measured here: `git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution silently dropped the branch's own half of a merged feature. Nothing failed, the gate stayed green, and the feature could not fire until someone read the code days later. " +
			"For a generated file, `magus vcs resolve` settles every conflicted one by regenerating. To take one side deliberately, say which: `git checkout --ours` or `--theirs`. To keep the merged result, it is already in the file."},
	{Name: string(denyRuleNotesAuthor), Decision: "deny",
		Catches: "an agent authoring a human's note, whose only provenance is who wrote it",
		Why: "A note is the one thing in the knowledge graph nothing here corroborates later, so its only provenance is the person who wrote it and signed the commit. " +
			"That is why it is refused however the write is spelled: `capture` files a review transcript as a note and `promote` writes a memory record into the SHARED store, where the commit puts a person's name on prose they never read. " +
			"`magus memory put <name>` is the agent-writable store, where every entry cites a ref a later reader can re-run."},
	{Name: string(denyRuleOutputPipe), Decision: "deny",
		Catches: "magus output piped into a filter, when magus projects the record itself",
		Why: "magus projects its own record, so the filter is answering a question the command takes a flag for: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field, `-s` to silence progress. " +
			"The half a reader cannot discover by trying again is the exit status: a pipe takes it from the LAST stage, so a failing magus reads as exit 0 and nothing says so."},
	{Name: string(denyRuleOutputRedirect), Decision: "deny",
		Catches: "magus output redirected to a file, which the run log already holds",
		Why: "There is no legitimate shape of this against magus. Silencing and keeping are the only two intents and magus has a lever for each: `--silent` says nothing until something fails, and `-o json --tee <file>` keeps the STRUCTURED output rather than console text, which is not a format anything should parse. " +
			"A target run persists its whole log either way and prints a ref for it, so capturing the console is redundant."},
	{Name: string(denyRuleProcessPoll), Decision: "deny",
		Catches: "a process table inspected to wait on magus work the lock already reports",
		Why: "A magus run holds a project lock and announces itself, and `magus status --watch=15s` reads that same lock state continuously: holder PID, command, age, waiters. " +
			"`pgrep`, `pidof` and `ps` invent a second waiter that races the real one, has no bound of its own, and answers a question the lock message already answered."},
	{Name: string(denyRuleRawTool), Decision: "deny",
		Catches: "a toolchain command a spell already wraps, run outside the cache",
		Why: "magus covers these exactly and adds cache, sandbox and affected tracking, so the refusal costs nothing: `magus run <target> <project>`, and `magus describe targets -o name` lists what this workspace calls them. " +
			"Tool flags go after `--`. A raw WRITE (codegen, a formatter with -w/--write/--fix, `go mod tidy`, build output landing on a tracked path) is the firm half: it leaves the owning target reporting drift it did not cause, and that has no exceptions. " +
			"The guard reads the command being RUN, so a wrapper, a `VAR=value` prefix or `bash -c` reaches the same verdict. " +
			"It was an advisory first, and changed behavior zero times over a long session while leaving the Go build cache poisoned by uninstrumented runs, which is why it denies."},
	{Name: string(denyRuleReadAck), Decision: "deny",
		Catches: "an agent stamping a read receipt, which records that a PERSON read a change",
		Why: "This is not a permission an agent is missing: there is no spelling of it an agent may use, because an agent stamping the changeset would make the measure mean nothing for everybody, including the human relying on it. " +
			"Report what is unread instead: `magus diff --impact` names every changed file carrying no receipt, and `magus diff -o json` puts read_state on each file for a caller to branch on."},
	{Name: string(denyRuleScriptedRewrite), Decision: "deny",
		Catches: "a scripted substitute-and-write, which cannot tell your symbol from a dependency's",
		Why: "A regex cannot tell YOUR symbol from a dependency's symbol of the same name. " +
			"A `\\.Sum\\b` rewrite aimed at one proto field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`, and the damage is written before any diff is read. " +
			"The graph knows which is which and a pattern never can: `magus refs <symbol> --occurrences` returns verified sites, per file, with columns. " +
			"Run `magus graph build` first if refs reports a project not-indexed, because that verdict means unknown rather than absent, and taking it for \"no matches\" is how a rename misses half its sites. " +
			"Rewriting raw TEXT (prose, a config value, a string literal) has no graph equivalent; say so and use an editor tool."},
	{Name: string(denyRuleSedInPlace), Decision: "deny",
		Catches: "`sed -i`, whose two spellings destroy each other's work across platforms",
		Why: "`sed -i` is not portable and the two spellings destroy each other's work. " +
			"GNU reads `sed -i 's/x/y/' f` as an edit; BSD and macOS read that same script as the BACKUP SUFFIX and take the next argument as the script. " +
			"`sed -i '' ...` is the macOS spelling and makes GNU edit nothing. " +
			"So a command that works here mangles the file on the next machine, by WRITING, before anyone reads a diff. " +
			"An editor tool reads the file first and reports what it changed, and for a whole-tree rename `magus refs <symbol> --occurrences` gives column-precise sites a pattern cannot. Reading with sed is untouched."},
	{Name: string(denyRuleSharedStash), Decision: "deny",
		Catches: "a bare stash push or pop, on a stack every worktree shares",
		Why: "The stash stack is shared across every worktree of a repository, so a bare `pop` can take an entry another session pushed, and a bare `push` can bury one. " +
			"Prefer a temporary commit to set work aside. If you must stash, push with a unique `-m` tag, capture your entry's SHA, and restore with `apply <sha>` rather than `pop`."},
	{Name: string(denyRuleSiblingCheckout), Decision: "deny",
		Catches: "a magus command relocated into another checkout, judging a tree nobody ships",
		Why: "A binary links the spell sources of the tree it was built from, so a verdict it reaches about a DIFFERENT checkout describes a tree that exists nowhere, and anything it regenerates lands there unmarked. " +
			"Run magus from the workspace it belongs to and name the project as an argument; a different workspace is `--root <path>`."},
	{Name: string(denyRuleStageAll), Decision: "deny",
		Catches: "`git add -A`, which sweeps regenerated output into a commit about something else",
		Why: "A magus target writes its declared outputs as it runs, so the tree here is routinely dirty with files you did not edit. " +
			"`-A` sweeps those and any build residue into a commit about something else, with no signal that it happened. " +
			"Measured: one such call put 69 files, a whole regenerated docs site plus five untouched source files, into a commit about four collection methods. " +
			"`magus vcs add` classifies every dirty path against the declared output globs, keeps a source change and the outputs it produced together, and reports anything undeclared instead of staging it."},
	{Name: string(denyRuleSymbolSearch), Decision: "deny",
		Catches: "a recursive text search for a symbol the index defines and can enumerate",
		Why: "It fires only when the index can VOUCH for the name: the symbol is defined here and no project's index is older than its sources. " +
			"On those terms `magus refs <symbol> --occurrences` knows every definition and reference, including the generated and cross-language ones a pattern misses. " +
			"Searching raw TEXT is untouched and has its own answer: `magus refs --text <pattern> [<path>...]` is a literal substring search with grep's exit codes, scoped by the same trailing paths."},
	{Name: string(denyRuleThrowawayCopy), Decision: "deny",
		Catches: "a run inside a temp or scratchpad copy, which leaves the real tree unverified",
		Why: "A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits. " +
			"No magus run needs a clean tree; run from the workspace and name the project. If you genuinely need a pristine tree, use a throwaway `git worktree add`, not a copy."},
	{Name: string(denyRuleWholeTree), Decision: "deny",
		Catches: "a whole-tree VCS reset, checkout, restore or clean, which cannot be undone",
		Why: "These destroy uncommitted and untracked work across the WHOLE tree, including a concurrent session's, and nothing recorded anywhere can give it back. " +
			"It is the one category where an over-eager refusal is the safe direction, which is why an unparsable line falls back to the pattern rather than passing. " +
			"Verify in place instead: no magus run needs a clean tree."},
	{Name: string(denyRuleWorktreeRemove), Decision: "deny",
		Catches: "removing a worktree, which may hold another session's uncommitted work",
		Why: "A worktree is where another session may be working right now, and its uncommitted changes live nowhere else. " +
			"Check it is clean first with `git -C <path> status`, and remove it only once you know what it holds."},
}

// advisoryDocs documents every rule that EXPLAINS rather than refuses. Several are
// agent-shaped by construction (a lease, a focus boundary, host wiring): they are
// catalogued anyway, because a reader asking what this workspace enforces is owed the
// whole set rather than the half that happens to apply to them today.
var advisoryDocs = []RuleDoc{
	{Name: string(advisoryCheckpointState), Decision: "advise", Catches: "a command reaching for a tree's identity, which a revision alone cannot give"},
	{Name: string(advisoryCodeSearch), Decision: "advise",
		Catches: "a repo-wide text search that the symbol graph may answer better",
		Why: "A text match misses the generated, indirect and cross-language references the graph knows about, so the two agree only when the pattern is a real symbol. " +
			"It ADVISES rather than refuses because that is exactly the case it cannot check in advance: an empty semantic result means the pattern was text, and grep was the right tool after all. " +
			"Pick by the question: `magus refs <symbol>` for a code symbol, `magus query \"<terms>\"` for a domain entity, `magus refs --text <pattern>` for raw text."},
	{Name: string(advisoryDocSearch), Decision: "advise", Catches: "a search through markdown, where headings are indexed as doc sections"},
	{Name: string(advisoryFocus), Decision: "advise", Catches: "a read or write outside the paths the running job declared"},
	{Name: string(advisoryGateRepeat), Decision: "advise", Catches: "the gate run again soon after it passed, repeating work already done"},
	{Name: string(advisoryGeneratedWrite), Decision: "advise", Catches: "a hand edit to a declared output, which the next run overwrites"},
	{Name: string(advisoryGraphStale), Decision: "advise", Catches: "a graph read while the index is older than the sources it describes"},
	{Name: string(advisoryHookWiring), Decision: "advise", Catches: "a write to the host wiring that decides whether these rules run at all"},
	{Name: string(advisoryInstalledSkill), Decision: "advise", Catches: "a write to an installed skill copy, which re-installing discards"},
	{Name: string(advisoryLeaseInvalid), Decision: "advise", Catches: "a call naming a lease this workspace's job store does not declare"},
	{Name: string(advisoryLeaseTerminal), Decision: "advise", Catches: "a call naming a lease whose row has already finished"},
	{Name: string(advisoryMemoryWrite), Decision: "advise", Catches: "a write to a memory file, where the memory surface is the way in"},
	{Name: string(advisoryNewFile), Decision: "advise", Catches: "a new file in a directory whose naming has settled"},
	{Name: string(advisoryNewSourceDir), Decision: "advise", Catches: "a new file that opens a directory, which is a boundary rather than a file"},
	{Name: string(advisoryPrecedent), Decision: "advise",
		Catches: "a hunt for one distinctive name, which refs answers with verified sites",
		Why: "A precedent hunt is a search for one distinctive name, and it is the search the graph answers best: refs lists verified sites, so you land on working code instead of assembling it from grep hits. " +
			"Measured over 1,499 sessions: 42% of new files were preceded by one of these, 71% in subagent sessions, where only 12.9% reached for a magus verb at all."},
	{Name: string(advisoryPushGate), Decision: "advise", Catches: "a push with no gate run since the last change"},
	{Name: string(advisoryRegenSource), Decision: "advise", Catches: "a hand edit to a file a target regenerates"},
	{Name: string(advisoryRevertClassify), Decision: "advise",
		Catches: "a revert that has not classified what it is reverting",
		Why: "Reverting regenerated output is the wrong default. An agent that did not hand-edit a `gen/` file concludes it is not \"its\" change and discards it, but a generate target rewriting its declared outputs is the system working, and those outputs belong in the same commit as the source that moved them. " +
			"The honest test is whether the SOURCE changed, not whether anyone typed into the output. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged; that drift is environmental and worth reporting rather than discarding."},
	{Name: string(advisoryScopeDrift), Decision: "advise", Catches: "a write into a project this session has no dependency edge to"},
	{Name: string(advisorySkillSource), Decision: "advise", Catches: "a write to an installed skill copy rather than to its source"},
	{Name: string(advisorySourceRead), Decision: "advise", Catches: "an unbounded source read the symbol index has already answered"},
	{Name: string(advisoryStageClassify), Decision: "advise", Catches: "staging without classifying, when generated and source differ"},
	{Name: string(advisoryStaleBinary), Decision: "advise",
		Catches: "a verdict from a binary older than the rules in the tree around it",
		Why: "The failure this catches is silent and convincing: change a rule, run the guard, read `pass`, conclude the rule does not work, when what answered was the previous build. " +
			"That happened twice in one session, both times because a rebuild had been skipped without anyone noticing. It is appended to a DENY every time rather than held, because a refusal from rules the caller has already changed is the case it exists for."},
	{Name: string(advisoryUnleasedWrite), Decision: "advise", Catches: "a write magus cannot attribute while a fleet is running"},
}

// Rules returns the whole catalog, denies first and each tier sorted by name: the order a
// reader scans, with the tier that blocks them at the top.
func Rules() []RuleDoc {
	out := make([]RuleDoc, 0, len(denyRuleDocs)+len(advisoryDocs))
	out = append(out, denyRuleDocs...)
	out = append(out, advisoryDocs...)
	slices.SortFunc(out, func(a, b RuleDoc) int {
		// Deny sorts before advise, which is neither alphabetical nor accidental: a
		// reader opening this list is asking what stops them first.
		if a.Decision != b.Decision {
			if a.Decision == "deny" {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// Rule looks one rule up by name, reporting false for a name nobody declares. The
// comparison is exact: a near-miss that resolved would report a different rule's terms
// as this one's.
func Rule(name string) (RuleDoc, bool) {
	name = strings.TrimSpace(name)
	for _, r := range Rules() {
		if r.Name == name {
			return r, true
		}
	}
	return RuleDoc{}, false
}

// advisoryKinds is every enrolled kind, for the test that pairs the catalog against the
// constants. Declared here rather than derived, for the same reason the docs are: a kind
// that nothing lists is a kind nothing can miss.
var advisoryKinds = []hint.MarkerKind{
	advisoryStaleBinary, advisoryCodeSearch, advisoryDocSearch, advisorySourceRead,
	advisoryPrecedent, advisoryStageClassify, advisoryUnleasedWrite, advisorySkillSource,
	advisoryRegenSource, advisoryGraphStale, advisoryGateRepeat, advisoryFocus,
	advisoryHookWiring, advisoryNewFile, advisoryLeaseTerminal, advisoryLeaseInvalid,
	advisoryGeneratedWrite, advisoryInstalledSkill, advisoryMemoryWrite,
	advisoryScopeDrift, advisoryNewSourceDir,
}

// advisoryRuleNames are the advisories that name themselves WITHOUT enrolling in the
// once-per-session gate, so they are denyRuleName values and not kinds. The catalog test
// walks them beside advisoryKinds: what makes a rule catalogable is having a name, and
// these have one.
var advisoryRuleNames = []denyRuleName{
	advisoryPushGate, advisoryRevertClassify, advisoryCheckpointState,
}

// advisoryNames is every advisory's name, held or not: the set the catalog must cover.
func advisoryNames() []string {
	out := make([]string, 0, len(advisoryKinds)+len(advisoryRuleNames))
	for _, k := range advisoryKinds {
		out = append(out, string(k))
	}
	for _, n := range advisoryRuleNames {
		out = append(out, string(n))
	}
	return out
}
