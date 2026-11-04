// share-dom.test.ts - the lifetime the share panel offers first.

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { mountSharePanel } from "./share";

function pressedLabels(): string[] {
  return [
    ...document.querySelectorAll('.console-shell-share__durations [aria-pressed="true"]'),
  ].map((b) => (b.textContent ?? "").trim());
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("mountSharePanel", () => {
  test("the picker opens on the shortest lifetime, alone", () => {
    const panel = mountSharePanel();
    panel.open();
    assert.deepEqual(pressedLabels(), ["15 minutes"]);
  });

  test("a chosen lifetime replaces the default", () => {
    const panel = mountSharePanel();
    panel.open();
    const buttons = [
      ...document.querySelectorAll<HTMLButtonElement>(".console-shell-share__durations button"),
    ];
    const other = buttons.find((b) => (b.textContent ?? "").trim() !== "15 minutes");
    assert.ok(other, "the picker offers more than one lifetime");
    other.click();
    assert.deepEqual(pressedLabels(), [(other.textContent ?? "").trim()]);
  });
});
