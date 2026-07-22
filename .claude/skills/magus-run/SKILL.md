---
name: magus-run
description: Run builds, tests, lints, and codegen through magus targets instead of raw language tools. Use when working in a repo that has a magusfile.buzz and you are about to build, test, lint, format, or regenerate anything - especially before reaching for go test, npm, eslint, pytest, or another tool directly. Also use when a magus target fails and you need its captured output.
---

# Running work through magus

magus is the task orchestrator: targets declare their inputs, outputs, and
sandbox, and magus caches results and computes what a change affects. Invoking a
raw language tool directly bypasses all of that, so the cache goes stale, declared
outputs drift, and `magus affected` can no longer vouch for your change.

## Rules

1. Prefer the MCP tools; they return structured content with nothing to silence.
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
   `generate`, or a custom target from the catalog. `MAGUS.md` (committed at the
   workspace root) lists every target per project; `magus_describe`
   (kind=targets) classifies each as canonical, spell, or custom.
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
- `-o json`: when you will parse the result (run records, describe, query),
  ask for structure instead of scraping human text.

WRONG: `magus run test | head -50` (lossy: drops the failing tail that matters).
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

Flags and target sets differ per workspace and magus version. Trust `MAGUS.md`,
`magus describe target <name>`, and `magus <verb> -h` over anything remembered.

<!-- generated by: magus agent install; agent-skill-version: 9; knowledge-schema-version: 6; do not edit, re-run to update -->
