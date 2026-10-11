// cheatsheet-dom.test.ts - the keyboard-shortcuts sheet as a dialog. It was a backdrop with a role and
// a label on it, a span for a title, and a text glyph for a close; it took no focus, let Tab walk out
// into the page behind it, and left focus nowhere when it closed. happy-dom is registered globally by
// test-setup.mjs.

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { createCheatsheet } from "./cheatsheet";
import type { Command } from "./commands";

const commands: Command[] = [
  { id: "console.tab.next", label: "Next tab", group: "Tabs", run() {} },
  { id: "console.sidebar.toggle", label: "Toggle navigation rail", group: "General", run() {} },
];

function sheet() {
  const made = createCheatsheet({
    commands: () => commands,
    keymap: () => ({ "console.tab.next": "mod+alt+ArrowRight", "console.sidebar.toggle": "mod+b" }),
    mac: false,
  });
  document.body.append(made.el);
  return made;
}

describe("the keyboard shortcuts sheet", () => {
  afterEach(() => document.body.replaceChildren());

  test("the box is the dialog, labelled by an h1 title", () => {
    const made = sheet();
    const box = made.el.querySelector<HTMLElement>(".pf-v6-c-modal-box");
    assert.equal(box?.getAttribute("role"), "dialog");
    assert.equal(box?.getAttribute("aria-modal"), "true");
    const title = made.el.querySelector("h1.pf-v6-c-modal-box__title");
    assert.equal(box?.getAttribute("aria-labelledby"), title?.id);
    assert.equal(title?.textContent, "Keyboard shortcuts");
    assert.equal(made.el.getAttribute("role"), null, "the backdrop is not the dialog");
  });

  test("the close control is PF's close markup with a drawn icon", () => {
    const made = sheet();
    const close = made.el.querySelector<HTMLButtonElement>(".pf-v6-c-modal-box__close button");
    assert.ok(close);
    assert.equal(close.getAttribute("aria-label"), "Close");
    assert.ok(close.querySelector("svg"));
    assert.equal(close.textContent, "", "no multiplication sign in text");
  });

  test("opening moves focus in, and closing hands it back", () => {
    const trigger = document.createElement("button");
    document.body.append(trigger);
    const made = sheet();
    trigger.focus();
    made.show();
    const box = made.el.querySelector<HTMLElement>(".pf-v6-c-modal-box");
    // ok(), not equal(): a failing equal() inspects both DOM nodes, which looks like a hang.
    assert.ok(document.activeElement === box, "focus moves into the dialog");
    made.hide();
    assert.equal(made.el.hidden, true);
    assert.ok(document.activeElement === trigger, "and returns to what had it");
  });

  test("Tab cannot leave the dialog", () => {
    const made = sheet();
    made.show();
    const close = made.el.querySelector<HTMLButtonElement>(".pf-v6-c-modal-box__close button");
    close?.focus();
    const tab = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    close?.dispatchEvent(tab);
    assert.equal(tab.defaultPrevented, true, "the only stop wraps to itself");
    assert.ok(document.activeElement === close);
  });

  test("Escape closes it", () => {
    const made = sheet();
    made.show();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    assert.equal(made.el.hidden, true);
  });

  // The hint used to say "Press Esc or click outside to dismiss" over a sheet that closes the moment
  // the "?" it was held open with is released.
  test("the hint tells the hold from the button", () => {
    const made = sheet();
    const hint = made.el.querySelector(".console-shell-cheatsheet__hint")?.textContent ?? "";
    assert.match(hint, /let go/);
    assert.match(hint, /status bar/);
    assert.match(hint, /Shortcuts app/);
  });

  test("groups are h2 headings over their shortcuts", () => {
    const made = sheet();
    made.show();
    const heads = [...made.el.querySelectorAll("h2.console-shell-shortcuts__group-title")].map(
      (h) => h.textContent,
    );
    assert.deepEqual(heads, ["Tabs", "General"]);
    assert.ok(made.el.querySelector("kbd.console-shell-keycap"));
  });
});
