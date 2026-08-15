// activityDrawer.ts - the shell's activity drawer: what magus is running right now, and what ran
// recently. It is the immediate sibling of share.ts, built on the same right-docked side-panel
// idiom (the notification-center / reference-drawer family): one hidden singleton appended to
// document.body, toggled by a status-bar button through the one delegated footer click in main.ts,
// dismissed on Escape or an outside pointerdown.
//
// It diverges from share.ts on exactly ONE point, deliberately. Share moves focus into the panel on
// open, because sharing is a task you came to perform. This is a readout you glance at WHILE working
// somewhere else, and it repaints on a timer, so pulling focus would interrupt the very thing being
// watched. Nothing here ever calls focus(). The summary line is an aria-live="polite" region instead,
// so a screen reader is told what changed without the caret leaving where the user put it. It is the
// SUMMARY that is live and not the lists: a list rebuilt every few seconds inside a live region
// re-announces every row on every tick, which is how a considerate feature becomes an unusable one.
//
// Two sections, newest first within each:
//
//   RUNNING - the live pool's running targets, plus the workspace locks held right now. Both come
//             from one Status frame (StatusService.GetStatus - the same message the dashboard reads
//             over its SSE), and both are needed: a running target is work the daemon's own pool is
//             executing, while a lock holder is a separate magus PROCESS mutating a project, which
//             is what a run started from a terminal looks like and which the pool cannot see at all.
//             Showing only the first would report an idle daemon during someone else's build.
//   RECENT  - the daemon's retained run descriptors (GET /api/v1/outputs), newest first, each with
//             the outcome it finished with. This is the same read-only feed the log viewer's run
//             browser paints its tree from.
//
// Polled, not streamed, following the dashboard's activity-tile idiom (setInterval around a unary
// read): the shell owns no SSE of its own, and opening a second status stream alongside the
// dashboard's would buy nothing a four-second poll does not. The timer runs only while the panel is
// OPEN - a closed drawer is not a reason to talk to the daemon.

import { createClient } from "@connectrpc/connect";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { StatusService, type Status } from "../gen/magus/status/v1/status_pb";
import { authHeaders, createDaemonTransport, getLiveToken, resolveDaemonHost } from "../lib/daemon";

// The refresh cadence, and the deadline one refresh gets. Deliberately NOT the operator's configured
// refresh rate (lib/settings getPollMs, default 20s): that rate governs how often a dashboard redraws
// aggregate counters, and a panel answering "what is happening right now" is stale at 20 seconds. The
// dashboard's own activity tile made the same call for the same reason.
const POLL_MS = 4000;
const FETCH_TIMEOUT_MS = 4000;
// How many finished runs the RECENT section lists. The feed returns everything the output store has
// retained, which on a busy workspace is thousands of rows - far past the point where a drawer is
// being read rather than scrolled.
const RECENT_LIMIT = 25;

// ---- the row model ---------------------------------------------------------

// ActivityRow is one line in either section. Three different sources (a pool slot, a lock holder, a
// finished run) are projected into ONE shape so each section renders as a single serialized list
// rather than growing a renderer per source.
export interface ActivityRow {
  // Stable within its section: an invocation id, "lock:<pid>:<project>", or an output ref.
  id: string;
  // The lead line: the command, or "project:target".
  title: string;
  // The trailing meta - project, pid, step, age, duration - whatever that source actually knows.
  detail: string;
  // The instant the row is anchored to: when a running thing STARTED, when a finished one ENDED.
  // Sorts its section newest first. 0 means the source reported no usable time.
  atMs: number;
  // "" while the answer is not known yet (everything RUNNING), "pass"/"fail" once it is.
  outcome: "" | "pass" | "fail";
  // The delegation unit this row belongs to. Always absent today. Phase 2 introduces the delegation
  // ledger and groups entries by unit; carrying the field now makes that a join rather than a change
  // to every producer and every consumer at once.
  unit?: string;
}

