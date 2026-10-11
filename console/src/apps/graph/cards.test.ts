// cards.test.ts - pure-math coverage for cards.ts: measureCards' width
// clamping and ellipsize's fit/truncate behavior. Canvas 2D is not available
// under plain node, so ctx is faked with a minimal structural object whose
// measureText mirrors a monospace-ish metric (width = 6px per character) -
// enough to exercise the clamp and ellipsize logic deterministically without
// a real canvas.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CARD_H,
  CARD_MAX_W,
  CARD_MIN_W,
  cardDetail,
  drawCard,
  ellipsize,
  measureCards,
} from "./cards.js";
import type { GNode } from "./types.js";

function fakeCtx(): CanvasRenderingContext2D {
  return {
    font: "",
    measureText: (s: string) => ({ width: s.length * 6 }),
  } as unknown as CanvasRenderingContext2D;
}

function fakeNode(id: string, label: string): GNode {
  return {
    id,
    kind: "target",
    label,
    degree: 0,
    r: 0,
    x: 0,
    y: 0,
    fx: null,
    fy: null,
  } as unknown as GNode;
}

test("measureCards: a very long label clamps to CARD_MAX_W", () => {
  const ctx = fakeCtx();
  const nodes = [fakeNode("a", "a".repeat(80))];
  measureCards(ctx, nodes, "sans");
  assert.equal(nodes[0].w, CARD_MAX_W);
  assert.equal(nodes[0].h, CARD_H);
});

test("measureCards: a 1-char label clamps up to CARD_MIN_W", () => {
  const ctx = fakeCtx();
  const nodes = [fakeNode("a", "x")];
  measureCards(ctx, nodes, "sans");
  assert.equal(nodes[0].w, CARD_MIN_W);
  assert.equal(nodes[0].h, CARD_H);
});

test("measureCards: a mid-length label sits strictly between MIN and MAX", () => {
  const ctx = fakeCtx();
  // 20 chars * 6px + 2*10 padding = 140, comfortably inside [96, 200].
  const nodes = [fakeNode("a", "a".repeat(20))];
  measureCards(ctx, nodes, "sans");
  assert.equal(nodes[0].w, 140);
});

// The cache is keyed on nothing but "already measured", so a font change has to force a re-measure
// or a card sized for the old font clips its own label.
test("measureCards: a font change re-measures an already-measured node", () => {
  const nodes = [fakeNode("a", "a".repeat(20))];
  measureCards(fakeCtx(), nodes, "sans");
  assert.equal(nodes[0].w, 140);

  // Same font, wider metric: the cached width stands, which is the skip doing its job.
  const wide = {
    font: "",
    measureText: (s: string) => ({ width: s.length * 12 }),
  } as unknown as CanvasRenderingContext2D;
  measureCards(wide, nodes, "sans");
  assert.equal(nodes[0].w, 140);

  // Different font: everything is measured again, so the wider metric lands.
  measureCards(wide, nodes, "serif");
  assert.equal(nodes[0].w, CARD_MAX_W);
});

test("ellipsize: returns the original string when it already fits", () => {
  const ctx = fakeCtx();
  const text = "short";
  assert.equal(ellipsize(ctx, text, 200), text);
});

test("ellipsize: returns a shorter mid-ellipsis string when it does not fit", () => {
  const ctx = fakeCtx();
  const text = "a-very-long-target-name-that-does-not-fit";
  const out = ellipsize(ctx, text, 100);
  assert.ok(out.length < text.length);
  assert.ok(out.includes("..."));
  // Middle-ellipsis: keeps a prefix and a suffix of the original text.
  const [head, tail] = out.split("...");
  assert.ok(text.startsWith(head));
  assert.ok(text.endsWith(tail));
});

test("ellipsize: deterministic across repeated calls on the same input", () => {
  const ctx = fakeCtx();
  const text = "another-quite-long-identifier-for-a-target";
  const first = ellipsize(ctx, text, 90);
  const second = ellipsize(ctx, text, 90);
  assert.equal(first, second);
});

// cardDetail: how much of a card survives the zoom. The thresholds are stated in screen
// pixels, so the assertions below convert through the card's world-unit geometry.

test("a card at natural scale paints in full", () => {
  assert.equal(cardDetail(1), "full");
});

test("a label too small to read drops out before the box does", () => {
  // 12 world units of text, so the 7px label floor is crossed at k = 7/12.
  assert.equal(cardDetail(0.6), "full");
  assert.equal(cardDetail(0.5), "plain");
});

test("a card thinner than a sliver becomes a dot", () => {
  // CARD_H is 34 world units, so the 11px box floor is crossed just under k = 0.324.
  assert.equal(cardDetail(0.35), "plain");
  assert.equal(cardDetail(0.3), "dot");
});

test("the fit that frames a whole build DAG lands in dot territory", () => {
  assert.equal(cardDetail(0.12), "dot");
});

test("detail only ever coarsens as the view zooms out", () => {
  const rank = { full: 2, plain: 1, dot: 0 };
  let prev = rank[cardDetail(2)];
  for (let k = 2; k > 0.02; k -= 0.01) {
    const r = rank[cardDetail(k)];
    assert.ok(r <= prev, "detail increased while zooming out at k=" + k.toFixed(2));
    prev = r;
  }
});

// The lower line of a card (project, duration) is read at 12px, the console's type floor. The fake
// context records every font a fillText was painted in and what it painted.
function recordingCtx() {
  const painted: Array<{ text: string; font: string; x: number; y: number }> = [];
  const ctx = {
    font: "",
    textAlign: "left",
    measureText: (s: string) => ({ width: s.length * 6 }),
    fillText(text: string, x: number, y: number) {
      painted.push({ text, font: this.font, x, y });
    },
    fillRect() {},
    strokeRect() {},
    beginPath() {},
    arc() {},
    fill() {},
    stroke() {},
  };
  return { ctx: ctx as unknown as CanvasRenderingContext2D, painted };
}

const THEME = { bg: "", text: "", muted: "", border: "", accent: "", font: "sans" };

test("drawCard: the project and duration lines are painted at 12px, never smaller", () => {
  const { ctx, painted } = recordingCtx();
  const n = fakeNode("t", "build");
  n.w = 200;
  n.h = CARD_H;
  n.attrs = { project: "console" };
  drawCard(ctx, n, {
    theme: THEME,
    kindColor: "",
    alpha: 1,
    selected: false,
    anchor: false,
    zoomK: 1,
    durationText: "1.2s",
  });
  assert.deepEqual(
    painted.map((p) => p.text),
    ["build", "console", "1.2s"],
  );
  for (const p of painted) {
    const px = Number(/(\d+)px/.exec(p.font)?.[1]);
    assert.ok(px >= 12, p.text + " was painted at " + px + "px");
  }
});

test("drawCard: a long project name leaves the duration its width", () => {
  const { ctx, painted } = recordingCtx();
  const n = fakeNode("t", "build");
  n.w = 120;
  n.h = CARD_H;
  n.attrs = { project: "a-very-long-project-name" };
  drawCard(ctx, n, {
    theme: THEME,
    kindColor: "",
    alpha: 1,
    selected: false,
    anchor: false,
    zoomK: 1,
    durationText: "12.5s",
  });
  const project = painted.find((p) => p.text !== "build" && p.text !== "12.5s");
  assert.ok(project, "the project line was painted");
  // 6px per character, and the duration needs its own width plus the gap.
  const budget = 120 - 3 - 2 * 10 - ("12.5s".length * 6 + 6);
  assert.ok(
    project.text.length * 6 <= budget,
    "project text " + project.text + " overruns " + budget,
  );
});
