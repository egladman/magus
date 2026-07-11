// placeholders.ts - a clearly-marked seam for a feature that needs a server-side
// contract that has NOT landed yet. It renders a muted "coming soon" note today and is
// the drop-in point for the per-target volatility column. Deliberately inert (no data
// wiring) so the seam is obvious and typed. (The live execution timeline that shared
// this file has landed - see tiles/gantt.ts.)

import type { DashboardState } from "../state";
import { Card, h, type Tile } from "./card";

// TODO(wave-3b): the per-target volatility column. Blocked on a server volatility source (a
// pass/fail history per target) that has NOT landed. When it does, add a "Volatility" column
// to the per-target table (targets.ts) rather than a separate tile; this seam documents
// the dependency so it is not silently forgotten.
export function volatilityPlaceholderTile(): Tile {
  const card = new Card("volatility", "Volatility", { term: "Volatility", label: "volatility", note: "coming soon" });
  card.body.append(h("p", "seam-note",
    "A per-target volatility rate needs a server-side pass/fail history that is not on the wire yet. It will become a column in the per-target table."));
  return { el: card.el, update(_s: DashboardState) { /* seam: no server volatility source yet */ }, destroy() {} };
}
