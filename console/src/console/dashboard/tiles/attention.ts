// attention.ts - the "needs attention" hero: the first thing the eye lands on. It answers
// "is anything failing? what's running?" at a glance, before any metric tile. Three loud
// counts (failing / running / queued) plus a one-line verdict derived from the live status
// frame: failing targets shout in red, otherwise a degraded/down daemon warns, otherwise a
// calm "all clear". When something is failing AND the dashboard is live, the failing count
// deep-links into that run's log so the fix is one click away.
//
// This is deliberately NOT a collapsible Card: the summary is always visible - it is the
// board's headline, not a foldable panel.

import type { DashboardState, StatusView } from "../state";
import { h, helpGlyph, type Tile } from "./card";
import { logsLink } from "../../../lib/daemon";
import { showToast } from "../../../lib/refresh-toast";

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
// so the failing count can deep-link into the run whose log an operator needs. Exported so
// the Big Picture tile (tiles/bigPicture.ts) can compute the same TV-friendly verdict without
// duplicating the scan.
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

// verdictFor derives the one-line headline + detail from a status frame and its failing count, in
// priority order: failing targets, then an unhealthy daemon, then all clear. Exported so the
// Big Picture tile can show the identical verdict at TV scale without re-deriving the rule.
export function verdictFor(status: StatusView, failing: number): Verdict {
  const running = status.pool.running;
  const down = status.health.cls === "fail";
  const degraded = status.health.cls === "warn";
  if (failing > 0) {
    return {
      state: "attention",
      line: "Attention needed",
      sub: failing === 1 ? "1 target is failing" : failing + " targets are failing",
    };
  }
  if (down || degraded) {
    return {
      state: "warn",
      line: down ? "Daemon down" : "Daemon degraded",
      sub: "The pool is up but the daemon reports " + status.health.label + ".",
    };
  }
  return {
    state: "clear",
    line: "All clear",
    sub:
      running > 0
        ? running === 1
          ? "1 target running, nothing failing"
          : running + " targets running, nothing failing"
        : "Nothing failing, pool is idle",
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
  btn.title = f.label + " failed - open its output or copy the command to rerun it";

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
  // render() sets verdict.textContent on every status frame, and textContent replaces the element's
  // entire child list - so a button appended inside it is destroyed by the first frame that lands,
  // about a second after mount. It is the kind of bug that looks like the feature was never wired:
  // the markup is right at construction and gone before anyone looks.
  const verdictRow = h("div", "console-dashboard-hero__verdictrow");
  verdictRow.append(
    verdict,
    helpGlyph(
      "This line is a judgement, not a measurement, and it is made in a fixed order: any target" +
        " FAILING outranks everything and says Attention needed; otherwise an unhealthy daemon says" +
        " degraded or down; otherwise it is All clear, whether or not work is running. So All clear" +
        " means nothing is broken - it does not mean nothing is happening, and a busy pool is still" +
        " all clear. Anything failing is named underneath, and each name opens its own output.",
      "this verdict",
    ),
  );
  const detail = h("p", "console-dashboard-hero__detail");
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

    const v = verdictFor(status, failing);
    root.dataset.state = v.state;
    verdict.textContent = v.line;
    detail.textContent = v.sub;

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

  return {
    el: root,
    update(s: DashboardState) {
      if (s.status) render(s.status, s.liveHost, s.conn.state === "demo");
    },
    destroy() {},
  };
}
