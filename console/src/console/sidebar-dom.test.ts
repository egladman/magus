// sidebar-dom.test.ts - what createSidebar actually renders, and what it rewrites when the workspace
// or the expanded preference changes. sidebar.test.ts covers the pure mapping behind it. happy-dom is
// registered globally by test-setup.mjs.

import assert from "node:assert/strict";
import { test } from "node:test";
import { createSidebar } from "./sidebar";
import { buildLauncher, type Launchable } from "./home";
import type { Workspace } from "./tabs";
import type { PulseView } from "./pulse";
import { signal } from "./view";
import type { Persisted } from "../lib/persist";

// The rail only reads and subscribes; the durable half is persist.ts's own business. This is that
// cell without localStorage.
function cell<T>(initial: T): Persisted<T> {
  let value = initial;
  const listeners = new Set<(v: T) => void>();
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

const SURFACES: Launchable[] = [
  { pageId: "dashboard", label: "Dashboard", hint: "What magus is doing right now" },
  { pageId: "logs", label: "Log Viewer", hint: "Read a run's captured output" },
];

function mount(ws: Workspace, expanded = false) {
  const host = document.createElement("nav");
  const wsCell = cell<Workspace>(ws);
  const expCell = cell<boolean>(expanded);
  const pulse = signal<PulseView | null>(null);
  const opened: string[] = [];
  const bar = createSidebar(host, wsCell, expCell, pulse, SURFACES, {
    onOpen: (id) => opened.push(id),
  });
  return { host, wsCell, expCell, pulse, opened, bar };
}

function link(host: HTMLElement, pageId: string): HTMLButtonElement {
  const el = host.querySelector<HTMLButtonElement>(`[data-rail-surface="${pageId}"]`);
  assert.ok(el, `no rail row for ${pageId}`);
  return el;
}

test("renders one PF nav row per surface", () => {
  const { host } = mount({ tabs: [], activeId: null });
  assert.equal(host.querySelectorAll(".pf-v6-c-nav__item").length, 2);
  assert.equal(link(host, "dashboard").querySelector("svg") != null, true);
  assert.equal(
    link(host, "dashboard").querySelector(".pf-v6-c-nav__link-text")?.textContent,
    "Dashboard",
  );
});

// Collapsed, the label is clipped by CSS rather than removed, so the button's own aria-label is the
// only thing naming the row to a screen reader. It has to be there in both states.
test("each row is named independently of its visible label", () => {
  const { host } = mount({ tabs: [], activeId: null });
  assert.equal(link(host, "logs").getAttribute("aria-label"), "Log Viewer");
  assert.equal(
    link(host, "logs").querySelector(".pf-v6-c-nav__link-text")?.textContent,
    "Log Viewer",
  );
  assert.equal(link(host, "logs").title, "Log Viewer - Read a run's captured output");
});

// The whole reason surfaceIconSvg was pulled out of the launcher: the rail and the launcher card
// mark the same app, and two hand-kept copies of eight glyphs drift the first time one is redrawn.
// Both sides are built through their REAL entry points - comparing either against surfaceIconSvg
// would only prove that function equals itself. Compared by geometry, since the two draw at
// different sizes.
test("a rail row draws the same glyph as that app's launcher card", () => {
  const { host } = mount({ tabs: [], activeId: null });
  const launcher = buildLauncher(
    SURFACES,
    () => {},
    () => {},
  );
  const geometry = (el: Element | null | undefined): string[] =>
    el
      ? [...el.querySelectorAll("path, circle, rect, polyline, line")].map(
          (n) => n.tagName + ":" + [...n.attributes].map((a) => a.name + "=" + a.value).join(","),
        )
      : [];

  for (const s of SURFACES) {
    const rail = geometry(link(host, s.pageId).querySelector("svg"));
    const card = geometry(
      launcher.querySelector(`[data-open="${s.pageId}"] .console-launcher-card__icon svg`),
    );
    assert.ok(rail.length > 0, `the rail drew no glyph for ${s.label}`);
    assert.deepEqual(rail, card, `the rail and the launcher disagree on the ${s.label} glyph`);
  }
});

test("clicking a row asks the console to open that surface", () => {
  const { host, opened } = mount({ tabs: [], activeId: null });
  link(host, "logs").click();
  assert.deepEqual(opened, ["logs"]);
});

test("the active tab's surface is current, an open one is only marked open", () => {
  const { host } = mount({
    tabs: [
      { id: "a", pageId: "dashboard", title: "Dashboard" },
      { id: "b", pageId: "logs", title: "Log Viewer" },
    ],
    activeId: "b",
  });
  const logs = link(host, "logs");
  const dash = link(host, "dashboard");
  assert.equal(logs.classList.contains("pf-m-current"), true);
  assert.equal(logs.getAttribute("aria-current"), "page");
  assert.equal(dash.classList.contains("pf-m-current"), false);
  assert.equal(dash.hasAttribute("aria-current"), false);
  assert.equal(dash.hasAttribute("data-tab-open"), true);
});

test("a workspace change repaints the rows in place", () => {
  const { host, wsCell } = mount({
    tabs: [{ id: "a", pageId: "dashboard", title: "Dashboard" }],
    activeId: "a",
  });
  const before = link(host, "dashboard");
  wsCell.set({ tabs: [{ id: "b", pageId: "logs", title: "Log Viewer" }], activeId: "b" });
  assert.equal(link(host, "dashboard"), before, "the row element was rebuilt rather than updated");
  assert.equal(before.classList.contains("pf-m-current"), false);
  assert.equal(before.hasAttribute("data-tab-open"), false);
  assert.equal(link(host, "logs").classList.contains("pf-m-current"), true);
});

test("the toggle drives the expanded cell, and the cell drives the rail", () => {
  const { host, expCell } = mount({ tabs: [], activeId: null });
  const toggle = host.querySelector<HTMLButtonElement>("#console-sidebar-toggle");
  assert.ok(toggle);
  assert.equal(host.hasAttribute("data-expanded"), false);
  assert.equal(toggle.getAttribute("aria-expanded"), "false");

  toggle.click();
  assert.equal(expCell.get(), true);
  assert.equal(host.hasAttribute("data-expanded"), true);
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  assert.equal(toggle.getAttribute("aria-label"), "Collapse the navigation rail");

  // Set from elsewhere (the palette command, another tab): the rail follows the cell, not the click.
  expCell.set(false);
  assert.equal(host.hasAttribute("data-expanded"), false);
});

test("destroy stops the rail following the workspace", () => {
  const { host, wsCell, bar } = mount({ tabs: [], activeId: null });
  bar.destroy();
  wsCell.set({ tabs: [{ id: "a", pageId: "logs", title: "Log Viewer" }], activeId: "a" });
  assert.equal(link(host, "logs").classList.contains("pf-m-current"), false);
});

// A daemon that is not answering and a pool that is idle are DIFFERENT facts, and only one of them is
// a number. Showing a zero for the first would be a measurement the console never took.
test("no reading hides the pulse rather than showing a zero", () => {
  const { host, pulse } = mount({ tabs: [], activeId: null });
  const el = host.querySelector<HTMLElement>("#console-sidebar-pulse");
  assert.ok(el);
  assert.equal(el.hidden, true);

  pulse.set({ running: 0, queued: 0 });
  assert.equal(el.hidden, false, "an idle pool is a real reading and shows");
  assert.equal(el.dataset.state, "idle");

  pulse.set(null);
  assert.equal(el.hidden, true, "losing the daemon hides it again");
});

test("the reading follows the pool and the rail's own width", () => {
  const { host, pulse, expCell } = mount({ tabs: [], activeId: null });
  const el = host.querySelector<HTMLElement>("#console-sidebar-pulse");
  assert.ok(el);
  const text = () => el.querySelector(".pf-v6-c-nav__link-text")?.textContent;

  pulse.set({ running: 2, queued: 0 });
  assert.equal(text(), "2");
  assert.equal(el.dataset.state, "running");
  assert.equal(el.getAttribute("aria-label"), "2 running in this workspace");

  // Expanding must repaint the reading, not just the rows - the collapsed form is a bare number.
  expCell.set(true);
  assert.equal(text(), "2 running");

  pulse.set({ running: 2, queued: 5 });
  assert.equal(text(), "2 running, 5 queued");
  assert.equal(el.dataset.state, "queued");
});
