// placeholders.ts - clearly-marked seams for Wave 3b. These render a muted
// "coming soon" note today and are the drop-in points for two features that need a
// server-side contract that has NOT landed yet. They are deliberately inert (no data
// wiring) so the seam is obvious and typed.

import type { DashboardState } from "../state";
import { Card, h, type Tile } from "./card";

// TODO(wave-3b): live execution Gantt + graph recolor. Blocked on the run-state wire
// contract landing in status_pb.ts (per-span start/end/state). When it arrives, this
// tile subscribes to that slice and draws a timeline; the graph explorer recolors from
// the same signal. Until then it is a labeled seam, not a stub with fake data.
export function ganttPlaceholderTile(): Tile {
  const card = new Card("gantt", "Live execution", { term: "Trace", label: "trace", note: "coming soon" });
  card.body.append(h("p", "seam-note",
    "A live execution timeline (per-span start, duration, state) lands with the run-state wire contract. This tile is the seam it drops into."));
  return { el: card.el, update(_s: DashboardState) { /* seam: no wire contract yet */ }, destroy() {} };
}

// TODO(wave-3b): the per-target flake column. Blocked on a server flake source (a
// pass/fail history per target) that has NOT landed. When it does, add a "Flake" column
// to the per-target table (targets.ts) rather than a separate tile; this seam documents
// the dependency so it is not silently forgotten.
export function flakePlaceholderTile(): Tile {
  const card = new Card("flake", "Flake", { term: "Flake", label: "flake", note: "coming soon" });
  card.body.append(h("p", "seam-note",
    "A per-target flake rate needs a server-side pass/fail history that is not on the wire yet. It will become a column in the per-target table."));
  return { el: card.el, update(_s: DashboardState) { /* seam: no server flake source yet */ }, destroy() {} };
}
