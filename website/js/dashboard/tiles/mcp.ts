// mcp.ts - the MCP tool I/O table (NEW). One row per tool from the metrics Snapshot's
// mcp_tools: call/error tallies, input/output payload sizes (p50/p95/total), and call
// duration percentiles. No glossary term exists for MCP, so the heading is plain text.

import type { DashboardState, McpToolView } from "../state";
import { fmtBytes, fmtCount, fmtDur } from "../state";
import { SortableTable, type Column } from "./widgets";
import { Card, type Tile } from "./card";

const columns: Column<McpToolView>[] = [
  { key: "tool", label: "Tool", text: (r) => r.tool, sort: (r) => r.tool },
  { key: "calls", label: "Calls", numeric: true, text: (r) => fmtCount(r.calls), sort: (r) => r.calls },
  { key: "errors", label: "Errors", numeric: true, text: (r) => fmtCount(r.errors), sort: (r) => r.errors },
  { key: "inP50", label: "In p50", numeric: true, text: (r) => fmtBytes(r.inputP50), sort: (r) => r.inputP50 },
  { key: "inP95", label: "In p95", numeric: true, text: (r) => fmtBytes(r.inputP95), sort: (r) => r.inputP95 },
  { key: "inTotal", label: "In total", numeric: true, text: (r) => fmtBytes(r.inputTotal), sort: (r) => Number(r.inputTotal) },
  { key: "outP50", label: "Out p50", numeric: true, text: (r) => fmtBytes(r.outputP50), sort: (r) => r.outputP50 },
  { key: "outP95", label: "Out p95", numeric: true, text: (r) => fmtBytes(r.outputP95), sort: (r) => r.outputP95 },
  { key: "outTotal", label: "Out total", numeric: true, text: (r) => fmtBytes(r.outputTotal), sort: (r) => Number(r.outputTotal) },
  { key: "durP50", label: "Dur p50", numeric: true, text: (r) => fmtDur(r.durationP50), sort: (r) => r.durationP50 },
  { key: "durP95", label: "Dur p95", numeric: true, text: (r) => fmtDur(r.durationP95), sort: (r) => r.durationP95 },
];

export function mcpTile(): Tile {
  const card = new Card("mcp", "MCP tools", { note: "tool I/O and latency" });
  const table = new SortableTable<McpToolView>(columns, { sortKey: "calls", emptyText: "No MCP tool calls recorded yet." });
  card.body.append(table.el);

  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.metrics) return;
      card.setNote(`${s.metrics.mcpTools.length} tools`);
      table.setRows(s.metrics.mcpTools);
    },
    destroy() {},
  };
}
