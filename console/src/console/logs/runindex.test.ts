import assert from "node:assert/strict";
import { test } from "node:test";
import {
  buildFacets,
  buildRunRows,
  buildRunTree,
  commandText,
  matchesFilter,
  parseRunFilter,
  relTime,
  toggleFilterTerm,
  type RunLog,
  type RunSummary,
} from "./runindex";

const NOW = 1_700_000_000_000;

function run(over: Partial<RunSummary> = {}): RunSummary {
  return {
    ref: "out1111",
    project: "services/identity",
    target: "build",
    inv: "invA",
    failed: false,
    timestamp_ms: NOW - 60_000,
    duration_ms: 2100,
    ...over,
  };
}

function log(over: Partial<RunLog> = {}): RunLog {
  return {
    inv: "invA",
    arguments: ["run", "build", "services/identity"],
    trigger: "run",
    started_ms: NOW - 62_000,
    finished_ms: NOW - 60_000,
    status: "pass",
    ...over,
  };
}

test("parseRunFilter splits known keys from free text", () => {
  const f = parseRunFilter("status:fail target:build authkit");
  assert.deepEqual(f.keyed, [
    { key: "status", value: "fail" },
    { key: "target", value: "build" },
  ]);
  assert.deepEqual(f.texts, ["authkit"]);
  assert.equal(f.empty, false);
});

test("parseRunFilter treats an unknown key as free text", () => {
  const f = parseRunFilter("branch:main");
  assert.deepEqual(f.keyed, []);
  assert.deepEqual(f.texts, ["branch:main"]);
});

test("an empty query matches everything", () => {
  assert.equal(parseRunFilter("   ").empty, true);
  assert.equal(matchesFilter(run(), log(), parseRunFilter("")), true);
});

test("free text reaches the command line, not just the target", () => {
  const ci = log({ arguments: ["affected", "ci"], trigger: "ci" });
  assert.equal(matchesFilter(run(), ci, parseRunFilter("affected")), true);
  // The same row without its invocation has nothing to match the command against.
  assert.equal(matchesFilter(run(), undefined, parseRunFilter("affected")), false);
});

test("status: is exact, project:/target:/ref: are prefix or substring", () => {
  const f = parseRunFilter.bind(null);
  assert.equal(matchesFilter(run({ failed: true }), log(), f("status:fail")), true);
  assert.equal(matchesFilter(run({ failed: false }), log(), f("status:fail")), false);
  assert.equal(matchesFilter(run(), log(), f("project:identity")), true);
  assert.equal(matchesFilter(run(), log(), f("ref:out11")), true);
  assert.equal(matchesFilter(run(), log(), f("ref:9999")), false);
  assert.equal(matchesFilter(run(), log({ trigger: "ci" }), f("trigger:ci")), true);
  assert.equal(matchesFilter(run(), log({ trigger: "ci" }), f("trigger:run")), false);
});

test("keyed terms combine with AND", () => {
  const f = parseRunFilter("project:identity status:pass");
  assert.equal(matchesFilter(run(), log(), f), true);
  assert.equal(matchesFilter(run({ failed: true }), log(), f), false);
});

test("runs mode nests invocation -> target and keeps the feed order", () => {
  const runs = [
    run({ ref: "outA1", inv: "invA", target: "build" }),
    run({ ref: "outA2", inv: "invA", target: "test", failed: true }),
    run({ ref: "outB1", inv: "invB", project: "libs/authkit" }),
  ];
  const logs = [
    log({ inv: "invA", arguments: ["affected", "ci"], status: "fail" }),
    log({ inv: "invB", started_ms: NOW - 900_000 }),
  ];
  const tree = buildRunTree({ runs, logs, mode: "runs", filter: parseRunFilter(""), now: NOW });
  assert.deepEqual(
    tree.map((n) => n.id),
    ["inv:invA", "inv:invB"],
  );
  assert.equal(tree[0].description, "magus affected ci");
  assert.equal(tree[0].count, 2);
  assert.equal(tree[0].status, "fail");
  assert.deepEqual(
    tree[0].children?.map((c) => c.id),
    ["ref:outA1", "ref:outA2"],
  );
  assert.deepEqual(tree[0].select, {
    kind: "invocation",
    inv: "invA",
    label: "magus affected ci",
  });
  assert.deepEqual(tree[0].children?.[1].select, {
    kind: "output",
    run: runs[1],
    focus: "test",
  });
});

test("an invocation's own outcome beats its targets' fold", () => {
  const tree = buildRunTree({
    runs: [run()],
    logs: [log({ status: "fail" })],
    mode: "runs",
    filter: parseRunFilter(""),
    now: NOW,
  });
  assert.equal(tree[0].status, "fail");
});

