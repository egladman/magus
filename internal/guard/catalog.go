package guard

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/types"
)

// The rule catalog: what this workspace enforces, as data a reader can list.
//
// Until this existed there was no way to ask the question. A rule announced itself only
// by firing, so the only route to the set was to trip each one, and a verdict naming
// `stage-all` gave a reader a string with nowhere to take it. That is the gap
// docs/doctrine.md calls out under "automation you can interrogate": a verdict magus
// cannot account for is a defect in the same class as a wrong answer.
//
// It is a registry of rows rather than a method on each rule because the rules are not
// objects: they are functions returning prose, spread over four files and matched in a
// switch. A row beside the constants is the one shape where a missing entry is visible,
// and TestEveryRuleIsCatalogued is what makes it visible. Each row is its own top-level
// declaration, so two changes that add two rules add two declarations instead of both
// editing one table; denyDoc and adviseDoc file it into the registry Rules reads.
//
// Catches is deliberately short and deliberately not the verdict text. The verdict tells
// one caller what to run instead; this tells a reader what the rule is FOR, so a list of
// thirty-eight reads as a set of conventions rather than a wall of remediation.

// ruleDocEntry is one registered row and the tier it was filed under.
type ruleDocEntry struct {
	doc types.RuleDoc
	// advisory rows are the names a workspace can raise to deny.
	advisory bool
	// enrolled advisories are held to one firing per session by a hint.MarkerKind gate.
	enrolled bool
}

var ruleDocRegistry []ruleDocEntry

// denyDoc files the row of a rule named by a denyRuleName constant, whatever it does by
// default: the decision a row shows is builtin.Defaults', never a literal kept here.
func denyDoc(d types.RuleDoc) types.RuleDoc {
	ruleDocRegistry = append(ruleDocRegistry, ruleDocEntry{doc: d})
	return d
}

// adviseDoc files the row of a rule that only ever EXPLAINS. Several are agent-shaped by
// construction (a lease, a focus boundary, host wiring): they are catalogued anyway,
// because a reader asking what this workspace enforces is owed the whole set rather than
// the half that happens to apply to them today. enrolled says the advisory is held to one
// firing per session; the rest speak on every match.
func adviseDoc(d types.RuleDoc, enrolled bool) types.RuleDoc {
	ruleDocRegistry = append(ruleDocRegistry, ruleDocEntry{doc: d, advisory: true, enrolled: enrolled})
	return d
}

var ruleDocBackgroundLeft = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleBackgroundLeft),
	Catches: "an agent finishing while shell commands it ran in the background are still running",
	Why: "A host does not stop a finished subagent's background tasks, and once the subagent has reported nobody is told when one ends or that it is still running: a transcript search left behind by a scout ran for four hours. " +
		"At a subagent's stop magus reads the subagent's own transcript for the shell commands it ran in the background (run_in_background, a foreground call the host moved to the background, a watcher's command) that no completion notice, stop call or watcher expiry has ended, and refuses the stop naming each by id, start time, age and command. " +
		"At a root session's stop it advises instead, and only of tasks older than 30 minutes: a root wakes on a task's completion notice, so a young task is one it is waiting on, and the person is there to decide. " +
		"Reads the host's transcript by shape, so a host whose transcript records no background task in that shape is never refused. Set to advise, a subagent's stop is advised too.",
})

var ruleDocBackgroundUnbounded = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleBackgroundUnbounded),
	Catches: "a shell command run in the background with nothing to end it",
	Why: "A backgrounded command outlives the agent that started it, and the host stops it only when asked. " +
		"It fires when the wiring reports that the host runs the call in the background and some command in the line is not bounded: `timeout N` or `gtimeout N` in front of it (stock macOS ships neither; Homebrew's coreutils installs `gtimeout`), a magus target run (bounded by its target's timeout), or a builtin that returns at once. A pipeline is bounded by its first command; a while or until loop never is. " +
		"timeout signals its own process group, so a child that starts its own session escapes it; `--kill-after` ends a command that ignores TERM. A watcher's command is bounded by its own expiry and is not judged here.",
})

var ruleDocBacktickSubstitution = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleBacktickSubstitution),
	Catches: "a backtick command substitution, which inside double quotes runs a command",
	Why: "Inside double quotes a backtick starts a command substitution, so a pattern or a message carrying a literal backtick runs code: the backtick pairs with the next one anywhere on the line, and everything between them becomes one command. " +
		"A `grep` whose pattern and a later stage's pattern each hold a triple backtick pair them into ONE substitution that swallows the file operand and the rest of the pipeline. What is left is a `grep` with no file, reading a stdin that never closes. " +
		"A literal backtick belongs in single quotes, where it is text. A substitution is written `$(...)`, which nests and cannot pair with a stray backtick. " +
		"Backticks inside single quotes and inside a quoted heredoc (`<<'EOF'`) are text and never fire; neither does a line that does not parse.",
})

var ruleDocBusyWait = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleBusyWait),
	Catches: "a while or until loop that sleeps, or a for loop that sleeps until it breaks out",
	Why: "A backgrounded command is tracked and announces its own completion, so starting it and doing something else is strictly better than watching it. " +
		"Whatever the loop tests (a process, a file test on a task's output file, a grep of it) and whatever else each pass runs, a loop that sleeps is waiting: wait for the task's completion notice, or `magus job wait <job>` for a job; `magus status --watch` is for a person watching. " +
		"A loop probing a process the caller did not start (`kill -0`, the process table, `magus status` or a job row) gets no such announcement, so its deny names the record magus keeps of that work instead: `magus job wait <job>` for a job, `magus status --watch` for the project lock. " +
		"The loop also has no bound of its own: past the tool timeout it is BACKGROUNDED rather than killed, and goes on polling a condition that may never arrive, because a run that failed early never prints the line being grepped for. " +
		"Several have had to be killed by hand. A host's watcher running the same loop is judged the same way, since it is the same poll that also wakes the agent on every line and at expiry. " +
		"A shell script is judged by its content, so `bash wait.sh` and a write of wait.sh get the verdict the loop would get typed inline; so do the output-pipe, output-redirect and unknown-env rules.",
})

var ruleDocCacheDirWrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleCacheDirWrite),
	Catches: "a write into this checkout's magus cache dir, which magus alone owns",
})

var ruleDocChainedRun = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleChainedRun),
	Catches: "magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast",
	Why: "A pipe of magus runs keeps what the chain was for: stages whose projects overlap run in order, a failed stage stops the ones after it, and the last stage exits with the first failure (MGS3030), so no `set -o pipefail` is needed. " +
		"It differs from `&&` in one way, which the deny states: a stage on disjoint projects runs alongside the others and finishes even after an upstream stage fails. The pipeline still exits red. " +
		"The same target chained over several project sets is served the pipe too, which keeps the order the chain chose; the deny names the one call for when order does not matter. " +
		"It advises instead, and says why, wherever the pipe would do something else. That covers `||`, a redirect or any other command on the line, and a later `affected` stage, which reads its projects from the diff before the upstream's writes land: `affected generate:rw` stays its own call ahead of `affected ci`. " +
		"It also covers a later `run` naming no projects, which would inherit the upstream's; a stage that reads stdin, takes no locks, or writes -o output upstream; stages on different binaries or roots; " +
		"a stage after `go-build` or `build` in magus's own checkout, since every stage starts at once and would run the ./magus being replaced; and Windows, where no pipe proves its upstream.",
})

var ruleDocClaimedDeclaration = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleClaimedDeclaration),
	Catches: "a leased edit landing in a declaration another live job claims (`run.go#executeStages`)",
	Why: "A write path may claim one declaration of a file, so two jobs can start on one file and integrate in order. " +
		"The claim holds only if an edit into the other job's declaration is caught before it lands, because afterwards both diffs touch it and neither applies over the other. " +
		"The edit is applied to the file in memory, or a whole-file write's content is compared with the file it replaces, and the changed lines are placed by the same diff-driver matching the job footprint uses, so the declaration this names is the one `magus job wait` would report. " +
		"It fires only for a job-bound writer whose own claims in the file do not name the declaration, and only when another live job claims a declaration of that file; a write that lands in the writer's claims, in no one's, or above the first declaration passes. " +
		"An edit that does not apply to the file on disk, and a file whose lines cannot be placed, stay graded by path alone.",
})

var ruleDocExitStatusEcho = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleExitStatusEcho),
	Catches: "a line ending by printing an exit status, which the harness already reports",
	Why: "The harness reports a nonzero exit on its own and success needs no confirmation, so `cmd; echo \"rc=$?\"` adds lines and no information. " +
		"It also misreports: the echo exits 0, so the line as a whole passes whatever `cmd` did. " +
		"Every spelling of that ending fires: `echo`/`printf` of `$?` with literal text, to the console or stderr; the same through a capture (`rc=$?; echo $rc`); `cmd || echo \"failed $?\"`, where the echo runs exactly when cmd failed; and `${PIPESTATUS[...]}`, whose answer is `set -o pipefail` or no pipe. " +
		"`exit $?`, `[ $? -ne 0 ]`, an echo mid-script, one after `&&`, and one redirected to a file keep the status for later logic and are untouched. " +
		"Chain with `&&`, or make separate calls, when a failure must not be masked.",
})

var ruleDocFilterWithoutInput = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleFilterWithoutInput),
	Catches: "a filter with no file, pipe or redirect, which reads a stdin nothing feeds",
	Why: "A filter given no input reads stdin, and under an agent harness stdin is the harness's own: where the harness holds it open, nothing writes to it and nothing closes it, so the call waits past the tool timeout and goes on waiting in the background. " +
		"Name the input: a file operand, a pipe into the command, or a `<`, `<<` or `<<<` redirect on it or on a loop or block around it. " +
		"It reads each tool's own flag grammar, so `grep -e pat file`, `jq --arg k v . f` and `head -n 5 file` are fed, and `tr`, `tee` and `xargs` fire whenever nothing feeds them, because their operands are never input. " +
		"A recursive grep with no path passes: GNU grep, macOS's BSD grep and the ugrep a host may put behind `grep` all search the working directory then. " +
		"What the guard cannot classify passes, because it refuses only what it can prove: an unknown flag, an unquoted expansion that may split into several words, `jq -n`, an awk program with a BEGIN block, a command inside a function body. " +
		"ripgrep with no path passes for the same reason: it searches the working directory unless stdin is a pipe or a file, which a hook cannot see.",
})

