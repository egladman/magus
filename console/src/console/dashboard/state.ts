// state.ts - the dashboard's store shape, its formatters, and the proto -> view-model
// mappers. Tiles NEVER see a raw protobuf message: transport.ts maps every wire
// message through the functions here into plain view-model objects, and only those
// are written into the store. That keeps the tiles ignorant of protobuf-es and the
// wire's bigint/Timestamp quirks, and gives every number one formatting home.

import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
  Health,
  TargetRun_State,
  type Status,
  type Pool,
  type Run,
  type TargetRun,
} from "../../gen/magus/status/v1alpha1/status_pb";
import type {
  Snapshot,
  Latency,
  Remote,
  TargetStat,
  MCPToolStat,
  Buzz,
  Sandbox,
  Sample as ProtoSample,
} from "../../gen/magus/metrics/v1alpha1/metrics_pb";
import type { Insight } from "../../gen/magus/insight/v1alpha1/insight_pb";
import type { ConnState } from "../../lib/daemon";

// ---- formatters ------------------------------------------------------------

export function fmtArgs(args: string[] | undefined): string {
  return args && args.length ? "magus " + args.join(" ") : "magus";
}

export function fmtBytes(n: number | bigint): string {
  let v = Number(n || 0);
  if (v <= 0) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
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

// tsMillisOrNow converts a protobuf Timestamp to epoch milliseconds, substituting NOW when the
// field is absent. The "OrNow" is in the name because that substitution is a real decision and a
// lossy one: an event with no timestamp renders as "0s ago", i.e. indistinguishable from one that
// just happened. Callers that would rather show nothing want activity/adapter.ts's tsMillis, which
// returns null instead - the two used to share the plain name and differ only in that, which is
// exactly the kind of pair a reader resolves wrongly.
export function tsMillisOrNow(ts: Timestamp | undefined): number {
  if (!ts) return Date.now();
  return Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
}

export function relTime(ts: Timestamp | undefined): string {
  if (!ts) return "";
  const secs = Math.max(0, Math.round((Date.now() - tsMillisOrNow(ts)) / 1000));
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

export interface HealthView {
  label: string;
  cls: string;
}
export interface PoolView {
  capacity: number;
  running: number;
  queued: number;
  mode: string;
}
export interface CacheView {
  hits: number;
  misses: number;
  errors: number;
  hitRate: number | null;
  sizeBytes: number | bigint;
}
export interface RunningTargetView {
  args: string[];
  step: string;
  startTime?: Timestamp;
  invocation: string;
}
export interface WorkspaceView {
  root: string;
  hits?: number;
  misses?: number;
  errors?: number;
  lastAccessTime?: Timestamp;
  // Name of the selected secret-provider spell; "" or undefined means the built-in
  // environment provider. The NAME only - the wire carries no reference and no value.
  secretProvider?: string;
}
export interface ServiceView {
  id: string;
  label: string;
  command: string;
  ports: string[];
  state: string;
  dependents: number;
  startedAt?: Timestamp;
}
// LockView is one held per-project workspace lock and the process holding it.
//
// Held is the normal state of a mutating run, so this is never rendered as a fault.
// The value is the holder: an OS file lock carries no identity, and a lock lives
// exactly as long as its holder, so one held by a process nobody remembers starting
// blocks every other run silently. Age is what separates the two cases.
export interface LockWaiterView {
  pid: number;
  command: string;
  waitTime?: Timestamp;
}
export interface LockView {
  project: string;
  pid: number;
  command: string;
  dir: string;
  acquireTime?: Timestamp;
  // Supplied by the daemon so this renderer cannot drift from the CLI's judgement.
  staleAfterSeconds: number;
  // Who is stalled behind this holder. A holder alone says who is working; this says
  // who is paying for it, which is the half a reader of a stuck queue wants.
  waiters: LockWaiterView[];
}
export interface ConfigView {
  defaultCharms: string[];
  concurrency: number;
  sandbox: boolean;
}

// A target's lifecycle state, as plain view-model strings that double as the gantt
// tile's CSS class suffixes (.gantt-bar.running, .gantt-bar.passed, ...). Kept in
// lockstep with magus.status.v1alpha1.TargetRun.State but stringly-typed so tiles never
// import the proto enum.
export type TargetState = "unspecified" | "queued" | "running" | "passed" | "failed" | "cached";

// TargetRunView is one target's execution within a run: its state, its wall-clock
// window (startMs unset while QUEUED, endMs unset while active), and, once finished,
// the output reference the gantt bar deep-links to.
export interface TargetRunView {
  project: string;
  target: string;
  label: string; // "project:target" (or just the target when project is empty)
  state: TargetState;
  terminal: boolean; // passed | failed | cached
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
  // Workspace locks held right now. Empty when nothing is mutating a project.
  locks: LockView[];
  magusVersion: string; // the daemon binary's version (status BuildInfo.version)
  daemonVersion: string;
}

const TARGET_STATE: Record<number, TargetState> = {
  [TargetRun_State.UNSPECIFIED]: "unspecified",
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
    label: t.project ? t.project + ":" + t.target : t.target || "",
    state,
    terminal: state === "passed" || state === "failed" || state === "cached",
    startMs: t.startTime ? tsMillisOrNow(t.startTime) : null,
    endMs: t.endTime ? tsMillisOrNow(t.endTime) : null,
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
  return {
    hits,
    misses,
    errors,
    hitRate: total > 0 ? hits / total : null,
    sizeBytes: cache ? cache.sizeBytes : 0,
  };
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
      args: c.args || [],
      step: c.step || "",
      startTime: c.startTime,
      invocation: c.invocation || "",
    })),
    runs: (st.runs || []).map(mapRun),
    workspaces: ((pool && pool.workspaces) || []).map((w) => ({
      root: w.root,
      hits: w.cache ? Number(w.cache.hits) : undefined,
      misses: w.cache ? Number(w.cache.misses) : undefined,
      errors: w.cache ? Number(w.cache.errors) : undefined,
      lastAccessTime: w.lastAccessTime,
      secretProvider: w.secretProvider || undefined,
    })),
    services: (st.services || []).map((sv) => ({
      id: sv.id || "",
      label: sv.label || "",
      command: sv.command || "",
      ports: sv.ports || [],
      state: sv.state || "",
      dependents: sv.dependents || 0,
      startedAt: sv.startTime,
    })),
    locks: (st.locks || []).map((l) => ({
      project: l.project || "",
      pid: l.pid || 0,
      command: l.command || "",
      dir: l.dir || "",
      acquireTime: l.acquireTime,
      staleAfterSeconds: l.staleAfterSeconds || 0,
      waiters: (l.waiters || []).map((w) => ({
        pid: w.pid || 0,
        command: w.command || "",
        waitTime: w.waitTime,
      })),
    })),
    magusVersion: st.build?.version || "",
    daemonVersion: (pool && pool.daemonVersion) || "",
  };
}

