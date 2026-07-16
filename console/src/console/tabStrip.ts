// tabStrip.ts - the DOM tab strip for the console: it renders a Workspace (tabs.ts) as a row
// of tabs and drives the pure reducers on interaction. The console owns mounting the active surface;
// this component owns only the strip UI and reports intent through callbacks (select / close / new).
//
// PatternFly (W0 spike): the strip is built from PatternFly's Tabs component classes
// (.pf-v6-c-tabs pf-m-box, __list, __item, __link, __item-action, __add) rather than hand-styled
// spans - no custom presentational classes, only pf-v6-* + the app hooks (data-tab-id) and ARIA
// (role=tab/tablist, aria-selected). The console mounts strip.el into #console-tabs and only uses the
// callbacks below, so the tiling/reconcile logic is untouched: only the emitted classes changed.
// tabViews stays pure so the Workspace->view mapping is unit-tested; the DOM wiring is a thin layer.

import { type Workspace } from "./tabs";
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
  // Explicit dimensions: PF's close action normally holds a webfont <i> glyph with intrinsic size;
  // an inline <svg> without width/height collapses to 0x0, so the close X must size itself.
  svg.setAttribute("width", "12");
  svg.setAttribute("height", "12");
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
  // PatternFly Tabs root: pf-m-box gives the boxed/raised active-tab look the console wants (an app
  // tab row, not an underline nav). The <ul> is the role=tablist; the strip itself is the PF chrome.
  const strip = document.createElement("div");
  strip.className = "pf-v6-c-tabs pf-m-box";

  // The strip only REPORTS intent - the console owns the workspace mutations (activate/close) so the
  // keybindings can drive the same operations. The strip re-renders automatically because it is bound
  // to the persisted workspace (bind(ws, render) below), so a console-side ws.set reflects here.
  const select = (id: string): void => cb.onSelect(id);
  const close = (id: string): void => cb.onClose(id);

  function render(): void {
    strip.replaceChildren();

    const list = document.createElement("ul");
    list.className = "pf-v6-c-tabs__list";
    list.setAttribute("role", "tablist");

    for (const v of tabViews(ws.get())) {
      // pf-m-action marks an item that carries a trailing action button (the close); pf-m-current is
      // the active tab. data-tab-id is the app hook; role=tab + aria-selected carry the ARIA.
      const item = document.createElement("li");
      item.className = "pf-v6-c-tabs__item pf-m-action" + (v.active ? " pf-m-current" : "");

      const link = document.createElement("button");
      link.type = "button";
      link.className = "pf-v6-c-tabs__link";
      link.dataset.tabId = v.id;
      link.setAttribute("role", "tab");
      link.setAttribute("tabindex", v.active ? "0" : "-1");
      link.setAttribute("aria-selected", v.active ? "true" : "false");
      const label = document.createElement("span");
      label.className = "pf-v6-c-tabs__item-text";
      label.textContent = v.title;
      link.append(label);
      link.addEventListener("click", () => select(v.id));
      // Roving keyboard: Enter/Space activate, so a keyboard user can drive the strip.
      link.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); select(v.id); }
      });

      // The close: PF renders it as a plain button inside __item-action; the icon sits in __item-action-icon.
      const action = document.createElement("span");
      action.className = "pf-v6-c-tabs__item-action";
      const x = document.createElement("button");
      x.type = "button";
      x.className = "pf-v6-c-button pf-m-plain";
      x.setAttribute("aria-label", "Close " + v.title);
      x.title = "Close";
      const xicon = document.createElement("span");
      xicon.className = "pf-v6-c-tabs__item-action-icon";
      xicon.append(closeIcon());
      x.append(xicon);
      x.addEventListener("click", (ev) => { ev.stopPropagation(); close(v.id); });
      action.append(x);

      item.append(link, action);
      list.append(item);
    }
    strip.append(list);

    // The new-tab affordance: PF's __add slot holding a plain button. The console decides which
    // surface to open via onNew.
    const add = document.createElement("span");
    add.className = "pf-v6-c-tabs__add";
    const addBtn = document.createElement("button");
    addBtn.type = "button";
    addBtn.className = "pf-v6-c-button pf-m-plain";
    addBtn.setAttribute("aria-label", "New tab");
    addBtn.title = "New tab";
    addBtn.textContent = "+";
    addBtn.addEventListener("click", () => cb.onNew());
    add.append(addBtn);
    strip.append(add);
  }

  // bind(ws, render) renders once now AND on every workspace change - the persisted cell already IS
  // a Signal (get/set/subscribe), so the view layer drives it directly. scope collects the
  // subscription so destroy() drops it cleanly (the reference use of the console/view primitives).
  const sc = scope();
  sc.add(bind(ws, render));
  return { el: strip, refresh: render, destroy: () => sc.dispose() };
}
