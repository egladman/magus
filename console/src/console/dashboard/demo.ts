// demo.ts - a daemon-free showcase mode for the dashboard.
//
// The dashboard normally streams a live magus daemon (transport.ts). This module is
// the "show it off with nothing running" path: it never opens a socket, never talks
// protobuf, and never touches the daemon module. It just SYNTHESIZES the same plain
// view-model the tiles already read (DashboardState in state.ts) and pushes it into
// the store on a ~1s tick, so every tile renders as if a busy daemon were attached.
//
// Because the tile <-> store boundary is pure view-model (no wire types below it),
// the fixture here is a normal typed object: `pnpm run typecheck` verifies it against
// the same interfaces the real mappers produce, so it can't silently drift when a
// tile gains a field. Entered from main.ts on a `#demo` fragment or the empty-state
// "See a demo" button.

import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import type { Store } from "../../lib/store";
import type {
  DashboardState,
  StatusView,
  MetricsView,
  SampleView,
  InsightView,
  RunView,
  TargetRunView,
  TargetState,
  AgentActivityView,
  AgentHostView,
  AgentCallView,
} from "./state";
import { AGENT_WINDOW_MS } from "./state";
import { SCENARIO_CATALOG, WORKSPACE_ROOT, scenarioInsight } from "../demo-scenario";

const CAPACITY = 8;
const TICK_MS = 1000;
// Cadence for the streaming log preview. 500ms pushing 1-3 lines was 2-6 lines a second, which
// reads as live but is faster than anyone can actually follow: a line was gone before the eye
// reached it, and on a wall display the tail was pure motion with no readable content. At ~1400ms
// and 1-2 lines it is a little under a line a second, which is a pace you can read while the
// content still visibly moves. The tile is a preview, not a firehose - the log viewer is where
// someone goes to keep up with everything.
const LOG_TICK_MS = 1400;
const LOG_MAX = 200; // rolling captured-output buffer kept for the activity preview
const HISTORY = 200; // seed samples (~a GitHub-year strip fills fast at ~1/s)

// A rotating catalog of captured-output lines the demo activity preview streams. They are what the
// REAL tools print - go test package lines, a golangci-lint finding, eslint's stylish report, tsc
// with file:line:col, buildkit layer progress, a vite summary - over the shared scenario's acme
// monorepo (services/*, apps/*, libs/*), so the streaming tail reads as the same workspace the tiles
// above describe. Cycled with a touch of jitter so the tail churns without looking scripted. Plain
// ASCII, no real machine data.
//
// The order is a sweep in progress rather than a shuffle: scope, then the cache decisions, then the
// tools talking, then the summary. Read top to bottom it is one `magus affected ci`, which is what
// the tile is a preview OF.
//
// A few lines carry what real captured output carries, because the preview renders through the same
// renderLine() the log viewer uses (tiles/activity.ts) and the demo is where that is seen:
//   - a leading [pass]/[fail]/[warn]/[info]/[summary] token, which magus itself emits and the
//     renderer promotes to a colored badge (a tool's own brackets, like buildkit's "#12" step
//     lines, are NOT status tokens and stay plain, which is right)
//
// A CACHE HIT IS "[pass] ... (cached, 95ms)", never "[cached] ...".
//
// An earlier revision of this list invented both a "[cached]" head token and "[cache] hit/miss"
// chatter lines. magus emits neither. internal/cache/log.go prints the outcome word and the cache
// state in separate places on purpose - "[pass] api (cached, 42ms)" - because whether a target
// passed and whether it had to run are orthogonal, which is the same split Bazel makes. The demo
// showing them fused was worse than cosmetic: this is the only place most people ever see magus
// output rendered, so a reader learned a log format the tool does not have.
//   - real ANSI SGR escapes, on exactly the lines whose tool actually colours them (golangci-lint,
//     eslint, buildkit, vite), which the renderer maps onto the theme-aware --console-ansi-* slots
// Without them the showcase would render the whole pipeline and look identical to the flat
// monospace dump it replaced.
const LOG_SNIPPETS: string[] = [
  "projects: services/identity, apps/dashboard, libs/authkit, services/ledger, . (affected)",
  "charms: rw",
  "[pass] libs/authkit build (cached, 95ms)",
  "ok  \tgithub.com/acme/acme/services/identity/internal/session\t0.188s",
  "ok  \tgithub.com/acme/acme/services/identity/internal/token\t0.436s",
  "[pass] services/identity test (ran, 3.9s)",
  "out7f31c2",
  "\u001b[31mservices/catalog/index.go:88:2\u001b[0m: field mu is unused \u001b[2m(unused)\u001b[0m",
  "[warn] services/catalog lint (ran, 1.2s) - 1 issue",
  "apps/dashboard/src/api/session.ts(42,18): error TS2339: Property 'audience' does not exist on type 'SessionClaims'.",
  "[fail] apps/dashboard typecheck (ran, 1.9s)",
  "/Users/eli/Repos/acme/apps/admin/src/routes/billing.tsx",
  "  42:7  \u001b[33mwarning\u001b[0m  'invoiceId' is assigned a value but never used  @typescript-eslint/no-unused-vars",
  "\u001b[33m1 problem (0 errors, 1 warning)\u001b[0m",
  "vite v6.0.7 building for production...",
  "\u001b[36mdist/assets/admin-3b71c0d9.js\u001b[0m   318.42 kB | gzip: 96.10 kB",
  "#12 [builder 4/7] RUN go build -trimpath -o /out/ledgerd ./cmd/ledgerd",
  "#12 \u001b[32mDONE\u001b[0m 6.4s",
  "#17 exporting layers 1.8s done",
  "#17 writing image sha256:9c1f4b2ea0d7 done",
  " Test Files  14 passed (14)",
  "      Tests  212 passed (212)",
  "[sandbox] apply rules=read:512 write:88 exec:12",
  "[info] \u001b[36mrestored\u001b[0m 3 shared services from the warm pool",
  "[summary] 9 cached, 4 ran, 0 failed in 12.6s",
];

