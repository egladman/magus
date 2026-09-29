// interact.ts - the upgrade a static figure gets once it is on the page: viewBox zoom and pan,
// fit, focus-and-dim, hover, and keyboard travel through the nodes. It only adds listeners and
// data-* attributes; the SVG the server drew stays the figure, links and all.
//
// Deliberately NOT the graph explorer's camera: that one zooms on a plain wheel because it owns
// the whole pane, while a figure sits in a scrolling page, so here a plain wheel scrolls the page
// and only ctrl/cmd+wheel (which is also what a trackpad pinch sends) zooms.

export interface ViewBox {
  readonly x: number;
  readonly y: number;
  readonly w: number;
  readonly h: number;
}

export interface Point {
  readonly x: number;
  readonly y: number;
}

export interface ScreenRect {
  readonly left: number;
  readonly top: number;
  readonly width: number;
  readonly height: number;
}

// Relative to the figure's own size: 8x in, and out to where the figure is a quarter of the frame.
export const MIN_SCALE = 0.25;
export const MAX_SCALE = 8;
export const ZOOM_STEP = 1.25;
export const GLIDE_MS = 180;
// Gap kept around the figure when it is fitted, in figure units.
export const FIT_PAD = 16;

export function parseViewBox(s: string | null): ViewBox | null {
  if (!s) return null;
  const n = s
    .trim()
    .split(/[\s,]+/)
    .map(Number);
  if (n.length !== 4 || n.some((v) => !Number.isFinite(v)) || n[2] <= 0 || n[3] <= 0) return null;
  return { x: n[0], y: n[1], w: n[2], h: n[3] };
}

export function formatViewBox(v: ViewBox): string {
  const r = (n: number): string => String(Math.round(n * 100) / 100);
  return [r(v.x), r(v.y), r(v.w), r(v.h)].join(" ");
}

// scaleOf is how far v is zoomed relative to the figure's natural box: 2 is twice as close.
export function scaleOf(natural: ViewBox, v: ViewBox): number {
  return natural.w / v.w;
}

// zoomAbout zooms by factor (above 1 is closer) keeping the figure point p where it is on
// screen, clamped to [MIN_SCALE, MAX_SCALE] of the natural box.
export function zoomAbout(natural: ViewBox, v: ViewBox, factor: number, p: Point): ViewBox {
  const target = Math.min(MAX_SCALE, Math.max(MIN_SCALE, scaleOf(natural, v) * factor));
  const f = target / scaleOf(natural, v);
  const w = v.w / f;
  const h = v.h / f;
  return { x: p.x - (p.x - v.x) / f, y: p.y - (p.y - v.y) / f, w, h };
}

export function centerOf(v: ViewBox): Point {
  return { x: v.x + v.w / 2, y: v.y + v.h / 2 };
}

// fitBox frames the whole figure with FIT_PAD around it. The svg keeps its default
// preserveAspectRatio (xMidYMid meet), so the frame's aspect needs no correction here.
export function fitBox(natural: ViewBox): ViewBox {
  return {
    x: natural.x - FIT_PAD,
    y: natural.y - FIT_PAD,
    w: natural.w + 2 * FIT_PAD,
    h: natural.h + 2 * FIT_PAD,
  };
}

// unitsPerPixel is the figure distance one screen pixel covers under meet scaling.
export function unitsPerPixel(v: ViewBox, rect: ScreenRect): number {
  if (rect.width <= 0 || rect.height <= 0) return 1;
  return Math.max(v.w / rect.width, v.h / rect.height);
}

// clientToFigure maps a pointer position to figure units, accounting for the letterbox meet
// scaling leaves on the short axis.
export function clientToFigure(
  v: ViewBox,
  rect: ScreenRect,
  clientX: number,
  clientY: number,
): Point {
  const u = unitsPerPixel(v, rect);
  const ox = (rect.width - v.w / u) / 2;
  const oy = (rect.height - v.h / u) / 2;
  return { x: v.x + (clientX - rect.left - ox) * u, y: v.y + (clientY - rect.top - oy) * u };
}

export function panBy(v: ViewBox, rect: ScreenRect, dxPx: number, dyPx: number): ViewBox {
  const u = unitsPerPixel(v, rect);
  return { x: v.x - dxPx * u, y: v.y - dyPx * u, w: v.w, h: v.h };
}

// wheelFactor turns a ctrl/cmd wheel delta into a zoom factor. deltaMode 1 is lines, which some
// mice send; a pinch arrives as small pixel deltas.
export function wheelFactor(deltaY: number, deltaMode: number): number {
  const px = deltaMode === 1 ? deltaY * 16 : deltaY;
  return Math.exp(-px * 0.0025);
}

