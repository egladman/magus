// split.ts - the one adjustable dimension of the Big Picture canvas: how much width the left stack
// (the verdict and the run timeline) takes from the live-activity column beside it.
//
// ONE knob, snapped to whole grid columns, and that restraint is the design rather than a shortcut.
// A freeform drag would let the canvas be dragged into states the layout cannot honor - fractional
// columns, a panel too narrow to render its content, the bin-packing this mode exists for quietly
// undone - on a surface that is usually being looked at rather than used. Snapping to a column
// boundary means every reachable state is one the grid already knows how to lay out, so there is no
// such thing as a broken arrangement to get stuck in.
//
// == Forward compatibility: persist INTENT, never layout ==
//
// The tempting thing to store is the arrangement: which panel sits where, how wide each one is. It
// is also the thing that breaks the first time a panel is added or removed, because a saved
// arrangement is a claim about a set of panels that no longer exists - and it breaks silently, for
// exactly the users who have been around long enough to have customized anything.
//
// So what is stored is a single integer: how many of the twelve columns the left stack should get.
// It says what the operator WANTED, not what the layout happened to be when they wanted it. Add a
// panel, remove one, re-cut the bottom row entirely, and the preference still means the same thing
// and still applies. Nothing about the panel set is in storage, so nothing about the panel set can
// invalidate it.
//
// Two further rules make that safe:
//
//   1. A SCHEMA VERSION rides along, and a value that does not match the version this code
//      understands is discarded rather than migrated or coerced. Discarding costs one operator one
//      re-drag; guessing at the meaning of a value written by different code costs a broken canvas
//      that no amount of dragging fixes.
//   2. Every read is RANGE-CHECKED against the constraints in force right now. lib/persist's cell
//      falls back only when JSON.parse throws, so a value that is well-formed but nonsense - a 40
//      written when the range was wider, a string, a null - would otherwise sail through and be
//      handed to CSS. Bounds are code, not storage, so tightening them later automatically retires
//      any preference that no longer fits.
//
// The net contract: storage can only ever move the split within a range this build considers valid,
// or be ignored. There is no stored value that can produce a layout this code would not itself
// draw.

import {
  bigPictureSplitCell as splitCell,
  SPLIT_DEFAULT,
  SPLIT_MAX,
  SPLIT_MIN,
  SPLIT_SCHEMA as SCHEMA,
  type SplitPref,
} from "../../layoutPrefs";
import { viewMode } from "./bigPicture";

// The wide canvas is twelve columns (dashboard.css). The split is how many of them the left stack
// takes; the activity column takes the rest.
const COLUMNS = 12;

// The cell, its schema and its bounds come from layoutPrefs so the Settings surface can export
// and import this preference without re-declaring the storage key or the fallback beside it.
// Everything that INTERPRETS the stored value still lives here - readSplit below is the only
// reader, and it discards anything it does not recognize.

// readSplit is the ONLY way this module reads the preference. Everything storage can do wrong -
// an older schema, a hand-edited value, a number outside the current bounds, a type that is not a
// number at all - collapses to the default here.
export function readSplit(): number {
  const pref = splitCell.get() as Partial<SplitPref> | null;
  if (!pref || pref.v !== SCHEMA) return SPLIT_DEFAULT;
  const cols = pref.cols;
  if (typeof cols !== "number" || !Number.isInteger(cols)) return SPLIT_DEFAULT;
  if (cols < SPLIT_MIN || cols > SPLIT_MAX) return SPLIT_DEFAULT;
  return cols;
}

function writeSplit(cols: number): void {
  splitCell.set({ v: SCHEMA, cols: clamp(cols) });
}

function clamp(cols: number): number {
  return Math.min(SPLIT_MAX, Math.max(SPLIT_MIN, Math.round(cols)));
}

// apply publishes the split as two PLAIN INTEGER custom properties. grid-column takes an integer
// line number and does not accept calc(), so the derived "next line" is computed here rather than
// in the stylesheet - CSS cannot do that arithmetic on a line number.
function apply(host: HTMLElement, cols: number): void {
  host.style.setProperty("--bp-split", String(cols));
  host.style.setProperty("--bp-split-next", String(cols + 1));
}

