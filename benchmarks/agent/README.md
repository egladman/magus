# Agent harness-effectiveness benchmark

Same model, same tasks, two provisioning setups. The question is not "is magus
good" but two falsifiable claims:

1. The agent with the magus integration spends fewer tokens and dollars to reach the
   same outcome (context economy).
2. The same agent succeeds more often and violates fewer repo invariants
   (outcome quality).

If the numbers say the integration costs more for the same outcomes, that is a
finding. The design notes lived in the memory store and went with it; the published
runs are under `docs/benchmarks/`.

## Arms

| Arm       | What the worktree gets                                                                                    |
| --------- | --------------------------------------------------------------------------------------------------------- |
| `rampant` | magus binary and a magusfile, no agent integration: no skills, no hooks, no MAGUS.md, a minimal CLAUDE.md |
| `full`    | everything `magus agent install` ships, plus hook wiring and the real routing index                       |

`skills-full`, `skills-short` and `skills-c1` are `full` with one skill form
each, for measuring how far the skill bodies compress; `arms/README.md` has the
switch, and `compress.manifest` the grid:

```sh
BENCH_C1_DIR=/abs/path/to/c1-skills BENCH_ALLOWED_TOOLS=Bash \
    BENCH_RESULTS_DIR=results-compress ./run.sh --manifest compress.manifest
```

The arms differ only in provisioning. Model, effort, prompt, permission mode,
budget caps, worktree layout and fixture SHA are identical. Neither arm
registers an MCP server; `arms/README.md` says why it is held at zero in both
rather than switched.

## Prerequisites

- `bash`, `git`, and `node` (every task check is stdlib node)
- `jq`, which the arm probes use to read `settings.json` and doctor's output
- the `claude` CLI, logged in (see below)
- a Docker Engine API socket, for a SWE-bench trial (below)

`_selftest` is not an arm. It provisions nothing and exists so the runner can be
exercised without the real setups; `tasks/_placeholder` is its counterpart.
Neither is part of the task set.

## Headline metric

**Cost-of-pass**: expected dollars per correct solution. Efficiency is
conditioned on success, so a cheap failure is not efficient. The companion table
puts each cell's pass rate, with its passes of its graded runs, beside the median
tokens and dollars it spent. The null control is the false-pass check: a pass
rate is evidence only where the null control fails.

## Running a pilot

One task, two arms, three reps:

```sh
./benchmarks/large-monorepo/setup.sh                 # materializes gen/repo
cd benchmarks/agent
./run.sh --arm rampant --task <task> --reps 3 --model <model> --effort <effort> \
    --magus-binary "$PWD/../../magus"
./run.sh --arm full --task <task> --reps 3 --model <model> --effort <effort> \
    --magus-binary "$PWD/../../magus"
```

Same grid from a manifest, one run.sh flag line per run:

```sh
./run.sh --manifest pilot.manifest
```

The agent sessions need a login. Interactive CLIs share the keychain session
`claude login` refreshes; a headless run wants the long-lived token
`claude setup-token` mints, and the runner takes it two ways. Exported as
`CLAUDE_CODE_OAUTH_TOKEN` in the launching shell, it rides the environment
scrub by name. Or, on macOS, store it once in the keychain and let the workspace's
secret provider hand it to the runner, so nothing is ever exported into a shell
and a transcript never sees it:

```sh
security add-generic-password -a "$USER" -s CLAUDE_BENCH_TOKEN -w   # prompts, no echo
MAGUS_SECRET_PROVIDER=system-keychain magus run agent-bench-run . -- pilot.manifest
```

`agent-bench-run` resolves `CLAUDE_BENCH_TOKEN` through `magus\secret.read`, hands
it to `run.sh` as `CLAUDE_CODE_OAUTH_TOKEN` in that one child's environment (never
argv, which the run log records), and magus redacts the value from everything it
captures.

Always run the controls for a task before trusting any number from it:

```sh
./run.sh --arm rampant --task <task> --reps 1 --model <m> --effort <e> \
    --magus-binary <path> --control golden
./run.sh --arm rampant --task <task> --reps 1 --model <m> --effort <e> \
    --magus-binary <path> --control null
```

`--dry-run` prints the plan and touches nothing.

## What a run does

Fresh `git worktree` off the fixture repo at its pinned SHA, then `seed.sh`,
`provision.sh`, `probe.sh`, the agent under a wall-clock timeout, `git diff`,
`check.sh`, worktree removed. A failed probe aborts the run before the agent
starts and records `probe_failed`. A run in an unverified arm is worse than no
run.

