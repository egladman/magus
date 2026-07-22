// keybindings.test.ts - keybindingRows is the editor's only logic (compute each command's effective
// chord + where it came from), so it is tested here without a DOM. The capture/table live in the
// browser harness.

import { test } from "node:test";
import assert from "node:assert/strict";
import { keybindingRows } from "./keybindings";
import type { Command, Keymap } from "./commands";

const cmd = (id: string, label: string, group: string): Command => ({ id, label, group, run() {} });
const commands: Command[] = [
  cmd("tab.new", "New tab", "Tabs"),
  cmd("pane.split", "Split pane", "Panes"),
  cmd("pane.focusLeft", "Focus pane left", "Panes"),
];
const defaults: Keymap = { "tab.new": "mod+t", "pane.split": "mod+\\", "pane.focusLeft": "alt+h" };

test("keybindingRows: no override -> the default chord, source default", () => {
  const rows = keybindingRows(commands, defaults, {});
  assert.deepEqual(rows, [
    { id: "tab.new", label: "New tab", group: "Tabs", chord: "mod+t", source: "default" },
    { id: "pane.split", label: "Split pane", group: "Panes", chord: "mod+\\", source: "default" },
    {
      id: "pane.focusLeft",
      label: "Focus pane left",
      group: "Panes",
      chord: "alt+h",
      source: "default",
    },
  ]);
});

test("keybindingRows: a user override wins and is marked custom; a normalized value matches", () => {
  const rows = keybindingRows(commands, defaults, { "tab.new": "Cmd+N" });
  assert.deepEqual(rows[0], {
    id: "tab.new",
    label: "New tab",
    group: "Tabs",
    chord: "mod+n",
    source: "custom",
  });
});

test("keybindingRows: an empty-string override is a disabled binding", () => {
  const rows = keybindingRows(commands, defaults, { "pane.split": "" });
  assert.deepEqual(rows[1], {
    id: "pane.split",
    label: "Split pane",
    group: "Panes",
    chord: "",
    source: "disabled",
  });
});