// RunDescriptor mirrors one row of the daemon's GET /api/v1/outputs JSON. It is the same wire DTO
// the log viewer's run browser reads (logs/runtree.ts's RunSummary), redeclared rather than imported:
// each console surface is its own bundle, and reaching across for an eight-field interface would pull
// the run browser's module into the shell to get it. timestamp_ms is when the run FINISHED (the cache
// stamps the descriptor as it records the output), so its age reads as "how long ago this ran".
export interface RunDescriptor {
  ref: string;
  project: string;
  target: string;
  inv?: string;
  failed: boolean;
  error?: string;
  timestamp_ms: number;
  duration_ms: number;
}

// tsMillis converts a protobuf Timestamp to epoch milliseconds, or 0 when the field is absent. It is
// the null-ish variant on purpose (compare dashboard/state.ts's tsMillisOrNow, which substitutes NOW):
// a running target whose start time the daemon did not report must not render as "0s", which reads as
// "just started" and is indistinguishable from the truth.
function tsMillis(ts: Timestamp | undefined): number {
  if (!ts) return 0;
  return Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
}

// relAge renders how long ago an instant was, in the terse units the dashboard already uses
// (12s / 4m / 2h). Empty for an unknown instant, per tsMillis above.
export function relAge(atMs: number, nowMs: number): string {
  if (atMs <= 0) return "";
  const secs = Math.max(0, Math.round((nowMs - atMs) / 1000));
  if (secs < 60) return secs + "s";
  const mins = Math.round(secs / 60);
  if (mins < 60) return mins + "m";
  return Math.round(mins / 60) + "h";
}

// fmtMs renders a wall-clock duration as "820ms" / "12.4s"; "" for a run the store recorded no
// duration for, so a zero never masquerades as an instant build.
export function fmtMs(ms: number): string {
  if (!(ms > 0)) return "";
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(1) + "s";
}

// runningRows projects one Status frame into the RUNNING section: every pool slot that is executing,
// then every workspace lock held right now, merged and sorted newest first.
export function runningRows(st: Status | undefined, nowMs: number): ActivityRow[] {
  const rows: ActivityRow[] = [];
  for (const t of st?.pool?.runningTargets ?? []) {
    const atMs = tsMillis(t.startTime);
    const args = t.args ?? [];
    rows.push({
      id: t.invocation || "run:" + args.join(" "),
      title: args.length ? "magus " + args.join(" ") : "magus",
      detail: [t.workspace, t.step, relAge(atMs, nowMs)].filter(Boolean).join(" - "),
      atMs,
      outcome: "",
    });
  }
  for (const l of st?.locks ?? []) {
    const atMs = tsMillis(l.acquireTime);
    rows.push({
      id: "lock:" + l.pid + ":" + l.project,
      // The holder's argv is what identifies it. The project alone would not: two runs against the
      // same tree are the case a reader most needs to tell apart.
      title: l.command || "lock held",
      detail: [l.project || ".", l.pid ? "pid " + l.pid : "", relAge(atMs, nowMs)]
        .filter(Boolean)
        .join(" - "),
      atMs,
      outcome: "",
    });
  }
  return rows.sort((a, b) => b.atMs - a.atMs);
}

// recentRows projects the run-descriptor feed into the RECENT section, newest first and capped. The
// feed is already newest-first, but it is sorted here anyway so the ordering is this module's
// guarantee rather than an assumption about a route it does not own.
export function recentRows(runs: RunDescriptor[], nowMs: number): ActivityRow[] {
  return runs
    .map(
      (r): ActivityRow => ({
        id: r.ref,
        title: r.project ? r.project + ":" + r.target : r.target || r.ref,
        detail: [fmtMs(r.duration_ms), relAge(r.timestamp_ms, nowMs)].filter(Boolean).join(" - "),
        atMs: r.timestamp_ms,
        // A descriptor exists only for a run that finished, so the outcome is always known here.
        outcome: r.failed ? "fail" : "pass",
      }),
    )
    .sort((a, b) => b.atMs - a.atMs)
    .slice(0, RECENT_LIMIT);
}

