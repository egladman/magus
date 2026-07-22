import { must } from "../lib/must";
// demo-scenario.ts - the ONE fabricated workspace every console surface's demo derives from.
//
// The console has four daemon-free showcases (logs waterfall, recent-runs tree, activity trail,
// dashboard) that each used to invent their own disjoint fixture. A reader who opened two of them
// side by side saw contradictions - a Linux-kernel build in the log viewer, unrelated svc/api runs
// in the tree, a third cast of MCP calls in the activity trail. This module is the single source
// of truth those surfaces now share: one plausible product monorepo ("acme") and one scripted
// three-hour timeline of runs, MCP calls, a background job, a sandbox denial, and VCS insight, so
// the surfaces corroborate each other the way a real workspace would.
//
// Everything is a pure function of an injected `now` (epoch ms) with FIXED offsets and NO
// Math.random - determinism is the point: the same output refs must line up across surfaces, and
// the module is directly unit-testable (demo-scenario.test.ts asserts the cross-references hold).
// It carries NO protobuf or view-model imports: each surface's demo.ts maps this plain data into
// its own wire/view type, which keeps the story here and the transport concerns there.

const MIN = 60_000;

// The fictional monorepo the whole showcase inhabits. Projects and targets are IDENTICAL across
// every surface; a reader who learns "svc/api:test" in the tree meets the same name in the trail,
// the waterfall, and the dashboard.
export const WORKSPACE = "acme";
export const WORKSPACE_ROOT = "~/Repos/acme";

// ---- runs ------------------------------------------------------------------

export type RunState = "passed" | "failed" | "cached";

// ScenarioRun is one target execution in the timeline. endMs is when it finished (the tree's
// "how long ago" and the RESULT event key off it); startMs = endMs - durationMs frames the
// waterfall span and lets an MCP call that triggered the run sit a moment before the run's end.
// The optional journal fields (execs/stdout/stderr) are the captured-output shape the log
// viewer's Journal replays for the two invocations it streams; other runs omit them.
export interface ScenarioRun {
  ref: string;
  inv: string;
  project: string;
  target: string;
  trigger: string; // mcp (agent-driven) | ci (the sweep) | cli (manual)
  state: RunState;
  startMs: number;
  endMs: number;
  durationMs: number;
  error?: string;
  execs?: string[]; // subprocess command lines, for the journal EXEC markers
  stdout?: string; // a representative stdout line
  stderr?: string; // failure output (may be multi-line)
}

// The invocation ids the timeline threads runs onto. The CI sweep's targets all share INV_CI, so
// the tree and dashboard group them as one run; the agent-driven test runs each get their own.
export const INV_CI = "invci7a";
export const INV_TEST_BREAK = "inv92e1";
export const INV_TEST_FIX = "inv80f2";
export const INV_BUILD_FRESH = "inv5b00";

