// toolchain.ts - the Toolchain tile: a count of what needs acting on in the workspace's toolchain,
// and the way into the Tools app, which owns the table.
//
// Zero is worth stating as a number, because a tile that shows only the non-zero counts reads as
// "not checked" when the toolchain is fine. The note leads with the windows and carries the
// lifecycle provider's state, since an unreached provider leaves every end-of-life count at zero
// for a reason that is not good news.

import type { DashboardState } from "../state";
import { openSurface } from "../../../desktop/surface-navigation";
import { lifecycleNote, toolCounts } from "../../tools/model";
import { StatStrip } from "./widgets";
import { Card, h, type Tile } from "./card";

const RESTING_NOTE = "The binaries this workspace drives, and the version window each is held to.";

export function toolchainTile(): Tile {
  const card = new Card("toolchain", "Toolchain", { note: RESTING_NOTE });
  const counts = new StatStrip([
    { key: "eol", label: "Past end of life", accent: "err" },
    { key: "outside", label: "Outside window", accent: "err" },
    { key: "unannounced", label: "Unannounced", accent: "info" },
    { key: "unpinned", label: "Unpinned", accent: "info" },
  ]);
  // A workspace with no windows is a legitimate resting state, so the empty copy says what to
  // declare rather than only reporting an absence.
  const empty = h(
    "p",
    "console-dashboard-row__empty",
    "No project declares a probed tool. A spell declares what its ops need with supported; a project declares its own policy with the tools key.",
  );
  const open = document.createElement("button");
  open.type = "button";
  open.className = "pf-v6-c-button pf-m-link pf-m-inline";
  open.dataset.openSurface = "tools";
  open.append(h("span", "pf-v6-c-button__text", "Open Tools"));
  open.addEventListener("click", () => openSurface({ pageId: "tools" }));
  card.body.append(counts.el, empty, open);

  return {
    el: card.el,
    update(state: DashboardState) {
      const view = state.tools;
      const rows = view?.rows ?? [];
      const c = toolCounts(rows);
      counts.el.hidden = rows.length === 0;
      empty.hidden = rows.length > 0;
      counts.set("eol", String(c.pastEol));
      counts.set("outside", String(c.outsideWindow));
      counts.set("unannounced", String(c.unannounced));
      counts.set("unpinned", String(c.unpinned));
      const windows =
        rows.length === 0
          ? RESTING_NOTE
          : c.outsideWindow > 0
            ? c.outsideWindow + " outside their window"
            : c.total + " tools, all inside their window";
      const notes = [windows];
      const lifecycle = lifecycleNote(view?.lifecycle);
      if (lifecycle) notes.push(lifecycle);
      card.setNote(notes.join("; "));
    },
    destroy() {},
  };
}
