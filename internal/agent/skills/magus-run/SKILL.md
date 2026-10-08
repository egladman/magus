# Running work through magus

magus is the task orchestrator, and its unit of work is the target. Targets declare
their inputs, outputs and sandbox. magus caches results and computes what a change
affects. A raw language tool bypasses all of that{{if .Full}}, so the cache goes stale, declared
outputs drift, and `magus affected` can no longer vouch for your change{{else}}: the cache goes
stale and `magus affected` cannot vouch for your change{{end}}.

## Which project a command hits

{{if .Full}}magus is CWD-relative: a bare `magus run`/`ls`/`describe` acts on the project holding
your current directory, or the whole workspace from the root. Do not assume the root.
Scope explicitly so a command means the same anywhere: name the project (`magus run
test web`), or let `magus affected` compute the set from the diff. `magus where <name>`
resolves a name to its path; over MCP, `{{tool "client"}}` (`{{buzz "where"}}`, the project that contains a directory) ignores the CWD.{{else}}magus is CWD-relative, so never assume the root. Scope explicitly:

- Name the project: `magus run test web`.
- Or let `magus affected` compute the set from the diff.

`magus where <name>` resolves a name to its path. MCP tools ignore the CWD.{{end}}

{{if .Full}}`--root <path>`, or `-C` after make's idiom, sets where that walk STARTS. Its argument
is a plain path: any directory, holding one file or none, nested or not. It is not a
workspace and not a checkout; calling it either leads a reader to think it must be
one.

What the walk FINDS is separate:

1. The nearest `magus.yaml` wins.
2. Absent any, the outermost CONTIGUOUS run of `magusfiles/`, `magusfile.buzz` or
   `go.mod` wins.

It can resolve somewhere other than the path you passed, or resolve nothing.{{else}}`--root <path>` (or `-C`) sets where the walk STARTS: any directory, not a workspace
or a checkout. Where it lands is separate. The nearest `magus.yaml` wins, else the
outermost contiguous run of `magusfiles/`, `magusfile.buzz` or `go.mod`.{{end}}

{{if .Full}}Two consequences worth knowing before you debug one. Running a binary by
absolute path does NOT set its working directory, so `/elsewhere/magus run build .`
still walks up from YOUR cwd and can act on a tree you never named. And nearest-wins
means a directory holding its own `magus.yaml` inside a larger checkout resolves to
itself, so where a command lands is a question about markers on disk, never about the
VCS.{{else}}Running a binary by absolute path does NOT set its working directory: it
still walks up from your cwd. Pass `--root` when you mean elsewhere.{{end}}

## How to run work

1. Prefer the MCP tools{{if .Full}}; they return structured content with nothing to silence{{end}}.
   Call an exposed MCP tool directly. If it is missing or its call fails, use the
   CLI fallback below.
   - Do not gate that choice on `magus status --probe=mcp`. It tests loopback HTTP,
     while a host may use stdio or the server's Unix socket.
   - `magus status --probe=readiness` checks that this workspace is loaded on the
     server socket. It does not test the host's MCP registration.
   - Hosts manage their own connection; never start a server for an agent.{{if .Full}}
     Do not make the connection a prerequisite for completing the work.{{end}}
   - `{{tool "client"}}` (`{{buzz "run"}}`): run named projects, with the same arguments as `magus run`{{if .Full}} (or the cwd
     project). Use when you know which projects to run{{end}}.
   - `{{tool "client"}}` (`{{buzz "affected"}}`): the projects a VCS change touched. It
     returns the set and does not run it{{if .Full}}. The gate is `magus affected ci`{{end}}.

   `{{tool "client"}}` is bounded at 10 minutes when called directly. A host that
   supports MCP tasks can run it as a task without that bound. Send a long run (a
   full `ci`, a gate) to the CLI unless your host runs `{{tool "client"}}` as a task.

   If the MCP tool errors or is absent, run the CLI equivalent{{if .Full}}: `magus run
   <target>`, `magus affected list` for the set, `magus affected <target>` to run
   it. Do not stop, and do not drop to a raw language tool. When you shell out,
   silence it (`-s`) so a
   passing run costs a few lines, not a scroll of progress.{{else}}:
   - `magus run <target>` to run a target.
   - `magus affected list` for the set; `magus affected <target>` to run it.
   - Silence it with `-s`. Do not stop, and do not drop to a raw language tool.{{end}}
