// tabs.test.ts - the workspace reducers are pure, so they are tested without a DOM.
// Run: `pnpm run test` (esbuild bundles every *.test.ts to node and runs it under
// node --test). Covers the focus-transfer rules that a tab bar is easy to get wrong.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  emptyWorkspace,
  openTab,
  closeTab,
  setActive,
  setLayout,
  renameTab,
  migratePlanToDashboard,
  type Workspace,
} from "./tabs";
import type { Pane } from "./tiling";

const tab = (id: string, pageId = id) => ({ id, pageId, title: id });

test("openTab into an empty workspace appends and activates", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  assert.deepEqual(ws, { tabs: [tab("a")], activeId: "a" });
});

test("migratePlanToDashboard turns an active legacy Plan tab into Dashboard", () => {
  const before: Workspace = { tabs: [tab("plan", "plan")], activeId: "plan" };
  const migrated = migratePlanToDashboard(before);
  assert.deepEqual(migrated.workspace, {
    tabs: [{ id: "plan", pageId: "dashboard", title: "Dashboard" }],
    activeId: "plan",
  });
  assert.equal(migrated.openedPlan, true);
});

test("migratePlanToDashboard removes a duplicate Plan tab when Dashboard already exists", () => {
  const before: Workspace = {
    tabs: [tab("dashboard"), tab("plan", "plan")],
    activeId: "plan",
  };
  const migrated = migratePlanToDashboard(before);
  assert.deepEqual(migrated.workspace, {
    tabs: [tab("dashboard")],
    activeId: "dashboard",
  });
  assert.equal(migrated.openedPlan, true);
});

test("openTab appends in order and activates the new tab", () => {
  let ws = openTab(emptyWorkspace, tab("a"));
  ws = openTab(ws, tab("b"));
  assert.deepEqual(
    ws.tabs.map((t) => t.id),
    ["a", "b"],
  );
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
  assert.deepEqual(
    ws.tabs.map((t) => t.id),
    ["a", "c"],
  );
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

test("setLayout records a tab's split tree and leaves its siblings untouched", () => {
  const ws = openTab(openTab(emptyWorkspace, tab("a")), tab("b"));
  const split: Pane = {
    kind: "split",
    id: "s1",
    dir: "row",
    ratio: 0.5,
    a: { kind: "leaf", id: "a", pageId: "a" },
    b: { kind: "leaf", id: "p2", pageId: "logs" },
  };
  const next = setLayout(ws, "a", split);
  assert.deepEqual(next.tabs.find((t) => t.id === "a")?.layout, split);
  assert.equal(next.tabs.find((t) => t.id === "b")?.layout, undefined);
  assert.equal(next.activeId, ws.activeId);
});

test("setLayout does not mutate its input and no-ops on an unknown tab", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  const leaf: Pane = { kind: "leaf", id: "a", pageId: "a" };
  setLayout(ws, "a", leaf);
  assert.equal(ws.tabs[0].layout, undefined); // input untouched
  assert.equal(setLayout(ws, "zzz", leaf), ws); // unknown id returns the same reference
});

test("renameTab retitles one tab and leaves its siblings and the active id alone", () => {
  const ws = openTab(openTab(emptyWorkspace, tab("a")), tab("b"));
  const next = renameTab(ws, "a", "src/main.ts");
  assert.equal(next.tabs.find((t) => t.id === "a")?.title, "src/main.ts");
  assert.equal(next.tabs.find((t) => t.id === "b")?.title, "b");
  assert.equal(next.activeId, ws.activeId);
});

test("renameTab keeps the rest of the tab (pageId, layout) intact", () => {
  const leaf: Pane = { kind: "leaf", id: "a", pageId: "logs" };
  const ws = setLayout(openTab(emptyWorkspace, tab("a", "logs")), "a", leaf);
  const next = renameTab(ws, "a", "out-1234");
  assert.equal(next.tabs[0].pageId, "logs");
  assert.deepEqual(next.tabs[0].layout, leaf);
});

test("renameTab does not mutate its input", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  renameTab(ws, "a", "renamed");
  assert.equal(ws.tabs[0].title, "a");
});

// The reducer feeds a persisted cell whose subscribers re-render, so a surface re-reporting the
// document it already has open must not wake them: identity, not just deep equality, is the contract.
test("renameTab returns the same reference for an unknown id or an unchanged title", () => {
  const ws = openTab(emptyWorkspace, tab("a"));
  assert.equal(renameTab(ws, "zzz", "whatever"), ws);
  assert.equal(renameTab(ws, "a", "a"), ws);
});
