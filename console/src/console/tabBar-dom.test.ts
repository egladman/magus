// tabBar-dom.test.ts - what createTabBar actually RENDERS. tabBar.test.ts covers the pure
// Workspace->TabView mapping (including the disambiguation rules); this covers the other half, that
// the mapping reaches the DOM: the label, the dimmed disambiguating hint, the full-path tooltip, and
// the ARIA a tablist owes a screen reader. happy-dom is registered globally by test-setup.mjs.

import assert from "node:assert/strict";
import { test } from "node:test";
import { createTabBar, type TabBarCallbacks } from "./tabBar";
import type { Workspace } from "./tabs";
import type { Persisted } from "../lib/persist";

// The bar binds to a persisted cell, but only ever reads and subscribes; the durable half is
// persist.ts's own business (and its own tests). This is that cell without localStorage.
function cell(initial: Workspace): Persisted<Workspace> {
  let value = initial;
  const listeners = new Set<(v: Workspace) => void>();
  return {
    get: () => value,
    set(v) {
      value = v;
      for (const fn of [...listeners]) fn(v);
    },
    update(fn) {
      this.set(fn(value));
    },
    persistOnly() {},
    subscribe(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    flushed: () => Promise.resolve(),
  };
}

const noop: TabBarCallbacks = {
  onSelect() {},
  onClose() {},
  onSplit() {},
  onMoveToWindow() {},
  onAdoptTab() {},
};

const tab = (id: string, title: string) => ({ id, pageId: "diff", title });

// The recurring fixture: two tabs whose documents share a basename, which is the only shape that
// produces a hint.
const sameNamed: Workspace = {
  tabs: [tab("t1", "src/console/main.ts"), tab("t2", "src/logs/main.ts")],
  activeId: "t1",
};

// Renders a bar, hands it to `run`, then always destroys it - createTabBar mounts a context menu on
// document.body and registers document listeners, so a leaked bar would stack them across tests.
function withBar(ws: Workspace, run: (el: HTMLElement) => void): void {
  const bar = createTabBar(cell(ws), noop);
  try {
    run(bar.el);
  } finally {
    bar.destroy();
  }
}

const labels = (el: HTMLElement) =>
  [...el.querySelectorAll(".pf-v6-c-tabs__item-text")].map((e) => e.textContent);
const hints = (el: HTMLElement) =>
  [...el.querySelectorAll(".pf-v6-c-tabs__link")].map(
    (e) => e.querySelector(".console-tabbar__hint")?.textContent ?? null,
  );

test("a tab renders its document name, not the whole path it was named from", () => {
  withBar({ tabs: [tab("t1", "src/console/main.ts")], activeId: "t1" }, (el) => {
    assert.deepEqual(labels(el), ["main.ts"]);
    assert.deepEqual(hints(el), [null]); // nothing to disambiguate against
  });
});

test("same-named tabs render their disambiguating hint as a separate element", () => {
  withBar(sameNamed, (el) => {
    assert.deepEqual(labels(el), ["main.ts", "main.ts"]);
    assert.deepEqual(hints(el), ["console", "logs"]);
  });
});

// The label is a basename and the tab may be too narrow to show even that, so the whole path has to
// stay reachable somewhere. Hovering is where a browser and an editor both put it.
test("the tooltip carries the full path, not the elided label", () => {
  withBar(sameNamed, (el) => {
    const titles = [...el.querySelectorAll<HTMLElement>(".pf-v6-c-tabs__link")].map((e) => e.title);
    assert.deepEqual(titles, ["console/main.ts", "logs/main.ts"]);
  });
});

test("a tab with no hint uses its plain name as the tooltip", () => {
  withBar({ tabs: [tab("t1", "Log Viewer")], activeId: "t1" }, (el) => {
    assert.equal(el.querySelector<HTMLElement>(".pf-v6-c-tabs__link")?.title, "Log Viewer");
  });
});

// The hint is inside the link rather than beside it, so it is part of the tab's accessible name -
// a screen reader on two same-named tabs hears which is which instead of "main.ts" twice.
test("the hint is inside the tab control, so it is part of the accessible name", () => {
  withBar(sameNamed, (el) => {
    const link = el.querySelector<HTMLElement>(".pf-v6-c-tabs__link");
    assert.equal(link?.textContent, "main.tsconsole");
  });
});

test("exactly the active tab carries aria-selected and the roving tabindex", () => {
  const ws = { tabs: [tab("t1", "a.ts"), tab("t2", "b.ts")], activeId: "t2" };
  withBar(ws, (el) => {
    const links = [...el.querySelectorAll<HTMLElement>('[role="tab"]')];
    assert.deepEqual(
      links.map((e) => e.getAttribute("aria-selected")),
      ["false", "true"],
    );
    assert.deepEqual(
      links.map((e) => e.getAttribute("tabindex")),
      ["-1", "0"],
    );
  });
});

// The close button's accessible name is built from the tab title, so renaming a tab has to rename
// its close action too - otherwise "Close Log Viewer" lingers on a tab now showing a file.
test("the close button is named after the tab's current document", () => {
  withBar({ tabs: [tab("t1", "src/console/main.ts")], activeId: "t1" }, (el) => {
    assert.equal(
      el.querySelector(".pf-v6-c-tabs__item-action button")?.getAttribute("aria-label"),
      "Close main.ts",
    );
  });
});

// The bar binds to the workspace cell, so a rename elsewhere (the console retitling a tab after its
// surface opened something) has to reach the DOM without anyone re-rendering by hand.
test("renaming a tab in the workspace re-renders the bar", () => {
  const ws = cell({ tabs: [tab("t1", "Log Viewer")], activeId: "t1" });
  const bar = createTabBar(ws, noop);
  try {
    assert.deepEqual(labels(bar.el), ["Log Viewer"]);
    ws.set({ tabs: [tab("t1", "out4f2a1c")], activeId: "t1" });
    assert.deepEqual(labels(bar.el), ["out4f2a1c"]);
  } finally {
    bar.destroy();
  }
});
