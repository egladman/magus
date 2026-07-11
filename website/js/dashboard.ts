// dashboard.ts - magus's control surface.
//
// Two live feeds from the loopback daemon, both locked to 127.0.0.1/[::1] and both
// authenticated with the SAME bearer token that arrives in the URL fragment
// (#live=host:port&token=), which the browser never transmits:
//
//   1. `/api/v1/events` SSE (`event: status`) decodes magus.status.v1 for the
//      instantaneous view: health pill, pool-occupancy grid, in-flight calls,
//      loaded workspaces, and live cache tallies. This is the connection whose
//      open/close drives the connected/disconnected pill.
//   2. magus.metrics.v1.MetricsService.StreamMetrics over ConnectRPC (browser-native
//      Connect protocol, NOT gRPC) for the developer-grade health view: latency
//      percentiles per operation family, remote-cache transfer stats, and a
//      backfilled Sample history. The first stream message is a Backfill (history),
//      then a Snapshot per ~1s tick.
//
// The utilization history grid and the cache hit-rate chart are seeded from the
// metrics Backfill, then kept live by synthesizing one Sample per status frame
// (the status stream carries live pool.in_use/capacity/waiting + cache tallies;
// metrics Snapshots carry the latency/remote families the status stream does not).
//
// The security-critical live helpers (validateLiveHost / consumeLiveToken /
// fetchSSE) mirror js/graph-explorer.js so all three pages enforce the same
// loopback lock and share one sessionStorage token key.

import { fromBinary } from "@bufbuild/protobuf";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { createClient, type Client, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import uPlot from "uplot";
import { StatusSchema, Health, type Status, type Pool } from "./gen/magus/status/v1/status_pb";
import {
  MetricsService,
  type Snapshot,
  type Latency,
  type Sample as ProtoSample,
} from "./gen/magus/metrics/v1/metrics_pb";
import "./nav.js"; // reuse the site's exact nav dropdown behavior (hamburger <-> X, dismiss)

// el asserts the element exists - every id referenced here is authored in
// dashboard.html, so a missing one is a build-time contract break, not a runtime
// branch. The genuinely optional handles (offline-badge, chart containers before
// the panels are revealed) are guarded at their call sites.
const el = (id: string): HTMLElement => document.getElementById(id) as HTMLElement;
const opt = (id: string): HTMLElement | null => document.getElementById(id);
function setText(id: string, text: string): void {
  const e = opt(id);
  if (e) e.textContent = text;
}

// Super-basic local persistence (localStorage): the remembered daemon host so a reload
// resumes without re-opening the printed link, and the set of collapsed cards. Nothing
// high-stakes - all wrapped so a storage-disabled browser degrades to no persistence.
const LS_DAEMON = "magus-dashboard-daemon";
const LS_COLLAPSED = "magus-dashboard-collapsed";
const DISCONNECT_GRACE = 3; // consecutive stream failures before the pill flips to "disconnected"

function saveDaemon(host: string): void { try { localStorage.setItem(LS_DAEMON, host); } catch { /* ignore */ } }
function savedDaemon(): string | null { try { return localStorage.getItem(LS_DAEMON); } catch { return null; } }
function forgetDaemon(): void { try { localStorage.removeItem(LS_DAEMON); } catch { /* ignore */ } }

function loadCollapsed(): Set<string> {
  try { return new Set(JSON.parse(localStorage.getItem(LS_COLLAPSED) || "[]") as string[]); } catch { return new Set(); }
}
function saveCollapsed(set: Set<string>): void {
  try { localStorage.setItem(LS_COLLAPSED, JSON.stringify([...set])); } catch { /* ignore */ }
}

// ---- hash params -----------------------------------------------------------

type HashParams = Record<string, string>;

function parseHash(): HashParams {
  const h = location.hash.replace(/^#/, "");
  const params: HashParams = {};
  for (const part of h.split("&")) {
    if (!part) continue;
    const i = part.indexOf("=");
    if (i < 0) { params[part] = ""; continue; }
    params[decodeURIComponent(part.slice(0, i))] = decodeURIComponent(part.slice(i + 1));
  }
  return params;
}

// ---- live host validation / token (mirror graph-explorer.js) ---------------

function validateLiveHost(hostPort: string): string | null {
  let u: URL;
  try {
    u = new URL("http://" + hostPort);
  } catch {
    return null;
  }
  if (u.username || u.password) return null;
  if (u.pathname !== "/" || u.search || u.hash) return null;
  if (u.hostname !== "127.0.0.1" && u.hostname !== "::1" && u.hostname !== "[::1]") return null;
  return u.host;
}

function consumeLiveToken(params: HashParams): void {
  if (!params.token) return;
  const remember = localStorage.getItem("magus-live-remember") === "1";
  if (remember) {
    localStorage.setItem("magus-live-token", params.token);
  } else {
    sessionStorage.setItem("magus-live-token", params.token);
  }
  const kept: string[] = [];
  for (const k of Object.keys(params)) {
    if (k === "token") continue;
    kept.push(k + "=" + encodeURIComponent(params[k]));
  }
  const next = kept.length ? "#" + kept.join("&") : "#";
  history.replaceState(null, "", location.pathname + location.search + next);
}

function getLiveToken(): string | null {
  return sessionStorage.getItem("magus-live-token") || localStorage.getItem("magus-live-token") || null;
}

type SSEHeaders = Record<string, string>;

async function fetchSSE(
  url: string,
  headers: SSEHeaders,
  onEvent: (type: string, data: string) => void,
  onError: (e: Error) => void,
  signal: AbortSignal,
  onOpen?: () => void,
): Promise<void> {
  let response: Response;
  try {
    response = await fetch(url, { headers, signal });
  } catch (e) {
    if (e instanceof Error && e.name === "AbortError") return;
    onError(e instanceof Error ? e : new Error(String(e)));
    return;
  }
  if (!response.ok) {
    onError(new Error("HTTP " + response.status));
    return;
  }
  if (onOpen) onOpen();
  if (!response.body) { onError(new Error("no stream body")); return; }
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) { onError(new Error("stream ended")); return; }
      buf += value;
      let boundary: number;
      while ((boundary = buf.indexOf("\n\n")) >= 0) {
        const chunk = buf.slice(0, boundary);
        buf = buf.slice(boundary + 2);
        if (!chunk.trim()) continue;
        let eventType = "message", data = "";
        for (const line of chunk.split("\n")) {
          if (line.startsWith("event:")) eventType = line.slice(6).replace(/^ /, "").trim();
          else if (line.startsWith("data:")) data = line.slice(5).replace(/^ /, "").trim();
        }
        onEvent(eventType, data);
      }
    }
  } catch (e) {
    if (!(e instanceof Error) || e.name !== "AbortError") onError(e instanceof Error ? e : new Error(String(e)));
  }
}

