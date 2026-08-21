// viewport.ts - framing the graph in the part of the canvas the operator can actually see.
// Pure: no module state, no DOM.
//
// The canvas fills the stage, and the stage floats chrome on top of it - the legend panel down
// the left edge, the toolbar and the result line across the top. Framing against the raw canvas
// rect therefore centers the graph partly underneath that chrome, which reads as a fit that
// missed. These functions take the measured rectangles and hand back the usable box.

/** A measured rectangle in viewport (CSS pixel) coordinates, as getBoundingClientRect gives it. */
export interface Rect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/** How far chrome intrudes from each edge, in CSS pixels. */
export interface Insets {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export const NO_INSETS: Insets = { left: 0, right: 0, top: 0, bottom: 0 };

const EDGES = ["left", "right", "top", "bottom"] as const;

/**
 * overlayInsets measures how far each overlay reaches in over `viewport`.
 *
 * An overlay insets exactly ONE edge, chosen to sacrifice the least usable area. Charging every
 * edge an overlay touches would leave almost nothing for a corner element; charging none would
 * put the graph back under the legend. Nearest edge is the wrong tie-break: the toolbar sits the
 * same 10px from the top as from the right, and charging its right edge reserves a 180px-wide
 * margin for a 40px-tall button row. Area is what the framing actually spends.
 *
 * Overlays with no area (a hidden panel measures 0x0) and overlays that miss the viewport
 * entirely are ignored.
 */
export function overlayInsets(viewport: Rect, overlays: readonly Rect[]): Insets {
  const w = viewport.right - viewport.left;
  const h = viewport.bottom - viewport.top;
  const out: Insets = { ...NO_INSETS };
  for (const o of overlays) {
    if (o.right <= o.left || o.bottom <= o.top) continue;
    if (o.right <= viewport.left || o.left >= viewport.right) continue;
    if (o.bottom <= viewport.top || o.top >= viewport.bottom) continue;
    const reach = {
      left: Math.min(o.right - viewport.left, w),
      right: Math.min(viewport.right - o.left, w),
      top: Math.min(o.bottom - viewport.top, h),
      bottom: Math.min(viewport.bottom - o.top, h),
    };
    let edge: (typeof EDGES)[number] = "left";
    for (const e of EDGES) if (cost(reach[e], e, w, h) < cost(reach[edge], edge, w, h)) edge = e;
    out[edge] = Math.max(out[edge], reach[edge]);
  }
  return out;
}

// The usable area an inset of `reach` on `edge` gives up.
function cost(reach: number, edge: (typeof EDGES)[number], w: number, h: number): number {
  return edge === "left" || edge === "right" ? reach * h : reach * w;
}

/** The world-space bounding box of whatever is being framed. */
export interface WorldBox {
  minX: number;
  minY: number;
  maxX: number;
  maxY: number;
}

/** A d3-zoom transform: scale `k`, then translate by `x`/`y`. */
export interface FitTransform {
  k: number;
  x: number;
  y: number;
}

export interface FitOptions {
  pad?: number;
  minScale?: number;
  maxScale?: number;
}

/**
 * fitTransform returns the zoom transform that frames `box` inside the part of a `w` x `h`
 * viewport that `insets` leaves visible, centered in that usable box rather than in the
 * viewport. Scale is clamped to [minScale, maxScale] (default 0.1 to 8).
 *
 * A degenerate box - one point, or a span of zero on an axis - frames at maxScale rather than
 * dividing by zero. Insets larger than the viewport collapse to a minimum usable span instead
 * of going negative, so a narrow pane still produces a usable transform rather than a mirrored
 * one.
 */
export function fitTransform(
  box: WorldBox,
  w: number,
  h: number,
  insets: Insets = NO_INSETS,
  opts: FitOptions = {},
): FitTransform {
  const pad = opts.pad ?? 48;
  const minScale = opts.minScale ?? 0.1;
  const maxScale = opts.maxScale ?? 8;
  const MIN_SPAN = 40; // never let chrome squeeze the usable box to nothing

  const usableW = Math.max(MIN_SPAN, w - insets.left - insets.right);
  const usableH = Math.max(MIN_SPAN, h - insets.top - insets.bottom);
  const spanX = box.maxX - box.minX;
  const spanY = box.maxY - box.minY;

  const fitW = Math.max(1, usableW - 2 * pad);
  const fitH = Math.max(1, usableH - 2 * pad);
  const k = Math.max(
    minScale,
    Math.min(
      maxScale,
      Math.min(spanX > 0 ? fitW / spanX : maxScale, spanY > 0 ? fitH / spanY : maxScale),
    ),
  );

  // The center of the usable box, in viewport pixels.
  const vx = insets.left + usableW / 2;
  const vy = insets.top + usableH / 2;
  const cx = (box.minX + box.maxX) / 2;
  const cy = (box.minY + box.maxY) / 2;
  return { k, x: vx - cx * k, y: vy - cy * k };
}

/**
 * usableCenter returns the center of the visible box in viewport pixels - where the force
 * simulation should settle so the cold, un-fitted view lands beside the chrome rather than
 * under it. The initial transform is the identity, so viewport pixels and world units coincide.
 */
export function usableCenter(
  w: number,
  h: number,
  insets: Insets = NO_INSETS,
): { x: number; y: number } {
  const usableW = Math.max(40, w - insets.left - insets.right);
  const usableH = Math.max(40, h - insets.top - insets.bottom);
  return { x: insets.left + usableW / 2, y: insets.top + usableH / 2 };
}

/**
 * recenterOn returns `fit` moved so `focus` sits in the middle of the usable box, at the scale
 * `fit` chose. Framing a set answers where the SET is; when the operator has a node selected
 * the question is where THAT is, and only the scale is still worth taking from the fit.
 */
export function recenterOn(
  fit: FitTransform,
  focus: { x: number; y: number },
  w: number,
  h: number,
  insets: Insets = NO_INSETS,
): FitTransform {
  const c = usableCenter(w, h, insets);
  return { k: fit.k, x: c.x - focus.x * fit.k, y: c.y - focus.y * fit.k };
}
