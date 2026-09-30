// demo.ts - the toolchain the #demo console shows: one tool past its end of life, one unpinned, one
// the provider has no release schedule for, so every filter has something to find.

import type { ToolRowView, ToolsView } from "../dashboard/state";

const row = (over: Partial<ToolRowView>): ToolRowView => ({
  project: ".",
  bin: "go",
  spell: "go",
  installed: "v1.26.5",
  spellWindow: ">= 1.25",
  workspaceWindow: ">= 1.26",
  effectiveWindow: ">= 1.26",
  verdict: "inside",
  violation: false,
  code: "",
  probedAtMs: 0,
  cycle: "1.26",
  eol: "2027-02-11",
  support: "supported",
  ...over,
});

// demoToolsView stamps the probes relative to now, so their ages read as recent.
export function demoToolsView(now: number): ToolsView {
  const rows = [
    row({ probedAtMs: now - 40_000 }),
    row({
      project: "services/identity",
      installed: "v1.25.3",
      workspaceWindow: "",
      effectiveWindow: ">= 1.25",
      cycle: "1.25",
      eol: "2026-08-19",
      support: "eol",
      probedAtMs: now - 40_000,
    }),
    row({
      project: "console",
      bin: "node",
      spell: "typescript",
      installed: "v24.11.0",
      spellWindow: "",
      workspaceWindow: ">= 22, < 25",
      effectiveWindow: ">= 22, < 25",
      cycle: "24",
      eol: "2028-04-30",
      probedAtMs: now - 95_000,
    }),
    row({
      project: "docs",
      bin: "pnpm",
      spell: "pnpm",
      installed: "10.4.1",
      spellWindow: "",
      workspaceWindow: "",
      effectiveWindow: "",
      cycle: "",
      eol: "",
      support: "unannounced",
      probedAtMs: now - 95_000,
    }),
  ];
  return {
    rows,
    lifecycle: { provider: "endoflife-date", state: "cached", sources: [], detail: "" },
  };
}
