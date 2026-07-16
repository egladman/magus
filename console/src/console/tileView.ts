// tileView.ts - the DOM renderer for one tab's split-pane layout. A tab's body is a Pane tree
// (tiling.ts): a single leaf when un-split (the common case) or a tree of splits after the operator
// tiles it. This module renders that tree into nested CSS grids with draggable dividers, mounts a
// surface into each leaf, tracks the focused pane, and drives the pure tree ops on split / close /
// focus / drag. All the layout MATH is pure and unit-tested in tiling.ts; this file is the DOM/mount
// layer that reads the tree and calls those ops - the split from the porting-discipline checklist.
//
// Two invariants keep tiling smooth:
//   - Reconcile REUSES cached pane and split elements by id (moving a node with append() preserves
//     it), so re-rendering after a split never tears down and re-mounts an untouched surface - a
//     surface keeps its DOM, scroll, and stream across a layout change.
//   - A divider drag updates ONLY the dragged split container's grid template (no reconcile), so
//     panes do not move or remount 60x/sec while dragging; the final ratio persists on pointerup.
//
// An empty leaf (pageId "") renders an in-pane launcher - how a fresh split pane becomes a chosen
// surface. The focused pane owns the shared per-tab status bar (via setVisible), so a background
// streamer in another pane stays quiet, matching the single-tab behavior.

import {
  leaves, splitLeaf, closePane, setRatio, setLeafPage, pickAxis, neighborInDirection,
  type Pane, type Split, type Leaf, type Direction,
} from "./tiling";
import type { PageController } from "./page";
import { h } from "./view";

// The divider thickness (px). Kept here as the single source so the grid template and the CSS hit
// box agree; the visual line is styled in console.css.
const DIVIDER = 5;

// A surface the in-pane launcher can drop into an empty pane.
export interface TileSurface {
  pageId: string;
  label: string;
  hint: string;
}

// What the console injects: how to mount a surface into a host, how to persist the tree, and the
// launcher's surface list. mountSurface returns the surface's controller (or null if it declined /
// is unknown), which tileView drives for visibility and teardown.
export interface TileDeps {
  seed: Pane; // the tab's initial tree: a single leaf, or a restored split tree
  surfaces: TileSurface[];
  mountSurface(pageId: string, host: HTMLElement): Promise<PageController<unknown, unknown> | null>;
  onLayoutChange(tree: Pane): void; // persist (the console writes it into the tab's layout)
}

export interface TileView {
  readonly el: HTMLElement;
  split(dir?: Split["dir"]): void; // split the focused pane; dir defaults to pickAxis(aspect)
  closeFocused(): boolean; // close the focused pane; returns true when it was the LAST pane (tab empties)
  focus(dir: Direction): void; // move focus to the nearest pane in a screen direction
  setVisible(visible: boolean): void; // the tab became active (true) or was hidden by another (false)
  deactivate(): void; // tear down every mounted surface
}

// One mounted pane: its host element (the surface, or the launcher for an empty leaf), the surface
// controller once resolved, and the surface it currently holds (to detect a pageId change).
interface PaneRuntime {
  host: HTMLElement;
  controller: PageController<unknown, unknown> | null;
  pageId: string;
}

// splitById finds a split node by id - the drag handler needs the live node to read its direction
// and updated ratio. A read helper (leaves() is its leaf-side sibling), kept local since only the
// renderer needs it.
function splitById(root: Pane, id: string): Split | null {
  if (root.kind === "leaf") return null;
  if (root.id === id) return root;
  return splitById(root.a, id) ?? splitById(root.b, id);
}

