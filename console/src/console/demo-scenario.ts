import { must } from "../lib/guards";
// demo-scenario.ts - the ONE fabricated workspace every console surface's demo derives from.
//
// The console has four daemon-free showcases (logs waterfall, recent-runs tree, activity trail,
// dashboard) that each used to invent their own disjoint fixture. A reader who opened two of them
// side by side saw contradictions - a Linux-kernel build in the log viewer, unrelated runs in the
// tree, a third cast of MCP calls in the activity trail. This module is the single source of truth
// those surfaces now share: one plausible product monorepo ("acme") and one scripted three-hour
// timeline of runs, MCP calls, a background job, a sandbox denial, and VCS insight, so the surfaces
// corroborate each other the way a real workspace would.
//
// This fixture does the product's FIRST-IMPRESSION work: #demo and the empty state's "See the demo"
// button are what someone with no daemon running meets first. So it is written to survive being read
// closely. The workspace is not a four-project placeholder: it is a twelve-project service monorepo,
// its captured output is what the real tools actually print (go test package lines, tsc diagnostics
// with file:line:col, a vite summary replayed out of the cache), and its story holds together when a
// reader opens two surfaces and compares them line by line.
//
// THE STORY, in the order it happened - one shared-library contract change and its blast radius:
//   1. ~3h ago   `magus affected ci` sweeps green on main, mostly out of cache. The baseline: this
//                is what the repo looks like when nothing is wrong.
//   2. ~100m     an agent asks the graph what services/identity declares; the daemon then reindexes
//                symbols after a branch switch - the developer has just checked out the branch
//                carrying an edit to libs/authkit, the shared token library.
//   3. ~92m      the agent runs services/identity:test. It FAILS: authkit's claims type stopped
//                carrying the audience the service's token verifier asserts on.
//   4. ~88m      libs/authkit lints CLEAN, which is exactly the point - the break is a contract
//                change, not a lint bug, and no linter was ever going to catch it.
//   5. ~84m      apps/dashboard:typecheck FAILS too. The same claims change moved a field the web
//                client reads, so the blast radius is wider than the one service - which is what
//                the hotspot and co-change lenses below then say in aggregate.
//   6. ~80m/76m  the fix lands: services/identity:test and apps/dashboard:typecheck both go green.
//   7. ~40m      services/identity:generate is DENIED a write outside the workspace (its codegen
//                template still resolved an output path through the module cache).
//   8. ~18m      the agent asks for an output ref that has already aged out of the store.
//   9. ~3m       services/identity:build, fresh and green.
//
// Everything is a pure function of an injected `now` (epoch ms) with FIXED offsets and NO
// Math.random - determinism is the point: the same output refs must line up across surfaces, and
// the module is directly unit-testable (demo-scenario.test.ts asserts the cross-references hold).
// It carries NO protobuf or view-model imports: each surface's demo.ts maps this plain data into
// its own wire/view type, which keeps the story here and the transport concerns there.

const MIN = 60_000;

// The fictional monorepo the whole showcase inhabits. Projects and targets are IDENTICAL across
// every surface; a reader who learns "services/identity:test" in the tree meets the same name in
// the trail, the waterfall, and the dashboard.
//
// The tree is the conventional four-bucket monorepo layout, so its shape is recognizable before any
// of the names are:
//   services/gateway   services/identity   services/ledger   services/catalog   (Go)
//   apps/dashboard     apps/admin                                               (TypeScript)
//   libs/authkit       libs/protocol       libs/ui                              (shared)
//   tools/migrate      docs                .                                    (support + root)
// libs/authkit is the shared library at the center of the story; services/identity and
// apps/dashboard are the two downstream consumers its change broke.
export const WORKSPACE = "acme";
export const WORKSPACE_ROOT = "~/Repos/acme";

// The Go module path the workspace's packages live under, so `go test` package lines read the way
// they do in a real repo (the grafana/grafana shape: one module, many nested packages).
const GOMOD = "github.com/acme/acme";

// ---- runs ------------------------------------------------------------------

export type RunState = "passed" | "failed" | "cached";

