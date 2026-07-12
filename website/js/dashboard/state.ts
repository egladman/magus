// state.ts - the dashboard's store shape, its formatters, and the proto -> view-model
// mappers. Tiles NEVER see a raw protobuf message: transport.ts maps every wire
// message through the functions here into plain view-model objects, and only those
// are written into the store. That keeps the tiles ignorant of protobuf-es and the
// wire's bigint/Timestamp quirks, and gives every number one formatting home.

import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
  Health, TargetRun_State, type Status, type Pool, type Run, type TargetRun,
} from "../gen/magus/status/v1/status_pb";
import type {
  Snapshot, Latency, Remote, TargetStat, MCPToolStat, Buzz, Sandbox, Sample as ProtoSample,
} from "../gen/magus/metrics/v1/metrics_pb";
import type { ConnState } from "../lib/daemon";

// ---- formatters ------------------------------------------------------------

export function fmtArgs(args: string[] | undefined): string {
  return (args && args.length) ? "magus " + args.join(" ") : "magus";
}

export function fmtBytes(n: number | bigint): string {
  let v = Number(n || 0);
  if (v <= 0) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)) + " " + u[i];
}

export function fmtCount(n: number | bigint): string {
  return Number(n || 0).toLocaleString();
}

// fmtDur renders a duration given in SECONDS as a plain-ASCII string (us / ms / s).
// The metrics wire carries every latency in seconds.
export function fmtDur(sec: number | null | undefined): string {
  if (sec == null || sec <= 0) return "-";
  const ms = sec * 1000;
  if (ms < 1) return Math.round(ms * 1000) + " us";
  if (ms < 1000) return (ms < 10 ? ms.toFixed(1) : Math.round(ms).toString()) + " ms";
  return sec.toFixed(sec < 10 ? 2 : 1) + " s";
}

export function fmtPct(fraction: number | null | undefined): string {
  if (fraction == null) return "-";
  return Math.round(fraction * 100) + "%";
}

export function tsMillis(ts: Timestamp | undefined): number {
  if (!ts) return Date.now();
  return Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
}

export function relTime(ts: Timestamp | undefined): string {
  if (!ts) return "";
  const secs = Math.max(0, Math.round((Date.now() - tsMillis(ts)) / 1000));
  if (secs < 60) return secs + "s";
  const mins = Math.round(secs / 60);
  if (mins < 60) return mins + "m";
  return Math.round(mins / 60) + "h";
}

export function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString();
}

// ---- connection ------------------------------------------------------------

export interface ConnView {
  // "none" (never connected, nothing pending) and "demo" (the daemon-free showcase
  // fed by demo.ts, no real connection at all) are dashboard-only states layered on
  // top of the daemon module's connecting/connected/disconnected.
  state: ConnState | "none" | "demo";
  detail?: string;
}

// ---- status view-model (SSE) -----------------------------------------------

const HEALTH: Record<number, { label: string; cls: string }> = {
  [Health.HEALTHY]: { label: "healthy", cls: "ok" },
  [Health.DEGRADED]: { label: "degraded", cls: "warn" },
  [Health.DOWN]: { label: "down", cls: "fail" },
  [Health.UNSPECIFIED]: { label: "unknown", cls: "" },
};

export interface HealthView { label: string; cls: string; }
export interface PoolView { capacity: number; running: number; queued: number; mode: string; }
export interface CacheView { hits: number; misses: number; errors: number; hitRate: number | null; sizeBytes: number | bigint; }
export interface RunningTargetView { args: string[]; step: string; startTime?: Timestamp; invocation: string; }
export interface WorkspaceView { root: string; hits?: number; misses?: number; errors?: number; lastAccessTime?: Timestamp; }
export interface ServiceView { id: string; label: string; command: string; ports: string[]; state: string; dependents: number; startedAt?: Timestamp; }

// A target's lifecycle state, as plain view-model strings that double as the gantt
// tile's CSS class suffixes (.gantt-bar.running, .gantt-bar.passed, ...). Kept in
// lockstep with magus.status.v1.TargetRun.State but stringly-typed so tiles never
// import the proto enum.
export type TargetState = "unspecified" | "queued" | "running" | "passed" | "failed" | "cached";

