// broker.ts - the broker: the per-user process holding this host's concurrency slots, declared
// memory and shared services, with the claims holding them right now. Every magus on the machine
// asks it before a step starts, so a run that waits on a slot is waiting on something listed here.
//
// A missing broker is not one state. Under `broker: off` nothing ever starts one; otherwise a run
// starts it on demand and it exits once it holds nothing, so "not running" between runs is normal.
// The card says which, and what a step does meanwhile, rather than a bare "down".

import {
  fmtBytes,
  relTime,
  type BrokerPolicy,
  type BrokerView,
  type DashboardState,
} from "../state";
import { Card, h, type Tile } from "./card";
import { fitRows } from "./density";
import { factList, type Fact } from "./widgets";

// ABSENT says what a step does with no broker, per policy: the fact an operator needs when the
// card reads "not running".
const ABSENT: Record<BrokerPolicy, string> = {
  off: "off: runs never start or ask one, and each run hosts its own services",
  "best-effort": "not running: the next run starts one, and runs unarbitrated until it answers",
  required: "not running: steps refuse to start until one answers (MGS3022)",
};

// share renders "held/budget unit", or the held figure alone when the broker has not measured the
// budget: a zero there means unmeasured, not a host with no room.
function share(held: string, budget: number, budgetText: string): string {
  return budget > 0 ? held + " of " + budgetText : held + " (budget unmeasured)";
}

export function brokerTile(): Tile {
  const card = new Card("broker", "Broker", {
    term: "Broker",
    label: "broker",
    why:
      "Every magus on this machine asks the broker before a step starts, so it is what keeps two" +
      " checkouts from both taking the whole host. A run waiting on a slot is waiting on a holder" +
      " listed here.",
  });
  const stateLabel = h("span", "pf-v6-c-label pf-m-compact");
  const state = h("span", "pf-v6-c-label__content", "-");
  stateLabel.append(state);
  card.noteNode().replaceWith(stateLabel);
  const facts = h("div", "console-dashboard-facts");
  const holders = h("ul", "console-dashboard-rowlist");
  card.body.append(facts, holders);

  // The facts list is rebuilt only when what it says changes: the broker is read once a frame and
  // almost never differs, and rebuilding a description list per second helps nobody.
  let painted = "";
  function setFacts(rows: Fact[]): void {
    const signature = JSON.stringify(rows.map((r) => [r.term, r.value]));
    if (signature === painted) return;
    painted = signature;
    facts.replaceChildren(factList(rows));
  }

  function render(b: BrokerView | null, policy: BrokerPolicy): void {
    card.setEmpty(null);
    holders.replaceChildren();
    if (!b) {
      state.textContent = policy === "off" ? "off" : "not running";
      setFacts([{ term: "Policy", value: ABSENT[policy] }]);
      return;
    }
    state.textContent = "running";
    const rows: Fact[] = [
      {
        term: "Slots",
        value: share(String(b.heldSlots), b.budgetSlots, String(b.budgetSlots)),
      },
      {
        term: "Memory",
        value: share(
          fmtBytes(b.heldMb * 1024 * 1024),
          b.budgetMb,
          fmtBytes(b.budgetMb * 1024 * 1024),
        ),
      },
      { term: "Process", value: "pid " + b.pid + (b.version ? ", " + b.version : "") },
      { term: "Policy", value: policy },
    ];
    const up = relTime(b.startTime);
    if (up) rows.push({ term: "Up", value: up });
    if (b.idleExitSeconds > 0)
      rows.push({ term: "Exits", value: "after " + b.idleExitSeconds + "s holding nothing" });
    if (b.socket) rows.push({ term: "Socket", value: b.socket });
    setFacts(rows);
    for (const c of b.holders) {
      const li = h("li", "console-dashboard-row");
      const detail = [c.slots + (c.slots === 1 ? " slot" : " slots")];
      if (c.memoryMb > 0) detail.push(fmtBytes(c.memoryMb * 1024 * 1024));
      if (c.pid) detail.push("pid " + c.pid);
      const age = relTime(c.startTime);
      if (age) detail.push("held " + age);
      li.append(
        h("code", "console-dashboard-row__cmd", (c.project || ".") + ":" + c.target),
        h("span", "console-dashboard-row__meta", detail.join(", ")),
      );
      // The holder's command and checkout are what name a holder from another worktree, so they are
      // text on the row rather than a tooltip.
      const where = [c.command, c.dir].filter(Boolean).join("  ");
      if (where) li.append(h("code", "console-dashboard-row__detail", where));
      holders.append(li);
    }
  }

  const unfit = fitRows(card.body, holders, (hidden) => "+" + String(hidden) + " more holders");

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.broker, s.status.brokerPolicy);
    },
    destroy() {
      unfit();
    },
  };
}
