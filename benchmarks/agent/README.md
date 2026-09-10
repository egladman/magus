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

| Arm       | What the worktree gets                                                                                |
| --------- | ----------------------------------------------------------------------------------------------------- |
| `rampant` | magus binary and a magusfile, no agent surface: no skills, no hooks, no MAGUS.md, a minimal CLAUDE.md |
| `full`    | everything `magus agent install` ships, plus hook wiring and the real routing index                   |

The arms differ only in provisioning. Model, effort, prompt, permission mode,
budget caps, worktree layout and fixture SHA are identical. Neither arm
registers an MCP server; `arms/README.md` says why it is held at zero in both
rather than switched.

## Prerequisites

- `bash`, `git`, and `node` (every task check is stdlib node)
- `jq`, which the arm probes use to read `settings.json` and doctor's output
- the `claude` CLI, logged in (see below)
- `docker`, for every SWE-bench trial (below)

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
run; the extractor skips the first two.

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
caps were actually live. Until then the dollar cap and the wall clock are the
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
(`MAGUS_HINTS_ENABLED=false`, `GUARD_MAGUS_BIN`): `provision.sh` writes it per
run through `arm_write_env` in `arms/lib.sh`, and `agent.sh` sources it before
the scrub. cwd is the worktree and `--setting-sources project` limits settings
to the worktree's own `.claude/settings.json`; `HOME` is kept, so whatever the
host reads from `~/.claude` (memory, user-scoped skills) reaches both arms
equally. `env.sh` also exports `RUNNER_MCP_CONFIG`, the empty server set the
session gets under `--strict-mcp-config`. MCP registration is user-scoped, so
without that flag the operator's own servers would leak their tool schemas
into both arms' context windows.

## SWE-bench Verified as a task source

`--task swebench:<instance_id>` runs one instance of SWE-bench Verified (500
real GitHub issues over twelve Python repos) instead of a fixture task. The
arms, model, effort, caps and results schema are unchanged; what changes is
where the trial runs, because the repo's Python environment exists only inside
the instance's published Docker image.

What is consumed, and from where:

- The table itself, via the Hugging Face rows API (no auth). `swebench/fetch.sh`
  downloads the five 100-row pages into `swebench/verified.jsonl` (ignored, about
  9 MB, `hints_text` dropped, sorted by `instance_id`) and prints its sha256.
  `swebench/verified.sha256` pins the digest a previous fetch observed; a fetch
  that disagrees exits non-zero, so an upstream edit to the table is noticed
  rather than graded against.
- `swebench/pilot.jsonl`, committed: 24 whole rows chosen by rule, not by hand.
  `swebench/pilot.sh` takes the first two instances of every repo by
  `instance_id` (flask has only one), then the next instances by `instance_id`
  from django and sympy until there are 24. A pilot needs nothing from the full
  table.
- The per-instance Docker images, from Epoch AI's registry
  (`ghcr.io/epoch-research/swe-bench.eval.<arch>.<instance_id>`), which carries
  every Verified instance for x86_64 and a best-effort arm64 set. The runner
  picks the image for the host's own architecture and never runs emulated: wall
  clock is a headline metric, and an emulated container is several times slower.
  The dataset's own `image` field names the upstream Docker Hub build, which is
  amd64 only; `BENCH_EMULATE=1` is the one way to use it on another host. An
  arm64 image the registry calls untested is validated like every instance, by
  its golden and null controls.
- The log parsers from `swebench/harness/log_parsers/python.py` (MIT), ported to
  Go in `cmd/swegrade` with the attribution in the source: the twelve
  parser names the Verified set uses, which are four distinct parsers
  (`pytest`, `pytest_options`, `pytest_v2`, `django`, `sympy`, `matplotlib`,
  `seaborn` and their aliases). Nothing from the `swebench` Python package is
  installed anywhere.

How a container trial works, per rep:

1. `swebench/images.sh` builds the trial image once per (instance, magus
   binary): the instance image plus node LTS, `@anthropic-ai/claude-code`, `jq`,
   and the given magus binary copied to `/usr/local/bin/magus`
   (`swebench/bench.Dockerfile`). The tag carries a digest of the binary, so a
   different build under test gets a different image.
2. A trial container starts from it with the run directory mounted at `/work`
   and this checkout mounted read-only at `/src`. `swebench/seed.sh` writes a
   `magus.yaml` and a magusfile from `swebench/magusfile.buzz.tmpl` (one python
   project at `/testbed`, a `test` target that forks pytest) and lists every
   provisioning path in `.git/info/exclude`, so the agent starts from a clean
   `git status`. The unchanged `arms/<arm>/provision.sh` and `probe.sh` then run
   inside the container against `/testbed`; both arms provision and probe
   there exactly as on a host worktree.
