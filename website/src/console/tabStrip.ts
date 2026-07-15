// tabStrip.ts - the DOM tab strip for the console: it renders a Workspace (tabs.ts) as a row
// of tabs and drives the pure reducers on interaction. The console owns mounting the active surface;
// this component owns only the strip UI and reports intent through callbacks (select / close / new).
//
// The tabs are <span role="tab">, NOT <button>: Pico themes button and [role=button] as a solid
// primary control (the recurring blue-bleed trap), but leaves [role=tab] alone, so the strip stays
// unthemed and we style it ourselves. tabViews is pure so the Workspace->view mapping is unit-tested;
// the DOM wiring below is a thin layer over it.

import { closeTab, setActive, type Workspace } from "./tabs";
import type { Persisted } from "../lib/persist";
import { bind, scope } from "./view";

// A tab as the strip renders it: identity, label, and whether it is the active surface.
export interface TabView {
  id: string;
  title: string;
  active: boolean;
}

// tabViews maps a Workspace to the per-tab view models the strip renders. Pure.
export function tabViews(ws: Workspace): TabView[] {
  return ws.tabs.map((t) => ({ id: t.id, title: t.title, active: t.id === ws.activeId }));
}

// Callbacks the console supplies: a tab became active (mount/show it), a tab closed (unmount it), or
// the new-tab affordance was used (the console opens a surface picker).
export interface TabStripCallbacks {
  onSelect(id: string): void;
  onClose(id: string): void;
  onNew(): void;
}

export interface TabStrip {
  readonly el: HTMLElement;
  refresh(): void; // re-render from the current workspace (e.g. after the console opens a tab)
  destroy(): void; // drop the workspace subscription
}

// closeIcon returns a small X, matching the console's inline-SVG icon convention (avoids a
// non-ASCII glyph in source and themes with currentColor).
function closeIcon(): SVGElement {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", "M6 6l12 12M18 6L6 18");
  path.setAttribute("stroke", "currentColor");
  path.setAttribute("stroke-width", "2");
  path.setAttribute("stroke-linecap", "round");
  svg.append(path);
  return svg;
}

// createTabStrip builds the strip bound to the persisted workspace: interactions read-modify-write
// it through the tabs.ts reducers, then re-render. It subscribes to the cell so a change elsewhere
// (another browser tab, or the console opening a surface) reflects here too.
export function createTabStrip(ws: Persisted<Workspace>, cb: TabStripCallbacks): TabStrip {
  const strip = document.createElement("div");
  strip.setAttribute("role", "tablist"); // styled via #console-tabs [role=tablist] - no class

  const select = (id: string): void => {
    ws.set(setActive(ws.get(), id));
    cb.onSelect(id);
    render();
  };
  const close = (id: string): void => {
    const next = closeTab(ws.get(), id);
    ws.set(next);
    cb.onClose(id);
    if (next.activeId) cb.onSelect(next.activeId);
    render();
  };

  function render(): void {
    strip.replaceChildren();
    for (const v of tabViews(ws.get())) {
      // No classes: role=tab + aria-selected drive the styling (#console-tabs [role=tab]
      // [aria-selected=true]); data-tab-id identifies it; the close is a [data-close] span.
      const tab = document.createElement("span");
      tab.dataset.tabId = v.id;
      tab.setAttribute("role", "tab");
      tab.setAttribute("tabindex", v.active ? "0" : "-1");
      tab.setAttribute("aria-selected", v.active ? "true" : "false");

      const label = document.createElement("span");
      label.textContent = v.title;

      const x = document.createElement("span");
      x.dataset.close = "";
      x.setAttribute("aria-label", "Close " + v.title);
      x.title = "Close";
      x.append(closeIcon());
      x.addEventListener("click", (ev) => { ev.stopPropagation(); close(v.id); });

      tab.append(label, x);
      tab.addEventListener("click", () => select(v.id));
      // Roving keyboard: Enter/Space activate, so a keyboard user can drive the strip.
      tab.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); select(v.id); }
      });
      strip.append(tab);
    }

    // The new-tab affordance. A span (not [role=button]) to dodge Pico's button theming; the console
    // decides which surface to open via onNew.
    const add = document.createElement("span");
    add.dataset.new = "";
    add.setAttribute("tabindex", "0");
    add.setAttribute("aria-label", "New tab");
    add.title = "New tab";
    add.textContent = "+";
    add.addEventListener("click", () => cb.onNew());
    add.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); cb.onNew(); } });
    strip.append(add);
  }

  // bind(ws, render) renders once now AND on every workspace change - the persisted cell already IS
  // a Signal (get/set/subscribe), so the view layer drives it directly. scope collects the
  // subscription so destroy() drops it cleanly (the reference use of the console/view primitives).
  const sc = scope();
  sc.add(bind(ws, render));
  return { el: strip, refresh: render, destroy: () => sc.dispose() };
}