var ruleDocGrepReader = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleGrepReader),
	Catches: "a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body",
	Why: "A context count guesses at a declaration's length: too short cuts the body off and costs another call, too long spends lines on whatever follows. " +
		"`magus refs X --definition --source` prints the declaration whole, numbered and checked against the index. Where the index cannot vouch for the name, the deny serves `sed -n <first>,<last>p <file>` instead, the declaration's own lines from a parse of the file the search reads (a named file or glob, or under a directory the files the index last saw name it). " +
		"The single-file allowance symbol-search gives `grep -n 'func X' f.go` does not apply: with -A, -B or -C the search is the read. " +
		"A deny resting on the index advises instead while the graph describes another tree, as graph-stale; one parsed from a named file does not. " +
		"It fires only when every alternative is a definition lookup (`func X`, `func (r *T) X`, `type X`, `type X struct`) and a declaration of each name is found; a search for uses, a case-insensitive one, or one with any text alternative is left to the search rules. A pipe after it is named as not reproduced.",
})

var ruleDocInterpreterRewrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleInterpreterRewrite),
	Catches: "an inline interpreter rewriting a file this tree already carries",
	Why: "A `python -c` or `node -e` that reads a tracked file, substitutes, and writes it back is an edit nobody reviewed: it lands before a diff exists, and the script that produced it is gone the moment the line ends. " +
		"The editor tool reads the file first and reports what it changed, which is the same edit with a record of itself. " +
		"It fires on the WRITE, not the interpreter: a one-liner that computes something, prints it, or creates a file the tree does not carry is untouched, and so is anything under a scratch path. " +
		"Only a write's destination counts: the path an `open(..., 'w')`, a pathlib or `writeFile` writer, an in-place flag or an awk redirect names, a variable read through its assignment. A tracked path the program carries as data, in a list it prints to stdout or a report it writes to scratch, is not one. A destination spelled from no literal at all, such as argv, is read as the interpreter's operands.",
})

var ruleDocConflictRewrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleConflictRewrite),
	Catches: "an inline interpreter rewriting a file that holds merge conflict markers",
	Why: "A script that opens a file for writing truncates it there, so one that raises before it writes leaves the file empty, and mid-merge the working copy is the only copy of the half-resolved file. " +
		"It denies by default because the policy that would raise interpreter-rewrite to deny is often what fails to load mid-merge, its own files conflicted too. " +
		"It fires on two shapes: a write whose destination, read the way interpreter-rewrite reads one, is a file in the workspace holding conflict markers; and a write whose destination cannot be read at all, from a program that names such a file or carries a conflict marker itself, which is resolving hunks by script. " +
		"A destination under a scratch path, a new file, and a program that only reads are untouched. Your editor tool reads the file first and reports what it changed; where the workspace carries a script that resolves hunks, the verdict names it.",
})

var ruleDocSpawnUnbriefed = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denySpawnUnbriefed),
	Catches: "a subagent spawned before the multi-agent skill loaded",
	Why: "Four decisions a spawn cannot be corrected for later are made before the child starts: which worktree it is cut from, which lease grades its writes, which model it runs, and that git stays with the orchestrator. The magus-multi-agent skill carries all four. " +
		"A skill nobody is required to read is a skill nobody reads, so the spawn waits on the load instead of trusting it. " +
		"It grades the session, never the prompt: it asks whether a marker file exists, so a handed-over prompt that merely mentions a denied command is untouched. Load Skill(magus-multi-agent) once and every later spawn in the session passes.",
})

var ruleDocBuzzUnbriefed = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyBuzzUnbriefed),
	Catches: "the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill",
	Why: "Buzz is in no model's training data, so what gets written is Go or TypeScript with the serial numbers filed off, and enough of it parses to reach review. " +
		"Typical errors of an agent with this repository open throughout: fs\\glob indexed as strings when it returns [Path]; .append on a list declared without mut; the ternary form, which upstream-strict parsing rejects outside --embedded; archive\\extract, which does not exist; a missing `import \"fs\"`; and .sub sliced by character on BYTE-indexed strings. Reading first supplies every one of them. " +
		"It grades the session, not the file: one Skill(magus-buzz-lang) and every later Buzz write passes. Reads are never gated, since reading is how the language gets learned, so `magus buzz <file>` and `magus buzz -t <file>` run something that already exists and go untouched.",
})

var ruleDocArchitectureUnbriefed = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyArchitectureUnbriefed),
	Catches: "an agent's next call after a structure question, or a new directory, before the architecture skill",
	Why: "A structure question is answered from the workspace's own edges: what imports what, where a cycle closes, which layer a package sits in, how far a change reaches. An agent reading files one at a time answers it from the files it happened to open, and proposes a boundary nobody needed. The magus-architecture-review skill is how the graph gets asked. " +
		"Two acts arm it. A message the person submits with structure vocabulary (architecture, boundaries, layering, coupling, imports, a new package, where something belongs, blast radius) marks the session, and the agent's next graded call (a shell line, a read, a search, an edit, a magus tool call) waits on the load. A write that creates a new directory waits on it too, since that write draws a boundary whatever was asked. " +
		"A load counts for the agent that made it: a subagent in that session loads the skill itself, and a parent's load does not brief its child. Load Skill(magus-architecture-review), or its -full twin, once and the agent's later calls pass. " +
		"It stands down on a host whose wiring does not report skill loads, where nothing could ever clear it; the new-directory advisory still speaks there.",
})

var ruleDocPushUngated = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRulePushUngated),
	Catches: "a push at a commit with no green gate: the person is asked, a leased worker refused",
	Why: "The advisory this replaced fired on EVERY push, having read nothing: it told a caller who had just gated and a caller who had never gated the same sentence, which is a toll rather than a reminder. " +
		"This one reads the run log, so the finding is a fact: which invocations ran the gate, which commit each was built from, and how it finished. That is what makes stopping the call legitimate here where the rest of this tier only advises. " +
		"It matches on the COMMIT and not the exact tree, deliberately: an exact match would expire on the first comment typo after a green run, which is the delta the cadence already says to push, and a rule that fires there is one people route around. " +
		"Publishing work in progress is legitimate and indistinguishable from an oversight, so a session no job lease binds gets the verdict `ask`: the host's own approval prompt puts the push in front of the person, and approving it publishes. A marker the agent types is not consent, so nothing it says clears this. " +
		"A session bound to a lease is a worker, and workers do not publish: it gets `deny`, and nobody is asked. " +
		"A push that follows a commit, merge, cherry-pick, am, rebase, reset or pull on the same line (git, hg, sl or jj; joined by `;`, `&&`, `||`, a newline or a subshell) is denied for everyone: the revision is read before the line runs, so a gate recorded at that HEAD says nothing about the commit that goes out. Run the commit alone and push in the next call, where the push is graded at the real HEAD.",
})

var ruleDocHumanOnly = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleHumanOnly),
	Catches: "a magus run reaching a human_only target: the person is asked, a leased worker refused",
	Why: "Some targets cost what only a person may decide to spend: a benchmark against a paid model API, a suite that drives real hosts on real logins. " +
		"A magusfile marks one with `human_only` and a reason saying what a run costs. Each `magus run` and `magus affected` on the line is expanded the way the engine would run it: over the projects it names, or every project for `affected` or a run naming none, and down each target's ctx.needs chain. " +
		"A run naming a human-only target itself, from a session no job lease binds, gets the verdict `ask`: the host's own approval prompt shows the reason, the target's recorded p75 and run count and whether it is cached, and approving runs it once. A hook that cannot put an ask in front of the person refuses instead. " +
		"A leased worker is refused and nobody is asked: workers do not spend for the orchestrator. " +
		"A run that reaches one through another target's chain is refused for everyone, naming each human-only target it reaches: a run reporting green over work it skipped is a pass the push gate would trust. " +
		"A command magus itself suggested is judged too, since the person, not the suggestion, owns the cost. " +
		"A person's own terminal never meets the guard, so nothing changes there. Set to advise, the reason is told and the run proceeds.",
})

var ruleDocInlineAlias = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleInlineAlias),
	Catches: "a VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs",
	Why: "git expands `git -c alias.x='reset --hard' x` into `git reset --hard`, so the word every git rule reads as the subcommand names nothing, and a reset, clean or push would pass unjudged. " +
		"The guard refuses the line rather than judging it as every destructive verb at once: the arguments those rules read come from the alias body too, and `--config-env` or an inline `include.path`, which loads a file that may define aliases, keeps the body off the line altogether. " +
		"The other backends are held to the same bar: hg's and sl's `--config alias.x=...`, jj's `--config aliases.x=...`, and the options that load config from a file or a TOML string (`--config-file`, sl's `--configfile`, jj's `--config-toml`). " +
		"Spell out the command the alias stands for. Any other config setting is untouched, and each tool's global options (`git -C`, `hg -R`, `jj --at-op`, `--no-pager`) are read past the way the tool reads them.",
})

