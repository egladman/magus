// waterfall.ts - the trace-waterfall (Timeline) view. A Datadog-style waterfall of the
// invocation: the magus OTel model is invocation=trace, target-exec=span, step=child span. The
// magus.viewer.v1 wire format carries no explicit trace_id/span_id, so the span tree is
// reconstructed structurally from what the events DO carry: the Invocation's start/end frame the
// axis; each (project,target) group is a target span (it ends at its RESULT event's time and
// starts result.time - result.duration); and the EXEC events within a target are its step
// child-spans (each starts at its own time and ends at the next EXEC in the same target, or the
// target's end). It renders as inline SVG (presentation attributes + CSS classes, no chart
// library, no external request), supports drag-to-focus (brush) and a wall-clock preset picker.

import { Kind } from "../../gen/magus/viewer/v1/viewer_pb";
import type { Event, Status } from "../../gen/magus/viewer/v1/viewer_pb";
import type { FocusWin, InvocationView, Source, SpanMulti, SpanTree, Step, TargetSpan } from "./state";
import { state, waterfallSource } from "./state";
import { bodyEl, el } from "./dom";
import { cmdLabel, statusName } from "./model";
import { matchAllTexts, targetRelevant } from "./filter";
import { render } from "./render";
import type { Timestamp, Duration } from "@bufbuild/protobuf/wkt";

const WF_NS = "http://www.w3.org/2000/svg";
const WF_VIEW_W = 900;   // viewBox width; the SVG scales to the panel via its viewBox
const WF_LABEL_W = 230;  // left gutter for span labels (indented for steps)
const WF_RIGHT = 64;     // right gutter for the per-target duration text
const WF_AXIS_H = 18;    // top strip for the time axis
const WF_ROW_H = 20;     // one span row
const WF_BAR_H = 11;     // a target bar
const WF_STEP_BAR_H = 7; // a step (child-span) bar
const WF_PLOT_W = WF_VIEW_W - WF_RIGHT - WF_LABEL_W;

// A domain (visible time window) - the full run span or a focus window.
interface Domain {
  t0: number;
  t1: number;
}

// One drawn waterfall row (a target bar or a step child-bar).
interface WfRow {
  label: string;
  s: number;
  e: number;
  status: string;
  step: boolean;
  dim: boolean;
}

function wfSvg(tag: string): SVGElement {
  return document.createElementNS(WF_NS, tag) as SVGElement;
}

// tsMs converts a protobuf Timestamp ({seconds: bigint, nanos: number}) to epoch millis, or
// null when unset (e.g. a still-running invocation's end_time).
export function tsMs(ts: Timestamp | null | undefined): number | null {
  if (!ts) return null;
  return Number(ts.seconds || 0n) * 1000 + Number(ts.nanos || 0) / 1e6;
}

// durMs converts a protobuf Duration to millis (0 when unset).
function durMs(d: Duration | undefined): number {
  if (!d) return 0;
  return Number(d.seconds || 0n) * 1000 + Number(d.nanos || 0) / 1e6;
}

// durMsText renders a raw millisecond span as "12ms" / "1.20s" (the axis/label sibling of
// durText, which takes a Duration message).
function durMsText(ms: number): string {
  if (ms < 1) return "0ms";
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(ms < 10000 ? 2 : 1) + "s";
}

// wfTrunc keeps an SVG label inside its gutter (SVG <text> does not clip); the full text
// rides a <title> tooltip.
function wfTrunc(s: string, max: number): string {
  return s.length > max ? s.slice(0, max - 3) + "..." : s;
}

// buildSpansMulti builds per-invocation span groups (each via buildSpans) and the combined
// time domain across all of them - the shared axis a multi-invocation waterfall plots on.
export function buildSpansMulti(sources: Source[]): SpanMulti {
  const groups: SpanMulti["groups"] = [];
  let t0 = Infinity, t1 = -Infinity;
  for (const s of sources) {
    const sp = buildSpans(s.events, s.invocation);
    if (!sp.targets.length) continue;
    groups.push({ label: cmdLabel(s.invocation && s.invocation.command), targets: sp.targets });
    t0 = Math.min(t0, sp.t0); t1 = Math.max(t1, sp.t1);
  }
  if (!isFinite(t0)) t0 = 0;
  if (!isFinite(t1) || t1 <= t0) t1 = t0 + 1;
  return { t0, t1, groups };
}

