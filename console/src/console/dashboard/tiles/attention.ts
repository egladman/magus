// attention.ts - the "needs attention" hero: the first thing the eye lands on.
//
// Its headline is the ATTENTION QUEUE - the durable list of blocks agents have raised that are
// waiting on a person, read from GET /api/v1/attention, the same queue `magus session attention`
// lists and `magus session dispose` closes from any worktree of this repo.
//
// It used to derive that headline from the live run counts instead: failing targets shouted
// "Attention needed", otherwise "All clear". That verdict predates the queue and can now
// DISAGREE with it in both directions - a board reading "All clear" while three agents sit
// blocked, or "Attention needed" over a queue nobody has to touch. Two things called attention
// on one screen, and the reader has to work out which is lying. So the verdict comes from the
// queue now, and only from the queue.
//
// The failing/running/queued counts stay, as FACTS about live runs rather than as a verdict:
// they carry the deep links into the failing run's log and the rerun commands, and a failure is
// still worth seeing next to the queue. They just no longer decide what the headline says.
//
// This is deliberately NOT a collapsible Card: the summary is always visible - it is the
// board's headline, not a foldable panel.

import type { DashboardState, StatusView } from "../state";
import { ALL_WORKSPACES, onWorkspaceScope, workspaceScope } from "../../../lib/scope";
import { h, helpGlyph, type Tile } from "./card";
import { logsLink } from "../../../lib/daemon";
import { showToast } from "../../../lib/refresh-toast";
import {
  ageLabel,
  disposeAttention,
  firstLine,
  loadAttention,
  type AttentionRead,
  type AttentionRequest,
} from "./attentionQueue";

// The poll cadence and request budget the delegation tile reads its ledger on. Same numbers on
// purpose: both tiles poll a small JSON route on the same daemon, and two boards refreshing at
// two rhythms would make one of them look stuck.
const REFRESH_MS = 4_000;
const REQUEST_TIMEOUT_MS = 10_000;

// QUEUE_LIST_MAX caps the rows drawn, with a residual count, for the reason every other list on
// this board is capped: a hook that fires on every prompt can raise a handful of blocks at once,
// and thirty rows would push the headline itself off a Big Picture panel. Nothing is hidden -
// the residual line says how many more, and `magus session attention` lists them all.
const QUEUE_LIST_MAX = 5;

// One document-level listener closes any open chip menu on an outside click, rather than one per
// chip: the list is rebuilt on every status frame, so per-chip listeners would accumulate against
// detached nodes about once a second.
if (typeof document !== "undefined") {
  document.addEventListener("click", () => {
    for (const m of document.querySelectorAll<HTMLElement>(".console-dashboard-hero__failmenu"))
      m.hidden = true;
    for (const b of document.querySelectorAll(".console-dashboard-hero__failbtn"))
      b.setAttribute("aria-expanded", "false");
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key !== "Escape") return;
    for (const m of document.querySelectorAll<HTMLElement>(".console-dashboard-hero__failmenu"))
      m.hidden = true;
    for (const b of document.querySelectorAll(".console-dashboard-hero__failbtn"))
      b.setAttribute("aria-expanded", "false");
  });
}

// firstFailedInv returns the invocation id of the earliest run carrying a failed target,
// so the failing count can deep-link into the run whose log an operator needs. Exported for the
// tests beside this file; the Big Picture tile this comment used to name as the other consumer
// derives its own headline and imports nothing from here.
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

// failingTargets lists what is actually broken, rather than only how many things are.
//
// The hero said "2 targets are failing" and stopped there, which tells an operator that they have a
// problem and nothing whatsoever about it - they then went to the timeline to find out WHICH, and
// to the log viewer to find out why. Every one of those hops is already answerable from the status
// frame the hero is rendering: the run carries each target's label and its output ref.
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

