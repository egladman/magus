// table-dom.test.ts - the shared sortable table: what a header click does to the row order and to
// aria-sort, the one meaning "asc" and "desc" have for every column type, the accessible structure, and
// the three things its empty line can say. document comes from test-setup.mjs (node --import).

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { type Column, SortableTable } from "./table";

interface Row {
  name: string;
  hits: number;
}

const cols: Column<Row>[] = [
  { key: "name", label: "Name", text: (r) => r.name, sort: (r) => r.name },
  { key: "hits", label: "Hits", text: (r) => String(r.hits), sort: (r) => r.hits, numeric: true },
];

const rows: Row[] = [
  { name: "b", hits: 2 },
  { name: "c", hits: 9 },
  { name: "a", hits: 5 },
];

const opts = { label: "Targets" };

function names(t: SortableTable<Row>): string[] {
  return [...t.el.querySelectorAll("tbody tr")].map(
    (tr) => tr.firstElementChild?.textContent ?? "",
  );
}

function hits(t: SortableTable<Row>): string[] {
  return [...t.el.querySelectorAll("tbody td[data-num]")].map((td) => td.textContent ?? "");
}

function header(t: SortableTable<Row>, key: string): HTMLElement {
  const th = t.el.querySelector<HTMLElement>(`th[data-key="${key}"]`);
  assert.ok(th, `header ${key}`);
  return th;
}

function click(t: SortableTable<Row>, key: string): void {
  header(t, key).querySelector("button")?.click();
}

describe("SortableTable", () => {
  test("a text column sorts A to Z, then a click reverses it", () => {
    const t = new SortableTable(cols, opts);
    t.setRows(rows);
    assert.deepEqual(names(t), ["a", "b", "c"]);
    assert.equal(header(t, "name").getAttribute("aria-sort"), "ascending");
    assert.equal(header(t, "name").dataset.sort, "asc");

    click(t, "name");
    assert.deepEqual(names(t), ["c", "b", "a"]);
    assert.equal(header(t, "name").getAttribute("aria-sort"), "descending");
    assert.equal(header(t, "name").dataset.sort, "desc");
  });

  test("asc is low to high for a number column too; it opens on the largest", () => {
    const t = new SortableTable(cols, opts);
    t.setRows(rows);
    click(t, "hits");
    assert.deepEqual(hits(t), ["9", "5", "2"], "first sort of a numeric column is descending");
    assert.equal(header(t, "hits").getAttribute("aria-sort"), "descending");

    click(t, "hits");
    assert.deepEqual(hits(t), ["2", "5", "9"], "ascending is low to high");
    assert.equal(header(t, "hits").getAttribute("aria-sort"), "ascending");
    assert.equal(header(t, "name").getAttribute("aria-sort"), null, "only the sorted one");
    assert.equal(header(t, "name").dataset.sort, "");
  });

  test("the initial sort can be named, in either direction", () => {
    const t = new SortableTable(cols, { ...opts, sortKey: "hits", sortDir: "asc" });
    t.setRows(rows);
    assert.deepEqual(hits(t), ["2", "5", "9"]);
    assert.equal(header(t, "hits").getAttribute("aria-sort"), "ascending");
  });

  test("text sorts case-insensitively with digit runs by value", () => {
    const t = new SortableTable(cols, opts);
    t.setRows([
      { name: "run 10", hits: 1 },
      { name: "Run 9", hits: 1 },
      { name: "alpha", hits: 1 },
    ]);
    assert.deepEqual(names(t), ["alpha", "Run 9", "run 10"]);
  });

  test("marks numeric cells, and the table is named", () => {
    const t = new SortableTable(cols, opts);
    t.setRows(rows);
    click(t, "hits");
    assert.equal(t.el.querySelectorAll("tbody td[data-num]").length, 3);
    assert.equal(t.el.querySelector("table")?.getAttribute("aria-label"), "Targets");
  });

  test("is a PF compact table with column-scoped headers and labelled cells", () => {
    const t = new SortableTable(cols, { ...opts, caption: "All targets" });
    t.setRows(rows);
    const table = t.el.querySelector("table");
    assert.ok(table?.classList.contains("pf-v6-c-table"));
    assert.ok(table?.classList.contains("pf-m-compact"));
    assert.ok(table?.classList.contains("pf-m-sticky-header"));
    assert.equal(table?.querySelector("caption")?.textContent, "All targets");
    for (const th of t.el.querySelectorAll("th")) {
      assert.equal(th.getAttribute("scope"), "col");
      assert.ok(th.querySelector("button[type=button]"), "the sort control is a real button");
      assert.equal(th.querySelector("svg")?.getAttribute("aria-hidden"), "true");
    }
    assert.equal(t.el.querySelector("tbody td")?.getAttribute("data-label"), "Name");
  });

  test("the table sits directly in one scroll wrapper", () => {
    const t = new SortableTable(cols, opts);
    const wrap = t.el.querySelector(".console-table__wrap");
    assert.equal(wrap?.querySelector("table")?.parentElement, wrap);
    assert.equal(t.el.querySelectorAll(".console-table__wrap").length, 1);
  });

  test("a header click moves only the sort arrow's direction", () => {
    const t = new SortableTable(cols, opts);
    const path = (key: string): string =>
      header(t, key).querySelector("path")?.getAttribute("d") ?? "";
    const arrow = (): string => path("name");
    const up = arrow();
    click(t, "name");
    assert.notEqual(arrow(), up);
    assert.notEqual(arrow(), path("hits"));
  });

  test("the empty line shows with no rows, and follows setEmptyText", () => {
    const t = new SortableTable(cols, { ...opts, emptyText: "Loading..." });
    const empty = t.el.querySelector<HTMLElement>(".console-table__empty");
    assert.ok(empty);
    t.setRows([]);
    assert.equal(empty.hidden, false);
    assert.equal(empty.textContent, "Loading...");

    t.setEmptyText("Nothing matches.");
    assert.equal(empty.textContent, "Nothing matches.");

    t.setRows(rows);
    assert.equal(empty.hidden, true);
  });

  test("an unresolved reason hides the table until it is cleared", () => {
    const t = new SortableTable(cols, opts);
    const wrap = t.el.querySelector<HTMLElement>(".console-table__wrap");
    const empty = t.el.querySelector<HTMLElement>(".console-table__empty");
    assert.ok(wrap && empty);
    t.setRows(rows);

    t.setUnresolved("Server not reachable.");
    assert.equal(wrap.hidden, true);
    assert.equal(empty.hidden, false);
    assert.equal(empty.textContent, "Server not reachable.");

    t.setUnresolved(null);
    assert.equal(wrap.hidden, false);
    assert.equal(empty.hidden, true);
    assert.deepEqual(names(t), ["a", "b", "c"]);
  });

  test("a caller that gives no label still gets a named table", () => {
    const t = new SortableTable(cols);
    assert.equal(t.el.querySelector("table")?.getAttribute("aria-label"), "Table");
  });
});
