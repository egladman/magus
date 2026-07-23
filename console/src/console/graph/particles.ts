// particles.ts - the explorer's information-carrying motion layer: pure math plus a
// small amount of module state (the current flow-edge list and the pulse map). This
// module owns NEITHER the rAF loop NOR the canvas - main.ts drives requestAnimationFrame,
// calls tick(now) once per frame, and paints the returned points/pulses into the shared
// draw() pass. Keeping the loop and the DOM out of this file is what makes it importable
// and testable under plain node (see particles.test.ts).
//
// Determinism: tick(now) is a pure function of (module state, now) - two calls with the
// same now and the same state return identical flowPoints. No Math.random, no Date.now/
// new Date; every timestamp this module touches arrives as an argument.

// FlowEdge is a full polyline INCLUDING both endpoints, ordered dependency-end ->
// dependent-end (the direction build flow travels: left to right in the DAG modes).
export interface FlowEdge {
  pts: { x: number; y: number }[];
}

// PERIOD_MS is the wall-clock time for one particle to traverse ANY edge, regardless of
// the edge's world-space length - constant period, not constant world speed, so short and
// long edges pulse in the same rhythm instead of particles crawling long edges slowly.
const PERIOD_MS = 2400;

// PULSE_LIFETIME_MS is how long a live-refresh recency pulse stays visible before it is
// pruned out of the map in tick().
const PULSE_LIFETIME_MS = 900;

// EDGE_CAP is the scale guard: above this many flow edges the per-frame particle math
// stops being "a few dots" and starts being visible cost for no extra information, so
// setFlowEdges refuses and stores null.
export const EDGE_CAP = 400;

let flowEdges: FlowEdge[] | null = null;
let pulses = new Map<string, number>(); // nodeId -> startedMs

// setFlowEdges stores edges only when non-null, non-empty, and within EDGE_CAP; any other
// input (null, empty, or over-cap) clears the stored list. Returns whether flow is now
// active, so the caller (main.ts's buildFlowEdges) can track flowOn from the POST-cap
// outcome instead of the pre-cap edge count it was given.
export function setFlowEdges(edges: FlowEdge[] | null): boolean {
  const ok = !!edges && edges.length > 0 && edges.length <= EDGE_CAP;
  flowEdges = ok ? edges : null;
  return ok;
}

export function setPulses(nodeIds: string[], nowMs: number): void {
  for (const id of nodeIds) pulses.set(id, nowMs);
}

// resetMotion clears both flow edges and pulses; main.ts calls it on teardown so a
// leftover rAF frame (or the next graph load) does not paint stale motion.
export function resetMotion(): void {
  flowEdges = null;
  pulses = new Map();
}

// samplePolyline is arc-length parameterized: t in [0,1] maps to the point that fraction
// along the TOTAL polyline length (the sum of segment lengths), not the fraction of
// vertices. t<=0 clamps to the first point, t>=1 clamps to the last. Pure: does not read
// module state.
export function samplePolyline(
  pts: readonly { x: number; y: number }[],
  t: number,
): { x: number; y: number } {
  if (pts.length === 0) return { x: 0, y: 0 };
  if (pts.length === 1) return { x: pts[0].x, y: pts[0].y };
  if (t <= 0) return { x: pts[0].x, y: pts[0].y };
  if (t >= 1) {
    const last = pts[pts.length - 1];
    return { x: last.x, y: last.y };
  }

  const segLens: number[] = [];
  let total = 0;
  for (let i = 0; i < pts.length - 1; i++) {
    const dx = pts[i + 1].x - pts[i].x;
    const dy = pts[i + 1].y - pts[i].y;
    const len = Math.sqrt(dx * dx + dy * dy);
    segLens.push(len);
    total += len;
  }
  if (total === 0) return { x: pts[0].x, y: pts[0].y };

  const target = t * total;
  let covered = 0;
  for (let i = 0; i < segLens.length; i++) {
    const segLen = segLens[i];
    if (target <= covered + segLen || i === segLens.length - 1) {
      const segT = segLen === 0 ? 0 : (target - covered) / segLen;
      const a = pts[i];
      const b = pts[i + 1];
      return { x: a.x + (b.x - a.x) * segT, y: a.y + (b.y - a.y) * segT };
    }
    covered += segLen;
  }
  const last = pts[pts.length - 1];
  return { x: last.x, y: last.y };
}

// particleAlpha ramps 0 -> 0.8 over the first 10% of travel and 0.8 -> 0 over the last
// 10%, flat 0.8 in between, so a particle fades in/out at an edge's endpoints instead of
// popping in and out of existence. Pure.
export function particleAlpha(t: number): number {
  const PEAK = 0.8;
  const RAMP = 0.1;
  if (t <= 0 || t >= 1) return 0;
  if (t < RAMP) return PEAK * (t / RAMP);
  if (t > 1 - RAMP) return PEAK * ((1 - t) / RAMP);
  return PEAK;
}

// tick reads module state for the given frame time and returns what main.ts should paint,
// or null when there is nothing to draw (no flow edges and no live pulses) so the caller
// can skip the frame entirely. Two calls with the same nowMs return identical flowPoints -
// deterministic, no RNG, no wall-clock reads inside.
export function tick(
  nowMs: number,
): { flowPoints: { x: number; y: number; alpha: number }[]; pulses: Map<string, number> } | null {
  // Prune expired pulses first so an all-expired map plus no flow edges reports null.
  for (const [id, startedMs] of pulses) {
    if (nowMs - startedMs > PULSE_LIFETIME_MS) pulses.delete(id);
  }

  if (!flowEdges && pulses.size === 0) return null;

  const flowPoints: { x: number; y: number; alpha: number }[] = [];
  if (flowEdges) {
    for (let edgeIndex = 0; edgeIndex < flowEdges.length; edgeIndex++) {
      const pts = flowEdges[edgeIndex].pts;
      if (pts.length < 2) continue;
      for (let particleIndex = 0; particleIndex < 2; particleIndex++) {
        const phaseOffset = (edgeIndex * 0.37 + particleIndex * 0.5) % 1;
        const progress = (nowMs / PERIOD_MS + phaseOffset) % 1;
        const pt = samplePolyline(pts, progress);
        flowPoints.push({ x: pt.x, y: pt.y, alpha: particleAlpha(progress) });
      }
    }
  }

  const pulseOut = new Map<string, number>();
  for (const [id, startedMs] of pulses) {
    pulseOut.set(id, (nowMs - startedMs) / PULSE_LIFETIME_MS);
  }

  return { flowPoints, pulses: pulseOut };
}
