// cacheRate.ts - cache hit-rate over time. The Sample counters are cumulative, so a
// per-interval rate is derived by diffing adjacent samples: hits / (hits + misses)
// over each interval. A quiet interval (no cache activity) plots as a gap (null).

import type { DashboardState, SampleView } from "../state";
import { TimeChart, onThemeChange } from "../charts/uplot";
import { Card, h, type Tile } from "./card";
import type uPlot from "uplot";

export function cacheRateTile(): Tile {
  let chart: TimeChart;
  const card = new Card("cache", "Cache hit-rate", {
    term: "Cache", note: "per-interval hits / (hits + misses)",
    onReveal: () => { chart.build(); chart.resize(); },
  });
  const plot = h("div", "chart-plot");
  const legend = h("div", "chart-legend");
  legend.append(h("span", "lg lg-hit", "hit rate"));
  card.body.append(plot, legend);

  chart = new TimeChart(plot, {
    series: [{ label: "hit rate", colorVar: "--c-hit", fillVar: "--c-hit", width: 1.75 }],
    yFormat: (v) => v + "%",
    ySize: 44,
    yRange: [0, 100],
  });

  function derive(samples: SampleView[]): uPlot.AlignedData {
    const t: number[] = [], rate: (number | null)[] = [];
    for (let i = 1; i < samples.length; i++) {
      const a = samples[i - 1], b = samples[i];
      const dh = Math.max(0, b.cacheHits - a.cacheHits);
      const dm = Math.max(0, b.cacheMisses - a.cacheMisses);
      const total = dh + dm;
      t.push(b.at / 1000);
      rate.push(total > 0 ? (dh / total) * 100 : null);
    }
    return [t, rate] as uPlot.AlignedData;
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
    destroy() { window.removeEventListener("resize", onResize); offTheme(); chart.destroy(); },
  };
}
