"""Data shapes the analysis stages hand each other, and their JSON boundary.

extract.py writes one run record per line of metrics.jsonl; analyze.py reads
those back and writes one Analysis to analysis.json; report.py reads that.
Every shape is a frozen dataclass, and `to_json` / `from_json` are the only
places a stage meets a dict, so the file formats are these classes' fields.
"""

from __future__ import annotations

import dataclasses
import enum
import types
import typing
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any, Self


class Arm(enum.StrEnum):
    """The two recipes the paired comparison names.

    A record's arm stays a plain string: the runner accepts arms outside this
    pair (`_selftest`), and cells and pass rates cover whatever ran.
    """

    FULL = "full"
    RAMPANT = "rampant"


class ControlKind(enum.StrEnum):
    GOLDEN = "golden"
    NULL = "null"


class Verdict(enum.StrEnum):
    LOWER = "lower under full"
    HIGHER = "higher under full"
    INCONCLUSIVE = "inconclusive"


class JsonRecord:
    """Mixin for the frozen dataclasses below: nested fields out, nested fields in.

    `from_json` rebuilds a value from its field annotation: a JsonRecord, an
    enum, `X | None`, `tuple[X, ...]` and `dict[str, X]` are decoded, any other
    annotation is taken as the JSON scalar it already is. A field with a default
    may be absent from the data; any other missing field raises KeyError.
    """

    def to_json(self) -> dict[str, Any]:
        return dataclasses.asdict(self)  # type: ignore[call-overload]

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> Self:
        hints = typing.get_type_hints(cls)
        values: dict[str, Any] = {}
        for field in dataclasses.fields(cls):  # type: ignore[arg-type]
            if field.name in data:
                values[field.name] = _decode(hints[field.name], data[field.name])
            elif _required(field):
                raise KeyError(field.name)
        return cls(**values)


def _required(field: dataclasses.Field[Any]) -> bool:
    return field.default is dataclasses.MISSING and field.default_factory is dataclasses.MISSING


def _decode(hint: Any, value: Any) -> Any:
    origin = typing.get_origin(hint)
    if origin in (types.UnionType, typing.Union):
        if value is None:
            return None
        inner = next(arg for arg in typing.get_args(hint) if arg is not type(None))
        return _decode(inner, value)
    if origin is tuple:
        return tuple(_decode(typing.get_args(hint)[0], item) for item in value)
    if origin is dict:
        return {key: _decode(typing.get_args(hint)[1], item) for key, item in value.items()}
    if isinstance(hint, type) and issubclass(hint, JsonRecord):
        return hint.from_json(value)
    if isinstance(hint, type) and issubclass(hint, enum.Enum):
        return hint(value)
    return value


@dataclass(frozen=True)
class TokenCounts(JsonRecord):
    """Billed tokens of one run. total_billed is a token count, not a price."""

    input: int
    output: int
    cache_read: int
    cache_write: int
    total_billed: int


@dataclass(frozen=True)
class GuardEvents(JsonRecord):
    """Guard activity read from the magus trail.

    A run carries None instead when no trail was captured; zero means the guard
    was present and silent, and the two must never be conflated.
    """

    denials: int
    advisories: int
    skill_loads: int


@dataclass(frozen=True)
class InvariantViolations(JsonRecord):
    tests_deleted: tuple[str, ...] = ()


@dataclass(frozen=True)
class ControlRun(JsonRecord):
    """A golden or null control: identity, kind and check verdict, no agent."""

    run_id: str
    arm: str
    task: str
    rep: int
    model: str
    control: ControlKind
    exit_reason: str | None
    check_exit: int | None
    success: bool | None


