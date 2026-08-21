// cacheRateSeries.ts - the rule for turning cumulative cache counters into a per-interval
// rate. It lives apart from the tile that draws it because it is domain logic, not chart
// code: every interesting case here is a decision about when two samples MAY NOT be
// subtracted, and those are worth testing without a canvas.
//
// A cumulative counter can only be differenced against another reading of THE SAME
// counter. Three things break that, and each emits a gap rather than a number:
//
//   - a baseline crossover (cacheSrc): the metrics backfill reads the global OTel counter,
//     the live synthesis the warm-workspace sum. Different totals, so their difference is
//     not a rate.
//   - a generation change: the daemon restarted and the counters went back to zero.
//   - an unmeasured endpoint: a rate needs two real readings.
//
// A gap is the honest output for all three. The temptation in each case is to clamp the
// negative difference to zero, which reports "no cache activity this interval" - a plausible
// number that is simply false, and unlike a gap it does not look wrong.

import type { SampleView } from "../state";

// RatePoint is one interval: the time of its LATER endpoint, and the hit rate over it as a
// percentage, or null for an interval that cannot be measured.
export interface RatePoint {
  atMs: number;
  rate: number | null;
}

// comparable reports whether two adjacent samples measure the same counter, so their
// difference is a rate rather than an artifact.
export function comparable(a: SampleView, b: SampleView): boolean {
  if (a.cacheSrc !== b.cacheSrc) return false;
  // An unknown generation on either side counts as a boundary: joining an unknown process
  // to a known one is the guess the field exists to prevent.
  if (a.generation === null || b.generation === null) return false;
  return a.generation === b.generation;
}

// cacheRateSeries derives one point per adjacent pair. A quiet interval - both counters
// unchanged - is also a gap: there is no rate to report when nothing happened, and drawing
// it as 0% would claim every lookup missed.
export function cacheRateSeries(samples: SampleView[]): RatePoint[] {
  const out: RatePoint[] = [];
  for (let i = 1; i < samples.length; i++) {
    const a = samples[i - 1];
    const b = samples[i];
    out.push({ atMs: b.at, rate: intervalRate(a, b) });
  }
  return out;
}

function intervalRate(a: SampleView, b: SampleView): number | null {
  if (!comparable(a, b)) return null;
  if (
    a.cacheHits === null ||
    b.cacheHits === null ||
    a.cacheMisses === null ||
    b.cacheMisses === null
  ) {
    return null;
  }
  const hits = b.cacheHits - a.cacheHits;
  const misses = b.cacheMisses - a.cacheMisses;
  // Negative here would mean a counter went backwards within one generation, which the
  // comparable() checks above have already ruled out for every cause we can name. Treat a
  // surviving negative as unmeasurable rather than clamping it: an unexplained decrease is
  // a fact about the data, and a clamp would launder it into a confident zero.
  if (hits < 0 || misses < 0) return null;
  const total = hits + misses;
  return total > 0 ? (hits / total) * 100 : null;
}
