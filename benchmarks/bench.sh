#!/usr/bin/env bash
# bench.sh — multi-tool monorepo benchmark driver
#
# Usage: ./bench.sh <fixture> [<size>] [tool ...]
#
#   fixture : go | ts | polyglot
#   size    : integer project count (default 50; ignored for polyglot)
#   tools   : subset of magus make turbo nx lage moon bazel
#             defaults to all applicable for the fixture
#
# Required : hyperfine ≥ 1.18 (https://github.com/sharkdp/hyperfine)
# Output   : results/*.json  (gitignored)
#            BENCHMARKS.md   (updated in-place)
#
# Env overrides:
#   BENCH_WARMUP=1              hyperfine warmup runs (default 1)
#   BENCH_RUNS=10               hyperfine measurement runs (default 10)
#   BENCH_SKIP_VERSION_CHECK=1  skip versions.lock comparison
#   BENCH_DRY_RUN=1             print commands without running them
#   BENCH_JOBS=8                parallelism handed to every tool (default 8)
#   MAGUS_BIN=magus             override the magus binary
#   MAKE_BIN=make               override the make binary (see below)
#
# Which make: versions.lock pins GNU Make 4.4.1. macOS ships GNU Make 3.81 as
# `make`, so a mac run needs MAKE_BIN=gmake to compare against the pinned one;
# the version check warns when it does not, and BENCHMARKS.md records whichever
# binary actually ran.
#
# Parallelism: every tool is given BENCH_JOBS explicitly. Left to itself each
# one picks its own default (usually the host core count), which on a 10-core
# machine handed the tools without a flag a ~25% wider pool than the rest.
#
# Daemon variants tested:
#   magus : "daemonless" (no daemon) and "daemon" (stable daemon running)
#   nx    : "daemonless" (NX_DAEMON=false) and "daemon" (default, daemon enabled)
#   other : "daemonless" only (no daemon concept applies)

set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
VERSIONS_LOCK="$BENCH_DIR/versions.lock"

HF_WARMUP="${BENCH_WARMUP:-1}"
HF_RUNS="${BENCH_RUNS:-10}"
SKIP_VER="${BENCH_SKIP_VERSION_CHECK:-0}"
DRY_RUN="${BENCH_DRY_RUN:-0}"
JOBS="${BENCH_JOBS:-8}"
MAGUS="${MAGUS_BIN:-magus}"
MAKE="${MAKE_BIN:-make}"

# The aggregator probes the same binaries to record what was measured.
export MAGUS_BIN="$MAGUS"
export MAKE_BIN="$MAKE"

# ── colors ───────────────────────────────────────────────────────────────────
# live: false during a dry run. It guards every side effect outside hyperfine:
# warm-up builds, cache clears, the scratch commits the change scenarios need,
# and daemon start/stop. Without it "print the commands" meant "run most of
# them, then print one".
live() { [[ "$DRY_RUN" != "1" ]]; }

red()     { printf '\033[1;31m%s\033[0m\n' "$*"; }
green()   { printf '\033[1;32m%s\033[0m\n' "$*"; }
yellow()  { printf '\033[1;33m%s\033[0m\n' "$*"; }
section() { printf '\n\033[1;34m=== %s ===\033[0m\n' "$*"; }
die()     { red "error: $*" >&2; exit 1; }

# ── argument parsing ──────────────────────────────────────────────────────────
usage() {
    echo "usage: $0 <fixture> [<size>] [tool ...]"
    echo "  fixture:  go | ts | polyglot"
    echo "  size:     project count (default 50, ignored for polyglot)"
    echo "  tools:    default all applicable for fixture"
    exit 1
}
if [[ $# -lt 1 ]]; then usage; fi

FIXTURE="$1"; shift
SIZE=50
if [[ $# -gt 0 && "$1" =~ ^[0-9]+$ ]]; then
    SIZE="$1"; shift
fi
TOOLS=("${@}")

if [[ "${#TOOLS[@]}" -eq 0 ]]; then
    case "$FIXTURE" in
        go)       TOOLS=(magus make) ;;
        ts)       TOOLS=(magus turbo nx lage moon) ;;
        polyglot) TOOLS=(magus make moon) ;;
        *) die "unknown fixture '$FIXTURE'. Expected: go | ts | polyglot" ;;
    esac
