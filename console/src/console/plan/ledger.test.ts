// ledger.test.ts - the delegation ledger's model. Every function under test is pure and DOM-free
// (main.ts keeps the SVG, the poll and the keyboard on the other side of the file boundary), so it
// runs directly under node with no happy-dom.
//
// What is worth pinning here is the READING the model produces, not its shape: whether a plan a
// person typed by hand still draws (a duplicate id, a self-parent, a parent that is not in the
// table, a cycle), whether no_return survives as its own state all the way to the overview line,
// and whether a missing endpoint stays distinguishable from an empty plan. Each of those fails
// SILENTLY - a plausible-looking plan that says the wrong thing is worse than an empty panel, and
// the worst of them reads "nothing was delegated" when the truth is "nobody asked".

import { test } from "node:test";
import assert from "node:assert/strict";
import type { ActivityRow } from "../activityDrawer";
import {
  ageLabel,
  buildPlan,
  isStale,
  joinRuns,
  layoutPlan,
  loadLedger,
  normalizeState,
  overviewLine,
  parseOverlaps,
  parseUnits,
  treeOrder,
  STALE_AFTER_MS,
  type DelegationUnit,
} from "./ledger";

function unit(partial: Partial<DelegationUnit> & { id: string }): DelegationUnit {
  return { state: "declared", ...partial };
}

function row(partial: Partial<ActivityRow> & { id: string }): ActivityRow {
  return { title: partial.id, detail: "", atMs: 0, outcome: "", ...partial };
}

// The plan every assembly test reads: a root with two children, one of which has a child of its
// own, plus one ordering constraint that is NOT a parent link (b2 must wait for b1).
const TREE: DelegationUnit[] = [
  unit({ id: "root", goal: "ship the surface" }),
  unit({ id: "b1", parent: "root", state: "pass" }),
  unit({ id: "b2", parent: "root", state: "running", depends_on: ["b1"] }),
  unit({ id: "b2a", parent: "b2", state: "no_return", read_only: true }),
];

// ---- assembly --------------------------------------------------------------

test("parents, children and depth come out of a flat unit list", () => {
  const plan = buildPlan(TREE);
  assert.deepEqual(plan.roots, ["root"]);
  assert.deepEqual(plan.byId.get("root")?.children, ["b1", "b2"]);
  assert.deepEqual(plan.byId.get("b2")?.children, ["b2a"]);
  assert.deepEqual(
    plan.nodes.map((n) => [n.id, n.depth]),
    [
      ["root", 0],
      ["b1", 1],
      ["b2", 1],
      ["b2a", 2],
    ],
  );
});

test("the tree reads parents before children, which is the list's reading order", () => {
  assert.deepEqual(treeOrder(buildPlan(TREE)), ["root", "b1", "b2", "b2a"]);
});

// The two edge kinds answer different questions - who spawned whom, and what has to finish first -
// and the drawing has to be able to tell them apart. One list carrying both keeps the layout and
// the renderer reading the identical set.
test("parent edges and depends_on edges are both present and stay labelled", () => {
  const plan = buildPlan(TREE);
  assert.deepEqual(
    plan.edges.map((e) => [e.kind, e.from, e.to]),
    [
      ["parent", "root", "b1"],
      ["parent", "root", "b2"],
      ["depends_on", "b1", "b2"],
      ["parent", "b2", "b2a"],
    ],
  );
});

// A ledger is a table a person or an agent keeps by hand, so it arrives with the mistakes those
// produce. None of them may cost the reader the whole picture.
test("a unit naming a parent the ledger does not carry is drawn as a root, and reported", () => {
  const plan = buildPlan([unit({ id: "orphan", parent: "somewhere-else" })]);
  assert.deepEqual(plan.roots, ["orphan"]);
  assert.deepEqual(plan.dangling, ["orphan"]);
  assert.equal(plan.byId.get("orphan")?.parent, null);
  assert.equal(
    plan.byId.get("orphan")?.danglingParent,
    "somewhere-else",
    "the name it gave is kept, so the detail can say what is missing rather than pretending it is a root",
  );
  assert.equal(plan.edges.length, 0, "an edge needs two endpoints");
});