export function createTileView(deps: TileDeps): TileView {
  const el = h("div"); // the tab body; holds exactly one child (the rendered tree root)
  el.dataset.tileRoot = "";

  let tree: Pane = deps.seed;
  const panes = new Map<string, PaneRuntime>(); // leafId -> its mounted pane
  const splitEls = new Map<string, HTMLElement>(); // splitId -> its grid container (for live drag)
  const mounting = new Set<string>(); // leaves whose async mount is in flight (dodge double-mount)
  let focusId: string = leaves(tree)[0]?.id ?? "";
  let tabVisible = false;

  // A fresh, tree-unique id. The salt is per-tileView so ids minted this session never collide with
  // ids restored from a persisted tree (a different salt), and the counter keeps them distinct
  // within the session. Browser app code, so a random salt is fine (unlike a workflow script).
  let seq = 0;
  const salt = Math.floor(Math.random() * 1e6).toString(36);
  const newId = (prefix: string): string => prefix + salt + (seq++).toString(36);

  // commit persists the current tree through the console. Called after every structural change and
  // on drag pointerup (not per move - a drag persists once it settles).
  const commit = (): void => deps.onLayoutChange(tree);

  // treeHasSurface guards the launcher against opening a single-instance surface twice in one tab
  // (two live log viewers would fight over module state).
  const treeHasSurface = (pageId: string): boolean => leaves(tree).some((l) => l.pageId === pageId);

  // applyGrid writes a split container's CSS grid template from its direction and ratio: the a-side
  // gets `ratio` of the axis, the divider a fixed track, the b-side the rest. Used both by a full
  // render and, alone, by a live drag (so dragging never reconciles).
  function applyGrid(container: HTMLElement, split: Split): void {
    const track = `${split.ratio}fr ${DIVIDER}px ${1 - split.ratio}fr`;
    if (split.dir === "row") {
      container.style.gridTemplateColumns = track;
      container.style.gridTemplateRows = "";
    } else {
      container.style.gridTemplateRows = track;
      container.style.gridTemplateColumns = "";
    }
  }

  // makeDivider builds the draggable divider for a split. data-dir carries the split axis so CSS
  // shows the right resize cursor even at idle. Pointer capture keeps the drag alive when the cursor
  // outruns the thin hit box; the move handler recomputes the ratio from the pointer position within
  // the container and live-updates only that container's grid.
  function makeDivider(split: Split): HTMLElement {
    const splitId = split.id;
    const d = h("div");
    d.dataset.divider = splitId;
    d.dataset.dir = split.dir;
    d.addEventListener("pointerdown", (ev) => {
      ev.preventDefault();
      const container = splitEls.get(splitId);
      const live = splitById(tree, splitId);
      if (!container || !live) return;
      const axis = live.dir; // fixed for the drag; only the ratio changes
      const rect = container.getBoundingClientRect();
      d.setPointerCapture(ev.pointerId);
      const onMove = (e: PointerEvent): void => {
        const ratio = axis === "row"
          ? (e.clientX - rect.left) / rect.width
          : (e.clientY - rect.top) / rect.height;
        tree = setRatio(tree, splitId, ratio);
        const updated = splitById(tree, splitId);
        if (updated) applyGrid(container, updated);
      };
      const onUp = (): void => {
        d.removeEventListener("pointermove", onMove);
        d.removeEventListener("pointerup", onUp);
        commit(); // persist the settled ratio
      };
      d.addEventListener("pointermove", onMove);
      d.addEventListener("pointerup", onUp);
    });
    return d;
  }

  // ensurePane returns a leaf's host element, creating it (and its click-to-focus wiring) once and
  // reusing it forever after, so a reconcile moves the node rather than rebuilding it.
  function ensurePane(leaf: Leaf): HTMLElement {
    let p = panes.get(leaf.id);
    if (!p) {
      const host = h("div");
      host.dataset.paneId = leaf.id;
      host.tabIndex = -1; // focusable programmatically; a click also focuses (below)
      host.addEventListener("pointerdown", () => setFocus(leaf.id));
      p = { host, controller: null, pageId: leaf.pageId };
      panes.set(leaf.id, p);
    }
    return p.host;
  }

  // buildPane returns the DOM for a pane subtree, reusing cached elements by id. A leaf yields its
  // host; a split yields a grid container holding [a, divider, b] with the template applied.
  function buildPane(pane: Pane): HTMLElement {
    if (pane.kind === "leaf") return ensurePane(pane);
    let container = splitEls.get(pane.id);
    if (!container) {
      container = h("div");
      container.dataset.splitId = pane.id;
      splitEls.set(pane.id, container);
    }
    container.replaceChildren(buildPane(pane.a), makeDivider(pane), buildPane(pane.b));
    applyGrid(container, pane);
    return container;
  }

  // render reconciles the DOM to the current tree, then mounts/relaunches each leaf's content and
  // reapplies focus + visibility. Structural only - a drag skips this (see makeDivider).
  function render(): void {
    el.replaceChildren(buildPane(tree));
    const live = new Set(leaves(tree).map((l) => l.id));
    // Prune panes whose leaf is gone (closed): tear the surface down and drop the runtime.
    for (const [id, p] of [...panes]) {
      if (!live.has(id)) { p.controller?.deactivate(); panes.delete(id); }
    }
    // Prune split containers no longer in the tree.
    const liveSplits = new Set<string>();
    (function collect(p: Pane): void { if (p.kind === "split") { liveSplits.add(p.id); collect(p.a); collect(p.b); } })(tree);
    for (const id of [...splitEls.keys()]) if (!liveSplits.has(id)) splitEls.delete(id);
    // Now that hosts are attached and visible, fill each leaf: launcher for an empty pane, else mount.
    for (const leaf of leaves(tree)) syncLeaf(leaf);
    applyFocus();
    applyVisibility();
  }

  // syncLeaf brings a pane's content in line with its leaf: an empty pane shows the launcher; a
  // surface pane mounts once. A pageId change (launcher pick) tears the old content down first.
  function syncLeaf(leaf: Leaf): void {
    const p = panes.get(leaf.id);
    if (!p) return;
    if (p.pageId !== leaf.pageId) {
      p.controller?.deactivate();
      p.controller = null;
      p.host.replaceChildren();
      p.pageId = leaf.pageId;
    }
    if (leaf.pageId === "") { renderLauncher(leaf.id); return; }
    if (p.controller || mounting.has(leaf.id)) return;
    void mountLeaf(leaf.id);
  }

  // mountLeaf activates a surface into an attached, visible host (so a surface that measures its DOM
  // at init sees real dimensions). If the pane vanished while the mount was in flight, the resolved
  // surface is torn down at once.
  async function mountLeaf(leafId: string): Promise<void> {
    const p = panes.get(leafId);
    if (!p || p.pageId === "" || p.controller || mounting.has(leafId)) return;
    mounting.add(leafId);
    p.host.replaceChildren();
    const controller = await deps.mountSurface(p.pageId, p.host);
    mounting.delete(leafId);
    if (!panes.has(leafId)) { controller?.deactivate(); return; }
    p.controller = controller;
    applyVisibility();
  }

  // renderLauncher paints the in-pane surface picker into an empty pane. Surfaces already open in
  // this tab are omitted (single-instance). Choosing one fills the leaf and mounts it via render().
  // PatternFly (W2): the picker is a PF Gallery of clickable Cards, matching the home launcher; the
  // [data-pane-launcher] prompt + [data-open] card hooks (and the choose() behavior) are unchanged.
  function renderLauncher(leafId: string): void {
    const p = panes.get(leafId);
    if (!p || p.host.querySelector("[data-pane-launcher]")) return; // already showing
    const wrap = h("div");
    wrap.dataset.paneLauncher = "";
    wrap.append(h("p", undefined, "Open a surface in this pane"));
    const list = h("div", "pf-v6-l-gallery pf-m-gutter");
    for (const s of deps.surfaces) {
      if (treeHasSurface(s.pageId)) continue;
      const item = h("div", "pf-v6-c-card pf-m-clickable pf-m-compact");
      item.dataset.open = s.pageId;
      // A real clickable button (role + tabindex + the Enter/Space handler below); Pico's old
      // [role=button] white-on-white bleed was dropped at the W4 cutover, so this is safe now.
      item.setAttribute("role", "button");
      item.tabIndex = 0;
      item.setAttribute("aria-label", "Open " + s.label);
      const titleEl = h("div", "pf-v6-c-card__title");
      titleEl.append(h("span", "pf-v6-c-card__title-text", s.label));
      item.append(titleEl, h("div", "pf-v6-c-card__body", s.hint));
      const choose = (): void => { tree = setLeafPage(tree, leafId, s.pageId); commit(); render(); };
      item.addEventListener("click", choose);
      item.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); choose(); } });
      list.append(item);
    }
    p.host.replaceChildren(wrap, list);
  }

  // applyFocus marks the focused pane (data-focus) so CSS can ring it, but only while tiled - a lone
  // pane needs no focus affordance.
  function applyFocus(): void {
    const tiled = leaves(tree).length > 1;
    for (const [id, p] of panes) {
      if (tiled && id === focusId) p.host.dataset.focus = "";
      else delete p.host.dataset.focus;
    }
  }

  // applyVisibility tells each surface whether it OWNS the shared per-tab status bar: the focused
  // pane of a visible tab does; every other pane suppresses its shared-status writes (its tiles keep
  // updating). When the whole tab is hidden, no pane owns the bar. Mirrors the single-tab model.
  function applyVisibility(): void {
    for (const [id, p] of panes) p.controller?.setVisible?.(tabVisible && id === focusId);
  }

  // setFocus changes the focused pane and refreshes the ring + status ownership. A no-op for an
  // unknown or unchanged id.
  function setFocus(id: string): void {
    if (id === focusId || !panes.has(id)) return;
    focusId = id;
    applyFocus();
    applyVisibility();
  }

  function split(dir?: Split["dir"]): void {
    const p = panes.get(focusId);
    if (!p) return;
    const rect = p.host.getBoundingClientRect();
    const axis = dir ?? pickAxis(rect);
    const newLeafId = newId("p");
    tree = splitLeaf(tree, focusId, axis, newId("s"), { id: newLeafId, pageId: "" });
    focusId = newLeafId; // focus the fresh pane so its launcher is ready
    commit();
    render();
  }

  function closeFocused(): boolean {
    const next = closePane(tree, focusId);
    if (next === null) return true; // last pane: the console closes the whole tab
    tree = next;
    focusId = leaves(tree)[0]?.id ?? ""; // fall to the surviving subtree's first leaf
    commit();
    render(); // prunes + deactivates the closed pane
    return false;
  }

  function focus(dir: Direction): void {
    const from = panes.get(focusId);
    if (!from) return;
    const candidates = leaves(tree)
      .filter((l) => l.id !== focusId)
      .map((l) => ({ id: l.id, rect: panes.get(l.id)!.host.getBoundingClientRect() }));
    const target = neighborInDirection(from.host.getBoundingClientRect(), candidates, dir);
    if (target) { setFocus(target); panes.get(target)?.host.focus(); }
  }

  function setVisible(visible: boolean): void {
    tabVisible = visible;
    applyVisibility();
  }

  function deactivate(): void {
    for (const p of panes.values()) p.controller?.deactivate();
    panes.clear();
    splitEls.clear();
    el.replaceChildren();
  }

  render();
  return { el, split, closeFocused, focus, setVisible, deactivate };
}
