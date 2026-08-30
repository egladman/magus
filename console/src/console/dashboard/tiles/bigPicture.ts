// bigPicture.ts - the dashboard's header control row plus the "Big Picture" MODE SWITCH. The
// header holds the active-workspace switcher (an "Active workspace" ToggleGroup, shown only when
// the daemon serves more than one workspace, so the common single-workspace case stays
// unobtrusive) and the Big Picture button.
//
// SCOPE, and it is not uniform - the wire splits in two:
//   - Attributable to a workspace: RunningTarget.workspace (every running unit of work) and
//     ActivityEvent.workspace. These CAN be filtered by the picker; today they are not.
//   - Daemon-wide by construction: Pool.capacity/running/queued and everything in the metrics
//     service. There is ONE pool and one cache behind every loaded workspace, so these are not
//     unfiltered by omission - there is no per-workspace number to show.
// The picker currently only anchors the loaded-workspaces tile's highlight.
// (This note used to say the wire carried no per-workspace attribution at all. It does, for runs and
// activity - which is what makes scoping the WORK possible while the machine stays daemon-wide.)
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

import { onWorkspaceScope, workspaceScope } from "../../../lib/scope";
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
// The dashboard no longer owns which workspace you are looking at - the browser TAB does, through the
// title bar's scope picker (lib/scope.ts). This adapter keeps the tiles that anchor on a workspace
// reading the same answer as the rest of the window, rather than a second persisted pick that could
// disagree with it. The old "dashboard-active-workspace" cell is gone: two controls for one fact is
// the ambiguity this change exists to remove.
export const activeWorkspace = {
  get: workspaceScope,
  subscribe: onWorkspaceScope,
};

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

// toggleBigPicture is the one flip both entries share: the button below and the dashboard's "b"
// key. Exported so the key does not re-derive "which way am I going" from viewMode and drift from
// the button's answer - two readers of one mode is how a toggle ends up half-applied.
export function toggleBigPicture(): void {
  if (viewMode.get() === "bigPicture") exitBigPicture();
  else enterBigPicture();
}

// dashboardHeader is the dashboard's always-visible chrome row - not a Card, sitting above the
// panels (like the attention hero). It holds the active-workspace picker (left, only past a
// single workspace) and the Big Picture button (right, always present).
export function dashboardHeader(): Tile {
  const root = h("div", "console-dashboard-viewbar");
  root.setAttribute("aria-label", "Dashboard controls");
  root.dataset.controlSize = "default";

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
  bigPictureBtn.addEventListener("click", () => toggleBigPicture());
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

  return {
    el: root,
    // The header holds no per-frame state. Its one control is the Big Picture button, which follows
    // viewMode rather than the status stream.
    update() {},
    destroy() {
      unbindViewMode();
    },
  };
}
