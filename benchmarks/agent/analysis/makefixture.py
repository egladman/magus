#!/usr/bin/env python3
"""Write a synthetic results tree: two arms, two tasks, three reps.

The numbers are fixed constants so the tests can assert exact token, dollar and
byte totals by hand rather than by re-running the generator's arithmetic. It
carries the shapes the extractor has to survive: a failing run, a run with no
activity trail, a run whose cache writes report a 5m/1h split, and a diff that
deletes a test file.
"""

import argparse
import json
import os

MODEL = "claude-opus-5"

ARMS = {
    "full": {"turns": 4, "input": 1000, "output": 200, "cache_read": 8000, "cache_write": 1200,
             "result_bytes": 500, "distinct_reads": 4, "wall_ms": 120000},
    "rampant": {"turns": 7, "input": 1500, "output": 300, "cache_read": 12000, "cache_write": 1500,
                "result_bytes": 900, "distinct_reads": 3, "wall_ms": 240000},
}

TASKS = ("task-a", "task-b")
REPS = (1, 2, 3)

# The one run that reports its cache-write TTL split instead of the flat counter.
SPLIT_TTL_RUN = ("full", "task-b", 1)
# The one run whose activity trail was never captured.
NO_TRAIL_RUN = ("full", "task-a", 3)
# The one failing run.
FAILING_RUN = ("rampant", "task-b", 2)
# The one run whose diff deletes a test file.
TEST_DELETING_RUN = ("rampant", "task-a", 1)

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


def run_id(arm, task, rep):
    return "%s-%s-r%d-20260909T120000Z" % (arm, task, rep)


def transcript_lines(arm, task, rep):
    """Build a stream-json transcript with per-turn usage, tool calls and results."""
    spec = ARMS[arm]
    per_input = spec["input"] + 100 * (rep - 1)
    lines = [
        {"type": "system", "subtype": "init", "session_id": run_id(arm, task, rep), "model": MODEL}
    ]
    for turn in range(1, spec["turns"] + 1):
        usage = {
            "input_tokens": per_input,
            "output_tokens": spec["output"],
            "cache_read_input_tokens": spec["cache_read"],
        }
        if (arm, task, rep) == SPLIT_TTL_RUN:
            usage["cache_creation_input_tokens"] = spec["cache_write"]
            usage["cache_creation"] = {
                "ephemeral_5m_input_tokens": spec["cache_write"] * 2 // 3,
                "ephemeral_1h_input_tokens": spec["cache_write"] // 3,
            }
        else:
            usage["cache_creation_input_tokens"] = spec["cache_write"]
        read_path = "src/mod%d.ts" % (turn % spec["distinct_reads"])
        lines.append(
            {
                "type": "assistant",
                "message": {
                    "id": "msg_%s_%d" % (run_id(arm, task, rep), turn),
                    "role": "assistant",
                    "model": MODEL,
                    "usage": usage,
                    "content": [
                        {"type": "text", "text": "turn %d" % turn},
                        {
                            "type": "tool_use",
                            "id": "tu_%d_a" % turn,
                            "name": "Bash",
                            "input": {"command": "magus run test ."},
                        },
                        {
                            "type": "tool_use",
                            "id": "tu_%d_b" % turn,
                            "name": "Read",
                            "input": {"file_path": read_path},
                        },
                    ],
                },
            }
        )
        lines.append(
            {
                "type": "user",
                "message": {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": "tu_%d_a" % turn,
                            "content": "o" * spec["result_bytes"],
                        },
                        {
                            "type": "tool_result",
                            "tool_use_id": "tu_%d_b" % turn,
                            "content": [{"type": "text", "text": "r" * spec["result_bytes"]}],
                        },
                    ],
                },
            }
        )
    lines.append(
        {
            "type": "result",
            "subtype": "success",
            "duration_ms": ARMS[arm]["wall_ms"] + 1000 * rep,
            "num_turns": spec["turns"],
            "total_cost_usd": 0.0,
            # The host's own total, which extract prefers for output: a real record's
            # per-turn output counts one streamed chunk each.
            "usage": {
                "input_tokens": per_input * spec["turns"],
                "output_tokens": spec["output"] * spec["turns"],
                "cache_read_input_tokens": spec["cache_read"] * spec["turns"],
            },
        }
    )
    return lines