// TargetRunView is one target's execution within a run: its state, its wall-clock
// window (startMs unset while QUEUED, endMs unset while active), and, once finished,
// the output reference the gantt bar deep-links to.
export interface TargetRunView {
  project: string;
  target: string;
  label: string;             // "project:target" (or just the target when project is empty)
  state: TargetState;
  terminal: boolean;         // passed | failed | cached
  startMs: number | null;
  endMs: number | null;
  outputRef: string;
  durationMs: number;
}

// RunView groups a run's targets under its invocation + trigger, the row-group the
// live gantt draws.
export interface RunView {
  inv: string;
  trigger: string;
  targets: TargetRunView[];
}

export interface StatusView {
  health: HealthView;
  pool: PoolView;
  cache: CacheView;
  runningTargets: RunningTargetView[];
  runs: RunView[];
  workspaces: WorkspaceView[];
  // Shared services the daemon is hosting right now (deduped across the whole daemon, kept warm
  // between runs). Empty when none are held.
  services: ServiceView[];
  magusVersion: string;
  daemonVersion: string;
}

const TARGET_STATE: Record<number, TargetState> = {
  [TargetRun_State.STATE_UNSPECIFIED]: "unspecified",
  [TargetRun_State.QUEUED]: "queued",
  [TargetRun_State.RUNNING]: "running",
  [TargetRun_State.PASSED]: "passed",
  [TargetRun_State.FAILED]: "failed",
  [TargetRun_State.CACHED]: "cached",
};

function mapTargetRun(t: TargetRun): TargetRunView {
  const state = TARGET_STATE[t.state] || "unspecified";
  return {
    project: t.project || "",
    target: t.target || "",
    label: t.project ? t.project + ":" + t.target : (t.target || ""),
    state,
    terminal: state === "passed" || state === "failed" || state === "cached",
    startMs: t.startedAt ? tsMillis(t.startedAt) : null,
    endMs: t.endedAt ? tsMillis(t.endedAt) : null,
    outputRef: t.outputRef || "",
    durationMs: Number(t.durationMs || 0),
  };
}

function mapRun(r: Run): RunView {
  return {
    inv: r.inv || "",
    trigger: r.trigger || "",
    targets: (r.targets || []).map(mapTargetRun),
  };
}

function mapCache(cache: Pool["cache"] | undefined): CacheView {
  const hits = cache ? Number(cache.hits) : 0;
  const misses = cache ? Number(cache.misses) : 0;
  const errors = cache ? Number(cache.errors) : 0;
  const total = hits + misses;
  return { hits, misses, errors, hitRate: total > 0 ? hits / total : null, sizeBytes: cache ? cache.sizeBytes : 0 };
}

export function mapStatus(st: Status): StatusView {
  const pool = st.pool;
  return {
    health: HEALTH[st.health] || HEALTH[Health.UNSPECIFIED],
    pool: {
      capacity: pool ? pool.capacity : 0,
      running: pool ? pool.running : 0,
      queued: pool ? pool.queued : 0,
      mode: (pool && pool.mode) || "",
    },
    cache: mapCache(pool && pool.cache),
    runningTargets: ((pool && pool.runningTargets) || []).map((c) => ({
      args: c.args || [], step: c.step || "", startTime: c.startTime, invocation: c.invocation || "",
    })),
    runs: (st.runs || []).map(mapRun),
    workspaces: ((pool && pool.workspaces) || []).map((w) => ({
      root: w.root,
      hits: w.cache ? Number(w.cache.hits) : undefined,
      misses: w.cache ? Number(w.cache.misses) : undefined,
      errors: w.cache ? Number(w.cache.errors) : undefined,
      lastAccessTime: w.lastAccessTime,
    })),
    services: (st.services || []).map((sv) => ({
      id: sv.id || "", label: sv.label || "", command: sv.command || "",
      ports: sv.port || [], state: sv.state || "", dependents: sv.dependents || 0,
      startedAt: sv.startedAt,
    })),
    magusVersion: st.magusVersion || "",
    daemonVersion: (pool && pool.daemonVersion) || "",
  };
}

// ---- metrics view-model (ConnectRPC Snapshot) ------------------------------

export interface LatView { count: number; p50: number; p95: number; p99: number; max: number; }