// summaryLine is what the aria-live region announces: the two counts in one sentence, so a screen
// reader hears "3 running, 25 recent" when a tick changes it and nothing at all when it does not.
export function summaryLine(running: number, recent: number): string {
  return running + " running, " + recent + " recent";
}

// ---- the panel -------------------------------------------------------------

export interface ActivityDrawer {
  open(): void;
  close(): void;
  toggle(): void;
}

// mountActivityDrawer builds the singleton drawer (hidden) once and returns its controller. The shell
// wires the status-bar activity button (rebuilt per surface) to toggle() through one delegated click,
// exactly as it does for the share panel.
export function mountActivityDrawer(): ActivityDrawer {
  const panel = document.createElement("section");
  panel.className = "console-shell-activity";
  panel.id = "console-activitypanel";
  // A region, NOT share.ts's dialog: a dialog promises focus management, and this panel deliberately
  // never takes focus. Stated explicitly rather than left to the implicit role of a named <section>,
  // so the difference from its sibling is visible here rather than inferred from an omission.
  panel.setAttribute("role", "region");
  panel.setAttribute("aria-label", "Activity");
  panel.hidden = true;

  const head = document.createElement("div");
  head.className = "console-shell-activity__head";
  const title = document.createElement("span");
  title.className = "console-shell-activity__title";
  title.textContent = "Activity";
  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "pf-v6-c-button pf-m-plain console-shell-activity__close";
  closeBtn.setAttribute("aria-label", "Close activity panel");
  closeBtn.textContent = "×"; // multiplication sign, the console's close glyph
  head.append(title, closeBtn);

  const body = document.createElement("div");
  body.className = "console-shell-activity__body";

  const summary = document.createElement("p");
  summary.className = "console-shell-activity__summary";
  summary.setAttribute("aria-live", "polite");
  summary.textContent = summaryLine(0, 0);

  const running = buildSection("Running", "Nothing is running.");
  const recent = buildSection("Recent", "No runs recorded yet.");
  body.append(summary, running.el, recent.el);
  panel.append(head, body);
  document.body.append(panel);

  let open = false;
  let timer: ReturnType<typeof setInterval> | null = null;

  const setOpen = (v: boolean): void => {
    if (v === open) return;
    open = v;
    panel.hidden = !v;
    panel.setAttribute("aria-hidden", v ? "false" : "true");
    if (v) {
      void refresh();
      timer = setInterval(() => void refresh(), POLL_MS);
    } else if (timer) {
      clearInterval(timer);
      timer = null;
    }
    // No focus() on open, and none on close: see the header comment. The panel is read, not entered.
  };

  closeBtn.addEventListener("click", () => setOpen(false));

  // Dismiss on an outside pointerdown or Escape. A click on any status-bar activity button
  // ([data-activity-toggle]) is that button's own toggle, so ignore it here to avoid closing then
  // immediately reopening (or vice versa). Same shape as share.ts's dismissal.
  document.addEventListener("pointerdown", (e) => {
    if (!open) return;
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (panel.contains(t)) return;
    if (t instanceof Element && t.closest("[data-activity-toggle]")) return;
    setOpen(false);
  });
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) setOpen(false);
  });

  // refresh reads both feeds for one tick and repaints. The two are fetched together but fail
  // independently: a status read that fails must not blank a run list that answered, and vice versa,
  // so each section keeps its own honest empty state rather than sharing one "something went wrong".
  async function refresh(): Promise<void> {
    const host = resolveDaemonHost();
    if (!host) {
      running.render([], "Not connected to a daemon.");
      recent.render([], "Not connected to a daemon.");
      setSummary(0, 0);
      return;
    }
    const [status, runs] = await Promise.all([fetchStatus(host), fetchRuns(host)]);
    const now = Date.now();
    const runningList = runningRows(status, now);
    const recentList = runs ? recentRows(runs, now) : [];
    running.render(
      runningList,
      status ? "Nothing is running." : "Could not read the daemon's status.",
    );
    recent.render(recentList, runs ? "No runs recorded yet." : "Could not read the run history.");
    setSummary(runningList.length, recentList.length);
  }

  // Written only when it actually changed: assigning the same string still counts as a mutation to
  // some assistive tech, which would re-announce an unchanged count on every four-second tick.
  function setSummary(runningCount: number, recentCount: number): void {
    const line = summaryLine(runningCount, recentCount);
    if (summary.textContent !== line) summary.textContent = line;
  }

  return {
    open: () => setOpen(true),
    close: () => setOpen(false),
    toggle: () => setOpen(!open),
  };
}