var ruleDocMagusTimeout = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleMagusTimeout),
	Catches: "a magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside",
	Why: "A timeout wrapper ends magus with a signal from outside, so the run log records no cause, a target's tools can outlive the process that started them, and the next reader sees a run that stopped rather than one that timed out. " +
		"magus bounds a run itself: `--timeout <dur>` bounds the whole run and cancels its own process tree, `--target-timeout <dur>` caps each target, and `--stall-timeout <dur>` stops a run making no progress. " +
		"A `run` or `affected` is served the same argv with `--timeout` and the wrapper's duration (600 becomes 10m). Any other verb is served bare: it holds no lock worth waiting on, since a held lock refuses at once (MGS3009). " +
		"`magus buzz` has no bound of its own, so a wrapped script is advised rather than refused. " +
		"It reads every flag the wrapper takes and the launchers around it (`env`, `nice`, `nohup`, `sh -c`, `eval`). " +
		"A wrapper sending QUIT or ABRT is left alone, since that is how a goroutine dump is taken from a hung run, and so is a verb that runs until interrupted: `watch`, `events`, `job watch` and `--version`.",
})

var ruleDocMergeSideCheckout = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleMergeSideCheckout),
	Catches: "a checkout of one merge side over a conflicted file, which discards the merge",
	Why: "It reads like \"undo my edit to this file\" and is not: during a merge the working-tree copy IS the merge, and this replaces it wholesale with one side. " +
		"`git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution silently drops the branch's own half of a merged feature: nothing fails, the gate stays green, and the loss surfaces only when someone reads the code. " +
		"For a generated file, `magus vcs resolve` settles every conflicted one by regenerating. To take one side deliberately, say which: `git checkout --ours` or `--theirs`. To keep the merged result, it is already in the file.",
})

var ruleDocNotesAuthor = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleNotesAuthor),
	Catches: "an agent authoring a human's note, whose only provenance is who wrote it",
	Why: "A note is the one thing in the knowledge graph nothing here corroborates later, so its only provenance is the person who wrote it and signed the commit. " +
		"That is why it is refused however the write is spelled: `capture` files a review transcript as a note, where the commit puts a person's name on prose they never read. " +
		"Read the store instead, and say what belongs in it so the person can write it.",
})

var ruleDocCredentialVerb = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleCredentialVerb),
	Catches: "an agent minting, printing, rotating or revoking a credential through the CLI",
	Why: "An agent holds the token it was given, and a session that mints another holds a grant nobody handed it. " +
		"Refused: the console and connector token `create` and `revoke` commands, `magus graph export --open --follow` (its link carries a sign-in code), and `magus config token print`, `generate` and `revoke`, the operator token that reaches token management. " +
		"It holds however the binary is spelled: `./magus`, a path, `go run ./cmd/magus`, or inside a `$(...)` substitution. " +
		"This is a seatbelt for a harness that opted in, not a boundary: a process running as the user can reach the same files.",
})

var ruleDocTokenState = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleTokenState),
	Catches: "an agent reading or writing the token secrets: the operator token file or the token store",
	Why: "The operator token file (`magus/mcp_token` in the user state dir) and the token store (`magus/tokens.d`) are the credentials the server checks, so reading one hands a session a grant and writing one mints a token. " +
		"Refused for file writes and shell commands alike: an editor write aimed at them, and any shell line that names them, whatever the command (`cat`, `cp`, a redirect, an interpreter's inline script). A path is matched by name anywhere in a word and by resolving it against where the call runs. " +
		"A bare listing passes (`ls`, `du`, `stat`, `test` of the state dir or a token file), alone or piped into a text filter such as `head` or `grep`: it shows file names, and none is a secret, since the operator file is always `mcp_token` and a store entry is `<token name>.json`, the name `magus config mcp connector ls` already prints. A listing inside a substitution, or piped into anything else (`| xargs cat`), is refused like any mention. " +
		"Reads through a host's read tool are not graded: that hook only records, by contract. This is a seatbelt, not a boundary against a process running as the user.",
})

var ruleDocOutputPipe = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleOutputPipe),
	Catches: "magus output piped into a filter, when magus projects the record itself",
	Why: "magus projects its own record, so the filter is answering a question the command takes a flag for: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field, `-s` to silence progress. " +
		"The half a reader cannot discover by trying again is the exit status: a pipe takes it from the LAST stage, so a failing magus reads as exit 0 and nothing says so. " +
		"It denies on `run`, `affected`, `x` and every verb that is not a graph read. A read-only graph verb (`refs`, `query`, `explain`, `describe`) gets the same answer as the graph-pipe advisory, and a help request (`--help`, `-h`) passes: neither loses a failure.",
})

var ruleDocOutputRedirect = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleOutputRedirect),
	Catches: "magus output sent to a file or discarded, which the run log already holds",
	Why: "Silencing and keeping are the only two intents and magus has a lever for each: `--silent` says nothing until something fails, and `-o json --tee <file>` keeps the STRUCTURED output rather than console text, which is not a format anything should parse. " +
		"A target run persists its whole log either way and prints a ref for it, so capturing the console is redundant. " +
		"It judges where each stream ENDS. Either stream landing in a file fires, and so does stdout landing in /dev/null. `2>/dev/null` fires on `run`, `affected` and `x`, which write their failure block (cause, output ref, reproduce line) to stderr, and passes on every other verb, whose stderr carries at most an error line the exit status also reports. " +
		"`2>&1` alone passes: both streams still reach the reader.",
})

var ruleDocProcessPoll = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleProcessPoll),
	Catches: "a process table inspected to wait on magus work the lock already reports",
	Why: "A magus run holds a project lock and announces itself, and `magus status --watch=15s` reads that same lock state continuously: holder PID, command, age. " +
		"`pgrep`, `pidof` and `ps` invent a poll with no bound of its own that answers a question the lock message already answered.",
})

var ruleDocPolicyUnloaded = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRulePolicyUnloaded),
	Catches: "a push, merge, spawn or state-writing magus verb while the guard policy that judges it does not load",
	Why: "Misconfiguration is an error: when neither the working tree nor its approved copy loads, the workspace rules that judge these calls are not running, and letting the call through would read as judged. " +
		"It fires only when the last policy that did load in this cache registered the seam's rule; with no record there was never a rule to lose, and the call passes. " +
		"Every other call still runs on the built-in rules, so the fix stays runnable. The verdict names the likeliest fix for the caller: a rebuild in a checkout of magus, a placed binary for a leased worker, an update elsewhere.",
})

var ruleDocRawTool = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleRawTool),
	Catches: "a toolchain command a spell already wraps, run outside the cache",
	Why: "magus covers these exactly and adds cache, sandbox and affected tracking, so the refusal costs nothing: `magus run <target> <project>`, and `magus describe targets -o name` lists what this workspace calls them. " +
		"Tool flags go after `--`. A raw WRITE (codegen, a formatter with -w/--write/--fix, `go mod tidy`, build output landing on a tracked path) is the firm half: it leaves the owning target reporting drift it did not cause, and that has no exceptions. " +
		"The guard reads the command being RUN, so a wrapper, a `VAR=value` prefix or `bash -c` reaches the same verdict, and `go -C <dir> <verb>` reads the same as `go <verb> -C <dir>`. " +
		"Asking a tool for its usage or version runs nothing over the tree and passes: `go clean --help`, `gofmt -h`, `go help clean`, `govulncheck -V`. The help flag has to be the tool's own, last on the line, after a subcommand a spell renders; one handed to a program is work, so `go run main.go --help` and `go test ./... -args --help` are refused. " +
		"`gofmt -l` and `gofmt -d` pass too, without `-w`: they list or diff and write nothing, so they leave no drift for the format target to report. `go list` passes because no spell renders it. " +
		"One command is exempt, in a checkout of magus itself: `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`, alone on its line with no wrapper and no other prefix, in a checkout root that has no `magus` binary yet, is advised rather than refused, because a fresh checkout has no other way to get its first binary. " +
		"It runs the real go-build target (its generate steps and stamped link) instead of a bare link, so the first binary is the one every later build would make. `--no-cache` because that target's key cannot express the embedded-spell ordering and once replayed a binary missing tools; Go's content-addressed build cache stays on, `-trimpath` matches the target's own build so packages compile once, and `GOEXPERIMENT=jsonv2` because magus refuses to compile without it and a fresh checkout cannot count on mise to set it. " +
		"Every other raw go command in that root, a bare `go build -o magus ./cmd/magus` included, is refused and served the bootstrap. Once the binary exists the deny applies again and names `./magus run go-build .`. " +
		"None of this is a leased worker's: there is one binary per base, the main session builds it and places a copy in each worker checkout, so a worker is refused every build of it and served the one command that copies the main checkout's binary into its own, `<main checkout>/magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id> --from <main checkout>`. " +
		"A checkout that cannot load its own sources (MGS1021) has a second exemption, because no target can run there and the bootstrap fails to compile against generated files that lag their sources: " +
		"alone on its line, with no environment prefix but `GOEXPERIMENT`, the relink MGS1021 prints (`go build [-trimpath] -o magus ./cmd/magus`) and the generators the `*_generate` targets run (`go generate <package>` inside the checkout, `go run [-trimpath] ./cmd/magus-utils <generator>`, never its release subcommands) are advised rather than refused, with a binary or without. " +
		"The guard learns this by loading the workspace, and only for one of those lines; the binary judging is the checkout's own `./magus` when there is one. Once the workspace loads, they are refused again. " +
		"A run outside the target also leaves the Go build cache holding uninstrumented results.",
})

var ruleDocAgentSignOff = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleAgentSignOff),
	Catches: "an agent stamping a read receipt or closing an attention request, which only a person may do",
	Why: "This is not a permission an agent is missing: there is no spelling of either an agent may use, because an agent stamping the changeset or closing its own block would make the measure mean nothing for everybody, including the human relying on it. " +
		"Report what is unread instead: `magus diff --impact` names every changed file carrying no receipt, and `magus diff -o json` puts read_state on each file for a caller to branch on. " +
		"Waiting on a request instead: say you are waiting on its id and hand it back; `magus session dispose <id>` is a person's to run.",
})

var ruleDocScriptedRewrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleScriptedRewrite),
	Catches: "a scripted substitute-and-write, which cannot tell your symbol from a dependency's",
	Why: "A regex cannot tell YOUR symbol from a dependency's symbol of the same name. " +
		"A `\\.Sum\\b` rewrite aimed at one proto field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`, and the damage is written before any diff is read. " +
		"The graph knows which is which and a pattern never can: `magus refs <symbol> --occurrences` returns verified sites, per file, with columns. " +
		"Run `magus graph build` first if refs reports a project not-indexed, because that verdict means unknown rather than absent, and taking it for \"no matches\" is how a rename misses half its sites. " +
		"Rewriting raw TEXT (prose, a config value, a string literal) has no graph equivalent; say so and use an editor tool. " +
		"A script file is judged by its program: `python3 p.py`, and a write of p.py, get the verdict the same program would get inline. A program whose every named path lies outside the workspace is untouched. " +
		"An APPEND passes, since it adds to the end and cannot mangle a line already there: a heredoc appended with `cat >> f <<EOF` or `tee -a`, whatever program its text documents, and a program whose every file write opens in append mode (`open(p, 'a')`, perl's `'>>'`) or goes to its own stdout.",
})

var ruleDocSedInPlace = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSedInPlace),
	Catches: "`sed -i`, whose two spellings destroy each other's work across platforms",
	Why: "`sed -i` is not portable and the two spellings destroy each other's work. " +
		"GNU reads `sed -i 's/x/y/' f` as an edit; BSD and macOS read that same script as the BACKUP SUFFIX and take the next argument as the script. " +
		"`sed -i '' ...` is the macOS spelling and makes GNU edit nothing. " +
		"So a command that works here mangles the file on the next machine, by WRITING, before anyone reads a diff. " +
		"An editor tool reads the file first and reports what it changed, and for a whole-tree rename `magus refs <symbol> --occurrences` gives column-precise sites a pattern cannot. Reading with sed is untouched.",
})

var ruleDocSharedStash = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSharedStash),
	Catches: "a bare stash push or pop, on a stack every worktree shares",
	Why: "The stash stack is shared across every worktree of a repository, so a bare `pop` can take an entry another session pushed, and a bare `push` can bury one. " +
		"Prefer a temporary commit to set work aside. If you must stash, push with a unique `-m` tag, capture your entry's SHA, and restore with `apply <sha>` rather than `pop`.",
})

var ruleDocSiblingCheckout = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSiblingCheckout),
	Catches: "a magus command relocated into another checkout, judging a tree nobody ships",
	Why: "A binary links the spell sources of the tree it was built from, so a verdict it reaches about a DIFFERENT checkout describes a tree that exists nowhere, and anything it regenerates lands there unmarked. " +
		"Run magus from the workspace it belongs to and name the project as an argument; a different workspace is `--root <path>`.",
})

var ruleDocStaleBinary = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleStaleBinary),
	Catches: "a call that changes state while the magus judging it cannot load this tree's guard policy",
	Why: "A magus older than the tree, or one answering for a checkout with no ./magus, cannot load the magusfile, so none of the workspace's rules run and a fresh worktree has no record of them to fall back on. The magusfile visibly registering a guard rule is enough to refuse. " +
		"Edits, spawns, pushes, writing MCP tools and every command that changes state wait until a magus loads the tree. A line passes only when it proves read-only (every program reads, no redirect writes a file, nothing is computed by a substitution), is the fix (`./magus run go-build .`, the relink, the bootstrap over a ./magus that cannot load the tree, each alone on its line), or is a read-only scout recording its work (`magus job exec`, `magus job exit`, `magus buzz --record`). " +
		"A leased worker is never told to build: there is one binary per base and the main session places it, so the worker's one fix is the placement it is served, `<main checkout>/magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id> --from <main checkout>`. A load failure no newer binary fixes, such as a typo in a checkout with its own ./magus, is policy-unloaded's instead.",
})

var ruleDocStageAll = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleStageAll),
	Catches: "a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output",
	Why: "A magus target writes its declared outputs as it runs, so the tree here is routinely dirty with files you did not edit. " +
		"`-A` sweeps those and any build residue into a commit about something else, with no signal that it happened, and `-u` reaches the same outputs: it stages every TRACKED change across the whole tree, which is the same sweep minus files that are merely untracked, and a target's declared outputs are ordinarily tracked already. " +
		"One such call has put a whole regenerated docs site and untouched source files into a commit about something else. " +
		"`magus vcs add` classifies every dirty path against the declared output globs, keeps a source change and the outputs it produced together, and reports anything undeclared instead of staging it.",
})

var ruleDocSymbolSearch = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSymbolSearch),
	Catches: "a text search of the tree for a symbol, a declaration or a diagnostic code the graph answers",
	Why: "Each alternative of the pattern is classified by what it looks for, and the deny names every classification and why, so a false positive is disputable from the message alone. " +
		"A name is a declaration lookup (`func X`, `type X`, `class X`, `function X`, `const X`), a CamelCase identifier, or a short name the syntax marks as one: a word search (`\\bX\\b`, -w), a qualified member (`pkg.X`, `\\.X`) or an assignment, when it is capitalized. " +
		"Text is everything else: a plain word, which is as likely prose; snake_case, which in this tree is a config key, a JSON tag or a Buzz function; a lowercase call or member (`Mkdir(` is os.Mkdir as often as a local function); a standard-library member (`os.Rename`); a declaration of a lowercase word many share (`func main`); a string literal, a path, a hyphenated word or a regular expression. With -i a plain or marked short name widens to text, and a CamelCase one stays a name. " +
		"The deny fires when ANY alternative is a name the index defines, serving `magus refs X --occurrences` per name (`--definition --source` for a declaration lookup, which prints the body in place of the grep-then-sed pair) and `magus refs --text <literal> <paths>` for each literal text alternative, and `magus explain diagnostic:<code>` for a code the graph holds a node for. " +
		"A stale index still answers, serving `magus graph build --silent` first: it still knows the names it held, and it vouches for a declaration lookup or a CamelCase call it has not indexed yet, since that is the name a branch is adding; a bare word it does not hold stays text. An index goes stale on the first edit, so a rule that stood down for a stale one would let every symbol search through. Only a workspace with no symbol index at all is advised instead, since a deny there routes nowhere. " +
		"An index built at another revision is stale on the same terms; a rebase still underway turns the deny into graph-stale's advice, since a rebuild then would describe a tree about to move. " +
		"The search must reach the tree: a directory, a glob, or several files. One named file is a read and runs, with refs advised when the index vouches for the name; a definition lookup carrying -A, -B or -C is a read of the body, and grep-reader refuses it. " +
		"The files refs answers for are the languages a spell declares a symbol indexer for (here Go and TypeScript), read from the spell catalog. A search of stdin, Markdown, a log, Buzz (source no indexer reads, so a Buzz `fun` or `object` lookup is text too), a directory holding none of those languages (a skills tree, fixtures), a dot-directory (.github, .git), node_modules, a revision, or a tree outside the workspace runs. " +
		"A search of the tree carries the index's own answer when the index is current: every file under the searched paths with its occurrence count and lines, as `magus refs` prints them. It is refs' answer, not grep's: comments, strings and prose are not in it. " +
		"A pipe after the search is not reproduced. The deny still carries the unfiltered answer and says so: a model of sort, sed or awk substituted for the real tool diverges from it.",
})

var ruleDocSearchTranslation = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSearchTranslation),
	Catches: "a text search whose pattern a graph query provably answers with the same entities",
	Why: "The pattern is compiled in the tool's own dialect (BRE, ERE or fixed) and run against the graph's ids when the command is judged, so the deny names a query that was checked rather than one that looks equivalent, and carries that query's answer, bounded to twenty results and a count, so the refused search costs nothing. " +
		"These shapes qualify. A pattern that can only match MGS codes (`MGS30[23]`, `MGS30..`, `MGS302[0-9]\\|MGS303[0-9]`), over any path in the workspace, becomes `magus query kind=diagnostic 'id=~^diagnostic:...$'`, and a single literal code keeps symbol-search's `magus explain diagnostic:<code>`. " +
		"A pattern selecting every Markdown heading of the files searched (`^#`, `^#\\+`), when those lines match the section nodes the graph holds file for file and none sits in a code fence, becomes `magus query kind=docsection 'id=~^docsection:<file>#'`. " +
		"A search of a magusfile whose every hit declares a target the graph holds becomes `magus explain target:<project>:<name>`. " +
		"A search of one Go file whose every hit declares a symbol the index holds (`^func`, `^func Test`, `func (s \\*Store)`) becomes `magus explain file:<path>`, with the names and their lines inline. " +
		"A file-finding call whose every file is a node the graph holds becomes `magus query kind=file 'id=~^file:...'`: a pattern when one selects exactly those files, else the files enumerated when there are twenty or fewer. The proof walks what the call walks, so it holds whatever revision the index was built at. It covers `find` (-name, -path, their negations, -type f, -maxdepth), `fd` (a name pattern or glob, -e, -t f, -d, smart case), `rg --files` (-g and -t), `ls -R <dir>`, and an `ls <dir>` whose every visible entry is a file node or a directory holding one, which becomes `magus query 'id=~^(?:file|dir):<dir>/[^/]+$'`. " +
		"`git ls-files [<dir|glob>]`, alone or piped into a search of its paths, is proved against what version control tracks: the graph indexes only some tracked files and never an untracked one, so the deny answers for the indexed ones and names every other match; past twenty such files it is silent. " +
		"A metadata flag (-l, -a, -t, -S), hidden or ignored files (fd -H, rg -uu), an untracked-files question (`--others`), one named file (a tracked check), an inverted or counted filter, a file the graph does not index, or a walk past the budget is silent. " +
		"A host's own content and file search tools are judged as the rg and find lines they stand for, read by the shape of their input. " +
		"A pipe after the search is not reproduced, except the search a tracked listing is piped into: the deny carries the query's unfiltered answer and says so, rather than a model of the filter that could diverge from the real tool. " +
		"Anything else stays silent: -i, -v, -c, -l, -x, context flags, a stale index, a level-specific heading pattern, a BZZ code, a line anchor on a code, a heading inside a fence, one hit that is a call or a comment, stdin, or a tree outside the workspace. A graph describing another tree advises, as graph-stale, except for a listing, which the walk proves.",
})

