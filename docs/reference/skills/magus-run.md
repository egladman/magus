---
title: magus-run
description: "Run builds, tests, lints, and codegen through magus targets."
tags: [agents, skills, magus-run]
skill_full_bytes: 8164
skill_simple_bytes: 6630
---

# magus-run

Run builds, tests, lints, and codegen through magus targets. Use BEFORE typing go test, go build, npm test, npx, eslint, prettier, pytest, tsc, cargo, or any other raw language tool in a repo with magusfile.buzz at the root - a target covers the work, and the raw tool bypasses the cache, the sandbox, and affected tracking. Also use when a magus target fails and you need its captured output, and for the final pre-commit gate (magus affected ci).

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills            # the full form below
magus agent install .claude/skills --simple   # the short form below
```

An installed copy carries a provenance stamp, so `magus graph verify` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## Full form

The default: the steps plus the rationale for each.

```markdown
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
   Two distinct tools, pick by what you know:
   - `magus_run_target` {target, projects} - run named projects (or the cwd
     project). Use when you know which projects to run.
   - `magus_run_affected` {target, base} - run ONLY the projects a VCS change
     touched; magus computes the set. Use for a pre-commit/CI gate.

   Fallback is an instruction, not a hint: if the MCP tool errors or no magus
   daemon is connected, run the CLI equivalent (`magus run <target>` /
   `magus affected <target>`) instead. Do not stop, and do not drop to a raw
   language tool. When you shell out, silence it (`-s`, next section) so a
   passing run costs a few lines, not a scroll of progress.
2. Always reach for a top-level target first: `build`, `test`, `lint`, `format`,
   `generate`, or a custom target from the catalog. `magus describe targets`
   lists every target (`-o name` for bare names) and classifies each as
   canonical, spell, or custom; `magus_describe` (kind=targets) is the MCP
   equivalent. Ask the workspace rather than reading `MAGUS.md`: that file is a
   generated index for humans, true only as of its last regeneration.
3. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...)
   for work a target covers. If no target covers it, say so rather than silently
   going around magus.
4. `ci` is the canonical anchor target: the one command that composes the
   pipeline (typically generate, lint, build, test). When you consider your
   change done, run `magus affected ci` as the final gate - it runs the full
   pipeline over every project your change reaches, which is how you learn about
   ramifications in projects you never touched. Verify the build in place; never
   `git stash`/`reset` first (data-loss-prone and pointless - the tree is
   already what you want to verify).

## Command patterns

```sh
magus run test                    # cwd project (or all), top-level target
magus run build web               # scope to projects (positional, after target)
magus affected test               # only projects affected by the VCS diff
magus affected ci                 # the final gate before handing work back
```

MCP equivalents: `magus_run_target` {target, projects, dry_run} and
`magus_run_affected` {target, base, dry_run}. Use `magus_where` to resolve a
fuzzy project name first.

WRONG: `go test ./...` after editing Go in a magus workspace.
CORRECT: `magus run test`, then `magus affected ci` once the change is done.

## Output control: silence runs, read structure

You are a machine reader; no news is good news. Shape the output instead of
truncating it after the fact:

- `-s` / `--silent`: the default for every CLI run. Progress is dropped; a pass
  is a few lines (result line + output ref), a failure keeps a bounded tail of
  the failing project plus the ref to fetch the rest.
- `-q` / `--quiet`: looser - drops progress, keeps errors and the failing
  project's full output.
- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`. Ask for the
  shape you want. `json`/`yaml` to parse, `jsonl` to stream records, `name` for
  bare identifiers one per line, `template=` to project exactly the fields you
  need and nothing else.

### Never pipe a magus command through a text filter

**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`,
or `wc`.** Every magus command already has an output contract, so filtering its
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
`--stdin`, not a tee flag (there is no `--tee`). These are contracts on both
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
genuinely need one op - a single formatter, one linter - address it directly with
the spell-qualified form:

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

List the ops behind a target with `magus describe target <name>`: it prints the
fully-evaluated dispatch plan per project (sources, outputs, spells, policy).
Reach for op-direct forms to iterate on one failure, then re-run the top-level
target before you call the work done: ci runs the full composition, so the
full composition is what has to pass.

## When a target fails

Each target's result line mints an output reference id (`ref1a2b3c`).

1. Fetch the exact captured output: `magus_output` {ref} over MCP, or
   `magus query output ref1a2b3c` on the CLI. Do this instead of re-running the
   target to see the error again.
2. `magus_tail_log` {project} returns the most recent captured log for a project
   when you have no ref.
3. `magus doctor` validates the workspace itself (config, cache, tool
   availability, cycles) when failures look environmental rather than caused by
   your change.

## Fetching current behavior

Flags and target sets differ per workspace and magus version. Trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything remembered - and over `MAGUS.md`, which is generated output that
lags the tree between regenerations.
```

## Short form (`--simple`)

The same steps with the rationale withheld; the bar under the heading above shows by how much. Both are hand-authored from one source body; see [Agents](../../guides/integrations/agents.md) for when to prefer which.

<details>
<summary>Show the short form</summary>

```markdown
# Running work through magus

