#!/usr/bin/env bash
# bench.sh — multi-tool monorepo benchmark driver
#
# Usage: ./bench.sh <fixture> [<size>] [tool ...]
#
#   fixture : go | ts | polyglot
#   size    : integer project count (default 50; ignored for polyglot)
#   tools   : subset of magus-luajit magus-gopherlua make turbo nx lage moon bazel
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
#   MAGUS_BIN=magus             override the magus binary (must be CGO_ENABLED=1
#                               build when testing magus-luajit)
#
# Lua engine axis:
#   magus-luajit    runs magus with MAGUS_INTERPRETER_LUA_ENGINE=luajit  (cgo build required)
#   magus-gopherlua runs magus with MAGUS_INTERPRETER_LUA_ENGINE=gopherlua (pure-Go engine)
#   The same binary is used for both; only the env var differs.
#
# Daemon variants tested:
#   magus-* : "daemonless" (no daemon) and "daemon" (stable daemon running)
#   nx      : "daemonless" (NX_DAEMON=false) and "daemon" (default, daemon enabled)
#   other   : "daemonless" only (no daemon concept applies)

set -euo pipefail

BENCH_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="$BENCH_DIR/results"
VERSIONS_LOCK="$BENCH_DIR/versions.lock"

HF_WARMUP="${BENCH_WARMUP:-1}"
HF_RUNS="${BENCH_RUNS:-10}"
SKIP_VER="${BENCH_SKIP_VERSION_CHECK:-0}"
DRY_RUN="${BENCH_DRY_RUN:-0}"
MAGUS="${MAGUS_BIN:-magus}"

# ── colours ───────────────────────────────────────────────────────────────────
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
        go)       TOOLS=(magus-luajit magus-gopherlua make) ;;
        ts)       TOOLS=(magus-luajit magus-gopherlua turbo nx lage moon) ;;
        polyglot) TOOLS=(magus-luajit magus-gopherlua make moon) ;;
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
check_tools() {
    command -v hyperfine >/dev/null 2>&1 || die "hyperfine not found. Install: sudo apt install hyperfine"
    local missing=()
    for t in "${TOOLS[@]}"; do
        local fam="${t%%-*}"
        case "$fam" in
            magus) command -v "$MAGUS" >/dev/null 2>&1 || missing+=("magus (build: cd tack && CGO_ENABLED=1 go build -o magus ./magus/cmd/magus)") ;;
            make)  command -v make >/dev/null 2>&1  || missing+=("make") ;;
            turbo) command -v turbo >/dev/null 2>&1 || missing+=("turbo  (pnpm install -g turbo@latest)") ;;
            nx)    command -v nx    >/dev/null 2>&1 || missing+=("nx     (pnpm install -g nx@latest)") ;;
            lage)  command -v lage  >/dev/null 2>&1 || missing+=("lage   (pnpm install -g @microsoft/lage@latest)") ;;
            moon)  command -v moon  >/dev/null 2>&1 || missing+=("moon   (curl -fsSL https://moonrepo.dev/install/moon.sh | bash)") ;;
            bazel) command -v bazel >/dev/null 2>&1 || missing+=("bazel  (https://bazel.build/install)") ;;
        esac
    done
    if [[ "${#missing[@]}" -gt 0 ]]; then
        die "missing tools:  ${missing[*]}"
    fi
}

# ── daemon helpers ────────────────────────────────────────────────────────────

# _stable_sock: path of the magus stable daemon socket (may not exist).
_stable_sock() {
    local dir
    if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
        dir="$XDG_RUNTIME_DIR/magus"
    else
        dir="${TMPDIR:-/tmp}/magus-$(id -u)"
    fi
    echo "$dir/magus-daemon.sock"
}

