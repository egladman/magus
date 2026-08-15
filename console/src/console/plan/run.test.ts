// run.test.ts - the derived run plan's model. Every function under test is pure and DOM-free
// (main.ts keeps the SVG, the poll and the keyboard on the other side of the file boundary), so it
// runs directly under node with no happy-dom.
//
// What is worth pinning here is the READING the model produces, not its shape. Three of these fail
// SILENTLY, and each produces a plausible-looking picture that says the wrong thing:
//
//   - A state this console does not recognize must never read as a pass or a fail.
//   - An anchor it does not recognize must never read as "following the running ...". The whole
//     value of this view is that a reader can trust it is live when it says it is.
//   - A missing endpoint must stay distinguishable from a workspace where nothing has run.

import { test } from "node:test";
import assert from "node:assert/strict";
import { layoutPlan } from "./ledger";
import {
  anchorPhrase,
  emptyRunPlan,
  loadRunPlan,
  normalizeAnchor,
  normalizeRunState,
  parseRunPlan,
  runOverviewLine,
  runPlanUrl,
} from "./run";

// The plan every assembly test reads, in the contract's exact shape: lint and build both wait on
// generate, and test waits on build. from is the DEPENDENCY, to is the DEPENDENT.
const BODY = {
  target: "ci",
  anchor: "running",
  nodes: [
    { id: ".:generate", project: ".", target: "generate", state: "pass", ref: "out1a2b3c" },
    { id: ".:lint", project: ".", target: "lint", state: "pass", ref: "out4d5e6f" },
    { id: ".:build", project: ".", target: "build", state: "running", ref: "" },
    { id: "console:test", project: "console", target: "test", state: "idle", ref: "" },
  ],
  edges: [
    { from: ".:generate", to: ".:lint" },
    { from: ".:generate", to: ".:build" },
    { from: ".:build", to: "console:test" },
  ],
};

// ---- assembly --------------------------------------------------------------

test("the contract's node list comes back whole, in served order", () => {
  const plan = parseRunPlan(BODY);
  assert.equal(plan.target, "ci");
  assert.equal(plan.anchor, "running");
  assert.deepEqual(
    plan.nodes.map((n) => [n.id, n.project, n.target, n.state]),
    [
      [".:generate", ".", "generate", "pass"],
      [".:lint", ".", "lint", "pass"],
      [".:build", ".", "build", "running"],
      ["console:test", "console", "test", "idle"],
    ],
  );
  assert.equal(plan.byId.get(".:generate")?.ref, "out1a2b3c");
  assert.equal(plan.byId.get(".:build")?.ref, "", "no ref is an empty string, never undefined");
});

test("the counts break down by state", () => {
  assert.deepEqual(parseRunPlan(BODY).counts, { idle: 1, running: 1, pass: 2, fail: 0 });
});

test("edges keep the contract's orientation - from is the dependency", () => {
  assert.deepEqual(
    parseRunPlan(BODY).edges.map((e) => [e.from, e.to]),
    [
      [".:generate", ".:lint"],
      [".:generate", ".:build"],
      [".:build", "console:test"],
    ],
  );
});

// A response is parsed from the network, so it arrives with whatever a daemon (or a proxy, or a
// partial write) produced. None of it may cost the reader the whole picture.
test("a node with no id is dropped rather than drawn as something no edge can reach", () => {
  const plan = parseRunPlan({ nodes: [{ id: "" }, { project: "." }, null, "nope", { id: "a" }] });
  assert.deepEqual(
    plan.nodes.map((n) => n.id),
    ["a"],
  );
});

test("a duplicate id keeps the first node", () => {
  const plan = parseRunPlan({
    nodes: [
      { id: "a", project: "first" },
      { id: "a", project: "second" },
    ],
  });
  assert.equal(plan.nodes.length, 1);
  assert.equal(plan.byId.get("a")?.project, "first");
});

