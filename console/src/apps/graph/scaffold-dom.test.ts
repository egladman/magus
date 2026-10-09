// scaffold-dom.test.ts - structural invariants of the graph app's markup.
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

const scaffold = readFileSync("src/apps/graph/scaffold.html", "utf8");

function parse(): HTMLElement {
  const host = document.createElement("div");
  host.innerHTML = scaffold;
  return host;
}

test("no reference block wires a control by id", () => {
  const host = parse();
  const blocks = [...host.querySelectorAll<HTMLElement>("[data-ref-section]")];
  assert.ok(blocks.length > 0, "expected the app to carry reference blocks");
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
//
// This used to require the trigger to sit inside the query bar, "next to the input it writes into".
// It sits in the stage header now. Adjacency to the field is what made it a fourth button hanging
// off an 18rem input, which is the shape that made the app's one teaching control read as that
// field's overflow menu. What has to hold is that the trigger EXISTS and is LIVE - the placement is
// a design call, and pinning it here only made the test a second opinion on that call.
test("the query builder has a live trigger", () => {
  const host = parse();
  const btn = host.querySelector("#query-builder-btn");
  assert.ok(btn, "without this button the views and the filter grammar are unreachable");
  // A reference block is cloned id-stripped and hidden at its source, so a control wired by id from
  // inside one is dead both ways. That is the assertion worth keeping.
  assert.equal(btn.closest("[data-ref-section]"), null, "the trigger must not be reference copy");
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

// The builder is the only control that teaches the query language, and it sits third in a row of
// four identically-styled control buttons. Drawn as vertical dots it read as this text field's
// overflow menu, which is what that glyph means everywhere else, and the aria-label saying
// otherwise reaches nobody who can see. A visible word is what makes it findable.
test("the query builder is named, and is not drawn as an overflow menu", () => {
  const host = parse();
  const btn = host.querySelector<HTMLElement>("#query-builder-btn");
  assert.ok(btn, "the builder is the one way to ask without knowing the syntax");
  assert.equal(btn.querySelector(".pf-v6-c-button__text")?.textContent, "Ask");
  // Three same-sized circles in a column is the kebab, whatever the markup calls it.
  const dots = [...btn.querySelectorAll("svg circle")];
  const xs = new Set(dots.map((c) => c.getAttribute("cx")));
  assert.ok(
    !(dots.length >= 3 && xs.size === 1),
    "a vertical column of three dots is an overflow menu to every reader who has used software",
  );
});

// Figures opens a different view; it is not a third graph. As a segment of the single-select Graph
// group it was clipped off the sidebar's right edge and announced as one of the choices.
test("Figures is a button of its own, not a segment of the Graph group", () => {
  const host = parse();
  const group = host.querySelector('[aria-labelledby="graphkind-label"]');
  assert.ok(group, "the Graph group");
  assert.deepEqual(
    [...group.querySelectorAll("button")].map((b) => b.getAttribute("data-graphkind")),
    ["targets", "knowledge"],
  );
  assert.equal(group.querySelector("[data-graph-mode]"), null);
  const open = host.querySelector<HTMLElement>("#graph-figures-open");
  assert.ok(open, "a Figures button");
  assert.equal(open.getAttribute("data-graph-mode"), "figures");
  assert.equal(open.closest(".pf-v6-c-toggle-group"), null);
  assert.equal(open.hasAttribute("aria-pressed"), false, "it opens a view; it is not a toggle");
});

test("the Graph and Color groups each have a row to themselves", () => {
  const host = parse();
  assert.equal(host.querySelector(".console-graph-sidebar__viewpair"), null);
  const kind = host.querySelector('[aria-labelledby="graphkind-label"]');
  const color = host.querySelector('[aria-labelledby="color-label"]');
  assert.ok(kind && color);
  assert.notEqual(
    kind.closest(".console-graph-sidebar__viewcell"),
    color.closest(".console-graph-sidebar__viewcell"),
  );
});

test("the query field is named by its visible label", () => {
  const host = parse();
  const input = host.querySelector<HTMLInputElement>("#node-search");
  assert.ok(input);
  const label = host.querySelector<HTMLLabelElement>('label[for="node-search"]');
  assert.ok(label, "a real label");
  assert.equal(label.textContent, "magus query");
  assert.equal(input.hasAttribute("aria-label"), false, "an aria-label would override the label");
});

test("the notice host and the detail card carry no live region of their own", () => {
  const host = parse();
  assert.equal(host.querySelector("#explain-card")?.hasAttribute("aria-live"), false);
  const status = host.querySelector("#graph-status");
  assert.ok(status);
  assert.equal(status.hasAttribute("role"), false, "ui/alert.ts's alert supplies the live role");
  assert.equal(status.children.length, 0, "the alert is built by setStatus");
  assert.ok(
    host.querySelector("#explain-status[role='status']"),
    "one line says what the card shows",
  );
});

test("the remember row uses a PF check and the reference table a PF table", () => {
  const host = parse();
  const cb = host.querySelector("#live-remember-cb");
  assert.ok(cb?.classList.contains("pf-v6-c-check__input"));
  assert.equal(
    host.querySelector('label[for="live-remember-cb"]')?.className,
    "pf-v6-c-check__label",
  );
  assert.ok(host.querySelector("table.pf-v6-c-table"), "a PF table");
  assert.equal(host.querySelector(".console-graph-help__table"), null);
});

test("the way back from Figures is a plain button, not a one-item navigation landmark", () => {
  const host = parse();
  const bar = host.querySelector("#graph-figures-controls");
  assert.ok(bar);
  assert.notEqual(bar.tagName, "NAV");
  assert.ok(bar.querySelector("#graph-figures-back"));
});

test("the empty state names no menu that does not feed this app", () => {
  const host = parse();
  const empty = host.querySelector("#graph-empty-state");
  assert.ok(empty);
  assert.doesNotMatch(empty.textContent ?? "", /acme|Workspace menu/);
  assert.match(empty.textContent ?? "", /#demo/);
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
