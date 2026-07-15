// demo.ts - a synthesized activity trail for the daemon-free showcase (the shared #demo fragment) and
// the no-daemon empty state's "see the demo" path. It fabricates a representative page of
// ActivityEvents - an MCP tool call with payload sizes and a response preview, a background job, a
// config change, a token mint, a denied sandbox write, and a failed tool call - so the Activity
// surface's rendering (foldable, status-accented sections, ok vs error accents) is fully inspectable
// without a running daemon. Newest first, the order the service returns.

import { create } from "@bufbuild/protobuf";
import { ActivityEventSchema, Kind, Outcome, type ActivityEvent } from "../../gen/magus/activity/v1/activity_pb";

// ago builds a protobuf Timestamp init `secs` seconds before `now` (epoch ms). Plain init objects are
// enough - create() accepts them for the nested well-known types, and the adapter reads seconds/nanos.
function ago(now: number, secs: number): { seconds: bigint; nanos: number } {
  const ms = now - secs * 1000;
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: (ms % 1000) * 1_000_000 };
}

// dur builds a protobuf Duration init from a millisecond count.
function dur(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.round((ms % 1000) * 1_000_000) };
}

// demoEvents returns the synthetic trail, timed relative to `now` (epoch ms) so it reads as "just
// happened". The caller passes Date.now() at render time.
export function demoEvents(now: number): ActivityEvent[] {
  return [
    create(ActivityEventSchema, {
      time: ago(now, 3), kind: Kind.MCP_TOOL_CALL, actor: "claude-code", action: "magus_run_target",
      outcome: Outcome.OK, duration: dur(842),
      requestBytes: 214n, requestRef: "reqa1b2c3", responseBytes: 5310n, responseRef: "ref9f8e7d",
      preview: "ok  build  cached\nok  test   0.4s", workspace: "magus",
    }),
    create(ActivityEventSchema, {
      time: ago(now, 12), kind: Kind.JOB, actor: "daemon", action: "symbol-reindex",
      outcome: Outcome.OK, duration: dur(1260),
      preview: "indexed 1648 symbols across 42 files", workspace: "magus",
    }),
    create(ActivityEventSchema, {
      time: ago(now, 48), kind: Kind.MCP_TOOL_CALL, actor: "claude-code", action: "magus_query",
      outcome: Outcome.OK, duration: dur(37),
      requestBytes: 96n, responseBytes: 1820n, responseRef: "ref33aa11",
      preview: "kind:spell project:website  ->  12 matches", workspace: "website",
    }),
    create(ActivityEventSchema, {
      time: ago(now, 95), kind: Kind.CONFIG_CHANGE, actor: "eli", action: "mcp connector add",
      outcome: Outcome.OK,
      preview: "added connector token \"laptop\" (expires in 30d)", workspace: "magus",
    }),
    create(ActivityEventSchema, {
      time: ago(now, 140), kind: Kind.TOKEN_LIFECYCLE, actor: "daemon", action: "cli token issued",
      outcome: Outcome.OK, workspace: "magus",
    }),
    create(ActivityEventSchema, {
      time: ago(now, 210), kind: Kind.SANDBOX_DENIAL, actor: "build:internal/cache", action: "write /etc/hosts",
      outcome: Outcome.ERROR,
      error: "sandbox denied a write outside the workspace: /etc/hosts", workspace: "magus",
    }),
    create(ActivityEventSchema, {
      time: ago(now, 320), kind: Kind.MCP_TOOL_CALL, actor: "claude-code", action: "magus_output",
      outcome: Outcome.ERROR, duration: dur(8),
      error: "unknown output reference: refdeadbe", requestBytes: 40n, workspace: "magus",
    }),
  ];
}
