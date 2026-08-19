---
title: magus-run
description: "Run builds, tests, lints, and codegen through magus targets."
tags: [agents, skills, magus-run]
skill_full_bytes: 10877
skill_simple_bytes: 7113
---

# magus-run

Run builds, tests, lints, and codegen through magus targets. Use BEFORE typing go test, go build, npm test, npx, eslint, prettier, pytest, tsc, cargo, or any other raw language tool in a repo with magusfile.buzz at the root - a target covers the work, and the raw tool bypasses the cache, the sandbox, and affected tracking. Also use when a magus target fails and you need its captured output, and for the final pre-commit gate (magus affected ci).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus graph verify` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus graph verify` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `37` |
| `knowledge-schema-version` | `9` |
| `skill-content` | `a8b8e03490ee` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Running work through magus

magus is the task orchestrator: targets declare their inputs, outputs, and
sandbox, and magus caches results and computes what a change affects. Invoking a
raw language tool directly bypasses all of that, so the cache goes stale, declared
outputs drift, and `magus affected` can no longer vouch for your change.

## Which project a command hits

magus is CWD-relative: a bare `magus run`/`ls`/`describe` acts on the project holding
your current directory, or the whole workspace from the root. Do not assume the root.
Scope explicitly so a command means the same anywhere: name the project (`magus run
test web`), or let `magus affected` compute the set from the diff. `magus where <name>`
resolves a name to its path; over MCP, `magus_where`/`magus_describe` ignore the CWD.

## Rules

1. Prefer the MCP tools; they return structured content with nothing to silence.
   At session start, or after an MCP call fails, check `magus status --probe=mcp`.
   If it is unavailable, say once that `magus server start` restores the full
   agent experience, then continue with the CLI fallback below. Do not make the
   daemon a prerequisite for completing the work.
   - `magus_run_target` {target, projects} - run named projects (or the cwd
     project). Use when you know which projects to run.
   - `magus_run_affected` {target, base} - run ONLY the projects a VCS change
     touched; magus computes the set. Use for a pre-commit/CI gate.

   If the MCP tool errors or no daemon is connected, run the CLI equivalent
   (`magus run <target>` / `magus affected <target>`). Do not stop, and do not
   drop to a raw language tool. When you shell out, silence it (`-s`) so a
   passing run costs a few lines, not a scroll of progress.
2. Verification is `ci`'s job, not a sequence you compose. `ci` is the one
   target name magus enforces: the command that composes the pipeline
   (typically generate, lint, build, test) in the order the magusfile declares.
   Run `magus run ci <project>` for the project you are working in, and
   `magus affected ci` as the final gate once the change is done - it runs the
   full pipeline over every project your change reaches, which is how you learn
   about ramifications in projects you never touched. Hand-running lint,
   format, and test one at a time re-derives an order the magusfile already
   owns, and the step you forget fails silently by omission. Verify in place;
   never `git stash`/`reset` first (data-loss-prone and pointless - the tree is
   already what you want to verify).
3. Reach for an individual target only to iterate on a failure `ci` named:
   rerunning one failing target is cheaper than the pipeline while you fix it,
   and `ci` afterwards proves the change. `magus describe targets` lists every
   target (`-o name` for bare names) and classifies each as canonical, spell,
   or custom; `magus_describe` (kind=targets) is the MCP equivalent. Ask the
   workspace rather than reading `MAGUS.md`: that file is a generated index
   for humans, true only as of its last regeneration.
4. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...)
   for work a target covers. If no target covers it, say so rather than silently
   going around magus.
5. Rewriting DEPENDENCY state (`go get`, `go mod tidy`, `pnpm add`, `cargo
   update`, `uv lock`, `pip-compile`) needs the `relock` charm: `magus run
   <target>:relock <project>`, so the rewrite happens inside magus, cached and
   visible to affected tracking. It is reserved and deliberately not part of `rw` -
   `rw` covers output reproducible from a clean checkout, `relock` covers state that
   depends on what a registry serves today. `ci` strips both, so a gate verifies
   the committed lockfile rather than refreshing it. Applying a lockfile (`npm ci`,
   `pnpm install --frozen-lockfile`) re-resolves nothing and needs no charm.