// A working target group while reconstructing spans from an event stream.
interface SpanGroupWork {
  project: string;
  target: string;
  execs: { t: number | null; text: string }[];
  result: { t: number | null; dur: number; status: Status; ref: string } | null;
  first: number | null;
  last: number | null;
}

// buildSpans reconstructs the {t0, t1, targets:[{label,status,ref,s,e,steps:[{label,s,e}]}]}
// span tree from an event stream. See the module comment for how each span's window is
// derived; returns an empty targets list when nothing carries plottable timing.
function buildSpans(events: Event[], invocation: InvocationView): SpanTree {
  const groups = new Map<string, SpanGroupWork>();
  const order: SpanGroupWork[] = [];
  let minT = Infinity;
  let maxT = -Infinity;
  for (const ev of events || []) {
    const t = tsMs(ev.time);
    if (t !== null) { if (t < minT) minT = t; if (t > maxT) maxT = t; }
    if (ev.kind !== Kind.EXEC && ev.kind !== Kind.OUTPUT && ev.kind !== Kind.RESULT) continue;
    const key = ev.project + " " + ev.target;
    let g = groups.get(key);
    if (!g) { g = { project: ev.project, target: ev.target, execs: [], result: null, first: null, last: null }; groups.set(key, g); order.push(g); }
    if (t !== null) { if (g.first === null) g.first = t; g.last = t; }
    if (ev.kind === Kind.EXEC) g.execs.push({ t, text: ev.text });
    else if (ev.kind === Kind.RESULT) g.result = { t, dur: durMs(ev.duration), status: ev.status, ref: ev.ref };
  }

  const targets: TargetSpan[] = [];
  for (const g of order) {
    let e = g.result && g.result.t !== null ? g.result.t : g.last;
    const s = g.result && g.result.t !== null && g.result.dur > 0 ? g.result.t - g.result.dur : g.first;
    if (s === null) continue; // no timing at all for this group; nothing to plot
    if (e === null || e < s) e = s;
    const label = (g.project && g.project !== "." ? g.project + ":" : "") + (g.target || "output");
    const steps: Step[] = [];
    const timed = g.execs.filter((x) => x.t !== null) as { t: number; text: string }[];
    for (let i = 0; i < timed.length; i++) {
      const ss = timed[i].t;
      const ee = i + 1 < timed.length ? timed[i + 1].t : e;
      steps.push({ label: timed[i].text || "step", s: ss, e: Math.max(ss, ee) });
    }
    targets.push({ label, status: g.result ? statusName(g.result.status) : "", ref: g.result ? g.result.ref : "", s, e, steps });
  }

  // Axis: the invocation frame when present, else the observed event range. Target spans are
  // always inside this window (wfTimeX clamps regardless), so a missing invocation timestamp
  // just falls back to the data.
  let t0 = tsMs(invocation && invocation.startTime);
  let t1 = tsMs(invocation && invocation.endTime);
  if (t0 === null) t0 = targets.length ? Math.min(minT, ...targets.map((t) => t.s)) : minT;
  if (t1 === null) t1 = targets.length ? Math.max(maxT, ...targets.map((t) => t.e)) : maxT;
  if (!isFinite(t0)) t0 = 0;
  if (!isFinite(t1) || t1 <= t0) t1 = t0 + 1;
  return { t0, t1, targets };
}

// timelineAvailable reports whether the loaded log carries enough timing to plot a waterfall
// (at least one target span). Gates the Timeline toolbar button.
export function timelineAvailable(): boolean {
  return buildSpansMulti(waterfallSource()).groups.length > 0;
}

function wfTimeX(t: number, sp: Domain): number {
  const span = sp.t1 - sp.t0 || 1;
  const c = Math.min(sp.t1, Math.max(sp.t0, t));
  return WF_LABEL_W + ((c - sp.t0) / span) * WF_PLOT_W;
}

