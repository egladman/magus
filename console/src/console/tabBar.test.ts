// tabBar.test.ts - the pure Workspace->view mapping the tab bar renders from. The DOM wiring
// needs a browser; tabViews is pure and runs under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { tabViews } from "./tabBar";
import { openTab, emptyWorkspace } from "./tabs";

test("tabViews maps every tab and marks exactly the active one", () => {
  let ws = openTab(emptyWorkspace, { id: "a", pageId: "logs", title: "Log viewer" });
  ws = openTab(ws, { id: "b", pageId: "graph", title: "Graph" });
  assert.deepEqual(tabViews(ws), [
    { id: "a", title: "Log viewer", active: false },
    { id: "b", title: "Graph", active: true }, // openTab activates the last opened
  ]);
});

test("an empty workspace yields no tab views", () => {
  assert.deepEqual(tabViews(emptyWorkspace), []);
});

test("no tab is active when activeId points nowhere", () => {
  const ws = { tabs: [{ id: "a", pageId: "logs", title: "Log viewer" }], activeId: null };
  assert.deepEqual(tabViews(ws), [{ id: "a", title: "Log viewer", active: false }]);
});
