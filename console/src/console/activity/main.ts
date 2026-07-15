// main.ts - the console's Activity surface: the daemon's audit trail (magus.activity.v1) painted with
// the SAME foldable, status-accented sections as the log viewer (buildSection over the shared render
// model), so a run's output and the trail read as one design. Unlike logs/graph/dashboard it has NO
// standalone page - it is built fresh into a console host. It lists a page of events via
// ActivityService.ListActivity when a daemon is reachable (a #live link, or the last daemon the
// dashboard connected to), and shows a synthesized demo trail on the shared #demo fragment so the
// design is inspectable offline. activate(host) builds the scaffold, kicks the initial load, and
// returns a teardown the console calls on close (it just marks in-flight loads stale - there is no
// long-lived stream yet).

import { createClient } from "@connectrpc/connect";
import { ActivityService, type ActivityEvent } from "../../gen/magus/activity/v1/activity_pb";
import { activityToModel } from "./adapter";
import { buildSection } from "../render/sections";
import { parseHash, wantsDemo, validateLiveHost, consumeLiveToken, createDaemonTransport } from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";
import { demoEvents } from "./demo";

const PAGE_SIZE = 100;

// The SAME key the dashboard remembers its last daemon under, so opening Activity after connecting the
// dashboard resumes the same loopback host without re-entering it. Read-only here.
const daemonCell = persisted<string | null>("dashboard-daemon", null);

interface Refs {
  body: HTMLElement;
  empty: HTMLElement;
  emptyTitle: HTMLElement;
  emptySub: HTMLElement;
  demoBtn: HTMLButtonElement;
  conn: HTMLElement;
  refresh: HTMLButtonElement;
}

// buildScaffold assembles the surface DOM on PatternFly - a PF Toolbar for the chrome, PF Buttons, and
// a PF EmptyState for the cold state - matching the log viewer's migrated surface, so a run's output
// and the trail read as one design. The trail entries themselves reuse the shared buildSection render
// model into .log-body (kept + token-repointed in logs.css); .panel/.log-scroll/.log-body are the
// escape-hatch frame classes logs.css still provides. The empty state carries the log-empty class
// alongside the PF class only for logs.css's `.log-empty[hidden] { display: none }` toggle rule (PF's
// EmptyState is display:flex, which would otherwise beat the hidden attribute).
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "panel activity-app");

  // Toolbar: the trail title on the left, the connection note + Refresh button aligned to the right.
  const bar = h("header", "pf-v6-c-toolbar");
  const idContent = h("div", "pf-v6-c-toolbar__content");
  const idSection = h("div", "pf-v6-c-toolbar__content-section");
  const idItem = h("div", "pf-v6-c-toolbar__item");
  idItem.append(h("span", "name", "Activity trail"));
  idSection.append(idItem);
  idContent.append(idSection);

  const ctrlContent = h("div", "pf-v6-c-toolbar__content");
  const ctrlSection = h("div", "pf-v6-c-toolbar__content-section");
  const actionGroup = h("div", "pf-v6-c-toolbar__group pf-m-action-group pf-m-align-end");
  const connItem = h("div", "pf-v6-c-toolbar__item");
  const conn = h("span", "activity-conn");
  connItem.append(conn);
  const btnItem = h("div", "pf-v6-c-toolbar__item");
  const refresh = h("button", "pf-v6-c-button pf-m-secondary pf-m-small") as HTMLButtonElement;
  refresh.type = "button";
  refresh.title = "Reload the trail";
  const refreshIcon = h("span", "pf-v6-c-button__icon pf-m-start");
  refreshIcon.innerHTML = '<svg class="btn-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="23 4 23 10 17 10"/><path d="M20.5 15a9 9 0 1 1-2.1-9.4L23 10"/></svg>';
  refresh.append(refreshIcon, h("span", "pf-v6-c-button__text btn-label", "Refresh"));
  btnItem.append(refresh);
  actionGroup.append(connItem, btnItem);
  ctrlSection.append(actionGroup);
  ctrlContent.append(ctrlSection);
  bar.append(idContent, ctrlContent);

  const scroll = h("div", "log-scroll");
  const body = h("div", "log-body");

  const empty = h("div", "pf-v6-c-empty-state log-empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyIcon = h("div", "pf-v6-c-empty-state__icon");
  emptyIcon.setAttribute("aria-hidden", "true");
  emptyIcon.innerHTML = '<svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><circle cx="3.5" cy="6" r="1.2"/><circle cx="3.5" cy="12" r="1.2"/><circle cx="3.5" cy="18" r="1.2"/></svg>';
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "No daemon connected");
  const emptyBody = h("div", "pf-v6-c-empty-state__body");
  const emptySub = h("p");
  emptySub.textContent = "The activity trail records what the daemon did - MCP calls, jobs, config changes. Start the daemon and open the live link, or see the demo.";
  const emptyActions = h("div", "pf-v6-c-empty-state__actions");
  const demoBtn = h("button", "pf-v6-c-button pf-m-secondary pf-m-small") as HTMLButtonElement;
  demoBtn.type = "button";
  demoBtn.append(h("span", "pf-v6-c-button__text btn-label", "See the demo"));
  emptyActions.append(demoBtn);
  emptyBody.append(emptySub, emptyActions);
  emptyContent.append(emptyIcon, emptyTitle, emptyBody);
  empty.append(emptyContent);

  scroll.append(body, empty);
  panel.append(bar, scroll);
  host.append(panel);
  return { body, empty, emptyTitle, emptySub, demoBtn, conn, refresh };
}

