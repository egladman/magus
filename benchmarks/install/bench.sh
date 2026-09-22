#!/usr/bin/env bash
# bench.sh: `magus run install` against a pnpm workspace on the same four projects.
#
# Usage: benchmarks/install/bench.sh
#
# The contenders install the four JavaScript projects of THIS checkout:
#
#   pnpm   a scratch pnpm workspace holding copies of the four package.json and
#          pnpm-lock.yaml files (sharedWorkspaceLockfile: false, so each keeps its own
#          lock), installed with `pnpm install -r --frozen-lockfile --prefer-offline`
#   magus  `magus run install <the four projects>` in this checkout
#
# Three cases, each measured by hyperfine with the pnpm store warm:
#
#   fresh     no node_modules anywhere, as in a new worktree. magus may seed from a
#             sibling checkout; the scratch workspace has none to seed from.
#   noop      nothing changed since the last install
#   lockfile  console/pnpm-lock.yaml changed (a comment toggled before every run)
#
# The fresh case DELETES this checkout's four node_modules and the lockfile case edits
# console/pnpm-lock.yaml; both are restored on exit.
#
# Env overrides:
#   MAGUS_BIN=./magus   the magus binary, relative to the checkout root
#   BENCH_RUNS=7        measured runs per case
#   BENCH_OUT=<dir>     where hyperfine's JSON lands (default: a temp dir)
#
# Required: hyperfine, pnpm, and a magus built from this checkout.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MAGUS="${MAGUS_BIN:-./magus}"
RUNS="${BENCH_RUNS:-7}"
OUT="${BENCH_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/magus-install-bench.XXXXXX")}"
PROJECTS=(console docs docs/guides/integrations/agents libs/textsearch)
LOCK="$ROOT/console/pnpm-lock.yaml"

for bin in hyperfine pnpm; do
    command -v "$bin" >/dev/null || { echo "bench.sh: $bin is required" >&2; exit 2; }
done
cd "$ROOT"
[ -x "$MAGUS" ] || { echo "bench.sh: $MAGUS is not an executable magus binary" >&2; exit 2; }

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/magus-install-bench-ws.XXXXXX")"
cp "$LOCK" "$OUT/console-pnpm-lock.yaml.orig"
restore() {
    cp "$OUT/console-pnpm-lock.yaml.orig" "$LOCK"
    rm -rf "$SCRATCH"
    # Leave the checkout installed, as it was found.
    "$MAGUS" run -s install "${PROJECTS[@]}" >/dev/null 2>&1 || true
}
trap restore EXIT

# The scratch workspace: the same manifests and locks, one pnpm-workspace.yaml carrying
# the settings the four projects' own files set.
{
    echo "packages:"
    for p in "${PROJECTS[@]}"; do echo "  - \"$p\""; done
    echo "sharedWorkspaceLockfile: false"
    echo "minimumReleaseAge: 14400"
    echo "allowBuilds:"
    echo "  msgpackr-extract: false"
} >"$SCRATCH/pnpm-workspace.yaml"
echo '{"name": "magus-install-bench", "private": true}' >"$SCRATCH/package.json"
for p in "${PROJECTS[@]}"; do
    mkdir -p "$SCRATCH/$p"
    cp "$ROOT/$p/package.json" "$ROOT/$p/pnpm-lock.yaml" "$SCRATCH/$p/"
done

PNPM="pnpm -C $SCRATCH install -r --frozen-lockfile --prefer-offline"
MAGUS_CMD="$MAGUS run -s install ${PROJECTS[*]}"

clean_trees() {
    local base="$1"
    for p in "${PROJECTS[@]}"; do printf ' %s/%s/node_modules' "$base" "$p"; done
}
CLEAN_SCRATCH="rm -rf $(clean_trees "$SCRATCH") $SCRATCH/node_modules"
CLEAN_CHECKOUT="rm -rf $(clean_trees "$ROOT")"
# A trailing comment changes the lock's bytes without changing what it pins.
cat >"$OUT/toggle.sh" <<'EOF'
#!/usr/bin/env bash
f="$1"
if [ "$(tail -n1 "$f")" = "# bench-toggle" ]; then
    sed -i.bak '$d' "$f" && rm -f "$f.bak"
else
    echo "# bench-toggle" >>"$f"
fi
EOF
chmod +x "$OUT/toggle.sh"
TOGGLE_SCRATCH="$OUT/toggle.sh $SCRATCH/console/pnpm-lock.yaml"
TOGGLE_CHECKOUT="$OUT/toggle.sh $LOCK"

# Warm both: the pnpm store, the scratch trees, and magus's install entries.
$PNPM >/dev/null
$MAGUS_CMD >/dev/null

hf() {
    local name="$1"
    shift
    hyperfine --runs "$RUNS" --export-json "$OUT/$name.json" --style basic "$@"
}

hf fresh \
    --prepare "$CLEAN_SCRATCH" -n "pnpm -r (fresh)" "$PNPM" \
    --prepare "$CLEAN_CHECKOUT" -n "magus install (fresh)" "$MAGUS_CMD"
hf noop --warmup 1 \
    -n "pnpm -r (noop)" "$PNPM" \
    -n "magus install (noop)" "$MAGUS_CMD"
hf lockfile \
    --prepare "$TOGGLE_SCRATCH" -n "pnpm -r (lockfile)" "$PNPM" \
    --prepare "$TOGGLE_CHECKOUT" -n "magus install (lockfile)" "$MAGUS_CMD"

echo
echo "median seconds (hyperfine JSON in $OUT):"
for c in fresh noop lockfile; do
    python3 - "$OUT/$c.json" <<'EOF'
import json, sys
for r in json.load(open(sys.argv[1]))["results"]:
    print(f"  {r['command']:<28} {r['median']:.3f}")
EOF
done