The fixture is `benchmarks/large-monorepo/gen/repo`, never the magus repo
itself. Override with `BENCH_FIXTURE_REPO` only for runner self-tests.

## Results schema

`results/<run-id>/`, where run-id is `<arm>-<task>-r<rep>-<utc-stamp>`:

| File                                  | Contents                                                                                                                                     |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `meta.json`                           | run_id, arm, task, rep, model, effort, max_turns, budget_usd, magus_binary, magus_version, fixture_sha, started, ended, exit_reason, control |
| `transcript.jsonl`                    | the raw stream-json, unmodified; absent for a control run, which launches no agent                                                           |
| `final.diff`                          | `git diff` of the worktree after the run, untracked files included                                                                           |
| `check.txt`, `check.exit`             | acceptance-check output and exit code (0 = pass)                                                                                             |
| `timing.json`                         | wall_ms, time_to_first_edit_ms, time_to_done_ms                                                                                              |
| `activity/`                           | copy of the worktree's `.magus/activity/`, when the arm produced one                                                                         |
| `probe.txt`                           | arm-verification output, pass or fail                                                                                                        |
| `prompt.md`, `agent.log`, `setup.log` | the prompt as given, agent stderr, seed/provision output                                                                                     |

`exit_reason` is one of `ok`, `timeout`, `agent_error`, `seed_failed`,
`provision_failed`, `probe_failed`, `control_error`, or a control verdict
(`control_golden_ok`, `control_golden_failed`, `control_null_ok`,
`control_null_failed`). `control` is `golden`, `null`, or empty for a scored
run; the extractor records the first two as control rows the report's Controls
table reads, and never scores them.

Timings are runner-side and tool-agnostic: `time_to_first_edit_ms` is the first
poll at which the worktree differs from how seeding and provisioning left it,
not something parsed out of the transcript, so it means the same thing for any
agent plugged into the runner. It is `null` when the agent changed nothing.
`final.diff` is against the fixture SHA, so a task that seeds a dirty worktree
has its seed delta in there alongside the agent's work.

`results/` is gitignored except its `.gitkeep`.

## Controls

The two failure modes that silently invalidate a benchmark are a checker that
nothing can pass and a checker that everything passes.

- **Golden control** (`--control golden`) runs the task's `solution.sh` instead
  of an agent. The check must pass. If it does not, the task is unsolvable as
  written and every failure recorded against it is meaningless.
- **Null control** (`--control null`) runs no agent at all. The check must fail.
  If it passes, the task is already solved at its seed state and every success
  recorded against it is vacuous. That exact defect is what killed the previous
  eval harness: its trajectory constraints saw empty metadata, so half passed
  vacuously and half failed vacuously, and nobody noticed because no control ran.

A control that behaves unexpectedly is recorded as a `control_*_failed` run,
announced on stderr, and makes `run.sh` exit non-zero. In a manifest that
failure does not stop the lines after it: every line runs, the failed line
numbers are listed at the end, and the manifest exits 1.

## Budget caps

Per-task, from `tasks/<id>/meta.json`: `budget_usd` becomes the session's
`--max-budget-usd`, `max_turns` is the turn cap. A hard cap is what keeps the
arms comparable; without one the weaker arm just spends more. `BENCH_TIMEOUT_S`
(default 1800) is the wall-clock cap and the backstop for a session that hangs.

Caveat, and it is a real gap: **`claude` 2.1.212 exposes no `--max-turns`.**
`agent.sh` passes the flag if a future CLI grows it and otherwise prints a
notice; `max_turns` is still recorded in `meta.json` so a transcript says which
caps were live. Until then the dollar cap and the wall clock are the
only enforced bounds.

## Swapping the agent

Every agent session goes through
`runner/agent.sh <worktree> <prompt-file> <transcript-out> <max-turns> <model> <effort>`.
`RUNNER_AGENT=<path>` replaces it. `runner/fake-agent.sh` is the no-cost
stand-in used to test the runner: it writes a schema-valid transcript with all
four token counters, and with `FAKE_AGENT_SOLVE=1` applies the task's oracle
solution.

