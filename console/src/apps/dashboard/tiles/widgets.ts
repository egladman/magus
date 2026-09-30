// widgets.ts - shared DOM builders used by several tiles: KPI stat strips and
// metric grids. Kept dumb: they build/patch DOM from plain view-model values,
// no protobuf and no store awareness.

import { h } from "./card";

export type Accent = "hit" | "miss" | "rate" | "size" | "err" | "info";

// StatStrip is a row of KPI cells (label + big monospace value), each with a
// colored left rule. Cells are addressed by key so update() patches values in
// place rather than rebuilding the DOM every tick.
export class StatStrip {
  readonly el: HTMLElement;
  private cells = new Map<string, HTMLElement>();

  constructor(specs: { key: string; label: string; accent: Accent }[]) {
    this.el = h("div", "console-dashboard-statstrip");
    for (const s of specs) {
      const cell = h("div", "console-dashboard-stat");
      cell.dataset.accent = s.accent;
      cell.append(h("span", "console-dashboard-stat__key", s.label));
      const v = h("span", "console-dashboard-stat__value", "-");
      cell.append(v);
      this.cells.set(s.key, v);
      this.el.append(cell);
    }
  }

  set(key: string, value: string): void {
    const c = this.cells.get(key);
    if (c) c.textContent = value;
  }
}

// MetricGrid is a dense grid of labeled scalar readouts (label + monospace value),
// optionally split into captioned groups. Values are patched in place by key. Used
// by the Buzz and Sandbox tiles, which are many small numbers rather than a series.
export class MetricGrid {
  readonly el: HTMLElement;
  private cells = new Map<string, HTMLElement>();

  constructor(groups: { caption?: string; items: { key: string; label: string }[] }[]) {
    this.el = h("div", "console-dashboard-metric__groups");
    for (const g of groups) {
      const section = h("div", "console-dashboard-metric__group");
      if (g.caption) section.append(h("p", "console-dashboard-metric__caption", g.caption));
      const grid = h("div", "console-dashboard-metric__grid");
      for (const it of g.items) {
        const cell = h("div", "console-dashboard-metric");
        cell.append(h("span", "console-dashboard-metric__key", it.label));
        const v = h("span", "console-dashboard-metric__value", "-");
        this.cells.set(it.key, v);
        cell.append(v);
        grid.append(cell);
      }
      section.append(grid);
      this.el.append(section);
    }
  }

  set(key: string, value: string): void {
    const c = this.cells.get(key);
    if (c) c.textContent = value;
  }
}