var ruleDocThrowawayCopy = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleThrowawayCopy),
	Catches: "a run inside a temp or scratchpad copy, which leaves the real tree unverified",
	Why: "A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits. " +
		"No magus run needs a clean tree; run from the workspace and name the project. If you genuinely need a pristine tree, use a throwaway `git worktree add`, not a copy.",
})

var ruleDocVcsOffSwitch = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleVCSOffSwitch),
	Catches: "an agent's write setting vcs.enabled: false in a magus.yaml this workspace reads",
	Why: "With vcs off, vcs.Resolve returns no VCS, so the guard has no approved copy to compare a policy edit against and every workspace rule is read from the working tree alone. " +
		"Decided by parsing the proposed magus.yaml content, never by matching text. A person editing their own checkout, with no lease and no spawn ancestry, is untouched.",
})

var ruleDocUnknownEnv = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleUnknownEnv),
	Catches: "a retired or misspelled MAGUS_* variable handed to a command",
	Why: "A retired or misspelled name is ignored without a word, so the setting the caller meant never takes effect and nothing says so. " +
		"It fires on the names a command's environment receives: a `NAME=value` prefix, `env NAME=value`, `env -u NAME`, and `export`. It asks config.EnvVarProblem, the check magus's own startup refuses on (MGS1046), so a name the guard denies is one the binary would refuse, and one it cannot prove wrong passes both.",
})

var ruleDocBriefCommand = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleBriefCommand),
	Catches: "a spawn or continuation brief that teaches a command the guard denies",
	Why: "A worker runs the commands in its brief as written, so a denied one is refused in every worker the brief reaches, or teaches each of them a way around the refusal. " +
		"Only what the brief presents as a command is graded, a fenced shell block or an inline code span, with the same rules a shell line gets. A line naming a command to forbid it (never, do not, denied, instead of) is passed over, and a `<placeholder>` reads as a word rather than a redirect.",
})

var ruleDocFocusRead = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleFocusRead),
	Catches: "a read outside the paths a focus lease was given",
})

var ruleDocHookWiringWrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleHookWiringWrite),
	Catches: "a leased or agent-attributed write to the hook wiring the guard is installed by",
})

var ruleDocLeaseGate = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleLeaseGate),
	Catches: "a leased worker running the gate instead of the check it was assigned",
})

var ruleDocLeaseHarness = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleLeaseHarness),
	Catches: "a leased worker rewriting the harness skill trees that steer it",
})

var ruleDocLeaseRebind = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleLeaseRebind),
	Catches: "a leased worker rewriting who it is or what its own job row says",
})

var ruleDocLeaseUndeclared = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleLeaseUndeclared),
	Catches: "a call graded under a lease id the job store has no row for, or a binding it tombstoned",
})

var ruleDocLeaseVcs = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleLeaseVCS),
	Catches: "a worker lease mutating version control, or committing outside its own branch and checkout",
	Why: "The orchestrator lands every unit from the worker's tree, so a worker that pushes, stashes, reverts, resets, cleans, rebases, merges or removes a worktree changes the state it is integrated from, and a whole-tree revert destroys a sibling's uncommitted work. " +
		"A worker may run only what reads the repository or touches its own index and branch: status, diff, log and the other reads, add, restore of named paths, switch or checkout without a force, branch creation, a fetch into remote-tracking refs; any other verb, an alias from config included, is refused. " +
		"A remove or move of named paths (git rm and mv, hg and sl remove and rename) is allowed too, and lease-write grades each path as it grades rm and mv; one whose paths the line does not spell out, through `.`, a glob, pathspec magic, an hg pattern or a list read from a file, is refused. " +		"A commit is the one exception beyond those: allowed only in the lease's checkout_root, when that is a secondary checkout on a named branch other than the base, with any backend. " +
		"A git cherry-pick and a git apply are graded as a commit is, since `git diff | git apply -3 && git commit -C` lands the same change as the cherry-pick. " +
		"On a detached HEAD the refusal names `git switch <job>`, or `git switch -c <job>` when the orchestrator made no branch for the job. " +
		"Every spelling is placed alike: `git -C <dir>`, `--git-dir`, `GIT_DIR=`, a `cd` before it, and vcs\\cmd from a Buzz script under the lease. " +
		"Paths compare by device and inode, so a case variant or a firmlinked spelling of a checkout is that checkout. " +
		"A commit whose checkout cannot be read is refused, since it cannot be shown to be the worker's own. " +
		"vcs\\cmd may also run any git operation but a push in a repository standing wholly outside the job's checkout, such as a script's scratch repository: its directory, -C and the work tree a --git-dir implies are outside the checkout, its common git directory is not the job's, git reports that directory as its work tree, and neither -c nor --config-env nor GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR or GIT_INDEX_FILE is set. " +
		"A scratch repository inside the checkout, under .magus/ say, is refused like a submodule there: it holds the job's files under another git directory. " +
		"proc\\exec and proc\\shell refuse git, hg, sl and jj outright and name the vcs\\cmd call to make instead, so a Buzz script's version control is judged here. " +
		"A worker is a row with a parent, a lease a subagent holds, or a caller that names no session; only an identified root session holding a parentless row is the root.",
})

var ruleDocLeaseWrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleLeaseWrite),
	Catches: "a leased write outside its write paths, or into a path it was denied or another lease owns",
	Why: "The boundary is the orchestrator's declaration in the job store; the guard reads it back for a file write and for a shell line, in the same words. " +
		"A shell line's relative paths resolve from the directory it runs in. A remove or move (rm, rmdir, mv, git rm and mv, hg and sl remove and rename) is graded by every path it reaches: each operand, and everything a directory operand holds, must be inside the write paths and outside the deny paths. " +
		"A leased write before the job has reported the base it landed on is refused under the same name, since nothing yet records which revision the work applies to.",
})

var ruleDocWholeTree = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleWholeTree),
	Catches: "a whole-tree VCS reset, checkout, restore or clean, which cannot be undone",
	Why: "These destroy uncommitted and untracked work across the WHOLE tree, including a concurrent session's, and nothing recorded anywhere can give it back. " +
		"It is the one category where an over-eager refusal is the safe direction, which is why an unparsable line falls back to the pattern rather than passing. " +
		"Verify in place instead: no magus run needs a clean tree. " +
		"git's own help passes, because git documents that it prints usage without running: `git stash --help`, `git reset -h`, `git help stash`. It has to be the whole line, with nothing between the verb and the flag, so `git reset --hard --help`, `git -c ... stash --help`, a `VAR=value` prefix, `sh -c` or a pipe are judged as work.",
})

var ruleDocWorkerCheckOnly = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleWorkerCheckOnly),
	Catches: "a bound worker running a target other than its row's check or one writing its write paths",
	Why: "A worker's row names one check, and the orchestrator runs every other target serially, in its own tree, after the units land. " +
		"Two workers running the same target at once from two worktrees double the load and prove nothing the orchestrator's one run does not: the cache replays a run that has landed and never two in flight, and two trees share no key. " +
		"Allowed under a live lease: the row's check, with or without a charm and forwarded args; a `magus run` of a target that declares an output among the row's write paths, read from the workspace's own declarations (a `-generate` name or a charm stands in when the workspace cannot load); and every other verb, and every run that only reports (--plan, --dry-run, --graph). " +
		"`magus affected` is never the check, since it takes its projects from the diff. Rebuilding magus's own binary (`go-build .`) is not on the list either: there is one binary per base, the main session builds it and places a copy in the worker's checkout. An unbound caller, a row that owns the gate and a row declaring no check are untouched. " +
		"The same rule grades a spawn or continuation brief naming a live row (by `magus.lease=`, `job exec` or its JOB ID line): a brief telling that worker to run another target is refused under brief-command, quoting the line, before any worker exists.",
})

var ruleDocWorkerBackgroundRun = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleWorkerBackgroundRun),
	Catches: "a bound worker running a magus target in the background",
	Why: "A worker that backgrounds its check ends its turn with the run pending and parks there: nothing reports the result to the session that spawned it, and the background tasks pile up. " +
		"It fires when the wiring reports that the host runs the call in the background, for a `magus run` or `magus affected` by a worker under a live lease that does not own the gate. A run that only reports (--plan, --dry-run, --graph, --help) passes. " +
		"A wiring that does not report how the host runs a call leaves it silent, and so does a pre-authorized next, since the host's choice is the call's and not the line's. " +
		"The answer is a foreground call with a timeout that covers the run; check-timeout names one from the target's last real run, and a check longer than the host's foreground maximum is one to hand back for a narrower check.",
})

var ruleDocWorktreeRemove = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleWorktreeRemove),
	Catches: "removing a worktree magus cannot prove holds nothing that would be lost",
	Why: "A worktree may be where another session is working right now, and what it holds may exist nowhere else. " +
		"`git worktree remove` passes only when every condition holds: the path is a linked worktree of this repository (per `git worktree list`), not the main one and not the checkout the session runs in; " +
		"it has no modified, staged or untracked files (ignored files do not count); " +
		"every commit its HEAD carries is on a remote-tracking ref or the base branch, or every path those commits changed reads the same in the HEAD of the session's own checkout, so a cherry-pick under a new id counts, whatever state the job taken in it ended in; " +
		"no live job was taken in it, and an exited one serves `magus job wait <id>`; " +
		"and it is not locked, unless the session is the root and spawned that worktree's worker for a job that ended pass or fail, as its own spawn record shows. " +
		"That lock is the one thing `-f -f` reaches past, and a plain removal of such a worktree is served the `git worktree remove -f -f <path>` line; a `magus checkout` lock never is. " +
		"`git worktree unlock` is judged by the same conditions but the lock, since an unlocked worktree has nothing else stopping its removal. " +
		"The refusal names each failed condition and the command that inspects it, and `--force` changes nothing else. " +
		"Removal cannot be undone, so a fact that cannot be read refuses too: an unreadable job store, a line that does not parse, a path that is not a literal word, a removal inside a conditional or behind a wrapper. " +
		"`git worktree prune` passes, since it only clears records of directories already gone, and `git worktree remove --help` and `-h`, alone on the line, print usage and pass. " +
		"`jj workspace forget` stays refused: the jj driver cannot yet report a workspace's registration or which of its commits are published, so nothing proves a forget loses nothing.",
})

