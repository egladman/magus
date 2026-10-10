// attention.ts - the "needs attention" hero: the first thing the eye lands on.
//
// Its headline is the ATTENTION QUEUE - the durable list of blocks agents have raised that are
// waiting on a person, read from GET /api/v1/attention, the same queue `magus session attention`
// lists and `magus session dispose` closes from any worktree of this repo.
//
// The verdict comes from the queue and only from the queue. It used to derive from the live run
// counts instead, and the two disagreed in both directions: "All clear" while three agents sat
// blocked, or "Attention needed" over a queue nobody had to touch.
//
// The failing/running/queued counts stay, as FACTS about live runs rather than as a verdict:
// they carry the deep links into the failing run's log and the rerun commands, and a failure is
// still worth seeing next to the queue. They just no longer decide what the headline says. The
// headline therefore never takes the success colour while a failing count shows beside it.
//
// This is deliberately NOT a collapsible Card: the summary is always visible - it is the
// board's headline, not a foldable panel.

import type { DashboardState, StatusView } from "../state";
import { ALL_WORKSPACES, onWorkspaceScope, workspaceScope } from "../../../lib/scope";
import { h, helpGlyph, prose, type Tile } from "./card";
import { logsLink } from "../../../lib/server";
import { showToast } from "../../../lib/refresh-toast";
import { reportFailure } from "../../../lib/notifications";
import { menuToggle } from "../../../ui/menu-toggle";
import { statusIcon, statusText, type Status } from "../../../ui/status";
import { menuButton, type MenuAction, type MenuButton } from "./menu";
import {
  ageLabel,
  disposeAttention,
  disposeStartsHidden,
  firstLine,
  loadAttention,
  subjectLine,
  type AttentionRead,
  type AttentionRequest,
} from "./attentionQueue";

// The poll cadence and request budget the jobs tile reads on. Same numbers on
// purpose: both tiles poll a small JSON route on the same server, and two boards refreshing at
// two rhythms would make one of them look stuck.
const REFRESH_MS = 4_000;
const REQUEST_TIMEOUT_MS = 10_000;

// QUEUE_LIST_MAX caps the rows drawn, with a residual count, for the reason every other list on
// this board is capped: a hook that fires on every prompt can raise a handful of blocks at once,
// and thirty rows would push the headline itself off a Big Picture panel. Nothing is hidden -
// the residual line says how many more, and `magus session attention` lists them all.
const QUEUE_LIST_MAX = 5;

// firstFailedInv returns the invocation id of the earliest run carrying a failed target,
// so the failing count can deep-link into the run whose log an operator needs. Exported for the
// tests beside this file.
export function firstFailedInv(status: StatusView): string {
  for (const run of status.runs) {
    if (run.targets.some((t) => t.state === "failed")) return run.inv;
  }
  return "";
}

export function countFailing(status: StatusView): number {
  let n = 0;
  for (const run of status.runs) {
    for (const t of run.targets) if (t.state === "failed") n++;
  }
  return n;
}

// FailedTarget is one failing target and the handle needed to open it. outputRef is what the log
// viewer resolves to the captured output; inv is the fallback when a target failed without leaving
// a ref (it opens the whole run instead of nothing).
export interface FailedTarget {
  label: string;
  inv: string;
  outputRef: string;
}

// failingTargets lists what is actually broken, rather than only how many things are: every one of
// the hops from "2 targets are failing" to the log is answerable from the status frame the hero is
// already rendering, since the run carries each target's label and its output ref.
export function failingTargets(status: StatusView): FailedTarget[] {
  const out: FailedTarget[] = [];
  for (const run of status.runs) {
    for (const t of run.targets) {
      if (t.state !== "failed") continue;
      out.push({ label: t.label || t.target || "-", inv: run.inv, outputRef: t.outputRef });
    }
  }
  return out;
}

export interface Verdict {
  state: "clear" | "warn" | "attention";
  line: string;
  sub: string;
}

