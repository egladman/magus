// main.ts - the console's Activity app: the server's activity record (magus.activity.v1alpha1) painted
// with the SAME foldable sections as the log viewer (buildSection over the shared render model), so a
// run's output and the record read as one design. Unlike logs/graph/dashboard it has NO standalone
// page - it is built fresh into a console host. It lists a page of events via
// ActivityService.ListActivityEvents when a server is reachable (a #port link, the server-origin/shared
// console, or the last server the dashboard connected to), resolves an event's payload refs on demand
// through ActivityService.GetPayload, and shows a synthesized demo on the shared #demo fragment so the
// design is inspectable offline. What a job DID lands here; the Jobs view is where one is read and
// run. activate(host) builds the scaffold, kicks the initial load, and returns a teardown the console
// calls on close (it marks in-flight loads stale - there is no long-lived stream yet).

import { Code, ConnectError, createClient, type Client } from "@connectrpc/connect";
import {
  ActivityService,
  Kind,
  Outcome,
  type ActivityEvent,
} from "@wire/activity/v1alpha1/activity_pb";
import {
  PAYLOAD_MAX_BYTES,
  activityToModel,
  groupEventsByKind,
  humanBytes,
  kindLabel,
  durText,
  payloadLabel,
  payloadLines,
  payloadRefs,
  tsMillis,
  type PayloadRef,
} from "./adapter";
import { sessionKey, sessionLabel, sessionLineage, type SessionNode } from "./lineage";
import { notify } from "../../lib/notifications";
import { buildSection, renderLine, sectionToggle, setSectionOpen } from "../../render/sections";
import { createFilterField, type FilterField } from "../../render/filterField";
import { attachTreeKeys } from "../../render/treeKeys";
import { timeEl } from "../../render/time";
import {
  chevron,
  mountCollapsiblePanel,
  tickRelativeTimes,
  type CollapsiblePanel,
} from "../logs/runtree";
import {
  parseHash,
  wantsDemo,
  resolveServerHostOrRemembered,
  isUnreachable,
  adoptServerOrigin,
  consumeLiveToken,
  createServerTransport,
} from "../../lib/server";
import { errMessage, must } from "../../lib/guards";
import { persisted } from "../../lib/persist";
import { subscribeDefaultHost } from "../../lib/settings";
import { h } from "../../desktop/view";
import { inlineAlert } from "../../ui/alert";
import { emptyStateShell } from "../../ui/empty-state";
import { statusGlyph, statusMark } from "../../ui/status";
import {
  renderConnectPrompt,
  renderEmptyMessage,
  type ConnectPromptState,
  type EmptyStateSlots,
} from "../../desktop/connectPrompt";
import type { AppInstance } from "../../desktop/standalone";
import { demoEvents } from "./demo";

const PAGE_SIZE = 100;
const PURPOSE = "Activity records what the server did: MCP calls, jobs, config changes.";
// An index with more nodes than this earns a filter box, as PF's tree view guidance puts it.
const FILTER_ABOVE = 7;

interface Refs {
  scroll: HTMLElement;
  notice: HTMLElement;
  body: HTMLElement;
  empty: HTMLElement;
  emptyIcon: HTMLElement;
  emptySlots: EmptyStateSlots;
}

const EMPTY_ICON =
  '<svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><circle cx="3.5" cy="6" r="1.2"/><circle cx="3.5" cy="12" r="1.2"/><circle cx="3.5" cy="18" r="1.2"/></svg>';

// buildScaffold assembles the app DOM on PatternFly: the shared render frame plus a PF EmptyState for
// the cold state, matching the log viewer, so a run's output and the record read as one design. The
// events reuse the shared buildSection render model into .console-render-body. There is deliberately
// NO toolbar: the reload control and the event count live in the collapsible event-index panel
// (mounted in activate), so a second floating bar of chrome is not needed. The empty state carries
// console-render-empty alongside the PF class only for frame.css's `[hidden]` toggle rule (PF's
// EmptyState is display:flex, which would otherwise beat the hidden attribute).
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "console-render-panel");

  const scroll = h("div", "console-render-scroll");
  const notice = h("div", "console-activity-notice");
  notice.hidden = true;
  const body = h("div", "console-render-body");

  const iconHolder = document.createElement("template");
  iconHolder.innerHTML = EMPTY_ICON;
  const state = emptyStateShell({
    heading: "h2",
    classes: "console-render-empty",
    icon: iconHolder.content,
    ways: true,
  });
  const emptyMessage = h("p");
  state.body.append(emptyMessage);

  scroll.append(notice, body, state.root);
  panel.append(scroll);
  host.append(panel);
  return {
    scroll,
    notice,
    body,
    empty: state.root,
    emptyIcon: must(state.icon),
    emptySlots: { title: state.title, message: emptyMessage, actions: state.actions },
  };
}

