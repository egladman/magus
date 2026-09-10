# Benchmark analysis

Three stdlib-only Python scripts that turn a results tree into a report:
`extract.py` (runs to metrics), `analyze.py` (metrics to statistics),
`report.py` (statistics to markdown). They only read artifacts, so a scored run
is analyzed as many times as you like without re-running an agent.

Python runs in Docker only. Nothing here imports anything outside the standard
library, so `python:3.12-slim` runs it as-is with no build:

```sh
docker pull python:3.12-slim
```

## Invocations

Every command bind-mounts `benchmarks/agent` at `/w`.

```sh
docker run --rm -v "$PWD/benchmarks/agent:/w" python:3.12-slim \
  python /w/analysis/extract.py /w/results -o /w/metrics.jsonl

docker run --rm -v "$PWD/benchmarks/agent:/w" python:3.12-slim \
  python /w/analysis/analyze.py /w/metrics.jsonl -o /w/analysis.json --seed 20260902

docker run --rm -v "$PWD/benchmarks/agent:/w" python:3.12-slim \
  python /w/analysis/report.py /w/analysis.json -o /w/report.md
```

Tests, and a synthetic results tree to run the three scripts against:

```sh
docker run --rm -v "$PWD/benchmarks/agent:/w" -w /w/analysis python:3.12-slim \
  python -m unittest discover -v

docker run --rm -v "$PWD/benchmarks/agent:/w" python:3.12-slim \
  python /w/analysis/makefixture.py /tmp/fx
```

The magus agent guard denies a raw `docker run` that does work, so inside this
repo the same steps are targets: `magus run agent-bench-test .` runs the unit
tests, and `magus run agent-bench-report .` runs extract, analyze and report
over `results/` (`-- --results <dir>` for another tree, `-- --image <img>` for
another image). Both need a magus binary that loads this workspace.

`extract.py` takes `--pricing` if you need a table other than the one beside it.
`analyze.py` takes `--seed`; the bootstrap is seeded per metric and task, so the
same metrics.jsonl and seed produce byte-identical analysis.json.

## Inputs

One directory per run under `results/`, named `<arm>-<task>-r<rep>-<stamp>`:

| File                     | Read for                                                                                                                                     |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `meta.json`              | run_id, arm, task, rep, model, effort, max_turns, budget_usd, magus_binary, magus_version, fixture_sha, started, ended, exit_reason, control |
| `transcript.jsonl`       | tokens, dollars, turns, tool calls, file reads, tool-result bytes                                                                            |
| `final.diff`             | invariant violations                                                                                                                         |
| `check.exit`             | success                                                                                                                                      |
| `timing.json`            | wall_ms, time_to_first_edit_ms, time_to_done_ms                                                                                              |
| `activity/events.jsonl`  | guard events (optional)                                                                                                                      |
| `check.txt`, `probe.txt` | kept for the human, not parsed                                                                                                               |

## Metric definitions

- **tokens** - summed over assistant records' `message.usage`, deduplicated by
  message id: `input`, `output`, `cache_read` (`cache_read_input_tokens`),
  `cache_write` (`cache_creation_input_tokens`, or the 5m/1h breakdown under
  `cache_creation` when the transcript reports one). `total_billed` is the sum
  of all four; it is a token count, not a price.
- **dollars** - per-model token totals priced from `pricing.json` (USD per
  million tokens, read from the published pricing page on the date recorded in
  that file). A model missing from the table stops extraction rather than being
  guessed at. When a transcript reports only the flat cache-write counter, those
  tokens are priced at the 5-minute rate and the record carries
  `cache_write_ttl_assumed: true`, which the report surfaces as a caveat: the
  dollars are a floor. `reported_cost_usd` is whatever the transcript's own
  `result` record claimed, kept only as a cross-check.
- **turns** - assistant records, deduplicated by message id.
- **tool_calls** - `tool_use` blocks, total and by tool name.
- **file_reads / re_read_rate** - Read and NotebookRead calls;
  `re_read_rate = (reads - distinct paths) / reads`, so 0.5 means half the reads
  were of a path already read.
- **tool_result_bytes** - characters of `tool_result` content entering context.
- **guard_events** - from the activity trail: an `agent_command` event whose
  preview contains `guard: deny` is a denial and one containing `guard: advis`
  an advisory; a `skill.*` action, or a `file.read` under a `/skills/` path, is a
  skill load. **Null when no trail was captured, never zero** - zero means the
  guard was present and silent.
- **success** - `check.exit == 0`. A run with no `check.exit` is null (unknown),
  not a failure; the report lists it under its caveats as an unknown outcome,
  excluded from every pass rate.
- **control runs** - a run whose `meta.json` carries `control` (`golden` or
  `null`) never launched an agent and has no transcript. The extractor skips it
  with a note and it contributes no row; its verdict is the `exit_reason` the
  runner recorded. A scored run without a transcript is still an error.
- **invariant_violations** - `tests_deleted` lists paths the diff deletes whose
  basename contains `_test.` or `.test.`. Deletion is the only violation read
  from a diff deterministically; everything else belongs in an acceptance check.

## Statistics

- **Paired deltas** - per task, per metric, `full` rep i minus `rampant` rep i.
  A rep present in only one arm contributes to no delta and is reported as an
  unpaired cell. Never a comparison of independent means.
- **CI** - percentile interval from a 10,000-sample bootstrap of the mean paired
  delta, seeded from `(seed, metric, task)` so each interval is independent of
  what else is in the run set.
- **p** - two-sided bootstrap p, Holm-adjusted across tasks within a metric.
- **Verdict** - `lower under full` / `higher under full` only when the CI
  excludes zero AND `|delta| >= 10%` of the rampant median. Everything else is
  `inconclusive`; a statistically clean 2 percent is not a finding.
- **pass@1** - successes / reps in a cell, with a Wilson score 95% interval.
  **pass^k** - 1 only when all k reps in the cell passed.
- **cost-of-pass** - mean dollars / pass rate, per arm. Reported as infinite when
  the pass rate is zero; never silently dropped.
- **Efficiency conditioned on success** - each cell also carries
  `metrics_success_only`; a cheap failure is not efficient.
- Every spread is reported as median plus IQR alongside the mean.
