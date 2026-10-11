// table.ts - the console's one sortable table, shared by the Dashboard's tiles and the Tools app. It is
// a PatternFly compact Table with a sortable header. Its rules are styles/components/Table/table.css, loaded with the
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

// PF's sort glyphs, 32-unit viewBox: the amount-sort arrow for a sorted column and the up/down pair
// for one that can be. PF ships ascending only; the descending glyph is that path flipped
// top to bottom, as its React icon set draws the pair.
const SORT_ASC =
  "M30 16a1 1 0 0 1-1 1H15a1 1 0 0 1 0-2h14a1 1 0 0 1 1 1Zm-5 5H15a1 1 0 1 0 0 2h10a1 1 0 1 0 0-2Zm-4 6h-6a1 1 0 1 0 0 2h6a1 1 0 1 0 0-2Zm-6-17a.999.999 0 0 0 .707-1.707l-5.646-5.646a1.501 1.501 0 0 0-2.121 0L2.294 8.293a.999.999 0 1 0 1.414 1.414l4.293-4.293V29a1 1 0 1 0 2 0V5.414l4.293 4.293a.997.997 0 0 0 .707.293Z";
const SORT_NONE =
  "M21.293 23.293 17 27.586V4.414l4.293 4.293a.997.997 0 0 0 1.414 0 .999.999 0 0 0 0-1.414l-5.646-5.646a1.5 1.5 0 0 0-2.121 0L9.294 7.293a.999.999 0 1 0 1.414 1.414l4.293-4.293v23.172l-4.293-4.293a.999.999 0 1 0-1.414 1.414l5.646 5.646c.292.293.676.438 1.061.438s.768-.146 1.061-.438l5.646-5.646a.999.999 0 1 0-1.414-1.414Z";

// indicator draws the header's sort glyph: ascending, descending, or the pair that says "sortable".
function indicator(dir: SortDir | null): HTMLElement {
  const span = h("span", "pf-v6-c-table__sort-indicator");
  const svg = document.createElementNS(SVG, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", "0 0 32 32");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("role", "img");
  const path = document.createElementNS(SVG, "path");
  path.setAttribute("d", dir === null ? SORT_NONE : SORT_ASC);
  if (dir === "desc") path.setAttribute("transform", "matrix(1 0 0 -1 0 32)");
  svg.append(path);
  span.append(svg);
  return span;
}

// gridBreakpoint picks the container width below which PF stacks the table into labelled rows. A table
// needs room for its columns, so the more it has the wider the pane must be before it stays tabular.
function gridBreakpoint(columns: number): "md" | "lg" | "xl" {
  if (columns <= 4) return "md";
  return columns <= 7 ? "lg" : "xl";
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

    // The wrapper is the container PF's grid breakpoints key on, so the table stacks when its PANE is
    // narrow rather than when the window is.
    const wrap = h("div", "console-table__wrap");
    this.wrap = wrap;
    const table = h(
      "table",
      `pf-v6-c-table pf-m-compact pf-m-grid-${gridBreakpoint(cols.length)} pf-m-sticky-header console-table`,
    );
    table.setAttribute("role", "grid");
    table.setAttribute("aria-label", opts.label ?? "Table");
    if (opts.caption) {
      const caption = h("caption", "pf-v6-c-table__caption", opts.caption);
      table.append(caption);
    }
    const thead = h("thead", "pf-v6-c-table__thead");
    const tr = h("tr", "pf-v6-c-table__tr");
    tr.setAttribute("role", "row");
    for (const c of cols) {
      const th = h("th", "pf-v6-c-table__th pf-v6-c-table__sort pf-m-nowrap");
      th.setAttribute("role", "columnheader");
      th.scope = "col";
      if (c.numeric) th.dataset.num = "";
      th.dataset.key = c.key;
      const btn = h("button", "pf-v6-c-table__button");
      btn.type = "button";
      btn.addEventListener("click", () => this.toggleSort(c.key));
      const content = h("span", "pf-v6-c-table__button-content");
      content.append(h("span", "pf-v6-c-table__text", c.label), indicator(null));
      btn.append(content);
      th.append(btn);
      tr.append(th);
    }
    thead.append(tr);
    this.tbody = h("tbody", "pf-v6-c-table__tbody");
    this.tbody.setAttribute("role", "rowgroup");
    table.append(thead, this.tbody);
    wrap.append(table);

    this.emptyText = opts.emptyText ?? "No data yet.";
    this.empty = h("p", "console-table__empty", this.emptyText);
    this.empty.hidden = true;

    // The frame carries the scroll cue: a fade on each edge that has more table past it. It sits
    // around the scroller because the cue must not scroll away with the rows it hints at.
    const host = h("div", "console-table__frame");
    host.append(wrap, this.empty);
    this.el = host;
    this.syncHeaders();
    this.watchOverflow();
  }

  // watchOverflow keeps the frame's data-more naming the edges with unseen table past them. The
  // wrapper scrolls (so does the page, if nothing bounds it), and a cue that only knew the first
  // paint would stay lit after the reader reached the end.
  private watchOverflow(): void {
    this.wrap.addEventListener("scroll", () => this.syncOverflow(), { passive: true });
    if (typeof ResizeObserver !== "undefined") {
      const ro = new ResizeObserver(() => this.syncOverflow());
      ro.observe(this.wrap);
      const table = this.wrap.firstElementChild;
      if (table) ro.observe(table);
    }
  }

  private syncOverflow(): void {
    const w = this.wrap;
    const edges: string[] = [];
    if (w.scrollLeft > 1) edges.push("inline-start");
    if (w.scrollLeft + w.clientWidth < w.scrollWidth - 1) edges.push("inline-end");
    if (w.scrollTop + w.clientHeight < w.scrollHeight - 1) edges.push("block-end");
    if (edges.length > 0) this.el.dataset.more = edges.join(" ");
    else delete this.el.dataset.more;
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
      tr.setAttribute("role", "row");
      for (const c of this.cols) {
        const td = h("td", "pf-v6-c-table__td pf-m-nowrap", c.text(row));
        td.setAttribute("role", "cell");
        td.dataset.label = c.label;
        if (c.numeric) td.dataset.num = "";
        tr.append(td);
      }
      this.tbody.append(tr);
    }
    // A pending reason outranks the row count: rows arriving from an earlier poll must not turn the
    // reason back into a header a reader would take as current.
    this.empty.hidden = this.unresolved === null && sorted.length > 0;
    this.syncOverflow();
  }
}
