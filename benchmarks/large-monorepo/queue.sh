#!/usr/bin/env bash
# queue.sh: merge-queue scenarios on the enriched fixture, driving the mergequeue CLI
# (libs/mergequeue) with magus as its affected hook and its gate.
#
#   Q1  planning latency over N open changes, each editing one feature library:
#       admission, conflict checks and partitioning. Q1a is the whole plan, asking
#       `magus affected ci --plan --stdin` for every change's affected set; Q1b is the
#       queue alone, re-planning the same changes with those sets already in the input.
#   Q2  speculative stages: three stacked changes to the platform packages (logging,
#       then http, then metrics: one partition), validated by `mergequeue validate`
#       gating on the platform test suites, at --depth 1 (each stage after the one
#       below, cache warm) and --depth 3 (all three at once). Per-stage gate times come
#       from the verdicts.
#   Q3  land as each stage goes green: validation at --depth 3 and `mergequeue apply`
#       run side by side, and each merge's time since validation started is
#       read from the landing events. A green stage should land before the stages
#       above it finish.
#
# Needs ./setup.sh first; BENCH_SKIP_INSTALL=1 is enough, since no scenario runs next.
# Results land in results/queue/ and are summarized to stdout.
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$DIR/../.." && pwd)"
REPO="$DIR/gen/repo"
MAGUS="${MAGUS_BIN:-magus}"
OUT="$DIR/results/queue"
RUNS="${BENCH_RUNS:-5}"
SIZES="${BENCH_QUEUE_SIZES:-10 50 100}"
BASE="bench/queue"

[[ -d "$REPO/.git" ]] || { echo "queue: run ./setup.sh first (BENCH_SKIP_INSTALL=1 is enough)" >&2; exit 1; }
MAGUS="$(command -v "$MAGUS")"
mkdir -p "$OUT"
MQ="$OUT/mergequeue"
go -C "$ROOT/libs/mergequeue" build -o "$MQ" ./cmd/mergequeue
# Every stage is a worktree of its own; pointing them all at the checkout's cache is what
# lets a stage replay what the stage below already ran.
export MAGUS_CACHE_DIR="$REPO/.magus"
# Concurrent stages share this machine's build budget. Failing fast on a full budget
# (exit 75) would measure the retry delay, not the queue; waiting for room is the point.
unset MAGUS_NO_WAIT

g() { git -C "$REPO" -c user.name=magus-bench -c user.email=bench@magus.invalid "$@"; }

# Seconds with a fraction, on both GNU and BSD userlands.
now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }

g checkout -q -B "$BASE" bench/enriched

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

# changes_json: every queue/* branch as a mergequeue.changes/v1 document, in name order.
changes_json() {
    g for-each-ref --sort=refname --format='%(refname:lstrip=3) %(objectname)' refs/heads/queue/ |
        jq -R -s --arg base "$BASE" '{schema: "mergequeue.changes/v1", base: $base,
            changes: [split("\n")[] | select(. != "") | split(" ") |
                {id: .[0], head: .[1], ref: ("refs/heads/queue/" + .[0]), title: ("queue/" + .[0])}]}'
}

AFFECTED="'$MAGUS' affected ci --plan --stdin"
GATE="'$MAGUS' affected tests --base \"\$MERGEQUEUE_ONTO\""

APPS=(crew flight-simulator navigation ticket-booking warp-drive-manager)