# _ensure_no_magus_daemon: stop any running magus stable daemon and wait for
# the socket to disappear. `magus server stop` connects directly to the
# daemon socket via adopt.Shutdown (it is not forwarded through adopt.Forward).
_ensure_no_magus_daemon() {
    local sock; sock=$(_stable_sock)
    unset MAGUS_DAEMON_SOCKET
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

# start_magus_daemon: start a fresh magus stable daemon and wait for socket.
start_magus_daemon() {
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
    export MAGUS_DAEMON_SOCKET="unix://$sock"
    DAEMON_STARTED=1
}

# stop_magus_daemon: stop daemon if we started it; always unset the socket var.
stop_magus_daemon() {
    if [[ "$DAEMON_STARTED" == "1" ]]; then
        _ensure_no_magus_daemon
        DAEMON_STARTED=0
    fi
    unset MAGUS_DAEMON_SOCKET 2>/dev/null || true
}

# ── per-tool command table ────────────────────────────────────────────────────
# get_cmd <tool> <scenario> <fixture> <daemon>
#   tool   : may be "magus-luajit", "magus-gopherlua", or bare names
#   daemon : "daemonless" | "daemon"
# Returns the command string, or "n/a" when the scenario doesn't apply.
get_cmd() {
    local tool="$1" scenario="$2" fixture="$3" daemon="$4"
    local family="${tool%%-*}"  # "magus-luajit" → "magus", "nx" → "nx"

    # Engine env prefix for magus variants.
    # "magus-luajit" → "MAGUS_INTERPRETER_LUA_ENGINE=luajit ", "magus" → ""
    local engine_env=""
    if [[ "$family" == "magus" && "$tool" != "magus" ]]; then
        engine_env="MAGUS_INTERPRETER_LUA_ENGINE=${tool#magus-} "
    fi

    # nx daemon prefix: disable daemon for "daemonless" runs
    local nx_prefix=""
    if [[ "$family" == "nx" && "$daemon" == "daemonless" ]]; then
        nx_prefix="NX_DAEMON=false "
    fi

    # For the TypeScript fixture, magus S1-S3 work fine. S4-S7 require
    # topological build ordering (libs before apps): cache.RunAll currently
    # launches all goroutines immediately without honouring depends_on as a
    # scheduling gate, so apps start before their lib deps finish. This is a
    # known gap in RunAll — depends_on is intended to drive both affected
    # detection AND build scheduling, but the scheduler side is not yet wired.
    # Turbo/nx/lage handle this via their own task graphs (turbo.json, nx.json).
    if [[ "$family" == "magus" && "$fixture" == "ts" ]]; then
        case "$scenario" in S4|S5|S6|S7) echo "n/a"; return;; esac
    fi

    case "$family:$scenario" in
        # S1 — startup
        magus:S1)  echo "${engine_env}${MAGUS} version" ;;
        make:S1)   echo "make --version" ;;
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
            else echo "${engine_env}${MAGUS} ls"; fi ;;
        make:S2)   echo "n/a" ;;
        turbo:S2)  echo "turbo ls" ;;
        nx:S2)     echo "${nx_prefix}nx show projects" ;;
        lage:S2)   echo "lage info" ;;
        moon:S2)   echo "moon project list" ;;
        bazel:S2)  echo "bazel query //..." ;;

        # S3 — affected dry-run
        magus:S3)  echo "${engine_env}${MAGUS} affected build --dry-run" ;;
        make:S3)   echo "n/a" ;;
        turbo:S3)  echo "turbo run build --dry --filter=[HEAD~1]" ;;
        nx:S3)     echo "${nx_prefix}nx affected --target=build --dry-run" ;;
        lage:S3)   echo "n/a" ;;  # lage has no affected computation
        moon:S3)   echo "moon ci --base=HEAD~1 --dryRun" ;;
        bazel:S3)  echo "n/a" ;;  # would need file-to-label mapping

        # S4 — cold build (prepare handles cache clear)
        magus:S4)  echo "${engine_env}${MAGUS} run build --concurrency=8" ;;
        make:S4)   echo "make -j8 all" ;;
        turbo:S4)  echo "turbo run build --concurrency=8" ;;
        nx:S4)     echo "${nx_prefix}nx run-many -t build --parallel=8" ;;
        lage:S4)   echo "lage build" ;;
        moon:S4)   echo "moon run :build" ;;
        bazel:S4)  echo "bazel build //..." ;;

        # S5 — warm cache (cache persists from prior S4 population)
        magus:S5)  echo "${engine_env}${MAGUS} run build --concurrency=8" ;;
        make:S5)   echo "make -j8 all" ;;
        turbo:S5)  echo "turbo run build --concurrency=8" ;;
        nx:S5)     echo "${nx_prefix}nx run-many -t build --parallel=8" ;;
        lage:S5)   echo "lage build" ;;
        moon:S5)   echo "moon run :build" ;;
        bazel:S5)  echo "bazel build //..." ;;

        # S6 — one leaf file changed
        magus:S6)  echo "${engine_env}${MAGUS} run build --concurrency=8" ;;
        make:S6)   echo "make -j8 all" ;;
        turbo:S6)  echo "turbo run build --concurrency=8" ;;
        nx:S6)     echo "${nx_prefix}nx run-many -t build --parallel=8" ;;
        lage:S6)   echo "lage build" ;;
        moon:S6)   echo "moon run :build" ;;
        bazel:S6)  echo "bazel build //..." ;;

        # S7 — one upstream lib changed
        magus:S7)  echo "${engine_env}${MAGUS} run build --concurrency=8" ;;
        make:S7)   echo "n/a" ;;  # make has no dependency graph
        turbo:S7)  echo "turbo run build --concurrency=8" ;;
        nx:S7)     echo "${nx_prefix}nx run-many -t build --parallel=8" ;;
        lage:S7)   echo "lage build" ;;
        moon:S7)   echo "moon run :build" ;;
        bazel:S7)  echo "bazel build //..." ;;

        *) echo "n/a" ;;
    esac
}

