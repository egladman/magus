#!/usr/bin/env bash
# bench.sh — large-monorepo benchmark driver (magus vs turbo, nx, lage).
#
# Runs against the workspace materialized by ./setup.sh (work/repo). Emits one
# hyperfine JSON per (tool, scenario) under results/, using the same filename
# convention the shared aggregator (../aggregate) consumes, then regenerates
# BENCHMARKS-large-monorepo.md.
#
# Usage: ./bench.sh [tool ...]
#   tool : subset of  magus-luajit magus-gopherlua turbo nx lage
#          (default: all of them)
#
# Scenarios (see README.md for the mapping rationale):
#   S4  cold build      empty cache + no .next, build everything
#   S5  warm replay      build again with a fully-populated cache
#   S6  one leaf changed  edit an app page, rebuild (only that app is affected)
#   S7  one lib changed   edit a feature lib, rebuild (its app is affected)
#
# Env:
#   MAGUS_BIN=magus            magus binary (CGO build for magus-luajit)
#   BENCH_CONCURRENCY=10       per-tool parallelism
#   BENCH_RUNS=3               hyperfine measured runs for S4/S5
#   BENCH_INCR_RUNS=1          hyperfine measured runs for S6/S7
#   BENCH_SCENARIOS="S4 S5 S6 S7"   which scenarios to run
#   BENCH_DRY_RUN=1            print hyperfine commands, do not execute
#   BENCH_SKIP_VERSION_CHECK=1 skip versions.lock comparison
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$DIR/gen/repo"
RESULTS="$DIR/results"
AGG="$DIR/../aggregate"
VLOCK="$DIR/versions.lock"

MAGUS="${MAGUS_BIN:-magus}"
C="${BENCH_CONCURRENCY:-10}"
RUNS="${BENCH_RUNS:-3}"
INCR_RUNS="${BENCH_INCR_RUNS:-1}"
DRY="${BENCH_DRY_RUN:-0}"
SKIP_VER="${BENCH_SKIP_VERSION_CHECK:-0}"
SCENARIOS=(${BENCH_SCENARIOS:-S4 S5 S6 S7})

# fixture token must be a single word (the aggregator splits filenames on "-").
FIXTURE="largemono"
LEAF="apps/crew/pages/index.tsx"
UPSTREAM_LIB="packages/crew/important-feature-0/src/index.ts"

red()     { printf '\033[1;31m%s\033[0m\n' "$*"; }
green()   { printf '\033[1;32m%s\033[0m\n' "$*"; }
yellow()  { printf '\033[1;33m%s\033[0m\n' "$*"; }
section() { printf '\n\033[1;34m=== %s ===\033[0m\n' "$*"; }
die()     { red "error: $*" >&2; exit 1; }

TOOLS=("$@")
[[ "${#TOOLS[@]}" -eq 0 ]] && TOOLS=(magus-luajit magus-gopherlua turbo nx lage)

# ── preflight ───────────────────────────────────────────────────────────────
[[ -d "$REPO/node_modules" ]] || die "workspace not set up — run ./setup.sh first"
command -v hyperfine >/dev/null 2>&1 || die "hyperfine not found (sudo apt install hyperfine)"
BIN="$REPO/node_modules/.bin"

check_versions() {
    [[ "$SKIP_VER" == "1" || ! -f "$VLOCK" ]] && return 0
    local key want got
    while IFS='=' read -r key want; do
        [[ -z "$key" || "${key:0:1}" == "#" ]] && continue
        case "$key" in
            node)  got=$(node --version 2>/dev/null | tr -d 'v' || true) ;;
            npm)   got=$(npm --version 2>/dev/null || true) ;;
            turbo) got=$("$BIN/turbo" --version 2>/dev/null | grep -Eo '[0-9.]+' | head -1 || true) ;;
            nx)    got=$("$BIN/nx" --version 2>/dev/null | grep -Eo '[0-9.]+' | head -1 || true) ;;
            lage)  got=$("$BIN/lage" --version 2>/dev/null | grep -Eo '[0-9.]+' | head -1 || true) ;;
            hyperfine) got=$(hyperfine --version 2>/dev/null | grep -Eo '[0-9.]+' | head -1 || true) ;;
            *) continue ;;
        esac
        [[ -n "$got" && "$got" != "$want" ]] && yellow "warn: $key version is $got, versions.lock pins $want"
    done < "$VLOCK"
}

