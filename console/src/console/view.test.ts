// view.test.ts - the reactive primitives. signal/bind/scope are pure (no DOM), so they run under
// node; h() needs a document and is exercised in the browser. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { bind, scope, signal } from "./view";

test("signal: get returns the current value; set updates it and notifies subscribers", () => {
  const s = signal(1);
  assert.equal(s.get(), 1);
  const seen: number[] = [];
  const off = s.subscribe((v) => seen.push(v));
  s.set(2);
  s.set(3);
  assert.equal(s.get(), 3);
  assert.deepEqual(seen, [2, 3]);
  off();
  s.set(4);
  assert.deepEqual(seen, [2, 3]); // no notification after unsubscribe
});

test("signal: a listener unsubscribing itself mid-notify does not skip the others", () => {
  const s = signal(0);
  const seen: string[] = [];
  const offA = s.subscribe(() => {
    seen.push("a");
    offA();
  });
  s.subscribe(() => seen.push("b"));
  s.set(1);
  assert.deepEqual(seen, ["a", "b"]); // b still ran despite a removing itself
  s.set(2);
  assert.deepEqual(seen, ["a", "b", "b"]); // a is gone, b stays
});

test("bind: applies immediately, then on each change; the disposer stops it", () => {
  const s = signal("x");
  const seen: string[] = [];
  const dispose = bind(s, (v) => seen.push(v));
  assert.deepEqual(seen, ["x"]); // immediate
  s.set("y");
  assert.deepEqual(seen, ["x", "y"]);
  dispose();
  s.set("z");
  assert.deepEqual(seen, ["x", "y"]); // bound effect no longer fires
});

test("scope: dispose runs every collected disposer once, and is idempotent", () => {
  const sc = scope();
  const calls: string[] = [];
  sc.add(() => calls.push("1"));
  sc.add(() => calls.push("2"));
  sc.dispose();
  assert.deepEqual(calls, ["1", "2"]);
  sc.dispose(); // already drained - no double-fire
  assert.deepEqual(calls, ["1", "2"]);
});
