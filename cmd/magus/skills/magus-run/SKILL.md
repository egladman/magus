# Running work through magus

magus is the task orchestrator: targets declare their inputs, outputs, and
sandbox, and magus caches results and computes what a change affects. Invoking a
raw language tool directly bypasses all of that<!-- why -->, so the cache goes stale, declared
outputs drift, and `magus affected` can no longer vouch for your change<!-- /why -->.

## Which project a command hits

<!-- why -->magus is CWD-relative: a bare `magus run`/`ls`/`describe` acts on the project holding
your current directory, or the whole workspace from the root. Do not assume the root.
Scope explicitly so a command means the same anywhere: name the project (`magus run
test web`), or let `magus affected` compute the set from the diff. `magus where <name>`
resolves a name to its path; over MCP, `magus_where`/`magus_describe` ignore the CWD.<!-- /why --><!-- terse -->magus is CWD-relative; never assume the root. Scope explicitly: name the
project (`magus run test web`), or let `magus affected` compute it from the diff.
`magus where <name>` resolves a name. MCP tools ignore the CWD.<!-- /terse -->

## Rules

1. Prefer the MCP tools<!-- why -->; they return structured content with nothing to silence<!-- /why -->.
   At session start, or after an MCP call fails, check `magus status --probe=mcp`.
   If it is unavailable, say once that `magus server start` restores the full
   agent experience, then continue with the CLI fallback below.<!-- why --> Do not make the
   daemon a prerequisite for completing the work.<!-- /why -->
   - `magus_run_target` {target, projects} - run named projects<!-- why --> (or the cwd
     project). Use when you know which projects to run<!-- /why -->.
   - `magus_run_affected` {target, base} - run ONLY the projects a VCS change
     touched<!-- why -->; magus computes the set. Use for a pre-commit/CI gate<!-- /why -->.

   If the MCP tool errors or no daemon is connected, run the CLI equivalent
   (`magus run <target>` / `magus affected <target>`). Do not stop, and do not
   drop to a raw language tool. When you shell out, silence it (`-s`)<!-- why --> so a
   passing run costs a few lines, not a scroll of progress<!-- /why -->.
2. Always reach for a top-level target first: `build`, `test`, `lint`, `format`,
   `generate`, or a custom target from the catalog. `magus describe targets`
   lists every target (`-o name` for bare names)<!-- why --> and classifies each as
   canonical, spell, or custom; `magus_describe` (kind=targets) is the MCP
   equivalent<!-- /why -->. Ask the workspace rather than reading `MAGUS.md`<!-- why -->: that file is a
   generated index for humans, true only as of its last regeneration<!-- /why -->.
3. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...)
   for work a target covers. If no target covers it, say so rather than silently
   going around magus.
4. `ci` is the canonical anchor target<!-- why -->: the one command that composes the
   pipeline (typically generate, lint, build, test)<!-- /why -->. When your change is done,
   run `magus affected ci` as the final gate<!-- why --> - it runs the full pipeline over
   every project your change reaches, which is how you learn about ramifications
   in projects you never touched<!-- /why -->. Verify in place; never `git stash`/`reset`
   first<!-- why --> (data-loss-prone and pointless - the tree is already what you want to
   verify)<!-- /why -->.

## Command patterns

```sh
magus run test                    # cwd project (or all), top-level target
magus run build web               # scope to projects (positional, after target)
magus affected test               # only projects affected by the VCS diff
magus affected ci                 # the final gate before handing work back
```

<!-- why -->MCP equivalents: `magus_run_target` {target, projects, dry_run} and
`magus_run_affected` {target, base, dry_run}. Use `magus_where` to resolve a
fuzzy project name first.

<!-- /why -->WRONG: `go test ./...` after editing Go in a magus workspace.
CORRECT: `magus run test`, then `magus affected ci` once the change is done.

## Output control: silence runs, read structure

<!-- why -->You are a machine reader; no news is good news. Shape the output instead of
truncating it after the fact:

