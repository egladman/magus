#!/usr/bin/env python3
"""Turn a benchmark results tree into one metrics record per run.

Reads `<results>/<run-id>/` directories written by the runner (meta.json,
transcript.jsonl, final.diff, check.exit, timing.json, activity/events.jsonl)
and writes metrics.jsonl, one ScoredRun or ControlRun per line. Every derived
number comes from a file on disk; a run whose transcript carries no usage
accounting is an error, not a row of zeros.
"""

from __future__ import annotations

import argparse
import json
import posixpath
import re
import sys
from collections.abc import Iterator, Mapping
from dataclasses import dataclass, field, replace
from pathlib import Path
from typing import Any, NamedTuple

from records import (
    ControlKind,
    ControlRun,
    GuardEvents,
    InvariantViolations,
    RunRecord,
    ScoredRun,
    TokenCounts,
)

USD_PER_TOKEN = 1_000_000.0

# A transcript records the dated model id the API served (claude-opus-5-20260301);
# the pricing table is keyed by alias, so the date is stripped before lookup.
DATED_MODEL_SUFFIX = re.compile(r"-\d{8}$")

# Tool names whose call counts feed file_reads / re_read_rate.
READ_TOOLS = frozenset({"Read", "NotebookRead"})

TEST_FILE_MARKERS = ("_test.", ".test.")

REQUIRED_META = ("run_id", "arm", "task", "rep", "model")
OPTIONAL_META = (
    "effort",
    "max_turns",
    "budget_usd",
    "magus_binary",
    "magus_version",
    "fixture_sha",
    "started",
    "ended",
    "exit_reason",
)


class ExtractError(Exception):
    """A run's artifacts cannot be measured; extraction must stop and say why."""


@dataclass(frozen=True)
class Rates:
    """USD per million tokens for one model, in the pricing table's units."""

    input: float
    output: float
    cache_read: float
    cache_write_5m: float
    cache_write_1h: float


@dataclass(frozen=True)
class PriceTable:
    models: Mapping[str, Rates]

    def rates(self, model: str) -> Rates | None:
        return self.models.get(model) or self.models.get(DATED_MODEL_SUFFIX.sub("", model))


def load_pricing(path: Path) -> PriceTable:
    """Load the per-model price table; the file is the only source of prices."""
    with path.open(encoding="utf-8") as fh:
        table = json.load(fh)
    models = table.get("models")
    if not isinstance(models, dict) or not models:
        raise ExtractError(f"pricing table {path} has no models")
    try:
        return PriceTable({model: Rates(**rates) for model, rates in models.items()})
    except TypeError as exc:
        raise ExtractError(f"pricing table {path}: a model lacks the five rates: {exc}") from exc


@dataclass(frozen=True)
class UsageCounts:
    """The five billable counters of one assistant turn, or their sum over a model."""

    input: int = 0
    output: int = 0
    cache_read: int = 0
    cache_write_5m: int = 0
    cache_write_1h: int = 0

    def __add__(self, other: UsageCounts) -> UsageCounts:
        return UsageCounts(
            self.input + other.input,
            self.output + other.output,
            self.cache_read + other.cache_read,
            self.cache_write_5m + other.cache_write_5m,
            self.cache_write_1h + other.cache_write_1h,
        )

    def cost(self, rates: Rates) -> float:
        total = 0.0
        total += self.input * rates.input / USD_PER_TOKEN
        total += self.output * rates.output / USD_PER_TOKEN
        total += self.cache_read * rates.cache_read / USD_PER_TOKEN
        total += self.cache_write_5m * rates.cache_write_5m / USD_PER_TOKEN
        total += self.cache_write_1h * rates.cache_write_1h / USD_PER_TOKEN
        return total

    def as_tokens(self) -> TokenCounts:
        cache_write = self.cache_write_5m + self.cache_write_1h
        return TokenCounts(
            input=self.input,
            output=self.output,
            cache_read=self.cache_read,
            cache_write=cache_write,
            total_billed=self.input + self.output + self.cache_read + cache_write,
        )


