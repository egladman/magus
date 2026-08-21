// sidebar-dom.test.ts - what createSidebar actually renders, and what it rewrites when the workspace
// or the expanded preference changes. sidebar.test.ts covers the pure mapping behind it. happy-dom is
// registered globally by test-setup.mjs.

import assert from "node:assert/strict";
import { test } from "node:test";
import { createSidebar } from "./sidebar";
import { buildLauncher, type Launchable } from "./home";
import type { Workspace } from "./tabs";
import type { PulseView } from "./pulse";
import type { Badge } from "./badges";
import { signal } from "./view";
const SURFACES: Launchable[] = [
  { pageId: "dashboard", label: "Dashboard", hint: "What magus is doing right now" },
  { pageId: "logs", label: "Log Viewer", hint: "Read a run's captured output" },
  { pageId: "settings", label: "Settings", hint: "Console settings", utility: true },
];

function mount(ws: Workspace, expanded = false, focusedPageId: string | null = null) {
  const host = document.createElement("nav");
  const wsCell = signal<Workspace>(ws);
  const expCell = signal<boolean>(expanded);
  const pulse = signal<PulseView | null>(null);
  const focused = signal<string | null>(focusedPageId);
  const badges = signal<Record<string, Badge>>({});
  const opened: string[] = [];
  const bar = createSidebar(
    host,
    { ws: wsCell, expanded: expCell, pulse, focused, badges, surfaces: SURFACES },
    { onOpen: (id: string) => opened.push(id) },
  );
  return { host, wsCell, expCell, pulse, focused, badges, opened, bar };
}

function link(host: HTMLElement, pageId: string): HTMLButtonElement {
  const el = host.querySelector<HTMLButtonElement>(`[data-rail-surface="${pageId}"]`);
  assert.ok(el, `no rail row for ${pageId}`);
  return el;
}

