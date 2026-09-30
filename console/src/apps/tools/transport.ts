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
import { enumWord } from "../../lib/enums";
import type { ToolRowView, ToolsView } from "../dashboard/state";

// The server decides what a violation is and renders the windows, so this maps them through. The
// enums turn into words from their value names; the unspecified word is the honest reading of an
// unset value: a verdict nobody set is unknown ("we could not check" must not read as inside), a
// lifecycle state nobody set is unwired (nothing was asked), and no support is blank.
export function mapTools(resp: ListToolsResponse): ToolsView {
  const rows: ToolRowView[] = [];
  for (const proj of resp.projects) {
    for (const tool of proj.tools) {
      rows.push({
        project: proj.path,
        bin: tool.bin,
        spell: tool.spell,
        installed: tool.installedVersion,
        spellWindow: tool.spellWindow,
        workspaceWindow: tool.workspaceWindow,
        effectiveWindow: tool.effectiveWindow,
        verdict: enumWord(Verdict, tool.verdict, "unknown"),
        violation: tool.violation,
        code: tool.diagnosticCode,
        probedAtMs: tool.probeTime
          ? Number(tool.probeTime.seconds) * 1000 + Math.floor(tool.probeTime.nanos / 1e6)
          : 0,
        cycle: tool.cycle,
        eol: tool.eol,
        support: enumWord(Support, tool.support, ""),
      });
    }
  }
  return {
    rows,
    lifecycle: {
      provider: resp.lifecycle?.provider ?? "",
      state: enumWord(
        LifecycleState,
        resp.lifecycle?.state ?? LifecycleState.UNSPECIFIED,
        "unwired",
      ),
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