export interface SplitHandle {
  el: HTMLElement;
  destroy(): void;
}

// mountSplitHandle adds the drag affordance and keeps the canvas in sync with the stored split.
//
// The handle is a GRID ITEM, placed by the same grid as the panels (dashboard.css puts it in the
// column the split names). It is not positioned or measured here at all.
//
// That is the technique the pane tiler already uses - tileView.ts gives its divider a real track in
// the split container's grid template rather than floating it over one - and it is worth copying
// wholesale because it makes a whole class of bug unrepresentable. This started out absolutely
// positioned and measured against the activity panel's edge on a ResizeObserver, and in that form
// it was wrong three separate ways: the observer watched a container that is `fixed; inset: 0` and
// therefore never resizes, so internal reflows never repositioned it; `left` resolves against the
// offset parent's padding box while getBoundingClientRect is viewport-relative, so the two origins
// differed by the canvas padding; and half the handle's width had to be fudged back out to center
// it on a seam. All three were re-implementations of arithmetic the grid already does correctly.
// Letting the grid place it deleted every one of them.
//
// What remains here is only what the grid cannot know: the stored preference, and turning a drag or
// an arrow key into a column count.
export function mountSplitHandle(host: HTMLElement): SplitHandle {
  const el = document.createElement("div");
  el.className = "console-dashboard-split";
  el.setAttribute("role", "separator");
  el.setAttribute("aria-orientation", "vertical");
  el.setAttribute("aria-label", "Resize the live activity column");
  el.tabIndex = 0;

  let cols = readSplit();
  apply(host, cols);

  function syncAria(): void {
    el.setAttribute("aria-valuenow", String(cols));
    el.setAttribute("aria-valuemin", String(SPLIT_MIN));
    el.setAttribute("aria-valuemax", String(SPLIT_MAX));
  }

  // reposition parks the handle on the boundary the layout actually produced. Called after any
  // change to the split, and on every resize of the canvas.
  function set(next: number): void {
    const clamped = clamp(next);
    if (clamped === cols) return;
    cols = clamped;
    apply(host, cols);
    writeSplit(cols);
    syncAria();
    // No repositioning step: publishing the new column count IS the move. The grid re-places the
    // handle along with the panels, in the same layout pass, so they cannot disagree.
  }

  // Drag. The pointer's x is converted to the NEAREST column boundary rather than to a width, which
  // is what makes the motion snap: the handle jumps between the boundaries the grid can actually
  // produce, so what is being dragged is the choice, not a pixel value that later has to be
  // rounded into one.
  function onPointerDown(ev: PointerEvent): void {
    ev.preventDefault();
    el.setPointerCapture(ev.pointerId);
    el.dataset.dragging = "";

    const move = (e: PointerEvent): void => {
      const box = host.getBoundingClientRect();
      if (box.width === 0) return;
      set(Math.round(((e.clientX - box.left) / box.width) * COLUMNS));
    };
    const up = (e: PointerEvent): void => {
      el.releasePointerCapture(e.pointerId);
      delete el.dataset.dragging;
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  }

  // Keyboard, because a separator that only responds to a drag is unusable to anyone not using a
  // mouse - and Home restores the default without needing to remember what it was.
  function onKeyDown(ev: KeyboardEvent): void {
    if (ev.key === "ArrowLeft") set(cols - 1);
    else if (ev.key === "ArrowRight") set(cols + 1);
    else if (ev.key === "Home") set(SPLIT_DEFAULT);
    else return;
    ev.preventDefault();
  }

  // Double-click resets. The one thing an operator who has dragged themselves somewhere they do not
  // like will look for, and it costs a line.
  function onDoubleClick(): void {
    set(SPLIT_DEFAULT);
  }

  el.addEventListener("pointerdown", onPointerDown);
  el.addEventListener("keydown", onKeyDown);
  el.addEventListener("dblclick", onDoubleClick);
  syncAria();

  return {
    el,
    destroy(): void {
      el.removeEventListener("pointerdown", onPointerDown);
      el.removeEventListener("keydown", onKeyDown);
      el.removeEventListener("dblclick", onDoubleClick);
    },
  };
}