2. Verification is `ci`'s job, not a sequence you compose. `ci` is the one target
   name magus enforces{{if .Full}}: the command that composes the pipeline
   (typically generate, lint, build, test) in the order the magusfile declares{{end}}.
   - Run `magus run ci <project>` for the project you are working in.
   - Run `magus affected ci` as the final gate once the change is done{{if .Full}}; it runs the
     full pipeline over every project your change reaches, which is how you learn
     about ramifications in projects you never touched{{end}}.
   - Never hand-run lint, format, and test one at a time. That re-derives an order
     the magusfile owns, and the step you forget fails silently by omission.
   - A gate you ADDED this session is not proven by its green. Make it FAIL once
     (break the input it checks, watch it go red, restore) before counting its
     pass. A check wired to the wrong path passes the same way.
   - Verify in place; never `git stash`/`reset` first{{if .Full}} (data-loss-prone and pointless: the tree is
     already what you want to verify){{else}}. It destroys a concurrent agent's untracked
     work, and the tree is already what you want to verify{{end}}.
3. Reach for an individual target only to iterate on a failure `ci` named.
   Rerunning one failing target is cheaper while you fix it; `ci` afterwards
   proves the change. `magus describe targets` lists every target (`-o name` for
   bare names){{if .Full}} and classifies each as canonical, spell,
   or custom; `{{tool "client"}}` (`{{buzz "describe.target"}}`) is the MCP equivalent{{end}}. Ask the
   workspace; do not read `MAGUS.md`{{if .Full}}: that file is a generated index
   for humans, true only as of its last regeneration{{end}}.
4. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...) for
   work a target covers. If no target covers it, say so; never silently go around
   magus.
5. Rewriting DEPENDENCY state needs the `update` charm: `magus run
   <target>:update <project>`{{if .Full}}, so the rewrite happens inside magus, cached and
   visible to affected tracking{{end}}. That covers `go get`, `go mod tidy`, `pnpm add`,
   `cargo update`, `uv lock` and `pip-compile`.
   - `update` is reserved and not part of `rw`. `rw` covers output reproducible
     from a clean checkout; `update` covers what a registry serves today.{{if .Full}} `ci`
     strips both, so a gate verifies the committed lockfile rather than refreshing it.{{end}}
   - Applying a lockfile (`npm ci`, `pnpm install --frozen-lockfile`) re-resolves
     nothing and needs no charm.

## Command patterns

```sh
magus run ci web                  # verify one project: the composed pipeline, in order
magus affected ci                 # the final gate: everything the diff reaches
magus run test web                # iterate on the one failing target ci named
magus affected test               # only projects affected by the VCS diff
```

{{if .Full}}MCP equivalents: `{{tool "client"}}` (`{{buzz "run"}}`) with the arguments of `magus run`.
`{{buzz "affected"}}` returns the affected project set and does not run it. `{{buzz "impact"}}` is why each project is in that set. `{{buzz "where"}}` answers which project contains a directory.

WRONG: `go test ./...` after editing Go in a magus workspace; also wrong is
hand-sequencing `magus run lint`, `format`, `test` to check your own work.
CORRECT: `magus run ci <project>` while working, `magus affected ci` once the
change is done, and a single narrower target only to iterate on a failure.

To prove a command on Linux without opening a pull request, magus's own repository
runs it on a GitHub Actions runner (`magus buzz hack/remote/on-actions.buzz --
<command>`) or in a local Podman container (`magus buzz hack/remote/on-linux.buzz --
<command>`).{{else}}WRONG: `go test ./...` after editing Go. Also wrong: hand-sequencing
`magus run lint`, `format`, `test` to check your own work.
CORRECT: `magus run ci <project>` while working, and `magus affected ci` once done.{{end}}

## Output control: silence runs, read structure

{{if .Full}}You are a machine reader; no news is good news. Shape the output instead of
truncating it after the fact:
{{end}}

- `-s` / `--silent`: the default for every CLI run.{{if .Full}} Progress is dropped; a pass
  is a few lines (result line + output ref), a failure keeps a bounded tail of
  the failing project plus the ref to fetch the rest.{{else}} A pass prints a
  result line plus an output ref; a failure adds a bounded tail.{{end}}
  DROP it when the question is what RAN versus what replayed. The per-target
  timings and the `(cached, 320ms)` / `(ran, 5m28s)` verdict print only without
  it.{{if .Full}} Reaching for shell `time` around a silent run measures the wall clock magus
  already reported and hides which targets were cache hits.{{end}}
- `-q` / `--quiet`: looser; drops progress, keeps errors and the failing project's
  full output.
- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`.{{if .Full}} Ask for the
  shape you want. `json`/`yaml` to parse, `jsonl` to stream records, `name` for
  bare identifiers one per line, `template=` to project exactly the fields you
  need and nothing else.{{end}}

### Never pipe or redirect a magus command

**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`, or
`wc`. Do NOT redirect it with `> file`, `>> file`, or `2>&1`.** The guard denies
both.

- A pipe REPLACES the exit status with the last stage's. `magus affected ci | tail`
  reports tail's success, so a failing gate reads as exit 0.
- You never need to capture the output. Every run persists its full log, and a
  failure prints that path with the output ref.{{if .Full}}

