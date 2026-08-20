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

test("the Clear control sits with the live Explore chips", () => {
  const host = parse();
  const clear = host.querySelector("#clear-view-btn");
  assert.ok(clear, "expected a clear-view button");
  assert.equal(clear.closest("[data-ref-section]"), null, "Clear must not live in reference copy");
  assert.ok(
    clear.closest(".console-graph-sidebar__views"),
    "Clear must sit in the sidebar Explore group, beside the chips it clears",
  );
});

test("the remember-workspace checkbox sits in the live sidebar", () => {
  const host = parse();
  const cb = host.querySelector("#live-remember-cb");
  assert.ok(cb, "expected a remember checkbox");
  assert.equal(cb.closest("[data-ref-section]"), null, "a live control must not be reference copy");
});

test("the affected chip ships hidden until a live affected set exists", () => {
  const host = parse();
  const chip = host.querySelector<HTMLElement>("[data-view='affected']");
  assert.ok(chip, "expected an affected chip");
  // No status producer populates StatusOutput.Affected (see main.ts fetchLiveStatus), so the
  // chip hides on the same [data-conditional] footing as the critical chip. Shipping it
  // visible-and-disabled instead would be a control the user can see and never reach.
  assert.ok(chip.hasAttribute("hidden"), "affected chip must start hidden");
  assert.ok(chip.hasAttribute("data-conditional"), "affected chip must be conditional");
  assert.ok(!chip.hasAttribute("disabled"), "hide the chip rather than shipping it disabled");
});
