// gantt.ts - the live execution timeline. One row per target, grouped under its run
// (invocation + trigger). The x-axis is wall-clock over a rolling recent window (the
// last WINDOW_MS to "now"); each target draws a bar from its start to its end (or to
// "now" while running), colored by state via .gantt-bar.<state> classes so it reuses
// the dashboard's fixed hit/miss/err/info palette. Hand-rolled inline SVG with a tiny
// local linear time scale - a timeline is rows x time, not a node graph, so it needs no
// layout library. A finished bar (passed|failed|cached) with an output reference
// deep-links to the log viewer for that ref, mirroring the running-targets tile.

import type { DashboardState, RunView, TargetRunView } from "../state";
import { Card, h, type Tile } from "./card";

const SVGNS = "http://www.w3.org/2000/svg";

const WINDOW_MS = 60_000; // rolling window: last 60s to now
const VIEW_W = 720;       // viewBox width; the SVG scales to the tile via its viewBox
const LABEL_W = 150;      // left gutter for target labels
const RIGHT_PAD = 12;
const AXIS_H = 14;        // top strip for the time-axis tick labels
const RUN_H = 16;         // a run-group header row
const ROW_H = 18;         // one target row
const BAR_H = 10;
const MIN_BAR_W = 2;      // instant (cached) bars stay visible
const PLOT_W = VIEW_W - RIGHT_PAD - LABEL_W;
const TICK_MS = WINDOW_MS; // full window per axis tick

function svg(tag: string): SVGElement {
  return document.createElementNS(SVGNS, tag) as SVGElement;
}

// truncate keeps a label inside the left gutter (SVG <text> does not clip on its own);
// the full label rides a <title> tooltip.
function truncate(s: string, max: number): string {
  return s.length > max ? s.slice(0, max - 3) + "..." : s;
}

// barSpan resolves a target's [start, end] in ms for this render, filling active/finished
// gaps from the fields that are present (endedAt vs startedAt+durationMs), against `now`.
function barSpan(t: TargetRunView, now: number): { s: number; e: number } | null {
  if (t.state === "unspecified") return null;
  if (t.state === "queued") {
    // No start yet: a pending pip anchored at the current-time line.
    return { s: now, e: now };
  }
  if (t.state === "running") {
    return { s: t.startMs ?? now, e: now };
  }
  // terminal: passed | failed | cached
  const e = t.endMs ?? (t.startMs != null ? t.startMs + t.durationMs : now);
  const s = t.startMs ?? (t.endMs != null ? t.endMs - t.durationMs : e);
  return { s, e };
}

function fmtDurMs(ms: number): string {
  if (ms <= 0) return "";
  if (ms < 1000) return Math.round(ms) + " ms";
  return (ms / 1000).toFixed(ms < 10_000 ? 2 : 1) + " s";
}

