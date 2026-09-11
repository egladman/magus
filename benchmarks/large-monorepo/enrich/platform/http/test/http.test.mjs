import { test } from 'node:test';
import assert from 'node:assert/strict';

import { HttpError, buildUrl, request } from '../index.mjs';

test('buildUrl returns the base when there is no query', () => {
  assert.equal(buildUrl('https://example.test/v1'), 'https://example.test/v1');
});

test('buildUrl sorts the query keys', () => {
  assert.equal(buildUrl('https://example.test/v1', { b: 2, a: 1 }), 'https://example.test/v1?a=1&b=2');
});

test('buildUrl percent-encodes keys and values', () => {
  assert.equal(
    buildUrl('https://example.test/v1', { 'q p': 'a b&c=d' }),
    'https://example.test/v1?q%20p=a%20b%26c%3Dd',
  );
});

test('request resolves options over the defaults', () => {
  const req = request('https://example.test/v1', { method: 'POST', retries: 2 });
  assert.equal(req.method, 'POST');
  assert.equal(req.retries, 2);
  assert.equal(req.timeoutMs, 1000);
});

test('request keeps a zero timeout override', () => {
  assert.equal(request('https://example.test/v1', { timeoutMs: 0 }).timeoutMs, 0);
});

test('request rejects an empty base url', () => {
  assert.throws(() => request(''), { name: 'ConfigError' });
});

test('HttpError carries the status', () => {
  assert.equal(new HttpError(404, 'gone').status, 404);
});