test("an edge needs two endpoints that are both here", () => {
  const plan = parseRunPlan({
    nodes: [{ id: "a" }, { id: "b" }],
    edges: [
      { from: "a", to: "b" },
      { from: "a", to: "ghost" },
      { from: "ghost", to: "b" },
      { from: "a", to: "a" },
      { from: "", to: "b" },
      null,
    ],
  });
  assert.deepEqual(
    plan.edges.map((e) => [e.from, e.to]),
    [["a", "b"]],
    "a self-edge would be a loop in the layout, and a half-edge has nothing to draw to",
  );
});

test("a body that is not the documented shape yields an empty plan rather than throwing", () => {
  for (const body of [null, undefined, {}, { nodes: "nope" }, { nodes: [], edges: "nope" }]) {
    const plan = parseRunPlan(body);
    assert.equal(plan.nodes.length, 0);
    assert.equal(plan.edges.length, 0);
  }
  assert.equal(emptyRunPlan().nodes.length, 0);
});

// ---- states ----------------------------------------------------------------

test("an unrecognized state reads as idle and keeps what the daemon said", () => {
  const plan = parseRunPlan({ nodes: [{ id: "a", state: "skipped" }] });
  const node = plan.byId.get("a");
  assert.equal(node?.state, "idle", "nothing unknown may ever read as a pass or a fail");
  assert.equal(node?.rawState, "skipped");
});

// no_return belongs to the delegation ledger alone: it is what a worker that never came back
// leaves behind, and an engine that resolved a DAG always knows what happened to every node.
test("no_return is not a state this plan has - it reads as idle like any other unknown", () => {
  assert.equal(normalizeRunState("no_return"), "idle");
  assert.equal(normalizeRunState(undefined), "idle");
  assert.equal(normalizeRunState(7), "idle");
  assert.equal(normalizeRunState("fail"), "fail");
});

// The wire carries exactly four states. "queued" is the one a reader of the pool surfaces would
// most plausibly expect to find here and the one an implementation would most plausibly add: the
// engine resolves running over a stale pass server-side, so waiting-to-start arrives as idle.
test("there is no queued on the wire - it reads as idle", () => {
  assert.equal(normalizeRunState("queued"), "idle");
  assert.deepEqual(parseRunPlan({ nodes: [{ id: "a", state: "queued" }] }).counts, {
    idle: 1,
    running: 0,
    pass: 0,
    fail: 0,
  });
});

// ---- the anchor ------------------------------------------------------------

test("an unrecognized anchor reads as default, never as following something live", () => {
  assert.equal(normalizeAnchor("queued"), "default");
  assert.equal(normalizeAnchor(undefined), "default");
  assert.equal(normalizeAnchor("running"), "running");
  const plan = parseRunPlan({ target: "ci", anchor: "queued", nodes: [{ id: "a" }] });
  assert.equal(
    anchorPhrase(plan),
    "ci",
    "a claim about liveness is the one thing an unknown value must not be allowed to make",
  );
});

test("the anchor phrase says how the daemon chose, in the daemon's four ways", () => {
  const at = (anchor: string): string => anchorPhrase(parseRunPlan({ target: "ci", anchor }));
  assert.equal(at("running"), "following the running ci");
  assert.equal(at("recent"), "last run: ci");
  assert.equal(at("default"), "ci");
  assert.equal(at("explicit"), "ci");
});

test("a plan whose target the daemon did not name gets no phrase rather than an invented one", () => {
  assert.equal(anchorPhrase(parseRunPlan({ anchor: "running", nodes: [{ id: "a" }] })), "");
  assert.equal(runOverviewLine(parseRunPlan({ nodes: [{ id: "a" }] })), "1 target - 0 fail");
});

// ---- the overview line -----------------------------------------------------

// The line LEADS with the anchor because that is the first thing a reader needs: whether they are
// watching work happen or reading a record of work that finished.
test("the overview leads with the anchor, then counts the targets and breaks down the states", () => {
  assert.equal(
    runOverviewLine(parseRunPlan(BODY)),
    "following the running ci - 4 targets - 1 running, 2 pass - 0 fail",
  );
});