## Command patterns

```sh
magus run ci web                  # verify one project: the composed pipeline, in order
magus affected ci                 # the final gate: everything the diff reaches
magus run test web                # iterate on the one failing target ci named
magus affected test               # only projects affected by the VCS diff
```

MCP equivalents: `magus_run_target` {target, projects, dry_run} and
`magus_run_affected` {target, base, dry_run}. Use `magus_where` to resolve a
fuzzy project name first.

WRONG: `go test ./...` after editing Go in a magus workspace; also wrong is
hand-sequencing `magus run lint`, `format`, `test` to check your own work.
CORRECT: `magus run ci <project>` while working, `magus affected ci` once the
change is done, and a single narrower target only to iterate on a failure.

## Output control: silence runs, read structure

You are a machine reader; no news is good news. Shape the output instead of
truncating it after the fact:

- `-s` / `--silent`: the default for every CLI run. Progress is dropped; a pass
  is a few lines (result line + output ref), a failure keeps a bounded tail of
  the failing project plus the ref to fetch the rest.
  DROP it when the question is what RAN versus what replayed: the per-target
  timings and the `(cached, 320ms)` / `(ran, 5m28s)` verdict only print without
  it. Reaching for shell `time` around a silent run measures the wall clock magus
  already reported and hides which targets were cache hits.
- `-q` / `--quiet`: looser - drops progress, keeps errors and the failing
  project's full output.
- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`. Ask for the
  shape you want. `json`/`yaml` to parse, `jsonl` to stream records, `name` for
  bare identifiers one per line, `template=` to project exactly the fields you
  need and nothing else.

### Never pipe OR redirect a magus command

**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`,
or `wc`, and do NOT redirect it with `> file`, `>> file`, or `2>&1`.** Both are
denied by the guard. A pipe also REPLACES the exit status with the last stage's,
so `magus affected ci | tail` reports tail's success and a failing gate reads as
exit 0. You never need to capture the output: every run persists its full log,
and a failure prints that path along with the output ref. Every magus command already has an output contract, so filtering its
text after the fact is always the wrong tool. It is not a style preference; it
actively breaks things:

- It is lossy in the direction that matters. A truncating filter drops the
  failing tail and the output ref, which is the only part you needed.
- It hides progress on a long run, so a working command looks hung and gets
  killed.
- It scrapes a human-facing layout that is free to change, instead of reading a
  stable contract that is not.
- `grep` cannot see structure. A field you matched textually may belong to a
  different record entirely.

Replace the filter with the flag that already does it:

| Instead of | Use |
|---|---|
| `\| grep <field>` | `-o template='{{.Field}}'` |
| `\| grep -c .` (counting) | `-o json` and read the count, or the verb's own summary |
| `\| head` / `\| tail` (quieting) | `-s` / `--silent` |
| `\| grep -i error` | `-s` (a pass prints almost nothing; failures already surface) |
| `\| awk '{print $1}'` | `-o name` |
| `\| jq` after `-o text` | `-o json` first, then `jq` |

`jq` on `-o json` is fine: that is consuming a contract, not scraping text. The
prohibition is on text filters standing in for an output format.

**Piping magus INTO magus is supported and encouraged.** The composition seam is
`--stdin`. (`--tee <file>` exists but is not a composition seam: it mirrors
STRUCTURED output only - `-o json|yaml|jsonl|template` - never console text.) These are contracts on both
ends, so they are the opposite of the antipattern above:

```sh
magus watch | magus affected --stdin        # changed paths -> affected set
magus affected ci --plan | magus run ci-shard:gha   # plan -> shard matrix
```

Rule of thumb: a pipe whose right-hand side is magus, or `jq` over `-o json`, is
composition. A pipe whose right-hand side is a text filter is a missing `-o`.

WRONG: `magus run test | head -50` (drops the failing tail that matters).
WRONG: `magus query "kind:target" -o name | grep -c .` (use the JSON count).
CORRECT: `magus run test -s`, then fetch the printed ref for full detail.