The session is launched with `env -i` and a whitelist: `HOME`, `PATH`, the
locale, the credential names, plus every name the worktree's
`.benchmark/env.sh` exports. That file is how an arm sets its levers
(`MAGUS_HINTS_ENABLED=false`, `__MAGUS_BIN`): `provision.sh` writes it per
run through `arm_write_env` in `arms/lib.sh`, and `agent.sh` sources it before
the scrub. cwd is the worktree and `--setting-sources project` limits settings
to the worktree's own `.claude/settings.json`. `HOME` is kept, because the CLI
finds its login through it, but nothing of the operator's `~/.claude` reaches a
run: `--setting-sources project` also drops the user `CLAUDE.md`, user skills,
user agents and user hooks, and `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` drops the
auto-memory section. A transcript's `init` event lists the skills and agents the
session loaded and carries `memory_paths: null`. `BENCH_CLAUDE_BIN` picks the
CLI build (default `claude` on `PATH`). Headless under `acceptEdits`, the CLI
denies every command that is not read-only, and with the guard wired that is
every command, since the guard rewrites each one to close its stdin;
`BENCH_ALLOWED_TOOLS=Bash` pre-approves them, and the guard's own denies still
apply. `env.sh` also exports `RUNNER_MCP_CONFIG`, the empty server set the
session gets under `--strict-mcp-config`. MCP registration is user-scoped, so
without that flag the operator's own servers would leak their tool schemas
into both arms' context windows.

## Go task suites

The benchmark's tasks beyond the fixture tasks are Go, task data included: SWE-bench
Verified is Python, and the benchmark carries no Python. `cmd/benchtasks` writes each
suite as one `bench.Task` per line under `tasks/`:

- `benchtasks swebench -o tasks/swebench-multilingual-go.jsonl` fetches SWE-bench
  Multilingual through the Hugging Face rows API (no auth) and keeps the 42 rows of its
  five Go repositories (caddy, gin, prometheus, hugo, terraform). It refuses the table
  when its digest leaves the pin compiled into `internal/benchtasks`, and the dataset
  card when it stops declaring MIT.
- `benchtasks history --repo . --ref main --after <cutoff> -o tasks/history-go.jsonl`
  mines this repository's first-parent commits after a model's training cutoff for
  bugfix, interrogation and regeneration tasks. A bugfix is kept only when its new test
  fails at the parent and passes at the fix, which takes two `go test` runs per
  candidate in a scratch clone. It writes what it found and fails naming any kind it
  found too few of.

The container steps a SWE-bench trial needs are Go in `internal/container`: it pins a
task image by digest, refuses an emulated platform unless allowed, creates the container
with CPU and memory limits over the Docker Engine API (Docker Desktop, podman and colima
all serve it), copies magus in, seeds one Go project per module, applies a patch with
the reference harness's apply ladder and runs the row's eval script. `cmd/swegrade`
grades the eval log, reading `go test` output and `go test -json` events. `run.sh` does
not drive these steps: it refuses a `swebench:<instance_id>` task until a Go runner
replaces it.

## Analysis

The analysis is a Go package under `internal/bench/` with one binary,
`cmd/benchreport`; `internal/bench/README.md` has the invocations, and
`magus run agent-bench-report .` builds it and runs the whole pipeline over
`results/`.

## Publishing a run

`results/` is gitignored, so a run that is worth keeping is published as its
report: `magus run agent-bench-report . -- --publish <provider>` writes the
rendered report to `reports/<date>/<provider>/<model>.md`, the date being
today's and the model the one the runs used, with the run's environment stamped
after the title (publish time, scored runs, model and provider, the magus build
the trials recorded, the host). That directory is committed, and the docs
project mirrors every file in it onto the site under Benchmarks at the same
path, so a number a reader meets there traces back to a date, a build and a
machine. Pass `--results <dir>` for a tree other than `results/`, as the Opus
pilot did; a tree that mixes models is refused.

## Not built yet

- Containerized agent sessions for the fixture tasks. Those runs execute on the
  host today, so host state is a shared confound rather than an isolated one.
- A Go runner for the SWE-bench tasks: `internal/container` has the steps, and
  nothing drives them yet.
- MCP as a switch. Both arms hold it at zero (`arms/README.md`); measuring it
  needs an estimate of the tool-schema floor in the system prompt, which
  transcripts never show and the extractor does not model.
- An enforced turn cap: `claude` exposes no `--max-turns`, so `max_turns` is
  recorded and not enforced (see Budget caps).
- An LLM judge and its blinding pass. Nothing in the task set needs one today:
  every task grades deterministically, against an `ANSWER.md` set or a held-out
  test. A task that cannot be graded that way has no home yet.
- Warm-versus-cold start is still undecided; whichever is chosen has to be held
  identical across arms, and the runner does nothing to warm or cool anything.