// attentionVerdict derives the headline from the ATTENTION QUEUE, and from nothing else.
//
// Every other signal on this board - failing targets, server health, pool depth - is something
// magus observed; a request is something a person was asked for and has not yet given. Folding an
// observation in would let the headline say "Attention needed" over an empty queue, and a reader
// who clears that twice stops reading the one line on the board that means somebody is waiting.
//
// A server with no attention route, and a server that could not be read, both mean the queue is
// UNKNOWN, and each gets its own verdict: rendering unknown as "no open requests" is
// indistinguishable from the good state.
export function attentionVerdict(read: AttentionRead, nowMs: number = Date.now()): Verdict {
  if (read.kind === "absent") {
    return {
      state: "warn",
      line: "Queue unavailable",
      sub:
        "This server does not serve an attention queue, so nothing here can say whether" +
        " anyone is blocked.",
    };
  }
  if (read.kind === "unreadable") {
    return {
      state: "warn",
      line: "Queue unreadable",
      sub: "The attention queue could not be read, so this is not a report that nobody is waiting.",
    };
  }

  const n = read.requests.length;
  if (n === 0) {
    return {
      state: "clear",
      line: "Nobody waiting",
      sub:
        "No agent is blocked on a person. Requests arrive through `magus session notify` and" +
        " close only when someone disposes of them.",
    };
  }
  const oldest = read.requests[0];
  const age = ageLabel(oldest.opened_ms, nowMs);
  return {
    state: "attention",
    line: n === 1 ? "1 request waiting" : n + " requests waiting",
    sub:
      "Blocked on a person, oldest first" +
      (age ? ", waiting " + age : "") +
      ". Nothing closes a request on its own.",
  };
}

// verdictStatus is the shape half of the headline. A calm queue beside a failing count takes the
// neutral mark, not the success one: "nobody is waiting" is true and is not good news while
// something is failing, and green there contradicts the red count next to it.
export function verdictStatus(state: Verdict["state"], failing: number): Status {
  if (state === "attention") return "danger";
  if (state === "warn") return "warning";
  return failing > 0 ? "neutral" : "success";
}

// reproduceCommand is what you would type to run this failing target again yourself.
//
// Opening the log is only half of what someone does with a failure; the other half is reproducing
// it locally, and magus's own failure output already ends with a `reproduce:` line for exactly that
// reason.
//
// A root-project target has no project argument: `magus run lint` is correct and `magus run lint .`
// is noise, so the label is only appended when there is one.
export function reproduceCommand(label: string): string {
  const sep = label.lastIndexOf(":");
  if (sep <= 0) return "magus run " + label;
  const project = label.slice(0, sep);
  const target = label.slice(sep + 1);
  if (!target) return "magus run " + label;
  return project === "." ? "magus run " + target : "magus run " + target + " " + project;
}

// inspectCommand fetches a failure's full captured output by ref, which is the low-token way to
// read it - the same instruction the CLI prints next to a failure. Empty when the target left no
// ref (it failed before producing one), in which case the menu simply omits the entry rather than
// offering a command that cannot work.
export function inspectCommand(outputRef: string): string {
  return outputRef ? "magus query output " + outputRef : "";
}

// copy puts text on the clipboard and reports whether it went. Best-effort: an insecure origin or a
// denied permission means no clipboard, and the caller says so rather than silently appearing to
// have worked.
function copy(text: string): Promise<boolean> {
  const clip = navigator.clipboard;
  if (!clip || typeof clip.writeText !== "function") return Promise.resolve(false);
  return clip.writeText(text).then(
    () => true,
    () => false,
  );
}

function copyAction(label: string, command: string): MenuAction {
  return {
    label,
    run() {
      void copy(command).then((ok) => {
        showToast(
          "Dashboard",
          ok ? "Copied: " + command : "Could not access the clipboard.",
          ok ? "ok" : "error",
        );
      });
    },
  };
}

