#!/usr/bin/env python3
"""Render analysis.json as report.md.

Every table and every caveat is generated from the analysis; nothing here is
written by hand, so a report can never claim more than the data behind it.
"""

import argparse
import json
import sys

# Metrics given their own table. The rest stay in analysis.json rather than
# padding the report with fourteen tables nobody reads.
HEADLINE_METRICS = (
    ("total_billed_tokens", "total billed tokens"),
    ("dollars", "dollars"),
    ("wall_ms", "wall clock (ms)"),
    ("turns", "turns"),
    ("tool_calls", "tool calls"),
    ("file_reads", "file reads"),
    ("re_read_rate", "re-read rate"),
    ("tool_result_bytes", "tool result bytes"),
)

# Below this the paired deltas are directional at best; Terminal-Bench runs 5.
MIN_REPS = 5


def num(value, digits=2):
    if value is None:
        return "n/a"
    if isinstance(value, float):
        return "%.*f" % (digits, value)
    return str(value)


def pct(value):
    return "n/a" if value is None else "%.0f%%" % (100.0 * value)


def interval(low, high, digits=2):
    if low is None or high is None:
        return "n/a"
    return "[%s, %s]" % (num(low, digits), num(high, digits))


def controls(analysis, out):
    out.append("## Controls")
    out.append("")
    out.append(
        "Whether each task's check can tell a solution from its absence: the golden "
        "control applies the known solution and must pass, the null control touches "
        "nothing and must fail. A pass rate below is only worth reading where both hold."
    )
    out.append("")
    out.append("| task | golden (pass/n) | null (pass/n) | checks discriminate |")
    out.append("| --- | --- | --- | --- |")
    for task in analysis["tasks"]:
        cell = analysis["controls"].get(task) or {}
        golden = cell.get("golden") or {"passes": 0, "n": 0}
        null = cell.get("null") or {"passes": 0, "n": 0}
        if cell.get("discriminates"):
            verdict = "yes"
        elif golden["n"] == 0 or null["n"] == 0:
            verdict = "UNVERIFIED (control missing)"
        else:
            verdict = "NO"
        out.append(
            "| %s | %d/%d | %d/%d | %s |"
            % (task, golden["passes"], golden["n"], null["passes"], null["n"], verdict)
        )
    out.append("")


def headline(analysis, out):
    out.append("## Headline: cost-of-pass")
    out.append("")
    out.append("Expected dollars per correct solution (mean dollars / pass rate).")
    out.append("")
    out.append("| arm | n | passes | pass rate | Wilson 95% | mean $ | median $ | cost-of-pass |")
    out.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
    for arm in analysis["arms"]:
        summary = analysis["arm_summary"][arm]
        cost = summary["cost_of_pass_usd"]
        cost_text = "infinite (no passes)" if cost["infinite"] else "$" + num(cost["value"], 4)
        out.append(
            "| %s | %d | %d | %s | %s | %s | %s | %s |"
            % (
                arm,
                summary["n"],
                summary["successes"],
                pct(summary["pass_rate"]),
                interval(summary["pass_rate_ci"][0], summary["pass_rate_ci"][1], 3),
                "$" + num(summary["dollars"]["mean"], 4),
                "$" + num(summary["dollars"]["median"], 4),
                cost_text,
            )
        )
    out.append("")


def pareto(analysis, out):
    out.append("## Correctness against median tokens")
    out.append("")
    out.append("The cost-accuracy frontier in text: pass rate beside the token spend it cost.")
    out.append("")
    out.append(
        "| arm | task | n | pass@1 | median tokens | median tokens (passes only) | median $ |"
    )
    out.append("| --- | --- | --- | --- | --- | --- | --- |")
    for key in sorted(analysis["cells"]):
        arm, task = key.split("/", 1)
        cell = analysis["cells"][key]
        out.append(
            "| %s | %s | %d | %s | %s | %s | %s |"
            % (
                arm,
                task,
                cell["n"],
                pct(cell["pass_at_1"]),
                num(cell["metrics"]["total_billed_tokens"]["median"], 0),
                num(cell["metrics_success_only"]["total_billed_tokens"]["median"], 0),
                "$" + num(cell["metrics"]["dollars"]["median"], 4),
            )
        )
    out.append("")


def deltas(analysis, out):
    out.append("## Paired deltas, full minus rampant")
    out.append("")
    out.append(
        "Rep i of one arm is paired with rep i of the other. CI is a seeded %d-sample "
        "bootstrap of the mean paired delta; p is Holm-adjusted across tasks. A verdict "
        "needs the CI to exclude zero AND the delta to reach %.0f%% of the rampant median."
        % (analysis["bootstrap_iters"], 100.0 * analysis["min_relative_delta"])
    )
    out.append("")
    for name, label in HEADLINE_METRICS:
        per_task = analysis["paired"].get(name)
        if not per_task:
            continue
        out.append("### %s" % label)
        out.append("")
        out.append(
            "| task | pairs | median delta | mean delta | 95% CI | relative | p (Holm) | verdict |"
        )
        out.append("| --- | --- | --- | --- | --- | --- | --- | --- |")
        for task in analysis["tasks"]:
            row = per_task.get(task)
            if row is None:
                continue
            digits = 4 if name in ("dollars", "re_read_rate") else 1
            out.append(
                "| %s | %d | %s | %s | %s | %s | %s | %s |"
                % (
                    task,
                    row["n_pairs"],
                    num(row["delta_median"], digits),
                    num(row["delta_mean"], digits),
                    interval(row["ci_low"], row["ci_high"], digits),
                    pct(row["relative"]),
                    num(row.get("p_holm"), 3),
                    row["verdict"],
                )
            )
        out.append("")


