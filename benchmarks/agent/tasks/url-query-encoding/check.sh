#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="$(cd "${1:?usage: check.sh <worktree>}" && pwd)"

# Held out rather than run from the tree: gutting the package's own tests must not
# be a way to pass.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/heldout.test.mjs" <<EOF
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { buildUrl, request } from '$WT/packages/platform/http/index.mjs';

test('an absent query returns the base unchanged', () => {
  assert.equal(buildUrl('https://example.test/v1'), 'https://example.test/v1');
  assert.equal(buildUrl('https://example.test/v1', {}), 'https://example.test/v1');
});

test('keys stay sorted', () => {
  assert.equal(buildUrl('https://e.test', { b: 2, a: 1, c: 3 }), 'https://e.test?a=1&b=2&c=3');
});

test('values are percent-encoded', () => {
  assert.equal(buildUrl('https://e.test', { q: 'a b&c=d' }), 'https://e.test?q=a%20b%26c%3Dd');
  assert.equal(buildUrl('https://e.test', { path: 'x/y?z' }), 'https://e.test?path=x%2Fy%3Fz');
});

test('keys are percent-encoded too', () => {
  assert.equal(buildUrl('https://e.test', { 'q p': 1 }), 'https://e.test?q%20p=1');
});

test('request still routes through buildUrl', () => {
  assert.equal(
    request('https://e.test', { query: { q: 'a b' } }).url,
    'https://e.test?q=a%20b',
  );
});
EOF

node --test "$TMP/heldout.test.mjs"
tl_platform_tests "$WT"
