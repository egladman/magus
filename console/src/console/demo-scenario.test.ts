// demo-scenario.test.ts - the shared demo scenario is the single source of truth every surface's
// showcase derives from, so its internal cross-references (the ones that make the surfaces
// corroborate each other) are worth pinning: refs that the trail points at must exist in the run
// list, the pruned ref must NOT, run-driving MCP calls must sit just before their runs and agree on
// duration, and all instants must derive from the injected `now`. Pure data, no DOM: runs under node.

import { test } from "node:test";
import assert from "node:assert/strict";
import { must } from "../lib/guards";
import {
  scenarioRuns,
  scenarioActivity,
  scenarioInsight,
  PRUNED_REF,
  WORKSPACE,
  INV_BUILD_FRESH,
  INV_TEST_FIX,
  INV_TEST_BREAK,
  INV_CI,
} from "./demo-scenario";

const NOW = 1_760_000_000_000; // a fixed anchor; every assertion is relative to it

test("runs are newest-first and span the last ~3 hours", () => {
  const runs = scenarioRuns(NOW);
  assert.ok(runs.length >= 8 && runs.length <= 12, "8-12 runs");
  for (let i = 1; i < runs.length; i++) {
    assert.ok(runs[i - 1].endMs >= runs[i].endMs, "endMs is descending (newest first)");
  }
  const oldest = runs[runs.length - 1];
  const ageMin = (NOW - oldest.endMs) / 60_000;
  assert.ok(ageMin > 150 && ageMin < 200, "the CI sweep is ~3h back");
  // Every run's window is internally consistent.
  for (const r of runs) {
    assert.equal(r.endMs - r.startMs, r.durationMs, r.ref + " window matches duration");
    assert.equal(r.state === "failed", r.state === "failed"); // failed runs carry an error
    if (r.state === "failed") assert.ok(r.error, r.ref + " failed run has an error");
  }
});

test("run refs are unique and the CI sweep shares one invocation", () => {
  const runs = scenarioRuns(NOW);
  const refs = new Set(runs.map((r) => r.ref));
  assert.equal(refs.size, runs.length, "no duplicate refs");
  const ci = runs.filter((r) => r.inv === INV_CI);
  assert.ok(ci.length >= 3, "the sweep is several targets under one invocation");
  assert.ok(
    ci.some((r) => r.state === "cached"),
    "the sweep is mostly cache hits",
  );
  assert.ok(
    ci.every((r) => r.state !== "failed"),
    "the sweep is green",
  );
});

test("the pruned ref is deliberately absent from the run list", () => {
  const runs = scenarioRuns(NOW);
  assert.ok(!runs.some((r) => r.ref === PRUNED_REF), "PRUNED_REF never resolves to a run");
});

test("activity is newest-first, all in the scenario workspace", () => {
  const events = scenarioActivity(NOW);
  for (let i = 1; i < events.length; i++) {
    assert.ok(events[i - 1].timeMs >= events[i].timeMs, "timeMs is descending");
  }
  for (const e of events) assert.equal(e.workspace, WORKSPACE);
});

test("each run-driving MCP call ties to its run by ref, timing, and duration", () => {
  const runs = scenarioRuns(NOW);
  const byInv = (inv: string) => must(runs.find((r) => r.inv === inv));
  const events = scenarioActivity(NOW);
  const runCalls = events.filter((e) => e.kind === "mcp" && e.action === "magus_run_target");
  assert.equal(runCalls.length, 3, "fresh build, the fix, and the break");

  for (const [inv, ok] of [
    [INV_BUILD_FRESH, true],
    [INV_TEST_FIX, true],
    [INV_TEST_BREAK, false],
  ] as const) {
    const run = byInv(inv);
    const call = runCalls.find((c) => c.responseRef === run.ref);
    assert.ok(call, "a call points at " + run.ref);
    assert.equal(call.ok, ok, run.ref + " outcome matches the run");
    // The call is recorded at the run's START, so it sits just before the run's completion.
    assert.equal(call.timeMs, run.startMs, "call time is the run start");
    assert.ok(call.timeMs < run.endMs, "call sits before the run's completion timestamp");
    assert.equal(call.durationMs, run.durationMs, "durations agree across surfaces");
  }
});

test("the failed output lookup names the pruned ref; the sandbox denial mirrors the failed run", () => {
  const runs = scenarioRuns(NOW);
  const events = scenarioActivity(NOW);
  const lookup = events.find((e) => e.action === "magus_output");
  assert.ok(lookup && !lookup.ok, "the lookup failed");
  assert.match(must(lookup.error), new RegExp(PRUNED_REF), "it names the pruned ref");

  const denial = events.find((e) => e.kind === "sandbox");
  const generate = runs.find((r) => r.target === "generate");
  assert.ok(denial && generate, "both the denial and the denied run exist");
  assert.equal(generate.state, "failed");
  // Same instant and same story on both surfaces.
  assert.equal(denial.timeMs, generate.endMs);
  assert.match(must(generate.error), /sandbox/);
});

test("insight names scenario files, dates derive from now, and svc/api:test is the volatile one", () => {
  const si = scenarioInsight(NOW);
  assert.ok(
    si.hotspots.some((h) => h.name === "lib/core/users.go"),
    "the edit that broke the test leads",
  );
  for (const h of si.hotspots) {
    assert.ok(
      h.lastCommitMs <= NOW && NOW - h.lastCommitMs < 4 * 60 * 60_000,
      "commit instants derive from now",
    );
  }
  const flaky = si.volatility.targets.find((t) => t.volatile);
  assert.ok(
    flaky && flaky.project === "svc/api" && flaky.target === "test",
    "svc/api:test is volatile",
  );
  assert.ok(flaky.lastPassMs <= NOW, "last pass derives from now");
});
