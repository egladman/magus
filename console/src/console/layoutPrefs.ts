// layoutPrefs.ts - the persisted cells for UI-shape preferences: the things a person ends up
// with by USING the console rather than by opening Settings (a split they dragged, a zoom they
// set, cards they folded away).
//
// They live together for one reason: the Settings surface exports and imports them as the
// envelope's `layout` section, and it must not re-declare their storage keys and fallbacks to do
// it. Two `persisted()` cells over one key are two sources of truth for its default, and they
// drift the moment one side changes - so the owning module and the exporter share the cell
// itself, not the string.
//
// What belongs here: a preference someone chose deliberately and would want on their other
// machine. What does not: one-time bookkeeping (dashboard-collapse-seeded), session state (open
// tabs, the last daemon connected to), and test fixtures. See settings/model.ts for why each of
// those is excluded rather than merely forgotten.

import { persisted } from "../lib/persist";

// Bumped only when the MEANING of the stored value changes. A mismatch is discarded, never
// migrated. Owned here because the split cell is declared here.
export const SPLIT_SCHEMA = 1;

export interface SplitPref {
  v: number;
  cols: number;
}

// The default direction a plain split takes (mod+\, and the Panes tray's Horizontal/Vertical
// picks): "row" (side by side) or "col" (stacked). Global - a choice made once sticks across
// every tab and reload rather than resetting.
export const splitModeCell = persisted<"row" | "col">("split-mode", "row");

// Dashboard cards the reader has folded away, by card id.
export const collapsedCardsCell = persisted<string[]>("dashboard-collapsed", []);

// Log viewer text zoom, as a multiplier.
export const logsZoomCell = persisted<number>("logs-zoom", 1);

// Big Picture split-handle position, in grid columns. The bounds live here with the cell so the
// fallback cannot disagree with them; split.ts still owns what they MEAN and does all the
// validation and clamping on read.
export const SPLIT_DEFAULT = 8;
export const SPLIT_MIN = 6; // below this the run timeline cannot show a target label
export const SPLIT_MAX = 10; // above this the activity column is too narrow for a line of output

export const bigPictureSplitCell = persisted<SplitPref>("dashboard-bigpicture-split", {
  v: SPLIT_SCHEMA,
  cols: SPLIT_DEFAULT,
});
