// activityDrawer.test.ts - the drawer's row model. Every function under test is pure and DOM-free
// (activityDrawer.ts keeps the projection separate from the panel), so it runs directly under node.
//
// What is worth pinning here is the READING each row produces, not the markup: whether the two
// RUNNING sources actually merge into one ordered list, whether an unknown timestamp stays unknown
// instead of rendering as "just now", and whether an outcome is only claimed once it is known. Each
// of those is a decision this module makes, and each fails silently - a plausible-looking row that
// says the wrong thing is worse than an empty panel.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { Output } from "@wire/viewer/v1alpha1/viewer_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { Status } from "@wire/status/v1alpha1/status_pb";
import {
  fmtMs,
  wireDescriptors,
  recentRows,
  relAge,
  runningRows,
  summaryLine,
  type ActivityRow,
  type RunDescriptor,
} from "./activityDrawer";

const NOW = 1_700_000_000_000;

// ts builds a protobuf Timestamp for an epoch-ms instant. Cast through unknown because the generated
// wrapper carries a $typeName the projection never reads.
function ts(ms: number): Timestamp {
  return {
    seconds: BigInt(Math.floor(ms / 1000)),
    nanos: (ms % 1000) * 1e6,
  } as unknown as Timestamp;
}

// status builds a minimal Status frame. Only pool.runningTargets and locks are read.
function status(partial: { runningTargets?: unknown[]; locks?: unknown[] }): Status {
  return {
    pool: { runningTargets: partial.runningTargets ?? [] },
    locks: partial.locks ?? [],
  } as unknown as Status;
}

function run(partial: Partial<RunDescriptor>): RunDescriptor {
  return {
    ref: "ref1",
    project: "svc/api",
    target: "build",
    failed: false,
    timestamp_ms: NOW,
    duration_ms: 0,
    ...partial,
  };
}

function ids(rows: ActivityRow[]): string[] {
  return rows.map((r) => r.id);
}

test("relAge: seconds, minutes, hours - and empty for an instant nobody reported", () => {
  assert.equal(relAge(NOW - 12_000, NOW), "12s");
  assert.equal(relAge(NOW - 4 * 60_000, NOW), "4m");
  assert.equal(relAge(NOW - 2 * 3600_000, NOW), "2h");
  // The whole point of the empty case: a missing timestamp must not read as "0s", which is
  // indistinguishable from something that started this instant.
  assert.equal(relAge(0, NOW), "");
  // A clock skewed into the future clamps rather than printing a negative age.
  assert.equal(relAge(NOW + 5000, NOW), "0s");
});

test("fmtMs: milliseconds under a second, seconds above, empty for an unrecorded duration", () => {
  assert.equal(fmtMs(820), "820ms");
  assert.equal(fmtMs(12_400), "12.4s");
  assert.equal(fmtMs(0), "");
  assert.equal(fmtMs(-1), "");
});

test("runningRows merges pool targets with lock holders, newest first", () => {
  const rows = runningRows(
    status({
      runningTargets: [
        {
          args: ["run", "build", "."],
          workspace: "~/repo",
          step: "compile",
          startTime: ts(NOW - 60_000),
          invocation: "inv-old",
        },
        {
          args: ["run", "test", "."],
          workspace: "~/repo",
          step: "",
          startTime: ts(NOW - 5_000),
          invocation: "inv-new",
        },
      ],
      locks: [
        {
          project: "svc/api",
          pid: 4242,
          command: "magus run build svc/api",
          dir: "/w",
          acquireTime: ts(NOW - 30_000),
        },
      ],
    }),
    NOW,
  );
  // A lock holder is a SEPARATE magus process (typically a terminal run) that the pool cannot see, so
  // the merge is the feature: dropping either source reports an idle daemon while work is underway.
  assert.deepEqual(ids(rows), ["inv-new", "lock:4242:svc/api", "inv-old"]);
});

test("runningRows never claims an outcome: nothing running has finished yet", () => {
  const rows = runningRows(
    status({
      runningTargets: [
        { args: ["run", "build"], workspace: "", step: "", startTime: ts(NOW), invocation: "inv1" },
      ],
      locks: [{ project: ".", pid: 7, command: "magus run ci", dir: "/w", acquireTime: ts(NOW) }],
    }),
    NOW,
  );
  assert.deepEqual(
    rows.map((r) => r.outcome),
    ["", ""],
  );
});

test("runningRows renders a pool slot as its argv and a lock as its holder", () => {
  const rows = runningRows(
    status({
      runningTargets: [
        {
          args: ["run", "build", "."],
          workspace: "~/repo",
          step: "compile",
          startTime: ts(NOW - 3000),
          invocation: "inv1",
        },
      ],
      locks: [
        {
          project: "svc/api",
          pid: 4242,
          command: "magus run build svc/api",
          dir: "/w",
          acquireTime: ts(NOW - 9000),
        },
      ],
    }),
    NOW,
  );
  assert.equal(rows[0].title, "magus run build .");
  assert.equal(rows[0].detail, "~/repo - compile - 3s");
  // The holder's argv identifies it; two runs against the same tree differ only there.
  assert.equal(rows[1].title, "magus run build svc/api");
  assert.equal(rows[1].detail, "svc/api - pid 4242 - 9s");
});

