// palette.test.ts - matchCommands is the palette's only logic (a pure substring filter), so it is
// tested here without a DOM. The overlay DOM in palette.ts is exercised in the browser harness.

import { test } from "node:test";
import assert from "node:assert/strict";
import { matchCommands } from "./palette";
import type { Command } from "./commands";

const cmd = (id: string, label: string, group?: string): Command => ({ id, label, group, run() {} });
const cmds: Command[] = [
  cmd("tab.new", "New tab", "Tabs"),
  cmd("tab.close", "Close pane or tab", "Tabs"),
  cmd("pane.split", "Split pane", "Panes"),
];

test("matchCommands with an empty query returns every command in order", () => {
  assert.deepEqual(matchCommands(cmds, "").map((c) => c.id), ["tab.new", "tab.close", "pane.split"]);
  assert.deepEqual(matchCommands(cmds, "   ").map((c) => c.id), ["tab.new", "tab.close", "pane.split"]);
});

test("matchCommands filters case-insensitively across group and label", () => {
  assert.deepEqual(matchCommands(cmds, "split").map((c) => c.id), ["pane.split"]);
  assert.deepEqual(matchCommands(cmds, "TAB").map((c) => c.id), ["tab.new", "tab.close"]); // group "Tabs"
  assert.deepEqual(matchCommands(cmds, "close").map((c) => c.id), ["tab.close"]);
});

test("matchCommands returns nothing when no command matches", () => {
  assert.deepEqual(matchCommands(cmds, "zzz"), []);
});
