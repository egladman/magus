// main.ts - the console's Activity surface: the daemon's audit trail (magus.activity.v1) painted with
// the SAME foldable, status-accented sections as the log viewer (buildSection over the shared render
// model), so a run's output and the trail read as one design. Unlike logs/graph/dashboard it has NO
// standalone page - it is built fresh into a console host. It lists a page of events via
// ActivityService.ListActivity when a daemon is reachable (a #port link, the daemon-origin/shared
// console, or the last daemon the dashboard connected to), and shows a synthesized demo trail on the
// shared #demo fragment so the
// design is inspectable offline. activate(host) builds the scaffold, kicks the initial load, and
// returns a teardown the console calls on close (it just marks in-flight loads stale - there is no
// long-lived stream yet).

import { createClient } from "@connectrpc/connect";
import {
  ActivityService,
  Kind,
  Outcome,
  type ActivityEvent,
} from "../../gen/magus/activity/v1/activity_pb";
import { activityToModel, groupEventsByKind, tsMillis } from "./adapter";
import { notify } from "../../lib/notifications";
import { buildSection } from "../render/sections";
import { chevron, mountCollapsiblePanel, relTime, type CollapsiblePanel } from "../logs/runtree";
import {
  parseHash,
  wantsDemo,
  daemonAttach,
  validateLoopbackHost,
  consumeLiveToken,
  createDaemonTransport,
} from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";
import { demoEvents } from "./demo";

const PAGE_SIZE = 100;

// The SAME key the dashboard remembers its last daemon under, so opening Activity after connecting the
// dashboard resumes the same loopback host without re-entering it. Read-only here.
const daemonCell = persisted<string | null>("dashboard-daemon", null);

interface Refs {
  scroll: HTMLElement;
  body: HTMLElement;
  empty: HTMLElement;
  emptyTitle: HTMLElement;
  emptySub: HTMLElement;
  demoBtn: HTMLButtonElement;
}

// buildScaffold assembles the surface DOM on PatternFly - the shared render frame plus a PF EmptyState
// for the cold state - matching the log viewer's migrated surface, so a run's output and the trail read
// as one design. The trail entries reuse the shared buildSection render model into .console-render-body.
// There is deliberately NO toolbar: the reload control and the event count live in the collapsible
// event-index panel (mounted in activate), so a second floating bar of chrome is not needed.
// The empty state carries the console-render-empty class alongside the PF class only for logs.css's
// `.console-render-empty[hidden] { display: none }` toggle rule (PF's EmptyState is display:flex,
// which would otherwise beat the hidden attribute).
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "console-render-panel");

  const scroll = h("div", "console-render-scroll");
  const body = h("div", "console-render-body");

  const empty = h("div", "pf-v6-c-empty-state console-render-empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyIcon = h("div", "pf-v6-c-empty-state__icon");
  emptyIcon.setAttribute("aria-hidden", "true");
  emptyIcon.innerHTML =
    '<svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><circle cx="3.5" cy="6" r="1.2"/><circle cx="3.5" cy="12" r="1.2"/><circle cx="3.5" cy="18" r="1.2"/></svg>';
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "No daemon connected");
  const emptyBody = h("div", "pf-v6-c-empty-state__body");
  const emptySub = h("p");
  emptySub.textContent =
    "The activity trail records what the daemon did: MCP calls, jobs, config changes.";
  // Two "ways" mirroring the log viewer / graph empty state - a command to go live, or the demo button.
  // The data-empty-* hooks pick up the shared grid + mobile stacking from logs.css, so it matches logs.
  const emptyActions = h("div", "pf-v6-c-empty-state__actions");
  emptyActions.dataset.emptyWays = "";

  const wayLive = h("div");
  wayLive.dataset.emptyWay = "";
  const liveLabel = h("span", undefined, "Connect a daemon");
  liveLabel.dataset.emptyWayLabel = "";
  const liveCmd = h("pre");
  liveCmd.dataset.emptyCmd = "";
  liveCmd.append(h("code", undefined, "magus server start"));
  const liveHint = h("span", undefined, "Then open the live link it prints.");
  liveHint.dataset.emptyHint = "";
  wayLive.append(liveLabel, liveCmd, liveHint);

  const wayDemo = h("div");
  wayDemo.dataset.emptyWay = "";
  const demoLabel = h("span", undefined, "Try the demo");
  demoLabel.dataset.emptyWayLabel = "";
  const demoBtn = h("button", "pf-v6-c-button pf-m-primary") as HTMLButtonElement;
  demoBtn.type = "button";
  demoBtn.append(h("span", "pf-v6-c-button__text", "See the demo"));
  const demoHint = h("span", undefined, "A synthesized trail, no daemon needed.");
  demoHint.dataset.emptyHint = "";
  wayDemo.append(demoLabel, demoBtn, demoHint);

  emptyActions.append(wayLive, wayDemo);
  emptyBody.append(emptySub, emptyActions);
  emptyContent.append(emptyIcon, emptyTitle, emptyBody);
  empty.append(emptyContent);

  scroll.append(body, empty);
  panel.append(scroll);
  host.append(panel);
  return { scroll, body, empty, emptyTitle, emptySub, demoBtn };
}

