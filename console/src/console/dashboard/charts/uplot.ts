// uplot.ts - a theme-aware uPlot wrapper. uPlot draws to a canvas with colors it
// is handed at construction, so a light/dark theme flip means rebuilding the chart
// with fresh CSS-variable colors. This wrapper owns that lifecycle: build lazily
// (a chart created while its container is display:none measures 0 wide), setData on
// each tick, setSize on resize, and rebuild() on a theme change. All colors are
// read from CSS custom properties so both themes carry over with no JS color table.

import uPlot from "uplot";

export function cssVar(name: string, fallback = "#888"): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

// onThemeChange fires cb when the effective theme flips (the theme toggle stamps
// data-theme on <html>, or the OS prefers-color-scheme changes), deduping on the
// actual resolved CSS-variable colors so a no-op mutation doesn't churn. Returns an
// unsubscribe. Each chart tile uses this to recolor itself; nothing global.
export function onThemeChange(cb: () => void): () => void {
  const sig = () => [
    cssVar("--pf-t--global--text--color--regular"), cssVar("--console-chart-axis"),
    cssVar("--console-status-running"), cssVar("--console-status-ok"), cssVar("--console-status-warn"),
  ].join("|");
  let last = sig();
  const check = (): void => {
    const s = sig();
    if (s === last) return;
    last = s;
    cb();
  };
  const mo = new MutationObserver(check);
  mo.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  const mm = matchMedia("(prefers-color-scheme: dark)");
  mm.addEventListener("change", check);
  return () => { mo.disconnect(); mm.removeEventListener("change", check); };
}

const CHART_HEIGHT = 132;

function axisBase(): uPlot.Axis {
  return {
    stroke: cssVar("--console-chart-axis"),
    grid: { stroke: cssVar("--console-chart-grid"), width: 0.5 },
    ticks: { stroke: cssVar("--console-chart-grid"), width: 0.5 },
    font: "11px " + cssVar("--pf-t--global--font--family--body", "system-ui, sans-serif"),
  };
}

export interface SeriesSpec { label: string; colorVar: string; fillVar?: string; width?: number; }

export interface ChartSpec {
  series: SeriesSpec[];
  // Y-axis tick formatter (e.g. duration or percent).
  yFormat: (v: number) => string;
  ySize?: number;
  yRange?: [number, number];
}

// TimeChart is a time-x line chart. It defers construction until build() is called
// on a visible container, updates in place via setData, and rebuilds on theme change.
export class TimeChart {
  private container: HTMLElement;
  private spec: ChartSpec;
  private chart: uPlot | null = null;
  private data: uPlot.AlignedData;

  constructor(container: HTMLElement, spec: ChartSpec) {
    this.container = container;
    this.spec = spec;
    this.data = [[], ...spec.series.map(() => [])] as unknown as uPlot.AlignedData;
  }

  private width(): number {
    return Math.max(160, this.container.clientWidth || 560);
  }

  build(): void {
    if (this.chart) return;
    // A chart created while its container is display:none / collapsed (clientWidth 0)
    // would measure 0 wide and never recover; defer until it is actually on screen.
    // build() is idempotent, so callers can invoke it on every tick.
    if (this.container.clientWidth === 0) return;
    const yAxis: uPlot.Axis = {
      ...axisBase(),
      size: this.spec.ySize ?? 54,
      values: (_u, splits) => splits.map((v) => this.spec.yFormat(v as number)),
    };
    const opts: uPlot.Options = {
      width: this.width(),
      height: CHART_HEIGHT,
      legend: { show: false },
      cursor: { points: { size: 5 }, focus: { prox: 16 } },
      scales: { x: { time: true }, ...(this.spec.yRange ? { y: { range: this.spec.yRange } } : {}) },
      axes: [axisBase(), yAxis],
      series: [
        {},
        ...this.spec.series.map((s) => ({
          label: s.label,
          stroke: cssVar(s.colorVar),
          fill: s.fillVar ? cssVar(s.fillVar) : undefined,
          width: s.width ?? 1.5,
          points: { show: false },
        })),
      ],
    };
    this.chart = new uPlot(opts, this.data, this.container);
  }

  setData(data: uPlot.AlignedData): void {
    this.data = data;
    this.chart?.setData(data);
  }

  resize(): void {
    this.chart?.setSize({ width: this.width(), height: CHART_HEIGHT });
  }

  // rebuild recolors the chart for a theme flip, preserving the current data.
  rebuild(): void {
    if (!this.chart) return;
    const data = this.data;
    this.chart.destroy();
    this.chart = null;
    this.build();
    this.setData(data);
  }

  destroy(): void {
    if (this.chart) { this.chart.destroy(); this.chart = null; }
  }
}