// notifyDenials raises a bell-tier notification for each sandbox denial in a freshly loaded page of
// events. A denial is a trust-changing event a human should see: something a target did was blocked, and
// the build may be wrong as a result. Called ONLY from the live load path, so the demo (which never
// calls it) cannot light the bell. Deduped per event (kind + time + action) so re-loading the record
// does not re-fire; there is no in-app URL that addresses a single event, so it carries no deep link -
// the activity app the reader is already on IS the destination.
function notifyDenials(events: ActivityEvent[]): void {
  for (const ev of events) {
    if (ev.kind !== Kind.SANDBOX_DENIAL) continue;
    const ms = tsMillis(ev.time);
    const action = ev.action || "a sandboxed operation";
    notify({
      source: "Activity",
      kind: "error",
      key: "sandbox:" + (ms ?? 0) + ":" + action,
      message: "Sandbox denied " + action + ".",
    });
  }
}

// leafAction is a row's name: the action, or the kind tag when the event has none.
function leafAction(ev: ActivityEvent): string {
  return ev.action || "event";
}

// matchesIndexFilter is the index filter box: every typed word must appear in the event's action,
// actor or source, case-insensitively.
function matchesIndexFilter(ev: ActivityEvent, words: string[]): boolean {
  if (words.length === 0) return true;
  const hay = (leafAction(ev) + " " + ev.actor + " " + kindLabel(ev.kind)).toLowerCase();
  return words.every((w) => hay.includes(w));
}

// IndexMode is how the event index groups the page: by the source that recorded each event, or by
// the agent session that produced it.
type IndexMode = "kind" | "session";

// IndexState is what survives a repaint of the index: the branches the reader opened or closed,
// keyed by mode and branch id so each grouping holds its own, and the leaf they selected, by the
// event's position in the page. A branch with no entry takes the grouping's default.
interface IndexState {
  open: Map<string, boolean>;
  current: number | null;
}

// indexBranch builds one collapsible tree branch: a labelled node with a count badge over the list
// its children go in, wired to its own expand toggle. sub takes the second line of PF's compact node
// ("" leaves the branch a single line). onToggle reports each open or close so the caller can
// remember it past the next repaint.
function indexBranch(
  label: string,
  sub: string,
  count: number,
  expanded: boolean,
  onToggle: (open: boolean) => void,
): { branch: HTMLElement; kids: HTMLElement } {
  const branch = h("li", "pf-v6-c-tree-view__list-item");
  branch.setAttribute("role", "treeitem");
  branch.setAttribute("aria-expanded", String(expanded));
  branch.setAttribute("aria-selected", "false");
  branch.tabIndex = -1;
  if (expanded) branch.classList.add("pf-m-expanded");

  const bContent = h("div", "pf-v6-c-tree-view__content");
  const bNode = h("button", "pf-v6-c-tree-view__node");
  bNode.type = "button";
  bNode.tabIndex = -1;
  bNode.title = sub ? label + "  " + sub : label;
  const bContainer = h("span", "pf-v6-c-tree-view__node-container");
  const toggle = h("span", "pf-v6-c-tree-view__node-toggle");
  const ticon = h("span", "pf-v6-c-tree-view__node-toggle-icon");
  ticon.append(chevron());
  toggle.append(ticon);
  const bNodeContent = h("span", "pf-v6-c-tree-view__node-content");
  bNodeContent.append(h("span", "pf-v6-c-tree-view__node-title", label));
  if (sub)
    bNodeContent.append(
      h("span", "pf-v6-c-tree-view__node-text console-activity-index__session", sub),
    );
  const badge = h("span", "pf-v6-c-tree-view__node-count");
  badge.append(h("span", "pf-v6-c-badge pf-m-read", String(count)));
  bContainer.append(toggle, bNodeContent, badge);
  bNode.append(bContainer);
  bContent.append(bNode);
  branch.append(bContent);

  const kids = h("ul", "pf-v6-c-tree-view__list");
  kids.setAttribute("role", "group");
  branch.append(kids);

  bNode.addEventListener("click", () => {
    const open = branch.classList.toggle("pf-m-expanded");
    branch.setAttribute("aria-expanded", String(open));
    onToggle(open);
  });
  return { branch, kids };
}