test("a self-parent is a root, not a loop", () => {
  const plan = buildPlan([unit({ id: "a", parent: "a" })]);
  assert.deepEqual(plan.roots, ["a"]);
  assert.equal(
    plan.byId.get("a")?.danglingParent,
    "",
    "it named itself, which is a typo, not a gap",
  );
  assert.equal(plan.edges.length, 0);
});

test("a parent cycle flattens instead of hanging, and every unit still appears", () => {
  const plan = buildPlan([unit({ id: "a", parent: "b" }), unit({ id: "b", parent: "a" })]);
  assert.deepEqual(
    plan.nodes.map((n) => n.depth),
    [0, 0],
  );
  assert.deepEqual(
    treeOrder(plan).sort(),
    ["a", "b"],
    "a cycle must not drop a unit from the list",
  );
});

test("a duplicate id keeps the first row", () => {
  const plan = buildPlan([unit({ id: "a", goal: "first" }), unit({ id: "a", goal: "second" })]);
  assert.equal(plan.nodes.length, 1);
  assert.equal(plan.byId.get("a")?.unit.goal, "first");
});

test("a depends_on naming a unit outside the ledger draws no edge and is not invented", () => {
  const plan = buildPlan([unit({ id: "a", depends_on: ["ghost", "a"] })]);
  assert.equal(plan.edges.length, 0);
  assert.deepEqual(
    plan.byId.get("a")?.unit.depends_on,
    ["ghost", "a"],
    "the raw list survives so the detail can show what was actually written",
  );
});

// ---- states ----------------------------------------------------------------

test("an unrecognized state reads as declared and keeps what the ledger said", () => {
  const plan = buildPlan([unit({ id: "a", state: "cancelled" })]);
  const node = plan.byId.get("a");
  assert.equal(node?.state, "declared", "nothing unknown may ever read as a pass or a fail");
  assert.equal(node?.rawState, "cancelled");
});

test("no_return is its own state and is never folded into fail", () => {
  assert.equal(normalizeState("no_return"), "no_return");
  const plan = buildPlan([unit({ id: "a", state: "no_return" }), unit({ id: "b", state: "fail" })]);
  assert.equal(plan.counts.no_return, 1);
  assert.equal(plan.counts.fail, 1);
});

// ---- the overview line -----------------------------------------------------

test("the overview counts the units, breaks down the states, and calls out no-return", () => {
  assert.equal(
    overviewLine(buildPlan(TREE)),
    "4 units - 1 declared, 1 running, 1 pass - 1 no-return",
  );
});

// A call-out that disappears at zero is a call-out a reader stops watching for.
test("the no-return call-out is present even when it is zero", () => {
  assert.equal(
    overviewLine(buildPlan([unit({ id: "a", state: "pass" })])),
    "1 unit - 1 pass - 0 no-return",
  );
  assert.equal(overviewLine(buildPlan([])), "0 units - 0 no-return");
});

// ---- the heartbeat ---------------------------------------------------------

// A row is re-put on every state change, so the gap since the last one is the only evidence this
// surface has that a worker is still there. What the gap MEANS stays the reader's call - nothing
// here transitions a unit, and no-return is never inferred.
test("a row nobody has touched past the threshold is stale; a fresh one is not", () => {
  const now = 1_755_300_000_000;
  const sec = (msAgo: number): number => (now - msAgo) / 1000;
  assert.equal(isStale(false, sec(STALE_AFTER_MS + 1000), now), true);
  assert.equal(isStale(false, sec(STALE_AFTER_MS - 1000), now), false);
});

test("a finished row is never stale, and neither is one carrying no timestamp", () => {
  const now = 1_755_300_000_000;
  const long = (now - STALE_AFTER_MS * 10) / 1000;
  assert.equal(isStale(true, long, now), false, "a unit that finished is not going to be touched");
  assert.equal(
    isStale(false, 0, now),
    false,
    "an unstamped row is a daemon fact, not a dead worker",
  );
});

test("age reads at the coarsest granularity that still answers the question", () => {
  const now = 1_755_300_000_000;
  const ago = (secs: number): number => now / 1000 - secs;
  assert.equal(ageLabel(ago(9), now), "9s");
  assert.equal(ageLabel(ago(150), now), "2m");
  assert.equal(ageLabel(ago(7200), now), "2h");
  assert.equal(ageLabel(ago(200000), now), "2d");
  assert.equal(ageLabel(0, now), "", "no timestamp renders nothing rather than a confident zero");
});