export interface DemoHandle {
  stop(): void;
}

// A deterministic-enough jitter without importing anything: a tiny LCG seeded from a
// counter so the walk looks organic across reloads without Math.random's noise.
function makeRng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 0x1_0000_0000;
  };
}

// The dashboard is a LIVE board, so its gantt keeps churning past the scenario's fixed history; it
// draws that forward motion from the shared scenario catalog, so even the synthesized live run stays
// inside the same acme monorepo and target vocabulary the rest of the console shows.
const CATALOG = SCENARIO_CATALOG;

const TRIGGERS = ["cli", "watch", "ci"];

export function startDemo(store: Store<DashboardState>): DemoHandle {
  const rng = makeRng(0x9e3779b9);
  const t0 = Date.now();

  // ---- evolving pool + cache counters -------------------------------------
  // running/queued random-walk ONLY to make the utilization HISTORY chart breathe; the attention
  // hero's live RUNNING/QUEUED counts are derived from the actual runs in buildStatus (a local const
  // shadows these) so the hero always matches the Live activity list exactly.
  let running = 4;
  let queued = 0;
  let hits = 4213;
  let misses = 587;
  let errors = 2;
  let sizeBytes = 742 * 1024 * 1024;

  // ---- gantt run scheduler ------------------------------------------------
  // A small stateful generator: each run accumulates targets; the trailing pair is
  // always [running, queued]. When the running one finishes, the queued promotes and
  // a fresh queued is appended; a run caps at a few targets, then a new run starts.
  let catIdx = 0;
  const nextCat = () => CATALOG[catIdx++ % CATALOG.length];
  const ref = (n: number): string => "out" + (0x1a2b3c + n * 0x111).toString(16).slice(0, 6);
  const invId = (n: number): string => (0xa1b2c3d4e5f6 + n * 0x1357).toString(16).slice(0, 12);

  let refN = 0;
  let invN = 0;
  const runs: RunView[] = [];

  // planned holds each target's intended duration and whether it is allowed to fail. It is a side
  // map rather than extra fields on TargetRunView because that type is the WIRE-derived view model
  // the tiles read - the demo must not invent fields on it that a real daemon never sends, or a tile
  // could quietly come to depend on something only the showcase provides.
  const planned = new WeakMap<TargetRunView, { durMs: number; flaky: boolean }>();

  function newTarget(
    state: TargetState,
    startMs: number | null,
    endMs: number | null,
  ): TargetRunView {
    const c = nextCat();
    const durationMs = endMs != null && startMs != null ? endMs - startMs : 0;
    const t: TargetRunView = {
      project: c.project,
      target: c.target,
      label: c.project ? c.project + ":" + c.target : c.target,
      state,
      terminal: state === "passed" || state === "failed" || state === "cached",
      startMs,
      endMs,
      outputRef: state === "passed" || state === "cached" || state === "failed" ? ref(refN++) : "",
      durationMs,
    };
    // Jitter so two runs of the same target are not identical to the pixel, which is what made the
    // old timeline read as a repeating pattern rather than as work.
    planned.set(t, { durMs: c.durMs * (0.75 + rng() * 0.5), flaky: !!c.flaky });
    return t;
  }

  // A run is a WAVE, not a queue of one. `magus affected ci` on a monorepo fans out across the
  // pool: several targets start together, the short ones finish underneath the long poles, and the
  // queue drains behind them. The old scheduler ran exactly one target at a time, which meant the
  // pool grid showed one lit cube out of eight, the timeline showed one bar, and the whole
  // concurrency story the dashboard exists to tell was invisible in the showcase.
  const RUN_WIDTH = 4; // targets fanned out per invocation
  function startRun(now: number): RunView {
    const run: RunView = {
      inv: invId(invN++),
      trigger: TRIGGERS[invN % TRIGGERS.length],
      targets: [newTarget("running", now, null)],
    };
    for (let i = 0; i < RUN_WIDTH; i++) run.targets.push(newTarget("queued", null, null));
    runs.push(run);
    return run;
  }

  // countRunning is over ALL runs, not one: the pool is daemon-wide, so two invocations in flight
  // share its slots and the board has to reflect that.
  function countRunning(): number {
    let n = 0;
    for (const r of runs) for (const t of r.targets) if (t.state === "running") n++;
    return n;
  }

  // Seed a couple of finished runs behind "now" so the timeline isn't empty on frame 1.
  (function seedRuns(): void {
    const past = startRun(t0 - 46_000);
    for (const t of past.targets) {
      t.state = "passed";
      t.terminal = true;
    }
    past.targets[0].endMs = t0 - 42_000;
    past.targets[0].startMs = t0 - 46_000;
    past.targets[0].durationMs = 4000;
    past.targets[1] = newTarget("cached", t0 - 42_000, t0 - 41_800);
    past.targets.push(newTarget("passed", t0 - 41_000, t0 - 35_500));
    startRun(t0 - 9_000); // the live run: [running, queued]
  })();

  // The concurrency the wave is currently aiming for. It BREATHES between a nearly-idle pool and a
  // nearly-saturated one rather than sitting at a constant, because the pool grid, the utilization
  // chart and the queued count are all readouts of exactly this number - held constant they are
  // three different ways of drawing a straight line, and none of them demonstrates anything.
  let aim = 5;
  let aimUntil = t0 + 20_000;
  function retarget(now: number): void {
    if (now < aimUntil) return;
    // Weighted toward busy: an idle board is the least interesting thing to look at, but a board
    // that is ALWAYS saturated never shows the queue draining.
    const roll = rng();
    aim = roll < 0.25 ? 2 : roll < 0.75 ? 5 : 7;
    aimUntil = now + 15_000 + rng() * 25_000;
  }

  function advanceRuns(now: number): void {
    retarget(now);

    // 1. Finish anything whose planned duration has elapsed, across every run.
    for (const run of runs) {
      for (const t of run.targets) {
        if (t.state !== "running" || t.startMs == null) continue;
        const p = planned.get(t);
        if (!p || now - t.startMs < p.durMs) continue;
        // Only the targets the catalog marks flaky are allowed to fail, so the same handful of
        // suites break the way they do in a real repo - and the failing deep-link, the volatility
        // lens and the run history stay consistent with one another.
        const failed = p.flaky && rng() < 0.22;
        t.state = failed ? "failed" : rng() < 0.28 ? "cached" : "passed";
        t.terminal = true;
        t.endMs = now;
        t.durationMs = now - t.startMs;
        t.outputRef = ref(refN++);
      }
    }

    // 2. Promote queued targets until the pool is at its aim. Oldest run first, so a wave drains in
    // the order it was submitted rather than the newest invocation starving the others.
    for (const run of runs) {
      for (const t of run.targets) {
        if (countRunning() >= aim) break;
        if (t.state !== "queued") continue;
        t.state = "running";
        t.startMs = now;
      }
    }

    // 3. Keep work arriving. A new invocation starts when the pool has room and nothing is queued
    // behind it, or occasionally alongside an existing one - two concurrent invocations is the
    // case the board is least able to show and the one an operator most often actually has.
    const queuedLeft = runs.some((r) => r.targets.some((t) => t.state === "queued"));
    if (countRunning() < aim && (!queuedLeft || rng() < 0.05)) startRun(now);

    // Trim runs whose every target ended before the visible window (keep memory flat). Deeper than
    // the old cap of 4: a wave is several runs wide now, and trimming at 4 would drop invocations
    // that are still on screen in the timeline.
    while (runs.length > 8) runs.shift();
  }

  // ---- agent activity ------------------------------------------------------
  // Synthesized to look like what the trail actually produces: a couple of hosts, a handful of
  // sessions between them, mostly-passing guard verdicts with the occasional advise and a rare
  // deny. It matters that the demo shows this at all - the agent tile hides itself when there is no
  // traffic, so without a synthesized frame the showcase would simply never render the feature.
  //
  // Counters climb slowly and monotonically; sessions stay put. A demo where the session count
  // jitters every second would suggest agents connecting and dropping constantly, which is the
  // opposite of the "these are long-lived, this is a recency window" point the tile makes.
  const agentStart = t0;
  function buildAgents(now: number): AgentActivityView {
    const mins = Math.max(0, (now - agentStart) / 60000);
    const claudeCalls = 34 + Math.floor(mins * 7);
    const codexCalls = 11 + Math.floor(mins * 2);
    const hosts: AgentHostView[] = [
      {
        host: "claude-code",
        sessions: 2,
        calls: claudeCalls,
        denied: 1,
        advised: 3,
        lastMs: now - 4000,
      },
      {
        host: "codex",
        sessions: 1,
        calls: codexCalls,
        denied: 0,
        advised: 1,
        // Deliberately cool: past the tile's RECENT_MS, so the showcase demonstrates a seat that
        // has gone quiet rather than only ever showing every seat lit.
        lastMs: now - 95_000,
      },
    ];
    // The recent calls are the SAME session the activity trail records (demo-scenario.ts): claude-code
    // is the host that ran services/identity:test and is still working, so the tools it calls here are
    // the tools that appear there - magus_run_target, magus_query - and the guard verdicts around them
    // are the ones that story implies. The deny is a raw `go test` the guard turned back because the
    // run belongs to magus; the advise is the edit to libs/authkit that started all of this. Those two
    // rows are what the tile exists to make findable, so the showcase must contain both.
    const recent: AgentCallView[] = [
      { atMs: now - 4000, host: "mcp", tool: "magus_run_target", decision: "", mcp: true },
      {
        atMs: now - 12_000,
        host: "claude-code",
        tool: "file.write",
        decision: "advise",
        mcp: false,
      },
      { atMs: now - 26_000, host: "mcp", tool: "magus_query", decision: "", mcp: true },
      {
        atMs: now - 41_000,
        host: "claude-code",
        tool: "shell.command",
        decision: "deny",
        mcp: false,
      },
      { atMs: now - 95_000, host: "codex", tool: "shell.command", decision: "pass", mcp: false },
    ];
    return {
      windowMs: AGENT_WINDOW_MS,
      hosts,
      recent,
      totalCalls: claudeCalls + codexCalls,
      totalSessions: 3,
      denied: 1,
      mcpCalls: 18 + Math.floor(mins * 3),
    };
  }

  // ---- static-ish metrics + insight ---------------------------------------
  function buildMetrics(now: number, j: number): MetricsView {
    return {
      capturedMs: now,
      latency: {
        target: { count: 1284 + j, p50: 0.412, p95: 1.87, p99: 3.44, max: 8.9 },
        cache: { count: 4931 + j * 3, p50: 0.0019, p95: 0.0071, p99: 0.019, max: 0.14 },
        poolWait: { count: 1284 + j, p50: 0.004, p95: 0.061, p99: 0.22, max: 1.9 },
        graphQuery: { count: 212 + (j >> 2), p50: 0.031, p95: 0.11, p99: 0.28, max: 0.61 },
      },
      remote: {
        hits: 1873,
        misses: 402,
        errors: 1,
        hitRate: 1873 / (1873 + 402),
        durationP50: 0.043,
        durationP95: 0.21,
        ioCount: 2276,
        bytesTotal: 5_912_334_221,
      },
      // Per-target aggregates over the acme monorepo the scenario describes, and the ops that
      // produced them (go-test, tsc, golangci-lint - the real op names, not invented ones).
      // services/identity:test carries the errors and the lowest cache-hit rate, because it is the
      // target that flapped; apps/dashboard:typecheck carries the single error the timeline shows at
      // 84m; the cached-heavy build targets sit high. So the p50s here agree with the durations in
      // SCENARIO_CATALOG and the outcomes agree with the run history and the volatility lens.
      targetStats: [
        {
          project: "services/identity",
          target: "test",
          spell: "go-test",
          count: 188,
          p50: 3.6,
          p95: 5.4,
          p99: 7.1,
          cacheHitRate: 0.41,
          success: 184,
          errors: 4,
        },
        {
          project: "services/identity",
          target: "build",
          spell: "go-build",
          count: 342,
          p50: 0.42,
          p95: 1.1,
          p99: 2.0,
          cacheHitRate: 0.88,
          success: 342,
          errors: 0,
        },
        {
          project: "apps/dashboard",
          target: "build",
          spell: "vite",
          count: 96,
          p50: 0.83,
          p95: 1.9,
          p99: 2.7,
          cacheHitRate: 0.72,
          success: 96,
          errors: 0,
        },
        {
          project: "apps/dashboard",
          target: "typecheck",
          spell: "tsc",
          count: 74,
          p50: 2.3,
          p95: 3.4,
          p99: 4.6,
          cacheHitRate: 0.55,
          success: 73,
          errors: 1,
        },
        {
          project: "libs/authkit",
          target: "lint",
          spell: "golangci-lint",
          count: 129,
          p50: 0.6,
          p95: 1.2,
          p99: 2.1,
          cacheHitRate: 0.61,
          success: 129,
          errors: 0,
        },
      ],
      // The MCP tools the scenario's agent actually called (magus_run_target, magus_query,
      // magus_output), so the metrics tile names the same tools the activity trail records - including
      // the one magus_output error (the pruned-ref lookup).
      mcpTools: [
        {
          tool: "magus_run_target",
          calls: 63,
          errors: 1,
          inputP50: 205,
          inputP95: 320,
          inputTotal: 13_104,
          outputP50: 4_120,
          outputP95: 9_800,
          outputTotal: 271_402,
          durationP50: 2.1,
          durationP95: 4.4,
        },
        {
          tool: "magus_query",
          calls: 214,
          errors: 0,
          inputP50: 84,
          inputP95: 190,
          inputTotal: 19_006,
          outputP50: 1_640,
          outputP95: 6_210,
          outputTotal: 402_118,
          durationP50: 0.034,
          durationP95: 0.09,
        },
        {
          tool: "magus_output",
          calls: 41,
          errors: 1,
          inputP50: 44,
          inputP95: 60,
          inputTotal: 1_920,
          outputP50: 3_200,
          outputP95: 9_100,
          outputTotal: 158_400,
          durationP50: 0.008,
          durationP95: 0.02,
        },
      ],
      buzz: {
        execCount: 8123 + j * 2,
        execP50: 0.0007,
        execP95: 0.004,
        compileCount: 412,
        compileP50: 0.0031,
        compileP95: 0.012,
        hostCallCount: 20_114,
        hostCallP50: 0.00002,
        hostCallP95: 0.0004,
        sessionPoolReuse: 3901,
        sessionPoolIdle: 6,
        sessionPoolEvictions: 42,
        sessionWarmP50: 0.0009,
        sessionWarmP95: 0.006,
        importCount: 733,
        importP50: 0.0004,
        importP95: 0.003,
        spellResolveCount: 1288,
        spellResolveP50: 0.0002,
        spellResolveP95: 0.0012,
        jitRuns: 61,
        vmFaults: 0,
      },
      sandbox: {
        applyP50: 0.0011,
        applyP95: 0.0068,
        rulesRead: 5120,
        rulesWrite: 880,
        rulesExec: 311,
        envRules: 96,
        checksAllow: 41_882,
        checksDeny: 7,
        envDropped: 214,
      },
    };
  }

  // The VCS lenses come from the shared scenario (demo-scenario.ts), so the hotspots, co-change,
  // ownership, trend, and volatility all name the acme monorepo's files and reconcile with its run
  // history: libs/authkit/claims.go leads churn and blast radius (its edit broke both
  // services/identity:test and apps/dashboard:typecheck, and it co-changes with exactly those two
  // files), and services/identity:test leads volatility (it failed, then passed). The scenario carries
  // commit/pass instants as epoch ms so the dates render relative to `now` instead of hard-coded
  // literals.
  const date = (ms: number): string => new Date(ms).toLocaleDateString();
  const si = scenarioInsight(t0);
  const insight: InsightView = {
    commits: si.commits,
    hotspots: si.hotspots.map((n) => ({
      name: n.name,
      churn: n.churn,
      authors: n.authors,
      blastRadius: n.blastRadius,
      lastCommit: date(n.lastCommitMs),
    })),
    affinity: si.affinity.map((p) => ({ a: p.a, b: p.b, count: p.count, hidden: p.hidden })),
    ownership: si.ownership.map((o) => ({
      path: o.path,
      primary: o.primary,
      primaryShare: o.primaryShare,
      authors: o.authors,
      busFactor1: o.busFactor1,
      stale: o.stale,
    })),
    trend: si.trend.map((t) => ({
      path: t.path,
      delta: t.delta,
      recent: t.recent,
      earlier: t.earlier,
    })),
    volatility: {
      threshold: si.volatility.threshold,
      targets: si.volatility.targets.map((v) => ({
        label: v.project ? v.project + ":" + v.target : v.target,
        score: v.score,
        volatile: v.volatile,
        pass: v.pass,
        fail: v.fail,
        volatileCount: v.volatileCount,
        samples: v.samples,
        lastPass: date(v.lastPassMs),
      })),
    },
  };

  // ---- seed the sample strip ----------------------------------------------
  const samples: SampleView[] = [];
  {
    let h = hits - 900,
      m = misses - 130;
    for (let i = HISTORY; i > 0; i--) {
      const r = Math.round(1 + rng() * (CAPACITY - 1));
      const q = rng() < 0.12 ? Math.round(rng() * 3) : 0;
      h += Math.round(rng() * 6);
      m += Math.round(rng() * 2);
      samples.push({
        at: t0 - i * 1000,
        running: r,
        capacity: CAPACITY,
        queued: q,
        cacheHits: h,
        cacheMisses: m,
        cacheSrc: "status",
      });
    }
    hits = h;
    misses = m;
  }

  // ---- status builder -----------------------------------------------------
  function buildStatus(now: number): StatusView {
    const runningTargets = runs.flatMap((run) =>
      run.targets
        .filter((t) => t.state === "running")
        .map((t) => ({
          args: t.project ? ["run", t.target, t.project] : ["run", t.target],
          step: t.target === "test" ? "go test ./..." : "",
          startTime: t.startMs != null ? timestampFromMs(t.startMs) : undefined,
          invocation: run.inv,
        })),
    );
    // Derive the pool counts from the SAME runs the Live activity shows, so the hero's RUNNING count
    // equals the number of running targets listed (and QUEUED likewise), never a stale static number.
    const running = runningTargets.length;
    const queued = runs.flatMap((run) => run.targets).filter((t) => t.state === "queued").length;
    const total = hits + misses;
    return {
      health: { label: "healthy", cls: "ok" },
      pool: { capacity: CAPACITY, running, queued, mode: "daemon" },
      cache: { hits, misses, errors, hitRate: total > 0 ? hits / total : null, sizeBytes },
      runningTargets,
      runs: runs.map((r) => ({ ...r, targets: r.targets.slice() })),
      // acme (the scenario's monorepo) is the busy workspace the daemon is serving; magus (the
      // tool itself) sits idle beside it. So the workspaces tile names the same root the rest of the
      // board describes.
      workspaces: [
        {
          root: WORKSPACE_ROOT,
          hits: Math.round(hits * 0.82),
          misses: Math.round(misses * 0.8),
          errors,
          lastAccessTime: timestampFromMs(now - 1200),
          // One workspace declares a provider and one does not, so the demo shows both
          // states of the slot: present means a magusfile named a provider spell, absent
          // means the built-in environment provider applies.
          secretProvider: "onepassword",
        },
        {
          root: "~/Repos/magus",
          hits: Math.round(hits * 0.18),
          misses: Math.round(misses * 0.2),
          errors: 0,
          lastAccessTime: timestampFromMs(now - 38_000),
        },
      ],
      // One busy holder and one abandoned one, because the whole point of the tile is that
      // those two look identical until you read the age and the directory. Both name real projects
      // in the scenario's tree, and the busy one sits in the workspace root the tile above lists -
      // a lock on a project that exists nowhere else on the board is a lock nobody can act on.
      locks: [
        {
          project: "services/ledger",
          pid: 48210,
          command: "magus run test services/ledger",
          dir: "/Users/eli/Repos/acme",
          acquireTime: timestampFromMs(now - 41_000),
          staleAfterSeconds: 600,
          waiters: [],
        },
        {
          project: ".",
          pid: 71557,
          command: "magus run serve",
          dir: "/Users/eli/Repos/acme/.worktrees/deleted-branch",
          acquireTime: timestampFromMs(now - 6 * 24 * 60 * 60 * 1000),
          staleAfterSeconds: 600,
          waiters: [
            { pid: 12044, command: "magus run build", waitTime: timestampFromMs(now - 900_000) },
            { pid: 12105, command: "magus affected ci", waitTime: timestampFromMs(now - 240_000) },
          ],
        },
      ],
      services: [
        {
          id: "svc9f21",
          label: "postgres:16",
          command: "docker run postgres:16",
          ports: ["5432"],
          state: "running",
          dependents: 3,
          startedAt: timestampFromMs(now - 214_000),
        },
        {
          id: "svc4c08",
          label: "redis:7",
          command: "docker run redis:7-alpine",
          ports: ["6379"],
          state: "idle",
          dependents: 0,
          startedAt: timestampFromMs(now - 88_000),
        },
      ],
      magusVersion: "0.2.0",
      daemonVersion: "0.2.0",
    };
  }

  // ---- streaming log preview ----------------------------------------------
  // A rolling buffer of captured-output lines feeding the activity tile's live tail. It
  // grows on its own faster timer (below) so the preview scrolls between status frames.
  let logIdx = 0;
  const logBuf: string[] = [];
  function appendLog(): void {
    const n = 1 + Math.floor(rng() * 2); // 1-2 lines per tick (see LOG_TICK_MS on the pacing)
    for (let i = 0; i < n; i++) {
      logBuf.push(LOG_SNIPPETS[logIdx++ % LOG_SNIPPETS.length]);
    }
    while (logBuf.length > LOG_MAX) logBuf.shift();
  }
  // Seed enough to fill the preview at its TALLEST, which is Big Picture, where it now stretches to
  // fill the panel rather than stopping at the board's cap. Seeding only a board's worth left the
  // presentation view opening on a half-empty box that took a minute of real time to fill in.
  for (let i = 0; i < 60; i++) appendLog();

  // ---- publish + tick -----------------------------------------------------
  let j = 0;
  function publish(): void {
    const now = Date.now();
    advanceRuns(now);
    // Random-walk the pool a touch so the seats and utilization grid breathe.
    running = Math.max(1, Math.min(CAPACITY, running + Math.round((rng() - 0.5) * 3)));
    queued = running >= CAPACITY - 1 && rng() < 0.5 ? Math.round(rng() * 3) : 0;
    hits += Math.round(rng() * 6);
    misses += Math.round(rng() * 2);
    if (rng() < 0.03) errors += 1;
    sizeBytes += Math.round(rng() * 40_000);
    // Append a live utilization sample; keep the strip bounded.
    samples.push({
      at: now,
      running,
      capacity: CAPACITY,
      queued,
      cacheHits: hits,
      cacheMisses: misses,
      cacheSrc: "status",
    });
    if (samples.length > HISTORY + 120) samples.shift();

    store.set({
      conn: { state: "demo" },
      liveHost: null, // no live host: rows/bars render non-clickable, no dead deep-links
      status: buildStatus(now),
      metrics: buildMetrics(now, j++),
      samples: samples.slice(),
      insight,
      agents: buildAgents(now),
      logLines: logBuf.slice(),
    });
  }

  publish();
  const timer = window.setInterval(publish, TICK_MS);
  // The log preview streams on its own faster tick, pushing only the logLines slice so the
  // tail scrolls smoothly between the ~1s status frames.
  const logTimer = window.setInterval(() => {
    appendLog();
    store.set({ logLines: logBuf.slice() });
  }, LOG_TICK_MS);
  return {
    stop() {
      window.clearInterval(timer);
      window.clearInterval(logTimer);
    },
  };
}
