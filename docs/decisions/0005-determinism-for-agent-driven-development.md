---
title: "ADR 0005: determinism for agent-driven development"
order: 5
description: The v0.5.0 finishing pass, recorded in full. A stalled multi-agent change showed where agents improvise and where magus stays silent. The person drives; magus answers from the graph, the job store and typed APIs, and prints what to run. Every idea and use case raised during the pass is listed here with its state, so this page is also the plan.
tags: [adr, decision, agents, guard, mcp, jobs, vcs, buzz, hack, scope]
---

# ADR 0005: determinism for agent-driven development

- **Status:** Accepted, partly implemented. Each item below carries its own state.
- **Date:** 2026-09-29
- **States:** *done* is in the tree; *in progress* has a worker on it; *planned* is decided
  and queued; *proposed* is an idea not yet decided; *not built* was weighed and declined.

## Context

A change of about 340 files was left uncommitted when a Codex session hit its usage limit
halfway through reviewing it. That session had been finishing a Cursor session that ended
the same way. Picking the work up in Claude Code, reviewing it with five parallel Go
reviewers plus earlier TypeScript and Buzz passes (about 140 findings, nits included), and
fixing it with isolated workers showed a pattern rather than a list of bugs: wherever
magus had no deterministic answer, agents improvised, and wherever a guard rule had no
evidence to judge with, it went quiet.

Measured during the pass:

- Discovery went through the knowledge graph for 6% of reads and searches in recent
  Claude sessions (11% in this one, 20% for Codex, 2% for Cursor). Ranged reads took
  their line numbers from grep about 25 times as often as from the graph.
- 657 whole reads of Go or Markdown files over 120 lines ran; 3 were denied.
- 908 magus invocations across Claude transcripts were wrapped in coreutils `timeout`.
- `--concurrency 1`, recommended to keep a busy machine calm, deadlocked every target
  whose body calls `ctx.needs`.
- Two jobs whose holders died on a usage limit kept their leases for 38 hours and blocked
  new forks. No Stop or SubagentStop hook fired for the limit.
- A raw `git diff <snapshot>` reported 43 untracked files as deleted; no magus command
  could say what had changed since a snapshot, untracked files included, or who owned
  each change.
- Of 2,007 interpreter and heredoc calls, the largest groups were mining transcripts
  (260), rewriting one block (245), counting command output (216), building job records
  (125) and renaming by regex (46, 34 of them denied).

## Principles

1. **The person drives.** magus prints what to run and the person runs it. magus does
   not write host configuration or own a checkout.
2. **Determinism where magus holds the facts.** The graph, the job store, the declared
   outputs and the VCS state answer questions an agent would otherwise guess at.
3. **Typed all the way.** MCP results, Buzz members and JSON records are typed. An agent
   reads a type faster and more reliably than prose.
4. **The idiomatic middle.** Keep a part only when a person sees a better outcome from
   it, reuse an existing mechanism before adding one, and push complexity into magus only
   when it buys the user a better experience.
5. **Do not fight the model.** Agents keep their tools (Python included). magus offers a
   better path and denies only the shapes that cause harm, each deny naming the command
   it would accept.

## 1. Attention queue (the original Cursor brief)

| Use case | State |
|---|---|
| A repeat of a block the queue already holds does not toast again; waiting and permission toast only when the request is newly opened; a failure still toasts; a block with no queue row (no source id, no repository) still toasts | done |
| A request row leads with the files the event carried (`Where.Files`), stored beside the lease and kept out of the request id, so a richer where-line never re-keys a row | done |
| `magus session attention` and the console tile list those files; the agent's message stays the caption | done |
| A permission request closes from the expanded row only (Dispose appears after the row is open); waiting keeps the composer; the reason stays optional; `magus session dispose` stays a typed id; no dispose-all | done |
| The doctrine page gains the judgment entry and the "manual on purpose" row: disposing stays manual because a person has to have seen the subject | done |
| Group rows that share a subject (same outcome, lease and files) once hooks send files; dispose stays per id | proposed |
| A guard that failed open becomes a standing absence on status: the fail-open arm records a `session notify --outcome diagnostic` and status shows it | proposed |
| Leave out: review-time scores, a prompt asking the person to explain an approval, a model watching for fatigue, planted canaries, an ask on every command. Test for anything added later: if it would teach someone to clear the queue unread, it does not go in | not built |

## 2. MCP

