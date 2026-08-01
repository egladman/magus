#!/usr/bin/env python3
"""Report compression stats for the embedded agent skills.

Installs BOTH permutations with the real renderer into temporary directories and
measures the flat files that actually ship.

Going through `magus agent install` rather than re-implementing applyVariant here
is deliberate: this script cannot drift from the Go renderer, and it keeps
working unchanged when the body syntax changes.

Run from the repo root.

Usage: python3 evals/tools/measure_skills.py [--json]
"""

import argparse
import glob
import json
import os
import shlex
import subprocess
import sys
import tempfile

MAGUS = os.environ.get("MAGUS_BIN", "go run ./cmd/magus")


def install(dest, simple):
    """Render one permutation into dest with the real renderer."""
    cmd = shlex.split(MAGUS) + ["agent", "install", "--dir", dest, "--force", ".claude/skills"]
    if simple:
        cmd.append("--simple")
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit("install failed: {}\n{}".format(" ".join(cmd), proc.stderr))
    return {
        os.path.basename(os.path.dirname(path)): open(path, encoding="utf-8").read()
        for path in sorted(glob.glob(os.path.join(dest, ".claude", "skills", "*", "SKILL.md")))
    }


def compose(text):
    """Byte counts by line category, for the prose vs hard-content split."""
    counts = {"prose": 0, "table": 0, "code": 0, "heading": 0, "blank": 0}
    in_fence = False
    for line in text.split("\n"):
        n = len(line) + 1
        stripped = line.strip()
        if stripped.startswith("```"):
            in_fence = not in_fence
            counts["code"] += n
        elif in_fence:
            counts["code"] += n
        elif stripped.startswith("|"):
            counts["table"] += n
        elif stripped.startswith("#"):
            counts["heading"] += n
        elif not stripped:
            counts["blank"] += n
        else:
            counts["prose"] += n
    return counts


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()

    with tempfile.TemporaryDirectory() as tmp:
        full_bodies = install(os.path.join(tmp, "full"), simple=False)
        simple_bodies = install(os.path.join(tmp, "simple"), simple=True)

    if not full_bodies:
        print("no skills installed; run from the repo root", file=sys.stderr)
        return 1

    sources = {
        os.path.basename(os.path.dirname(path)): open(path, encoding="utf-8").read()
        for path in sorted(glob.glob("cmd/magus/skills/*/SKILL.md"))
    }

    rows = []
    for name, full in sorted(full_bodies.items()):
        simple = simple_bodies.get(name, "")
        body = sources.get(name, "")
        comp = compose(simple)
        hard = comp["table"] + comp["code"]
        total = sum(comp.values()) or 1
        rows.append({
            "skill": name,
            "full_bytes": len(full),
            "simple_bytes": len(simple),
            "cut_pct": round(100 - 100 * len(simple) / len(full), 1) if full else 0.0,
            "full_only_branches": body.count("{{if .Full}}"),
            "simple_only_branches": body.count("{{if .Simple}}"),
            "two_wordings": body.count("{{else}}"),
            "simple_prose_pct": round(100 * comp["prose"] / total, 1),
            "simple_hard_pct": round(100 * hard / total, 1),
        })

    tot_full = sum(row["full_bytes"] for row in rows)
    tot_simple = sum(row["simple_bytes"] for row in rows)
    summary = {
        "skills": rows,
        "total_full_bytes": tot_full,
        "total_simple_bytes": tot_simple,
        "total_cut_pct": round(100 - 100 * tot_simple / tot_full, 1),
        "total_full_only_branches": sum(row["full_only_branches"] for row in rows),
        "total_simple_only_branches": sum(row["simple_only_branches"] for row in rows),
        "total_two_wordings": sum(row["two_wordings"] for row in rows),
    }

    if args.json:
        print(json.dumps(summary, indent=2))
        return 0

    print("{:<22} {:>7} {:>7} {:>6} {:>6} {:>6} {:>8}".format(
        "skill", "full", "simple", "cut", "ifFull", "else", "prose%"))
    for row in rows:
        print("{:<22} {:>7} {:>7} {:>5}% {:>6} {:>6} {:>7}%".format(
            row["skill"], row["full_bytes"], row["simple_bytes"],
            row["cut_pct"], row["full_only_branches"], row["two_wordings"],
            row["simple_prose_pct"]))
    print("{:<22} {:>7} {:>7} {:>5}% {:>6} {:>6}".format(
        "TOTAL", summary["total_full_bytes"], summary["total_simple_bytes"],
        summary["total_cut_pct"], summary["total_full_only_branches"],
        summary["total_two_wordings"]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
