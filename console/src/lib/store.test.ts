// store.test.ts - the dashboard's fan-out store. The behavior worth pinning is what happens
// to the OTHER subscribers when one misbehaves: the dashboard pushes one status frame to every
// tile about once a second, so a single broken tile must not be able to stop the board.

import { test } from "node:test";
import assert from "node:assert/strict";
import { createStore } from "./store";

test("a throwing subscriber does not stop the ones after it", () => {
  const store = createStore({ n: 0 });
  const seen: string[] = [];
  store.subscribe(() => seen.push("first"));
  store.subscribe(() => {
    throw new Error("this tile is broken");
  });
  store.subscribe(() => seen.push("third"));

  // Iterating the live set with no guard abandoned the loop here, and "third" - and every
  // tile registered after it - silently stopped updating for the rest of the session.
  assert.doesNotThrow(() => store.set({ n: 1 }));
  assert.deepEqual(seen, ["first", "third"]);
});

test("unsubscribing during a notify does not skip a later subscriber", () => {
  const store = createStore({ n: 0 });
  const seen: string[] = [];
  const off = store.subscribe(() => {
    seen.push("first");
    off(); // a tile that tears itself down on the frame it is being updated by
  });
  store.subscribe(() => seen.push("second"));

  store.set({ n: 1 });
  assert.deepEqual(seen, ["first", "second"], "the snapshot must be walked in full");

  seen.length = 0;
  store.set({ n: 2 });
  assert.deepEqual(seen, ["second"], "and the unsubscribe must still have taken effect");
});

test("subscribers see the merged whole state, not the patch", () => {
  const store = createStore({ a: 1, b: 2 });
  let got: { a: number; b: number } | null = null;
  store.subscribe((s) => {
    got = s;
  });
  store.set({ b: 9 });
  assert.deepEqual(got, { a: 1, b: 9 });
  assert.deepEqual(store.get(), { a: 1, b: 9 });
});
