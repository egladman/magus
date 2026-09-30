// table.ts - the console's one sortable table, shared by the Dashboard's tiles and the Tools app.
// Its rules are styles/table.css, loaded with the shell.

import { h } from "../desktop/view";

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
  private emptyText: string;
  private wrap: HTMLElement;
  private unresolved: string | null = null;

  constructor(cols: Column<T>[], opts: { sortKey?: string; emptyText?: string } = {}) {
    this.cols = cols;
    this.sortKey = opts.sortKey ?? cols[0].key;
    this.sortDir = 1;

    const wrap = h("div", "console-table__wrap");
    this.wrap = wrap;
    const table = h("table", "console-table");
    const thead = h("thead");
    const tr = h("tr");
    for (const c of cols) {
      const th = h("th");
      if (c.numeric) th.dataset.num = "";
      const btn = h("button", "console-table__sort", c.label);
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

  private toggleSort(key: string): void {
    if (this.sortKey === key) this.sortDir = this.sortDir === 1 ? -1 : 1;
    else {
      this.sortKey = key;
      this.sortDir = 1;
    }
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
      const va = col.sort(a),
        vb = col.sort(b);
      let cmp: number;
      if (typeof va === "number" && typeof vb === "number")
        cmp = vb - va; // numbers: high-to-low as "desc"
      else cmp = String(va) < String(vb) ? -1 : String(va) > String(vb) ? 1 : 0;
      return this.sortDir === 1 ? cmp : -cmp;
    });
    this.tbody.replaceChildren();
    for (const row of sorted) {
      const tr = h("tr");
      for (const c of this.cols) {
        const td = h("td", undefined, c.text(row));
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
