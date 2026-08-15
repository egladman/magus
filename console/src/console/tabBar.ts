// tabBar.ts - the DOM tab bar for the console: it renders a Workspace (tabs.ts) as a row
// of tabs and drives the pure reducers on interaction. The console owns mounting the active surface;
// this component owns only the bar UI and reports intent through callbacks (select / close).
//
// There is no new-tab ("+") affordance: opening a surface is the launcher empty state (zero tabs) or
// the command bar ("Open ...") with a tab already open, so the bar is purely the open tabs.
//
// PatternFly (W0 spike): the bar is built from PatternFly's Tabs component classes
// (.pf-v6-c-tabs pf-m-box, __list, __item, __link, __item-action) rather than hand-styled
// spans - no custom presentational classes, only pf-v6-* + the app hooks (data-tab-id) and ARIA
// (role=tab/tablist, aria-selected). The console mounts bar.el into #console-tabs and only uses the
// callbacks below, so the tiling/reconcile logic is untouched: only the emitted classes changed.
// tabViews stays pure so the Workspace->view mapping is unit-tested; the DOM wiring is a thin layer.

import { type Workspace } from "./tabs";
import type { Persisted } from "../lib/persist";
import { bind, scope } from "./view";

// A tab as the bar renders it: identity, the label it shows, an optional disambiguating hint
// (see disambiguate), and whether it is the active surface.
export interface TabView {
  id: string;
  title: string;
  hint?: string;
  active: boolean;
}

// A title split into the part a tab shows and the part available to tell it from a same-named
// sibling. A path-shaped document title ("src/console/main.ts") yields the basename as the label
// and the directories above it as parents; anything else (a surface name, an output ref) is its
// own label with no parents, so it never gets shortened into nonsense.
interface TitleParts {
  label: string;
  parents: string[];
}

function splitTitle(title: string): TitleParts {
  const segs = title.split("/").filter((s) => s !== "");
  if (segs.length < 2) return { label: title, parents: [] };
  return { label: segs[segs.length - 1], parents: segs.slice(0, -1) };
}

// disambiguate is the editor behaviour for same-named documents: two tabs both showing `main.ts`
// are indistinguishable, so each grows the shortest run of parent directories that tells them
// apart ("console/main.ts" vs "logs/main.ts" become the hints "console" and "logs"). The hint is
// returned SEPARATELY from the label so the bar can render it dimmed beside the file name rather
// than lengthening the name itself - the tab stays scannable at a glance.
//
// The depth is grown uniformly across a same-named group rather than minimised per tab: tabs that
// disambiguate at the same depth line up visually, and a group where one member needed a deeper
// path than its neighbour reads as arbitrary. Tabs whose label is unique get no hint at all, so
// the common case (every tab showing a different thing) is unchanged.
//
// Two tabs on genuinely the same path exhaust their parents and keep identical hints - there is no
// further information to show, and inventing one would lie.
function disambiguate(parts: TitleParts[]): (string | undefined)[] {
  const groups = new Map<string, number[]>();
  for (let i = 0; i < parts.length; i++) {
    const at = groups.get(parts[i].label);
    if (at) at.push(i);
    else groups.set(parts[i].label, [i]);
  }
  const hints: (string | undefined)[] = parts.map(() => undefined);
  for (const idxs of groups.values()) {
    if (idxs.length < 2) continue;
    const maxDepth = Math.max(...idxs.map((i) => parts[i].parents.length));
    for (let depth = 1; depth <= maxDepth; depth++) {
      const suffixes = idxs.map((i) => parts[i].parents.slice(-depth).join("/"));
      const distinct = new Set(suffixes).size === suffixes.length;
      // Stop at the first depth that separates the group, or at the deepest available - past that
      // there is nothing left to add and every extra segment is noise.
      if (distinct || depth === maxDepth) {
        idxs.forEach((i, n) => {
          hints[i] = suffixes[n] || undefined;
        });
        break;
      }
    }
  }
  return hints;
}

// tabViews maps a Workspace to the per-tab view models the bar renders. Pure.
export function tabViews(ws: Workspace): TabView[] {
  const parts = ws.tabs.map((t) => splitTitle(t.title));
  const hints = disambiguate(parts);
  return ws.tabs.map((t, i) => ({
    id: t.id,
    title: parts[i].label,
    hint: hints[i],
    active: t.id === ws.activeId,
  }));
}

// Callbacks the console supplies: a tab became active (mount/show it), a tab closed (unmount it), a
// tab's tile should split (a new pane appears beside/below its currently focused one), a tab should
// move out into its own OS window (the console opens the app window and closes the tab), or a tab was
// dropped onto another tab (drag-to-adopt: the source tab's focused pane moves into the target as a
// new split pane).
export interface TabBarCallbacks {
  onSelect(id: string): void;
  onClose(id: string): void;
  onSplit(id: string, dir: "row" | "col"): void;
  onMoveToWindow(id: string): void;
  onAdoptTab(sourceId: string, targetId: string): void;
}