// indexLeaf builds one event row. Selecting it marks the row current across the whole tree,
// records it in state, and calls onSelect with the event's position in the page.
function indexLeaf(
  event: ActivityEvent,
  index: number,
  now: number,
  state: IndexState,
  onSelect: (index: number) => void,
): HTMLElement {
  const leaf = h("li", "pf-v6-c-tree-view__list-item");
  leaf.setAttribute("role", "treeitem");
  leaf.dataset.leaf = "";
  const selected = state.current === index;
  leaf.setAttribute("aria-selected", String(selected));
  leaf.tabIndex = -1;
  const lContent = h("div", "pf-v6-c-tree-view__content");
  const lNode = h("button", "pf-v6-c-tree-view__node");
  lNode.type = "button";
  lNode.tabIndex = -1;
  if (selected) {
    lNode.classList.add("pf-m-current");
    lNode.setAttribute("aria-current", "true");
  }
  const err = event.outcome === Outcome.ERROR;
  const ms = tsMillis(event.time);
  lNode.title = (err ? "Error" : "OK") + " - " + leafAction(event);
  const lContainer = h("span", "pf-v6-c-tree-view__node-container");
  const lNodeContent = h("span", "pf-v6-c-tree-view__node-content");
  // node-content is a COLUMN in PatternFly - its slot for a title over a text line - so anything
  // appended to it takes a row of its own. The outcome mark rides INSIDE the title, in front of the
  // action it marks, and the time takes the second line. EVERY row carries the mark, not just the
  // failures: the run browser marks every row, and marking only errors here taught two rules for one
  // symbol.
  const title = h("span", "pf-v6-c-tree-view__node-title");
  title.append(
    statusMark(err ? "danger" : "success", err ? "Error" : "OK"),
    h("span", "console-activity-index__action", leafAction(event)),
  );
  lNodeContent.append(title);
  if (ms !== null) {
    const text = h("span", "pf-v6-c-tree-view__node-text");
    text.append(timeEl(ms, now));
    lNodeContent.append(text);
  }
  lContainer.append(lNodeContent);
  lNode.append(lContainer);
  lContent.append(lNode);
  leaf.append(lContent);
  lNode.addEventListener("click", () => {
    const root = leaf.closest(".pf-v6-c-tree-view");
    root?.querySelectorAll(".pf-v6-c-tree-view__node.pf-m-current").forEach((n) => {
      n.classList.remove("pf-m-current");
      n.removeAttribute("aria-current");
      n.closest("li")?.setAttribute("aria-selected", "false");
    });
    lNode.classList.add("pf-m-current");
    lNode.setAttribute("aria-current", "true");
    leaf.setAttribute("aria-selected", "true");
    state.current = index;
    onSelect(index);
  });
  return leaf;
}

// kindBranches lists a branch per kind that occurred, in the adapter's fixed source order. The
// first kind starts expanded so the newest events show without a click.
function kindBranches(
  events: ActivityEvent[],
  now: number,
  state: IndexState,
  words: string[],
  onSelect: (index: number) => void,
): HTMLElement[] {
  const out: HTMLElement[] = [];
  groupEventsByKind(events).forEach((group, gi) => {
    const shown = group.events.filter((e) => matchesIndexFilter(e.event, words));
    if (shown.length === 0) return;
    const key = "kind:" + group.label;
    const { branch, kids } = indexBranch(
      group.label,
      "",
      shown.length,
      state.open.get(key) ?? gi === 0,
      (open) => state.open.set(key, open),
    );
    for (const { event, index } of shown)
      kids.append(indexLeaf(event, index, now, state, onSelect));
    out.push(branch);
  });
  return out;
}

// sessionBranch builds one session's branch: its own events in page order, then the sessions it
// spawned. A spawned session is drawn EXPANDED whatever its parent's state: this mode exists to
// show a fan-out, and a collapsed child hides the thing the reader switched modes to see. A session
// the filter leaves nothing of is dropped, unless one of its children survives.
function sessionBranch(
  node: SessionNode,
  now: number,
  state: IndexState,
  words: string[],
  expanded: boolean,
  onSelect: (index: number) => void,
): HTMLElement | null {
  const key = "session:" + sessionKey(node.host, node.session);
  const own = node.events.filter((e) => matchesIndexFilter(e.event, words));
  const children = node.children
    .map((child) => sessionBranch(child, now, state, words, true, onSelect))
    .filter((c): c is HTMLElement => c !== null);
  if (own.length === 0 && children.length === 0) return null;
  const { branch, kids } = indexBranch(
    sessionLabel(node),
    node.session,
    own.length,
    state.open.get(key) ?? expanded,
    (open) => state.open.set(key, open),
  );
  branch.dataset.session = node.session;
  for (const { event, index } of own) kids.append(indexLeaf(event, index, now, state, onSelect));
  for (const child of children) kids.append(child);
  return branch;
}

// sessionBranches lists a branch per session that produced agent events. Only an agent command or
// spawn carries a session, so this mode lists FEWER events than "by kind": an MCP call or a job has
// no agent behind it to group by.
function sessionBranches(
  events: ActivityEvent[],
  now: number,
  state: IndexState,
  words: string[],
  onSelect: (index: number) => void,
): HTMLElement[] {
  return sessionLineage(events)
    .map((node, i) => sessionBranch(node, now, state, words, i === 0, onSelect))
    .filter((b): b is HTMLElement => b !== null);
}