export const LAT_KEYS = ["target", "cache", "poolWait", "graphQuery"] as const;
export type LatKey = typeof LAT_KEYS[number];
export const LAT_META: Record<LatKey, { label: string; term: string }> = {
  // The wire field is "cache" (renamed from the old "cacheOp": "op" collides with the
  // Operation glossary term, and the family measures a Cache.Run, not a resolved op).
  target: { label: "Target execution", term: "Target" },
  cache: { label: "Cache op", term: "Cache" },
  poolWait: { label: "Pool wait", term: "Pool" },
  graphQuery: { label: "Graph query", term: "Knowledge graph" },
};

function mapLat(l: Latency | undefined): LatView | null {
  if (!l) return null;
  return { count: Number(l.count), p50: l.p50, p95: l.p95, p99: l.p99, max: l.max };
}

export interface RemoteView {
  hits: number; misses: number; errors: number; hitRate: number | null;
  durationP50: number; durationP95: number; ioCount: number; bytesTotal: number | bigint;
}

function mapRemote(r: Remote | undefined): RemoteView | null {
  if (!r) return null;
  const hits = Number(r.hits), misses = Number(r.misses);
  const total = hits + misses;
  return {
    hits, misses, errors: Number(r.errors), hitRate: total > 0 ? hits / total : null,
    durationP50: r.durationP50, durationP95: r.durationP95, ioCount: Number(r.ioCount), bytesTotal: r.bytesTotal,
  };
}

export interface TargetStatView {
  project: string; target: string; spell: string; count: number;
  p50: number; p95: number; p99: number; cacheHitRate: number; success: number; errors: number;
}

function mapTargetStat(t: TargetStat): TargetStatView {
  return {
    project: t.project, target: t.target, spell: t.spell, count: Number(t.count),
    p50: t.p50, p95: t.p95, p99: t.p99, cacheHitRate: t.cacheHitRate,
    success: Number(t.success), errors: Number(t.errors),
  };
}

export interface McpToolView {
  tool: string; calls: number; errors: number;
  inputP50: number; inputP95: number; inputTotal: number | bigint;
  outputP50: number; outputP95: number; outputTotal: number | bigint;
  durationP50: number; durationP95: number;
}

function mapMcpTool(m: MCPToolStat): McpToolView {
  return {
    tool: m.tool, calls: Number(m.calls), errors: Number(m.errors),
    inputP50: m.inputP50, inputP95: m.inputP95, inputTotal: m.inputTotal,
    outputP50: m.outputP50, outputP95: m.outputP95, outputTotal: m.outputTotal,
    durationP50: m.durationP50, durationP95: m.durationP95,
  };
}

export interface BuzzView {
  execCount: number; execP50: number; execP95: number;
  compileCount: number; compileP50: number; compileP95: number;
  hostCallCount: number; hostCallP50: number; hostCallP95: number;
  sessionPoolReuse: number; sessionPoolIdle: number; sessionPoolEvictions: number;
  sessionWarmP50: number; sessionWarmP95: number;
  importCount: number; importP50: number; importP95: number;
  spellResolveCount: number; spellResolveP50: number; spellResolveP95: number;
  jitRuns: number; vmFaults: number;
}

function mapBuzz(b: Buzz | undefined): BuzzView | null {
  if (!b) return null;
  return {
    execCount: Number(b.execCount), execP50: b.execP50, execP95: b.execP95,
    compileCount: Number(b.compileCount), compileP50: b.compileP50, compileP95: b.compileP95,
    hostCallCount: Number(b.hostCallCount), hostCallP50: b.hostCallP50, hostCallP95: b.hostCallP95,
    sessionPoolReuse: Number(b.sessionPoolReuse), sessionPoolIdle: Number(b.sessionPoolIdle),
    sessionPoolEvictions: Number(b.sessionPoolEvictions),
    sessionWarmP50: b.sessionWarmP50, sessionWarmP95: b.sessionWarmP95,
    importCount: Number(b.importCount), importP50: b.importP50, importP95: b.importP95,
    spellResolveCount: Number(b.spellResolveCount), spellResolveP50: b.spellResolveP50, spellResolveP95: b.spellResolveP95,
    jitRuns: Number(b.jitRuns), vmFaults: Number(b.vmFaults),
  };
}

export interface SandboxView {
  applyP50: number; applyP95: number;
  rulesRead: number; rulesWrite: number; rulesExec: number; envRules: number;
  checksAllow: number; checksDeny: number; envDropped: number;
}

