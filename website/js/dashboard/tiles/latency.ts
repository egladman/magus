// latency.ts - latency percentiles per operation family. Four small charts (target
// execution, cache op, pool wait, graph query), each plotting p50/p95/p99 over time
// with an exact current readout beside it. The metrics Snapshot carries the current
// percentiles per family; this tile keeps a rolling per-family time-series.

import type { DashboardState, LatKey, LatView } from "../state";
import { LAT_KEYS, LAT_META, fmtCount, fmtDur } from "../state";
import { TimeChart, onThemeChange } from "../charts/uplot";
import { glossaryLink } from "../../lib/glossary";
import { Card, h, type Tile } from "./card";
import type uPlot from "uplot";

interface LatSeries { t: number[]; p50: number[]; p95: number[]; p99: number[]; }
const CHART_HISTORY = 240; // points kept per chart (~4 min at 1s)

function emptySeries(): LatSeries { return { t: [], p50: [], p95: [], p99: [] }; }

export function latencyTile(): Tile {
  const charts = {} as Record<LatKey, TimeChart>;
  const readouts = {} as Record<LatKey, HTMLElement>;
  const data = {} as Record<LatKey, LatSeries>;

  const buildAll = () => { for (const k of LAT_KEYS) charts[k].build(); };
  const resizeAll = () => { for (const k of LAT_KEYS) charts[k].resize(); };

  const card = new Card("latency", "Latency", { term: "Latency", defaultCollapsed: true, onReveal: () => { buildAll(); resizeAll(); } });
  // The note carries the p50/p95/p99 swatch legend plus a Percentile deep-link.
  const note = h("span");
  note.append(h("span", "lg lg-p50", "p50"), h("span", "lg lg-p95", "p95"), h("span", "lg lg-p99", "p99"), document.createTextNode(" "));
  note.append(glossaryLink("Percentile", { label: "percentiles" }));
  // The note is rich (swatch legend + glossary link), not a plain string, so populate
  // the note node directly instead of via setNote.
  card.noteNode().replaceChildren(note);

  const gridEl = h("div", "chart-grid");
  for (const k of LAT_KEYS) {
    data[k] = emptySeries();
    const fig = h("figure", "chart");
    fig.append(h("figcaption", undefined, LAT_META[k].label));
    const plot = h("div", "chart-plot");
    const ro = h("div", "chart-readout", "no data yet");
    fig.append(plot, ro);
    gridEl.append(fig);
    readouts[k] = ro;
    charts[k] = new TimeChart(plot, {
      series: [
        { label: "p50", colorVar: "--c-info" },
        { label: "p95", colorVar: "--c-miss" },
        { label: "p99", colorVar: "--c-err" },
      ],
      yFormat: (v) => fmtDur(v),
      ySize: 54,
    });
  }
  card.body.append(gridEl);

  function aligned(d: LatSeries): uPlot.AlignedData {
    return [d.t, d.p50, d.p95, d.p99] as uPlot.AlignedData;
  }

  function feed(k: LatKey, tSec: number, lat: LatView): void {
    const d = data[k];
    d.t.push(tSec); d.p50.push(lat.p50); d.p95.push(lat.p95); d.p99.push(lat.p99);
    if (d.t.length > CHART_HISTORY) {
      const drop = d.t.length - CHART_HISTORY;
      d.t.splice(0, drop); d.p50.splice(0, drop); d.p95.splice(0, drop); d.p99.splice(0, drop);
    }
    charts[k].setData(aligned(d));
    readouts[k].textContent =
      `count ${fmtCount(lat.count)} - p50 ${fmtDur(lat.p50)} - p95 ${fmtDur(lat.p95)} - p99 ${fmtDur(lat.p99)} - max ${fmtDur(lat.max)}`;
  }

  const onResize = resizeAll;
  window.addEventListener("resize", onResize);
  const offTheme = onThemeChange(() => { for (const k of LAT_KEYS) charts[k].rebuild(); });

  // The store re-publishes on every frame (status or metrics); a family time-series
  // must only grow one point per NEW snapshot, so skip a publish that carries the
  // same capturedAt we already plotted.
  let lastMs = -1;

  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.metrics || s.metrics.capturedMs === lastMs) return;
      lastMs = s.metrics.capturedMs;
      buildAll(); // idempotent; each chart defers itself until visible
      const tSec = s.metrics.capturedMs / 1000;
      for (const k of LAT_KEYS) {
        const lat = s.metrics.latency[k];
        if (lat) feed(k, tSec, lat);
      }
    },
    destroy() {
      window.removeEventListener("resize", onResize);
      offTheme();
      for (const k of LAT_KEYS) charts[k].destroy();
    },
  };
}