The silent run plus ref-fetch IS the low-token failure loop: never re-run a
target just to see its error again.

## When you need finer granularity

Every top-level target composes spell ops (tool-native operations). When you
genuinely need one op - a single formatter, one linter - address one directly
with the spell-qualified form:

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

List the ops behind a target with `magus describe target <name>`: it prints the
fully-evaluated dispatch plan per project (sources, outputs, spells, policy).
Re-run the top-level target before you call the work done: ci runs the full
composition, so the full composition is what has to pass.

## When a target fails

Each target's result line mints an output reference id (`out1a2b3c`).

1. Fetch the exact captured output: `magus_output` {ref} over MCP, or
   `magus query output out1a2b3c` on the CLI. Do this instead of re-running the
   target to see the error again.
2. `magus_tail_log` {project} returns the most recent captured log for a project
   when you have no ref.
3. `magus doctor` validates the workspace itself (config, cache, tool
   availability, cycles) when failures look environmental rather than caused by
   your change.

## When a target is waiting on another magus process

Do not write `sleep`/`ps` polling loops or invent a second waiter. The lock
message already names the holder, and `magus status --watch=15s` reads that same
lock state continuously: holder PID, command, directory, age, and waiters.
Keep the status watch attached until the lock releases, then let the queued
target continue. A long-running target is not evidence of a hang by itself.

```sh
magus status --watch=15s
```

If the lock has crossed its stale warning threshold, report the exact holder
and inspect its captured target output. Do not send signals based on a guessed
PID or a fixed elapsed delay; process termination needs an explicit, verified
owner policy.

The same status view lists shared services, including each service's lifecycle
state and current dependent count. Use it to distinguish an idle retained
service from active shared work before deciding how to proceed.

## Fetching current behavior

Flags and target sets differ per workspace and magus version. Trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything remembered - and over `MAGUS.md`, which is generated output that
lags the tree between regenerations.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Running work through magus

magus is the task orchestrator: targets declare their inputs, outputs, and
sandbox, and magus caches results and computes what a change affects. Invoking a
raw language tool directly bypasses all of that, so the cache goes
stale and `magus affected` can no longer vouch for your change.

## Which project a command hits

magus is CWD-relative; never assume the root. Scope explicitly: name the
project (`magus run test web`), or let `magus affected` compute it from the diff.
`magus where <name>` resolves a name. MCP tools ignore the CWD.

## Rules

1. Prefer the MCP tools.
   At session start, or after an MCP call fails, check `magus status --probe=mcp`.
   If it is unavailable, say once that `magus server start` restores the full
   agent experience, then continue with the CLI fallback below.
   - `magus_run_target` {target, projects} - run named projects.
   - `magus_run_affected` {target, base} - run ONLY the projects a VCS change
     touched.

   If the MCP tool errors or no daemon is connected, run the CLI equivalent
   (`magus run <target>` / `magus affected <target>`). Do not stop, and do not
   drop to a raw language tool. When you shell out, silence it (`-s`).
2. Verification is `ci`'s job, not a sequence you compose. `ci` is the one
   target name magus enforces.
   Run `magus run ci <project>` for the project you are working in, and
   `magus affected ci` as the final gate once the change is done. Hand-running lint,
   format, and test one at a time re-derives an order the magusfile already
   owns, and the step you forget fails silently by omission. Verify in place;
   never `git stash`/`reset` first (it destroys a concurrent agent's untracked
   work, and the tree is already what you want to verify).
3. Reach for an individual target only to iterate on a failure `ci` named:
   rerunning one failing target is cheaper than the pipeline while you fix it,
   and `ci` afterwards proves the change. `magus describe targets` lists every
   target (`-o name` for bare names). Ask the
   workspace rather than reading `MAGUS.md`.
4. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...)
   for work a target covers. If no target covers it, say so rather than silently
   going around magus.
