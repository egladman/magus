// tileView-dom.test.ts - the tile's document-title reporting: which pane speaks for the tab, and
// when the console is (re)subscribed to it. Needs a document because tileView mounts real hosts;
// happy-dom is registered globally by test-setup.mjs. The layout MATH is tested in tiling.test.ts
// and the workspace reducers in tabs.test.ts - this covers only the seam between them.

import assert from "node:assert/strict";
import { test } from "node:test";
import { createTileView, type TileDeps } from "./tileView";
import { signal } from "./view";
import type { PageController } from "./page";

// Mounts are async (the real ones are a lazy import()), so every assertion about a mounted surface
// has to come after one turn of the event loop.
const mounted = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

// A stub surface with a document title the test drives. mountSurface resolves on a microtask, like
// the real lazy import() does - which is the whole point: a pane exists before its controller.
function stubDeps(titles: Record<string, ReturnType<typeof signal<string | null>>>) {
  const seen: [string | null, string][] = [];
  const deps: TileDeps = {
    seed: { kind: "leaf", id: "p1", pageId: "logs" },
    surfaces: [
      { pageId: "logs", label: "Log Viewer", hint: "" },
      { pageId: "graph", label: "Graph Explorer", hint: "" },
    ],
    async mountSurface(pageId): Promise<PageController<unknown, unknown>> {
      return {
        search: { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) },
        docTitle: titles[pageId],
        setVisible() {},
        deactivate() {},
      };
    },
    onLayoutChange() {},
    onTitleChange(title, pageId) {
      seen.push([title, pageId]);
    },
  };
  return { deps, seen };
}

// The trap this file exists for: a pane is created BEFORE its surface resolves, so the first
// applyTitle runs against a controller-less pane. An "already subscribed" check keyed on the pane
// id alone makes the later call - the one that finally has a controller - look redundant, and the
// tile never subscribes at all. The tab then keeps its static name whatever the surface opens.
test("a surface that resolves after its pane is created still gets subscribed", async () => {
  const logs = signal<string | null>(null);
  const { deps, seen } = stubDeps({ logs });
  createTileView(deps);
  assert.deepEqual(seen, [[null, "logs"]]); // pre-mount: no controller yet, so no document
  await new Promise((r) => setTimeout(r, 0)); // let the mount resolve
  logs.set("out4f2a1c");
  assert.deepEqual(seen.at(-1), ["out4f2a1c", "logs"]);
});

test("the tile reports every later document change, not just the first", async () => {
  const logs = signal<string | null>(null);
  const { deps, seen } = stubDeps({ logs });
  createTileView(deps);
  await new Promise((r) => setTimeout(r, 0));
  logs.set("out-one");
  logs.set("out-two");
  assert.deepEqual(seen.at(-1), ["out-two", "logs"]);
});

test("closing the document reports null, so the console can restore the surface name", async () => {
  const logs = signal<string | null>(null);
  const { deps, seen } = stubDeps({ logs });
  createTileView(deps);
  await new Promise((r) => setTimeout(r, 0));
  logs.set("out4f2a1c");
  logs.set(null);
  assert.deepEqual(seen.at(-1), [null, "logs"]);
});

test("deactivate drops the subscription so a torn-down surface cannot retitle the tab", async () => {
  const logs = signal<string | null>(null);
  const { deps, seen } = stubDeps({ logs });
  const tile = createTileView(deps);
  await new Promise((r) => setTimeout(r, 0));
  tile.deactivate();
  const after = seen.length;
  logs.set("out-late");
  assert.equal(seen.length, after);
});

// A surface with no document concept (the dashboard) omits docTitle entirely; the tile must still
// report for it, because a null is what tells the console to put the static surface name back.
test("a surface without a docTitle reports null rather than staying silent", async () => {
  const { deps, seen } = stubDeps({});
  createTileView(deps);
  await new Promise((r) => setTimeout(r, 0));
  assert.deepEqual(seen.at(-1), [null, "logs"]);
});

// --- a tiled tab: the FOCUSED pane speaks for it -----------------------------------------------
// A tab can hold a tree of surfaces but the bar has room for one name, so the tab is named after
// whichever pane has focus. These drive the real split/focus/close ops rather than a single pane.

