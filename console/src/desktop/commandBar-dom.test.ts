// commandBar-dom.test.ts - the command bar's TWO-STAGE flow, the half commandBar.test.ts cannot see.
// That file covers the two pure rankers; this covers what the bar does between them: a command that
// declares targets asks instead of running, Enter on a target dispatches the command WITH it, and
// Escape backs out of the question rather than dismissing the bar. happy-dom is registered globally
// by test-setup.mjs.
//
// Everything is built inside the tests rather than at module scope: the suite runs under
// --experimental-test-isolation=none, so one process is shared across files and a fixture hung on
// document at import time would be another file's problem too.

import assert from "node:assert/strict";
import { test } from "node:test";
import { createCommandBar } from "./commandBar";
import type { Command } from "./commands";

// A bar over two commands: one that acts immediately, one that asks which tab.
function bar(): {
  el: HTMLElement;
  open: () => void;
  ran: { id: string; arg?: unknown }[];
  input: HTMLInputElement;
  prompt: () => string;
  rows: () => string[];
  key: (k: string) => void;
  type: (v: string) => void;
} {
  const ran: { id: string; arg?: unknown }[] = [];
  const commands: Command[] = [
    { id: "console.sidebar.toggle", label: "Collapse", run() {} },
    {
      id: "console.tab.goto",
      label: "Go to tab",
      targets: () => [
        { value: "t1", label: "Dashboard" },
        { value: "t2", label: "Notes" },
      ],
      run() {},
    },
  ];
  const made = createCommandBar({
    commands: () => commands,
    keymap: () => ({}),
    mac: false,
    onRun: (id, arg) => ran.push({ id, arg }),
  });
  document.body.append(made.el);
  const input = made.el.querySelector("input") as HTMLInputElement;
  return {
    el: made.el,
    open: made.open,
    ran,
    input,
    prompt: () => made.el.querySelector(".console-shell-commandbar__prompt")?.textContent ?? "",
    rows: () =>
      [...made.el.querySelectorAll(".console-shell-commandbar__item")].map(
        (b) => b.textContent?.trim() ?? "",
      ),
    key: (k) =>
      void input.dispatchEvent(
        new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }),
      ),
    type: (v) => {
      input.value = v;
      input.dispatchEvent(new Event("input", { bubbles: true }));
    },
  };
}

test("a command with no targets runs on Enter, with no argument", () => {
  const b = bar();
  b.open();
  b.type("sidebar.toggle");
  b.key("Enter");
  assert.deepEqual(b.ran, [{ id: "console.sidebar.toggle", arg: undefined }]);
  assert.equal(b.el.hidden, true);
});

test("a command WITH targets asks instead of running, and lists them by label", () => {
  const b = bar();
  b.open();
  b.type("tab.goto");
  b.key("Enter");
  assert.deepEqual(b.ran, [], "picking the command must not dispatch it");
  assert.equal(b.el.hidden, false, "the bar stays open to ask");
  assert.equal(b.prompt(), "go to tab");
  assert.deepEqual(b.rows(), ["Dashboard", "Notes"]);
  assert.equal(b.input.value, "", "the query that found the command must not filter its targets");
});

test("Enter on a target dispatches the staged command with that target's value", () => {
  const b = bar();
  b.open();
  b.type("tab.goto");
  b.key("Enter");
  b.type("notes");
  b.key("Enter");
  assert.deepEqual(b.ran, [{ id: "console.tab.goto", arg: "t2" }]);
  assert.equal(b.el.hidden, true);
});

test("Escape backs out of the target list; a second Escape closes the bar", () => {
  const b = bar();
  b.open();
  b.type("tab.goto");
  b.key("Enter");
  b.key("Escape");
  assert.equal(b.el.hidden, false, "the first Escape answers 'not that command', not 'go away'");
  assert.equal(b.prompt(), "run");
  assert.ok(b.rows().includes("sidebar.toggle"), "back to listing commands");
  b.key("Escape");
  assert.equal(b.el.hidden, true);
  assert.deepEqual(b.ran, []);
});

// The input is the combobox: focus never leaves it, and aria-activedescendant names the option the
// arrow keys are on, so a screen reader follows the selection without the options taking focus.
test("the input is a combobox that controls the listbox and follows the selection", () => {
  const b = bar();
  b.open();
  assert.equal(b.input.getAttribute("role"), "combobox");
  assert.equal(b.input.getAttribute("aria-label"), "Search commands");
  const list = document.getElementById(b.input.getAttribute("aria-controls") ?? "");
  assert.equal(list?.getAttribute("role"), "listbox");
  assert.equal(b.input.getAttribute("aria-expanded"), "true");
  const first = b.input.getAttribute("aria-activedescendant");
  assert.ok(first && document.getElementById(first)?.getAttribute("role") === "option");
  b.key("ArrowRight");
  const second = b.input.getAttribute("aria-activedescendant");
  assert.notEqual(second, first, "the arrow moved the active option");
  assert.equal(document.getElementById(second ?? "")?.getAttribute("aria-selected"), "true");
});

test("no match says so in a live region, and collapses the combobox", () => {
  const b = bar();
  b.open();
  b.type("zzzzzz");
  const status = b.el.querySelector('[role="status"]');
  assert.equal(status?.textContent, "No matching commands");
  assert.equal(b.input.getAttribute("aria-expanded"), "false");
  assert.equal(b.input.getAttribute("aria-activedescendant"), null);
  assert.deepEqual(b.rows(), []);
  b.type("");
  assert.equal(status?.textContent, "", "the message clears when there are matches again");
});

test("reopening after a target was picked starts a fresh question, not mid-stage", () => {
  const b = bar();
  b.open();
  b.type("tab.goto");
  b.key("Enter");
  b.key("Enter"); // take the first target, closing the bar
  assert.deepEqual(b.ran, [{ id: "console.tab.goto", arg: "t1" }]);
  b.open();
  assert.equal(b.prompt(), "run");
  assert.ok(b.rows().includes("tab.goto"));
});
