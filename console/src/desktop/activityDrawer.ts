// activityDrawer.ts - the shell's activity drawer: what magus is running right now, and what ran
// recently. It is the immediate sibling of share.ts, built on the same right-docked side-panel
// idiom (panel.ts): one hidden singleton appended to document.body, toggled by a status-bar button
// through the one delegated footer click in main.ts.
//
// It diverges from share.ts on two points, deliberately. Share moves focus into the panel on open,
// because sharing is a task you came to perform; this is a readout you glance at WHILE working
// somewhere else, and it repaints on a timer, so pulling focus would interrupt the very thing being
// watched. And Share closes on a click outside, while this closes only on Escape, its close button
// or its toggle: a click into the app behind it is the reader getting on with the work it sits
// beside. The summary line is a polite status region instead of focus, so a screen reader is told
// what changed without the caret leaving where the user put it. It is the SUMMARY that is live and
// not the lists: a list rebuilt every few seconds inside a live region re-announces every row on
// every tick, which is how a considerate feature becomes an unusable one.
//
// Two sections, newest first within each:
//
//   RUNNING - the live pool's running targets, plus the workspace locks held right now. Both come
//             from one Status frame (StatusService.GetStatus - the same message the dashboard reads
//             over its SSE), and both are needed: a running target is work the server's own pool is
//             executing, while a lock holder is a separate magus PROCESS mutating a project, which
//             is what a run started from a terminal looks like and which the pool cannot see at all.
//             Showing only the first would report an idle server during someone else's build.
//   RECENT  - the server's retained run descriptors (GET /api/v1/outputs), newest first, each with
//             the outcome it finished with. This is the same read-only feed the log viewer's run
//             browser paints its tree from.
//
// Polled, not streamed, following the dashboard's activity-tile idiom (setInterval around a unary
// read): the shell owns no SSE of its own, and opening a second status stream alongside the
// dashboard's would buy nothing a four-second poll does not. The timer runs only while the panel is
// OPEN - a closed drawer is not a reason to talk to the server.

import { createClient } from "@connectrpc/connect";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { StatusService, type Status } from "@wire/status/v1alpha1/status_pb";
import { ViewerService, type Output } from "@wire/viewer/v1alpha1/viewer_pb";
import {
  createServerTransport,
  getLiveToken,
  parseHash,
  resolveServerHost,
  wantsDemo,
} from "../lib/server";
import { statusMark, type Status as StatusKind } from "../ui/status";
import { scenarioRuns } from "./demo-scenario";
import { buildPanel, panelBehavior } from "./panel";

// The refresh cadence, and the deadline each read inside one refresh gets. Deliberately NOT the
// operator's configured refresh rate (lib/settings getPollMs, default 20s): that rate governs how
// often a dashboard redraws aggregate counters, and a panel answering "what is happening right now"
// is stale at 20 seconds. The dashboard's own activity tile made the same call for the same reason.
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
  // The job this row belongs to; the wire spelling stays "unit". The join exists
  // (plan/jobs.ts joinRuns); what is still missing is a producer that stamps
  // the field - no run or trail event records its unit yet.
  unit?: string;
  // The detail without its age: what the source knows about the row, apart from when. The drawer
  // writes its own time beside it, in one format; `detail` keeps the age inline for the Plan app's
  // compact list, which is the other reader of these rows.
  facts?: string;
  // Where the run opens in the Log Viewer. Absent for a row with nothing stored to open, such as a
  // lock held by another process.
  href?: string;
}

