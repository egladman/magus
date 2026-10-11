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
// Storage is cleared too, so resolveServerHost finds nothing configured and the refresh a
// newly-opened drawer kicks off resolves to the not-connected state without touching the network.
//
// Emptying the body is NOT what a shell tearing a drawer down does, though - the listeners live on
// document, not in the panel, and clearing the body leaves them - so every test ends with destroy()
// rather than close(). The teardown section at the bottom is where that is the subject.
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
  drawer.toggle();
  assert.equal(panel().hidden, true);
  // `hidden` is the whole of it: a second attribute saying the same thing is one more place to go stale.
  assert.equal(panel().getAttribute("aria-hidden"), null);
  drawer.destroy();
});

test("reports every open and close, so the toggles can say which they are", () => {
  const seen: boolean[] = [];
  const drawer = mountActivityDrawer({ onChange: (open) => seen.push(open) });
  drawer.open();
  assert.equal(drawer.isOpen(), true);
  drawer.close();
  drawer.toggle();
  assert.deepEqual(seen, [true, false, true]);
  drawer.destroy();
});

// A readout watched while working somewhere else: a click into the app behind it is the work, not a
// request to dismiss. Only Escape, the close button and the drawer's own toggle close it.
test("a click outside does not close it, but Escape and the close button do", () => {
  const elsewhere = document.createElement("button");
  document.body.append(elsewhere);
  const drawer = mountActivityDrawer();
  drawer.open();
  elsewhere.dispatchEvent(new Event("pointerdown", { bubbles: true }));
  elsewhere.click();
  assert.equal(panel().hidden, false, "working in the page behind it must not dismiss the panel");
  panel().querySelector<HTMLButtonElement>(".console-shell-panel__close")?.click();
  assert.equal(panel().hidden, true);
  drawer.open();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(panel().hidden, true);
  drawer.destroy();
});

test("its title is an h2 the panel is labelled by, and its close is an svg", () => {
  const drawer = mountActivityDrawer();
  const title = panel().querySelector("h2");
  assert.equal(title?.textContent, "Activity");
  assert.equal(panel().getAttribute("aria-labelledby"), title?.id);
  const close = panel().querySelector(".console-shell-panel__close");
  assert.ok(close?.querySelector("svg"), "a drawn glyph, not a multiplication sign in text");
  assert.equal(close?.textContent, "");
  drawer.destroy();
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
  drawer.destroy();
  assert.equal(document.activeElement, elsewhere, "and tearing it down does not yank it back");
});

test("is a region, not a dialog - it promises no focus management", () => {
  const drawer = mountActivityDrawer();
  assert.equal(panel().getAttribute("role"), "region");
  drawer.destroy();
});

test("the summary is the polite status region; the lists are not", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  const summary = panel().querySelector(".console-shell-activity__summary");
  assert.ok(summary, "there is a summary line");
  assert.equal(summary?.getAttribute("role"), "status");
  const lists = [...panel().querySelectorAll(".console-shell-activity__list")];
  assert.equal(lists.length, 2, "two sections: running, and recent");
  for (const l of lists) {
    assert.equal(
      l.getAttribute("aria-live"),
      null,
      "a list rebuilt every poll must not re-announce every row",
    );
  }
  drawer.destroy();
});

test("Escape dismisses an open drawer and is inert while it is shut", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(panel().hidden, true);
  // Firing again on a closed panel is a no-op rather than a re-toggle.
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(panel().hidden, true);
  drawer.destroy();
});

test("with no server configured, both sections say so instead of reading as empty", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  const empties = [...panel().querySelectorAll(".console-shell-activity__empty")].map(
    (e) => e.textContent,
  );
  // "Not connected" and "nothing is running" are different facts, and the second one is a lie here.
  assert.deepEqual(empties, ["Not connected to a server.", "Not connected to a server."]);
  drawer.destroy();
});

// Demo has no server, and "not connected" in the console's one demo mode is wrong twice: the status
// bar says demo, and the drawer was the only place that said otherwise.
test("in the demo it lists the demo's own runs and says they are demo data", () => {
  location.hash = "#demo";
  const drawer = mountActivityDrawer();
  drawer.open();
  const summary = panel().querySelector(".console-shell-activity__summary")?.textContent ?? "";
  assert.match(summary, /demo data/);
  const links = [
    ...panel().querySelectorAll<HTMLAnchorElement>("a.console-shell-activity__row-link"),
  ];
  assert.ok(links.length > 0, "the demo scenario has finished runs to list");
  assert.match(links[0].getAttribute("href") ?? "", /^logs\/#demo&ref=/);
  assert.equal(panel().textContent?.includes("Not connected"), false);
  drawer.destroy();
  location.hash = "";
});

test("a finished run links to the log viewer and carries one dated <time>", () => {
  location.hash = "#demo";
  const drawer = mountActivityDrawer();
  drawer.open();
  const row = panel().querySelector(".console-shell-activity__list > li");
  assert.ok(row?.querySelector("a[href]"), "the title opens the run");
  const time = row?.querySelector("time");
  assert.ok(time?.getAttribute("datetime"), "a machine-readable instant");
  assert.ok(time?.title, "the full timestamp rides in the title");
  assert.ok(
    row?.querySelector("[data-status]"),
    "the outcome has a shape and a word, not a colour",
  );
  const count = panel().querySelector(".pf-v6-c-badge.pf-m-read");
  assert.ok(count, "counts are PF read badges");
  drawer.destroy();
  location.hash = "";
});

// ---- teardown --------------------------------------------------------------

test("destroy removes the panel, and a destroyed drawer stays destroyed", () => {
  const drawer = mountActivityDrawer();
  drawer.open();
  // Destroyed while OPEN, which is what a shell going away mid-session does. The poll interval goes
  // with it: an interval nobody clears keeps this process alive after the suite is done.
  drawer.destroy();
  assert.equal(document.getElementById("console-activitypanel"), null, "the panel is detached");
  drawer.open();
  drawer.toggle();
  assert.equal(
    document.getElementById("console-activitypanel"),
    null,
    "a stale reference cannot put a detached panel back on screen",
  );
});

// The dismissal listeners are on document rather than inside the panel, so a drawer dropped without
// destroy() leaves a pair behind per mount. What that would cost is what this pins: the events a
// live drawer answers are answered ONCE, by the live one.
test("a drawer mounted after a destroy is the only one answering", () => {
  mountActivityDrawer().destroy();
  const live = mountActivityDrawer();
  live.open();
  assert.equal(document.querySelectorAll("#console-activitypanel").length, 1);
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
  assert.equal(panel().hidden, true);
  live.open();
  assert.equal(panel().hidden, false, "and nothing else is toggling it back");
  live.destroy();
});