// ---- the live join ---------------------------------------------------------

test("runs join onto the unit they name, in the order they were fed", () => {
  const plan = buildPlan(TREE);
  const join = joinRuns(plan, [
    row({ id: "inv1", unit: "b2", title: "magus run test" }),
    row({ id: "out9", unit: "b2", title: "console:test", outcome: "pass" }),
    row({ id: "inv2", unit: "b1" }),
  ]);
  assert.deepEqual(
    join.byUnit.get("b2")?.map((r) => r.id),
    ["inv1", "out9"],
  );
  assert.deepEqual(
    join.byUnit.get("b1")?.map((r) => r.id),
    ["inv2"],
  );
  assert.equal(
    join.byUnit.has("root"),
    false,
    "a unit with no runs gets no bucket, not an empty one",
  );
});

// This is the whole state of the world today: nothing stamps a unit onto the activity feeds yet.
// Treating that as evidence of a stale ledger would make the warning permanent and worthless.
test("a run naming no unit is unattributed, not unmatched", () => {
  const join = joinRuns(buildPlan(TREE), [row({ id: "inv1" }), row({ id: "inv2", unit: "" })]);
  assert.equal(join.byUnit.size, 0);
  assert.deepEqual(join.unmatched, []);
});

test("a run naming a unit this ledger does not carry is unmatched, which means the plan is stale", () => {
  const join = joinRuns(buildPlan(TREE), [row({ id: "inv1", unit: "b3" })]);
  assert.deepEqual(
    join.unmatched.map((r) => r.id),
    ["inv1"],
  );
});

// ---- placement -------------------------------------------------------------

test("a child is placed to the right of its parent, and a dependent right of its dependency", () => {
  const plan = buildPlan(TREE);
  const at = layoutPlan(plan).at;
  const x = (id: string): number => at.get(id)?.x ?? Number.NaN;
  assert.ok(x("root") < x("b1"), "a child cannot start before the parent that delegated it");
  assert.ok(x("b1") < x("b2"), "b2 waits on b1, so it sits downstream of it");
  assert.ok(x("b2") < x("b2a"));
});

test("placement is deterministic - the same plan lays out identically", () => {
  const a = layoutPlan(buildPlan(TREE));
  const b = layoutPlan(buildPlan(TREE));
  assert.equal(a.viewBox, b.viewBox);
  assert.deepEqual([...a.at.entries()], [...b.at.entries()]);
});

test("an empty plan lays out without a NaN viewBox", () => {
  const layout = layoutPlan(buildPlan([]));
  assert.equal(layout.viewBox, "0 0 1 1");
  assert.equal(layout.at.size, 0);
});

// A cycle in depends_on is a planning mistake worth SEEING. The layout breaks it by reversing one
// edge, and reports which, so the renderer can mark the reversal as its own fiction.
test("a depends_on cycle is broken and the reversed edge is reported", () => {
  const plan = buildPlan([
    unit({ id: "a", depends_on: ["b"] }),
    unit({ id: "b", depends_on: ["a"] }),
  ]);
  const layout = layoutPlan(plan);
  assert.equal(layout.back.size, 1, "exactly one of the two edges is layout fiction");
  assert.equal(layout.at.size, 2);
});

// ---- parsing ---------------------------------------------------------------

test("parseUnits keeps the documented fields and drops a row with no id", () => {
  const units = parseUnits({
    units: [
      {
        id: "u1",
        parent: "root",
        goal: "g",
        checkpoint: "c",
        owned_paths: ["a.ts", 7],
        forbidden_paths: [],
        depends_on: ["u0"],
        tier: "sonnet",
        validation: "console:test",
        state: "running",
        read_only: true,
        releases: [
          { path: "b.ts", digest: "sha256:abc", released_at: 1755300000 },
          { digest: "sha256:def" },
        ],
        created: 1755299000,
        updated: 1755300000,
      },
      { goal: "no id here" },
      null,
    ],
  });
  assert.equal(units.length, 1);
  assert.deepEqual(
    units[0]?.owned_paths,
    ["a.ts"],
    "a non-string path is dropped, not stringified",
  );
  assert.equal(units[0]?.read_only, true);
  assert.equal(units[0]?.tier, "sonnet");
  // Unix seconds, as the route serves them. Read as strings they were silently discarded, and a
  // surface that cannot read this field cannot tell a fresh row from an abandoned one.
  assert.equal(units[0]?.updated, 1755300000);
  assert.equal(units[0]?.created, 1755299000);
  assert.deepEqual(
    units[0]?.releases,
    [{ path: "b.ts", digest: "sha256:abc", released_at: 1755300000 }],
    "a release naming no path points at nothing a reader can open",
  );
});

