#!/usr/bin/env bash
set -euo pipefail
WT="${1:?usage: solution.sh <worktree>}"

node "$WT/tools/gen-api.mjs" --write
