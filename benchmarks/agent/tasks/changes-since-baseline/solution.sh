#!/usr/bin/env bash
set -euo pipefail
WT="${1:?usage: solution.sh <worktree>}"

cat > "$WT/ANSWER.md" <<'ANSWER'
## Answer

- apps/crew
- packages/platform/http
- packages/platform/metrics
- packages/warp-drive-manager/important-feature-2

## Behavior change

The default request timeoutMs in packages/platform/http went from 1000 to 2500.
ANSWER
