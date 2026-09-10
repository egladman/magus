#!/usr/bin/env python3
"""Turn a benchmark results tree into one metrics record per run.

Reads `<results>/<run-id>/` directories written by the runner (meta.json,
transcript.jsonl, final.diff, check.exit, timing.json, activity/events.jsonl)
and writes metrics.jsonl. Every derived number comes from a file on disk; a run
whose transcript carries no usage accounting is an error, not a row of zeros.
"""

import argparse
import json
import os
import posixpath
import re
import sys

USD_PER_TOKEN = 1_000_000.0

# A transcript records the dated model id the API served (claude-opus-5-20260301);
# the pricing table is keyed by alias, so the date is stripped before lookup.
DATED_MODEL_SUFFIX = re.compile(r"-\d{8}$")

# Tool names whose call counts feed file_reads / re_read_rate.
READ_TOOLS = ("Read", "NotebookRead")

TEST_FILE_MARKERS = ("_test.", ".test.")


class ExtractError(Exception):
    """A run's artifacts cannot be measured; extraction must stop and say why."""


def norm_content(content):
    """Flatten a message/tool_result content field to the text a reader would see."""
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for block in content:
            if isinstance(block, str):
                parts.append(block)
            elif isinstance(block, dict):
                if block.get("type") == "text":
                    parts.append(block.get("text") or "")
                elif block.get("type") == "image":
                    parts.append("[image]")
                else:
                    parts.append(json.dumps(block, sort_keys=True))
        return "\n".join(parts)
    return json.dumps(content, sort_keys=True)


def load_pricing(path):
    """Load the per-model price table; the file is the only source of prices."""
    with open(path, encoding="utf-8") as fh:
        table = json.load(fh)
    models = table.get("models")
    if not isinstance(models, dict) or not models:
        raise ExtractError("pricing table %s has no models" % path)
    return models


def price_usage(by_model, pricing, run_id):
    """Cost the per-model token totals. An unpriced model stops the run."""
    total = 0.0
    for model in sorted(by_model):
        rates = pricing.get(model)
        if rates is None:
            rates = pricing.get(DATED_MODEL_SUFFIX.sub("", model))
        if rates is None:
            raise ExtractError(
                "%s: model %r is absent from the pricing table; add its published prices"
                % (run_id, model)
            )
        counts = by_model[model]
        total += counts["input"] * rates["input"] / USD_PER_TOKEN
        total += counts["output"] * rates["output"] / USD_PER_TOKEN
        total += counts["cache_read"] * rates["cache_read"] / USD_PER_TOKEN
        total += counts["cache_write_5m"] * rates["cache_write_5m"] / USD_PER_TOKEN
        total += counts["cache_write_1h"] * rates["cache_write_1h"] / USD_PER_TOKEN
    return total


def _empty_counts():
    return {
        "input": 0,
        "output": 0,
        "cache_read": 0,
        "cache_write_5m": 0,
        "cache_write_1h": 0,
    }