def pass_rates(analysis, out):
    out.append("## pass@1 and pass^k")
    out.append("")
    out.append("pass@1 is capability; pass^k (all k reps succeed) is reliability.")
    out.append("")
    out.append("| arm | task | k | pass@1 | Wilson 95% | pass^k |")
    out.append("| --- | --- | --- | --- | --- | --- |")
    for key in sorted(analysis["cells"]):
        arm, task = key.split("/", 1)
        cell = analysis["cells"][key]
        out.append(
            "| %s | %s | %d | %s | %s | %s |"
            % (
                arm,
                task,
                cell["k"],
                pct(cell["pass_at_1"]),
                interval(cell["pass_at_1_ci"][0], cell["pass_at_1_ci"][1], 3),
                num(cell["pass_pow_k"], 0),
            )
        )
    out.append("")


def caveats(analysis, out):
    quality = analysis["data_quality"]
    lines = []
    cells = analysis["cells"]
    small = sorted(key for key in cells if cells[key]["n"] < MIN_REPS)
    counts = ", ".join("%s n=%d" % (key, cells[key]["n"]) for key in sorted(cells))
    lines.append("Reps per cell: %s." % counts)
    if small:
        lines.append(
            "Under the %d-rep protocol: %s. Treat those deltas as directional."
            % (MIN_REPS, ", ".join(small))
        )
    if quality["incomplete_pairs"]:
        for gap in quality["incomplete_pairs"]:
            lines.append(
                "Unpaired: %s rep %s is missing arm(s) %s, so it contributes to no delta."
                % (gap["task"], gap["rep"], ", ".join(gap["missing_arms"]))
            )
    if quality["runs_without_check"]:
        lines.append(
            "Unknown outcome: %d run(s) carry no acceptance check and are excluded from "
            "pass rates (%s)."
            % (len(quality["runs_without_check"]), ", ".join(quality["runs_without_check"]))
        )
    if quality["runs_without_guard_events"]:
        lines.append(
            "No activity trail for %d run(s) (%s); their guard_events are null, not zero."
            % (
                len(quality["runs_without_guard_events"]),
                ", ".join(quality["runs_without_guard_events"]),
            )
        )
    if quality["runs_with_assumed_cache_ttl"]:
        lines.append(
            "Cache-write TTL was not reported for %d run(s) (%s); those writes are priced "
            "at the 5-minute rate, so their dollars are a floor."
            % (
                len(quality["runs_with_assumed_cache_ttl"]),
                ", ".join(quality["runs_with_assumed_cache_ttl"]),
            )
        )
    if quality["runs_with_invariant_violations"]:
        lines.append(
            "Invariant violation (test file deleted) in %s."
            % ", ".join(quality["runs_with_invariant_violations"])
        )
    if len(analysis["models"]) > 1:
        lines.append(
            "More than one model appears across runs (%s); the arm is no longer the only "
            "variable." % ", ".join(analysis["models"])
        )
    ratio = quality.get("table_to_billed_ratio_median")
    if ratio is not None and not 0.9 <= ratio <= 1.1:
        lines.append(
            "Dollars are the host's billed cost; the pricing table would have said %.2fx "
            "that, so the table is wrong for this model and only backs runs with no "
            "result record." % ratio
        )
    if quality.get("runs_without_billed_cost"):
        lines.append(
            "No billed cost for %d run(s); their dollars come from the pricing table."
            % len(quality["runs_without_billed_cost"])
        )
    unverified = [t for t in analysis["tasks"] if not analysis["controls"].get(t, {}).get("discriminates")]
    if unverified:
        lines.append(
            "Checks not shown to discriminate for %s (see Controls); pass rates there are "
            "not evidence." % ", ".join(unverified)
        )
    out.append("## Caveats")
    out.append("")
    for line in lines:
        out.append("- %s" % line)
    out.append("")


def render(analysis):
    out = []
    out.append("# Harness-effectiveness benchmark")
    out.append("")
    out.append(
        "%d runs, %d task(s), arms %s, model(s) %s, bootstrap seed %d."
        % (
            analysis["runs"],
            len(analysis["tasks"]),
            " and ".join(analysis["arms"]),
            " and ".join(analysis["models"]) or "unrecorded",
            analysis["seed"],
        )
    )
    out.append("")
    controls(analysis, out)
    headline(analysis, out)
    pareto(analysis, out)
    deltas(analysis, out)
    pass_rates(analysis, out)
    caveats(analysis, out)
    return "\n".join(out)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("analysis", help="analysis.json from analyze.py")
    parser.add_argument("-o", "--out", required=True, help="report.md to write")
    args = parser.parse_args(argv)

    with open(args.analysis, encoding="utf-8") as fh:
        analysis = json.load(fh)
    text = render(analysis)
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(text + "\n")
    print("wrote %s" % args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
