#!/usr/bin/env python3
"""Turn metrics.jsonl into analysis.json: paired deltas, pass rates, cost-of-pass.

Arms are compared per task on PAIRED reps (full rep i against rampant rep i),
never as independent means. Every interval is a seeded 10k bootstrap or a Wilson
score interval, so the same input and seed give byte-identical output.
"""

import argparse
import json
import math
import random
import statistics
import sys

BOOTSTRAP_ITERS = 10000
Z_95 = 1.959963984540054

# Relative size a paired delta must clear before it earns a verdict, on top of
# the CI excluding zero. A significant 2 percent difference is not a finding.
MIN_RELATIVE_DELTA = 0.10

TREATMENT_ARM = "full"
BASELINE_ARM = "rampant"

METRICS = (
    ("total_billed_tokens", ("tokens", "total_billed")),
    ("input_tokens", ("tokens", "input")),
    ("output_tokens", ("tokens", "output")),
    ("cache_read_tokens", ("tokens", "cache_read")),
    ("cache_write_tokens", ("tokens", "cache_write")),
    ("dollars", ("dollars",)),
    ("wall_ms", ("wall_ms",)),
    ("time_to_first_edit_ms", ("time_to_first_edit_ms",)),
    ("time_to_done_ms", ("time_to_done_ms",)),
    ("turns", ("turns",)),
    ("tool_calls", ("tool_calls",)),
    ("file_reads", ("file_reads",)),
    ("re_read_rate", ("re_read_rate",)),
    ("tool_result_bytes", ("tool_result_bytes",)),
)


def metric_value(record, path):
    """Read a metric out of a record by its key path; missing reads as None."""
    value = record
    for key in path:
        if not isinstance(value, dict) or key not in value:
            return None
        value = value[key]
    return value if isinstance(value, (int, float)) and not isinstance(value, bool) else None


def wilson_interval(successes, total, z=Z_95):
    """Wilson score interval for a binomial proportion. (None, None) when total is 0."""
    if total <= 0:
        return (None, None)
    p = successes / total
    denom = 1.0 + z * z / total
    center = (p + z * z / (2.0 * total)) / denom
    half = z * math.sqrt(p * (1.0 - p) / total + z * z / (4.0 * total * total)) / denom
    return (max(0.0, center - half), min(1.0, center + half))


def percentile(sorted_values, q):
    """Nearest-rank percentile over an already sorted list."""
    if not sorted_values:
        return None
    index = int(math.floor(q * (len(sorted_values) - 1)))
    return sorted_values[index]


def bootstrap_paired(deltas, seed_key, iters=BOOTSTRAP_ITERS):
    """Bootstrap the mean of paired deltas; returns the CI and a two-sided p value.

    The RNG is seeded from the metric and task name so each interval is
    independent of how many other cells are analyzed alongside it.
    """
    n = len(deltas)
    if n == 0:
        return {"ci_low": None, "ci_high": None, "p": None}
    rng = random.Random(seed_key)
    means = []
    for _ in range(iters):
        total = 0.0
        for _ in range(n):
            total += deltas[rng.randrange(n)]
        means.append(total / n)
    means.sort()
    at_or_below = sum(1 for m in means if m <= 0.0)
    at_or_above = sum(1 for m in means if m >= 0.0)
    p = min(1.0, 2.0 * min(at_or_below, at_or_above) / iters)
    return {
        "ci_low": percentile(means, 0.025),
        "ci_high": percentile(means, 0.975),
        "p": max(p, 1.0 / iters),
    }


def holm(pvalues):
    """Holm-Bonferroni adjust a {key: p} mapping across the family of tasks."""
    items = sorted((p, key) for key, p in pvalues.items() if p is not None)
    m = len(items)
    adjusted = {}
    running = 0.0
    for i, (p, key) in enumerate(items):
        value = min(1.0, (m - i) * p)
        running = max(running, value)
        adjusted[key] = running
    for key, p in pvalues.items():
        if p is None:
            adjusted[key] = None
    return adjusted


def describe(values):
    """Median, IQR and mean together; a mean alone hides the spread that matters."""
    clean = sorted(v for v in values if v is not None)
    if not clean:
        return {"n": 0, "median": None, "iqr": [None, None], "mean": None}
    if len(clean) == 1:
        q1 = q3 = clean[0]
    else:
        q1, _, q3 = statistics.quantiles(clean, n=4, method="inclusive")
    return {
        "n": len(clean),
        "median": statistics.median(clean),
        "iqr": [q1, q3],
        "mean": statistics.fmean(clean),
    }


def load_records(path):
    records = []
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if line:
                records.append(json.loads(line))
    if not records:
        raise SystemExit("analyze: %s is empty" % path)
    return records


def cell_stats(records):
    """Pass rates and metric spreads for one (arm, task) cell."""
    n = len(records)
    successes = sum(1 for r in records if r.get("success") is True)
    unknown = sum(1 for r in records if r.get("success") is None)
    pass_at_1 = successes / n if n else None
    low, high = wilson_interval(successes, n)
    successful = [r for r in records if r.get("success") is True]
    return {
        "n": n,
        "successes": successes,
        "unknown_outcomes": unknown,
        "pass_at_1": pass_at_1,
        "pass_at_1_ci": [low, high],
        "k": n,
        "pass_pow_k": 1.0 if n and successes == n else 0.0,
        "metrics": {
            name: describe([metric_value(r, path) for r in records]) for name, path in METRICS
        },
        "metrics_success_only": {
            name: describe([metric_value(r, path) for r in successful]) for name, path in METRICS
        },
    }


