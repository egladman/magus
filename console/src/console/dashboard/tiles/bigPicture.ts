// bigPicture.ts - the dashboard's header control row plus "Big Picture" mode. The header holds
// the active-workspace switcher (an "Active workspace" ToggleGroup, shown only when the daemon
// serves more than one workspace, so the common single-workspace case stays unobtrusive) and the
// Big Picture button. Board-mode data is daemon-wide (the wire carries no per-workspace run
// attribution to filter by); the picker anchors the loaded-workspaces tile's highlight for now.
//
// Big Picture fullscreens the whole app for a TV-readable presentation view, like SteamOS. The
// button calls requestFullscreen() on document.documentElement; a single fullscreenchange listener
// is the one source of truth for viewMode, so Escape, the browser chrome, and the button itself
// all keep the fullscreen state and the swapped-in layout in sync.

import type { DashboardState, StatusView, WorkspaceView } from "../state";
import { fmtCount, fmtPct } from "../state";
import { countFailing, verdictFor } from "./attention";
import { persisted } from "../../../lib/persist";
import { signal, bind, h } from "../../view";
import type { Tile } from "./card";

export type ViewMode = "board" | "bigPicture";

// viewMode is an in-memory signal, not a persisted cell: it mirrors document.fullscreenElement,
// which the browser itself resets on reload/navigation, so persisting it would just go stale.
export const viewMode = signal<ViewMode>("board");

// "" (not null) so the cell has one plain string type: toggleGroup below binds it directly as a
// ToggleGroup<string> cell, no null-vs-string cast at the call site. "" reads as "nothing picked yet".
// Unlike viewMode this DOES persist - which workspace an operator was looking at is worth
// remembering across a reload, the way the collapsed-card picks are.
export const activeWorkspace = persisted<string>("dashboard-active-workspace", "");

// The single source of truth for viewMode: whatever the browser's fullscreen state actually is,
// module-scoped so it is wired exactly once regardless of how many times dashboardHeader() (and
// therefore mountTiles) runs across a console tab's close/reopen.
if (typeof document !== "undefined") {
  document.addEventListener("fullscreenchange", () => {
    viewMode.set(document.fullscreenElement ? "bigPicture" : "board");
  });
}

// wsLabel shortens a workspace root to its last path segment for the compact chip/card
// label; the full root stays available as a title tooltip.
function wsLabel(root: string): string {
  const parts = root.replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || root;
}

