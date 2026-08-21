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
// no symmetry at all and looked like noise. Here the motif repeats exactly `folds` times.
test("the figure repeats exactly as many times as it has folds", () => {
  for (let i = 0; i < 200; i++) {
    const spec = sigilSpec("/Repos/p" + i);
    assert.ok(spec.folds >= 3 && spec.folds <= 8, "folds " + spec.folds);
    const d = /<path d="([^"]*)Z"/.exec(renderSigil(spec, 100))?.[1] ?? "";
    const verts = d.split(/[ML]/).filter(Boolean).length;
    // One hand-over point plus the motif, mirrored or not, per wedge.
    const perWedge = 1 + spec.motif.length * (spec.mirror ? 2 : 1);
    assert.equal(verts, perWedge * spec.folds, "p" + i + " is not k-fold symmetric");
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
