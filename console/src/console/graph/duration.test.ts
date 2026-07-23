// duration.test.ts - nodeDurationMs's three-spelling reconciliation and
// formatDuration's boundary/rounding rules. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { formatDuration, nodeDurationMs } from "./duration";
import type { GNode } from "./types";

function node(partial: Record<string, unknown>): GNode {
  return partial as unknown as GNode;
}

test("nodeDurationMs reads DurationMs (number)", () => {
  assert.equal(nodeDurationMs(node({ DurationMs: 1234 })), 1234);
});

test("nodeDurationMs reads duration_ms (number)", () => {
  assert.equal(nodeDurationMs(node({ duration_ms: 5678 })), 5678);
});

test("nodeDurationMs reads attrs.DurationMs (string)", () => {
  assert.equal(nodeDurationMs(node({ attrs: { DurationMs: "999" } })), 999);
});

test("nodeDurationMs returns 0 when no spelling is present", () => {
  assert.equal(nodeDurationMs(node({})), 0);
});

test("nodeDurationMs never returns NaN for a garbage attrs.DurationMs", () => {
  const v = nodeDurationMs(node({ attrs: { DurationMs: "abc" } }));
  assert.equal(Number.isNaN(v), false);
  assert.equal(v, 0);
});

test("nodeDurationMs prefers the first positive spelling in order", () => {
  assert.equal(
    nodeDurationMs(node({ DurationMs: 0, duration_ms: 42, attrs: { DurationMs: "7" } })),
    42,
  );
});

test("formatDuration boundaries", () => {
  assert.equal(formatDuration(0), "0ms");
  assert.equal(formatDuration(5), "5ms");
  assert.equal(formatDuration(999), "999ms");
  assert.equal(formatDuration(1000), "1.0s");
  assert.equal(formatDuration(1234), "1.2s");
  assert.equal(formatDuration(59999), "1m 00s");
  assert.equal(formatDuration(60000), "1m 00s");
  assert.equal(formatDuration(124000), "2m 04s");
});