// RunDescriptor mirrors one row of the server's GET /api/v1/outputs JSON. It is the same wire DTO
// the log viewer's run browser reads (logs/runtree.ts's RunSummary), redeclared rather than imported:
// each console app is its own bundle, and reaching across for an eight-field interface would pull
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

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// finite is the number check the two timestamps go through: typeof alone lets NaN and Infinity past,
// and both of those are numbers that ruin what they touch.
function finite(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

// wireDescriptors maps the ViewerService feed onto the rows this panel and the plan app both
// render. Both work in unix millis, which is the unit the store itself records.
export function wireDescriptors(outputs: readonly Output[]): RunDescriptor[] {
  return outputs.map((o) => ({
    ref: o.ref,
    project: o.project,
    target: o.target,
    inv: o.invocation,
    failed: o.failed,
    error: o.error,
    timestamp_ms: tsMillis(o.createTime),
    duration_ms: o.duration ? Number(o.duration.seconds) * 1000 + o.duration.nanos / 1e6 : 0,
  }));
}

// tsMillis converts a protobuf Timestamp to epoch milliseconds, or 0 when the field is absent. It is
// the null-ish variant on purpose (compare dashboard/state.ts's tsMillisOrNow, which substitutes NOW):
// a running target whose start time the server did not report must not render as "0s", which reads as
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
    const facts = [t.workspace, t.step].filter(Boolean).join(" - ");
    rows.push({
      id: t.invocation || "run:" + args.join(" "),
      title: args.length ? "magus " + args.join(" ") : "magus",
      detail: [facts, relAge(atMs, nowMs)].filter(Boolean).join(" - "),
      facts,
      atMs,
      outcome: "",
      // The log viewer opens an invocation by id; a slot with none has nothing to link to.
      href: t.invocation ? viewerHref("inv", t.invocation, false) : undefined,
    });
  }
  for (const l of st?.locks ?? []) {
    const atMs = tsMillis(l.acquireTime);
    const facts = [l.project || ".", l.pid ? "pid " + l.pid : ""].filter(Boolean).join(" - ");
    rows.push({
      id: "lock:" + l.pid + ":" + l.project,
      // The holder's argv is what identifies it. The project alone would not: two runs against the
      // same tree are the case a reader most needs to tell apart.
      title: l.command || "lock held",
      detail: [facts, relAge(atMs, nowMs)].filter(Boolean).join(" - "),
      facts,
      atMs,
      outcome: "",
    });
  }
  return rows.sort((a, b) => b.atMs - a.atMs);
}

// recentRows projects the run-descriptor feed into the RECENT section, newest first and capped. The
// feed is already newest-first, but it is sorted here anyway so the ordering is this module's
// guarantee rather than an assumption about a route it does not own.
export function recentRows(runs: RunDescriptor[], nowMs: number, demo = false): ActivityRow[] {
  return runs
    .map(
      (r): ActivityRow => ({
        id: r.ref,
        title: r.project ? r.project + ":" + r.target : r.target || r.ref,
        detail: [fmtMs(r.duration_ms), relAge(r.timestamp_ms, nowMs)].filter(Boolean).join(" - "),
        facts: fmtMs(r.duration_ms),
        atMs: r.timestamp_ms,
        // A descriptor exists only for a run that finished, so the outcome is always known here.
        outcome: r.failed ? "fail" : "pass",
        href: r.ref ? viewerHref("ref", r.ref, demo) : undefined,
      }),
    )
    .sort((a, b) => b.atMs - a.atMs)
    .slice(0, RECENT_LIMIT);
}

// summaryLine is what the live region announces: the two counts in one sentence, so a screen
// reader hears "3 running, 25 recent" when a tick changes it and nothing at all when it does not.
// Demo data says so, since the same sentence over fabricated runs would read as a claim about a server.
export function summaryLine(running: number, recent: number, demo = false): string {
  return running + " running, " + recent + " recent" + (demo ? " (demo data)" : "");
}

// viewerHref is the Log Viewer deep link for one run, relative like the Runs app's: every console
// page resolves "logs/" against /console/, so it works at a server origin, under the docs site's
// base path, or on a dev port. The demo carries its fragment so a demo run opens as one.
function viewerHref(key: "inv" | "ref", value: string, demo: boolean): string {
  return "logs/#" + (demo ? "demo&" : "") + key + "=" + encodeURIComponent(value);
}

