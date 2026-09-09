#!/usr/bin/env bash
set -euo pipefail
WT="${1:?usage: solution.sh <worktree>}"

cat > "$WT/ANSWER.md" <<'ANSWER'
## Answer

- apps/crew
- apps/flight-simulator
- apps/navigation
- apps/ticket-booking
ANSWER