// scenarioRuns returns the canonical run history, NEWEST FIRST, spanning ~the last three hours.
// The beats: a green `magus affected ci` sweep ~3h ago (mostly cached); a lib/core edit that broke
// svc/api's test, run by an agent ~92m ago (FAIL); routine lint/typecheck; the fix and a passing
// re-run ~80m ago; a codegen target denied a write outside its sandbox ~40m ago; a fresh green
// build minutes ago. The refs here are the join keys every other surface points back to.
export function scenarioRuns(now: number): ScenarioRun[] {
  const at = (
    endMin: number,
    durMs: number,
  ): { startMs: number; endMs: number; durationMs: number } => {
    const endMs = now - endMin * MIN;
    return { startMs: endMs - durMs, endMs, durationMs: durMs };
  };
  return [
    {
      ref: "out5b0091",
      inv: INV_BUILD_FRESH,
      project: "svc/api",
      target: "build",
      trigger: "mcp",
      state: "passed",
      ...at(3, 2100),
      execs: ["go build ./cmd/api"],
      stdout: "ok  svc/api:build  2.1s",
    },
    {
      ref: "out40de1a",
      inv: "inv40de",
      project: "svc/api",
      target: "generate",
      trigger: "cli",
      state: "failed",
      ...at(40, 300),
      error: "sandbox denied a write outside the workspace: /etc/hosts",
      execs: ["go run ./cmd/gen"],
      stderr: "sandbox: denied write /etc/hosts (outside workspace)\nFAIL svc/api:generate",
    },
    {
      ref: "out80f2c7",
      inv: INV_TEST_FIX,
      project: "svc/api",
      target: "test",
      trigger: "mcp",
      state: "passed",
      ...at(80, 3600),
      execs: ["go test ./users"],
      stdout: "ok  svc/api:test  3.6s",
    },
    {
      ref: "out84a3b0",
      inv: "inv84a3",
      project: "web/app",
      target: "typecheck",
      trigger: "cli",
      state: "passed",
      ...at(84, 2400),
      execs: ["tsc --noEmit"],
      stdout: "ok  web/app:typecheck  2.4s",
    },
    {
      ref: "out88c110",
      inv: "inv88c1",
      project: "lib/core",
      target: "lint",
      trigger: "cli",
      state: "passed",
      ...at(88, 640),
      execs: ["golangci-lint run ./..."],
      stdout: "ok  lib/core:lint  0.6s",
    },
    {
      ref: "out92e1d4",
      inv: INV_TEST_BREAK,
      project: "svc/api",
      target: "test",
      trigger: "mcp",
      state: "failed",
      ...at(92, 4200),
      error: "assertion failed: users_test.go:118: got 500, want 200",
      execs: ["go build ./...", "go test ./users"],
      stderr:
        "--- FAIL: TestListUsers (0.03s)\n    users_test.go:118: got 500, want 200\nFAIL svc/api:test",
    },
    // The `magus affected ci` sweep ~3h ago: one invocation, mostly cache hits, all green.
    {
      ref: "outci7a1",
      inv: INV_CI,
      project: "svc/api",
      target: "test",
      trigger: "ci",
      state: "passed",
      ...at(178, 3900),
      execs: ["go test ./..."],
      stdout: "ok  svc/api:test  3.9s",
    },
    {
      ref: "outci7a2",
      inv: INV_CI,
      project: "web/app",
      target: "build",
      trigger: "ci",
      state: "cached",
      ...at(179, 180),
      stdout: "cached  web/app:build",
    },
    {
      ref: "outci7a3",
      inv: INV_CI,
      project: "lib/core",
      target: "build",
      trigger: "ci",
      state: "cached",
      ...at(179, 95),
      stdout: "cached  lib/core:build",
    },
    {
      ref: "outci7a4",
      inv: INV_CI,
      project: ".",
      target: "lint",
      trigger: "ci",
      state: "passed",
      ...at(180, 610),
      execs: ["golangci-lint run ./..."],
      stdout: "ok  .:lint  0.6s",
    },
  ];
}

// A ref shaped like the CI sweep's outputs but deliberately absent from scenarioRuns: it aged out
// of the retained store, so the agent's later magus_output lookup for it fails. This is the join
// key for the "failed lookup for a pruned ref" beat in the trail.
export const PRUNED_REF = "outci7a9";

// ---- activity trail --------------------------------------------------------

export type ActKind = "mcp" | "job" | "config" | "token" | "sandbox";

// ScenarioActivity is one trail entry as plain data (activity/demo.ts maps it to an ActivityEvent
// proto). timeMs is when the action happened: for an MCP call that drove a run, it is the run's
// START (endMs - durationMs), so the call sits just before the run's recorded completion and their
// durations agree. responseRef points at the run's output ref, tying the trail to the tree, the
// waterfall, and the dashboard gantt.
export interface ScenarioActivity {
  kind: ActKind;
  actor: string;
  action: string;
  ok: boolean;
  timeMs: number;
  durationMs?: number;
  requestBytes?: number;
  requestRef?: string;
  responseBytes?: number;
  responseRef?: string;
  preview?: string;
  error?: string;
  workspace: string;
}

