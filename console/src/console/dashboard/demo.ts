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
  DashboardState, StatusView, MetricsView, SampleView, InsightView,
  RunView, TargetRunView, TargetState,
} from "./state";
import { SCENARIO_CATALOG, WORKSPACE_ROOT, scenarioInsight } from "../demo-scenario";

const CAPACITY = 8;
const TICK_MS = 1000;
const LOG_TICK_MS = 500; // faster cadence for the streaming log preview so it reads as live
const LOG_MAX = 200;     // rolling captured-output buffer kept for the activity preview
const HISTORY = 200;     // seed samples (~a GitHub-year strip fills fast at ~1/s)

// A rotating catalog of plausible captured-output lines (build / test / lint / cache / sandbox) the
// demo activity preview streams. They echo the shared scenario's acme monorepo (svc/api, web/app,
// lib/core), so the streaming tail reads as the same workspace the tiles above describe. Cycled with
// a touch of jitter so the tail churns without looking scripted. Plain ASCII, no real machine data.
const LOG_SNIPPETS: string[] = [
  "[build] compiling acme/svc/api/cmd/api",
  "[build] compiling acme/lib/core/users",
  "ok  \tacme/svc/api\t2.10s",
  "[test] === RUN   TestListUsers",
  "[test] --- PASS: TestListUsers (0.03s)",
  "[test] === RUN   TestUserPool",
  "[test] --- PASS: TestUserPool (0.01s)",
  "[lint] lib/core/users.go: ok",
  "[vet]  no problems found",
  "[typecheck] web/app/src/main.ts: ok",
  "[cache] hit  spell=go-build target=svc/api:build",
  "[cache] miss spell=go-test target=svc/api:test (running)",
  "[sandbox] apply rules=read:512 write:88 exec:12",
];

export interface DemoHandle { stop(): void; }