export function lerpBox(a: ViewBox, b: ViewBox, t: number): ViewBox {
  const k = (p: number, q: number): number => p + (q - p) * t;
  return { x: k(a.x, b.x), y: k(a.y, b.y), w: k(a.w, b.w), h: k(a.h, b.h) };
}

export function glideMs(reducedMotion: boolean): number {
  return reducedMotion ? 0 : GLIDE_MS;
}

// parseEdge reads data-edge="src->dst". Node ids never hold ">", so the first "->" splits.
export function parseEdge(s: string | null): readonly [string, string] | null {
  if (!s) return null;
  const i = s.indexOf("->");
  if (i <= 0 || i + 2 >= s.length) return null;
  return [s.slice(0, i), s.slice(i + 2)];
}

// neighbours is focus plus every node one declared edge away, in either direction. Declared
// only: edges come from the figure's own data-edge set, so focus, dim and hover never show a
// relation the figure does not draw, and a node with no edges lights only itself.
export function neighbours(
  edges: readonly (readonly [string, string])[],
  focus: string,
): Set<string> {
  const out = new Set([focus]);
  for (const [a, b] of edges) {
    if (a === focus) out.add(b);
    if (b === focus) out.add(a);
  }
  return out;
}

export interface Placed {
  readonly id: string;
  readonly x: number;
  readonly y: number;
}

// Two nodes whose tops sit this close read as one row.
const ROW_SLACK = 24;

// readingOrder is top to bottom, then left to right within a row, the order a reader scans a
// figure in, which declaration order is not once flow has laid it out.
export function readingOrder(nodes: readonly Placed[]): string[] {
  const sorted = [...nodes].sort((a, b) => a.y - b.y || a.x - b.x);
  const rows: Placed[][] = [];
  for (const n of sorted) {
    const row = rows[rows.length - 1];
    if (row && Math.abs(n.y - row[0].y) <= ROW_SLACK) row.push(n);
    else rows.push([n]);
  }
  return rows.flatMap((r) => r.sort((a, b) => a.x - b.x).map((n) => n.id));
}

export type FigureAction = "fit" | "zoom-in" | "zoom-out" | "actual" | "clear";

export interface KeyInput {
  readonly key: string;
  readonly ctrlKey: boolean;
  readonly metaKey: boolean;
  readonly altKey: boolean;
}

// figureKey maps a key pressed inside the figure. A modified key is never ours: ctrl/cmd with
// + - 0 is the browser's own page zoom, and the console's chords all carry a modifier.
export function figureKey(e: KeyInput): FigureAction | null {
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  switch (e.key) {
    case "f":
      return "fit";
    case "+":
    case "=":
      return "zoom-in";
    case "-":
    case "_":
      return "zoom-out";
    case "0":
      return "actual";
    case "Escape":
      return "clear";
    default:
      return null;
  }
}

// ---- the DOM half ------------------------------------------------------------------------

export interface FigureOptions {
  // The element that frames the svg; keys are read here, so they reach the figure only while
  // focus is inside it.
  readonly frame: HTMLElement;
  readonly svg: SVGSVGElement;
  readonly reducedMotion: () => boolean;
  // Called when the focused node changes, null when focus clears.
  readonly onFocusChange?: (id: string | null) => void;
}

export interface FigureController {
  fit(): void;
  zoom(factor: number): void;
  actual(): void;
  focusNode(id: string | null): void;
  focused(): string | null;
  // replace swaps in a new svg (a client-side re-layout) keeping the current zoom.
  replace(svg: SVGSVGElement): void;
  viewBox(): ViewBox;
  destroy(): void;
}

// The natural box is read once, before anything rewrites the viewBox.
const NATURAL = "data-natural-viewbox";

function naturalOf(svg: SVGSVGElement): ViewBox {
  const stored = parseViewBox(svg.getAttribute(NATURAL));
  if (stored) return stored;
  const vb = parseViewBox(svg.getAttribute("viewBox")) ?? {
    x: 0,
    y: 0,
    w: Number(svg.getAttribute("width")) || 800,
    h: Number(svg.getAttribute("height")) || 600,
  };
  svg.setAttribute(NATURAL, formatViewBox(vb));
  return vb;
}

function nodeElements(svg: SVGSVGElement): Element[] {
  return [...svg.querySelectorAll("[data-node]")];
}

function edgeElements(svg: SVGSVGElement): Element[] {
  return [...svg.querySelectorAll("[data-edge]")];
}

export function edgesOf(svg: SVGSVGElement): (readonly [string, string])[] {
  const out: (readonly [string, string])[] = [];
  for (const e of edgeElements(svg)) {
    const pair = parseEdge(e.getAttribute("data-edge"));
    if (pair) out.push(pair);
  }
  return out;
}

