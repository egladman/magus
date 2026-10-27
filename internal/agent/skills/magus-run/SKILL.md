# Running work through magus

{{if .Full}}magus is the task orchestrator, and its unit of work is the target. Targets declare
their inputs, outputs and sandbox. magus caches results and computes what a change
affects. A raw language tool bypasses all of that, so the cache goes stale, declared
outputs drift, and `{{cmd "affected"}}` can no longer vouch for your change.{{else}}magus: task orchestrator; unit of work: target.
Targets declare inputs, outputs, sandbox; magus caches results, computes what change affects.
Raw language tool bypasses all that: cache goes stale, `{{cmd "affected"}}` cannot vouch for change.{{end}}

{{if .Full}}## Which project a command hits{{else}}## Project scope{{end}}

{{if .Full}}magus is CWD-relative: a bare `{{cmd "run"}}`/`ls`/`describe` acts on the project holding
your current directory, or the whole workspace from the root. Do not assume the root.
Scope explicitly so a command means the same anywhere: name the project (`{{cmd "run"}}
test web`), or let `{{cmd "affected"}}` compute the set from the diff. `{{cmd "where"}} <name>`
resolves a name to its path; over MCP, `{{tool "client"}}` (`{{buzz "where"}}`, the project that contains a directory) ignores the CWD.{{else}}magus is CWD-relative -> never assume root. Scope explicitly:
- Name project: `{{cmd "run"}} test web`.
- Or `{{cmd "affected"}}` computes set from diff.{{end}}{{if .Full}}

`--root <path>`, or `-C` after make's idiom, sets where that walk STARTS. Its argument
is a plain path: any directory, holding one file or none, nested or not. It is not a
workspace and not a checkout; calling it either leads a reader to think it must be
one.{{end}}{{if .Full}}

What the walk FINDS is separate:{{end}}

{{if .Full}}1. The nearest `magus.yaml` wins.
2. Absent any, the outermost CONTIGUOUS run of `magusfiles/`, `magusfile.buzz` or
   `go.mod` wins.

It can resolve somewhere other than the path you passed, or resolve nothing.

Two consequences worth knowing before you debug one. Running a binary by
absolute path does NOT set its working directory, so `/elsewhere/magus run build .`
still walks up from YOUR cwd and can act on a tree you never named. And nearest-wins
means a directory holding its own `magus.yaml` inside a larger checkout resolves to
itself, so where a command lands is a question about markers on disk, never about the
VCS.{{else}}`{{cmd "where"}} <name>`: name -> path. MCP tools ignore CWD.
`--root <path>` (or `-C`): where walk STARTS: any directory, not workspace or checkout.
Where it lands is separate: nearest `magus.yaml` wins, else outermost contiguous run of `magusfiles/`, `magusfile.buzz` or `go.mod`.
Binary run by absolute path does NOT set working directory: walks up from cwd -> pass `--root` when you mean elsewhere.{{end}}

## How to run work

{{if .Full}}1. Prefer the MCP tools; they return structured content with nothing to silence.
   Call an exposed MCP tool directly. If it is missing or its call fails, use the
   CLI fallback below.
   - Do not gate that choice on `{{cmd "status"}} --probe=mcp`. It tests loopback HTTP,
     while a host may use stdio or the server's Unix socket.
   - `{{cmd "status"}} --probe=readiness` checks that this workspace is loaded on the
     server socket. It does not test the host's MCP registration.
   - Hosts manage their own connection; never start a server for an agent.
     Do not make the connection a prerequisite for completing the work.
   - `{{tool "client"}}` (`{{buzz "run"}}`): run named projects, with the same arguments as `{{cmd "run"}}` (or the cwd
     project). Use when you know which projects to run.
   - `{{tool "client"}}` (`{{buzz "affected"}}`): the projects a VCS change touched. It
     returns the set and does not run it. The gate is `{{cmd "affected"}} ci`.{{else}}1. Prefer MCP tools; call exposed tool directly. If missing or call fails -> CLI fallback below.
   - Do not gate that choice on `{{cmd "status"}} --probe=mcp`: tests loopback HTTP; host may use stdio or server Unix socket.
   - `{{cmd "status"}} --probe=readiness`: checks workspace loaded on server socket; does not test host MCP registration.
   - Hosts manage own connection; never start server for agent.
   - `{{tool "client"}}` (`{{buzz "run"}}`): run named projects, same arguments as `{{cmd "run"}}`.
   - `{{tool "client"}}` (`{{buzz "affected"}}`): projects VCS change touched; returns set, does not run it.{{end}}

