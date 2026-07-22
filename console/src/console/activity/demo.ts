// demo.ts - the activity trail for the daemon-free showcase (the shared #demo fragment) and the
// no-daemon empty state's "see the demo" path. It maps the shared scenario's activity beats
// (demo-scenario.ts) into ActivityEvent protos, so the trail tells the SAME story as the recent-runs
// tree, the log waterfall, and the dashboard: the same agent MCP calls that drove the runs (tied by
// responseRef and timing), the branch-switch reindex job, the config/token beats that authorized the
// agent, the denied sandbox write, and the failed lookup for a pruned ref. Newest first, the order
// the service returns. Because it is the scenario's data in wire shape, the Activity surface's
// rendering (foldable, status-accented sections, ok vs error accents, the kind-grouped index) is
// fully inspectable without a running daemon.

import { create } from "@bufbuild/protobuf";
import {
  ActivityEventSchema,
  Kind,
  Outcome,
  type ActivityEvent,
} from "../../gen/magus/activity/v1/activity_pb";
import { scenarioActivity, type ActKind } from "../demo-scenario";

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

// demoEvents returns the synthetic trail, timed relative to `now` (epoch ms) so it reads as "just
// happened". The caller passes Date.now() at render time.
export function demoEvents(now: number): ActivityEvent[] {
  return scenarioActivity(now).map((e) =>
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
}
