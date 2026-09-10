#!/usr/bin/env python3
"""Render analysis.json as report.md.

Every table and every caveat is generated from the analysis; nothing here is
written by hand, so a report can never claim more than the data behind it.
"""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Iterable, Iterator, Sequence
from pathlib import Path
from typing import NamedTuple

from records import Analysis, ControlCell, ControlCount, PairedDelta


class Headline(NamedTuple):
    name: str
    label: str
    digits: int


# Metrics given their own delta table. The rest stay in analysis.json rather
# than padding the report with fourteen tables nobody reads.
HEADLINE_METRICS = (
    Headline("total_billed_tokens", "total billed tokens", 1),
    Headline("dollars", "dollars", 4),
    Headline("wall_ms", "wall clock (ms)", 1),
    Headline("turns", "turns", 1),
    Headline("tool_calls", "tool calls", 1),
    Headline("file_reads", "file reads", 1),
    Headline("re_read_rate", "re-read rate", 4),
    Headline("tool_result_bytes", "tool result bytes", 1),
)

# Below this the paired deltas are directional at best; Terminal-Bench runs 5.
MIN_REPS = 5

DELTA_HEADER = (
    "task",
    "pairs",
    "median delta",
    "mean delta",
    "95% CI",
    "relative",
    "p (Holm)",
    "verdict",
)

NO_CONTROLS = ControlCount(n=0, passes=0, run_ids=())


class ReportError(Exception):
    """The analysis file cannot be rendered; say which file and why."""


def num(value: float | None, digits: int = 2) -> str:
    if value is None:
        return "n/a"
    if isinstance(value, float):
        return f"{value:.{digits}f}"
    return str(value)


def usd(value: float | None) -> str:
    return f"${num(value, 4)}"


def pct(value: float | None) -> str:
    return "n/a" if value is None else f"{100.0 * value:.0f}%"


def interval(bounds: tuple[float | None, float | None], digits: int = 2) -> str:
    low, high = bounds
    if low is None or high is None:
        return "n/a"
    return f"[{num(low, digits)}, {num(high, digits)}]"


def table(header: Sequence[str], rows: Iterable[Sequence[str]]) -> Iterator[str]:
    yield "| " + " | ".join(header) + " |"
    yield "| " + " | ".join("---" for _ in header) + " |"
    for row in rows:
        yield "| " + " | ".join(row) + " |"


def section(title: str, blurb: str, body: Iterable[str]) -> Iterator[str]:
    yield f"## {title}"
    yield ""
    yield blurb
    yield ""
    yield from body
    yield ""


def controls(analysis: Analysis) -> Iterator[str]:
    unverified = ControlCell(golden=NO_CONTROLS, null=NO_CONTROLS, discriminates=False)

    def row(task: str) -> tuple[str, ...]:
        cell = analysis.controls.get(task, unverified)
        if cell.discriminates:
            verdict = "yes"
        elif cell.golden.n == 0 or cell.null.n == 0:
            verdict = "UNVERIFIED (control missing)"
        else:
            verdict = "NO"
        return (
            task,
            f"{cell.golden.passes}/{cell.golden.n}",
            f"{cell.null.passes}/{cell.null.n}",
            verdict,
        )

    yield from section(
        "Controls",
        "Whether each task's check can tell a solution from its absence: the golden "
        "control applies the known solution and must pass, the null control touches "
        "nothing and must fail. A pass rate below is only worth reading where both hold.",
        table(
            ("task", "golden (pass/n)", "null (pass/n)", "checks discriminate"),
            map(row, analysis.tasks),
        ),
    )


def headline(analysis: Analysis) -> Iterator[str]:
    def row(arm: str) -> tuple[str, ...]:
        summary = analysis.arm_summary[arm]
        cost = summary.cost_of_pass_usd
        return (
            arm,
            str(summary.n),
            str(summary.successes),
            pct(summary.pass_rate),
            interval(summary.pass_rate_ci, 3),
            usd(summary.dollars.mean),
            usd(summary.dollars.median),
            "infinite (no passes)" if cost.infinite else usd(cost.value),
        )

    yield from section(
        "Headline: cost-of-pass",
        "Expected dollars per correct solution (mean dollars / pass rate).",
        table(
            ("arm", "n", "passes", "pass rate", "Wilson 95%", "mean $", "median $", "cost-of-pass"),
            map(row, analysis.arms),
        ),
    )


def pareto(analysis: Analysis) -> Iterator[str]:
    def row(key: str) -> tuple[str, ...]:
        arm, task = key.split("/", 1)
        cell = analysis.cells[key]
        return (
            arm,
            task,
            str(cell.n),
            pct(cell.pass_at_1),
            num(cell.metrics["total_billed_tokens"].median, 0),
            num(cell.metrics_success_only["total_billed_tokens"].median, 0),
            usd(cell.metrics["dollars"].median),
        )

    yield from section(
        "Correctness against median tokens",
        "The cost-accuracy frontier in text: pass rate beside the token spend it cost.",
        table(
            (
                "arm",
                "task",
                "n",
                "pass@1",
                "median tokens",
                "median tokens (passes only)",
                "median $",
            ),
            map(row, sorted(analysis.cells)),
        ),
    )


def deltas(analysis: Analysis) -> Iterator[str]:
    yield "## Paired deltas, full minus rampant"
    yield ""
    yield (
        f"Rep i of one arm is paired with rep i of the other. CI is a seeded "
        f"{analysis.bootstrap_iters}-sample bootstrap of the mean paired delta; p is "
        f"Holm-adjusted across tasks. A verdict needs the CI to exclude zero AND the delta "
        f"to reach {100.0 * analysis.min_relative_delta:.0f}% of the rampant median."
    )
    yield ""
    for metric in HEADLINE_METRICS:
        per_task = analysis.paired.get(metric.name)
        if not per_task:
            continue
        yield f"### {metric.label}"
        yield ""
        rows = (
            _delta_row(task, per_task[task], metric.digits)
            for task in analysis.tasks
            if task in per_task
        )
        yield from table(DELTA_HEADER, rows)
        yield ""


