// activityDrawer-dom.test.ts - the drawer's panel mechanics. document/window are registered globally
// by test-setup.mjs (node --import), so these run under node:test like the other *-dom tests.
//
// The row model is covered next door in activityDrawer.test.ts. What is pinned HERE is the one thing
// this panel does differently from its sibling share.ts, and the one thing a later refactor would
// most plausibly "fix" back: opening it must NOT move focus. It is a readout watched while working
// somewhere else, so a panel that grabs the caret every time it opens - or every time a poll repaints
// it - interrupts the exact activity the user opened it to watch. The aria wiring that replaces focus
// management (a polite summary, and lists that are NOT live regions) is pinned for the same reason:
// a list rebuilt every four seconds inside a live region re-announces every row on every tick.

import assert from "node:assert/strict";
import { test, beforeEach } from "node:test";
import { mountActivityDrawer } from "./activityDrawer";

// A fresh body per test: mountActivityDrawer appends a singleton and the suite runs with
// --experimental-test-isolation=none, so panels would otherwise accumulate across tests.
// Storage is cleared too, so resolveDaemonHost finds nothing configured and the refresh a
// newly-opened drawer kicks off resolves to the not-connected state without touching the network.
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
});

function panel(): HTMLElement {
  const el = document.getElementById("console-activitypanel");
  assert.ok(el, "the drawer mounts a panel with a stable id (aria-controls points at it)");
  return el as HTMLElement;
}

test("mounts hidden, and toggles open and shut", () => {
  const drawer = mountActivityDrawer();
  assert.equal(panel().hidden, true, "a drawer nobody asked for must not be on screen");
  drawer.toggle();
  assert.equal(panel().hidden, false);
  assert.equal(panel().getAttribute("aria-hidden"), "false");
  drawer.toggle();
  assert.equal(panel().hidden, true);
  assert.equal(panel().getAttribute("aria-hidden"), "true");
  drawer.close();
});

test("opening does not move focus", () => {
  const elsewhere = document.createElement("input");
  document.body.append(elsewhere);
  const drawer = mountActivityDrawer();
  elsewhere.focus();
  drawer.open();
  assert.equal(
    document.activeElement,
    elsewhere,
    "the caret stays where the user put it; this is a readout, not a task",
  );
  assert.equal(
    panel().contains(document.activeElement),
    false,
    "nothing inside the panel may take focus on open",
  );
  drawer.close();
  assert.equal(document.activeElement, elsewhere, "and closing does not yank it back either");
});

test("is a region, not a dialog - it promises no focus management", () => {
  const drawer = mountActivityDrawer();
  assert.equal(panel().getAttribute("role"), "region");
  assert.equal(panel().getAttribute("aria-label"), "Activity");
  drawer.close();
});

test("the summary is the polite live region; the lists are not", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  const summary = panel().querySelector(".console-shell-activity__summary");
  assert.ok(summary, "there is a summary line");
  assert.equal(summary?.getAttribute("aria-live"), "polite");
  const lists = [...panel().querySelectorAll(".console-shell-activity__list")];
  assert.equal(lists.length, 2, "two sections: running, and recent");
  for (const l of lists) {
    assert.equal(
      l.getAttribute("aria-live"),
      null,
      "a list rebuilt every poll must not re-announce every row",
    );
  }
  drawer.close();
});

test("Escape dismisses an open drawer and is inert while it is shut", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(panel().hidden, true);
  // Firing again on a closed panel is a no-op rather than a re-toggle.
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(panel().hidden, true);
  drawer.close();
});

test("with no daemon configured, both sections say so instead of reading as empty", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  const empties = [...panel().querySelectorAll(".console-shell-activity__empty")].map(
    (e) => e.textContent,
  );
  // "Not connected" and "nothing is running" are different facts, and the second one is a lie here.
  assert.deepEqual(empties, ["Not connected to a daemon.", "Not connected to a daemon."]);
  drawer.close();
});
