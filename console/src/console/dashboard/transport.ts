// transport.ts - the two live feeds, mapped into the store. Nothing here touches the
// DOM: it owns the daemon connections and writes view-model into the store; tiles
// subscribe. Two feeds ride alongside each other, both locked to the validated
// loopback host and both bearing the shared token:
//
//   1. /api/v1/events SSE (event: status) -> magus.status.v1alpha1.Status: the instantaneous
//      view (health, pool, running targets, workspaces, live cache tallies). Its
//      open/close is THE connection whose state drives the connected/disconnected pill.
//   2. magus.metrics.v1alpha1.MetricsService.StreamMetrics over ConnectRPC: the developer view
//      (latency percentiles, remote cache, per-target/MCP/Buzz/Sandbox families). First
//      message is a Backfill (Sample history), then a Snapshot per ~1s tick.
//
// The utilization grid + cache-rate chart are seeded from the metrics Backfill, then
// kept live by synthesizing one Sample per status frame (the status stream carries live
// pool occupancy + cache tallies; the metrics stream carries the families it does not).

import { fromBinary } from "@bufbuild/protobuf";
import { createClient, type Client } from "@connectrpc/connect";
import {
  StatusSchema,
  StatusService,
  type Status,
} from "../../gen/magus/status/v1alpha1/status_pb";
import { MetricsService } from "../../gen/magus/metrics/v1alpha1/metrics_pb";
import { ActivityService, Kind } from "../../gen/magus/activity/v1alpha1/activity_pb";
import { InsightService } from "../../gen/magus/insight/v1alpha1/insight_pb";
import { ToolService, Verdict } from "../../gen/magus/tool/v1alpha1/tool_pb";
import {
  authHeaders,
  createDaemonTransport,
  fetchSSE,
  getLiveToken,
  type SSEHeaders,
} from "../../lib/daemon";
import { getPollMs } from "../../lib/settings";
import type { Store } from "../../lib/store";
import {
  mapStatus,
  mapSnapshot,
  mapSample,
  mapInsight,
  mapAgentActivity,
  type AgentEventWire,
  type DashboardState,
  type SampleView,
  type ToolRowView,
  renderWindow,
} from "./state";

const GRID_MAX = 7 * 52; // ~a GitHub year of columns; the rolling sample window
const RECONNECT_MS = 3000;
// The activity poll's cadence, and how many events a page asks for. Independent of the operator's
// refresh-rate setting: the agent tile reports a five-minute window, so a slower poll only delays
// when a new call shows up, and a faster one buys nothing. The page has to be deep enough to cover
// that window on a busy daemon - an agent mid-task easily produces a few hundred tool calls.
const ACTIVITY_POLL_MS = 4000;
const ACTIVITY_PAGE = 500;
// Insight is an on-demand unary read (magus.insight.v1alpha1.InsightService.GetInsight), server-side
// cached ~10s. Not on the status SSE: it is polled on a cadence, refetched on open and on a manual
// refresh. The interval is the operator's configured refresh rate (getPollMs, default 20s - just
// above the server cache TTL).

export interface TransportCallbacks {
  onStatusOpen(host: string): void;
  onStatusError(host: string): void;
}

