#!/usr/bin/env python3
"""Write a synthetic results tree: two arms, two tasks, three reps.

The numbers are fixed constants so the tests can assert exact token, dollar and
byte totals by hand rather than by re-running the generator's arithmetic. It
carries the shapes the extractor has to survive: a failing run, a run with no
activity trail, a run whose cache writes report a 5m/1h split, and a diff that
deletes a test file.
"""

from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

MODEL = "claude-opus-5"


@dataclass(frozen=True)
class ArmSpec:
    turns: int
    input: int
    output: int
    cache_read: int
    cache_write: int
    result_bytes: int
    distinct_reads: int
    wall_ms: int


ARMS = {
    "full": ArmSpec(
        turns=4,
        input=1000,
        output=200,
        cache_read=8000,
        cache_write=1200,
        result_bytes=500,
        distinct_reads=4,
        wall_ms=120000,
    ),
    "rampant": ArmSpec(
        turns=7,
        input=1500,
        output=300,
        cache_read=12000,
        cache_write=1500,
        result_bytes=900,
        distinct_reads=3,
        wall_ms=240000,
    ),
}

TASKS = ("task-a", "task-b")
REPS = (1, 2, 3)

Run = tuple[str, str, int]

# The one run that reports its cache-write TTL split instead of the flat counter.
SPLIT_TTL_RUN: Run = ("full", "task-b", 1)
# The one run whose activity trail was never captured.
NO_TRAIL_RUN: Run = ("full", "task-a", 3)
# The one failing run.
FAILING_RUN: Run = ("rampant", "task-b", 2)
# The one run whose diff deletes a test file.
TEST_DELETING_RUN: Run = ("rampant", "task-a", 1)

DIFF = """diff --git a/src/app.ts b/src/app.ts
index 1111111..2222222 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,3 +1,4 @@
 export function app() {
+  return 1;
 }
"""

DELETED_TEST_DIFF = """diff --git a/src/app.test.ts b/src/app.test.ts
deleted file mode 100644
index 3333333..0000000
--- a/src/app.test.ts
+++ /dev/null
@@ -1,3 +0,0 @@
-test("app", () => {
-  expect(app()).toBe(1);
-});
"""


def run_id(arm: str, task: str, rep: int) -> str:
    return f"{arm}-{task}-r{rep}-20260909T120000Z"


def _usage(spec: ArmSpec, run: Run, per_input: int) -> dict[str, Any]:
    usage: dict[str, Any] = {
        "input_tokens": per_input,
        "output_tokens": spec.output,
        "cache_read_input_tokens": spec.cache_read,
        "cache_creation_input_tokens": spec.cache_write,
    }
    if run == SPLIT_TTL_RUN:
        usage["cache_creation"] = {
            "ephemeral_5m_input_tokens": spec.cache_write * 2 // 3,
            "ephemeral_1h_input_tokens": spec.cache_write // 3,
        }
    return usage


def _assistant_turn(spec: ArmSpec, run: Run, turn: int, per_input: int) -> dict[str, Any]:
    return {
        "type": "assistant",
        "message": {
            "id": f"msg_{run_id(*run)}_{turn}",
            "role": "assistant",
            "model": MODEL,
            "usage": _usage(spec, run, per_input),
            "content": [
                {"type": "text", "text": f"turn {turn}"},
                {
                    "type": "tool_use",
                    "id": f"tu_{turn}_a",
                    "name": "Bash",
                    "input": {"command": "magus run test ."},
                },
                {
                    "type": "tool_use",
                    "id": f"tu_{turn}_b",
                    "name": "Read",
                    "input": {"file_path": f"src/mod{turn % spec.distinct_reads}.ts"},
                },
            ],
        },
    }


def _tool_results(spec: ArmSpec, turn: int) -> dict[str, Any]:
    return {
        "type": "user",
        "message": {
            "role": "user",
            "content": [
                {
                    "type": "tool_result",
                    "tool_use_id": f"tu_{turn}_a",
                    "content": "o" * spec.result_bytes,
                },
                {
                    "type": "tool_result",
                    "tool_use_id": f"tu_{turn}_b",
                    "content": [{"type": "text", "text": "r" * spec.result_bytes}],
                },
            ],
        },
    }


