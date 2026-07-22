// config.ts - the daemon's resolved read-only configuration: the default charms it applies to every
// run (as effect-colored pills), the concurrency cap, and whether the sandbox is on. Read once from
// the JSON status endpoint (not the proto event stream), so the card hides until that arrives. Lets
// an operator see what the daemon is set to do without dropping to the terminal.

import type { DashboardState, ConfigView } from "../state";
import { Card, h, type Tile } from "./card";

// Color a default charm by its EFFECT, mapped to a PatternFly Label color modifier: rw mutates the
// tree (loud -> red), cd delivers a side effect (blue), gha shapes output (benign -> green). A custom
// charm is neutral: the empty string leaves the default PF Label, which is grey (PF v6 has no
// pf-m-grey modifier - grey IS the uncolored default).
function charmColor(charm: string): string {
  if (charm === "rw") return "red";
  if (charm === "cd") return "blue";
  if (charm === "gha") return "green";
  return "";
}

export function configTile(): Tile {
  const card = new Card("config", "Configuration", { term: "Charm", label: "default charms" });
  const body = h("div", "console-dashboard-config__body");
  card.body.append(body);

  function row(key: string, valueEl: HTMLElement): HTMLElement {
    const r = h("div", "console-dashboard-config__row");
    r.append(h("span", "console-dashboard-config__key", key), valueEl);
    return r;
  }

  function render(c: ConfigView, version: string, daemonVersion: string): void {
    card.el.hidden = false;
    const pills = h("span", "console-dashboard-config__charms");
    if (c.defaultCharms.length) {
      for (const ch of c.defaultCharms) {
        const color = charmColor(ch);
        const p = h("span", "pf-v6-c-label pf-m-compact" + (color ? " pf-m-" + color : ""));
        p.append(h("span", "pf-v6-c-label__content", ch));
        pills.append(p);
      }
    } else {
      pills.append(h("span", "console-dashboard-config__value", "none"));
    }
    const rows = [
      row("Default charms", pills),
      row(
        "Concurrency",
        h(
          "span",
          "console-dashboard-config__value",
          c.concurrency ? String(c.concurrency) : "auto",
        ),
      ),
      row("Sandbox", h("span", "console-dashboard-config__value", c.sandbox ? "on" : "off")),
    ];
    // The daemon's magus version lives here with the rest of the config, not as a stray number card.
    // Show the daemon version too only when it differs from the reported magus version.
    if (version) {
      const v =
        daemonVersion && daemonVersion !== version
          ? version + " (daemon " + daemonVersion + ")"
          : version;
      rows.push(row("magus version", h("span", "console-dashboard-config__value", v)));
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
