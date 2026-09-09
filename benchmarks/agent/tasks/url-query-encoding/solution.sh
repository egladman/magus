#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="${1:?usage: solution.sh <worktree>}"

# shellcheck disable=SC2016  # JS template literals, not shell expansions
tl_edit "$WT/packages/platform/http/index.mjs" \
    'const pairs = keys.map((key) => `${key}=${query[key]}`);' \
    'const pairs = keys.map((key) => `${encodeURIComponent(key)}=${encodeURIComponent(String(query[key]))}`);'