// A call-out that disappears at zero is a call-out a reader stops watching for - the same
// discipline the ledger's no-return count follows.
test("the fail count is present even when it is zero", () => {
  assert.equal(
    runOverviewLine(parseRunPlan({ target: "ci", anchor: "recent", nodes: [{ id: "a" }] })),
    "last run: ci - 1 target - 0 fail",
  );
});

// On a plan nobody has started every node is idle, so counting them would just restate the total.
test("idle is not counted in the line, but fail and running are", () => {
  const plan = parseRunPlan({
    target: "ci",
    anchor: "default",
    nodes: [{ id: "a" }, { id: "b" }, { id: "c", state: "fail" }, { id: "d", state: "running" }],
  });
  assert.equal(runOverviewLine(plan), "ci - 4 targets - 1 running - 1 fail");
});

// ---- placement -------------------------------------------------------------

// The run plan places through the SAME layered pass the ledger view does, which is the whole point
// of the second tenant: two sources, one drawing.
test("a dependent is placed to the right of what it depends on", () => {
  const at = layoutPlan(parseRunPlan(BODY)).at;
  const x = (id: string): number => at.get(id)?.x ?? Number.NaN;
  assert.ok(x(".:generate") < x(".:lint"), "lint waits on generate, so it sits downstream of it");
  assert.ok(x(".:generate") < x(".:build"));
  assert.ok(x(".:build") < x("console:test"));
});

test("an empty plan lays out without a NaN viewBox", () => {
  const layout = layoutPlan(emptyRunPlan());
  assert.equal(layout.viewBox, "0 0 1 1");
  assert.equal(layout.at.size, 0);
});

// The plan carries TWO kinds of edge on ONE undifferentiated wire: the steps within a project, and
// the project-level ordering that runs one project's anchor after another's. Neither carries a kind
// field, and nothing here may invent one. Dropping the second is the failure that looks most like a
// working drawing - the plan renders as one disconnected island per project, still laid out, still
// plausible, and silently missing the constraint that makes it a build order.
test("project-level ordering edges survive beside the intra-project steps", () => {
  const plan = parseRunPlan({
    target: "ci",
    anchor: "explicit",
    nodes: [
      { id: "libs/api:ci", project: "libs/api", target: "ci", state: "pass" },
      { id: "libs/api:build", project: "libs/api", target: "build", state: "pass" },
      { id: "app:ci", project: "app", target: "ci", state: "running" },
      { id: "app:build", project: "app", target: "build", state: "pass" },
    ],
    edges: [
      { from: "libs/api:build", to: "libs/api:ci" },
      { from: "libs/api:ci", to: "app:ci" },
      { from: "app:build", to: "app:ci" },
    ],
  });
  assert.equal(plan.edges.length, 3, "the anchor-to-anchor edge is an ordinary edge, not a kind");
  const at = layoutPlan(plan).at;
  const x = (id: string): number => at.get(id)?.x ?? Number.NaN;
  assert.ok(
    x("libs/api:ci") < x("app:ci"),
    "one project's anchor runs after another's, and the drawing has to place it that way",
  );
  assert.ok(x("libs/api:build") < x("libs/api:ci"));
});

// ---- the URL ---------------------------------------------------------------

// The bare URL is what tells the daemon to pick the anchor itself. Naming a target is an override,
// and sending one by accident would silently turn a live view into a browse of a fixed target.
test("following names no target; overriding names one, encoded", () => {
  assert.equal(runPlanUrl("127.0.0.1:7391", ""), "http://127.0.0.1:7391/api/v1/plan");
  assert.equal(runPlanUrl("127.0.0.1:7391", "ci"), "http://127.0.0.1:7391/api/v1/plan?target=ci");
  assert.equal(
    runPlanUrl("127.0.0.1:7391", "ci:rw"),
    "http://127.0.0.1:7391/api/v1/plan?target=ci%3Arw",
  );
});

