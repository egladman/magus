// transport.ts - the two live feeds, mapped into the store. Nothing here touches the
// DOM: it owns the daemon connections and writes view-model into the store; tiles
// subscribe. Two feeds ride alongside each other, both locked to the validated
// loopback host and both bearing the shared token:
//
//   1. /api/v1/events SSE (event: status) -> magus.status.v1.Status: the instantaneous
//      view (health, pool, running targets, workspaces, live cache tallies). Its
//      open/close is THE connection whose state drives the connected/disconnected pill.
//   2. magus.metrics.v1.MetricsService.StreamMetrics over ConnectRPC: the developer view
//      (latency percentiles, remote cache, per-target/MCP/Buzz/Sandbox families). First
//      message is a Backfill (Sample history), then a Snapshot per ~1s tick.
//
// The utilization grid + cache-rate chart are seeded from the metrics Backfill, then
// kept live by synthesizing one Sample per status frame (the status stream carries live
// pool occupancy + cache tallies; the metrics stream carries the families it does not).

import { fromBinary } from "@bufbuild/protobuf";
import { createClient, type Client } from "@connectrpc/connect";
import { StatusSchema, type Status } from "../gen/magus/status/v1/status_pb";
import { MetricsService } from "../gen/magus/metrics/v1/metrics_pb";
import {
  authHeaders, createDaemonTransport, fetchSSE, getLiveToken, type SSEHeaders,
} from "../lib/daemon";
import type { Store } from "../lib/store";
import {
  mapStatus, mapSnapshot, mapSample, type DashboardState, type SampleView,
} from "./state";

const GRID_MAX = 7 * 52; // ~a GitHub year of columns; the rolling sample window
const RECONNECT_MS = 3000;

export interface TransportCallbacks {
  onStatusOpen(host: string): void;
  onStatusError(host: string): void;
}

export class DashboardTransport {
  private store: Store<DashboardState>;
  private cb: TransportCallbacks;

  private samples: SampleView[] = [];
  private seeded = false;
  private lastSampleAt = 0;

  private statusAbort: AbortController | null = null;
  private statusRetry: ReturnType<typeof setTimeout> | null = null;
  private metricsAbort: AbortController | null = null;
  private metricsRetry: ReturnType<typeof setTimeout> | null = null;

  constructor(store: Store<DashboardState>, cb: TransportCallbacks) {
    this.store = store;
    this.cb = cb;
  }

  connect(host: string): void {
    this.disconnect();
    this.connectStatus(host);
    this.startMetrics(host);
  }

  disconnect(): void {
    if (this.statusAbort) { this.statusAbort.abort(); this.statusAbort = null; }
    if (this.statusRetry) { clearTimeout(this.statusRetry); this.statusRetry = null; }
    this.stopMetrics();
  }

  // ---- status SSE ----------------------------------------------------------

  private connectStatus(host: string): void {
    if (this.statusAbort) this.statusAbort.abort();
    this.statusAbort = new AbortController();
    const url = "http://" + host + "/api/v1/events";
    const headers: SSEHeaders = authHeaders();
    void fetchSSE(
      url,
      headers,
      (type, data) => {
        if (type !== "status") return;
        try {
          const raw = Uint8Array.from(atob(data), (ch) => ch.charCodeAt(0));
          this.onStatus(fromBinary(StatusSchema, raw));
        } catch {
          // Ignore a malformed frame; the next one supersedes it.
        }
      },
      () => {
        this.cb.onStatusError(host);
        this.scheduleStatusReconnect(host);
      },
      this.statusAbort.signal,
      () => {
        this.store.set({ liveHost: host });
        this.cb.onStatusOpen(host);
      },
    );
  }

  private scheduleStatusReconnect(host: string): void {
    if (this.statusRetry) return;
    this.statusRetry = setTimeout(() => {
      this.statusRetry = null;
      if (this.statusAbort && !this.statusAbort.signal.aborted) this.connectStatus(host);
    }, RECONNECT_MS);
  }

  // stopStatusReconnect halts the status reconnect loop (used by main when a
  // never-connected resume gives up, so it stops hammering an absent daemon).
  stopStatusReconnect(): void {
    if (this.statusRetry) { clearTimeout(this.statusRetry); this.statusRetry = null; }
    this.stopMetrics();
  }

  private onStatus(st: Status): void {
    const view = mapStatus(st);
    // Synthesize one utilization Sample from this live frame (the metrics stream does
    // not carry pool occupancy) so the grid + rate chart stay live.
    this.appendSample({
      at: Date.now(),
      running: view.pool.running,
      capacity: view.pool.capacity,
      queued: view.pool.queued,
      cacheHits: view.cache.hits,
      cacheMisses: view.cache.misses,
    });
    this.store.set({ status: view, samples: this.samples });
  }

  // ---- metrics stream (ConnectRPC) -----------------------------------------

  private makeMetricsClient(host: string): Client<typeof MetricsService> {
    return createClient(MetricsService, createDaemonTransport(host, getLiveToken()));
  }

  private startMetrics(host: string): void {
    this.stopMetrics();
    this.metricsAbort = new AbortController();
    void this.runMetrics(host, this.metricsAbort.signal);
  }

  private stopMetrics(): void {
    if (this.metricsAbort) { this.metricsAbort.abort(); this.metricsAbort = null; }
    if (this.metricsRetry) { clearTimeout(this.metricsRetry); this.metricsRetry = null; }
  }

  private async runMetrics(host: string, signal: AbortSignal): Promise<void> {
    const client = this.makeMetricsClient(host);
    try {
      for await (const res of client.streamMetrics({}, { signal })) {
        if (res.of.case === "backfill") this.seedSamples(res.of.value.samples.map(mapSample));
        else if (res.of.case === "snapshot") this.store.set({ metrics: mapSnapshot(res.of.value) });
      }
      if (!signal.aborted) this.scheduleMetricsRetry(host); // stream ended cleanly: reconnect
    } catch {
      if (!signal.aborted) this.scheduleMetricsRetry(host);
    }
  }

  private scheduleMetricsRetry(host: string): void {
    if (this.metricsRetry) return;
    this.metricsRetry = setTimeout(() => {
      this.metricsRetry = null;
      if (this.metricsAbort && !this.metricsAbort.signal.aborted) void this.runMetrics(host, this.metricsAbort.signal);
    }, RECONNECT_MS);
  }

  // ---- sample history ------------------------------------------------------

  private seedSamples(history: SampleView[]): void {
    if (this.seeded) return;
    this.seeded = true;
    // Any live samples appended before the Backfill land after history.
    this.samples = history.concat(this.samples);
    if (this.samples.length > GRID_MAX) this.samples = this.samples.slice(this.samples.length - GRID_MAX);
    if (this.samples.length) this.lastSampleAt = this.samples[this.samples.length - 1].at;
    this.store.set({ samples: this.samples });
  }

  // appendSample records a synthesized live Sample, throttled to ~1/s so a burst of
  // status frames doesn't flood the grid.
  private appendSample(s: SampleView): void {
    if (this.samples.length && s.at - this.lastSampleAt < 900) return;
    this.lastSampleAt = s.at;
    this.samples.push(s);
    if (this.samples.length > GRID_MAX) this.samples.shift();
  }
}
