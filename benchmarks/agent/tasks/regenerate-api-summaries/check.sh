#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="$(cd "${1:?usage: check.sh <worktree>}" && pwd)"

# The drift check alone would pass vacuously if the .mjs sources were deleted, so
# pin the population and the behavior alongside it.
count="$(find "$WT/packages" -name api.md -not -path '*/node_modules/*' | wc -l | tr -d ' ')"
if [[ "$count" != "14" ]]; then
    echo "check: expected 14 api summaries, found $count" >&2
    exit 1
fi

node "$WT/tools/gen-api.mjs" --check
tl_platform_tests "$WT"