test("runningRows tolerates an absent pool, absent locks, and an absent status", () => {
  assert.deepEqual(runningRows(undefined, NOW), []);
  assert.deepEqual(runningRows({} as unknown as Status, NOW), []);
  assert.deepEqual(runningRows(status({}), NOW), []);
});

test("runningRows leaves an unreported start time out of the meta entirely", () => {
  const rows = runningRows(
    status({
      runningTargets: [
        { args: ["run", "build"], workspace: "", step: "compile", invocation: "inv1" },
      ],
    }),
    NOW,
  );
  assert.equal(rows[0].detail, "compile", "no age is honest; '0s' would be a fabricated one");
  assert.equal(rows[0].atMs, 0);
});

test("recentRows maps the store's failed flag to the row outcome", () => {
  const rows = recentRows([run({ ref: "a", failed: false }), run({ ref: "b", failed: true })], NOW);
  assert.deepEqual(
    rows.map((r) => [r.id, r.outcome]),
    [
      ["a", "pass"],
      ["b", "fail"],
    ],
  );
});

test("recentRows orders newest first regardless of the order the feed arrived in", () => {
  const rows = recentRows(
    [
      run({ ref: "old", timestamp_ms: NOW - 600_000 }),
      run({ ref: "newest", timestamp_ms: NOW - 1_000 }),
      run({ ref: "mid", timestamp_ms: NOW - 60_000 }),
    ],
    NOW,
  );
  assert.deepEqual(ids(rows), ["newest", "mid", "old"]);
});

test("recentRows labels a run project:target, falling back when the project is empty", () => {
  const rows = recentRows(
    [
      run({ ref: "a", project: "svc/api", target: "build:rw", duration_ms: 12_400 }),
      run({ ref: "b", project: "", target: "generate" }),
    ],
    NOW,
  );
  // The charm suffix is preserved: "build:rw" and "build" are different invocations.
  assert.equal(rows[0].title, "svc/api:build:rw");
  assert.equal(rows[0].detail, "12.4s - 0s");
  assert.equal(rows[1].title, "generate");
});

test("recentRows caps the list so the panel is read rather than scrolled", () => {
  const feed = Array.from({ length: 60 }, (_, i) =>
    run({ ref: "r" + i, timestamp_ms: NOW - i * 1000 }),
  );
  const rows = recentRows(feed, NOW);
  assert.equal(rows.length, 25);
  assert.equal(
    rows[0].id,
    "r0",
    "the cap keeps the newest, not the first 25 the feed happened to send",
  );
});

test("every row carries the phase-2 unit slot, unset until a delegation ledger fills it", () => {
  const rows = [
    ...runningRows(
      status({
        runningTargets: [
          {
            args: ["run", "build"],
            workspace: "",
            step: "",
            startTime: ts(NOW),
            invocation: "inv1",
          },
        ],
        locks: [{ project: ".", pid: 7, command: "magus run ci", dir: "/w", acquireTime: ts(NOW) }],
      }),
      NOW,
    ),
    ...recentRows([run({})], NOW),
  ];
  assert.equal(rows.length, 3);
  for (const r of rows) assert.equal(r.unit, undefined);
  // The field is optional, so a producer can start stamping it without touching any producer that
  // does not know about units yet.
  const tagged: ActivityRow = { ...rows[0], unit: "unit-3" };
  assert.equal(tagged.unit, "unit-3");
});

test("summaryLine is what the live region announces", () => {
  assert.equal(summaryLine(0, 0), "0 running, 0 recent");
  assert.equal(summaryLine(3, 25), "3 running, 25 recent");
});

// ---- reading the feed ------------------------------------------------------

// The four tests that stood here checked hand-rolled JSON coercion - a timestamp that is not a
// number, a row with no ref, a body that is not the documented shape. Connect decodes this feed now,
// so none of those states is representable and a test for them would assert nothing. What IS worth
// pinning is the unit conversion: the wire carries a Timestamp and a Duration, the panel renders
// millis, and an absent field must read as 0 rather than NaN.
test("wireDescriptors converts the wire's timestamp and duration to millis", () => {
  const rows = wireDescriptors([
    {
      ref: "out1",
      project: "api",
      target: "build",
      invocation: "inv1",
      failed: false,
      error: "",
      createTime: { seconds: 1n, nanos: 500_000_000 },
      duration: { seconds: 2n, nanos: 250_000_000 },
    },
  ] as unknown as Output[]);

  assert.equal(rows[0]?.timestamp_ms, 1500);
  assert.equal(rows[0]?.duration_ms, 2250);
});

test("wireDescriptors reads an absent time as 0, never NaN", () => {
  const rows = wireDescriptors([
    { ref: "out1", project: "", target: "", invocation: "", failed: false, error: "" },
  ] as unknown as Output[]);

  assert.equal(rows[0]?.timestamp_ms, 0);
  assert.equal(rows[0]?.duration_ms, 0);
});