{{if .Full}}   `{{tool "client"}}` is bounded at 10 minutes when called directly. A host that
   supports MCP tasks can run it as a task without that bound. Send a long run (a
   full `ci`, a gate) to the CLI unless your host runs `{{tool "client"}}` as a task.{{else}}   `{{tool "client"}}` bounded at 10 minutes when called directly; host supporting MCP tasks can run it as task without bound.
   Long run (full `ci`, gate) -> CLI unless host runs `{{tool "client"}}` as task.{{end}}

{{if .Full}}   If the MCP tool errors or is absent, run the CLI equivalent: `{{cmd "run"}}
   <target>`, `{{cmd "affected"}} list` for the set, `{{cmd "affected"}} <target>` to run
   it. Do not stop, and do not drop to a raw language tool. When you shell out,
   silence it (`-s`) so a
   passing run costs a few lines, not a scroll of progress.{{else}}   If MCP tool errors or absent -> CLI equivalent:
   - `{{cmd "run"}} <target>`: run target.
   - `{{cmd "affected"}} list`: set; `{{cmd "affected"}} <target>`: run it.
   - Silence with `-s`. Do not stop; do not drop to raw language tool.{{end}}
{{if .Full}}2. Verification is `ci`'s job, not a sequence you compose. `ci` is the one target
   name magus enforces: the command that composes the pipeline
   (typically generate, lint, build, test) in the order the magusfile declares.
   - Run `{{cmd "run"}} ci <project>` for the project you are working in.
   - Run `{{cmd "affected"}} ci` as the final gate once the change is done; it runs the
     full pipeline over every project your change reaches, which is how you learn
     about ramifications in projects you never touched.
   - Never hand-run lint, format, and test one at a time. That re-derives an order
     the magusfile owns, and the step you forget fails silently by omission.
   - A gate you ADDED this session is not proven by its green. Make it FAIL once
     (break the input it checks, watch it go red, restore) before counting its
     pass. A check wired to the wrong path passes the same way.
   - Verify in place; never `git stash`/`reset` first (data-loss-prone and pointless: the tree is
     already what you want to verify).{{else}}2. Verification is `ci`'s job, not sequence you compose; `ci` is only target name magus enforces.
   - `{{cmd "run"}} ci <project>` for project you work in.
   - `{{cmd "affected"}} ci`: final gate once change done.
   - Never hand-run lint, format, test one at a time: re-derives order magusfile owns; forgotten step fails silently by omission.
   - Gate you ADDED this session not proven by its green. Make it FAIL once (break input it checks, watch red, restore) before counting its pass; check wired to wrong path passes same way.
   - Verify in place; never `git stash`/`reset` first: destroys concurrent agent's untracked work, and tree already is what you verify.{{end}}
{{if .Full}}3. Reach for an individual target only to iterate on a failure `ci` named.
   Narrow it as "When a target fails" below says; `ci` afterwards proves the
   change. `{{cmd "describe targets"}}` lists every target (`-o name` for
   bare names) and classifies each as canonical, spell,
   or custom; `{{tool "client"}}` (`{{buzz "describe.target"}}`) is the MCP equivalent. Ask the
   workspace; do not read `MAGUS.md`: that file is a generated index
   for humans, true only as of its last regeneration.{{else}}3. Individual target only to iterate on failure `ci` named: narrow it as "When a target fails" says; `ci` afterwards proves change. `{{cmd "describe targets"}}` lists every target (`-o name` for bare names). Ask workspace; do not read `MAGUS.md`.{{end}}
{{if .Full}}4. Do not run raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...) for
   work a target covers. If no target covers it, say so; never silently go around
   magus.{{else}}4. Never raw language tools (`go test`, `eslint`, `pytest`, `tsc`, ...) for work target covers. If no target covers it, say so; never silently go around magus.{{end}}
{{if .Full}}5. Rewriting DEPENDENCY state needs the `update` charm: `{{cmd "run"}}
   <target>:update <project>`, so the rewrite happens inside magus, cached and
   visible to affected tracking. That covers `go get`, `go mod tidy`, `pnpm add`,
   `cargo update`, `uv lock` and `pip-compile`.
   - `update` is reserved and not part of `rw`. `rw` covers output reproducible
     from a clean checkout; `update` covers what a registry serves today. `ci`
     strips both, so a gate verifies the committed lockfile rather than refreshing it.
   - Applying a lockfile (`npm ci`, `pnpm install --frozen-lockfile`) re-resolves
     nothing and needs no charm.{{else}}5. Rewriting DEPENDENCY state needs `update` charm: `{{cmd "run"}} <target>:update <project>`; covers `go get`, `go mod tidy`, `pnpm add`, `cargo update`, `uv lock`, `pip-compile`.
   - `update` reserved, not part of `rw`. `rw`: output reproducible from clean checkout; `update`: what registry serves today.
   - Applying lockfile (`npm ci`, `pnpm install --frozen-lockfile`) re-resolves nothing, needs no charm.{{end}}

