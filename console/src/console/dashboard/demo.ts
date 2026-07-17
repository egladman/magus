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

const CAPACITY = 8;
const TICK_MS = 1000;
const LOG_TICK_MS = 500; // faster cadence for the streaming log preview so it reads as live
const LOG_MAX = 200;     // rolling captured-output buffer kept for the activity preview
const HISTORY = 200;     // seed samples (~a GitHub-year strip fills fast at ~1/s)

// A rotating catalog of plausible captured-output lines (build / test / lint / cache /
// sandbox) the demo activity preview streams. Cycled with a touch of jitter so the tail
// churns without looking scripted. Plain ASCII, no real machine data.
const LOG_SNIPPETS: string[] = [
  "[build] compiling github.com/egladman/magus/internal/cache",
  "[build] compiling github.com/egladman/magus/internal/interp",
  "ok  \tgithub.com/egladman/magus/internal/cache\t0.42s",
  "[test] === RUN   TestCache_WarmHit",
  "[test] --- PASS: TestCache_WarmHit (0.01s)",
  "[test] === RUN   TestDepgraph_Cycle",
  "[test] --- PASS: TestDepgraph_Cycle (0.03s)",
  "[lint] internal/interp/eval.go: ok",
  "[vet]  no problems found",
  "[scribe] rendered website/gen/dashboard/index.html",
  "[cache] hit  spell=go-build target=build",
  "[cache] miss spell=go-test target=test (running)",
  "[sandbox] apply rules=read:512 write:88 exec:12",
];

export interface DemoHandle { stop(): void; }

// A deterministic-enough jitter without importing anything: a tiny LCG seeded from a
// counter so the walk looks organic across reloads without Math.random's noise.
function makeRng(seed: number): () => number {
  let s = seed >>> 0;
  return () => { s = (s * 1664525 + 1013904223) >>> 0; return s / 0x1_0000_0000; };
}

// One rotating catalog of targets the fake pool "runs". Durations are short (a few
// seconds) so the gantt visibly churns.
const CATALOG: { project: string; target: string; durMs: number }[] = [
  { project: "", target: "build", durMs: 4200 },
  { project: "", target: "test", durMs: 7300 },
  { project: "", target: "lint", durMs: 3100 },
  { project: "", target: "generate", durMs: 5600 },
  { project: "website", target: "build", durMs: 4800 },
  { project: "internal/cache", target: "coverage", durMs: 6100 },
  { project: "gopherbuzz", target: "buzz-test", durMs: 3900 },
];

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
      targetStats: [
        { project: "", target: "test", spell: "go-test", count: 342, p50: 1.9, p95: 4.2, p99: 6.8, cacheHitRate: 0.71, success: 338, errors: 4 },
        { project: "", target: "build", spell: "go-build", count: 511, p50: 0.42, p95: 1.1, p99: 2.0, cacheHitRate: 0.88, success: 511, errors: 0 },
        { project: "website", target: "build", spell: "scribe", count: 96, p50: 0.83, p95: 1.9, p99: 2.7, cacheHitRate: 0.64, success: 95, errors: 1 },
        { project: "", target: "lint", spell: "golangci", count: 203, p50: 2.7, p95: 5.1, p99: 7.9, cacheHitRate: 0.55, success: 201, errors: 2 },
      ],
      mcpTools: [
        { tool: "graph_query", calls: 418, errors: 0, inputP50: 74, inputP95: 210, inputTotal: 41_233, outputP50: 1_902, outputP95: 8_140, outputTotal: 2_113_884, durationP50: 0.012, durationP95: 0.058 },
        { tool: "explain", calls: 152, errors: 1, inputP50: 63, inputP95: 120, inputTotal: 12_004, outputP50: 3_211, outputP95: 9_920, outputTotal: 933_712, durationP50: 0.021, durationP95: 0.09 },
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

  const insight: InsightView = {
    commits: 312,
    hotspots: [
      { name: "internal/cache/cache.go", churn: 41, authors: 3, blastRadius: 27, lastCommit: "Jul 10, 2026" },
      { name: "internal/interp/eval.go", churn: 33, authors: 2, blastRadius: 44, lastCommit: "Jul 9, 2026" },
      { name: "website/scribe.buzz", churn: 28, authors: 1, blastRadius: 12, lastCommit: "Jul 11, 2026" },
      { name: "cmd/magus/main.go", churn: 22, authors: 4, blastRadius: 51, lastCommit: "Jul 8, 2026" },
    ],
    affinity: [
      { a: "internal/cache/cache.go", b: "internal/cache/output_store.go", count: 14, hidden: false },
      { a: "types/status.go", b: "internal/proc/server.go", count: 11, hidden: false },
      { a: "website/dashboard.css", b: "website/js/dashboard/main.ts", count: 9, hidden: true },
    ],
    ownership: [
      { path: "gopherbuzz", primary: "eli", primaryShare: 92, authors: 2, busFactor1: true, stale: false },
      { path: "internal/sandbox", primary: "eli", primaryShare: 78, authors: 3, busFactor1: false, stale: true },
      { path: "website", primary: "eli", primaryShare: 85, authors: 2, busFactor1: false, stale: false },
    ],
    trend: [
      { path: "internal/service/console", delta: 18, recent: 24, earlier: 6 },
      { path: "website/js/dashboard", delta: 12, recent: 15, earlier: 3 },
      { path: "internal/depgraph", delta: -7, recent: 2, earlier: 9 },
    ],
    volatility: {
      threshold: 0.2,
      targets: [
        { label: "internal/proc:test", score: 0.34, volatile: true, pass: 41, fail: 3, volatileCount: 5, samples: 49, lastPass: "Jul 11, 2026" },
        { label: "website:build", score: 0.08, volatile: false, pass: 61, fail: 0, volatileCount: 1, samples: 62, lastPass: "Jul 11, 2026" },
      ],
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
      workspaces: [
        { root: "~/Repos/magus", hits: Math.round(hits * 0.82), misses: Math.round(misses * 0.8), errors, lastAccessTime: timestampFromMs(now - 1200) },
        { root: "~/Repos/acme", hits: Math.round(hits * 0.18), misses: Math.round(misses * 0.2), errors: 0, lastAccessTime: timestampFromMs(now - 38_000) },
      ],
      services: [
        { id: "svc9f21", label: "postgres:16", command: "docker run postgres:16", ports: ["5432"], state: "running", dependents: 3, startedAt: timestampFromMs(now - 214_000) },
        { id: "svc4c08", label: "redis:7", command: "docker run redis:7-alpine", ports: ["6379"], state: "idle", dependents: 0, startedAt: timestampFromMs(now - 88_000) },
      ],
      magusVersion: "1.4.2",
      daemonVersion: "1.4.2",
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