test("an unfiltered tree keeps a run that produced no retained output; a filter drops it", () => {
  const opts = { runs: [], logs: [log({ inv: "invA" })], mode: "runs" as const, now: NOW };
  const all = buildRunTree({ ...opts, filter: parseRunFilter("") });
  assert.equal(all.length, 1);
  assert.equal(all[0].count, undefined);
  assert.equal(buildRunTree({ ...opts, filter: parseRunFilter("build") }).length, 0);
});

test("outputs whose invocation is not in the runs feed still list", () => {
  const tree = buildRunTree({
    runs: [run({ inv: "invGone" })],
    logs: [],
    mode: "runs",
    filter: parseRunFilter(""),
    now: NOW,
  });
  assert.equal(tree.length, 1);
  assert.equal(tree[0].description, "invGone");
  assert.equal(tree[0].select, undefined);
});

test("projects mode nests project -> target -> run and folds a mixed branch", () => {
  const runs = [
    run({ ref: "outA1", target: "build:rw" }),
    run({ ref: "outA2", target: "build", failed: true }),
    run({ ref: "outB1", project: "libs/authkit", target: "lint" }),
  ];
  const tree = buildRunTree({
    runs,
    logs: [],
    mode: "projects",
    filter: parseRunFilter(""),
    now: NOW,
  });
  assert.deepEqual(
    tree.map((n) => n.label),
    ["services/identity", "libs/authkit"],
  );
  // The charm suffix collapses, so both executions group under the declared target name.
  assert.deepEqual(
    tree[0].children?.map((c) => c.label),
    ["build"],
  );
  assert.equal(tree[0].children?.[0].count, 2);
  assert.equal(tree[0].status, "mixed");
});

test("commandText reads as the command that was typed", () => {
  assert.equal(commandText(log({ arguments: ["affected", "ci"] })), "magus affected ci");
  assert.equal(commandText(undefined), "");
});

test("relTime degrades to a clock time past a day", () => {
  assert.equal(relTime(NOW - 5_000, NOW), "5s ago");
  assert.equal(relTime(NOW - 5 * 60_000, NOW), "5m ago");
  assert.equal(relTime(NOW - 3 * 3_600_000, NOW), "3h ago");
  assert.match(relTime(NOW - 3 * 86_400_000, NOW), /^\d\d\/\d\d \d\d:\d\d$/);
});

test("buildRunRows joins outputs onto their invocation, newest first", () => {
  const runs = [
    run({ ref: "outA1", inv: "invA", target: "build" }),
    run({ ref: "outA2", inv: "invA", target: "test", failed: true }),
    run({ ref: "outB1", inv: "invB" }),
  ];
  const logs = [
    log({ inv: "invA", arguments: ["affected", "ci"], started_ms: NOW - 5000, finished_ms: NOW }),
    log({ inv: "invB", started_ms: NOW - 900_000, finished_ms: NOW - 880_000 }),
  ];
  const rows = buildRunRows(runs, logs, true);
  assert.deepEqual(
    rows.map((r) => r.inv),
    ["invA", "invB"],
  );
  assert.equal(rows[0].command, "magus affected ci");
  assert.equal(rows[0].outputs.length, 2);
  assert.equal(rows[0].durationMs, 5000);
  assert.equal(rows[0].status, "pass", "the journal's own outcome wins over its targets");
  assert.equal(rows[1].outputs.length, 1);
});

test("buildRunRows keeps an output-less run only when unfiltered", () => {
  const logs = [log({ inv: "invA" })];
  assert.equal(buildRunRows([], logs, true).length, 1);
  assert.equal(buildRunRows([], logs, false).length, 0);
});

test("buildRunRows falls back to output timestamps when the journal is gone", () => {
  const rows = buildRunRows([run({ inv: "invGone", timestamp_ms: NOW - 1000 })], [], true);
  assert.equal(rows[0].command, "invGone");
  assert.equal(rows[0].log, undefined);
  assert.equal(rows[0].startMs, NOW - 1000);
});