| Use case | State |
|---|---|
| One `client` tool runs a Buzz `main(args)` against the typed magus module; the per-verb tools it covers are removed | done |
| Tool names drop the `magus_` prefix: `client`, `buzz`, `status`, `config`, `console`, `diff` (hosts already namespace by server) | done |
| Results travel as MCP structured content, byte-identical to the text copy; a skew warning drops the structured copy so a host cannot miss it | done |
| `client` is bounded at 10 minutes when called directly and declares MCP task support, so a task-capable host runs it in the background and can cancel it | done |
| Nested magus runs from a worker no longer inherit the worker flag, and their output no longer corrupts the worker's JSON reply | done |
| The workers live in `internal/interp/mcpclient` and `internal/interp/transform`, sharing one stdlib allowlist | done |
| `magus status --probe=mcp` checks only the loopback HTTP listener, so skills no longer gate the CLI fallback on it | done |
| `magus affected ci --plan -o json` is typed; the Markdown summary exists only for `-o template` | done |
| The guard grades only calls addressed to the magus server (`mcp__magus__<tool>`), and parses a `client` script to see its gate runs and job writes | done |
| One merged package for both workers (research favored it) | not built |

## 3. Guard

| Use case | State |
|---|---|
| Deny magus wrapped in `timeout`/`gtimeout`; serve `--timeout` for run and affected, the bare command otherwise, advise for `magus buzz`; exempt goroutine dumps and long-running watchers | done |
| `gtimeout` is peeled like `timeout`, so it no longer hides a command from every rule | done |
| `read-navigation` keeps judging on a stale index by parsing the file itself and serving `sed -n A,Bp` per declaration | done |
| A multi-file read is judged per file | done |
| Claude's and Codex's own Read tool is judged like Cursor's (restated as `cat` or a ranged `sed`); a limit over 300 lines counts as a whole read | done |
| Buzz files get a declaration map and ranged reads (83% measured precision, shipped as a deny) | done |
| grep used as a reader (`'func X' -A N`) is denied and served `magus refs X --definition --source` (85% measured precision) | done |
| Rules below 80% measured precision ship as advice, with the measurement in the catalog | done |
| Every recursive literal grep sent to `refs --text`; wiring Claude's Grep and Glob tools | not built |
| Precision: the interpreter-rewrite rule judges real write targets, not every tracked path a script mentions | done |
| Precision: a heredoc that merely mentions `.magus/logs` is not a write to the cache directory | proposed |
| A `magus buzz` script writing a tracked file is judged like the same write from Python | planned |
| The scratch-path deny text matches behavior (scratch edits outside the workspace are not tracked-file rewrites) | planned |
| Host policy: a recursive search of the run-log directory is denied; Windows paths match; `rg -t` values are not paths | done |
| Advise on `git diff <rev>` while untracked files exist (they read as deleted) and on hand-rolled `commit-tree` snapshots | planned |
| A Cursor hook that receives malformed JSON says so instead of allowing silently | done |
| Precision: a read-only script that merely opens files under `.magus/activity` is not a cache-directory write | planned |
| Precision: the check on worker briefs judges each command as if run in the orchestrator's checkout, so it refuses the one-command bootstrap (allowed only where no binary exists) and treats a backticked word such as `cat` in prose as a command being taught | planned |
| Agent shell commands run with stdin at end-of-file: the guard's hook prefixes `exec </dev/null;` through the host's input rewrite, announced once per session and recorded in the trail. A subagent's `grep ... $(cat <missing file>) \| wc -l` read the host's open stdin for three and a half hours, and the host backgrounds a timed-out command instead of killing it (reported upstream). It is the only rewrite the guard makes | planned |
| Every verdict records its catalog rule name (about 21,000 recorded verdicts carry none, so they cannot be tallied) | planned |
| A write to a tracked `hack/` script by a session with no lease naming it gets an ask, not a silent pass: the host's approval prompt is the person's consent to a self-improvement change. Needs a `magus\guard.ask` member | planned |

## 4. Jobs, leases and change tracking

| Use case | State |
|---|---|
| `magus vcs checkpoint --preserve` is how a dirty tree reaches isolated workers (`git switch --detach <handle>`), and jobs are graded against the preserved commit rather than its parent | planned |
| `magus diff --rev <rev>` compares the working tree with a revision, untracked content included | planned |
| The checkpoint digest covers every untracked file and binary edits | planned |
| `magus diff` and `describe file` name the lease that wrote each path and the job that owns it | planned |
| A live job whose holder provably stopped (its transcript ends in a host error such as a usage limit) shows that evidence, with its checkout's dirty and unpushed state, in `ls jobs`, `describe job` and the fork refusal | planned |
| "Stale" counts the lease's last guarded call, not only the row's last update | planned |
| Ending leases automatically, heartbeat expiry, new session-end hooks | not built |
| Widening a live job's write paths without resetting it to declared (today a re-fork resets it, found three times) | proposed |
| Replacing a declared job whose dependencies changed is judged against its new dependencies (today it is judged against the stored row, so the fix is remove and fork again) | planned |
| A typed `magus\activity` member returning resolved guard verdicts, so scripts stop reading the activity store directly | proposed |