// verdictLabel maps the wire enum to the word the table shows. UNKNOWN stays its own
// label rather than collapsing into "inside": "we could not check" must not read as fine.
const verdictLabel = (v: Verdict): "inside" | "too old" | "too new" | "unknown" => {
  switch (v) {
    case Verdict.TOO_OLD:
      return "too old";
    case Verdict.TOO_NEW:
      return "too new";
    case Verdict.INSIDE:
      return "inside";
    default:
      return "unknown";
  }
};

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

  private activityHost: string | null = null;
  private activityTimer: ReturnType<typeof setInterval> | null = null;

  private toolsHost: string | null = null;
  private toolsTimer: ReturnType<typeof setInterval> | null = null;
  private insightHost: string | null = null;
  private insightAbort: AbortController | null = null;
  private insightTimer: ReturnType<typeof setInterval> | null = null;

  // stopped is a permanent give-up latch. Once set (by stop()), no feed reschedules:
  // the status reconnect, the metrics retry, and the insight poll all bail while it is
  // true, so a never-connected resume that gives up stops hammering an absent daemon
  // entirely. connect() clears it before starting a fresh set of feeds.
  private stopped = false;
  // A hidden dashboard retains its latest state but does not retain a live connection or poll
  // loop. The console calls suspend/resume with pane visibility, so this is not a timer policy
  // every tile has to rediscover for itself.
  private host: string | null = null;
  private suspended = false;

  constructor(store: Store<DashboardState>, cb: TransportCallbacks) {
    this.store = store;
    this.cb = cb;
  }

  connect(host: string): void {
    this.stopped = false;
    this.suspended = false;
    this.host = host;
    this.disconnect();
    this.connectStatus(host);
    this.startMetrics(host);
    this.startInsight(host);
    this.startTools(host);
    this.startActivity(host);
    void this.fetchObservingSince(host);
  }

  disconnect(): void {
    if (this.statusAbort) {
      this.statusAbort.abort();
      this.statusAbort = null;
    }
    if (this.statusRetry) {
      clearTimeout(this.statusRetry);
      this.statusRetry = null;
    }
    this.stopMetrics();
    this.stopInsight();
    this.stopTools();
    this.stopActivity();
  }

  // stop is the permanent give-up: it tears down all three feeds (status SSE, metrics
  // stream, and the insight poll, each with its retry timer) and latches `stopped` so
  // nothing reschedules. Used when a never-connected resume abandons the host, so NO
  // request loop runs against a daemon that isn't there. connect() clears the latch.
  stop(): void {
    this.stopped = true;
    this.host = null;
    this.disconnect();
  }

  suspend(): void {
    if (this.suspended || !this.host) return;
    this.suspended = true;
    this.disconnect();
  }

  resume(): void {
    if (!this.suspended) return;
    this.suspended = false;
    const host = this.host;
    if (host) this.connect(host);
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
    if (this.stopped || this.suspended || this.statusRetry) return;
    this.statusRetry = setTimeout(() => {
      this.statusRetry = null;
      if (this.statusAbort && !this.statusAbort.signal.aborted) this.connectStatus(host);
    }, RECONNECT_MS);
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
      // The status pool tallies are a DIFFERENT counter baseline than the metrics
      // Backfill's OTel counter; tag it so cacheRate skips the crossover diff.
      cacheSrc: "status",
      // The streamed frame does not carry observing-since - it rides the one-shot envelope -
      // so reuse the value fetched at connect. A daemon restart drops this stream, and the
      // reconnect re-fetches, so the value cannot outlive the process it identifies.
      generation: this.observingSinceMs,
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
    if (this.metricsAbort) {
      this.metricsAbort.abort();
      this.metricsAbort = null;
    }
    if (this.metricsRetry) {
      clearTimeout(this.metricsRetry);
      this.metricsRetry = null;
    }
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
    if (this.stopped || this.metricsRetry) return;
    this.metricsRetry = setTimeout(() => {
      this.metricsRetry = null;
      if (this.metricsAbort && !this.metricsAbort.signal.aborted)
        void this.runMetrics(host, this.metricsAbort.signal);
    }, RECONNECT_MS);
  }

  // ---- insight (on-demand ConnectRPC poll) ---------------------------------
  // A unary GetInsight against the validated loopback host, bearing the shared token
  // and mapped into the store. Polled on a modest cadence since it is server-side
  // cached; refetched immediately on connect and on a manual refresh.

  // ---- activity trail poll -------------------------------------------------
  //
  // POLLED, not streamed, and deliberately so for now. ActivityService exposes only ListActivityEvents -
  // a request/response page - so a live feed would mean designing and shipping a server-stream
  // first. A page of the most recent events every few seconds is enough for a tile that reports
  // "agents active in the last five minutes", and it needs no proto change at all. If the tile ever
  // wants per-event immediacy, that is the moment to add the stream, not before.
  private startActivity(host: string): void {
    this.stopActivity();
    this.activityHost = host;
    void this.fetchActivity();
    this.activityTimer = setInterval(() => void this.fetchActivity(), ACTIVITY_POLL_MS);
  }

  private stopActivity(): void {
    this.activityHost = null;
    if (this.activityTimer) {
      clearInterval(this.activityTimer);
      this.activityTimer = null;
    }
  }

  private async fetchActivity(): Promise<void> {
    if (this.stopped || this.suspended) return;
    const host = this.activityHost;
    if (!host) return;
    try {
      const client = createClient(ActivityService, createDaemonTransport(host, getLiveToken()));
      const resp = await client.listActivityEvents({
        pageSize: ACTIVITY_PAGE,
        // Only the two kinds the tile reads. Filtering server-side keeps a busy daemon's job and
        // memory events from crowding the page and pushing agent events off the end of it.
        filter: { kinds: [Kind.AGENT_COMMAND, Kind.MCP_TOOL_CALL] },
      });
      const events: AgentEventWire[] = resp.events.map((e) => ({
        atMs: e.time ? Number(e.time.seconds) * 1000 + Math.floor(e.time.nanos / 1e6) : 0,
        isAgentCommand: e.kind === Kind.AGENT_COMMAND,
        isMcpCall: e.kind === Kind.MCP_TOOL_CALL,
        host: e.host || "",
        session: e.session || "",
        preview: e.preview || "",
        action: e.action || "",
      }));
      this.store.set({ agents: mapAgentActivity(events, Date.now()) });
    } catch {
      // A daemon without a trail, an older daemon without the host/session fields, or a blip: keep
      // whatever is on screen and let the next poll retry. The tile has its own empty state.
    }
  }

  // ---- toolchain (on-demand ConnectRPC poll) -------------------------------
  //
  // Polled on the same modest cadence as insight, and for a stronger reason: behind this
  // RPC the daemon FORKS a version probe per declared tool, cached there behind a TTL. A
  // fast poll would turn a left-open dashboard into a fork loop, so the tile shows the
  // probe's age instead of pretending the reading is live.

  private startTools(host: string): void {
    this.stopTools();
    this.toolsHost = host;
    void this.fetchTools();
    this.toolsTimer = setInterval(() => void this.fetchTools(), getPollMs());
  }

  private stopTools(): void {
    this.toolsHost = null;
    if (this.toolsTimer) {
      clearInterval(this.toolsTimer);
      this.toolsTimer = null;
    }
  }

  // refreshTools forces an out-of-band refetch (the section's refresh button). It does not
  // bypass the daemon's probe TTL, so pressing it repeatedly costs nothing.
  refreshTools(): void {
    if (this.toolsHost) void this.fetchTools();
  }

  private async fetchTools(): Promise<void> {
    if (this.stopped || this.suspended) return;
    const host = this.toolsHost;
    if (!host) return;
    try {
      const client = createClient(ToolService, createDaemonTransport(host, getLiveToken()));
      const resp = await client.listTools({});
      const rows: ToolRowView[] = [];
      for (const proj of resp.projects) {
        for (const tool of proj.tools) {
          rows.push({
            project: proj.path,
            bin: tool.bin,
            spell: tool.spell,
            installed: tool.installedVersion,
            spellWindow: renderWindow(tool.spellBounds),
            workspaceWindow: renderWindow(tool.workspaceBounds),
            effectiveWindow: renderWindow(tool.effective),
            verdict: verdictLabel(tool.verdict),
            code: tool.diagnosticCode,
            probedAtMs: tool.probeTime
              ? Number(tool.probeTime.seconds) * 1000 + Math.floor(tool.probeTime.nanos / 1e6)
              : 0,
          });
        }
      }
      const violations = rows.filter((r) => r.code !== "").length;
      this.store.set({ tools: { rows, violations } });
    } catch {
      // A network blip or a daemon with no workspace: leave the prior view in place.
    }
  }

  private startInsight(host: string): void {
    this.stopInsight();
    this.insightHost = host;
    // Said HERE, not at construction: before a daemon is attached nothing is reading anything, and
    // seeding this in the initial state made six tiles claim a read was in progress on a dashboard
    // that had never connected.
    this.store.set({ insightNote: "Reading history..." });
    void this.fetchInsight();
    this.insightTimer = setInterval(() => void this.fetchInsight(), getPollMs());
  }

  private stopInsight(): void {
    // Nothing will poll again, so leaving the in-progress note standing would keep asserting a read
    // that has stopped - the same lie this field exists to remove, one state further along.
    if (this.insightHost) this.store.set({ insightNote: "Not connected." });
    this.insightHost = null;
    if (this.insightAbort) {
      this.insightAbort.abort();
      this.insightAbort = null;
    }
    if (this.insightTimer) {
      clearInterval(this.insightTimer);
      this.insightTimer = null;
    }
  }

  // refreshInsight forces an out-of-band refetch (the section's refresh button). Returns the
  // fetch's promise, not fire-and-forget, so the button can show feedback for exactly the
  // duration of the click that asked for it - a background poll tick has no caller to tell.
  refreshInsight(): Promise<void> {
    return this.insightHost ? this.fetchInsight() : Promise.resolve();
  }

  private async fetchInsight(): Promise<void> {
    if (this.stopped || this.suspended) return;
    const host = this.insightHost;
    if (!host) return;
    // A tick that lands while the previous one is still running SKIPS rather than superseding it.
    // The history walk can outrun the poll interval (the floor is 2s), and aborting-and-restarting
    // every tick then means no request ever finishes: the tiles would sit on "Reading history..."
    // for as long as the dashboard is open, which is a worse lie than stale numbers. stopInsight
    // still aborts, so teardown is unaffected.
    if (this.insightAbort) return;
    const ctrl = new AbortController();
    this.insightAbort = ctrl;
    try {
      const client = createClient(InsightService, createDaemonTransport(host, getLiveToken()));
      const resp = await client.getInsight({}, { signal: ctrl.signal });
      // The SUCCESS path needs the same guard as the catch. A superseded poll can still resolve if
      // its body was already buffered when the next tick aborted it, and writing here would both
      // overwrite the newer poll's insight and clear an error the newer poll had just recorded.
      if (ctrl.signal.aborted) return;
      this.store.set({
        insight: mapInsight(resp),
        insightNote: null,
        insightUpdatedAt: Date.now(),
      });
    } catch (e) {
      // A network blip or a daemon with no workspace (CodeUnavailable): leave the prior insight in
      // place; the poll retries. But RECORD it, because on the first connect there is no prior
      // insight to leave and the tiles would otherwise report an empty window as a measured result.
      //
      // Tested on ctrl's own signal rather than the error: a superseded poll is this poller
      // cancelling itself and is not a failure, and the transport does not promise a stable error
      // shape for it (a fetch abort and a Connect Canceled do not look alike).
      if (ctrl.signal.aborted) return;
      const msg = e instanceof Error ? e.message : String(e);
      this.store.set({ insightNote: "The daemon did not answer (" + msg + ")." });
    } finally {
      // Releases the in-flight latch above. Guarded on identity so a poll that stopInsight already
      // superseded cannot clear a NEWER poll's controller on its way out.
      if (this.insightAbort === ctrl) this.insightAbort = null;
    }
  }

  // One-shot fetch of the daemon's observing-since (when it began collecting the counters) and its
  // resolved config, read via the typed StatusService.GetStatus RPC. Both ride the one-shot response
  // envelope (not the streamed Status frame) because they are static per session. This replaced the
  // deprecated JSON GET /api/v1/status route. Best-effort: a failure just means no since-caption / config;
  // it never blocks the live view.
  // The generation the live synthesis stamps on each sample; see onStatus.
  private observingSinceMs: number | null = null;

  private async fetchObservingSince(host: string): Promise<void> {
    try {
      const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
      const resp = await client.getStatus({});
      const ts = resp.observeStartTime;
      if (ts) {
        this.observingSinceMs = Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6);
        this.store.set({ observingSince: this.observingSinceMs });
      }
      if (resp.config) {
        this.store.set({
          config: {
            defaultCharms: resp.config.defaultCharms,
            concurrency: resp.config.concurrency,
            sandbox: resp.config.sandbox,
          },
        });
      }
    } catch {
      // Network blip or abort: leave observingSince null; nothing on the board depends on it.
    }
  }

  // ---- sample history ------------------------------------------------------

  private seedSamples(history: SampleView[]): void {
    if (this.seeded) return;
    this.seeded = true;
    // Any live samples appended before the Backfill land after history.
    this.samples = history.concat(this.samples);
    if (this.samples.length > GRID_MAX)
      this.samples = this.samples.slice(this.samples.length - GRID_MAX);
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