5. Rewriting DEPENDENCY state (`go get`, `go mod tidy`, `pnpm add`, `cargo
   update`, `uv lock`, `pip-compile`) needs the `relock` charm: `magus run
   <target>:relock <project>`. It is reserved and deliberately not part of `rw` -
   `rw` covers output reproducible from a clean checkout, `relock` covers state that
   depends on what a registry serves today. Applying a lockfile (`npm ci`,
   `pnpm install --frozen-lockfile`) re-resolves nothing and needs no charm.

## Command patterns

```sh
magus run ci web                  # verify one project: the composed pipeline, in order
magus affected ci                 # the final gate: everything the diff reaches
magus run test web                # iterate on the one failing target ci named
magus affected test               # only projects affected by the VCS diff
```

WRONG: `go test ./...` after editing Go in a magus workspace; also wrong is
hand-sequencing `magus run lint`, `format`, `test` to check your own work.
CORRECT: `magus run ci <project>` while working, `magus affected ci` once the
change is done, and a single narrower target only to iterate on a failure.

## Output control: silence runs, read structure

- `-s` / `--silent`: the default for every CLI run. A pass prints a
  result line plus an output ref; a failure adds a bounded tail.
  DROP it when the question is what RAN versus what replayed: the per-target
  timings and the `(cached, 320ms)` / `(ran, 5m28s)` verdict only print without
  it.
- `-q` / `--quiet`: looser - drops progress, keeps errors and the failing
  project's full output.
- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`.

### Never pipe OR redirect a magus command

**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`,
or `wc`, and do NOT redirect it with `> file`, `>> file`, or `2>&1`.** Both are
denied by the guard. A pipe also REPLACES the exit status with the last stage's,
so `magus affected ci | tail` reports tail's success and a failing gate reads as
exit 0. You never need to capture the output: every run persists its full log,
and a failure prints that path along with the output ref. Use the flag
instead: `-o template='{{.Field}}'` for a field, `-o name` for bare identifiers,
`-o json` to parse, `-s` to quieten. `jq` over `-o json` is fine - that is a
contract, not scraped text.

**Piping magus INTO magus is supported and encouraged.** The composition seam is
`--stdin`. (`--tee <file>` exists but is not a composition seam: it mirrors
STRUCTURED output only - `-o json|yaml|jsonl|template` - never console text.)

```sh
magus watch | magus affected --stdin        # changed paths -> affected set
magus affected ci --plan | magus run ci-shard:gha   # plan -> shard matrix
```

Rule of thumb: a pipe whose right-hand side is magus, or `jq` over `-o json`, is
composition. A pipe whose right-hand side is a text filter is a missing `-o`.
## When you need finer granularity

Every top-level target composes spell ops (tool-native operations). address one directly
with the spell-qualified form:

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

List the ops behind a target with `magus describe target <name>`.
Re-run the top-level target before you call the work done.

## When a target fails

Each target's result line mints an output reference id (`out1a2b3c`).

1. Fetch the exact captured output: `magus_output` {ref} over MCP, or
   `magus query output out1a2b3c` on the CLI. Never re-run just to see the error again.
2. `magus_tail_log` {project} returns the most recent captured log for a project
   when you have no ref.
3. `magus doctor` validates the workspace itself (config, cache, tool
   availability, cycles).

## When a target is waiting on another magus process

Do not write `sleep`/`ps` polling loops or invent a second waiter. The lock
message already names the holder, and `magus status --watch=15s` reads that same
lock state continuously: holder PID, command, directory, age, and waiters.
Keep the status watch attached until the lock releases, then let the queued
target continue. A long-running target is not evidence of a hang by itself.

```sh
magus status --watch=15s
```

If the lock has crossed its stale warning threshold, report the exact holder
and inspect its captured target output. Do not send signals based on a guessed
PID or a fixed elapsed delay; process termination needs an explicit, verified
owner policy.

The same status view lists shared services, including each service's lifecycle
state and current dependent count. Use it to distinguish an idle retained
service from active shared work before deciding how to proceed.

## Fetching current behavior

Trust `magus describe targets`, `magus describe target <name>` and
`magus <verb> -h` over anything remembered, and over `MAGUS.md`.
````


</details>
