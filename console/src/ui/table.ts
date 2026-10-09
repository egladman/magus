// table.ts - the console's one sortable table, shared by the Dashboard's tiles and the Tools app. It is
// a PatternFly compact Table with a sortable header. Its rules are styles/table.css, loaded with the
// shell.

import { h } from "../desktop/view";

export type SortDir = "asc" | "desc";

export interface Column<T> {
  key: string;
  label: string;
  // Rendered cell text.
  text: (row: T) => string;
  // Sort value. Numbers order numerically, anything else as text.
  sort: (row: T) => number | string;
  // Right-align numeric columns. A numeric column's first sort is descending, because the largest
  // is what a reader scans for; every other column starts ascending.
  numeric?: boolean;
}

export interface SortableTableOptions {
  // The table's accessible name. Required in practice: a table without one is announced as "table"
  // and nothing more. Omitting it is deprecated and falls back to a generic name.
  label?: string;
  // A visible caption, for a table whose heading is not already beside it.
  caption?: string;
  sortKey?: string;
  // Direction of the initial sort; defaults by the column's type as described on Column.numeric.
  sortDir?: SortDir;
  emptyText?: string;
}

// Collation orders text the way a reader expects: case-insensitive, with digit runs by value, so
// "run 9" precedes "run 10".
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

// compare orders two sort values ascending: A to Z, low to high. Descending is its negation, for every
// column type alike; that is the only meaning "asc" and "desc" have here.
function compare(a: number | string, b: number | string): number {
  if (typeof a === "number" && typeof b === "number") return a - b;
  return collator.compare(String(a), String(b));
}

const SVG = "http://www.w3.org/2000/svg";

// indicator draws the header's sort arrow: up, down, or the pair that says "sortable".
function indicator(dir: SortDir | null): HTMLElement {
  const span = h("span", "pf-v6-c-table__sort-indicator");
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG, "path");
  path.setAttribute(
    "d",
    dir === "asc"
      ? "M12 19V5M5 12l7-7 7 7"
      : dir === "desc"
        ? "M12 5v14M5 12l7 7 7-7"
        : "M8 9l4-4 4 4M16 15l-4 4-4-4",
  );
  svg.append(path);
  span.append(svg);
  return span;
}

// SortableTable renders rows into a table whose headers toggle the sort column and
// direction. It rebuilds its <tbody> on each render() (the row counts are small -
// tens of targets/tools), preserving the active sort. Purely presentational: the
// caller hands it view-model rows.
//
// The scroll container is the one wrapper around the table. Give it a height (a bounded parent, or
// --console-table-max-block-size) and the header stays put while the rows scroll; without one the
// table is as tall as its rows and the page scrolls instead.
export class SortableTable<T> {
  readonly el: HTMLElement;
  private tbody: HTMLElement;
  private cols: Column<T>[];
  private rows: T[] = [];
  private sortKey: string;
  private sortDir: SortDir;
  private empty: HTMLElement;
  private emptyText: string;
  private wrap: HTMLElement;
  private unresolved: string | null = null;

  constructor(cols: Column<T>[], opts: SortableTableOptions = {}) {
    this.cols = cols;
    this.sortKey = opts.sortKey ?? cols[0].key;
    this.sortDir = opts.sortDir ?? this.firstDir(this.sortKey);

    const wrap = h("div", "console-table__wrap");
    this.wrap = wrap;
    const table = h("table", "pf-v6-c-table pf-m-compact pf-m-sticky-header console-table");
    table.setAttribute("aria-label", opts.label ?? "Table");
    if (opts.caption) {
      const caption = h("caption", "pf-v6-c-table__caption", opts.caption);
      table.append(caption);
    }
    const thead = h("thead", "pf-v6-c-table__thead");
    const tr = h("tr", "pf-v6-c-table__tr");
    for (const c of cols) {
      const th = h("th", "pf-v6-c-table__th pf-v6-c-table__sort");
      th.scope = "col";
      if (c.numeric) th.dataset.num = "";
      th.dataset.key = c.key;
      const btn = h("button", "pf-v6-c-table__button");
      btn.type = "button";
      btn.addEventListener("click", () => this.toggleSort(c.key));
      const content = h("div", "pf-v6-c-table__button-content");
      content.append(h("span", "pf-v6-c-table__text", c.label), indicator(null));
      btn.append(content);
      th.append(btn);
      tr.append(th);
    }
    thead.append(tr);
    this.tbody = h("tbody", "pf-v6-c-table__tbody");
    table.append(thead, this.tbody);
    wrap.append(table);

    this.emptyText = opts.emptyText ?? "No data yet.";
    this.empty = h("p", "console-table__empty", this.emptyText);
    this.empty.hidden = true;

    const host = h("div");
    host.append(wrap, this.empty);
    this.el = host;
    this.syncHeaders();
  }

