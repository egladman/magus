// jobs.test.ts - the job model. Every function under test is pure and DOM-free (main.ts keeps the
// SVG, the poll and the keyboard on the other side of the file boundary, and the two service calls
// are exercised end to end in main-dom.test.ts), so this runs directly under node with no happy-dom.
//
// What is worth pinning here is the READING the model produces, not its shape: whether a listing an
// agent wrote by hand still draws (a duplicate id, a self-parent, a parent that is not in the
// listing, a cycle), whether no_return survives as its own state all the way to the overview line,
// and whether a job the daemon is running right now can ever read as one nobody started. Each of
// those fails SILENTLY - a plausible-looking picture that says the wrong thing is worse than an
// empty panel.

import { test } from "node:test";
import assert from "node:assert/strict";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { JobHolder, JobOverlapSchema, JobSchema, type Job } from "@wire/job/v1alpha1/job_pb";
import type { ActivityRow } from "../activityDrawer";
import {
  ageLabel,
  buildJobTree,
  isStale,
  joinRuns,
  jobKey,
  lastRunLine,
  layoutNodes,
  normalizeState,
  overviewLine,
  sizeLine,
  treeOrder,
  STALE_AFTER_MS,
} from "./jobs";

function job(id: string, fields: MessageInitShape<typeof JobSchema> = {}): Job {
  return create(JobSchema, {
    name: "jobs/" + id,
    id,
    holder: JobHolder.SESSION,
    state: "declared",
    ...fields,
  });
}

function overlap(a: string, b: string, pathsA: string[], pathsB: string[]) {
  return create(JobOverlapSchema, { jobA: a, jobB: b, pathsA, pathsB });
}

function row(partial: Partial<ActivityRow> & { id: string }): ActivityRow {
  return { title: partial.id, detail: "", atMs: 0, outcome: "", ...partial };
}

// The listing every assembly test reads: a root with two children, one of which has a child of its
// own, plus one ordering constraint that is NOT a parent link (b2 must wait for b1).
const TREE: Job[] = [
  job("root", { goal: "ship the view" }),
  job("b1", { parent: "root", state: "pass" }),
  job("b2", { parent: "root", state: "running", dependsOn: ["b1"] }),
  job("b2a", { parent: "b2", state: "no_return", readOnly: true }),
];

// ---- assembly --------------------------------------------------------------

test("parents, children and depth come out of a flat job list", () => {
  const model = buildJobTree(TREE);
  assert.deepEqual(model.roots, ["root"]);
  assert.deepEqual(model.byId.get("root")?.children, ["b1", "b2"]);
  assert.deepEqual(model.byId.get("b2")?.children, ["b2a"]);
  assert.deepEqual(
    model.nodes.map((n) => [n.id, n.depth]),
    [
      ["root", 0],
      ["b1", 1],
      ["b2", 1],
      ["b2a", 2],
    ],
  );
});

test("the tree reads parents before children, which is the list's reading order", () => {
  assert.deepEqual(treeOrder(buildJobTree(TREE)), ["root", "b1", "b2", "b2a"]);
});

// The two edge kinds answer different questions - who handed out what, and what has to finish
// first - and the drawing has to be able to tell them apart. One list carrying both keeps the layout
// and the renderer reading the identical set.
test("parent edges and depends_on edges are both present and stay labeled", () => {
  const model = buildJobTree(TREE);
  assert.deepEqual(
    model.edges.map((e) => [e.kind, e.from, e.to]),
    [
      ["parent", "root", "b1"],
      ["parent", "root", "b2"],
      ["depends_on", "b1", "b2"],
      ["parent", "b2", "b2a"],
    ],
  );
});

// A job's fields are written by whoever declared it, so they arrive with the mistakes that produces.
// None of them may cost the reader the whole picture.
test("a job naming a parent this listing does not carry is drawn as a root, and reported", () => {
  const model = buildJobTree([job("orphan", { parent: "somewhere-else" })]);
  assert.deepEqual(model.roots, ["orphan"]);
  assert.deepEqual(model.dangling, ["orphan"]);
  assert.equal(model.byId.get("orphan")?.parent, null);
  assert.equal(
    model.byId.get("orphan")?.danglingParent,
    "somewhere-else",
    "the name it gave is kept, so the detail can say what is missing rather than pretending it is a root",
  );
  assert.equal(model.edges.length, 0, "an edge needs two endpoints");
});

test("a self-parent is a root, not a loop", () => {
  const model = buildJobTree([job("a", { parent: "a" })]);
  assert.deepEqual(model.roots, ["a"]);
  assert.equal(
    model.byId.get("a")?.danglingParent,
    "",
    "it named itself, which is a typo, not a gap",
  );
  assert.equal(model.edges.length, 0);
});

