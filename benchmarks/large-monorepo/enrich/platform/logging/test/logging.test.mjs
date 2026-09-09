import { test } from 'node:test';
import assert from 'node:assert/strict';

import { LogLevel, createLogger, formatEntry } from '../index.mjs';

test('formatEntry sorts fields by key', () => {
  assert.equal(formatEntry('info', 'hello', { b: 2, a: 1 }), 'INFO hello a=1 b=2');
});

test('formatEntry tolerates a missing field bag', () => {
  assert.equal(formatEntry('warn', 'bare'), 'WARN bare');
});

test('createLogger drops entries below the floor', () => {
  const logger = createLogger('svc', 'warn');
  assert.equal(logger.log('debug', 'quiet'), null);
  assert.equal(logger.log('error', 'loud'), 'ERROR [svc] loud');
  assert.deepEqual(logger.entries, ['ERROR [svc] loud']);
});

test('LogLevel orders the levels', () => {
  assert.ok(LogLevel.debug < LogLevel.info);
  assert.ok(LogLevel.info < LogLevel.warn);
  assert.ok(LogLevel.warn < LogLevel.error);
});
