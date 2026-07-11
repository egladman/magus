// sandbox.ts - the filesystem sandbox panel (NEW). Rolls up the magus.sandbox.*
// families from the metrics Snapshot: apply latency, the rule counts a sandbox was
// built from (read / write / exec / env), allow/deny check tallies, and dropped
// environment variables. The heading deep-links the Sandbox glossary term.

import type { DashboardState, SandboxView } from "../state";
import { fmtCount, fmtDur } from "../state";
import { MetricGrid } from "./widgets";
import { Card, type Tile } from "./card";

export function sandboxTile(): Tile {
  const card = new Card("sandbox", "Filesystem sandbox", { term: "Sandbox", note: "rules and access checks" });
  const grid = new MetricGrid([
    { caption: "Apply", items: [
      { key: "applyP50", label: "apply p50" }, { key: "applyP95", label: "apply p95" },
    ] },
    { caption: "Rules", items: [
      { key: "rulesRead", label: "read" }, { key: "rulesWrite", label: "write" }, { key: "rulesExec", label: "exec" }, { key: "envRules", label: "env rules" },
    ] },
    { caption: "Checks", items: [
      { key: "checksAllow", label: "allow" }, { key: "checksDeny", label: "deny" }, { key: "envDropped", label: "env dropped" },
    ] },
  ]);
  card.body.append(grid.el);

  function render(sb: SandboxView): void {
    grid.set("applyP50", fmtDur(sb.applyP50)); grid.set("applyP95", fmtDur(sb.applyP95));
    grid.set("rulesRead", fmtCount(sb.rulesRead)); grid.set("rulesWrite", fmtCount(sb.rulesWrite));
    grid.set("rulesExec", fmtCount(sb.rulesExec)); grid.set("envRules", fmtCount(sb.envRules));
    grid.set("checksAllow", fmtCount(sb.checksAllow)); grid.set("checksDeny", fmtCount(sb.checksDeny));
    grid.set("envDropped", fmtCount(sb.envDropped));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      const sb = s.metrics?.sandbox;
      card.el.hidden = !sb;
      if (sb) render(sb);
    },
    destroy() {},
  };
}