// ---- metrics view-model (ConnectRPC Snapshot) ------------------------------

export interface LatView {
  count: number;
  p50: number;
  p95: number;
  p99: number;
  max: number;
}

export const LAT_KEYS = ["target", "cache", "poolWait", "graphQuery"] as const;
export type LatKey = (typeof LAT_KEYS)[number];
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
  hits: number;
  misses: number;
  errors: number;
  hitRate: number | null;
  durationP50: number;
  durationP95: number;
  ioCount: number;
  bytesTotal: number | bigint;
}

function mapRemote(r: Remote | undefined): RemoteView | null {
  if (!r) return null;
  const hits = Number(r.hits),
    misses = Number(r.misses);
  const total = hits + misses;
  return {
    hits,
    misses,
    errors: Number(r.errors),
    hitRate: total > 0 ? hits / total : null,
    durationP50: r.durationP50,
    durationP95: r.durationP95,
    ioCount: Number(r.ioCount),
    bytesTotal: r.transferredBytes,
  };
}

export interface TargetStatView {
  project: string;
  target: string;
  spell: string;
  count: number;
  p50: number;
  p95: number;
  p99: number;
  cacheHitRate: number;
  success: number;
  errors: number;
}

function mapTargetStat(t: TargetStat): TargetStatView {
  return {
    project: t.project,
    target: t.target,
    spell: t.spell,
    count: Number(t.count),
    p50: t.p50,
    p95: t.p95,
    p99: t.p99,
    cacheHitRate: t.cacheHitRate,
    success: Number(t.success),
    errors: Number(t.errors),
  };
}

export interface McpToolView {
  tool: string;
  calls: number;
  errors: number;
  inputP50: number;
  inputP95: number;
  inputTotal: number | bigint;
  outputP50: number;
  outputP95: number;
  outputTotal: number | bigint;
  durationP50: number;
  durationP95: number;
}