var ruleDocReadNavigation = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleReadNavigation),
	Catches: "a whole read of a Go, Buzz or Markdown file past the workspace's line limit",
	Why: "The deny carries the file's declarations or headings with their lines, and the command that prints one of them, so the refused read costs nothing. " +
		"The map is parsed from the file itself, so a stale index still gets one: where the index vouches for every name, `magus refs <name> --definition --source` prints one checked against it and the graph lists the map; where it does not, and for Buzz, which nothing indexes for refs, `sed -n <first>,<last>p <file>` prints one by its lines. " +
		"A Go declaration spans its doc comment to its closing brace, each member of a grouped var, const or type is its own entry, and a method is named `Type.Method`. A Buzz entry is a top-level fun, test, object or enum, from its doc comment to the line before the next statement. " +
		"Every file a `cat` or `nl` prints is judged, so `cat a.go b.go` is refused for whichever mapped file is over the threshold. A host's read tool is restated as the same line (`cat <file>`, or `sed -n` over its offset and limit, a limit over 300 counting as whole) and judged the same way. " +
		"The size worth flagging is the rule's `lines` setting, which a workspace sets: the binary ships no number for it. " +
		"Silent on a short file, TypeScript (no parser here), SKILL.md, AGENTS.md and a host's own instruction file (written to be read whole), a generated output, a path outside the workspace, a file that does not parse, and a read feeding a pipe or redirect. " +
		"It refuses each caller once per file: the same caller asking for the same file again has the map in hand and chose the whole read, so that read passes.",
})

var ruleDocSleepPoll = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSleepPoll),
	Catches: "a sleep before a one-shot status read, or a backgrounded call that only sleeps",
	Why: "Both are a wait whose loop is the caller's own next call. A sleep of ten seconds or more followed on the same line by a status read (`kill -0`, the process table, `magus status` or a job row, a pull request's checks, a CI run) looks once and leaves the next look to the next call. " +
		"A backgrounded call that only sleeps and prints is a timer: its notice says only that time passed, where the work's own backgrounded call is announced with its result. " +
		"A watch (`--watch`) is not a status read, and a loop is busy-wait's. " +
		"It cannot tell a one-off settle before a single check from the tenth look of a poll; the served watch answers both.",
})

var ruleDocReadPaging = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleReadPaging),
	Catches: "a mapped file read window by window, past two windows of it",
	Why: "read-navigation refuses a whole read and serves the file's map; paging through the file instead spends the same lines over several calls. " +
		"Each caller's windows of each Go, Buzz or Markdown file are counted, and the third distinct one is refused with the map. " +
		"A window inside one declaration or heading is a targeted read and never counts, nor does a repeat of a window already read, a whole read, or a file short enough for read-navigation to pass whole. " +
		"It cannot tell a walk through the file from reads of three spans that each cross a declaration's edge.",
})

var ruleDocHeredocWrite = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleHeredocWrite),
	Catches: "a here-document written to a file, judged as the file write it is",
	Why: "`cat > f.go <<'EOF'` and `tee f.go <<'EOF'` write a file the way a host's write tool does, so the body is judged as that write: the comment, lease and workspace write rules hold on either channel. " +
		"A refused write refuses the line under this name, with the write rule's own in brackets, and advice about the write rides the line. " +
		"Only a literal body is judged: one that expands a parameter or runs a substitution is text nobody can read before it runs. A target in /dev, a stderr redirect and a heredoc feeding a command's stdin are not writes. " +
		"It cannot tell a scratch file the same line deletes from a file meant to stay.",
})

var ruleDocHooksOff = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleHooksOff),
	Catches: "a git call with the repository's hooks switched off",
	Why: "Hooks are the checks a repository puts in front of a commit or push, and `--no-verify`, `git commit -n` and `-c core.hooksPath=/dev/null` (or an empty path) skip every one of them. " +
		"A hooks path naming a directory is a different set of hooks rather than none, so it passes; `-n` counts only on commit, where it means `--no-verify`. " +
		"It cannot tell a repository with no hooks installed, where the flag skips nothing, from one where it skips them all.",
})

var ruleDocBriefPlainCommands = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleBriefPlainCommands),
	Catches: "a brief for an isolated worker that does not hold it to one plain command per call",
	Why: "A host that runs a worker in a worktree of its own refuses each command it cannot prove stays inside that worktree, and a substitution, a loop or an exec is one it cannot. " +
		"So a spawn into its own worktree must say \"one plain command per call; no `$()`, loops or exec\" (any wording naming one command per call, or plain commands), and must not itself show a command of those shapes. " +
		"A spawn into a shared checkout passes. It cannot tell a brief that says the same thing in other words from one that never says it.",
})

var ruleDocMonitorDuplicate = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleMonitorDuplicate),
	Catches: "a second live watcher on one subject, or one on work the agent backgrounded itself",
	Why: "A watcher is a host tool that runs a command in the background and wakes the agent on each line it prints, and again when it expires. " +
		"Its subject is what its command reads: a process pattern, a pid, a file, a job, the project lock, a CI run; a command naming none is its own subject. " +
		"A second watcher while the first is live wakes the agent twice per event; one on a host task capture, or on the process of a command this agent backgrounded, repeats the notice the host already gives. " +
		"A backgrounded call that watches without end (`magus status --watch`, `tail -f`, `watch`) is judged the same way, and a leased worker is refused one outright, since its waits are its own runs. " +
		"Re-arming a watcher that expired passes. It cannot tell a process pattern that also matches someone else's run from one matching only the agent's own.",
})

var ruleDocMonitorPile = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleMonitorPile),
	Catches: "an agent arming a waiter while several of its own are already live",
	Why: "Every live waiter, a watcher, a backgrounded sleep or an endless backgrounded watch, wakes its agent on its own schedule, and the ones still armed after the work ends wake a finished agent until they expire. " +
		"The third live waiter of one agent is advised against and the fifth refused. " +
		"A waiter counts from when it is armed until its own timeout, so it cannot tell a watcher whose command already exited from one still running.",
})

var ruleDocSpawnWaiter = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSpawnWaiter),
	Catches: "a spawn whose child exists to wait on, poll or watch other work",
	Why: "A host tells a parent when each child ends, and a root waits on a job with `magus job wait`, so an agent spawned to wait spends a context on a notice that already comes. " +
		"It reads only the title and the opening of the brief: one that opens on wait, poll, monitor, watch or babysit, or says to wait for, on or until an agent, worker or job. " +
		"It cannot tell a brief opening on a feature named for watching (\"Monitor endpoint: add a route\") from one whose child only waits.",
})

var ruleDocSpawnDuplicate = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSpawnDuplicate),
	Catches: "a spawn for a job that already has a worker",
	Why: "A spawn titled `<parent>/<role> <job>` hands that job to its child. When an earlier spawn in this session was already attributed to that job and its row is still declared or running, a second worker would write the same paths from a second context. " +
		"Continue the worker with a message instead, or stop it first. " +
		"A running row alone does not fire it, since a row stays running after its worker dies; another session's worker is not this rule's to see. " +
		"It cannot tell this session's worker that died from one still alive.",
})

var ruleDocCaptureFilter = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryCaptureFilter),
	Catches: "a filter over a run capture or log, which cuts the failure block apart",
	Why: "A failure prints five lines together: the target, the cause, an output ref, the command that reads that ref, and the command to reproduce it. " +
		"A filter keeps the one line it matched and drops the rest, so `grep 'cause:'` keeps the symptom and discards the ref that reads the whole log two lines below it. " +
		"A range print (`sed -n '1,200p'`) is a filter too: it cuts by POSITION, and the block sits wherever the run left it. " +
		"The better route is an output contract up front, `-o jsonl --tee <file>`, queried with `jq`. " +
		"It ADVISES rather than refuses: most such filters are a search the reader needs, and a refused reader then reads the whole file into context. " +
		"It fires only on a file a filter READS: a host task capture (`tasks/<id>.output`) or a run log (`.magus/logs/<hex>.log`). A pattern shaped like one, such as `grep 'global\\.output' cmd/`, is not a capture.",
}, true)

var ruleDocCheckpointState = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryCheckpointState),
	Catches: "a command reaching for a tree's identity, which a revision alone cannot give",
}, false)

var ruleDocCheckTimeout = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryCheckTimeout),
	Catches: "a bound worker's magus run whose timeout is shorter than the target's last real run allows for",
	Why: "A call the host times out moves to the background, and a worker that then ends its turn parks until the run ends, with nobody told the result. " +
		"The duration is the newest run of that target and project that really ran, from the newest invocations in the repository's session store; a replay or a run refused before it started does not count. " +
		"It names a timeout of half again that duration, rounded up to a minute and capped at the host's foreground maximum. When the last run alone took longer than the maximum it says the check cannot finish in the foreground and to ask the orchestrator for a narrower one. " +
		"It reads the host's foreground default and maximum from the wiring, which declares them, and the call's own timeout, and is silent when either limit is undeclared, when the target has no recorded run, and for anyone but a worker under a live lease that does not own the gate. " +
		"Held to one firing per target per session.",
}, true)

