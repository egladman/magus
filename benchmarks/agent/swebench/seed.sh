#!/usr/bin/env sh
# seed.sh <worktree> <instance-id>: put a SWE-bench checkout into the shape an
# arm can provision. Runs INSIDE the trial container, where the worktree is
# /testbed and this checkout is mounted read-only.
#
# Everything provisioning adds is listed in .git/info/exclude rather than
# committed: the agent then starts from a clean `git status`, and the diff the
# grader applies carries only what the agent changed.
set -eu

WT=$1
ID=$2
HERE=$(cd "$(dirname "$0")" && pwd)

[ -d "$WT/.git" ] || { printf 'seed.sh: %s is not a git checkout\n' "$WT" >&2; exit 1; }

git config --global --add safe.directory "$WT"
printf 'telemetry:\n  enabled: false\n' >"$WT/magus.yaml"
sed "s/@@INSTANCE_ID@@/$ID/" "$HERE/magusfile.buzz.tmpl" >"$WT/magusfile.buzz"

cat >>"$WT/.git/info/exclude" <<'EXCLUDE'
/magus.yaml
/magusfile.buzz
/.magus/
/.benchmark/
/.claude/
/CLAUDE.md
/MAGUS.md
EXCLUDE

printf 'seed: %s is instance %s\n' "$WT" "$ID"