def arm_summary(records):
    """Arm-level pass rate and cost-of-pass: expected dollars per correct solution."""
    n = len(records)
    successes = sum(1 for r in records if r.get("success") is True)
    dollars = [metric_value(r, ("dollars",)) for r in records]
    spread = describe(dollars)
    pass_rate = successes / n if n else 0.0
    low, high = wilson_interval(successes, n)
    if pass_rate > 0 and spread["mean"] is not None:
        cost_of_pass = {"value": spread["mean"] / pass_rate, "infinite": False}
    else:
        cost_of_pass = {"value": None, "infinite": True}
    return {
        "n": n,
        "successes": successes,
        "pass_rate": pass_rate,
        "pass_rate_ci": [low, high],
        "dollars": spread,
        "cost_of_pass_usd": cost_of_pass,
    }


def paired_deltas(by_arm_task_rep, tasks, seed):
    """Per metric, per task: bootstrap the full-minus-rampant delta over matched reps."""
    out = {}
    for name, path in METRICS:
        per_task = {}
        pvalues = {}
        for task in tasks:
            deltas = []
            baseline = []
            reps = sorted(
                set(by_arm_task_rep.get((TREATMENT_ARM, task), {}))
                & set(by_arm_task_rep.get((BASELINE_ARM, task), {}))
            )
            for rep in reps:
                treated = metric_value(by_arm_task_rep[(TREATMENT_ARM, task)][rep], path)
                base = metric_value(by_arm_task_rep[(BASELINE_ARM, task)][rep], path)
                if treated is None or base is None:
                    continue
                deltas.append(treated - base)
                baseline.append(base)
            boot = bootstrap_paired(deltas, "%d|%s|%s" % (seed, name, task))
            base_median = statistics.median(baseline) if baseline else None
            delta_mean = statistics.fmean(deltas) if deltas else None
            relative = None
            if delta_mean is not None and base_median:
                relative = delta_mean / base_median
            excludes_zero = (
                boot["ci_low"] is not None
                and boot["ci_high"] is not None
                and (boot["ci_low"] > 0 or boot["ci_high"] < 0)
            )
            if excludes_zero and relative is not None and abs(relative) >= MIN_RELATIVE_DELTA:
                verdict = "lower under full" if delta_mean < 0 else "higher under full"
            else:
                verdict = "inconclusive"
            per_task[task] = {
                "n_pairs": len(deltas),
                "delta_mean": delta_mean,
                "delta_median": statistics.median(deltas) if deltas else None,
                "ci_low": boot["ci_low"],
                "ci_high": boot["ci_high"],
                "p": boot["p"],
                "baseline_median": base_median,
                "relative": relative,
                "ci_excludes_zero": excludes_zero,
                "verdict": verdict,
            }
            pvalues[task] = boot["p"]
        for task, adjusted in holm(pvalues).items():
            per_task[task]["p_holm"] = adjusted
        out[name] = per_task
    return out


def data_quality(records, by_arm_task_rep, tasks, arms):
    """Facts the report's caveats are generated from, not prose about them."""
    incomplete = []
    for task in tasks:
        reps = set()
        for arm in arms:
            reps |= set(by_arm_task_rep.get((arm, task), {}))
        for rep in sorted(reps):
            missing = [arm for arm in arms if rep not in by_arm_task_rep.get((arm, task), {})]
            if missing:
                incomplete.append({"task": task, "rep": rep, "missing_arms": missing})
    return {
        "runs_without_guard_events": sorted(
            r["run_id"] for r in records if r.get("guard_events") is None
        ),
        "runs_without_check": sorted(
            r["run_id"] for r in records if r.get("success") is None
        ),
        "runs_with_assumed_cache_ttl": sorted(
            r["run_id"] for r in records if r.get("cache_write_ttl_assumed")
        ),
        "runs_with_invariant_violations": sorted(
            r["run_id"]
            for r in records
            if (r.get("invariant_violations") or {}).get("tests_deleted")
        ),
        "incomplete_pairs": incomplete,
    }


def analyze(records, seed):
    arms = sorted({r["arm"] for r in records})
    tasks = sorted({r["task"] for r in records})
    by_arm_task = {}
    by_arm_task_rep = {}
    for record in records:
        key = (record["arm"], record["task"])
        by_arm_task.setdefault(key, []).append(record)
        by_arm_task_rep.setdefault(key, {})[record["rep"]] = record

    models = sorted({r.get("model") for r in records if r.get("model")})
    return {
        "seed": seed,
        "bootstrap_iters": BOOTSTRAP_ITERS,
        "min_relative_delta": MIN_RELATIVE_DELTA,
        "runs": len(records),
        "arms": arms,
        "tasks": tasks,
        "models": models,
        "cells": {
            "%s/%s" % (arm, task): cell_stats(by_arm_task[(arm, task)])
            for (arm, task) in sorted(by_arm_task)
        },
        "arm_summary": {
            arm: arm_summary([r for r in records if r["arm"] == arm]) for arm in arms
        },
        "paired": paired_deltas(by_arm_task_rep, tasks, seed),
        "data_quality": data_quality(records, by_arm_task_rep, tasks, arms),
    }


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("metrics", help="metrics.jsonl from extract.py")
    parser.add_argument("-o", "--out", required=True, help="analysis.json to write")
    parser.add_argument("--seed", type=int, default=20260902, help="bootstrap seed")
    args = parser.parse_args(argv)

    analysis = analyze(load_records(args.metrics), args.seed)
    with open(args.out, "w", encoding="utf-8") as fh:
        json.dump(analysis, fh, sort_keys=True, indent=2)
        fh.write("\n")
    print(
        "analyzed %d runs, %d tasks, %d arms -> %s"
        % (analysis["runs"], len(analysis["tasks"]), len(analysis["arms"]), args.out)
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