def price_usage(by_model: Mapping[str, UsageCounts], pricing: PriceTable, run_id: str) -> float:
    """Cost the per-model token totals. An unpriced model stops the run."""
    total = 0.0
    for model in sorted(by_model):
        rates = pricing.rates(model)
        if rates is None:
            raise ExtractError(
                f"{run_id}: model {model!r} is absent from the pricing table; "
                "add its published prices"
            )
        total += by_model[model].cost(rates)
    return total


def content_text(content: Any) -> str:
    """Flatten a message/tool_result content field to the text a reader would see."""
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(_block_text(block) for block in content)
    return json.dumps(content, sort_keys=True)


def _block_text(block: Any) -> str:
    if isinstance(block, str):
        return block
    if not isinstance(block, dict):
        return ""
    match block.get("type"):
        case "text":
            return block.get("text") or ""
        case "image":
            return "[image]"
        case _:
            return json.dumps(block, sort_keys=True)


def _split_usage(usage: Mapping[str, Any], run_id: str, index: int) -> tuple[UsageCounts, bool]:
    """Pull the token counters out of one turn's usage; the flag says the TTL was guessed."""
    for key in ("input_tokens", "output_tokens"):
        if not isinstance(usage.get(key), int):
            raise ExtractError(
                f"{run_id}: assistant record {index} has no {key}; refusing to report zero tokens"
            )
    write_5m = 0
    write_1h = 0
    breakdown = usage.get("cache_creation")
    if isinstance(breakdown, dict):
        write_5m = breakdown.get("ephemeral_5m_input_tokens") or 0
        write_1h = breakdown.get("ephemeral_1h_input_tokens") or 0
    ttl_assumed = False
    if write_5m == 0 and write_1h == 0:
        # Only the flat counter is present, so the TTL is unknowable; 5m is the
        # API default and the cheaper of the two, which keeps the bill a floor.
        write_5m = usage.get("cache_creation_input_tokens") or 0
        ttl_assumed = write_5m > 0
    counts = UsageCounts(
        input=usage["input_tokens"],
        output=usage["output_tokens"],
        cache_read=usage.get("cache_read_input_tokens") or 0,
        cache_write_5m=write_5m,
        cache_write_1h=write_1h,
    )
    return counts, ttl_assumed


@dataclass(frozen=True)
class Transcript:
    """What one transcript.jsonl measures: tokens per model, turns, tools, reads."""

    by_model: Mapping[str, UsageCounts]
    cache_write_ttl_assumed: bool
    turns: int
    tool_calls_by_name: Mapping[str, int]
    read_paths: tuple[str, ...]
    tool_result_bytes: int
    reported_cost_usd: float | None

    @property
    def tokens(self) -> TokenCounts:
        return sum(self.by_model.values(), UsageCounts()).as_tokens()

    @property
    def tool_calls(self) -> int:
        return sum(self.tool_calls_by_name.values())

    @property
    def distinct_files_read(self) -> int:
        return len(set(self.read_paths))

    @property
    def re_read_rate(self) -> float:
        reads = len(self.read_paths)
        return (reads - self.distinct_files_read) / reads if reads else 0.0


