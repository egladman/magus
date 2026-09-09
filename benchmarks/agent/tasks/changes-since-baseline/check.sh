#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="${1:?usage: check.sh <worktree>}"

tl_grade_answer "$WT/ANSWER.md" '## Answer' \
    apps/crew \
    packages/platform/http \
    packages/platform/metrics \
    packages/warp-drive-manager/important-feature-2

# shellcheck disable=SC2016  # the node program is a literal
node -e '
  const fs = require("fs");
  const answer = process.argv[1];
  const lines = fs.readFileSync(answer, "utf8").split("\n");
  const start = lines.findIndex((l) => l.trim().toLowerCase() === "## behavior change");
  if (start < 0) {
    console.error("check: no \"## Behavior change\" section");
    process.exit(1);
  }
  const body = lines.slice(start + 1).join(" ");
  const cut = body.indexOf("\n#") >= 0 ? body.slice(0, body.indexOf("\n#")) : body;
  const missing = ["timeoutMs", "2500"].filter((token) => !cut.includes(token));
  if (missing.length) {
    console.error(`check: behavior change does not name ${missing.join(" and ")}`);
    process.exit(1);
  }
' "$WT/ANSWER.md"
