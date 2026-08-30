import { test } from "node:test";
import assert from "node:assert/strict";
import { demoLeases, demoOverlaps } from "./demo";
import { buildPlan, isTerminal, normalizeState, LEASE_STATES, type LeaseState } from "./ledger";

// The showcase is a first impression, and these pin the claims its header comment makes: that it
// exercises every state, that both warnings are reachable, and that it stays put across reads.

const NOW = 1_760_000_000_000;

test("the demo ledger builds a plan with every lease rooted in one tree", () => {
  const model = buildPlan(demoLeases(NOW), demoOverlaps());
  assert.equal(model.nodes.length, 7);
  const roots = model.nodes.filter((n) => !n.parent);
  assert.equal(roots.length, 1, "one root, so the tree reads as a single plan");
  assert.equal(roots[0]?.id, "claims-audience");
});

test("every state the surface can draw appears in the demo", () => {
  const seen = new Set<LeaseState>(demoLeases(NOW).map((u) => normalizeState(u.state)));
  for (const s of LEASE_STATES) {
    assert.ok(seen.has(s), `the showcase must exercise the ${s} state`);
  }
});

test("the overlap names two leases that exist and really do intersect", () => {
  const ids = new Set(demoLeases(NOW).map((u) => u.id));
  const overlaps = demoOverlaps();
  assert.ok(overlaps.length > 0, "the overlap warning must be reachable in the demo");
  for (const o of overlaps) {
    assert.ok(ids.has(o.lease_a), `${o.lease_a} is not a lease in the demo`);
    assert.ok(ids.has(o.lease_b), `${o.lease_b} is not a lease in the demo`);
    assert.ok(o.paths_a.length > 0 && o.paths_b.length > 0, "each side declares its own paths");
    // Not a formality: an overlap whose paths do not actually intersect would be the surface
    // crying wolf in the one place a reader is asked to trust it.
    const hit = o.paths_a.some((a) => o.paths_b.some((b) => a.startsWith(b) || b.startsWith(a)));
    assert.ok(hit, `${o.lease_a} and ${o.lease_b} are declared to overlap but their paths do not`);
  }
});

test("a stale non-terminal lease exists, since a terminal row draws no warning", () => {
  const leases = demoLeases(NOW);
  const nowSec = Math.floor(NOW / 1000);
  const stale = leases.filter(
    (u) => !isTerminal(normalizeState(u.state)) && nowSec - (u.updated ?? nowSec) > 3600,
  );
  assert.ok(stale.length > 0, "the staleness warning must be reachable in the demo");
});

test("dependencies and parents only ever name leases that exist", () => {
  const leases = demoLeases(NOW);
  const ids = new Set(leases.map((u) => u.id));
  for (const u of leases) {
    if (u.parent) assert.ok(ids.has(u.parent), `${u.id} names a missing parent ${u.parent}`);
    for (const d of u.depends_on ?? []) {
      assert.ok(ids.has(d), `${u.id} depends on a missing lease ${d}`);
    }
  }
});

test("timestamps are epoch SECONDS in the past, never strings", () => {
  const nowSec = Math.floor(NOW / 1000);
  for (const u of demoLeases(NOW)) {
    for (const [field, v] of [
      ["created", u.created],
      ["updated", u.updated],
    ] as const) {
      assert.equal(typeof v, "number", `${u.id}.${field} must be a number`);
      // Seconds, not milliseconds: served as ms these read as dates far in the future and every
      // age label in the surface silently becomes wrong rather than absent.
      assert.ok((v as number) <= nowSec, `${u.id}.${field} is in the future`);
      assert.ok((v as number) > nowSec - 86_400, `${u.id}.${field} looks like milliseconds`);
    }
    assert.ok((u.updated ?? 0) >= (u.created ?? 0), `${u.id} was updated before it was created`);
  }
});

test("the fixture is a pure function of now, so two reads agree", () => {
  assert.deepEqual(demoLeases(NOW), demoLeases(NOW));
  assert.deepEqual(demoOverlaps(), demoOverlaps());
});
