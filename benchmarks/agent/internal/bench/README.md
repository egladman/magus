# Benchmark analysis

A Go package, `internal/bench`, and one binary, `cmd/benchreport`, that turn a
results tree into a report in three stages: `extract` (runs to metrics),
`analyze` (metrics to statistics), `report` (statistics to markdown). They only
read artifacts, so a scored run is analyzed as many times as you like without
re-running an agent. `records.go` holds the record types the stages hand each
other; their json tags are the file formats. No dependency beyond the standard
library and the workspace's own JSON codec.

The pipeline was ported from Python and its output is pinned byte for byte to
what the Python wrote: `internal/pycompat/` carries the pieces of CPython the numbers
depend on (random.Random's Mersenne Twister and seeding, math.fsum, float
repr, json.dumps with sort_keys), and `testdata/fixture-*` is the Python's
output over the synthetic tree, which the tests reproduce exactly.

## Invocations

Every command takes one positional argument and `-o` for the file it writes.
`extract` reads `pricing.json` from `benchmarks/agent/` relative to
the working directory unless `-p` names another table.

```sh
magus run go::go-build . -- -o /tmp/benchreport ./benchmarks/agent/cmd/benchreport

/tmp/benchreport extract benchmarks/agent/results -o benchmarks/agent/metrics.jsonl
/tmp/benchreport analyze benchmarks/agent/metrics.jsonl -o benchmarks/agent/analysis.json --seed 20260902
/tmp/benchreport report benchmarks/agent/analysis.json -o benchmarks/agent/report.md
```

`makefixture <dir> -model <alias>` writes a synthetic results tree under `<dir>/results`, two
arms by two tasks by three reps with fixed constants, which is what the tests
assert exact totals against.

Inside this repo the whole pipeline is one target: `magus run
agent-bench-report .` builds the binary and runs extract, analyze and report
over `results/` (`-- --results <dir>` for another tree, `-- --synthetic` to
generate the fixture and report on that instead). The tests are ordinary Go
tests in the root module, so `magus run test .` covers them; to run only these,
`magus run go::go-test . -- -run TestBench`. Both need a magus binary that
loads this workspace.

`analyze` takes `--seed`; the bootstrap is seeded per metric and task, so the
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
- **dollars** - the host's own bill, `total_cost_usd` from the transcript's
  `result` record (kept as `reported_cost_usd`), when it recorded a positive one.
  `table_dollars_usd` is the per-model token totals priced from `pricing.json`
  (USD per million tokens, read from the published pricing page on the date
  recorded in that file); it is the fallback for a transcript that ends without
  a result record, and the report says how far it sits from the bill. A model
  missing from the table still stops extraction rather than being guessed at.
  When a transcript reports only the flat cache-write counter, those tokens are
  priced at the 5-minute rate and the record carries
  `cache_write_ttl_assumed: true`, which the report surfaces as a caveat: the
  table dollars are a floor.
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
  `null`) never launched an agent and has no transcript. The extractor writes a
  record with only its identity, kind and check verdict; the report's Controls
  section reads those to say whether each task's check discriminates (golden
  passes, null fails), and a pass rate is flagged as not evidence until it does.
  Controls never enter a cell or a pairing. A scored run without a transcript is
  still an error.
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
