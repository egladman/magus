// main-dom.test.ts - the Shortcuts app's rows. Each row used to be a role=button div holding an edit
// button, so a keyboard user met a button inside a button and a screen reader read the whole row as
// one name. A row is now a list item with a real Run button and, beside it, a real edit button, and
// the keys describe Run instead of being a third control.

import assert from "node:assert/strict";
import { beforeEach, describe, test } from "node:test";
import type { Command } from "../../desktop/commands";
import { createShortcutsApp } from "./main";

const commands: Command[] = [
  { id: "console.tab.next", label: "Next tab", group: "Tabs", run() {} },
  { id: "console.open.logs", label: "Open Log Viewer", group: "Open", run() {} },
];

describe("the Shortcuts app", () => {
  let host: HTMLElement;
  const ran: string[] = [];
  const edited: (string | undefined)[] = [];

  beforeEach(async () => {
    ran.length = 0;
    edited.length = 0;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    await createShortcutsApp({
      commands: () => commands,
      keymap: () => ({ "console.tab.next": "mod+alt+ArrowRight" }),
      mac: false,
      run: (id) => ran.push(id),
      editableIds: new Set(["console.tab.next"]),
      onEditKeybindings: (id) => edited.push(id),
    }).activate(host);
  });

  const rows = (): HTMLElement[] => [...host.querySelectorAll<HTMLElement>("li")];

  test("a row is a list item, never a button holding buttons", () => {
    assert.equal(rows().length, 2);
    for (const row of rows()) {
      assert.equal(row.getAttribute("role"), null);
      assert.equal(row.getAttribute("tabindex"), null);
      assert.equal(row.querySelectorAll("button button").length, 0);
    }
  });

  test("Run is a real button named for the shortcut, and runs it", () => {
    const run = rows()[0].querySelector<HTMLButtonElement>("button.pf-m-secondary");
    assert.equal(run?.textContent, "Run");
    assert.equal(run?.getAttribute("aria-label"), "Run Next tab");
    run?.click();
    assert.deepEqual(ran, ["console.tab.next"]);
  });

  test("the keys describe the Run button", () => {
    const run = rows()[0].querySelector("button.pf-m-secondary");
    const keys = document.getElementById(run?.getAttribute("aria-describedby") ?? "");
    assert.ok(keys?.querySelector("kbd.console-shell-keycap"));
    assert.match(keys?.textContent ?? "", /Alt/);
    // A shortcut with no keys has nothing to describe it by.
    const bare = rows()[1].querySelector("button.pf-m-secondary");
    assert.equal(bare?.getAttribute("aria-describedby"), null);
  });

  test("edit is a sibling button, only on a rebindable shortcut", () => {
    const edit = rows()[0].querySelector<HTMLButtonElement>('button[data-role="edit"]');
    assert.equal(edit?.getAttribute("aria-label"), "Edit shortcut for Next tab");
    assert.equal(edit?.parentElement, rows()[0].querySelector(".console-shell-shortcuts__actions"));
    edit?.click();
    assert.deepEqual(edited, ["console.tab.next"]);
    assert.deepEqual(ran, [], "editing does not run it");
    assert.equal(rows()[1].querySelector('button[data-role="edit"]'), null);
  });

  test("groups are h2 headings that name their lists", () => {
    const lists = [...host.querySelectorAll("ul")];
    assert.equal(lists.length, 2);
    for (const list of lists) {
      const label = document.getElementById(list.getAttribute("aria-labelledby") ?? "");
      assert.equal(label?.tagName, "H2");
    }
  });

  // One word for the thing: the page says shortcut, not action, command or keybinding.
  test("the banner and its control say shortcut", () => {
    const banner = host.querySelector(".console-shell-shortcuts__banner");
    assert.match(banner?.textContent ?? "", /shortcut/);
    assert.doesNotMatch(banner?.textContent ?? "", /action|keybinding/i);
    const all = banner?.querySelector("button");
    assert.equal(all?.textContent, "Edit shortcuts");
    all?.click();
    assert.deepEqual(edited, [undefined]);
  });
});
