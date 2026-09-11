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

import { ConfigError, loadConfig, mergeConfig } from '$WT/packages/platform/config/index.mjs';

test('a false override wins', () => {
  assert.deepEqual(mergeConfig({ retry: true }, { retry: false }), { retry: false });
});

test('a zero override wins', () => {
  assert.deepEqual(mergeConfig({ retries: 3 }, { retries: 0 }), { retries: 0 });
});

test('an empty-string override wins', () => {
  assert.deepEqual(mergeConfig({ prefix: 'v1' }, { prefix: '' }), { prefix: '' });
});

test('a null override wins', () => {
  assert.deepEqual(mergeConfig({ ca: 'root' }, { ca: null }), { ca: null });
});

test('an undefined override is still skipped', () => {
  assert.deepEqual(mergeConfig({ a: 1 }, { a: undefined }), { a: 1 });
});

test('nested objects still merge without mutating base', () => {
  const base = { tls: { verify: true, ca: 'root' } };
  assert.deepEqual(mergeConfig(base, { tls: { verify: false } }), {
    tls: { verify: false, ca: 'root' },
  });
  assert.equal(base.tls.verify, true);
});

test('loadConfig still reports a missing required key', () => {
  assert.throws(
    () => loadConfig({ host: null }, {}, ['host']),
    (err) => err instanceof ConfigError && err.key === 'host',
  );
});

test('loadConfig still resolves overrides', () => {
  assert.deepEqual(loadConfig({ host: 'localhost', port: 80 }, { port: 0 }, ['host']), {
    host: 'localhost',
    port: 0,
  });
});
EOF

node --test "$TMP/heldout.test.mjs"
tl_platform_tests "$WT"