echo "==> Q1 planning latency"
for n in $SIZES; do
    clear_queue
    for ((i = 0; i < n; i++)); do
        app="${APPS[i % 5]}"
        change "$(printf '%03d' "$i")" "packages/$app/important-feature-$(( (i / 5) % 20 ))/src/index.ts" "// queue change $i"
    done
    g checkout -q "$BASE"
    changes_json > "$OUT/q1-$n-changes.json"
    hyperfine --runs "$RUNS" --warmup 1 --export-json "$OUT/q1a-$n.json" \
        "'$MQ' -C '$REPO' plan --remote . --changes '$OUT/q1-$n-changes.json' --affected \"$AFFECTED\" --out '$OUT/q1-$n-plan.json'" >/dev/null
    # The plan's admitted changes carry the sets the hook answered: the same queue, with
    # nothing left to ask.
    jq --arg base "$BASE" '{schema: "mergequeue.changes/v1", base: $base, changes: [.partitions[][]]}' \
        "$OUT/q1-$n-plan.json" > "$OUT/q1-$n-affected.json"
    hyperfine --runs "$RUNS" --warmup 1 --export-json "$OUT/q1b-$n.json" \
        "'$MQ' -C '$REPO' plan --remote . --changes '$OUT/q1-$n-affected.json' --out '$OUT/q1b-$n-plan.json'" >/dev/null
    printf 'Q1 n=%-4s with hook mean %7.3fs  queue alone mean %6.3fs  partitions %s\n' "$n" \
        "$(jq '.results[0].mean' "$OUT/q1a-$n.json")" "$(jq '.results[0].mean' "$OUT/q1b-$n.json")" \
        "$(jq '.partitions | length' "$OUT/q1-$n-plan.json")"
done

# Stacked: each change cuts from the base, so the three merge cleanly in order.
platform_changes() {
    clear_queue
    change 1 packages/platform/logging/index.mjs "// queue change 1"
    change 2 packages/platform/http/index.mjs "// queue change 2"
    change 3 packages/platform/metrics/index.mjs "// queue change 3"
    g checkout -q "$BASE"
    changes_json > "$OUT/q2-changes.json"
    "$MQ" -C "$REPO" plan --remote . --changes "$OUT/q2-changes.json" --affected "$AFFECTED" \
        --depth "$1" --out "$OUT/q2-plan-d$1.json" > /dev/null
}

echo "==> Q2 speculative stages"
# One discarded pass first: the first stage checkout of 80k files otherwise pays for a
# cold filesystem cache, which is not the queue's cost.
platform_changes 3
"$MQ" -C "$REPO" validate --remote . --plan "$OUT/q2-plan-d3.json" --gate "$GATE" \
    --verdicts "$OUT/q2-warmup" > "$OUT/q2-warmup.jsonl" 2> "$OUT/q2-warmup.log"
for depth in 1 3; do
    rm -rf "$REPO/.magus/cache" "$OUT/q2-d$depth"
    platform_changes "$depth"
    start=$(now)
    "$MQ" -C "$REPO" validate --remote . --plan "$OUT/q2-plan-d$depth.json" --gate "$GATE" \
        --verdicts "$OUT/q2-d$depth" > "$OUT/q2-d$depth.jsonl" 2> "$OUT/q2-d$depth.log"
    end=$(now)
    printf 'Q2 depth=%s  wall %.2fs\n' "$depth" "$(echo "$end - $start" | bc)"
    for id in 1 2 3; do
        jq -r '"  #\(.change.id) \(.decision) depth=\(.depth) gate=\(.duration_ms)ms"' "$OUT/q2-d$depth/$id/verdict.json"
    done
done

echo "==> Q3 land as each stage goes green"
rm -rf "$REPO/.magus/cache" "$OUT/q3"
platform_changes 3
land_base="bench/queue-land"
g branch -f "$land_base" "$BASE"
jq --arg b "$land_base" '.base = $b' "$OUT/q2-plan-d3.json" > "$OUT/q3-plan.json"
start=$(now)
"$MQ" -C "$REPO" validate --remote . --plan "$OUT/q3-plan.json" --gate "$GATE" \
    --verdicts "$OUT/q3" > "$OUT/q3-validate.jsonl" 2> "$OUT/q3-validate.log" &
validator=$!
QUEUE_REPO="$REPO" QUEUE_BASE="$land_base" "$MQ" -C "$REPO" apply --remote . \
    --provider "$DIR/queue-local.buzz" --interval 200ms "$OUT/q3" > "$OUT/q3-land.jsonl" 2> "$OUT/q3-land.log"
wait "$validator"
start_iso=$(jq -rn --argjson s "$start" '$s | todate')
jq -r --argjson s "$start" 'select(.kind == "merged" or .kind == "decided")
    | "  \(.kind) #\(.change) at +\((.time | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601) - ($s | floor))s"' \
    "$OUT/q3-validate.jsonl" "$OUT/q3-land.jsonl" | sort -t+ -k2 -n
echo "  (validation started $start_iso)"
g branch -D "$land_base" >/dev/null
clear_queue