fi

# ── version-lock check ────────────────────────────────────────────────────────
check_versions() {
    [[ "$SKIP_VER" == "1" ]] || [[ ! -f "$VERSIONS_LOCK" ]] && return 0
    while IFS='=' read -r key want; do
        [[ -z "$key" || "${key:0:1}" == "#" ]] && continue
        local got=""
        case "$key" in
            hyperfine) got=$(hyperfine --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            magus)     got=$("$MAGUS" version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            make)      got=$("$MAKE" --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 || true) ;;
            node)      got=$(node --version 2>/dev/null | tr -d 'v' || true) ;;
            pnpm)      got=$(pnpm --version 2>/dev/null || true) ;;
            turbo)     got=$(turbo --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            nx)        got=$(nx --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            lage)      got=$(lage --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            moon)      got=$(moon --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            bazel)     got=$(bazel --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true) ;;
            *) continue ;;
        esac
        if [[ -n "$got" && "$got" != "$want" ]]; then
            yellow "warn: $key version mismatch (want $want, got $got) — set BENCH_SKIP_VERSION_CHECK=1 to suppress"
        fi
    done < "$VERSIONS_LOCK"
}

# ── tool availability check ───────────────────────────────────────────────────

# works: run the tool's own version command and require it to succeed. `command
# -v` passes for a shim that exits non-zero on every invocation, which then
# shows up as a tool that is astonishingly fast at everything.
works() { "$@" >/dev/null 2>&1; }

check_tools() {
    works hyperfine --version || die "hyperfine not found or not runnable. Install: sudo apt install hyperfine"
    local missing=()
    for t in "${TOOLS[@]}"; do
        local fam="${t%%-*}"
        case "$fam" in
            magus) works "$MAGUS" version   || missing+=("magus (build: magus run go-build .)") ;;
            make)  works "$MAKE" --version  || missing+=("make   (MAKE_BIN=$MAKE)") ;;
            turbo) works turbo --version    || missing+=("turbo  (pnpm install -g turbo@latest)") ;;
            nx)    works nx --version       || missing+=("nx     (pnpm install -g nx@latest)") ;;
            lage)  works lage --version     || missing+=("lage   (pnpm install -g @microsoft/lage@latest)") ;;
            moon)  works moon --version     || missing+=("moon   (curl -fsSL https://moonrepo.dev/install/moon.sh | bash)") ;;
            bazel) works bazel --version    || missing+=("bazel  (https://bazel.build/install)") ;;
        esac
    done
    if [[ "${#missing[@]}" -gt 0 ]]; then
        die "missing or broken tools:  ${missing[*]}"
    fi
}

# ── daemon helpers ────────────────────────────────────────────────────────────

# _stable_sock: path of the magus server socket (may not exist).
_stable_sock() {
    local dir
    if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
        dir="$XDG_RUNTIME_DIR/magus"
    else
        dir="${TMPDIR:-/tmp}/magus-$(id -u)"
    fi
    echo "$dir/server.sock"
}

# _ensure_no_magus_daemon: stop any running magus server and wait for
# the socket to disappear. `magus server stop` connects directly to the
# daemon socket via adopt.Shutdown (it is not forwarded through adopt.Forward).
_ensure_no_magus_daemon() {
    live || return 0
    local sock; sock=$(_stable_sock)
    unset MAGUS_PROC_SOCKET
    if [[ -S "$sock" ]]; then
        "$MAGUS" server stop >/dev/null 2>&1 || true
        local i=0
        while [[ -S "$sock" && $i -lt 20 ]]; do
            sleep 0.1
            (( i++ )) || true
        done
        rm -f "$sock" 2>/dev/null || true
    fi
}