test("parseOverlaps keeps the pairs it can draw and drops the ones it cannot", () => {
  assert.deepEqual(
    parseOverlaps({
      overlaps: [
        { a: "u1", b: "u2", paths: ["internal/ledger", 7] },
        { a: "u1", b: "u1", paths: [] },
        { a: "u1" },
        null,
      ],
    }),
    [{ a: "u1", b: "u2", paths: ["internal/ledger"] }],
  );
  // A daemon predating the field sends none, which is an absence and not a failure.
  assert.deepEqual(parseOverlaps({ units: [] }), []);
  assert.deepEqual(parseOverlaps(null), []);
});

// The warning belongs on BOTH rows: either one is where a reader might be standing when they need
// to know the other exists.
test("an overlap lands on both units, carrying the other id and the paths", () => {
  const plan = buildPlan(
    [unit({ id: "a", state: "running" }), unit({ id: "b", state: "running" })],
    [{ a: "a", b: "b", paths: ["internal/ledger"] }],
  );
  assert.equal(plan.byId.get("a")?.overlaps.length, 1);
  assert.deepEqual(plan.byId.get("b")?.overlaps[0]?.paths, ["internal/ledger"]);
  assert.equal(plan.overlaps.length, 1);
});

test("an overlap naming a unit this ledger does not carry is dropped, not half-drawn", () => {
  const plan = buildPlan([unit({ id: "a" })], [{ a: "a", b: "ghost", paths: ["x"] }]);
  assert.deepEqual(plan.byId.get("a")?.overlaps, []);
  assert.deepEqual(plan.overlaps, []);
});

test("a body that is not the documented shape yields no units rather than throwing", () => {
  assert.deepEqual(parseUnits(null), []);
  assert.deepEqual(parseUnits({}), []);
  assert.deepEqual(parseUnits({ units: "nope" }), []);
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

// The reason this distinction exists: on every daemon that predates the ledger, GET /api/v1/ledger
// 404s. Reporting that as an empty plan would tell a reader nothing was delegated.
test("a 404 is absent - the route is missing, not the plan", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 404 }),
    () => loadLedger("127.0.0.1:7391"),
  );
  assert.deepEqual(read, { kind: "absent" });
});

test("a 501 is absent too - an unimplemented route is a missing one", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 501 }),
    () => loadLedger("127.0.0.1:7391"),
  );
  assert.equal(read.kind, "absent");
});

test("any other failure is unreadable, and says which", async () => {
  const read = await stubFetch(
    () => ({ ok: false, status: 500 }),
    () => loadLedger("127.0.0.1:7391"),
  );
  assert.deepEqual(read, { kind: "unreadable", detail: "HTTP 500" });
});

test("a refused connection is unreadable rather than absent", async () => {
  const real = globalThis.fetch;
  globalThis.fetch = (() => Promise.reject(new Error("Failed to fetch"))) as typeof fetch;
  try {
    const read = await loadLedger("127.0.0.1:7391");
    assert.deepEqual(read, { kind: "unreadable", detail: "Failed to fetch" });
  } finally {
    globalThis.fetch = real;
  }
});

test("a served ledger comes back parsed, overlaps included", async () => {
  const read = await stubFetch(
    () => ({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          units: [{ id: "root" }, { id: "b1" }],
          overlaps: [{ a: "root", b: "b1", paths: ["internal/ledger"] }],
        }),
    }),
    () => loadLedger("127.0.0.1:7391"),
  );
  assert.equal(read.kind, "ok");
  assert.deepEqual(read.kind === "ok" ? read.units.map((u) => u.id) : [], ["root", "b1"]);
  assert.deepEqual(read.kind === "ok" ? read.overlaps.map((o) => o.b) : [], ["b1"]);
});
