// transport.ts - the Tools app's read: one ListTools call mapped into the ToolsView that both this
// app and the Dashboard's Toolchain tile read. The server forks a version probe per tool behind a
// TTL, so callers poll on getPollMs rather than faster.

import { createClient } from "@connectrpc/connect";
import {
  LifecycleState,
  Support,
  ToolService,
  Verdict,
  type ListToolsResponse,
} from "@wire/tool/v1alpha1/tool_pb";
import { createServerTransport } from "../../lib/server";
import {
  renderWindow,
  type LifecycleView,
  type ToolRowView,
  type ToolsView,
} from "../dashboard/state";

// UNKNOWN stays its own word: "we could not check" must not read as inside.
const verdictLabel = (v: Verdict): ToolRowView["verdict"] => {
  switch (v) {
    case Verdict.TOO_OLD:
      return "too old";
    case Verdict.TOO_NEW:
      return "too new";
    case Verdict.INSIDE:
      return "inside";
    default:
      return "unknown";
  }
};

const supportLabel = (s: Support): ToolRowView["support"] => {
  switch (s) {
    case Support.SUPPORTED:
      return "supported";
    case Support.EOL:
      return "eol";
    case Support.UNANNOUNCED:
      return "unannounced";
    case Support.UNKNOWN:
      return "unknown";
    default:
      return "";
  }
};

// An unset state reads as unwired: nothing was asked.
const lifecycleStateLabel = (s: LifecycleState): LifecycleView["state"] => {
  switch (s) {
    case LifecycleState.LIVE:
      return "live";
    case LifecycleState.CACHED:
      return "cached";
    case LifecycleState.OFFLINE:
      return "offline";
    case LifecycleState.UNREACHED:
      return "unreached";
    default:
      return "unwired";
  }
};

export function mapTools(resp: ListToolsResponse): ToolsView {
  const rows: ToolRowView[] = [];
  for (const proj of resp.projects) {
    for (const tool of proj.tools) {
      rows.push({
        project: proj.path,
        bin: tool.bin,
        spell: tool.spell,
        installed: tool.installedVersion,
        spellWindow: renderWindow(tool.spellBounds),
        workspaceWindow: renderWindow(tool.workspaceBounds),
        effectiveWindow: renderWindow(tool.effective),
        verdict: verdictLabel(tool.verdict),
        code: tool.diagnosticCode,
        probedAtMs: tool.probeTime
          ? Number(tool.probeTime.seconds) * 1000 + Math.floor(tool.probeTime.nanos / 1e6)
          : 0,
        cycle: tool.cycle,
        eol: tool.eol,
        support: supportLabel(tool.support),
      });
    }
  }
  return {
    rows,
    violations: rows.filter((r) => r.code !== "").length,
    lifecycle: {
      provider: resp.lifecycle?.provider ?? "",
      state: lifecycleStateLabel(resp.lifecycle?.state ?? LifecycleState.UNSPECIFIED),
      sources: resp.lifecycle?.sources ?? [],
      detail: resp.lifecycle?.detail ?? "",
    },
  };
}

// fetchToolsView resolves to null when the call failed. The server transport has already raised
// the failure to the reader, so a null is not reported a second time.
export async function fetchToolsView(
  host: string,
  token: string | null,
): Promise<ToolsView | null> {
  try {
    const client = createClient(ToolService, createServerTransport(host, token));
    return mapTools(await client.listTools({}));
  } catch {
    // reported: by the server transport.
    return null;
  }
}