# Returns the cache-clear prepare command for S4.
# For TypeScript fixtures we also remove tsc incremental artefacts (*.tsbuildinfo
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
        make)  echo "make clean 2>/dev/null || rm -rf out" ;;
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
        echo "[dry-run] hyperfine --warmup $warmup --runs $runs${prepare:+ --prepare '$prepare'} '$cmd' -> $outfile"
        return
    fi

    local hf_args=(--warmup "$warmup" --runs "$runs" --ignore-failure --export-json "$outfile")
    if [[ -n "$prepare" ]]; then hf_args+=(--prepare "$prepare"); fi
    hyperfine "${hf_args[@]}" "$cmd"
}

# ── scenario runner ───────────────────────────────────────────────────────────
# run_all_scenarios <fixture> <size> <tool> <daemon-label>
# Expects CWD = gen/ directory with git repo initialised.
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
        echo "// bench-s3" > "$scratch"
        git add -A >/dev/null
        git -c commit.gpgsign=false commit -q -m "bench: S3 scratch" >/dev/null
        run_bench "$RESULTS_DIR/${prefix}-S3.json" "$HF_WARMUP" "$HF_RUNS" "" "$cmd"
        git reset --hard HEAD~1 >/dev/null
        rm -f "$scratch"
        git add -A >/dev/null
        git -c commit.gpgsign=false commit -q -m "bench: revert S3 scratch" >/dev/null 2>&1 || true
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
        if [[ -n "$clear" ]]; then eval "$clear" >/dev/null 2>&1 || true; fi
        eval "$cmd" >/dev/null 2>&1 || true
        run_bench "$RESULTS_DIR/${prefix}-S5.json" 2 "$HF_RUNS" "" "$cmd"
    fi

    # ── S6: one leaf file changed ─────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S6" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S6 one leaf changed..."
        local leaf; leaf=$(cat "$FIXTURE_DIR/.bench-leaf-file")
        local clear; clear=$(get_clear_cache "$tool" "$fixture")
        if [[ -n "$clear" ]]; then eval "$clear" >/dev/null 2>&1 || true; fi
        eval "$cmd" >/dev/null 2>&1 || true
        local cache_dir; cache_dir=$(tool_cache_dir "$tool")
        local snap="${cache_dir}-s6-snap"
        if [[ -n "$cache_dir" && -d "$cache_dir" ]]; then cp -r "$cache_dir" "$snap" 2>/dev/null || true; fi
        echo "// bench-s6-change" >> "$leaf"
        git add -A >/dev/null
        git -c commit.gpgsign=false commit -q -m "bench: S6 leaf change"
        local prep=""
        if [[ -n "$cache_dir" && -d "$snap" ]]; then
            prep="rm -rf '${cache_dir}' && cp -r '${snap}' '${cache_dir}' && printf '// bench-s6-%s\n' \$(date +%N) >> '${leaf}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s6"
        else
            prep="printf '// bench-s6-%s\n' \$(date +%N) >> '${leaf}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s6"
        fi
        run_bench "$RESULTS_DIR/${prefix}-S6.json" 1 "$HF_RUNS" "$prep" "$cmd"
        git reset --hard "$INITIAL_SHA" >/dev/null
        if [[ -n "$cache_dir" && -d "$snap" ]]; then
            rm -rf "$cache_dir" && cp -r "$snap" "$cache_dir" 2>/dev/null || true
        fi
        rm -rf "$snap" 2>/dev/null || true
    fi

    # ── S7: upstream lib changed ──────────────────────────────────────────────
    cmd=$(get_cmd "$tool" "S7" "$fixture" "$daemon")
    if [[ "$cmd" != "n/a" ]]; then
        echo "S7 upstream lib changed..."
        local upstream; upstream=$(cat "$FIXTURE_DIR/.bench-upstream-file")
        local clear; clear=$(get_clear_cache "$tool" "$fixture")
        if [[ -n "$clear" ]]; then eval "$clear" >/dev/null 2>&1 || true; fi
        eval "$cmd" >/dev/null 2>&1 || true
        local cache_dir; cache_dir=$(tool_cache_dir "$tool")
        local snap="${cache_dir}-s7-snap"
        if [[ -n "$cache_dir" && -d "$cache_dir" ]]; then cp -r "$cache_dir" "$snap" 2>/dev/null || true; fi
        echo "// bench-s7-change" >> "$upstream"
        git add -A >/dev/null
        git -c commit.gpgsign=false commit -q -m "bench: S7 upstream change"
        local prep=""
        if [[ -n "$cache_dir" && -d "$snap" ]]; then
            prep="rm -rf '${cache_dir}' && cp -r '${snap}' '${cache_dir}' && printf '// bench-s7-%s\n' \$(date +%N) >> '${upstream}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s7"
        else
            prep="printf '// bench-s7-%s\n' \$(date +%N) >> '${upstream}' && git add -A && git -c commit.gpgsign=false commit -q -m bench-s7"
        fi
        run_bench "$RESULTS_DIR/${prefix}-S7.json" 1 "$HF_RUNS" "$prep" "$cmd"
        git reset --hard "$INITIAL_SHA" >/dev/null
        if [[ -n "$cache_dir" && -d "$snap" ]]; then
            rm -rf "$cache_dir" && cp -r "$snap" "$cache_dir" 2>/dev/null || true
        fi
        rm -rf "$snap" 2>/dev/null || true
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

# Print active engine versions for the record.
section "Tool versions"
"$MAGUS" version --verbose 2>/dev/null || "$MAGUS" version
MAGUS_INTERPRETER_LUA_ENGINE=luajit    "$MAGUS" version --verbose 2>/dev/null | grep "lua engine" | sed 's/^/  luajit:     /' || true
MAGUS_INTERPRETER_LUA_ENGINE=gopherlua "$MAGUS" version --verbose 2>/dev/null | grep "lua engine" | sed 's/^/  gopherlua:  /' || true

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

            # daemon-on run (default nx daemon behaviour)
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemon"
            ;;
        *)
            run_all_scenarios "$FIXTURE" "$SIZE" "$tool" "daemonless"
            ;;
    esac
done

cd "$BENCH_DIR"

# Aggregate results → BENCHMARKS.md
section "Aggregating results"
if (cd "$BENCH_DIR/aggregate" && GOWORK=off go run . "$RESULTS_DIR") > BENCHMARKS.md; then
    green "BENCHMARKS.md updated"
else
    yellow "aggregator failed; raw JSON in $RESULTS_DIR"
fi

green "Done. Results in $RESULTS_DIR"
