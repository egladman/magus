// buzz.ts - the Buzz interpreter panel (NEW). Rolls up the magus.buzz.* families
// from the metrics Snapshot: script exec/compile latency, the native-boundary
// host-call family, session-pool health (reuse / idle / evictions / warm), import and
// spell resolution, and the VM-level jit/fault counters. The heading deep-links Buzz.

import type { DashboardState, BuzzView } from "../state";
import { fmtCount, fmtDur } from "../state";
import { MetricGrid } from "./widgets";
import { Card, type Tile } from "./card";

export function buzzTile(): Tile {
  const card = new Card("buzz", "Buzz interpreter", {
    term: "Buzz",
    defaultCollapsed: true,
    note: "script exec and session pool",
  });
  const grid = new MetricGrid([
    {
      caption: "Execution",
      items: [
        { key: "execCount", label: "exec count" },
        { key: "execP50Seconds", label: "exec p50" },
        { key: "execP95Seconds", label: "exec p95" },
        { key: "compileCount", label: "compile count" },
        { key: "compileP50Seconds", label: "compile p50" },
        { key: "compileP95Seconds", label: "compile p95" },
        { key: "hostCallCount", label: "host calls" },
        { key: "hostCallP50Seconds", label: "host p50" },
        { key: "hostCallP95Seconds", label: "host p95" },
      ],
    },
    {
      caption: "Session pool",
      items: [
        { key: "reuse", label: "reuse" },
        { key: "idle", label: "idle" },
        { key: "evictions", label: "evictions" },
        { key: "warmP50", label: "warm p50" },
        { key: "warmP95", label: "warm p95" },
      ],
    },
    {
      caption: "Resolution and VM",
      items: [
        { key: "importCount", label: "imports" },
        { key: "importP50Seconds", label: "import p50" },
        { key: "importP95Seconds", label: "import p95" },
        { key: "resolveCount", label: "spell resolves" },
        { key: "resolveP50", label: "resolve p50" },
        { key: "resolveP95", label: "resolve p95" },
        { key: "jit", label: "jit runs" },
        { key: "faults", label: "vm faults" },
      ],
    },
  ]);
  card.body.append(grid.el);

  function render(b: BuzzView): void {
    grid.set("execCount", fmtCount(b.execCount));
    grid.set("execP50Seconds", fmtDur(b.execP50Seconds));
    grid.set("execP95Seconds", fmtDur(b.execP95Seconds));
    grid.set("compileCount", fmtCount(b.compileCount));
    grid.set("compileP50Seconds", fmtDur(b.compileP50Seconds));
    grid.set("compileP95Seconds", fmtDur(b.compileP95Seconds));
    grid.set("hostCallCount", fmtCount(b.hostCallCount));
    grid.set("hostCallP50Seconds", fmtDur(b.hostCallP50Seconds));
    grid.set("hostCallP95Seconds", fmtDur(b.hostCallP95Seconds));
    grid.set("reuse", fmtCount(b.sessionPoolReuse));
    grid.set("idle", fmtCount(b.sessionPoolIdle));
    grid.set("evictions", fmtCount(b.sessionPoolEvictions));
    grid.set("warmP50", fmtDur(b.sessionWarmP50Seconds));
    grid.set("warmP95", fmtDur(b.sessionWarmP95Seconds));
    grid.set("importCount", fmtCount(b.importCount));
    grid.set("importP50Seconds", fmtDur(b.importP50Seconds));
    grid.set("importP95Seconds", fmtDur(b.importP95Seconds));
    grid.set("resolveCount", fmtCount(b.spellResolveCount));
    grid.set("resolveP50", fmtDur(b.spellResolveP50Seconds));
    grid.set("resolveP95", fmtDur(b.spellResolveP95Seconds));
    grid.set("jit", fmtCount(b.jitRuns));
    grid.set("faults", fmtCount(b.vmFaults));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      const b = s.metrics?.buzz;
      card.el.hidden = !b;
      if (b) render(b);
    },
    destroy() {},
  };
}
