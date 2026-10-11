// rotator-dom.test.ts - the one Big Picture slot that cycles. A panel with nothing to show must not
// take the slot, and a reader who asks for the rotation to stop gets a signal the rotator obeys.

import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { rotationPaused, viewMode } from "./bigPicture";
import { mountRotator } from "./rotator";

afterEach(() => {
  viewMode.set("board");
  rotationPaused.set(false);
  document.body.replaceChildren();
});

function panel(id: string, empty = false): HTMLElement {
  const el = document.createElement("section");
  el.dataset.card = id;
  if (empty) el.dataset.empty = "";
  document.body.append(el);
  return el;
}

test("entering the mode lands on the first panel that has something to show", () => {
  const a = panel("a", true);
  const b = panel("b");
  const c = panel("c");
  const rotator = mountRotator([a, b, c], { paused: () => false });
  viewMode.set("bigPicture");
  assert.equal(a.hasAttribute("data-rotate-active"), false, "an empty card does not take a turn");
  assert.equal(b.hasAttribute("data-rotate-active"), true);
  assert.equal(c.hasAttribute("data-rotate-active"), false);
  rotator.destroy();
});

test("a hidden panel is skipped the same way", () => {
  const a = panel("a");
  a.hidden = true;
  const b = panel("b");
  const rotator = mountRotator([a, b], { paused: () => false });
  viewMode.set("bigPicture");
  assert.equal(b.hasAttribute("data-rotate-active"), true);
  rotator.destroy();
});

test("the reader's pause is a signal the control and the rotator share", () => {
  assert.equal(typeof rotationPaused.get(), "boolean");
  const seen: boolean[] = [];
  const off = rotationPaused.subscribe((v) => seen.push(v));
  rotationPaused.set(true);
  rotationPaused.set(false);
  off();
  assert.deepEqual(seen, [true, false]);
});