// ScenarioRun is one target execution in the timeline. endMs is when it finished (the tree's
// "how long ago" and the RESULT event key off it); startMs = endMs - durationMs frames the
// waterfall span and lets an MCP call that triggered the run sit a moment before the run's end.
// The optional journal fields are the captured-output shape the log viewer's Journal replays for
// the two invocations it streams:
//   execs   the subprocess command lines magus actually ran, one EXEC marker each. A CACHED run has
//           none - that is the whole meaning of a cache hit - so cached runs omit the field.
//   stdout  what the tool wrote, verbatim, newline-separated. A cached run's stdout is the REPLAY
//           of the stored output, which is why apps/dashboard:build below reports a 3.71s vite
//           build inside a 180ms target: the saving is the point.
//   stderr  the failure block, newline-separated.
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
  stdout?: string; // captured stdout, newline-separated
  stderr?: string; // failure output, newline-separated
}

// The invocation ids the timeline threads runs onto. The CI sweep's targets all share INV_CI, so
// the tree and dashboard group them as one run; the agent-driven test runs each get their own.
export const INV_CI = "invci7a";
export const INV_TEST_BREAK = "inv92e1";
export const INV_TEST_FIX = "inv80f2";
export const INV_BUILD_FRESH = "inv5b00";

// scenarioRuns returns the canonical run history, NEWEST FIRST, spanning ~the last three hours.
// It is the story above as data; the refs here are the join keys every other surface points back
// to, so a ref a reader meets in the trail resolves to the same target, timing and outcome in the
// tree, the waterfall and the dashboard.
export function scenarioRuns(now: number): ScenarioRun[] {
  const at = (
    endMin: number,
    durMs: number,
  ): { startMs: number; endMs: number; durationMs: number } => {
    const endMs = now - endMin * MIN;
    return { startMs: endMs - durMs, endMs, durationMs: durMs };
  };
  return [
    // Beat 9: fresh and green. The last thing that happened is a pass, so the showcase does not
    // open on a workspace that is simply broken.
    {
      ref: "out5b0091",
      inv: INV_BUILD_FRESH,
      project: "services/identity",
      target: "build",
      trigger: "mcp",
      state: "passed",
      ...at(3, 2100),
      execs: ["go build -trimpath -o bin/identityd ./cmd/identityd", "go vet ./..."],
      // A green Go build is nearly silent; the one line it does print is the toolchain fetching a
      // dependency the branch bumped. Leaving it near-empty is honest, and it gives the viewer a
      // quiet target to contrast with the noisy failing one.
      stdout: "go: downloading github.com/golang-jwt/jwt/v5 v5.2.1",
    },
    // Beat 7: the sandbox denial. The generate target's buf template still resolved its output dir
    // through the Go module cache - a real and very common way to write outside the workspace by
    // accident - so magus denied the write and the target aborted in 300ms.
    {
      ref: "out40de1a",
      inv: "inv40de",
      project: "services/identity",
      target: "generate",
      trigger: "cli",
      state: "failed",
      ...at(40, 300),
      error:
        "sandbox denied a write outside the workspace: " +
        "/Users/eli/go/pkg/mod/github.com/acme/protocol@v1.4.0/token.pb.go",
      execs: ["buf generate --template buf.gen.yaml"],
      stderr:
        "sandbox: denied write /Users/eli/go/pkg/mod/github.com/acme/protocol@v1.4.0/token.pb.go\n" +
        "sandbox: workspace is /Users/eli/Repos/acme\n" +
        "Failure: plugin protoc-gen-go: exit status 1\n" +
        "FAIL services/identity:generate",
    },
    // Beat 6b: the web side closes. Same target, same command, four minutes after the fix - the
    // pair (out84a3b0 -> out76b4e2) is what makes the typecheck thread a thread and not a loose end.
    {
      ref: "out76b4e2",
      inv: "inv76b4",
      project: "apps/dashboard",
      target: "typecheck",
      trigger: "cli",
      state: "passed",
      ...at(76, 2400),
      // A clean tsc prints nothing at all, so there is no stdout to record. Compare with the
      // failing run below, which is the same command and all diagnostics.
      execs: ["pnpm exec tsc --noEmit"],
    },
    // Beat 6a: the fix. Same project, same target, same command as the failure at 92m - only the
    // outcome and the token package's timing differ, which is the comparison the run tree exists
    // to make easy.
    {
      ref: "out80f2c7",
      inv: INV_TEST_FIX,
      project: "services/identity",
      target: "test",
      trigger: "mcp",
      state: "passed",
      ...at(80, 3600),
      execs: ["go build ./...", "go test -race -count=1 ./..."],
      stdout:
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/session\t0.188s\n" +
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/store\t1.014s\n" +
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/token\t0.436s",
    },
    // Beat 5: the blast radius widens. The same claims change that broke the Go verifier also moved
    // a field the web client reads, and tsc says so with file:line:col. Piped (not a TTY), so no
    // ANSI here - this is the plain diagnostic form magus captures.
    {
      ref: "out84a3b0",
      inv: "inv84a3",
      project: "apps/dashboard",
      target: "typecheck",
      trigger: "cli",
      state: "failed",
      ...at(84, 1900),
      error: "tsc: 2 errors in src/api/session.ts",
      execs: ["pnpm exec tsc --noEmit"],
      stderr:
        "src/api/session.ts(42,18): error TS2339: Property 'audience' does not exist on type 'SessionClaims'.\n" +
        "src/api/session.ts(58,7): error TS2739: Type '{ subject: string; expiresAt: number; }' is missing " +
        "the following properties from type 'SessionClaims': audience, issuedAt\n" +
        "Found 2 errors in the same file, starting at: src/api/session.ts:42",
    },
    // Beat 4: the library itself is clean. This beat exists to be UNsurprising - a reader who
    // assumes the linter would have caught it gets the answer here, next to the failures.
    {
      ref: "out88c110",
      inv: "inv88c1",
      project: "libs/authkit",
      target: "lint",
      trigger: "cli",
      state: "passed",
      ...at(88, 640),
      execs: ["golangci-lint run ./..."],
      stdout: "0 issues.",
    },
    // Beat 3: the break. This is the invocation the log viewer STREAMS, so it carries the fullest
    // captured output in the fixture: the packages that passed on stdout, the failing package's
    // assertions on stderr, and a `go test` summary shaped exactly the way the tool shapes it.
    {
      ref: "out92e1d4",
      inv: INV_TEST_BREAK,
      project: "services/identity",
      target: "test",
      trigger: "mcp",
      state: "failed",
      ...at(92, 4200),
      error: "2 tests failed in internal/token: TestVerifyExpiredToken, TestVerifyAudienceMismatch",
      execs: ["go build ./...", "go test -race -count=1 ./..."],
      stdout:
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/session\t0.184s\n" +
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/store\t1.021s",
      stderr:
        "--- FAIL: TestVerifyExpiredToken (0.02s)\n" +
        "    verify_test.go:118: Verify(expired) error = <nil>, want token: expired\n" +
        "--- FAIL: TestVerifyAudienceMismatch (0.01s)\n" +
        '    verify_test.go:143: claims.Audience = "", want "acme-dashboard"\n' +
        "FAIL\t" +
        GOMOD +
        "/services/identity/internal/token\t0.412s\n" +
        "FAIL",
    },
    // Beat 1: the `magus affected ci` sweep ~3h ago. One invocation, four targets, mostly cache
    // hits, all green - the log viewer renders it as the completed sibling beside the failure, so
    // a reader sees cached and passed and failed side by side on one axis.
    {
      ref: "outci7a1",
      inv: INV_CI,
      project: "services/identity",
      target: "test",
      trigger: "ci",
      state: "passed",
      ...at(178, 3900),
      execs: ["go test -race -count=1 ./..."],
      stdout:
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/session\t0.190s\n" +
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/store\t1.034s\n" +
        "ok  \t" +
        GOMOD +
        "/services/identity/internal/token\t0.402s",
    },
    // A cache hit that REPLAYS its stored output: 180ms of wall clock reporting a 3.71s vite build.
    // That gap is the single clearest statement of what the cache is for, and it is why this run
    // keeps its stdout while the cached Go build below keeps none. The ANSI here is vite's own -
    // it is the one tool in the sweep that colors its summary.
    {
      ref: "outci7a2",
      inv: INV_CI,
      project: "apps/dashboard",
      target: "build",
      trigger: "ci",
      state: "cached",
      ...at(179, 180),
      stdout:
        "vite v6.0.7 building for production...\n" +
        "412 modules transformed.\n" +
        "\u001b[36mdist/assets/index-8f2c1a44.js\u001b[0m   412.06 kB | gzip: 128.44 kB\n" +
        "built in 3.71s",
    },
    // The other cache hit, deliberately silent: a green `go build` prints nothing, so there is
    // nothing to replay. The viewer has to render a target with no captured output at all, and the
    // showcase is where that case should be visible rather than discovered in production.
    {
      ref: "outci7a3",
      inv: INV_CI,
      project: "libs/authkit",
      target: "build",
      trigger: "ci",
      state: "cached",
      ...at(179, 95),
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
      stdout: "0 issues.",
    },
  ];
}

