// mcp.ts - the MCP tool I/O table (NEW). One row per tool from the metrics Snapshot's
// mcp_tools: call/error tallies, input/output payload sizes (p50/p95/total), and call
// duration percentiles. No glossary term exists for MCP, so the heading is plain text.

import type { DashboardState, McpToolView } from "../state";
import { fmtBytes, fmtCount, fmtDur } from "../state";
import { SortableTable, type Column } from "./widgets";
import { Card, type Tile } from "./card";

const columns: Column<McpToolView>[] = [
  { key: "tool", label: "Tool", text: (r) => r.tool, sort: (r) => r.tool },
  {
    key: "calls",
    label: "Calls",
    numeric: true,
    text: (r) => fmtCount(r.calls),
    sort: (r) => r.calls,
  },
  {
    key: "errors",
    label: "Errors",
    numeric: true,
    text: (r) => fmtCount(r.errors),
    sort: (r) => r.errors,
  },
  {
    key: "inP50",
    label: "In p50",
    numeric: true,
    text: (r) => fmtBytes(r.inputP50Bytes),
    sort: (r) => r.inputP50Bytes,
  },
  {
    key: "inP95",
    label: "In p95",
    numeric: true,
    text: (r) => fmtBytes(r.inputP95Bytes),
    sort: (r) => r.inputP95Bytes,
  },
  {
    key: "inTotal",
    label: "In total",
    numeric: true,
    text: (r) => fmtBytes(r.inputTotal),
    sort: (r) => Number(r.inputTotal),
  },
  {
    key: "outP50",
    label: "Out p50",
    numeric: true,
    text: (r) => fmtBytes(r.outputP50Bytes),
    sort: (r) => r.outputP50Bytes,
  },
  {
    key: "outP95",
    label: "Out p95",
    numeric: true,
    text: (r) => fmtBytes(r.outputP95Bytes),
    sort: (r) => r.outputP95Bytes,
  },
  {
    key: "outTotal",
    label: "Out total",
    numeric: true,
    text: (r) => fmtBytes(r.outputTotal),
    sort: (r) => Number(r.outputTotal),
  },
  {
    key: "durP50",
    label: "Dur p50",
    numeric: true,
    text: (r) => fmtDur(r.durationP50Seconds),
    sort: (r) => r.durationP50Seconds,
  },
  {
    key: "durP95",
    label: "Dur p95",
    numeric: true,
    text: (r) => fmtDur(r.durationP95Seconds),
    sort: (r) => r.durationP95Seconds,
  },
];

export function mcpTile(): Tile {
  const card = new Card("mcp", "MCP tools", {
    defaultCollapsed: true,
    note: "tool I/O and latency",
  });
  const table = new SortableTable<McpToolView>(columns, {
    sortKey: "calls",
    emptyText: "No MCP tool calls recorded yet.",
  });
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