def _split_usage(usage, run_id, index):
    """Pull the four token counters out of one assistant turn's usage block."""
    for key in ("input_tokens", "output_tokens"):
        if not isinstance(usage.get(key), int):
            raise ExtractError(
                "%s: assistant record %d has no %s; refusing to report zero tokens"
                % (run_id, index, key)
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
    return {
        "input": usage["input_tokens"],
        "output": usage["output_tokens"],
        "cache_read": usage.get("cache_read_input_tokens") or 0,
        "cache_write_5m": write_5m,
        "cache_write_1h": write_1h,
    }, ttl_assumed


def read_transcript(path, run_id, default_model):
    """Walk a stream-json transcript into token, tool and turn counts.

    Raises ExtractError when no assistant record carries usage, or when one
    carries a usage block missing a token counter. Both cases are the vacuous
    metrics defect: silence there reads as a free run.
    """
    if not os.path.exists(path):
        raise ExtractError("%s: transcript.jsonl is missing" % run_id)

    by_model = {}
    seen_messages = set()
    turns = 0
    ttl_assumed = False
    tool_calls = {}
    read_paths = []
    tool_result_bytes = 0
    init_model = None
    reported_cost = None
    saw_result = False

    with open(path, encoding="utf-8", errors="replace") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except ValueError as exc:
                raise ExtractError("%s: malformed transcript line: %s" % (run_id, exc))
            kind = rec.get("type")
            if kind == "system" and rec.get("subtype") == "init":
                init_model = rec.get("model") or init_model
                continue
            if kind == "result":
                saw_result = True
                cost = rec.get("total_cost_usd")
                if isinstance(cost, (int, float)):
                    reported_cost = float(cost)
                continue
            message = rec.get("message")
            if not isinstance(message, dict):
                continue
            if kind == "assistant":
                mid = message.get("id")
                if mid is not None and mid in seen_messages:
                    continue
                if mid is not None:
                    seen_messages.add(mid)
                turns += 1
                usage = message.get("usage")
                if not isinstance(usage, dict):
                    raise ExtractError(
                        "%s: assistant record %d has no message.usage; refusing to "
                        "report zero tokens" % (run_id, turns)
                    )
                counts, assumed = _split_usage(usage, run_id, turns)
                ttl_assumed = ttl_assumed or assumed
                model = message.get("model") or init_model or default_model
                if not model:
                    raise ExtractError("%s: assistant record %d has no model" % (run_id, turns))
                bucket = by_model.setdefault(model, _empty_counts())
                for key, value in counts.items():
                    bucket[key] += value
                for block in message.get("content") or []:
                    if not isinstance(block, dict) or block.get("type") != "tool_use":
                        continue
                    name = block.get("name") or "unknown"
                    tool_calls[name] = tool_calls.get(name, 0) + 1
                    if name in READ_TOOLS:
                        inp = block.get("input") or {}
                        read_paths.append(inp.get("file_path") or inp.get("notebook_path") or "")
            elif kind == "user":
                for block in message.get("content") or []:
                    if isinstance(block, dict) and block.get("type") == "tool_result":
                        tool_result_bytes += len(norm_content(block.get("content")))

    if turns == 0:
        raise ExtractError("%s: transcript carries no assistant records" % run_id)

    totals = _empty_counts()
    for counts in by_model.values():
        for key, value in counts.items():
            totals[key] += value

    distinct = len(set(read_paths))
    reads = len(read_paths)
    return {
        "by_model": by_model,
        "tokens": {
            "input": totals["input"],
            "output": totals["output"],
            "cache_read": totals["cache_read"],
            "cache_write": totals["cache_write_5m"] + totals["cache_write_1h"],
            "total_billed": sum(totals.values()),
        },
        "cache_write_ttl_assumed": ttl_assumed,
        "turns": turns,
        "tool_calls": sum(tool_calls.values()),
        "tool_calls_by_name": dict(sorted(tool_calls.items())),
        "file_reads": reads,
        "distinct_files_read": distinct,
        "re_read_rate": (reads - distinct) / reads if reads else 0.0,
        "tool_result_bytes": tool_result_bytes,
        "reported_cost_usd": reported_cost,
        "saw_result_record": saw_result,
    }


def read_guard_events(path):
    """Count guard denials, advisories and skill loads in the magus activity trail.

    Returns None when the trail was not captured; the caller must keep that null
    rather than substituting zero, which would read as a guard that never fired.
    """
    if not os.path.exists(path):
        return None
    denials = 0
    advisories = 0
    skill_loads = 0
    with open(path, encoding="utf-8", errors="replace") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("kind") != "agent_command":
                continue
            action = (event.get("action") or "").lower()
            preview = (event.get("preview") or "").lower()
            target = (event.get("path") or event.get("target") or "").lower()
            if "guard: deny" in preview:
                denials += 1
            elif "guard: advis" in preview:
                advisories += 1
            if action.startswith("skill.") or (action == "file.read" and "/skills/" in target):
                skill_loads += 1
    return {"denials": denials, "advisories": advisories, "skill_loads": skill_loads}


def read_invariant_violations(path):
    """Find deleted test files in a unified diff. Deletion is the only deterministic tell."""
    deleted = []
    if not os.path.exists(path):
        return {"tests_deleted": deleted}
    current = None
    with open(path, encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if line.startswith("diff --git "):
                parts = line.split()
                current = parts[2][2:] if len(parts) >= 4 else None
            elif line.startswith("deleted file mode") and current:
                name = posixpath.basename(current)
                if any(marker in name for marker in TEST_FILE_MARKERS):
                    deleted.append(current)
    return {"tests_deleted": sorted(deleted)}


def read_check(run_dir):
    """Read check.exit as the run's outcome. Absent means unknown, not failed."""
    path = os.path.join(run_dir, "check.exit")
    if not os.path.exists(path):
        return None, None
    with open(path, encoding="utf-8", errors="replace") as fh:
        raw = fh.read().strip()
    if raw == "":
        return None, None
    try:
        code = int(raw.split()[0])
    except ValueError:
        return None, None
    return code, code == 0


def read_timing(run_dir):
    path = os.path.join(run_dir, "timing.json")
    if not os.path.exists(path):
        return {"wall_ms": None, "time_to_first_edit_ms": None, "time_to_done_ms": None}
    with open(path, encoding="utf-8") as fh:
        timing = json.load(fh)
    return {
        "wall_ms": timing.get("wall_ms"),
        "time_to_first_edit_ms": timing.get("time_to_first_edit_ms"),
        "time_to_done_ms": timing.get("time_to_done_ms"),
    }


META_FIELDS = (
    "run_id",
    "arm",
    "task",
    "rep",
    "model",
    "effort",
    "max_turns",
    "budget_usd",
    "magus_binary",
    "magus_version",
    "fixture_sha",
    "started",
    "ended",
    "exit_reason",
    "control",
)

REQUIRED_META = ("run_id", "arm", "task", "rep", "model")


def extract_run(run_dir, pricing):
    """Build one metrics record for a single run directory.

    Returns None for a control run (golden or null): no agent ran, so there is
    no transcript to measure, and its verdict is already in meta.json's
    exit_reason. A scored run without a transcript is still an error.
    """
    meta_path = os.path.join(run_dir, "meta.json")
    if not os.path.exists(meta_path):
        raise ExtractError("%s: meta.json is missing" % run_dir)
    with open(meta_path, encoding="utf-8") as fh:
        meta = json.load(fh)
    for field in REQUIRED_META:
        if meta.get(field) in (None, ""):
            raise ExtractError("%s: meta.json has no %s" % (run_dir, field))
    run_id = meta["run_id"]
    if meta.get("control"):
        return None

    transcript = read_transcript(os.path.join(run_dir, "transcript.jsonl"), run_id, meta.get("model"))
    check_exit, success = read_check(run_dir)

    record = {field: meta.get(field) for field in META_FIELDS}
    record.update(
        {
            "tokens": transcript["tokens"],
            "dollars": price_usage(transcript["by_model"], pricing, run_id),
            "reported_cost_usd": transcript["reported_cost_usd"],
            "cache_write_ttl_assumed": transcript["cache_write_ttl_assumed"],
            "turns": transcript["turns"],
            "tool_calls": transcript["tool_calls"],
            "tool_calls_by_name": transcript["tool_calls_by_name"],
            "file_reads": transcript["file_reads"],
            "distinct_files_read": transcript["distinct_files_read"],
            "re_read_rate": transcript["re_read_rate"],
            "tool_result_bytes": transcript["tool_result_bytes"],
            "guard_events": read_guard_events(os.path.join(run_dir, "activity", "events.jsonl")),
            "check_exit": check_exit,
            "success": success,
            "invariant_violations": read_invariant_violations(os.path.join(run_dir, "final.diff")),
        }
    )
    record.update(read_timing(run_dir))
    return record


def summary_line(record):
    """One line per run, for the operator watching extraction."""
    if record["success"] is True:
        outcome = "PASS"
    elif record["success"] is False:
        outcome = "FAIL"
    else:
        outcome = "NOCHECK"
    guard = record["guard_events"]
    guard_text = "guard=none" if guard is None else "guard=%d/%d/%d" % (
        guard["denials"],
        guard["advisories"],
        guard["skill_loads"],
    )
    return "%-34s %-8s %-10s %-7s tok=%-9d $%.4f turns=%-3d tools=%-3d %s" % (
        record["run_id"],
        record["arm"],
        record["task"],
        outcome,
        record["tokens"]["total_billed"],
        record["dollars"],
        record["turns"],
        record["tool_calls"],
        guard_text,
    )


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("results", help="directory holding one subdirectory per run")
    parser.add_argument("-o", "--out", required=True, help="metrics.jsonl to write")
    parser.add_argument(
        "-p",
        "--pricing",
        default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "pricing.json"),
        help="pricing table (default: the one shipped beside this script)",
    )
    args = parser.parse_args(argv)

    pricing = load_pricing(args.pricing)
    run_dirs = sorted(
        os.path.join(args.results, name)
        for name in os.listdir(args.results)
        if os.path.isdir(os.path.join(args.results, name))
    )
    if not run_dirs:
        raise ExtractError("no run directories under %s" % args.results)

    records = []
    controls = 0
    for run_dir in run_dirs:
        record = extract_run(run_dir, pricing)
        if record is None:
            controls += 1
            print("skipped %s: control run, nothing to measure" % os.path.basename(run_dir))
        else:
            records.append(record)
    if not records:
        raise ExtractError("no scored runs under %s (%d control runs)" % (args.results, controls))
    records.sort(key=lambda r: r["run_id"])
    with open(args.out, "w", encoding="utf-8") as fh:
        for record in records:
            fh.write(json.dumps(record, sort_keys=True) + "\n")
            print(summary_line(record))
    print("wrote %d runs to %s (%d control runs skipped)" % (len(records), args.out, controls))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ExtractError as err:
        print("extract: %s" % err, file=sys.stderr)
        sys.exit(2)