test("a parent cycle flattens instead of hanging, and every job still appears", () => {
  const model = buildJobTree([job("a", { parent: "b" }), job("b", { parent: "a" })]);
  assert.deepEqual(
    model.nodes.map((n) => n.depth),
    [0, 0],
  );
  assert.deepEqual(
    treeOrder(model).sort(),
    ["a", "b"],
    "a cycle must not drop a job from the list",
  );
});

test("a duplicate id keeps the first entry", () => {
  const model = buildJobTree([job("a", { goal: "first" }), job("a", { goal: "second" })]);
  assert.equal(model.nodes.length, 1);
  assert.equal(model.byId.get("a")?.job.goal, "first");
});

test("a depends_on naming a job outside this listing draws no edge and is not invented", () => {
  const model = buildJobTree([job("a", { dependsOn: ["ghost", "a"] })]);
  assert.equal(model.edges.length, 0);
  assert.deepEqual(
    model.byId.get("a")?.job.dependsOn,
    ["ghost", "a"],
    "the raw list survives so the detail can show what was actually declared",
  );
});

// The id is what a job is drawn, selected and joined by, and the daemon may send either spelling.
test("a job with no bare id falls back to the last segment of its resource name", () => {
  assert.equal(jobKey(create(JobSchema, { name: "jobs/rotate-activities" })), "rotate-activities");
  assert.equal(jobKey(create(JobSchema, { name: "jobs/x", id: "x" })), "x");
  const model = buildJobTree([create(JobSchema, { name: "jobs/clear-cache" })]);
  assert.deepEqual(
    model.nodes.map((n) => n.id),
    ["clear-cache"],
  );
});

// ---- states ----------------------------------------------------------------

test("an unrecognized state reads as declared and keeps what the daemon said", () => {
  const model = buildJobTree([job("a", { state: "cancelled" })]);
  const node = model.byId.get("a");
  assert.equal(node?.state, "declared", "nothing unknown may ever read as a pass or a fail");
  assert.equal(node?.rawState, "cancelled");
});

test("no_return is its own state and is never folded into fail", () => {
  assert.equal(normalizeState("no_return"), "no_return");
  const model = buildJobTree([job("a", { state: "no_return" }), job("b", { state: "fail" })]);
  assert.equal(model.counts.no_return, 1);
  assert.equal(model.counts.fail, 1);
});

// The daemon's own catalog reports an instance in flight on its own flag rather than in the
// lifecycle string, so a job the reader can watch working must not be drawn as one nobody started.
test("a job in flight reads as running whatever its state string says", () => {
  const model = buildJobTree([
    create(JobSchema, {
      name: "jobs/clear-cache",
      id: "clear-cache",
      holder: JobHolder.DAEMON,
      running: true,
    }),
  ]);
  assert.equal(model.byId.get("clear-cache")?.state, "running");
  assert.equal(model.byId.get("clear-cache")?.holder, JobHolder.DAEMON);
});

// ---- the overview line -----------------------------------------------------

test("the overview counts the jobs, breaks down the states, and calls out no-return", () => {
  assert.equal(
    overviewLine(buildJobTree(TREE)),
    "4 jobs. 1 declared, 1 running, 1 pass. 1 no-return.",
  );
});

// A call-out that disappears at zero is a call-out a reader stops watching for.
test("the no-return call-out is present even when it is zero", () => {
  assert.equal(
    overviewLine(buildJobTree([job("a", { state: "pass" })])),
    "1 job. 1 pass. 0 no-return.",
  );
  assert.equal(overviewLine(buildJobTree([])), "0 jobs. 0 no-return.");
});

// ---- what a daemon job carries ---------------------------------------------

test("size reads both halves when they are there, and nothing when they are not", () => {
  assert.equal(
    sizeLine(job("a", { target: { sizeBytes: 2048n, itemCount: 12n } })),
    "2.0 KB, 12 items",
  );
  assert.equal(sizeLine(job("a", { target: { itemCount: 1n } })), "1 item");
  assert.equal(sizeLine(job("a")), "", "a job that maintains nothing reports no size, not 0 B");
});

test("the last run reads its age, and says so when it failed", () => {
  const now = 1_755_300_000_000;
  const ended = (secsAgo: number): bigint => BigInt(now / 1000 - secsAgo);
  assert.equal(
    lastRunLine(job("a", { lastRun: { endTime: { seconds: ended(120) }, ok: true } }), now),
    "last run 2m ago",
  );
  assert.equal(
    lastRunLine(job("a", { lastRun: { endTime: { seconds: ended(120) } } }), now),
    "last run 2m ago (failed)",
  );
  assert.equal(
    lastRunLine(job("a"), now),
    "",
    "a job that has not run is not a job whose run failed",
  );
});

// ---- the heartbeat ---------------------------------------------------------

