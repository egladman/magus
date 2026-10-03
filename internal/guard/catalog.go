package guard

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
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
// It is a TABLE rather than a method on each rule because the rules are not objects: they
// are functions returning prose, spread over four files and matched in a switch. A table
// beside the constants is the one shape where a missing entry is visible, and
// TestEveryRuleIsCatalogued is what makes it visible.
//
// Catches is deliberately short and deliberately not the verdict text. The verdict tells
// one caller what to run instead; this tells a reader what the rule is FOR, so a list of
// thirty-eight reads as a set of conventions rather than a wall of remediation.

// denyRuleDocs documents every rule that REFUSES. Ordered by name here only for review;
// Rules sorts what it returns.
var denyRuleDocs = []types.RuleDoc{
	{Name: string(denyRuleBacktickSubstitution), Decision: "deny",
		Catches: "a backtick command substitution, which inside double quotes runs a command",
		Why: "Inside double quotes a backtick starts a command substitution, so a pattern or a message carrying a literal backtick runs code: the backtick pairs with the next one anywhere on the line, and everything between them becomes one command. " +
			"Measured here: a `grep` whose pattern and a later stage's pattern each held a triple backtick paired them into ONE substitution that swallowed the file operand and the rest of the pipeline. What was left was a `grep` with no file, reading a stdin that never closed, and it held a subagent for two hours. " +
			"A literal backtick belongs in single quotes, where it is text. A substitution is written `$(...)`, which nests and cannot pair with a stray backtick. " +
			"Backticks inside single quotes and inside a quoted heredoc (`<<'EOF'`) are text and never fire; neither does a line that does not parse."},
	{Name: string(denyRuleBusyWait), Decision: "deny",
		Catches: "a loop that only sleeps between polls, holding a tool slot for its whole wait",
		Why: "A backgrounded command is tracked and announces its own completion, so starting it and doing something else is strictly better than watching it. " +
			"A loop probing a process the caller did not start (`kill -0`, the process table, `magus status` or a job row) gets no such announcement, and its deny says only what holds for it: the loop occupies a tool slot for the whole wait. " +
			"The loop also has no bound of its own: past the tool timeout it is BACKGROUNDED rather than killed, and goes on polling a condition that may never arrive, because a run that failed early never prints the line being grepped for. " +
			"Several have had to be killed by hand. Waiting on something OUTSIDE this machine, a remote queue or a deploy nobody here started, is what a host's monitor surface is for. " +
			"A shell script is judged by its content, so `bash wait.sh` and a write of wait.sh get the verdict the loop would get typed inline; so do the output-pipe, output-redirect and unknown-env rules."},
	{Name: string(denyRuleCacheDirWrite), Decision: "deny", Catches: "a write into this checkout's magus cache dir, which magus alone owns"},
	{Name: string(denyRuleChainedRun), Decision: "deny",
		Catches: "magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast",
		Why: "A pipe of magus runs keeps what the chain was for: stages whose projects overlap run in order, a failed stage stops the ones after it, and the last stage exits with the first failure (MGS3030), so no `set -o pipefail` is needed. " +
			"It differs from `&&` in one way, which the deny states: a stage on disjoint projects runs alongside the others and finishes even after an upstream stage fails. The pipeline still exits red. " +
			"The same target chained over several project sets is served the pipe too, which keeps the order the chain chose; the deny names the one call for when order does not matter. Measured 2026-09-27, every such chain was `generate:rw docs` then `generate:rw .`, which one call would reverse. " +
			"It advises instead, and says why, wherever the pipe would do something else. That covers `||`, a redirect or any other command on the line, and a later `affected` stage, which reads its projects from the diff before the upstream's writes land: `affected generate:rw` stays its own call ahead of `affected ci`. " +
			"It also covers a later `run` naming no projects, which would inherit the upstream's; a stage that reads stdin, takes no locks, or writes -o output upstream; stages on different binaries or roots; " +
			"a stage after `go-build` or `build` in magus's own checkout, since every stage starts at once and would run the ./magus being replaced; and Windows, where no pipe proves its upstream."},
	{Name: string(denyRuleClaimedDeclaration), Decision: "deny",
		Catches: "a leased edit landing in a declaration another live job claims (`run.go#executeStages`)",
		Why: "A write path may claim one declaration of a file, so two jobs can start on one file and integrate in order. " +
			"The claim holds only if an edit into the other job's declaration is caught before it lands, because afterwards both diffs touch it and neither applies over the other. " +
			"The edit is applied to the file in memory and its changed lines are placed by the same diff-driver matching the job footprint uses, so the declaration this names is the one `magus job wait` would report. " +
			"It fires only for a job-bound writer whose own claims in the file do not name the declaration, and only when another live job claims a declaration of that file; an edit that lands in the writer's claims, in no one's, or above the first declaration passes. " +
			"A payload carrying no edit, such as a whole-file write, and a file whose lines cannot be placed stay graded by path alone."},
	{Name: string(denyRuleExitStatusEcho), Decision: "deny",
		Catches: "a line ending by printing an exit status, which the harness already reports",
		Why: "The harness reports a nonzero exit on its own and success needs no confirmation, so `cmd; echo \"rc=$?\"` adds lines and no information. " +
			"It also misreports: the echo exits 0, so the line as a whole passes whatever `cmd` did. " +
			"Every spelling of that ending fires: `echo`/`printf` of `$?` with literal text, to the console or stderr; the same through a capture (`rc=$?; echo $rc`); `cmd || echo \"failed $?\"`, where the echo runs exactly when cmd failed; and `${PIPESTATUS[...]}`, whose answer is `set -o pipefail` or no pipe. " +
			"Measured 2026-09-24: 508 lines ended this way against 2 denies, when only a bare trailing `echo $?` fired. " +
			"`exit $?`, `[ $? -ne 0 ]`, an echo mid-script, one after `&&`, and one redirected to a file keep the status for later logic and are untouched. " +
			"Chain with `&&`, or make separate calls, when a failure must not be masked."},
	{Name: string(denyRuleFilterWithoutInput), Decision: "deny",
		Catches: "a filter with no file, pipe or redirect, which reads a stdin nothing feeds",
		Why: "A filter given no input reads stdin, and under an agent harness stdin is the harness's own: where the harness holds it open, nothing writes to it and nothing closes it, so the call waits past the tool timeout and goes on waiting in the background. Measured: one such `grep` held a subagent for two hours. " +
			"Name the input: a file operand, a pipe into the command, or a `<`, `<<` or `<<<` redirect on it or on a loop or block around it. " +
			"It reads each tool's own flag grammar, so `grep -e pat file`, `jq --arg k v . f` and `head -n 5 file` are fed, and `tr`, `tee` and `xargs` fire whenever nothing feeds them, because their operands are never input. " +
			"A recursive grep with no path passes: GNU grep, macOS's BSD grep 2.6 and the ugrep a host may put behind `grep` all search the working directory then, measured 2026-09-26, and those were all five of the rule's measured denies. " +
			"What the guard cannot classify passes, because it refuses only what it can prove: an unknown flag, an unquoted expansion that may split into several words, `jq -n`, an awk program with a BEGIN block, a command inside a function body. " +
			"ripgrep with no path passes for the same reason: it searches the working directory unless stdin is a pipe or a file, which a hook cannot see."},
	{Name: string(denyRuleGrepReader), Decision: "deny",
		Catches: "a definition lookup with a context flag (`grep -A40 'func X'`), which uses grep to read the body",
		Why: "A context count guesses at a declaration's length: too short cuts the body off and costs another call, too long spends lines on whatever follows. " +
			"`magus refs X --definition --source` prints the declaration whole, numbered and checked against the index. Where the index cannot vouch for the name, the deny serves `sed -n <first>,<last>p <file>` instead, the declaration's own lines from a parse of the file the search reads (a named file or glob, or under a directory the files the index last saw name it). " +
			"The single-file allowance symbol-search gives `grep -n 'func X' f.go` does not apply: with -A, -B or -C the search is the read. " +
			"A deny resting on the index advises instead while the graph describes another tree, as graph-stale; one parsed from a named file does not. " +
			"It fires only when every alternative is a definition lookup (`func X`, `func (r *T) X`, `type X`, `type X struct`) and a declaration of each name is found; a search for uses, a case-insensitive one, or one with any text alternative is left to the search rules. A pipe after it is named as not reproduced. " +
			"Measured 2026-09-29 over the audit's transcripts from three hosts: 1,456 context-flag definition lookups, 16 of them denied by any rule. A hand-read sample of 39 held 33 (85%) where the served command answered what the grep asked; the 6 misses filtered the body through a second grep for a few lines, which the served command answers at a higher cost."},
	{Name: string(denyRuleInterpreterRewrite), Decision: "deny",
		Catches: "an inline interpreter rewriting a file this tree already carries",
		Why: "A `python -c` or `node -e` that reads a tracked file, substitutes, and writes it back is an edit nobody reviewed: it lands before a diff exists, and the script that produced it is gone the moment the line ends. " +
			"The editor tool reads the file first and reports what it changed, which is the same edit with a record of itself. " +
			"It fires on the WRITE, not the interpreter: a one-liner that computes something, prints it, or creates a file the tree does not carry is untouched, and so is anything under a scratch path. " +
			"Only a write's destination counts: the path an `open(..., 'w')`, a pathlib or `writeFile` writer, an in-place flag or an awk redirect names, a variable read through its assignment. A tracked path the program carries as data, in a list it prints to stdout or a report it writes to scratch, is not one. A destination spelled from no literal at all, such as argv, is read as the interpreter's operands."},
	{Name: string(denySpawnUnbriefed), Decision: "deny",
		Catches: "a subagent spawned before the multi-agent skill loaded",
		Why: "Four decisions a spawn cannot be corrected for later are made before the child starts: which worktree it is cut from, which lease grades its writes, which model it runs, and that git stays with the orchestrator. The magus-multi-agent skill carries all four. " +
			"Measured across 2,147 session transcripts: zero loads, under every name and every variant, while the two skills a hook DEMANDS loaded 415 times. A skill nobody is required to read is a skill nobody reads, and this workspace had already written that down before measuring it again here. " +
			"It grades the session, never the prompt: it asks whether a marker file exists, so a handed-over prompt that merely mentions a denied command is untouched. Load Skill(magus-multi-agent) once and every later spawn in the session passes."},
	{Name: string(denyBuzzUnbriefed), Decision: "deny",
		Catches: "the first Buzz a session authors, by file write or `magus buzz -e`, before reading the Buzz skill",
		Why: "Buzz is in no model's training data, so what gets written is Go or TypeScript with the serial numbers filed off, and enough of it parses to reach review. " +
			"Six errors in one session, by an agent with this repository open throughout: fs\\glob indexed as strings when it returns [Path]; .append on a list declared without mut; the ternary form, which upstream-strict parsing rejects outside --embedded; archive\\extract, which does not exist; a missing `import \"fs\"`; and .sub sliced by character on BYTE-indexed strings. Reading first supplies every one of them. " +
			"It grades the session, not the file: one Skill(magus-buzz-lang) and every later Buzz write passes. Reads are never gated, since reading is how the language gets learned, so `magus buzz <file>` and `magus buzz -t <file>` run something that already exists and go untouched."},
	{Name: string(denyRulePushUngated), Decision: "deny",
		Catches: "a push at a commit with no green gate: the person is asked, a leased worker refused",
		Why: "The advisory this replaced fired on EVERY push, having read nothing: it told a caller who had just gated and a caller who had never gated the same sentence, which is a toll rather than a reminder. " +
			"This one reads the run log, so the finding is a fact: which invocations ran the gate, which commit each was built from, and how it finished. That is what makes stopping the call legitimate here where the rest of this tier only advises. " +
			"It matches on the COMMIT and not the exact tree, deliberately: an exact match would expire on the first comment typo after a green run, which is the delta the cadence already says to push, and a rule that fires there is one people route around. " +
			"Publishing work in progress is legitimate and indistinguishable from an oversight, so a session no job lease binds gets the verdict `ask`: the host's own approval prompt puts the push in front of the person, and approving it publishes. A marker the agent types is not consent, so nothing it says clears this. " +
			"A session bound to a lease is a worker, and workers do not publish: it gets `deny`, and nobody is asked."},
	{Name: string(denyRuleInlineAlias), Decision: "deny",
		Catches: "a VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs",
		Why: "git expands `git -c alias.x='reset --hard' x` into `git reset --hard`, so the word every git rule reads as the subcommand names nothing, and a reset, clean or push would pass unjudged. " +
			"The guard refuses the line rather than judging it as every destructive verb at once: the arguments those rules read come from the alias body too, and `--config-env` or an inline `include.path`, which loads a file that may define aliases, keeps the body off the line altogether. " +
			"The other backends are held to the same bar: hg's and sl's `--config alias.x=...`, jj's `--config aliases.x=...`, and the options that load config from a file or a TOML string (`--config-file`, sl's `--configfile`, jj's `--config-toml`). " +
			"Spell out the command the alias stands for. Any other config setting is untouched, and each tool's global options (`git -C`, `hg -R`, `jj --at-op`, `--no-pager`) are read past the way the tool reads them."},
	{Name: string(denyRuleMagusTimeout), Decision: "deny",
		Catches: "a magus call wrapped in coreutils `timeout` or `gtimeout`, which kills it from outside",
		Why: "A timeout wrapper ends magus with a signal from outside, so the run log records no cause, a target's tools can outlive the process that started them, and the next reader sees a run that stopped rather than one that timed out. " +
			"magus bounds a run itself: `--timeout <dur>` bounds the whole run and cancels its own process tree, `--target-timeout <dur>` caps each target, and `--stall-timeout <dur>` stops a run making no progress. " +
			"A `run` or `affected` is served the same argv with `--timeout` and the wrapper's duration (600 becomes 10m). Any other verb is served bare: it holds no lock worth waiting on, since a held lock refuses at once (MGS3009). " +
			"`magus buzz` has no bound of its own, so a wrapped script is advised rather than refused. " +
			"It reads every flag the wrapper takes and the launchers around it (`env`, `nice`, `nohup`, `sh -c`, `eval`). " +
			"A wrapper sending QUIT or ABRT is left alone, since that is how a goroutine dump is taken from a hung run, and so is a verb that runs until interrupted: `watch`, `events`, `job watch` and `--version`."},
	{Name: string(denyRuleMergeSideCheckout), Decision: "deny",
		Catches: "a checkout of one merge side over a conflicted file, which discards the merge",
		Why: "It reads like \"undo my edit to this file\" and is not: during a merge the working-tree copy IS the merge, and this replaces it wholesale with one side. " +
			"Measured here: `git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution silently dropped the branch's own half of a merged feature. Nothing failed, the gate stayed green, and the feature could not fire until someone read the code days later. " +
			"For a generated file, `magus vcs resolve` settles every conflicted one by regenerating. To take one side deliberately, say which: `git checkout --ours` or `--theirs`. To keep the merged result, it is already in the file."},
	{Name: string(denyRuleNotesAuthor), Decision: "deny",
		Catches: "an agent authoring a human's note, whose only provenance is who wrote it",
		Why: "A note is the one thing in the knowledge graph nothing here corroborates later, so its only provenance is the person who wrote it and signed the commit. " +
			"That is why it is refused however the write is spelled: `capture` files a review transcript as a note, where the commit puts a person's name on prose they never read. " +
			"Read the store instead, and say what belongs in it so the person can write it."},
	{Name: string(denyRuleCredentialVerb), Decision: "deny",
		Catches: "an agent minting, printing, rotating or revoking a credential through the CLI",
		Why: "An agent holds the token it was given, and a session that mints another holds a grant nobody handed it. " +
			"Refused: the console and connector token `create` and `revoke` commands, `magus graph export --open --follow` (its link carries a sign-in code), and `magus config token print`, `generate` and `revoke`, the operator token that reaches token management. " +
			"It holds however the binary is spelled: `./magus`, a path, `go run ./cmd/magus`, or inside a `$(...)` substitution. " +
			"This is a seatbelt for a harness that opted in, not a boundary: a process running as the user can reach the same files."},
	{Name: string(denyRuleTokenState), Decision: "deny",
		Catches: "an agent reading or writing the token secrets: the operator token file or the token store",
		Why: "The operator token file (`magus/mcp_token` in the user state dir) and the token store (`magus/tokens.d`) are the credentials the server checks, so reading one hands a session a grant and writing one mints a token. " +
			"Refused on both graded surfaces: an editor write aimed at them, and any shell line that names them, whatever the command (`cat`, `cp`, a redirect, an interpreter's inline script). A path is matched by name anywhere in a word and by resolving it against where the call runs. " +
			"A bare listing passes (`ls`, `du`, `stat`, `test` of the state dir or a token file), alone or piped into a text filter such as `head` or `grep`: it shows file names, and none is a secret, since the operator file is always `mcp_token` and a store entry is `<token name>.json`, the name `magus config mcp connector ls` already prints. A listing inside a substitution, or piped into anything else (`| xargs cat`), is refused like any mention. " +
			"Reads through a host's read tool are not graded: that hook only records, by contract. This is a seatbelt, not a boundary against a process running as the user."},
	{Name: string(denyRuleOutputPipe), Decision: "deny",
		Catches: "magus output piped into a filter, when magus projects the record itself",
		Why: "magus projects its own record, so the filter is answering a question the command takes a flag for: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field, `-s` to silence progress. " +
			"The half a reader cannot discover by trying again is the exit status: a pipe takes it from the LAST stage, so a failing magus reads as exit 0 and nothing says so. " +
			"It denies on `run`, `affected`, `x` and every verb that is not a graph read. A read-only graph verb (`refs`, `query`, `explain`, `describe`) gets the same answer as the graph-pipe advisory, and a help request (`--help`, `-h`) passes: neither loses a failure."},
	{Name: string(denyRuleOutputRedirect), Decision: "deny",
		Catches: "magus output sent to a file or discarded, which the run log already holds",
		Why: "Silencing and keeping are the only two intents and magus has a lever for each: `--silent` says nothing until something fails, and `-o json --tee <file>` keeps the STRUCTURED output rather than console text, which is not a format anything should parse. " +
			"A target run persists its whole log either way and prints a ref for it, so capturing the console is redundant. " +
			"It judges where each stream ENDS. Either stream landing in a file fires, and so does stdout landing in /dev/null. `2>/dev/null` fires on `run`, `affected` and `x`, which write their failure block (cause, output ref, reproduce line) to stderr, and passes on every other verb, whose stderr carries at most an error line the exit status also reports. " +
			"`2>&1` alone passes: both streams still reach the reader. Measured 2026-09-26: 556 of 841 denies were stderr-only."},
	{Name: string(denyRuleProcessPoll), Decision: "deny",
		Catches: "a process table inspected to wait on magus work the lock already reports",
		Why: "A magus run holds a project lock and announces itself, and `magus status --watch=15s` reads that same lock state continuously: holder PID, command, age. " +
			"`pgrep`, `pidof` and `ps` invent a poll with no bound of its own that answers a question the lock message already answered."},
	{Name: string(denyRuleRawTool), Decision: "deny",
		Catches: "a toolchain command a spell already wraps, run outside the cache",
		Why: "magus covers these exactly and adds cache, sandbox and affected tracking, so the refusal costs nothing: `magus run <target> <project>`, and `magus describe targets -o name` lists what this workspace calls them. " +
			"Tool flags go after `--`. A raw WRITE (codegen, a formatter with -w/--write/--fix, `go mod tidy`, build output landing on a tracked path) is the firm half: it leaves the owning target reporting drift it did not cause, and that has no exceptions. " +
			"The guard reads the command being RUN, so a wrapper, a `VAR=value` prefix or `bash -c` reaches the same verdict, and `go -C <dir> <verb>` reads the same as `go <verb> -C <dir>`. " +
			"Asking a tool for its usage or version runs nothing over the tree and passes: `go clean --help`, `gofmt -h`, `go help clean`, `govulncheck -V`. The help flag has to be the tool's own, last on the line, after a subcommand a spell renders; one handed to a program is work, so `go run main.go --help` and `go test ./... -args --help` are refused. " +
			"`gofmt -l` and `gofmt -d` pass too, without `-w`: they list or diff and write nothing, so they leave no drift for the format target to report. `go list` passes because no spell renders it. " +
			"One command is exempt, in a checkout of magus itself: `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`, alone on its line with no wrapper and no other prefix, in a checkout root that has no `magus` binary yet, is advised rather than refused, because a fresh checkout has no other way to get its first binary. " +
			"It runs the real go-build target (its generate steps and stamped link) instead of a bare link, so the first binary is the one every later build would make. `--no-cache` because that target's key cannot express the embedded-spell ordering and once replayed a binary missing tools; Go's content-addressed build cache stays on, `-trimpath` matches the target's own build so packages compile once, and `GOEXPERIMENT=jsonv2` because magus refuses to compile without it and a fresh checkout cannot count on mise to set it. " +
			"Every other raw go command in that root, a bare `go build -o magus ./cmd/magus` included, is refused and served the bootstrap. Once the binary exists the deny applies again and names `./magus run go-build .`. " +
			"A checkout that cannot load its own sources (MGS1021) has a second exemption, because no target can run there and the bootstrap fails to compile against generated files that lag their sources: " +
			"alone on its line, with no environment prefix but `GOEXPERIMENT`, the relink MGS1021 prints (`go build [-trimpath] -o magus ./cmd/magus`) and the generators the `*_generate` targets run (`go generate <package>` inside the checkout, `go run [-trimpath] ./cmd/magus-utils <generator>`, never its release subcommands) are advised rather than refused, with a binary or without. " +
			"The guard learns this by loading the workspace, and only for one of those lines; the binary judging is the checkout's own `./magus` when there is one. Once the workspace loads, they are refused again. " +
			"It was an advisory first, and changed behavior zero times over a long session while leaving the Go build cache poisoned by uninstrumented runs, which is why it denies."},
	{Name: string(denyRuleAgentSignOff), Decision: "deny",
		Catches: "an agent stamping a read receipt or closing an attention request, which only a person may do",
		Why: "This is not a permission an agent is missing: there is no spelling of either an agent may use, because an agent stamping the changeset or closing its own block would make the measure mean nothing for everybody, including the human relying on it. " +
			"Report what is unread instead: `magus diff --impact` names every changed file carrying no receipt, and `magus diff -o json` puts read_state on each file for a caller to branch on. " +
			"Waiting on a request instead: say you are waiting on its id and hand it back; `magus session dispose <id>` is a person's to run."},
	{Name: string(denyRuleScriptedRewrite), Decision: "deny",
		Catches: "a scripted substitute-and-write, which cannot tell your symbol from a dependency's",
		Why: "A regex cannot tell YOUR symbol from a dependency's symbol of the same name. " +
			"A `\\.Sum\\b` rewrite aimed at one proto field also hits the OTel SDK's `metricdata.Sum` and a histogram's `dp.Sum`, and the damage is written before any diff is read. " +
			"The graph knows which is which and a pattern never can: `magus refs <symbol> --occurrences` returns verified sites, per file, with columns. " +
			"Run `magus graph build` first if refs reports a project not-indexed, because that verdict means unknown rather than absent, and taking it for \"no matches\" is how a rename misses half its sites. " +
			"Rewriting raw TEXT (prose, a config value, a string literal) has no graph equivalent; say so and use an editor tool. " +
			"A script file is judged by its program: `python3 p.py`, and a write of p.py, get the verdict the same program would get inline. A program whose every named path lies outside the workspace is untouched. " +
			"An APPEND passes, since it adds to the end and cannot mangle a line already there: a heredoc appended with `cat >> f <<EOF` or `tee -a`, whatever program its text documents, and a program whose every file write opens in append mode (`open(p, 'a')`, perl's `'>>'`) or goes to its own stdout."},
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
		Catches: "a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output",
		Why: "A magus target writes its declared outputs as it runs, so the tree here is routinely dirty with files you did not edit. " +
			"`-A` sweeps those and any build residue into a commit about something else, with no signal that it happened, and `-u` reaches the same outputs: it stages every TRACKED change across the whole tree, which is the same sweep minus files that are merely untracked, and a target's declared outputs are ordinarily tracked already. " +
			"Measured: one such call put 69 files, a whole regenerated docs site plus five untouched source files, into a commit about four collection methods. " +
			"`magus vcs add` classifies every dirty path against the declared output globs, keeps a source change and the outputs it produced together, and reports anything undeclared instead of staging it."},
	{Name: string(denyRuleSymbolSearch), Decision: "deny",
		Catches: "a text search of the tree for a symbol, a declaration or a diagnostic code the graph answers",
		Why: "Each alternative of the pattern is classified by what it looks for, and the deny names every classification and why, so a false positive is disputable from the message alone. " +
			"A name is a declaration lookup (`func X`, `type X`, `class X`, `function X`, `const X`), a CamelCase identifier, or a short name the syntax marks as one: a word search (`\\bX\\b`, -w), a qualified member (`pkg.X`, `\\.X`) or an assignment, when it is capitalized. " +
			"Text is everything else: a plain word, which is as likely prose; snake_case, which in this tree is a config key, a JSON tag or a Buzz function; a lowercase call or member (`Mkdir(` is os.Mkdir as often as a local function); a standard-library member (`os.Rename`); a declaration of a lowercase word many share (`func main`); a string literal, a path, a hyphenated word or a regular expression. With -i a plain or marked short name widens to text, and a CamelCase one stays a name. " +
			"The deny fires when ANY alternative is a name the index defines, serving `magus refs X --occurrences` per name (`--definition --source` for a declaration lookup, which prints the body in place of the grep-then-sed pair) and `magus refs --text <literal> <paths>` for each literal text alternative, and `magus explain diagnostic:<code>` for a code the graph holds a node for. " +
			"A stale index still refuses, serving `magus graph build --silent` first: it still knows the names it held, and it vouches for a declaration lookup or a CamelCase call it has not indexed yet, since that is the name a branch is adding; a bare word it does not hold stays text. Measured 2026-09-30: the fail-open advice a stale index used to give let every symbol search through, since an index goes stale on the first edit. Only a workspace with no symbol index at all is advised instead, since a deny there routes nowhere. " +
			"An index built at another revision is stale on the same terms; a rebase still underway turns the deny into graph-stale's advice, since a rebuild then would describe a tree about to move. " +
			"The search must reach the tree: a directory, a glob, or several files. One named file is a read and runs, with refs advised when the index vouches for the name; a definition lookup carrying -A, -B or -C is a read of the body, and grep-reader refuses it. " +
			"The files refs answers for are the languages a spell declares a symbol indexer for (here Go and TypeScript), read from the spell catalog. A search of stdin, Markdown, a log, Buzz (source no indexer reads, so a Buzz `fun` or `object` lookup is text too), a directory holding none of those languages (a skills tree, fixtures), a dot-directory (.github, .git), node_modules, a revision, or a tree outside the workspace runs. " +
			"A search of the tree carries the index's own answer when the index is current: every file under the searched paths with its occurrence count and lines, as `magus refs` prints them. It is refs' answer, not grep's: comments, strings and prose are not in it. " +
			"A pipe after the search is not reproduced. The deny still carries the unfiltered answer and says so: a model of sort, sed or awk substituted for the real tool diverges from it. " +
			"Measured 2026-09-24 over 14,773 search patterns: 45% were alternations and 13% definition lookups. Measured 2026-09-30: of the 1,211 distinct search lines the guard recorded that week, the old rule refused one."},
	{Name: string(denyRuleSearchTranslation), Decision: "deny",
		Catches: "a text search whose pattern a graph query provably answers with the same entities",
		Why: "The pattern is compiled in the tool's own dialect (BRE, ERE or fixed) and run against the graph's ids when the command is judged, so the deny names a query that was checked rather than one that looks equivalent, and carries that query's answer, bounded to twenty results and a count, so the refused search costs nothing. " +
			"These shapes qualify. A pattern that can only match MGS codes (`MGS30[23]`, `MGS30..`, `MGS302[0-9]\\|MGS303[0-9]`), over any path in the workspace, becomes `magus query kind=diagnostic 'id=~^diagnostic:...$'`, and a single literal code keeps symbol-search's `magus explain diagnostic:<code>`. " +
			"A pattern selecting every Markdown heading of the files searched (`^#`, `^#\\+`), when those lines match the section nodes the graph holds file for file and none sits in a code fence, becomes `magus query kind=docsection 'id=~^docsection:<file>#'`. " +
			"A search of a magusfile whose every hit declares a target the graph holds becomes `magus explain target:<project>:<name>`. " +
			"A search of one Go file whose every hit declares a symbol the index holds (`^func`, `^func Test`, `func (s \\*Store)`) becomes `magus explain file:<path>`, with the names and their lines inline. " +
			"A file-finding call whose every file is a node the graph holds becomes `magus query kind=file 'id=~^file:...'`: a pattern when one selects exactly those files, else the files enumerated when there are twenty or fewer. The proof walks what the call walks, so it holds whatever revision the index was built at. It covers `find` (-name, -path, their negations, -type f, -maxdepth), `fd` (a name pattern or glob, -e, -t f, -d, smart case), `rg --files` (-g and -t), `ls -R <dir>`, and an `ls <dir>` whose every visible entry is a file node or a directory holding one, which becomes `magus query 'id=~^(?:file|dir):<dir>/[^/]+$'`. " +
			"`git ls-files [<dir|glob>]`, alone or piped into a search of its paths, is proved against what version control tracks: the graph indexes only some tracked files and never an untracked one (measured 2026-09-30: 2,689 of 4,940, the rest mostly Markdown under changes/ and docs/), so the deny answers for the indexed ones and names every other match; past twenty such files it is silent. " +
			"A metadata flag (-l, -a, -t, -S), hidden or ignored files (fd -H, rg -uu), an untracked-files question (`--others`), one named file (a tracked check), an inverted or counted filter, a file the graph does not index, or a walk past the budget is silent. " +
			"A host's own content and file search tools are judged as the rg and find lines they stand for, read by the shape of their input. " +
			"A pipe after the search is not reproduced, except the search a tracked listing is piped into: the deny carries the query's unfiltered answer and says so, rather than a model of the filter that could diverge from the real tool. " +
			"Anything else stays silent: -i, -v, -c, -l, -x, context flags, a stale index, a level-specific heading pattern, a BZZ code, a line anchor on a code, a heading inside a fence, one hit that is a call or a comment, stdin, or a tree outside the workspace. A graph describing another tree advises, as graph-stale, except for a listing, which the walk proves. " +
			"Measured 2026-09-26 over 89,116 searches in 1,441 transcripts: 8,500 looked for a symbol, 1,389 listed a file's declarations, 369 its headings, 319 diagnostic codes, 176 target declarations, 1,366 were a `find -name`."},
	{Name: string(denyRuleThrowawayCopy), Decision: "deny",
		Catches: "a run inside a temp or scratchpad copy, which leaves the real tree unverified",
		Why: "A run inside a temp or scratchpad copy judges a tree nobody ships: a green gate leaves the real tree unverified, generated files land in the copy, and the cache splits. " +
			"No magus run needs a clean tree; run from the workspace and name the project. If you genuinely need a pristine tree, use a throwaway `git worktree add`, not a copy."},
	{Name: string(denyRuleVCSOffSwitch), Decision: "deny",
		Catches: "an agent's write setting vcs.enabled: false in a magus.yaml this workspace reads",
		Why: "With vcs off, vcs.Resolve returns no VCS, so the guard has no approved copy to compare a policy edit against and every workspace rule is read from the working tree alone. " +
			"Decided by parsing the proposed magus.yaml content, never by matching text. A person editing their own checkout, with no lease and no spawn ancestry, is untouched."},
	{Name: string(denyRuleUnknownEnv), Decision: "deny",
		Catches: "a retired or misspelled MAGUS_* variable handed to a command",
		Why: "A retired or misspelled name is ignored without a word, so the setting the caller meant never takes effect and nothing says so. " +
			"Measured 2026-09-24: the day MAGUS_NO_WAIT was removed, agents prefixed 462 commands with it, copied from 33 briefs. " +
			"It fires on the names a command's environment receives: a `NAME=value` prefix, `env NAME=value`, `env -u NAME`, and `export`. It asks config.EnvVarProblem, the check magus's own startup refuses on (MGS1046), so a name the guard denies is one the binary would refuse, and one it cannot prove wrong passes both."},
	{Name: string(denyRuleBriefCommand), Decision: "deny",
		Catches: "a spawn or continuation brief that teaches a command the guard denies",
		Why: "A worker runs the commands in its brief as written, so a denied one is refused in every worker the brief reaches, or teaches each of them a way around the refusal. " +
			"Measured 2026-09-24: 33 briefs seeded 462 prefixes of a retired variable. " +
			"Only what the brief presents as a command is graded, a fenced shell block or an inline code span, with the same rules a shell line gets. A line naming a command to forbid it (never, do not, denied, instead of) is passed over, and a `<placeholder>` reads as a word rather than a redirect."},
	{Name: string(denyRuleFocusRead), Decision: "deny", Catches: "a read outside the paths a focus lease was given"},
	{Name: string(denyRuleHookWiringWrite), Decision: "deny", Catches: "a leased or agent-attributed write to the hook wiring the guard is installed by"},
	{Name: string(denyRuleLeaseGate), Decision: "deny", Catches: "a leased worker running the gate instead of the check it was assigned"},
	{Name: string(denyRuleLeaseHarness), Decision: "deny", Catches: "a leased worker rewriting the harness skill trees that steer it"},
	{Name: string(denyRuleLeaseRebind), Decision: "deny", Catches: "a leased worker rewriting who it is or what its own job row says"},
	{Name: string(denyRuleLeaseUndeclared), Decision: "deny", Catches: "a call graded under a lease id the job store has no row for, or a binding it tombstoned"},
	{Name: string(denyRuleLeaseVCS), Decision: "deny",
		Catches: "a worker lease pushing, stashing or reverting, or committing outside its own branch and checkout",
		Why: "The orchestrator lands every unit from the worker's tree, so a worker that pushes, stashes, reverts, resets, cleans, rebases, merges, cherry-picks or removes a worktree changes the state it is integrated from, and a whole-tree revert destroys a sibling's uncommitted work. " +
			"A commit is the one exception: allowed only in the lease's checkout_root, when that is a secondary checkout on a named branch other than the base, with any backend. " +
			"Every spelling is placed alike: `git -C <dir>`, `--git-dir`, `GIT_DIR=`, a `cd` before it, and vcs\\cmd from a Buzz script under the lease. " +
			"A commit whose checkout cannot be read is refused, since it cannot be shown to be the worker's own. " +
			"A worker is a row with a parent, a lease a subagent holds, or a caller that names no session; only an identified root session holding a parentless row is the root."},
	{Name: string(denyRuleLeaseWrite), Decision: "deny",
		Catches: "a leased write outside its write paths, or into a path it was denied or another lease owns",
		Why: "The boundary is the orchestrator's declaration in the job store; the guard reads it back on both surfaces, a file write and a shell line, in the same words. " +
			"A leased write before the job has reported the base it landed on is refused under the same name, since nothing yet records which revision the work applies to."},
	{Name: string(denyRuleWholeTree), Decision: "deny",
		Catches: "a whole-tree VCS reset, checkout, restore or clean, which cannot be undone",
		Why: "These destroy uncommitted and untracked work across the WHOLE tree, including a concurrent session's, and nothing recorded anywhere can give it back. " +
			"It is the one category where an over-eager refusal is the safe direction, which is why an unparsable line falls back to the pattern rather than passing. " +
			"Verify in place instead: no magus run needs a clean tree. " +
			"git's own help passes, because git documents that it prints usage without running: `git stash --help`, `git reset -h`, `git help stash`. It has to be the whole line, with nothing between the verb and the flag, so `git reset --hard --help`, `git -c ... stash --help`, a `VAR=value` prefix, `sh -c` or a pipe are judged as work."},
	{Name: string(denyRuleWorkerCheckOnly), Decision: "deny",
		Catches: "a bound worker running a target other than its row's check or one writing its write paths",
		Why: "A worker's row names one check, and the orchestrator runs every other target serially, in its own tree, after the units land. " +
			"Measured 2026-09-29: briefs told six workers they \"may also run\" `magus run lint docs`, and two ran it at the same moment from two worktrees. The cache replays a run that has landed and never two in flight, and two trees share no key, so the pair doubled the load and proved nothing the orchestrator's one run does not. " +
			"Allowed under a live lease: the row's check, with or without a charm and forwarded args; a `magus run` of a target that declares an output among the row's write paths, read from the workspace's own declarations (a `-generate` name or a charm stands in when the workspace cannot load); magus's own `go-build .` in its own checkout; and every other verb, and every run that only reports (--plan, --dry-run, --graph). " +
			"`magus affected` is never the check, since it takes its projects from the diff. An unbound caller, a row that owns the gate and a row declaring no check are untouched. " +
			"The same rule grades a spawn or continuation brief naming a live row (by `magus.lease=`, `job exec` or its JOB ID line): a brief telling that worker to run another target is refused under brief-command, quoting the line, before any worker exists."},
	{Name: string(denyRuleWorktreeRemove), Decision: "deny",
		Catches: "removing a worktree magus cannot prove holds nothing that would be lost",
		Why: "A worktree may be where another session is working right now, and what it holds may exist nowhere else. " +
			"`git worktree remove` passes only when every condition holds: the path is a linked worktree of this repository (per `git worktree list`), not the main one and not the checkout the session runs in; " +
			"it has no modified, staged or untracked files (ignored files do not count); " +
			"every commit its HEAD carries is on a remote-tracking ref or the base branch, or a job that finished (pass or fail) was taken in it and filed its result; " +
			"no live job was taken in it; and it is not locked. " +
			"The refusal names each failed condition and the command that inspects it, and `--force` changes nothing. " +
			"Removal cannot be undone, so a fact that cannot be read refuses too: an unreadable job store, a line that does not parse, a path that is not a literal word, a removal inside a conditional or behind a wrapper. " +
			"`git worktree prune` passes, since it only clears records of directories already gone, and `git worktree remove --help` and `-h`, alone on the line, print usage and pass. " +
			"`jj workspace forget` stays refused: the jj driver cannot yet report a workspace's registration or which of its commits are published, so nothing proves a forget loses nothing."},
}