// ---- formatting ------------------------------------------------------------

const HEALTH: Record<number, { label: string; cls: string }> = {
  [Health.HEALTHY]: { label: "healthy", cls: "ok" },
  [Health.DEGRADED]: { label: "degraded", cls: "warn" },
  [Health.DOWN]: { label: "down", cls: "fail" },
  [Health.UNSPECIFIED]: { label: "unknown", cls: "" },
};

type ConnState = "connecting" | "connected" | "disconnected" | "none";

function setConn(state: ConnState, detail?: string): void {
  const c = el("dash-conn");
  const map: Record<string, string> = {
    connecting: "connecting...", connected: "connected",
    disconnected: detail || "reconnecting", none: "not connected",
  };
  c.textContent = map[state] || state;
  c.dataset.state = state;
}

function fmtArgs(args: string[] | undefined): string {
  return (args && args.length) ? "magus " + args.join(" ") : "magus";
}

function fmtBytes(n: number | bigint): string {
  let v = Number(n || 0);
  if (v <= 0) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return (v < 10 && i > 0 ? v.toFixed(1) : Math.round(v)) + " " + u[i];
}

function fmtCount(n: number | bigint): string {
  return Number(n || 0).toLocaleString();
}

// fmtDur renders a duration given in SECONDS as a human, plain-ASCII string
// (us / ms / s). The metrics wire carries every latency in seconds.
function fmtDur(sec: number | null | undefined): string {
  if (sec == null || sec <= 0) return "-";
  const ms = sec * 1000;
  if (ms < 1) return Math.round(ms * 1000) + " us";
  if (ms < 1000) return (ms < 10 ? ms.toFixed(1) : Math.round(ms).toString()) + " ms";
  return sec.toFixed(sec < 10 ? 2 : 1) + " s";
}

function tsMillis(ts: Timestamp | undefined): number {
  if (!ts) return Date.now();
  return Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
}
function tsSeconds(ts: Timestamp | undefined): number {
  if (!ts) return Date.now() / 1000;
  return Number(ts.seconds) + (ts.nanos || 0) / 1e9;
}

function relTime(ts: Timestamp | undefined): string {
  if (!ts) return "";
  const secs = Math.max(0, Math.round((Date.now() - tsMillis(ts)) / 1000));
  if (secs < 60) return secs + "s";
  const mins = Math.round(secs / 60);
  if (mins < 60) return mins + "m";
  return Math.round(mins / 60) + "h";
}

function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString();
}

function cssVar(name: string, fallback = "#888"): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

