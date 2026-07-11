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

// firstFailedInv returns the invocation id of the earliest run carrying a failed target,
// so the failing count can deep-link into the run whose log an operator needs.
function firstFailedInv(status: StatusView): string {
  for (const run of status.runs) {
    if (run.targets.some((t) => t.state === "failed")) return run.inv;
  }
  return "";
}

function countFailing(status: StatusView): number {
  let n = 0;
  for (const run of status.runs) {
    for (const t of run.targets) if (t.state === "failed") n++;
  }
  return n;
}

export function attentionTile(): Tile {
  const root = h("section", "dash-hero");
  root.setAttribute("aria-label", "Needs attention");

  const headline = h("div", "hero-headline");
  const verdict = h("p", "hero-verdict");
  const detail = h("p", "hero-detail");
  headline.append(verdict, detail);

  const metrics = h("div", "hero-metrics");
  // The failing metric is an <a> so it can deep-link to the failing run's log in live mode;
  // it degrades to a plain block (via a swapped node) when there is nothing to link to.
  const failWrap = h("div", "hero-metric hero-fail");
  const failLink = h("a", "hero-metric-link");
  const failN = h("span", "hero-n", "0");
  const failL = h("span", "hero-l", "failing");
  failLink.append(failN, failL);
  failWrap.append(failLink);

  const runWrap = h("div", "hero-metric hero-run");
  const runN = h("span", "hero-n", "0");
  runWrap.append(runN, h("span", "hero-l", "running"));

  const queueWrap = h("div", "hero-metric hero-queue");
  const queueN = h("span", "hero-n", "0");
  queueWrap.append(queueN, h("span", "hero-l", "queued"));

  metrics.append(failWrap, runWrap, queueWrap);
  root.append(headline, metrics);

  function render(status: StatusView, liveHost: string | null): void {
    const failing = countFailing(status);
    const running = status.pool.running;
    const queued = status.pool.queued;
    const down = status.health.cls === "fail";
    const degraded = status.health.cls === "warn";

    failN.textContent = String(failing);
    runN.textContent = String(running);
    queueN.textContent = String(queued);
    // data-n gates each count's color: quiet at zero, loud when there is something to show.
    failWrap.dataset.n = failing > 0 ? "some" : "none";
    runWrap.dataset.n = running > 0 ? "some" : "none";
    queueWrap.dataset.n = queued > 0 ? "some" : "none";

    // Verdict, in priority order: failing targets, then an unhealthy daemon, then all clear.
    let state: string, line: string, sub: string;
    if (failing > 0) {
      state = "attention";
      line = "Attention needed";
      sub = failing === 1 ? "1 target is failing" : failing + " targets are failing";
    } else if (down || degraded) {
      state = "warn";
      line = down ? "Daemon down" : "Daemon degraded";
      sub = "The pool is up but the daemon reports " + status.health.label + ".";
    } else {
      state = "clear";
      line = "All clear";
      sub = running > 0
        ? (running === 1 ? "1 target running, nothing failing" : running + " targets running, nothing failing")
        : "Nothing failing, pool is idle";
    }
    root.dataset.state = state;
    verdict.textContent = line;
    detail.textContent = sub;

    // Wire the failing count into the failing run's log when we are live and have an inv.
    const inv = failing > 0 ? firstFailedInv(status) : "";
    if (failing > 0 && liveHost && inv) {
      failLink.setAttribute("href", "../logs/#live=" + encodeURIComponent(liveHost) + "&inv=" + encodeURIComponent(inv));
      failWrap.dataset.linked = "true";
    } else {
      failLink.removeAttribute("href");
      delete failWrap.dataset.linked;
    }
  }

  return {
    el: root,
    update(s: DashboardState) { if (s.status) render(s.status, s.liveHost); },
    destroy() {},
  };
}
