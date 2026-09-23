#!/usr/bin/env bash
# queue.sh - merge-queue scenarios on the enriched fixture (magus only).
#
#   Q1  planning latency: `magus vcs queue --dry-run` over N open changes, each editing
#       one feature library; admission, affected closures, conflict checks, partitioning.
#   Q2  speculative stages: three stacked changes to the platform packages (logging, then
#       http, then metrics: one partition), validated by `magus vcs queue` gating on the
#       platform test suites, at --depth 1 (each stage after the one below, cache warm)
#       and --depth 3 (all three at once). Per-stage gate times come from the manifest.
#   Q2r what stage 2 would cost gated in full (--base main) instead of on stage 1: with
#       the cache stage 1 left, and with an empty one.
#
# Needs ./setup.sh first; BENCH_SKIP_INSTALL=1 is enough, since no scenario runs next.
# Results land in results/queue/ and are summarized to stdout.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$DIR/gen/repo"
MAGUS="${MAGUS_BIN:-magus}"
OUT="$DIR/results/queue"
RUNS="${BENCH_RUNS:-5}"
SIZES="${BENCH_QUEUE_SIZES:-10 50 100}"
BASE="bench/queue"
# The changes are local branches, so the queue fetches from this repository itself.

[[ -d "$REPO/.git" ]] || { echo "queue: run ./setup.sh first (BENCH_SKIP_INSTALL=1 is enough)" >&2; exit 1; }
MAGUS="$(command -v "$MAGUS")"
mkdir -p "$OUT"

g() { git -C "$REPO" -c user.name=magus-bench -c user.email=bench@magus.invalid "$@"; }

# Seconds with a fraction, on both GNU and BSD userlands.
now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }

echo "==> fixture: $BASE wires the local queue provider"
g checkout -q -B "$BASE" bench/enriched
cp "$DIR/spells/queue-local.buzz" "$REPO/spells/queue-local.buzz"
cat > "$REPO/magusfile.buzz" <<'MF'
import "magus";
import "spells/queue-local" as queue;
magus\queue.provider(queue);
MF
g add spells/queue-local.buzz magusfile.buzz
g commit -qm "wire the benchmark's merge-queue provider"

clear_queue() {
    g for-each-ref --format='%(refname)' refs/heads/queue/ | while read -r ref; do g update-ref -d "$ref"; done
}

# change <id> <path> <line>: a queue/<id> branch off $BASE appending one line to path.
change() {
    g checkout -q -B "queue/$1" "$BASE"
    printf '%s\n' "$3" >> "$REPO/$2"
    g add "$2"
    g commit -qm "queue change $1"
}

APPS=(crew flight-simulator navigation ticket-booking warp-drive-manager)

echo "==> Q1 planning latency"
for n in $SIZES; do
    clear_queue
    for ((i = 0; i < n; i++)); do
        app="${APPS[i % 5]}"
        change "$(printf '%03d' "$i")" "packages/$app/important-feature-$(( (i / 5) % 20 ))/src/index.ts" "// queue change $i"
    done
    g checkout -q "$BASE"
    hyperfine --runs "$RUNS" --warmup 1 --export-json "$OUT/q1-$n.json" \
        "cd '$REPO' && '$MAGUS' vcs queue --dry-run --base $BASE --remote ." >/dev/null
    ( cd "$REPO" && "$MAGUS" vcs queue --dry-run --base "$BASE" --remote . ) > "$OUT/q1-$n.log"
    printf 'Q1 n=%-4s mean %6.3fs  min %6.3fs  partitions %s\n' "$n" \
        "$(jq '.results[0].mean' "$OUT/q1-$n.json")" "$(jq '.results[0].min' "$OUT/q1-$n.json")" \
        "$(grep -c '^queue: partition' "$OUT/q1-$n.log")"
done

echo "==> Q2 speculative stages"
clear_queue
# Stacked: each change cuts from the previous so the three merge cleanly in order.
g checkout -q -B queue/1 "$BASE"
printf '%s\n' "// queue change 1" >> "$REPO/packages/platform/logging/index.mjs"
g add packages/platform/logging/index.mjs; g commit -qm "queue change 1"
g checkout -q -B queue/2 "$BASE"
printf '%s\n' "// queue change 2" >> "$REPO/packages/platform/http/index.mjs"
g add packages/platform/http/index.mjs; g commit -qm "queue change 2"
g checkout -q -B queue/3 "$BASE"
printf '%s\n' "// queue change 3" >> "$REPO/packages/platform/metrics/index.mjs"
g add packages/platform/metrics/index.mjs; g commit -qm "queue change 3"
g checkout -q "$BASE"

# One discarded pass first: the first stage checkout of 80k files otherwise pays for
# a cold filesystem cache, which is not the queue's cost.
( cd "$REPO" && "$MAGUS" vcs queue --base "$BASE" --remote . --target tests --depth 3 ) > "$OUT/q2-warmup.log" 2>&1
for depth in 1 3; do
    rm -rf "$REPO/.magus/cache" "$OUT/q2-d$depth"
    start=$(now)
    ( cd "$REPO" && "$MAGUS" vcs queue --base "$BASE" --remote . --target tests --depth "$depth" --out "$OUT/q2-d$depth" ) > "$OUT/q2-d$depth.log" 2>&1
    end=$(now)
    printf 'Q2 depth=%s  wall %.2fs\n' "$depth" "$(echo "$end - $start" | bc)"
    jq -r '.changes[] | "  #\(.change.id) \(.decision) depth=\(.depth) gate=\(.duration)"' "$OUT/q2-d$depth/manifest.json"
done

echo "==> Q2r stage 2 gated in full"
stage1=$(jq -r '.changes[] | select(.change.id == "1") | .stage' "$OUT/q2-d1/manifest.json")
stage2=$(jq -r '.changes[] | select(.change.id == "2") | .stage' "$OUT/q2-d1/manifest.json")
main=$(jq -r '.base_sha' "$OUT/q2-d1/manifest.json")
wt="$(mktemp -d)/stage2"
g worktree add -q --detach "$wt" "$stage2"
empty="$(mktemp -d)"
hyperfine --runs "$RUNS" --export-json "$OUT/q2r.json" \
    -n "on stage 1 (the queue's gate)" "cd '$wt' && MAGUS_CACHE_DIR='$REPO/.magus' '$MAGUS' affected tests --base $stage1" \
    -n "full, stage-1 cache" "cd '$wt' && MAGUS_CACHE_DIR='$REPO/.magus' '$MAGUS' affected tests --base $main" \
    -n "full, empty cache" --prepare "rm -rf '$empty'/*" "cd '$wt' && MAGUS_CACHE_DIR='$empty' '$MAGUS' affected tests --base $main" >/dev/null
jq -r '.results[] | "Q2r \(.command): mean \(.mean * 1000 | floor)ms min \(.min * 1000 | floor)ms"' "$OUT/q2r.json"
g worktree remove --force "$wt"
clear_queue