DAEMON_STARTED=0

# start_magus_daemon: start a fresh magus server and wait for socket.
start_magus_daemon() {
    live || return 0
    _ensure_no_magus_daemon
    "$MAGUS" server start >/dev/null 2>&1 &
    local sock; sock=$(_stable_sock)
    local i=0
    while [[ ! -S "$sock" && $i -lt 50 ]]; do
        sleep 0.1
        (( i++ )) || true
    done
    if [[ ! -S "$sock" ]]; then
        yellow "warn: magus daemon did not start within 5s"
    fi
    export MAGUS_PROC_SOCKET="unix://$sock"
    DAEMON_STARTED=1
}

# stop_magus_daemon: stop daemon if we started it; always unset the socket var.
stop_magus_daemon() {
    if [[ "$DAEMON_STARTED" == "1" ]]; then
        _ensure_no_magus_daemon
        DAEMON_STARTED=0
    fi
    unset MAGUS_PROC_SOCKET 2>/dev/null || true
}

# ── per-tool command table ────────────────────────────────────────────────────
# get_cmd <tool> <scenario> <fixture> <daemon>
#   tool   : magus, make, turbo, nx, lage, moon, bazel
#   daemon : "daemonless" | "daemon"
# Returns the command string, or "n/a" when the scenario doesn't apply.
get_cmd() {
    local tool="$1" scenario="$2" fixture="$3" daemon="$4"
    local family="${tool%%-*}"

    # nx daemon prefix: disable daemon for "daemonless" runs
    local nx_prefix=""
    if [[ "$family" == "nx" && "$daemon" == "daemonless" ]]; then
        nx_prefix="NX_DAEMON=false "
    fi

    # For the TypeScript fixture, magus S1-S3 work fine. S4-S7 are skipped (n/a):
    # the synthetic tree wires cross-package deps via workspace:*, but pnpm does
    # not reliably symlink @bench/lib-* into each app's node_modules, so `tsc -b`
    # fails with TS2307 for EVERY tool, not just magus (see README "known issues").
    # Build ordering is not the blocker: magus honors the magusfile's magus.needs
    # edges, so libs finish before apps. The linking gap remains.
    if [[ "$family" == "magus" && "$fixture" == "ts" ]]; then
        case "$scenario" in S4|S5|S6|S7) echo "n/a"; return;; esac
    fi

    # The go fixture is N independent services with no shared libs, so it has no
    # upstream to change: fixtures/go/gen.sh points .bench-upstream-file at the
    # same file as the leaf. Running S7 there re-measured S6 and published it
    # under the "one upstream lib changed" heading.
    if [[ "$fixture" == "go" && "$scenario" == "S7" ]]; then
        echo "n/a"; return
    fi

    case "$family:$scenario" in
        # S1 — startup
        magus:S1)  echo "${MAGUS} version" ;;
        make:S1)   echo "${MAKE} --version" ;;
        turbo:S1)  echo "turbo --version" ;;
        nx:S1)     echo "${nx_prefix}nx --version" ;;
        lage:S1)   echo "lage --version" ;;
        moon:S1)   echo "moon --version" ;;
        bazel:S1)  echo "bazel version" ;;

        # S2 — project discovery
        # magus ls is only supported in no-daemon mode (the stable daemon's
        # dispatchAdopted only supports run/affected; ls is run locally).
        magus:S2)
            if [[ "$daemon" == "daemon" ]]; then echo "n/a"
            else echo "${MAGUS} ls"; fi ;;
        make:S2)   echo "n/a" ;;
        turbo:S2)  echo "turbo ls" ;;
        nx:S2)     echo "${nx_prefix}nx show projects" ;;
        lage:S2)   echo "lage info" ;;
        moon:S2)   echo "moon project list" ;;
        bazel:S2)  echo "bazel query //..." ;;

        # S3 affected dry-run. --base HEAD~1: the S3 harness commits a scratch
        # change, so the comparison ref is the previous commit. Without it magus
        # defaults to origin/main, which the throwaway bench repo lacks (git exit
        # 128), silently falling back to "all projects" and inflating S3.
        #
        # nx gets the same baseline. It also gets --graph=stdout, because
        # `nx affected` has no --dry-run: that flag belongs to the generator
        # commands, and nx forwards an unrecognized option to the executor, so
        # `nx affected --target=build --dry-run` ran every affected build. The
        # scenario is planning-only, and --graph=stdout is nx's planning output.
        magus:S3)  echo "${MAGUS} affected build --dry-run --base HEAD~1" ;;
        make:S3)   echo "n/a" ;;
        turbo:S3)  echo "turbo run build --dry --filter=[HEAD~1]" ;;
        nx:S3)     echo "${nx_prefix}nx affected --target=build --base=HEAD~1 --head=HEAD --graph=stdout" ;;
        lage:S3)   echo "n/a" ;;  # lage has no affected computation
        moon:S3)   echo "moon ci --base=HEAD~1 --dryRun" ;;
        bazel:S3)  echo "n/a" ;;  # would need file-to-label mapping

        make:S7)   echo "n/a" ;;  # make has no dependency graph

        # S4-S7 are all "build everything, let the tool decide what to skip";
        # they differ only in what the harness does to the tree beforehand.
        magus:S4|magus:S5|magus:S6|magus:S7|\
        make:S4|make:S5|make:S6|\
        turbo:S4|turbo:S5|turbo:S6|turbo:S7|\
        nx:S4|nx:S5|nx:S6|nx:S7|\
        lage:S4|lage:S5|lage:S6|lage:S7|\
        moon:S4|moon:S5|moon:S6|moon:S7|\
        bazel:S4|bazel:S5|bazel:S6|bazel:S7)
            build_cmd "$family" "$nx_prefix" ;;

        *) echo "n/a" ;;
    esac
}