// A ref shaped like the CI sweep's outputs but deliberately absent from scenarioRuns: it aged out
// of the retained store, so the agent's later magus_output lookup for it fails. This is the join
// key for the "failed lookup for a pruned ref" beat in the trail.
// ScenarioInvocation is one COMMAND in the scenario - the unit the run browser lists and the log
// viewer opens. scenarioRuns records what each TARGET did; this records what was asked for, which no
// run row carries and which is how a person actually remembers a run ("the affected ci I kicked off
// before lunch"). Derived rather than stored: the story is already fully determined by the runs, and
// a second hand-maintained table would be one more thing to keep in step with it.
export interface ScenarioInvocation {
  inv: string;
  arguments: string[]; // full argv, subcommand included
  trigger: string; // journal Trigger vocabulary: run | affected | ci | direct
  startMs: number;
  endMs: number;
  failed: boolean;
}

// scenarioInvocations folds the run history into its invocations, NEWEST FIRST, spanning each one
// from its first target's start to its last one's end.
export function scenarioInvocations(now: number): ScenarioInvocation[] {
  const order: ScenarioInvocation[] = [];
  const byId = new Map<string, ScenarioInvocation>();
  for (const r of scenarioRuns(now)) {
    const seen = byId.get(r.inv);
    if (seen) {
      seen.startMs = Math.min(seen.startMs, r.startMs);
      seen.endMs = Math.max(seen.endMs, r.endMs);
      seen.failed = seen.failed || r.state === "failed";
      continue;
    }
    const ci = r.inv === INV_CI;
    const inv: ScenarioInvocation = {
      inv: r.inv,
      arguments: ci ? ["affected", "ci"] : ["run", r.target.split(":")[0], r.project],
      trigger: ci ? "ci" : r.trigger === "mcp" ? "direct" : "run",
      startMs: r.startMs,
      endMs: r.endMs,
      failed: r.state === "failed",
    };
    byId.set(r.inv, inv);
    order.push(inv);
  }
  return order;
}

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
// failed generate run; the reindex job explains why the graph was warm right after the branch
// switch that brought the libs/authkit edit in; the pruned-ref lookup fails against PRUNED_REF; the
// token/connector beats set up how the agent was authorized in the first place.
//
// The previews are magus's OWN output, not the tools' - a `[pass]`/`[fail]` glyph, the project, the
// target, and the parenthetical that separates "cached" from "ran". That is deliberately a
// different voice from the captured output in scenarioRuns above, because it is a different thing:
// the trail records what the agent was handed back, the journal records what the tool printed.
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
      preview: "[pass] services/identity build (ran, 2.1s)",
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
      actor: "services/identity:generate",
      action: "write /Users/eli/go/pkg/mod/github.com/acme/protocol@v1.4.0/token.pb.go",
      ok: false,
      timeMs: at(40),
      error:
        "sandbox denied a write outside the workspace: " +
        "/Users/eli/go/pkg/mod/github.com/acme/protocol@v1.4.0/token.pb.go",
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
      preview:
        "[pass] services/identity test (ran, 3.6s)\n" +
        "ok  github.com/acme/acme/services/identity/internal/token  0.436s",
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
      error: "services/identity:test failed: 2 assertions",
      preview:
        "[fail] services/identity test (ran, 4.2s)\n" +
        "--- FAIL: TestVerifyExpiredToken (0.02s)\n" +
        "    verify_test.go:118: Verify(expired) error = <nil>, want token: expired",
      workspace: WORKSPACE,
    },
    {
      kind: "job",
      actor: "daemon",
      action: "symbol-reindex",
      ok: true,
      timeMs: at(96),
      durationMs: 1260,
      // The branch switch that brought the libs/authkit edit into the tree. It sits four minutes
      // before the failing test on purpose: it is the answer to "why did this suddenly break".
      preview: "reindexed after checkout feat/authkit-claims: 4182 symbols across 318 files",
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
      preview: "kind:target project:services/identity  ->  9 matches",
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
// renders them relative to `now` instead of hard-coding literal dates.
//
// The files are the story's own, which is what makes the lenses readable rather than decorative:
// libs/authkit/claims.go is the edit that broke both downstream consumers, so it leads churn AND
// blast radius; the two files it co-changes with are exactly the two that failed; and
// services/identity:test leads volatility because it is the target that failed and then passed.
export interface ScenarioInsight {
  commits: number;
  hotspots: {
    name: string;
    churn: number;
    authors: number;
    blastRadius: number;
    lastCommitMs: number;
  }[];
  hotspotFiles: {
    path: string;
    commits: number;
    complexity: number;
    score: number;
    authors: number;
    moves: number;
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
      // The shared library leads on both axes: a lot of recent churn AND the widest blast radius,
      // which together are the whole reason one edit took down two unrelated-looking targets.
      {
        name: "libs/authkit/claims.go",
        churn: 46,
        authors: 2,
        blastRadius: 38,
        lastCommitMs: at(92),
      },
      {
        name: "services/identity/internal/token/verify.go",
        churn: 31,
        authors: 3,
        blastRadius: 22,
        lastCommitMs: at(80),
      },
      {
        name: "services/identity/internal/token/verify_test.go",
        churn: 24,
        authors: 2,
        blastRadius: 9,
        lastCommitMs: at(80),
      },
      {
        name: "apps/dashboard/src/api/session.ts",
        churn: 19,
        authors: 2,
        blastRadius: 14,
        lastCommitMs: at(84),
      },
    ],
    // The same lens one level down, at the granularity a reader can open. claims.go leads here
    // too, and it is the file the whole scenario turns on. session.ts carries moves: 2 on
    // purpose - it is the case the project-level table cannot show and the one lineage
    // tracking exists for, a file that was rewritten AND shuffled between directories twice,
    // whose history would read as three quieter files if churn were attributed by path.
    hotspotFiles: [
      {
        path: "libs/authkit/claims.go",
        commits: 46,
        complexity: 310,
        score: 14260,
        authors: 2,
        moves: 0,
        lastCommitMs: at(92),
      },
      {
        path: "apps/dashboard/src/api/session.ts",
        commits: 19,
        complexity: 402,
        score: 7638,
        authors: 2,
        moves: 2,
        lastCommitMs: at(84),
      },
      {
        path: "services/identity/internal/token/verify.go",
        commits: 31,
        complexity: 205,
        score: 6355,
        authors: 3,
        moves: 0,
        lastCommitMs: at(80),
      },
      {
        path: "services/identity/internal/token/verify_test.go",
        commits: 24,
        complexity: 96,
        score: 2304,
        authors: 2,
        moves: 0,
        lastCommitMs: at(80),
      },
    ],
    // The first two pairs are the story stated as history: claims.go has been changing WITH both
    // downstream files for a while, so the lens would have predicted this break. The third is
    // marked hidden - a coupling inside authkit that no import graph shows.
    affinity: [
      {
        a: "libs/authkit/claims.go",
        b: "services/identity/internal/token/verify_test.go",
        count: 12,
        hidden: false,
      },
      {
        a: "libs/authkit/claims.go",
        b: "apps/dashboard/src/api/session.ts",
        count: 8,
        hidden: false,
      },
      { a: "libs/authkit/claims.go", b: "libs/authkit/keyset.go", count: 6, hidden: true },
    ],
    ownership: [
      // The shared library everything depends on is also the one with a bus factor of 1. Putting
      // that next to its blast radius above is the point of having both lenses on one board.
      {
        path: "libs/authkit",
        primary: "eli",
        primaryShare: 91,
        authors: 2,
        busFactor1: true,
        stale: false,
      },
      {
        path: "services/identity",
        primary: "eli",
        primaryShare: 74,
        authors: 3,
        busFactor1: false,
        stale: false,
      },
      {
        path: "apps/dashboard",
        primary: "eli",
        primaryShare: 83,
        authors: 2,
        busFactor1: false,
        stale: true,
      },
    ],
    trend: [
      { path: "libs/authkit", delta: 16, recent: 22, earlier: 6 },
      { path: "services/identity", delta: 11, recent: 18, earlier: 7 },
      { path: "apps/dashboard", delta: -5, recent: 4, earlier: 9 },
    ],
    volatility: {
      threshold: 0.2,
      targets: [
        // The target that actually flapped in this timeline (failed at 92m, passed at 80m) is the
        // one the lens names. lastPassMs is the fix run's instant, not an invented date.
        {
          project: "services/identity",
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
          project: "apps/dashboard",
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
// SCENARIO_CATALOG is the set of project:target pairs the live showcase schedules work from.
//
// It is deliberately WIDER than the handful of targets the scripted timeline above names. Those
// eight carry the story a reader follows across surfaces and must stay exactly as they are - the
// log viewer, the run tree and the trail all cross-reference them. But a workspace that only ever
// runs eight things does not look like a monorepo, and the dashboard is where that shows: an
// eight-slot pool with one cube lit, a timeline with one row, and a "2 workspaces" board conveyed a
// toy, not a tool built for a repo nobody can hold in their head.
//
// So the catalog spans the whole twelve-project tree and the full spread of target kinds - the fast
// ones that mostly land as cache hits (typecheck, lint, generate), the slow ones that dominate a
// wave (e2e, image-build, test), and the ones in between. The durations matter as much as the
// names: they are sized to the work, so a typecheck is a couple of seconds and an image build or a
// browser suite is tens of seconds. A scheduler drawing from a uniform catalog produces a uniform
// timeline, and real build waves are ragged, with a couple of long poles and a lot of short work
// finishing underneath them.
//
// `flaky` marks targets allowed to fail in the live scheduler. Confining failure to a named few
// mirrors how a real repo behaves - the same handful of suites are the ones that break - and keeps
// the volatility lens, the failing deep-link and the run history telling the same story. The one
// the scripted timeline actually broke, services/identity:test, is one of them.
export const SCENARIO_CATALOG: {
  project: string;
  target: string;
  durMs: number;
  flaky?: boolean;
}[] = [
  // The story targets, unchanged: every other surface's fixture references these by name.
  { project: "services/identity", target: "build", durMs: 2100 },
  { project: "services/identity", target: "test", durMs: 3900, flaky: true },
  { project: "services/identity", target: "generate", durMs: 1200 },
  { project: "apps/dashboard", target: "build", durMs: 4800 },
  { project: "apps/dashboard", target: "typecheck", durMs: 2400 },
  { project: "libs/authkit", target: "build", durMs: 1600 },
  { project: "libs/authkit", target: "lint", durMs: 640 },
  { project: ".", target: "lint", durMs: 3100 },
  // The rest of the monorepo: enough breadth that a wave fills the pool and the timeline reads as
  // a real dependency-ordered sweep rather than a queue of one.
  { project: "libs/authkit", target: "test", durMs: 2600 },
  { project: "libs/protocol", target: "generate", durMs: 1400 },
  { project: "libs/protocol", target: "lint", durMs: 800 },
  { project: "libs/ui", target: "build", durMs: 3700 },
  { project: "libs/ui", target: "test", durMs: 2900 },
  { project: "services/gateway", target: "build", durMs: 1900 },
  { project: "services/gateway", target: "test", durMs: 6100 },
  { project: "services/gateway", target: "image-build", durMs: 9800 },
  { project: "services/ledger", target: "build", durMs: 2600 },
  { project: "services/ledger", target: "test", durMs: 7400, flaky: true },
  { project: "services/ledger", target: "image-build", durMs: 11200 },
  { project: "services/catalog", target: "build", durMs: 3300 },
  { project: "services/catalog", target: "test", durMs: 4700 },
  { project: "apps/admin", target: "build", durMs: 5100 },
  { project: "apps/admin", target: "typecheck", durMs: 1800 },
  { project: "apps/admin", target: "e2e", durMs: 14600, flaky: true },
  { project: "tools/migrate", target: "build", durMs: 2200 },
  { project: "tools/migrate", target: "test", durMs: 3400 },
  { project: "docs", target: "generate", durMs: 1100 },
  { project: "docs", target: "lint", durMs: 700 },
];
