// zoomControl-dom.test.ts - the stepper's ownership rules. document/window come from happy-dom via
// test-setup.mjs (node --import), which is what the -dom suffix selects.
//
// Every bug this control can have is SILENT: a second stepper appears beside the first, or a
// torn-down surface removes the live one. Nothing throws, so only a test catches it.

import { test, beforeEach } from "node:test";
import assert from "node:assert/strict";
import { mountZoomControl } from "./zoomControl";

function steppers(): number {
  return document.querySelectorAll("#console-statusbar .console-zoom").length;
}

function opts(): Parameters<typeof mountZoomControl>[0] {
  let z = 1;
  return {
    get: () => z,
    zoomIn: () => {
      z *= 2;
    },
    zoomOut: () => {
      z /= 2;
    },
    reset: () => {
      z = 1;
    },
  };
}

beforeEach(() => {
  document.body.innerHTML =
    '<footer id="console-statusbar"><div class="console-shell-statusbar__right"></div></footer>';
});

test("mounting twice leaves ONE stepper, not a stack", () => {
  // The log viewer is a cached singleton whose activate() runs again on every reopen, so this is
  // the real sequence rather than a hypothetical one.
  mountZoomControl(opts());
  mountZoomControl(opts());
  mountZoomControl(opts());
  assert.equal(steppers(), 1);
});

test("a preempted surface cannot remove its successor\'s stepper", () => {
  const first = mountZoomControl(opts());
  const second = mountZoomControl(opts());
  assert.equal(steppers(), 1, "the second mount preempted the first");
  // The first surface tears down LATER, the way a backgrounded pane does.
  first?.remove();
  assert.equal(steppers(), 1, "the live stepper survives a late teardown from the old holder");
  second?.remove();
  assert.equal(steppers(), 0, "the holder\'s own remove still clears the slot");
});

test("remove is idempotent, so a double teardown is harmless", () => {
  const ctl = mountZoomControl(opts());
  ctl?.remove();
  ctl?.remove();
  assert.equal(steppers(), 0);
});

test("the slot can be re-acquired after it is released", () => {
  mountZoomControl(opts())?.remove();
  assert.equal(steppers(), 0);
  mountZoomControl(opts());
  assert.equal(steppers(), 1, "releasing must clear the holder, or the next mount is refused");
});

test("with no status bar there is nothing to dock into, and that is not a crash", () => {
  document.body.innerHTML = "<div></div>";
  assert.equal(mountZoomControl(opts()), null);
});

test("the readout reports the surface\'s own factor, and reset returns it to 100%", () => {
  const o = opts();
  const ctl = mountZoomControl(o);
  const readout = document.querySelector<HTMLElement>('.console-zoom [data-zoom="reset"]');
  assert.equal(readout?.textContent, "100%");
  document.querySelector<HTMLElement>('.console-zoom [data-zoom="in"]')?.click();
  assert.equal(
    readout?.textContent,
    "200%",
    "the click drove the surface AND repainted the readout",
  );
  document.querySelector<HTMLElement>('.console-zoom [data-zoom="reset"]')?.click();
  assert.equal(readout?.textContent, "100%");
  // A change made by any other route - a command, ctrl+wheel - is reflected through sync().
  o.zoomIn();
  ctl?.sync();
  assert.equal(readout?.textContent, "200%");
});