# build_cmd <family> <nx-prefix>: the tool's "build everything" invocation, with
# parallelism pinned to JOBS for every tool so none of them is silently running
# with a wider pool than the others.
build_cmd() {
    local family="$1" nx_prefix="$2"
    case "$family" in
        magus) echo "${MAGUS} run build --concurrency=${JOBS}" ;;
        make)  echo "${MAKE} -j${JOBS} all" ;;
        turbo) echo "turbo run build --concurrency=${JOBS}" ;;
        nx)    echo "${nx_prefix}nx run-many -t build --parallel=${JOBS}" ;;
        lage)  echo "lage build --concurrency ${JOBS}" ;;
        moon)  echo "moon run :build --concurrency ${JOBS}" ;;
        bazel) echo "bazel build --jobs=${JOBS} //..." ;;
        *)     echo "n/a" ;;
    esac
}

# Returns the cache-clear prepare command for S4.
# For TypeScript fixtures we also remove tsc incremental artifacts (*.tsbuildinfo
# and dist/ directories outside node_modules) so hyperfine --prepare starts from a
# true cold-compiler state.  Without this, `tsc -b` sees the tsbuildinfo and no-ops
# even when the tool's own cache (e.g. .magus) was deleted.
_ts_extra_clear='find . -name "*.tsbuildinfo" -delete 2>/dev/null; find . -name "dist" -type d -not -path "*/node_modules/*" -exec rm -rf {} + 2>/dev/null; true'

get_clear_cache() {
    local family="${1%%-*}"
    local fixture="${2:-}"
    local ts_clear=""
    [[ "$fixture" == "ts" ]] && ts_clear="; ${_ts_extra_clear}"
    case "$family" in
        magus) echo "rm -rf .magus${ts_clear}" ;;
        make)  echo "${MAKE} clean 2>/dev/null || rm -rf out" ;;
        turbo) echo "rm -rf .turbo${ts_clear}" ;;
        nx)    echo "rm -rf .nx/cache${ts_clear}" ;;
        lage)  echo "rm -rf node_modules/.cache/lage${ts_clear}" ;;
        moon)  echo "rm -rf .moon/cache${ts_clear}" ;;
        bazel) echo "bazel clean" ;;
        *)     echo "" ;;
    esac
}

