// dashboard.js - magus's control surface.
//
// Consumes the daemon bridge's loopback SSE (`/api/v1/events`, `event: status`
// frames) and renders live pool / cache / health decoded from the magus.status.v1
// protobuf. There is NO server round-trip beyond the loopback daemon: the host and
// bearer token arrive in the URL fragment (#live=host:port&token=), which the browser
// never transmits. The cache hit/miss trend is plotted entirely client-side from the
// stream - no backend history store. The security-critical live helpers
// (validateLiveHost / consumeLiveToken / fetchSSE) mirror js/graph-explorer.js so all
// three pages enforce the same loopback lock and share one sessionStorage token key.

import { fromBinary } from "@bufbuild/protobuf";
import { StatusSchema, Health } from "./gen/magus/status/v1/status_pb";
import "./nav.js"; // reuse the site's exact nav dropdown behavior (hamburger <-> X, dismiss)

const el = (id) => document.getElementById(id);

// Super-basic local persistence (localStorage): the remembered daemon host so a reload
// resumes without re-opening the printed link, and the set of collapsed cards. Nothing
// high-stakes - all wrapped so a storage-disabled browser degrades to no persistence.
const LS_DAEMON = "magus-dashboard-daemon";
const LS_COLLAPSED = "magus-dashboard-collapsed";
const DISCONNECT_GRACE = 3; // consecutive stream failures before the pill flips to "disconnected"

function saveDaemon(host) { try { localStorage.setItem(LS_DAEMON, host); } catch { /* ignore */ } }
function savedDaemon() { try { return localStorage.getItem(LS_DAEMON); } catch { return null; } }
function forgetDaemon() { try { localStorage.removeItem(LS_DAEMON); } catch { /* ignore */ } }

function loadCollapsed() {
  try { return new Set(JSON.parse(localStorage.getItem(LS_COLLAPSED) || "[]")); } catch { return new Set(); }
}
function saveCollapsed(set) {
  try { localStorage.setItem(LS_COLLAPSED, JSON.stringify([...set])); } catch { /* ignore */ }
}

// ---- hash params -----------------------------------------------------------

function parseHash() {
  const h = location.hash.replace(/^#/, "");
  const params = {};
  for (const part of h.split("&")) {
    if (!part) continue;
    const i = part.indexOf("=");
    if (i < 0) { params[part] = ""; continue; }
    params[decodeURIComponent(part.slice(0, i))] = decodeURIComponent(part.slice(i + 1));
  }
  return params;
}

// ---- live host validation / token (mirror graph-explorer.js) ---------------

function validateLiveHost(hostPort) {
  let u;
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

function consumeLiveToken(params) {
  if (!params.token) return;
  const remember = localStorage.getItem("magus-live-remember") === "1";
  if (remember) {
    localStorage.setItem("magus-live-token", params.token);
  } else {
    sessionStorage.setItem("magus-live-token", params.token);
  }
  const kept = [];
  for (const k of Object.keys(params)) {
    if (k === "token") continue;
    kept.push(k + "=" + encodeURIComponent(params[k]));
  }
  const next = kept.length ? "#" + kept.join("&") : "#";
  history.replaceState(null, "", location.pathname + location.search + next);
}

function getLiveToken() {
  return sessionStorage.getItem("magus-live-token") || localStorage.getItem("magus-live-token") || null;
}

async function fetchSSE(url, headers, onEvent, onError, signal, onOpen) {
  let response;
  try {
    response = await fetch(url, { headers, signal });
  } catch (e) {
    if (e.name === "AbortError") return;
    onError(e);
    return;
  }
  if (!response.ok) {
    onError(new Error("HTTP " + response.status));
    return;
  }
  if (onOpen) onOpen();
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) { onError(new Error("stream ended")); return; }
      buf += value;
      let boundary;
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
    if (e.name !== "AbortError") onError(e);
  }
}

// ---- formatting ------------------------------------------------------------

const HEALTH = {
  [Health.HEALTHY]: { label: "healthy", cls: "ok" },
  [Health.DEGRADED]: { label: "degraded", cls: "warn" },
  [Health.DOWN]: { label: "down", cls: "fail" },
  [Health.UNSPECIFIED]: { label: "unknown", cls: "" },
};

function setConn(state, detail) {
  const c = el("dash-conn");
  const map = { connecting: "connecting...", connected: "connected", disconnected: detail || "reconnecting", none: "not connected" };
  c.textContent = map[state] || state;
  c.dataset.state = state;
}

function fmtArgs(args) {
  return (args && args.length) ? "magus " + args.join(" ") : "magus";
}

function fmtBytes(n) {
  n = Number(n || 0);
  if (n <= 0) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)) + " " + u[i];
}

function relTime(ts) {
  if (!ts) return "";
  const ms = Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
  const secs = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (secs < 60) return secs + "s";
  const mins = Math.round(secs / 60);
  if (mins < 60) return mins + "m";
  return Math.round(mins / 60) + "h";
}

// ---- cache sparkline (client-side history) ---------------------------------

