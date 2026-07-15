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

// buildScaffold assembles the surface DOM, reusing the log viewer's panel/toolbar/scroll classes
// (logs.css) so the trail wears the same chrome as a run's output.
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "panel activity-app");

  const bar = h("header", "file-bar");
  const id = h("div", "file-bar-id");
  id.append(h("span", "name", "Activity trail"));
  const conn = h("span", "activity-conn");
  const actions = h("div", "log-actions");
  const group = h("div", "btn-group");
  const refresh = h("button", "outline") as HTMLButtonElement;
  refresh.type = "button";
  refresh.title = "Reload the trail";
  refresh.append(h("span", "btn-label", "Refresh"));
  group.append(refresh);
  actions.append(conn, group);
  bar.append(id, actions);

  const scroll = h("div", "log-scroll");
  const body = h("div", "log-body");

  const empty = h("div", "log-empty");
  const icon = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  icon.setAttribute("class", "console-empty-icon");
  icon.setAttribute("viewBox", "0 0 24 24");
  icon.setAttribute("fill", "none");
  icon.setAttribute("stroke", "currentColor");
  icon.setAttribute("stroke-width", "1.5");
  icon.innerHTML = '<line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><circle cx="3.5" cy="6" r="1.2"/><circle cx="3.5" cy="12" r="1.2"/><circle cx="3.5" cy="18" r="1.2"/>';
  const emptyTitle = h("p", "console-empty-title", "No daemon connected");
  const emptySub = h("p", "console-empty-sub");
  emptySub.textContent = "The activity trail records what the daemon did - MCP calls, jobs, config changes. Start the daemon and open the live link, or see the demo.";
  const demoBtn = h("button", "outline") as HTMLButtonElement;
  demoBtn.type = "button";
  demoBtn.append(h("span", "btn-label", "See the demo"));
  empty.append(icon, emptyTitle, emptySub, demoBtn);

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