function mapMcpTool(m: MCPToolStat): McpToolView {
  return {
    tool: m.tool,
    calls: Number(m.calls),
    errors: Number(m.errors),
    inputP50: m.inputP50,
    inputP95: m.inputP95,
    inputTotal: m.inputTotal,
    outputP50: m.outputP50,
    outputP95: m.outputP95,
    outputTotal: m.outputTotal,
    durationP50: m.durationP50,
    durationP95: m.durationP95,
  };
}

export interface BuzzView {
  execCount: number;
  execP50: number;
  execP95: number;
  compileCount: number;
  compileP50: number;
  compileP95: number;
  hostCallCount: number;
  hostCallP50: number;
  hostCallP95: number;
  sessionPoolReuse: number;
  sessionPoolIdle: number;
  sessionPoolEvictions: number;
  sessionWarmP50: number;
  sessionWarmP95: number;
  importCount: number;
  importP50: number;
  importP95: number;
  spellResolveCount: number;
  spellResolveP50: number;
  spellResolveP95: number;
  jitRuns: number;
  vmFaults: number;
}

function mapBuzz(b: Buzz | undefined): BuzzView | null {
  if (!b) return null;
  return {
    execCount: Number(b.execCount),
    execP50: b.execP50,
    execP95: b.execP95,
    compileCount: Number(b.compileCount),
    compileP50: b.compileP50,
    compileP95: b.compileP95,
    hostCallCount: Number(b.hostCallCount),
    hostCallP50: b.hostCallP50,
    hostCallP95: b.hostCallP95,
    sessionPoolReuse: Number(b.sessionPoolReuse),
    sessionPoolIdle: Number(b.sessionPoolIdle),
    sessionPoolEvictions: Number(b.sessionPoolEvictions),
    sessionWarmP50: b.sessionWarmP50,
    sessionWarmP95: b.sessionWarmP95,
    importCount: Number(b.importCount),
    importP50: b.importP50,
    importP95: b.importP95,
    spellResolveCount: Number(b.spellResolveCount),
    spellResolveP50: b.spellResolveP50,
    spellResolveP95: b.spellResolveP95,
    jitRuns: Number(b.jitRuns),
    vmFaults: Number(b.vmFaults),
  };
}

export interface SandboxView {
  applyP50: number;
  applyP95: number;
  rulesRead: number;
  rulesWrite: number;
  rulesExec: number;
  envRules: number;
  checksAllow: number;
  checksDeny: number;
  envDropped: number;
}

