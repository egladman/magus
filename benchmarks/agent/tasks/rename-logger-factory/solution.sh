#!/usr/bin/env bash
set -euo pipefail
WT="${1:?usage: solution.sh <worktree>}"

files="$(grep -rl --exclude-dir=node_modules --exclude-dir=.git 'createLogger' "$WT")"
[[ -n "$files" ]] || { echo "solution: nothing to rename" >&2; exit 1; }

while IFS= read -r file; do
    # shellcheck disable=SC2016  # the node program is a literal
    node -e '
      const fs = require("fs");
      const file = process.argv[1];
      fs.writeFileSync(file, fs.readFileSync(file, "utf8").split("createLogger").join("makeLogger"));
    ' "$file"
done <<< "$files"

node "$WT/tools/gen-api.mjs" --write