// renderWaterfall draws the current invocation's span tree into the log body as inline SVG
// (presentation attributes + CSS classes for color, no chart library, no external request).
export function renderWaterfall(): void {
  const multi = buildSpansMulti(waterfallSource());
  if (!multi.groups.length) {
    const p = document.createElement("p");
    p.className = "console-log-waterfall__empty";
    p.textContent = "This log has no timing data to plot as a waterfall.";
    bodyEl.appendChild(p);
    return;
  }

  const total = multi.t1 - multi.t0;
  // dom is the visible time domain: the focus window (clamped to the run) when set, else the
  // full span across all invocations. drawWfAxis/drawWfRow scale to dom, so a focus window
  // zooms; the shared axis is what makes the time range meaningful across invocations.
  const dom = focusFor(multi);
  const q = state.filterParsed;
  const filtering = !q.empty;
  const outOfWin = (s: number, e: number): boolean => !!state.focusWin && (e < dom.t0 || s > dom.t1);
  // Multiple invocations get a labelled group header each; a single one renders headerless.
  const showHeaders = multi.groups.length > 1;

  const allTargets = multi.groups.flatMap((g) => g.targets);
  const nt = allTargets.length;
  const nMatch = filtering ? allTargets.filter((t) => targetRelevant(q, t)).length : nt;
  const caption = document.createElement("p");
  caption.className = "console-log-waterfall__caption";
  caption.textContent = (showHeaders ? multi.groups.length + " invocations, " : "") +
    nt + (nt === 1 ? " target" : " targets") + " over " + durMsText(total) +
    (filtering ? " - " + nMatch + " matching" : "") +
    (state.focusWin ? " - focused to " + durMsText(dom.t1 - dom.t0) : "");
  bodyEl.appendChild(caption);

  let rows = 0;
  for (const g of multi.groups) { rows += showHeaders ? 1 : 0; for (const t of g.targets) rows += 1 + t.steps.length; }
  const h = WF_AXIS_H + rows * WF_ROW_H + 6;

  const root = wfSvg("svg") as SVGSVGElement;
  root.setAttribute("viewBox", "0 0 " + WF_VIEW_W + " " + h);
  root.setAttribute("class", "console-log-waterfall__svg");
  root.setAttribute("preserveAspectRatio", "xMinYMin meet");
  root.setAttribute("role", "img");
  root.setAttribute("aria-label", "Invocation trace waterfall");

  drawWfAxis(root, dom, h);

  let y = WF_AXIS_H + 2;
  for (const g of multi.groups) {
    if (showHeaders) { drawWfGroupHead(root, g.label, y); y += WF_ROW_H; }
    for (const t of g.targets) {
      // Dim non-matching (filter) OR out-of-window (time focus) spans instead of removing them,
      // so the row layout stays stable and the waterfall reads as a focused view.
      const tRelevant = targetRelevant(q, t);
      const tDim = (filtering && !tRelevant) || outOfWin(t.s, t.e);
      drawWfRow(root, { label: t.label, s: t.s, e: t.e, status: t.status, step: false, dim: tDim }, y, dom);
      y += WF_ROW_H;
      for (const st of t.steps) {
        const sRelevant = tRelevant && (q.texts.length === 0 || matchAllTexts(q, st.label) || matchAllTexts(q, t.label));
        const sDim = (filtering && !sRelevant) || outOfWin(st.s, st.e);
        drawWfRow(root, { label: st.label, s: st.s, e: st.e, status: "", step: true, dim: sDim }, y, dom);
        y += WF_ROW_H;
      }
    }
  }
  attachWfBrush(root, dom, h);
  bodyEl.appendChild(root);
  updateFocusUI(multi);
}

// drawWfGroupHead draws an invocation group header row (the command), spanning the label gutter.
function drawWfGroupHead(root: SVGSVGElement, label: string, y: number): void {
  const t = wfSvg("text");
  t.setAttribute("x", "4");
  t.setAttribute("y", String(y + WF_ROW_H / 2 + 3));
  t.setAttribute("class", "console-log-waterfall__grouphead");
  t.textContent = wfTrunc(label || "invocation", 48);
  root.appendChild(t);
}

