// table-dom.test.ts - the shared sortable table: what a header click does to the row order, and
// the three things its empty line can say. document comes from test-setup.mjs (node --import).

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { SortableTable, type Column } from "./table";

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

function names(t: SortableTable<Row>): string[] {
  return [...t.el.querySelectorAll("tbody tr")].map(
    (tr) => tr.firstElementChild?.textContent ?? "",
  );
}

function header(t: SortableTable<Row>, key: string): HTMLElement {
  const th = t.el.querySelector<HTMLElement>(`th[data-key="${key}"]`);
  assert.ok(th, `header ${key}`);
  return th;
}

describe("SortableTable", () => {
  test("sorts by the first column, then a click on it reverses", () => {
    const t = new SortableTable(cols);
    t.setRows(rows);
    assert.deepEqual(names(t), ["a", "b", "c"]);
    assert.equal(header(t, "name").dataset.sort, "desc");

    header(t, "name").querySelector("button")?.click();
    assert.deepEqual(names(t), ["c", "b", "a"]);
    assert.equal(header(t, "name").dataset.sort, "asc");
  });

  test("a numeric column starts high-to-low and marks its cells", () => {
    const t = new SortableTable(cols);
    t.setRows(rows);
    header(t, "hits").querySelector("button")?.click();
    assert.deepEqual(
      [...t.el.querySelectorAll("tbody td[data-num]")].map((td) => td.textContent),
      ["9", "5", "2"],
    );
    assert.equal(header(t, "name").dataset.sort, "");
  });

  test("the empty line shows with no rows, and follows setEmptyText", () => {
    const t = new SortableTable(cols, { emptyText: "Loading..." });
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
    const t = new SortableTable(cols);
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
});
