// keybindings-dom.test.ts - the recorder's TEARDOWN, which is the part with teeth.
//
// beginCapture installs a capture-phase keydown listener on `document` that preventDefault()s
// every key, and arms a timer that commits the recording after a pause. Both outlive the modal
// they were started from, so dismissing mid-Record used to leave the console eating every
// keystroke app-wide until a reload - and then rebinding the command anyway, 900ms later, from
// a dialog the user had already closed. That is a whole-keyboard outage from one stray Escape,
// which is why it is pinned here rather than left to the browser harness.
//
// document/window are registered globally by test-setup.mjs (node --import), same as the other
// *-dom tests.

import assert from "node:assert/strict";
import { test } from "node:test";
import type { Command, Keymap } from "./commands";
import { createKeybindingsOverlay } from "./keybindings";
import type { Persisted } from "../lib/persist";

const commands: Command[] = [{ id: "tab.new", label: "New tab", group: "Tabs", run() {} }];
const defaults: Keymap = { "tab.new": "mod+t" };

// A minimal in-memory Persisted cell: the editor only needs get/update/subscribe, and a real
// one would drag localStorage into a test about listener lifetime.
function cell(): Persisted<Keymap> {
  let value: Keymap = {};
  const listeners = new Set<(v: Keymap) => void>();
  return {
    get: () => value,
    set(v) {
      value = v;
      for (const fn of listeners) fn(value);
    },
    update(fn) {
      this.set(fn(value));
    },
    persistOnly() {},
    subscribe(fn) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}

// press dispatches a real keydown through `document` and reports whether something
// swallowed it. The capture listener is the only thing that would.
function press(key: string): boolean {
  const ev = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  document.dispatchEvent(ev);
  return ev.defaultPrevented;
}

function startRecording(el: HTMLElement): void {
  const record = el.querySelector<HTMLButtonElement>('[data-command="tab.new"] button');
  assert.ok(record, "the tab.new row must expose a Record button");
  record.click();
}

test("closing the overlay mid-Record releases the keyboard", () => {
  const keymap = cell();
  const overlay = createKeybindingsOverlay({ commands, defaults, keymap });
  document.body.append(overlay.el);
  overlay.open();

  startRecording(overlay.el);
  assert.equal(press("a"), true, "while recording, the capture listener owns every key");

  overlay.close();
  assert.equal(
    press("a"),
    false,
    "after close the capture listener must be gone, or the console eats every keystroke",
  );

  overlay.el.remove();
});

test("closing the overlay mid-Record abandons the pending rebind", async () => {
  const keymap = cell();
  const overlay = createKeybindingsOverlay({ commands, defaults, keymap });
  document.body.append(overlay.el);
  overlay.open();

  startRecording(overlay.el);
  press("x"); // one chord captured; the commit timer is now armed
  overlay.close();

  // Past CAPTURE_COMMIT_MS (900ms): a surviving timer would have written the override by now.
  await new Promise((r) => setTimeout(r, 1000));
  assert.deepEqual(keymap.get(), {}, "a dismissed recording must not rebind the command later");

  overlay.el.remove();
});
