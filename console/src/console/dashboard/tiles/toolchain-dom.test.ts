// toolchain-dom.test.ts - the Toolchain tile. document/window come from test-setup.mjs
// (node --import), the same harness alerts-dom.test.ts runs under.
//
// The markup is not what is worth pinning. Two readings are, because both are wrong in a way
// that looks fine on screen: a window whose `below` renders as a maximum (it is the first
// version REJECTED, so "< 25" accepts 24.19.0), and a note that says "all inside their window"
// while a violation sits in the table. A reader acts on the note before reading the rows.

import assert from "node:assert/strict";
import { test } from "node:test";
import { toolchainTile } from "./toolchain";
import { initialState, type DashboardState, type ToolRowView } from "../state";

function row(over: Partial<ToolRowView> = {}): ToolRowView {
  return {
    project: ".",
    bin: "go",
    spell: "go",
    installed: "v1.26.5",
    spellWindow: "",
    workspaceWindow: ">= 1.26",
    effectiveWindow: ">= 1.26",
    verdict: "inside",
    code: "",
    probedAtMs: 0,
    ...over,
  };
}

function stateWith(rows: ToolRowView[]): DashboardState {
  return {
    ...initialState(),
    tools: { rows, violations: rows.filter((r) => r.code !== "").length },
  };
}

function cells(el: HTMLElement): string[][] {
  return [...el.querySelectorAll("tbody tr")].map((tr) =>
    [...tr.querySelectorAll("td")].map((td) => td.textContent ?? ""),
  );
}

function note(el: HTMLElement): string {
  return el.querySelector(".console-dashboard-tile__note")?.textContent ?? "";
}

test("a violation is named with the code the CLI would raise", () => {
  // The console and a terminal must not disagree about the same pair. If the tile says a
  // version is fine and `magus run` fails on MGS3006, the tile is the thing that gets
  // distrusted.
  const tile = toolchainTile();
  tile.update(
    stateWith([
      row({
        project: "console",
        bin: "node",
        spell: "typescript",
        installed: "v26.5.0",
        workspaceWindow: ">= 22, < 25",
        effectiveWindow: ">= 22, < 25",
        verdict: "too new",
        code: "MGS3006",
      }),
    ]),
  );
  const [r] = cells(tile.el);
  assert.deepEqual(r.slice(0, 4), ["node", "console", "v26.5.0", ">= 22, < 25"]);
  assert.equal(r[5], "too new (MGS3006)");
  assert.match(note(tile.el), /^1 outside their window$/);
});

test("the note reports zero violations rather than staying quiet about them", () => {
  // Silence reads as "not checked". A workspace whose toolchain is current is a result, and
  // the reader should be able to tell it apart from a tile that never loaded.
  const tile = toolchainTile();
  tile.update(stateWith([row(), row({ project: "docs", bin: "pnpm" })]));
  assert.equal(note(tile.el), "2 tools, all inside their window");
});

test("the declared-by column separates the spell's window from the project's", () => {
  // Whose bound is failing is the first question, and Intersect has already discarded that by
  // the time the CLI builds its message. This column is the only place the answer survives.
  const tile = toolchainTile();
  tile.update(
    stateWith([
      row({ bin: "a", spellWindow: ">= 1.26", workspaceWindow: "" }),
      row({ bin: "b", spellWindow: "", workspaceWindow: ">= 22" }),
      row({ bin: "c", spellWindow: ">= 1.26", workspaceWindow: ">= 1.27" }),
      row({ bin: "d", spellWindow: "", workspaceWindow: "" }),
    ]),
  );
  assert.deepEqual(
    cells(tile.el).map((r) => r[4]),
    ["spell", "workspace", "spell + workspace", "-"],
  );
});

test("an unconstrained tool says so instead of rendering an empty window", () => {
  // A blank cell reads as missing data. Nothing constraining a tool is a real, common state.
  const tile = toolchainTile();
  tile.update(stateWith([row({ workspaceWindow: "", effectiveWindow: "" })]));
  assert.equal(cells(tile.el)[0][3], "unconstrained");
});

test("a tool that could not be probed reads as not found, not as a version", () => {
  const tile = toolchainTile();
  tile.update(stateWith([row({ installed: "", verdict: "unknown" })]));
  const [r] = cells(tile.el);
  assert.equal(r[2], "not found");
  assert.equal(r[5], "unknown", "an unprobeable tool is not a violation");
  assert.equal(r[6], "-", "and it has no probe age to show");
});

test("an empty view keeps the tile's own note and offers the empty state", () => {
  const tile = toolchainTile();
  tile.update(initialState());
  assert.equal(cells(tile.el).length, 0);
  assert.match(note(tile.el), /version window each is held to/);
  assert.match(
    tile.el.querySelector(".console-dashboard-row__empty")?.textContent ?? "",
    /supported/,
  );
});