// ---- sample history (utilization grid + cache-rate chart) ------------------
// One unified Sample shape fed from two sources: the metrics Backfill (history)
// and a live synthesis per status frame. Counters are cumulative; the cache-rate
// chart diffs adjacent samples for a per-interval rate.

interface Sample {
  at: number;        // ms
  inUse: number;
  capacity: number;  // 0 = unlimited
  waiting: number;
  cacheHits: number;
  cacheMisses: number;
}

const GRID_ROWS = 7;
const GRID_MAX = GRID_ROWS * 52; // ~a GitHub year of columns; the rolling window
let samples: Sample[] = [];
let peakInUse = 1;   // for coloring an unlimited pool (capacity 0) by relative load
let seeded = false;  // Backfill seeds once; reconnects don't wipe the live history
let lastSampleAt = 0;

function toSample(s: ProtoSample): Sample {
  return {
    at: tsMillis(s.at),
    inUse: s.inUse,
    capacity: s.capacity,
    waiting: s.waiting,
    cacheHits: Number(s.cacheHits),
    cacheMisses: Number(s.cacheMisses),
  };
}

function seedSamples(history: ProtoSample[]): void {
  if (seeded) return;
  seeded = true;
  const hist = history.map(toSample);
  samples = hist.concat(samples); // any live samples appended before the Backfill land after history
  if (samples.length > GRID_MAX) samples = samples.slice(samples.length - GRID_MAX);
  for (const s of samples) if (s.inUse > peakInUse) peakInUse = s.inUse;
  if (samples.length) lastSampleAt = samples[samples.length - 1].at;
  renderGrid();
  renderRateChart();
}

// appendSample records a synthesized live Sample, throttled to ~1/s so a burst of
// status frames doesn't flood the grid.
function appendSample(s: Sample): void {
  if (samples.length && s.at - lastSampleAt < 900) return;
  lastSampleAt = s.at;
  samples.push(s);
  if (s.inUse > peakInUse) peakInUse = s.inUse;
  if (samples.length > GRID_MAX) samples.shift();
  renderGrid();
  renderRateChart();
}

// ---- pool utilization grid (GitHub-contribution-style SVG) ------------------

const SVGNS = "http://www.w3.org/2000/svg";

// utilColor maps a sample to a fill + opacity ramp (the hand-rolled linear scale,
// so no d3-scale dependency). Busy = info color; a queued sample (waiting > 0)
// switches to the miss color to flag saturation. An unlimited pool (capacity 0)
// is colored by load relative to the observed peak.
function utilColor(s: Sample): { fill: string; opacity: number } {
  let u: number;
  if (s.capacity > 0) u = Math.min(1, s.inUse / s.capacity);
  else u = s.inUse > 0 ? Math.min(1, s.inUse / Math.max(peakInUse, 1)) : 0;
  const base = s.waiting > 0 ? cssVar("--c-miss") : cssVar("--c-info");
  const opacity = s.inUse <= 0 && s.waiting <= 0 ? 0.06 : 0.15 + 0.85 * u;
  return { fill: base, opacity };
}

function renderGrid(): void {
  const host = opt("util-grid");
  if (!host) return;
  const SQ = 12, GAP = 3;
  const n = samples.length;
  const cols = Math.max(1, Math.ceil(n / GRID_ROWS));
  const w = Math.max(1, cols * (SQ + GAP) - GAP);
  const h = Math.max(1, GRID_ROWS * (SQ + GAP) - GAP);
  const svg = document.createElementNS(SVGNS, "svg");
  svg.setAttribute("viewBox", `0 0 ${w} ${h}`);
  svg.setAttribute("class", "util-svg");
  svg.setAttribute("preserveAspectRatio", "xMinYMin meet");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "Pool utilization history");
  const frag = document.createDocumentFragment();
  for (let i = 0; i < n; i++) {
    const s = samples[i];
    const col = Math.floor(i / GRID_ROWS), row = i % GRID_ROWS;
    const { fill, opacity } = utilColor(s);
    const r = document.createElementNS(SVGNS, "rect");
    r.setAttribute("x", String(col * (SQ + GAP)));
    r.setAttribute("y", String(row * (SQ + GAP)));
    r.setAttribute("width", String(SQ));
    r.setAttribute("height", String(SQ));
    r.setAttribute("rx", "2");
    r.setAttribute("fill", fill);
    r.setAttribute("fill-opacity", opacity.toFixed(3));
    r.setAttribute("class", "util-sq");
    const title = document.createElementNS(SVGNS, "title");
    const cap = s.capacity > 0 ? `${s.inUse}/${s.capacity}` : `${s.inUse} (unlimited)`;
    title.textContent = `${clock(s.at)} - ${cap} in use${s.waiting > 0 ? ", " + s.waiting + " waiting" : ""}`;
    r.appendChild(title);
    frag.appendChild(r);
  }
  svg.appendChild(frag);
  host.replaceChildren(svg);
  setText("util-note", n ? `${n} samples, newest ${clock(samples[n - 1].at)}` : "no samples yet");
}

