// utilization.ts - pool utilization history as a GitHub-contribution-style SVG grid:
// one square per Sample, colored by utilization (busy = running color; a queued sample
// switches to the queued color to flag saturation; an unlimited pool is colored by load
// relative to the observed peak). Seeded from the metrics Backfill, then kept live by
// one synthesized sample per status frame (both arrive in state.samples).
//
// Its colors come from the console's semantic tokens (--console-status-*, defined in tokens.css
// onto PF status tokens), which are theme-aware, so the grid colors correctly in light and dark.

import type { DashboardState, SampleView } from "../state";
import { clock } from "../state";
import { cssVar, onThemeChange } from "../charts/uplot";
import { Card, h, type Tile } from "./card";

const SVGNS = "http://www.w3.org/2000/svg";
const GRID_ROWS = 7;

export function utilizationTile(): Tile {
  const card = new Card("util", "Pool utilization", {
    note: "no samples yet",
    why:
      "Pool occupancy over time, one square per sample, so saturation reads as a pattern rather" +
      " than as one instant. A band of saturated squares with work queued means the machine was" +
      " the constraint for that whole stretch.",
  });

  const grid = h("div", "console-dashboard-util__grid");
  const readout = h("p", "console-dashboard-chart__readout", "No samples yet.");
  const legend = h("div", "console-dashboard-util__legend");
  const scale = h("span", "console-dashboard-util__scale");
  scale.append(document.createTextNode("idle "));
  const ramp = h("span", "console-dashboard-util__ramp");
  ramp.setAttribute("aria-hidden", "true");
  // Five ramp swatches; their opacities come from .util-ramp i:nth-child(n) in
  // dashboard.css (no inline styles).
  for (let i = 0; i < 5; i++) ramp.append(h("i"));
  scale.append(ramp, document.createTextNode(" full"));
  legend.append(
    scale,
    h("span", "console-dashboard-legend console-dashboard-legend--queued", "queued"),
  );
  // The legend below decodes the SHADING but not the layout, and the layout is the part nobody
  // guesses: this is not a bar chart, it is one square per sample laid out in reading order. Told
  // that, the tile becomes obvious; left to work it out, a viewer reads the rows as categories.
  const howto = h(
    "p",
    "console-dashboard-gantt__howto",
    "One square per sample in reading order, oldest first and newest at the end. A dark run of" +
      " squares is a stretch where the pool stayed busy.",
  );
  card.body.append(howto, grid, readout, legend);

  let samples: SampleView[] = [];
  let peakRunning = 1;

  // utilColor maps a sample to a fill + opacity ramp (a hand-rolled linear scale, no
  // d3-scale dep). A queued sample (queued > 0) switches to the queued color.
  // An unmeasured tick gets the subtle neutral, NOT the idle ramp: a square nobody measured
  // must not read as a square that was idle.
  // The tooltip is what actually carries that distinction. At 0.1 against idle's 0.06 the two
  // are a few percent of alpha apart, so neither opacity nor hue separates them on their own.
  function utilColor(s: SampleView): { fill: string; opacity: number } {
    if (s.running === null || s.queued === null) {
      return { fill: cssVar("--pf-t--global--icon--color--subtle"), opacity: 0.1 };
    }
    let u: number;
    if (s.capacity !== null && s.capacity > 0) u = Math.min(1, s.running / s.capacity);
    else u = s.running > 0 ? Math.min(1, s.running / Math.max(peakRunning, 1)) : 0;
    const base =
      s.queued > 0 ? cssVar("--console-status-queued") : cssVar("--console-status-running");
    const opacity = s.running <= 0 && s.queued <= 0 ? 0.06 : 0.15 + 0.85 * u;
    return { fill: base, opacity };
  }

  // describe is one sample in words, for the square's title and the readout under the grid.
  function describe(s: SampleView): string {
    if (s.running === null) return `${clock(s.at)}, not measured`;
    const cap =
      s.capacity !== null && s.capacity > 0
        ? `${s.running}/${s.capacity}`
        : `${s.running} (unlimited)`;
    return `${clock(s.at)}, ${cap} running${s.queued !== null && s.queued > 0 ? ", " + s.queued + " queued" : ""}`;
  }

  function render(): void {
    peakRunning = 1;
    for (const s of samples)
      if (s.running !== null && s.running > peakRunning) peakRunning = s.running;
    const SQ = 12,
      GAP = 3;
    const n = samples.length;
    const cols = Math.max(1, Math.ceil(n / GRID_ROWS));
    const w = Math.max(1, cols * (SQ + GAP) - GAP);
    const ht = Math.max(1, GRID_ROWS * (SQ + GAP) - GAP);
    const svg = document.createElementNS(SVGNS, "svg");
    svg.setAttribute("viewBox", `0 0 ${w} ${ht}`);
    svg.setAttribute("class", "console-dashboard-util__svg");
    svg.setAttribute("preserveAspectRatio", "xMinYMin meet");
    svg.setAttribute("role", "img");
    svg.setAttribute(
      "aria-label",
      "Pool utilization history, one square per sample. The readout below has the newest sample.",
    );
    const frag = document.createDocumentFragment();
    for (let i = 0; i < n; i++) {
      const s = samples[i];
      const col = Math.floor(i / GRID_ROWS),
        row = i % GRID_ROWS;
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
      title.textContent = describe(s);
      r.appendChild(title);
      frag.appendChild(r);
    }
    svg.appendChild(frag);
    grid.replaceChildren(svg);
    card.setNote(n ? `${n} samples, newest ${clock(samples[n - 1].at)}` : "no samples yet");
    readout.textContent = n
      ? "Newest sample: " + describe(samples[n - 1]) + "."
      : "No samples yet.";
  }

  const offTheme = onThemeChange(render);

  return {
    el: card.el,
    update(s: DashboardState) {
      samples = s.samples;
      render();
    },
    destroy() {
      offTheme();
    },
  };
}