# ── per-tool command + cache reset ──────────────────────────────────────────
# build_cmd <tool> : the build invocation (run with CWD=work/repo)
build_cmd() {
    local tool="$1" fam="${tool%%-*}" eng=""
    [[ "$fam" == "magus" && "$tool" != "magus" ]] && eng="MAGUS_INTERPRETER_LUA_ENGINE=${tool#magus-} "
    case "$fam" in
        magus) echo "${eng}${MAGUS} run build --concurrency=$C" ;;
        turbo) echo "$BIN/turbo run build --concurrency=$C --no-daemon" ;;
        nx)    echo "NX_DAEMON=false $BIN/nx run-many -t build --parallel=$C" ;;
        lage)  echo "$BIN/lage build --concurrency $C" ;;
        *) die "unknown tool '$tool'" ;;
    esac
}

# clear_cmd <tool> : reset that tool's cache AND all .next so a cold build is
# genuinely cold for every tool (next build is incremental via .next/cache).
clear_cmd() {
    local fam="${1%%-*}" nexts='apps/*/.next'
    case "$fam" in
        magus) echo "rm -rf .magus $nexts" ;;
        turbo) echo "rm -rf .turbo node_modules/.cache/turbo $nexts" ;;
        nx)    echo "rm -rf .nx/cache $nexts" ;;
        lage)  echo "rm -rf node_modules/.cache/lage $nexts" ;;
    esac
}

hf() { # hf <outfile> <warmup> <runs> <prepare|''> <cmd>
    local out="$1" warmup="$2" runs="$3" prep="$4" cmd="$5"
    mkdir -p "$(dirname "$out")"
    if [[ "$DRY" == "1" ]]; then
        echo "[dry-run] hyperfine --warmup $warmup --runs $runs${prep:+ --prepare '$prep'} '$cmd' -> ${out##*/}"
        return
    fi
    local args=(--warmup "$warmup" --runs "$runs" --ignore-failure --export-json "$out")
    [[ -n "$prep" ]] && args+=(--prepare "$prep")
    ( cd "$REPO" && hyperfine "${args[@]}" "$cmd" )
}

reset_tree() { ( cd "$REPO" && git checkout -q -- . 2>/dev/null || true ); }

# warm <clear> <cmd> : populate a fresh cache (cold clear, then one build) so a
# subsequent timed run measures the warm/incremental path. Skipped in dry-run.
warm() {
    [[ "$DRY" == "1" ]] && { echo "[dry-run] warm: ($1) && ($2)"; return; }
    ( cd "$REPO" && eval "$1" >/dev/null 2>&1; eval "$2" >/dev/null 2>&1 || true )
}

run_tool() {
    local tool="$1" cmd clear out
    cmd="$(build_cmd "$tool")"; clear="$(clear_cmd "$tool")"
    section "$tool"
    for s in "${SCENARIOS[@]}"; do
        out="$RESULTS/${FIXTURE}-0-${tool}-daemonless-${s}.json"
        case "$s" in
            S4) # cold: clear cache + .next before every run
                echo "S4 cold build"
                hf "$out" 0 "$RUNS" "$clear" "$cmd" ;;
            S5) # warm: populate once, then measure with everything cached
                echo "S5 warm replay"
                warm "$clear" "$cmd"
                hf "$out" 1 "$RUNS" "" "$cmd" ;;
            S6|S7) # incremental: warm, then edit one file and rebuild
                local file; [[ "$s" == "S6" ]] && file="$LEAF" || file="$UPSTREAM_LIB"
                echo "$s incremental ($file)"
                warm "$clear" "$cmd"
                # --prepare appends a unique line so the input (and cache key) changes each run
                local prep="printf '\\n// bench-%s\\n' \$(date +%s%N) >> '$file'"
                hf "$out" 0 "$INCR_RUNS" "$prep" "$cmd"
                reset_tree ;;
        esac
    done
}

# ── main ────────────────────────────────────────────────────────────────────
check_versions
section "Tool versions"
"$MAGUS" version 2>/dev/null | head -1 || true
for t in turbo nx lage; do printf '%-7s ' "$t"; "$BIN/$t" --version 2>/dev/null | head -1 || echo "?"; done

reset_tree
for tool in "${TOOLS[@]}"; do run_tool "$tool"; done
reset_tree

section "Aggregating"
if [[ "$DRY" == "1" ]]; then green "dry run — no results to aggregate"; exit 0; fi
if out=$(cd "$AGG" && GOWORK=off go run . "$RESULTS" 2>/dev/null); then
    printf '%s\n' "$out" > "$DIR/BENCHMARKS-large-monorepo.md"
    green "wrote BENCHMARKS-large-monorepo.md"
else
    yellow "aggregator failed; raw JSON in $RESULTS"
fi