// ---- uPlot charts ----------------------------------------------------------
// Four per-family latency charts (p50/p95/p99 over time) plus one cache hit-rate
// chart. Colors are read from CSS variables so light/dark carries over; a theme
// change rebuilds the charts with fresh colors. Instances are updated in place
// with setData on each tick, never re-created per snapshot.

const LAT_KEYS = ["target", "cacheOp", "poolWait", "graphQuery"] as const;
type LatKey = typeof LAT_KEYS[number];
const LAT_META: Record<LatKey, { chart: string; ro: string; label: string }> = {
  target: { chart: "chart-target", ro: "ro-target", label: "Target" },
  cacheOp: { chart: "chart-cacheop", ro: "ro-cacheop", label: "Cache op" },
  poolWait: { chart: "chart-poolwait", ro: "ro-poolwait", label: "Pool wait" },
  graphQuery: { chart: "chart-graphquery", ro: "ro-graphquery", label: "Graph query" },
};

interface LatSeries { t: number[]; p50: number[]; p95: number[]; p99: number[]; }
const CHART_HISTORY = 240; // points kept per chart (~4 min at 1s)
const latData: Record<LatKey, LatSeries> = {
  target: { t: [], p50: [], p95: [], p99: [] },
  cacheOp: { t: [], p50: [], p95: [], p99: [] },
  poolWait: { t: [], p50: [], p95: [], p99: [] },
  graphQuery: { t: [], p50: [], p95: [], p99: [] },
};
const latCharts: Partial<Record<LatKey, uPlot>> = {};

interface RateSeries { t: number[]; rate: (number | null)[]; }
const rateData: RateSeries = { t: [], rate: [] };
let rateChart: uPlot | null = null;
let chartsBuilt = false;

const CHART_HEIGHT = 132;

function containerWidth(id: string): number {
  const c = opt(id);
  return Math.max(160, (c && c.clientWidth) || 560);
}

function axisBase(): uPlot.Axis {
  return {
    stroke: cssVar("--pico-muted-color"),
    grid: { stroke: cssVar("--pico-muted-border-color"), width: 0.5 },
    ticks: { stroke: cssVar("--pico-muted-border-color"), width: 0.5 },
    font: "11px " + cssVar("--pico-font-family", "system-ui, sans-serif"),
  };
}

function makeLatChart(key: LatKey): uPlot | null {
  const container = opt(LAT_META[key].chart);
  if (!container) return null;
  const yAxis: uPlot.Axis = { ...axisBase(), size: 54, values: (_u, splits) => splits.map((v) => fmtDur(v as number)) };
  const opts: uPlot.Options = {
    width: containerWidth(LAT_META[key].chart),
    height: CHART_HEIGHT,
    legend: { show: false },
    cursor: { points: { size: 5 }, focus: { prox: 16 } },
    scales: { x: { time: true } },
    axes: [axisBase(), yAxis],
    series: [
      {},
      { label: "p50", stroke: cssVar("--c-info"), width: 1.5, points: { show: false } },
      { label: "p95", stroke: cssVar("--c-miss"), width: 1.5, points: { show: false } },
      { label: "p99", stroke: cssVar("--c-err"), width: 1.5, points: { show: false } },
    ],
  };
  const d = latData[key];
  return new uPlot(opts, [d.t, d.p50, d.p95, d.p99] as uPlot.AlignedData, container);
}

function makeRateChart(): uPlot | null {
  const container = opt("chart-rate");
  if (!container) return null;
  const yAxis: uPlot.Axis = { ...axisBase(), size: 44, values: (_u, splits) => splits.map((v) => v + "%") };
  const opts: uPlot.Options = {
    width: containerWidth("chart-rate"),
    height: CHART_HEIGHT,
    legend: { show: false },
    cursor: { points: { size: 5 }, focus: { prox: 16 } },
    scales: { x: { time: true }, y: { range: [0, 100] } },
    axes: [axisBase(), yAxis],
    series: [
      {},
      { label: "hit rate", stroke: cssVar("--c-hit"), fill: cssVar("--c-hit") + "22", width: 1.75, points: { show: false } },
    ],
  };
  return new uPlot(opts, [rateData.t, rateData.rate] as uPlot.AlignedData, container);
}

// ensureCharts builds the uPlot instances the first time the panels are visible
// (a chart created while its container is display:none measures 0 wide).
function ensureCharts(): void {
  if (chartsBuilt) return;
  if (opt("dash-panels")?.hidden) return;
  for (const k of LAT_KEYS) { const u = makeLatChart(k); if (u) latCharts[k] = u; }
  rateChart = makeRateChart();
  chartsBuilt = true;
}

