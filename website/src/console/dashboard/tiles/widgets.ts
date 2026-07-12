// widgets.ts - shared DOM builders used by several tiles: KPI stat strips and
// sortable tables. Kept dumb: they build/patch DOM from plain view-model values,
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
    this.el = h("div", "stat-strip");
    for (const s of specs) {
      const cell = h("div", "stat");
      cell.dataset.accent = s.accent;
      cell.append(h("span", "stat-k", s.label));
      const v = h("span", "stat-v", "-");
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
    this.el = h("div", "metric-groups");
    for (const g of groups) {
      const section = h("div", "metric-group");
      if (g.caption) section.append(h("p", "metric-caption", g.caption));
      const grid = h("div", "metric-grid");
      for (const it of g.items) {
        const cell = h("div", "metric");
        cell.append(h("span", "metric-k", it.label));
        const v = h("span", "metric-v", "-");
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

export interface Column<T> {
  key: string;
  label: string;
  // Rendered cell text.
  text: (row: T) => string;
  // Sort value (number sorts descending by default; string ascending).
  sort: (row: T) => number | string;
  // Right-align numeric columns.
  numeric?: boolean;
}

// SortableTable renders rows into a table whose headers toggle the sort column and
// direction. It rebuilds its <tbody> on each render() (the row counts are small -
// tens of targets/tools), preserving the active sort. Purely presentational: the
// caller hands it view-model rows.
export class SortableTable<T> {
  readonly el: HTMLElement;
  private tbody: HTMLElement;
  private cols: Column<T>[];
  private rows: T[] = [];
  private sortKey: string;
  private sortDir: 1 | -1;
  private empty: HTMLElement;

  constructor(cols: Column<T>[], opts: { sortKey?: string; emptyText?: string } = {}) {
    this.cols = cols;
    this.sortKey = opts.sortKey ?? cols[0].key;
    this.sortDir = 1;

    const wrap = h("div", "table-wrap");
    const table = h("table", "dash-table");
    const thead = h("thead");
    const tr = h("tr");
    for (const c of cols) {
      const th = h("th", c.numeric ? "num" : undefined);
      const btn = h("button", "th-sort", c.label);
      btn.type = "button";
      btn.addEventListener("click", () => this.toggleSort(c.key));
      th.append(btn);
      th.dataset.key = c.key;
      tr.append(th);
    }
    thead.append(tr);
    this.tbody = h("tbody");
    table.append(thead, this.tbody);
    wrap.append(table);

    this.empty = h("p", "row-empty", opts.emptyText ?? "No data yet.");
    this.empty.hidden = true;

    const host = h("div");
    host.append(wrap, this.empty);
    this.el = host;
    this.syncHeaders();
  }

  setRows(rows: T[]): void {
    this.rows = rows;
    this.render();
  }

  private toggleSort(key: string): void {
    if (this.sortKey === key) this.sortDir = (this.sortDir === 1 ? -1 : 1);
    else { this.sortKey = key; this.sortDir = 1; }
    this.syncHeaders();
    this.render();
  }

  private syncHeaders(): void {
    for (const th of this.el.querySelectorAll<HTMLElement>("th")) {
      const active = th.dataset.key === this.sortKey;
      th.dataset.sort = active ? (this.sortDir === 1 ? "desc" : "asc") : "";
    }
  }

  private render(): void {
    const col = this.cols.find((c) => c.key === this.sortKey) ?? this.cols[0];
    const sorted = this.rows.slice().sort((a, b) => {
      const va = col.sort(a), vb = col.sort(b);
      let cmp: number;
      if (typeof va === "number" && typeof vb === "number") cmp = vb - va; // numbers: high-to-low as "desc"
      else cmp = String(va) < String(vb) ? -1 : String(va) > String(vb) ? 1 : 0;
      return this.sortDir === 1 ? cmp : -cmp;
    });
    this.tbody.replaceChildren();
    for (const row of sorted) {
      const tr = h("tr");
      for (const c of this.cols) {
        const td = h("td", c.numeric ? "num" : undefined, c.text(row));
        tr.append(td);
      }
      this.tbody.append(tr);
    }
    this.empty.hidden = sorted.length > 0;
  }
}