// A deterministic-enough jitter without importing anything: a tiny LCG seeded from a
// counter so the walk looks organic across reloads without Math.random's noise.
function makeRng(seed: number): () => number {
  let s = seed >>> 0;
  return () => { s = (s * 1664525 + 1013904223) >>> 0; return s / 0x1_0000_0000; };
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

  function newTarget(state: TargetState, startMs: number | null, endMs: number | null): TargetRunView {
    const c = nextCat();
    const durationMs = endMs != null && startMs != null ? endMs - startMs : 0;
    return {
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
  }

  function startRun(now: number): RunView {
    const run: RunView = {
      inv: invId(invN++),
      trigger: TRIGGERS[invN % TRIGGERS.length],
      targets: [newTarget("running", now, null), newTarget("queued", null, null)],
    };
    runs.push(run);
    return run;
  }

  // Seed a couple of finished runs behind "now" so the timeline isn't empty on frame 1.
  (function seedRuns(): void {
    const past = startRun(t0 - 46_000);
    for (const t of past.targets) { t.state = "passed"; t.terminal = true; }
    past.targets[0].endMs = t0 - 42_000; past.targets[0].startMs = t0 - 46_000; past.targets[0].durationMs = 4000;
    past.targets[1] = newTarget("cached", t0 - 42_000, t0 - 41_800);
    past.targets.push(newTarget("passed", t0 - 41_000, t0 - 35_500));
    startRun(t0 - 9_000); // the live run: [running, queued]
  })();

  function advanceRuns(now: number): void {
    const run = runs[runs.length - 1];
    const rt = run.targets.find((t) => t.state === "running");
    if (rt && rt.startMs != null) {
      const cat = CATALOG[(catIdx - run.targets.length + 8) % CATALOG.length];
      const planned = cat ? cat.durMs : 5000;
      if (now - rt.startMs >= planned) {
        // Finish it (1-in-4 lands as a cache hit), promote the queued one, enqueue next.
        rt.state = rng() < 0.25 ? "cached" : "passed";
        rt.terminal = true;
        rt.endMs = now;
        rt.durationMs = now - rt.startMs;
        rt.outputRef = ref(refN++);
        const q = run.targets.find((t) => t.state === "queued");
        if (run.targets.length >= 5 || !q) {
          startRun(now);
        } else {
          q.state = "running";
          q.startMs = now;
          run.targets.push(newTarget("queued", null, null));
        }
      }
    }
    // Trim runs whose every target ended before the visible window (keep memory flat).
    while (runs.length > 4) runs.shift();
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
        hits: 1873, misses: 402, errors: 1, hitRate: 1873 / (1873 + 402),
        durationP50: 0.043, durationP95: 0.21, ioCount: 2276, bytesTotal: 5_912_334_221,
      },
      // Per-target aggregates over the acme monorepo the scenario describes. svc/api:test carries a
      // few errors (it flapped) and a lower cache-hit rate; the cached-heavy build/typecheck targets
      // sit high - reconciling, roughly, with the run history and the volatility lens.
      targetStats: [
        { project: "svc/api", target: "test", spell: "go-test", count: 188, p50: 3.6, p95: 5.4, p99: 7.1, cacheHitRate: 0.41, success: 184, errors: 4 },
        { project: "svc/api", target: "build", spell: "go-build", count: 342, p50: 0.42, p95: 1.1, p99: 2.0, cacheHitRate: 0.88, success: 342, errors: 0 },
        { project: "web/app", target: "build", spell: "esbuild", count: 96, p50: 0.83, p95: 1.9, p99: 2.7, cacheHitRate: 0.72, success: 96, errors: 0 },
        { project: "web/app", target: "typecheck", spell: "tsc", count: 74, p50: 2.3, p95: 3.4, p99: 4.6, cacheHitRate: 0.55, success: 73, errors: 1 },
        { project: "lib/core", target: "lint", spell: "golangci", count: 129, p50: 0.6, p95: 1.2, p99: 2.1, cacheHitRate: 0.61, success: 129, errors: 0 },
      ],
      // The MCP tools the scenario's agent actually called (magus_run_target, magus_query,
      // magus_output), so the metrics tile names the same tools the activity trail records - including
      // the one magus_output error (the pruned-ref lookup).
      mcpTools: [
        { tool: "magus_run_target", calls: 63, errors: 1, inputP50: 205, inputP95: 320, inputTotal: 13_104, outputP50: 4_120, outputP95: 9_800, outputTotal: 271_402, durationP50: 2.1, durationP95: 4.4 },
        { tool: "magus_query", calls: 214, errors: 0, inputP50: 84, inputP95: 190, inputTotal: 19_006, outputP50: 1_640, outputP95: 6_210, outputTotal: 402_118, durationP50: 0.034, durationP95: 0.09 },
        { tool: "magus_output", calls: 41, errors: 1, inputP50: 44, inputP95: 60, inputTotal: 1_920, outputP50: 3_200, outputP95: 9_100, outputTotal: 158_400, durationP50: 0.008, durationP95: 0.02 },
      ],
      buzz: {
        execCount: 8123 + j * 2, execP50: 0.0007, execP95: 0.004,
        compileCount: 412, compileP50: 0.0031, compileP95: 0.012,
        hostCallCount: 20_114, hostCallP50: 0.00002, hostCallP95: 0.0004,
        sessionPoolReuse: 3901, sessionPoolIdle: 6, sessionPoolEvictions: 42,
        sessionWarmP50: 0.0009, sessionWarmP95: 0.006,
        importCount: 733, importP50: 0.0004, importP95: 0.003,
        spellResolveCount: 1288, spellResolveP50: 0.0002, spellResolveP95: 0.0012,
        jitRuns: 61, vmFaults: 0,
      },
      sandbox: {
        applyP50: 0.0011, applyP95: 0.0068,
        rulesRead: 5120, rulesWrite: 880, rulesExec: 311, envRules: 96,
        checksAllow: 41_882, checksDeny: 7, envDropped: 214,
      },
    };
  }

  // The VCS lenses come from the shared scenario (demo-scenario.ts), so the hotspots, co-change,
  // ownership, trend, and volatility all name the acme monorepo's files and reconcile with its run
  // history: lib/core/users.go leads churn/blast radius (its edit broke svc/api:test), and svc/api:test
  // leads volatility (it failed, then passed). The scenario carries commit/pass instants as epoch ms so
  // the dates render relative to `now` instead of the old hard-coded literals.
  const date = (ms: number): string => new Date(ms).toLocaleDateString();
  const si = scenarioInsight(t0);
  const insight: InsightView = {
    commits: si.commits,
    hotspots: si.hotspots.map((n) => ({
      name: n.name, churn: n.churn, authors: n.authors, blastRadius: n.blastRadius, lastCommit: date(n.lastCommitMs),
    })),
    affinity: si.affinity.map((p) => ({ a: p.a, b: p.b, count: p.count, hidden: p.hidden })),
    ownership: si.ownership.map((o) => ({
      path: o.path, primary: o.primary, primaryShare: o.primaryShare, authors: o.authors, busFactor1: o.busFactor1, stale: o.stale,
    })),
    trend: si.trend.map((t) => ({ path: t.path, delta: t.delta, recent: t.recent, earlier: t.earlier })),
    volatility: {
      threshold: si.volatility.threshold,
      targets: si.volatility.targets.map((v) => ({
        label: v.project ? v.project + ":" + v.target : v.target,
        score: v.score, volatile: v.volatile, pass: v.pass, fail: v.fail,
        volatileCount: v.volatileCount, samples: v.samples, lastPass: date(v.lastPassMs),
      })),
    },
  };

  // ---- seed the sample strip ----------------------------------------------
  const samples: SampleView[] = [];
  {
    let h = hits - 900, m = misses - 130;
    for (let i = HISTORY; i > 0; i--) {
      const r = Math.round(1 + rng() * (CAPACITY - 1));
      const q = rng() < 0.12 ? Math.round(rng() * 3) : 0;
      h += Math.round(rng() * 6);
      m += Math.round(rng() * 2);
      samples.push({ at: t0 - i * 1000, running: r, capacity: CAPACITY, queued: q, cacheHits: h, cacheMisses: m, cacheSrc: "status" });
    }
    hits = h; misses = m;
  }

  // ---- status builder -----------------------------------------------------
  function buildStatus(now: number): StatusView {
    const runningTargets = runs
      .flatMap((run) => run.targets
        .filter((t) => t.state === "running")
        .map((t) => ({
          args: t.project ? ["run", t.target, t.project] : ["run", t.target],
          step: t.target === "test" ? "go test ./..." : "",
          startTime: t.startMs != null ? timestampFromMs(t.startMs) : undefined,
          invocation: run.inv,
        })));
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
      // acme (the scenario's monorepo) is the busy workspace the daemon is serving; magus (the tool
      // itself) sits idle beside it. So the workspaces tile names the same root the rest of the board
      // describes.
      workspaces: [
        { root: WORKSPACE_ROOT, hits: Math.round(hits * 0.82), misses: Math.round(misses * 0.8), errors, lastAccessTime: timestampFromMs(now - 1200) },
        { root: "~/Repos/magus", hits: Math.round(hits * 0.18), misses: Math.round(misses * 0.2), errors: 0, lastAccessTime: timestampFromMs(now - 38_000) },
      ],
      services: [
        { id: "svc9f21", label: "postgres:16", command: "docker run postgres:16", ports: ["5432"], state: "running", dependents: 3, startedAt: timestampFromMs(now - 214_000) },
        { id: "svc4c08", label: "redis:7", command: "docker run redis:7-alpine", ports: ["6379"], state: "idle", dependents: 0, startedAt: timestampFromMs(now - 88_000) },
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
    const n = 1 + Math.floor(rng() * 3); // 1-3 lines per tick
    for (let i = 0; i < n; i++) {
      logBuf.push(LOG_SNIPPETS[logIdx++ % LOG_SNIPPETS.length]);
    }
    while (logBuf.length > LOG_MAX) logBuf.shift();
  }
  // Seed a screenful so the preview is populated on the first paint.
  for (let i = 0; i < 24; i++) appendLog();

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
    samples.push({ at: now, running, capacity: CAPACITY, queued, cacheHits: hits, cacheMisses: misses, cacheSrc: "status" });
    if (samples.length > HISTORY + 120) samples.shift();

    store.set({
      conn: { state: "demo" },
      liveHost: null, // no live host: rows/bars render non-clickable, no dead deep-links
      status: buildStatus(now),
      metrics: buildMetrics(now, j++),
      samples: samples.slice(),
      insight,
      logLines: logBuf.slice(),
    });
  }

  publish();
  const timer = window.setInterval(publish, TICK_MS);
  // The log preview streams on its own faster tick, pushing only the logLines slice so the
  // tail scrolls smoothly between the ~1s status frames.
  const logTimer = window.setInterval(() => { appendLog(); store.set({ logLines: logBuf.slice() }); }, LOG_TICK_MS);
  return { stop() { window.clearInterval(timer); window.clearInterval(logTimer); } };
}