// scenarioActivity returns the trail NEWEST FIRST (the order the service returns). Each MCP run
// call mirrors a run in scenarioRuns by responseRef and timing; the sandbox denial mirrors the
// failed generate run; the reindex job explains why the graph was warm right after a branch
// switch; the pruned-ref lookup fails against PRUNED_REF; the token/connector beats set up how the
// agent was authorized in the first place.
export function scenarioActivity(now: number): ScenarioActivity[] {
  const runs = scenarioRuns(now);
  // Each agent-driven invocation holds a single run, so lookup by invocation id is unambiguous.
  const run = (inv: string): ScenarioRun => must(runs.find((x) => x.inv === inv));
  const fresh = run(INV_BUILD_FRESH);
  const fix = run(INV_TEST_FIX);
  const broke = run(INV_TEST_BREAK);
  const at = (min: number): number => now - min * MIN;
  return [
    {
      kind: "mcp",
      actor: "claude-code",
      action: "magus_run_target",
      ok: true,
      timeMs: fresh.startMs,
      durationMs: fresh.durationMs,
      requestBytes: 198,
      requestRef: "reqb5d091",
      responseBytes: 4100,
      responseRef: fresh.ref,
      preview: "ok  svc/api:build  2.1s",
      workspace: WORKSPACE,
    },
    {
      kind: "mcp",
      actor: "claude-code",
      action: "magus_output",
      ok: false,
      timeMs: at(18),
      durationMs: 6,
      requestBytes: 44,
      error: "unknown output reference: " + PRUNED_REF,
      workspace: WORKSPACE,
    },
    {
      kind: "sandbox",
      actor: "svc/api:generate",
      action: "write /etc/hosts",
      ok: false,
      timeMs: at(40),
      error: "sandbox denied a write outside the workspace: /etc/hosts",
      workspace: WORKSPACE,
    },
    {
      kind: "mcp",
      actor: "claude-code",
      action: "magus_run_target",
      ok: true,
      timeMs: fix.startMs,
      durationMs: fix.durationMs,
      requestBytes: 210,
      requestRef: "req80f2c7",
      responseBytes: 5200,
      responseRef: fix.ref,
      preview: "ok  svc/api:test  3.6s\n--- PASS: TestListUsers",
      workspace: WORKSPACE,
    },
    {
      kind: "mcp",
      actor: "claude-code",
      action: "magus_run_target",
      ok: false,
      timeMs: broke.startMs,
      durationMs: broke.durationMs,
      requestBytes: 210,
      responseBytes: 3800,
      responseRef: broke.ref,
      error: "svc/api:test failed: 1 assertion",
      preview: "FAIL svc/api:test\nusers_test.go:118: got 500, want 200",
      workspace: WORKSPACE,
    },
    {
      kind: "job",
      actor: "daemon",
      action: "symbol-reindex",
      ok: true,
      timeMs: at(96),
      durationMs: 1260,
      preview: "reindexed after branch switch: 1648 symbols across 42 files",
      workspace: WORKSPACE,
    },
    {
      kind: "mcp",
      actor: "claude-code",
      action: "magus_query",
      ok: true,
      timeMs: at(100),
      durationMs: 34,
      requestBytes: 88,
      responseBytes: 1640,
      responseRef: "outq100a",
      preview: "kind:target project:svc/api  ->  9 matches",
      workspace: WORKSPACE,
    },
    {
      kind: "config",
      actor: "eli",
      action: "mcp connector add",
      ok: true,
      timeMs: at(150),
      preview: 'added connector token "laptop" (expires in 30d)',
      workspace: WORKSPACE,
    },
    {
      kind: "token",
      actor: "daemon",
      action: "cli token issued",
      ok: true,
      timeMs: at(155),
      workspace: WORKSPACE,
    },
  ];
}

// ---- dashboard insight (VCS lenses) ----------------------------------------