// renderIndexTree (re)builds the event index into container as a PF TreeView grouped the given way:
// branches with a count badge over per-event leaves. Selecting a leaf calls onSelect(index) with
// the event's position in the page, so the caller can reveal that section. The returned function
// re-seats the keyboard's tab stop, which the caller invokes after the rows settle.
function renderIndexTree(
  container: HTMLElement,
  events: ActivityEvent[],
  now: number,
  mode: IndexMode,
  state: IndexState,
  words: string[],
  onSelect: (index: number) => void,
): void {
  container.replaceChildren();
  const branches =
    mode === "session"
      ? sessionBranches(events, now, state, words, onSelect)
      : kindBranches(events, now, state, words, onSelect);
  if (branches.length === 0) {
    // By kind, an empty tree means an empty page (and the panel hides itself) or a filter that left
    // nothing. By session the page can be full and still group nothing, and a blank panel reads as
    // a broken one.
    if (words.length > 0) {
      container.append(h("p", "console-log-runs__empty", "No events match the filter."));
    } else if (mode === "session") {
      container.append(
        h(
          "p",
          "console-log-runs__empty",
          "No agent sessions on this page. Only agent commands and spawns carry a session; By kind lists every event.",
        ),
      );
    }
    return;
  }

  const tree = h("div", "pf-v6-c-tree-view pf-m-compact pf-m-no-background pf-m-truncate");
  const list = h("ul", "pf-v6-c-tree-view__list");
  list.setAttribute("role", "tree");
  list.setAttribute("aria-label", "Events");
  for (const branch of branches) list.append(branch);
  tree.append(list);
  container.append(tree);
  attachTreeKeys(list)();
}

// The grouping modes the toggle offers, in order. The first is the resting one.
const INDEX_MODES: ReadonlyArray<{ id: IndexMode; label: string; title: string }> = [
  { id: "kind", label: "By kind", title: "Group by the source that recorded the event" },
  {
    id: "session",
    label: "By session",
    title: "Group agent commands by session, nesting a spawned session under its parent",
  },
];

// indexModeCell remembers which grouping the reader last chose, so reopening Activity resumes it.
const indexModeCell = persisted<IndexMode>("activity-index-mode", INDEX_MODES[0].id);

// mountIndexControls docks the grouping toggle and the filter box between the index header and the
// tree. It reuses the run browser's control strip and segmented-toggle rules rather than authoring a
// second pair: this IS that aside, and the strip was written for exactly this slot.
function mountIndexControls(
  panel: CollapsiblePanel,
  onChange: () => void,
): { mode: () => IndexMode; field: FilterField } {
  const bar = h("div", "console-log-runs__controls");
  bar.dataset.controlSize = "compact";
  const group = h("div", "pf-v6-c-toggle-group console-log-runs__modes");
  group.setAttribute("role", "group");
  group.setAttribute("aria-label", "Group events by");

  const buttons = new Map<IndexMode, HTMLButtonElement>();
  for (const m of INDEX_MODES) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button");
    btn.type = "button";
    btn.title = m.title;
    btn.dataset.indexMode = m.id;
    const selected = m.id === indexModeCell.get();
    btn.setAttribute("aria-pressed", String(selected));
    if (selected) btn.classList.add("pf-m-selected");
    btn.append(h("span", "pf-v6-c-toggle-group__text", m.label));
    btn.addEventListener("click", () => {
      if (indexModeCell.get() === m.id) return;
      indexModeCell.set(m.id);
      for (const [id, b] of buttons) {
        b.classList.toggle("pf-m-selected", id === m.id);
        b.setAttribute("aria-pressed", String(id === m.id));
      }
      onChange();
    });
    buttons.set(m.id, btn);
    item.append(btn);
    group.append(item);
  }

  const field = createFilterField({
    label: "Filter events",
    placeholder: "Filter events",
    onChange: () => onChange(),
  });
  // Shown once the index has more than a handful of nodes (see repaintIndex).
  field.el.hidden = true;

  bar.append(group, field.el);
  panel.head.after(bar);
  return { mode: indexModeCell.get, field };
}

// PayloadControl is one "show request/response" control: the body line it sits on, its button and
// that button's label span (PF keeps a button's text in its own element, so the label is swapped
// there rather than over the button's children), the note a failure reason lands in, and the ref
// being resolved.
interface PayloadControl {
  row: HTMLElement;
  btn: HTMLButtonElement;
  text: HTMLElement;
  note: HTMLElement;
  ref: PayloadRef;
}

// motion is the scroll behaviour that respects a reader who asked for less of it.
function motion(): ScrollBehavior {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
}

