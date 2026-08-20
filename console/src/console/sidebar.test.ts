// sidebar.test.ts - the pure Workspace -> SidebarItem mapping. sidebar-dom.test.ts covers what
// createSidebar renders; this covers the rules that decide what it renders.

import assert from "node:assert/strict";
import { test } from "node:test";
import { pulseLabel, pulseTitle, sidebarItems } from "./sidebar";
import type { Launchable } from "./home";
import type { Workspace } from "./tabs";

const SURFACES: Launchable[] = [
  { pageId: "dashboard", label: "Dashboard", hint: "now" },
  { pageId: "logs", label: "Log Viewer", hint: "output" },
  { pageId: "graph", label: "Graph Explorer", hint: "graph" },
];

test("every surface gets a row, in the surface list's order", () => {
  const items = sidebarItems({ tabs: [], activeId: null }, SURFACES, null);
  assert.deepEqual(
    items.map((i) => i.pageId),
    ["dashboard", "logs", "graph"],
  );
  assert.equal(
    items.every((i) => !i.open && !i.current),
    true,
  );
});

test("a tab marks its surface open, and the focused surface current", () => {
  const ws: Workspace = {
    tabs: [
      { id: "a", pageId: "logs", title: "Log Viewer" },
      { id: "b", pageId: "graph", title: "Graph Explorer" },
    ],
    activeId: "b",
  };
  const by = new Map(sidebarItems(ws, SURFACES, "graph").map((i) => [i.pageId, i]));
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
  // A tiled tab holds BOTH surfaces, so both are open - but only the focused one is current. Marking
  // every surface in the tab lit two rows at once with nothing saying where input would land.
  const by = new Map(sidebarItems(ws, SURFACES, "logs").map((i) => [i.pageId, i]));
  assert.equal(by.get("logs")?.open, true);
  assert.equal(by.get("logs")?.current, true);
  assert.equal(by.get("dashboard")?.open, true, "the tab's other pane is open");
  assert.equal(by.get("dashboard")?.current, false, "but it is not the focused one");
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
  const by = new Map(sidebarItems(ws, SURFACES, "logs").map((i) => [i.pageId, i]));
  assert.equal(by.get("graph")?.open, true);
  assert.equal(by.get("graph")?.current, false);
});

// activeId can name a tab that is no longer in the list (a close that raced a restore). Nothing may
// be current then, and the rail must still render every row rather than throwing.
test("no focused surface leaves nothing current", () => {
  const ws: Workspace = {
    tabs: [{ id: "a", pageId: "logs", title: "Log Viewer" }],
    activeId: "gone",
  };
  const items = sidebarItems(ws, SURFACES, null);
  assert.equal(
    items.some((i) => i.current),
    false,
  );
  assert.equal(items.find((i) => i.pageId === "logs")?.open, true);
});

// Collapsed there is room for one number, and it is the running count - a queue depth means nothing
// without knowing whether anything holds the slots.
test("collapsed, the reading is the running count alone", () => {
  assert.equal(pulseLabel({ running: 3, queued: 2 }, false), "3");
  assert.equal(pulseLabel({ running: 0, queued: 0 }, false), "0");
});

// An empty queue is left OFF rather than shown as "0 queued", so the second clause appearing is
// itself the signal that work is backing up.
test("expanded, the queue joins the reading only once there is one", () => {
  assert.equal(pulseLabel({ running: 3, queued: 0 }, true), "3 running");
  assert.equal(pulseLabel({ running: 3, queued: 2 }, true), "3 running, 2 queued");
});

// Collapsed, a bare "3" is not something a screen reader can make sense of, so the full sentence is
// the accessible name in BOTH states. It says DAEMON, not workspace: the pool is daemon-wide, and a
// workspace-scoped reading would attribute a sibling workspace's runs to the one you are looking at.
test("the spoken reading is the full sentence, scoped to the daemon", () => {
  assert.equal(pulseTitle({ running: 1, queued: 0 }), "1 running on this daemon");
  assert.equal(pulseTitle({ running: 1, queued: 4 }), "1 running on this daemon, 4 queued");
});
