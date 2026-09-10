#!/usr/bin/env python3
"""Turn metrics.jsonl into analysis.json: paired deltas, pass rates, cost-of-pass.

Arms are compared per task on PAIRED reps (full rep i against rampant rep i),
never as independent means. Every interval is a seeded 10k bootstrap or a Wilson
score interval, so the same input and seed give byte-identical output.
"""

from __future__ import annotations

import argparse
import json
import math
import random
import statistics
import sys
from collections import defaultdict
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, replace
from pathlib import Path
from typing import NamedTuple

from records import (
    Analysis,
    Arm,
    ArmSummary,
    CellStats,
    ControlCell,
    ControlCount,
    ControlKind,
    ControlRun,
    CostOfPass,
    DataQuality,
    PairedDelta,
    RunRecord,
    ScoredRun,
    Spread,
    UnpairedRep,
    Verdict,
    run_from_json,
)

BOOTSTRAP_ITERS = 10000
Z_95 = 1.959963984540054

# Relative size a paired delta must clear before it earns a verdict, on top of
# the CI excluding zero. A significant 2 percent difference is not a finding.
MIN_RELATIVE_DELTA = 0.10

TREATMENT_ARM = Arm.FULL
BASELINE_ARM = Arm.RAMPANT


class AnalyzeError(Exception):
    """The metrics file cannot be analyzed; say which file and why."""


@dataclass(frozen=True)
class Metric:
    name: str
    read: Callable[[ScoredRun], float | None]


METRICS = (
    Metric("total_billed_tokens", lambda r: r.tokens.total_billed),
    Metric("input_tokens", lambda r: r.tokens.input),
    Metric("output_tokens", lambda r: r.tokens.output),
    Metric("cache_read_tokens", lambda r: r.tokens.cache_read),
    Metric("cache_write_tokens", lambda r: r.tokens.cache_write),
    Metric("dollars", lambda r: r.dollars),
    Metric("wall_ms", lambda r: r.wall_ms),
    Metric("time_to_first_edit_ms", lambda r: r.time_to_first_edit_ms),
    Metric("time_to_done_ms", lambda r: r.time_to_done_ms),
    Metric("turns", lambda r: r.turns),
    Metric("tool_calls", lambda r: r.tool_calls),
    Metric("file_reads", lambda r: r.file_reads),
    Metric("re_read_rate", lambda r: r.re_read_rate),
    Metric("tool_result_bytes", lambda r: r.tool_result_bytes),
)


class Interval(NamedTuple):
    low: float | None
    high: float | None


def wilson_interval(successes: int, total: int, z: float = Z_95) -> Interval:
    """Wilson score interval for a binomial proportion. (None, None) when total is 0."""
    if total <= 0:
        return Interval(None, None)
    p = successes / total
    denom = 1.0 + z * z / total
    center = (p + z * z / (2.0 * total)) / denom
    half = z * math.sqrt(p * (1.0 - p) / total + z * z / (4.0 * total * total)) / denom
    return Interval(max(0.0, center - half), min(1.0, center + half))


def percentile(sorted_values: Sequence[float], q: float) -> float | None:
    """Nearest-rank percentile over an already sorted list."""
    if not sorted_values:
        return None
    return sorted_values[math.floor(q * (len(sorted_values) - 1))]


class Bootstrap(NamedTuple):
    ci_low: float | None
    ci_high: float | None
    p: float | None


def bootstrap_paired(
    deltas: Sequence[float], seed_key: str, iters: int = BOOTSTRAP_ITERS
) -> Bootstrap:
    """Bootstrap the mean of paired deltas; returns the CI and a two-sided p value.

    The RNG is seeded from the metric and task name so each interval is
    independent of how many other cells are analyzed alongside it.
    """
    n = len(deltas)
    if n == 0:
        return Bootstrap(None, None, None)
    rng = random.Random(seed_key)
    means = sorted(_resampled_mean(deltas, rng) for _ in range(iters))
    at_or_below = sum(1 for m in means if m <= 0.0)
    at_or_above = sum(1 for m in means if m >= 0.0)
    p = min(1.0, 2.0 * min(at_or_below, at_or_above) / iters)
    return Bootstrap(percentile(means, 0.025), percentile(means, 0.975), max(p, 1.0 / iters))