@dataclass(frozen=True)
class ScoredRun(JsonRecord):
    """One agent run measured from its artifacts.

    `success` is None when no check ran, and `guard_events` is None when no
    trail was captured; neither is a zero. `dollars` is the host's billed cost
    when the transcript recorded one and the priced table total otherwise.
    """

    run_id: str
    arm: str
    task: str
    rep: int
    model: str
    tokens: TokenCounts
    dollars: float
    table_dollars_usd: float
    reported_cost_usd: float | None
    cache_write_ttl_assumed: bool
    turns: int
    tool_calls: int
    tool_calls_by_name: dict[str, int]
    file_reads: int
    distinct_files_read: int
    re_read_rate: float
    tool_result_bytes: int
    guard_events: GuardEvents | None
    check_exit: int | None
    success: bool | None
    invariant_violations: InvariantViolations
    wall_ms: int | None
    time_to_first_edit_ms: int | None
    time_to_done_ms: int | None
    effort: str | None = None
    max_turns: int | None = None
    budget_usd: float | None = None
    magus_binary: str | None = None
    magus_version: str | None = None
    fixture_sha: str | None = None
    started: str | None = None
    ended: str | None = None
    exit_reason: str | None = None

    def to_json(self) -> dict[str, Any]:
        # Every row carries the key so a reader tells the two kinds apart by it.
        return {**super().to_json(), "control": None}


RunRecord = ScoredRun | ControlRun


def run_from_json(data: Mapping[str, Any]) -> RunRecord:
    if data.get("control"):
        return ControlRun.from_json(data)
    return ScoredRun.from_json(data)


@dataclass(frozen=True)
class Spread(JsonRecord):
    """Median and IQR beside the mean; a mean alone hides the spread that matters."""

    n: int
    median: float | None
    iqr: tuple[float | None, float | None]
    mean: float | None


@dataclass(frozen=True)
class CellStats(JsonRecord):
    """Pass rates and metric spreads of one (arm, task) cell."""

    n: int
    successes: int
    unknown_outcomes: int
    pass_at_1: float | None
    pass_at_1_ci: tuple[float | None, float | None]
    k: int
    pass_pow_k: float
    metrics: dict[str, Spread]
    metrics_success_only: dict[str, Spread]


@dataclass(frozen=True)
class CostOfPass(JsonRecord):
    """Expected dollars per correct solution; infinite at a zero pass rate."""

    value: float | None
    infinite: bool


@dataclass(frozen=True)
class ArmSummary(JsonRecord):
    n: int
    successes: int
    pass_rate: float
    pass_rate_ci: tuple[float | None, float | None]
    dollars: Spread
    cost_of_pass_usd: CostOfPass


@dataclass(frozen=True)
class PairedDelta(JsonRecord):
    """Treatment minus baseline for one metric on one task, over matched reps."""

    n_pairs: int
    delta_mean: float | None
    delta_median: float | None
    ci_low: float | None
    ci_high: float | None
    p: float | None
    baseline_median: float | None
    relative: float | None
    ci_excludes_zero: bool
    verdict: Verdict
    p_holm: float | None = None


@dataclass(frozen=True)
class ControlCount(JsonRecord):
    n: int
    passes: int
    run_ids: tuple[str, ...]


@dataclass(frozen=True)
class ControlCell(JsonRecord):
    """Whether one task's check discriminates: golden must pass, null must fail."""

    golden: ControlCount
    null: ControlCount
    discriminates: bool


@dataclass(frozen=True)
class UnpairedRep(JsonRecord):
    task: str
    rep: int
    missing_arms: tuple[str, ...]


@dataclass(frozen=True)
class DataQuality(JsonRecord):
    """Facts the report's caveats are generated from, not prose about them."""

    table_to_billed_ratio_median: float | None
    runs_without_billed_cost: tuple[str, ...]
    runs_without_guard_events: tuple[str, ...]
    runs_without_check: tuple[str, ...]
    runs_with_assumed_cache_ttl: tuple[str, ...]
    runs_with_invariant_violations: tuple[str, ...]
    incomplete_pairs: tuple[UnpairedRep, ...]


@dataclass(frozen=True)
class Analysis(JsonRecord):
    """analysis.json. Cells are keyed `arm/task`; paired is keyed metric, then task."""

    seed: int
    bootstrap_iters: int
    min_relative_delta: float
    runs: int
    arms: tuple[str, ...]
    tasks: tuple[str, ...]
    models: tuple[str, ...]
    controls: dict[str, ControlCell]
    cells: dict[str, CellStats]
    arm_summary: dict[str, ArmSummary]
    paired: dict[str, dict[str, PairedDelta]]
    data_quality: DataQuality
