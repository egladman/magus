#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="${1:?usage: check.sh <worktree>}"

tl_grade_answer "$WT/ANSWER.md" '## Answer' \
    apps/crew \
    apps/flight-simulator \
    apps/navigation \
    apps/ticket-booking