export function ganttTile(): Tile {
  const card = new Card("gantt", "Live execution", { term: "Trace", label: "trace", note: "idle" });
  const wrap = h("div", "gantt-scroll");
  const empty = h("p", "row-empty", "No active runs.");
  const legend = h("div", "gantt-legend");
  for (const [cls, text] of [
    ["running", "running"], ["queued", "queued"], ["passed", "passed"],
    ["failed", "failed"], ["cached", "cached"],
  ] as const) {
    legend.append(h("span", "lg lg-" + cls, text));
  }
  card.body.append(wrap, empty, legend);

  let runs: RunView[] = [];
  let liveHost: string | null = null;

  function timeX(t: number, t0: number, now: number): number {
    const span = now - t0 || 1;
    const clamped = Math.min(now, Math.max(t0, t));
    return LABEL_W + ((clamped - t0) / span) * PLOT_W;
  }

  function drawAxis(root: SVGElement, t0: number, now: number): void {
    const axisLine = svg("line");
    axisLine.setAttribute("x1", String(LABEL_W));
    axisLine.setAttribute("x2", String(VIEW_W - RIGHT_PAD));
    axisLine.setAttribute("y1", String(AXIS_H));
    axisLine.setAttribute("y2", String(AXIS_H));
    axisLine.setAttribute("class", "gantt-axis-line");
    root.appendChild(axisLine);
    // Three ticks: window start, midpoint, now.
    const ticks: [number, string][] = [
      [t0, "-" + Math.round(TICK_MS / 1000) + "s"],
      [t0 + TICK_MS / 2, "-" + Math.round(TICK_MS / 2000) + "s"],
      [now, "now"],
    ];
    for (const [t, txt] of ticks) {
      const x = timeX(t, t0, now);
      const grid = svg("line");
      grid.setAttribute("x1", String(x));
      grid.setAttribute("x2", String(x));
      grid.setAttribute("y1", String(AXIS_H));
      grid.setAttribute("y2", "100%");
      grid.setAttribute("class", "gantt-grid");
      root.appendChild(grid);
      const label = svg("text");
      label.setAttribute("x", String(txt === "now" ? x - 2 : x + 2));
      label.setAttribute("y", "10");
      label.setAttribute("class", "gantt-axis-label");
      if (txt === "now") label.setAttribute("text-anchor", "end");
      label.textContent = txt;
      root.appendChild(label);
    }
  }

  function drawBar(root: SVGElement, t: TargetRunView, rowY: number, t0: number, now: number): void {
    const span = barSpan(t, now);
    if (!span) return;
    const x1 = timeX(span.s, t0, now);
    const x2 = timeX(span.e, t0, now);
    const w = Math.max(MIN_BAR_W, x2 - x1);
    // A queued pip sits just left of the now-line so it reads as "waiting to start".
    const x = t.state === "queued" ? Math.max(LABEL_W, timeX(now, t0, now) - MIN_BAR_W) : x1;
    const rect = svg("rect");
    rect.setAttribute("x", x.toFixed(2));
    rect.setAttribute("y", String(rowY + (ROW_H - BAR_H) / 2));
    rect.setAttribute("width", w.toFixed(2));
    rect.setAttribute("height", String(BAR_H));
    rect.setAttribute("rx", "2");
    rect.setAttribute("class", "gantt-bar " + t.state);
    const elapsed = t.durationMs > 0 ? t.durationMs : (t.startMs != null ? now - t.startMs : 0);
    const dur = fmtDurMs(elapsed);
    const title = svg("title");
    title.textContent = t.label + " - " + t.state + (dur ? " (" + dur + ")" : "");
    rect.appendChild(title);

    // Finished bars with a ref deep-link to the log viewer for that ref, carrying the
    // live host so the viewer can resolve it - same relative path the running-targets
    // tile uses, opened in a new tab so the live board stays put.
    if (t.terminal && t.outputRef && liveHost) {
      const a = svg("a");
      a.setAttribute("href", "../logs/#live=" + encodeURIComponent(liveHost) + "&ref=" + encodeURIComponent(t.outputRef));
      a.setAttribute("target", "_blank");
      a.setAttribute("rel", "noopener");
      a.setAttribute("class", "gantt-link");
      a.appendChild(rect);
      root.appendChild(a);
    } else {
      root.appendChild(rect);
    }
  }

  function render(): void {
    const now = Date.now();
    const t0 = now - WINDOW_MS;
    // Window the runs: drop any target whose span is entirely before the visible
    // window (a terminal bar that finished more than WINDOW_MS ago), and any run
    // left with no visible targets. Otherwise stale terminal bars clamp to
    // MIN_BAR_W stubs and pile up against the left gutter forever.
    const visibleRuns = runs
      .map((r) => ({ ...r, targets: r.targets.filter((t) => {
        const span = barSpan(t, now);
        return !span || span.e >= t0;
      }) }))
      .filter((r) => r.targets.length > 0);
    const nTargets = visibleRuns.reduce((n, r) => n + r.targets.length, 0);
    empty.hidden = nTargets > 0;
    if (nTargets === 0) {
      wrap.replaceChildren();
      card.setNote("idle");
      return;
    }

    const totalH = AXIS_H + visibleRuns.reduce((n, r) => n + RUN_H + r.targets.length * ROW_H, 0) + 4;
    const root = svg("svg");
    root.setAttribute("viewBox", "0 0 " + VIEW_W + " " + totalH);
    root.setAttribute("class", "gantt-svg");
    root.setAttribute("preserveAspectRatio", "xMinYMin meet");
    root.setAttribute("role", "img");
    root.setAttribute("aria-label", "Live execution timeline");

    drawAxis(root, t0, now);

    let y = AXIS_H;
    let running = 0;
    for (const run of visibleRuns) {
      const head = svg("text");
      head.setAttribute("x", "2");
      head.setAttribute("y", String(y + 12));
      head.setAttribute("class", "gantt-run-label");
      const inv = run.inv ? run.inv.slice(0, 12) : "run";
      head.textContent = (run.trigger || "run") + " " + inv;
      root.appendChild(head);
      y += RUN_H;
      for (const t of run.targets) {
        if (t.state === "running") running++;
        const label = svg("text");
        label.setAttribute("x", "8");
        label.setAttribute("y", String(y + BAR_H + 2));
        label.setAttribute("class", "gantt-target-label");
        label.textContent = truncate(t.label || t.target || "-", 22);
        const lt = svg("title");
        lt.textContent = t.label;
        label.appendChild(lt);
        root.appendChild(label);
        drawBar(root, t, y, t0, now);
        y += ROW_H;
      }
    }

    wrap.replaceChildren(root);
    card.setNote(visibleRuns.length + (visibleRuns.length === 1 ? " run" : " runs") + ", " + running + " running");
  }

  // A light 1s ticker advances the running bars and rolls the window forward even when
  // no new status frame has arrived; cleared on destroy.
  const ticker = window.setInterval(() => { if (runs.length) render(); }, 1000);

  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.status) return;
      runs = s.status.runs;
      liveHost = s.liveHost;
      render();
    },
    destroy() { window.clearInterval(ticker); },
  };
}