@dataclass
class _TranscriptTally:
    """Mutable accumulator behind read_transcript; one method per record kind."""

    run_id: str
    default_model: str | None
    by_model: dict[str, UsageCounts] = field(default_factory=dict)
    seen_messages: set[str] = field(default_factory=set)
    turns: int = 0
    ttl_assumed: bool = False
    tool_calls: dict[str, int] = field(default_factory=dict)
    read_paths: list[str] = field(default_factory=list)
    tool_result_bytes: int = 0
    init_model: str | None = None
    reported_cost: float | None = None
    result_output: int | None = None

    def take(self, rec: Mapping[str, Any]) -> None:
        kind = rec.get("type")
        if kind == "system" and rec.get("subtype") == "init":
            self.init_model = rec.get("model") or self.init_model
        elif kind == "result":
            self._take_result(rec)
        elif kind in ("assistant", "user"):
            message = rec.get("message")
            if not isinstance(message, dict):
                return
            if kind == "assistant":
                self._take_assistant(message)
            else:
                self._take_user(message)

    def _take_result(self, rec: Mapping[str, Any]) -> None:
        cost = rec.get("total_cost_usd")
        if isinstance(cost, (int, float)):
            self.reported_cost = float(cost)
        usage = rec.get("usage")
        if isinstance(usage, dict) and isinstance(usage.get("output_tokens"), int):
            self.result_output = usage["output_tokens"]

    def _take_assistant(self, message: Mapping[str, Any]) -> None:
        message_id = message.get("id")
        if message_id is not None:
            if message_id in self.seen_messages:
                return
            self.seen_messages.add(message_id)
        self.turns += 1
        usage = message.get("usage")
        if not isinstance(usage, dict):
            raise ExtractError(
                f"{self.run_id}: assistant record {self.turns} has no message.usage; "
                "refusing to report zero tokens"
            )
        counts, assumed = _split_usage(usage, self.run_id, self.turns)
        self.ttl_assumed = self.ttl_assumed or assumed
        model = message.get("model") or self.init_model or self.default_model
        if not model:
            raise ExtractError(f"{self.run_id}: assistant record {self.turns} has no model")
        self.by_model[model] = self.by_model.get(model, UsageCounts()) + counts
        for block in _blocks(message):
            if block.get("type") == "tool_use":
                self._take_tool_use(block)

    def _take_tool_use(self, block: Mapping[str, Any]) -> None:
        name = block.get("name") or "unknown"
        self.tool_calls[name] = self.tool_calls.get(name, 0) + 1
        if name in READ_TOOLS:
            args = block.get("input") or {}
            self.read_paths.append(args.get("file_path") or args.get("notebook_path") or "")

    def _take_user(self, message: Mapping[str, Any]) -> None:
        for block in _blocks(message):
            if block.get("type") == "tool_result":
                self.tool_result_bytes += len(content_text(block.get("content")))

    def finish(self) -> Transcript:
        if self.turns == 0:
            raise ExtractError(f"{self.run_id}: transcript carries no assistant records")
        return Transcript(
            by_model=self._billed_output(),
            cache_write_ttl_assumed=self.ttl_assumed,
            turns=self.turns,
            tool_calls_by_name=dict(sorted(self.tool_calls.items())),
            read_paths=tuple(self.read_paths),
            tool_result_bytes=self.tool_result_bytes,
            reported_cost_usd=self.reported_cost,
        )

    def _billed_output(self) -> dict[str, UsageCounts]:
        # An assistant record's usage carries the output of one streamed chunk, not
        # the turn, so their sum runs a whole run's output at a twentieth of what the
        # host bills (118 summed against 2628 in the result record, pilot 2026-09-10).
        # The input and cache counters agree between the two, so only output is
        # replaced, apportioned across models by the share each summed to.
        if self.result_output is None:
            return self.by_model
        summed = sum(counts.output for counts in self.by_model.values())
        billed: dict[str, UsageCounts] = {}
        for model, counts in self.by_model.items():
            share = counts.output / summed if summed else 1.0 / len(self.by_model)
            billed[model] = replace(counts, output=round(self.result_output * share))
        return billed


def _blocks(message: Mapping[str, Any]) -> list[dict[str, Any]]:
    content = message.get("content")
    if not isinstance(content, list):
        return []
    return [block for block in content if isinstance(block, dict)]


def _jsonl_records(path: Path, run_id: str) -> Iterator[Any]:
    with path.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if not line.strip():
                continue
            try:
                yield json.loads(line)
            except json.JSONDecodeError as exc:
                raise ExtractError(f"{run_id}: malformed {path.name} line: {exc}") from exc


