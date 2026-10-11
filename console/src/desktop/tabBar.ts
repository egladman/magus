// tabBar.ts - the DOM tab bar for the console: it renders a Workspace (tabs.ts) as a row
// of tabs and drives the pure reducers on interaction. The console owns mounting the active app;
// this component owns only the bar UI and reports intent through callbacks (select / close).
//
// There is no new-tab ("+") affordance: opening an app is the launcher empty state (zero tabs) or
// the command bar ("Open ...") with a tab already open, so the bar is purely the open tabs.
//
// The bar is built from PatternFly's Tabs component classes (.pf-v6-c-tabs pf-m-box, __list, __item,
// __link, __item-action) rather than hand-styled spans. The ARIA follows the WAI-ARIA tabs pattern the
// PF docs describe: a labelled tablist, tabs that name the panel they control, and the panel (the pane
// host main.ts mounts) naming the tab that labels it.
// tabViews stays pure so the Workspace->view mapping is unit-tested; the DOM wiring is a thin layer.

import { type Workspace } from "./tabs";
import type { Persisted } from "../lib/persist";
import { createContextMenu, isKeyboardContext } from "./contextMenu";
import { SPLIT_WORD, closeGlyph, svgIcon } from "./statusbar";
import { bind, scope } from "./view";

// A tab as the bar renders it: identity, the label it shows, an optional disambiguating hint
// (see disambiguate), and whether it is the active app.
export interface TabView {
  id: string;
  title: string;
  hint?: string;
  active: boolean;
}

// The ids that tie a tab to its panel. main.ts stamps the panel's id and role on the pane host.
export const tabElementId = (id: string): string => "console-tab-" + id;
export const tabPanelId = (id: string): string => "console-tabpanel-" + id;

// A title split into the part a tab shows and the part available to tell it from a same-named
// sibling. A path-shaped document title ("src/console/main.ts") yields the basename as the label
// and the directories above it as parents; anything else (an app name, an output ref) is its
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

// disambiguate is the editor behavior for same-named documents: two tabs both showing `main.ts`
// are indistinguishable, so each grows the shortest run of parent directories that tells them
// apart ("console/main.ts" vs "logs/main.ts" become the hints "console" and "logs"). The hint is
// returned SEPARATELY from the label so the bar can render it dimmed beside the file name rather
// than lengthening the name itself - the tab stays scannable at a glance.
//
// The depth is grown uniformly across a same-named group rather than minimized per tab: tabs that
// disambiguate at the same depth line up visually, and a group where one member needed a deeper
// path than its neighbor reads as arbitrary. Tabs whose label is unique get no hint at all, so
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

// kebabIcon is the vertical three-dot mark the tab-actions button wears.
function kebabIcon(): SVGElement {
  const svg = svgIcon(16);
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("stroke", "none");
  for (const cy of ["5", "12", "19"]) {
    const dot = document.createElementNS("http://www.w3.org/2000/svg", "circle");
    dot.setAttribute("cx", "12");
    dot.setAttribute("cy", cy);
    dot.setAttribute("r", "1.7");
    svg.append(dot);
  }
  return svg;
}

// The part of the bar a control belongs to, so a re-render can put focus back on the same control.
type Part = "tab" | "close" | "actions";

interface FocusKey {
  id: string;
  part: Part;
}

// The tab menu is opened for one tab from one origin; the origin is where focus returns.
interface MenuTarget {
  id: string;
  title: string;
  origin: HTMLElement;
  // Pointer coordinates when a mouse opened it, else null and the menu hangs under the origin.
  at: { x: number; y: number } | null;
}

