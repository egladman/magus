import { test } from 'node:test';
import assert from 'node:assert/strict';

import { Counter, Timer, snapshot } from '../index.mjs';

test('Counter adds and resets', () => {
  const hits = new Counter('hits');
  assert.equal(hits.add(), 1);
  assert.equal(hits.add(4), 5);
  hits.reset();
  assert.equal(hits.value, 0);
});

test('Timer averages its samples', () => {
  const latency = new Timer('latency');
  latency.record(10);
  latency.record(20);
  assert.equal(latency.average(), 15);
});

test('Timer with no samples averages to zero', () => {
  assert.equal(new Timer('idle').average(), 0);
});

test('snapshot keys by metric name in sorted order', () => {
  const hits = new Counter('hits');
  hits.add(2);
  const latency = new Timer('latency');
  latency.record(8);
  assert.deepEqual(Object.keys(snapshot([latency, hits])), ['hits', 'latency']);
  assert.deepEqual(snapshot([latency, hits]), { hits: 2, latency: 8 });
});