## Command patterns

```sh
magus run ci web                  # verify one project: the composed pipeline, in order
magus affected ci                 # the final gate: everything the diff reaches
magus run test web                # iterate on the one failing target ci named
magus run test web -- -k 'name'   # rerun only the failing tests (pytest filter shown)
magus affected test               # only projects affected by the VCS diff
```
{{if .Full}}
MCP equivalents: `{{tool "client"}}` (`{{buzz "run"}}`) with the arguments of `{{cmd "run"}}`.
`{{buzz "affected"}}` returns the affected project set and does not run it. `{{buzz "impact"}}` is why each project is in that set. `{{buzz "where"}}` answers which project contains a directory.
{{end}}
{{if .Full}}WRONG: `go test ./...` after editing Go in a magus workspace; also wrong is
hand-sequencing `{{cmd "run"}} lint`, `format`, `test` to check your own work.
CORRECT: `{{cmd "run"}} ci <project>` while working, `{{cmd "affected"}} ci` once the
change is done, and a single narrower target only to iterate on a failure.{{else}}Wrong: `go test ./...` after editing Go; hand-sequencing `{{cmd "run"}} lint`, `format`, `test` to check own work.
Right: `{{cmd "run"}} ci <project>` while working; `{{cmd "affected"}} ci` once done.{{end}}{{if .Full}}

To prove a command on Linux without opening a pull request, magus's own repository
runs it on a GitHub Actions runner (`{{cmd "buzz"}} hack/remote/on-actions.buzz --
<command>`) or in a local Podman container (`{{cmd "buzz"}} hack/remote/on-linux.buzz --
<command>`).{{end}}

