// scaffold-dom.test.ts - structural invariants of the graph surface's markup.
//
// The Reference drawer (ui/ref-drawer.ts) CLONES every [data-ref-section] block, strips ids
// from the clone, and leaves the source hidden by overrides.css's
// [data-ref-section]{display:none}. A control wired BY ID from inside a reference block is
// therefore unreachable both ways: invisible at its source, id-less in its clone. These tests
// pin the placement rules that follow from that.
//
// The path is relative to the pnpm cwd (console/), not to this file: esbuild bundles every
// test into .testcache/ before node runs it, so __dirname would point at the bundle.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const scaffold = readFileSync("src/console/graph/scaffold.html", "utf8");

function parse(): HTMLElement {
  const host = document.createElement("div");
  host.innerHTML = scaffold;
  return host;
}

test("no reference block wires a control by id", () => {
  const host = parse();
  const blocks = [...host.querySelectorAll<HTMLElement>("[data-ref-section]")];
  assert.ok(blocks.length > 0, "expected the surface to carry reference blocks");
  for (const block of blocks) {
    const ids = [...block.querySelectorAll("[id]")].map((el) => el.id);
    assert.deepEqual(
      ids,
      [],
      "reference-block clones are id-stripped, so an id here can only be a dead control: " +
        block.querySelector("summary")?.textContent,
    );
  }
});

test("Clear is a live control, not reference copy", () => {
  const host = parse();
  const clear = host.querySelector("#clear-view-btn");
  assert.ok(clear, "expected a clear-view button");
  assert.equal(clear.closest("[data-ref-section]"), null, "Clear must not live in reference copy");
});

// The question chips moved into querybuilder.ts, which builds its cards in JS. What the scaffold
// still owes is the way IN: without this button the views and the filter grammar are unreachable,
// and nothing else in the markup would show it missing.
test("the query builder has a live trigger", () => {
  const host = parse();
  const btn = host.querySelector("#query-builder-btn");
  assert.ok(btn, "expected the builder trigger beside the filter input");
  assert.equal(btn.closest("[data-ref-section]"), null, "the trigger must not be reference copy");
  assert.ok(
    btn.closest(".console-graph-sidebar__search"),
    "the trigger belongs in the query bar, next to the input it writes into",
  );
});

test("the remember-workspace checkbox sits in the live sidebar", () => {
  const host = parse();
  const cb = host.querySelector("#live-remember-cb");
  assert.ok(cb, "expected a remember checkbox");
  assert.equal(cb.closest("[data-ref-section]"), null, "a live control must not be reference copy");
});

// The view chips are gone, so the scaffold no longer decides which questions are askable - the
// builder does, from live state. What must not come back is a view hard-coded in the markup, where
// it would be offered whether or not the loaded graph can answer it.
test("no LIVE view control is hard-coded in the scaffold", () => {
  const host = parse();
  const live = [...host.querySelectorAll("[data-view]")].filter(
    (el) => !el.closest("[data-ref-section]"),
  );
  assert.deepEqual(
    live.map((el) => el.getAttribute("data-view")),
    [],
    "views are built by querybuilder.ts from viewUnavailable(); a static one cannot know if it applies",
  );
});

test("data-conditional is the only mechanism for data-backed visibility", () => {
  // The marker used to mean two things: a CSS rule scoped to .console-graph-views__chip, plus a
  // separately-set `hidden` for anything else (a PF button outspecifies a bare attribute
  // selector). One unscoped !important rule now covers every element, so nothing should still
  // be carrying both.
  const host = parse();
  for (const el of host.querySelectorAll<HTMLElement>("[data-conditional]")) {
    assert.ok(
      !el.hasAttribute("hidden"),
      "[data-conditional] already hides this; the extra hidden is the old second mechanism: " +
        el.outerHTML.slice(0, 80),
    );
  }
});
