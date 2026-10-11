// tabBar-dom.test.ts - what createTabBar actually RENDERS. tabBar.test.ts covers the pure
// Workspace->TabView mapping (including the disambiguation rules); this covers the other half, that
// the mapping reaches the DOM: the label, the dimmed disambiguating hint, the full-path tooltip, and
// the ARIA a tablist owes a screen reader. happy-dom is registered globally by test-setup.mjs.

import assert from "node:assert/strict";
import { test } from "node:test";
import { createTabBar, tabElementId, tabPanelId, type TabBarCallbacks } from "./tabBar";
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
    (e) => e.querySelector(".console-shell-tabs__hint")?.textContent ?? null,
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
    assert.equal(el.querySelector("[data-tab-close]")?.getAttribute("aria-label"), "Close main.ts");
  });
});

// The bar binds to the workspace cell, so a rename elsewhere (the console retitling a tab after its
// app opened something) has to reach the DOM without anyone re-rendering by hand.
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

// ---- the WAI-ARIA tabs pattern ----------------------------------------------------------------

test("the strip is a labelled tablist whose items are presentational", () => {
  withBar(sameNamed, (el) => {
    const list = el.querySelector('[role="tablist"]');
    assert.equal(list?.getAttribute("aria-label"), "Open apps");
    for (const li of el.querySelectorAll(".pf-v6-c-tabs__item")) {
      assert.equal(li.getAttribute("role"), "presentation");
    }
  });
});

test("each tab has an id and names the panel it controls", () => {
  withBar(sameNamed, (el) => {
    const links = [...el.querySelectorAll<HTMLElement>('[role="tab"]')];
    assert.deepEqual(
      links.map((l) => l.id),
      [tabElementId("t1"), tabElementId("t2")],
    );
    assert.deepEqual(
      links.map((l) => l.getAttribute("aria-controls")),
      [tabPanelId("t1"), tabPanelId("t2")],
    );
  });
});

// ---- focus survives the re-render --------------------------------------------------------------

// Enter on a tab selects it, the console writes the workspace, and the bar rebuilds its list. That
// dropped focus to <body>, so the next arrow key went nowhere.
test("pressing Enter on a tab leaves focus on that tab", () => {
  const ws = cell({ tabs: [tab("t1", "a.ts"), tab("t2", "b.ts")], activeId: "t1" });
  const bar = createTabBar(ws, {
    ...noop,
    onSelect: (id) => ws.set({ ...ws.get(), activeId: id }),
  });
  document.body.append(bar.el);
  try {
    const second = bar.el.querySelector<HTMLElement>('[data-tab-id="t2"]');
    second?.focus();
    second?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    const active = document.activeElement as HTMLElement | null;
    assert.equal(active?.dataset.tabId, "t2", "focus follows the tab through the rebuild");
    assert.equal(active?.getAttribute("aria-selected"), "true");
  } finally {
    bar.destroy();
    bar.el.remove();
  }
});

// ---- the tab menu -------------------------------------------------------------------------------

function menu(): HTMLElement {
  const m = document.querySelector<HTMLElement>(".console-shell-tabs__menu");
  assert.ok(m, "the bar mounts one tab menu on <body>");
  return m;
}

test("Shift+F10 and the ContextMenu key open the tab menu on a tab, with focus inside it", () => {
  const bar = createTabBar(cell(sameNamed), noop);
  document.body.append(bar.el);
  try {
    const link = bar.el.querySelector<HTMLElement>('[data-tab-id="t2"]');
    link?.focus();
    assert.equal(menu().hidden, true);
    link?.dispatchEvent(
      new KeyboardEvent("keydown", { key: "F10", shiftKey: true, bubbles: true }),
    );
    assert.equal(menu().hidden, false);
    const items = [...menu().querySelectorAll('[role="menuitem"]')].map((i) => i.textContent);
    assert.deepEqual(items, [
      "Split side by side",
      "Split stacked",
      "Move to new window",
      "Close main.ts",
    ]);
    assert.equal(document.activeElement?.getAttribute("role"), "menuitem");
    menu().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.equal(menu().hidden, true);
    // ok(), not equal(): a failing equal() inspects both DOM nodes, which looks like a hang.
    assert.ok(document.activeElement === link, "Escape returns focus to the tab that asked");
    link?.dispatchEvent(new KeyboardEvent("keydown", { key: "ContextMenu", bubbles: true }));
    assert.equal(menu().hidden, false);
  } finally {
    bar.destroy();
    bar.el.remove();
  }
});

test("the menu has arrows: Down moves to the next item and wraps", () => {
  const bar = createTabBar(cell(sameNamed), noop);
  document.body.append(bar.el);
  try {
    const link = bar.el.querySelector<HTMLElement>('[data-tab-id="t1"]');
    link?.dispatchEvent(new KeyboardEvent("keydown", { key: "ContextMenu", bubbles: true }));
    const items = [...menu().querySelectorAll<HTMLElement>('[role="menuitem"]')];
    assert.ok(document.activeElement === items[0]);
    menu().dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    assert.ok(document.activeElement === items[1]);
    menu().dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    menu().dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    assert.ok(document.activeElement === items[items.length - 1]);
  } finally {
    bar.destroy();
    bar.el.remove();
  }
});

// iOS fires no contextmenu on a long press, so touch needs a visible way in. It sits on the active
// tab only, where the phone's strip has the room and the reader's attention is.
test("the active tab carries a visible actions button that opens the same menu", () => {
  const splits: string[] = [];
  const bar = createTabBar(cell(sameNamed), {
    ...noop,
    onSplit: (id, dir) => splits.push(id + dir),
  });
  document.body.append(bar.el);
  try {
    const buttons = bar.el.querySelectorAll<HTMLElement>("[data-tab-actions]");
    assert.equal(buttons.length, 1, "only the active tab");
    const more = buttons[0];
    assert.equal(more.dataset.tabActions, "t1");
    assert.equal(more.getAttribute("aria-label"), "Actions for main.ts");
    assert.equal(more.getAttribute("aria-haspopup"), "menu");
    assert.equal(more.getAttribute("aria-expanded"), "false");
    more.click();
    assert.equal(menu().hidden, false);
    assert.equal(more.getAttribute("aria-expanded"), "true");
    menu().querySelector<HTMLElement>('[role="menuitem"]')?.click();
    assert.deepEqual(splits, ["t1row"]);
    assert.equal(menu().hidden, true, "choosing an item closes the menu");
  } finally {
    bar.destroy();
    bar.el.remove();
  }
});