# ── hyperfine wrapper ─────────────────────────────────────────────────────────
# run_bench <outfile> <warmup> <runs> <prepare-or-empty> <cmd>
run_bench() {
    local outfile="$1" warmup="$2" runs="$3" prepare="$4" cmd="$5"
    mkdir -p "$(dirname "$outfile")"

    if [[ "$DRY_RUN" == "1" ]]; then
        # The inner quotes are part of the command line being echoed, not this one.
        # shellcheck disable=SC2016
        echo "[dry-run] hyperfine --warmup $warmup --runs $runs${prepare:+ --prepare '$prepare'} '$cmd' -> $outfile"
        return
    fi

    local hf_args=(--warmup "$warmup" --runs "$runs" --ignore-failure --export-json "$outfile")
    if [[ -n "$prepare" ]]; then hf_args+=(--prepare "$prepare"); fi
    hyperfine "${hf_args[@]}" "$cmd"
}

# ── scenario runner ───────────────────────────────────────────────────────────
# run_all_scenarios <fixture> <size> <tool> <daemon-label>
# Expects CWD = gen/ directory with git repo initialized.
run_all_scenarios() {
    local fixture="$1" size="$2" tool="$3" daemon="$4"
    local prefix="${fixture}-${size}-${tool}-${daemon}"

    section "$tool ($daemon) on $fixture-$size"

    # ── S1: startup ───────────────────────────────────────────────────────────
    local cmd; cmd=$(get_cmd "$tool" "S1" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S1 startup..."
        run_bench "$RESULTS_DIR/${prefix}-S1.json" 3 50 "" "$cmd"
    fi

    # ── S2: discovery ─────────────────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S2" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S2 discovery..."
        run_bench "$RESULTS_DIR/${prefix}-S2.json" "$HF_WARMUP" "$HF_RUNS" "" "$cmd"
    fi

    # ── S3: affected dry-run ──────────────────────────────────────────────────
    # Commit a scratch change so HEAD~1 baseline differs from HEAD.
    cmd=$(get_cmd "$tool" "S3" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S3 affected dry-run..."
        local leaf; leaf=$(cat "$FIXTURE_DIR/.bench-leaf-file")
        local scratch="$leaf.s3-scratch"
        if live; then
            echo "// bench-s3" > "$scratch"
            git add -A >/dev/null
            git -c commit.gpgsign=false commit -q -m "bench: S3 scratch" >/dev/null
        fi
        run_bench "$RESULTS_DIR/${prefix}-S3.json" "$HF_WARMUP" "$HF_RUNS" "" "$cmd"
        if live; then
            git reset --hard HEAD~1 >/dev/null
            rm -f "$scratch"
            git add -A >/dev/null
            git -c commit.gpgsign=false commit -q -m "bench: revert S3 scratch" >/dev/null 2>&1 || true
        fi
    fi

    # ── S4: cold build ────────────────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S4" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S4 cold build..."
        local clear; clear=$(get_clear_cache "$tool" "$fixture")
        run_bench "$RESULTS_DIR/${prefix}-S4.json" 1 "$HF_RUNS" "$clear" "$cmd"
    fi

    # ── S5: warm cache ────────────────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S5" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S5 warm cache..."
        local clear; clear=$(get_clear_cache "$tool" "$fixture")
        if live; then
            if [[ -n "$clear" ]]; then eval "$clear" >/dev/null 2>&1 || true; fi
            eval "$cmd" >/dev/null 2>&1 || true
        fi
        run_bench "$RESULTS_DIR/${prefix}-S5.json" 2 "$HF_RUNS" "" "$cmd"
    fi

    # ── S6: one leaf file changed ─────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S6" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S6 one leaf changed..."
        local leaf; leaf=$(cat "$FIXTURE_DIR/.bench-leaf-file")
        local clear; clear=$(get_clear_cache "$tool" "$fixture")
        local cache_dir; cache_dir=$(tool_cache_dir "$tool")
        local snap="${cache_dir}-s6-snap"
        if live; then
            if [[ -n "$clear" ]]; then eval "$clear" >/dev/null 2>&1 || true; fi
            eval "$cmd" >/dev/null 2>&1 || true
            if [[ -n "$cache_dir" && -d "$cache_dir" ]]; then cp -r "$cache_dir" "$snap" 2>/dev/null || true; fi
            echo "// bench-s6-change" >> "$leaf"
            git add -A >/dev/null
            git -c commit.gpgsign=false commit -q -m "bench: S6 leaf change"
        fi
        local prep=""
        if [[ -n "$cache_dir" && -d "$snap" ]]; then
            prep="rm -rf '${cache_dir}' && cp -r '${snap}' '${cache_dir}' && printf '// bench-s6-%s\n' \$(date +%N) >> '${leaf}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s6"
        else
            prep="printf '// bench-s6-%s\n' \$(date +%N) >> '${leaf}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s6"
        fi
        run_bench "$RESULTS_DIR/${prefix}-S6.json" 1 "$HF_RUNS" "$prep" "$cmd"
        if live; then
            git reset --hard "$INITIAL_SHA" >/dev/null
            if [[ -n "$cache_dir" && -d "$snap" ]]; then
                rm -rf "$cache_dir" && cp -r "$snap" "$cache_dir" 2>/dev/null || true
            fi
            rm -rf "$snap" 2>/dev/null || true
        fi
    fi

    # ── S7: upstream lib changed ──────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S7" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S7 upstream lib changed..."
        local upstream; upstream=$(cat "$FIXTURE_DIR/.bench-upstream-file")
        local clear; clear=$(get_clear_cache "$tool" "$fixture")
        local cache_dir; cache_dir=$(tool_cache_dir "$tool")
        local snap="${cache_dir}-s7-snap"
        if live; then
            if [[ -n "$clear" ]]; then eval "$clear" >/dev/null 2>&1 || true; fi
            eval "$cmd" >/dev/null 2>&1 || true
            if [[ -n "$cache_dir" && -d "$cache_dir" ]]; then cp -r "$cache_dir" "$snap" 2>/dev/null || true; fi
            echo "// bench-s7-change" >> "$upstream"
            git add -A >/dev/null
            git -c commit.gpgsign=false commit -q -m "bench: S7 upstream change"
        fi
        local prep=""
        if [[ -n "$cache_dir" && -d "$snap" ]]; then
            prep="rm -rf '${cache_dir}' && cp -r '${snap}' '${cache_dir}' && printf '// bench-s7-%s\n' \$(date +%N) >> '${upstream}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s7"
        else
            prep="printf '// bench-s7-%s\n' \$(date +%N) >> '${upstream}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s7"
        fi
        run_bench "$RESULTS_DIR/${prefix}-S7.json" 1 "$HF_RUNS" "$prep" "$cmd"
        if live; then
            git reset --hard "$INITIAL_SHA" >/dev/null
            if [[ -n "$cache_dir" && -d "$snap" ]]; then
                rm -rf "$cache_dir" && cp -r "$snap" "$cache_dir" 2>/dev/null || true
            fi
            rm -rf "$snap" 2>/dev/null || true
        fi
    fi
}

# Returns the tool's local cache directory (relative to gen/).
tool_cache_dir() {
    local family="${1%%-*}"
    case "$family" in
        magus) echo ".magus" ;;
        turbo) echo "node_modules/.cache/turbo" ;;
        nx)    echo ".nx/cache" ;;
        lage)  echo "node_modules/.cache/lage" ;;
        moon)  echo ".moon/cache" ;;
        bazel) echo "" ;;  # bazel uses its own global cache; no simple cp
        make)  echo "" ;;
        *)     echo "" ;;
    esac
}

