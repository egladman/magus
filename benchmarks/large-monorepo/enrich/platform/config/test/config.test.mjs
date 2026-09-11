import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ConfigError, loadConfig, mergeConfig } from '../index.mjs';

test('mergeConfig overlays scalars', () => {
  assert.deepEqual(mergeConfig({ a: 1, b: 2 }, { b: 3 }), { a: 1, b: 3 });
});

test('mergeConfig keeps a false override', () => {
  assert.deepEqual(mergeConfig({ retry: true }, { retry: false }), { retry: false });
});

test('mergeConfig keeps a zero and an empty-string override', () => {
  assert.deepEqual(mergeConfig({ retries: 3, prefix: 'v1' }, { retries: 0, prefix: '' }), {
    retries: 0,
    prefix: '',
  });
});

test('mergeConfig skips an undefined override', () => {
  assert.deepEqual(mergeConfig({ a: 1 }, { a: undefined }), { a: 1 });
});

test('mergeConfig recurses into nested objects without mutating base', () => {
  const base = { tls: { verify: true, ca: 'root' } };
  assert.deepEqual(mergeConfig(base, { tls: { verify: false } }), {
    tls: { verify: false, ca: 'root' },
  });
  assert.equal(base.tls.verify, true);
});

test('loadConfig throws ConfigError naming the missing key', () => {
  assert.throws(
    () => loadConfig({ host: null }, {}, ['host']),
    (err) => err instanceof ConfigError && err.key === 'host',
  );
});

test('loadConfig returns the resolved config', () => {
  assert.deepEqual(loadConfig({ host: 'localhost', port: 80 }, { port: 8080 }, ['host']), {
    host: 'localhost',
    port: 8080,
  });
});