function feedLatChart(key: LatKey): void {
  const d = latData[key];
  latCharts[key]?.setData([d.t, d.p50, d.p95, d.p99] as uPlot.AlignedData);
}

function resizeCharts(): void {
  for (const k of LAT_KEYS) {
    latCharts[k]?.setSize({ width: containerWidth(LAT_META[k].chart), height: CHART_HEIGHT });
  }
  rateChart?.setSize({ width: containerWidth("chart-rate"), height: CHART_HEIGHT });
}

function destroyCharts(): void {
  for (const k of LAT_KEYS) { latCharts[k]?.destroy(); delete latCharts[k]; }
  if (rateChart) { rateChart.destroy(); rateChart = null; }
  chartsBuilt = false;
}

function rebuildCharts(): void {
  if (!chartsBuilt) return;
  destroyCharts();
  ensureCharts();
}

// A theme change swaps the CSS-variable colors; detect it and rebuild the charts
// (and recolor the grid) so both themes read correctly.
let themeSig = "";
function themeColorsSig(): string {
  return [cssVar("--pico-color"), cssVar("--pico-muted-color"), cssVar("--c-info"), cssVar("--c-hit"), cssVar("--c-miss")].join("|");
}
function watchTheme(): void {
  themeSig = themeColorsSig();
  const check = (): void => {
    const s = themeColorsSig();
    if (s === themeSig) return;
    themeSig = s;
    rebuildCharts();
    renderGrid();
  };
  new MutationObserver(check).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", check);
}

// ---- metrics snapshot handling ---------------------------------------------

function capLat(d: LatSeries): void {
  if (d.t.length <= CHART_HISTORY) return;
  const drop = d.t.length - CHART_HISTORY;
  d.t.splice(0, drop); d.p50.splice(0, drop); d.p95.splice(0, drop); d.p99.splice(0, drop);
}

function onSnapshot(snap: Snapshot): void {
  const t = tsSeconds(snap.capturedAt);
  for (const key of LAT_KEYS) {
    const lat = snap[key] as Latency | undefined;
    if (!lat) continue;
    const d = latData[key];
    d.t.push(t); d.p50.push(lat.p50); d.p95.push(lat.p95); d.p99.push(lat.p99);
    capLat(d);
    feedLatChart(key);
    setText(
      LAT_META[key].ro,
      `count ${fmtCount(lat.count)}  ·  p50 ${fmtDur(lat.p50)}  ·  p95 ${fmtDur(lat.p95)}  ·  p99 ${fmtDur(lat.p99)}  ·  max ${fmtDur(lat.max)}`,
    );
  }

  const r = snap.remote;
  if (r) {
    const hits = Number(r.hits), misses = Number(r.misses);
    const tot = hits + misses;
    setText("rem-hits", fmtCount(hits));
    setText("rem-misses", fmtCount(misses));
    setText("rem-errors", fmtCount(r.errors));
    setText("rem-rate", tot > 0 ? Math.round((hits / tot) * 100) + "%" : "-");
    setText("rem-p50", fmtDur(r.durationP50));
    setText("rem-p95", fmtDur(r.durationP95));
    setText("rem-io", fmtCount(r.ioCount));
    setText("rem-bytes", fmtBytes(r.bytesTotal));
  }
}

// renderRateChart derives a per-interval cache hit-rate from adjacent Sample diffs
// (cumulative counters), so the line reflects live activity, not lifetime totals.
function renderRateChart(): void {
  rateData.t.length = 0;
  rateData.rate.length = 0;
  for (let i = 1; i < samples.length; i++) {
    const a = samples[i - 1], b = samples[i];
    const dh = Math.max(0, b.cacheHits - a.cacheHits);
    const dm = Math.max(0, b.cacheMisses - a.cacheMisses);
    const total = dh + dm;
    rateData.t.push(b.at / 1000);
    rateData.rate.push(total > 0 ? (dh / total) * 100 : null);
  }
  rateChart?.setData([rateData.t, rateData.rate] as uPlot.AlignedData);
}

// ---- status rendering (SSE) ------------------------------------------------

let liveHost: string | null = null; // set once connected; used to deep-link running calls into live logs

const SLOT_CAP = 256; // soft cap on rendered cubes so a huge pool never bloats the DOM