{{if .Full}}## Output control: silence runs, read structure{{else}}## Output control{{end}}
{{if .Full}}
You are a machine reader; no news is good news. Shape the output instead of
truncating it after the fact:
{{end}}
{{if .Full}}- `-s` / `--silent`: the default for every CLI run. Progress is dropped; a pass
  prints one line, `ref  <output ref>`, and a failure keeps a bounded tail of
  the failing project plus the ref to fetch the rest. With `--tee <file>` a
  structured record goes to the file alone.
  DROP it when the question is what RAN versus what replayed. The per-target
  timings and the `(cached, 320ms)` / `(ran, 5m28s)` verdict print only without
  it. Reaching for shell `time` around a silent run measures the wall clock magus
  already reported and hides which targets were cache hits.{{else}}- `-s` / `--silent`: default for every CLI run. Pass: one `ref` line; failure adds bounded tail. DROP it when question is what RAN versus replayed: per-target timings and `(cached, 320ms)` / `(ran, 5m28s)` verdict print only without it.{{end}}
{{if .Full}}- `-q` / `--quiet`: looser; drops progress, keeps errors and the failing project's
  full output.{{else}}- `-q` / `--quiet`: looser; drops progress, keeps errors and failing project's full output.{{end}}
{{if .Full}}- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`. Ask for the
  shape you want. `json`/`yaml` to parse, `jsonl` to stream records, `name` for
  bare identifiers one per line, `template=` to project exactly the fields you
  need and nothing else.{{else}}- `-o <fmt>`: `text|json|yaml|jsonl|name|template=<go-template>`.{{end}}

### Read worker streams as records

JSONL is a record stream, not a progress message. Keep the complete capture with
the host or `-o jsonl --tee <file>`; parse its contract before selecting fields.
Project the terminal outcome, validation refs, unresolved risks and requests for
input before returning data to the orchestrator. Fetch tool transcripts only to
investigate a specific failure. Handled tool errors and denials stay in the
capture. A filter must preserve terminal failures, unresolved blockers, unchecked work,
missing or malformed terminal records, and the producer's exit status; an empty
projection is not success. Never select failures by searching prose for words
such as `deny`, or truncate a stream with head or tail.

For magus records, use `-o template` or `-o json` with a JSON projection. Full
records stay available for audit; replaying every claim, permitted deviation and
routine guard event spends context without changing the next action.

{{if .Full}}### Never pipe or redirect a magus command{{else}}### Never pipe or redirect magus{{end}}

{{if .Full}}**Do NOT pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`, or
`wc`. Do NOT redirect it with `> file`, `>> file`, or `2>&1`.** The guard denies
both.
{{else}}Never pipe magus output through `grep`, `head`, `tail`, `awk`, `sed`, `cut`, `wc`. Never redirect with `> file`, `>> file`, `2>&1`. Guard denies both.{{end}}
{{if .Full}}- A pipe REPLACES the exit status with the last stage's. `{{cmd "affected"}} ci | tail`
  reports tail's success, so a failing gate reads as exit 0.{{else}}- Pipe REPLACES exit status with last stage's: `{{cmd "affected"}} ci | tail` reports tail's success -> failing gate reads as exit 0.{{end}}
{{if .Full}}- You never need to capture the output. Every run persists its full log, and a
  failure prints that path with the output ref.{{else}}- Never need to capture output: every run persists full log; failure prints path with output ref.{{end}}{{if .Full}}

Every magus command already has an output contract, so filtering its
text after the fact is always the wrong tool. It is not a style preference; it
actively breaks things:{{end}}{{if .Full}}

- It is lossy in the direction that matters. A truncating filter drops the
  failing tail and the output ref, which is the only part you needed.
- It hides progress on a long run, so a working command looks hung and gets
  killed.
- It scrapes a human-facing layout that is free to change, instead of reading a
  stable contract that is not.
- `grep` cannot see structure. A field you matched textually may belong to a
  different record entirely.{{end}}{{if .Full}}

Replace the filter with the flag that already does it:{{end}}

