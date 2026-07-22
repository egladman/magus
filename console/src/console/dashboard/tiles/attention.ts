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
import { h, type Tile } from "./card";
import { logsLink } from "../../../lib/daemon";

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

export function attentionTile(): Tile {
  const root = h("section", "console-dashboard-hero");
  root.setAttribute("aria-label", "Needs attention");

  const headline = h("div", "console-dashboard-hero__headline");
  const verdict = h("p", "console-dashboard-hero__verdict");
  const detail = h("p", "console-dashboard-hero__detail");
  headline.append(verdict, detail);

  const metrics = h("div", "console-dashboard-hero__metrics");
  // The failing metric is an <a> so it can deep-link to the failing run's log in live mode;
  // it degrades to a plain block (via a swapped node) when there is nothing to link to.
  const failWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__fail");
  const failLink = h("a", "console-dashboard-hero__metriclink");
  const failN = h("span", "console-dashboard-hero__n", "0");
  const failL = h("span", "console-dashboard-hero__l", "failing");
  failLink.append(failN, failL);
  failWrap.append(failLink);

  const runWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__run");
  const runN = h("span", "console-dashboard-hero__n", "0");
  runWrap.append(runN, h("span", "console-dashboard-hero__l", "running"));

  const queueWrap = h("div", "console-dashboard-hero__metric console-dashboard-hero__queue");
  const queueN = h("span", "console-dashboard-hero__n", "0");
  queueWrap.append(queueN, h("span", "console-dashboard-hero__l", "queued"));

  metrics.append(failWrap, runWrap, queueWrap);
  root.append(headline, metrics);

  function render(status: StatusView, liveHost: string | null): void {
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
  }

  return {
    el: root,
    update(s: DashboardState) {
      if (s.status) render(s.status, s.liveHost);
    },
    destroy() {},
  };
}