// positionOf reads a node's top-left from its first placed shape. Attributes rather than
// getBBox, which needs layout and is absent from a DOM without one.
function positionOf(el: Element): Point {
  const shape = el.querySelector("rect, polygon, path, text");
  if (!shape) return { x: 0, y: 0 };
  const x = Number(shape.getAttribute("x"));
  const y = Number(shape.getAttribute("y"));
  if (Number.isFinite(x) && Number.isFinite(y) && shape.hasAttribute("x")) return { x, y };
  const pts = (shape.getAttribute("points") ?? "")
    .trim()
    .split(/[\s,]+/)
    .map(Number);
  if (pts.length >= 2 && pts.every(Number.isFinite)) return { x: pts[0], y: pts[1] };
  return { x: 0, y: 0 };
}

// setRoving makes exactly one node tabbable, so Tab enters the figure once, travels the nodes
// in reading order through the keydown handler below, and leaves from either end.
function setRoving(nodes: readonly Element[], current: Element | null): void {
  for (const n of nodes) n.setAttribute("tabindex", n === current ? "0" : "-1");
}

export function attachFigure(opts: FigureOptions): FigureController {
  const { frame } = opts;
  let svg = opts.svg;
  let natural = naturalOf(svg);
  let current = fitBox(natural);
  let focusedId: string | null = null;
  let glide = 0;
  let order: Element[] = [];
  const ac = new AbortController();
  const on = { signal: ac.signal };

  const rect = (): ScreenRect => svg.getBoundingClientRect();
  const apply = (v: ViewBox): void => {
    current = v;
    svg.setAttribute("viewBox", formatViewBox(v));
  };
  const moveTo = (target: ViewBox): void => {
    if (glide) cancelAnimationFrame(glide);
    glide = 0;
    const ms = glideMs(opts.reducedMotion());
    if (ms === 0 || typeof requestAnimationFrame !== "function") {
      apply(target);
      return;
    }
    const from = current;
    const start = performance.now();
    const step = (now: number): void => {
      const t = Math.min(1, (now - start) / ms);
      apply(lerpBox(from, target, 1 - (1 - t) * (1 - t)));
      glide = t < 1 ? requestAnimationFrame(step) : 0;
    };
    glide = requestAnimationFrame(step);
  };

  const indexNodes = (): void => {
    const nodes = nodeElements(svg);
    const byId = new Map(nodes.map((n) => [n.getAttribute("data-node") ?? "", n]));
    order = readingOrder(
      nodes.map((n) => ({ id: n.getAttribute("data-node") ?? "", ...positionOf(n) })),
    ).flatMap((id) => {
      const el = byId.get(id);
      return el ? [el] : [];
    });
    setRoving(order, order[0] ?? null);
  };

  const paintFocus = (): void => {
    const edges = edgesOf(svg);
    if (focusedId === null) {
      svg.removeAttribute("data-focused");
      for (const el of [...nodeElements(svg), ...edgeElements(svg)]) {
        el.removeAttribute("data-dim");
        el.removeAttribute("data-lit");
      }
      return;
    }
    const keep = neighbours(edges, focusedId);
    svg.setAttribute("data-focused", focusedId);
    for (const n of nodeElements(svg)) {
      const id = n.getAttribute("data-node") ?? "";
      n.toggleAttribute("data-dim", !keep.has(id));
      n.toggleAttribute("data-lit", id === focusedId);
    }
    for (const e of edgeElements(svg)) {
      const pair = parseEdge(e.getAttribute("data-edge"));
      const touches = pair !== null && (pair[0] === focusedId || pair[1] === focusedId);
      e.toggleAttribute("data-dim", !touches);
      e.toggleAttribute("data-lit", touches);
    }
  };

  const setHot = (id: string | null): void => {
    for (const e of edgeElements(svg)) {
      const pair = parseEdge(e.getAttribute("data-edge"));
      e.toggleAttribute(
        "data-hot",
        id !== null && pair !== null && (pair[0] === id || pair[1] === id),
      );
    }
  };

  const nodeOf = (t: EventTarget | null): Element | null =>
    t instanceof Element ? t.closest("[data-node]") : null;

  const controller: FigureController = {
    fit: () => moveTo(fitBox(natural)),
    zoom: (factor) => moveTo(zoomAbout(natural, current, factor, centerOf(current))),
    actual: () => moveTo(natural),
    focusNode: (id) => {
      focusedId = id;
      paintFocus();
      opts.onFocusChange?.(id);
    },
    focused: () => focusedId,
    replace: (next) => {
      const kept = current;
      svg = next;
      natural = naturalOf(svg);
      apply(kept);
      indexNodes();
      const still = nodeElements(svg).some((n) => n.getAttribute("data-node") === focusedId);
      if (focusedId !== null && !still) controller.focusNode(null);
      else paintFocus();
    },
    viewBox: () => current,
    destroy: () => {
      if (glide) cancelAnimationFrame(glide);
      ac.abort();
    },
  };

  // Listeners sit on the frame and resolve the svg at event time, so replace() needs no rewiring.
  frame.addEventListener(
    "wheel",
    (e) => {
      if (!e.ctrlKey && !e.metaKey) return; // a plain wheel scrolls the page
      e.preventDefault();
      const p = clientToFigure(current, rect(), e.clientX, e.clientY);
      if (glide) cancelAnimationFrame(glide);
      glide = 0;
      apply(zoomAbout(natural, current, wheelFactor(e.deltaY, e.deltaMode), p));
    },
    { passive: false, signal: ac.signal },
  );

  let drag: { id: number; x: number; y: number; moved: boolean } | null = null;
  frame.addEventListener(
    "pointerdown",
    (e) => {
      if (e.button !== 0 || !svg.contains(e.target instanceof Node ? e.target : null)) return;
      drag = { id: e.pointerId, x: e.clientX, y: e.clientY, moved: false };
    },
    on,
  );
  frame.addEventListener(
    "pointermove",
    (e) => {
      if (drag && drag.id === e.pointerId) {
        const dx = e.clientX - drag.x;
        const dy = e.clientY - drag.y;
        if (!drag.moved && Math.hypot(dx, dy) < 3) return;
        // Captured only once it is a drag: a capture taken on pointerdown retargets the click,
        // and a plain click on a node has to reach its link.
        if (!drag.moved) frame.setPointerCapture?.(e.pointerId);
        drag.moved = true;
        frame.dataset.dragging = "";
        drag.x = e.clientX;
        drag.y = e.clientY;
        apply(panBy(current, rect(), dx, dy));
        return;
      }
      setHot(nodeOf(e.target)?.getAttribute("data-node") ?? null);
    },
    on,
  );
  const endDrag = (e: PointerEvent): void => {
    if (!drag || drag.id !== e.pointerId) return;
    const moved = drag.moved;
    drag = null;
    delete frame.dataset.dragging;
    frame.releasePointerCapture?.(e.pointerId);
    // A drag that ends on a link must not also follow it.
    if (moved)
      frame.addEventListener("click", (c) => c.preventDefault(), { capture: true, once: true });
  };
  frame.addEventListener("pointerup", endDrag, on);
  frame.addEventListener("pointercancel", endDrag, on);
  frame.addEventListener("pointerleave", () => setHot(null), on);

  frame.addEventListener(
    "dblclick",
    (e) => {
      const n = nodeOf(e.target);
      if (!n) return;
      e.preventDefault();
      controller.focusNode(n.getAttribute("data-node"));
    },
    on,
  );

  frame.addEventListener(
    "focusin",
    (e) => {
      const n = nodeOf(e.target);
      if (n) {
        setRoving(order, n);
        setHot(n.getAttribute("data-node"));
      }
    },
    on,
  );

  frame.addEventListener(
    "keydown",
    (e) => {
      const n = nodeOf(e.target);
      if (e.key === "Tab" && n && !e.ctrlKey && !e.metaKey && !e.altKey) {
        const i = order.indexOf(n);
        const next = order[i + (e.shiftKey ? -1 : 1)];
        if (i < 0 || !next) return; // the ends let Tab leave the figure
        e.preventDefault();
        setRoving(order, next);
        (next as HTMLElement | SVGElement).focus();
        return;
      }
      // Enter on a link is the browser's; on a node with nowhere to go it focuses.
      if (e.key === "Enter" && n && n.tagName.toLowerCase() !== "a") {
        e.preventDefault();
        controller.focusNode(n.getAttribute("data-node"));
        return;
      }
      const action = figureKey(e);
      if (!action || (action === "clear" && focusedId === null)) return;
      // The graph explorer binds bare keys on document; stop ours reaching it.
      e.stopPropagation();
      e.preventDefault();
      switch (action) {
        case "fit":
          controller.fit();
          break;
        case "zoom-in":
          controller.zoom(ZOOM_STEP);
          break;
        case "zoom-out":
          controller.zoom(1 / ZOOM_STEP);
          break;
        case "actual":
          controller.actual();
          break;
        case "clear":
          controller.focusNode(null);
          break;
      }
    },
    on,
  );

  indexNodes();
  apply(fitBox(natural));
  return controller;
}
