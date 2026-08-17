// bigPicture.ts - the dashboard's header control row plus the "Big Picture" MODE SWITCH. The
// header holds the active-workspace switcher (an "Active workspace" ToggleGroup, shown only when
// the daemon serves more than one workspace, so the common single-workspace case stays
// unobtrusive) and the Big Picture button. Board-mode data is daemon-wide (the wire carries no
// per-workspace run attribution to filter by); the picker anchors the loaded-workspaces tile's
// highlight for now.
//
// Big Picture is a MODE, NOT A VIEW. There is deliberately no bigPictureTile() here and no second
// component tree anywhere: Big Picture is the same board tiles, a subset of them, laid out by a
// different grid and scaled up. main.ts decides which tiles survive the switch; dashboard.css does
// the rest off [data-bigpicture]. This file owns only the switch itself.
//
// It used to be a view - a tile that re-rendered the attention hero's verdict and metrics under
// different class names, plus a second workspace-card renderer over the same WorkspaceView[]. That
// meant a new metric had to be written twice, in two files and two stylesheets, and the two copies
// were already drifting (the hero linked its failing count to the failing run's log; the copy did
// not). Placing the real tiles instead means one renderer per concept, which is the rule any future
// panel has to keep: a new metric family is one new tile file plus one grid-area line.
//
// setViewMode updates both the mode signal and the document attribute; fullscreen changes feed into
// it, while route and button entry can use it without fullscreen.

import type { DashboardState, WorkspaceView } from "../state";
import { persisted } from "../../../lib/persist";
import { signal, bind, h } from "../../view";
import type { Tile } from "./card";

export type ViewMode = "board" | "bigPicture";

// viewMode is in-memory because presentation mode ends when the dashboard surface is deactivated.
export const viewMode = signal<ViewMode>("board");

// "" (not null) so the cell has one plain string type: toggleGroup below binds it directly as a
// ToggleGroup<string> cell, no null-vs-string cast at the call site. "" reads as "nothing picked yet".
// Unlike viewMode this DOES persist - which workspace an operator was looking at is worth
// remembering across a reload, the way the collapsed-card picks are.
export const activeWorkspace = persisted<string>("dashboard-active-workspace", "");

// [data-bigpicture] on the DOCUMENT element is what every stylesheet keys off: dashboard.css turns
// the panels container into the fixed canvas, and console.css suppresses the console's own chrome
// (title bar, tab strip, status bar, reference panel). It lives on the document element rather than
// on the panels container because the chrome it hides is the shell's, not the dashboard's.
//
// Kept in lockstep with the viewMode signal by setting both in one place. Nothing else may write
// either: two writers is how a mode ends up half-applied.
function setViewMode(mode: ViewMode): void {
  viewMode.set(mode);
  document.documentElement.toggleAttribute("data-bigpicture", mode === "bigPicture");
}

// pinned marks a mode entered by ROUTE rather than by the button, which changes what leaving
// fullscreen means. On a wall display the page is opened at #big-picture and left alone; someone
// toggling the browser's own fullscreen (F11) on and off must not drop that display back to the
// board, because nobody is standing there to put it back. Entered from the button, the opposite is
// true: leaving fullscreen IS the request to leave, and Escape has to work.
let pinned = false;

// enterBigPictureRoute is the no-gesture entry: a #big-picture link a TV, an HDMI stick, or a kiosk
// browser can simply be pointed at. It asks for no fullscreen at all (the API would refuse without
// a user gesture anyway) - the chrome suppression is entirely attribute-driven, so the mode is
// complete without one.
export function enterBigPictureRoute(): void {
  pinned = true;
  setViewMode("bigPicture");
}

// The single source of truth for viewMode WHILE FULLSCREEN IS DRIVING IT: whatever the browser's
// fullscreen state actually is. Module-scoped so it is wired exactly once regardless of how many
// times dashboardHeader() (and therefore mountTiles) runs across a console tab's close/reopen.
if (typeof document !== "undefined") {
  document.addEventListener("fullscreenchange", () => {
    if (document.fullscreenElement) {
      setViewMode("bigPicture");
      return;
    }
    if (!pinned) setViewMode("board");
  });
}

// wsLabel shortens a workspace root to its last path segment for the compact chip/card
// label; the full root stays available as a title tooltip.
function wsLabel(root: string): string {
  const parts = root.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || root;
}

// How many workspaces get a chip before the rest fold into the overflow menu. Three fits beside the
// label and the Big Picture button at a phone width without the row wrapping.
const VISIBLE_WORKSPACES = 3;

