// demo.ts - the activity trail for the server-free showcase (the shared #demo fragment) and the
// no-server empty state's "see the demo" path. It maps the shared scenario's activity beats
// (demo-scenario.ts) into ActivityEvent protos, so the trail shows the SAME runs as the recent-runs
// tree, the log waterfall, and the dashboard: the same agent MCP calls that drove the runs (tied by
// responseRef and timing), the branch-switch reindex job that explains WHY services/identity:test
// suddenly broke, the config/token beats that authorized the agent, the denied sandbox write, and
// the failed lookup for a pruned ref. It adds one agent fan-out of its own (an orchestrator session
// that spawned a worker), because the scenario has none and By session cannot be read without one.
// Newest first, the order the service returns. Because it is wire-shaped data, the Activity app's
// rendering (foldable sections, ok vs error, the kind and session groupings) is fully inspectable
// without a running server.

import { create } from "@bufbuild/protobuf";
import {
  ActivityEventSchema,
  Kind,
  Outcome,
  type ActivityEvent,
} from "@wire/activity/v1alpha1/activity_pb";
import { scenarioActivity, type ActKind } from "../../desktop/demo-scenario";

// The scenario speaks a terse kind tag; the wire enum is the proto Kind. One mapping table keeps the
// two vocabularies aligned in one place.
const KIND: Record<ActKind, Kind> = {
  mcp: Kind.MCP_TOOL_CALL,
  job: Kind.JOB,
  config: Kind.CONFIG_CHANGE,
  token: Kind.TOKEN_LIFECYCLE,
  sandbox: Kind.SANDBOX_DENIAL,
};

// ts builds a protobuf Timestamp init from epoch ms. Plain init objects are enough - create()
// accepts them for the nested well-known types, and the adapter reads seconds/nanos.
function ts(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: (ms % 1000) * 1_000_000 };
}

// dur builds a protobuf Duration init from a millisecond count.
function dur(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.round((ms % 1000) * 1_000_000) };
}

// agentBeat is one command or spawn an agent session recorded. The scenario has no agent sessions of
// its own, so this adds one orchestrator that spawned a worker, which is what the By session grouping
// exists to show: a fan-out, with a command the guard denied inside the child.
interface AgentBeat {
  minutesAgo: number;
  kind: "command" | "spawn";
  session: string;
  action: string;
  // The lease a spawn handed down, or the lease a command ran under.
  unit?: string;
  leaseFrom?: string;
  verdict?: "pass" | "deny";
}

const AGENT_HOST = "claude";
const AGENT_BEATS: readonly AgentBeat[] = [
  { minutesAgo: 6, kind: "command", session: "sess-orchestrator", action: "Bash", verdict: "pass" },
  {
    minutesAgo: 9,
    kind: "command",
    session: "sess-worker",
    action: "Bash",
    unit: "worker/ui-logs",
    leaseFrom: "flag",
    verdict: "deny",
  },
  {
    minutesAgo: 11,
    kind: "command",
    session: "sess-worker",
    action: "Edit",
    unit: "worker/ui-logs",
    leaseFrom: "flag",
    verdict: "pass",
  },
  {
    minutesAgo: 14,
    kind: "spawn",
    session: "sess-orchestrator",
    action: "Task",
    unit: "worker/ui-logs",
  },
  {
    minutesAgo: 16,
    kind: "command",
    session: "sess-orchestrator",
    action: "Bash",
    verdict: "pass",
  },
];

function agentEvents(now: number): ActivityEvent[] {
  return AGENT_BEATS.map((b) =>
    create(ActivityEventSchema, {
      time: ts(now - b.minutesAgo * 60_000),
      kind: b.kind === "spawn" ? Kind.AGENT_SPAWN : Kind.AGENT_COMMAND,
      actor: "agent:claude",
      action: b.action,
      outcome: Outcome.OK,
      host: AGENT_HOST,
      session: b.session,
      unit: b.unit ?? "",
      leaseFrom: b.leaseFrom ?? "",
      preview: b.verdict ? "guard: " + b.verdict : "",
    }),
  );
}

// demoEvents returns the synthetic trail, timed relative to `now` (epoch ms) so it reads as "just
// happened". The caller passes Date.now() at render time. Newest first, as the service returns.
export function demoEvents(now: number): ActivityEvent[] {
  const scenario = scenarioActivity(now).map((e) =>
    create(ActivityEventSchema, {
      time: ts(e.timeMs),
      kind: KIND[e.kind],
      actor: e.actor,
      action: e.action,
      outcome: e.ok ? Outcome.OK : Outcome.ERROR,
      duration: e.durationMs != null ? dur(e.durationMs) : undefined,
      error: e.error ?? "",
      requestBytes: e.requestBytes != null ? BigInt(e.requestBytes) : 0n,
      requestRef: e.requestRef ?? "",
      responseBytes: e.responseBytes != null ? BigInt(e.responseBytes) : 0n,
      responseRef: e.responseRef ?? "",
      preview: e.preview ?? "",
      workspace: e.workspace,
    }),
  );
  const at = (ev: ActivityEvent): number => Number(ev.time?.seconds ?? 0n);
  return [...scenario, ...agentEvents(now)].sort((a, b) => at(b) - at(a));
}