// renderSlots draws the concurrency pool as an occupancy grid: one cube per capacity slot,
// filled when in use, plus dashed cubes for tasks queued waiting on a slot (airplane-seating
// read). An unlimited pool (capacity 0) shows one cube per in-use slot.
function renderSlots(pool: Pool | undefined): void {
  const cap = pool ? pool.capacity : 0;
  const used = pool ? pool.inUse : 0;
  const waiting = pool ? pool.waiting : 0;
  setText("pool-summary", cap > 0 ? `${used} / ${cap} slots` : `${used} in use, unlimited`);

  const total = Math.min((cap > 0 ? cap : used) + waiting, SLOT_CAP);
  const frag = document.createDocumentFragment();
  for (let i = 0; i < total; i++) {
    const s = document.createElement("div");
    const slots = cap > 0 ? cap : used;
    s.className = "slot" + (i < used ? " busy" : i >= slots ? " waiting" : "");
    frag.append(s);
  }
  el("slot-grid").replaceChildren(frag);
  el("wait-legend").hidden = waiting === 0;
  setText("wait-count", String(waiting));
}

function renderStatus(st: Status): void {
  el("dash-connect").hidden = true;
  el("dash-panels").hidden = false;
  ensureCharts();

  const h = HEALTH[st.health] || HEALTH[Health.UNSPECIFIED];
  const badge = el("dash-health");
  badge.hidden = false;
  badge.textContent = h.label;
  badge.dataset.health = h.cls;

  const pool = st.pool;
  const mode = el("dash-mode");
  if (pool && pool.mode) { mode.textContent = pool.mode; mode.hidden = false; } else { mode.hidden = true; }
  renderSlots(pool);

  // Cache activity (pool-wide aggregate).
  const cache = pool && pool.cache;
  const hits = cache ? Number(cache.hits) : 0;
  const misses = cache ? Number(cache.misses) : 0;
  const errors = cache ? Number(cache.errors) : 0;
  setText("stat-hits", hits.toLocaleString());
  setText("stat-misses", misses.toLocaleString());
  setText("stat-errors", errors.toLocaleString());
  const total = hits + misses;
  setText("stat-hitrate", total > 0 ? Math.round((hits / total) * 100) + "%" : "-");
  setText("stat-size", cache ? fmtBytes(cache.sizeBytes) : "-");

  // Synthesize one utilization Sample from this live frame (the metrics Snapshot
  // stream does not carry pool occupancy) so the grid + rate chart stay live.
  appendSample({
    at: Date.now(),
    inUse: pool ? pool.inUse : 0,
    capacity: pool ? pool.capacity : 0,
    waiting: pool ? pool.waiting : 0,
    cacheHits: hits,
    cacheMisses: misses,
  });

  // In-flight calls (deep-link to the live log when the call carries an invocation id).
  const calls = (pool && pool.calls) || [];
  setText("calls-count", String(calls.length));
  el("calls-empty").hidden = calls.length > 0;
  const cl = el("calls-list");
  cl.replaceChildren();
  for (const c of calls) {
    const clickable = liveHost && c.invocation;
    const row = document.createElement(clickable ? "a" : "li") as HTMLElement;
    row.className = "row";
    if (clickable) (row as HTMLAnchorElement).href = "../logs/#live=" + encodeURIComponent(liveHost!) + "&inv=" + encodeURIComponent(c.invocation);
    const cmd = document.createElement("code");
    cmd.className = "row-cmd";
    cmd.textContent = fmtArgs(c.args);
    const meta = document.createElement("span");
    meta.className = "row-meta";
    const bits: string[] = [];
    if (c.subOp) bits.push(c.subOp);
    const t = relTime(c.startTime);
    if (t) bits.push(t);
    meta.textContent = bits.join(" · ");
    row.append(cmd, meta);
    cl.append(row);
  }

  // Loaded workspaces, each with its own cache tallies.
  const wss = (pool && pool.workspaces) || [];
  setText("ws-count", String(wss.length));
  el("ws-empty").hidden = wss.length > 0;
  const wl = el("ws-list");
  wl.replaceChildren();
  for (const w of wss) {
    const li = document.createElement("li");
    li.className = "row";
    const root = document.createElement("code");
    root.className = "row-cmd";
    root.textContent = w.root;
    const meta = document.createElement("span");
    meta.className = "row-ws-cache";
    if (w.cache) {
      const mk = (cls: string, label: string, v: number | bigint): HTMLElement => {
        const s = document.createElement("span");
        s.className = cls;
        s.textContent = label + " " + Number(v || 0);
        return s;
      };
      meta.append(mk("h", "H", w.cache.hits), mk("m", "M", w.cache.misses));
      if (Number(w.cache.errors) > 0) meta.append(mk("e", "E", w.cache.errors));
    } else {
      meta.textContent = relTime(w.lastAccessTime);
    }
    li.append(root, meta);
    wl.append(li);
  }

  setText("dash-magus-version", st.magusVersion ? "magus " + st.magusVersion : "");
  setText("dash-daemon-version", pool && pool.daemonVersion ? "daemon " + pool.daemonVersion : "");
}