def trail_lines(arm):
    """Activity trail events; only the full arm has a guard to fire."""
    events = [
        {"kind": "agent_command", "action": "shell.command", "preview": "magus run build ."},
        {"kind": "agent_command", "action": "file.read", "path": "src/app.ts", "preview": "read"},
    ]
    if arm == "full":
        events.extend(
            [
                {"kind": "agent_command", "action": "shell.command",
                 "preview": "guard: deny go test ./..."},
                {"kind": "agent_command", "action": "shell.command",
                 "preview": "guard: deny go build ./cmd/magus"},
                {"kind": "agent_command", "action": "shell.command",
                 "preview": "guard: advisory prefer magus query over grep"},
                {"kind": "agent_command", "action": "file.read",
                 "path": ".claude/skills/magus-run/SKILL.md", "preview": "read"},
                {"kind": "agent_command", "action": "skill.load", "preview": "magus-query"},
            ]
        )
    return events


def write_run(results_dir, arm, task, rep):
    spec = ARMS[arm]
    rid = run_id(arm, task, rep)
    run_dir = os.path.join(results_dir, rid)
    os.makedirs(run_dir, exist_ok=True)

    meta = {
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
    }
    with open(os.path.join(run_dir, "meta.json"), "w", encoding="utf-8") as fh:
        json.dump(meta, fh, indent=2, sort_keys=True)
        fh.write("\n")

    with open(os.path.join(run_dir, "transcript.jsonl"), "w", encoding="utf-8") as fh:
        for record in transcript_lines(arm, task, rep):
            fh.write(json.dumps(record, sort_keys=True) + "\n")

    diff = DIFF
    if (arm, task, rep) == TEST_DELETING_RUN:
        diff = DIFF + DELETED_TEST_DIFF
    with open(os.path.join(run_dir, "final.diff"), "w", encoding="utf-8") as fh:
        fh.write(diff)

    failed = (arm, task, rep) == FAILING_RUN
    with open(os.path.join(run_dir, "check.exit"), "w", encoding="utf-8") as fh:
        fh.write("1\n" if failed else "0\n")
    with open(os.path.join(run_dir, "check.txt"), "w", encoding="utf-8") as fh:
        fh.write("FAIL 1 of 3 assertions\n" if failed else "OK 3 of 3 assertions\n")

    with open(os.path.join(run_dir, "timing.json"), "w", encoding="utf-8") as fh:
        json.dump(
            {
                "wall_ms": spec["wall_ms"] + 1000 * rep,
                "time_to_first_edit_ms": spec["wall_ms"] // 4 + 500 * rep,
                "time_to_done_ms": spec["wall_ms"] - 2000 + 1000 * rep,
            },
            fh,
            indent=2,
            sort_keys=True,
        )
        fh.write("\n")

    with open(os.path.join(run_dir, "probe.txt"), "w", encoding="utf-8") as fh:
        fh.write("arm=%s skills=%s hooks=%s mcp=%s\n" % (
            arm,
            "28" if arm == "full" else "0",
            "3" if arm == "full" else "0",
            "on" if arm == "full" else "off",
        ))

    if (arm, task, rep) != NO_TRAIL_RUN:
        activity = os.path.join(run_dir, "activity")
        os.makedirs(activity, exist_ok=True)
        with open(os.path.join(activity, "events.jsonl"), "w", encoding="utf-8") as fh:
            for event in trail_lines(arm):
                fh.write(json.dumps(event, sort_keys=True) + "\n")
    return run_dir


def build(root):
    """Materialize the fixture under <root>/results and return that directory."""
    results_dir = os.path.join(root, "results")
    os.makedirs(results_dir, exist_ok=True)
    for arm in sorted(ARMS):
        for task in TASKS:
            for rep in REPS:
                write_run(results_dir, arm, task, rep)
    return results_dir


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("root", help="directory to create results/ under")
    args = parser.parse_args(argv)
    print("wrote %s" % build(args.root))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
