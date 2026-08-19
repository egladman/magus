import test from "node:test";
import assert from "node:assert/strict";
import { cacheRateSeries, comparable } from "./cacheRateSeries";
import type { SampleView } from "../state";

// sample builds a SampleView with the fields this rule reads, defaulting to one generation
// and one counter source so a test names only the axis it is about.
function sample(over: Partial<SampleView> = {}): SampleView {
  return {
    at: 1_000,
    running: 0,
    capacity: 4,
    queued: 0,
    cacheHits: 0,
    cacheMisses: 0,
    cacheSrc: "metrics",
    generation: 100,
    ...over,
  };
}

test("a rate is the hit share of the interval, not of the running total", () => {
  const pts = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 90, cacheMisses: 10 }),
    // 90% lifetime so far, but this interval was 3 hits and 1 miss.
    sample({ at: 2_000, cacheHits: 93, cacheMisses: 11 }),
  ]);
  assert.equal(pts.length, 1);
  assert.equal(pts[0].atMs, 2_000, "a point is stamped at its LATER endpoint");
  assert.equal(pts[0].rate, 75);
});

test("an interval where nothing happened has no rate to report", () => {
  const pts = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 5, cacheMisses: 5 }),
    sample({ at: 2_000, cacheHits: 5, cacheMisses: 5 }),
  ]);
  assert.equal(pts[0].rate, null, "0% would claim every lookup missed");
});

// The bug this rule was added for. Before generation identity, a restart produced a
// negative difference that a Math.max(0, ...) clamp turned into a zero delta - drawn as a
// minute with no cache activity, which is a plausible reading of a real event that did not
// happen.
test("a daemon restart breaks the series instead of plotting a confident zero", () => {
  const pts = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 900, cacheMisses: 100, generation: 100 }),
    sample({ at: 2_000, cacheHits: 3, cacheMisses: 1, generation: 200 }),
  ]);
  assert.equal(pts[0].rate, null, "counters restarted at zero; there is no rate across that");
});

test("an unknown generation on either side is a boundary", () => {
  assert.equal(comparable(sample({ generation: null }), sample({ generation: 100 })), false);
  assert.equal(comparable(sample({ generation: 100 }), sample({ generation: null })), false);
  assert.equal(comparable(sample({ generation: 100 }), sample({ generation: 100 })), true);
});

test("the backfill-to-live crossover is a boundary even within one generation", () => {
  const pts = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 900, cacheMisses: 100, cacheSrc: "metrics" }),
    sample({ at: 2_000, cacheHits: 12, cacheMisses: 3, cacheSrc: "status" }),
  ]);
  assert.equal(pts[0].rate, null, "the two feeds count different things");
});

test("an unmeasured endpoint yields a gap, never an invented zero", () => {
  const withNull = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 10, cacheMisses: 1 }),
    sample({ at: 2_000, cacheHits: null, cacheMisses: null }),
  ]);
  assert.equal(withNull[0].rate, null);
});

// A decrease with no crossover and no generation change is unexplained. Clamping it would
// launder a data fault into a confident number.
test("an unexplained decrease is unmeasurable rather than clamped", () => {
  const pts = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 50, cacheMisses: 5 }),
    sample({ at: 2_000, cacheHits: 40, cacheMisses: 5 }),
  ]);
  assert.equal(pts[0].rate, null);
});

test("a run of samples yields one point per interval", () => {
  const pts = cacheRateSeries([
    sample({ at: 1_000, cacheHits: 0, cacheMisses: 0 }),
    sample({ at: 2_000, cacheHits: 1, cacheMisses: 1 }),
    sample({ at: 3_000, cacheHits: 4, cacheMisses: 1 }),
  ]);
  assert.deepEqual(
    pts.map((p) => p.rate),
    [50, 100], // second interval: 3 hits, 0 misses
  );
  assert.equal(cacheRateSeries([sample()]).length, 0, "one sample spans no interval");
  assert.equal(cacheRateSeries([]).length, 0);
});
