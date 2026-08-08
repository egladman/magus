// persist.test.ts - the durable-cell invariants that matter for correctness: get() reflects a set()
// synchronously (the in-memory value is the source of truth), update() is an atomic read-modify-write
// against the LIVE value, and the durable writes are serialized in call order through the write chain.
// A tiny in-memory localStorage stub records the value order setItem observes so the serialization is
// observable without a browser. persisted() guards `window`, so no DOM shim is needed here.

import { test } from "node:test";
import assert from "node:assert/strict";
import { persisted } from "./persist";

// Install a minimal localStorage stub on the global; setItem records each written value (in order) so a
// test can assert the durable writes landed in the same order the set() calls were made.
function stubStorage(): { writes: string[] } {
  const store = new Map<string, string>();
  const writes: string[] = [];
  (globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (k: string): string | null => (store.has(k) ? (store.get(k) as string) : null),
    setItem: (k: string, v: string): void => {
      store.set(k, v);
      writes.push(v);
    },
    removeItem: (k: string): void => {
      store.delete(k);
    },
  };
  return { writes };
}

// Every test that sets a value ends by awaiting flushed(), even where the assertion is
// about the synchronous side. set() enqueues a durable write and returns, so a test that
// simply ends leaves that write pending and whether it ever runs depends on process-exit
// timing - which made these lines flip between covered and uncovered from run to run and
// moved the coverage badge the drift gate compares.
test("persisted: get() reflects set() synchronously (in-memory source of truth)", async () => {
  stubStorage();
  const cell = persisted<number>("t-poll", 1);
  cell.set(42);
  assert.equal(cell.get(), 42); // no await: current is the sync source of truth
  await cell.flushed();
});

test("persisted: update() is an atomic read-modify-write against the live value", async () => {
  stubStorage();
  const cell = persisted<Record<string, string>>("t-map", {});
  cell.set({ a: "1" });
  cell.update((prev) => ({ ...prev, b: "2" }));
  cell.update((prev) => ({ ...prev, c: "3" })); // sees b, not the pre-b snapshot
  assert.deepEqual(cell.get(), { a: "1", b: "2", c: "3" });
  await cell.flushed();
});

test("persisted: consecutive updates do not clobber (get()+set() would drop b)", async () => {
  stubStorage();
  const cell = persisted<Record<string, boolean>>("t-flags", {});
  cell.update((prev) => ({ ...prev, x: true }));
  cell.update((prev) => ({ ...prev, y: true }));
  assert.deepEqual(cell.get(), { x: true, y: true });
  await cell.flushed();
});

test("persisted: durable writes are serialized in call order", async () => {
  const { writes } = stubStorage();
  const cell = persisted<number>("t-order", 0);
  for (const n of [1, 2, 3, 4, 5]) cell.set(n);
  assert.equal(cell.get(), 5); // the last value is visible immediately, before any write flushes
  await cell.flushed(); // drain the chain; a timer would only approximate this
  assert.deepEqual(writes.map(Number), [1, 2, 3, 4, 5]); // durable writes landed in order, none dropped
});

test("persisted: subscribers are notified before the durable write runs", async () => {
  const { writes } = stubStorage();
  const cell = persisted<number>("t-sub", 0);
  let writesAtNotify = -1;
  cell.subscribe(() => {
    writesAtNotify = writes.length;
  });
  cell.set(9);
  // The subscriber runs synchronously inside set(), before the queued microtask writes to storage.
  assert.equal(writesAtNotify, 0);
  await cell.flushed();
});

test("persisted: persistOnly writes storage without updating current or notifying", async () => {
  const { writes } = stubStorage();
  const cell = persisted<number>("t-saveonly", 1);
  let notified = false;
  cell.subscribe(() => {
    notified = true;
  });
  cell.persistOnly(7);
  assert.equal(cell.get(), 1); // the live value is untouched (Save does not hot-reload the session)
  assert.equal(notified, false); // no subscriber fires
  await cell.flushed(); // drain the chain
  assert.deepEqual(writes.map(Number), [7]); // but storage did receive the value
});

test("persisted: a fresh cell reads back a persistOnly write from storage", async () => {
  stubStorage();
  const a = persisted<number>("t-readback", 1);
  a.persistOnly(42);
  await a.flushed(); // the write has landed, not merely had time to
  const b = persisted<number>("t-readback", 1); // simulates a reload: a new cell over the same key
  assert.equal(b.get(), 42);
});