function mapSandbox(s: Sandbox | undefined): SandboxView | null {
  if (!s) return null;
  return {
    applyP50: s.applyP50, applyP95: s.applyP95,
    rulesRead: Number(s.rulesRead), rulesWrite: Number(s.rulesWrite), rulesExec: Number(s.rulesExec),
    envRules: Number(s.envRules), checksAllow: Number(s.checksAllow), checksDeny: Number(s.checksDeny),
    envDropped: Number(s.envDropped),
  };
}

export interface MetricsView {
  capturedMs: number;
  latency: Record<LatKey, LatView | null>;
  remote: RemoteView | null;
  targetStats: TargetStatView[];
  mcpTools: McpToolView[];
  buzz: BuzzView | null;
  sandbox: SandboxView | null;
}

export function mapSnapshot(snap: Snapshot): MetricsView {
  return {
    capturedMs: tsMillis(snap.capturedAt),
    latency: {
      target: mapLat(snap.target),
      cache: mapLat(snap.cache),
      poolWait: mapLat(snap.poolWait),
      graphQuery: mapLat(snap.graphQuery),
    },
    remote: mapRemote(snap.remote),
    targetStats: (snap.targetStats || []).map(mapTargetStat),
    mcpTools: (snap.mcpTools || []).map(mapMcpTool),
    buzz: mapBuzz(snap.buzz),
    sandbox: mapSandbox(snap.sandbox),
  };
}

// ---- utilization samples ---------------------------------------------------
// One unified Sample shape fed from two sources: the metrics Backfill (history)
// and a live synthesis per status frame. Counters are cumulative; the cache-rate
// chart diffs adjacent samples for a per-interval rate.
//
// The cache tallies do NOT share a baseline across those two sources: the metrics
// Backfill carries the global monotonic OTel counter (magus.cache.hits), while the
// live status synthesis carries the sum of the currently-warm workspaces' cache
// counters. `cacheSrc` records which one so the cache-rate chart can refuse to diff
// across the crossover (a mismatched-baseline diff shows a spurious gap or spike);
// occupancy (running/capacity/queued) comes from the same StatusReport in both, so
// it needs no such tag.

export type CacheSrc = "metrics" | "status";

export interface SampleView {
  at: number;        // ms
  running: number;
  capacity: number;  // 0 = unlimited
  queued: number;
  cacheHits: number;
  cacheMisses: number;
  cacheSrc: CacheSrc; // baseline source of cacheHits/cacheMisses
}

export function mapSample(s: ProtoSample): SampleView {
  return {
    at: tsMillis(s.at),
    running: s.running,
    capacity: s.capacity,
    queued: s.queued,
    cacheHits: Number(s.cacheHits),
    cacheMisses: Number(s.cacheMisses),
    cacheSrc: "metrics",
  };
}

// ---- insight view-model (on-demand JSON) -----------------------------------
// GET /api/v1/insight returns types.InsightView as PLAIN JSON (json.Marshal, not
// protobuf), so this axis has no generated proto: the wire shapes below mirror the
// Go json tags exactly, and mapInsight folds them into camelCase view-models the
// tiles read. Times arrive as RFC3339 strings (a zero time.Time serializes to the
// year-0001 sentinel), so fmtDateStr renders them and treats the sentinel as blank.

export function fmtDateStr(iso: string | undefined): string {
  if (!iso) return "-";
  const t = Date.parse(iso);
  if (!Number.isFinite(t) || t <= 0) return "-"; // the 0001-01-01 zero-value parses negative
  return new Date(t).toLocaleDateString();
}

// Raw wire shapes (snake_case json tags from types/*.go). Not exported: only the
// mapped view-models below cross into tiles.
interface HotspotNodeWire { path: string; churn?: number; authors?: number; blast_radius?: number; last_commit?: string; }
interface HotspotWire { commits: number; since?: string; nodes: HotspotNodeWire[] | null; }
interface CoChangeWire { a: string; b: string; count: number; hidden?: boolean; }
interface AffinityWire { commits: number; pairs: CoChangeWire[] | null; }
interface OwnershipWire { path: string; commits: number; authors: number; primary: string; primary_share: number; bus_factor_1?: boolean; stale?: boolean; }
interface OwnershipOutWire { commits: number; projects: OwnershipWire[] | null; }
interface TrendWire { path: string; recent: number; earlier: number; delta: number; }
interface TrendOutWire { commits: number; projects: TrendWire[] | null; }
interface VolatilityTargetWire { project: string; target: string; score: number; volatile?: boolean; pass: number; fail: number; volatile_count: number; samples: number; last_pass?: string; }
interface VolatilityWire { threshold: number; targets: VolatilityTargetWire[] | null; }
export interface InsightWire {
  hotspots: HotspotWire;
  affinity: AffinityWire;
  ownership: OwnershipOutWire;
  trend: TrendOutWire;
  volatility: VolatilityWire | null;
}