def transcript_lines(arm: str, task: str, rep: int) -> list[dict[str, Any]]:
    """Build a stream-json transcript with per-turn usage, tool calls and results."""
    spec = ARMS[arm]
    run: Run = (arm, task, rep)
    per_input = spec.input + 100 * (rep - 1)
    lines: list[dict[str, Any]] = [
        {"type": "system", "subtype": "init", "session_id": run_id(*run), "model": MODEL}
    ]
    for turn in range(1, spec.turns + 1):
        lines.append(_assistant_turn(spec, run, turn, per_input))
        lines.append(_tool_results(spec, turn))
    lines.append(
        {
            "type": "result",
            "subtype": "success",
            "duration_ms": spec.wall_ms + 1000 * rep,
            "num_turns": spec.turns,
            "total_cost_usd": 0.0,
            # The host's own total, which extract prefers for output: a real record's
            # per-turn output counts one streamed chunk each.
            "usage": {
                "input_tokens": per_input * spec.turns,
                "output_tokens": spec.output * spec.turns,
                "cache_read_input_tokens": spec.cache_read * spec.turns,
            },
        }
    )
    return lines


def trail_lines(arm: str) -> list[dict[str, str]]:
    """Activity trail events; only the full arm has a guard to fire."""
    events = [
        {"kind": "agent_command", "action": "shell.command", "preview": "magus run build ."},
        {"kind": "agent_command", "action": "file.read", "path": "src/app.ts", "preview": "read"},
    ]
    if arm == "full":
        events += [
            {
                "kind": "agent_command",
                "action": "shell.command",
                "preview": "guard: deny go test ./...",
            },
            {
                "kind": "agent_command",
                "action": "shell.command",
                "preview": "guard: deny go build ./cmd/magus",
            },
            {
                "kind": "agent_command",
                "action": "shell.command",
                "preview": "guard: advisory prefer magus query over grep",
            },
            {
                "kind": "agent_command",
                "action": "file.read",
                "path": ".claude/skills/magus-run/SKILL.md",
                "preview": "read",
            },
            {"kind": "agent_command", "action": "skill.load", "preview": "magus-query"},
        ]
    return events


def _write_json(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def _write_jsonl(path: Path, values: list[dict[str, Any]]) -> None:
    lines = "".join(json.dumps(value, sort_keys=True) + "\n" for value in values)
    path.write_text(lines, encoding="utf-8")


def write_run(results_dir: Path, arm: str, task: str, rep: int) -> Path:
    spec = ARMS[arm]
    run: Run = (arm, task, rep)
    rid = run_id(*run)
    run_dir = results_dir / rid
    run_dir.mkdir(parents=True, exist_ok=True)

    _write_json(
        run_dir / "meta.json",
        {
            "run_id": rid,
            "arm": arm,
            "task": task,
            "rep": rep,
            "model": MODEL,
            "effort": "high",
            "max_turns": 60,
            "budget_usd": 5.0,
            "magus_binary": "/w/bin/magus",
            "magus_version": "v0.4.2",
            "fixture_sha": "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567",
            "started": "2026-09-09T12:00:00Z",
            "ended": "2026-09-09T12:04:00Z",
            "exit_reason": "done",
        },
    )
    _write_jsonl(run_dir / "transcript.jsonl", transcript_lines(arm, task, rep))
    (run_dir / "final.diff").write_text(
        DIFF + DELETED_TEST_DIFF if run == TEST_DELETING_RUN else DIFF, encoding="utf-8"
    )

    failed = run == FAILING_RUN
    (run_dir / "check.exit").write_text("1\n" if failed else "0\n", encoding="utf-8")
    (run_dir / "check.txt").write_text(
        "FAIL 1 of 3 assertions\n" if failed else "OK 3 of 3 assertions\n", encoding="utf-8"
    )

    _write_json(
        run_dir / "timing.json",
        {
            "wall_ms": spec.wall_ms + 1000 * rep,
            "time_to_first_edit_ms": spec.wall_ms // 4 + 500 * rep,
            "time_to_done_ms": spec.wall_ms - 2000 + 1000 * rep,
        },
    )

    probe = "skills=28 hooks=3 mcp=on" if arm == "full" else "skills=0 hooks=0 mcp=off"
    (run_dir / "probe.txt").write_text(f"arm={arm} {probe}\n", encoding="utf-8")

    if run != NO_TRAIL_RUN:
        activity = run_dir / "activity"
        activity.mkdir(exist_ok=True)
        _write_jsonl(activity / "events.jsonl", trail_lines(arm))
    return run_dir


def build(root: Path) -> Path:
    """Materialize the fixture under <root>/results and return that directory."""
    results_dir = root / "results"
    results_dir.mkdir(parents=True, exist_ok=True)
    for arm in sorted(ARMS):
        for task in TASKS:
            for rep in REPS:
                write_run(results_dir, arm, task, rep)
    return results_dir


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("root", type=Path, help="directory to create results/ under")
    args = parser.parse_args(argv)
    print(f"wrote {build(args.root)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