// wsAccessMs is a workspace's last-touched instant in epoch ms, 0 when the daemon did not report
// one, so an unreported workspace sorts to the end rather than to the front.
function wsAccessMs(w: WorkspaceView): number {
  return w.lastAccessTime ? Number(w.lastAccessTime.seconds) * 1000 : 0;
}

// overflowItem builds the "+N" chip that ends the toggle group and the menu behind it. Same PF menu
// vocabulary as the hero's failing-target chips and the activity tile's open picker, so every
// "choose one of these" on this board looks and behaves the same.
function overflowItem(rest: WorkspaceView[], signal: AbortSignal): HTMLElement {
  const wrap = h("div", "pf-v6-c-toggle-group__item console-dashboard-viewbar__more");
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "pf-v6-c-toggle-group__button";
  btn.setAttribute("aria-haspopup", "menu");
  btn.setAttribute("aria-expanded", "false");
  // The count, not a bare ellipsis: "+4" says how much is hidden, which is the thing a reader
  // wants before deciding whether opening the menu is worth it.
  btn.append(h("span", "pf-v6-c-toggle-group__text", "+" + rest.length));
  btn.title = rest.length + " more workspace" + (rest.length === 1 ? "" : "s");

  const menu = h("div", "pf-v6-c-menu console-dashboard-viewbar__moremenu");
  menu.hidden = true;
  menu.setAttribute("role", "menu");
  const list = h("ul", "pf-v6-c-menu__list");
  const content = h("div", "pf-v6-c-menu__content");
  content.append(list);
  menu.append(content);
  const close = (): void => {
    menu.hidden = true;
    btn.setAttribute("aria-expanded", "false");
  };
  list.replaceChildren(
    ...rest.map((w) => {
      const li = h("li", "pf-v6-c-menu__list-item");
      const item = document.createElement("button");
      item.type = "button";
      item.className = "pf-v6-c-menu__item";
      item.setAttribute("role", "menuitem");
      item.title = w.root;
      item.append(h("span", "pf-v6-c-menu__item-main", wsLabel(w.root)));
      item.addEventListener("click", () => {
        close();
        // Selecting promotes it into the visible chips on the next paint, because the picker always
        // keeps the active workspace visible.
        activeWorkspace.set(w.root);
      });
      li.append(item);
      return li;
    }),
  );
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const open = menu.hidden;
    menu.hidden = !open;
    btn.setAttribute("aria-expanded", String(open));
  });
  menu.addEventListener("click", (ev) => ev.stopPropagation());
  if (typeof document !== "undefined") {
    document.addEventListener("click", close, { signal });
    document.addEventListener(
      "keydown",
      (ev) => {
        if (ev.key === "Escape") close();
      },
      { signal },
    );
  }
  wrap.append(btn, menu);
  return wrap;
}

// A small PF ToggleGroup builder for the workspace picker: N labeled buttons, one selected at a
// time, painted from a persisted cell.
// The caller names the group (a visible label element it owns, via aria-labelledby), so this no
// longer takes an ariaLabel of its own - one control, one name.
interface ToggleGroup {
  el: HTMLElement;
  destroy(): void;
}

function toggleGroup<T extends string>(
  items: { value: T; label: string; title?: string }[],
  cell: { get(): T; set(v: T): void; subscribe(fn: (v: T) => void): () => void },
): ToggleGroup {
  const root = h("div", "pf-v6-c-toggle-group");
  root.setAttribute("role", "group");
  const buttons: { btn: HTMLButtonElement; value: T }[] = [];
  for (const item of items) {
    const wrap = h("div", "pf-v6-c-toggle-group__item");
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "pf-v6-c-toggle-group__button";
    if (item.title) btn.title = item.title;
    btn.append(h("span", "pf-v6-c-toggle-group__text", item.label));
    btn.addEventListener("click", () => cell.set(item.value));
    wrap.append(btn);
    root.append(wrap);
    buttons.push({ btn, value: item.value });
  }
  const paint = (): void => {
    const current = cell.get();
    for (const { btn, value } of buttons) {
      const on = value === current;
      btn.classList.toggle("pf-m-selected", on);
      btn.setAttribute("aria-pressed", String(on));
    }
  };
  paint();
  const unsubscribe = cell.subscribe(paint);
  return { el: root, destroy: unsubscribe };
}