// createTabBar builds the bar bound to the persisted workspace: interactions read-modify-write
// it through the tabs.ts reducers, then re-render. It subscribes to the cell so a change elsewhere
// (another browser tab, or the console opening an app) reflects here too.
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

  // The tab menu: right-click, the ContextMenu key or Shift+F10 on a tab, or the actions button on the
  // active tab, which is the route touch has (iOS fires no contextmenu on a long press). It carries the
  // things the always-visible close X cannot afford the width for. One menu, moved to its origin
  // (contextMenu.ts), so re-rendering the bar cannot orphan an open menu.
  const menu = createContextMenu("console-shell-tabs__menu", "Tab actions");

  const openMenu = (t: MenuTarget): void => {
    menu.open({
      origin: t.origin,
      at: t.at,
      refind: () => findControl({ id: t.id, part: "tab" }),
      items: [
        { label: "Split " + SPLIT_WORD.row.toLowerCase(), run: () => cb.onSplit(t.id, "row") },
        { label: "Split " + SPLIT_WORD.col.toLowerCase(), run: () => cb.onSplit(t.id, "col") },
        { label: "Move to new window", run: () => cb.onMoveToWindow(t.id) },
        { label: "Close " + t.title, run: () => close(t.id) },
      ],
    });
  };

  const list = document.createElement("ul");
  list.className = "pf-v6-c-tabs__list";
  list.setAttribute("role", "tablist");
  list.setAttribute("aria-label", "Open apps");
  bar.append(list);

  // findControl returns the control a FocusKey names, if it is still on the bar.
  function findControl(key: FocusKey): HTMLElement | null {
    const sel =
      key.part === "tab"
        ? "[data-tab-id]"
        : key.part === "close"
          ? "[data-tab-close]"
          : "[data-tab-actions]";
    const attr = key.part === "tab" ? "tabId" : key.part === "close" ? "tabClose" : "tabActions";
    for (const el of list.querySelectorAll<HTMLElement>(sel)) {
      if (el.dataset[attr] === key.id) return el;
    }
    return null;
  }

  // focusedControl names the control that holds focus, so render() can put it back: rebuilding the
  // list drops focus to <body>, which is what Enter on a tab used to do.
  function focusedControl(): FocusKey | null {
    const el = document.activeElement;
    if (!(el instanceof HTMLElement) || !list.contains(el)) return null;
    if (el.dataset.tabId !== undefined) return { id: el.dataset.tabId, part: "tab" };
    if (el.dataset.tabClose !== undefined) return { id: el.dataset.tabClose, part: "close" };
    if (el.dataset.tabActions !== undefined) return { id: el.dataset.tabActions, part: "actions" };
    return null;
  }

  function render(): void {
    const keep = focusedControl();
    list.replaceChildren();

    for (const v of tabViews(ws.get())) {
      // pf-m-action marks an item that carries a trailing action button (the close); pf-m-current is
      // the active tab. The li is presentational: the tab inside it is what carries the role.
      const item = document.createElement("li");
      item.className = "pf-v6-c-tabs__item pf-m-action" + (v.active ? " pf-m-current" : "");
      item.setAttribute("role", "presentation");

      const link = document.createElement("button");
      link.type = "button";
      link.className = "pf-v6-c-tabs__link";
      link.id = tabElementId(v.id);
      link.dataset.tabId = v.id;
      link.setAttribute("role", "tab");
      link.setAttribute("aria-controls", tabPanelId(v.id));
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
        hint.className = "console-shell-tabs__hint";
        hint.textContent = v.hint;
        link.append(hint);
      }
      // The full title as the hover tooltip: the label is a basename and may be elided, so this is
      // where the whole path stays available without widening the tab.
      link.title = v.hint ? v.hint + "/" + v.title : v.title;
      link.addEventListener("contextmenu", (ev) => {
        ev.preventDefault();
        // A keyboard-invoked contextmenu event carries no pointer position.
        const fromKeyboard = isKeyboardContext(ev);
        openMenu({
          id: v.id,
          title: v.title,
          origin: link,
          at: fromKeyboard ? null : { x: ev.clientX, y: ev.clientY },
        });
      });

      // Drag-to-adopt: dropping this tab onto another moves its currently-focused pane into that tab
      // as a new split pane (main.ts's moveAppToTab, via onAdoptTab). Pointer-based, mirroring the
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
        const dropped = dropTarget;
        clearTabDrop();
        link.removeAttribute("data-dragging");
        const targetId = dropped?.dataset.tabId;
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
      // Enter/Space/click, so arrowing past tabs does not thrash the outlet. The ContextMenu key and
      // Shift+F10 open the tab menu, the keyboard's right-click.
      link.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          select(v.id);
          return;
        }
        if (ev.key === "ContextMenu" || (ev.key === "F10" && ev.shiftKey)) {
          ev.preventDefault();
          openMenu({ id: v.id, title: v.title, origin: link, at: null });
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
        // Roving tabindex: the tab holding focus is the one Tab returns to.
        tabs.forEach((t, i) => t.setAttribute("tabindex", i === next ? "0" : "-1"));
        tabs[next]?.focus();
      });

      // The actions: PF renders them as plain buttons inside __item-action. The close is on every tab; the
      // actions button is on the active one only, so a tab does not spend a second hit area on the
      // phone, where the active tab is the one you are about to act on.
      const action = document.createElement("span");
      action.className = "pf-v6-c-tabs__item-action";
      if (v.active) {
        const more = document.createElement("button");
        more.type = "button";
        more.className = "pf-v6-c-button pf-m-plain";
        more.dataset.tabActions = v.id;
        more.setAttribute("aria-label", "Actions for " + v.title);
        more.setAttribute("aria-haspopup", "menu");
        more.setAttribute("aria-expanded", "false");
        more.title = "Actions";
        const micon = document.createElement("span");
        micon.className = "pf-v6-c-tabs__item-action-icon";
        micon.append(kebabIcon());
        more.append(micon);
        // Stopped here so the document-level outside-click close in the menu does not see the click
        // that opens the menu and shut it again at once.
        more.addEventListener("click", (ev) => {
          ev.stopPropagation();
          if (more.getAttribute("aria-expanded") === "true") menu.close();
          else openMenu({ id: v.id, title: v.title, origin: more, at: null });
        });
        action.append(more);
      }
      const x = document.createElement("button");
      x.type = "button";
      x.className = "pf-v6-c-button pf-m-plain";
      x.dataset.tabClose = v.id;
      x.setAttribute("aria-label", "Close " + v.title);
      x.title = "Close";
      const xicon = document.createElement("span");
      xicon.className = "pf-v6-c-tabs__item-action-icon";
      xicon.append(closeGlyph(12));
      x.append(xicon);
      x.addEventListener("click", (ev) => {
        ev.stopPropagation();
        close(v.id);
      });
      action.append(x);

      item.append(link, action);
      list.append(item);
    }

    if (keep) findControl(keep)?.focus({ preventScroll: true });
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
      menu.destroy();
    },
  };
}
