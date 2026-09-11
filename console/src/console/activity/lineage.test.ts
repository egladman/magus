// lineage.test.ts - the session grouping and the spawn -> lease -> child join. lineage.ts is pure
// and DOM-free, so it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { Kind, Outcome, type ActivityEvent } from "@wire/activity/v1alpha1/activity_pb";
import { sessionLabel, sessionLineage } from "./lineage";

// ev builds a minimal ActivityEvent. Casts through unknown because the generated Message carries a
// $typeName the module never reads.
function ev(partial: Partial<ActivityEvent>): ActivityEvent {
  return {
    kind: Kind.AGENT_COMMAND,
    actor: "",
    action: "",
    outcome: Outcome.OK,
    error: "",
    requestRef: "",
    responseRef: "",
    preview: "",
    requestBytes: 0n,
    responseBytes: 0n,
    workspace: "",
    host: "",
    session: "",
    unit: "",
    ...partial,
  } as unknown as ActivityEvent;
}

function cmd(session: string, partial: Partial<ActivityEvent> = {}): ActivityEvent {
  return ev({ kind: Kind.AGENT_COMMAND, session, host: "claude", action: "Bash", ...partial });
}

function spawn(
  session: string,
  lease: string,
  partial: Partial<ActivityEvent> = {},
): ActivityEvent {
  return ev({
    kind: Kind.AGENT_SPAWN,
    session,
    host: "claude",
    action: "Task",
    unit: lease,
    ...partial,
  });
}

// at builds a Timestamp from epoch ms; the adapter reads only seconds and nanos.
function at(ms: number): Timestamp {
  return {
    seconds: BigInt(Math.floor(ms / 1000)),
    nanos: (ms % 1000) * 1_000_000,
  } as unknown as Timestamp;
}

test("groups agent events by session, keeping page order and original indices", () => {
  const events = [
    cmd("s1", { action: "Bash" }),
    ev({ kind: Kind.MCP_TOOL_CALL, session: "s1", action: "magus_query" }),
    cmd("s2", { action: "Read" }),
    cmd("s1", { action: "Edit" }),
  ];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["s1", "s2"],
  );
  const s1 = roots[0];
  assert.equal(s1.host, "claude");
  assert.equal(s1.commands, 2, "the MCP event is not an agent command and is not counted");
  assert.deepEqual(
    s1.events.map((e) => e.index),
    [0, 3],
    "only the agent events, at the positions they held in the page",
  );
});

test("denied counts the guard's deny preview, not every failure", () => {
  const events = [
    cmd("s1", { preview: "guard: deny" }),
    cmd("s1", { preview: "guard: pass" }),
    cmd("s1", { preview: "guard: advise", outcome: Outcome.ERROR }),
    cmd("s1", { preview: "guard: deny" }),
  ];

  const [s1] = sessionLineage(events);
  assert.equal(s1.commands, 4);
  assert.equal(s1.denied, 2);
});

test("a spawn's lease adopts the session that ran commands under it", () => {
  const events = [
    spawn("parent", "harness/child-work"),
    cmd("child", { unit: "harness/child-work" }),
    cmd("parent"),
  ];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["parent"],
    "the child is nested, not listed beside its parent",
  );
  assert.deepEqual(roots[0].spawns, [{ action: "Task", lease: "harness/child-work" }]);
  assert.deepEqual(
    roots[0].children.map((n) => n.session),
    ["child"],
  );
  assert.deepEqual(roots[0].children[0].leases, ["harness/child-work"]);
});

test("a spawn whose lease nobody acted under adopts nothing", () => {
  const events = [spawn("parent", "harness/never-started"), cmd("parent")];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["parent"],
  );
  assert.deepEqual(roots[0].children, [], "a declared lease is not by itself a child");
  assert.deepEqual(roots[0].spawns, [{ action: "Task", lease: "harness/never-started" }]);
});

// The trail lists a page newest-first, so the child's commands sit ABOVE the spawn that handed it
// the lease, and a second session that declared the same lease later sits above them both. The
// claim goes to the earliest spawn by time, not to whichever session the page mentions first.
test("the join reads the same on a newest-first page", () => {
  const events = [
    spawn("later", "harness/child-work", { time: at(5_000) }),
    cmd("child", { unit: "harness/child-work", time: at(4_000) }),
    cmd("parent", { time: at(3_000) }),
    spawn("parent", "harness/child-work", { time: at(2_000) }),
  ];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["later", "parent"],
    "roots keep page order",
  );
  assert.deepEqual(roots[0].children, [], "the later spawn did not claim the child");
  assert.deepEqual(
    roots[1].children.map((n) => n.session),
    ["child"],
  );
});

// A parent that binds itself to the lease it registered still ACTS under it, so its own commands
// would otherwise be the lease's actor and the child would attach to nobody.
test("the spawner's own commands under its lease do not steal the join", () => {
  const events = [
    spawn("parent", "harness/child-work"),
    cmd("parent", { unit: "harness/child-work" }),
    cmd("child", { unit: "harness/child-work" }),
  ];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["parent"],
  );
  assert.deepEqual(
    roots[0].children.map((n) => n.session),
    ["child"],
  );
});

// A lease re-bound to a second worker has two actors, and both were spawned by the same parent.
test("every session that acted under a spawned lease is a child", () => {
  const events = [
    spawn("parent", "harness/work"),
    cmd("w1", { unit: "harness/work" }),
    cmd("w2", { unit: "harness/work" }),
  ];

  const [parent] = sessionLineage(events);
  assert.deepEqual(
    parent.children.map((n) => n.session),
    ["w1", "w2"],
  );
});

// Session ids are the host tool's, so two hosts can mint the same id for unrelated sessions.
test("the same session id on two hosts is two sessions", () => {
  const events = [cmd("s1", { host: "claude" }), cmd("s1", { host: "codex" })];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.host + ":" + n.session),
    ["claude:s1", "codex:s1"],
  );
  assert.deepEqual(
    roots.map((n) => n.commands),
    [1, 1],
  );
});

test("events with no session are skipped, never pooled", () => {
  const events = [cmd("", { host: "codex" }), cmd("", { host: "codex" }), cmd("s1")];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["s1"],
  );
  assert.equal(roots[0].commands, 1, "the unattributed commands did not land on the one session");
});

test("a session is both a child and a parent", () => {
  const events = [
    spawn("top", "harness/mid"),
    cmd("mid", { unit: "harness/mid" }),
    spawn("mid", "harness/leaf"),
    cmd("leaf", { unit: "harness/leaf" }),
  ];

  const roots = sessionLineage(events);
  assert.deepEqual(
    roots.map((n) => n.session),
    ["top"],
  );
  const mid = roots[0].children[0];
  assert.equal(mid.session, "mid");
  assert.deepEqual(
    mid.children.map((n) => n.session),
    ["leaf"],
  );
});

test("sessionLabel names the host, the counts and the leases", () => {
  const [s1] = sessionLineage([
    cmd("s1", { unit: "harness/one", preview: "guard: deny" }),
    cmd("s1", { unit: "harness/one" }),
  ]);
  assert.equal(sessionLabel(s1), "claude, 2 commands, 1 denied, harness/one");

  const [s2] = sessionLineage([cmd("s2", { host: "" })]);
  assert.equal(sessionLabel(s2), "unknown host, 1 command");
});
