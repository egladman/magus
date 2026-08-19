// cacheRate.ts - cache hit-rate over time. The Sample counters are cumulative, so a
// per-interval rate is derived by diffing adjacent samples: hits / (hits + misses)
// over each interval. A quiet interval (no cache activity) plots as a gap (null).
//
// When two samples may NOT be subtracted - a baseline crossover, a daemon restart, an
// unmeasured endpoint - lives in cacheRateSeries.ts, which this tile only plots.

import type { DashboardState, SampleView } from "../state";
import { cacheRateSeries } from "./cacheRateSeries";
import { TimeChart, onThemeChange } from "../charts/uplot";
import { Card, h, type Tile } from "./card";
import type uPlot from "uplot";

export function cacheRateTile(): Tile {
  let chart: TimeChart;
  const card = new Card("cache", "Cache hit-rate", {
    term: "Cache",
    note: "per-interval hits / (hits + misses)",
    why:
      "Hit rate over time, not the running total. A rate that drops and stays down means some" +
      " input now changes on every run. The cumulative number averages that away.",
    onReveal: () => {
      chart.build();
      chart.resize();
    },
  });
  const plot = h("div", "console-dashboard-chart__plot");
  const legend = h("div", "console-dashboard-chart__legend");
  legend.append(h("span", "console-dashboard-legend console-dashboard-legend--hit", "hit rate"));
  card.body.append(plot, legend);

  chart = new TimeChart(plot, {
    series: [
      {
        label: "hit rate",
        colorVar: "--console-status-ok",
        fillVar: "--console-status-ok",
        width: 1.75,
      },
    ],
    yFormat: (v) => v + "%",
    ySize: 44,
    yRange: [0, 100],
  });

  function derive(samples: SampleView[]): uPlot.AlignedData {
    const pts = cacheRateSeries(samples);
    return [pts.map((p) => p.atMs / 1000), pts.map((p) => p.rate)] as uPlot.AlignedData;
  }

  const onResize = () => chart.resize();
  window.addEventListener("resize", onResize);
  const offTheme = onThemeChange(() => chart.rebuild());

  return {
    el: card.el,
    update(s: DashboardState) {
      chart.build(); // idempotent; defers itself until the container is visible
      chart.setData(derive(s.samples));
    },
    destroy() {
      window.removeEventListener("resize", onResize);
      offTheme();
      chart.destroy();
    },
  };
}
