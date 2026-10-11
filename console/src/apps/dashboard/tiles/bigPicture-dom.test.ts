// bigPicture-dom.test.ts - the dashboard's view controls. The Big Picture button is one ordinary
// secondary button whose name never changes: aria-pressed says it is on, so the label does not also
// flip. The pause control for the rotating slot only exists inside the mode.

import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { dashboardHeader, resetBigPicture, rotationPaused, viewMode } from "./bigPicture";

afterEach(() => {
  resetBigPicture();
  rotationPaused.set(false);
  document.body.replaceChildren();
});

function mount() {
  const header = dashboardHeader();
  document.body.append(header.el);
  const big = header.el.querySelector<HTMLButtonElement>(".console-dashboard-viewbar__bigpicture");
  const pause = header.el.querySelector<HTMLButtonElement>(".console-dashboard-viewbar__rotation");
  assert.ok(big && pause);
  return { header, big, pause };
}

test("Big Picture is a plain secondary button with a stable name and a pressed state", () => {
  const { header, big } = mount();
  assert.ok(big.classList.contains("pf-m-secondary"));
  assert.equal(big.closest(".pf-v6-c-toggle-group"), null, "not a one-item toggle group");
  assert.equal(big.textContent, "Big Picture");
  assert.equal(big.getAttribute("aria-pressed"), "false");

  big.click();
  assert.equal(viewMode.get(), "bigPicture");
  assert.equal(big.getAttribute("aria-pressed"), "true");
  assert.equal(big.textContent, "Big Picture", "the label does not flip with the state");

  big.click();
  assert.equal(viewMode.get(), "board");
  assert.equal(big.getAttribute("aria-pressed"), "false");
  header.destroy();
});

test("the rotation can be paused from inside the mode, and only there", () => {
  const { header, big, pause } = mount();
  assert.equal(pause.hidden, true, "no rotation on the board to pause");
  big.click();
  assert.equal(pause.hidden, false);
  assert.equal(pause.textContent, "Pause rotation");
  assert.equal(pause.getAttribute("aria-pressed"), "false");

  pause.click();
  assert.equal(rotationPaused.get(), true);
  assert.equal(pause.getAttribute("aria-pressed"), "true");
  assert.equal(pause.textContent, "Pause rotation", "stable name here too");
  header.destroy();
});

test("the controls are a named group, not a bare div with a label", () => {
  const { header } = mount();
  assert.equal(header.el.getAttribute("role"), "group");
  assert.ok(header.el.getAttribute("aria-label"));
  header.destroy();
});