// whenLabel is the one time format the drawer uses: relative while it is recent ("12s ago",
// "4m ago"), a dated clock time once an hour has passed ("Oct 9, 14:03"). "" for an instant the
// source did not report, so a missing time never reads as "just now".
export function whenLabel(atMs: number, nowMs: number): string {
  if (atMs <= 0) return "";
  if (nowMs - atMs < 3600_000) return relAge(atMs, nowMs) + " ago";
  return new Date(atMs).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// demoDescriptors reads the demo scenario's run history as the same descriptors the server feed
// yields, so the drawer shows the demo's own runs and says so, rather than "not connected".
function demoDescriptors(nowMs: number): RunDescriptor[] {
  return scenarioRuns(nowMs).map((r) => ({
    ref: r.ref,
    project: r.project,
    target: r.target,
    inv: r.inv,
    failed: r.state === "failed",
    error: r.error,
    timestamp_ms: r.endMs,
    duration_ms: r.durationMs,
  }));
}

// ---- the panel -------------------------------------------------------------

export interface ActivityDrawer {
  open(): void;
  close(): void;
  toggle(): void;
  isOpen(): boolean;
  // Remove the drawer for good: its timer, its document listeners, and the panel itself. close()
  // is not that - it hides a panel that is meant to be reopened, so the listeners stay. A shell that
  // is going away needs this one, or every re-mount leaves another keydown listener on document
  // holding a detached panel alive and toggling it.
  destroy(): void;
}

export interface ActivityOptions {
  // Runs after every open or close, so the status bar's toggles can say which they are.
  onChange?: (open: boolean) => void;
}

// mountActivityDrawer builds the singleton drawer (hidden) once and returns its controller. The shell
// wires the status-bar activity button (rebuilt per app) to toggle() through one delegated click,
// exactly as it does for the share panel.
export function mountActivityDrawer(options: ActivityOptions = {}): ActivityDrawer {
  // A region, NOT share.ts's dialog: a dialog promises focus management, and this panel deliberately
  // never takes focus.
  const shell = buildPanel({ id: "console-activitypanel", title: "Activity", role: "region" });
  const body = shell.body;

  const summary = document.createElement("p");
  summary.className = "console-shell-activity__summary";
  summary.setAttribute("role", "status");
  summary.textContent = summaryLine(0, 0);

  const running = buildSection("Running", "Nothing is running.");
  const recent = buildSection("Recent", "No runs recorded yet.");
  body.append(summary, running.el, recent.el);
  document.body.append(shell.el);

  let destroyed = false;
  let timer: ReturnType<typeof setInterval> | null = null;
  // The generation a read is stamped with. A read that resolves after a newer one started - or after
  // the panel shut - is dropped rather than painted: the poll is four seconds and a slow server
  // answers out of order, so without this the tick before last can repaint over the newest rows.
  let generation = 0;
  // The reads in flight, so shutting the panel stops them. A closed drawer is not a reason to keep
  // the server answering, and it is the same rule the timer already follows.
  let reading: AbortController | null = null;

  // stopReading retires whatever is in flight - the abort ends the request, the bumped generation
  // makes sure a response already on its way in cannot paint.
  const stopReading = (): void => {
    generation++;
    reading?.abort();
    reading = null;
  };

  // Closes on Escape, the close button, or its own status-bar toggle, and on nothing else. This is a
  // readout the reader glances at while working somewhere else, so a click into the app behind it must
  // not dismiss it. No focus moves in on open: see the header comment.
  const behavior = panelBehavior(shell, {
    toggles: "[data-activity-toggle]",
    closeOnOutside: false,
    onChange(v) {
      if (v) {
        void refresh();
        timer = setInterval(() => void refresh(), POLL_MS);
      } else {
        if (timer) {
          clearInterval(timer);
          timer = null;
        }
        stopReading();
      }
      options.onChange?.(v);
    },
  });

  // refresh reads both feeds for one tick and repaints. The two are fetched together but fail
  // independently: a status read that fails must not blank a run list that answered, and vice versa,
  // so each section keeps its own honest empty state rather than sharing one "something went wrong".
  async function refresh(): Promise<void> {
    // This tick supersedes whatever the last one is still waiting for - including when there is
    // nothing to read, or a read still out from the server that just went away would paint rows
    // over "not connected".
    stopReading();
    // The fragment decides demo, as everywhere else in the shell: with no server to read, the drawer
    // shows the demo's own runs and says they are demo data, not "not connected".
    if (wantsDemo(parseHash())) {
      const now = Date.now();
      const demoList = recentRows(demoDescriptors(now), now, true);
      running.render([], "Nothing is running in the demo.", now);
      recent.render(demoList, "No demo runs.", now);
      setSummary(0, demoList.length, true);
      return;
    }
    const host = resolveServerHost();
    if (!host) {
      running.render([], "Not connected to a server.", Date.now());
      recent.render([], "Not connected to a server.", Date.now());
      setSummary(0, 0);
      return;
    }
    const ac = new AbortController();
    reading = ac;
    const gen = generation;
    const [status, runs] = await Promise.all([
      fetchStatus(host, ac.signal),
      fetchRuns(host, ac.signal),
    ]);
    if (gen !== generation) return;
    const now = Date.now();
    const runningList = runningRows(status.kind === "ok" ? status.status : undefined, now);
    const recentList = runs.kind === "ok" ? recentRows(runs.runs, now) : [];
    // The reason rides the empty line rather than being dropped: "could not read the server's
    // status" is the same sentence whether the server went away, the token went stale, or the read
    // ran out of time, and only the last of those is worth waiting through.
    running.render(
      runningList,
      status.kind === "ok"
        ? "Nothing is running."
        : "Could not read the server's status: " + status.detail,
      now,
    );
    recent.render(
      recentList,
      runs.kind === "ok"
        ? "No runs recorded yet."
        : "Could not read the run history: " + runs.detail,
      now,
    );
    setSummary(runningList.length, recentList.length);
  }

  // Written only when it actually changed: assigning the same string still counts as a mutation to
  // some assistive tech, which would re-announce an unchanged count on every four-second tick.
  function setSummary(runningCount: number, recentCount: number, demo = false): void {
    const line = summaryLine(runningCount, recentCount, demo);
    if (summary.textContent !== line) summary.textContent = line;
  }

  return {
    // A destroyed drawer stays destroyed: a stale reference cannot put a detached panel back on screen.
    open: () => {
      if (!destroyed) behavior.open();
    },
    close: behavior.close,
    toggle: () => {
      if (!destroyed) behavior.toggle();
    },
    isOpen: behavior.isOpen,
    destroy() {
      if (destroyed) return;
      // Shut it first, while the behavior still works: that is what clears the timer and aborts the reads.
      behavior.close();
      destroyed = true;
      behavior.destroy();
      shell.el.remove();
    },
  };
}

// Section is one titled list plus its count and its empty state - the drawer has two, built the same
// way, so the construction lives here once.
interface Section {
  el: HTMLElement;
  render(rows: ActivityRow[], emptyText: string, nowMs: number): void;
}

function buildSection(heading: string, initialEmpty: string): Section {
  const el = document.createElement("section");
  el.className = "console-shell-activity__section";

  const headEl = document.createElement("h3");
  headEl.className = "console-shell-activity__section-head";
  const label = document.createElement("span");
  label.textContent = heading;
  // A PF Badge in its read state: a count, not a status.
  const count = document.createElement("span");
  count.className = "pf-v6-c-badge pf-m-read";
  count.textContent = "0";
  headEl.append(label, count);

  const list = document.createElement("ul");
  list.className = "console-shell-activity__list";
  list.setAttribute("role", "list");

  const empty = document.createElement("p");
  empty.className = "console-shell-activity__empty";
  empty.textContent = initialEmpty;

  el.append(headEl, list, empty);

  return {
    el,
    render(rows: ActivityRow[], emptyText: string, nowMs: number): void {
      count.textContent = String(rows.length);
      empty.textContent = emptyText;
      empty.hidden = rows.length > 0;
      list.replaceChildren(...rows.map((r) => rowEl(r, nowMs)));
    },
  };
}

// outcomeStatus is the shape an outcome wears: a row that has finished passed or failed, one that has
// not is still running. The row's left rule is the colour; this is the icon and the word beside it.
function outcomeStatus(outcome: ActivityRow["outcome"]): StatusKind {
  if (outcome === "pass") return "success";
  if (outcome === "fail") return "danger";
  return "running";
}

// rowEl renders one row: an outcome mark, the command in mono (a link to the run when there is one
// stored), its meta after, one time, and - once a producer stamps it - the job it belongs to.
function rowEl(row: ActivityRow, nowMs: number): HTMLElement {
  const li = document.createElement("li");
  li.className = "console-shell-activity__row";
  // State is a data attribute, never a modifier class (README.md). Absent while the outcome is
  // still unknown, so an in-flight row is styled as neutral rather than provisionally green.
  if (row.outcome) li.dataset.outcome = row.outcome;

  const lead = document.createElement("div");
  lead.className = "console-shell-activity__row-title";
  lead.append(statusMark(outcomeStatus(row.outcome)));
  const titleEl = document.createElement("code");
  titleEl.textContent = row.title;
  if (row.href) {
    const link = document.createElement("a");
    link.className = "console-shell-activity__row-link";
    link.href = row.href;
    link.append(titleEl);
    lead.append(link);
  } else {
    lead.append(titleEl);
  }
  li.append(lead);

  if (row.unit) {
    const unitEl = document.createElement("span");
    unitEl.className = "console-shell-activity__row-unit";
    unitEl.textContent = row.unit;
    li.append(unitEl);
  }

  const metaEl = document.createElement("span");
  metaEl.className = "console-shell-activity__row-meta";
  const facts = row.facts ?? row.detail;
  if (facts) metaEl.append(document.createTextNode(facts));
  const when = whenLabel(row.atMs, nowMs);
  if (when) {
    if (facts) metaEl.append(document.createTextNode(" - "));
    const time = document.createElement("time");
    time.dateTime = new Date(row.atMs).toISOString();
    time.title = new Date(row.atMs).toLocaleString();
    time.textContent = when;
    metaEl.append(time);
  }
  li.append(metaEl);
  return li;
}

// The two answers each read can give, in the shape plan/jobs.ts's JobsRead uses: it came back,
// or it did not and here is WHY. A bare undefined/null tells the panel that something went wrong and
// nothing else, which is the half of the answer a reader cannot act on.
type StatusRead =
  | { readonly kind: "ok"; readonly status: Status | undefined }
  | { readonly kind: "unreadable"; readonly detail: string };

type RunsRead =
  | { readonly kind: "ok"; readonly runs: RunDescriptor[] }
  | { readonly kind: "unreadable"; readonly detail: string };

// why is what an exception contributes to the failure line. An aborted or timed-out read arrives as
// a DOMException whose message says which of the two it was, and that distinction is the reason the
// reason is carried at all.
function why(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// deadline is the signal one read runs under: the panel's own abort (a close, a destroy) OR the
// four-second cap, whichever fires first. Both halves are needed - a timeout alone keeps a shut
// drawer talking to the server, and an abort alone lets a read that never answers hold the poll's
// place forever.
function deadline(signal: AbortSignal): AbortSignal {
  return AbortSignal.any([signal, AbortSignal.timeout(FETCH_TIMEOUT_MS)]);
}

// fetchStatus reads one Status frame over the typed StatusService, the unary counterpart to the
// stream the dashboard subscribes to. Never throws: a blip leaves an honest empty state naming the
// failure instead of throwing into the poll timer.
async function fetchStatus(host: string, signal: AbortSignal): Promise<StatusRead> {
  try {
    const client = createClient(StatusService, createServerTransport(host, getLiveToken()));
    const resp = await client.getStatus({}, { signal: deadline(signal) });
    return { kind: "ok", status: resp.status };
  } catch (e) {
    return { kind: "unreadable", detail: why(e) };
  }
}

// fetchRuns reads the server's retained run descriptors. "Could not read the history" stays
// distinguishable from "the history is empty"; the two mean very different things to someone
// wondering why the panel is blank.
async function fetchRuns(host: string, signal: AbortSignal): Promise<RunsRead> {
  try {
    const client = createClient(ViewerService, createServerTransport(host, getLiveToken()));
    const resp = await client.listOutputs({}, { signal: deadline(signal) });
    return { kind: "ok", runs: wireDescriptors(resp.outputs) };
  } catch (e) {
    return { kind: "unreadable", detail: why(e) };
  }
}
