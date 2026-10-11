// config.ts - the server's resolved read-only configuration: the default charms it applies to every
// run, the concurrency cap, and whether the sandbox is on. Read once from the typed status RPC (not
// the event stream), so the card says it is waiting until that arrives. Lets an operator see what
// the server is set to do without dropping to the terminal.

import type { DashboardState, ConfigView } from "../state";
import { Card, h, type Tile } from "./card";
import { factList, type Fact } from "./widgets";

// charmLabel is one default charm as a PF Label whose link is the label's own content, pointed at
// the Graph Explorer's deep-link grammar (kind:/project:/relation:/id:/symbol:, the browser twin of
// `magus query` - see graph/main.ts's QUERY_FIELDS), scoped to that charm's node so its "uses" edges
// are one click from here. Every charm is the one neutral colour: the old red for rw read as a fault
// where the charm only says a run may write.
function charmLabel(charm: string): HTMLElement {
  const label = h("span", "pf-v6-c-label pf-m-compact");
  const link = h("a", "pf-v6-c-label__content") as HTMLAnchorElement;
  link.href = "../graph/#q=" + encodeURIComponent("kind:charm " + charm);
  link.append(h("span", "pf-v6-c-label__text", charm));
  label.append(link);
  return label;
}

export function configTile(): Tile {
  const card = new Card("config", "Configuration", { term: "Charm", label: "default charms" });
  const body = h("div", "console-dashboard-facts");
  card.body.append(body);

  let painted = "";
  function render(c: ConfigView, version: string, ownerVersion: string): void {
    card.setEmpty(null);
    // The server's magus version lives here with the rest of the config, not as a stray number card.
    // Show the pool owner's build too only when it differs from the reported one.
    const shownVersion =
      ownerVersion && ownerVersion !== version ? version + " (pool " + ownerVersion + ")" : version;
    // The frame arrives every second and the configuration almost never changes, so the list is
    // rebuilt only when what it says does.
    const signature = JSON.stringify([c.defaultCharms, c.concurrency, c.sandbox, shownVersion]);
    if (signature === painted) return;
    painted = signature;

    const charms = h("span", "console-dashboard-config__charms");
    if (c.defaultCharms.length) charms.append(...c.defaultCharms.map(charmLabel));
    else charms.textContent = "none";
    const rows: Fact[] = [
      {
        term: "Default charms",
        value: charms,
        help:
          "Applied to every target that does not declare its own: rw allows the run to modify the" +
          " tree, cd delivers a side effect (a deploy or publish), gha shapes CI output. Click a" +
          " charm to see everything that uses it in the graph explorer.",
      },
      {
        term: "Concurrency",
        value: c.concurrency ? String(c.concurrency) : "auto",
        help: "The most targets this server runs at once. auto sizes to the machine's CPU count.",
      },
      {
        term: "Sandbox",
        value: c.sandbox ? "on" : "off",
        help:
          "Whether target execution is confined to a sandbox that can restrict filesystem and" +
          " network access beyond a target's own declared workspace.",
      },
    ];
    if (shownVersion) rows.push({ term: "magus version", value: shownVersion });
    body.replaceChildren(factList(rows));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.config) render(s.config, s.status?.magusVersion ?? "", s.status?.ownerVersion ?? "");
      else card.setEmpty("The server has not reported its configuration yet.");
    },
    destroy() {},
  };
}
