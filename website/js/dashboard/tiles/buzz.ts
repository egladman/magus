// buzz.ts - the Buzz interpreter panel (NEW). Rolls up the magus.buzz.* families
// from the metrics Snapshot: script exec/compile latency, the native-boundary
// host-call family, session-pool health (reuse / idle / evictions / warm), import and
// spell resolution, and the VM-level jit/fault counters. The heading deep-links Buzz.

import type { DashboardState, BuzzView } from "../state";
import { fmtCount, fmtDur } from "../state";
import { MetricGrid } from "./widgets";
import { Card, type Tile } from "./card";

export function buzzTile(): Tile {
  const card = new Card("buzz", "Buzz interpreter", { term: "Buzz", defaultCollapsed: true, note: "script exec and session pool" });
  const grid = new MetricGrid([
    { caption: "Execution", items: [
      { key: "execCount", label: "exec count" }, { key: "execP50", label: "exec p50" }, { key: "execP95", label: "exec p95" },
      { key: "compileCount", label: "compile count" }, { key: "compileP50", label: "compile p50" }, { key: "compileP95", label: "compile p95" },
      { key: "hostCallCount", label: "host calls" }, { key: "hostCallP50", label: "host p50" }, { key: "hostCallP95", label: "host p95" },
    ] },
    { caption: "Session pool", items: [
      { key: "reuse", label: "reuse" }, { key: "idle", label: "idle" }, { key: "evictions", label: "evictions" },
      { key: "warmP50", label: "warm p50" }, { key: "warmP95", label: "warm p95" },
    ] },
    { caption: "Resolution and VM", items: [
      { key: "importCount", label: "imports" }, { key: "importP50", label: "import p50" }, { key: "importP95", label: "import p95" },
      { key: "resolveCount", label: "spell resolves" }, { key: "resolveP50", label: "resolve p50" }, { key: "resolveP95", label: "resolve p95" },
      { key: "jit", label: "jit runs" }, { key: "faults", label: "vm faults" },
    ] },
  ]);
  card.body.append(grid.el);

  function render(b: BuzzView): void {
    grid.set("execCount", fmtCount(b.execCount)); grid.set("execP50", fmtDur(b.execP50)); grid.set("execP95", fmtDur(b.execP95));
    grid.set("compileCount", fmtCount(b.compileCount)); grid.set("compileP50", fmtDur(b.compileP50)); grid.set("compileP95", fmtDur(b.compileP95));
    grid.set("hostCallCount", fmtCount(b.hostCallCount)); grid.set("hostCallP50", fmtDur(b.hostCallP50)); grid.set("hostCallP95", fmtDur(b.hostCallP95));
    grid.set("reuse", fmtCount(b.sessionPoolReuse)); grid.set("idle", fmtCount(b.sessionPoolIdle)); grid.set("evictions", fmtCount(b.sessionPoolEvictions));
    grid.set("warmP50", fmtDur(b.sessionWarmP50)); grid.set("warmP95", fmtDur(b.sessionWarmP95));
    grid.set("importCount", fmtCount(b.importCount)); grid.set("importP50", fmtDur(b.importP50)); grid.set("importP95", fmtDur(b.importP95));
    grid.set("resolveCount", fmtCount(b.spellResolveCount)); grid.set("resolveP50", fmtDur(b.spellResolveP50)); grid.set("resolveP95", fmtDur(b.spellResolveP95));
    grid.set("jit", fmtCount(b.jitRuns)); grid.set("faults", fmtCount(b.vmFaults));
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