def _delta_row(task: str, delta: PairedDelta, digits: int) -> tuple[str, ...]:
    return (
        task,
        str(delta.n_pairs),
        num(delta.delta_median, digits),
        num(delta.delta_mean, digits),
        interval((delta.ci_low, delta.ci_high), digits),
        pct(delta.relative),
        num(delta.p_holm, 3),
        delta.verdict,
    )


def pass_rates(analysis: Analysis) -> Iterator[str]:
    def row(key: str) -> tuple[str, ...]:
        arm, task = key.split("/", 1)
        cell = analysis.cells[key]
        return (
            arm,
            task,
            str(cell.k),
            pct(cell.pass_at_1),
            interval(cell.pass_at_1_ci, 3),
            num(cell.pass_pow_k, 0),
        )

    yield from section(
        "pass@1 and pass^k",
        "pass@1 is capability; pass^k (all k reps succeed) is reliability.",
        table(
            ("arm", "task", "k", "pass@1", "Wilson 95%", "pass^k"),
            map(row, sorted(analysis.cells)),
        ),
    )


def caveats(analysis: Analysis) -> Iterator[str]:
    yield "## Caveats"
    yield ""
    for line in caveat_lines(analysis):
        yield f"- {line}"
    yield ""


def caveat_lines(analysis: Analysis) -> Iterator[str]:
    """One caveat per fact in the data that limits what the tables above can claim."""
    quality = analysis.data_quality
    cells = analysis.cells
    yield "Reps per cell: " + ", ".join(f"{key} n={cells[key].n}" for key in sorted(cells)) + "."
    if small := sorted(key for key in cells if cells[key].n < MIN_REPS):
        yield (
            f"Under the {MIN_REPS}-rep protocol: {', '.join(small)}. "
            "Treat those deltas as directional."
        )
    for gap in quality.incomplete_pairs:
        yield (
            f"Unpaired: {gap.task} rep {gap.rep} is missing arm(s) {', '.join(gap.missing_arms)}, "
            "so it contributes to no delta."
        )
    if quality.runs_without_check:
        yield (
            f"Unknown outcome: {len(quality.runs_without_check)} run(s) carry no acceptance check "
            f"and are excluded from pass rates ({', '.join(quality.runs_without_check)})."
        )
    if no_trail := quality.runs_without_guard_events:
        yield (
            f"No activity trail for {len(no_trail)} run(s) ({', '.join(no_trail)}); "
            "their guard_events are null, not zero."
        )
    if assumed := quality.runs_with_assumed_cache_ttl:
        yield (
            f"Cache-write TTL was not reported for {len(assumed)} run(s) ({', '.join(assumed)}); "
            "those writes are priced at the 5-minute rate, so their dollars are a floor."
        )
    if violated := quality.runs_with_invariant_violations:
        yield f"Invariant violation (test file deleted) in {', '.join(violated)}."
    if len(analysis.models) > 1:
        yield (
            f"More than one model appears across runs ({', '.join(analysis.models)}); the arm is "
            "no longer the only variable."
        )
    ratio = quality.table_to_billed_ratio_median
    if ratio is not None and not 0.9 <= ratio <= 1.1:
        yield (
            f"Dollars are the host's billed cost; the pricing table would have said {ratio:.2f}x "
            "that, so the table is wrong for this model and only backs runs with no result record."
        )
    if quality.runs_without_billed_cost:
        yield (
            f"No billed cost for {len(quality.runs_without_billed_cost)} run(s); "
            "their dollars come from the pricing table."
        )
    unverified = [task for task in analysis.tasks if not _discriminates(analysis, task)]
    if unverified:
        yield (
            f"Checks not shown to discriminate for {', '.join(unverified)} (see Controls); pass "
            "rates there are not evidence."
        )


def _discriminates(analysis: Analysis, task: str) -> bool:
    cell = analysis.controls.get(task)
    return cell is not None and cell.discriminates


def render(analysis: Analysis) -> str:
    intro = (
        f"{analysis.runs} runs, {len(analysis.tasks)} task(s), arms {' and '.join(analysis.arms)}, "
        f"model(s) {' and '.join(analysis.models) or 'unrecorded'}, bootstrap seed {analysis.seed}."
    )
    lines = [
        "# Harness-effectiveness benchmark",
        "",
        intro,
        "",
        *controls(analysis),
        *headline(analysis),
        *pareto(analysis),
        *deltas(analysis),
        *pass_rates(analysis),
        *caveats(analysis),
    ]
    return "\n".join(lines)


def load_analysis(path: Path) -> Analysis:
    with path.open(encoding="utf-8") as fh:
        try:
            return Analysis.from_json(json.load(fh))
        except (json.JSONDecodeError, KeyError, TypeError, ValueError) as exc:
            raise ReportError(f"{path}: not an analysis.json: {exc}") from exc


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("analysis", type=Path, help="analysis.json from analyze.py")
    parser.add_argument("-o", "--out", type=Path, required=True, help="report.md to write")
    args = parser.parse_args(argv)

    args.out.write_text(render(load_analysis(args.analysis)) + "\n", encoding="utf-8")
    print(f"wrote {args.out}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ReportError as err:
        print(f"report: {err}", file=sys.stderr)
        sys.exit(1)