def read_transcript(path: Path, run_id: str, default_model: str | None) -> Transcript:
    """Walk a stream-json transcript into token, tool and turn counts.

    Raises ExtractError when no assistant record carries usage, or when one
    carries a usage block missing a token counter. Both cases are the vacuous
    metrics defect: silence there reads as a free run.
    """
    if not path.exists():
        raise ExtractError(f"{run_id}: transcript.jsonl is missing")
    tally = _TranscriptTally(run_id, default_model)
    for rec in _jsonl_records(path, run_id):
        tally.take(rec)
    return tally.finish()


def read_guard_events(path: Path) -> GuardEvents | None:
    """Count guard denials, advisories and skill loads in the magus activity trail.

    Returns None when the trail was not captured; the caller must keep that null
    rather than substituting zero, which would read as a guard that never fired.
    """
    if not path.exists():
        return None
    denials = advisories = skill_loads = 0
    for event in _trail_events(path):
        action = (event.get("action") or "").lower()
        preview = (event.get("preview") or "").lower()
        target = (event.get("path") or event.get("target") or "").lower()
        if "guard: deny" in preview:
            denials += 1
        elif "guard: advis" in preview:
            advisories += 1
        if action.startswith("skill.") or (action == "file.read" and "/skills/" in target):
            skill_loads += 1
    return GuardEvents(denials=denials, advisories=advisories, skill_loads=skill_loads)


def _trail_events(path: Path) -> Iterator[dict[str, Any]]:
    """The trail's agent_command events; a malformed line is skipped, not fatal."""
    with path.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(event, dict) and event.get("kind") == "agent_command":
                yield event


def read_invariant_violations(path: Path) -> InvariantViolations:
    """Find deleted test files in a unified diff. Deletion is the only deterministic tell."""
    if not path.exists():
        return InvariantViolations()
    deleted: list[str] = []
    current: str | None = None
    with path.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if line.startswith("diff --git "):
                parts = line.split()
                current = parts[2][2:] if len(parts) >= 4 else None
            elif line.startswith("deleted file mode") and current:
                name = posixpath.basename(current)
                if any(marker in name for marker in TEST_FILE_MARKERS):
                    deleted.append(current)
    return InvariantViolations(tests_deleted=tuple(sorted(deleted)))


def read_check_exit(run_dir: Path) -> int | None:
    """Read check.exit as the run's outcome. Absent or unreadable means unknown, not failed."""
    path = run_dir / "check.exit"
    if not path.exists():
        return None
    raw = path.read_text(encoding="utf-8", errors="replace").split()
    try:
        return int(raw[0])
    except (IndexError, ValueError):
        return None


class Timing(NamedTuple):
    wall_ms: int | None = None
    time_to_first_edit_ms: int | None = None
    time_to_done_ms: int | None = None


def read_timing(run_dir: Path) -> Timing:
    path = run_dir / "timing.json"
    if not path.exists():
        return Timing()
    with path.open(encoding="utf-8") as fh:
        timing = json.load(fh)
    return Timing(**{name: timing.get(name) for name in Timing._fields})


def _load_meta(run_dir: Path) -> dict[str, Any]:
    path = run_dir / "meta.json"
    if not path.exists():
        raise ExtractError(f"{run_dir}: meta.json is missing")
    with path.open(encoding="utf-8") as fh:
        meta = json.load(fh)
    for name in REQUIRED_META:
        if meta.get(name) in (None, ""):
            raise ExtractError(f"{run_dir}: meta.json has no {name}")
    return meta


