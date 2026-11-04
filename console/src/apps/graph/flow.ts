// The explorer's motion layer as pure math plus two pieces of module state: the active flow-edge
// set and the recency pulses. main.ts owns the timer and the canvas; every timestamp arrives as an
// argument, so the same inputs always paint the same frame.

// EDGE_CAP bounds the animated set. The graph carries about 25k links, and a set past this reads as
// a moving haze rather than a path.
export const EDGE_CAP = 400;

// Screen pixels; the caller divides by the zoom so dashes keep their size on screen.
export const FLOW_DASH_PX = 6;
export const FLOW_GAP_PX = 5;

// Screen pixels per second along the edge. Build order moves fastest, structure slowest.
const FLOW_SPEED_PX_S: Readonly<Record<string, number>> = {
  depends_on: 24,
  uses: 16,
  contains: 8,
};
const FLOW_SPEED_DEFAULT_PX_S = 16;

const PULSE_LIFETIME_MS = 900;

export interface FlowLink {
  source: string | { id: string };
  target: string | { id: string };
  relation: string;
}

function id(e: string | { id: string }): string {
  return typeof e === "string" ? e : e.id;
}

export function flowSpeed(relation: string): number {
  return FLOW_SPEED_PX_S[relation] ?? FLOW_SPEED_DEFAULT_PX_S;
}

// flowOrigin is the end the flow leaves from. A depends_on link stores the dependent as source, and
// build flow runs from the dependency to it; every other relation flows source to target.
export function flowOrigin(link: FlowLink): string {
  return link.relation === "depends_on" ? id(link.target) : id(link.source);
}

// dashOffset is the lineDashOffset (or stroke-dashoffset) for a path stroked from its start, in the
// units of k = screen pixels per path unit. forward means the path starts at the flow's origin. The
// result stays within one dash period so it never grows without bound.
export function dashOffset(phaseMs: number, speedPx: number, forward: boolean, k = 1): number {
  const period = FLOW_DASH_PX + FLOW_GAP_PX;
  const travelled = ((phaseMs / 1000) * speedPx) % period;
  const offset = (forward ? -travelled : travelled) / k;
  return offset === 0 ? 0 : offset;
}

let flowSet: ReadonlySet<FlowLink> | null = null;
let pulses = new Map<string, number>(); // node id -> started ms

// setFlowLinks stores the view's flow edges when there are some and no more than EDGE_CAP, and
// clears them otherwise. It reports whether flow is now on.
export function setFlowLinks(links: readonly FlowLink[] | null): boolean {
  flowSet = links && links.length > 0 && links.length <= EDGE_CAP ? new Set(links) : null;
  return flowSet !== null;
}

// flows says whether a link animates: it is in the view's flow set, or it touches the highlighted
// node and that node's neighborhood (highlightDegree) fits under EDGE_CAP.
export function flows(link: FlowLink, highlight: string | null, highlightDegree: number): boolean {
  if (flowSet?.has(link)) return true;
  if (highlight === null || highlightDegree > EDGE_CAP) return false;
  return id(link.source) === highlight || id(link.target) === highlight;
}

export function setPulses(nodeIds: readonly string[], nowMs: number): void {
  for (const n of nodeIds) pulses.set(n, nowMs);
}

export function resetMotion(): void {
  flowSet = null;
  pulses = new Map();
}

// pulseProgress prunes expired pulses and returns each live one's progress in [0, 1], or null when
// none is left, which is what lets the caller's timer stop.
export function pulseProgress(nowMs: number): Map<string, number> | null {
  for (const [n, started] of pulses) {
    if (nowMs - started > PULSE_LIFETIME_MS) pulses.delete(n);
  }
  if (pulses.size === 0) return null;
  const out = new Map<string, number>();
  for (const [n, started] of pulses) out.set(n, (nowMs - started) / PULSE_LIFETIME_MS);
  return out;
}