## 5. Engine

| Use case | State |
|---|---|
| A target that calls `ctx.needs` runs at `--concurrency 1`: the Buzz pool's slot is host-owned through a generic gopherbuzz hook, and every holder (pool, spell fan-out, proc server) is visible to the deadlock check | done |
| The guard never executes bytecode an agent could plant in the working tree; the bytecode store lives in the user cache, keyed by a compiler stamp | done |
| The bytecode cache invalidates on changes to already-bound and shadowing imports, keeps import types, private names and source lines | done |
| `git cat-file` batch reads refuse a path containing a newline (it desynchronized every later read) | done |
| `!` means one thing everywhere: within one declaration call, every exclusion removes matches of every glob in that call, whatever the order; a call made only of exclusions is refused, for file globs and target patterns alike. It is parsed once where it is declared into a typed glob that carries its exclusions, and every consumer (cache, clean, describe, doctor, watch, diff, merge driver, graph) matches through that one type. A review found the first version, a flat list of strings with `!` entries, re-derived by hand in about ten places with three different meanings | done |
| Every VCS backend honors exclusions. git: `.gitattributes` keeps positive output patterns and adds one `!merge !linguist-generated` line per tracked file an exclusion carves out, computed from the real files, because gitattributes has no exclusion and last match wins. Mercurial and Sapling: a carved file is routed to `:merge` ahead of the output patterns. jj, which routes no paths: `vcs resolve` picks the files through the same typed glob | done |
| The hand-written runtime files move out of `internal/interp/bindings/gen/` into their own package, `internal/interp/bindings/ffi`; the exclusion feature stays for tests and shared directories | done |
| Mercurial's and Sapling's fallback no longer picks the magus merge tool for every conflicted file; `vcs/hgfamily.go` becomes `vcs/mercurial.go` | done |
| A fresh checkout bootstraps with one command, `go run -trimpath ./cmd/magus run go-build --no-cache .`: the real target, magus cache bypassed, Go cache kept | done |
| The root project declares its dependency on `proto` (it uses the generated Go code), which the graph lacked; `split-change` found it | planned |
| `magus --root <dir> buzz` reads the VCS of `<dir>`, not of the process's working directory | planned |
| `vcs\ref()` on a detached checkout returns what its documentation says (it returns `HEAD`, the docs say empty) | planned |

## 6. Harness and host configuration

| Use case | State |
|---|---|
| `magus agent harness apply` and `remove` are removed; `magus describe harness` prints the plan and the one merge command | done |
| Codex reaches magus MCP through the per-checkout stdio `./magus mcp` | done |
| Shell hook glue is retired: every `docs/guides/integrations/agents/*.sh` is deleted, the three session-load scripts without a Buzz twin are ported, and every reference follows | planned |
| Load the Buzz authoring skill when an agent writes a `.buzz` file. The rule (`buzz-unbriefed`) already exists but never fires on Claude Code (0 verdicts over 1,950 Buzz writes): the path and Bash hook entries must declare that they observe skill loads, and a skill load must count per agent, not per session | planned |

## 7. Scripts in `hack/`

`tools/` moves to `hack/` (*planned*). Reference scripts sit flat beside it with a
`hack/README.md` index; each carries a read-only test block that `buzz-test` runs, so a
reference cannot rot. Agents copy and adapt them; editing a shared script is a
self-improvement change the person asks for.