# ── TS fixture pnpm install ───────────────────────────────────────────────────
ts_setup() {
    if [[ ! -f "pnpm-workspace.yaml" ]]; then
        die "not a pnpm workspace (expected pnpm-workspace.yaml)"
    fi
    if [[ ! -d "node_modules" ]]; then
        echo "running pnpm install..." >&2
        pnpm install --frozen-lockfile 2>/dev/null || pnpm install
    fi
}

# ── main ──────────────────────────────────────────────────────────────────────
check_versions
check_tools

# Print the magus version for the record.
section "Tool versions"
"$MAGUS" version --verbose 2>/dev/null || "$MAGUS" version

# Kill any running magus daemon before we start — we manage it ourselves below.
_ensure_no_magus_daemon

FIXTURE_DIR="$BENCH_DIR/fixtures/$FIXTURE"
if [[ ! -d "$FIXTURE_DIR" ]]; then die "fixture directory not found: $FIXTURE_DIR"; fi

if [[ "$FIXTURE" == "polyglot" ]]; then
    SIZE=0
fi

# Generate fixture
section "Generating $FIXTURE fixture (N=$SIZE)"
"$FIXTURE_DIR/gen.sh" "$SIZE"

GEN_DIR="$FIXTURE_DIR/gen"
if [[ ! -d "$GEN_DIR" ]]; then die "gen.sh did not create $GEN_DIR"; fi

