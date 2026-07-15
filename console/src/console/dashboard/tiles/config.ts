// config.ts - the daemon's resolved read-only configuration: the default charms it applies to every
// run (as effect-colored pills), the concurrency cap, and whether the sandbox is on. Read once from
// the JSON status endpoint (not the proto event stream), so the card hides until that arrives. Lets
// an operator see what the daemon is set to do without dropping to the terminal.

import type { DashboardState, ConfigView } from "../state";
import { Card, h, type Tile } from "./card";

// Color a default charm by its EFFECT: rw mutates the tree (loud), cd delivers a side effect, gha
// shapes output (benign), anything else is a custom charm (neutral).
function charmEffect(charm: string): string {
  if (charm === "rw") return "loud";
  if (charm === "cd") return "deliver";
  if (charm === "gha") return "benign";
  return "neutral";
}

export function configTile(): Tile {
  const card = new Card("config", "Configuration", { term: "Charm", label: "default charms" });
  const body = h("div", "config-body");
  card.body.append(body);

  function row(key: string, valueEl: HTMLElement): HTMLElement {
    const r = h("div", "config-row");
    r.append(h("span", "config-k", key), valueEl);
    return r;
  }

  function render(c: ConfigView, version: string, daemonVersion: string): void {
    card.el.hidden = false;
    const pills = h("span", "config-charms");
    if (c.defaultCharms.length) {
      for (const ch of c.defaultCharms) {
        const p = h("span", "charm-pill", ch);
        p.dataset.effect = charmEffect(ch);
        pills.append(p);
      }
    } else {
      pills.append(h("span", "config-v", "none"));
    }
    const rows = [
      row("Default charms", pills),
      row("Concurrency", h("span", "config-v", c.concurrency ? String(c.concurrency) : "auto")),
      row("Sandbox", h("span", "config-v", c.sandbox ? "on" : "off")),
    ];
    // The daemon's magus version lives here with the rest of the config, not as a stray number card.
    // Show the daemon version too only when it differs from the reported magus version.
    if (version) {
      const v = daemonVersion && daemonVersion !== version ? version + " (daemon " + daemonVersion + ")" : version;
      rows.push(row("magus version", h("span", "config-v", v)));
    }
    body.replaceChildren(...rows);
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.config) render(s.config, s.status?.magusVersion ?? "", s.status?.daemonVersion ?? "");
      else card.el.hidden = true;
    },
    destroy() {},
  };
}