var ruleDocDependencyInstall = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryDependencyInstall),
	Catches: "a raw package install that the cached install target already runs",
}, false)

var ruleDocDependencyUpdate = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryDependencyUpdate),
	Catches: "a raw dependency update outside a target's update charm",
}, false)

var ruleDocEchoOnSuccess = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryEchoOnSuccess),
	Catches: "an `&& echo` that restates what the exit status already says",
}, false)

var ruleDocLeaseState = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryLeaseState),
	Catches: "a leased write while its row reports a diverged base, a re-entered path, or a bad pattern",
}, false)

var ruleDocStdinClosed = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryStdinClosed),
	Catches: "shell commands run with stdin at end-of-file, said once per session",
	Why: "An agent's shell command inherits an open stdin nobody writes to, so anything that reads it (grep or cat with no operand, read, a prompt, ssh, a pager) waits forever, and a host that times the call out backgrounds it rather than killing it. " +
		"A grep whose file operands expand to nothing reads that stdin until the host gives up. " +
		"Where the host lets a hook rewrite the call, the guard prefixes the command with `exec </dev/null;`, never on a refused call and never twice. A heredoc, a pipe or a `<` still give a command its input, since each sets stdin for its own command. " +
		"A prefix rather than a `{ <command>` ... `} </dev/null` group: one host's isolation check for worktree agents judges the rewritten line, and it refuses the group as too complex even around `stat` or `git status`, where it refuses the prefix only on a line it already found borderline (runtime-computed values beside a redirect).",
}, true)

var ruleDocTimedMagus = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryTimedMagus),
	Catches: "`time` around a silent magus run, which already reports its own durations",
}, false)

var ruleDocReadSymbol = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryReadSymbol),
	Catches: "a bounded read inside one indexed declaration, which refs --definition --source prints checked",
	Why:     "Silent inside a method: refs resolves bare names, so its command would print every method of that name.",
}, false)

var ruleDocFocus = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryFocus),
	Catches: "a read or write outside the paths the running job declared",
}, true)

var ruleDocGateRepeat = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(advisoryGateRepeat),
	Catches: "the gate run a third time over a tree its last green verdict already covers",
	Why: "From the third gate run inside two hours, the run is refused when the newest verdict the branch recorded passed and no file that differs from that verdict's commit was written after it: such a gate can only pass again. " +
		"Short of that, the rule states what the gate has cost so far and the command that sizes the risk, once per session, and refuses nothing. " +
		"A run log, a session store or version control it cannot read refuses nothing.",
})

var ruleDocGeneratedWrite = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryGeneratedWrite),
	Catches: "a hand edit to a declared output, which the next run overwrites",
}, true)

var ruleDocGraphPipe = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryGraphPipe),
	Catches: "a read-only graph verb piped into a text filter, when magus projects the record itself",
	Why: "The same answer output-pipe gives, offered rather than imposed: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field. " +
		"It advises on `refs`, `query`, `explain` and `describe` because they change nothing and their pipe loses no failure. A reader refused on these verbs tends to go back to `grep -rn`, which answers with less than the graph read it was denied.",
}, true)

var ruleDocGraphStale = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryGraphStale),
	Catches: "a graph read, or a graph-backed deny, while the graph describes another tree",
	Why: "symbol-search, grep-reader and search-translation deny in favor of a graph answer, so they first ask whether the graph describes the tree on disk: no merge, rebase, cherry-pick or revert underway, read through the workspace's version control, and a guard index built at the current revision. " +
		"When either fails the search runs, and this names why and `magus graph build`, to run once the operation is finished: a rebuild mid-rebase would describe a tree about to change. " +
		"An index that records another revision is stale for every kind, so after a history rewrite the next lookup waits for a rebuild rather than trusting it.",
}, true)

var ruleDocHookWiring = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryHookWiring),
	Catches: "a write to the host wiring that decides whether these rules run at all",
}, true)

var ruleDocInstalledSkill = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryInstalledSkill),
	Catches: "a write to an installed skill copy, which re-installing discards",
}, true)

var ruleDocLeaseInvalid = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryLeaseInvalid),
	Catches: "a call naming a lease this workspace's job store does not declare",
}, true)

var ruleDocLeaseTerminal = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryLeaseTerminal),
	Catches: "a call naming a lease whose row has already finished",
}, true)

var ruleDocLeasedPath = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryLeasedPath),
	Catches: "a write into paths a running lease owns, by a caller that names no lease",
	Why: "The writer is either that lease, not saying so, or someone about to collide with whoever took it, a person or not; magus cannot tell which, so it advises rather than refuses, and says where the job was taken. " +
		"It speaks once per session per lease: repeating it on every write would tell the writer nothing the first did not.",
}, true)

var ruleDocInstructionWrite = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryInstruction),
	Catches: "a write to a cross-host instruction file, which every session loads whole",
	Why: "A cross-host instruction file is read in full at the start of every session on every host, so a sentence there costs context forever. " +
		"A rule the guard already refuses or doctor already reports is restated context: delete it the moment the tool starts saying it.",
}, true)

var ruleDocNewFile = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryNewFile),
	Catches: "a new file in a directory whose naming has settled",
}, true)

var ruleDocNewSourceDir = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryNewSourceDir),
	Catches: "a new file that opens a directory, which is a boundary rather than a file",
}, true)

var ruleDocPrecedentSearch = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryPrecedent),
	Catches: "a hunt for one distinctive name, which refs answers with verified sites",
	Why: "A precedent hunt is a search for one distinctive name, and it is the search the graph answers best: refs lists verified sites, so you land on working code instead of assembling it from grep hits. " +
		"In a workspace with no symbol index yet it advises once and stays quiet after: a repeat saying the index is still missing changes nothing.",
}, true)

var ruleDocSpawnNestedWaiter = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisorySpawnNestedWaiter),
	Catches: "a subagent spawning a helper whose title or brief opening speaks of waiting or watching",
	Why: "Waiting belongs to whoever spawned the work: the host tells a parent when its child ends. A worker handing that to a helper of its own stacks a third context on a notice two already get. " +
		"It advises, once a session, because a worker may also spawn a child to build a watch feature, which the words alone cannot tell apart.",
}, true)

var ruleDocPushGate = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryPushGate),
	Catches: "a push the run log does not prove ungated, which names the gate and lets it through",
}, false)

var ruleDocRegenSource = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryRegenSource),
	Catches: "a hand edit to a file a target regenerates",
}, true)

var ruleDocRevertClassify = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryRevertClassify),
	Catches: "a revert that has not classified what it is reverting",
	Why: "Reverting regenerated output is the wrong default. An agent that did not hand-edit a `gen/` file concludes it is not \"its\" change and discards it, but a generate target rewriting its declared outputs is the system working, and those outputs belong in the same commit as the source that moved them. " +
		"The honest test is whether the SOURCE changed, not whether anyone typed into the output. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged; that drift is environmental and worth reporting rather than discarding.",
}, false)

var ruleDocScopeDrift = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryScopeDrift),
	Catches: "a write into a project this session has no dependency edge to",
}, true)

var ruleDocSkillSource = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisorySkillSource),
	Catches: "a write to an installed skill copy rather than to its source",
}, true)

var ruleDocSourceRead = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisorySourceRead),
	Catches: "an unbounded source read the symbol index has already answered",
}, true)

var ruleDocSplitRun = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisorySplitRun),
	Catches: "the same target run again on a different project set, as a separate call",
	Why: "`magus run` and `magus affected` take one target and many projects, so the same target run twice on two project sets is usually one call typed as two: `magus run lint . docs` covers what `magus run lint .` and `magus run lint docs` would otherwise cost as two workspace loads. " +
		"It compares the session's last magus run/affected invocation against this one: same target, same charms, a different project set, inside a ten-minute window. The one-line shape (`magus run lint . && magus run lint docs`) is chained-run's, which refuses it and names the combined call. " +
		"Charms count as part of the target identity, so `lint` and `lint:rw` are never combined into one call. Held to one firing per session.",
}, true)

var ruleDocSharedCheckout = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisorySharedCheckout),
	Catches: "a spawn into a checkout a live job already holds and writes",
	Why: "Two workers in one checkout share every file in it, and the pair nobody survives is a magusfile, magus.yaml or spell source: half-saved, it stops the workspace loading for everybody there at once. " +
		"It fires on the spawn, the one moment the choice between one checkout and two is still free to make. " +
		"Prove the write paths disjoint with `magus describe file`, or give the new worker its own worktree, which is the answer whenever the write paths touch workspace configuration.",
}, true)

var ruleDocStageClassify = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryStageClassify),
	Catches: "staging without classifying, when generated and source differ",
}, true)

var ruleDocUnleasedWrite = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryUnleasedWrite),
	Catches: "a write magus cannot attribute while a fleet is running",
}, true)

var ruleDocWorkspaceCommandFailed = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryCommandRuleFailed),
	Catches: "a command the workspace's rule could not judge, so only the built-in rules graded it",
	Why: "A workspace command rule that fails to load or errors judges nothing, and a command reaching a verdict without it must say so rather than read as fully judged. " +
		"The first notice in a session names each failing side and its error; a repeat is one line naming what applied. " +
		"It stands alone only when the built-in rules passed the command; on any other verdict the note is appended to that verdict. " +
		"Standing alone, the one line is said once per caller: like every advisory not held by its own marker, the same text is not repeated to the same caller in a session. " +
		"A workspace cannot set it: magus\\guard.builtins refuses the name, since the notice is how the workspace learns its own rule judged nothing.",
}, false)