// verdictFor derives the headline from the ATTENTION QUEUE, and from nothing else.
//
// The queue is the only source here on purpose. Every other signal on this board - failing
// targets, daemon health, pool depth - is something magus observed; a request is something a
// person was asked for and has not yet given. Folding an observation in would let the headline
// say "Attention needed" over an empty queue, and a reader who clears that twice stops reading
// the one line on the board that means somebody is waiting on them.
//
// The three reads that are not "ok" get their own verdicts rather than being flattened into a
// calm one: a daemon with no attention route, and a daemon that could not be read, both mean
// the queue is UNKNOWN. Rendering unknown as "no open requests" is the single worst thing this
// tile could do, because it is indistinguishable from the good state.
export function verdictFor(read: AttentionRead, nowMs: number = Date.now()): Verdict {
  if (read.kind === "absent") {
    return {
      state: "warn",
      line: "Queue unavailable",
      sub:
        "This daemon does not serve an attention queue, so nothing here can say whether" +
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

// reproduceCommand is what you would type to run this failing target again yourself.
//
// It matters because opening the log is only half of what someone does with a failure. The other
// half is reproducing it locally, and magus's own failure output already ends with a `reproduce:`
// line for exactly that reason - the dashboard offering the log but not the command would make the
// board strictly less useful than the terminal it is meant to save you a trip to.
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

// failChip builds one failing-target chip and the menu behind it.
//
// A MENU rather than a plain link, because there are two genuinely different things to do with a
// failure and picking one for the reader would be guessing: read the output here, or reproduce it
// in a terminal. Both are one action away from a name that was previously not even shown.
//
// The chip is a <button> with aria-haspopup, not an <a>: its primary action is "offer the choices",
// and dressing that as a link would promise navigation it does not perform.
function failChip(f: FailedTarget, href: string): HTMLElement {
  const wrap = h("div", "console-dashboard-hero__failchip");

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "console-dashboard-hero__failbtn";
  btn.textContent = f.label;
  btn.setAttribute("aria-haspopup", "menu");
  btn.setAttribute("aria-expanded", "false");
  btn.title = f.label + " failed. Open its output or copy the command to rerun it";

  const menu = h("div", "pf-v6-c-menu console-dashboard-hero__failmenu");
  menu.hidden = true;
  menu.setAttribute("role", "menu");
  const list = h("ul", "pf-v6-c-menu__list");
  menu.append(h("div", "pf-v6-c-menu__content", "") as HTMLElement);
  menu.querySelector(".pf-v6-c-menu__content")?.append(list);

  const close = (): void => {
    menu.hidden = true;
    btn.setAttribute("aria-expanded", "false");
  };

  function item(label: string, run: () => void): HTMLElement {
    const li = h("li", "pf-v6-c-menu__list-item");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-menu__item";
    b.setAttribute("role", "menuitem");
    b.append(h("span", "pf-v6-c-menu__item-main", label));
    b.addEventListener("click", () => {
      run();
      close();
    });
    li.append(b);
    return li;
  }

  if (href) {
    list.append(
      item("Open in log viewer", () => {
        // A new tab: on a shared or fullscreened board, navigating away is not something the next
        // person can undo.
        window.open(href, "_blank", "noopener");
      }),
    );
  }
  const rerun = reproduceCommand(f.label);
  list.append(
    item("Copy rerun command", () => {
      void copy(rerun).then((ok) => {
        showToast(
          "Dashboard",
          ok ? "Copied: " + rerun : "Could not access the clipboard.",
          ok ? "ok" : "error",
        );
      });
    }),
  );
  const inspect = inspectCommand(f.outputRef);
  if (inspect) {
    list.append(
      item("Copy output-ref command", () => {
        void copy(inspect).then((ok) => {
          showToast(
            "Dashboard",
            ok ? "Copied: " + inspect : "Could not access the clipboard.",
            ok ? "ok" : "error",
          );
        });
      }),
    );
  }

  btn.addEventListener("click", (ev) => {
    ev.stopPropagation(); // or the document handler below closes it in the same tick
    const open = menu.hidden;
    // Only one chip's menu at a time: two open menus overlapping is unreadable, and the second
    // would be positioned over the first.
    for (const other of document.querySelectorAll<HTMLElement>(".console-dashboard-hero__failmenu"))
      other.hidden = true;
    for (const other of document.querySelectorAll(".console-dashboard-hero__failbtn"))
      other.setAttribute("aria-expanded", "false");
    menu.hidden = !open;
    btn.setAttribute("aria-expanded", String(open));
  });
  menu.addEventListener("click", (ev) => ev.stopPropagation());
  wrap.append(btn, menu);
  return wrap;
}

export function attentionTile(): Tile {
  const root = h("section", "console-dashboard-hero");
  root.setAttribute("aria-label", "Needs attention");

  const headline = h("div", "console-dashboard-hero__headline");
  const verdict = h("p", "console-dashboard-hero__verdict");
  // The glyph is a SIBLING of the verdict, never a child of it.
  //
  // renderQueue() sets verdict.textContent on every poll, and textContent replaces the element's
  // entire child list - so a button appended inside it is destroyed by the first read that lands,
  // seconds after mount. It is the kind of bug that looks like the feature was never wired: the
  // markup is right at construction and gone before anyone looks.
  const verdictRow = h("div", "console-dashboard-hero__verdictrow");
  verdictRow.append(
    verdict,
    helpGlyph(
      "This headline reads the attention queue: blocks an agent raised that are waiting on a" +
        " person, the same queue `magus session attention` lists. Nothing closes a request but" +
        " you disposing of it. The failing/running/queued counts below are live run activity and" +
        " do NOT feed this verdict - a failing target is not a request, and an empty queue over a" +
        " red build is both facts being true at once.",
      "this verdict",
    ),
  );
  const detail = h("p", "console-dashboard-hero__detail");
  // The first paint, before any read has landed. No data-state is set with it, so the hero keeps
  // its neutral accent rule: the queue is UNKNOWN at this moment, and both the calm colour and
  // the loud one would be claims the tile has no basis for yet.
  verdict.textContent = "Reading the queue";
  detail.textContent = "Blocks agents raised that are waiting on a person.";
  headline.append(verdictRow, detail);

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
  // own deep links, and a second renderer of the same list is exactly the duplication the failing
  // list avoids by being the only place failures are named.
  const runWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__run");
  const runLink = h("a", "console-dashboard-hero__metriclink");
  const runN = h("span", "console-dashboard-hero__n", "0");
  runLink.append(runN, h("span", "console-dashboard-hero__l", "running"));
  runWrap.append(runLink);

  // Queued gets NO link, deliberately. There is nowhere to go: a queued target has not started, so
  // it has no output to open and no run to join. A link that resolved to the log viewer's empty
  // state would be worse than none - it teaches that the counts are not reliably clickable.
  //
  // What it gets instead is the answer to the question the number actually raises, which is not
  // "where" but "why is anything waiting". That is derivable from the frame already in hand: the
  // pool being full, or a lock being held, are the two reasons, and the tile can say which.
  const queueWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__queue");
  const queueN = h("span", "console-dashboard-hero__n", "0");
  queueWrap.append(queueN, h("span", "console-dashboard-hero__l", "queued"));

  metrics.append(failWrap, runWrap, queueWrap);

  // The failing-target list: WHAT is broken, one clickable row each, straight under the verdict.
  //
  // This is the difference between a display that reports and one that can be acted on. The hero
  // already had every fact it needed - the run frame carries each failing target's label and its
  // output ref - and was spending them on a single count. Naming them costs a few lines of DOM and
  // removes two navigation hops (timeline to find which, log viewer to find why).
  //
  // Capped, with a residual count, for the same reason every other list on this board is: a broken
  // dependency can fail thirty targets at once, and thirty rows would push the verdict itself off a
  // Big Picture panel. The cap is generous enough that the common case (a handful) shows in full.
  // The attention queue itself: one row per open request, straight under the verdict it is the
  // subject of. Above the failing-target list because it outranks it - a failing target is work
  // magus can tell you about, a request is work waiting on YOU.
  const queueList = h("ul", "console-dashboard-attention__list");
  queueList.hidden = true;
  // A polite live region: a request appearing is the one thing on this board a reader needs to
  // learn about without watching it. Polite rather than assertive because a queue row is a
  // request, not an alarm - agents request, people dispose, and nothing here interrupts.
  queueList.setAttribute("aria-live", "polite");
  const queueNote = h("p", "console-dashboard-attention__note");
  queueNote.hidden = true;
  headline.append(queueList, queueNote);

  const FAIL_LIST_MAX = 6;
  const failList = h("ul", "console-dashboard-hero__faillist");
  failList.hidden = true;
  // Inside the HEADLINE, not a third sibling of it. The hero is a wrapping flex row, so as a
  // sibling the list became a flex item competing with the counts for the row - it sat to their
  // right, stretched to the hero's full height, as a tall mostly-empty box. Nested in the headline
  // block it simply flows under the verdict and its detail line, which is also where it belongs:
  // it is the detail line's answer ("1 target is failing" -> "apps/admin:e2e"), not a fourth metric.
  headline.append(failList);
  root.append(headline, metrics);

  function render(status: StatusView, liveHost: string | null, demo: boolean): void {
    const failing = countFailing(status);
    const running = status.pool.running;
    const queued = status.pool.queued;

    failN.textContent = String(failing);
    runN.textContent = String(running);
    queueN.textContent = String(queued);
    // data-n gates each count's color: quiet at zero, loud when there is something to show.
    failWrap.dataset.n = failing > 0 ? "some" : "none";
    runWrap.dataset.n = running > 0 ? "some" : "none";
    queueWrap.dataset.n = queued > 0 ? "some" : "none";

    const scoped = workspaceScope() !== ALL_WORKSPACES;
    // The verdict's sub-line already says whose these counts are; say it on the figures too. People
    // read the three numbers first and the sentence under them second, and it is the numbers that
    // look like they contradict a scoped tile reporting nothing running.
    if (scoped) {
      metrics.title =
        "Pool counts are daemon-wide: one pool serves every workspace on this daemon.";
    } else {
      metrics.removeAttribute("title");
    }
    // No verdict is set here. The headline belongs to the queue (renderQueue), and a status
    // frame arriving about once a second must not overwrite it - that is precisely how the two
    // came to disagree, with whichever repainted last winning the line.

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
      runLink.title = "Open the live log viewer";
    } else {
      runLink.removeAttribute("href");
      delete runWrap.dataset.linked;
      runLink.removeAttribute("title");
    }

    // Queued explains ITSELF rather than linking. The two reasons anything waits are both in this
    // frame: every pool slot busy, or a lock held by another process. Naming which one turns a
    // number that reads as unexplained backlog into a fact with a cause.
    queueWrap.title = queuedReason(status, queued);

    renderFailList(status, liveHost, demo);
  }

  // queuedReason says WHY work is waiting, from the same status frame. Capacity is checked first
  // because a saturated pool is the ordinary reason and a held lock is the surprising one - but a
  // lock is named even when the pool is also full, since it is the one an operator may need to act
  // on. Capacity 0 means an unlimited pool, where saturation is not a possible explanation.
  function queuedReason(status: StatusView, queued: number): string {
    if (queued === 0) return "Nothing is waiting to start.";
    const held = status.locks.length;
    const saturated = status.pool.capacity > 0 && status.pool.running >= status.pool.capacity;
    if (held > 0) {
      const waiters = status.locks.reduce((n, l) => n + l.waiters.length, 0);
      if (waiters > 0) {
        return (
          queued +
          " waiting. " +
          waiters +
          (waiters === 1 ? " run is" : " runs are") +
          " blocked behind a held workspace lock."
        );
      }
    }
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

  // renderFailList names each failing target and links it to its own captured output.
  //
  // A target that failed WITHOUT an output ref still gets a row - it falls back to opening its run.
  // Dropping it would be the worst option available: the count would say three and the list would
  // show two, and the reader would have no way to tell which one they were not being shown.
  function renderFailList(status: StatusView, liveHost: string | null, demo: boolean): void {
    const failures = failingTargets(status);
    failList.hidden = failures.length === 0;
    if (failures.length === 0) {
      failList.replaceChildren();
      return;
    }
    const shown = failures.slice(0, FAIL_LIST_MAX);
    const rows: HTMLElement[] = shown.map((f) => {
      const li = h("li", "console-dashboard-hero__failrow");
      // An anchor only when there is somewhere to go: offline there is no host to resolve a ref
      // against, and a link that goes nowhere is worse than plain text.
      //
      // The demo gets the SHARED demo log viewer instead of nothing, the same fallback the
      // live-activity tile uses. Without it the one mode anybody browses the feature in is the one
      // mode where it renders as inert text - the showcase would demonstrate the count and hide the
      // thing the count is for.
      const href = liveHost
        ? f.outputRef
          ? logsLink(liveHost, { ref: f.outputRef })
          : f.inv
            ? logsLink(liveHost, { inv: f.inv })
            : ""
        : demo
          ? "../logs/#demo"
          : "";
      li.append(failChip(f, href));
      return li;
    });
    if (failures.length > shown.length) {
      const more = h(
        "li",
        "console-dashboard-hero__failmore",
        "+" + (failures.length - shown.length) + " more",
      );
      rows.push(more);
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

  // requestRow draws one open request: what to close, how long it has waited, what kind of block
  // it is, and the first line of what the agent said.
  //
  // The id is shown in full and in mono, because it is the handle for the OTHER surface: a
  // person reading this tile on a shared screen closes the request from their terminal with
  // `magus session dispose <id>`, and a truncated id cannot be typed.
  function requestRow(req: AttentionRequest, nowMs: number): HTMLElement {
    const li = h("li", "console-dashboard-attention__row");
    li.dataset.outcome = req.outcome;

    const head = h("div", "console-dashboard-attention__head");
    const id = h("code", "console-dashboard-attention__id", req.id);
    id.title = "magus session dispose " + req.id;
    head.append(id);

    const age = ageLabel(req.opened_ms, nowMs);
    if (age) head.append(h("span", "console-dashboard-attention__age", age));
    if (req.outcome) {
      const outcome = h("span", "pf-v6-c-label pf-m-compact console-dashboard-attention__outcome");
      outcome.append(h("span", "pf-v6-c-label__content", req.outcome));
      head.append(outcome);
    }
    // Which slice of a fleet's work is blocked. Absent for anything a person started by hand,
    // which is the ordinary case and not worth a placeholder.
    if (req.delegation) {
      head.append(h("span", "console-dashboard-attention__delegation", req.delegation));
    }
    head.append(disposeControl(req));

    const message = h("p", "console-dashboard-attention__message", firstLine(req.message));
    // The full text, unflattened, for the reader who needs more than the summary line. The row
    // stays one line tall either way; `magus session attention -o json` is the unabridged copy.
    if (req.message) message.title = req.message;

    li.append(head, message);
    return li;
  }

  // disposeControl is the button and the one-line reason composer behind it.
  //
  // A composer rather than a prompt(): the diff surface settled this for the same act. A
  // prompt() steals focus from the page, cannot show WHICH request is being closed, and hides
  // the message the reason is about while it is being typed. The composer sits in the row.
  //
  // Closing a request is the one write this tile makes, and it happens only here - one click,
  // one reason, one id. There is deliberately no "dispose all": a queue cleared without being
  // read is the failure mode the queue exists to prevent.
  function disposeControl(req: AttentionRequest): HTMLElement {
    const wrap = h("span", "console-dashboard-attention__dispose");
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "pf-v6-c-button pf-m-link pf-m-inline console-dashboard-attention__disposebtn";
    btn.append(h("span", "pf-v6-c-button__text", "Dispose"));
    btn.title = "Close this request. Say why, so the store records who answered it and what for.";
    btn.setAttribute("aria-expanded", "false");
    wrap.append(btn);

    const close = (): void => {
      wrap.querySelector(".console-dashboard-attention__composer")?.remove();
      btn.hidden = false;
      btn.setAttribute("aria-expanded", "false");
    };

    btn.addEventListener("click", () => {
      const box = h("span", "console-dashboard-attention__composer");
      const inputWrap = h("span", "pf-v6-c-form-control");
      const input = h("input", "pf-v6-c-form-control__text");
      input.type = "text";
      input.setAttribute("aria-label", "Why request " + req.id + " is being closed");
      // A reason is offered, never demanded. Somebody closing a block they just answered in
      // person has nothing to narrate, and a required field would be filled with "done".
      input.placeholder = "Why (optional). Enter to dispose, Esc to cancel.";
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
      box.append(inputWrap);
      btn.hidden = true;
      btn.setAttribute("aria-expanded", "true");
      wrap.append(box);
      input.focus();
    });
    return wrap;
  }

  // send posts the disposal and says what came back. A REFUSAL (unknown id, ambiguous prefix,
  // already closed by somebody else) is reported in the daemon's own words rather than as a
  // failure: each one is the daemon working, and each names what the person does next.
  async function send(req: AttentionRequest, reason: string): Promise<void> {
    if (!host) {
      showToast("Attention", "Not connected to a daemon, so nothing can be disposed.", "error");
      return;
    }
    const res = await disposeAttention(host, req.id, reason);
    if (torndown) return;
    if (res.kind === "ok") {
      showToast("Attention", "Disposed " + req.id + ".", "ok");
      // Re-read rather than dropping the row locally: another worktree may have closed
      // something else in the meantime, and the store is what every surface agrees on.
      lastRead = 0;
      refresh();
      return;
    }
    showToast("Attention", res.detail, "error");
    lastRead = 0;
    refresh();
  }

  // renderQueue paints the headline and the rows from one read. Every non-ok read gets its own
  // words - see verdictFor - so an unknown queue never renders as a calm one.
  function renderQueue(read: AttentionRead): void {
    const v = verdictFor(read);
    root.dataset.state = v.state;
    verdict.textContent = v.line;
    detail.textContent = v.sub;

    if (read.kind !== "ok" || read.requests.length === 0) {
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
    const rows = shown.map((req) => requestRow(req, now));
    if (read.requests.length > shown.length) {
      rows.push(
        h(
          "li",
          "console-dashboard-attention__more",
          "+" +
            (read.requests.length - shown.length) +
            " more; `magus session attention` lists the whole queue",
        ),
      );
    }
    queueList.replaceChildren(...rows);
    queueList.hidden = false;
    queueNote.hidden = true;
    queueNote.textContent = "";
  }

  const refresh = (): void => {
    if (!visible || !host || reading) return;
    reading = true;
    lastRead = Date.now();
    const current = ++request;
    controller = new AbortController();
    const timeout = window.setTimeout(() => {
      if (current === request) controller?.abort();
    }, REQUEST_TIMEOUT_MS);
    void loadAttention(host, controller.signal)
      .then((read) => {
        // A composer the reader is typing into must survive a poll landing under them. The
        // repaint is skipped rather than deferred: the next tick is 4s away, and the read it
        // brings will be fresher than this one.
        if (torndown || current !== request) return;
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
  // when the daemon pushes a frame.
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
        // would put a Dispose button on the showcase that closes nothing, and a reader who
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
        // No daemon, so the queue genuinely cannot be read. Said out loud rather than left at
        // whatever the last connected read showed: a stale "nobody waiting" outlives the
        // connection that earned it, and this is the tile where that reads as reassurance.
        renderQueue({ kind: "unreadable", detail: "not connected to a daemon" });
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
    },
  };
}