// ---- the read --------------------------------------------------------------

// stubFetch swaps the global fetch for one call and restores it. The suite runs with
// --experimental-test-isolation=none (one process for every file), so a stub left behind would be
// another file's network.
async function stubFetch<T>(impl: () => unknown, body: () => Promise<T>): Promise<T> {
  const real = globalThis.fetch;
  globalThis.fetch = (() => Promise.resolve(impl() as Response)) as typeof fetch;
  try {
    return await body();
  } finally {
    globalThis.fetch = real;
  }
}

// The reason this distinction exists: on every daemon that predates the run plan, GET /api/v1/plan
// 404s. Reporting that as an empty plan would tell a reader nothing has run.
test("a 404 is absent - the route is missing, not the plan", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 404 }),
    () => loadRunPlan("127.0.0.1:7391", ""),
  );
  assert.deepEqual(read, { kind: "absent" });
});

test("a 501 is absent too - an unimplemented route is a missing one", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 501 }),
    () => loadRunPlan("127.0.0.1:7391", ""),
  );
  assert.equal(read.kind, "absent");
});

// The console holds no list of the workspace's targets, so anything it wrote here itself would be a
// guess. The daemon named what it could not resolve, and that sentence is what a reader can act on.
// The route writes it with http.Error, so the body IS the message: plain text, taken verbatim.
test("a 400 is an unknown target, carrying the daemon's own words", async () => {
  const read = await stubFetch(
    () => ({
      ok: false,
      status: 400,
      text: () =>
        Promise.resolve('  unknown target "cli"; run `magus describe targets` to list them\n'),
    }),
    () => loadRunPlan("127.0.0.1:7391", "cli"),
  );
  assert.deepEqual(read, {
    kind: "unknown-target",
    detail: 'unknown target "cli"; run `magus describe targets` to list them',
  });
});

test("a 400 with no body is still an unknown target, just with nothing to quote", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 400, text: () => Promise.resolve("") }),
    () => loadRunPlan("127.0.0.1:7391", "cli"),
  );
  assert.deepEqual(read, { kind: "unknown-target", detail: "" });
});

test("any other failure is unreadable, and says which", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 500 }),
    () => loadRunPlan("127.0.0.1:7391", ""),
  );
  assert.deepEqual(read, { kind: "unreadable", detail: "HTTP 500" });
});

test("a refused connection is unreadable rather than absent", async () => {
  const real = globalThis.fetch;
  globalThis.fetch = (() => Promise.reject(new Error("Failed to fetch"))) as typeof fetch;
  try {
    const read = await loadRunPlan("127.0.0.1:7391", "");
    assert.deepEqual(read, { kind: "unreadable", detail: "Failed to fetch" });
  } finally {
    globalThis.fetch = real;
  }
});

test("a served plan comes back parsed", async () => {
  const read = await stubFetch(
    () => ({ ok: true, status: 200, json: () => Promise.resolve(BODY) }),
    () => loadRunPlan("127.0.0.1:7391", ""),
  );
  assert.equal(read.kind, "ok");
  assert.equal(read.kind === "ok" ? read.plan.nodes.length : 0, 4);
  assert.equal(read.kind === "ok" ? read.plan.anchor : "", "running");
});

test("the read asks for the URL the override implies", async () => {
  const seen: string[] = [];
  const real = globalThis.fetch;
  globalThis.fetch = ((input: RequestInfo | URL) => {
    seen.push(String(input));
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as Response);
  }) as typeof fetch;
  try {
    await loadRunPlan("127.0.0.1:7391", "");
    await loadRunPlan("127.0.0.1:7391", "build");
  } finally {
    globalThis.fetch = real;
  }
  assert.deepEqual(seen, [
    "http://127.0.0.1:7391/api/v1/plan",
    "http://127.0.0.1:7391/api/v1/plan?target=build",
  ]);
});
