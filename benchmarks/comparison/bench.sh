#!/usr/bin/env bash
# Compares wall-clock build times: make (serial, no content-addressed cache)
# vs magus run build (parallel, content-addressed cache).
set -euo pipefail

# Locate magus: prefer PATH, fall back to the Docker run incantation.
if ! command -v magus >/dev/null 2>&1; then
  echo "magus not found on PATH."
  echo "Run with Docker instead:"
  echo "  docker run --rm -v \"\$PWD\":/workspace ghcr.io/egladman/tack/magus:latest run build"
  exit 1
fi

section() { printf '\n\033[1;34m=== %s ===\033[0m\n' "$*"; }

elapsed_ms() {
  local start=$1 end
  end=$(date +%s%3N)
  echo $(( end - start ))
}

# Sanity-check: make must be available for the comparison to run.
command -v make >/dev/null 2>&1 || { echo "make not found; skipping make comparison"; }

# Clean slate.
rm -rf out .magus

section "1. make (cold, serial — always recompiles)"
start=$(date +%s%3N)
make -j1 all
printf '\033[1;32m→ %dms\033[0m\n' "$(elapsed_ms "$start")"

section "2. make (warm, serial — no content-addressed cache, still recompiles)"
start=$(date +%s%3N)
make -j1 all
printf '\033[1;32m→ %dms\033[0m\n' "$(elapsed_ms "$start")"

section "3. magus run build (cold, concurrency=8 — builds in parallel, populates cache)"
rm -rf .magus out
start=$(date +%s%3N)
magus --concurrency 8 run build
printf '\033[1;32m→ %dms\033[0m\n' "$(elapsed_ms "$start")"

section "4. magus run build (warm, concurrency=8 — pure cache replay, no compiler)"
start=$(date +%s%3N)
magus --concurrency 8 run build
printf '\033[1;32m→ %dms\033[0m\n' "$(elapsed_ms "$start")"

section "5. magus run build (one file changed — only affected service rebuilds)"
echo "// changed" >> svc-3/main.go
start=$(date +%s%3N)
magus --concurrency 8 run build
printf '\033[1;32m→ %dms\033[0m\n' "$(elapsed_ms "$start")"
# Restore the file.
git checkout svc-3/main.go 2>/dev/null || sed -i '/\/\/ changed/d' svc-3/main.go

printf '\n'