test("renders one PF nav row per surface", () => {
  const { host } = mount({ tabs: [], activeId: null });
  // Count SURFACE rows, not nav items: the collapse control is a row of the same kind and would
  // otherwise be counted as a surface that does not exist.
  assert.equal(host.querySelectorAll("[data-rail-surface]").length, 3);
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
  const launcher = buildLauncher(SURFACES, () => {});
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

test("the focused surface is current, an open one is only marked open", () => {
  const { host } = mount(
    {
      tabs: [
        { id: "a", pageId: "dashboard", title: "Dashboard" },
        { id: "b", pageId: "logs", title: "Log Viewer" },
      ],
      activeId: "b",
    },
    false,
    "logs",
  );
  const logs = link(host, "logs");
  const dash = link(host, "dashboard");
  assert.equal(logs.classList.contains("pf-m-current"), true);
  assert.equal(logs.getAttribute("aria-current"), "page");
  assert.equal(dash.classList.contains("pf-m-current"), false);
  assert.equal(dash.hasAttribute("aria-current"), false);
  assert.equal(dash.hasAttribute("data-tab-open"), true);
});

test("a workspace change repaints the rows in place", () => {
  const { host, wsCell, focused } = mount(
    { tabs: [{ id: "a", pageId: "dashboard", title: "Dashboard" }], activeId: "a" },
    false,
    "dashboard",
  );
  const before = link(host, "dashboard");
  wsCell.set({ tabs: [{ id: "b", pageId: "logs", title: "Log Viewer" }], activeId: "b" });
  focused.set("logs");
  assert.equal(link(host, "dashboard"), before, "the row element was rebuilt rather than updated");
  assert.equal(before.classList.contains("pf-m-current"), false);
  assert.equal(before.hasAttribute("data-tab-open"), false);
  assert.equal(link(host, "logs").classList.contains("pf-m-current"), true);
});

// Moving focus between panes of ONE tiled tab changes nothing about which tabs exist, so the rail has
// to follow focus on its own or it keeps pointing at the pane you just left.
test("moving focus inside a tiled tab moves the current row", () => {
  const { host, focused } = mount(
    {
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
    },
    false,
    "dashboard",
  );
  assert.equal(link(host, "dashboard").classList.contains("pf-m-current"), true);
  assert.equal(link(host, "logs").classList.contains("pf-m-current"), false);
  assert.equal(link(host, "logs").hasAttribute("data-tab-open"), true, "both panes are open");

  focused.set("logs");
  assert.equal(link(host, "dashboard").classList.contains("pf-m-current"), false);
  assert.equal(link(host, "logs").classList.contains("pf-m-current"), true);
  assert.equal(
    host.querySelectorAll("#console-sidebar .pf-m-current, [data-rail-surface].pf-m-current")
      .length,
    1,
    "exactly one row is ever current",
  );
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
  const { host, focused, bar } = mount({ tabs: [], activeId: null });
  bar.destroy();
  focused.set("logs");
  assert.equal(link(host, "logs").classList.contains("pf-m-current"), false);
});

// A daemon that is not answering and a pool that is idle are DIFFERENT facts, and only one of them is
// a number. Showing a zero for the first would be a measurement the console never took.
test("no reading hides the pulse rather than showing a zero", () => {
  const { host, pulse } = mount({ tabs: [], activeId: null });
  const el = host.querySelector<HTMLElement>("#console-sidebar-pulse");
  assert.ok(el);
  assert.equal(el.hidden, true);

  pulse.set({ running: 0, queued: 0, workspaces: [], cache: null });
  assert.equal(el.hidden, false, "an idle pool is a real reading and shows");
  assert.equal(el.dataset.state, "idle");

  pulse.set(null);
  assert.equal(el.hidden, true, "losing the daemon hides it again");
});

// aria-label is only honored on an element whose role supports naming from the author; a bare div's
// generic role is not one, so without the role the spoken name is silently dropped and a collapsed
// rail reads as "3".
test("the reading is nameable and announces itself", () => {
  const { host, pulse } = mount({ tabs: [], activeId: null });
  const el = host.querySelector<HTMLElement>("#console-sidebar-pulse");
  assert.ok(el);
  assert.equal(el.getAttribute("role"), "status");
  pulse.set({ running: 1, queued: 0, workspaces: [], cache: null });
  assert.equal(el.getAttribute("aria-label"), "1 running on this daemon");
});

// It is a live region, so rewriting identical text re-announces it. A 15s poll on a steady pool must
// not narrate the same number forever.
test("an unchanged reading is not rewritten", () => {
  const { host, pulse } = mount({ tabs: [], activeId: null });
  const el = host.querySelector<HTMLElement>("#console-sidebar-pulse");
  assert.ok(el);
  pulse.set({ running: 2, queued: 0, workspaces: [], cache: null });
  const txt = el.querySelector(".pf-v6-c-nav__link-text");
  assert.ok(txt);
  let writes = 0;
  const observer = new MutationObserver(() => writes++);
  observer.observe(txt, { childList: true, characterData: true, subtree: true });
  pulse.set({ running: 2, queued: 0, workspaces: [], cache: null });
  observer.disconnect();
  assert.equal(writes, 0, "the same reading was written to the live region again");
});

test("the reading follows the pool and the rail's own width", () => {
  const { host, pulse, expCell } = mount({ tabs: [], activeId: null });
  const el = host.querySelector<HTMLElement>("#console-sidebar-pulse");
  assert.ok(el);
  const text = () => el.querySelector(".pf-v6-c-nav__link-text")?.textContent;

  pulse.set({ running: 2, queued: 0, workspaces: [], cache: null });
  assert.equal(text(), "2");
  assert.equal(el.dataset.state, "running");
  assert.equal(el.getAttribute("aria-label"), "2 running on this daemon");

  // Expanding must repaint the reading, not just the rows - the collapsed form is a bare number.
  expCell.set(true);
  assert.equal(text(), "2 running");

  pulse.set({ running: 2, queued: 5, workspaces: [], cache: null });
  assert.equal(text(), "2 running, 5 queued");
  assert.equal(el.dataset.state, "queued");
});

// A meta surface belongs at the foot, out of the path of the lenses above it - the arrangement VS Code
// and macOS sidebars both use. The flag lives on the surface list so the rail is not a second place
// deciding what counts as utility.
test("utility surfaces are pinned in their own group", () => {
  const { host } = mount({ tabs: [], activeId: null });
  const utility = host.querySelector("[data-rail-utility]");
  assert.ok(utility);
  assert.equal(utility.querySelectorAll("[data-rail-surface]").length, 1);
  assert.equal(
    utility.querySelector("[data-rail-surface]")?.getAttribute("data-rail-surface"),
    "settings",
  );
  // ...and the lenses are NOT in it.
  assert.equal(utility.querySelector('[data-rail-surface="dashboard"]'), null);
  // The utility group comes after the main list, so it renders at the foot.
  const lists = [...host.querySelectorAll(".pf-v6-c-nav__list")];
  assert.equal(lists[lists.length - 1], utility);
});

test("right-clicking a row offers to open it, here or in its own window", () => {
  const { host, opened } = mount({ tabs: [], activeId: null });
  link(host, "logs").dispatchEvent(
    new MouseEvent("contextmenu", { bubbles: true, cancelable: true }),
  );
  const menu = document.querySelector(".console-shell-railmenu");
  assert.ok(menu);
  assert.equal((menu as HTMLElement).hidden, false);
  const items = [...menu.querySelectorAll(".pf-v6-c-menu__item-text")].map((n) => n.textContent);
  assert.deepEqual(items, ["Open Log Viewer", "Open in new window"]);

  // The first item routes through the same callback a plain click uses, rather than a second path.
  menu.querySelector<HTMLButtonElement>("button")?.click();
  assert.deepEqual(opened, ["logs"]);
  assert.equal((menu as HTMLElement).hidden, true, "acting on an item closes the menu");
});

// The menu lives on <body>, so a destroyed rail that left it behind would leak a menu AND the document
// listeners that drive it.
test("destroy takes the row menu with it", () => {
  const { host, bar } = mount({ tabs: [], activeId: null });
  link(host, "logs").dispatchEvent(
    new MouseEvent("contextmenu", { bubbles: true, cancelable: true }),
  );
  assert.ok(document.querySelector(".console-shell-railmenu"));
  bar.destroy();
  assert.equal(document.querySelector(".console-shell-railmenu"), null);
});

// A badge answers "how much is waiting". Nothing waiting is not a quantity, so it carries no mark -
// a zero on the rail would be a permanent decoration that never means anything.
test("a count of zero carries no badge", () => {
  const { host, badges } = mount({ tabs: [], activeId: null });
  const badge = () => link(host, "logs").querySelector<HTMLElement>("[data-rail-badge]");
  assert.equal(badge()?.hidden, true);
  badges.set({ logs: { count: 0, noun: "runs" } });
  assert.equal(badge()?.hidden, true, "zero is not a badge");
  badges.set({ logs: { count: 3, noun: "runs" } });
  assert.equal(badge()?.hidden, false);
  assert.equal(badge()?.textContent, "3");
});

// aria-label wins over an element's contents, so a badge left as bare text is announced to nobody.
// The count has to join the row's NAME, with its noun, or a screen reader hears "Log Viewer" whether
// three things are waiting or none.
test("the count joins the row's spoken name", () => {
  const { host, badges } = mount({ tabs: [], activeId: null });
  const row = link(host, "logs");
  assert.equal(row.getAttribute("aria-label"), "Log Viewer");
  badges.set({ logs: { count: 3, noun: "changed files" } });
  assert.equal(row.getAttribute("aria-label"), "Log Viewer, 3 changed files");
  // ...and it goes back when the count does, rather than stranding a stale number in the name.
  badges.set({});
  assert.equal(row.getAttribute("aria-label"), "Log Viewer");
});

// The collapsed rail is 44px, so a four-digit count would either overflow or shrink past reading.
test("a large count is capped rather than overflowing the rail", () => {
  const { host, badges } = mount({ tabs: [], activeId: null });
  badges.set({ logs: { count: 1240, noun: "runs" } });
  assert.equal(link(host, "logs").querySelector("[data-rail-badge]")?.textContent, "99+");
  // The SPOKEN name keeps the real number - the cap is a width constraint, not a measurement.
  assert.equal(link(host, "logs").getAttribute("aria-label"), "Log Viewer, 1240 runs");
});
