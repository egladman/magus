#!/usr/bin/env python3
"""Analyze paired full-versus-simple skill-eval results."""

import argparse
import json
import random
import sys
from collections import defaultdict


BOOTSTRAPS = 10_000
SEED = 20260731


def rows_from(document):
    if isinstance(document, list):
        return document
    return document.get("results", document.get("data", []))


def value(row, name):
    return (
        row.get(name)
        or row.get("metadata", {}).get(name)
        or row.get("vars", {}).get(name)
        or row.get("config", {}).get(name)
    )


def assertion_passed(assertion):
    return bool(assertion.get("passed", assertion.get("success", assertion.get("pass", False))))


def row_score(row):
    assertions = row.get("assertions", row.get("constraint_results", []))
    if not assertions:
        raise ValueError(f"result for task {value(row, 'task_id')!r} has no assertions")
    passed = sum(assertion_passed(assertion) for assertion in assertions)
    return passed / len(assertions), int(passed == len(assertions)), assertions


def percentile(values, fraction):
    values = sorted(values)
    index = round((len(values) - 1) * fraction)
    return values[index]


def bootstrap_interval(deltas):
    rng = random.Random(SEED)
    draws = []
    for _ in range(BOOTSTRAPS):
        sample = [rng.choice(deltas) for _ in deltas]
        draws.append(sum(sample) / len(sample))
    lower = percentile(draws, 0.025)
    upper = percentile(draws, 0.975)
    nonpositive = sum(draw <= 0 for draw in draws) / len(draws)
    nonnegative = sum(draw >= 0 for draw in draws) / len(draws)
    pvalue = min(1.0, 2 * min(nonpositive, nonnegative))
    return lower, upper, pvalue


def verdict(delta, lower, upper):
    if (lower > 0 or upper < 0) and abs(delta) >= 0.10:
        return "REAL"
    return "NOT DISTINGUISHABLE"


def holm(comparisons):
    ranked = sorted(enumerate(comparisons), key=lambda pair: pair[1]["pvalue"])
    rejected = [False] * len(comparisons)
    still_rejecting = True
    for rank, (index, comparison) in enumerate(ranked):
        threshold = 0.05 / (len(comparisons) - rank)
        if still_rejecting and comparison["pvalue"] <= threshold:
            rejected[index] = True
        else:
            still_rejecting = False
    for index, comparison in enumerate(comparisons):
        comparison["holm_reject"] = rejected[index]
        comparison["adjusted_verdict"] = (
            "REAL" if comparison["raw_verdict"] == "REAL" and rejected[index] else "NOT DISTINGUISHABLE"
        )


def analyze(rows):
    grouped = defaultdict(list)
    for row in rows:
        skill = value(row, "skill")
        permutation = value(row, "permutation")
        model = value(row, "model") or row.get("provider")
        if not permutation and isinstance(model, str) and "/" in model:
            permutation, model = model.split("/", 1)
        task_id = value(row, "task_id") or value(row, "id")
        trial = value(row, "trial") or 0
        if not all((skill, permutation, model, task_id)):
            raise ValueError("each result needs skill, permutation, model, and task_id metadata")
        instruction, prompt, assertions = row_score(row)
        grouped[(skill, permutation, model, task_id, str(trial))].append((instruction, prompt, assertions))

    trial_scores = {}
    guardrails = defaultdict(lambda: {"runs": 0, "skill_triggered": 0, "never_ran_violations": 0})
    for key, values in grouped.items():
        instruction = sum(value[0] for value in values) / len(values)
        prompt = sum(value[1] for value in values) / len(values)
        trial_scores[key] = (instruction, prompt)
        skill, _, model, _, _ = key
        counter = guardrails[(skill, model)]
        counter["runs"] += 1
        assertions = [assertion for _, _, result_assertions in values for assertion in result_assertions]
        if any(assertion.get("id", "").startswith("used-skill:") and assertion_passed(assertion) for assertion in assertions):
            counter["skill_triggered"] += 1
        counter["never_ran_violations"] += sum(
            assertion.get("id", "").startswith("never-ran:") and not assertion_passed(assertion)
            for assertion in assertions
        )

    task_scores = defaultdict(list)
    for (skill, permutation, model, task_id, _), score in trial_scores.items():
        task_scores[(skill, model, task_id, permutation)].append(score)
    means = {
        key: (
            sum(score[0] for score in values) / len(values),
            sum(score[1] for score in values) / len(values),
        )
        for key, values in task_scores.items()
    }

    by_comparison = defaultdict(dict)
    for (skill, model, task_id, permutation), score in means.items():
        by_comparison[(skill, model)][task_id, permutation] = score

    comparisons = []
    for (skill, model), task_map in sorted(by_comparison.items()):
        paired = []
        full_instruction = []
        simple_instruction = []
        full_prompt = []
        simple_prompt = []
        for task_id in sorted({task for task, _ in task_map}):
            full = task_map.get((task_id, "full"))
            simple = task_map.get((task_id, "simple"))
            if full is None or simple is None:
                continue
            paired.append(simple[0] - full[0])
            full_instruction.append(full[0])
            simple_instruction.append(simple[0])
            full_prompt.append(full[1])
            simple_prompt.append(simple[1])
        if not paired:
            continue
        delta = sum(paired) / len(paired)
        lower, upper, pvalue = bootstrap_interval(paired)
        counter = guardrails[(skill, model)]
        comparisons.append({
            "skill": skill,
            "model": model,
            "n_tasks": len(paired),
            "full_instruction": sum(full_instruction) / len(full_instruction),
            "simple_instruction": sum(simple_instruction) / len(simple_instruction),
            "full_prompt": sum(full_prompt) / len(full_prompt),
            "simple_prompt": sum(simple_prompt) / len(simple_prompt),
            "delta": delta,
            "lower": lower,
            "upper": upper,
            "pvalue": pvalue,
            "raw_verdict": verdict(delta, lower, upper),
            "skill_trigger_rate": counter["skill_triggered"] / counter["runs"],
            "never_ran_violations": counter["never_ran_violations"],
        })
    holm(comparisons)
    return comparisons


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("results")
    args = parser.parse_args()
    with open(args.results, encoding="utf-8") as source:
        comparisons = analyze(rows_from(json.load(source)))
    print("skill model full simple prompt-full prompt-simple delta 95% CI n raw holm trigger never-ran")
    for result in comparisons:
        print(
            "{skill} {model} {full_instruction:.3f} {simple_instruction:.3f} "
            "{full_prompt:.3f} {simple_prompt:.3f} {delta:+.3f} "
            "[{lower:+.3f}, {upper:+.3f}] {n_tasks} {raw_verdict} {adjusted_verdict} "
            "{skill_trigger_rate:.3f} {never_ran_violations}".format(**result)
        )
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"analyze: {error}", file=sys.stderr)
        sys.exit(1)
