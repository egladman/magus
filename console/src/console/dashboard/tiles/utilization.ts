// utilization.ts - pool utilization history as a GitHub-contribution-style SVG grid:
// one square per Sample, colored by utilization (busy = running color; a queued sample
// switches to the queued color to flag saturation; an unlimited pool is colored by load
// relative to the observed peak). Seeded from the metrics Backfill, then kept live by
// one synthesized sample per status frame (both arrive in state.samples).
//
// PatternFly (W0 spike): this is the reference tile. Its shell is a PatternFly Card
// (.pf-v6-c-card + __header/__title-text/__body/__footer) built inline here rather than the
// shared collapsible Card class - only pf-v6-* classes plus the app hook (data-card) and the
// tile's own grid/legend markup (styled by dashboard.css, unchanged). Its colors come from the
// console's NEW semantic tokens (--console-status-*, defined in tokens.css onto PF status
// tokens), not the old --c-* palette; those tokens are theme-aware so the grid colors correctly
// in light and dark. The dashboard's default-collapse affordance is not wired for the spike
// (that is a W3 detail); everything else - live updates, tooltips, theme re-render - is intact.

import type { DashboardState, SampleView } from "../state";
import { clock } from "../state";
import { cssVar, onThemeChange } from "../charts/uplot";
import { h, type Tile } from "./card";

const SVGNS = "http://www.w3.org/2000/svg";
const GRID_ROWS = 7;

export function utilizationTile(): Tile {
  // PatternFly Card shell. data-card is the app hook; every class is a pf-v6-* one.
  const card = h("div", "pf-v6-c-card");
  card.dataset.card = "util";

  const header = h("div", "pf-v6-c-card__header");
  const headerMain = h("div", "pf-v6-c-card__header-main");
  const titleWrap = h("div", "pf-v6-c-card__title");
  const title = h("h2", "pf-v6-c-card__title-text", "Pool utilization");
  titleWrap.append(title);
  headerMain.append(titleWrap);
  header.append(headerMain);

  const body = h("div", "pf-v6-c-card__body");
  const footer = h("div", "pf-v6-c-card__footer");
  const note = document.createElement("span");
  note.textContent = "no samples yet";
  footer.append(note);

  const grid = h("div", "console-dashboard-util__grid");
  grid.setAttribute("aria-label", "Pool utilization history");
  const legend = h("div", "console-dashboard-util__legend");
  const scale = h("span", "console-dashboard-util__scale");
  scale.append(document.createTextNode("idle "));
  const ramp = h("span", "console-dashboard-util__ramp");
  ramp.setAttribute("aria-hidden", "true");
  // Five ramp swatches; their opacities come from .util-ramp i:nth-child(n) in
  // dashboard.css (no inline styles).
  for (let i = 0; i < 5; i++) ramp.append(h("i"));
  scale.append(ramp, document.createTextNode(" full"));
  legend.append(scale, h("span", "console-dashboard-legend console-dashboard-legend--queued", "queued"));
  body.append(grid, legend);

  card.append(header, body, footer);

  let samples: SampleView[] = [];
  let peakRunning = 1;

  // utilColor maps a sample to a fill + opacity ramp (a hand-rolled linear scale, no
  // d3-scale dep). A queued sample (queued > 0) switches to the queued color.
  function utilColor(s: SampleView): { fill: string; opacity: number } {
    let u: number;
    if (s.capacity > 0) u = Math.min(1, s.running / s.capacity);
    else u = s.running > 0 ? Math.min(1, s.running / Math.max(peakRunning, 1)) : 0;
    const base = s.queued > 0 ? cssVar("--console-status-queued") : cssVar("--console-status-running");
    const opacity = s.running <= 0 && s.queued <= 0 ? 0.06 : 0.15 + 0.85 * u;
    return { fill: base, opacity };
  }

  function render(): void {
    peakRunning = 1;
    for (const s of samples) if (s.running > peakRunning) peakRunning = s.running;
    const SQ = 12, GAP = 3;
    const n = samples.length;
    const cols = Math.max(1, Math.ceil(n / GRID_ROWS));
    const w = Math.max(1, cols * (SQ + GAP) - GAP);
    const ht = Math.max(1, GRID_ROWS * (SQ + GAP) - GAP);
    const svg = document.createElementNS(SVGNS, "svg");
    svg.setAttribute("viewBox", `0 0 ${w} ${ht}`);
    svg.setAttribute("class", "console-dashboard-util__svg");
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
      r.setAttribute("class", "console-dashboard-util__square");
      const title = document.createElementNS(SVGNS, "title");
      const cap = s.capacity > 0 ? `${s.running}/${s.capacity}` : `${s.running} (unlimited)`;
      title.textContent = `${clock(s.at)} - ${cap} running${s.queued > 0 ? ", " + s.queued + " queued" : ""}`;
      r.appendChild(title);
      frag.appendChild(r);
    }
    svg.appendChild(frag);
    grid.replaceChildren(svg);
    note.textContent = n ? `${n} samples, newest ${clock(samples[n - 1].at)}` : "no samples yet";
  }

  const offTheme = onThemeChange(render);

  return {
    el: card,
    update(s: DashboardState) { samples = s.samples; render(); },
    destroy() { offTheme(); },
  };
}