// notifyDenials raises a bell-tier notification for each sandbox denial in a freshly loaded page of
// events. A denial is a trust-changing event a human should see: something a target did was blocked, and
// the build may be wrong as a result. Called ONLY from the live load path, so the demo (which never
// calls it) cannot light the bell. Deduped per event (kind + time + action) so re-loading the trail does
// not re-fire; there is no in-app URL that addresses a single trail entry, so it carries no deep link -
// the activity surface the reader is already on IS the destination.
function notifyDenials(events: ActivityEvent[]): void {
  for (const ev of events) {
    if (ev.kind !== Kind.SANDBOX_DENIAL) continue;
    const ms = tsMillis(ev.time);
    const action = ev.action || "a sandboxed operation";
    notify({
      source: "Activity Trail",
      kind: "error",
      key: "sandbox:" + (ms ?? 0) + ":" + action,
      message: "Sandbox denied " + action + ".",
    });
  }
}

// leafLabel is a tree leaf's text: the action (or the kind tag as a fallback) plus how long ago it
// happened, so the index reads "what, when" without opening the section.
function leafLabel(ev: ActivityEvent, now: number): string {
  const ms = tsMillis(ev.time);
  const when = ms === null ? "" : relTime(ms, now);
  const action = ev.action || "event";
  return when ? action + "  " + when : action;
}

// renderIndexTree (re)builds the event index into container: a PF TreeView grouping the page's events
// by kind (a branch per kind with a count badge) over per-event leaves. Selecting a leaf calls
// onSelect(index) with the event's position in the page, so the caller can reveal that section. The
// first kind starts expanded so the newest events show without a click.
function renderIndexTree(
  container: HTMLElement,
  events: ActivityEvent[],
  now: number,
  onSelect: (index: number) => void,
): void {
  container.replaceChildren();
  const groups = groupEventsByKind(events);
  if (groups.length === 0) return; // the panel is hidden when empty; no note needed

  const tree = h("div", "pf-v6-c-tree-view pf-m-guides");
  const list = h("ul", "pf-v6-c-tree-view__list");
  list.setAttribute("role", "tree");

  groups.forEach((group, gi) => {
    const branch = h("li", "pf-v6-c-tree-view__list-item");
    branch.setAttribute("role", "treeitem");
    const expanded = gi === 0;
    branch.setAttribute("aria-expanded", String(expanded));
    if (expanded) branch.classList.add("pf-m-expanded");

    const bContent = h("div", "pf-v6-c-tree-view__content");
    const bNode = h("button", "pf-v6-c-tree-view__node") as HTMLButtonElement;
    bNode.type = "button";
    const toggle = h("span", "pf-v6-c-tree-view__node-toggle");
    const ticon = h("span", "pf-v6-c-tree-view__node-toggle-icon");
    ticon.append(chevron());
    toggle.append(ticon);
    const bContainer = h("span", "pf-v6-c-tree-view__node-container");
    const bNodeContent = h("span", "pf-v6-c-tree-view__node-content");
    bNodeContent.append(h("span", "pf-v6-c-tree-view__node-text", group.label));
    bContainer.append(bNodeContent);
    const badge = h("span", "pf-v6-c-tree-view__node-count");
    badge.append(h("span", "pf-v6-c-badge pf-m-read", String(group.events.length)));
    bContainer.append(badge);
    bNode.append(toggle, bContainer);
    bContent.append(bNode);
    branch.append(bContent);

    const kids = h("ul", "pf-v6-c-tree-view__list");
    kids.setAttribute("role", "group");
    for (const { event, index } of group.events) {
      const leaf = h("li", "pf-v6-c-tree-view__list-item");
      leaf.setAttribute("role", "treeitem");
      const lContent = h("div", "pf-v6-c-tree-view__content");
      const lNode = h("button", "pf-v6-c-tree-view__node") as HTMLButtonElement;
      lNode.type = "button";
      const err = event.outcome === Outcome.ERROR;
      lNode.title = (err ? "error" : "ok") + " - " + leafLabel(event, now);
      const lContainer = h("span", "pf-v6-c-tree-view__node-container");
      const lNodeContent = h("span", "pf-v6-c-tree-view__node-content");
      if (err) {
        // Reuse the run browser's outcome dot (styled in logs.css, which this surface loads) to mark a
        // failed event in the index.
        const dot = h("span", "console-log-runs__dot");
        dot.dataset.status = "fail";
        lNodeContent.append(dot);
      }
      lNodeContent.append(h("span", "pf-v6-c-tree-view__node-text", leafLabel(event, now)));
      lContainer.append(lNodeContent);
      lNode.append(lContainer);
      lContent.append(lNode);
      leaf.append(lContent);
      lNode.addEventListener("click", () => {
        const root = leaf.closest(".pf-v6-c-tree-view");
        root
          ?.querySelectorAll(".pf-v6-c-tree-view__node.pf-m-current")
          .forEach((n) => n.classList.remove("pf-m-current"));
        lNode.classList.add("pf-m-current");
        onSelect(index);
      });
      kids.append(leaf);
    }
    branch.append(kids);
    bNode.addEventListener("click", () => {
      const open = branch.classList.toggle("pf-m-expanded");
      branch.setAttribute("aria-expanded", String(open));
    });
    list.append(branch);
  });

  tree.append(list);
  container.append(tree);
}

