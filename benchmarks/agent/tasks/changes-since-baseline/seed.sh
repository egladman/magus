#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
tl_seed "${1:?usage: seed.sh <worktree>}" "changes-since-baseline"