var ruleDocWorkspaceWriteFailed = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryWriteRuleFailed),
	Catches: "a file write the workspace's rule could not judge, so only the built-in rules graded it",
	Why: "A workspace write rule that fails to load or errors judges nothing, and a write reaching a verdict without it must say so rather than read as fully judged. " +
		"The first notice in a session names each failing side and its error; a repeat is one line naming what applied. " +
		"It stands alone only when the built-in rules passed the write; on any other verdict the note is appended to that verdict. " +
		"A workspace cannot set it: magus\\guard.builtins refuses the name, since the notice is how the workspace learns its own rule judged nothing.",
}, false)

var ruleDocWorkspaceSpawnFailed = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisorySpawnRuleFailed),
	Catches: "a spawn the workspace's rule could not judge, so only the built-in rules graded it",
	Why: "A workspace spawn rule that fails to load or errors judges nothing, and a spawn or continuation reaching a verdict without it must say so rather than read as fully judged. " +
		"The first notice in a session names each failing side and its error; a repeat is one line naming what applied. " +
		"It stands alone only when the built-in rules passed the call; on any other verdict the note is appended to that verdict. " +
		"A workspace cannot set it: magus\\guard.builtins refuses the name, since the notice is how the workspace learns its own rule judged nothing.",
}, false)

var ruleDocGuardUnfinished = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(advisoryUnfinished),
	Catches: "guard checks that never finished, so no rule judged their calls",
	Why: "A host stops waiting on a hook that has not answered within its hook timeout, with no trail row and no word to the agent. " +
		"So a check that has not answered within the guard's own budget, 7s against the host's 10s, stops waiting on its rules and answers unjudged: " +
		"a call that changes state is denied, to be retried, and one that only reads runs with a note saying no rule judged it. The trail records the check as lapsed. " +
		"Each check leaves a mark when it starts, before the workspace's rules load, and removes it when it answers while the host still waits. " +
		"The host stopped waiting when the hook process it started is gone, or past the hook timeout the host's harness declares in harness_limits; a host whose harness declares none is not taken to have given up on a slow check. " +
		"The next check in the session reports and clears every mark whose process ended without removing it, and every one still running past the declared timeout. " +
		"It stands alone only when the rules passed the call; on any other verdict the note is appended to that verdict. " +
		"A workspace cannot set it: magus\\guard.builtins refuses the name, since the notice is how the workspace learns a guard check of its own judged nothing.",
}, false)

var ruleDocRootContextBudget = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleRootContextBudget),
	Catches: "a session's main thread whose context has grown past its level's size",
	Why: "Every call of a session re-sends its whole context, mostly as cache reads, so its cost grows with the square of its length, and a main thread that does its own reading pays for each result on every call after it. " +
		"The size is the last usage the main transcript recorded, read after each call. At light it advises once at half the model's window, and says nothing for a model the table does not know; at standard it advises once at a fixed size, lower when the account bills per token than on a subscription quota; at strict it advises lower still and refuses the delegable commands (a recursive search, a whole-file read of source, a full diff, a target run, a web fetch) past a second size. " +
		"A tokens parameter replaces the level's sizes with one, at the rule's decision. It judges only the unleased main thread, and a thread magus inferred rather than one its host named is only advised. " +
		"A call whose output is a ref or one line is never judged by this rule, so a main thread past its budget can still build ./magus and file its work: the bootstrap build command, `magus run` or `magus affected` with `-s` or `--silent`, `magus run go-build` or `go::go-build`, `magus job fork`, `exit`, `apply` and `wait`, `magus query output --identity`, any magus command asked for `-o name` or `-o template=`, `git commit`, `cherry-pick` and `branch`, `git status -s`, and `git log --oneline` with a count (`-n`, `--max-count` or `-N`). A line that changes directory first is judged by the rest of it.",
})

var ruleDocRootHeavyCommand = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleRootHeavyCommand),
	Catches: "a main thread running delegable commands one after another without spawning",
	Why: "A recursive search, a whole-file read of source, a full diff, a target run or a web fetch is work a subagent does from a small context, returning only what matters. " +
		"Each delegable call since the main thread last spawned or continued a subagent counts; at standard it advises once at the eighth, and at strict it advises at the fifth and refuses from the eighth. " +
		"Spawning, the final gate, loading a skill, reading Markdown and editing never count, and neither does a command whose output is a ref or one line: the bootstrap build command, `magus run` or `magus affected` with `-s` or `--silent`, `magus run go-build` or `go::go-build`, `magus job fork`, `exit`, `apply` and `wait`, `magus query output --identity`, any magus command asked for `-o name` or `-o template=`, `git commit`, `cherry-pick` and `branch`, `git status -s`, and `git log --oneline` with a count (`-n`, `--max-count` or `-N`). Reading a run's whole output with `magus query output` does count. A calls parameter replaces the level's count.",
})

var ruleDocRootResultCap = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(denyRuleRootResultCap),
	Catches: "one tool result too large for a main thread's context, said on its next call",
	Why: "A tool result stays in the context every later call re-sends. The main transcript is read after each call for the results it gained, and one past the level's size is said on the next call: once a session at light, once per tool at standard and strict, with lower sizes at each. " +
		"It never refuses, since the result has already arrived. A chars parameter replaces the level's size.",
}, false)

var ruleDocRootRepeat = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleRootRepeat),
	Catches: "a main thread running the same delegable command again with no write since",
	Why: "An identical search, read, diff, run or fetch with no file written since returns what the context already holds. " +
		"At standard the second run is advised once; at strict it is advised and the third is refused. A file write starts the count over. A calls parameter replaces the level's count.",
})

var ruleDocSpawnNamesModel = denyDoc(types.RuleDoc{ //nolint:unused // read through the registry denyDoc fills
	Name:    string(denyRuleSpawnNamesModel),
	Catches: "a main thread spawning a subagent without naming the model it runs on",
	Why: "A spawn that names no model runs the child on the session's own, usually the costliest it uses. " +
		"At standard it advises once; at strict it refuses, and also refuses a spawn whose model and its job's model both name rows of the model table and differ.",
})

var ruleDocSpawnPrincipalForEconomy = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(denyRuleSpawnPrincipalForEconomy),
	Catches: "a read-only job's subagent spawned on one of the costliest models",
	Why: "A job marked read-only gathers evidence, which a cheaper model does as well. The spawn's model, or the session's own when it names none, counts as costly when its input price is above the median of the model table. " +
		"At standard it advises once; at strict it also advises on a spawn of the host's read-only explore agent type. It keys on declared facts, never on the prompt, and never refuses, since a read-only design job can need the strongest model.",
}, false)

var ruleDocCompatMarker = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(denyRuleCompatMarker),
	Catches: "a leased worker adding a marker that keeps code for older data or callers",
	Why: "Whether code stays for an old caller is a judgment about who still consumes the old behavior, which a worker cannot see from its write paths and the root can. " +
		"When a write a worker's lease allowed leaves more of a marker than it found (`compat(until:` and `Deprecated:` unless a markers parameter names others), the worker is told the root decides and to name it in unresolved_risks, and one attention row per lease states the job, the file, the marker and the model it ran on. " +
		"It never refuses and never asks the host: the write is not wrong, the decision is not the worker's. A session acting under no lease, a person or the root, is never told.",
}, false)

var ruleDocStall = adviseDoc(types.RuleDoc{ //nolint:unused // read through the registry adviseDoc fills
	Name:    string(denyRuleStall),
	Catches: "a leased worker refused by something only the root can clear",
	Why: "A worker blocked on its lease's boundary or on a magus that cannot load the tree waits and reports again, and the root hears late. " +
		"When lease-write, lease-vcs, lease-harness, lease-rebind, lease-undeclared or stale-binary refuses a leased worker, the refusal's verdict line ends with the job's exit, the block named as an unresolved risk, and one attention row per lease and rule states the facts: the job, the rule, the model it ran on and how often it was refused. It names no remedy, and never refuses anything itself. " +
		"A refusal repeating the last one, and a run of refused calls (a calls parameter sets its length), are recorded per lease and said to no one.",
}, false)

// Rules returns the whole catalog, denies first and each tier sorted by name: the order a
// reader scans, with the tier that blocks them at the top. Each row's Decision is the
// rule's compiled default; a workspace's own setting is applied where the rule fires.
func Rules() []types.RuleDoc {
	defaults := builtin.Defaults()
	out := make([]types.RuleDoc, 0, len(ruleDocRegistry))
	for _, e := range ruleDocRegistry {
		out = append(out, e.doc)
	}
	for i := range out {
		out[i].Decision = string(defaults[out[i].Name])
		out[i].Fixed = builtin.Fixed(out[i].Name)
	}
	slices.SortFunc(out, func(a, b types.RuleDoc) int {
		// Deny sorts before advise, which is neither alphabetical nor accidental: a
		// reader opening this list is asking what stops them first.
		if a.Decision != b.Decision {
			if a.Decision == string(builtin.Deny) {
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
func Rule(name string) (types.RuleDoc, bool) {
	name = strings.TrimSpace(name)
	for _, r := range Rules() {
		if r.Name == name {
			return r, true
		}
	}
	return types.RuleDoc{}, false
}

// Advisories that spoke on every match with no name, so their verdicts recorded none.
// Named without enrolling: a kind would hold each to one firing, which none asked for.
const (
	advisoryLeaseState        denyRuleName = "lease-state"
	advisoryDependencyUpdate  denyRuleName = "dependency-update"
	advisoryDependencyInstall denyRuleName = "dependency-install"
	advisoryEchoOnSuccess     denyRuleName = "echo-on-success"
	advisoryTimedMagus        denyRuleName = "timed-magus"
)

// advisoryNames is every advisory's name, held or not: the set the catalog must cover.
func advisoryNames() []string {
	var out []string
	for _, e := range ruleDocRegistry {
		if e.advisory {
			out = append(out, e.doc.Name)
		}
	}
	return out
}

// enrolledAdvisories is the name of every advisory filed as enrolled.
func enrolledAdvisories() []string {
	var out []string
	for _, e := range ruleDocRegistry {
		if e.enrolled {
			out = append(out, e.doc.Name)
		}
	}
	return out
}
