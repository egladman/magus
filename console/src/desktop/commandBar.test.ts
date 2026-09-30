// commandBar.test.ts - matchCommands is the command bar's ranking logic (a pure fuzzy-subsequence
// filter that reports match positions and a score), tested here without a DOM. The overlay itself is
// exercised in the browser harness.

import { test } from "node:test";
import assert from "node:assert/strict";
import { matchCommands, matchTargets, displayToken } from "./commandBar";
import type { Command, CommandTarget } from "./commands";

const cmd = (id: string, label: string, group?: string): Command => ({
  id,
  label,
  group,
  run() {},
});
const cmds: Command[] = [
  cmd("console.tab.new", "New tab", "Tabs"),
  cmd("console.tab.close", "Close pane or tab", "Tabs"),
  cmd("console.pane.split", "Split pane", "Panes"),
  cmd("console.cheatsheet.toggle", "Keyboard shortcuts", "General"),
];

test("displayToken drops the console namespace prefix and leaves bare ids alone", () => {
  assert.equal(displayToken("console.open.logs"), "open.logs");
  assert.equal(displayToken("bare.id"), "bare.id");
});

test("an empty query returns every command in registry order, unscored", () => {
  const r = matchCommands(cmds, "");
  assert.deepEqual(
    r.map((m) => m.command.id),
    cmds.map((c) => c.id),
  );
  assert.deepEqual(
    r.map((m) => m.score),
    [0, 0, 0, 0],
  );
  assert.deepEqual(r[0].hits, []);
});

test("a subsequence over the token matches and reports its hit positions", () => {
  const r = matchCommands(cmds, "split");
  assert.deepEqual(
    r.map((m) => m.command.id),
    ["console.pane.split"],
  );
  // "split" lands on s p l i t in "pane.split" (indices 5..9).
  assert.deepEqual(r[0].hits, [5, 6, 7, 8, 9]);
});

test("a non-contiguous subsequence still matches (tc -> tab.close)", () => {
  const ids = matchCommands(cmds, "tc").map((m) => m.command.id);
  assert.ok(ids.includes("console.tab.close"));
});

test("token-start matches outrank scattered mid-string ones", () => {
  // "ta" anchors tab.new / tab.close at the token start; it only appears scattered (low score) in the
  // other commands, so the two tab commands must rank first, ahead of pane.split.
  const ids = matchCommands(cmds, "ta").map((m) => m.command.id);
  assert.deepEqual(ids.slice(0, 2), ["console.tab.new", "console.tab.close"]);
  assert.ok(ids.indexOf("console.pane.split") > ids.indexOf("console.tab.close"));
});

test("a command is findable by its prose label, but label hits are not reported for token highlight", () => {
  const r = matchCommands(cmds, "keyboard");
  assert.deepEqual(
    r.map((m) => m.command.id),
    ["console.cheatsheet.toggle"],
  );
  assert.deepEqual(r[0].hits, []); // the match is in the label, past the token, so nothing to highlight
});

test("returns nothing when the query is a subsequence of no token or label", () => {
  assert.deepEqual(matchCommands(cmds, "zzq"), []);
});

// --- matchTargets: the second stage's ranking ---------------------------------
// Targets have no token - a tab is named, not addressed - so unlike matchCommands every hit is on the
// label and every hit is reported, because the label is what the row renders.

const targets: CommandTarget[] = [
  { value: "t1", label: "Dashboard" },
  { value: "t2", label: "Log Viewer" },
  { value: "t3", label: "main.ts - console" },
  { value: "t4", label: "main.ts - logs" },
];

test("an empty query returns every target in order, unscored", () => {
  const r = matchTargets(targets, "");
  assert.deepEqual(
    r.map((m) => m.target.value),
    ["t1", "t2", "t3", "t4"],
  );
  assert.deepEqual(
    r.map((m) => m.score),
    [0, 0, 0, 0],
  );
});

test("matchTargets reports hits on the LABEL, since that is what a target row renders", () => {
  const r = matchTargets(targets, "dash");
  assert.deepEqual(
    r.map((m) => m.target.value),
    ["t1"],
  );
  assert.deepEqual(r[0].hits, [0, 1, 2, 3]);
});

test("two same-named targets stay tellable apart by their disambiguating suffix", () => {
  // The suffix is what tabViews hands over for tabs that would otherwise both read main.ts, so
  // filtering on it has to reach exactly one of them.
  assert.deepEqual(
    matchTargets(targets, "logs").map((m) => m.target.value),
    ["t4"],
  );
});

test("returns nothing when the query is a subsequence of no label", () => {
  assert.deepEqual(matchTargets(targets, "zzq"), []);
});
