// tabs.test.ts - the workspace reducers are pure, so they are tested without a DOM.
// Run: `pnpm run test` (esbuild bundles every *.test.ts to node and runs it under
// node --test). Covers the focus-transfer rules that a tab strip is easy to get wrong.

import { test } from "node:test";
import assert from "node:assert/strict";
import { emptyWorkspace, openTab, closeTab, setActive, type Workspace } from "./tabs";

const tab = (id: string, pageId = id) => ({ id, pageId, title: id });

test("openTab into an empty workspace appends and activates", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  assert.deepEqual(ws, { tabs: [tab("a")], activeId: "a" });
});

test("openTab appends in order and activates the new tab", () => {
  let ws = openTab(emptyWorkspace, tab("a"));
  ws = openTab(ws, tab("b"));
  assert.deepEqual(ws.tabs.map((t) => t.id), ["a", "b"]);
  assert.equal(ws.activeId, "b");
});

test("openTab is idempotent by id - re-opening activates, never duplicates", () => {
  let ws = openTab(openTab(emptyWorkspace, tab("a")), tab("b"));
  ws = setActive(ws, "a");
  ws = openTab(ws, tab("b")); // b already open
  assert.equal(ws.tabs.length, 2);
  assert.equal(ws.activeId, "b");
});

test("openTab does not mutate its input", () => {
  const before: Workspace = { tabs: [], activeId: null };
  openTab(before, tab("a"));
  assert.deepEqual(before, { tabs: [], activeId: null });
});

test("closeTab of the active middle tab focuses the left neighbor", () => {
  let ws = openTab(openTab(openTab(emptyWorkspace, tab("a")), tab("b")), tab("c"));
  ws = setActive(ws, "b");
  ws = closeTab(ws, "b");
  assert.deepEqual(ws.tabs.map((t) => t.id), ["a", "c"]);
  assert.equal(ws.activeId, "a");
});

test("closeTab of the active first tab focuses the new left end", () => {
  let ws = openTab(openTab(emptyWorkspace, tab("a")), tab("b"));
  ws = setActive(ws, "a");
  ws = closeTab(ws, "a");
  assert.equal(ws.activeId, "b");
});

test("closeTab of the last remaining tab clears the active id", () => {
  let ws = openTab(emptyWorkspace, tab("a"));
  ws = closeTab(ws, "a");
  assert.deepEqual(ws, { tabs: [], activeId: null });
});

test("closeTab of a non-active tab leaves the active one untouched", () => {
  let ws = openTab(openTab(emptyWorkspace, tab("a")), tab("b"));
  ws = setActive(ws, "b");
  ws = closeTab(ws, "a");
  assert.equal(ws.activeId, "b");
});

test("closeTab of an unknown id is a no-op", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  assert.equal(closeTab(ws, "zzz"), ws);
});

test("setActive to an unknown id is a no-op", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  assert.equal(setActive(ws, "zzz"), ws);
});