| Script | Replaces | State |
|---|---|---|
| `rename-symbol` | regex renames: resolves through the graph, dry-runs `refs --rename`, applies on `--apply` | planned |
| `transcript-tally` | ad hoc transcript mining: counts tool calls or pattern matches in a jsonl | planned |
| `group-changes` | `git status \| awk \| sort \| uniq -c`: groups changed files by owning project and role | planned |
| `magus-json` | `-o json \| python`: typed field access on magus results | planned |
| `widen-job` | `jq` edits of job records: reads, appends paths, puts | planned |
| `diff-trees` | ad hoc tree comparisons, skipping declared outputs | planned |
| `code-census` | awk declaration counts: aggregates symbols from the graph | planned |
| `import-session` | picking up a session that ended abruptly (Codex, Claude Code, Cursor): prints what was asked, how it ended, the checkout and its state, and the preserve-and-switch commands, never writing into the other checkout | planned |
| `split-change` | splitting a large change by hand: groups by owning project, attaches outputs to their generator, orders by project dependencies, prints a stack of branches, creates local stacked branches on `--apply`, never pushes. Grouping is by project, so the root project stays one large branch; a finer split needs package or symbol coupling | done |
| `gha-run` (exists) | proving something on Linux without a pull request: pushes a scratch branch, dispatches `run.yaml`, reads the result, and `forget` deletes the run and the branch. A containerized local run (the Dagger approach) would be the principled answer; this is the quick one | done |
| An explicit ephemeral copy. `magus buzz --copy hack/x.buzz` copies the script outside the tree and outside `.magus/`, stamps its first line with the source path, digest and revision, and prints the path. Running the copy prints one line saying it is a one-off (modified or not) and records its provenance in the activity trail. The tracked script is never edited in place unless the person asked (see the guard's ask above) | editing a reference script in place | planned |
| `narrow-tests` | hand-built `magus run ... -- -run` lines (1,516): changed symbols to the tests that reference them directly, one line per project. Could instead be a tests column on `affected --impact` | proposed |
| `fan-out` (with `widen-job` folded in) | hand-written job records (391 forks): expands a typed plan's globs, drops declared outputs, proves write sets disjoint and clear of live leases, prints the fork records; `--apply` writes them through `magus\job` | proposed |
| `worker-changes` | shuttling patches between checkouts (724 `git -C` calls): reads a worker's checkout, grades its changes against its job, and on `--apply` copies source changes here, refusing a path that also changed here | proposed |
| `retro` | self-improvement: tallies recent guard verdicts per rule (denies that keep being retried, advice that rarely converts, the same edit to an ephemeral copy across sessions) and prints candidate rule, skill or memory changes with the command the person runs; never applies anything | proposed |
| `provenance` | `git log -S` and `gh pr` archaeology (324): the commits, pull requests, pinning tests and memory entries behind a symbol or text; `--fetch` names the host and time | proposed |
| `checkouts` | `git worktree list` rounds: every worktree's branch, dirty files, unpushed commits, lease and last guarded call; prints a removal command only for clean, landed, unleased checkouts | proposed |
| Conflict context for `magus vcs resolve` (each remaining hunk's enclosing declaration and the commits touching it), rather than a script | awk passes over conflict markers (211) | proposed |
| Running some scripts on a schedule | | proposed |
| Long term: an agent reaching for an interpreter reaches for Buzz | | proposed |

Fixes found while planning the scripts: the `magus-buzz-write` skill's worked example
imports `json` instead of `encoding/json` and calls a missing `fs\list`; `flags\parse`
needs names declared with their dashes and says nothing of it (*planned*).

## 8. Review fixes folded into the change

Beyond the items above, the review's findings were fixed or refuted with evidence, nits
included (*done*): the desktop-toast rule for rows without a queue id, typed-nil profile
observers, doubled trace phases, the `magus clean` report shape, the Buzz source rewrite
that changed string literals, unknown MCP parameters refused, JSON integers decoded as
integers in transforms, int-keyed Buzz maps surviving conversion to Go, the removed
`magus\guard.bash` alias, the removed MCP descriptor `Member` field, typed graph results
renamed `QueryResult`, `ExplainResult` and `RefsResult` with their keys listed, pending-cache
races and framing in the guard, auth `Class` to `Kind`, and stale skill text about
paging, memory refs, impact and insight.

## Practices for agent-run work

- Hand a dirty tree to workers with `magus vcs checkpoint --preserve`; never hand-roll a
  snapshot; never ask raw `git diff <rev>` whether a tree changed.
- Bound runs with `--target-timeout` or `--timeout`, never a wrapper.
- A worker brief never says "wait and retry": a worker runs its check once and reports
  a refusal; the orchestrator validates centrally. A worker is never parked waiting for a
  message: if its assignment may change it is stopped and a fresh one started, and later
  work is sequenced with job dependencies.
- A live job's write paths are widened with `magus\job\put`, which keeps its state; a
  re-fork resets it.
- Stale leases held by dead sessions are ended one at a time by a person, after their
  checkouts are rescued.

## Consequences

- Breaking changes before 1.0, each with a changelog entry: tool names, the removed
  harness verbs, the removed `magus\guard.bash`, renamed typed results, and the `tools/`
  path.
- The guard denies more; each new deny names the command it would accept and records its
  measured precision.
- Workers build on a preserved commit in their own worktrees; the orchestrator
  integrates their source changes and regenerates outputs once.