// bigPictureIcon is the Big Picture button's glyph: a big screen on a stand, built via
// createElementNS to match the console's shared icon convention (14x14 over a 24x24 viewBox,
// stroke on currentColor so it themes for free, aria-hidden since the button carries its own
// accessible name).
function bigPictureIcon(): SVGElement {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const screen = document.createElementNS(NS, "rect");
  screen.setAttribute("x", "2");
  screen.setAttribute("y", "4");
  screen.setAttribute("width", "20");
  screen.setAttribute("height", "13");
  screen.setAttribute("rx", "2");
  const neck = document.createElementNS(NS, "line");
  neck.setAttribute("x1", "12");
  neck.setAttribute("y1", "17");
  neck.setAttribute("x2", "12");
  neck.setAttribute("y2", "20");
  const base = document.createElementNS(NS, "line");
  base.setAttribute("x1", "8");
  base.setAttribute("y1", "20");
  base.setAttribute("x2", "16");
  base.setAttribute("y2", "20");
  svg.append(screen, neck, base);
  return svg;
}

// enterBigPicture / exitBigPicture set the MODE, and treat fullscreen as decoration on top of it.
//
// That order matters and is a deliberate reversal of how this used to work. Fullscreen was the
// source of truth, so the mode could only ever be reached through requestFullscreen() - which a
// browser is free to refuse (outside a user gesture, in an iframe without `allowfullscreen`, on a
// kiosk browser that does not implement it at all). On a refusal the button silently did nothing.
// Worse, it made the whole mode unreachable from a URL: the Fullscreen API REQUIRES a user gesture,
// so a TV, an HDMI stick, or a kiosk browser pointed at a link could never enter it. Chrome-less
// has to be reachable by attribute alone; a real fullscreen is a nicety for someone at a keyboard.
//
// So: set the mode first, then ask for fullscreen and ignore the answer. Entering twice is
// harmless (the fullscreenchange listener sets the same value), and exiting always works whether or
// not a fullscreen was ever granted.
function enterBigPicture(): void {
  setViewMode("bigPicture");
  document.documentElement.requestFullscreen?.().catch(() => {});
}

function exitBigPicture(): void {
  pinned = false; // an explicit leave releases a route pin, so the button always works
  setViewMode("board");
  // Only when we actually hold one: calling exitFullscreen() otherwise rejects, and on some
  // browsers logs an unhandled-rejection warning for a no-op.
  if (document.fullscreenElement) document.exitFullscreen?.().catch(() => {});
}

export function resetBigPicture(): void {
  exitBigPicture();
}

