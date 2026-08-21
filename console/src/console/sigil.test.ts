// sigil.test.ts - an identifier that changes is not one, and two identifiers that collide are worse
// than none. Those are the two properties worth pinning; everything else about the drawing is taste.

import assert from "node:assert/strict";
import { test } from "node:test";
import { SIGIL_HUES, sigilFigure, sigilHue, sigilSvg } from "./sigil";

const ACME = "/Users/eli/Repos/acme";
const MAGUS = "/Users/eli/Repos/magus";

test("the same workspace always draws the same mark", () => {
  assert.equal(sigilSvg(ACME), sigilSvg(ACME));
  assert.deepEqual(sigilFigure(ACME), sigilFigure(ACME));
  assert.equal(sigilHue(ACME), sigilHue(ACME));
});

// Sibling worktrees differ by a suffix and are exactly the pair someone needs to tell apart.
test("a near-identical root is not a near-identical mark", () => {
  assert.notEqual(sigilSvg(MAGUS), sigilSvg(MAGUS + "-2"));
  assert.notEqual(sigilSvg(MAGUS), sigilSvg(MAGUS + "/"));
  assert.notEqual(sigilSvg("/a/b"), sigilSvg("/a/c"));
});

// The bug this pins was VISIBLE and no test caught it: a stride of points-1 is coprime, so it passed
// the reach-every-vertex check, but it traverses the regular polygon backwards. A twelve-pointed
// sigil drew as a plain ring with no figure inside it.
test("a sigil is never a plain ring", () => {
  for (let i = 0; i < 400; i++) {
    const root = "/Users/eli/Repos/ws-" + i;
    const { points, step } = sigilFigure(root);
    assert.ok(points >= 5 && points <= 12, root + " points=" + points);
    assert.ok(step >= 2 && step <= points - 2, root + " step=" + step + " of " + points);
    assert.equal(gcd(step, points), 1, root + " stride does not reach every point");
  }
});

function gcd(a: number, b: number): number {
  return b === 0 ? a : gcd(b, a % b);
}

// Not a proof of no collisions - a 32-bit hash has them - but the marks have to be spread enough that
// a handful of workspaces on one machine are visibly distinct.
test("marks are spread across the space, not clustered", () => {
  const seen = new Set<string>();
  for (let i = 0; i < 200; i++) seen.add(JSON.stringify(sigilFigure("/Repos/p" + i)));
  assert.ok(seen.size > 20, "only " + seen.size + " distinct figures in 200 workspaces");
});

// Point count and hue alone left three of a dozen real workspaces looking like the same blue
// pentagram. The core is the axis that separates them, so it has to actually vary.
test("the drawing varies beyond its point count", () => {
  const drawings = new Set<string>();
  for (let i = 0; i < 300; i++) drawings.add(sigilSvg("/Repos/p" + i, 40));
  assert.ok(drawings.size > 250, "only " + drawings.size + " distinct drawings in 300 workspaces");

  // All four cores are reachable, or the axis is not really there.
  const cores = new Set<string>();
  for (let i = 0; i < 300; i++) {
    const svg = sigilSvg("/Repos/p" + i, 40);
    cores.add(
      svg.includes('r="2" fill')
        ? "dot"
        : /<circle[^>]*r="(?!2")[\d.]+"[^>]*\/><\/svg>|<circle cx="20" cy="20" r="[\d.]+"\/>/.test(
              svg,
            )
          ? "ring-or-none"
          : svg.split("<path").length > 2
            ? "triangle"
            : "none",
    );
  }
  assert.ok(cores.size >= 3, "cores seen: " + [...cores].join(","));
});

test("the hue is always one the palette defines", () => {
  for (const r of [ACME, MAGUS, "", "/x"]) {
    assert.ok(SIGIL_HUES.includes(sigilHue(r) as (typeof SIGIL_HUES)[number]), r);
  }
});

// Decorative: the workspace already has a name and this is not a second one.
test("the mark is drawn, not announced", () => {
  const svg = sigilSvg(ACME);
  assert.match(svg, /aria-hidden="true"/);
  assert.ok(!svg.includes("<title"));
});

test("an empty root still yields a drawable mark", () => {
  assert.match(sigilSvg(""), /^<svg /);
});
