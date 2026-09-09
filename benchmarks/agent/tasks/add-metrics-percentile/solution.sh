#!/usr/bin/env bash
set -euo pipefail
WT="${1:?usage: solution.sh <worktree>}"

cat >> "$WT/packages/platform/metrics/index.mjs" <<'PERCENTILE'

// Nearest-rank percentile over a copy of samples; 0 when there are none.
export function percentile(samples, p) {
  if (samples.length === 0) return 0;
  const sorted = [...samples].sort((a, b) => a - b);
  const rank = Math.max(0, Math.ceil((p / 100) * sorted.length) - 1);
  return sorted[rank];
}
PERCENTILE

node "$WT/tools/gen-api.mjs" --write
