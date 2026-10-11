// remote.ts - the remote cache panel: outcome tallies plus transfer latency and
// volume, from the metrics Snapshot's Remote family. Already on the wire; this tile
// renders it. The heading deep-links the Remote cache glossary term.

import type { DashboardState, RemoteView } from "../state";
import { fmtBytes, fmtCount, fmtDur, fmtPct } from "../state";
import { factList, type Fact } from "./widgets";
import { Card, h, type Tile } from "./card";

export function remoteTile(): Tile {
  const card = new Card("remote", "Remote cache", {
    term: "Remote cache",
    label: "remote cache",
    note: "get / put over the network",
    why:
      "Whether the team shares build results or everyone pays for the same work. A low remote hit" +
      " rate means keys are not matching across machines. Errors mean the round trip bought nothing.",
  });
  const body = h("div", "console-dashboard-facts");
  card.body.append(body);

  let painted = "";
  function render(r: RemoteView): void {
    card.setEmpty(null);
    const rows: Fact[] = [
      { term: "Hits", value: fmtCount(r.hits) },
      { term: "Misses", value: fmtCount(r.misses) },
      { term: "Errors", value: fmtCount(r.errors) },
      { term: "Hit rate", value: fmtPct(r.hitRate) },
      { term: "Transfer p50", value: fmtDur(r.durationP50Seconds) },
      { term: "Transfer p95", value: fmtDur(r.durationP95Seconds) },
      { term: "IO operations", value: fmtCount(r.ioCount) },
      { term: "Bytes moved", value: fmtBytes(r.bytesTotal) },
    ];
    const signature = JSON.stringify(rows.map((f) => f.value));
    if (signature === painted) return;
    painted = signature;
    body.replaceChildren(factList(rows, 2));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      const r = s.metrics?.remote;
      if (r) render(r);
      else
        card.setEmpty(
          "No remote cache traffic has been reported. Configure a remote cache to see it here.",
        );
    },
    destroy() {},
  };
}
