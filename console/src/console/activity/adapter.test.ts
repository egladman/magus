// adapter.test.ts - the activity trail -> RenderModel mapping. The adapter is pure and
// DOM-free (adapter.ts), so it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { Kind, Outcome, type ActivityEvent } from "@wire/activity/v1alpha1/activity_pb";
import { must } from "../../lib/guards";
import {
  PAYLOAD_MAX_BYTES,
  activityToModel,
  clockTime,
  durText,
  eventSection,
  groupEventsByKind,
  humanBytes,
  kindLabel,
  payloadLabel,
  payloadLines,
  payloadRefs,
  tsMillis,
} from "./adapter";

// ev builds a minimal ActivityEvent for the pure adapter. Casts through unknown because the
// generated Message carries a $typeName the adapter never reads.
function ev(partial: Partial<ActivityEvent>): ActivityEvent {
  return {
    kind: Kind.MCP_TOOL_CALL,
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
    ...partial,
  } as unknown as ActivityEvent;
}

test("kindLabel maps every kind to its terse tag", () => {
  assert.equal(kindLabel(Kind.MCP_TOOL_CALL), "mcp");
  assert.equal(kindLabel(Kind.JOB), "job");
  assert.equal(kindLabel(Kind.CONFIG_CHANGE), "config");
  assert.equal(kindLabel(Kind.TOKEN_LIFECYCLE), "token");
  assert.equal(kindLabel(Kind.SANDBOX_DENIAL), "sandbox");
  assert.equal(kindLabel(Kind.MEMORY), "memory");
  assert.equal(kindLabel(Kind.AGENT_COMMAND), "agent");
  assert.equal(kindLabel(Kind.CREDENTIAL_GRANT), "credential");
  assert.equal(kindLabel(Kind.AGENT_SPAWN), "spawn");
  assert.equal(kindLabel(Kind.NOTES), "notes");
  assert.equal(kindLabel(Kind.UNSPECIFIED), "event");
});

test("durText: absent/zero is empty, ms under a second, seconds above", () => {
  assert.equal(durText(undefined), "");
  assert.equal(durText({ seconds: 0n, nanos: 0 } as never), "");
  assert.equal(durText({ seconds: 0n, nanos: 12_000_000 } as never), "12ms");
  assert.equal(durText({ seconds: 1n, nanos: 200_000_000 } as never), "1.2s");
});

test("humanBytes scales B/KB/MB", () => {
  assert.equal(humanBytes(512), "512 B");
  assert.equal(humanBytes(1536), "1.5 KB");
  assert.equal(humanBytes(2 * 1024 * 1024), "2.0 MB");
});

test("tsMillis: absent is null, else seconds*1000 + nanos", () => {
  assert.equal(tsMillis(undefined), null);
  assert.equal(tsMillis({ seconds: 2n, nanos: 500_000_000 } as never), 2500);
});

// The dashboard's agent tile links a summary row to "../activity/#at=<atMs>", and the trail
// resolves it by matching that number against tsMillis(ev.time). The two sides therefore have to
// agree on the SAME epoch-ms for one event: AgentCallView.atMs is built from the trail event, and
// tsMillis is what the trail surface reads it back with. If these ever diverge the link silently
// reveals nothing, which looks like a missing event rather than a broken link.
test("tsMillis round-trips the epoch-ms an agent-call deep link carries", () => {
  const ev = { seconds: 1_754_000_000n, nanos: 123_000_000 } as never;
  const atMs = tsMillis(ev);
  assert.equal(atMs, 1_754_000_000_123);
  // What the trail does with "#at=1754000000123": Number() it, then compare by identity.
  assert.equal(Number(String(atMs)), tsMillis(ev));
});

test("clockTime formats HH:MM:SS and empties a null instant", () => {
  assert.equal(clockTime(null), "");
  assert.match(clockTime(0), /^\d{2}:\d{2}:\d{2}$/);
});

