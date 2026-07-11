// utilization.ts - pool utilization history as a GitHub-contribution-style SVG grid:
// one square per Sample, colored by utilization (busy = info color; a queued sample
// switches to the miss color to flag saturation; an unlimited pool is colored by load
// relative to the observed peak). Seeded from the metrics Backfill, then kept live by
// one synthesized sample per status frame (both arrive in state.samples).

import type { DashboardState, SampleView } from "../state";
import { clock } from "../state";
import { cssVar, onThemeChange } from "../charts/uplot";
import { Card, h, type Tile } from "./card";

const SVGNS = "http://www.w3.org/2000/svg";
const GRID_ROWS = 7;

export function utilizationTile(): Tile {
  const card = new Card("util", "Pool utilization", { term: "Concurrency", label: "utilization", note: "no samples yet" });
  const grid = h("div", "util-grid");
  grid.setAttribute("aria-label", "Pool utilization history");
  const legend = h("div", "util-legend");
  const scale = h("span", "util-legend-scale");
  scale.append(document.createTextNode("idle "));
  const ramp = h("span", "util-ramp");
  ramp.setAttribute("aria-hidden", "true");
  // Five ramp swatches; their opacities come from .util-ramp i:nth-child(n) in
  // dashboard.css (no inline styles).
  for (let i = 0; i < 5; i++) ramp.append(h("i"));
  scale.append(ramp, document.createTextNode(" full"));
  legend.append(scale, h("span", "lg lg-queued", "queued"));
  card.body.append(grid, legend);

  let samples: SampleView[] = [];
  let peakInUse = 1;

  // utilColor maps a sample to a fill + opacity ramp (a hand-rolled linear scale, no
  // d3-scale dep). A queued sample (waiting > 0) switches to the miss color.
  function utilColor(s: SampleView): { fill: string; opacity: number } {
    let u: number;
    if (s.capacity > 0) u = Math.min(1, s.inUse / s.capacity);
    else u = s.inUse > 0 ? Math.min(1, s.inUse / Math.max(peakInUse, 1)) : 0;
    const base = s.waiting > 0 ? cssVar("--c-miss") : cssVar("--c-info");
    const opacity = s.inUse <= 0 && s.waiting <= 0 ? 0.06 : 0.15 + 0.85 * u;
    return { fill: base, opacity };
  }

  function render(): void {
    peakInUse = 1;
    for (const s of samples) if (s.inUse > peakInUse) peakInUse = s.inUse;
    const SQ = 12, GAP = 3;
    const n = samples.length;
    const cols = Math.max(1, Math.ceil(n / GRID_ROWS));
    const w = Math.max(1, cols * (SQ + GAP) - GAP);
    const ht = Math.max(1, GRID_ROWS * (SQ + GAP) - GAP);
    const svg = document.createElementNS(SVGNS, "svg");
    svg.setAttribute("viewBox", `0 0 ${w} ${ht}`);
    svg.setAttribute("class", "util-svg");
    svg.setAttribute("preserveAspectRatio", "xMinYMin meet");
    svg.setAttribute("role", "img");
    svg.setAttribute("aria-label", "Pool utilization history");
    const frag = document.createDocumentFragment();
    for (let i = 0; i < n; i++) {
      const s = samples[i];
      const col = Math.floor(i / GRID_ROWS), row = i % GRID_ROWS;
      const { fill, opacity } = utilColor(s);
      const r = document.createElementNS(SVGNS, "rect");
      r.setAttribute("x", String(col * (SQ + GAP)));
      r.setAttribute("y", String(row * (SQ + GAP)));
      r.setAttribute("width", String(SQ));
      r.setAttribute("height", String(SQ));
      r.setAttribute("rx", "2");
      r.setAttribute("fill", fill);
      r.setAttribute("fill-opacity", opacity.toFixed(3));
      r.setAttribute("class", "util-sq");
      const title = document.createElementNS(SVGNS, "title");
      const cap = s.capacity > 0 ? `${s.inUse}/${s.capacity}` : `${s.inUse} (unlimited)`;
      title.textContent = `${clock(s.at)} - ${cap} in use${s.waiting > 0 ? ", " + s.waiting + " waiting" : ""}`;
      r.appendChild(title);
      frag.appendChild(r);
    }
    svg.appendChild(frag);
    grid.replaceChildren(svg);
    card.setNote(n ? `${n} samples, newest ${clock(samples[n - 1].at)}` : "no samples yet");
  }

  const offTheme = onThemeChange(render);

  return {
    el: card.el,
    update(s: DashboardState) { samples = s.samples; render(); },
    destroy() { offTheme(); },
  };
}