// Section is one titled list plus its count and its empty state - the drawer has two, built the same
// way, so the construction lives here once.
interface Section {
  el: HTMLElement;
  render(rows: ActivityRow[], emptyText: string): void;
}

function buildSection(heading: string, initialEmpty: string): Section {
  const el = document.createElement("section");
  el.className = "console-shell-activity__section";

  const headEl = document.createElement("h3");
  headEl.className = "console-shell-activity__sectionhead";
  const label = document.createElement("span");
  label.textContent = heading;
  const countLabel = document.createElement("span");
  countLabel.className = "pf-v6-c-label pf-m-compact";
  const count = document.createElement("span");
  count.className = "pf-v6-c-label__content";
  count.textContent = "0";
  countLabel.append(count);
  headEl.append(label, countLabel);

  const list = document.createElement("ul");
  list.className = "console-shell-activity__list";
  list.setAttribute("role", "list");

  const empty = document.createElement("p");
  empty.className = "console-shell-activity__empty";
  empty.textContent = initialEmpty;

  el.append(headEl, list, empty);

  return {
    el,
    render(rows: ActivityRow[], emptyText: string): void {
      count.textContent = String(rows.length);
      empty.textContent = emptyText;
      empty.hidden = rows.length > 0;
      list.replaceChildren(...rows.map(rowEl));
    },
  };
}

// rowEl renders one row: the command in mono, its meta after, and - once phase 2 populates it - the
// delegation unit it belongs to.
function rowEl(row: ActivityRow): HTMLElement {
  const li = document.createElement("li");
  li.className = "console-shell-activity__row";
  // State is a data attribute, never a modifier class (PATTERNFLY.md). Absent while the outcome is
  // still unknown, so an in-flight row is styled as neutral rather than provisionally green.
  if (row.outcome) li.dataset.outcome = row.outcome;

  const titleEl = document.createElement("code");
  titleEl.className = "console-shell-activity__rowtitle";
  titleEl.textContent = row.title;
  li.append(titleEl);

  if (row.unit) {
    const unitEl = document.createElement("span");
    unitEl.className = "console-shell-activity__rowunit";
    unitEl.textContent = row.unit;
    li.append(unitEl);
  }

  const metaEl = document.createElement("span");
  metaEl.className = "console-shell-activity__rowmeta";
  metaEl.textContent = row.detail;
  li.append(metaEl);
  return li;
}

// fetchStatus reads one Status frame over the typed StatusService, the unary counterpart to the
// stream the dashboard subscribes to. Resolves undefined on any failure so a blip leaves an honest
// empty state instead of throwing into the poll timer.
async function fetchStatus(host: string): Promise<Status | undefined> {
  try {
    const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
    const resp = await client.getStatus({});
    return resp.status;
  } catch {
    return undefined;
  }
}

// fetchRuns reads the daemon's retained run descriptors. Resolves null - not [] - on any failure, so
// "could not read the history" stays distinguishable from "the history is empty"; the two mean very
// different things to someone wondering why the panel is blank.
async function fetchRuns(host: string): Promise<RunDescriptor[] | null> {
  try {
    const res = await fetch("http://" + host + "/api/v1/outputs", {
      headers: authHeaders(),
      cache: "no-store",
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    const body = (await res.json()) as { outputs?: RunDescriptor[] };
    return Array.isArray(body.outputs) ? body.outputs : [];
  } catch {
    return null;
  }
}
