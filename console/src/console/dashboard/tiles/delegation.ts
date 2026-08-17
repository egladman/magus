import type { DashboardState } from "../state";
import {
  buildPlan,
  loadLedger,
  overviewLine,
  STATE_LABEL,
  STATE_MARK,
  treeOrder,
  type LedgerRead,
} from "../../plan/ledger";
import { demoOverlaps, demoUnits } from "../../plan/demo";
import { openSurface } from "../../surface-navigation";
import { Card, h, type Tile } from "./card";

const REFRESH_MS = 4_000;
const REQUEST_TIMEOUT_MS = 10_000;

export function delegationTile(): Tile {
  const card = new Card("delegation", "Work plan", {
    note: "waiting for ledger",
    why: "Declared work and anything that needs intervention.",
  });
  const summary = h("p", "console-dashboard-delegation__summary", "No active delegation.");
  summary.setAttribute("aria-live", "polite");
  const list = h("ul", "console-dashboard-delegation__list");
  const note = h("p", "console-dashboard-delegation__note");
  const detail = document.createElement("button");
  detail.type = "button";
  detail.className = "pf-v6-c-button pf-m-link pf-m-inline";
  detail.append(h("span", "pf-v6-c-button__text", "Work details"));
  detail.addEventListener("click", () =>
    openSurface({ pageId: "dashboard", dashboardMode: "plan" }),
  );
  card.body.append(summary, list, note, detail);

  let host = "";
  let lastRead = 0;
  let reading = false;
  let controller: AbortController | null = null;
  let request = 0;
  let disposed = false;
  let visible = true;

  const render = (read: LedgerRead): void => {
    if (read.kind === "absent") {
      card.setNote("ledger unavailable");
      summary.textContent = "This daemon does not provide a delegation ledger.";
      list.replaceChildren();
      note.textContent = "";
      return;
    }
    if (read.kind === "unreadable") {
      card.setNote("ledger unreadable");
      summary.textContent = "Delegation could not be read.";
      list.replaceChildren();
      note.textContent = read.detail;
      return;
    }

    const plan = buildPlan(read.units, read.overlaps);
    if (!plan.nodes.length) {
      card.setNote("no active delegation");
      summary.textContent = "No active delegation.";
      list.replaceChildren();
      note.textContent = "";
      return;
    }
    card.setNote(plan.nodes.length ? `${plan.nodes.length} units` : "no active delegation");
    summary.textContent = overviewLine(plan);
    const priority = { no_return: 0, fail: 1, running: 2, declared: 3, pass: 4 } as const;
    const active = treeOrder(plan)
      .map((id) => plan.byId.get(id))
      .filter((node): node is NonNullable<typeof node> => !!node)
      .filter((node) => node.state !== "pass")
      .sort((a, b) => priority[a.state] - priority[b.state])
      .slice(0, 4);
    list.replaceChildren(
      ...active.map((node) => {
        const item = h("li", "console-dashboard-delegation__item");
        item.dataset.state = node.state;
        item.append(
          h("span", "console-dashboard-delegation__mark", STATE_MARK[node.state]),
          h("code", "console-dashboard-delegation__id", node.id),
          h("span", "console-dashboard-delegation__state", STATE_LABEL[node.state]),
        );
        return item;
      }),
    );
    const warnings: string[] = [];
    if (plan.overlaps.length) warnings.push(`${plan.overlaps.length} overlapping assignments`);
    if (plan.dangling.length) warnings.push(`${plan.dangling.length} unresolved parents`);
    note.textContent = warnings.join(". ") + (warnings.length ? "." : "");
  };

  const refresh = (): void => {
    if (!visible || !host || reading) return;
    reading = true;
    lastRead = Date.now();
    const current = ++request;
    controller = new AbortController();
    const timeout = window.setTimeout(() => {
      if (current === request) controller?.abort();
    }, REQUEST_TIMEOUT_MS);
    void loadLedger(host, controller.signal)
      .then((read) => {
        if (!disposed && current === request) render(read);
      })
      .finally(() => {
        window.clearTimeout(timeout);
        if (current === request) reading = false;
      });
  };
  const interval = window.setInterval(refresh, REFRESH_MS);

  return {
    el: card.el,
    update(state: DashboardState) {
      if (state.conn.state === "demo") {
        host = "";
        request++;
        reading = false;
        controller?.abort();
        if (visible) render({ kind: "ok", units: demoUnits(Date.now()), overlaps: demoOverlaps() });
        return;
      }
      const nextHost = state.liveHost ?? "";
      if (nextHost !== host) {
        host = nextHost;
        lastRead = 0;
        request++;
        reading = false;
        controller?.abort();
      }
      if (visible && host && Date.now() - lastRead >= REFRESH_MS) refresh();
    },
    setVisible(nextVisible) {
      visible = nextVisible;
      if (visible) {
        lastRead = 0;
        refresh();
        return;
      }
      request++;
      reading = false;
      controller?.abort();
    },
    destroy() {
      disposed = true;
      request++;
      controller?.abort();
      window.clearInterval(interval);
    },
  };
}