3. `runner/agent.sh` runs inside the container with the same flags it uses on
   the host, `problem_statement` as the prompt (`/work/prompt.md`, outside the
   repo), and the transcript streaming to `/work/transcript.jsonl`. The
   credential reaches the container by name through `docker exec -e`; it is
   never on a command line, in a file, or in an image layer. `BENCH_TIMEOUT_S`
   is enforced by coreutils `timeout` inside the container.
4. `final.diff` is `git diff` of `/testbed` (untracked files included, the
   provisioning paths excluded), and the trial container is removed.
5. Grading runs in a **fresh** container from the instance image itself:
   `swebench/grade.sh` applies `final.diff` with the reference harness's apply
   ladder, then runs the row's `eval_script`, which resets the test files,
   applies `test_patch` and runs the named tests. The log lands in `eval.log`
   and is piped to `swegrade` (built as the `magus-bench/swegrade` image from
   `swebench/grader.Dockerfile`, so the host needs no Go toolchain), which
   writes the JSON verdict to `check.txt` and its exit status to `check.exit`.

The golden control applies the row's gold `patch` in the trial container in
place of an agent; the null control applies nothing. `meta.json` carries
`"task_source": "swebench"` and `"instance_id"`, `fixture_sha` is the row's
`base_commit`, and `task` is the `swebench:<id>` string, so the analysis reads
these runs unchanged. Run ids use `swebench-<id>` in place of the task name.
The run directory also keeps `row.json`, `gold.patch`, `test.patch`, `eval.sh`
and `eval.log`. Budget and turn caps come from `swebench/meta.json`.

`swegrade` grades the way `swebench/harness/grading.py` does: a FAIL_TO_PASS
test resolves on PASSED or XFAIL, a PASS_TO_PASS test is maintained on PASSED,
XFAIL or SKIPPED, a test the log never names counts as failed, a log carrying a
harness failure marker (patch apply, reset, timeout) resolves nothing, and the
truncated parametrized ids Verified carries resolve by prefix when every
candidate agrees. Exit 0 is resolved, 1 is not, 2 is unusable input.

The magus binary for this mode must be the linux build for the host's
architecture (`arm64` on an Apple Silicon Mac, `amd64` elsewhere). From the
workspace root:

```sh
magus run release-build . -- linux arm64
mkdir -p dist/linux-arm64
tar -xzf dist/magus_*_linux_arm64_static.tar.gz -C dist/linux-arm64 magus
```

`run.sh` refuses anything else at that path and prints those commands for the
architecture it needs. Then, from `benchmarks/agent`:

```sh
./swebench/fetch.sh                                  # optional: the full table
./run.sh --arm rampant --task swebench:pallets__flask-5014 --reps 1 \
    --model claude-sonnet-5 --effort high --magus-binary ../../dist/linux-arm64/magus \
    --control golden
./run.sh --arm rampant --task swebench:pallets__flask-5014 --reps 1 \
    --model claude-sonnet-5 --effort high --magus-binary ../../dist/linux-arm64/magus \
    --control null
./run.sh --manifest swebench/pilot.manifest          # 24 instances: controls, then 3 reps per arm
```

`RUNNER_AGENT` is not honored in this mode; the controls are the no-cost path.

## Analysis

The analysis is a Go package under `analysis/`, standard library only, with
one binary, `benchreport`; `internal/bench/README.md` has the invocations, and
`magus run agent-bench-report .` builds it and runs the whole pipeline over
`results/`.

## Not built yet

- Containerized agent sessions for the fixture tasks. Those runs execute on the
  host today, so host state is a shared confound rather than an isolated one;
  only the SWE-bench source runs in a container.
- MCP as a switch. Both arms hold it at zero (`arms/README.md`); measuring it
  needs an estimate of the tool-schema floor in the system prompt, which
  transcripts never show and the extractor does not model.
- An enforced turn cap: `claude` exposes no `--max-turns`, so `max_turns` is
  recorded and not enforced (see Budget caps).
- An LLM judge and its blinding pass. Nothing in the corpus needs one today:
  every task grades deterministically, against an `ANSWER.md` set or a held-out
  test. A task that cannot be graded that way has no home yet.
- Warm-versus-cold start is still undecided; whichever is chosen has to be held
  identical across arms, and the runner does nothing to warm or cool anything.