def _resampled_mean(deltas: Sequence[float], rng: random.Random) -> float:
    n = len(deltas)
    total = 0.0
    for _ in range(n):
        total += deltas[rng.randrange(n)]
    return total / n


def holm(pvalues: Mapping[str, float | None]) -> dict[str, float | None]:
    """Holm-Bonferroni adjust a {key: p} mapping across the family of tasks."""
    ranked = sorted((p, key) for key, p in pvalues.items() if p is not None)
    m = len(ranked)
    adjusted: dict[str, float | None] = {key: None for key in pvalues}
    running = 0.0
    for i, (p, key) in enumerate(ranked):
        running = max(running, min(1.0, (m - i) * p))
        adjusted[key] = running
    return adjusted


def describe(values: Iterable[float | None]) -> Spread:
    """Median, IQR and mean together; a mean alone hides the spread that matters."""
    clean = sorted(v for v in values if v is not None)
    if not clean:
        return Spread(n=0, median=None, iqr=(None, None), mean=None)
    if len(clean) == 1:
        q1 = q3 = clean[0]
    else:
        q1, _, q3 = statistics.quantiles(clean, n=4, method="inclusive")
    return Spread(
        n=len(clean), median=statistics.median(clean), iqr=(q1, q3), mean=statistics.fmean(clean)
    )


def load_records(path: Path) -> list[RunRecord]:
    records: list[RunRecord] = []
    with path.open(encoding="utf-8") as fh:
        for number, line in enumerate(fh, start=1):
            if not line.strip():
                continue
            try:
                records.append(run_from_json(json.loads(line)))
            except (json.JSONDecodeError, KeyError, TypeError, ValueError) as exc:
                raise AnalyzeError(f"{path} line {number}: not a metrics record: {exc}") from exc
    if not records:
        raise AnalyzeError(f"{path} is empty")
    return records


def _passes(runs: Iterable[ScoredRun | ControlRun]) -> int:
    return sum(1 for run in runs if run.success is True)


def cell_stats(runs: Sequence[ScoredRun]) -> CellStats:
    """Pass rates and metric spreads for one (arm, task) cell."""
    n = len(runs)
    successes = _passes(runs)
    successful = [run for run in runs if run.success is True]
    return CellStats(
        n=n,
        successes=successes,
        unknown_outcomes=sum(1 for run in runs if run.success is None),
        pass_at_1=successes / n if n else None,
        pass_at_1_ci=wilson_interval(successes, n),
        k=n,
        pass_pow_k=1.0 if n and successes == n else 0.0,
        metrics=_spreads(runs),
        metrics_success_only=_spreads(successful),
    )


def _spreads(runs: Sequence[ScoredRun]) -> dict[str, Spread]:
    return {metric.name: describe(map(metric.read, runs)) for metric in METRICS}


def arm_summary(runs: Sequence[ScoredRun]) -> ArmSummary:
    """Arm-level pass rate and cost-of-pass: expected dollars per correct solution."""
    n = len(runs)
    successes = _passes(runs)
    spread = describe(run.dollars for run in runs)
    pass_rate = successes / n if n else 0.0
    if pass_rate > 0 and spread.mean is not None:
        cost_of_pass = CostOfPass(value=spread.mean / pass_rate, infinite=False)
    else:
        cost_of_pass = CostOfPass(value=None, infinite=True)
    return ArmSummary(
        n=n,
        successes=successes,
        pass_rate=pass_rate,
        pass_rate_ci=wilson_interval(successes, n),
        dollars=spread,
        cost_of_pass_usd=cost_of_pass,
    )