test("buildFacets lists only values that occur, with counts", () => {
  const runs = [
    run({ ref: "o1", inv: "invA", project: "console", target: "build" }),
    run({ ref: "o2", inv: "invB", project: "console", target: "test", failed: true }),
    run({ ref: "o3", inv: "invC", project: "docs", target: "build" }),
  ];
  // Each run's status comes from its own journal, so the fixture has to say so - a failed OUTPUT
  // under a journal that recorded a pass is a run that passed, which is the rule buildRunRows pins.
  const logs = [
    log({ inv: "invA", trigger: "ci" }),
    log({ inv: "invB", trigger: "ci", status: "fail" }),
    log({ inv: "invC", trigger: "run" }),
  ];
  const facets = buildFacets(runs, logs, parseRunFilter(""));
  const byKey = Object.fromEntries(facets.map((f) => [f.key, f]));
  assert.deepEqual(
    byKey.status.values.map((v) => [v.value, v.count]),
    [
      ["pass", 2],
      ["fail", 1],
    ],
  );
  assert.deepEqual(
    byKey.project.values.map((v) => [v.value, v.count]),
    [
      ["console", 2],
      ["docs", 1],
    ],
  );
  assert.deepEqual(
    byKey.trigger.values.map((v) => [v.value, v.count]),
    [
      ["ci", 2],
      ["run", 1],
    ],
  );
});

// A count is RUNS carrying the value, never times it occurred - it has to predict what clicking
// leaves, and the list beside the rail is a list of runs. Two targets of one run count once.
test("a facet counts runs, not the outputs inside them", () => {
  const runs = [
    run({ ref: "o1", inv: "invA", project: "console", target: "build" }),
    run({ ref: "o2", inv: "invA", project: "console", target: "test" }),
  ];
  const facets = buildFacets(runs, [log({ inv: "invA" })], parseRunFilter(""));
  const byKey = Object.fromEntries(facets.map((f) => [f.key, f]));
  assert.deepEqual(
    byKey.project.values.map((v) => [v.value, v.count]),
    [["console", 1]],
  );
  assert.deepEqual(
    byKey.status.values.map((v) => [v.value, v.count]),
    [["pass", 1]],
  );
  // The targets themselves are still distinct values - it is the RUN that is counted once each.
  assert.deepEqual(
    byKey.target.values.map((v) => [v.value, v.count]),
    [
      ["build", 1],
      ["test", 1],
    ],
  );
});

// The point of facet counts is that they predict: each is scoped by every OTHER key, so a facet's
// unpicked values still say what picking them would give instead of collapsing to zero.
test("a facet's counts exclude its own selection", () => {
  const runs = [
    run({ ref: "o1", inv: "invA", project: "console" }),
    run({ ref: "o2", inv: "invB", project: "console", failed: true }),
    run({ ref: "o3", inv: "invC", project: "docs", failed: true }),
  ];
  const logs = [
    log({ inv: "invA" }),
    log({ inv: "invB", status: "fail" }),
    log({ inv: "invC", status: "fail" }),
  ];
  const facets = buildFacets(runs, logs, parseRunFilter("status:fail"));
  const byKey = Object.fromEntries(facets.map((f) => [f.key, f]));
  assert.deepEqual(
    byKey.status.values.map((v) => [v.value, v.count, v.active]),
    [
      ["fail", 2, true],
      ["pass", 1, false],
    ],
    "status still counts against the unfiltered-by-status set",
  );
  assert.deepEqual(
    byKey.project.values.map((v) => [v.value, v.count]),
    [
      ["console", 1],
      ["docs", 1],
    ],
    "project counts respect the active status term",
  );
});

test("an active facet value stays listed at zero so it can be turned off", () => {
  const facets = buildFacets(
    [run({ project: "console" })],
    [log()],
    parseRunFilter("project:nope"),
  );
  const project = facets.find((f) => f.key === "project");
  assert.ok(project);
  const nope = project.values.find((v) => v.value === "nope");
  assert.deepEqual([nope?.count, nope?.active], [0, true]);
});

test("toggleFilterTerm adds, removes, and keeps the rest of the query", () => {
  assert.equal(toggleFilterTerm("", "status", "fail"), "status:fail");
  assert.equal(toggleFilterTerm("status:fail", "status", "fail"), "");
  assert.equal(toggleFilterTerm("authkit", "project", "console"), "authkit project:console");
  assert.equal(
    toggleFilterTerm("project:console authkit", "project", "console"),
    "authkit",
    "removing a term leaves the free text alone",
  );
});

// Two statuses AND to nothing, which reads as the page being broken rather than as the query being
// impossible - so a single-valued facet replaces instead of accumulating.
test("toggleFilterTerm replaces a single-valued key and accumulates a multi-valued one", () => {
  assert.equal(toggleFilterTerm("status:pass", "status", "fail"), "status:fail");
  assert.equal(toggleFilterTerm("trigger:ci", "trigger", "run"), "trigger:run");
  assert.equal(
    toggleFilterTerm("project:console", "project", "docs"),
    "project:console project:docs",
  );
});
