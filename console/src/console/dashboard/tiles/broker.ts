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
  type ClaimView,
  type DashboardState,
  type WaitView,
} from "../state";
import { Card, h, type Tile } from "./card";
import { fitRows } from "./density";

// ABSENT says what a step does with no broker, per policy: the fact an operator needs when the
// card reads "not running".
const ABSENT: Record<BrokerPolicy, string> = {
  off: "off: runs never start or ask one, and each run hosts its own services",
  "best-effort": "not running: the next run starts one, and runs unarbitrated until it answers",
  required: "not running: steps refuse to start until one answers (MGS3022)",
};

function fact(label: string, value: string): HTMLElement {
  const li = h("li", "console-dashboard-row");
  li.append(
    h("code", "console-dashboard-row__cmd", label),
    h("span", "console-dashboard-row__meta", value),
  );
  return li;
}

// share renders "held/budget unit", or the held figure alone when the broker has not measured the
// budget: a zero there means unmeasured, not a host with no room.
function share(held: string, budget: number, budgetText: string): string {
  return budget > 0 ? held + " of " + budgetText : held + " (budget unmeasured)";
}

// waitText is a waiter's row: its place, what it asks for, and what keeps it out, named the way
// `magus status` names it in its blocked-by column.
function waitText(w: WaitView): string {
  const slots = Math.max(w.claim.slots, 1);
  const detail = ["waiting, place " + w.position, slots + (slots === 1 ? " slot" : " slots")];
  if (w.claim.memoryMb > 0) detail.push(fmtBytes(w.claim.memoryMb * 1024 * 1024));
  if (w.claim.pid) detail.push("pid " + w.claim.pid);
  const blocked: string[] = [];
  if (w.blockedBy.length && !w.ownRun) blocked.push("blocked by pid " + pids(w.blockedBy));
  if (w.ahead.length) blocked.push("behind pid " + pids(w.ahead));
  detail.push(blocked.length ? blocked.join(", ") : "blocked by its own run");
  const age = relTime(w.startTime);
  if (age) detail.push("for " + age);
  return detail.join(" - ");
}

function blockedKind(w: WaitView): string {
  if (w.ahead.length) return "ahead";
  return w.blockedBy.length && !w.ownRun ? "others" : "own-run";
}

function pids(cs: ClaimView[]): string {
  return cs.map((c) => String(c.pid)).join(", ");
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
  const facts = h("ul", "console-dashboard-rowlist");
  const holders = h("ul", "console-dashboard-rowlist");
  const waiters = h("ul", "console-dashboard-rowlist");
  waiters.setAttribute("aria-label", "waiting for capacity");
  card.body.append(facts, holders, waiters);

  function render(b: BrokerView | null, policy: BrokerPolicy): void {
    card.el.hidden = false;
    holders.replaceChildren();
    waiters.replaceChildren();
    if (!b) {
      state.textContent = policy === "off" ? "off" : "not running";
      facts.replaceChildren(fact("policy", ABSENT[policy]));
      return;
    }
    state.textContent = b.waiting.length ? "running, " + b.waiting.length + " waiting" : "running";
    const rows = [
      fact("slots", share(String(b.heldSlots), b.budgetSlots, String(b.budgetSlots))),
      fact(
        "memory",
        share(fmtBytes(b.heldMb * 1024 * 1024), b.budgetMb, fmtBytes(b.budgetMb * 1024 * 1024)),
      ),
      fact("process", "pid " + b.pid + (b.version ? " - " + b.version : "")),
      fact("policy", policy),
    ];
    const up = relTime(b.startTime);
    if (up) rows.push(fact("up", up));
    if (b.idleExitSeconds > 0)
      rows.push(fact("exits", "after " + b.idleExitSeconds + "s holding nothing"));
    if (b.socket) rows.push(fact("socket", b.socket));
    if (b.order) {
      const limit =
        b.backfillLimit > 0 ? ", passed over at most " + b.backfillLimit + " times" : "";
      rows.push(fact("line", b.order + limit));
    }
    facts.replaceChildren(...rows);
    for (const c of b.holders) {
      const li = h("li", "console-dashboard-row");
      const detail = [c.slots + (c.slots === 1 ? " slot" : " slots")];
      if (c.memoryMb > 0) detail.push(fmtBytes(c.memoryMb * 1024 * 1024));
      if (c.pid) detail.push("pid " + c.pid);
      const age = relTime(c.startTime);
      if (age) detail.push("held " + age);
      li.append(
        h("code", "console-dashboard-row__cmd", (c.project || ".") + ":" + c.target),
        h("span", "console-dashboard-row__meta", detail.join(" - ")),
      );
      // The holder's command and checkout are what name a holder from another worktree.
      li.title = [c.command, c.dir].filter(Boolean).join("\n");
      holders.append(li);
    }
    for (const w of b.waiting) {
      const li = h("li", "console-dashboard-row");
      li.dataset.blocked = blockedKind(w);
      li.append(
        h("code", "console-dashboard-row__cmd", (w.claim.project || ".") + ":" + w.claim.target),
        h("span", "console-dashboard-row__meta", waitText(w)),
      );
      li.title = [w.claim.command, w.claim.dir].filter(Boolean).join("\n");
      waiters.append(li);
    }
  }

  const unfit = fitRows(card.body, holders, (hidden) => "+" + String(hidden) + " more holders");

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.broker, s.status.brokerPolicy);
      else card.el.hidden = true;
    },
    destroy() {
      unfit();
    },
  };
}