{{if .Full}}| Instead of | Use |
|---|---|
| `\| grep <field>` | `-o template='{{"{{.Field}}"}}'` |
| `\| grep -c .` (counting) | `-o json` and read the count, or the verb's own summary |
| `\| head` / `\| tail` (quieting) | `-s` / `--silent` |
| `\| grep -i error` | `-s` (a pass prints almost nothing; failures already surface) |
| `\| awk '{print $1}'` | `-o name` |
| `\| jq` after `-o text` | `-o json` first, then `jq` |{{else}}Flag instead:
- `-o template='{{"{{.Field}}"}}'`: field.
- `-o name`: bare identifiers; `-o json`: parse.
- `-s`: quieten.{{end}}

{{if .Full}}`jq` on `-o json` is fine: that is consuming a contract, not scraping text. The
prohibition is on text filters standing in for an output format.{{else}}`jq` over `-o json` fine: contract, not scraped text.{{end}}

{{if .Full}}**Piping magus INTO magus is supported and encouraged.** The composition seam is
`--stdin`. `--tee <file>` is not a composition seam: it mirrors STRUCTURED output
only (`-o json|yaml|jsonl|template`), never console text. These are contracts on both
ends, so they are the opposite of the antipattern above:{{else}}Piping magus INTO magus supported, encouraged; composition seam: `--stdin`.
`--tee <file>` not composition seam: mirrors STRUCTURED output only (`-o json|yaml|jsonl|template`), never console text.{{end}}

```sh
magus watch | magus affected --stdin        # changed paths -> affected set
magus affected ci --plan | magus run --stdin      # plan -> run its shards
```

{{if .Full}}A pipe into magus, or `jq` over `-o json`, is composition. A pipe into a text filter
is a missing `-o`.

A backgrounded run's capture is magus output too: grepping the task file your host
wrote hits the `capture-filter` guard rule, since a grep drops the `output:` and `inspect:` lines
under the `cause:` you matched. Background it as `-o jsonl --tee <file>` to get a
contract you can `jq`; otherwise read the file whole.{{else}}Pipe into magus, or `jq` over `-o json`: composition. Pipe into text filter: missing `-o`.
Backgrounded run's capture is magus output too: grepping host's task file hits `capture-filter` guard rule. Background as `-o jsonl --tee <file>` -> contract you can `jq`; otherwise read file whole.{{end}}{{if .Full}}

WRONG: `{{cmd "run"}} test | head -50` (drops the failing tail that matters).
WRONG: `{{cmd "query"}} "kind=target" -o name | grep -c .` (use the JSON count).
CORRECT: `{{cmd "run"}} test -s`, then fetch the printed ref for full detail.{{end}}{{if .Full}}

The silent run plus ref-fetch IS the low-token failure loop: never re-run a
target just to see its error again.
{{end}}

## When you need finer granularity

{{if .Full}}Every top-level target composes spell ops (tool-native operations). When you
genuinely need one op (a single formatter, one linter), address one directly
with the spell-qualified form:{{else}}Every top-level target composes spell ops (tool-native operations); address one op with spell-qualified form:{{end}}

```sh
magus run go::go-test             # one op from the go spell
magus run buf::buf-lint
```

{{if .Full}}List the ops behind a target with `{{cmd "describe target"}} <name>`: it prints the
fully-evaluated dispatch plan per project (sources, outputs, spells, policy).
Re-run the top-level target before you call the work done: ci runs the full
composition, so the full composition is what has to pass.{{else}}Ops behind target: `{{cmd "describe target"}} <name>`.
Re-run top-level target before calling work done.{{end}}

{{if .Full}}## When a target fails{{else}}## When target fails{{end}}