export interface TabBar {
  readonly el: HTMLElement;
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

// createTabBar builds the bar bound to the persisted workspace: interactions read-modify-write
// it through the tabs.ts reducers, then re-render. It subscribes to the cell so a change elsewhere
// (another browser tab, or the console opening a surface) reflects here too.
export function createTabBar(ws: Persisted<Workspace>, cb: TabBarCallbacks): TabBar {
  // PatternFly Tabs root: pf-m-box gives the boxed/raised active-tab look the console wants (an app
  // tab row, not an underline nav). The <ul> is the role=tablist; the bar itself is the PF chrome.
  const bar = document.createElement("div");
  bar.className = "pf-v6-c-tabs pf-m-box";

  // The bar only REPORTS intent - the console owns the workspace mutations (activate/close) so the
  // keybindings can drive the same operations. The bar re-renders automatically because it is bound
  // to the persisted workspace (bind(ws, render) below), so a console-side ws.set reflects here.
  const select = (id: string): void => cb.onSelect(id);
  const close = (id: string): void => cb.onClose(id);

  // Right-click (or long-press on touch) a tab for its actions - the browser-tab idiom. It carries the
  // things the always-visible close X cannot afford the width for. Built once and moved to the pointer,
  // rather than one menu per tab, so re-rendering the bar cannot orphan an open menu.
  const ctx = document.createElement("div");
  ctx.className = "pf-v6-c-menu console-tabbar__ctx";
  ctx.hidden = true;
  const ctxList = document.createElement("ul");
  ctxList.className = "pf-v6-c-menu__list";
  ctxList.setAttribute("role", "menu");
  const ctxContent = document.createElement("div");
  ctxContent.className = "pf-v6-c-menu__content";
  ctxContent.append(ctxList);
  ctx.append(ctxContent);

  const closeCtx = (): void => {
    ctx.hidden = true;
  };
  const ctxItem = (label: string, run: () => void): HTMLLIElement => {
    const li = document.createElement("li");
    li.className = "pf-v6-c-menu__list-item";
    li.setAttribute("role", "none");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-menu__item";
    b.setAttribute("role", "menuitem");
    const main = document.createElement("span");
    main.className = "pf-v6-c-menu__item-main";
    const text = document.createElement("span");
    text.className = "pf-v6-c-menu__item-text";
    text.textContent = label;
    main.append(text);
    b.append(main);
    b.addEventListener("click", () => {
      closeCtx();
      run();
    });
    li.append(b);
    return li;
  };

  const openCtx = (id: string, title: string, x: number, y: number): void => {
    ctxList.replaceChildren(
      ctxItem("Split horizontal", () => cb.onSplit(id, "row")),
      ctxItem("Split vertical", () => cb.onSplit(id, "col")),
      ctxItem("Move to new window", () => cb.onMoveToWindow(id)),
      ctxItem("Close " + title, () => close(id)),
    );
    ctx.hidden = false;
    // Place at the pointer, then pull back inside the viewport (a tab near the right edge would
    // otherwise open its menu off-screen). Measured after unhiding so the box has a real size.
    const r = ctx.getBoundingClientRect();
    ctx.style.left = Math.min(x, window.innerWidth - r.width - 4) + "px";
    ctx.style.top = Math.min(y, window.innerHeight - r.height - 4) + "px";
    ctx.querySelector<HTMLElement>("button")?.focus();
  };

  // Lives on <body>, not in the bar: render() replaceChildren()s the bar, which would tear an open menu
  // out from under the pointer. Its document listeners ride destroy()'s AbortSignal so a rebuilt bar
  // cannot stack duplicates.
  document.body.append(ctx);
  const ac = new AbortController();
  document.addEventListener(
    "click",
    (e) => {
      if (!ctx.hidden && !ctx.contains(e.target as Node)) closeCtx();
    },
    { signal: ac.signal },
  );
  document.addEventListener(
    "keydown",
    (e: KeyboardEvent) => {
      if (e.key === "Escape") closeCtx();
    },
    { signal: ac.signal },
  );

  function render(): void {
    bar.replaceChildren();

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
      // The disambiguating parent path (only present when a sibling tab shows the same name),
      // dimmed and after the name - so the name is what you read and the hint is what you fall
      // back on. Inside the link, so it is part of the tab's accessible name rather than a
      // decoration a screen reader would skip.
      if (v.hint) {
        const hint = document.createElement("span");
        hint.className = "console-tabbar__hint";
        hint.textContent = v.hint;
        link.append(hint);
      }
      // The full title as the hover tooltip: the label is a basename and may be elided, so this is
      // where the whole path stays available without widening the tab.
      link.title = v.hint ? v.hint + "/" + v.title : v.title;
      link.addEventListener("contextmenu", (ev) => {
        ev.preventDefault();
        openCtx(v.id, v.title, ev.clientX, ev.clientY);
      });

      // Drag-to-adopt: dropping this tab onto another moves its currently-focused pane into that tab
      // as a new split pane (main.ts's moveSurfaceToTab, via onAdoptTab). Pointer-based, mirroring the
      // Panes tray's own drag-to-swap (wirePaneCellDrag in main.ts) - setPointerCapture pins move/up to
      // the link the drag STARTED on regardless of where the pointer travels, so elementFromPoint (not
      // event.target) is what finds whichever tab is currently under it. Only the primary button starts
      // a drag, so right-click still reaches the contextmenu handler above undisturbed.
      let dragMoved = false;
      let dropTarget: HTMLElement | null = null;
      const clearTabDrop = (): void => {
        dropTarget?.removeAttribute("data-tab-drop");
        dropTarget = null;
      };
      let startX = 0,
        startY = 0;
      link.addEventListener("pointerdown", (ev) => {
        if (ev.button !== 0) return;
        startX = ev.clientX;
        startY = ev.clientY;
        dragMoved = false;
        link.setPointerCapture(ev.pointerId);
      });
      link.addEventListener("pointermove", (ev) => {
        if (!link.hasPointerCapture(ev.pointerId)) return;
        if (!dragMoved && Math.hypot(ev.clientX - startX, ev.clientY - startY) > 5) {
          dragMoved = true;
          link.setAttribute("data-dragging", "");
        }
        if (!dragMoved) return;
        // Scoped to a tab LINK, not to any [data-tab-id]: main.ts puts that same attribute on
        // each mounted pane container, so a bare attribute selector treats the whole content
        // area as a drop target - drag a background tab, release it anywhere over content, and
        // it adopts into the active tab instead of doing nothing.
        const under =
          document
            .elementFromPoint(ev.clientX, ev.clientY)
            ?.closest<HTMLElement>(".pf-v6-c-tabs__link[data-tab-id]") ?? null;
        const next = under && under !== link ? under : null;
        if (next !== dropTarget) {
          clearTabDrop();
          dropTarget = next;
          dropTarget?.setAttribute("data-tab-drop", "");
        }
      });
      link.addEventListener("pointerup", (ev) => {
        link.releasePointerCapture(ev.pointerId);
        const target = dropTarget;
        clearTabDrop();
        link.removeAttribute("data-dragging");
        const targetId = target?.dataset.tabId;
        if (dragMoved && targetId) cb.onAdoptTab(v.id, targetId);
      });
      link.addEventListener("pointercancel", () => {
        clearTabDrop();
        link.removeAttribute("data-dragging");
        dragMoved = false;
      });
      // A real drag must not ALSO select the source tab - the click that follows pointerup is
      // suppressed once (dragMoved reset here, not in pointerup, so the flag survives to be read here).
      // A plain click (no movement) still selects, unchanged.
      link.addEventListener("click", (ev) => {
        if (dragMoved) {
          dragMoved = false;
          ev.preventDefault();
          ev.stopPropagation();
          return;
        }
        select(v.id);
      });
      // Roving keyboard (WAI-ARIA tablist): Enter/Space activate; Left/Right move focus between
      // tabs, Home/End jump to the ends. Focus follows the arrow but activation stays on
      // Enter/Space/click, so arrowing past tabs does not thrash the outlet.
      link.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          select(v.id);
          return;
        }
        if (
          ev.key !== "ArrowLeft" &&
          ev.key !== "ArrowRight" &&
          ev.key !== "Home" &&
          ev.key !== "End"
        )
          return;
        const tabs = [...list.querySelectorAll<HTMLButtonElement>('[role="tab"]')];
        const here = tabs.indexOf(link);
        if (here < 0) return;
        ev.preventDefault();
        let next = here;
        if (ev.key === "ArrowLeft") next = (here - 1 + tabs.length) % tabs.length;
        else if (ev.key === "ArrowRight") next = (here + 1) % tabs.length;
        else if (ev.key === "Home") next = 0;
        else if (ev.key === "End") next = tabs.length - 1;
        tabs[next]?.focus();
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
      x.addEventListener("click", (ev) => {
        ev.stopPropagation();
        close(v.id);
      });
      action.append(x);

      item.append(link, action);
      list.append(item);
    }
    bar.append(list);

    // DEFERRED: tab overflow scrolling. When the bar is too narrow for every tab (many tabs, a
    // narrow window, or the reference panel pinned open) the extra tabs are clipped and unreachable.
    // A first pass added PF scroll-button chevrons over a custom overflow-scroller, but it was reverted
    // to keep this change focused - revisit as its own task (PF's own pf-m-scrollable list would not
    // respond to a programmatic scrollBy, so the scroller has to be app-owned; see git history).
  }

  // bind(ws, render) renders once now AND on every workspace change - the persisted cell already IS
  // a Signal (get/set/subscribe), so the view layer drives it directly. scope collects the
  // subscription so destroy() drops it cleanly (the reference use of the console/view primitives).
  const sc = scope();
  sc.add(bind(ws, render));
  return {
    el: bar,
    destroy: () => {
      sc.dispose();
      ac.abort();
      ctx.remove();
    },
  };
}