def paired_delta(
    treated: Mapping[int, ScoredRun],
    baseline: Mapping[int, ScoredRun],
    metric: Metric,
    seed_key: str,
) -> PairedDelta:
    """Bootstrap treated-minus-baseline over the reps both arms ran, for one metric."""
    deltas: list[float] = []
    base_values: list[float] = []
    for rep in sorted(treated.keys() & baseline.keys()):
        after, before = metric.read(treated[rep]), metric.read(baseline[rep])
        if after is None or before is None:
            continue
        deltas.append(after - before)
        base_values.append(before)
    boot = bootstrap_paired(deltas, seed_key)
    base_median = statistics.median(base_values) if base_values else None
    delta_mean = statistics.fmean(deltas) if deltas else None
    relative = delta_mean / base_median if delta_mean is not None and base_median else None
    excludes_zero = (
        boot.ci_low is not None
        and boot.ci_high is not None
        and (boot.ci_low > 0 or boot.ci_high < 0)
    )
    if excludes_zero and relative is not None and abs(relative) >= MIN_RELATIVE_DELTA:
        verdict = Verdict.LOWER if delta_mean < 0 else Verdict.HIGHER
    else:
        verdict = Verdict.INCONCLUSIVE
    return PairedDelta(
        n_pairs=len(deltas),
        delta_mean=delta_mean,
        delta_median=statistics.median(deltas) if deltas else None,
        ci_low=boot.ci_low,
        ci_high=boot.ci_high,
        p=boot.p,
        baseline_median=base_median,
        relative=relative,
        ci_excludes_zero=excludes_zero,
        verdict=verdict,
    )


Cells = Mapping[tuple[str, str], Sequence[ScoredRun]]


def _by_rep(cells: Cells, arm: str, task: str) -> dict[int, ScoredRun]:
    return {run.rep: run for run in cells.get((arm, task), ())}


def paired_deltas(
    cells: Cells, tasks: Sequence[str], seed: int
) -> dict[str, dict[str, PairedDelta]]:
    """Per metric, per task: the full-minus-rampant delta, with p Holm-adjusted across tasks."""
    out: dict[str, dict[str, PairedDelta]] = {}
    for metric in METRICS:
        per_task = {
            task: paired_delta(
                _by_rep(cells, TREATMENT_ARM, task),
                _by_rep(cells, BASELINE_ARM, task),
                metric,
                f"{seed}|{metric.name}|{task}",
            )
            for task in tasks
        }
        adjusted = holm({task: delta.p for task, delta in per_task.items()})
        out[metric.name] = {
            task: replace(delta, p_holm=adjusted[task]) for task, delta in per_task.items()
        }
    return out


def data_quality(
    runs: Sequence[ScoredRun], cells: Cells, tasks: Sequence[str], arms: Sequence[str]
) -> DataQuality:
    """Facts the report's caveats are generated from, not prose about them."""
    incomplete: list[UnpairedRep] = []
    for task in tasks:
        reps_by_arm = {arm: set(_by_rep(cells, arm, task)) for arm in arms}
        for rep in sorted(set().union(*reps_by_arm.values())):
            missing = tuple(arm for arm in arms if rep not in reps_by_arm[arm])
            if missing:
                incomplete.append(UnpairedRep(task=task, rep=rep, missing_arms=missing))
    # How far the pricing table sits from what the host billed, over the runs that
    # carry both. A constant ratio far from 1 is a table error; the report says so
    # rather than letting a floor pass for a bill.
    ratios = [
        run.table_dollars_usd / run.reported_cost_usd
        for run in runs
        if run.reported_cost_usd and run.table_dollars_usd is not None
    ]
    return DataQuality(
        table_to_billed_ratio_median=statistics.median_high(ratios) if ratios else None,
        runs_without_billed_cost=_run_ids(run for run in runs if not run.reported_cost_usd),
        runs_without_guard_events=_run_ids(run for run in runs if run.guard_events is None),
        runs_without_check=_run_ids(run for run in runs if run.success is None),
        runs_with_assumed_cache_ttl=_run_ids(run for run in runs if run.cache_write_ttl_assumed),
        runs_with_invariant_violations=_run_ids(
            run for run in runs if run.invariant_violations.tests_deleted
        ),
        incomplete_pairs=tuple(incomplete),
    )