// fillHead writes an event's section head as structure rather than as one run-together string: the
// source and the outcome first, as PF Labels that never truncate, then the action, which is the
// part that gives way in a narrow pane, then who did it, how long it took, and when.
function fillHead(title: HTMLElement, ev: ActivityEvent, now: number): void {
  title.dataset.structured = "";
  const err = ev.outcome === Outcome.ERROR;
  const kind = h("span", "pf-v6-c-label pf-m-compact console-activity-head__kind");
  kind.append(h("span", "pf-v6-c-label__content", kindLabel(ev.kind)));

  const outcome = h(
    "span",
    "pf-v6-c-label pf-m-compact console-activity-head__outcome " +
      (err ? "pf-m-danger" : "pf-m-success"),
  );
  const outcomeContent = h("span", "pf-v6-c-label__content");
  const outcomeIcon = h("span", "pf-v6-c-label__icon");
  outcomeIcon.append(statusGlyph(err ? "danger" : "success"));
  outcomeContent.append(outcomeIcon, h("span", "pf-v6-c-label__text", err ? "Error" : "OK"));
  outcome.append(outcomeContent);

  const action = h("span", "console-activity-head__action", ev.action || kindLabel(ev.kind));
  action.title = ev.action;
  title.append(kind, outcome, action);
  if (ev.actor) title.append(h("span", "console-activity-head__actor", ev.actor));
  const dur = durText(ev.duration);
  if (dur) title.append(h("span", "console-activity-head__meta", dur));
  const ms = tsMillis(ev.time);
  if (ms !== null) title.append(timeEl(ms, now, "console-activity-head__time"));
}

