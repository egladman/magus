import { createLogger } from '../logging/index.mjs';

export class Counter {
  constructor(name) {
    this.name = name;
    this.value = 0;
  }

  // Adds delta (default 1) and returns the new value.
  add(delta = 1) {
    this.value += delta;
    return this.value;
  }

  reset() {
    this.value = 0;
  }
}

export class Timer {
  constructor(name) {
    this.name = name;
    this.samples = [];
  }

  record(ms) {
    this.samples.push(ms);
    return this.samples.length;
  }

  // Zero samples average to 0 rather than NaN, so a snapshot of an idle timer stays
  // JSON-serializable.
  average() {
    if (this.samples.length === 0) return 0;
    return this.samples.reduce((sum, ms) => sum + ms, 0) / this.samples.length;
  }
}

// Collapses counters and timers into a plain object keyed by metric name, in sorted
// key order, and logs the metric count.
export function snapshot(metrics) {
  const logger = createLogger('metrics');
  const out = {};
  for (const metric of [...metrics].sort((a, b) => a.name.localeCompare(b.name))) {
    out[metric.name] = metric instanceof Timer ? metric.average() : metric.value;
  }
  logger.log('info', 'snapshot', { metrics: Object.keys(out).length });
  return out;
}