// A job is re-put on every state change, so the gap since the last one is the only evidence this
// view has that a worker is still there. What the gap MEANS stays the reader's call - nothing here
// transitions a job, and no-return is never inferred.
test("a job nobody has touched past the threshold is stale; a fresh one is not", () => {
  const now = 1_755_300_000_000;
  const sec = (msAgo: number): number => (now - msAgo) / 1000;
  assert.equal(isStale(false, sec(STALE_AFTER_MS + 1000), now), true);
  assert.equal(isStale(false, sec(STALE_AFTER_MS - 1000), now), false);
});

test("a finished job is never stale, and neither is one carrying no timestamp", () => {
  const now = 1_755_300_000_000;
  const long = (now - STALE_AFTER_MS * 10) / 1000;
  assert.equal(isStale(true, long, now), false, "a job that finished is not going to be touched");
  assert.equal(
    isStale(false, 0, now),
    false,
    "an unstamped job is a daemon fact, not a dead worker",
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

test("runs join onto the job they name, in the order they were fed", () => {
  const model = buildJobTree(TREE);
  const join = joinRuns(model, [
    row({ id: "inv1", unit: "b2", title: "magus run test" }),
    row({ id: "out9", unit: "b2", title: "console:test", outcome: "pass" }),
    row({ id: "inv2", unit: "b1" }),
  ]);
  assert.deepEqual(
    join.byJob.get("b2")?.map((r) => r.id),
    ["inv1", "out9"],
  );
  assert.deepEqual(
    join.byJob.get("b1")?.map((r) => r.id),
    ["inv2"],
  );
  assert.equal(
    join.byJob.has("root"),
    false,
    "a job with no runs gets no bucket, not an empty one",
  );
});

// This is the whole state of the world today: nothing stamps a job onto the activity feeds yet.
// Treating that as evidence of a stale picture would make the warning permanent and worthless.
test("a run naming no job is unattributed, not unmatched", () => {
  const join = joinRuns(buildJobTree(TREE), [row({ id: "inv1" }), row({ id: "inv2", unit: "" })]);
  assert.equal(join.byJob.size, 0);
  assert.deepEqual(join.unmatched, []);
});

test("a run naming a job this listing does not carry is unmatched, which means it is stale", () => {
  const join = joinRuns(buildJobTree(TREE), [row({ id: "inv1", unit: "b3" })]);
  assert.deepEqual(
    join.unmatched.map((r) => r.id),
    ["inv1"],
  );
});

// ---- placement -------------------------------------------------------------

test("a child is placed to the right of its parent, and a dependent right of its dependency", () => {
  const at = layoutNodes(buildJobTree(TREE)).at;
  const x = (id: string): number => at.get(id)?.x ?? Number.NaN;
  assert.ok(x("root") < x("b1"), "a child cannot start before the parent that handed it out");
  assert.ok(x("b1") < x("b2"), "b2 waits on b1, so it sits downstream of it");
  assert.ok(x("b2") < x("b2a"));
});

test("placement is deterministic - the same listing lays out identically", () => {
  const a = layoutNodes(buildJobTree(TREE));
  const b = layoutNodes(buildJobTree(TREE));
  assert.equal(a.viewBox, b.viewBox);
  assert.deepEqual([...a.at.entries()], [...b.at.entries()]);
});

test("an empty listing lays out without a NaN viewBox", () => {
  const layout = layoutNodes(buildJobTree([]));
  assert.equal(layout.viewBox, "0 0 1 1");
  assert.equal(layout.at.size, 0);
});

// A cycle in depends_on is a mistake worth SEEING. The layout breaks it by reversing one edge, and
// reports which, so the renderer can mark the reversal as its own fiction.
test("a depends_on cycle is broken and the reversed edge is reported", () => {
  const layout = layoutNodes(
    buildJobTree([job("a", { dependsOn: ["b"] }), job("b", { dependsOn: ["a"] })]),
  );
  assert.equal(layout.back.size, 1, "exactly one of the two edges is layout fiction");
  assert.equal(layout.at.size, 2);
});

// ---- overlaps --------------------------------------------------------------

// The warning belongs on BOTH jobs: either one is where a reader might be standing when they need
// to know the other exists.
test("an overlap lands on both jobs, carrying the other id and each side's paths", () => {
  const model = buildJobTree(
    [job("a", { state: "running" }), job("b", { state: "running" })],
    [overlap("a", "b", ["internal/job"], ["internal/job/store.go"])],
  );
  assert.equal(model.byId.get("a")?.overlaps.length, 1);
  assert.deepEqual(model.byId.get("b")?.overlaps[0]?.pathsA, ["internal/job"]);
  assert.deepEqual(model.byId.get("b")?.overlaps[0]?.pathsB, ["internal/job/store.go"]);
  assert.equal(model.overlaps.length, 1);
});

test("an overlap naming a job this listing does not carry is dropped, not half-drawn", () => {
  const model = buildJobTree([job("a")], [overlap("a", "ghost", ["x"], ["x"])]);
  assert.deepEqual(model.byId.get("a")?.overlaps, []);
  assert.deepEqual(model.overlaps, []);
});