// activate builds the surface into host, loads once, and returns a teardown. Every async load checks
// `stale` before touching the DOM, so a load that resolves after the tab closed is dropped.
export function activate(host: HTMLElement): () => void {
  const refs = buildScaffold(host);
  let stale = false;

  // The event index: the collapsible left panel shared with the log viewer's run browser. Its refresh
  // icon re-runs load(); the "N events" count rides in its header. It starts collapsed on a phone and
  // hides entirely when the trail is empty (the golden empty-state card carries the cold state alone).
  const panel: CollapsiblePanel | null = mountCollapsiblePanel({
    scroll: refs.scroll,
    title: "Events",
    label: "Event index",
    onRefresh: load,
    hideWhenEmpty: true,
  });
  const conn = h("span", "console-activity-conn");
  if (panel) panel.head.insertBefore(conn, panel.refreshBtn);

  // reveal scrolls a section into view and expands it, so clicking an index leaf lands on that event.
  function reveal(index: number, sectionEls: HTMLElement[]): void {
    const el = sectionEls[index];
    if (!el) return;
    el.removeAttribute("data-collapsed");
    el.querySelector(".console-render-section__head")?.setAttribute("aria-expanded", "true");
    el.scrollIntoView({ behavior: "smooth", block: "center" });
  }

  function render(events: ActivityEvent[]): void {
    refs.body.replaceChildren();
    const model = activityToModel(events);
    // The adapter puts the ok/error accent in meta.status; buildSection defaults its accent from a
    // "[status]" title token (which the trail deliberately omits), so pass it through explicitly.
    const sectionEls: HTMLElement[] = model.sections.map((sec) =>
      buildSection(sec, { status: sec.meta?.status }),
    );
    for (const el of sectionEls) refs.body.append(el);
    const has = events.length > 0;
    refs.empty.hidden = has;
    const n = events.length;
    conn.textContent = n + (n === 1 ? " event" : " events");
    if (panel) {
      renderIndexTree(panel.treeBox, events, Date.now(), (i) => reveal(i, sectionEls));
      panel.applyDefault(has);
    }
  }

  function showEmpty(title: string, sub: string, connText: string): void {
    refs.body.replaceChildren();
    refs.empty.hidden = false;
    refs.emptyTitle.textContent = title;
    refs.emptySub.textContent = sub;
    conn.textContent = connText;
    if (panel) {
      renderIndexTree(panel.treeBox, [], Date.now(), () => {});
      panel.applyDefault(false);
    }
  }

  async function loadLive(daemonHost: string): Promise<void> {
    conn.textContent = "connecting...";
    try {
      const client = createClient(ActivityService, createDaemonTransport(daemonHost));
      const resp = await client.listActivity({ pageSize: PAGE_SIZE });
      if (stale) return;
      render(resp.events);
      notifyDenials(resp.events);
      if (resp.events.length === 0) {
        showEmpty(
          "No activity yet",
          "The daemon is connected but has not recorded any actions in this session.",
          "0 events",
        );
      }
    } catch (e) {
      if (stale) return;
      const msg = e instanceof Error ? e.message : String(e);
      showEmpty(
        "Could not reach the daemon",
        "The daemon at " +
          daemonHost +
          " did not answer (" +
          msg +
          "). Start it with: magus server start",
        "not connected",
      );
    }
  }

  // load resolves which source to read: an explicit #demo, then an explicit daemon attach (a #port
  // link or the daemon-origin/shared console), then the last daemon the dashboard remembered;
  // otherwise the cold empty state.
  function load(): void {
    const params = parseHash();
    consumeLiveToken(params);
    if (wantsDemo(params)) {
      render(demoEvents(Date.now()));
      return;
    }
    const linked = daemonAttach(params);
    const remembered = daemonCell.get();
    const daemonHost = linked ?? (remembered ? validateLoopbackHost(remembered) : null);
    if (daemonHost) {
      void loadLive(daemonHost);
      return;
    }
    showEmpty(
      "No daemon connected",
      "The activity trail records what the daemon did: MCP calls, jobs, config changes.",
      "not connected",
    );
  }

  refs.demoBtn.addEventListener("click", () => render(demoEvents(Date.now())));
  load();

  return () => {
    stale = true;
  };
}
