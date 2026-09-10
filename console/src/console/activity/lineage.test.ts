// lineage.test.ts - the session grouping and the spawn -> lease -> child join. lineage.ts is pure
// and DOM-free, so it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
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

function spawn(session: string, unit: string): ActivityEvent {
  return ev({ kind: Kind.AGENT_SPAWN, session, host: "claude", action: "Task", unit });
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
  assert.deepEqual(roots[0].spawns, [{ action: "Task", unit: "harness/child-work" }]);
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
  assert.deepEqual(roots[0].spawns, [{ action: "Task", unit: "harness/never-started" }]);
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
