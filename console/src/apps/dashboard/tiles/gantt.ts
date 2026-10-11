// gantt.ts - the live execution timeline. One row per target, grouped under its run
// (invocation + trigger). The x-axis is wall-clock over a rolling recent window (the
// last WINDOW_MS to "now"); each target draws a bar from its start to its end (or to
// "now" while running), colored by state via the bar's data-state attribute so it reuses
// the dashboard's fixed hit/miss/err/info palette. Hand-rolled inline SVG with a tiny
// local linear time scale - a timeline is rows x time, not a node graph, so it needs no
// layout library. A finished bar (passed|failed|cached) with an output reference
// deep-links to the log viewer for that ref, mirroring the running-targets tile.
//
// The drawing is laid out in CSS pixels at the width the card has, never scaled down from a fixed
// viewBox: a scaled drawing shrinks its text with it (to 3px on a narrow pane), and the type here is
// the console's 12px floor at every width. Below MIN_W the scroller scrolls sideways instead.

import type { DashboardState, RunView, TargetRunView } from "../state";
import { Card, h, scrollRegion, type Tile } from "./card";
import { logsLink } from "../../../lib/server";

const SVGNS = "http://www.w3.org/2000/svg";

const WINDOW_MS = 60_000; // rolling window: last 60s to now
const MIN_W = 560; // narrowest drawing, in px; narrower panes scroll
const LABEL_W = 172; // left gutter for target labels
const RIGHT_PAD = 12;
const AXIS_H = 22; // top strip for the time-axis tick labels
const RUN_H = 24; // a run-group header row
const ROW_H = 24; // one target row
const BAR_H = 12;
const MIN_BAR_W = 2; // instant (cached) bars stay visible
const LABEL_CHARS = 22; // a 12px mono label that fits the gutter
const TICK_MS = WINDOW_MS; // full window per axis tick

function svg(tag: string): SVGElement {
  return document.createElementNS(SVGNS, tag) as SVGElement;
}

// truncate keeps a label inside the left gutter (SVG <text> does not clip on its own);
// the full label rides a <title>.
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

// stateWord is the word a bar's colour has to be backed by. Failed and queued are both drawn from
// the danger hue (a queue that is full is a saturated pool), and the pale pip beside a solid bar is
// not a difference to rely on, so the two say what they are in text after the bar.
function stateWord(state: TargetRunView["state"]): string {
  if (state === "failed") return "failed";
  if (state === "queued") return "queued";
  if (state === "cached") return "cached";
  return "";
}

// Geometry for one render: the drawing's width, and the plot it leaves after the gutters.
interface Geo {
  w: number;
  plotW: number;
}

