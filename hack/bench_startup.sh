#!/usr/bin/env bash
# Spawn-based ground-truth measurement for magus cold-start latency.
#
# The in-process benchmarks in cmd/magus/startup_bench_test.go cannot see
# linker/runtime init costs (Go runtime stack, GC bootstrap, package init()
# functions) because those fire once per `go test` binary, not per iteration.
# This script builds a release-mode magus binary and times real cold starts
# against a fresh temp workspace.
#
# Use hyperfine if available (it gives min/median/p99); otherwise fall back
# to a date-based loop. Output is printed to stdout in a benchstat-friendly
# form so the numbers can be pasted into PR descriptions.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR"' EXIT

CGO_ENABLED="${CGO_ENABLED:-0}"
echo "building magus into $BIN_DIR (CGO_ENABLED=$CGO_ENABLED)..." >&2
(cd "$REPO_ROOT" && CGO_ENABLED="$CGO_ENABLED" go build -o "$BIN_DIR/magus" ./cmd/magus)

# Synthetic workspace so FindRoot succeeds without us depending on the
# checkout's own magusfiles.
WS="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR" "$WS"' EXIT
printf 'module bench\n' > "$WS/go.mod"
: > "$WS/magusfile.tl"

cd "$WS"
export MAGUS_DAEMON_SOCKET=""
unset MAGUS_LOG_LEVEL

CASES=(help version "completion bash" ls)

if command -v hyperfine >/dev/null 2>&1; then
  for c in "${CASES[@]}"; do
    printf '\n=== %s ===\n' "magus $c"
    # shellcheck disable=SC2086
    hyperfine --warmup 3 --runs 50 --shell=none "$BIN_DIR/magus $c"
  done
else
  echo "hyperfine not found; using bash time loop (less accurate)" >&2
  for c in "${CASES[@]}"; do
    total=0
    runs=20
    for _ in $(seq 1 $runs); do
      start=$(date +%s%N)
      # shellcheck disable=SC2086
      "$BIN_DIR/magus" $c >/dev/null 2>&1 || true
      end=$(date +%s%N)
      total=$(( total + (end - start) ))
    done
    avg=$(( total / runs ))
    printf 'magus %-20s avg %d ns (n=%d)\n' "$c" "$avg" "$runs"
  done
fi