def _run_ids(runs: Iterable[ScoredRun | ControlRun]) -> tuple[str, ...]:
    return tuple(sorted(run.run_id for run in runs))


def control_summary(controls: Sequence[ControlRun], tasks: Sequence[str]) -> dict[str, ControlCell]:
    """Per task, whether the checks discriminate: golden must pass, null must fail.

    A pass rate means nothing until this holds, because a check that accepts an
    untouched tree would grade every arm at 100%. A task with no control of one
    kind is reported as unverified rather than assumed.
    """
    out: dict[str, ControlCell] = {}
    for task in tasks:
        golden = _control_count(controls, task, ControlKind.GOLDEN)
        null = _control_count(controls, task, ControlKind.NULL)
        golden_all_pass = golden.n > 0 and golden.passes == golden.n
        null_all_fail = null.n > 0 and null.passes == 0
        out[task] = ControlCell(
            golden=golden, null=null, discriminates=golden_all_pass and null_all_fail
        )
    return out


def _control_count(controls: Sequence[ControlRun], task: str, kind: ControlKind) -> ControlCount:
    runs = [run for run in controls if run.task == task and run.control == kind]
    return ControlCount(n=len(runs), passes=_passes(runs), run_ids=_run_ids(runs))


def analyze(all_records: Sequence[RunRecord], seed: int) -> Analysis:
    controls = [record for record in all_records if isinstance(record, ControlRun)]
    runs = [record for record in all_records if isinstance(record, ScoredRun)]
    if not runs:
        raise AnalyzeError(f"no scored runs, only {len(controls)} control(s)")
    arms = tuple(sorted({run.arm for run in runs}))
    tasks = tuple(sorted({run.task for run in runs}))
    cells: defaultdict[tuple[str, str], list[ScoredRun]] = defaultdict(list)
    for run in runs:
        cells[(run.arm, run.task)].append(run)
    return Analysis(
        seed=seed,
        bootstrap_iters=BOOTSTRAP_ITERS,
        min_relative_delta=MIN_RELATIVE_DELTA,
        runs=len(runs),
        arms=arms,
        tasks=tasks,
        models=tuple(sorted({run.model for run in runs if run.model})),
        controls=control_summary(controls, tasks),
        cells={f"{arm}/{task}": cell_stats(cells[(arm, task)]) for arm, task in sorted(cells)},
        arm_summary={arm: arm_summary([run for run in runs if run.arm == arm]) for arm in arms},
        paired=paired_deltas(cells, tasks, seed),
        data_quality=data_quality(runs, cells, tasks, arms),
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("metrics", type=Path, help="metrics.jsonl from extract.py")
    parser.add_argument("-o", "--out", type=Path, required=True, help="analysis.json to write")
    parser.add_argument("--seed", type=int, default=20260902, help="bootstrap seed")
    args = parser.parse_args(argv)

    analysis = analyze(load_records(args.metrics), args.seed)
    with args.out.open("w", encoding="utf-8") as fh:
        json.dump(analysis.to_json(), fh, sort_keys=True, indent=2)
        fh.write("\n")
    print(
        f"analyzed {analysis.runs} runs, {len(analysis.tasks)} tasks, "
        f"{len(analysis.arms)} arms -> {args.out}"
    )
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except AnalyzeError as err:
        print(f"analyze: {err}", file=sys.stderr)
        sys.exit(1)