// focusFor clamps the active focus window to the run's span, or returns the full domain.
function focusFor(spans: Domain): Domain {
  if (!state.focusWin) return { t0: spans.t0, t1: spans.t1 };
  const t0 = Math.max(spans.t0, Math.min(state.focusWin.a, state.focusWin.b));
  const t1 = Math.min(spans.t1, Math.max(state.focusWin.a, state.focusWin.b));
  return t1 - t0 >= 1 ? { t0, t1 } : { t0: spans.t0, t1: spans.t1 };
}

// attachWfBrush lets you drag horizontally across the waterfall to set a focus window (the
// Datadog trace-zoom gesture). It converts client x -> SVG x -> time via the current domain,
// draws a selection rect, and on release sets focusWin and re-renders. A tiny drag is a no-op.
function attachWfBrush(root: SVGSVGElement, dom: Domain, h: number): void {
  let sx: number | null = null;
  let rect: SVGElement | null = null;
  const toTime = (clientX: number): number => {
    const r = root.getBoundingClientRect();
    const svgX = (clientX - r.left) * (WF_VIEW_W / r.width);
    const frac = (svgX - WF_LABEL_W) / WF_PLOT_W;
    return dom.t0 + Math.min(1, Math.max(0, frac)) * (dom.t1 - dom.t0);
  };
  const svgXOf = (clientX: number): number => {
    const r = root.getBoundingClientRect();
    return Math.min(WF_VIEW_W, Math.max(WF_LABEL_W, (clientX - r.left) * (WF_VIEW_W / r.width)));
  };
  root.addEventListener("pointerdown", (ev) => {
    if (ev.button !== 0) return;
    sx = ev.clientX;
    rect = wfSvg("rect");
    rect.setAttribute("class", "console-log-waterfall__brush");
    rect.setAttribute("y", "0");
    rect.setAttribute("height", String(h));
    const x = svgXOf(sx);
    rect.setAttribute("x", String(x)); rect.setAttribute("width", "0");
    root.appendChild(rect);
    root.setPointerCapture(ev.pointerId);
  });
  root.addEventListener("pointermove", (ev) => {
    if (sx === null || !rect) return;
    const a = svgXOf(sx), b = svgXOf(ev.clientX);
    rect.setAttribute("x", String(Math.min(a, b)));
    rect.setAttribute("width", String(Math.abs(b - a)));
  });
  const finish = (ev: PointerEvent): void => {
    if (sx === null) return;
    const a = toTime(sx), b = toTime(ev.clientX);
    sx = null; if (rect) { rect.remove(); rect = null; }
    if (Math.abs(b - a) < (dom.t1 - dom.t0) * 0.01) return; // too small: treat as a click
    state.focusWin = { a: Math.min(a, b), b: Math.max(a, b) };
    const sel = el("time-range"); if (sel) (sel as HTMLSelectElement).value = "custom";
    render();
  };
  root.addEventListener("pointerup", finish);
  root.addEventListener("pointercancel", () => { sx = null; if (rect) { rect.remove(); rect = null; } });
}

// updateFocusUI reflects the focus window into the readout + reset, and enables the time-range
// picker only in waterfall mode (where a time window is meaningful).
export function updateFocusUI(spans: SpanMulti | null): void {
  const sel = el("time-range");
  const win = el("console-log-focus__window");
  const reset = el("console-log-focus__reset");
  const active = state.timeline && !!spans && !!spans.groups && spans.groups.length > 0;
  if (sel) (sel as HTMLSelectElement).disabled = !active;
  if (win) {
    if (state.focusWin && active) { const d = focusFor(spans!); win.textContent = durMsText(d.t1 - d.t0) + " window"; win.hidden = false; }
    else win.hidden = true;
  }
  if (reset) reset.hidden = !(state.focusWin && active);
}

// clearFocus resets to the full run.
export function clearFocus(): void {
  state.focusWin = null;
  const sel = el("time-range"); if (sel) (sel as HTMLSelectElement).value = "all";
  if (state.model) render();
}

// applyTimeRange sets the focus window from a wall-clock preset (seconds back from the latest
// event), or clears it for "all". Invocation-agnostic: it is a window over event time.
export function applyTimeRange(value: string): void {
  if (value === "all" || value === "custom") { if (value === "all") state.focusWin = null; if (state.model) render(); return; }
  const secs = Number(value);
  const multi = buildSpansMulti(waterfallSource());
  if (!multi.groups.length || !Number.isFinite(secs)) return;
  state.focusWin = { a: multi.t1 - secs * 1000, b: multi.t1 };
  if (state.model) render();
}