// failChip builds one failing-target chip and the menu behind it.
//
// A MENU rather than a plain link, because there are two genuinely different things to do with a
// failure and picking one for the reader would be guessing: read the output here, or reproduce it
// in a terminal. The chip is a <button> with a popup, not an <a>: its primary action is "offer the
// choices", and dressing that as a link would promise navigation it does not perform.
function failChip(f: FailedTarget, href: string): MenuButton {
  const btn = menuToggle({
    variant: "secondary",
    small: true,
    danger: true,
    icon: statusIcon("danger"),
    text: f.label,
    classes: "console-dashboard-hero__failbtn",
  });
  btn.querySelector(".pf-v6-c-menu-toggle__text")?.prepend(statusText("danger", "Failed: "));

  const actions: MenuAction[] = [];
  if (href) {
    actions.push({
      label: "Open in log viewer",
      // A new tab: on a shared or fullscreened board, navigating away is not something the next
      // person can undo.
      run: () => window.open(href, "_blank", "noopener"),
    });
  }
  actions.push(copyAction("Copy rerun command", reproduceCommand(f.label)));
  const inspect = inspectCommand(f.outputRef);
  if (inspect) actions.push(copyAction("Copy output-ref command", inspect));
  return menuButton(btn, actions);
}

export function attentionTile(): Tile {
  const root = h("section", "console-dashboard-hero");
  root.setAttribute("aria-label", "Needs attention");

  const headline = h("div", "console-dashboard-hero__headline");
  const verdict = h("h3", "console-dashboard-hero__verdict");
  const verdictMark = h("span", "console-dashboard-hero__mark");
  // The text is its own node: renderQueue rewrites it on every read, and writing textContent on the
  // heading would take the mark with it.
  const verdictText = h("span", "console-dashboard-hero__verdicttext");
  verdict.append(verdictMark, verdictText);
  // The glyph is a SIBLING of the verdict, never a child of it, for the same reason.
  const verdictRow = h("div", "console-dashboard-hero__verdictrow");
  verdictRow.append(
    verdict,
    helpGlyph(
      "This headline reads the attention queue: blocks an agent raised that are waiting on a" +
        " person, the same queue `magus session attention` lists. Nothing closes a request but" +
        " you disposing of it. The failing/running/queued counts below are live run activity and" +
        " do NOT feed this verdict. A failing target is not a request, and an empty queue over a" +
        " red build is both facts being true at once.",
      "this verdict",
    ),
  );
  const detail = h("p", "console-dashboard-hero__detail");
  // The first paint, before any read has landed. No data-state is set with it, so the hero keeps
  // its neutral accent rule: the queue is UNKNOWN at this moment, and both the calm colour and
  // the loud one would be claims the tile has no basis for yet.
  verdictText.textContent = "Reading the queue";
  verdictMark.append(statusIcon("neutral"));
  detail.textContent = "Blocks agents raised that are waiting on a person.";
  headline.append(verdictRow, detail);

  const counts = h("div", "console-dashboard-hero__counts");
  const metrics = h("div", "console-dashboard-hero__metrics");
  // The failing metric is an <a> so it can deep-link to the failing run's log in live mode;
  // it degrades to a plain block (via a swapped node) when there is nothing to link to.
  const failWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__fail");
  const failLink = h("a", "console-dashboard-hero__metriclink");
  const failN = h("span", "console-dashboard-hero__n", "0");
  const failL = h("span", "console-dashboard-hero__l", "failing");
  failLink.append(failN, failL);
  failWrap.append(failLink);

  // Running is a link to the live log, the same destination the live-activity tile offers. One link
  // to the stream, NOT a chip per running target: the activity tile already lists those with their
  // own deep links.
  const runWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__run");
  const runLink = h("a", "console-dashboard-hero__metriclink");
  const runN = h("span", "console-dashboard-hero__n", "0");
  const runHint = h("span", "pf-v6-screen-reader", " (opens the live log viewer)");
  runHint.hidden = true;
  runLink.append(runN, h("span", "console-dashboard-hero__l", "running"), runHint);
  runWrap.append(runLink);

  // Queued gets NO link: a queued target has not started, so it has no output to open and no run to
  // join. What it gets instead is the answer to the question the number raises, which is not
  // "where" but "why is anything waiting", read as text under the counts.
  const queueWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__queue");
  const queueN = h("span", "console-dashboard-hero__n", "0");
  queueWrap.append(queueN, h("span", "console-dashboard-hero__l", "queued"));

  metrics.append(failWrap, runWrap, queueWrap);
  const reason = h("p", "console-dashboard-hero__reason");
  reason.hidden = true;
  counts.append(metrics, reason);

  // The attention queue itself: one row per open request, straight under the verdict it is the
  // subject of. Above the failing-target list because it outranks it - a failing target is work
  // magus can tell you about, a request is work waiting on YOU.
  const queueList = h("ul", "console-dashboard-attention__list");
  queueList.hidden = true;
  // Announced by its own region, which changes only when the COUNT changes: the list is rebuilt from
  // every read, and a live region wrapped around it would re-read every row on every poll.
  const announce = h("p", "pf-v6-screen-reader");
  announce.setAttribute("role", "status");
  const queueNote = h("p", "console-dashboard-attention__note");
  queueNote.hidden = true;
  headline.append(announce, queueList, queueNote);

  const FAIL_LIST_MAX = 6;
  const failList = h("ul", "console-dashboard-hero__faillist");
  failList.setAttribute("aria-label", "Failing targets");
  failList.dataset.controlSize = "compact";
  failList.hidden = true;
  // Inside the HEADLINE, not a third sibling of it: it is the detail line's answer ("1 target is
  // failing" -> "apps/admin:e2e"), not a fourth metric.
  headline.append(failList);
  root.append(headline, counts);

  let failing = 0;
  let verdictState: Verdict["state"] | null = null;
  let paintedMark = "";

  // paintMark redraws the headline's mark when its status changes. It depends on both the queue
  // (renderQueue) and the failing count (render), which arrive on different clocks.
  function paintMark(): void {
    if (!verdictState) return;
    const status = verdictStatus(verdictState, failing);
    if (status === paintedMark) return;
    paintedMark = status;
    verdictMark.replaceChildren(statusIcon(status));
  }

  function render(status: StatusView, liveHost: string | null, demo: boolean): void {
    failing = countFailing(status);
    const running = status.pool.running;
    const queued = status.pool.queued;

    failN.textContent = String(failing);
    runN.textContent = String(running);
    queueN.textContent = String(queued);
    // data-n gates each count's color: quiet at zero, loud when there is something to show.
    failWrap.dataset.n = failing > 0 ? "some" : "none";
    runWrap.dataset.n = running > 0 ? "some" : "none";
    queueWrap.dataset.n = queued > 0 ? "some" : "none";
    root.dataset.failing = failing > 0 ? "some" : "none";
    paintMark();

    // Whose the counts are, and why work is waiting, said as text under them. The verdict's sub-line
    // already says whose these counts are; people read the three numbers first and the sentence
    // under them second, and it is the numbers that look like they contradict a scoped tile.
    const scoped = workspaceScope() !== ALL_WORKSPACES;
    const lines = [queuedReason(status, queued)];
    if (scoped) lines.push("Pool counts are server-wide: one pool serves every workspace.");
    reason.textContent = lines.filter(Boolean).join(" ");
    reason.hidden = reason.textContent === "";

    // Wire the failing count into the failing run's log when we are live and have an inv.
    const inv = failing > 0 ? firstFailedInv(status) : "";
    if (failing > 0 && liveHost && inv) {
      failLink.setAttribute("href", logsLink(liveHost, { inv }));
      failWrap.dataset.linked = "true";
    } else {
      failLink.removeAttribute("href");
      delete failWrap.dataset.linked;
    }

    // Running -> the live log stream. Demo keeps the showcase self-consistent (../logs/#demo), the
    // same fallback the live-activity tile and the failing chips use.
    const runHref = liveHost ? logsLink(liveHost, {}) : demo ? "../logs/#demo" : "";
    if (running > 0 && runHref) {
      runLink.setAttribute("href", runHref);
      runWrap.dataset.linked = "true";
      runHint.hidden = false;
    } else {
      runLink.removeAttribute("href");
      delete runWrap.dataset.linked;
      runHint.hidden = true;
    }

    renderFailList(status, liveHost, demo);
  }

  // queuedReason says WHY work is waiting, from the same status frame. Capacity 0 means an
  // unlimited pool, where saturation is not a possible explanation. Empty when nothing waits.
  function queuedReason(status: StatusView, queued: number): string {
    if (queued === 0) return "";
    const saturated = status.pool.capacity > 0 && status.pool.running >= status.pool.capacity;
    if (saturated) {
      return (
        queued +
        " waiting: every one of the pool's " +
        status.pool.capacity +
        " slots is busy. They start as slots free up."
      );
    }
    return queued + " waiting to start.";
  }

  // The chips are rebuilt only when the set of failures changes. A frame arrives about once a second,
  // and rebuilding on each would close a menu the reader had just opened.
  let chips: MenuButton[] = [];
  let failSignature = "";

  // renderFailList names each failing target and links it to its own captured output.
  //
  // A target that failed WITHOUT an output ref still gets a row - it falls back to opening its run.
  // Dropping it would make the count say three and the list show two, with no way to tell which one
  // the reader was not being shown.
  function renderFailList(status: StatusView, liveHost: string | null, demo: boolean): void {
    const failures = failingTargets(status);
    const hrefFor = (f: FailedTarget): string =>
      liveHost
        ? f.outputRef
          ? logsLink(liveHost, { ref: f.outputRef })
          : f.inv
            ? logsLink(liveHost, { inv: f.inv })
            : ""
        : demo
          ? "../logs/#demo"
          : "";
    const signature = failures.map((f) => f.label + "|" + hrefFor(f)).join("\n");
    if (signature === failSignature) return;
    failSignature = signature;
    for (const chip of chips) chip.dispose();
    chips = [];
    failList.hidden = failures.length === 0;
    if (failures.length === 0) {
      failList.replaceChildren();
      return;
    }
    const shown = failures.slice(0, FAIL_LIST_MAX);
    const rows: HTMLElement[] = shown.map((f) => {
      const li = h("li", "console-dashboard-hero__failrow");
      const chip = failChip(f, hrefFor(f));
      chips.push(chip);
      li.append(chip.el);
      return li;
    });
    if (failures.length > shown.length) {
      rows.push(
        h(
          "li",
          "console-dashboard-hero__failmore",
          "+" + (failures.length - shown.length) + " more",
        ),
      );
    }
    failList.replaceChildren(...rows);
  }

  // ---- the queue ------------------------------------------------------------

  let host = "";
  let lastRead = 0;
  let reading = false;
  let controller: AbortController | null = null;
  let request = 0;
  // torndown, not "disposed": this tile has a domain meaning for that word - closing a request -
  // and a teardown flag wearing it would read as one at every call site.
  let torndown = false;
  let visible = true;
  let announced = -1;

  // requestRow draws one open request: what to close, how long it has waited, what kind of block
  // it is, the paths the event named, and the first line of what the agent said.
  //
  // The id is shown in full and in mono, because it is the handle for the OTHER app: a
  // person reading this tile on a shared screen closes the request from their terminal with
  // `magus session dispose <id>`, and a truncated id cannot be typed.
  //
  // A permission's close control stays off the row until it is opened. The paths are the
  // subject and the agent's message is the caption; opening shows the full text before
  // offering to close it. Waiting is a bookmark the person just answered in the host,
  // so its control is there from the start.
  function requestRow(req: AttentionRequest, nowMs: number, opened = false): HTMLElement {
    const li = h("li", "console-dashboard-attention__row");
    li.dataset.outcome = req.outcome;
    li.dataset.requestId = req.id;
    if (opened) li.dataset.open = "true";

    const head = h("div", "console-dashboard-attention__head");
    head.append(h("code", "console-dashboard-attention__id", req.id));

    head.append(h("span", "console-dashboard-attention__age", ageLabel(req.opened_ms, nowMs)));
    if (req.outcome) {
      const outcome = h("span", "pf-v6-c-label pf-m-compact console-dashboard-attention__outcome");
      outcome.append(h("span", "pf-v6-c-label__content", req.outcome));
      head.append(outcome);
    }
    // Which slice of a fleet's work is blocked. Absent for anything a person started by hand,
    // which is the ordinary case and not worth a placeholder.
    if (req.lease) {
      head.append(h("code", "console-dashboard-attention__lease", req.lease));
    }
    if (disposeStartsHidden(req.outcome) && !opened) {
      head.append(openControl(li, req));
    } else {
      head.append(disposeControl(req));
    }
    li.append(head);

    const subject = subjectLine(req.files);
    if (subject) li.append(h("p", "console-dashboard-attention__subject", subject));

    const summary = firstLine(req.message);
    const message = h("p", "console-dashboard-attention__message", opened ? req.message : summary);
    li.append(message);
    // The full text for a message the row had to shorten, as a control the reader can reach rather
    // than a tooltip: the summary line says what it is and this opens the rest.
    if (req.message && req.message !== summary && !disposeStartsHidden(req.outcome)) {
      li.append(moreControl(li, message, req.message, summary));
    }
    li.append(h("code", "console-dashboard-attention__command", "magus session dispose " + req.id));
    return li;
  }

  function moreControl(
    li: HTMLElement,
    message: HTMLElement,
    full: string,
    summary: string,
  ): HTMLElement {
    const btn = h(
      "button",
      "pf-v6-c-button pf-m-link pf-m-inline console-dashboard-attention__more",
    );
    btn.type = "button";
    const label = h("span", "pf-v6-c-button__text", "Show full message");
    btn.append(label);
    btn.setAttribute("aria-expanded", "false");
    btn.addEventListener("click", () => {
      const open = li.dataset.open !== "true";
      if (open) li.dataset.open = "true";
      else delete li.dataset.open;
      message.textContent = open ? full : summary;
      label.textContent = open ? "Show less" : "Show full message";
      btn.setAttribute("aria-expanded", String(open));
    });
    return btn;
  }

  // openControl is the look a permission asks for before it can be closed. Opening
  // expands the subject and message and replaces this button with the close composer.
  function openControl(li: HTMLElement, req: AttentionRequest): HTMLElement {
    const wrap = h("span", "console-dashboard-attention__dispose");
    const btn = h(
      "button",
      "pf-v6-c-button pf-m-link pf-m-inline console-dashboard-attention__openbtn",
    );
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-button__text", "Review request"));
    btn.setAttribute("aria-label", "Review request " + req.id + " before closing it");
    btn.addEventListener("click", () => {
      li.dataset.open = "true";
      const message = li.querySelector(".console-dashboard-attention__message");
      if (message instanceof HTMLElement) message.textContent = req.message;
      const dispose = disposeControl(req);
      wrap.replaceWith(dispose);
      dispose.querySelector<HTMLButtonElement>(".console-dashboard-attention__disposebtn")?.focus();
    });
    wrap.append(btn);
    return wrap;
  }

  // disposeControl is the button and the one-line reason composer behind it.
  //
  // A composer rather than a prompt(): the diff app settled this for the same act. A
  // prompt() steals focus from the page, cannot show WHICH request is being closed, and hides
  // the message the reason is about while it is being typed. The composer sits in the row.
  //
  // Closing a request is the one write this tile makes, and it happens only here - one click,
  // one reason, one id. There is deliberately no "close all": a queue cleared without being
  // read is the failure mode the queue exists to prevent.
  function disposeControl(req: AttentionRequest): HTMLElement {
    const wrap = h("span", "console-dashboard-attention__dispose");
    const btn = h(
      "button",
      "pf-v6-c-button pf-m-link pf-m-inline console-dashboard-attention__disposebtn",
    );
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-button__text", "Close request"));
    btn.setAttribute("aria-label", "Close request " + req.id);
    btn.setAttribute("aria-expanded", "false");
    wrap.append(btn);

    // Cancelling hands focus back to the control that opened the composer; the composer removing
    // itself would otherwise drop it on the page.
    const close = (): void => {
      wrap.querySelector(".console-dashboard-attention__composer")?.remove();
      btn.hidden = false;
      btn.setAttribute("aria-expanded", "false");
      btn.focus();
    };

    btn.addEventListener("click", () => {
      const box = h("span", "console-dashboard-attention__composer");
      const inputWrap = h("span", "pf-v6-c-form-control");
      const input = h("input", "pf-v6-c-form-control__text");
      input.type = "text";
      input.id = "console-attention-reason-" + req.id;
      input.setAttribute("aria-label", "Why request " + req.id + " is being closed");
      const helper = h("span", "pf-v6-c-helper-text");
      const helperItem = h("span", "pf-v6-c-helper-text__item");
      helperItem.append(
        h(
          "span",
          "pf-v6-c-helper-text__item-text",
          "Reason is optional. Enter closes the request, Escape cancels.",
        ),
      );
      helper.append(helperItem);
      helper.id = input.id + "-help";
      input.setAttribute("aria-describedby", helper.id);
      input.placeholder = "Reason";
      input.addEventListener("keydown", (e) => {
        e.stopPropagation(); // the board's single-letter keys must not fire mid-sentence
        if (e.key === "Escape") {
          e.preventDefault();
          close();
          return;
        }
        if (e.key !== "Enter") return;
        e.preventDefault();
        const reason = input.value.trim();
        close();
        void send(req, reason);
      });
      inputWrap.append(input);
      box.append(inputWrap, helper);
      btn.hidden = true;
      btn.setAttribute("aria-expanded", "true");
      wrap.append(box);
      input.focus();
    });
    return wrap;
  }

  // send posts the closure and says what came back. A REFUSAL (unknown id, ambiguous prefix,
  // already closed by somebody else) is reported in the server's own words rather than as a
  // failure: each one is the server working, and each names what the person does next.
  async function send(req: AttentionRequest, reason: string): Promise<void> {
    if (!host) {
      showToast("Attention", "Not connected to a server, so nothing can be closed.", "error");
      return;
    }
    const res = await disposeAttention(host, req.id, reason);
    if (torndown) return;
    if (res.kind === "ok") {
      showToast("Attention", "Closed request " + req.id + ".", "ok");
      // Re-read rather than dropping the row locally: another worktree may have closed
      // something else in the meantime, and the store is what every app agrees on.
      lastRead = 0;
      refresh();
      return;
    }
    showToast("Attention", res.detail, "error");
    lastRead = 0;
    refresh();
  }

  // The rows' own content, minus the age that moves on its own: the list is rebuilt when this
  // changes and the age is written in place, so a poll that returns the same queue leaves the
  // reader's focus and an open composer alone.
  let rowsSignature = "";

  // renderQueue paints the headline and the rows from one read. Every non-ok read gets its own
  // words - see attentionVerdict - so an unknown queue never renders as a calm one.
  function renderQueue(read: AttentionRead): void {
    const v = attentionVerdict(read);
    root.dataset.state = v.state;
    verdictState = v.state;
    verdictText.textContent = v.line;
    prose(detail, v.sub);
    paintMark();

    const count = read.kind === "ok" ? read.requests.length : -1;
    if (count !== announced) {
      announced = count;
      announce.textContent = count >= 0 ? v.line : "";
    }

    if (read.kind !== "ok" || read.requests.length === 0) {
      rowsSignature = "";
      queueList.hidden = true;
      queueList.replaceChildren();
      // The store path on the empty state, so a reader who expected a request knows which queue
      // was actually consulted - two worktrees of one repo share it, and a wrong root is the
      // failure that looks exactly like a quiet day.
      const store = read.kind === "ok" ? read.store : "";
      queueNote.textContent = store ? "Queue: " + store : "";
      queueNote.hidden = !queueNote.textContent;
      return;
    }

    const now = Date.now();
    const shown = read.requests.slice(0, QUEUE_LIST_MAX);
    const signature =
      shown.map((r) => [r.id, r.outcome, r.lease, r.message, r.files.length].join("|")).join("\n") +
      "#" +
      read.requests.length;
    queueNote.hidden = true;
    queueNote.textContent = "";
    queueList.hidden = false;
    if (signature === rowsSignature) {
      const byId = new Map(shown.map((r) => [r.id, r]));
      for (const row of queueList.querySelectorAll<HTMLElement>(
        ".console-dashboard-attention__row",
      )) {
        const req = byId.get(row.dataset.requestId ?? "");
        const age = row.querySelector(".console-dashboard-attention__age");
        if (req && age) age.textContent = ageLabel(req.opened_ms, now);
      }
      return;
    }
    rowsSignature = signature;
    const openedIDs = new Set(
      Array.from(
        queueList.querySelectorAll<HTMLElement>(".console-dashboard-attention__row[data-open]"),
      )
        .map((row) => row.dataset.requestId)
        .filter((id): id is string => id !== undefined),
    );
    const rows = shown.map((req) => requestRow(req, now, openedIDs.has(req.id)));
    if (read.requests.length > shown.length) {
      const rest = h("li", "console-dashboard-attention__rest");
      prose(
        rest,
        "+" +
          (read.requests.length - shown.length) +
          " more; `magus session attention` lists the whole queue",
      );
      rows.push(rest);
    }
    queueList.replaceChildren(...rows);
  }

  const refresh = (): void => {
    if (!visible || !host || reading) return;
    reading = true;
    lastRead = Date.now();
    const current = ++request;
    controller = new AbortController();
    let timedOut = false;
    const timeout = window.setTimeout(() => {
      if (current !== request) return;
      timedOut = true;
      controller?.abort();
    }, REQUEST_TIMEOUT_MS);
    void loadAttention(host, controller.signal)
      .then((read) => {
        // A composer the reader is typing into must survive a poll.
        if (torndown || current !== request) return;
        // An abort is silent in the transport, since a superseded read is not a failure; this one
        // is the deadline, so it is reported here.
        if (timedOut) {
          reportFailure(
            "Attention",
            "The attention queue did not answer within " + REQUEST_TIMEOUT_MS / 1000 + "s.",
            "attention:timeout",
          );
        }
        if (queueList.querySelector(".console-dashboard-attention__composer")) return;
        renderQueue(read);
      })
      .finally(() => {
        window.clearTimeout(timeout);
        if (current === request) reading = false;
      });
  };
  const interval = window.setInterval(refresh, REFRESH_MS);

  // The counts name whose they are, so they repaint when the tab's scope changes and not only
  // when the server pushes a frame.
  let latest: DashboardState | null = null;
  const repaint = (): void => {
    if (latest?.status) render(latest.status, latest.liveHost, latest.conn.state === "demo");
  };
  const offScope = onWorkspaceScope(repaint);

  return {
    el: root,
    update(s: DashboardState) {
      latest = s;
      repaint();

      if (s.conn.state === "demo") {
        // The demo shows the queue's SHAPE without inventing a block. A fabricated request
        // would put a close control on the showcase that closes nothing, and a reader who
        // presses it learns the wrong thing about the one control on this board that writes.
        host = "";
        request++;
        reading = false;
        controller?.abort();
        if (visible) renderQueue({ kind: "ok", requests: [], store: "" });
        return;
      }
      const nextHost = s.liveHost ?? "";
      if (nextHost !== host) {
        host = nextHost;
        lastRead = 0;
        request++;
        reading = false;
        controller?.abort();
      }
      if (!host) {
        // No server, so the queue genuinely cannot be read. Said out loud rather than left at
        // whatever the last connected read showed: a stale "nobody waiting" outlives the
        // connection that earned it, and this is the tile where that reads as reassurance.
        renderQueue({ kind: "unreadable", detail: "not connected to a server" });
        return;
      }
      if (visible && Date.now() - lastRead >= REFRESH_MS) refresh();
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
      torndown = true;
      request++;
      controller?.abort();
      window.clearInterval(interval);
      offScope();
      for (const chip of chips) chip.dispose();
      chips = [];
    },
  };
}