// advisoryDocs documents every rule that EXPLAINS rather than refuses. Several are
// agent-shaped by construction (a lease, a focus boundary, host wiring): they are
// catalogued anyway, because a reader asking what this workspace enforces is owed the
// whole set rather than the half that happens to apply to them today.
var advisoryDocs = []types.RuleDoc{
	{Name: string(advisoryCaptureFilter), Decision: "advise",
		Catches: "a filter over a run capture or log, which cuts the failure block apart",
		Why: "A failure prints five lines together: the target, the cause, an output ref, the command that reads that ref, and the command to reproduce it. " +
			"A filter keeps the one line it matched and drops the rest, so `grep 'cause:'` keeps the symptom and discards the ref that reads the whole log two lines below it. " +
			"A range print (`sed -n '1,200p'`) is a filter too: it cuts by POSITION, and the block sits wherever the run left it. " +
			"The better route is an output contract up front, `-o jsonl --tee <file>`, queried with `jq`. " +
			"It ADVISES rather than refuses: measured 2026-09-26, about three in four denies were a search the reader needed, and the refused agent then read the whole file into context. " +
			"It fires only on a file a filter READS: a host task capture (`tasks/<id>.output`) or a run log (`.magus/logs/<hex>.log`). A pattern shaped like one, such as `grep 'global\\.output' cmd/`, is not a capture."},
	{Name: string(advisoryCheckpointState), Decision: "advise", Catches: "a command reaching for a tree's identity, which a revision alone cannot give"},
	{Name: string(advisoryDependencyInstall), Decision: "advise", Catches: "a raw package install that the cached install target already runs"},
	{Name: string(advisoryDependencyUpdate), Decision: "advise", Catches: "a raw dependency update outside a target's update charm"},
	{Name: string(advisoryEchoOnSuccess), Decision: "advise", Catches: "an `&& echo` that restates what the exit status already says"},
	{Name: string(advisoryLeaseState), Decision: "advise", Catches: "a leased write while its row reports a diverged base, a re-entered path, or a bad pattern"},
	{Name: string(advisoryStdinClosed), Decision: "advise",
		Catches: "shell commands run with stdin at end-of-file, said once per session",
		Why: "An agent's shell command inherits an open stdin nobody writes to, so anything that reads it (grep or cat with no operand, read, a prompt, ssh, a pager) waits forever, and a host that times the call out backgrounds it rather than killing it. " +
			"Measured 2026-09-29: a grep whose file operands expanded to nothing read that stdin for 3.5 hours. " +
			"Where the host lets a hook rewrite the call, the guard prefixes the command with `exec </dev/null;`, never on a refused call and never twice. A heredoc, a pipe or a `<` still give a command its input, since each sets stdin for its own command. " +
			"A prefix rather than a `{ <command>` ... `} </dev/null` group: one host's isolation check for worktree agents judges the rewritten line, and measured 2026-09-29 it refused the group as too complex even around `stat` or `git status`, where it refuses the prefix only on a line it already found borderline (runtime-computed values beside a redirect)."},
	{Name: string(advisoryTimedMagus), Decision: "advise", Catches: "`time` around a silent magus run, which already reports its own durations"},
	{Name: string(denyRuleReadNavigation), Decision: "deny",
		Catches: "a whole read of a Go, Buzz or Markdown file over 120 lines",
		Why: "The deny carries the file's declarations or headings with their lines, and the command that prints one of them, so the refused read costs nothing. " +
			"The map is parsed from the file itself, so a stale index still gets one: where the index vouches for every name, `magus refs <name> --definition --source` prints one checked against it and the graph lists the map; where it does not, and for Buzz, which nothing indexes for refs, `sed -n <first>,<last>p <file>` prints one by its lines. " +
			"A Go declaration spans its doc comment to its closing brace, each member of a grouped var, const or type is its own entry, and a method is named `Type.Method`. A Buzz entry is a top-level fun, test, object or enum, from its doc comment to the line before the next statement. " +
			"Every file a `cat` or `nl` prints is judged, so `cat a.go b.go` is refused for whichever mapped file is over the threshold. A host's read tool is restated as the same line (`cat <file>`, or `sed -n` over its offset and limit, a limit over 300 counting as whole) and judged the same way. " +
			"120 lines is the p90 of a bounded read; measured 2026-09-26 over 66,548 Bash reads, 8,394 dumped a whole Go, Buzz or Markdown file, and 3.6% of whole reads were followed by an edit of that file. " +
			"Measured 2026-09-29 over the audit's transcripts: 657 whole reads of Go or Markdown files over 120 lines ran, 3 denied, most of them on a stale index or a multi-file cat (63); about 90% were reads a map serves, the rest a read before an edit (~38), the reader's own new file (2) and a skill read whole (~17). 299 of them came through the host read tool, which nothing judged. " +
			"Buzz: 125 whole reads over 120 lines, 5 denied by any rule; a hand-read sample of 35 held 29 (83%), the misses a read right before an edit and a review of the reader's own diff. " +
			"Silent on a short file, TypeScript (no parser here), SKILL.md, AGENTS.md and a host's own instruction file (written to be read whole), a generated output, a path outside the workspace, a file that does not parse, and a read feeding a pipe or redirect."},
	{Name: string(advisoryReadSymbol), Decision: "advise",
		Catches: "a bounded read inside one indexed declaration, which refs --definition --source prints checked",
		Why:     "Silent inside a method: refs resolves bare names, so its command would print every method of that name."},
	{Name: string(advisoryFocus), Decision: "advise", Catches: "a read or write outside the paths the running job declared"},
	{Name: string(advisoryGateRepeat), Decision: "advise", Catches: "the gate run again soon after it passed, repeating work already done"},
	{Name: string(advisoryGeneratedWrite), Decision: "advise", Catches: "a hand edit to a declared output, which the next run overwrites"},
	{Name: string(advisoryGraphPipe), Decision: "advise",
		Catches: "a read-only graph verb piped into a text filter, when magus projects the record itself",
		Why: "The same answer output-pipe gives, offered rather than imposed: `-o name` for ids, `-o json` for the whole record, `-o template='{{.field}}'` for one field. " +
			"It advises on `refs`, `query`, `explain` and `describe` because they change nothing and their pipe loses no failure. Measured 2026-09-26: 819 output-pipe denies landed on these verbs, and the refused agent went back to `grep -rn`, which answers with less than the graph read it was denied."},
	{Name: string(advisoryGraphStale), Decision: "advise",
		Catches: "a graph read, or a graph-backed deny, while the graph describes another tree",
		Why: "symbol-search, grep-reader and search-translation deny in favor of a graph answer, so they first ask whether the graph describes the tree on disk: no merge, rebase, cherry-pick or revert underway, read through the workspace's version control, and a guard index built at the current revision. " +
			"When either fails the search runs, and this names why and `magus graph build`, to run once the operation is finished: a rebuild mid-rebase would describe a tree about to change. " +
			"An index that records another revision is stale for every kind, so after a history rewrite the next lookup waits for a rebuild rather than trusting it."},
	{Name: string(advisoryHookWiring), Decision: "advise", Catches: "a write to the host wiring that decides whether these rules run at all"},
	{Name: string(advisoryInstalledSkill), Decision: "advise", Catches: "a write to an installed skill copy, which re-installing discards"},
	{Name: string(advisoryLeaseInvalid), Decision: "advise", Catches: "a call naming a lease this workspace's job store does not declare"},
	{Name: string(advisoryLeaseTerminal), Decision: "advise", Catches: "a call naming a lease whose row has already finished"},
	{Name: string(advisoryLeasedPath), Decision: "advise",
		Catches: "a write into paths a running lease owns, by a caller that names no lease",
		Why: "The writer is either that lease, not saying so, or someone about to collide with whoever took it, a person or not; magus cannot tell which, so it advises rather than refuses, and says where the job was taken. " +
			"It speaks once per session per lease. Every write used to repeat it: 8,419 servings in one audit, 52% of every advisory the guard served, for a fact the writer had after the first."},
	{Name: string(advisoryInstruction), Decision: "advise", Catches: "a write to a cross-host instruction file, which every session loads whole",
		Why: "A cross-host instruction file is read in full at the start of every session on every host, so a sentence there costs context forever. " +
			"A rule the guard already refuses or doctor already reports is restated context: delete it the moment the tool starts saying it."},
	{Name: string(advisoryNewFile), Decision: "advise", Catches: "a new file in a directory whose naming has settled"},
	{Name: string(advisoryNewSourceDir), Decision: "advise", Catches: "a new file that opens a directory, which is a boundary rather than a file"},
	{Name: string(advisoryPrecedent), Decision: "advise",
		Catches: "a hunt for one distinctive name, which refs answers with verified sites",
		Why: "A precedent hunt is a search for one distinctive name, and it is the search the graph answers best: refs lists verified sites, so you land on working code instead of assembling it from grep hits. " +
			"Measured over 1,499 sessions: 42% of new files were preceded by one of these, 71% in subagent sessions, where only 12.9% reached for a magus verb at all."},
	{Name: string(advisoryPushGate), Decision: "advise", Catches: "a push the run log does not prove ungated, which names the gate and lets it through"},
	{Name: string(advisoryRegenSource), Decision: "advise", Catches: "a hand edit to a file a target regenerates"},
	{Name: string(advisoryRevertClassify), Decision: "advise",
		Catches: "a revert that has not classified what it is reverting",
		Why: "Reverting regenerated output is the wrong default. An agent that did not hand-edit a `gen/` file concludes it is not \"its\" change and discards it, but a generate target rewriting its declared outputs is the system working, and those outputs belong in the same commit as the source that moved them. " +
			"The honest test is whether the SOURCE changed, not whether anyone typed into the output. Revert only when regenerating reproduces the same diff with the target's declared inputs unchanged; that drift is environmental and worth reporting rather than discarding."},
	{Name: string(advisoryScopeDrift), Decision: "advise", Catches: "a write into a project this session has no dependency edge to"},
	{Name: string(advisorySkillSource), Decision: "advise", Catches: "a write to an installed skill copy rather than to its source"},
	{Name: string(advisorySourceRead), Decision: "advise", Catches: "an unbounded source read the symbol index has already answered"},
	{Name: string(advisorySplitRun), Decision: "advise",
		Catches: "the same target run again on a different project set, as a separate call",
		Why: "`magus run` and `magus affected` take one target and many projects, so the same target run twice on two project sets is usually one call typed as two: `magus run lint . docs` covers what `magus run lint .` and `magus run lint docs` would otherwise cost as two workspace loads. " +
			"It compares the session's last magus run/affected invocation against this one: same target, same charms, a different project set, inside a ten-minute window. The one-line shape (`magus run lint . && magus run lint docs`) is chained-run's, which refuses it and names the combined call. " +
			"Charms count as part of the target identity, so `lint` and `lint:rw` are never combined into one call. Held to one firing per session."},
	{Name: string(advisoryStageClassify), Decision: "advise", Catches: "staging without classifying, when generated and source differ"},
	{Name: string(advisoryUnleasedWrite), Decision: "advise", Catches: "a write magus cannot attribute while a fleet is running"},
}