// dashboardHeader is the dashboard's always-visible chrome row - not a Card, sitting above the
// panels (like the attention hero). It holds the active-workspace picker (left, only past a
// single workspace) and the Big Picture button (right, always present).
export function dashboardHeader(): Tile {
  const root = h("div", "console-dashboard-viewbar");
  root.setAttribute("aria-label", "Dashboard controls");

  const wsWrap = h("div", "console-dashboard-viewbar__workspaces");
  wsWrap.hidden = true;
  root.append(wsWrap);

  const viewWrap = h("div", "console-dashboard-viewbar__view");
  const viewLabel = h("span", "console-dashboard-viewbar__label", "View");
  const viewGroup = h("div", "pf-v6-c-toggle-group");
  viewGroup.setAttribute("role", "group");
  const viewItem = h("div", "pf-v6-c-toggle-group__item");
  const bigPictureBtn = document.createElement("button");
  bigPictureBtn.type = "button";
  bigPictureBtn.className = "pf-v6-c-toggle-group__button console-dashboard-viewbar__bigpicture";
  bigPictureBtn.title = "Present this dashboard full-screen, with no console chrome";
  const btnIcon = h("span", "pf-v6-c-button__icon pf-m-start");
  btnIcon.append(bigPictureIcon());
  // The label is what makes the control self-describing, so it carries the accessible name and the
  // aria-label is dropped rather than duplicating it (a label plus an aria-label that says the same
  // thing is just two names for one control, and screen readers announce the override).
  const btnText = h("span", "pf-v6-c-toggle-group__text", "Big Picture");
  bigPictureBtn.append(btnIcon, btnText);
  bigPictureBtn.addEventListener("click", () => {
    if (viewMode.get() === "bigPicture") exitBigPicture();
    else enterBigPicture();
  });
  viewItem.append(bigPictureBtn);
  viewGroup.append(viewItem);
  viewWrap.append(viewLabel, viewGroup);
  root.append(viewWrap);

  const unbindViewMode = bind(viewMode, (mode) => {
    const active = mode === "bigPicture";
    bigPictureBtn.setAttribute("aria-pressed", String(active));
    bigPictureBtn.toggleAttribute("data-active", active);
    bigPictureBtn.classList.toggle("pf-m-selected", active);
    // Inside the mode this is the ONLY way out that does not require guessing, because the button
    // is all that the hover-reveal exit strip contains and Escape does nothing when the mode was
    // entered by route rather than by fullscreen. So it says what it will do, not what mode you are
    // in: a control still reading "Big Picture" while Big Picture is on is a toggle whose label
    // describes its state, which is exactly the ambiguity someone looking for the exit cannot
    // afford.
    btnText.textContent = active ? "Exit Big Picture" : "Big Picture";
    bigPictureBtn.title = active
      ? "Leave Big Picture and return to the dashboard"
      : "Present this dashboard full-screen, with no console chrome";
  });

  let lastWorkspaces: WorkspaceView[] = [];
  let lastRoots = "";
  let destroyPicker = (): void => {};
  function renderWorkspacePicker(): void {
    const show = viewMode.get() === "board" && lastWorkspaces.length > 1;
    wsWrap.hidden = !show;
    if (!show) {
      destroyPicker();
      destroyPicker = (): void => {};
      wsWrap.replaceChildren();
      return;
    }

    // Keep the pick valid: default to the first workspace, and fall back to it if the
    // previously active root drops out of the set (e.g. it went idle and was pruned).
    if (!activeWorkspace.get() || !lastWorkspaces.some((w) => w.root === activeWorkspace.get())) {
      activeWorkspace.set(lastWorkspaces[0].root);
    }

    // Order by recency and show only the first few as chips; the rest go behind an overflow menu.
    //
    // A ToggleGroup with one chip per workspace is fine at two and unusable at ten: it is a single
    // unwrapped row sharing the bar with the Big Picture button, so every extra workspace squeezes
    // the others until the row overflows. Recency is the right cut because the workspaces an
    // operator switches between are the ones the daemon has touched lately, and the long tail is
    // typically checkouts that were adopted once and went idle.
    const ordered = [...lastWorkspaces].sort((a, b) => wsAccessMs(b) - wsAccessMs(a));
    const active = activeWorkspace.get();
    let visible = ordered.slice(0, VISIBLE_WORKSPACES);
    // The ACTIVE workspace is always a chip, even when it is not among the most recent. Otherwise
    // the one workspace whose name the reader most needs to see is the one hidden in the menu, and
    // the bar shows a selection that appears to be nothing at all.
    if (!visible.some((w) => w.root === active)) {
      const activeView = ordered.find((w) => w.root === active);
      if (activeView) visible = [...visible.slice(0, VISIBLE_WORKSPACES - 1), activeView];
    }
    const overflow = ordered.filter((w) => !visible.some((v) => v.root === w.root));

    // The memo key includes the ACTIVE root, not just the set: which workspaces are visible depends
    // on the selection, so keying on the set alone would leave a stale chip row after a switch.
    const roots = active + " " + ordered.map((w) => w.root).join("\n");
    if (roots === lastRoots) return; // same set and same selection: the bar already reflects it
    lastRoots = roots;

    // A VISIBLE label, not just the group's aria-label. Two bare chips reading "acme" and "magus"
    // say nothing about what picking one does - they could as easily be a filter, a theme, or a
    // pair of tabs. The accessible name was already there; the sighted reader was the one being
    // asked to guess. The label carries the accessible name now (aria-labelledby), so the control
    // has exactly one name rather than a visible one and a different announced one.
    destroyPicker();
    const pickerAbort = new AbortController();
    const labelId = "dash-ws-picker-label";
    const label = h("span", "console-dashboard-viewbar__label", "Workspace");
    label.id = labelId;
    const group = toggleGroup<string>(
      visible.map((w) => ({ value: w.root, label: wsLabel(w.root), title: w.root })),
      activeWorkspace,
    );
    group.el.removeAttribute("aria-label");
    group.el.setAttribute("aria-labelledby", labelId);
    if (overflow.length > 0) group.el.append(overflowItem(overflow, pickerAbort.signal));
    wsWrap.replaceChildren(label, group.el);
    destroyPicker = (): void => {
      pickerAbort.abort();
      group.destroy();
    };
  }
  // Entering/leaving Big Picture must show/hide the workspace picker immediately, not on the
  // next ~1s status tick.
  const unbindWorkspacePicker = viewMode.subscribe(renderWorkspacePicker);

  return {
    el: root,
    update(s: DashboardState) {
      lastWorkspaces = s.status ? s.status.workspaces : [];
      renderWorkspacePicker();
    },
    destroy() {
      unbindViewMode();
      unbindWorkspacePicker();
      destroyPicker();
    },
  };
}
