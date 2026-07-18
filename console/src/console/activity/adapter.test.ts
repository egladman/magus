// adapter.test.ts - the activity trail -> RenderModel mapping. The adapter is pure and
// DOM-free (adapter.ts), so it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { Kind, Outcome, type ActivityEvent } from "../../gen/magus/activity/v1/activity_pb";
import {
  activityToModel, clockTime, durText, eventSection, groupEventsByKind, humanBytes, kindLabel, tsMillis,
} from "./adapter";

// ev builds a minimal ActivityEvent for the pure adapter. Casts through unknown because the
// generated Message carries a $typeName the adapter never reads; tests are excluded from tsc.
function ev(partial: Partial<ActivityEvent>): ActivityEvent {
  return {
    kind: Kind.MCP_TOOL_CALL, actor: "", action: "", outcome: Outcome.OK, error: "",
    requestRef: "", responseRef: "", preview: "", requestBytes: 0n, responseBytes: 0n,
    workspace: "", ...partial,
  } as unknown as ActivityEvent;
}

test("kindLabel maps every kind to its terse tag", () => {
  assert.equal(kindLabel(Kind.MCP_TOOL_CALL), "mcp");
  assert.equal(kindLabel(Kind.JOB), "job");
  assert.equal(kindLabel(Kind.CONFIG_CHANGE), "config");
  assert.equal(kindLabel(Kind.TOKEN_LIFECYCLE), "token");
  assert.equal(kindLabel(Kind.SANDBOX_DENIAL), "sandbox");
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

test("clockTime formats HH:MM:SS and empties a null instant", () => {
  assert.equal(clockTime(null), "");
  assert.match(clockTime(0), /^\d{2}:\d{2}:\d{2}$/);
});

test("an ok mcp call accents pass and heads with action+actor", () => {
  const sec = eventSection(ev({ action: "magus_query", actor: "agent:claude", outcome: Outcome.OK }));
  assert.equal(sec.meta?.status, "pass");
  assert.equal(sec.meta?.label, "mcp");
  assert.equal(sec.lines[0], sec.title);
  assert.match(sec.title, /magus_query {2}agent:claude/);
  assert.match(sec.title, /mcp - ok/);
});

test("an errored call accents fail and leads its body with the error text", () => {
  const sec = eventSection(ev({ action: "magus_run", outcome: Outcome.ERROR, error: "target not found" }));
  assert.equal(sec.meta?.status, "fail");
  assert.match(sec.title, / - error/);
  assert.equal(sec.lines[1], "target not found");
});

test("payload sizes, refs, preview lines, and workspace populate the body", () => {
  const sec = eventSection(ev({
    action: "magus_output", kind: Kind.MCP_TOOL_CALL,
    requestBytes: 40n, requestRef: "mcpaaaa", responseBytes: 2048n, responseRef: "mcpbbbb",
    preview: "line one\nline two", workspace: "/repo/magus",
  }));
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

test("groupEventsByKind buckets in fixed order, drops empty kinds, keeps original indices", () => {
  const events = [
    ev({ kind: Kind.JOB, action: "j0" }),
    ev({ kind: Kind.MCP_TOOL_CALL, action: "m1" }),
    ev({ kind: Kind.JOB, action: "j2" }),
    ev({ kind: Kind.SANDBOX_DENIAL, action: "s3", outcome: Outcome.ERROR }),
  ];
  const groups = groupEventsByKind(events);
  // MCP leads the fixed order even though a Job appeared first in the page; Config/Token have no
  // events and are absent.
  assert.deepEqual(groups.map((g) => g.label), ["MCP tool calls", "Jobs", "Sandbox denials"]);
  // Jobs bucket keeps page order and original indices (0 then 2).
  const jobs = groups.find((g) => g.label === "Jobs");
  assert.deepEqual(jobs?.events.map((e) => e.index), [0, 2]);
  assert.equal(jobs?.events[0].event.action, "j0");
  // The sandbox denial keeps its index 3, so the view can reach section 3.
  assert.equal(groups.find((g) => g.label === "Sandbox denials")?.events[0].index, 3);
});

test("groupEventsByKind collects an unknown kind under Other", () => {
  const groups = groupEventsByKind([ev({ kind: Kind.UNSPECIFIED, action: "x" })]);
  assert.deepEqual(groups.map((g) => g.label), ["Other"]);
  assert.equal(groups[0].events[0].index, 0);
});

test("activityToModel titles every section and counts them", () => {
  const model = activityToModel([
    ev({ action: "a" }), ev({ action: "b", outcome: Outcome.ERROR }),
  ]);
  assert.equal(model.sections.length, 2);
  assert.equal(model.titled, 2);
  assert.equal(model.sections[0].title, model.sections[0].lines[0]);
});