test("an ok mcp call accents pass and heads with action+actor", () => {
  const sec = eventSection(
    ev({ action: "magus_query", actor: "agent:claude", outcome: Outcome.OK }),
  );
  assert.equal(sec.meta?.status, "pass");
  assert.equal(sec.meta?.label, "mcp");
  assert.equal(sec.lines[0], sec.title);
  assert.match(must(sec.title), /magus_query {2}agent:claude/);
  assert.match(must(sec.title), /mcp, ok/);
});

test("an agent command observation renders as an agent event, not an execution result", () => {
  const sec = eventSection(
    ev({
      kind: Kind.AGENT_COMMAND,
      action: "Bash",
      actor: "session:abc",
      preview: "guard: deny",
      outcome: Outcome.OK,
    }),
  );
  assert.equal(sec.meta?.label, "agent");
  assert.equal(sec.meta?.status, "pass");
  assert.match(must(sec.title), /Bash {2}session:abc/);
  assert.match(must(sec.title), /agent, ok/);
  assert.ok(sec.lines.includes("guard: deny"));
});

test("an errored call accents fail and leads its body with the error text", () => {
  const sec = eventSection(
    ev({ action: "magus_run", outcome: Outcome.ERROR, error: "target not found" }),
  );
  assert.equal(sec.meta?.status, "fail");
  assert.match(must(sec.title), /, error/);
  assert.equal(sec.lines[1], "target not found");
});

test("payload sizes, refs, preview lines, and workspace populate the body", () => {
  const sec = eventSection(
    ev({
      action: "magus_output",
      kind: Kind.MCP_TOOL_CALL,
      requestBytes: 40n,
      requestRef: "mcpaaaa",
      responseBytes: 2048n,
      responseRef: "mcpbbbb",
      preview: "line one\nline two",
      workspace: "/repo/magus",
    }),
  );
  const body = sec.lines.slice(1);
  assert.ok(body.some((l) => l.includes("request 40 B") && l.includes("mcpaaaa")));
  assert.ok(body.some((l) => l.includes("response 2.0 KB") && l.includes("mcpbbbb")));
  assert.ok(body.includes("line one"));
  assert.ok(body.includes("line two"));
  assert.ok(body.includes("workspace: /repo/magus"));
});

test("a job event with no payload is just its head", () => {
  const sec = eventSection(ev({ kind: Kind.JOB, action: "scip-reindex", actor: "daemon" }));
  assert.equal(sec.meta?.label, "job");
  assert.deepEqual(sec.lines, [sec.title]);
});

// The expand control is built from these refs: an event that names a body must offer one, and an
// event that names none must not.
test("payloadRefs lists request then response, and nothing for an event with no body", () => {
  assert.deepEqual(payloadRefs(ev({ action: "scip-reindex" })), []);
  assert.deepEqual(
    payloadRefs(
      ev({
        requestRef: "mcpaaaa",
        requestBytes: 40n,
        responseRef: "mcpbbbb",
        responseBytes: 2048n,
      }),
    ),
    [
      { label: "request", ref: "mcpaaaa", bytes: 40 },
      { label: "response", ref: "mcpbbbb", bytes: 2048 },
    ],
  );
  // A spawn records the handed context and no response at all.
  const spawn = ev({ kind: Kind.AGENT_SPAWN, requestRef: "spwaaaa", requestBytes: 900n });
  assert.deepEqual(payloadRefs(spawn), [{ label: "request", ref: "spwaaaa", bytes: 900 }]);
});

test("payloadLabel names the body, and its size only when one was recorded", () => {
  const response = { label: "response", ref: "mcpbbbb", bytes: 2048 };
  assert.equal(payloadLabel(response), "show response (2.0 KB)");
  assert.equal(payloadLabel({ label: "request", ref: "mcpaaaa", bytes: 0 }), "show request");
});

test("payloadLines splits a body and treats a trailing newline as a terminator", () => {
  const body = new TextEncoder().encode("first\nsecond\n");
  assert.deepEqual(payloadLines(body), { lines: ["first", "second"], clipped: false });
});

