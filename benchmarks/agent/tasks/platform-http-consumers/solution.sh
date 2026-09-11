#!/usr/bin/env bash
set -euo pipefail
WT="${1:?usage: solution.sh <worktree>}"

cat > "$WT/ANSWER.md" <<'ANSWER'
## Answer

- packages/crew/important-feature-3
- packages/navigation/important-feature-7
- packages/ticket-booking/important-feature-12
ANSWER
