import type { DashboardState } from "../state";
import {
  buildJobTree,
  jobClient,
  listJobs,
  overviewLine,
  treeOrder,
  HOLDER_LABEL,
  STATE_LABEL,
  STATE_STATUS,
  type JobClient,
  type JobsRead,
} from "../plan/jobs";
import { demoOverlaps, demoJobs } from "../plan/demo";
import { openApp } from "../../../desktop/app-navigation";
import { reportFailure } from "../../../lib/notifications";
import { statusIcon } from "../../../ui/status";
import { Card, h, type Tile } from "./card";

const REFRESH_MS = 4_000;
const REQUEST_TIMEOUT_MS = 10_000;

export function jobsTile(): Tile {
  const card = new Card("jobs", "Jobs", {
    note: "waiting for the server",
    why:
      "Every job this server knows about, its own maintenance and the work sessions hold, with" +
      " anything that needs intervention first. Each row says whether the job is declared," +
      " running, passed, failed, or no-return: nothing was ever reported back, unlike a failure," +
      " the one state that needs a human, since no one is coming to tell you about it.",
  });
  const summary = h("p", "console-dashboard-jobs__summary", "No jobs.");
  const list = h("ul", "console-dashboard-jobs__list");
  const note = h("p", "console-dashboard-jobs__note");
  const detail = document.createElement("button");
  detail.type = "button";
  detail.className = "pf-v6-c-button pf-m-link pf-m-inline";
  // The way into the Jobs view stays when the card is empty: that is where the reader goes next.
  detail.dataset.keepEmpty = "";
  detail.append(h("span", "pf-v6-c-button__text", "All jobs"));
  detail.addEventListener("click", () => openApp({ pageId: "dashboard", mode: "jobs" }));
  card.body.append(summary, list, note, detail);

  let host = "";
  let client: JobClient | null = null;
  let lastRead = 0;
  let reading = false;
  let controller: AbortController | null = null;
  let request = 0;
  let disposed = false;
  let visible = true;

  // setText writes only when the text changed: the tile repaints on every read, and a rewritten
  // node is a mutation to assistive tech even when the words are the same.
  const setText = (el: HTMLElement, text: string): void => {
    if (el.textContent !== text) el.textContent = text;
  };

  const render = (read: JobsRead): void => {
    if (read.kind === "denied") {
      card.setNote("jobs not served");
      card.setEmpty("This server does not serve jobs.");
      list.replaceChildren();
      setText(note, "");
      return;
    }
    if (read.kind === "unreadable" || read.kind === "unreachable") {
      card.setNote("jobs unreadable");
      card.setEmpty("The jobs could not be read: " + read.detail);
      list.replaceChildren();
      setText(note, "");
      return;
    }

    const model = buildJobTree(read.jobs, read.overlaps);
    if (!model.nodes.length) {
      card.setNote("no jobs");
      card.setEmpty("No jobs yet. Nothing is running here and nothing has been handed out.");
      list.replaceChildren();
      setText(note, "");
      return;
    }
    card.setEmpty(null);
    card.setNote(`${model.nodes.length} jobs`);
    setText(summary, overviewLine(model));
    const priority = { no_return: 0, fail: 1, running: 2, declared: 3, pass: 4 } as const;
    const active = treeOrder(model)
      .map((id) => model.byId.get(id))
      .filter((node): node is NonNullable<typeof node> => !!node)
      .filter((node) => node.state !== "pass")
      .sort((a, b) => priority[a.state] - priority[b.state])
      .slice(0, 4);
    list.replaceChildren(
      ...active.map((node) => {
        const item = h("li", "console-dashboard-jobs__item");
        item.dataset.state = node.state;
        const mark = h("span", "console-dashboard-jobs__mark");
        mark.append(statusIcon(STATE_STATUS[node.state]));
        item.append(
          mark,
          h("code", "console-dashboard-jobs__id", node.id),
          // Which kind of job this is, beside its state: the board is where the two are most
          // easily confused, since a server job and a session's job read the same at a glance.
          h(
            "span",
            "console-dashboard-jobs__state",
            [HOLDER_LABEL[node.holder], STATE_LABEL[node.state]].filter(Boolean).join(", "),
          ),
        );
        return item;
      }),
    );
    const warnings: string[] = [];
    if (model.overlaps.length) warnings.push(`${model.overlaps.length} overlapping claims`);
    if (model.dangling.length) warnings.push(`${model.dangling.length} unresolved parents`);
    setText(note, warnings.join(". ") + (warnings.length ? "." : ""));
  };

  const refresh = (): void => {
    if (!visible || !host || reading) return;
    reading = true;
    lastRead = Date.now();
    const current = ++request;
    controller = new AbortController();
    const signal = controller.signal;
    let timedOut = false;
    const timeout = window.setTimeout(() => {
      if (current !== request) return;
      timedOut = true;
      controller?.abort();
    }, REQUEST_TIMEOUT_MS);
    client ??= jobClient(host);
    void listJobs(client, signal)
      .then((read) => {
        if (disposed || current !== request) return;
        // The transport stays silent about an abort, since a superseded read is not a failure; this
        // one is the deadline, so it is reported here.
        if (timedOut) {
          reportFailure(
            "Jobs",
            "The job list did not answer within " + REQUEST_TIMEOUT_MS / 1000 + "s.",
            "jobs:timeout",
          );
        }
        render(read);
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
        client = null;
        request++;
        reading = false;
        controller?.abort();
        if (visible) {
          render({ kind: "ok", jobs: demoJobs(Date.now()), overlaps: demoOverlaps() });
        }
        return;
      }
      const nextHost = state.liveHost ?? "";
      if (nextHost !== host) {
        host = nextHost;
        // A client carries the origin it was built for, so the next read has to open its own.
        client = null;
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
