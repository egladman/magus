#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="${1:?usage: solution.sh <worktree>}"

tl_edit "$WT/packages/platform/config/index.mjs" \
    'if (!value) continue;' \
    'if (value === undefined) continue;'
