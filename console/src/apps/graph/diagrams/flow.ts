// Moving dashes along a figure's edges. Each solid connector gets a dashed overlay clone in the
// accent, travelling source to destination; the figure's SVG and its static docs copy stay
// untouched. One d3-timer serves every mounted figure on the page.

import { select, selectAll } from "d3-selection";
import { timer, type Timer } from "d3-timer";
import { FLOW_DASH_PX, FLOW_GAP_PX, dashOffset, flowSpeed } from "../flow.js";

export const FLOW_CLASS = "console-diagrams__flow";

// A figure draws every connector source to destination, so each overlay runs forward. Figures do
// not say which relation an edge is, so they share the default speed.
const SPEED = flowSpeed("");

export interface FlowOptions {
  reducedMotion: () => boolean;
}

export interface Flow {
  // stop removes the overlays and leaves the shared timer if this was its last figure.
  // Idempotent.
  stop(): void;
}

interface Running {
  svg: SVGSVGElement;
  sources: Element[];
  overlays: Element[];
  phaseMs: number;
  reducedMotion: () => boolean;
}

const running = new Set<Running>();
let shared: Timer | null = null;
let lastElapsed = 0;
let visibilityWired = false;

function frame(elapsed: number): void {
  const dt = elapsed - lastElapsed;
  lastElapsed = elapsed;
  if (document.hidden) {
    stopShared();
    return;
  }
  for (const r of running) {
    // Reduced motion turned on, or the figure was swapped out without a stop().
    if (r.reducedMotion() || !r.svg.isConnected) {
      remove(r);
      continue;
    }
    // A hovered edge holds still so it can be read.
    if (r.sources.some((s) => s.hasAttribute("data-hot"))) continue;
    r.phaseMs += dt;
    selectAll(r.overlays).attr("stroke-dashoffset", dashOffset(r.phaseMs, SPEED, true));
  }
  if (running.size === 0) stopShared();
}

function startShared(): void {
  if (shared || running.size === 0 || document.hidden) return;
  lastElapsed = 0;
  shared = timer(frame);
}

function stopShared(): void {
  shared?.stop();
  shared = null;
}

function remove(r: Running): void {
  for (const o of r.overlays) o.remove();
  running.delete(r);
}

function wireVisibility(): void {
  if (visibilityWired) return;
  visibilityWired = true;
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) stopShared();
    else startShared();
  });
}

// attachFlow overlays svg's solid [data-edge] paths and starts them moving. A path that already
// carries a stroke-dasharray is dashed for a reason (an optional or security edge) and is left
// alone. Under reduced motion it draws nothing and returns a no-op Flow.
export function attachFlow(svg: SVGSVGElement, opts: FlowOptions): Flow {
  if (opts.reducedMotion()) return { stop: () => {} };
  const sources = [...svg.querySelectorAll("path[data-edge]")].filter(
    (p) => !p.hasAttribute("stroke-dasharray"),
  );
  const overlays: Element[] = [];
  for (const src of sources) {
    // The clone sits right after its edge, so it paints with it and the edge's dim state reaches it
    // through the sibling selector in diagrams.css. It drops what makes it an edge: interact.ts
    // reads [data-edge], and an id or a second arrowhead would duplicate the original's.
    const overlay = select(src)
      .clone(false)
      .attr("data-edge", null)
      .attr("id", null)
      .attr("marker-end", null)
      .attr("stroke", null)
      .attr("class", FLOW_CLASS)
      .attr("aria-hidden", "true")
      .attr("stroke-dasharray", FLOW_DASH_PX + " " + FLOW_GAP_PX)
      .node();
    if (overlay) overlays.push(overlay);
  }
  if (overlays.length === 0) return { stop: () => {} };
  const r: Running = { svg, sources, overlays, phaseMs: 0, reducedMotion: opts.reducedMotion };
  running.add(r);
  wireVisibility();
  startShared();
  return {
    stop: () => {
      if (!running.has(r)) return;
      remove(r);
      if (running.size === 0) stopShared();
    },
  };
}

// flowRunning reports whether the shared timer is armed; tests read it.
export function flowRunning(): boolean {
  return shared !== null;
}
