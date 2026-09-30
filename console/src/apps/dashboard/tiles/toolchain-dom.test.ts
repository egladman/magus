// toolchain-dom.test.ts - the Toolchain tile, a summary that opens the Tools app. document/window
// come from test-setup.mjs (node --import), the same harness alerts-dom.test.ts runs under.
//
// Two readings are worth pinning, because both are wrong in a way that looks fine on screen: a note
// that says "all inside their window" while a violation sits in the workspace, and a clean count
// when the lifecycle provider never answered. A reader acts on the summary without opening the app.

import assert from "node:assert/strict";
import { test } from "node:test";
import { toolchainTile } from "./toolchain";
import { surfaceNavigationEvent } from "../../../desktop/surface-navigation";
import { initialState, type DashboardState, type LifecycleView, type ToolRowView } from "../state";

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
    cycle: "",
    eol: "",
    support: "",
    ...over,
  };
}

const unwired: LifecycleView = { provider: "", state: "unwired", sources: [], detail: "" };

function stateWith(rows: ToolRowView[], lifecycle: LifecycleView = unwired): DashboardState {
  return {
    ...initialState(),
    tools: { rows, violations: rows.filter((r) => r.code !== "").length, lifecycle },
  };
}

// counts reads the four cells as label -> value.
function counts(el: HTMLElement): Record<string, string> {
  return Object.fromEntries(
    [...el.querySelectorAll(".console-dashboard-stat")].map((cell) => [
      cell.querySelector(".console-dashboard-stat__key")?.textContent ?? "",
      cell.querySelector(".console-dashboard-stat__value")?.textContent ?? "",
    ]),
  );
}

function note(el: HTMLElement): string {
  return el.querySelector(".console-dashboard-tile__note")?.textContent ?? "";
}

const live: LifecycleView = {
  provider: "endoflife-date",
  state: "live",
  sources: ["https://endoflife.date/api/v1/products/go"],
  detail: "",
};

test("the summary counts what needs acting on, and states the zeros", () => {
  const tile = toolchainTile();
  tile.update(
    stateWith(
      [
        row({ installed: "v1.25.3", support: "eol", workspaceWindow: "" }),
        row({ bin: "node", support: "supported" }),
        row({ bin: "pnpm", support: "unannounced", workspaceWindow: "" }),
        row({ bin: "buf", support: "unannounced" }),
      ],
      live,
    ),
  );
  assert.deepEqual(counts(tile.el), {
    "Past end of life": "1",
    "Outside window": "0",
    Unannounced: "2",
    Unpinned: "2",
  });
});

test("a violation is counted and named in the note with the lifecycle note after it", () => {
  // The console and a terminal must not disagree about the same pair: the count is the rows that
  // carry a diagnostic code, the same ones `magus run` would fail on.
  const tile = toolchainTile();
  tile.update(
    stateWith(
      [row({ bin: "node", verdict: "too new", code: "MGS3006" }), row({ support: "unknown" })],
      { provider: "endoflife-date", state: "unreached", sources: [], detail: "no route to host" },
    ),
  );
  assert.equal(counts(tile.el)["Outside window"], "1");
  assert.equal(
    note(tile.el),
    "1 outside their window; end of life unknown: endoflife-date did not answer (no route to host)",
  );
});

test("the note reports zero violations rather than staying quiet about them", () => {
  // Silence reads as "not checked". A workspace whose toolchain is current is a result, and the
  // reader should be able to tell it apart from a tile that never loaded.
  const tile = toolchainTile();
  tile.update(stateWith([row(), row({ project: "docs", bin: "pnpm" })]));
  assert.equal(note(tile.el), "2 tools, all inside their window");
});

test("the tile holds no table: the rows live in the Tools app", () => {
  const tile = toolchainTile();
  tile.update(stateWith([row()]));
  assert.equal(tile.el.querySelectorAll("table").length, 0);
});

test("Open Tools asks the shell to open the tools app", () => {
  const tile = toolchainTile();
  const opened: unknown[] = [];
  const listen = (e: Event): void => {
    opened.push((e as CustomEvent).detail);
  };
  window.addEventListener(surfaceNavigationEvent, listen);
  try {
    const open = tile.el.querySelector<HTMLButtonElement>("[data-open-surface]");
    assert.equal(open?.dataset.openSurface, "tools");
    assert.equal(open?.textContent, "Open Tools");
    open?.click();
  } finally {
    window.removeEventListener(surfaceNavigationEvent, listen);
  }
  assert.deepEqual(opened, [{ pageId: "tools" }]);
});

test("an empty view keeps the tile's own note and offers the empty state", () => {
  const tile = toolchainTile();
  tile.update(initialState());
  assert.match(note(tile.el), /version window each is held to/);
  assert.equal(tile.el.querySelector<HTMLElement>(".console-dashboard-statstrip")?.hidden, true);
  assert.match(
    tile.el.querySelector(".console-dashboard-row__empty")?.textContent ?? "",
    /supported/,
  );
});

test("rows arriving hide the empty state and show the counts", () => {
  const tile = toolchainTile();
  tile.update(initialState());
  tile.update(stateWith([row()]));
  assert.equal(tile.el.querySelector<HTMLElement>(".console-dashboard-statstrip")?.hidden, false);
  assert.equal(tile.el.querySelector<HTMLElement>(".console-dashboard-row__empty")?.hidden, true);
});
