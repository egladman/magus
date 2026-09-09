#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=../task-lib.sh disable=SC1091
. "$(cd "$(dirname "$0")/.." && pwd)/task-lib.sh"
WT="$(cd "${1:?usage: check.sh <worktree>}" && pwd)"

if grep -rq --exclude-dir=node_modules --exclude-dir=.git 'createLogger' "$WT"; then
    echo "check: createLogger still appears in the tree" >&2
    grep -rn --exclude-dir=node_modules --exclude-dir=.git -l 'createLogger' "$WT" >&2
    exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/heldout.test.mjs" <<EOF
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { makeLogger } from '$WT/packages/platform/logging/index.mjs';
import { request } from '$WT/packages/platform/http/index.mjs';
import { snapshot, Counter } from '$WT/packages/platform/metrics/index.mjs';
import { loadConfig } from '$WT/packages/platform/config/index.mjs';
import { featureLogger } from '$WT/packages/ticket-booking/important-feature-0/src/platform-bridge.mjs';

test('makeLogger keeps the old behavior', () => {
  const logger = makeLogger('svc', 'warn');
  assert.equal(logger.log('debug', 'quiet'), null);
  assert.equal(logger.log('error', 'loud'), 'ERROR [svc] loud');
});

test('the platform callers still work', () => {
  assert.equal(request('https://e.test').method, 'GET');
  assert.deepEqual(snapshot([new Counter('hits')]), { hits: 0 });
  assert.deepEqual(loadConfig({ host: 'h' }, {}, ['host']), { host: 'h' });
});

test('the feature bridges still work', () => {
  assert.equal(featureLogger().log('error', 'x'), 'ERROR [important-feature-0] x');
});
EOF

node --test "$TMP/heldout.test.mjs"
tl_platform_tests "$WT"
node "$WT/tools/gen-api.mjs" --check