function mapSandbox(s: Sandbox | undefined): SandboxView | null {
  if (!s) return null;
  return {
    applyP50: s.applyP50,
    applyP95: s.applyP95,
    rulesRead: Number(s.rulesRead),
    rulesWrite: Number(s.rulesWrite),
    rulesExec: Number(s.rulesExec),
    envRules: Number(s.envRules),
    checksAllow: Number(s.checksAllow),
    checksDeny: Number(s.checksDeny),
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
    capturedMs: tsMillisOrNow(snap.captureTime),
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

// A null field is UNMEASURED: the daemon's pool read or metric collection failed on that
// tick. It is not zero, and a renderer must not draw it as one - an idle-looking square and
// a square nobody measured are different facts. The live status feed always measures, so
// nulls only ever arrive on the backfill.
export interface SampleView {
  at: number; // ms
  running: number | null;
  capacity: number | null; // 0 = unlimited, null = unmeasured
  queued: number | null;
  cacheHits: number | null;
  cacheMisses: number | null;
  cacheSrc: CacheSrc; // baseline source of cacheHits/cacheMisses
}

export function mapSample(s: ProtoSample): SampleView {
  return {
    at: tsMillisOrNow(s.sampleTime),
    running: s.running ?? null,
    capacity: s.capacity ?? null,
    queued: s.queued ?? null,
    cacheHits: s.cacheHits === undefined ? null : Number(s.cacheHits),
    cacheMisses: s.cacheMisses === undefined ? null : Number(s.cacheMisses),
    cacheSrc: "metrics",
  };
}

// ---- insight view-model (magus.insight.v1alpha1) ---------------------------------
// InsightService.GetInsight returns the five lenses as a generated proto message, so
// this axis has no hand-written wire shape: mapInsight folds magus.insight.v1alpha1.Insight
// into the camelCase view-models the tiles read. Times are google.protobuf.Timestamp
// with a zero time.Time mapped to UNSET at the server boundary, so an absent time is
// simply an absent field rather than the year-0001 sentinel the JSON route emitted.

// fmtDate renders a wire Timestamp as a local date, blank when unset.
export function fmtDate(ts: Timestamp | undefined): string {
  if (!ts) return "-";
  return new Date(tsMillisOrNow(ts)).toLocaleDateString();
}

export interface HotspotNodeView {
  name: string;
  churn: number;
  authors: number;
  blastRadius: number;
  lastCommit: string;
}
// FileHotspotView is the per-FILE ranking, the granularity a reader can act on: a project
// tells you which subtree is hot, a file tells you what to open. `moves` is how many times
// the file changed path in the window - churn the score cannot see, because a file being
// moved around is a different kind of thrash from its contents being rewritten.
export interface FileHotspotView {
  path: string;
  commits: number;
  complexity: number;
  score: number;
  authors: number;
  moves: number;
  lastCommit: string;
}
export interface AffinityPairView {
  a: string;
  b: string;
  count: number;
  hidden: boolean;
}
export interface OwnershipRowView {
  path: string;
  primary: string;
  primaryShare: number;
  authors: number;
  busFactor1: boolean;
  stale: boolean;
}
export interface TrendRowView {
  path: string;
  delta: number;
  recent: number;
  earlier: number;
}
export interface VolatilityRowView {
  label: string;
  score: number;
  volatile: boolean;
  pass: number;
  fail: number;
  volatileCount: number;
  samples: number;
  lastPass: string;
}
export interface VolatilityView {
  threshold: number;
  targets: VolatilityRowView[];
}

// ---- toolchain view-model (magus.tool.v1alpha1) ----------------------------------
//
// One row per binary a project's spells drive. The three windows stay separate all the
// way to the table because the first question about a failing bound is who set it - the
// spell (what its ops need to run at all) or this project (what it has qualified) - and
// the CLI diagnostic cannot say, since the intersection discards provenance.

export interface ToolRowView {
  project: string;
  bin: string;
  spell: string;
  installed: string; // "" when the tool is absent or printed nothing version-shaped
  spellWindow: string; // rendered window, "" when unconstrained
  workspaceWindow: string;
  effectiveWindow: string;
  verdict: "inside" | "too old" | "too new" | "unknown";
  code: string; // MGS3005 / MGS3006, "" when satisfied
  probedAtMs: number; // 0 when never probed
}

export interface ToolsView {
  rows: ToolRowView[];
  violations: number;
}

// renderWindow turns a wire window into the notation the docs use. `below` is the first
// version REJECTED, so it renders as "< x" and never as a max.
export const renderWindow = (b: { min: string; below: string } | undefined): string => {
  if (!b) return "";
  const parts: string[] = [];
  if (b.min) parts.push(">= " + b.min);
  if (b.below) parts.push("< " + b.below);
  return parts.join(", ");
};

export interface InsightView {
  commits: number; // the git-history window shared by the four VCS lenses
  hotspots: HotspotNodeView[];
  hotspotFiles: FileHotspotView[];
  affinity: AffinityPairView[];
  ownership: OwnershipRowView[];
  trend: TrendRowView[];
  volatility: VolatilityView | null;
}

export function mapInsight(w: Insight): InsightView {
  return {
    commits: w.hotspots?.commits ?? 0,
    hotspots: (w.hotspots?.nodes ?? []).map((n) => ({
      name: n.path,
      churn: n.churn,
      authors: n.authors,
      blastRadius: n.blastRadius,
      lastCommit: fmtDate(n.lastCommitTime),
    })),
    hotspotFiles: (w.hotspots?.files ?? []).map((f) => ({
      path: f.path,
      commits: f.commits,
      complexity: f.complexity,
      score: f.score,
      authors: f.authors,
      moves: f.moves,
      lastCommit: fmtDate(f.lastCommitTime),
    })),
    affinity: (w.affinity?.pairs ?? []).map((p) => ({
      a: p.a,
      b: p.b,
      count: p.count,
      hidden: p.hidden,
    })),
    ownership: (w.ownership?.projects ?? []).map((o) => ({
      path: o.path,
      primary: o.primary || "-",
      primaryShare: o.primaryShare,
      authors: o.authors,
      busFactor1: o.busFactor1,
      stale: o.stale,
    })),
    trend: (w.trend?.projects ?? []).map((t) => ({
      path: t.path,
      delta: t.delta,
      recent: t.recent,
      earlier: t.earlier,
    })),
    volatility: w.volatility
      ? {
          threshold: w.volatility.threshold,
          targets: w.volatility.targets.map((v) => ({
            label: v.project ? v.project + ":" + v.target : v.target,
            score: v.score,
            volatile: v.volatile,
            pass: v.pass,
            fail: v.fail,
            volatileCount: v.volatileCount,
            samples: v.samples,
            lastPass: fmtDate(v.lastPassTime),
          })),
        }
      : null,
  };
}

// ---- agent activity view-model ---------------------------------------------
// Derived from the activity trail (magus.activity.v1alpha1), which records one event per agent tool
// invocation a guard hook observed, plus one per MCP tool call.
//
// WHAT THIS CAN AND CANNOT SAY. A hook fires BEFORE a tool call and there is no matching end
// signal - no session-open, no session-close, nothing that says an agent stopped. So the trail can
// answer "which agents did something recently" and cannot answer "how many agents are running now".
// Those differ: an agent that is thinking, or waiting on a long build, has an open session and no
// recent events, and would be invisible to any "currently running" count derived from this.
//
// The view therefore reports ACTIVE IN A WINDOW and is named for it, rather than presenting a
// recency count as a concurrency one. Getting that wrong would be the kind of number that looks
// authoritative and is quietly false.

export const AGENT_WINDOW_MS = 5 * 60 * 1000;

// One agent host's slice of the window. `sessions` counts distinct session ids; a host whose wrapper
// does not supply one contributes a single "unattributed" bucket rather than inflating the count.
export interface AgentHostView {
  host: string;
  sessions: number;
  calls: number;
  denied: number;
  advised: number;
  lastMs: number;
}

// One observed agent call, kept so the tile can show WHAT agents actually did rather than only how
// much. The counts answer "is anything happening"; this answers "what", which is the question a
// denial or a spike immediately raises and which aggregates structurally cannot answer.
export interface AgentCallView {
  atMs: number;
  host: string;
  tool: string; // the host tool name: shell.command, file.write, or an MCP tool
  decision: "deny" | "advise" | "pass" | "";
  mcp: boolean;
}

export interface AgentActivityView {
  windowMs: number;
  hosts: AgentHostView[]; // busiest first
  totalCalls: number;
  totalSessions: number;
  denied: number;
  mcpCalls: number;
  // The most recent calls, newest first, capped. Denied and advised calls are kept in preference to
  // passes when the cap bites: a board with room for ten lines should spend them on the ten that
  // might need a human, not the ten that happened to be most recent.
  recent: AgentCallView[];
}

// How many recent calls the view retains. Enough to fill the tile at Big Picture scale without
// keeping an unbounded slice of a busy daemon's trail in the store.
const RECENT_CAP = 12;

// The wire shape the poll hands over: the fields of magus.activity.v1alpha1.ActivityEvent this reads,
// already decoded. Kept structural so state.ts stays free of the generated enums.
export interface AgentEventWire {
  atMs: number;
  isAgentCommand: boolean;
  isMcpCall: boolean;
  host: string;
  session: string;
  preview: string;
  // The event's `action`: the host tool name for an agent command (shell.command, file.write) or
  // the tool name for an MCP call. What the call actually WAS, as opposed to how it was judged.
  action: string;
}

// guardDecision reads the verdict off the event LINE rather than fetching its response blob.
//
// trail.AppendAgentCommand writes preview as "guard: deny" / "guard: advise" / "guard: pass", so
// the decision is already in the listing. Fetching it properly would mean one GetPayload round trip
// per row, which for a 200-event window is 200 requests to render one tile.
function guardDecision(preview: string): "deny" | "advise" | "pass" | "" {
  const m = /^guard:\s*(deny|advise|pass)$/.exec(preview.trim());
  return m ? (m[1] as "deny" | "advise" | "pass") : "";
}

export function mapAgentActivity(events: AgentEventWire[], now: number): AgentActivityView {
  const cutoff = now - AGENT_WINDOW_MS;
  const byHost = new Map<string, { sessions: Set<string>; view: AgentHostView }>();
  let totalCalls = 0;
  let denied = 0;
  let mcpCalls = 0;
  const allSessions = new Set<string>();
  const recent: AgentCallView[] = [];

  for (const e of events) {
    if (e.atMs < cutoff) continue;
    if (e.isMcpCall) {
      mcpCalls++;
      recent.push({ atMs: e.atMs, host: "mcp", tool: e.action, decision: "", mcp: true });
    }
    if (!e.isAgentCommand) continue;
    // An unattributed event still counts as work done - it is only its ATTRIBUTION that is missing,
    // and dropping it would undercount the very traffic the tile exists to show. Wrappers on an
    // older magus produce these (the --agent-name flag postdates the current release).
    const host = e.host || "unattributed";
    let slot = byHost.get(host);
    if (!slot) {
      slot = {
        sessions: new Set<string>(),
        view: { host, sessions: 0, calls: 0, denied: 0, advised: 0, lastMs: 0 },
      };
      byHost.set(host, slot);
    }
    // Sessions are counted per HOST but also globally, and the two are not summable: one operator
    // running two hosts is two sessions, so the global figure is a sum of distinct ids, not of the
    // per-host counts, which could double-count a shared id.
    const session = e.session || host + ":unattributed";
    slot.sessions.add(session);
    allSessions.add(session);
    slot.view.calls++;
    totalCalls++;
    const decision = guardDecision(e.preview);
    if (decision === "deny") {
      slot.view.denied++;
      denied++;
    } else if (decision === "advise") {
      slot.view.advised++;
    }
    if (e.atMs > slot.view.lastMs) slot.view.lastMs = e.atMs;
    recent.push({ atMs: e.atMs, host, tool: e.action, decision, mcp: false });
  }

  // Newest first, then denials and advisories promoted ahead of passes. The cap is small enough
  // that a busy daemon would otherwise fill it entirely with routine passes and bury the one denial
  // in the window - which is the single entry the tile exists to surface.
  recent.sort((a, b) => b.atMs - a.atMs);
  const rank = (d: string): number => (d === "deny" ? 0 : d === "advise" ? 1 : 2);
  const kept = [...recent].sort((a, b) => rank(a.decision) - rank(b.decision)).slice(0, RECENT_CAP);
  kept.sort((a, b) => b.atMs - a.atMs);

  const hosts = [...byHost.values()].map((s) => ({ ...s.view, sessions: s.sessions.size }));
  hosts.sort((a, b) => b.calls - a.calls || a.host.localeCompare(b.host));
  return {
    windowMs: AGENT_WINDOW_MS,
    hosts,
    totalCalls,
    totalSessions: allSessions.size,
    denied,
    mcpCalls,
    recent: kept,
  };
}

// ---- the store shape -------------------------------------------------------
// One value published on every tick. Slices are filled independently: `status`
// arrives on the SSE frame, `metrics`/`samples` on the Connect stream, `insight`
// on a polled Connect unary read. Tiles read only the slice they render.
// `liveHost` deep-links running calls into live logs.

export interface DashboardState {
  conn: ConnView;
  liveHost: string | null;
  status: StatusView | null;
  metrics: MetricsView | null;
  samples: SampleView[];
  insight: InsightView | null;
  tools: ToolsView | null;
  // Agent traffic seen in the recent window (mapAgentActivity). null until the activity poll has
  // produced a frame, or when the daemon serves no trail.
  agents: AgentActivityView | null;
  // logLines is a rolling buffer of raw captured-output lines for the live-activity
  // preview. Only the demo feed (demo.ts) synthesizes it; live mode leaves it empty,
  // because the daemon's status SSE carries pool/health frames, not a raw-output
  // journal - a real live tail would need a journal SSE consumer (see activity.ts).
  logLines: string[];
  // config is the daemon's resolved read-only configuration (default charms, concurrency cap,
  // sandbox), read once from the JSON status endpoint alongside observingSince. null until known.
  config: ConfigView | null;
  // observingSince is when the daemon began collecting the telemetry/cache counters (epoch ms),
  // read once from the JSON status endpoint (it is static per session and not on the proto event
  // stream). null until known. Surfaced so the board can be transparent that the numbers are
  // cumulative since then and are NOT persisted across daemon restarts. The demo synthesizes one.
  observingSince: number | null;
}

export function initialState(): DashboardState {
  return {
    conn: { state: "none" },
    liveHost: null,
    status: null,
    metrics: null,
    samples: [],
    insight: null,
    tools: null,
    agents: null,
    logLines: [],
    observingSince: null,
    config: null,
  };
}