// activate builds the app into host, loads once, and returns a teardown. Every async load checks
// `stale` before touching the DOM, so a load that resolves after the tab closed is dropped.
// Returns the console's app shape (page.ts): a teardown plus setVisible, so the shell can tell
// this pane when it stops being the visible one. Every app hands back this shape rather than a
// bare teardown - an app with nowhere to put the hook is how the log viewer came to write a
// backgrounded tab's status bar.
export function activate(host: HTMLElement): AppInstance {
  const refs = buildScaffold(host);
  let stale = false;
  let loadedEvents: ActivityEvent[] = [];
  let nextPageToken = "";
  let loadMore: (() => void) | null = null;
  let moreBtn: HTMLButtonElement | null = null;
  let loading = false;
  // The client the live load built, kept past that load so expanding a payload reaches the same
  // server the events came from. Null on the demo, and that is what gates the expand control: a
  // synthesized event's refs name blobs no store holds, so the offer could only fail.
  let payloadClient: Client<typeof ActivityService> | null = null;
  // The event index: the collapsible left panel shared with the log viewer's run browser. Its refresh
  // icon re-runs load(); the "N events" count rides in its header. It starts collapsed in a narrow
  // pane and hides entirely when there are no events (the empty-state card carries the cold state).
  const panel: CollapsiblePanel | null = mountCollapsiblePanel({
    scroll: refs.scroll,
    title: "Event index",
    label: "Event index",
    bodyTitle: "Events, newest first",
    onRefresh: () => load(),
    hideWhenEmpty: true,
  });
  const conn = h("span", "console-activity-conn");
  // In a narrow pane the panel auto-collapses and takes conn with it - it lives in head, which is
  // hidden along with the rest of the aside. reopen is the one piece of the panel still on
  // screen in that state, so the count rides there too instead of vanishing with the tree.
  const countBadge = h("span", "console-log-runs__reopen-badge");
  // What the index is currently listing, held apart from the events themselves: switching the
  // grouping repaints the tree alone, and re-rendering the whole app for it would rebuild every
  // section and throw away the reader's scroll position and any payload they had expanded.
  let indexEvents: ActivityEvent[] = [];
  let indexSelect: (index: number) => void = () => {};
  const indexState: IndexState = { open: new Map(), current: null };
  let indexMode: () => IndexMode = () => INDEX_MODES[0].id;
  let indexField: FilterField | null = null;

  function repaintIndex(): void {
    if (!panel) return;
    const words = (indexField?.value() ?? "").toLowerCase().split(/\s+/).filter(Boolean);
    const total = indexEvents.length;
    if (indexField) {
      indexField.el.hidden = total <= FILTER_ABOVE;
      const mode = indexMode();
      renderIndexTree(panel.treeBox, indexEvents, Date.now(), mode, indexState, words, indexSelect);
      const shown = panel.treeBox.querySelectorAll("[data-leaf]").length;
      indexField.setResults(
        words.length ? shown + " of " + total : "",
        words.length
          ? shown === 0
            ? "No events match the filter"
            : shown + " of " + total + " events match the filter"
          : "",
      );
      return;
    }
    renderIndexTree(
      panel.treeBox,
      indexEvents,
      Date.now(),
      indexMode(),
      indexState,
      [],
      indexSelect,
    );
  }

  if (panel) {
    panel.head.insertBefore(conn, panel.refreshBtn);
    panel.reopen.append(countBadge);
    const controls = mountIndexControls(panel, repaintIndex);
    indexMode = controls.mode;
    indexField = controls.field;
  }

  // reveal expands a section, marks its head, moves focus to it and scrolls it into view, so
  // clicking an index leaf lands on that event with something to see and somewhere to continue from.
  function reveal(index: number, sectionEls: HTMLElement[]): void {
    const el = sectionEls[index];
    if (!el) return;
    for (const prev of refs.body.querySelectorAll("[data-highlight]"))
      prev.removeAttribute("data-highlight");
    setSectionOpen(el, true);
    el.querySelector(".console-render-section__head")?.setAttribute("data-highlight", "");
    sectionToggle(el)?.focus({ preventScroll: true });
    el.scrollIntoView({ behavior: motion(), block: "center" });
  }

  // attachPayloadExpand adds one control per stored body to a section's lines, each resolving the
  // full bytes by ref on click.
  //
  // WHY: the event line NAMES a body it does not carry - a response arrives as its first 240
  // characters, a request as nothing but a size and a ref - so the record described payloads and
  // left the reader no way to read one. GetPayload is the documented resolver for a ref
  // (activity.proto), and until now the console was the one client that never called it. There is
  // no CLI that reads a payload either, so pointing at one instead was not an option.
  //
  // The control goes in the BODY rather than the head's action group: head actions fade in on hover
  // (frame.css), which is fine for a copy button and wrong for the only door onto the content.
  function attachPayloadExpand(secEl: HTMLElement, ev: ActivityEvent): void {
    const client = payloadClient;
    if (!client) return;
    const lines = secEl.querySelector(".console-render-section__lines");
    if (!lines) return;
    for (const ref of payloadRefs(ev)) {
      const row = h("div", "console-render-line");
      const content = h("span", "console-render-line__content");
      const btn = h("button", "pf-v6-c-button pf-m-link pf-m-inline");
      btn.type = "button";
      const text = h("span", "pf-v6-c-button__text", payloadLabel(ref));
      btn.append(text);
      const note = h("span");
      btn.addEventListener("click", () => {
        void expandPayload(client, { row, btn, text, note, ref });
      });
      content.append(btn, note);
      row.append(content);
      lines.append(row);
    }
  }

  // expandPayload resolves one ref and replaces its control with the body it names. A NotFound is a
  // normal end state rather than a fault - the server rotates blobs out from under events that still
  // name them - so it reads as a fact on the line, with no retry offered for something that will
  // never come back. Any other failure keeps the control, so a server blip is retryable; the
  // transport has already raised it as a toast.
  async function expandPayload(
    client: Client<typeof ActivityService>,
    ctl: PayloadControl,
  ): Promise<void> {
    if (ctl.btn.disabled) return;
    const label = ctl.text.textContent ?? "";
    ctl.btn.disabled = true;
    ctl.btn.setAttribute("aria-busy", "true");
    ctl.note.textContent = "";
    ctl.text.textContent = "Loading " + ctl.ref.label + "...";
    try {
      const payload = await client.getPayload({ ref: ctl.ref.ref });
      if (stale) return;
      const { lines, clipped } = payloadLines(payload.body);
      // The ref leads the block: a section can expand both bodies, and two runs of text with no
      // header between them would read as one.
      const body = [renderLine(ctl.ref.label + " " + ctl.ref.ref, null)];
      for (const line of lines) body.push(renderLine(line, null));
      if (clipped) {
        const cut = humanBytes(PAYLOAD_MAX_BYTES) + " of " + humanBytes(Number(payload.sizeBytes));
        body.push(renderLine("showing the first " + cut, null));
      }
      ctl.row.replaceWith(...body);
    } catch (e) {
      if (stale) return;
      if (e instanceof ConnectError && e.code === Code.NotFound) {
        const gone = ctl.ref.label + " " + ctl.ref.ref + " is no longer stored";
        ctl.row.replaceWith(renderLine(gone, null));
        return;
      }
      ctl.text.textContent = label;
      ctl.btn.disabled = false;
      ctl.btn.removeAttribute("aria-busy");
      ctl.note.textContent = "  Could not read the " + ctl.ref.label + ": " + errMessage(e);
    }
  }

  function render(events: ActivityEvent[]): void {
    refs.body.replaceChildren();
    const now = Date.now();
    const model = activityToModel(events);
    // Sections map 1:1 onto events in order, so events[i] is the event section i was built from.
    // Only a failure takes the accent rule: a green rule on every passing event is noise, and the
    // outcome is a Label in the head either way.
    const sectionEls: HTMLElement[] = model.sections.map((sec, i) => {
      const ev = events[i];
      const el = buildSection(sec, {
        status: sec.meta?.status === "fail" ? "fail" : "",
        copyText: sec.lines.join("\n"),
        source: "Activity",
        countNoun: ["detail", "details"],
        fillTitle: (title) => fillHead(title, ev, now),
      });
      attachPayloadExpand(el, ev);
      return el;
    });
    for (const el of sectionEls) refs.body.append(el);
    const has = events.length > 0;
    refs.empty.hidden = has;
    const n = events.length;
    // "N events" is the count LOADED, not the count the server holds - the record pages. Saying so
    // costs one character and stops the number reading as a total, which it only is on the last page.
    conn.textContent = n + (n === 1 ? " event" : " events") + (nextPageToken ? "+" : "");
    // The badge stays a bare number - a corner overlay has no room for a sentence, and unlike conn
    // it is not cleared to an error/status string elsewhere, so it always reflects what is actually
    // loaded even while conn is saying something else (e.g. a failed "load older" page).
    countBadge.textContent = n > 0 ? String(n) + (nextPageToken ? "+" : "") : "";
    indexEvents = events;
    indexSelect = (i): void => reveal(i, sectionEls);
    if (panel) {
      repaintIndex();
      panel.applyDefault(has);
    }
    revealDeepLink(events, sectionEls);
    moreBtn = null;
    if (nextPageToken && loadMore) {
      const more = h("button", "pf-v6-c-button pf-m-secondary console-activity-more");
      more.type = "button";
      more.append(h("span", "pf-v6-c-button__text", "Load older activity"));
      more.addEventListener("click", loadMore);
      refs.body.append(more);
      moreBtn = more;
    }
  }

  // revealDeepLink honors "#at=<epoch-ms>", which is how the dashboard's agent tile hands an
  // operator the full entry behind a summary row it just showed them.
  //
  // Matching on the timestamp rather than an id because events carry none, and activityToModel maps
  // events 1:1 onto sections in order - so the event's index IS the section's index, which is the
  // same correspondence the index tree already relies on. An #at= that matches nothing (an aged-out
  // event, a trimmed page) does nothing rather than erroring: arriving at an unscrolled page is a
  // fine outcome, throwing during render is not.
  function revealDeepLink(events: ActivityEvent[], sectionEls: HTMLElement[]): void {
    const at = parseHash().at;
    if (!at) return;
    const want = Number(at);
    if (!Number.isFinite(want)) return;
    const index = events.findIndex((ev) => tsMillis(ev.time) === want);
    if (index >= 0) reveal(index, sectionEls);
  }

  // setBusy marks the load in flight. The refresh control and "Load older" are disabled and say so,
  // because a second press during a load would only queue another request, and a control that does
  // nothing visible reads as broken.
  function setBusy(busy: boolean): void {
    panel?.refreshBtn.toggleAttribute("disabled", busy);
    if (busy) panel?.refreshBtn.setAttribute("aria-busy", "true");
    else panel?.refreshBtn.removeAttribute("aria-busy");
    if (moreBtn) {
      moreBtn.disabled = busy;
      if (busy) moreBtn.setAttribute("aria-busy", "true");
      else moreBtn.removeAttribute("aria-busy");
      const text = moreBtn.querySelector(".pf-v6-c-button__text");
      if (text) text.textContent = busy ? "Loading older activity..." : "Load older activity";
    }
  }

  // showNotice puts a failure above the events that are still on screen: a refresh that fails must
  // not take the page the reader is reading with it. The Retry is the only action a PF inline alert
  // allows, and the toast the transport raised says the same in the corner.
  function showNotice(title: string, detail: string): void {
    const retry = h("button", "pf-v6-c-button pf-m-link pf-m-inline", "Retry");
    retry.type = "button";
    retry.addEventListener("click", () => load());
    refs.notice.hidden = false;
    refs.notice.replaceChildren(
      inlineAlert({ variant: "danger", title, body: detail, actions: [retry] }),
    );
  }

  function clearNotice(): void {
    refs.notice.hidden = true;
    refs.notice.replaceChildren();
  }

  // keepIndex holds the event index open even though there is nothing to list. The refresh control
  // lives in that panel's header, and applyDefault(false) with hideWhenEmpty hides the panel AND its
  // reopen rail - so on a failure the one affordance that could retry goes away with the data, and
  // this app has no toolbar to fall back on. A cold or genuinely empty page still collapses it.
  function showEmptyEvents(connText: string, keepIndex: boolean): void {
    refs.body.replaceChildren();
    clearNotice();
    refs.empty.hidden = false;
    conn.textContent = connText;
    indexEvents = [];
    indexSelect = (): void => {};
    // The selected position named an event in a list that is gone.
    indexState.current = null;
    if (panel) {
      repaintIndex();
      panel.applyDefault(keepIndex);
    }
  }

  // setEmptyTone swaps the empty state between its resting list icon and PF's danger treatment: the
  // danger modifier colours it, and the icon changes shape so the state is not carried by hue.
  function setEmptyTone(danger: boolean): void {
    refs.empty.classList.toggle("pf-m-danger", danger);
    if (danger) refs.emptyIcon.replaceChildren(statusGlyph("danger"));
    else refs.emptyIcon.innerHTML = EMPTY_ICON;
  }

  function showEmpty(
    title: string,
    message: string,
    connText: string,
    opts: { keepIndex?: boolean; danger?: boolean; refresh?: boolean } = {},
  ): void {
    showEmptyEvents(connText, opts.keepIndex ?? false);
    renderEmptyMessage(refs.emptySlots, title, message);
    setEmptyTone(opts.danger ?? false);
    if (opts.refresh) {
      const again = h("button", "pf-v6-c-button pf-m-primary", opts.danger ? "Retry" : "Refresh");
      again.type = "button";
      again.addEventListener("click", () => load());
      refs.emptySlots.actions.append(again);
    }
  }

  function showConnectPrompt(state: ConnectPromptState, connText: string, keepIndex = false): void {
    showEmptyEvents(connText, keepIndex);
    setEmptyTone(false);
    renderConnectPrompt(refs.emptySlots, state, { purpose: PURPOSE, onRetry: () => load() });
  }

  // Bumped by every first-page load, so the answer from an address the reader has since moved off
  // is dropped instead of painted over the current one. A "load older" page keeps the generation
  // it pages through.
  let loadGeneration = 0;

  async function loadLive(serverHost: string, pageToken = ""): Promise<void> {
    if (pageToken && loading) return;
    const generation = pageToken ? loadGeneration : ++loadGeneration;
    const superseded = (): boolean => stale || generation !== loadGeneration;
    loading = true;
    setBusy(true);
    // COLD loads only paint "connecting": showEmptyEvents clears the body, and this same path is what
    // the refresh control re-enters, so painting it over a populated page would blank what the
    // reader is reading before the request has even started. A refresh keeps the events on screen
    // until the new ones arrive, and keeps them if the new ones never do.
    const cold = !pageToken && loadedEvents.length === 0;
    if (cold) {
      conn.textContent = "connecting...";
      showConnectPrompt({ connection: "connecting", host: serverHost }, "connecting...", true);
    }
    try {
      const client = createClient(ActivityService, createServerTransport(serverHost));
      payloadClient = client;
      const resp = await client.listActivityEvents({ pageSize: PAGE_SIZE, pageToken });
      if (superseded()) return;
      loadedEvents = pageToken ? loadedEvents.concat(resp.events) : resp.events;
      nextPageToken = resp.nextPageToken;
      loadMore = nextPageToken ? () => void loadLive(serverHost, nextPageToken) : null;
      clearNotice();
      render(loadedEvents);
      notifyDenials(resp.events);
      if (loadedEvents.length === 0) {
        showEmpty(
          "No activity yet",
          "The server is connected but has not recorded any actions yet. MCP calls, jobs and config changes show up here as they happen.",
          "0 events",
          { refresh: true },
        );
      }
    } catch (e) {
      if (superseded()) return;
      const msg = errMessage(e);
      // A failed refresh or "load older" must not take the events already on screen with it. They
      // stay, a notice sits above them, and the paging button comes back with them.
      if (loadedEvents.length > 0) {
        render(loadedEvents);
        showNotice(pageToken ? "Could not load older activity" : "Could not refresh activity", msg);
        return;
      }
      if (!isUnreachable(e)) {
        showEmpty(
          "Could not read activity",
          "The server at " + serverHost + " answered with an error (" + msg + ").",
          "error",
          { keepIndex: true, danger: true, refresh: true },
        );
        return;
      }
      showConnectPrompt(
        { connection: "disconnected", host: serverHost, reason: msg },
        "not connected",
        true,
      );
    } finally {
      if (generation === loadGeneration) {
        loading = false;
        setBusy(false);
      }
    }
  }

  // load resolves which source to read: an explicit #demo, then resolveServerHostOrRemembered (a
  // #port link, the server-origin/shared console, the Settings address, or the last server the
  // dashboard reached); otherwise the cold empty state.
  function load(): void {
    const params = parseHash();
    consumeLiveToken(params);
    // adoptServerOrigin, not just consumeLiveToken. Each app is its own esbuild bundle, so
    // lib/server's "did we adopt this origin" flag is PER-BUNDLE state: the shell setting it
    // does not make it true in here, and serverAttach then returns null on a console served by
    // that very server. Without this the app works only after the dashboard has persisted a
    // host to localStorage, which is the shape of bug that looks fine on the developer's machine.
    adoptServerOrigin();
    if (wantsDemo(params)) {
      payloadClient = null;
      loadedEvents = demoEvents(Date.now());
      nextPageToken = "";
      loadMore = null;
      clearNotice();
      render(loadedEvents);
      return;
    }
    const serverHost = resolveServerHostOrRemembered(params);
    if (serverHost) {
      void loadLive(serverHost);
      return;
    }
    loadGeneration++; // any load still out belongs to an address that no longer resolves
    loading = false;
    setBusy(false);
    showConnectPrompt({ connection: "none" }, "not connected");
  }

  load();
  // The labels age whether or not anything loads, so a ticker rewrites them in place.
  const untick = tickRelativeTimes(host);
  // A new address is followed only while no events are on screen: a page the reader is reading keeps
  // the server it came from until they refresh.
  const unsubscribeHost = subscribeDefaultHost(() => {
    if (loadedEvents.length === 0) load();
  });

  return {
    // Nothing to suppress yet: Activity reloads on demand and holds no timer, and it writes no part
    // of the shared status bar. The hook is here so the answer is already in place the day it grows
    // one.
    setVisible(): void {},
    deactivate(): void {
      stale = true;
      unsubscribeHost();
      untick();
      panel?.dispose();
      indexField?.dispose();
    },
  };
}