const HISTORY = 90; // samples kept for the trend
const hitSeries = [];
const missSeries = [];
let lastHits = null, lastMisses = null;

// pushCacheSample records the per-frame DELTA in hits/misses so the sparkline shows
// activity rate, not the ever-growing cumulative totals.
function pushCacheSample(hits, misses) {
  if (lastHits !== null) {
    hitSeries.push(Math.max(0, hits - lastHits));
    missSeries.push(Math.max(0, misses - lastMisses));
    if (hitSeries.length > HISTORY) { hitSeries.shift(); missSeries.shift(); }
  }
  lastHits = hits;
  lastMisses = misses;
  drawSpark();
}

function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#888";
}

function drawSpark() {
  const canvas = el("spark");
  if (!canvas) return;
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth, h = canvas.clientHeight;
  if (canvas.width !== w * dpr || canvas.height !== h * dpr) {
    canvas.width = w * dpr; canvas.height = h * dpr;
  }
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const n = hitSeries.length;
  if (n < 2) return;
  const peak = Math.max(1, ...hitSeries, ...missSeries);
  const step = w / (HISTORY - 1);
  const line = (series, color) => {
    ctx.beginPath();
    for (let i = 0; i < series.length; i++) {
      const x = i * step;
      const y = h - (series[i] / peak) * (h - 4) - 2;
      i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
    }
    ctx.strokeStyle = color; ctx.lineWidth = 1.5; ctx.lineJoin = "round"; ctx.stroke();
  };
  line(missSeries, cssVar("--c-miss"));
  line(hitSeries, cssVar("--c-hit"));
}

// ---- rendering -------------------------------------------------------------

let liveHost = null; // set once connected; used to deep-link running calls into live logs

const SLOT_CAP = 256; // soft cap on rendered cubes so a huge pool never bloats the DOM

// renderSlots draws the concurrency pool as an occupancy grid: one cube per capacity slot,
// filled when in use, plus dashed cubes for tasks queued waiting on a slot (airplane-seating
// read). An unlimited pool (capacity 0) shows one cube per in-use slot.
function renderSlots(pool) {
  const cap = pool ? pool.capacity : 0;
  const used = pool ? pool.inUse : 0;
  const waiting = pool ? pool.waiting : 0;
  el("pool-summary").textContent = cap > 0 ? `${used} / ${cap} slots` : `${used} in use · unlimited`;

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
  el("wait-count").textContent = String(waiting);
}