{{if .Full}}Each target's result line mints an output reference id (`out1a2b3c`).
{{else}}Each target's result line mints output reference id (`out1a2b3c`).{{end}}
{{if .Full}}1. Fetch the exact captured output: `{{tool "client"}}` (`{{buzz "output"}}`) over MCP, or
   `{{cmd "query output"}} out1a2b3c` on the CLI. Do this instead of re-running the
   target to see the error again.{{else}}1. Fetch exact captured output: `{{tool "client"}}` (`{{buzz "output"}}`) over MCP, or `{{cmd "query output"}} out1a2b3c` on CLI. Never re-run to see error again.{{end}}
{{if .Full}}2. With no ref in hand, find it in the run that minted it. Every `{{cmd "run"}}` prints
   one per target. `{{cmd "session"}}` lists recent invocations with the targets
   they ran. There is no tool that fetches "the latest log for a
   project", because a second door onto the same bytes only makes an agent holding a
   ref pick between two.{{else}}2. No ref -> find it in minting run: every `{{cmd "run"}}` prints one per target; `{{cmd "session"}}` lists recent invocations with targets they ran.{{end}}
{{if .Full}}3. `{{cmd "doctor"}}` validates the workspace itself (config, cache, tool availability,
   cycles) when failures look environmental rather than caused by
   your change.{{else}}3. `{{cmd "doctor"}}` validates workspace itself (config, cache, tool availability, cycles).{{end}}

Fix a failed full run by narrowing, not by repeating it:

1. Read the failure from its ref and list the failing tests.
2. Rerun only those with the test runner's own filter, passed after `--`, until they
   pass: `{{cmd "run"}} test <project> -- <filter>`. Examples by runner:
   - Go (go test): `-- -run 'TestA|TestB'`
   - pytest: `-- -k 'name'`
   - Jest or Vitest: `-- -t 'name'`
   - cargo test: `-- name`
3. Run the full target ONCE more. A full run after a failed full run, with nothing
   narrowed between them, reports nothing new{{if .Full}} and costs the whole suite again{{end}}.

A test known to fail for an outside reason is named once and left out of every rerun.
Run generators before the full test run, so a drift test does not fail for nothing.

{{if .Full}}## When another magus process holds the project{{else}}## When another magus process holds project{{end}}

{{if .Full}}A run whose project lock or machine budget another magus invocation holds exits 75
at once, naming the holder.
{{else}}Run whose project lock or machine budget another magus invocation holds: exits 75 at once, naming holder.{{end}}
{{if .Full}}- Never write `sleep`/`ps`/`pgrep` polling loops. The guard denies `pgrep`,
  `pidof`, and `ps` for this reason.{{else}}- Never write `sleep`/`ps`/`pgrep` polling loops; guard denies `pgrep`, `pidof`, `ps`.{{end}}
{{if .Full}}- `{{cmd "status"}} --watch=15s` reads that lock state continuously: holder PID,
  command, directory, and age. Re-run once the lock releases.{{else}}- `{{cmd "status"}} --watch=15s`: reads lock state continuously: holder PID, command, directory, age. Re-run once lock releases.{{end}}
{{if .Full}}- A long-running holder is not, by itself, evidence of a hang.{{else}}- Long-running holder alone: not evidence of hang.{{end}}

```sh
magus status --watch=15s
```

{{if .Full}}If the lock has crossed its stale warning threshold, report the exact holder and
inspect its captured target output. Never signal a guessed PID or act on a fixed
elapsed delay. Process termination needs an explicit, verified owner policy.{{else}}If lock crossed stale warning threshold -> report exact holder, inspect its captured target output.
Never signal guessed PID or act on fixed elapsed delay; process termination needs explicit, verified owner policy.{{end}}{{if .Full}}

The same view lists shared services, each with its lifecycle state and dependent
count. Use it to tell an idle retained service from active shared work before
deciding how to proceed.
{{end}}

{{if .Full}}## Fetching current behavior{{else}}## Current behavior{{end}}

{{if .Full}}Flags and target sets differ per workspace and magus version. Trust
`{{cmd "describe targets"}}`, `{{cmd "describe target"}} <name>`, and `magus <verb> -h`
over anything remembered, and over `MAGUS.md`, which is generated output that
lags the tree between regenerations.{{else}}Trust `{{cmd "describe targets"}}`, `{{cmd "describe target"}} <name>`, `magus <verb> -h` over memory and `MAGUS.md`.{{end}}