def extract_run(run_dir: Path, pricing: PriceTable) -> RunRecord:
    """Build one metrics record for a single run directory.

    A control run (golden or null) yields a record with its identity, its
    control kind and its check verdict and nothing else: no agent ran, so there
    is no transcript to measure. The report reads those verdicts to say whether
    the task's check discriminates at all. A scored run without a transcript is
    still an error.
    """
    meta = _load_meta(run_dir)
    identity = {name: meta[name] for name in REQUIRED_META}
    check_exit = read_check_exit(run_dir)
    success = None if check_exit is None else check_exit == 0
    if meta.get("control"):
        try:
            kind = ControlKind(meta["control"])
        except ValueError:
            raise ExtractError(
                f"{run_dir}: meta.json control {meta['control']!r} is not golden or null"
            ) from None
        return ControlRun(
            **identity,
            control=kind,
            exit_reason=meta.get("exit_reason"),
            check_exit=check_exit,
            success=success,
        )

    run_id = meta["run_id"]
    transcript = read_transcript(run_dir / "transcript.jsonl", run_id, meta.get("model"))
    table_dollars = price_usage(transcript.by_model, pricing, run_id)
    return ScoredRun(
        **identity,
        **{name: meta.get(name) for name in OPTIONAL_META},
        tokens=transcript.tokens,
        # The host's own bill wins when it recorded one: the table is a floor kept
        # for transcripts that end without a result record, and it disagreed with
        # the host by a constant 0.665x on Sonnet 5 and 1.663x on Opus 5 in the
        # 2026-09-10 pilot, which is a table error rather than noise.
        dollars=transcript.reported_cost_usd or table_dollars,
        table_dollars_usd=table_dollars,
        reported_cost_usd=transcript.reported_cost_usd,
        cache_write_ttl_assumed=transcript.cache_write_ttl_assumed,
        turns=transcript.turns,
        tool_calls=transcript.tool_calls,
        tool_calls_by_name=dict(transcript.tool_calls_by_name),
        file_reads=len(transcript.read_paths),
        distinct_files_read=transcript.distinct_files_read,
        re_read_rate=transcript.re_read_rate,
        tool_result_bytes=transcript.tool_result_bytes,
        guard_events=read_guard_events(run_dir / "activity" / "events.jsonl"),
        check_exit=check_exit,
        success=success,
        invariant_violations=read_invariant_violations(run_dir / "final.diff"),
        **read_timing(run_dir)._asdict(),
    )


def summary_line(record: RunRecord) -> str:
    """One line per run, for the operator watching extraction."""
    if isinstance(record, ControlRun):
        return f"{record.run_id}: {record.control} control, check={record.check_exit}"
    outcome = {True: "PASS", False: "FAIL", None: "NOCHECK"}[record.success]
    guard = record.guard_events
    if guard is None:
        guard_text = "guard=none"
    else:
        guard_text = f"guard={guard.denials}/{guard.advisories}/{guard.skill_loads}"
    return (
        f"{record.run_id:<34} {record.arm:<8} {record.task:<10} {outcome:<7} "
        f"tok={record.tokens.total_billed:<9d} ${record.dollars:.4f} "
        f"turns={record.turns:<3d} tools={record.tool_calls:<3d} {guard_text}"
    )


def extract_all(results: Path, pricing: PriceTable) -> list[RunRecord]:
    """Every run under results, sorted by run id. A tree of only controls is an error."""
    run_dirs = sorted(path for path in results.iterdir() if path.is_dir())
    if not run_dirs:
        raise ExtractError(f"no run directories under {results}")
    records = [extract_run(run_dir, pricing) for run_dir in run_dirs]
    if all(isinstance(record, ControlRun) for record in records):
        raise ExtractError(f"no scored runs under {results} ({len(records)} control runs)")
    return sorted(records, key=lambda record: record.run_id)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("results", type=Path, help="directory holding one subdirectory per run")
    parser.add_argument("-o", "--out", type=Path, required=True, help="metrics.jsonl to write")
    parser.add_argument(
        "-p",
        "--pricing",
        type=Path,
        default=Path(__file__).resolve().parent / "pricing.json",
        help="pricing table (default: the one shipped beside this script)",
    )
    args = parser.parse_args(argv)

    records = extract_all(args.results, load_pricing(args.pricing))
    with args.out.open("w", encoding="utf-8") as fh:
        for record in records:
            fh.write(json.dumps(record.to_json(), sort_keys=True) + "\n")
            print(summary_line(record))
    controls = sum(isinstance(record, ControlRun) for record in records)
    print(f"wrote {len(records)} runs to {args.out} ({controls} of them controls)")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ExtractError as err:
        print(f"extract: {err}", file=sys.stderr)
        sys.exit(2)
