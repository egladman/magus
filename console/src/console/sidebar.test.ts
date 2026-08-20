// sidebar.test.ts - the pure Workspace -> SidebarItem mapping. sidebar-dom.test.ts covers what
// createSidebar renders; this covers the rules that decide what it renders.

import assert from "node:assert/strict";
import { test } from "node:test";
import { sidebarItems } from "./sidebar";
import type { Launchable } from "./home";
import type { Workspace } from "./tabs";

const SURFACES: Launchable[] = [
  { pageId: "dashboard", label: "Dashboard", hint: "now" },
  { pageId: "logs", label: "Log Viewer", hint: "output" },
  { pageId: "graph", label: "Graph Explorer", hint: "graph" },
];

test("every surface gets a row, in the surface list's order", () => {
  const items = sidebarItems({ tabs: [], activeId: null }, SURFACES);
  assert.deepEqual(
    items.map((i) => i.pageId),
    ["dashboard", "logs", "graph"],
  );
  assert.equal(
    items.every((i) => !i.open && !i.current),
    true,
  );
});

test("a tab marks its surface open, and the active tab's surface current", () => {
  const ws: Workspace = {
    tabs: [
      { id: "a", pageId: "logs", title: "Log Viewer" },
      { id: "b", pageId: "graph", title: "Graph Explorer" },
    ],
    activeId: "b",
  };
  const by = new Map(sidebarItems(ws, SURFACES).map((i) => [i.pageId, i]));
  assert.deepEqual(
    { open: by.get("logs")?.open, current: by.get("logs")?.current },
    { open: true, current: false },
  );
  assert.deepEqual(
    { open: by.get("graph")?.open, current: by.get("graph")?.current },
    { open: true, current: true },
  );
  assert.deepEqual(
    { open: by.get("dashboard")?.open, current: by.get("dashboard")?.current },
    { open: false, current: false },
  );
});

// A tiled tab hosts several surfaces at once, and every one of them is genuinely open - the rail has
// to read the layout tree, not just the tab's primary pageId, or a split tab reports one open surface
// and the console refuses to open the others (they are single-instance).
test("a tiled tab marks every surface in its layout", () => {
  const ws: Workspace = {
    tabs: [
      {
        id: "a",
        pageId: "dashboard",
        title: "Dashboard",
        layout: {
          kind: "split",
          id: "s",
          dir: "row",
          ratio: 0.5,
          a: { kind: "leaf", id: "l1", pageId: "dashboard" },
          b: { kind: "leaf", id: "l2", pageId: "logs" },
        },
      },
    ],
    activeId: "a",
  };
  const by = new Map(sidebarItems(ws, SURFACES).map((i) => [i.pageId, i]));
  assert.equal(by.get("logs")?.open, true);
  assert.equal(by.get("logs")?.current, true);
  assert.equal(by.get("graph")?.open, false);
});

// A background tab's surface is open without being what you are looking at. The rail draws the two
// states differently, so collapsing them would make the marker meaningless.
test("a background tab is open but not current", () => {
  const ws: Workspace = {
    tabs: [
      { id: "a", pageId: "logs", title: "Log Viewer" },
      { id: "b", pageId: "graph", title: "Graph Explorer" },
    ],
    activeId: "a",
  };
  const by = new Map(sidebarItems(ws, SURFACES).map((i) => [i.pageId, i]));
  assert.equal(by.get("graph")?.open, true);
  assert.equal(by.get("graph")?.current, false);
});

// activeId can name a tab that is no longer in the list (a close that raced a restore). Nothing may
// be current then, and the rail must still render every row rather than throwing.
test("an activeId with no tab leaves nothing current", () => {
  const ws: Workspace = {
    tabs: [{ id: "a", pageId: "logs", title: "Log Viewer" }],
    activeId: "gone",
  };
  const items = sidebarItems(ws, SURFACES);
  assert.equal(
    items.some((i) => i.current),
    false,
  );
  assert.equal(items.find((i) => i.pageId === "logs")?.open, true);
});