// activate builds the surface into host, loads once, and returns a teardown. Every async load checks
// `stale` before touching the DOM, so a load that resolves after the tab closed is dropped.
export function activate(host: HTMLElement): () => void {
  const refs = buildScaffold(host);
  let stale = false;

  function render(events: ActivityEvent[], demo: boolean): void {
    refs.body.replaceChildren();
    const model = activityToModel(events);
    // The adapter puts the ok/error accent in meta.status; buildSection defaults its accent from a
    // "[status]" title token (which the trail deliberately omits), so pass it through explicitly.
    for (const sec of model.sections) refs.body.append(buildSection(sec, { status: sec.meta?.status }));
    refs.empty.hidden = events.length > 0;
    const n = events.length;
    refs.conn.textContent = demo ? "demo data" : n + (n === 1 ? " event" : " events");
  }

  function showEmpty(title: string, sub: string, conn: string): void {
    refs.body.replaceChildren();
    refs.empty.hidden = false;
    refs.emptyTitle.textContent = title;
    refs.emptySub.textContent = sub;
    refs.conn.textContent = conn;
  }

  async function loadLive(daemonHost: string): Promise<void> {
    refs.conn.textContent = "connecting...";
    try {
      const client = createClient(ActivityService, createDaemonTransport(daemonHost));
      const resp = await client.listActivity({ pageSize: PAGE_SIZE });
      if (stale) return;
      render(resp.events, false);
      if (resp.events.length === 0) {
        showEmpty("No activity yet", "The daemon is connected but has not recorded any actions in this session.", "0 events");
      }
    } catch (e) {
      if (stale) return;
      const msg = e instanceof Error ? e.message : String(e);
      showEmpty("Could not reach the daemon", "The daemon at " + daemonHost + " did not answer (" + msg + "). Start it with: magus server start", "not connected");
    }
  }

  // load resolves which source to read: an explicit #demo, then a #live host, then the last daemon the
  // dashboard remembered; otherwise the cold empty state.
  function load(): void {
    const params = parseHash();
    consumeLiveToken(params);
    if (wantsDemo(params)) { render(demoEvents(Date.now()), true); return; }
    const linked = params.live !== undefined ? validateLiveHost(params.live) : null;
    const remembered = daemonCell.get();
    const daemonHost = linked ?? (remembered ? validateLiveHost(remembered) : null);
    if (daemonHost) { void loadLive(daemonHost); return; }
    showEmpty("No daemon connected", "The activity trail records what the daemon did - MCP calls, jobs, config changes. Start the daemon and open the live link, or see the demo.", "not connected");
  }

  refs.refresh.addEventListener("click", load);
  refs.demoBtn.addEventListener("click", () => render(demoEvents(Date.now()), true));
  load();

  return () => { stale = true; };
}