// A small PF ToggleGroup builder for the workspace picker: N labeled buttons, one selected at a
// time, painted from a persisted cell.
function toggleGroup<T extends string>(
  ariaLabel: string,
  items: { value: T; label: string; title?: string }[],
  cell: { get(): T; set(v: T): void; subscribe(fn: (v: T) => void): () => void },
): HTMLElement {
  const root = h("div", "pf-v6-c-toggle-group");
  root.setAttribute("role", "group");
  root.setAttribute("aria-label", ariaLabel);
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
  cell.subscribe(paint);
  return root;
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

// enterBigPicture / exitBigPicture drive the actual Fullscreen API. Both are best-effort: a
// browser can refuse requestFullscreen() (no user gesture, an embedding iframe without the
// `allowfullscreen` permission, etc.), and the fullscreenchange listener above is what keeps
// `viewMode` correct either way - these two never set viewMode themselves.
function enterBigPicture(): void {
  const root = document.documentElement;
  if (!root.requestFullscreen) return; // Fullscreen API unsupported: the button is a no-op
  root.requestFullscreen().catch(() => {}); // e.g. blocked outside a user gesture
}

function exitBigPicture(): void {
  if (!document.exitFullscreen) return;
  document.exitFullscreen().catch(() => {});
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

  const bigPictureBtn = document.createElement("button");
  bigPictureBtn.type = "button";
  bigPictureBtn.className = "console-dashboard-viewbar__bigpicture";
  bigPictureBtn.title = "Big Picture mode";
  bigPictureBtn.setAttribute("aria-label", "Big Picture mode");
  bigPictureBtn.append(bigPictureIcon());
  bigPictureBtn.addEventListener("click", () => {
    if (viewMode.get() === "bigPicture") exitBigPicture();
    else enterBigPicture();
  });
  root.append(bigPictureBtn);

  bind(viewMode, (mode) => {
    const active = mode === "bigPicture";
    bigPictureBtn.setAttribute("aria-pressed", String(active));
    bigPictureBtn.toggleAttribute("data-active", active);
  });

  let lastWorkspaces: WorkspaceView[] = [];
  let lastRoots = "";
  function renderWorkspacePicker(): void {
    const show = viewMode.get() === "board" && lastWorkspaces.length > 1;
    wsWrap.hidden = !show;
    if (!show) return;

    // Keep the pick valid: default to the first workspace, and fall back to it if the
    // previously active root drops out of the set (e.g. it went idle and was pruned).
    if (!activeWorkspace.get() || !lastWorkspaces.some((w) => w.root === activeWorkspace.get())) {
      activeWorkspace.set(lastWorkspaces[0].root);
    }

    const roots = lastWorkspaces.map((w) => w.root).join("\n");
    if (roots === lastRoots) return; // same set: the toggle group already reflects it
    lastRoots = roots;
    wsWrap.replaceChildren(
      toggleGroup<string>(
        "Active workspace",
        lastWorkspaces.map((w) => ({ value: w.root, label: wsLabel(w.root), title: w.root })),
        activeWorkspace,
      ),
    );
  }
  // Entering/leaving Big Picture must show/hide the workspace picker immediately, not on the
  // next ~1s status tick.
  viewMode.subscribe(renderWorkspacePicker);

  return {
    el: root,
    update(s: DashboardState) {
      lastWorkspaces = s.status ? s.status.workspaces : [];
      renderWorkspacePicker();
    },
    destroy() {},
  };
}

// bigPictureTile is the TV-friendly summary Big Picture mode swaps in: one verdict line, five
// oversized numbers, and one card per workspace. It reuses attention.ts's verdict rule so the
// two views never disagree about what "all clear" means, just at very different scales.
export function bigPictureTile(): Tile {
  const root = h("section", "console-dashboard-bigpicture");
  root.setAttribute("aria-label", "Big Picture");

  const verdict = h("h1", "console-dashboard-bigpicture__verdict");
  const detail = h("p", "console-dashboard-bigpicture__detail");
  root.append(verdict, detail);

  const metrics = h("div", "console-dashboard-bigpicture__metrics");
  function metric(label: string): { wrap: HTMLElement; n: HTMLElement } {
    const wrap = h("div", "console-dashboard-bigpicture__metric");
    const n = h("span", "console-dashboard-bigpicture__n", "-");
    wrap.append(n, h("span", "console-dashboard-bigpicture__l", label));
    metrics.append(wrap);
    return { wrap, n };
  }
  const fail = metric("failing");
  const running = metric("running");
  const queued = metric("queued");
  const hitRate = metric("cache hit rate");
  const busy = metric("pool busy");
  root.append(metrics);

  const wsGrid = h("div", "console-dashboard-bigpicture__wsgrid");
  root.append(wsGrid);

  function renderWorkspaces(workspaces: WorkspaceView[]): void {
    const cards = workspaces.map((w) => {
      const card = h("div", "console-dashboard-bigpicture__wscard");
      card.title = w.root;
      const stats =
        w.hits != null
          ? fmtCount(w.hits) + " hits, " + fmtCount(w.misses ?? 0) + " misses"
          : "idle";
      card.append(
        h("div", "console-dashboard-bigpicture__wsroot", wsLabel(w.root)),
        h("div", "console-dashboard-bigpicture__wsstats", stats),
      );
      return card;
    });
    wsGrid.replaceChildren(...cards);
  }

  function render(status: StatusView): void {
    const failing = countFailing(status);
    fail.n.textContent = String(failing);
    running.n.textContent = String(status.pool.running);
    queued.n.textContent = String(status.pool.queued);
    hitRate.n.textContent = fmtPct(status.cache.hitRate);
    busy.n.textContent =
      status.pool.capacity > 0 ? fmtPct(status.pool.running / status.pool.capacity) : "-";
    fail.wrap.dataset.n = failing > 0 ? "some" : "none";

    const v = verdictFor(status, failing);
    root.dataset.state = v.state;
    verdict.textContent = v.line;
    detail.textContent = v.sub;

    renderWorkspaces(status.workspaces);
  }

  return {
    el: root,
    update(s: DashboardState) {
      if (s.status) render(s.status);
    },
    destroy() {},
  };
}