// ---- metrics stream (ConnectRPC) -------------------------------------------
// A connect-web transport pointed at the loopback daemon origin, with an
// interceptor that stamps the shared bearer token on every request. StreamMetrics
// is server-streaming: consumed as an async iterable, first Backfill then Snapshots.

function makeMetricsClient(host: string, token: string | null): Client<typeof MetricsService> {
  const authInterceptor: Interceptor = (next) => async (req) => {
    if (token) req.header.set("Authorization", "Bearer " + token);
    return await next(req);
  };
  const transport = createConnectTransport({
    baseUrl: "http://" + host,
    interceptors: [authInterceptor],
  });
  return createClient(MetricsService, transport);
}

let metricsAbort: AbortController | null = null;
let metricsRetry: ReturnType<typeof setTimeout> | null = null;

function startMetrics(host: string): void {
  stopMetrics();
  metricsAbort = new AbortController();
  void runMetrics(host, metricsAbort.signal);
}

function stopMetrics(): void {
  if (metricsAbort) { metricsAbort.abort(); metricsAbort = null; }
  if (metricsRetry) { clearTimeout(metricsRetry); metricsRetry = null; }
}

async function runMetrics(host: string, signal: AbortSignal): Promise<void> {
  const client = makeMetricsClient(host, getLiveToken());
  try {
    for await (const res of client.streamMetrics({}, { signal })) {
      if (res.of.case === "backfill") seedSamples(res.of.value.samples);
      else if (res.of.case === "snapshot") onSnapshot(res.of.value);
    }
    if (!signal.aborted) scheduleMetricsRetry(host); // stream ended cleanly: reconnect
  } catch {
    if (!signal.aborted) scheduleMetricsRetry(host);
  }
}

function scheduleMetricsRetry(host: string): void {
  if (metricsRetry) return;
  metricsRetry = setTimeout(() => {
    metricsRetry = null;
    if (metricsAbort && !metricsAbort.signal.aborted) void runMetrics(host, metricsAbort.signal);
  }, 3000);
}

// ---- live connection (status SSE) ------------------------------------------

let abort: AbortController | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let everConnected = false; // true once a stream has opened at least once this session
let failCount = 0;         // consecutive stream failures since the last good open

function connectLive(host: string): void {
  if (abort) abort.abort();
  abort = new AbortController();
  if (!everConnected) setConn("connecting");
  const url = "http://" + host + "/api/v1/events";
  const token = getLiveToken();
  const headers: SSEHeaders = token ? { Authorization: "Bearer " + token } : {};
  startMetrics(host); // the metrics stream rides alongside the status SSE
  void fetchSSE(
    url,
    headers,
    (type, data) => {
      if (type !== "status") return;
      try {
        const raw = Uint8Array.from(atob(data), (ch) => ch.charCodeAt(0));
        renderStatus(fromBinary(StatusSchema, raw));
      } catch {
        // Ignore a malformed frame; the next one supersedes it.
      }
    },
    () => onLiveError(host),
    abort.signal,
    () => onLiveOpen(host),
  );
}

function onLiveOpen(host: string): void {
  failCount = 0;
  everConnected = true;
  liveHost = host;
  setConn("connected");
  wireLaunchers(host);
  saveDaemon(host); // remember it so a reload resumes
}

// onLiveError debounces disconnection: a brief blip stays "reconnecting" and keeps the last
// data on screen; only after DISCONNECT_GRACE consecutive failures does the pill go
// "disconnected". A never-connected resume attempt that gives up shows the confirm prompt.
function onLiveError(host: string): void {
  failCount++;
  if (everConnected) {
    setConn("disconnected", failCount >= DISCONNECT_GRACE ? undefined : "reconnecting");
  } else if (failCount >= DISCONNECT_GRACE) {
    setConn("disconnected");
    showResume(host, true);
    stopMetrics();
    return; // stop hammering a daemon that isn't there; the confirm form drives the retry
  } else {
    setConn("connecting");
  }
  scheduleReconnect(host);
}

function scheduleReconnect(host: string): void {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => { reconnectTimer = null; connectLive(host); }, 3000);
}

// showResume reveals the connect panel's reconnect form, pre-filled with host. failed=true
// after a resume attempt could not reach the saved daemon ("is this correct?").
function showResume(host: string | null, failed: boolean): void {
  el("dash-connect").hidden = false;
  el("dash-panels").hidden = true;
  const form = el("dash-resume");
  form.hidden = false;
  (el("dash-resume-host") as HTMLInputElement).value = host || "";
  setText("dash-connect-title", failed ? "Couldn't reach the daemon" : "Reconnect to the daemon");
  setText("dash-connect-sub", failed
    ? "The saved address didn't respond. Confirm it below, or start the daemon and open the link it prints."
    : "Resume your last daemon, or start a new one below.");
}