magus is the task orchestrator: targets declare their inputs, outputs, and
sandbox, and magus caches results and computes what a change affects. Invoking a
raw language tool directly bypasses all of that.

## Which project a command hits

magus is CWD-relative: a bare `magus run`/`ls`/`describe` acts on the project holding
your current directory, or the whole workspace from the root. Do not assume the root.
Scope explicitly: name the project (`magus run
test web`), or let `magus affected` compute the set from the diff. `magus where <name>`
resolves a name to its path; over MCP, `magus_where`/`magus_describe` ignore the CWD.

## Rules

1. Prefer the MCP tools.
   At session start, or after an MCP call fails, check `magus status --probe=mcp`.
   If it is unavailable, say once that `magus server start` restores the full
   agent experience, then continue with the CLI fallback below. Do not make the
   daemon a prerequisite for completing the work.
   Two distinct tools, pick by what you know:
   - `magus_run_target` {target, projects} - run named projects (or the cwd
     project). Use when you know which projects to run.
   - `magus_run_affected` {target, base} - run ONLY the projects a VCS change
     touched; magus computes the set. Use for a pre-commit/CI gate.

   Fallback is an instruction, not a hint: if the MCP tool errors or no magus
   daemon is connected, run the CLI equivalent (`magus run <target>` /
   `magus affected <target>`) instead. Do not stop, and do not drop to a raw
   language tool. When you shell out, silence it (`-s`, next section).
2. Always reach for a top-level target first: `build`, `test`, `lint`, `format`,
   `generate`, or a custom target from the catalog. `magus describe targets`
   lists every target (`-o name` for bare names) and classifies each as
   canonical, spell, or custom; `magus_describe` (kind=targets) is the MCP
   equivalent. Ask the workspace rather than reading `MAGUS.md`.
3. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...)
   for work a target covers. If no target covers it, say so rather than silently
   going around magus.
4. `ci` is the canonical anchor target: the one command that composes the
   pipeline (typically generate, lint, build, test). When you consider your
   change done, run `magus affected ci` as the final gate. Verify the build in place; never
   `git stash`/`reset` first (data-loss-prone and pointless - the tree is
   already what you want to verify).

## Command patterns

```sh
magus run test                    # cwd project (or all), top-level target
magus run build web               # scope to projects (positional, after target)
magus affected test               # only projects affected by the VCS diff
magus affected ci                 # the final gate before handing work back
```

MCP equivalents: `magus_run_target` {target, projects, dry_run} and
`magus_run_affected` {target, base, dry_run}. Use `magus_where` to resolve a
fuzzy project name first.

WRONG: `go test ./...` after editing Go in a magus workspace.
CORRECT: `magus run test`, then `magus affected ci` once the change is done.

## Output control: silence runs, read structure

 Shape the output instead of
truncating it after the fact:

- `-s` / `--silent`: the default for every CLI run. Progress is dropped; a pass
  is a few lines (result line + output ref), a failure keeps a bounded tail of
  the failing project plus the ref to fetch the rest.
- `-q` / `--quiet`: looser - drops progress, keeps errors and the failing
  project's full output.
- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`. Ask for the
  shape you want. `json`/`yaml` to parse, `jsonl` to stream records, `name` for
  bare identifiers one per line, `template=` to project exactly the fields you
  need and nothing else.

### Never pipe a magus command through a text filter

**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`,
or `wc`.** Every magus command already has an output contract, so filtering its
text after the fact is always the wrong tool.
Replace the filter with the flag that already does it:

| Instead of | Use |
|---|---|
| `\| grep <field>` | `-o template='{{.Field}}'` |
| `\| grep -c .` (counting) | `-o json` and read the count, or the verb's own summary |
| `\| head` / `\| tail` (quieting) | `-s` / `--silent` |
| `\| grep -i error` | `-s` (a pass prints almost nothing; failures already surface) |
| `\| awk '{print $1}'` | `-o name` |
| `\| jq` after `-o text` | `-o json` first, then `jq` |

`jq` on `-o json` is fine.

**Piping magus INTO magus is supported and encouraged.** The composition seam is
`--stdin`, not a tee flag (there is no `--tee`).

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
genuinely need one op - a single formatter, one linter - address it directly with
the spell-qualified form:

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

List the ops behind a target with `magus describe target <name>`: it prints the
fully-evaluated dispatch plan per project (sources, outputs, spells, policy).
Reach for op-direct forms to iterate on one failure, then re-run the top-level
target before you call the work done.

## When a target fails

Each target's result line mints an output reference id (`ref1a2b3c`).

1. Fetch the exact captured output: `magus_output` {ref} over MCP, or
   `magus query output ref1a2b3c` on the CLI.
2. `magus_tail_log` {project} returns the most recent captured log for a project
   when you have no ref.
3. `magus doctor` validates the workspace itself (config, cache, tool
   availability, cycles).

## Fetching current behavior

Flags and target sets differ per workspace and magus version. Trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything remembered - and over `MAGUS.md`.
```

</details>