test("adopting a second surface hands the tab's name to the newly focused pane", async () => {
  const logs = signal<string | null>("out-run-1");
  const graph = signal<string | null>("magusfile.buzz");
  const { deps, seen } = stubDeps({ logs, graph });
  const tile = createTileView(deps);
  await mounted();
  assert.deepEqual(seen.at(-1), ["out-run-1", "logs"]);
  tile.adopt("graph"); // splits and focuses the new pane
  await mounted();
  assert.deepEqual(seen.at(-1), ["magusfile.buzz", "graph"]);
});

test("moving focus back to the first pane restores its document as the tab's name", async () => {
  const logs = signal<string | null>("out-run-1");
  const graph = signal<string | null>("magusfile.buzz");
  const { deps, seen } = stubDeps({ logs, graph });
  const tile = createTileView(deps);
  await mounted();
  tile.adopt("graph");
  await mounted();
  tile.focusLeaf("p1");
  assert.deepEqual(seen.at(-1), ["out-run-1", "logs"]);
});

// The rule that makes a tiled tab legible: a pane you are NOT looking at must not rename the tab
// under you. Without it a background log stream loading a new run would rename a tab showing the
// graph - the tiling equivalent of the shared-status-bar leak applyVisibility already guards.
test("a background pane's document change does not retitle the tab", async () => {
  const logs = signal<string | null>("out-run-1");
  const graph = signal<string | null>("magusfile.buzz");
  const { deps, seen } = stubDeps({ logs, graph });
  const tile = createTileView(deps);
  await mounted();
  tile.adopt("graph"); // logs is now the background pane
  await mounted();
  const after = seen.length;
  logs.set("out-run-2");
  assert.equal(seen.length, after, "a background pane must not emit");
  assert.deepEqual(seen.at(-1), ["magusfile.buzz", "graph"]);
});

test("only the focused pane owns visibility while a tiled tab is shown", async () => {
  const titles: Record<string, ReturnType<typeof signal<string | null>>> = {
    logs: signal<string | null>(null),
    graph: signal<string | null>(null),
  };
  const calls: [string, boolean][] = [];
  const { deps } = stubDeps(titles);
  deps.mountSurface = async (pageId) => ({
    search: { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) },
    docTitle: titles[pageId],
    setVisible(visible) {
      calls.push([pageId, visible]);
    },
    deactivate() {},
  });
  const tile = createTileView(deps);
  await mounted();
  tile.setVisible(true);
  assert.deepEqual(calls.at(-1), ["logs", true]);

  tile.adopt("graph");
  await mounted();
  assert.deepEqual(calls.slice(-2), [
    ["logs", false],
    ["graph", true],
  ]);

  tile.setVisible(false);
  assert.deepEqual(calls.at(-1), ["graph", false]);
});

test("closing the focused pane names the tab after the survivor", async () => {
  const logs = signal<string | null>("out-run-1");
  const graph = signal<string | null>("magusfile.buzz");
  const { deps, seen } = stubDeps({ logs, graph });
  const tile = createTileView(deps);
  await mounted();
  tile.adopt("graph");
  await mounted();
  const graphLeaf = tile.snapshot().focusId;
  assert.equal(tile.closeLeaf(graphLeaf), false); // not the last pane
  await mounted();
  assert.deepEqual(seen.at(-1), ["out-run-1", "logs"]);
});

// Focus moved off the closed pane, so the surface that went away must also stop being able to
// speak - the subscription follows focus, it is not left behind on a torn-down controller.
test("a closed pane's surface can no longer retitle the tab", async () => {
  const logs = signal<string | null>("out-run-1");
  const graph = signal<string | null>("magusfile.buzz");
  const { deps, seen } = stubDeps({ logs, graph });
  const tile = createTileView(deps);
  await mounted();
  tile.adopt("graph");
  await mounted();
  tile.closeLeaf(tile.snapshot().focusId);
  await mounted();
  const after = seen.length;
  graph.set("something-else.ts");
  assert.equal(seen.length, after);
});