// ScenarioInsight mirrors the dashboard's InsightView but carries commit/pass instants as epoch
// ms (lastCommitMs / lastPassMs) rather than pre-formatted date strings, so dashboard/demo.ts
// renders them relative to `now` instead of hard-coding literal dates. The files and hotspots are
// the scenario's own: lib/core/users.go is the edit that broke svc/api's test, so it leads churn
// and blast radius, and svc/api:test leads volatility (it failed, then passed).
export interface ScenarioInsight {
  commits: number;
  hotspots: {
    name: string;
    churn: number;
    authors: number;
    blastRadius: number;
    lastCommitMs: number;
  }[];
  affinity: { a: string; b: string; count: number; hidden: boolean }[];
  ownership: {
    path: string;
    primary: string;
    primaryShare: number;
    authors: number;
    busFactor1: boolean;
    stale: boolean;
  }[];
  trend: { path: string; delta: number; recent: number; earlier: number }[];
  volatility: {
    threshold: number;
    targets: {
      project: string;
      target: string;
      score: number;
      volatile: boolean;
      pass: number;
      fail: number;
      volatileCount: number;
      samples: number;
      lastPassMs: number;
    }[];
  };
}

export function scenarioInsight(now: number): ScenarioInsight {
  const at = (min: number): number => now - min * MIN;
  return {
    commits: 214,
    hotspots: [
      { name: "lib/core/users.go", churn: 46, authors: 2, blastRadius: 38, lastCommitMs: at(92) },
      { name: "svc/api/handler.go", churn: 31, authors: 3, blastRadius: 22, lastCommitMs: at(80) },
      {
        name: "svc/api/users_test.go",
        churn: 24,
        authors: 2,
        blastRadius: 9,
        lastCommitMs: at(80),
      },
      {
        name: "web/app/src/api/client.ts",
        churn: 19,
        authors: 2,
        blastRadius: 14,
        lastCommitMs: at(180),
      },
    ],
    affinity: [
      { a: "lib/core/users.go", b: "svc/api/users_test.go", count: 12, hidden: false },
      { a: "svc/api/handler.go", b: "web/app/src/api/client.ts", count: 8, hidden: false },
      { a: "lib/core/users.go", b: "lib/core/pool.go", count: 6, hidden: true },
    ],
    ownership: [
      {
        path: "lib/core",
        primary: "eli",
        primaryShare: 91,
        authors: 2,
        busFactor1: true,
        stale: false,
      },
      {
        path: "svc/api",
        primary: "eli",
        primaryShare: 74,
        authors: 3,
        busFactor1: false,
        stale: false,
      },
      {
        path: "web/app",
        primary: "eli",
        primaryShare: 83,
        authors: 2,
        busFactor1: false,
        stale: true,
      },
    ],
    trend: [
      { path: "lib/core", delta: 16, recent: 22, earlier: 6 },
      { path: "svc/api", delta: 11, recent: 18, earlier: 7 },
      { path: "web/app", delta: -5, recent: 4, earlier: 9 },
    ],
    volatility: {
      threshold: 0.2,
      targets: [
        {
          project: "svc/api",
          target: "test",
          score: 0.31,
          volatile: true,
          pass: 44,
          fail: 3,
          volatileCount: 4,
          samples: 47,
          lastPassMs: at(80),
        },
        {
          project: "web/app",
          target: "build",
          score: 0.06,
          volatile: false,
          pass: 58,
          fail: 0,
          volatileCount: 1,
          samples: 59,
          lastPassMs: at(179),
        },
      ],
    },
  };
}

// ---- dashboard live gantt catalog -----------------------------------------

// The dashboard is a LIVE board, so its gantt keeps churning after the historical timeline; this
// catalog is the pool of scenario targets that live run draws from, so even the synthesized
// forward motion stays inside the same monorepo and target vocabulary.
export const SCENARIO_CATALOG: { project: string; target: string; durMs: number }[] = [
  { project: "svc/api", target: "build", durMs: 2100 },
  { project: "svc/api", target: "test", durMs: 3900 },
  { project: "web/app", target: "build", durMs: 4800 },
  { project: "web/app", target: "typecheck", durMs: 2400 },
  { project: "lib/core", target: "build", durMs: 1600 },
  { project: "lib/core", target: "test", durMs: 5200 },
  { project: ".", target: "lint", durMs: 3100 },
];
