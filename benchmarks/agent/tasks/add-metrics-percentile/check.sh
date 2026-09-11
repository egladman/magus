#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="$(cd "${1:?usage: check.sh <worktree>}" && pwd)"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/heldout.test.mjs" <<EOF
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { percentile, Timer, Counter, snapshot } from '$WT/packages/platform/metrics/index.mjs';

test('percentile uses the nearest rank', () => {
  assert.equal(percentile([5, 1, 3, 2, 4], 50), 3);
  assert.equal(percentile([5, 1, 3, 2, 4], 100), 5);
  assert.equal(percentile([5, 1, 3, 2, 4], 0), 1);
  assert.equal(percentile([40, 10, 30, 20], 25), 10);
  assert.equal(percentile([40, 10, 30, 20], 75), 30);
});

test('percentile of no samples is zero', () => {
  assert.equal(percentile([], 50), 0);
});

test('percentile does not mutate the caller array', () => {
  const samples = [5, 1, 3];
  percentile(samples, 50);
  assert.deepEqual(samples, [5, 1, 3]);
});

test('the rest of the package is unchanged', () => {
  const latency = new Timer('latency');
  latency.record(10);
  latency.record(20);
  assert.equal(latency.average(), 15);
  assert.deepEqual(snapshot([latency, new Counter('hits')]), { hits: 0, latency: 15 });
});
EOF

node --test "$TMP/heldout.test.mjs"
tl_platform_tests "$WT"
node "$WT/tools/gen-api.mjs" --check