function wireResumeForm(): void {
  const form = opt("dash-resume") as HTMLFormElement | null;
  if (!form) return;
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const host = validateLiveHost((el("dash-resume-host") as HTMLInputElement).value.trim());
    if (!host) {
      setText("dash-connect-sub", "That host must be literally 127.0.0.1 or [::1] with a port.");
      return;
    }
    everConnected = false;
    failCount = 0;
    setConn("connecting");
    connectLive(host);
  });
  el("dash-resume-forget").addEventListener("click", () => {
    forgetDaemon();
    form.hidden = true;
    setText("dash-connect-title", "No daemon connected");
    setText("dash-connect-sub",
      "The dashboard streams a running magus daemon's pool, cache, and health. Start the daemon, then open the live link it prints.");
    setConn("none");
  });
}

// initCollapse restores collapsed cards from localStorage and wires each card's caret to
// toggle + persist. Super-basic UI persistence, nothing high-stakes.
function initCollapse(): void {
  const collapsed = loadCollapsed();
  for (const tile of document.querySelectorAll<HTMLElement>(".tile[data-card]")) {
    if (tile.dataset.card && collapsed.has(tile.dataset.card)) tile.classList.add("collapsed");
  }
  for (const btn of document.querySelectorAll<HTMLElement>(".tile-collapse")) {
    btn.addEventListener("click", () => {
      const card = btn.dataset.card;
      if (!card) return;
      const tile = document.querySelector<HTMLElement>('.tile[data-card="' + card + '"]');
      if (!tile) return;
      tile.classList.toggle("collapsed");
      const set = loadCollapsed();
      if (tile.classList.contains("collapsed")) set.add(card); else set.delete(card);
      saveCollapsed(set);
      // A chart/grid has no size while its tile is folded; refit once it is revealed.
      if (!tile.classList.contains("collapsed")) { resizeCharts(); renderGrid(); }
    });
  }
}

// wireLaunchers points the log-viewer / graph-explorer links at live mode when the
// dashboard is connected (host only - the token is shared via sessionStorage).
function wireLaunchers(host: string): void {
  const logs = "../logs/#live=" + encodeURIComponent(host);
  const graph = "../graph/#live=" + encodeURIComponent(host);
  (el("launch-logs") as HTMLAnchorElement).href = logs;
  (el("launch-graph") as HTMLAnchorElement).href = graph;
  (el("menu-logs") as HTMLAnchorElement).href = logs;   // the app-bar menu opens them live too
  (el("menu-graph") as HTMLAnchorElement).href = graph;
}

// ---- boot ------------------------------------------------------------------

// registerServiceWorker installs the site's sw.js (resolved relative to this bundle, so it
// registers at the gen-root scope that also covers /dashboard/). This page does NOT load the
// docs main.js - which would inject the site search + nav chrome - so it registers the SW
// itself to stay an installable, offline-capable PWA surface. Only on a secure origin.
function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  const secure = location.protocol === "https:" || location.hostname === "localhost";
  if (!secure) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker.register(new URL("../sw.js", import.meta.url)).catch(() => {});
  });
}

function boot(): void {
  document.documentElement.classList.remove("no-js");
  registerServiceWorker();
  initCollapse();
  wireResumeForm();
  watchTheme();

  const badge = opt("offline-badge");
  const updateOffline = (): void => { if (badge) badge.hidden = navigator.onLine; };
  updateOffline();
  window.addEventListener("online", updateOffline);
  window.addEventListener("offline", updateOffline);
  window.addEventListener("resize", resizeCharts);

  const params = parseHash();
  consumeLiveToken(params);

  // A #live=host in the URL (the link magus printed) always wins.
  if (params.live !== undefined) {
    const host = validateLiveHost(params.live);
    if (!host) {
      setConn("disconnected", "invalid host");
      setText("dash-connect-title", "Can't connect");
      setText("dash-connect-sub",
        "The #live host must be literally 127.0.0.1 or [::1]. Re-open the link magus printed.");
      return;
    }
    connectLive(host);
    return;
  }

  // No link in the URL: optimistically resume the last daemon we connected to. If it is
  // gone, onLiveError surfaces the confirm-the-address form after the grace window.
  const saved = savedDaemon();
  if (saved && validateLiveHost(saved)) {
    setText("dash-connect-title", "Reconnecting...");
    setText("dash-connect-sub", "Resuming your last daemon at " + saved + ".");
    connectLive(saved);
    return;
  }
  setConn("none");
}

boot();