// Rules returns the whole catalog, denies first and each tier sorted by name: the order a
// reader scans, with the tier that blocks them at the top.
func Rules() []types.RuleDoc {
	out := make([]types.RuleDoc, 0, len(denyRuleDocs)+len(advisoryDocs))
	out = append(out, denyRuleDocs...)
	out = append(out, advisoryDocs...)
	slices.SortFunc(out, func(a, b types.RuleDoc) int {
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
func Rule(name string) (types.RuleDoc, bool) {
	name = strings.TrimSpace(name)
	for _, r := range Rules() {
		if r.Name == name {
			return r, true
		}
	}
	return types.RuleDoc{}, false
}

// advisoryKinds is every enrolled kind, for the test that pairs the catalog against the
// constants. Declared here rather than derived, for the same reason the docs are: a kind
// that nothing lists is a kind nothing can miss.
var advisoryKinds = []hint.MarkerKind{
	advisorySourceRead, advisoryPrecedent, advisoryStageClassify, advisoryUnleasedWrite, advisorySkillSource,
	advisoryRegenSource, advisoryGraphStale, advisoryGateRepeat, advisoryFocus,
	advisoryHookWiring, advisoryNewFile, advisoryLeaseTerminal, advisoryLeaseInvalid, advisoryLeasedPath,
	advisoryGeneratedWrite, advisoryInstalledSkill, advisoryInstruction,
	advisoryScopeDrift, advisoryNewSourceDir, advisorySplitRun, advisoryCaptureFilter,
	advisoryGraphPipe, advisoryStdinClosed,
}

// advisoryRuleNames are the advisories that name themselves WITHOUT enrolling in the
// once-per-session gate, so they are denyRuleName values and not kinds. The catalog test
// walks them beside advisoryKinds: what makes a rule catalogable is having a name, and
// these have one.
var advisoryRuleNames = []denyRuleName{
	advisoryPushGate, advisoryRevertClassify, advisoryCheckpointState,
	advisoryReadSymbol, advisoryLeaseState, advisoryDependencyUpdate, advisoryDependencyInstall,
	advisoryEchoOnSuccess, advisoryTimedMagus,
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
	out := make([]string, 0, len(advisoryKinds)+len(advisoryRuleNames))
	for _, k := range advisoryKinds {
		out = append(out, string(k))
	}
	for _, n := range advisoryRuleNames {
		out = append(out, string(n))
	}
	return out
}