export function ganttTile(): Tile {
  const card = new Card("gantt", "Live execution", {
    term: "Trace",
    label: "trace",
    note: "idle",
    why:
      "Where a run's wall clock actually went. The dashed leader is queued time and the solid bar is" +
      " real work. One duration hides that difference, and the two need opposite fixes.",
  });
  // How to READ the chart, printed on the tile rather than hidden behind the "?": the tile encodes
  // horizontal position (when), bar length (how long), the dashed leader (queued rather than
  // working), and the shaded block (which run). A popover is unreachable on a wall display, which
  // is exactly where an unexplained chart is most expensive.
  const howto = h(
    "p",
    "console-dashboard-gantt__howto",
    "Time runs left to right over the last minute and the right edge is now. Each shaded block is" +
      " one run, one row per target, and a bar is as long as the target took.",
  );
  const wrap = h("div", "console-dashboard-gantt__scroll");
  scrollRegion(wrap, "Execution timeline, scrolls sideways and down");
  const legend = h("div", "console-dashboard-gantt__legend");
  for (const [cls, text] of [
    ["running", "running"],
    ["queued", "queued"],
    ["passed", "passed"],
    ["failed", "failed"],
    ["cached", "cached"],
    // The one legend entry that is not a color. The dashed leader is the chart's most useful mark
    // and the least guessable, since nothing else on the board uses dashes to mean elapsed time.
    ["wait", "waiting to start"],
  ] as const) {
    legend.append(h("span", "console-dashboard-legend console-dashboard-legend--" + cls, text));
  }
  card.body.append(howto, wrap, legend);

  let runs: RunView[] = [];
  let liveHost: string | null = null;

  function timeX(t: number, t0: number, now: number, geo: Geo): number {
    const span = now - t0 || 1;
    const clamped = Math.min(now, Math.max(t0, t));
    return LABEL_W + ((clamped - t0) / span) * geo.plotW;
  }

  function drawAxis(root: SVGElement, t0: number, now: number, geo: Geo, totalH: number): void {
    const axisLine = svg("line");
    axisLine.setAttribute("x1", String(LABEL_W));
    axisLine.setAttribute("x2", String(geo.w - RIGHT_PAD));
    axisLine.setAttribute("y1", String(AXIS_H));
    axisLine.setAttribute("y2", String(AXIS_H));
    axisLine.setAttribute("class", "console-dashboard-gantt__axisline");
    root.appendChild(axisLine);
    // Three ticks: window start, midpoint, now.
    const ticks: [number, string][] = [
      [t0, "-" + Math.round(TICK_MS / 1000) + "s"],
      [t0 + TICK_MS / 2, "-" + Math.round(TICK_MS / 2000) + "s"],
      [now, "now"],
    ];
    for (const [t, txt] of ticks) {
      const x = timeX(t, t0, now, geo);
      const grid = svg("line");
      grid.setAttribute("x1", String(x));
      grid.setAttribute("x2", String(x));
      grid.setAttribute("y1", String(AXIS_H));
      grid.setAttribute("y2", String(totalH));
      grid.setAttribute("class", "console-dashboard-gantt__grid");
      root.appendChild(grid);
      const label = svg("text");
      label.setAttribute("x", String(txt === "now" ? x - 2 : x + 2));
      label.setAttribute("y", String(AXIS_H - 6));
      label.setAttribute("class", "console-dashboard-gantt__axislabel");
      if (txt === "now") label.setAttribute("text-anchor", "end");
      label.textContent = txt;
      root.appendChild(label);
    }
  }

  function drawBar(
    root: SVGElement,
    t: TargetRunView,
    rowY: number,
    t0: number,
    now: number,
    runStart: number | null,
    geo: Geo,
  ): void {
    const span = barSpan(t, now);
    if (!span) return;
    const x1 = timeX(span.s, t0, now, geo);
    const x2 = timeX(span.e, t0, now, geo);
    const w = Math.max(MIN_BAR_W, x2 - x1);

    // WAIT SEGMENT: the stretch between the run being submitted and this target actually starting.
    //
    // The bar alone conflates two very different facts. A target that took 40s because it ran for
    // 40s and one that took 40s because it sat in the queue for 35s and ran for 5 look identical,
    // and the second is the one worth acting on - it says the pool is the constraint, not the work.
    // Both times are already in the frame, so the distinction costs a rectangle.
    //
    // Drawn hollow and behind the bar so it reads as elapsed-but-not-working.
    if (runStart != null && t.startMs != null && t.startMs > runStart) {
      const wx1 = timeX(runStart, t0, now, geo);
      const ww = x1 - wx1;
      if (ww > 1) {
        const wait = svg("rect");
        wait.setAttribute("x", wx1.toFixed(2));
        wait.setAttribute("y", String(rowY + (ROW_H - BAR_H / 2) / 2));
        wait.setAttribute("width", ww.toFixed(2));
        wait.setAttribute("height", String(BAR_H / 2));
        wait.setAttribute("class", "console-dashboard-gantt__wait");
        root.appendChild(wait);
      }
    }
    // A queued pip sits just left of the now-line so it reads as "waiting to start".
    const x = t.state === "queued" ? Math.max(LABEL_W, timeX(now, t0, now, geo) - MIN_BAR_W) : x1;
    const rect = svg("rect");
    rect.setAttribute("x", x.toFixed(2));
    rect.setAttribute("y", String(rowY + (ROW_H - BAR_H) / 2));
    rect.setAttribute("width", w.toFixed(2));
    rect.setAttribute("height", String(BAR_H));
    rect.setAttribute("rx", "2");
    rect.setAttribute("class", "console-dashboard-gantt__bar");
    rect.setAttribute("data-state", t.state);
    const elapsed = t.durationMs > 0 ? t.durationMs : t.startMs != null ? now - t.startMs : 0;
    const dur = fmtDurMs(elapsed);

    // The duration, printed after the bar, with the state word when colour is not enough. A bar's
    // LENGTH is a comparison, not a measurement: it says this took longer than that, within a 60s
    // window, and nothing about how long either actually was.
    //
    // Suppressed for a bar that ends flush against the right edge (a still-running target keeps
    // moving) only when there is no room, so the label never overprints the "now" line.
    const caption = [stateWord(t.state), dur].filter(Boolean).join(" ");
    if (caption) {
      // After the bar when there is room; before it, right-aligned, when the bar runs up against the
      // right edge (most recent bars do). A caption with neither is dropped, never overprinted.
      const textW = caption.length * 7.5;
      const after = x + w + 4;
      const before = x - 4;
      const fitsAfter = after + textW < geo.w - RIGHT_PAD;
      if (fitsAfter || before - textW > LABEL_W) {
        const durText = svg("text");
        durText.setAttribute("x", (fitsAfter ? after : before).toFixed(2));
        if (!fitsAfter) durText.setAttribute("text-anchor", "end");
        durText.setAttribute("y", String(rowY + ROW_H / 2 + 4));
        durText.setAttribute("class", "console-dashboard-gantt__dur");
        durText.textContent = caption;
        root.appendChild(durText);
      }
    }

    // Finished bars with a ref deep-link to the log viewer for that ref, carrying the
    // live host so the viewer can resolve it - same relative path the running-targets
    // tile uses, opened in a new tab so the live board stays put.
    if (t.terminal && t.outputRef && liveHost) {
      const a = svg("a");
      a.setAttribute("href", logsLink(liveHost, { ref: t.outputRef }));
      a.setAttribute("target", "_blank");
      a.setAttribute("rel", "noopener");
      a.setAttribute("class", "console-dashboard-gantt__link");
      a.setAttribute("aria-label", "Open the output of " + t.label);
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
      .map((r) => ({
        ...r,
        targets: r.targets
          .filter((t) => {
            const span = barSpan(t, now);
            return !span || span.e >= t0;
          })
          // Sort by start time so the timeline reads as a top-left to bottom-right cascade;
          // queued/not-yet-started targets (no startMs) sort last, keeping their insertion
          // order via the stable sort.
          .sort((a, b) => (a.startMs ?? Infinity) - (b.startMs ?? Infinity)),
      }))
      .filter((r) => r.targets.length > 0);
    const nTargets = visibleRuns.reduce((n, r) => n + r.targets.length, 0);
    card.setEmpty(nTargets === 0 ? "No runs are active in the last minute." : null);
    if (nTargets === 0) {
      wrap.replaceChildren();
      card.setNote("idle");
      return;
    }

    // One pixel per drawing unit: the width is the scroller's, floored at MIN_W.
    const w = Math.max(MIN_W, Math.floor(wrap.clientWidth));
    const geo: Geo = { w, plotW: w - RIGHT_PAD - LABEL_W };
    const totalH =
      AXIS_H + visibleRuns.reduce((n, r) => n + RUN_H + r.targets.length * ROW_H, 0) + 4;
    const root = svg("svg");
    root.setAttribute("viewBox", "0 0 " + w + " " + totalH);
    root.setAttribute("width", String(w));
    root.setAttribute("height", String(totalH));
    root.setAttribute("class", "console-dashboard-gantt__svg");
    root.setAttribute("role", "img");

    drawAxis(root, t0, now, geo, totalH);

    let y = AXIS_H;
    let running = 0;
    let failed = 0;
    let group = 0;
    for (const run of visibleRuns) {
      // Group banding, drawn first so it sits behind the labels and bars.
      //
      // Zebra at RUN granularity, not row granularity. One tint per invocation blocks its targets
      // into a single readable unit; striping every row would put twenty alternating lines into a
      // tile that is already dense, and the stripes would compete with the bars for attention.
      const groupH = RUN_H + run.targets.length * ROW_H;
      const band = svg("rect");
      band.setAttribute("x", "0");
      band.setAttribute("y", String(y - 2));
      band.setAttribute("width", String(w));
      band.setAttribute("height", String(groupH));
      band.setAttribute("class", "console-dashboard-gantt__runband");
      band.setAttribute("data-zebra", group % 2 === 0 ? "even" : "odd");
      root.appendChild(band);
      group++;

      const head = svg("text");
      // x=8 matches the target labels below, so the header and its rows share one left edge and the
      // group reads as a block.
      head.setAttribute("x", "8");
      head.setAttribute("y", String(y + 16));
      head.setAttribute("class", "console-dashboard-gantt__runlabel");
      // Trigger and invocation id are two different kinds of thing on one line, so they are two
      // tspans rather than one string. The trigger is the word an operator reads; the id is an
      // opaque handle wanted only when copying it.
      const trigger = svg("tspan");
      trigger.setAttribute("class", "console-dashboard-gantt__runtrigger");
      trigger.textContent = run.trigger || "run";
      head.appendChild(trigger);
      if (run.inv) {
        const ref = svg("tspan");
        ref.setAttribute("class", "console-dashboard-gantt__runref");
        ref.setAttribute("dx", "6");
        ref.textContent = run.inv.slice(0, 12);
        head.appendChild(ref);
      }
      root.appendChild(head);

      // A per-run summary, right-aligned on the header row: how many targets, how much wall clock
      // the invocation has burned, and how many of its targets are still going. Wall clock is
      // first-start to last-end (or now, while anything is still running), which is the elapsed
      // time of the INVOCATION rather than the sum of its targets: with work running concurrently,
      // the sum is always larger than the time anyone waited.
      const starts = run.targets.map((t) => t.startMs).filter((v): v is number => v != null);
      const stillRunning = run.targets.filter((t) => t.state === "running").length;
      if (starts.length > 0) {
        const first = Math.min(...starts);
        const ends = run.targets
          .map((t) => (t.state === "running" ? now : t.endMs))
          .filter((v): v is number => v != null);
        const last = ends.length > 0 ? Math.max(...ends) : now;
        const bits = [
          run.targets.length + (run.targets.length === 1 ? " target" : " targets"),
          fmtDurMs(last - first),
        ];
        if (stillRunning > 0) bits.push(stillRunning + " running");
        const summary = svg("text");
        summary.setAttribute("x", String(w - RIGHT_PAD));
        summary.setAttribute("y", String(y + 16));
        summary.setAttribute("text-anchor", "end");
        summary.setAttribute("class", "console-dashboard-gantt__runsummary");
        summary.textContent = bits.join(", ");
        root.appendChild(summary);
      }
      y += RUN_H;
      // When this invocation's first target actually began. Every later target's gap from here is
      // queue wait rather than work, which is what drawBar shades.
      const runStart = run.targets.reduce<number | null>(
        (min, t) => (t.startMs == null ? min : min == null ? t.startMs : Math.min(min, t.startMs)),
        null,
      );
      for (const t of run.targets) {
        if (t.state === "running") running++;
        if (t.state === "failed") failed++;
        const label = svg("text");
        label.setAttribute("x", "8");
        label.setAttribute("y", String(y + ROW_H / 2 + 4));
        label.setAttribute("class", "console-dashboard-gantt__targetlabel");
        label.textContent = truncate(t.label || t.target || "-", LABEL_CHARS);
        const lt = svg("title");
        lt.textContent = t.label;
        label.appendChild(lt);
        root.appendChild(label);
        drawBar(root, t, y, t0, now, runStart, geo);
        y += ROW_H;
      }
    }

    root.setAttribute(
      "aria-label",
      "Execution timeline: " +
        visibleRuns.length +
        (visibleRuns.length === 1 ? " run, " : " runs, ") +
        nTargets +
        (nTargets === 1 ? " target, " : " targets, ") +
        running +
        " running, " +
        failed +
        " failed",
    );
    wrap.replaceChildren(root);
    card.setNote(
      visibleRuns.length +
        (visibleRuns.length === 1 ? " run" : " runs") +
        ", " +
        running +
        " running",
    );
  }

  // A light 1s ticker advances the running bars and rolls the window forward even when
  // no new status frame has arrived; cleared on destroy.
  const ticker = window.setInterval(() => {
    if (runs.length) render();
  }, 1000);
  // The drawing is sized to the scroller, so it is redrawn when the card changes width.
  const resize = new ResizeObserver(() => {
    if (runs.length) render();
  });
  resize.observe(wrap);

  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.status) return;
      runs = s.status.runs;
      liveHost = s.liveHost;
      render();
    },
    destroy() {
      window.clearInterval(ticker);
      resize.disconnect();
    },
  };
}