Every magus command already has an output contract, so filtering its
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
| `\| grep <field>` | `-o template='{{"{{.Field}}"}}'` |
| `\| grep -c .` (counting) | `-o json` and read the count, or the verb's own summary |
| `\| head` / `\| tail` (quieting) | `-s` / `--silent` |
| `\| grep -i error` | `-s` (a pass prints almost nothing; failures already surface) |
| `\| awk '{print $1}'` | `-o name` |
| `\| jq` after `-o text` | `-o json` first, then `jq` |

`jq` on `-o json` is fine: that is consuming a contract, not scraping text. The
prohibition is on text filters standing in for an output format.{{else}}

Use the flag instead:

- `-o template='{{"{{.Field}}"}}'` for a field.
- `-o name` for bare identifiers; `-o json` to parse.
- `-s` to quieten.

`jq` over `-o json` is fine: that is a contract, not scraped text.{{end}}

**Piping magus INTO magus is supported and encouraged.** The composition seam is
`--stdin`. `--tee <file>` is not a composition seam: it mirrors STRUCTURED output
only (`-o json|yaml|jsonl|template`), never console text.{{if .Full}} These are contracts on both
ends, so they are the opposite of the antipattern above:{{end}}

```sh
magus watch | magus affected --stdin        # changed paths -> affected set
magus affected ci --plan | magus run --stdin      # plan -> run its shards
```

A pipe into magus, or `jq` over `-o json`, is composition. A pipe into a text filter
is a missing `-o`.

A backgrounded run's capture is magus output too: grepping the task file your host
wrote hits the `capture-filter` guard rule{{if .Full}}, since a grep drops the `output:` and `inspect:` lines
under the `cause:` you matched{{end}}. Background it as `-o jsonl --tee <file>` to get a
contract you can `jq`; otherwise read the file whole.
{{if .Full}}

WRONG: `magus run test | head -50` (drops the failing tail that matters).
WRONG: `magus query "kind=target" -o name | grep -c .` (use the JSON count).
CORRECT: `magus run test -s`, then fetch the printed ref for full detail.

The silent run plus ref-fetch IS the low-token failure loop: never re-run a
target just to see its error again.
{{end}}

## When you need finer granularity

Every top-level target composes spell ops (tool-native operations).{{if .Full}} When you
genuinely need one op (a single formatter, one linter), address one{{else}} Address one op{{end}} directly
with the spell-qualified form:

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

List the ops behind a target with `magus describe target <name>`{{if .Full}}: it prints the
fully-evaluated dispatch plan per project (sources, outputs, spells, policy){{end}}.
Re-run the top-level target before you call the work done{{if .Full}}: ci runs the full
composition, so the full composition is what has to pass{{end}}.

## When a target fails

Each target's result line mints an output reference id (`out1a2b3c`).

1. Fetch the exact captured output: `{{tool "client"}}` (`{{buzz "output"}}`) over MCP, or
   `magus query output out1a2b3c` on the CLI.{{if .Full}} Do this instead of re-running the
   target to see the error again.{{else}} Never re-run just to see the error again.{{end}}
2. With no ref in hand, find it in the run that minted it. Every `magus run` prints
   one per target. `magus session` lists recent invocations with the targets
   they ran{{if .Full}}. There is no tool that fetches "the latest log for a
   project", because a second door onto the same bytes only makes an agent holding a
   ref pick between two{{end}}.
3. `magus doctor` validates the workspace itself (config, cache, tool availability,
   cycles){{if .Full}} when failures look environmental rather than caused by
   your change{{end}}.

## When another magus process holds the project

A run whose project lock or machine budget another magus invocation holds exits 75
at once, naming the holder.

- Never write `sleep`/`ps`/`pgrep` polling loops. The guard denies `pgrep`,
  `pidof`, and `ps` for this reason.
- `magus status --watch=15s` reads that lock state continuously: holder PID,
  command, directory, and age. Re-run once the lock releases.
- A long-running holder is not, by itself, evidence of a hang.

```sh
magus status --watch=15s
```

If the lock has crossed its stale warning threshold, report the exact holder and
inspect its captured target output. Never signal a guessed PID or act on a fixed
elapsed delay. Process termination needs an explicit, verified owner policy.
{{if .Full}}
The same view lists shared services, each with its lifecycle state and dependent
count. Use it to tell an idle retained service from active shared work before
deciding how to proceed.
{{end}}

## Fetching current behavior

{{if .Full}}Flags and target sets differ per workspace and magus version. Trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything remembered, and over `MAGUS.md`, which is generated output that
lags the tree between regenerations.{{else}}Trust `magus describe targets`, `magus describe target <name>` and
`magus <verb> -h` over anything remembered, and over `MAGUS.md`.{{end}}