  // setUnresolved swaps the empty line's copy for a reason the table has no rows to show, and hides
  // the TABLE (header and body both) while it stands. Without it a table that never received data
  // renders as a bare header - a shape that reads as "measured, and there was nothing", which is
  // the one claim it cannot make. Pass null once data arrives.
  setUnresolved(reason: string | null): void {
    // Idempotent: every tile calls this on EVERY store frame, and the live status stream produces
    // them continuously. Without this the null case re-sorts and rebuilds the whole tbody a second
    // time per frame, on top of the setRows() that follows it.
    if (reason === this.unresolved) return;
    this.unresolved = reason;
    if (reason === null) {
      this.empty.textContent = this.emptyText;
      this.wrap.hidden = false;
      this.render();
      return;
    }
    this.empty.textContent = reason;
    this.empty.hidden = false;
    this.wrap.hidden = true;
  }

  // setEmptyText changes what an empty table says, for a caller whose reason for no rows varies
  // (nothing exists, or a filter hides all of it).
  setEmptyText(text: string): void {
    this.emptyText = text;
    if (this.unresolved === null) this.empty.textContent = text;
  }

  setRows(rows: T[]): void {
    this.rows = rows;
    this.render();
  }

  private firstDir(key: string): SortDir {
    return this.cols.find((c) => c.key === key)?.numeric ? "desc" : "asc";
  }

  private toggleSort(key: string): void {
    if (this.sortKey === key) this.sortDir = this.sortDir === "asc" ? "desc" : "asc";
    else {
      this.sortKey = key;
      this.sortDir = this.firstDir(key);
    }
    this.syncHeaders();
    this.render();
  }

  private syncHeaders(): void {
    for (const th of this.el.querySelectorAll<HTMLElement>("th")) {
      const active = th.dataset.key === this.sortKey;
      th.classList.toggle("pf-m-selected", active);
      if (active) {
        th.dataset.sort = this.sortDir;
        th.setAttribute("aria-sort", this.sortDir === "asc" ? "ascending" : "descending");
      } else {
        th.dataset.sort = "";
        th.removeAttribute("aria-sort");
      }
      th.querySelector(".pf-v6-c-table__sort-indicator")?.replaceWith(
        indicator(active ? this.sortDir : null),
      );
    }
  }

  private render(): void {
    const col = this.cols.find((c) => c.key === this.sortKey) ?? this.cols[0];
    const sign = this.sortDir === "asc" ? 1 : -1;
    const sorted = this.rows.slice().sort((a, b) => sign * compare(col.sort(a), col.sort(b)));
    this.tbody.replaceChildren();
    for (const row of sorted) {
      const tr = h("tr", "pf-v6-c-table__tr");
      for (const c of this.cols) {
        const td = h("td", "pf-v6-c-table__td", c.text(row));
        td.dataset.label = c.label;
        if (c.numeric) td.dataset.num = "";
        tr.append(td);
      }
      this.tbody.append(tr);
    }
    // A pending reason outranks the row count: rows arriving from an earlier poll must not turn the
    // reason back into a header a reader would take as current.
    this.empty.hidden = this.unresolved === null && sorted.length > 0;
  }
}