function renderStatus(st) {
  el("dash-connect").hidden = true;
  el("dash-panels").hidden = false;

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
  el("stat-hits").textContent = hits.toLocaleString();
  el("stat-misses").textContent = misses.toLocaleString();
  el("stat-errors").textContent = errors.toLocaleString();
  const total = hits + misses;
  el("stat-hitrate").textContent = total > 0 ? Math.round((hits / total) * 100) + "%" : "-";
  el("stat-size").textContent = cache ? fmtBytes(cache.sizeBytes) : "-";
  pushCacheSample(hits, misses);

  // In-flight calls (deep-link to the live log when the call carries an invocation id).
  const calls = (pool && pool.calls) || [];
  el("calls-count").textContent = String(calls.length);
  el("calls-empty").hidden = calls.length > 0;
  const cl = el("calls-list");
  cl.replaceChildren();
  for (const c of calls) {
    const clickable = liveHost && c.invocation;
    const row = document.createElement(clickable ? "a" : "li");
    row.className = "row";
    if (clickable) row.href = "../logs/#live=" + encodeURIComponent(liveHost) + "&inv=" + encodeURIComponent(c.invocation);
    const cmd = document.createElement("code");
    cmd.className = "row-cmd";
    cmd.textContent = fmtArgs(c.args);
    const meta = document.createElement("span");
    meta.className = "row-meta";
    const bits = [];
    if (c.subOp) bits.push(c.subOp);
    const t = relTime(c.startTime);
    if (t) bits.push(t);
    meta.textContent = bits.join(" · ");
    row.append(cmd, meta);
    cl.append(row);
  }

  // Loaded workspaces, each with its own cache tallies.
  const wss = (pool && pool.workspaces) || [];
  el("ws-count").textContent = String(wss.length);
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
      const mk = (cls, label, v) => {
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

  el("dash-magus-version").textContent = st.magusVersion ? "magus " + st.magusVersion : "";
  el("dash-daemon-version").textContent = pool && pool.daemonVersion ? "daemon " + pool.daemonVersion : "";
}

// ---- live connection -------------------------------------------------------

let abort = null;
let reconnectTimer = null;
let everConnected = false; // true once a stream has opened at least once this session
let failCount = 0;         // consecutive stream failures since the last good open

function connectLive(host) {
  if (abort) abort.abort();
  abort = new AbortController();
  if (!everConnected) setConn("connecting");
  const url = "http://" + host + "/api/v1/events";
  const token = getLiveToken();
  const headers = token ? { Authorization: "Bearer " + token } : {};
  fetchSSE(
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

function onLiveOpen(host) {
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
function onLiveError(host) {
  failCount++;
  if (everConnected) {
    setConn(failCount >= DISCONNECT_GRACE ? "disconnected" : "reconnecting");
  } else if (failCount >= DISCONNECT_GRACE) {
    setConn("disconnected");
    showResume(host, true);
    return; // stop hammering a daemon that isn't there; the confirm form drives the retry
  } else {
    setConn("connecting");
  }
  scheduleReconnect(host);
}

function scheduleReconnect(host) {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => { reconnectTimer = null; connectLive(host); }, 3000);
}

// showResume reveals the connect panel's reconnect form, pre-filled with host. failed=true
// after a resume attempt could not reach the saved daemon ("is this correct?").
function showResume(host, failed) {
  el("dash-connect").hidden = false;
  el("dash-panels").hidden = true;
  const form = el("dash-resume");
  form.hidden = false;
  el("dash-resume-host").value = host || "";
  el("dash-connect-title").textContent = failed ? "Couldn't reach the daemon" : "Reconnect to the daemon";
  el("dash-connect-sub").textContent = failed
    ? "The saved address didn't respond. Confirm it below, or start the daemon and open the link it prints."
    : "Resume your last daemon, or start a new one below.";
}

function wireResumeForm() {
  const form = el("dash-resume");
  if (!form) return;
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const host = validateLiveHost(el("dash-resume-host").value.trim());
    if (!host) {
      el("dash-connect-sub").textContent = "That host must be literally 127.0.0.1 or [::1] with a port.";
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
    el("dash-connect-title").textContent = "No daemon connected";
    el("dash-connect-sub").textContent =
      "The dashboard streams a running magus daemon's pool, cache, and health. Start the daemon, then open the live link it prints.";
    setConn("none");
  });
}

// initCollapse restores collapsed cards from localStorage and wires each card's caret to
// toggle + persist. Super-basic UI persistence, nothing high-stakes.
function initCollapse() {
  const collapsed = loadCollapsed();
  for (const tile of document.querySelectorAll(".tile[data-card]")) {
    if (collapsed.has(tile.dataset.card)) tile.classList.add("collapsed");
  }
  for (const btn of document.querySelectorAll(".tile-collapse")) {
    btn.addEventListener("click", () => {
      const card = btn.dataset.card;
      const tile = document.querySelector('.tile[data-card="' + card + '"]');
      if (!tile) return;
      tile.classList.toggle("collapsed");
      const set = loadCollapsed();
      if (tile.classList.contains("collapsed")) set.add(card); else set.delete(card);
      saveCollapsed(set);
      // The sparkline canvas has no size while hidden, so redraw it once revealed.
      if (card === "cache" && !tile.classList.contains("collapsed")) drawSpark();
    });
  }
}

// wireLaunchers points the log-viewer / graph-explorer links at live mode when the
// dashboard is connected (host only - the token is shared via sessionStorage).
function wireLaunchers(host) {
  const logs = "../logs/#live=" + encodeURIComponent(host);
  const graph = "../graph/#live=" + encodeURIComponent(host);
  el("launch-logs").href = logs;
  el("launch-graph").href = graph;
  el("menu-logs").href = logs;   // the app-bar menu opens them live too
  el("menu-graph").href = graph;
}

// ---- boot ------------------------------------------------------------------

// registerServiceWorker installs the site's sw.js (resolved relative to this bundle, so it
// registers at the gen-root scope that also covers /dashboard/). This page does NOT load the
// docs main.js - which would inject the site search + nav chrome - so it registers the SW
// itself to stay an installable, offline-capable PWA surface. Only on a secure origin.
function registerServiceWorker() {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  const secure = location.protocol === "https:" || location.hostname === "localhost";
  if (!secure) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker.register(new URL("../sw.js", import.meta.url)).catch(() => {});
  });
}

function boot() {
  document.documentElement.classList.remove("no-js");
  registerServiceWorker();
  initCollapse();
  wireResumeForm();

  const badge = el("offline-badge");
  const updateOffline = () => { if (badge) badge.hidden = navigator.onLine; };
  updateOffline();
  window.addEventListener("online", updateOffline);
  window.addEventListener("offline", updateOffline);
  window.addEventListener("resize", drawSpark);

  const params = parseHash();
  consumeLiveToken(params);

  // A #live=host in the URL (the link magus printed) always wins.
  if (params.live !== undefined) {
    const host = validateLiveHost(params.live);
    if (!host) {
      setConn("disconnected", "invalid host");
      el("dash-connect-title").textContent = "Can't connect";
      el("dash-connect-sub").textContent =
        "The #live host must be literally 127.0.0.1 or [::1]. Re-open the link magus printed.";
      return;
    }
    connectLive(host);
    return;
  }

  // No link in the URL: optimistically resume the last daemon we connected to. If it is
  // gone, onLiveError surfaces the confirm-the-address form after the grace window.
  const saved = savedDaemon();
  if (saved && validateLiveHost(saved)) {
    el("dash-connect-title").textContent = "Reconnecting...";
    el("dash-connect-sub").textContent = "Resuming your last daemon at " + saved + ".";
    connectLive(saved);
    return;
  }
  setConn("none");
}

boot();
