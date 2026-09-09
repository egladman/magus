#!/usr/bin/env bash
# Runner self-test arm, not a scored arm: it provisions nothing so run.sh can be
# exercised without the real rampant/full recipes. See README.md.
set -euo pipefail
printf 'selftest: worktree=%s magus-binary=%s\n' "$1" "$2"