// The bound exists because a blob is whatever its producer wrote: one megabytes-long line of JSON
// is a normal MCP response, and the section renderer paints a line as a single row.
test("payloadLines clips a body past the bound and says so", () => {
  const body = new TextEncoder().encode("x".repeat(PAYLOAD_MAX_BYTES + 10));
  const out = payloadLines(body);
  assert.equal(out.clipped, true);
  assert.equal(out.lines.length, 1);
  assert.equal(out.lines[0].length, PAYLOAD_MAX_BYTES);
});

test("groupEventsByKind buckets in fixed order, drops empty kinds, keeps original indices", () => {
  const events = [
    ev({ kind: Kind.JOB, action: "j0" }),
    ev({ kind: Kind.MCP_TOOL_CALL, action: "m1" }),
    ev({ kind: Kind.JOB, action: "j2" }),
    ev({ kind: Kind.SANDBOX_DENIAL, action: "s3", outcome: Outcome.ERROR }),
    ev({ kind: Kind.AGENT_COMMAND, action: "Bash" }),
  ];
  const groups = groupEventsByKind(events);
  // MCP leads the fixed order even though a Job appeared first in the page; Config/Token have no
  // events and are absent.
  assert.deepEqual(
    groups.map((g) => g.label),
    ["MCP tool calls", "Jobs", "Sandbox denials", "Agent commands"],
  );
  // Jobs bucket keeps page order and original indices (0 then 2).
  const jobs = groups.find((g) => g.label === "Jobs");
  assert.deepEqual(
    jobs?.events.map((e) => e.index),
    [0, 2],
  );
  assert.equal(jobs?.events[0].event.action, "j0");
  // The sandbox denial keeps its index 3, so the view can reach section 3.
  assert.equal(groups.find((g) => g.label === "Sandbox denials")?.events[0].index, 3);
  assert.equal(groups.find((g) => g.label === "Agent commands")?.events[0].index, 4);
});

test("groupEventsByKind collects an unknown kind under Other", () => {
  const groups = groupEventsByKind([ev({ kind: Kind.UNSPECIFIED, action: "x" })]);
  assert.deepEqual(
    groups.map((g) => g.label),
    ["Other"],
  );
  assert.equal(groups[0].events[0].index, 0);
});

// kindLabel and KIND_GROUP_ORDER are both hand-maintained switches/tables over the wire Kind
// enum, and KindMemory and KindCredentialGrant already shipped in the proto and encodeKind while
// staying absent here: their events silently fell back to the "event" label and the "Other"
// bucket in the console. Enumerated straight off the generated Kind enum's own reverse mapping
// (not a second hand-maintained list), so the NEXT kind added to the proto fails this test the
// moment it lands, rather than shipping half-wired.
test("every non-UNSPECIFIED wire kind has a real label and a named group", () => {
  const allKinds = Object.values(Kind).filter((v): v is Kind => typeof v === "number");
  assert.ok(allKinds.length > 1, "sanity: the generated enum should list more than UNSPECIFIED");
  for (const kind of allKinds) {
    if (kind === Kind.UNSPECIFIED) continue;
    assert.notEqual(kindLabel(kind), "event", `Kind.${Kind[kind]} falls back to the default label`);

    const groups = groupEventsByKind([ev({ kind })]);
    assert.equal(groups.length, 1, `Kind.${Kind[kind]} did not produce exactly one group`);
    assert.notEqual(
      groups[0].label,
      "Other",
      `Kind.${Kind[kind]} is missing from KIND_GROUP_ORDER`,
    );
    assert.equal(groups[0].kind, kind);
  }
});

test("activityToModel titles every section and counts them", () => {
  const model = activityToModel([ev({ action: "a" }), ev({ action: "b", outcome: Outcome.ERROR })]);
  assert.equal(model.sections.length, 2);
  assert.equal(model.titled, 2);
  assert.equal(model.sections[0].title, model.sections[0].lines[0]);
});
