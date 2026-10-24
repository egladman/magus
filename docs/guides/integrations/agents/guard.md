---
title: The guard
description: "What magus session hook denies, what it only notes, and why: the five deny grounds, how a workspace sets a built-in rule, the file-write rules, the verdict contract a host wires into, and the observations magus records."
tags: [agents, guard, hooks, magus session hook, telemetry, activity]
---

# The guard

## What the guard is

The name follows established access-control terms (NIST SP 800-162,
<https://nvlpubs.nist.gov/nistpubs/specialpublications/nist.sp.800-162.pdf>).
Each agent host's hook is the policy enforcement point: it intercepts the tool
call and applies the verdict it gets back. magus's guard, `magus session
hook`, is the policy decision point: it decides allow, advise, or deny from
facts the repository already carries, such as declared outputs, the cache,
the graph, VCS state, and job leases. The workspace's rules, built in plus
anything declared in the root magusfile, are its policy. One sentence may
borrow "shield" as an analogy (Alshiekh et al., Safe Reinforcement Learning
via Shielding, AAAI 2018,
<https://cdn.aaai.org/ojs/11797/11797-13-15325-1-2-20201228.pdf>): a shield is a
monitor that vetoes a learning agent's proposed actions against a spec, and
the guard plays that role for magus, never choosing an action itself. magus
never calls a model and never owns the host's loop; see
[Prior art and terminology](../../../scope.md#prior-art-and-terminology) for
how this differs from a content-safety guardrail or an agent harness.

Most agent hosts can run a hook before executing a shell command or writing a
file. magus supplies the rule evaluation; the host supplies the hook that calls
it. `magus session hook` reads one command or one path, applies the rules, and
returns a neutral verdict. A harness descriptor merges opaque host-config
fragments that already name the shipped scripts or plugin; Magus does not
inject a reserved command or a reply codec. Portable hosts can render the
neutral verdict themselves.

Wiring is per host: [Claude Code](claude-code.md), [Codex](codex.md),
[Cursor](cursor.md), [OpenCode](opencode.md), or
[any host that can run a command](any-host.md). The rules below are the same
everywhere, because they come from one binary.

## Deny with a remedy

A rule worth having denies, and its refusal names a command the caller can run
now. An agent disregards advice that only prints, repeatedly, so the guard does
not ship advice as a default ([ADR 0013](../../../decisions/0013-the-guard-denies.md)).

A whole-tree `git reset --hard` destroys uncommitted and untracked work,
including a concurrent agent's, and nothing brings it back. A raw `go test`,
`git add -A` or a pipe on magus's output can be redone, but the replacement
answers what the refused call asked, and the refusal serves it. Both deny.

A rule whose remedy does not clear it is a notice: it states a fact, such as
which lease owns a path or why a graph-backed rule stood down, and never
refuses. A refusal that every retry meets again teaches the caller to route
around the guard.

A workspace that disagrees with a deny sets that rule to `advise` or `off` by
name, with [`magus\guard.builtins`](#setting-a-built-in-rule).

## The deny triggers

magus denies a call on one of five grounds, and a rule that fits none of them
advises.

**It cannot be undone.** The destructive whole-tree VCS operations, a bare
`git stash` push or pop on a stack every worktree shares, removing a worktree
magus cannot prove holds nothing that would be lost, a checkout of one merge
side over a conflicted file, a push at a commit the run log shows no green gate
for, and a backtick substitution, which can pair with a stray backtick and run
a command nobody wrote.

**It breaks a provenance guarantee.** The first ground judges the write:
whether it can be taken back. This one judges what the write does to the
checkout: the artifact's value depends on a guarantee about who authored it,
and undoing the write does not restore the guarantee.

Its instances are a write into a declared notes store and a read receipt an
agent mints for itself. Both refuse an agent authoring a human's statement, and
the notes store is the one worth reading out in full. A note is the one
thing in the knowledge graph that is not derived from the workspace: a doc comes
from markdown, a rationale from a comment, a symbol from an index, an author
from git, and rebuilding the graph recovers every one of them. A note's content
originates with a person, nothing in the repository corroborates it later, and
no rebuild recovers it. One agent-written note does not damage that note; it
damages a reader's ability to trust any note without checking blame, and a note
of uncertain authorship is worthless rather than merely weaker.

That ground licenses less than it might appear. It is not "the file is
important", and it is not a general provenance rule; source files carry
authorship too, and writing them is the job. It applies only where the artifact
has no other corroboration, which is what makes authorship its entire value.

**It tampers with the guard.** A write into the magus cache dir, into the hook
wiring the guard is installed by, or to `vcs.enabled: false`; a VCS alias
defined inline, which hides the command it runs; and a leased worker rewriting
its own job row. Each rewrites the evidence a later verdict is computed from,
and no later verdict says so.

**It handles a credential.** An agent minting, printing, rotating or revoking
a token through the CLI, or reading or writing the files that hold the
operator token and the token store. An agent holds the grant it was given, and a
session that mints another holds one nobody handed it.

**It crosses a boundary someone declared.** A leased worker writing outside its
write paths, pushing, running the gate in place of its check, running a target
other than the row's check, reading outside a focus lease, or editing a
declaration another live job claims. The orchestrator drew that line in the job
store and the guard reads it back; see
[what the guard enforces under a lease](leases.md#what-the-guard-enforces-under-a-lease).

## What magus denies

The guard parses the shell rather than pattern-matching the string, so it reads
the command being RUN: an environment prefix, `env -u GOROOT ...`, a launcher,
or `bash -c '...'` all reach the same verdict as the bare command.

- Destructive whole-tree VCS operations are denied: `git stash`, `git reset --hard`,
  `git checkout .`, `git restore .`, and `git clean -f`. `git worktree remove`
  destroys another tree's work rather than this one's, so it is judged instead:
  it passes only for a clean, unlocked linked worktree of this repository that
  is not your own, that no live job was taken in, and whose commits are all
  published or were filed by a finished job. The refusal names each condition
  that failed, `--force` changes nothing, and a fact magus cannot read refuses
  (see [worktree-remove](../../../reference/rules/worktree-remove.md)).
  Reading a stash is exempt
  (`git stash list`, `git stash show`), as is `git stash create`, which returns a
  commit object without touching the working tree or the stash stack.
  Every backend magus drives is covered, in its own spelling: `hg purge` and
  `hg clean` (and Sapling's), `hg revert --all`, `hg update --clean`, `sl goto
  --clean`, `jj abandon`, `jj restore` with no paths, and `jj workspace forget`.
  Naming paths is what separates a scoped revert from a whole-tree one in every
  dialect, and the scoped form advises rather than denies.

  This list read "these rules are git-shaped ... their commands are not matched
  today" until 2026-09-08, excused by jj keeping an operation log that makes its
  equivalents undoable. That is true of jj and was generalized to Mercurial
  without argument: hg has no operation log, and `hg purge` deletes untracked
  files with no backup at all. So the users with no protection were the ones
  whose backend gave them the least.
- **Writing into the declared notes store** (`knowledge.notes.shared`), however
  the write is spelled. A file write into the store is caught by the
  file-write rules; `magus notes edit` reading piped prose is a command, so it is caught
  here. The reason names the alternative: `magus notes edit`, for a person to
  write the note themselves. The opt-in is the key in the repository's own `magus.yaml`, and
  the rule is armed from that moment, before the store holds a single note,
  because otherwise an agent could author its first note and the deny would
  switch on afterwards. A declaration made anywhere else (an explicit
  `--config`, user-global config) is in effect in every workspace on the
  machine, so it arms this rule only where the store already exists.
- **Minting a read receipt** (`magus diff --ack`). A receipt records that a
  PERSON read a change, so there is no spelling of it an agent may use. The
  guard is wired into agent hosts, so every command reaching it came from an
  agent by construction and a person at a terminal never meets this rule. The
  reason routes to `magus diff --impact`, which names every changed file
  carrying no receipt, and says to hand that list back rather than stamp it.
- Credential access is denied: the console and connector token `create` and `revoke`
  commands, `magus config token print`, `generate` and `revoke`, and
  `magus graph export --open --follow`, whose link carries a sign-in code. Any
  line, and any editor write, that names the operator token file or the token
  store is refused too, whatever the command. A bare listing of the state dir
  passes: it shows file names, and none is a secret. This is a seatbelt for a
  harness that opted in, not a boundary: a process running as the user can reach
  the same files.
- Guard wiring and the VCS off-switch are protected: a leased or agent-attributed write to
  the hook wiring the guard is installed by, and a write setting `vcs.enabled:
  false` in a `magus.yaml` this workspace reads. With VCS off the guard has no
  approved copy to compare a policy edit against, so every workspace rule would
  be read from the working tree alone. A VCS alias defined inline
  (`git -c alias.x=...`, hg's and sl's `--config alias.x=...`, jj's
  `--config aliases.x=...`) is refused for the same reason: the alias body hides
  the command every other rule reads as the subcommand.
- Backtick substitution is denied: inside double quotes a backtick starts a command
  substitution, and it pairs with the next backtick anywhere on the line, so a
  pattern or a message carrying a literal backtick can run everything between
  them. A literal backtick belongs in single quotes; a substitution is
  `$(...)`.
- A push at a commit with no green gate is judged from the run log, which names
  which invocations ran the gate and at which commit. A session no lease binds is asked; a leased
  worker is refused, since workers do not publish.
- **Writing into the workspace's magus cache dir** (`.magus/` by default), on
  either kind of rule and under every role, unbound sessions included. That directory
  holds the files the guard's own verdicts are computed from: the `lease` marker
  naming which lease this checkout is bound to, the fire-once advisory markers,
  the touched-project set, and the served-next journal whose entries
  pre-authorize commands, plus the activity trail, the run logs, the outputs and
  the locks. An agent that edits any of it rewrites the evidence it is graded by,
  and no later verdict says so, which is why this one is not scoped to a boundary:
  what it protects is whether a boundary was checked at all. It ranks above the
  lease rules, so a worker whose `write_paths` happen to cover the directory
  reads what the directory IS rather than a verdict about whose it is. The
  file-write rules catch an editor tool's write; the shell-command rules catch a
  redirect (`>`, `>>`, `tee`) and the coreutils that take a path as an operand
  (`rm`, `mv`, `cp`, `mkdir`, `touch`, `truncate`, `chmod`, `sed -i`). The reason
  names the verbs instead: `magus job exec <id>` to take a lease, `magus clean` for
  the outputs, `magus query output <ref>` for a captured log. Reading is
  untouched, so `cat` on a log passes. A cache dir relocated by
  `MAGUS_CACHE_DIR` or `cache.dir` is matched at its resolved location, and the
  literal `.magus/` name is matched as well, so a command that spells it is
  refused even where the resolution is unavailable. magus's own commands are not
  judged here: every `magus run` writes in that directory, and the rule reads
  redirect targets and coreutil operands, never a `magus` argv.
- Rewriting your own job row while holding a lease is denied: taking another job's
  lease with `magus job exec <other-id>`, verifying a result with `magus job
  wait`, and a `client` call's `magus\job` writes, whichever channel they arrive
  on. Every lease-scoped rule below reads that row, so an agent that can rewrite
  it grades itself against a boundary nobody handed it from the next call on. A
  session holding no lease is untouched entirely, whether that is an
  orchestrator or a person in their own checkout, because those are the parties
  that write rows.

  Over MCP the agent's write is the `client` tool calling `magus\job`. The
  store refuses a write to a row the caller does not own. Dropping declarations
  the row already carries, which is how a holder releases a path, passes
  through. Giving a path back cannot widen a role, and whether a particular
  shrink is legitimate is the store's judgment. Recording the base a lease
  landed on passes, because it is a procedure the write path demands.
  Reading is untouched: `magus\job.list` and `magus ls jobs`.

  The guard parses a `client` script and renders each literal `magus\job.put`,
  `register`, `clear` and `wait` call as a line the lease rules read, so a
  worker's widening or forged checkpoint is refused before the store sees it.
  Only a call addressed to the magus server (`mcp__magus__<tool>`)
  is graded as a magus call, and the call is normalized to a command line before
  any rule sees it, so what the activity trail records is what was graded.
- **A worker's magus run in the background**
  ([worker-background-run](../../../reference/rules/worker-background-run.md)). A
  worker that backgrounds its check ends its turn with the run pending and parks there,
  and nobody is told the result. The wiring reports what the call asked of the host
  (`--call-background`, `--call-timeout-ms`), and a `magus run` or `magus affected` by a
  worker under a live lease that does not integrate the checkout is refused when the host would
  run it in the background. A wiring that reports nothing leaves the rule silent.
- **A backgrounded shell command with nothing to end it**
  ([background-unbounded](../../../reference/rules/background-unbounded.md)). A command
  the host runs in the background outlives the agent that started it, and the host
  stops it only when asked. Each command in the line needs a bound: `timeout N` or
  `gtimeout N` in front of it (stock macOS ships neither; Homebrew's coreutils
  installs `gtimeout`), a magus target run (bounded by its target's timeout), or a
  builtin that returns at once. A pipeline is bounded by its first command; a `while`
  or `until` loop never is. timeout signals its own process group, so a child that
  starts its own session escapes it; add `--kill-after=10` for a command that ignores
  TERM. A watcher's command ends at its own expiry and is not judged here.
- **Finishing with background work still running**
  ([background-left](../../../reference/rules/background-left.md)). A host does not
  stop a finished subagent's background tasks, and nobody is told when one ends: a
  transcript search a scout left behind ran for four hours after it reported. At a
  subagent's stop magus reads the subagent's own transcript for the shell commands
  it ran in the background (asked for, moved there by a foreground timeout, or a
  watcher's command) that no completion notice, stop call or watcher expiry has
  ended, and refuses the stop naming each by id, start time, age and command. Stop
  each by its id, or wait for its notice, then finish. At a root session's stop it
  only advises, and only of tasks older than 30 minutes: a root wakes on a task's
  completion notice, so a young task is one it waits on.

  What each host can carry:

  | Host        | Refuses a subagent's stop                                                                            | Advises at the root's stop                                                    | Bounds a backgrounded command                 |
  | ----------- | ---------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | --------------------------------------------- |
  | Claude Code | yes (SubagentStop `decision: block`)                                                                 | yes (Stop)                                                                    | yes (`run_in_background`)                     |
  | Codex       | no: SubagentStop reaches magus, but its rollout records no background task in a shape the rule reads | no, for the same reason                                                       | no: its shell call carries no background flag |
  | Cursor      | no: `subagentStop` never fired in probes, and its transcript has no tool ids, results or times       | no: `stop` never fires under `-p`, and the transcript cannot say what is live | no: the wiring reports no background flag     |
  | OpenCode    | no: the plugin sees session events, not a stop it can refuse                                         | no                                                                            | no: its bash tool has no background mode      |

  A host the rule cannot read is never refused: the rule says nothing rather than
  guess.
- **Switching the repository's git hooks off**
  ([hooks-off](../../../reference/rules/hooks-off.md)): `--no-verify`, `git commit -n`,
  and `-c core.hooksPath=/dev/null` or an empty path. Hooks are the checks a
  repository's owners put in front of a commit or push. A hooks path naming a
  directory is a different set of hooks rather than none, and passes.
- **A here-document written to a file**
  ([heredoc-write](../../../reference/rules/heredoc-write.md)) is judged as the file
  write it is: `cat > f.go <<'EOF'` and `tee f.go <<'EOF'` meet the comment, lease and
  workspace write rules a host's write tool meets, and a refused write refuses the
  line, naming the write rule in brackets. Only a literal body is judged; one that
  expands a parameter or runs a substitution cannot be read before it runs.
- **A second worker for one job**
  ([spawn-duplicate](../../../reference/rules/spawn-duplicate.md)): a spawn titled
  `<parent>/<role> <job>` for a row still declared or running that an earlier spawn
  in the session was attributed to. Continue the worker with a message, or stop it
  first. A running row alone is not enough: a row stays running after its worker dies.

Every file a patch writes is judged, not only the first: a multi-file
`apply_patch` is held to what a write of each file alone would meet, and a refusal
names the file it is about.

## What magus denies with a served command

These rules match a call that a later command can redo. Each refusal serves the
command that answers what the call asked. They deny by default, and a workspace
sets any of them to `advise`, which serves the same command once a session and
lets the call run, or to `off`, with
[`magus\guard.builtins`](#setting-a-built-in-rule).

| rule                                                                           | denies                                                                 |
| ------------------------------------------------------------------------------ | ---------------------------------------------------------------------- |
| [`architecture-unbriefed`](../../../reference/rules/architecture-unbriefed.md) | an agent acting on structure before it read the architecture skill     |
| [`brief-command`](../../../reference/rules/brief-command.md)                   | a spawn or continuation brief that teaches a command the guard refuses |
| [`brief-plain-commands`](../../../reference/rules/brief-plain-commands.md)     | a brief for an isolated worker that shows a substitution, loop or exec |
| [`busy-wait`](../../../reference/rules/busy-wait.md)                           | a while or until loop that sleeps; a for loop that sleeps until break  |
| [`buzz-unbriefed`](../../../reference/rules/buzz-unbriefed.md)                 | the first Buzz a session authors, before it read the Buzz skill        |
| [`chained-run`](../../../reference/rules/chained-run.md)                       | magus runs sequenced with `&&` or `;`                                  |
| [`exit-status-echo`](../../../reference/rules/exit-status-echo.md)             | a line ending by printing an exit status                               |
| [`filter-without-input`](../../../reference/rules/filter-without-input.md)     | a filter with no file, pipe or redirect                                |
| [`grep-reader`](../../../reference/rules/grep-reader.md)                       | a definition lookup with a context flag                                |
| [`interpreter-rewrite`](../../../reference/rules/interpreter-rewrite.md)       | an inline interpreter rewriting a tracked file                         |
| [`magus-timeout`](../../../reference/rules/magus-timeout.md)                   | magus wrapped in `timeout`                                             |
| [`monitor-duplicate`](../../../reference/rules/monitor-duplicate.md)           | a second live watcher on one subject, or one on your own work          |
| [`monitor-pile`](../../../reference/rules/monitor-pile.md)                     | a third live waiter of one agent (refused at the fifth when raised)    |
| [`output-pipe`](../../../reference/rules/output-pipe.md)                       | magus output piped into a filter                                       |
| [`output-redirect`](../../../reference/rules/output-redirect.md)               | magus output sent to a file or discarded                               |
| [`process-poll`](../../../reference/rules/process-poll.md)                     | a process table inspected to wait on magus work                        |
| [`raw-tool`](../../../reference/rules/raw-tool.md)                             | a toolchain command a spell already wraps                              |
| [`read-navigation`](../../../reference/rules/read-navigation.md)               | a whole read of a long Go, Buzz or Markdown file                       |
| [`read-paging`](../../../reference/rules/read-paging.md)                       | the third window of a mapped file, paged through by one caller         |
| [`interpreter-rewrite`](../../../reference/rules/interpreter-rewrite.md)       | a scripted substitute-and-write                                        |
| [`search-translation`](../../../reference/rules/search-translation.md)         | a text search a graph query provably answers                           |
| [`sed-in-place`](../../../reference/rules/sed-in-place.md)                     | `sed -i`                                                               |
| [`sibling-checkout`](../../../reference/rules/sibling-checkout.md)             | a magus command relocated into another checkout                        |
| [`sleep-poll`](../../../reference/rules/sleep-poll.md)                         | a sleep before a status read, or a backgrounded call that only sleeps  |
| [`spawn-unbriefed`](../../../reference/rules/spawn-unbriefed.md)               | a subagent spawned before the multi-agent skill loaded                 |
| [`spawn-waiter`](../../../reference/rules/spawn-waiter.md)                     | a spawn whose child exists to wait on other work                       |
| [`stage-all`](../../../reference/rules/stage-all.md)                           | a whole-tree `git add`                                                 |
| [`symbol-search`](../../../reference/rules/symbol-search.md)                   | a text search for a symbol the graph answers                           |
| [`throwaway-copy`](../../../reference/rules/throwaway-copy.md)                 | a run inside a temp or scratchpad copy                                 |
| [`unjudged-tool`](../../../reference/rules/unjudged-tool.md)                   | a tool call whose input no rule reads, from a tool the guard lacks     |
| [`unknown-env`](../../../reference/rules/unknown-env.md)                       | a retired or misspelled `MAGUS_*` variable                             |

The notices, such as `split-run` and `leased-path`, are in
[What magus explains](#what-magus-explains), beside `capture-filter` and
`graph-pipe`, which also deny by default. What follows says what each rule
matches; a workspace that set `advise` gets the same served command as advice.

- Raw language tools are refused: `go test`, `go build`, `go mod tidy`, `cargo build`,
  `gofmt -w`, `prettier --write`, and the rest. The match is the base PROGRAM a
  registered spell op renders plus the leading argv it renders with it, so the
  matched spelling is the one a spell would launch. A tool a spell
  reaches through a runner is therefore matched under the runner:
  `uv run pytest` and `pnpm exec eslint .` match, while bare `pytest`, `eslint`
  and `ruff` pass, because no spell renders those as the program. That is
  silence rather than endorsement; a target still covers the work.
  The reason names the escalation ladder: a top-level target first, then a
  single spell op (`magus run go::go-test <project>`), which still runs through
  magus, and `--dry-run` to see the exact command either would run. Read-only
  invocations pass: `gofmt -l` and `gofmt -d` report without writing, so they
  bypass nothing.
- Staging everything is refused: `git add -A`, `git add .`, `git add -u`. A magus
  target writes its declared outputs as it runs, so a tree is routinely dirty
  with generated files you did not edit; sweeping them into a commit about
  something else is how a focused change becomes unreviewable.
- Piping or redirecting magus's own output is refused: `| tail`, `> file`, `>> file`,
  `2>&1`. The equivalent is exact: `-o name|json|template=` returns the field
  the filter was reaching for, and every run persists its full log, so a failure
  prints that path with the ref. A pipe additionally replaces the exit status
  with the last stage's, so `magus affected ci | tail` reports tail's success
  and a failing gate reads as exit 0. `magus query output <ref>` is the one
  exemption: a raw captured tool log has no schema to project.

  A text filter aimed at the file a BACKGROUNDED run was captured to is refused on
  the same ground (`capture-filter`), and it is the shape that gets past the rule above: nothing on
  `grep -n cause: <capture> | head -8` is a magus invocation. The files are the
  host's task capture (`<id>.output`) and a persisted run log
  (`.magus/logs/<hex>.log`). A failure prints `[fail]`, `cause:`, `output:`,
  `inspect:` and `reproduce:` together, so a grep for the cause drops the ref that
  reads the rest, two lines below it. A range print (`sed -n '1,200p'`) counts: it
  cuts by position and the block is wherever the run left it. Reading the file
  whole does not: `cat <capture>` and an editor tool's read are both fine.
  Backgrounding the run as `-o jsonl --tee <file>` makes the capture a contract,
  after which `jq` over it is composition.
- In-place stream edits are refused: `sed -i`, `sed --in-place`. The flag is not
  portable and the two spellings destroy each other's work: GNU reads
  `sed -i 's/x/y/' f` as an edit, BSD and macOS read that same script as the
  backup suffix, and the portable-looking `sed -i '' ...` makes GNU edit
  nothing. The command that worked where it was written mangles the file on the
  next machine, by writing, so the damage lands before anyone reads a diff.
  Every host driving this guard has a structured editor tool that applies an
  exact replacement and reports what changed. Reading with sed is untouched.
  A **scripted substitute-and-write** is the same edit by another route and
  is refused with it: `perl -i` and `ruby -i` outright, and a `python` or `node`
  one-liner whose substitution (`re.sub`, `.replace(`) is followed on the line
  by a `.write(`. Deliberately narrow (an interpreter that only WRITES a file
  is ordinary authoring and passes), so a one-liner that writes before it
  substitutes slips through, and the rule is a habit rail rather than a fence.
- **Running magus from a copy of the workspace** in a temp or scratchpad
  directory (`cd /tmp/... && magus ...`, including via a variable assigned
  earlier on the same line). The verdict would describe a tree nobody ships:
  generated files land in the copy, the cache splits, and duplicated spell
  sources trip MGS1002. To work on a different workspace, pass `--root <path>`.
  A magus command relocated into a SIBLING CHECKOUT of this repository (a `cd`
  into a linked worktree, or into the main checkout reached from inside one) is
  refused on the same ground, recognized by reading the shared git directory
  rather than by the path's name: that tree's `./magus` was linked from ITS
  sources and its cache is keyed to ITS tree, so the verdict describes neither
  checkout. Magus takes the project as an argument, and a host shell tool that
  needs a different directory for one call has a working_directory (or cwd)
  field.
- **Waiting by hand is refused.** Work you start in the background is
  announced when it ends, so the waiter rules point every wait back at that
  notice, at `magus job wait <job>` for another agent's job, or at
  `magus status --watch=15s` for the project lock. `sleep-poll` matches a
  sleep of ten seconds or more followed on the line by a one-shot status read
  (`kill -0`, the process table, `magus status` or a job row,
  `gh pr view|checks|status`, `gh run view|list`), and a backgrounded call
  whose only work is sleeping and printing: a timer, whose notice says only
  that time passed. `busy-wait` matches any `while` or `until` loop that sleeps,
  whatever it tests and whatever else each pass runs, and a `for` loop that only
  sleeps or sleeps until it breaks out. A host's watcher (Claude Code's `Monitor`, recognized by
  shape: a command, a description and a `timeout_ms`) runs its command as a
  shell line, so busy-wait and process-poll judge that command too.
  `monitor-duplicate` refuses a second live watcher on one subject (the process
  pattern, pid, file, job, lock or CI run its command reads), one on a host task
  capture or on the process of a command you backgrounded, and a second endless
  backgrounded watch (`magus status --watch`, `tail -f`, `watch`); a leased
  worker is refused an endless watch outright, since its waits are its own runs.
  `monitor-pile` counts each agent's live waiters, watchers, timers and endless
  watches together, from when each is armed to its timeout, and advises at the
  third and refuses at the fifth. Re-arming a watcher that expired passes.
- **A spawn that only waits is refused** (`spawn-waiter`): a title or brief
  opening on wait, poll, monitor, watch or babysit, or saying to wait for, on or
  until an agent, worker or job. A subagent spawning a helper that speaks of
  waiting is refused and told to hand the wait back to its parent
  (`spawn-nested-waiter`, in [What magus explains](#what-magus-explains)).
- **Paging through a mapped file is refused** (`read-paging`): the third
  distinct `sed -n` window of one Go, Buzz or Markdown file by one caller,
  counting a host read tool's offset and limit, with the file's map. A window
  inside one declaration or heading is a targeted read and never counts.
- **A brief for a worker in its own worktree is refused** when it shows a
  command with a `$()`, a loop or an exec (`brief-plain-commands`). A host that
  isolates the worker refuses each command it cannot prove stays in the
  worktree, and a worker copies the commands its brief shows.

## Setting a built-in rule

Every compiled rule has a default decision: `deny`, `advise` or `off`. Every rule
with a remedy defaults to `deny`; a notice defaults to `advise` and refuses a
`deny`, and the cost rules default to `off` until a level turns them on. A workspace sets any rule by name with
`magus\guard.builtins` in its root magusfile:

```buzz
magus\guard.builtins({
    "stage-all": "advise",
    "unknown-env": "off",
    "read-navigation": {"decision": "deny", "lines": 120},
})
```

A value is `"deny"`, `"advise"`, `"off"`, or a record of `decision` and the
rule's parameters. `lines` is the only parameter today: it sizes the whole read
`read-navigation` flags, and the binary ships no number for it. A name no rule carries, a value outside the three,
and a parameter the rule lacks are errors at load, not silent no-ops; the error
names the nearest rule. `magus describe rules` shows each default.

The declaration follows the other workspace rules. A tightening applies at once;
a loosening waits until it is committed, because the guard runs the committed copy
of the magusfile beside the working tree's and keeps the stricter answer. This repository's own
declaration is `hack/policy/builtins.buzz`: it sets read-navigation's line limit,
with the figures that justified each deny beside it.

## Workspace rules

Rules a workspace adds are additive to the built-ins above.

- **Workspace-declared shell rules** (`magus\guard.shell({...})` in the root
  magusfile): additive deny or advise entries matched against the same parsed
  invocations the built-ins see (`program` plus optional arg subset). They
  strengthen only: a workspace deny may escalate a built-in advise or a pass, and a
  workspace advise fills silence only. A rule of this kind never changes a compiled
  rule; setting one of those is [`magus\guard.builtins`](#setting-a-built-in-rule).
  Rule ids are recorded as `workspace:<name>` on the
  verdict. Declared at magusfile load time, never mid-session, and deliberately
  not `magus.yaml` (runtime knobs) and not a host harness (those are harness
  spells selected with `magus\harness.provider`; several hosts are fine when you
  bounce between tools). Optional `dialect` selects the mvdan/sh parser variant
  for outer parse when judging rules; the last declared non-empty dialect wins.
- **A workspace spawn rule** (`magus\guard.spawn(fun)` in the root magusfile): one
  Buzz function called on every subagent spawn and continuation with a normalized
  request, answering allow, advise or deny. Strengthen only, like the shell rules.
  magus ships none; see [magus\guard.spawn](../../../reference/guard-spawn.md).
- **A workspace command rule** (`magus\guard.command(fun)` in the root magusfile): one
  Buzz function called on every shell command the rules above let through, with the
  parsed programs it runs, answering allow, advise or deny. The function form of
  `magus\guard.shell`, for a rule a program-and-args match cannot express. magus ships
  none; [magus\guard.command](../../../reference/guard-command.md) walks through writing
  a repository policy. `magus\guard.write(fun)` is the same seam for the files an agent
  writes through its host's edit tools.

| dialect | parser                        |
| ------- | ----------------------------- |
| `posix` | POSIX shell                   |
| `bash`  | GNU Bash (default)            |
| `mksh`  | MirBSD Korn shell             |
| `zsh`   | Z shell                       |
| `bats`  | Bash Automated Testing System |

Example:

```buzz
magus\guard.shell({
    name: "no-curl-prod",
    decision: "deny",
    program: "curl",
    args: ["https://prod.example/health"],
    reason: "Do not hit prod from an agent shell; use staging or a magus job.",
})
magus\guard.shell({
    name: "prefer-terraform-target",
    decision: "advise",
    program: "terraform",
    reason: "Prefer the workspace terraform target: magus run plan <project>.",
})

import "magus";
import "ghcr.io/egladman/magus/spells/harness/cursor";
import "ghcr.io/egladman/magus/spells/harness/codex";
import "ghcr.io/egladman/magus/spells/harness/claude-code" as claude;
import "ghcr.io/egladman/magus/spells/harness/opencode";
magus\harness.provider(cursor)
magus\harness.provider(codex)
magus\harness.provider(claude)
magus\harness.provider(opencode)
```

Then `magus describe harness` (no id) prints every wired host's fragments and the
one command that merges them; you read it and run it, since magus never writes host
config.

## What magus explains

An advise verdict carries context your host injects, and all four documented hosts
now deliver it to the model. What differs is WHEN. Claude Code and Codex attach it
to the call that is about to run, as `additionalContext` on the same pre-tool
event that carries a deny. Cursor and OpenCode have no message channel on a
gating event, so the explanation rides the post-tool event instead and lands with
the call's result. Codex used to be sent nothing at all, on a reading of its hook
contract its own current reference contradicts; the keys that make Codex mark a
hook run failed are `continue`, `stopReason` and `suppressOutput`.

The advisories below whose text names a command deny by default and explain only
where a workspace set them to `advise`: `capture-filter`, `check-timeout`,
`checkpoint-state`, `dependency-install`, `dependency-update`, `generated-write`,
`graph-pipe`, `installed-skill`, `lease-invalid`, `model-tier-context-cap` and
`spawn-nested-waiter`. The rest are notices, which never refuse.

- `git commit` and `git add <paths>`: classify the dirty tree first. Deliberate
  staging is the replacement the rule above points at, so it is never denied.
- A path-scoped `git checkout -- <paths>` or `git restore`: regenerated output
  is a declared target output, and reverting it because you did not hand-edit it
  is what makes CI fail on drift.
- A repo-wide text search (`grep -r`, `rg`, `find -name`): the graph answers
  structural questions from declared sources: `magus refs` for a code symbol,
  `magus query` for a domain entity.
- A dependency re-resolution (`go get`, `pnpm add`, `cargo update`, `uv lock`,
  `pip-compile`): the `update` charm is what grants that write inside magus, and
  it is deliberately not part of `rw`: `rw` covers output reproducible from a
  clean checkout, `update` covers state that depends on what a registry or a
  vulnerability feed serves
  today. Applying a lockfile (`npm ci`, `pnpm install --frozen-lockfile`)
  re-resolves nothing and passes. `go mod tidy` is matched by `raw-tool`
  because a spell op renders it, and its advice carries the same `update`
  route, since routing into magus without naming the charm would send you to a
  target that refuses the write.
- A tree-identity read (`git rev-parse HEAD`, `git describe`, `git stash
  create`): `magus vcs checkpoint` prints the revision plus a digest of the
  uncommitted patch, which identifies a dirty tree where the revision alone
  cannot, and records it on the activity trail. The layout questions
  `git rev-parse` also answers (`--show-toplevel`, `--git-dir`, `--abbrev-ref`)
  pass, because a checkpoint does not replace them.
- `time magus ...`, `timeout 5m magus ...`, and `magus ... && echo done`: magus
  already reports each target's duration and verdict, already takes `--timeout`,
  and already reports success through its exit status.
- A chained `magus run` (a second `run` or `affected` after any of `;`, `&&` or
  `||`): targets compose through `ctx.needs`, so running the LAST one usually
  pulls the rest in, and each extra invocation reloads the workspace. Only the
  dependency graph knows whether the two are genuinely independent, which is why
  this advises rather than denies.
- A subagent spawning a helper whose title or brief opening speaks of waiting or
  watching ([spawn-nested-waiter](../../../reference/rules/spawn-nested-waiter.md)),
  once a session: the host tells a parent when its child ends, so the wait belongs
  to whoever spawned the work.

- The `ci` gate, under a lease whose ledger row declares a narrower
  `validation`: the gate runs once per branch, in the orchestrator's tree, after
  every unit lands, and the deny hands back the narrow target this worker was
  assigned. A caller naming no lease, a lease with no live row, a row that
  declared no validation, and a row whose validation names `ci` are all
  unaffected. See
  [what the guard enforces under a lease](leases.md#what-the-guard-enforces-under-a-lease).
- A worker's magus run whose timeout is shorter than the target's last real run allows
  for ([check-timeout](../../../reference/rules/check-timeout.md)). It names a timeout
  of half again that run, capped at the host's foreground maximum, or says the check
  cannot finish in the foreground and to ask for a narrower one, as it does when the
  call already waits the maximum. The host's foreground
  default and maximum come from the wiring (`--foreground-default-ms`,
  `--foreground-max-ms`), which the harness spell declares; without them it is silent.
  Once per target per session.

Everything else about the command itself passes. Two rules then read state
outside the command line, and speak only into the silence the rules above leave:

- The CI gate, when this workspace's run log shows it has already run several
  times in the last two hours at real cost: it runs everything the diff reaches
  by construction, and it is the target worth saving for the end. The rule reads
  the command, so running a narrower target draws silence rather than an
  advisory arguing with what it just asked for.
- A binary older than the guard rules in the tree: appended to every verdict,
  including a deny, because a stale binary's verdicts are all suspect rather
  than only the ones that matched.
- A graph read (`magus refs`, `query`, `explain`, `path`) against a symbol index
  older than the sources it describes. The command's own output says the same
  thing under the answer, which is the half that works on every host with
  nothing wired; this one arrives a call earlier.

## Focus: the read boundary

A command that READS a path outside the project a session is working in draws a
focus advisory. The focus of a session is the project holding its working
directory, everything that project declares `depends_on` transitively, any
project nested inside it, and the files directly at the workspace root plus
`.claude/skills`: the declaration, the config, and the instructions every
project resolves through, whatever the cwd. Not the siblings, and not the
projects that depend on it: `magus affected` runs that direction, from a change
outward to what it could break, and focus runs the other one, from where you
stand back to what you legitimately need.

It is the read half of a boundary whose write half is a job's `write_paths`,
and the two catch different failures. A write outside your write paths collides with
another agent, and the diff eventually shows it. A read outside it collides with
nothing and leaves no trace: it spends tokens on a tree nobody asked about, and
it carries a sibling's practices and code quality into work that never chose
them. Nothing downstream can tell that happened.

The rule reads the operands of the commands that read a file or search a tree
(`cat`, `head`, `tail`, `sed`, `wc`, `grep`, `rg`, `find`, and their neighbors),
and says nothing about anything else: a rule that fired on an interpreter or a
build tool would be guessing at what the program does with its arguments. A
pattern is not a path, so `grep`'s first operand is skipped; an operand that
resolves outside the workspace is a different rule's business; a path no project
owns has no boundary it could be outside of.

It ADVISES by default and DENIES only under a lease this checkout holds
(`magus job exec <id>`), because a hard read boundary needs somebody to have
declared one. The job's `read_paths` names the paths whose projects it may read; it
falls back to `write_paths`, since a worker leased to edit a project was pointed
at that project. Widening is that field and nothing else. There is no
environment variable that turns the rule off, because a variable would be set
once, in a wrapper, by the first worker it inconvenienced, and nothing afterwards
would say the boundary had stopped being checked.

`magus describe file <path>` answers the same question before a read rather than
after one: each entry carries `focus: out` when it falls outside, and the report
names the focus it judged against.

## Advisories are said once

The advisories that carry a standing fact rather than a correction to the
command in front of you are held to one firing per session: the graph-beats-grep hint, the classify-before-staging reminder, the
index-staleness advisory, the enroll-a-lease notice an unleased write draws, the
host-wiring notice, the terminal-lease notice, and
the repository-scoped path rules above. The focus advisory is held twice over:
once per PATH, because a second out-of-focus file is a second fact, and once per
session for the full explanation, so every firing after the first is one line.
The second
identical paragraph teaches nothing, and this page's standard says why that
matters: a check that is red by default is a check people learn to ignore,
taking the real failures with it.

Denials are never silenced, and never long. Every deny prints its verdict, one
sentence naming the problem, then `nothing ran (N commands)` when the line held
several, at most one command to run instead, and a ref to the full verdict. The
rationale, every remedy and the rule's page live behind that ref:

```text
lease harness/typo is not declared; `magus ls jobs` lists the ids.
full verdict: magus query output grd1a2b3c4d5e6f7a8b
```

Each later deny from that rule in the session swaps the verdict for one line
naming the rule and what it catches:

```text
denied again [whole-tree]: a whole-tree VCS reset, checkout, restore or clean, which cannot be undone
nothing ran (2 commands)
full verdict: magus query output grd1a2b3c4d5e6f7a8b
```

The ref is stored in the activity trail under the `grd` prefix, and the line is
a breadcrumb with the id `deny-verdict`, so `magus session hints` counts how
often it is read.

An advisory has the same shape. Its first firing prints one sentence naming
what to do and why, with at most one command, then a ref to the full advice:
the rationale and the rule's page.

```text
magus workspace: run `magus affected ci` before publishing if you have not since your last change.
full advice: magus query output grd2b3c4d5e6f7a8b9c
```

That line is a breadcrumb with the id `advice-verdict`. A brief repeat has no
rationale to store, so it prints alone. An advisory whose text is the answer
it exists to deliver, such as the commands a search routes to or the files a
new one is named against, keeps that answer inline.

The advisories that correct the command itself (a `time` wrapper, a chained
run) are exempt from that hold, because a second firing reports a
second mistake. What no advisory does is say the same thing twice: an advice
whose rule and text match one already given to the same caller in the session
is dropped, so a repeat of a standing fact, a still-failing workspace rule
among them, reaches each caller once. A caller is the session, or the subagent
within it when the host names one, since a host's subagents share their root's
session. The precedent-search advice in a workspace with no symbol index yet
is said once and then goes quiet, because a repeat saying the index is still
missing changes nothing.

A deny repeats, never silenced, with one exception: read-navigation refuses a
whole read once per caller and file. The refusal carries the file's map, so the
same caller asking for the same file again has the map in hand and chose the
whole read; that read passes.

A session is identified by the `session_id` your host reports, on the flag or in
the envelope. A host that reports none is not silenced forever: those notices
expire on a two-hour clock instead, so the next session is told again. The state
is one empty marker file per session and kind under the cache directory, swept
after a week.

## File-write rules

`printf '%s' '<file>' | magus session hook --path` judges a file path rather
than a command. `--path` is a switch and takes no value: the path arrives on
stdin exactly as a command does. Three of its rules are definitive rather
than heuristic, because each reads DECLARATIONS: the
generated-output rule classifies the path against every target's declared
outputs, the notes rule against the declared notes store, and the lease rule
against what concurrent leases declared they own (see
[leases](leases.md)). The first advises, because a hand-edited generated
file is wasteful rather than destructive; the other two deny, on the provenance
trigger and on a collision no later rule can outrank.

The lease rule denies only what a DECLARATION settles: an enrolled lease writing
onto another live lease's owned paths, onto its own forbidden paths, outside
every entry in its own owned paths, at all when its row is `read_only`, or at all
before it has registered the base it landed on. An empty owned list on a row that
is not `read_only` is a boundary nobody wrote rather than one of size zero, and
scopes nothing. Four cases the rule cannot decide that way advise instead:

- The ledger exists but does not parse. It says no boundary was checked rather
  than blocking on a file it cannot read, because a lease whose boundary
  silently stopped being checked looks exactly like one nobody declared.
- A write onto another live lease's owned paths by a writer magus cannot
  attribute to a live lease: naming none, naming an id it cannot parse, or
  naming a valid id with no live row. That is the same collision the enrolled
  case denies, and it advises because magus cannot tell "not in the fleet" from
  "in it and not saying so", and blocking a person in their own checkout is the
  worse of the two ways to be wrong. It also records the write against the
  owner, so the lease whose file just moved can find out by asking the ledger.
- An id that is not a valid lease id (at most 128 characters of `A-Za-z0-9-_./:`).
  The call is graded as if it named no lease and told so, rather than rejected:
  an id magus cannot parse is one it cannot look up either, and erroring would
  block a tool call over metadata.
- A lease whose registered base is not the checkpoint it was handed. An
  orchestrator may have rebased the plan deliberately, which magus cannot tell
  from a worker that wandered; what it can do is keep the divergence from
  staying silent until the merge finds it.

Every one of those denials names the ACTOR who can move the boundary, and it is
never the reader: "your orchestrator can widen these write paths; you cannot. Report it as
an unresolved risk and stop." The texts they replace ended by naming the
`client` tool, meaning "ask the orchestrator", and two independent readers
took it as permission and widened their own row with it.

A fourth rule DENIES and is not about the boundary at all: a write to a harness
descriptor's own guard wiring. The descriptor declares the configuration path
that switches its guard on for the host's next session, so a bound lease cannot
edit that path regardless of what its write paths say. An unbound session gets a
once-per-session advisory instead, because rewiring a host is exactly what an
orchestrator or a person does, and what they are owed is the sentence saying
which descriptor owns the file. The failure is silent either way, since a
disarmed guard and a clean session produce identical output, which is the whole
reason the rule exists.

The rest are heuristics on the path, and each only fills a silence the
definitive rules leave: a cross-host instruction file (`AGENTS.md`, `CLAUDE.md`)
is where a workspace decision goes to be invisible to the next checkout, an
installed skill is generated and the next `--force` install erases the edit, and
a new source directory is a structural choice worth making deliberately rather
than by where a file happened to land.

Two more fire only inside magus's own checkout, identified by the markers only
its sources carry, and are inert in every other workspace: a
write to a shipped skill body or the MCP tool registry routes through the
authoring method those files are maintained by, and a write to a generator input
(a `.proto`, a Buzz host module descriptor) says to regenerate in the same
commit. Both name paths and a target that belong to this repository, which a
shipped verdict may not otherwise do; the gate is what makes them legitimate,
because outside this repository neither can fire at all.

One rule reads the environment rather than the path. A process carrying spawn
ancestry that writes while naming no lease, in a workspace whose ledger holds no
live row, is told how to enroll. The ancestry is a claim any local process can
set, so it may teach and may not judge: it can only ever turn silence into an
advisory, never deny, and never change what another rule decided.

All of them say nothing on any uncertainty. A rule fired on a guess trains the
reader to ignore it, and a deny fired on a guess blocks real work.

The shipped harnesses reach these rules through the entry every tool call reaches
(see [Every tool reaches the guard](#every-tool-reaches-the-guard)). A host you wire by
hand can instead wire `magus-path.buzz` to its file-editing tool.

## The verdict contract

The input arrives however your host can produce it: as raw text on stdin, or as
the host's own JSON event. magus reads `tool_input.command`,
`tool_input.file_path`, `session_id` and `hook_event_name` out of an envelope
directly, so a host that writes one needs neither `jq` nor `--path`; a payload
carrying a file path is judged as a write.

The verdict leaves through the standard output arm: `-o json` for a
schema-versioned envelope, `-o yaml`, `-o name` for the bare decision word, or
`-o template=<go-template>` to render your host's response dialect. Bare
`-o template` lists the fields.

```sh
printf '%s' 'git stash' | magus session hook -o json
printf '%s' 'go test ./...' | magus session hook -o name
printf '%s' 'MAGUS.md' | magus session hook --path -o name
```

A deny exits 2 with the verdict on stdout; a pass and an advise exit 0. An
empty event passes, but one the hook cannot read fails closed as a deny:
nothing was judged, so the call is blocked rather than cleared.

A fourth decision, `ask`, is the [human in the loop](../../../design/principles/human-in-the-loop.md)
made a verdict: only the person can clear the call. It also exits 2,
so a glue that reads nothing but the exit status still stops. magus returns it only to a
caller that passes `--renders-ask`, the claim that its reply puts the call in front of
the person or refuses it. Every other caller gets `deny` with a reason saying the hook
predates approval prompts, because a glue older than the decision renders an ask as
nothing and its host reads nothing as allow. The shipped templates and the OpenCode
plugin pass the flag; a `HOST_RESPONSE` you wrote yourself does not. Today the push gate
is its one source: a push at a commit no passing gate covers, from a session no job
lease binds, gets `ask`, and the reason names the commit and the gate state. The gate
covers every backend magus drives: `git push`, `hg push`, `sl push` (with or without
`--to`) and `jj git push`, relocated with `git -C`, `hg -R`, `sl -R` or `jj -R` too. It
matches the gate to the revision by content hash (git and Mercurial nodes, jj commit
ids); a revision it cannot match that way, such as a Mercurial local revision number or
a jj change id, makes it stand down rather than guess. The
host's own approval prompt shows it, and approving publishes the commit. Claude
Code and Cursor prompt from the hook. Codex and OpenCode hooks cannot, so their
harnesses write a native prompt rule (`.codex/rules/magus.rules`, and
`permission.bash` in `opencode.json`) and the hook answers that prompt: allow for a
push a gate covers, ask for an ungated one. Where no prompt can happen, those two
refuse the push and name the person's own terminal. A session bound to a job lease
always gets `deny`, because workers do not publish. Nothing the agent types clears
an `ask`, and every shipped template refuses a decision it does not know rather
than allowing it.

Every verdict carries `lease`: the ledger row it was graded under, absent when the
call named none. `--lease <id>` sets it and `magus.lease` in `$BAGGAGE` is the
default, so a shell that exported one for a whole session is still overridable per
call. The field answers a question nothing else on the wire did, which
declaration decided this, and two of the three ways to get it wrong used to look
identical to a guarded session:

- An id this workspace's ledger does not DECLARE is an error verdict: decision
  `deny`, exit 2, naming the id. Every lease-scoped rule reads that row, so a
  typo'd id was graded by nothing at all while the session believed itself inside
  a boundary. An id that will not PARSE is the other case and stays an advisory:
  magus cannot look it up either, and blocking a tool call over unreadable
  metadata is the failure the fail-open contract is written against.
- An id naming a row in a terminal state (`pass`, `fail`, `no_return`) prints one
  notice per session saying its rules are inert. That is true and was invisible:
  the lease-scoped rules only ever read live rows.

## Every tool reaches the guard

Every shipped harness wires every tool call to the guard, not only the shell, the
file editors and magus's own MCP tools: Claude Code and Codex through one `PreToolUse`
entry matching `*`, Cursor through `preToolUse` matching `*`, OpenCode through the
plugin's `tool.execute.before`. The glue hands `magus shell` the whole event, and the
guard judges the call by the shape of its input:

- a `command` is a command line, judged by the command rules;
- a file path is a read or a write, which the tool decides: a read is judged as the
  `cat` or `sed -n` line it stands for, so `read-navigation` and `read-paging` hold
  whichever tool reads, and anything else is judged as a write;
- a search pattern is the `rg` or `find` line it stands for;
- a prompt is a spawn, a skill name a skill load, a magus MCP call the CLI verb it
  stands for.

What the shape cannot settle, the guard reads from the host's tool rows: each tool by
the host's own name, with what it can do: run a command, read, write, spawn, reach the
network, or touch nothing at all (a todo list, a plan, a web lookup, a read of a
background task's output). The harness spell declares the rows (`mgs_listTools`), and
`magus agent harness install` writes them beside the host's config, one file per
capability with one tool name per line and a version file. The hook names that directory
with `--tool-rows`, so the guard reads them with no workspace load. A call to a tool that
touches nothing passes before any rule loads, so wiring every tool costs the host no wait
it did not already pay. With no rows to read (a hook config older than them, an install
that never ran) no tool is known, and the refusal says so and names the install;
`magus doctor` reports rows that differ from what the spell declares.

A call whose input has none of those shapes, from a tool the rows do not list, is
one no rule could judge. The
[`unjudged-tool`](../../../reference/rules/unjudged-tool.md) rule names it: it advises
once per session by default, since the guard cannot know such a tool is harmful, and
a workspace that wants every call judged sets it to `deny` with
[`magus\guard.builtins`](#setting-a-built-in-rule). Another MCP server's tool is the
common case.

## Commands magus suggested

A result's `next` breadcrumbs are complete commands magus computed, and a command
this session was served is PRE-AUTHORIZED: no advisory fires on it, and the
role-scoped rules (the gate, version control, the rebind rule, the focus deny)
stand down. Refusing magus's own suggestion is the tool disagreeing with itself in
front of a reader who has no way to tell which half to believe.

The workspace-wide denies never yield: whole-tree VCS, a stash on the shared
stack, a push at an ungated commit. Those protect everyone rather than a role.

The clearance is narrow by construction. It covers one command per line, matched
argv for argv against what was served, with only the binary's spelling normalized
(`./magus`, `magus`, an absolute path from a hook template are one command). It
expires after twenty further servings, it is per session, and a journal that is
not there clears nothing. The journal is one JSON line per served breadcrumb
(`{"ts":<unix ms>,"id":"<breadcrumb id>","argv":[...]}`) under the cache
directory, beside the advisory markers and keyed by the same hashed session id, so
a person asking why a call passed can read what was served. A producer with no
session id writes to the anonymous journal instead, which a CLI run always is, and
the guard reads both. The guard's activity event records which breadcrumb
cleared the call, so uptake per breadcrumb stays a query rather than a guess.

The obligation this creates sits UPSTREAM, not here: `next` is computed for the
acting role, and a test grades every breadcrumb the tree can serve through the
guard under each role, so a suggestion magus would refuse cannot ship.

A host integration is therefore a few lines of configuration you own, with no
host-specific code in magus.

## Not a security boundary

It reads a command string and returns an opinion. That catches a habit and does
nothing against intent. `TestGuardKnownHoles` records what it misses: a command
inside a script file, a program name from `$(...)` or a variable, a shell alias,
an adapter behind `make`.

You own the hook script and its response template. Edit them so denials stop
arriving, and you have configured your tool, the same way you can turn off every
rule in `.eslintrc`.

`magus affected ci` is the gate that holds. It is committed, it passes through
review, and no local config edit changes what it runs.

Ask where a config came from rather than who can edit it. One you wrote is
yours. One that arrived in a cloned repository is a stranger's code your host
may run: the same standing risk as that repo's `Makefile` or git hooks, and
older than agents. Read it before you run it.

## What magus records

Every `magus session hook` invocation with a readable command or path appends one
`agent_command` event to the local Activity Trail. This is product telemetry for
improving agent support: which host tool an agent selected, whether it reached a
magus tool or a raw command, and which guidance would move that workflow onto
magus. It is not a security feature and never an execution gate; recording is
best effort, local, and cannot change a verdict.

The hook writes a normalized request and response as content-addressed blobs
rather than the opaque host event. The request is schema-versioned and carries
only the stable fields:

```json
{
  "schema_version": 1,
  "host": "claude-code",
  "session": "abc123",
  "event": "PreToolUse",
  "tool": "Bash",
  "command": "magus run test ."
}
```

For a file-edit hook, `path` replaces `command`. The response carries the same
schema version plus `decision` and, where applicable, `reason`, `context`, or a
stable denial `rule` identifier.
`host` and `session` also sit on the event row itself, not only in the blob, so
a view can group a page of observations without fetching a payload per row.

No local process can discover which agent host started it, so the wrapper passes
the name in with `magus session hook --agent-name`; a wrapper that does not leaves the
field empty rather than guessing. An MCP call has no wrapper to ask and is
attributed from its HTTP `User-Agent` instead.

`agent_command` means observed invocation, not successful execution. A pre-tool
hook runs before the host decides whether to call the tool, so an `OUTCOME_OK`
event means magus recorded and evaluated the observation, not that a shell
process started, exited zero, or ran at all. Direct MCP calls stay
`mcp_tool_call` events for that reason: their wrapper sees the actual result.

Events live at `<cache-dir>/activity/events.jsonl` with blobs under
`<cache-dir>/activity/blobs`, under the trail's existing bounded retention
(10,000 newest events, unreferenced blobs collected on rotate). magus keeps the
commands and paths themselves, because they are the evidence that shows where
adoption breaks down, so keep credentials out of a command line. Inspect them
through the authenticated Activity view. There is no network exporter, no
scoring system, and no instrumentation inside a magus target or a Buzz execution
path.

A host without a hook cannot be observed: no local CLI can discover commands
another process did not report. The coverage boundary is explicit rather than
guessed.

## Recurring guard friction

The trail is evidence, not automatic self-modification. `magus doctor`'s
recurring-guard-denials check is read-only. It deduplicates repeated stable
denial rules (three times in one host session, or the same pattern across two
sessions) and reports the facts: rule, tool, denial count, session count, and
followed rate. It proposes no destination; a human decides whether to discard
the pattern, adapt a local skill or host harness, or report an upstream issue.

```sh
magus doctor
```

### Buzz harness spells (preferred)

A harness spell is selected by import. The shipped ones are not compiled into the
binary: they are published as OCI artifacts, imported by registry path, and pinned by
digest in `magus.lock` (see [Remote spells](../../../reference/remote-spells.md)), so
moving to a newer harness is a lock change, not an upgrade. To adapt one without
touching Magus source:

1. Copy `spells/harness/<id>/` from the magus repository into the workspace, e.g.
   `harness/<id>/`. Keep the spell's stable host id (`mgs_getName()`).
2. In `magus.yaml`, replace the registry path with the copy. The import in the root
   magusfile and `magus\harness.provider(...)` stay exactly as they are:

   ```yaml
   spells:
     ghcr.io/egladman/magus/spells/<id>:
       path: harness/<id>
   ```

3. Edit the workspace Buzz (matchers, managed fragments, guard command).
4. Merge what `magus describe harness` prints, then run `magus agent harness verify`. Commit the
   `magus.yaml` change and the forked spell together.

That is the ownership switch: Magus ships the default spell; the declaration in
`magus.yaml` chooses which tree `magus describe harness` reads, and it is the one line a
reviewer sees.
See the magus-workspace-rules skill section "Adapting a Buzz harness".

A later `magus run` request in the same host session is shown as a follow-up,
not a success: pre-tool hooks cannot observe execution or an exit status. After
a person makes a durable decision, use the existing workspace-rules loop to
record it and, when appropriate, write a stamped local skill. A host harness
update is not itself a decision. Change a compiled rule for THIS
workspace only by a committed declaration: `magus\guard.builtins({...})` in the
root magusfile sets it by name (see
[Setting a built-in rule](#setting-a-built-in-rule)), and an additive
`magus\guard.shell({...})` (deny or advise matched on parsed program + args)
adds a rule of your own.

One payload shape is recorded and never judged. A hook event carrying a `prompt`
rather than a command or a file path is a lease handoff: it appends an
`agent_spawn` event and returns `pass` without evaluating a rule, because there
is no command and no path to judge, and a prompt that merely mentions a denied
command would otherwise block the lease that describes it. See
[Leases](leases.md). The spawn rules that do run read session state, and a
workspace's own [magus\guard.spawn](../../../reference/guard-spawn.md) function
reads the normalized request; none of them runs the command rules over the prose.

## Measuring adoption

The point of the grep-to-query nudge is to move a number: how often agents reach
for the knowledge graph versus a raw text search. `magus agent adoption` reports
it from shell commands: the graph-to-grep ratio, the file reads a
targeted read would beat, and the top repo-wide greps whose pattern is a real
identifier, each with the graph command its shape routes to (`magus refs` for a
symbol, `magus query` for a diagnostic code or a Buzz op).

magus analyzes commands; it never reads a host's session logs, so extraction is
yours. For Claude Code, whose sessions are JSONL under `~/.claude/projects/`:

```sh
cat ~/.claude/projects/*/*.jsonl \
  | jq -r '.message.content[]? | select(.type=="tool_use" and .name=="Bash")
           | (.input.command | split("\n")[0])' \
  | magus agent adoption
```

A 1:20 ratio means the graph is barely used. The levers that move it are an
easier query grammar to reach for (`kind=x`, `id=~re`) and the advisory that
translates a caught grep into the graph command its pattern shape routes to.
