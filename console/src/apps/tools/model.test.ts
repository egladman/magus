import assert from "node:assert/strict";
import { test } from "node:test";
import type { LifecycleView, ToolRowView } from "../dashboard/state";
import {
  applyFilters,
  columns,
  declaredBy,
  lifecycleNote,
  lifecycleSource,
  toolCounts,
  type ToolFilterKey,
} from "./model";

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

const bins = (rows: ToolRowView[]): string[] => rows.map((r) => r.bin);
const on = (...keys: ToolFilterKey[]): Set<ToolFilterKey> => new Set(keys);

const rows = [
  row({ bin: "old", support: "eol" }),
  row({ bin: "tbd", support: "unannounced", workspaceWindow: "" }),
  row({ bin: "loose", support: "supported", workspaceWindow: "" }),
  row({ bin: "both", support: "eol", workspaceWindow: "" }),
];

test("each filter keeps only its rows and none keeps all", () => {
  assert.deepEqual(bins(applyFilters(rows, on())), ["old", "tbd", "loose", "both"]);
  assert.deepEqual(bins(applyFilters(rows, on("eol"))), ["old", "both"]);
  assert.deepEqual(bins(applyFilters(rows, on("unannounced"))), ["tbd"]);
  assert.deepEqual(bins(applyFilters(rows, on("unpinned"))), ["tbd", "loose", "both"]);
});

test("filters combine by narrowing", () => {
  assert.deepEqual(bins(applyFilters(rows, on("eol", "unpinned"))), ["both"]);
  assert.deepEqual(bins(applyFilters(rows, on("eol", "unannounced"))), []);
});

test("a tool with only its spell's window is unpinned", () => {
  const spellOnly = row({ spellWindow: ">= 1.25", workspaceWindow: "" });
  assert.equal(applyFilters([spellOnly], on("unpinned")).length, 1);
});

test("counts agree with the filters that produce them", () => {
  const violating = row({ bin: "bad", code: "MGS3005", verdict: "too old" });
  assert.deepEqual(toolCounts([...rows, violating]), {
    total: 5,
    pastEol: 2,
    outsideWindow: 1,
    unannounced: 1,
    unpinned: 3,
  });
});

test("columns show the pin, and say unpinned rather than leaving it blank", () => {
  const pin = columns().find((c) => c.key === "pin");
  assert.equal(pin?.text(row()), ">= 1.26");
  assert.equal(pin?.text(row({ workspaceWindow: "" })), "unpinned");
});

test("the declared-by column separates the spell's window from the project's", () => {
  // Whose bound is failing is the first question, and the intersection has already discarded that
  // by the time the CLI builds its message. This column is the only place the answer survives.
  assert.deepEqual(
    [
      row({ spellWindow: ">= 1.26", workspaceWindow: "" }),
      row({ spellWindow: "", workspaceWindow: ">= 22" }),
      row({ spellWindow: ">= 1.26", workspaceWindow: ">= 1.27" }),
      row({ spellWindow: "", workspaceWindow: "" }),
    ].map(declaredBy),
    ["spell", "workspace", "spell + workspace", "-"],
  );
});

test("a violation is named with the code the CLI would raise", () => {
  const verdict = columns().find((c) => c.key === "verdict");
  assert.equal(verdict?.text(row({ verdict: "too new", code: "MGS3006" })), "too new (MGS3006)");
  assert.equal(verdict?.text(row({ verdict: "unknown" })), "unknown");
});

test("a tool that could not be probed reads as not found and has no probe age", () => {
  const cols = columns(() => 100_000);
  const cell = (key: string, r: ToolRowView): string | undefined =>
    cols.find((c) => c.key === key)?.text(r);
  const absent = row({ installed: "", verdict: "unknown" });
  assert.equal(cell("installed", absent), "not found");
  assert.equal(cell("probed", absent), "-");
  assert.equal(cell("probed", row({ probedAtMs: 70_000 })), "30s ago");
  assert.equal(cell("probed", row({ probedAtMs: 100_000 - 5 * 60_000 })), "5m ago");
});

test("a tool whose spell names no product shows dashes, not unknown", () => {
  const cols = columns();
  const dashes = ["cycle", "eol", "support"].map((k) => cols.find((c) => c.key === k)?.text(row()));
  assert.deepEqual(dashes, ["-", "-", "-"]);
});

test("the lifecycle note speaks only when the answer is not live", () => {
  const state = (over: Partial<LifecycleView>): LifecycleView => ({
    provider: "endoflife-date",
    state: "live",
    sources: [],
    detail: "",
    ...over,
  });
  assert.equal(lifecycleNote(state({})), "");
  assert.equal(lifecycleNote(state({ state: "cached" })), "");
  assert.equal(lifecycleNote(undefined), "");
  assert.match(lifecycleNote(state({ state: "offline" })), /MAGUS_OFFLINE/);
  assert.equal(
    lifecycleNote(state({ state: "unreached", detail: "no route to host" })),
    "end of life unknown: endoflife-date did not answer (no route to host)",
  );
  assert.equal(lifecycleSource(state({ state: "cached" })), "endoflife-date (cached)");
  assert.equal(lifecycleSource(state({ state: "unwired", provider: "" })), "");
  assert.equal(lifecycleSource(state({ state: "unreached" })), "");
});
