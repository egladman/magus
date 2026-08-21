// status-dom.test.ts - who owns the connection dot.
//
// The bar is per-tab and two things write to it: the surface in front, and the shell's readiness
// poller. The rule they share is the data-owner stamp, and it is invisible - nothing about reading
// publishStatus tells you a bar it never touched stays the poller's to fill. A regression here is
// silent in the worst way: the dot keeps rendering, it just answers about the wrong thing.
//
// document/window are registered globally by test-setup.mjs (node --import), same as the other
// *-dom tests.

import assert from "node:assert/strict";
import { describe, test, beforeEach } from "node:test";
import { publishStatus } from "./status";

const conn = () => document.getElementById("console-conn") as HTMLElement;
const count = () => document.getElementById("console-count") as HTMLElement;

// Scoped in a suite so the hook does not register on the ROOT context: with
// --test-isolation=none every -dom file shares one process, and a global hook here would wipe
// the fixtures the other files just built (see signin-dom.test.ts, which learned this first).
describe("the console status bar", () => {
  beforeEach(() => {
    location.hash = "";
    document.body.innerHTML =
      '<span id="console-conn"></span><span id="console-count"></span>' +
      '<span id="console-observing"></span>';
  });

  test("a surface reporting its own link claims the dot", () => {
    publishStatus({ connection: "connected", label: "connected" });
    assert.equal(conn().textContent, "connected");
    assert.equal(conn().dataset.state, "connected");
    assert.equal(conn().dataset.owner, "surface");
  });

  // The whole point of the split: a surface with nothing to say about the daemon must leave the dot
  // alone rather than assert "not connected" about a link it never probed.
  test("a surface with no link of its own leaves the dot unclaimed", () => {
    publishStatus({ count: "2373 nodes" });
    assert.equal(conn().dataset.owner, undefined);
    assert.equal(conn().textContent, "");
    assert.equal(count().textContent, "2373 nodes");
    assert.equal(count().hidden, false);
  });

  // count and observing describe the surface's DATA, so they are written either way - the connection
  // half being absent must not take them down with it.
  test("data slots are written whether or not the dot is claimed", () => {
    publishStatus({ count: "14 events", observing: { text: "run 3", title: "watching run 3" } });
    assert.equal(count().textContent, "14 events");
    assert.equal(document.getElementById("console-observing")?.textContent, "run 3");

    publishStatus({ connection: "connected", label: "connected" });
    assert.equal(count().textContent, "");
    assert.equal(count().hidden, true);
  });

  // Demo is decided by the fragment, not by the surface, and that override predates the ownership
  // stamp. A claimed dot in demo mode still reads "demo".
  test("demo mode overrides a claimed label", () => {
    location.hash = "#demo";
    publishStatus({ connection: "connected", label: "connected" });
    assert.equal(conn().textContent, "demo");
    assert.equal(conn().dataset.state, "demo");
  });
});