# Init throwaway git repo
cd "$GEN_DIR"
if [[ ! -d ".git" ]]; then
    git init -q
    git config user.email "bench@bench.local"
    git config user.name "bench"
    git config commit.gpgsign false
    git config tag.gpgsign false
fi
git add -A >/dev/null
git -c commit.gpgsign=false commit -q -m "initial" 2>/dev/null || true
INITIAL_SHA=$(git rev-parse HEAD)

# TS-specific: install pnpm packages once before timing
if [[ "$FIXTURE" == "ts" ]]; then
    ts_setup
fi

# Run benchmarks
mkdir -p "$RESULTS_DIR"

for tool in "${TOOLS[@]}"; do
    local_family="${tool%%-*}"
    case "$local_family" in
        magus)
            # no-daemon run
            _ensure_no_magus_daemon
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemonless"

            # daemon-on run
            start_magus_daemon
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemon"
            stop_magus_daemon
            ;;
        nx)
            # no-daemon run (NX_DAEMON=false baked into get_cmd)
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemonless"

            # daemon-on run (default nx daemon behavior)
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemon"
            ;;
        *)
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemonless"
            ;;
    esac
done

cd "$BENCH_DIR"

# Aggregate results → BENCHMARKS.md
#
# The redirect goes to a temp file, never to BENCHMARKS.md: `> BENCHMARKS.md`
# truncates before the aggregator runs, so any aggregator failure replaced the
# published results with an empty file. A dry run does not aggregate at all,
# because it measured nothing and the only thing it could write is emptiness.
section "Aggregating results"
if [[ "$DRY_RUN" == "1" ]]; then
    yellow "dry run: BENCHMARKS.md left untouched"
else
    AGG_TMP="$(mktemp "${TMPDIR:-/tmp}/benchmarks.XXXXXX.md")"
    if (cd "$BENCH_DIR/aggregate" && GOWORK=off go run . "$RESULTS_DIR") > "$AGG_TMP"; then
        mv "$AGG_TMP" "$BENCH_DIR/BENCHMARKS.md"
        green "BENCHMARKS.md updated"
    else
        rm -f "$AGG_TMP"
        yellow "aggregator failed; BENCHMARKS.md unchanged, raw JSON in $RESULTS_DIR"
    fi
fi

green "Done. Results in $RESULTS_DIR"
