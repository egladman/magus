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
    { id: "a", title: "Log viewer", hint: undefined, active: false },
    { id: "b", title: "Graph", hint: undefined, active: true }, // openTab activates the last opened
  ]);
});

test("an empty workspace yields no tab views", () => {
  assert.deepEqual(tabViews(emptyWorkspace), []);
});

test("no tab is active when activeId points nowhere", () => {
  const ws = { tabs: [{ id: "a", pageId: "logs", title: "Log viewer" }], activeId: null };
  assert.deepEqual(tabViews(ws), [
    { id: "a", title: "Log viewer", hint: undefined, active: false },
  ]);
});

// --- auto-naming: a tab titled after the document its surface has open ------------------------
// The console writes a document title into the tab (tabs.ts renameTab); tabViews is what turns a
// path into the short label a tab can actually show, plus the hint that tells same-named tabs apart.

const wsOf = (...titles: string[]) => ({
  tabs: titles.map((title, i) => ({ id: "t" + i, pageId: "diff", title })),
  activeId: null,
});

test("a path title shows its basename, not the whole path", () => {
  assert.deepEqual(
    tabViews(wsOf("src/console/main.ts")).map((v) => [v.title, v.hint]),
    [["main.ts", undefined]],
  );
});

test("a non-path title is shown whole and never shortened", () => {
  assert.deepEqual(
    tabViews(wsOf("Log Viewer", "out-4f2a1c")).map((v) => [v.title, v.hint]),
    [
      ["Log Viewer", undefined],
      ["out-4f2a1c", undefined],
    ],
  );
});

test("same-named tabs grow the shortest parent path that separates them", () => {
  assert.deepEqual(
    tabViews(wsOf("src/console/main.ts", "src/logs/main.ts")).map((v) => [v.title, v.hint]),
    [
      ["main.ts", "console"],
      ["main.ts", "logs"],
    ],
  );
});

test("disambiguation deepens only as far as it must to separate the group", () => {
  // One shared parent (`a`) is useless, so both grow to two segments - and stop there rather than
  // walking the whole path.
  assert.deepEqual(
    tabViews(wsOf("x/one/a/f.ts", "y/two/a/f.ts")).map((v) => v.hint),
    ["one/a", "two/a"],
  );
});

test("a tab whose name is unique gets no hint even beside a disambiguated pair", () => {
  assert.deepEqual(
    tabViews(wsOf("src/a/f.ts", "src/b/f.ts", "src/c/g.ts")).map((v) => [v.title, v.hint]),
    [
      ["f.ts", "a"],
      ["f.ts", "b"],
      ["g.ts", undefined],
    ],
  );
});

test("identical paths exhaust their parents rather than inventing a difference", () => {
  assert.deepEqual(
    tabViews(wsOf("src/a/f.ts", "src/a/f.ts")).map((v) => v.hint),
    ["src/a", "src/a"],
  );
});

test("a bare basename beside a path one disambiguates without a bogus empty hint", () => {
  // The parentless tab has nothing to grow, so it keeps no hint while its sibling gains one.
  assert.deepEqual(
    tabViews(wsOf("f.ts", "src/a/f.ts")).map((v) => v.hint),
    [undefined, "a"],
  );
});
