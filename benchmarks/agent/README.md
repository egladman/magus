# Agent harness-effectiveness benchmark

Same model, same tasks, two provisioning recipes. The question is not "is magus
good" but two falsifiable claims:

1. **Context economy**: the agent with the magus surface spends fewer tokens
   and dollars to reach the same outcome.
2. **Outcome quality**: it succeeds more often and violates fewer repo
   invariants.

If the numbers say the surface costs more for the same outcomes, that is a
finding. Design and citations: `plans/harness-effectiveness-benchmark-2026-09-02.md`
in the memory store.

## Arms

| Arm | What the worktree gets |
| --- | --- |
| `rampant` | magus binary and a magusfile, no agent surface: no skills, no hooks, no MCP, no MAGUS.md, a minimal CLAUDE.md |
| `full` | everything `magus agent install` ships, plus hook wiring, MCP and the real routing index |

The arms differ only in provisioning. Model, effort, prompt, permission mode,
budget caps, worktree layout and fixture SHA are identical.

`_selftest` is not an arm. It provisions nothing and exists so the runner can be
exercised without the real recipes; `tasks/_placeholder` is its counterpart.
Neither is part of the corpus.

## Headline metric

**Cost-of-pass**: expected dollars per correct solution. Efficiency is
conditioned on success, so a cheap failure is not efficient. The companion chart
is correctness against median completion tokens, read as a cost-accuracy
frontier.

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

| File | Contents |
| --- | --- |
| `meta.json` | run_id, arm, task, rep, model, effort, max_turns, budget_usd, magus_binary, magus_version, fixture_sha, started, ended, exit_reason |
| `transcript.jsonl` | the raw stream-json, unmodified |
| `final.diff` | `git diff` of the worktree after the run, untracked files included |
| `check.txt`, `check.exit` | acceptance-check output and exit code (0 = pass) |
| `timing.json` | wall_ms, time_to_first_edit_ms, time_to_done_ms |
| `activity/` | copy of the worktree's `.magus/activity/`, when the arm produced one |
| `probe.txt` | arm-verification output, pass or fail |
| `prompt.md`, `agent.log`, `setup.log` | the prompt as given, agent stderr, seed/provision output |

`exit_reason` is one of `ok`, `timeout`, `agent_error`, `seed_failed`,
`provision_failed`, `probe_failed`, `control_error`, or a control verdict
(`control_golden_ok`, `control_golden_failed`, `control_null_ok`,
`control_null_failed`).

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
announced on stderr, and makes `run.sh` exit non-zero.

## Budget caps

Per-task, from `tasks/<id>/meta.json`: `budget_usd` becomes the session's
`--max-budget-usd`, `max_turns` is the turn cap. A hard cap is what keeps the
arms comparable; without one the weaker arm just spends more. `BENCH_TIMEOUT_S`
(default 1800) is the wall-clock cap and the backstop for a session that hangs.

Caveat, and it is a real gap: **`claude` 2.1.212 exposes no `--max-turns`.**
`agent.sh` passes the flag if a future CLI grows it and otherwise prints a
notice; `max_turns` is still recorded in `meta.json` so a transcript says which
caps were actually live. Until then the dollar cap and the wall clock are the
only enforced bounds.

## Swapping the agent

Every agent session goes through
`runner/agent.sh <worktree> <prompt-file> <transcript-out> <max-turns> <model> <effort>`.
`RUNNER_AGENT=<path>` replaces it. `runner/fake-agent.sh` is the no-cost
stand-in used to test the runner: it writes a schema-valid transcript with all
four token counters, and with `FAKE_AGENT_SOLVE=1` applies the task's oracle
solution.

The session is launched with `env -i` and a small whitelist, cwd set to the
worktree, `--setting-sources project` so only the worktree's own settings load,
and `--strict-mcp-config` with an empty config unless the arm supplies
`arms/<arm>/mcp.json`. MCP registration is user-scoped, so without that flag the
operator's own servers would leak their tool schemas into both arms' context
windows. An arm needing environment levers (`MAGUS_HINTS_ENABLED=false`, say)
drops a `KEY=VALUE` file at `arms/<arm>/env`.

## Analysis image

Python runs only in Docker. The image pins the interpreter and installs nothing;
analysis code and results are bind-mounted, and the analysis stays stdlib-only.

```sh
docker build -t magus-bench-analysis benchmarks/agent
docker run --rm \
    -v "$PWD/benchmarks/agent/analysis:/opt/analysis:ro" \
    -v "$PWD/benchmarks/agent/results:/results:ro" \
    magus-bench-analysis /opt/analysis/<script>.py /results
```

## Not built yet

- The metrics extractor and the statistics under `analysis/` (paired bootstrap
  deltas, Holm correction, Wilson intervals, pass@1 and pass^k).
- The judge harness for interrogation and catch-up tasks, including the blinding
  pass that strips magus command names, skill mentions and guard text before
  grading.
- Containerized agent sessions. Runs execute on the host today, so host state is
  a shared confound rather than an isolated one.
- The task corpus and the fixture enrichment (generated outputs with a real
  drift gate, cross-project dependencies, a `ci` composition).
- The arm recipes themselves.
- Warm-versus-cold start is still undecided; whichever is chosen has to be held
  identical across arms, and the runner does nothing to warm or cool anything.