export interface HotspotNodeView { name: string; churn: number; authors: number; blastRadius: number; lastCommit: string; }
export interface AffinityPairView { a: string; b: string; count: number; hidden: boolean; }
export interface OwnershipRowView { path: string; primary: string; primaryShare: number; authors: number; busFactor1: boolean; stale: boolean; }
export interface TrendRowView { path: string; delta: number; recent: number; earlier: number; }
export interface VolatilityRowView {
  label: string; score: number; volatile: boolean;
  pass: number; fail: number; volatileCount: number; samples: number; lastPass: string;
}
export interface VolatilityView { threshold: number; targets: VolatilityRowView[]; }

export interface InsightView {
  commits: number; // the git-history window shared by the four VCS lenses
  hotspots: HotspotNodeView[];
  affinity: AffinityPairView[];
  ownership: OwnershipRowView[];
  trend: TrendRowView[];
  volatility: VolatilityView | null;
}

export function mapInsight(w: InsightWire): InsightView {
  return {
    commits: w.hotspots?.commits ?? 0,
    hotspots: (w.hotspots?.nodes ?? []).map((n) => ({
      name: n.path,
      churn: n.churn ?? 0,
      authors: n.authors ?? 0,
      blastRadius: n.blast_radius ?? 0,
      lastCommit: fmtDateStr(n.last_commit),
    })),
    affinity: (w.affinity?.pairs ?? []).map((p) => ({
      a: p.a, b: p.b, count: p.count, hidden: !!p.hidden,
    })),
    ownership: (w.ownership?.projects ?? []).map((o) => ({
      path: o.path, primary: o.primary || "-", primaryShare: o.primary_share,
      authors: o.authors, busFactor1: !!o.bus_factor_1, stale: !!o.stale,
    })),
    trend: (w.trend?.projects ?? []).map((t) => ({
      path: t.path, delta: t.delta, recent: t.recent, earlier: t.earlier,
    })),
    volatility: w.volatility ? {
      threshold: w.volatility.threshold,
      targets: (w.volatility.targets ?? []).map((v) => ({
        label: v.project ? v.project + ":" + v.target : v.target,
        score: v.score, volatile: !!v.volatile,
        pass: v.pass, fail: v.fail, volatileCount: v.volatile_count,
        samples: v.samples, lastPass: fmtDateStr(v.last_pass),
      })),
    } : null,
  };
}

// ---- the store shape -------------------------------------------------------
// One value published on every tick. Slices are filled independently: `status`
// arrives on the SSE frame, `metrics`/`samples` on the Connect stream, `insight`
// on a polled on-demand JSON read. Tiles read only the slice they render.
// `liveHost` deep-links running calls into live logs.

export interface DashboardState {
  conn: ConnView;
  liveHost: string | null;
  status: StatusView | null;
  metrics: MetricsView | null;
  samples: SampleView[];
  insight: InsightView | null;
  // logLines is a rolling buffer of raw captured-output lines for the live-activity
  // preview. Only the demo feed (demo.ts) synthesizes it; live mode leaves it empty,
  // because the daemon's status SSE carries pool/health frames, not a raw-output
  // journal - a real live tail would need a journal SSE consumer (see activity.ts).
  logLines: string[];
  // observingSince is when the daemon began collecting the telemetry/cache counters (epoch ms),
  // read once from the JSON status endpoint (it is static per session and not on the proto event
  // stream). null until known. Surfaced so the board can be transparent that the numbers are
  // cumulative since then and are NOT persisted across daemon restarts. The demo synthesizes one.
  observingSince: number | null;
}

export function initialState(): DashboardState {
  return { conn: { state: "none" }, liveHost: null, status: null, metrics: null, samples: [], insight: null, logLines: [], observingSince: null };
}