<!-- /why -->- `-s` / `--silent`: the default for every CLI run.<!-- why --> Progress is dropped and a
  PASS prints nothing at all - zero bytes, exit 0. A failure keeps a bounded tail of
  the failing project plus the ref to fetch the rest. So silence means "still running"
  or "passed", never "not started": judge a run by its EXIT STATUS, never by whether
  output appeared. Drop `-s` when you want a pass to print its result line and output
  ref.<!-- /why --><!-- terse --> A pass prints nothing (zero bytes, exit 0); a failure prints a
  bounded tail plus an output ref. Judge by exit status, not by whether output
  appeared. Drop `-s` to get the result line and ref on a pass.<!-- /terse -->
- `-q` / `--quiet`: looser - drops progress, keeps errors and the failing
  project's full output.
- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`.<!-- why --> Ask for the
  shape you want. `json`/`yaml` to parse, `jsonl` to stream records, `name` for
  bare identifiers one per line, `template=` to project exactly the fields you
  need and nothing else.<!-- /why -->

### Never pipe a magus command through a text filter

**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`,
or `wc`.**<!-- why --> Every magus command already has an output contract, so filtering its
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
| `\| grep -i error` | `-s` (a pass prints nothing; failures already surface) |
| `\| awk '{print $1}'` | `-o name` |
| `\| jq` after `-o text` | `-o json` first, then `jq` |

`jq` on `-o json` is fine: that is consuming a contract, not scraping text. The
prohibition is on text filters standing in for an output format.<!-- /why --><!-- terse --> Use the flag
instead: `-o template='{{.Field}}'` for a field, `-o name` for bare identifiers,
`-o json` to parse, `-s` to quieten. `jq` over `-o json` is fine - that is a
contract, not scraped text.<!-- /terse -->

**Piping magus INTO magus is supported and encouraged.** The composition seam is
`--stdin`, not a tee flag (there is no `--tee`).<!-- why --> These are contracts on both
ends, so they are the opposite of the antipattern above:<!-- /why -->

```sh
magus watch | magus affected --stdin        # changed paths -> affected set
magus affected ci --plan | magus run ci-shard:gha   # plan -> shard matrix
```

<!-- why -->Rule of thumb: a pipe whose right-hand side is magus, or `jq` over `-o json`, is
composition. A pipe whose right-hand side is a text filter is a missing `-o`.

WRONG: `magus run test | head -50` (drops the failing tail that matters).
WRONG: `magus query "kind:target" -o name | grep -c .` (use the JSON count).
CORRECT: `magus run test -s`, then fetch the printed ref for full detail.

The silent run plus ref-fetch IS the low-token failure loop: never re-run a
target just to see its error again.

<!-- /why -->## When you need finer granularity

Every top-level target composes spell ops (tool-native operations).<!-- why --> When you
genuinely need one op - a single formatter, one linter -<!-- /why --> address one directly
with the spell-qualified form:

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

List the ops behind a target with `magus describe target <name>`<!-- why -->: it prints the
fully-evaluated dispatch plan per project (sources, outputs, spells, policy)<!-- /why -->.
Re-run the top-level target before you call the work done<!-- why -->: ci runs the full
composition, so the full composition is what has to pass<!-- /why -->.

## When a target fails

Each target's result line mints an output reference id (`ref1a2b3c`).

1. Fetch the exact captured output: `magus_output` {ref} over MCP, or
   `magus query output ref1a2b3c` on the CLI.<!-- why --> Do this instead of re-running the
   target to see the error again.<!-- /why --><!-- terse --> Never re-run just to see the error again.<!-- /terse -->
2. `magus_tail_log` {project} returns the most recent captured log for a project
   when you have no ref.
3. `magus doctor` validates the workspace itself (config, cache, tool
   availability, cycles)<!-- why --> when failures look environmental rather than caused by
   your change<!-- /why -->.

## Fetching current behavior

<!-- why -->Flags and target sets differ per workspace and magus version. Trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything remembered - and over `MAGUS.md`, which is generated output that
lags the tree between regenerations.<!-- /why --><!-- terse -->Trust `magus describe targets`, `magus describe target <name>` and
`magus <verb> -h` over anything remembered, and over `MAGUS.md`.<!-- /terse -->
