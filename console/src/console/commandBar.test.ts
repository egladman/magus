// commandBar.test.ts - matchCommands is the command bar's ranking logic (a pure fuzzy-subsequence
// filter that reports match positions and a score), tested here without a DOM. The overlay itself is
// exercised in the browser harness.

import { test } from "node:test";
import assert from "node:assert/strict";
import { matchCommands, displayToken } from "./commandBar";
import type { Command } from "./commands";

const cmd = (id: string, label: string, group?: string): Command => ({ id, label, group, run() {} });
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
  assert.deepEqual(r.map((m) => m.command.id), cmds.map((c) => c.id));
  assert.deepEqual(r.map((m) => m.score), [0, 0, 0, 0]);
  assert.deepEqual(r[0].hits, []);
});

test("a subsequence over the token matches and reports its hit positions", () => {
  const r = matchCommands(cmds, "split");
  assert.deepEqual(r.map((m) => m.command.id), ["console.pane.split"]);
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
  assert.deepEqual(r.map((m) => m.command.id), ["console.cheatsheet.toggle"]);
  assert.deepEqual(r[0].hits, []); // the match is in the label, past the token, so nothing to highlight
});

test("returns nothing when the query is a subsequence of no token or label", () => {
  assert.deepEqual(matchCommands(cmds, "zzq"), []);
});
