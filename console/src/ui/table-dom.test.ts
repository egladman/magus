// table-dom.test.ts - the shared sortable table: what a header click does to the row order and to
// aria-sort, the one meaning "asc" and "desc" have for every column type, the accessible structure, and
// the three things its empty line can say. document comes from test-setup.mjs (node --import).

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { must } from "../lib/guards";
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
    assert.equal(table?.getAttribute("role"), "grid", "PF requires it on a sortable table");
    assert.ok(table?.classList.contains("pf-m-grid-md"), "a two-column table stacks at md");
    for (const th of t.el.querySelectorAll("th")) {
      assert.equal(th.getAttribute("scope"), "col");
      assert.equal(th.getAttribute("role"), "columnheader");
      assert.ok(th.classList.contains("pf-m-nowrap"), "a header never truncates to a sliver");
      assert.ok(th.querySelector("button[type=button]"), "the sort control is a real button");
      assert.equal(th.querySelector("svg")?.getAttribute("aria-hidden"), "true");
    }
    const td = t.el.querySelector("tbody td");
    assert.equal(td?.getAttribute("data-label"), "Name");
    assert.equal(td?.getAttribute("role"), "cell");
    assert.equal(t.el.querySelector("tbody")?.getAttribute("role"), "rowgroup");
    assert.equal(t.el.querySelector("tbody tr")?.getAttribute("role"), "row");
  });

  test("the grid breakpoint widens with the number of columns", () => {
    const wide = (n: number): Column<Row>[] =>
      Array.from({ length: n }, (_, i) => ({
        key: "c" + i,
        label: "C" + i,
        text: (r: Row) => r.name,
        sort: (r: Row) => r.name,
      }));
    const mode = (n: number): string | undefined => {
      const table = must(new SortableTable(wide(n), opts).el.querySelector("table"));
      return [...table.classList].find((c) => c.startsWith("pf-m-grid-"));
    };
    assert.equal(mode(3), "pf-m-grid-md");
    assert.equal(mode(6), "pf-m-grid-lg");
    assert.equal(mode(11), "pf-m-grid-xl");
  });

  test("the frame names the edges of the table that scroll out of view", () => {
    const t = new SortableTable(cols, opts);
    const wrap = must(t.el.querySelector<HTMLElement>(".console-table__wrap"));
    // Nothing overflows in the test document, so no edge is named.
    t.setRows(rows);
    assert.equal(t.el.dataset.more, undefined);

    const metrics = (m: Record<string, number>): void => {
      for (const [k, v] of Object.entries(m)) {
        Object.defineProperty(wrap, k, { value: v, configurable: true });
      }
    };
    // 300 wide in a 200 box: the end has more table past it, and scrolling to the far end flips it.
    metrics({
      clientWidth: 200,
      scrollWidth: 300,
      clientHeight: 50,
      scrollHeight: 50,
      scrollLeft: 0,
    });
    wrap.dispatchEvent(new Event("scroll"));
    assert.equal(t.el.dataset.more, "inline-end");
    metrics({ scrollLeft: 50 });
    wrap.dispatchEvent(new Event("scroll"));
    assert.equal(t.el.dataset.more, "inline-start inline-end");
    metrics({ scrollLeft: 100 });
    wrap.dispatchEvent(new Event("scroll"));
    assert.equal(t.el.dataset.more, "inline-start");
  });

  test("the table sits directly in one scroll wrapper", () => {
    const t = new SortableTable(cols, opts);
    const wrap = t.el.querySelector(".console-table__wrap");
    assert.equal(wrap?.querySelector("table")?.parentElement, wrap);
    assert.equal(t.el.querySelectorAll(".console-table__wrap").length, 1);
  });

  test("a header click moves only the sort arrow's direction", () => {
    const t = new SortableTable(cols, opts);
    const path = (key: string): string => header(t, key).querySelector("path")?.outerHTML ?? "";
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