function drawWfAxis(root: SVGSVGElement, sp: Domain, h: number): void {
  const line = wfSvg("line");
  line.setAttribute("x1", String(WF_LABEL_W));
  line.setAttribute("x2", String(WF_VIEW_W - WF_RIGHT));
  line.setAttribute("y1", String(WF_AXIS_H));
  line.setAttribute("y2", String(WF_AXIS_H));
  line.setAttribute("class", "console-log-waterfall__axisline");
  root.appendChild(line);
  const total = sp.t1 - sp.t0;
  const ticks: [number, string][] = [[sp.t0, "0"], [sp.t0 + total / 2, durMsText(total / 2)], [sp.t1, durMsText(total)]];
  for (const [t, txt] of ticks) {
    const x = wfTimeX(t, sp);
    const grid = wfSvg("line");
    grid.setAttribute("x1", String(x));
    grid.setAttribute("x2", String(x));
    grid.setAttribute("y1", String(WF_AXIS_H));
    grid.setAttribute("y2", String(h));
    grid.setAttribute("class", "console-log-waterfall__grid");
    root.appendChild(grid);
    const label = wfSvg("text");
    const atEnd = t === sp.t1;
    label.setAttribute("x", String(atEnd ? x - 2 : x + 2));
    label.setAttribute("y", "11");
    label.setAttribute("class", "console-log-waterfall__axislabel");
    if (atEnd) label.setAttribute("text-anchor", "end");
    label.textContent = txt;
    root.appendChild(label);
  }
}

function drawWfRow(root: SVGSVGElement, row: WfRow, y: number, sp: Domain): void {
  const dur = row.e - row.s;
  const dim = row.dim; // dimmed (data-dim) when the filter excludes this span
  const label = wfSvg("text");
  label.setAttribute("x", String(row.step ? 20 : 6));
  label.setAttribute("y", String(y + WF_ROW_H / 2 + 3));
  label.setAttribute("class", "console-log-waterfall__label " + (row.step ? "console-log-waterfall__label--step" : "console-log-waterfall__label--target"));
  if (dim) label.setAttribute("data-dim", "");
  label.textContent = wfTrunc(row.label || "-", row.step ? 30 : 26);
  const lt = wfSvg("title");
  lt.textContent = row.label + " (" + durMsText(dur) + ")";
  label.appendChild(lt);
  root.appendChild(label);

  const x1 = wfTimeX(row.s, sp);
  const x2 = wfTimeX(row.e, sp);
  const bh = row.step ? WF_STEP_BAR_H : WF_BAR_H;
  const rect = wfSvg("rect");
  rect.setAttribute("x", x1.toFixed(2));
  rect.setAttribute("y", String(y + (WF_ROW_H - bh) / 2));
  rect.setAttribute("width", Math.max(2, x2 - x1).toFixed(2));
  rect.setAttribute("height", String(bh));
  rect.setAttribute("rx", "2");
  rect.setAttribute("class", "console-log-waterfall__bar" + (row.step ? " console-log-waterfall__bar--step" : ""));
  if (row.status) rect.setAttribute("data-status", row.status);
  if (dim) rect.setAttribute("data-dim", "");
  const rt = wfSvg("title");
  rt.textContent = row.label + " - " + durMsText(dur);
  rect.appendChild(rt);
  root.appendChild(rect);

  // Duration text in the right gutter, target rows only (step rows would crowd it).
  if (!row.step) {
    const d = wfSvg("text");
    d.setAttribute("x", String(WF_VIEW_W - 2));
    d.setAttribute("y", String(y + WF_ROW_H / 2 + 3));
    d.setAttribute("class", "console-log-waterfall__dur");
    if (dim) d.setAttribute("data-dim", "");
    d.setAttribute("text-anchor", "end");
    d.textContent = durMsText(dur);
    root.appendChild(d);
  }
}

// FocusWin re-export kept local; nothing outside needs it.
export type { FocusWin };
