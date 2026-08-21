// sigil.test.ts - three properties carry this: the mark never changes for a workspace, it is always
// symmetric (which is what keeps it from reading as noise), and no two workspaces a daemon has loaded
// can ever draw the same picture.

import assert from "node:assert/strict";
import { test } from "node:test";
import { SIGIL_HUES, assignSigils, renderSigil, sigilSpec, sigilSvg, specKey } from "./sigil";

const ACME = "/Users/eli/Repos/acme";
const MAGUS = "/Users/eli/Repos/magus";

test("the same workspace always draws the same mark", () => {
  assert.deepEqual(sigilSpec(ACME), sigilSpec(ACME));
  assert.equal(sigilSvg(ACME), sigilSvg(ACME));
});

test("a near-identical root is not a near-identical mark", () => {
  assert.notEqual(specKey(sigilSpec(MAGUS)), specKey(sigilSpec(MAGUS + "-2")));
  assert.notEqual(specKey(sigilSpec("/a/b")), specKey(sigilSpec("/a/c")));
});

// The property that stops these reading as scribble. An earlier version traced a name across a magic
// square, whose defining feature is scattering consecutive values to unrelated cells - so the path had
// no symmetry at all and looked like noise.
//
// Tested GEOMETRICALLY rather than by counting path commands: turn the figure by one wedge and it must
// land on itself. That survives changing how the curve is drawn - which counting did not, since the
// segments became quadratics the moment these were made less crystalline.
function endpoints(svg: string): [number, number][] {
  const d = /<path d="([^"]*)"/.exec(svg)?.[1] ?? "";
  const out: [number, number][] = [];
  for (const m of d.matchAll(/[MQ]([-\d.]+) ([-\d.]+)(?: ([-\d.]+) ([-\d.]+))?/g)) {
    // A Q carries a control point then an endpoint; an M carries only the point.
    const x = m[3] !== undefined ? Number(m[3]) : Number(m[1]);
    const y = m[4] !== undefined ? Number(m[4]) : Number(m[2]);
    out.push([x, y]);
  }
  return out;
}

test("turning the figure by one fold lands it on itself", () => {
  const SIZE = 200;
  const c = SIZE / 2;
  for (let i = 0; i < 120; i++) {
    const spec = sigilSpec("/Repos/sym" + i);
    const pts = endpoints(renderSigil(spec, SIZE));
    assert.ok(pts.length >= spec.folds, "sym" + i + " drew too little to test");
    const a = (Math.PI * 2) / spec.folds;
    const key = (q: [number, number]): string => q[0].toFixed(0) + "," + q[1].toFixed(0);
    const have = new Set(pts.map(key));
    for (const [x, y] of pts) {
      const dx = x - c;
      const dy = y - c;
      const rx = c + dx * Math.cos(a) - dy * Math.sin(a);
      const ry = c + dx * Math.sin(a) + dy * Math.cos(a);
      // Allow a pixel of slack: the path is emitted at two decimals and rotation is not exact in
      // floating point.
      const near = [...have].some((k) => {
        const [hx, hy] = k.split(",").map(Number);
        return Math.hypot(hx - rx, hy - ry) <= 1.5;
      });
      assert.ok(near, "sym" + i + " is not " + spec.folds + "-fold symmetric");
    }
  }
});

test("the figure closes", () => {
  for (let i = 0; i < 50; i++) {
    assert.match(sigilSvg("/Repos/c" + i), /<path d="M[^"]*Z"/, "c" + i + " did not close");
  }
});

// Uniqueness cannot be guaranteed against arbitrary inputs - finitely many pictures, unboundedly many
// paths. It CAN be guaranteed across the set a daemon has loaded, which is the only place a collision
// would ever be seen.
test("no two workspaces on one daemon can draw the same mark", () => {
  const roots = Array.from({ length: 200 }, (_, i) => "/Users/eli/Repos/ws" + i);
  const specs = assignSigils(roots);
  assert.equal(specs.size, roots.length);
  const keys = new Set([...specs.values()].map(specKey));
  assert.equal(keys.size, roots.length, "collisions: " + (roots.length - keys.size));
});

// Which of a colliding pair moves must not depend on the order the daemon happened to list them, or
// the same workspace would change mark between reloads.
test("assignment does not depend on the order the daemon lists them", () => {
  const roots = ["/r/a", "/r/b", "/r/c", "/r/d", "/r/e", "/r/f"];
  const forward = assignSigils(roots);
  const backward = assignSigils([...roots].reverse());
  for (const r of roots) {
    assert.equal(specKey(forward.get(r)!), specKey(backward.get(r)!), r + " moved");
  }
});

// A workspace whose mark did not have to move must keep the one it would have had alone, or the mark
// stops being a property of the workspace.
test("an uncontested workspace keeps its own mark", () => {
  const specs = assignSigils([ACME, MAGUS]);
  assert.equal(specKey(specs.get(ACME)!), specKey(sigilSpec(ACME)));
});

// The base generator has to be wide enough that resolution is rare rather than routine - otherwise the
// marks are really being assigned by list position, not derived from the workspace.
test("the base space is wide enough that collisions are rare", () => {
  const keys = new Set<string>();
  for (let i = 0; i < 500; i++) keys.add(specKey(sigilSpec("/Users/eli/Repos/project-" + i)));
  assert.ok(keys.size > 490, "only " + keys.size + " distinct marks in 500 workspaces");
});

// A motif that sits near the hand-over radius throughout draws a plain regular polygon: a valid mark
// and a forgettable one. `magus` came out as a bare heptagon before this was forced.
test("no sigil degenerates into a plain polygon", () => {
  for (let i = 0; i < 400; i++) {
    const spec = sigilSpec("/Repos/shape" + i);
    const reach = Math.max(...spec.motif.map((q) => Math.abs(q.r - spec.edge)));
    assert.ok(reach >= 0.29, "shape" + i + " is nearly a polygon (reach " + reach.toFixed(2) + ")");
  }
  const magus = sigilSpec("/Users/eli/Repos/magus");
  const reach = Math.max(...magus.motif.map((q) => Math.abs(q.r - magus.edge)));
  assert.ok(reach >= 0.29, "the case that prompted this is still flat");
});

test("the hue is always one the palette defines", () => {
  for (const r of [ACME, MAGUS, "", "/x"]) {
    assert.ok(SIGIL_HUES.includes(sigilSpec(r).hue as (typeof SIGIL_HUES)[number]), r);
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
