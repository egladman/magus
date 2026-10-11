// gantt-dom.test.ts - the live execution timeline. The drawing is laid out in CSS pixels at the card's
// width, so its type is 12px at every width; what is pinned here is the part a reader depends on
// beyond the picture: the drawing has a summary for a screen reader, and a failed bar and a queued
// pip, which share the danger hue, are told apart in words.

import assert from "node:assert/strict";
import { afterEach, beforeEach, test } from "node:test";
import { initialState, type RunView, type StatusView } from "../state";
import { ganttTile } from "./gantt";

beforeEach(() => localStorage.clear());
afterEach(() => document.body.replaceChildren());

function target(label: string, state: RunView["targets"][number]["state"], now: number) {
  const done = state === "failed" || state === "passed";
  return {
    project: "p",
    target: label,
    label,
    state,
    terminal: done,
    startMs: state === "queued" ? null : now - 8000,
    endMs: done ? now - 2000 : null,
    outputRef: "",
    durationMs: done ? 6000 : 0,
  };
}

function frame(runs: RunView[]) {
  return { ...initialState(), status: { runs } as unknown as StatusView };
}

function setWidth(el: HTMLElement, width: number): void {
  Object.defineProperty(el, "clientWidth", { value: width, configurable: true });
}

// mount builds the tile with a scroller that has the width a real card would, since happy-dom does
// no layout and the drawing is sized from it.
function mount() {
  const tile = ganttTile();
  document.body.append(tile.el);
  const scroller = tile.el.querySelector<HTMLElement>(".console-dashboard-gantt__scroll");
  assert.ok(scroller);
  setWidth(scroller, 1200);
  return { tile, scroller };
}

test("the drawing is one named picture that summarises the runs", () => {
  const now = Date.now();
  const { tile } = mount();
  try {
    tile.update(
      frame([
        {
          inv: "inv1",
          trigger: "ci",
          targets: [target("a:build", "passed", now), target("a:test", "failed", now)],
        },
      ]),
    );
    const svg = tile.el.querySelector(".console-dashboard-gantt__svg");
    assert.equal(svg?.getAttribute("role"), "img");
    assert.match(svg?.getAttribute("aria-label") ?? "", /1 run, 2 targets, 0 running, 1 failed/);
    assert.equal(svg?.getAttribute("width"), "1200", "drawn at the width the card has");
  } finally {
    tile.destroy();
  }
});

test("failed and queued say what they are in text beside the bar", () => {
  const now = Date.now();
  const { tile } = mount();
  try {
    tile.update(
      frame([
        {
          inv: "inv1",
          trigger: "ci",
          targets: [target("a:test", "failed", now), target("a:lint", "queued", now)],
        },
      ]),
    );
    const words = [...tile.el.querySelectorAll(".console-dashboard-gantt__dur")].map(
      (t) => t.textContent ?? "",
    );
    assert.ok(
      words.some((w) => w.startsWith("failed")),
      words.join("|"),
    );
  } finally {
    tile.destroy();
  }
});

test("its scroller can be reached and is named", () => {
  const { tile, scroller } = mount();
  try {
    assert.equal(scroller.tabIndex, 0);
    assert.equal(scroller.getAttribute("role"), "region");
    assert.ok(scroller.getAttribute("aria-label"));
  } finally {
    tile.destroy();
  }
});

test("a drawing narrower than the minimum scrolls instead of shrinking", () => {
  const now = Date.now();
  const { tile, scroller } = mount();
  setWidth(scroller, 300);
  try {
    tile.update(frame([{ inv: "i", trigger: "ci", targets: [target("a:build", "passed", now)] }]));
    const width = Number(
      tile.el.querySelector("svg.console-dashboard-gantt__svg")?.getAttribute("width"),
    );
    assert.ok(
      width >= 560,
      "a 300px card still gets a drawing wide enough for 12px text: " + width,
    );
  } finally {
    tile.destroy();
  }
});

test("with nothing running the card says so in the shared empty state", () => {
  const { tile } = mount();
  try {
    tile.update(frame([]));
    assert.equal(tile.el.hasAttribute("data-empty"), true);
  } finally {
    tile.destroy();
  }
});
