// log-viewer.js - the /logs/ Log Viewer. A purpose-built, read-only viewer for a magus
// run's captured output. The `#data=` fragment carries a magus.viewer.v1 Journal (protobuf,
// gzip+base64url), decoded here with the generated @bufbuild/protobuf client and rendered
// pretty from its STRUCTURE (per-target groups, exec command boundaries, result status) -
// no text-heuristic guessing. A pasted / dropped / `#src=`-fetched log has no structure, so
// it falls back to the heuristic text parse. Everything is local: nothing is ever uploaded.
//
// Bundled (esbuild) because it imports the proto client; every handler guards on its DOM
// target, so it is a no-op if the scaffold is absent (e.g. main.js loading on another page).

import { fromBinary } from "@bufbuild/protobuf";
import { JournalSchema, EventSchema, Kind, Status } from "./gen/magus/viewer/v1/viewer_pb";

const el = (id) => document.getElementById(id);

// setBtnLabel sets a toolbar button's text label without disturbing its icon: the label
// lives in a .btn-label span next to the SVG, so we can't just set button.textContent.
function setBtnLabel(btn, text) {
  if (!btn) return;
  const label = btn.querySelector(".btn-label");
  if (label) label.textContent = text;
  else btn.textContent = text;
}

const refEl = el("log-ref");
const refLabelEl = el("log-ref-label");
const statusEl = el("log-status");
const scrollEl = el("log-scroll");

// setRefIdentity fills the file-bar identity strip. A real ref gets a "Reference ID:" label
// (the codebase term, per docs/glossary.md) before the value; a non-ref state (a live run, a
// pasted log) shows just the value with no label.
function setRefIdentity(value, labeled) {
  if (refLabelEl) { refLabelEl.hidden = !labeled; refLabelEl.textContent = labeled ? "Reference ID:" : ""; }
  if (refEl) refEl.textContent = value;
}
const bodyEl = el("log-body");
const emptyEl = el("log-empty");
const panelEl = document.querySelector(".panel");
if (bodyEl && scrollEl) {
  init();
}

function init() {
  wireControls();
  wireInput();
  loadFromURL();
}

// --- Fragment decode (matches internal/render EncodeFragmentRaw) --------------
// base64url -> bytes -> gunzip -> text. DecompressionStream is widely supported;
// the whole path is local, so nothing is fetched and nothing is sent.
async function decodeFragment(b64url) {
  return new Response(await gunzipFragment(b64url)).text();
}

// decodeFragmentBytes is the binary sibling: base64url -> gunzip -> Uint8Array, for the
// protobuf Journal payload (decodeFragment layers a text decode on top for legacy text).
async function decodeFragmentBytes(b64url) {
  return new Uint8Array(await new Response(await gunzipFragment(b64url)).arrayBuffer());
}

function gunzipFragment(b64url) {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new Response(bytes).body.pipeThrough(new DecompressionStream("gzip"));
}

// viewerParams reads the deep-link parameters from the URL fragment (after #). EVERYTHING -
// the ref id, the encoded log (data), the live host and bearer token - rides the fragment,
// which the browser never transmits to any server, so nothing about the run ever leaves the
// machine. That absolute guarantee is why no parameter uses the query string.
function viewerParams() {
  const out = {};
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    out[part.slice(0, eq)] = decodeURIComponent(part.slice(eq + 1));
  }
  return out;
}

async function loadFromURL() {
  const params = viewerParams();
  const ref = params.ref || "";
  if (params.live) {
    connectLive(params);
    return;
  }
  if (params.data) {
    setStatus("decoding...");
    try {
      // #data= now carries a protobuf Journal (from `magus query ref --open`). Decode the
      // bytes and parse; if it is not a valid Journal (a legacy text link), fall back to the
      // text heuristic on the same gunzipped bytes.
      const bytes = await decodeFragmentBytes(params.data);
      let journal = null;
      try {
        const j = fromBinary(JournalSchema, bytes);
        if (j && j.events && j.events.length) journal = j;
      } catch (_) {
        journal = null;
      }
      if (journal) loadJournal(journal, ref);
      else loadText(new TextDecoder().decode(bytes), ref);
    } catch (e) {
      setStatus("could not decode the log", true);
    }
    return;
  }
  if (params.src) {
    setStatus("fetching...");
    try {
      const u = new URL(params.src, location.href);
      if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error("bad scheme");
      const r = await fetch(params.src, { headers: { Accept: "text/plain" } });
      if (!r.ok) throw new Error("fetch failed");
      loadText(await r.text(), ref);
    } catch (e) {
      setStatus("could not fetch the log", true);
    }
    return;
  }
  // No data: leave the empty state visible.
}

// --- Model: split the text into foldable sections -----------------------------
// A section begins at a recognized header line (a run header, a target status line,
// or a "-- project (failed) --" divider). Lines before the first header form an
// untitled preamble section that renders without a fold head.
function isHeaderLine(line) {
  const s = stripAnsi(line);
  return (
    /^-- .+ --\s*$/.test(s) ||
    /^\[(pass|fail|warn|dry|error|info)\]/.test(s) ||
    /^(projects|charms):/.test(s)
  );
}

function buildModel(text) {
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  // Drop a single trailing empty line from the split so a log ending in "\n" does
  // not render a blank final row.
  if (lines.length && lines[lines.length - 1] === "") lines.pop();

  const sections = [];
  let current = { title: null, lines: [] };
  for (const line of lines) {
    if (isHeaderLine(line)) {
      if (current.title !== null || current.lines.length) sections.push(current);
      current = { title: line, lines: [line] };
    } else {
      current.lines.push(line);
    }
  }
  if (current.title !== null || current.lines.length) sections.push(current);
  const titled = sections.filter((s) => s.title !== null).length;
  return { sections, titled };
}

// --- Render -------------------------------------------------------------------
let model = null;
let rawText = "";
// currentRef is the ref id from #ref=, if the log came from `magus query ... --open`.
// It powers the "copy as command" buttons; a pasted/dropped log has none.
let currentRef = "";
// pretty toggles the stylized structural view (default) vs the raw captured text.
let pretty = true;

// rawLines holds the pure captured output (reconstructed from a Journal's output events)
// so the RAW view shows exactly what `magus query <ref>` prints. null in heuristic (text)
// mode, where the RAW view falls back to the parsed section lines.
let rawLines = null;

function loadText(text, ref) {
  rawLines = null;
  model = buildModel(text);
  rawText = text;
  finishLoad(ref, summarize(text));
}

// loadJournal renders a magus.viewer.v1 Journal (the structured #data path): it builds the
// SAME section model the heuristic produces - so render()/search/fold/copy work unchanged -
// but from EVENTS, so grouping and status are exact, not regex-guessed.
function loadJournal(journal, ref) {
  const built = buildModelFromEvents(journal.events || [], journal.invocation);
  model = { sections: built.sections, titled: built.titled };
  rawLines = built.rawLines;
  rawText = built.rawLines.join("\n");
  finishLoad(ref, built.summary);
}

function finishLoad(ref, statusMsg) {
  currentRef = looksLikeRef(ref) ? ref : "";
  if (emptyEl) emptyEl.hidden = true;
  setRefIdentity(ref || "log", looksLikeRef(ref));
  render();
  setStatus(statusMsg);
  const foldBtn = el("fold-all-btn");
  if (foldBtn) foldBtn.hidden = model.titled === 0 || !pretty;
  const copyBtn = el("copy-all-btn");
  if (copyBtn) copyBtn.disabled = false;
  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) cmdBtn.hidden = !currentRef;
}

// buildModelFromEvents turns an event stream (a whole Journal's events, or the live events
// so far) into the {sections,...} render model: the invocation command becomes a lineage
// preamble; each (project,target) becomes a section whose exec events open "$ cmd" groups,
// output events are body lines, and the result event sets the status badge/accent in a
// synthesized head line the existing renderer understands. Reused by the static and live
// paths, so it must be a pure function of the events seen so far.
function buildModelFromEvents(events, invocation) {
  const groups = new Map();
  const order = [];
  const preamble = [];
  const rawLines = [];
  const cmd = invocation && invocation.command;
  if (cmd && cmd.verb) {
    preamble.push("$ magus " + cmd.verb + (cmd.args && cmd.args.length ? " " + cmd.args.join(" ") : ""));
  }
  for (const ev of events) {
    if (ev.kind === Kind.OUTPUT || ev.kind === Kind.EXEC || ev.kind === Kind.RESULT) {
      const key = ev.project + " " + ev.target;
      let g = groups.get(key);
      if (!g) { g = { project: ev.project, target: ev.target, body: [], result: null }; groups.set(key, g); order.push(g); }
      if (ev.kind === Kind.EXEC) g.body.push("$ " + ev.text);
      else if (ev.kind === Kind.OUTPUT) { g.body.push(ev.text); rawLines.push(ev.text); }
      else g.result = ev;
    } else if (ev.kind === Kind.WARN) {
      preamble.push("[warn] " + ev.text);
    } else if (ev.kind === Kind.SCOPE && ev.text) {
      preamble.push(ev.text);
    }
  }
  const sections = [];
  if (preamble.length) sections.push({ title: null, lines: preamble });
  for (const g of order) {
    const title = groupTitle(g);
    sections.push({ title, lines: [title, ...g.body] });
  }
  const titled = sections.filter((s) => s.title !== null).length;
  const summary =
    order.length + (order.length === 1 ? " target, " : " targets, ") +
    rawLines.length + (rawLines.length === 1 ? " line" : " lines");
  return { sections, titled, rawLines, summary };
}

function groupTitle(g) {
  const st = statusName(g.result ? g.result.status : Status.UNSPECIFIED);
  const name = (g.project && g.project !== "." ? g.project + ":" : "") + (g.target || "output");
  const dur = g.result && g.result.duration ? " (" + durText(g.result.duration) + ")" : "";
  const refTag = g.result && g.result.ref ? "  " + g.result.ref : "";
  return (st ? "[" + st + "] " : "") + name + dur + refTag;
}

function statusName(s) {
  if (s === Status.PASS) return "pass";
  if (s === Status.FAIL) return "fail";
  if (s === Status.CACHED) return "cached";
  return "";
}

// durText renders a protobuf Duration ({seconds: bigint, nanos: number}) as "12ms" / "1.2s".
function durText(d) {
  const ms = Number(d.seconds || 0n) * 1000 + Number(d.nanos || 0) / 1e6;
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(1) + "s";
}

// --- Live streaming (#live=host:port&token=) ----------------------------------
// A run started with `--live` prints a link to an ephemeral 127.0.0.1 SSE server. The viewer
// connects (fetch-based SSE + bearer token, mirroring the graph explorer's live client),
// decodes each frame as a protobuf Event, appends it, re-renders on a frame tick, and
// auto-scrolls unless the reader pins the view with Pause. Datadog-style live tail.
let liveEvents = [];
let liveInvocation = null;
let livePaused = false;
let liveRenderQueued = false;
let liveAbort = null;

function connectLive(params) {
  const host = validateLiveHost(params.live);
  if (!host) {
    setStatus("refusing a non-loopback live host", true);
    return;
  }
  consumeLiveToken(params); // stash the token, strip it from the URL so it never persists
  const token = getLiveToken();
  liveEvents = [];
  liveInvocation = null;
  livePaused = false;
  if (emptyEl) emptyEl.hidden = true;
  setRefIdentity("live", false);
  const pauseBtn = el("pause-btn");
  if (pauseBtn) {
    pauseBtn.hidden = false;
    setBtnLabel(pauseBtn, "Pause");
    pauseBtn.setAttribute("aria-pressed", "false");
  }
  setLiveStatus("connecting");

  liveAbort = new AbortController();
  const headers = token ? { Authorization: "Bearer " + token } : {};
  fetchSSE(
    "http://" + host + "/events",
    headers,
    onLiveEvent,
    (e) => setLiveStatus(/stream ended|done/i.test((e && e.message) || "") ? "done" : "disconnected"),
    liveAbort.signal,
    () => setLiveStatus("streaming"),
  );
}

function onLiveEvent(type, data) {
  if (type === "done") {
    setLiveStatus("done");
    if (liveAbort) liveAbort.abort();
    return;
  }
  if (!data) return;
  let ev;
  try {
    ev = fromBinary(EventSchema, base64ToBytes(data));
  } catch (_) {
    return; // ignore an undecodable frame rather than tearing down the stream
  }
  if (ev.kind === Kind.STARTED && ev.command) liveInvocation = { command: ev.command };
  liveEvents.push(ev);
  scheduleLiveRender();
}

// scheduleLiveRender coalesces a burst of events into one re-render per frame: rebuild the
// model from all events so far and render, auto-scrolling to the tail unless paused.
function scheduleLiveRender() {
  if (liveRenderQueued) return;
  liveRenderQueued = true;
  requestAnimationFrame(() => {
    liveRenderQueued = false;
    const built = buildModelFromEvents(liveEvents, liveInvocation);
    model = { sections: built.sections, titled: built.titled };
    rawLines = built.rawLines;
    rawText = built.rawLines.join("\n");
    render();
    setLiveStatus(liveAbort && liveAbort.signal.aborted ? "done" : "streaming");
    const foldBtn = el("fold-all-btn");
    if (foldBtn) foldBtn.hidden = model.titled === 0 || !pretty;
    const copyBtn = el("copy-all-btn");
    if (copyBtn) copyBtn.disabled = false;
    if (!livePaused && scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
  });
}

function setLiveStatus(state) {
  const pill = statusEl;
  if (!pill) return;
  const n = liveEvents.length;
  const labels = { connecting: "connecting...", streaming: "live", done: "done", disconnected: "disconnected" };
  pill.textContent = (labels[state] || state) + (n ? " - " + n + " events" : "");
  pill.classList.toggle("err", state === "disconnected");
  pill.classList.toggle("live", state === "streaming" || state === "connecting");
}

function base64ToBytes(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

// validateLiveHost / consumeLiveToken / getLiveToken / fetchSSE mirror graph-explorer.js:
// the live client is the same shape (loopback-only host, token in the fragment, fetch-based
// SSE so the token rides an Authorization header). Page-local: the tool pages are separate
// bundles, so the code is duplicated rather than shared.
function validateLiveHost(hostPort) {
  let u;
  try {
    u = new URL("http://" + hostPort);
  } catch {
    return null;
  }
  if (u.username || u.password) return null; // userinfo is never legitimate here
  if (u.pathname !== "/" || u.search || u.hash) return null; // no extra segments
  if (u.hostname !== "127.0.0.1" && u.hostname !== "::1" && u.hostname !== "[::1]") return null;
  return u.host;
}

function consumeLiveToken(params) {
  if (!params.token) return;
  sessionStorage.setItem("magus-live-token", params.token);
  // Strip the token out of the fragment (keeping the other fragment keys, e.g. live=), so the
  // secret never lingers in the URL bar, history, or a copied link.
  const kept = [];
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    const eq = part.indexOf("=");
    if (eq < 0 || part.slice(0, eq) === "token") continue;
    kept.push(part);
  }
  history.replaceState(null, "", location.pathname + location.search + (kept.length ? "#" + kept.join("&") : ""));
}

function getLiveToken() {
  return sessionStorage.getItem("magus-live-token") || null;
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
      if (done) {
        onError(new Error("stream ended"));
        return;
      }
      buf += value;
      let boundary;
      while ((boundary = buf.indexOf("\n\n")) >= 0) {
        const chunk = buf.slice(0, boundary);
        buf = buf.slice(boundary + 2);
        if (!chunk.trim()) continue;
        let eventType = "message";
        let data = "";
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

// looksLikeRef mirrors the CLI's cache.LooksLikeRef: the "copy as command" buttons
// only make sense when the page was seeded by a real ref (not a pasted file name).
function looksLikeRef(s) {
  return typeof s === "string" && /^ref[0-9a-f]+$/.test(s);
}

function summarize(text) {
  const lines = text ? text.split("\n").length : 0;
  const bytes = new Blob([text]).size;
  return lines + " line" + (lines === 1 ? "" : "s") + ", " + humanBytes(bytes);
}

function humanBytes(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

function render() {
  bodyEl.textContent = "";
  bodyEl.classList.toggle("raw", !pretty);
  // Raw view: the exact captured text, flat - line numbers + ANSI color, no folds,
  // no badges, no structural chrome. The pretty view (default) styles it below.
  if (!pretty) {
    // RAW: in Journal mode, the exact reconstructed output (what `magus query <ref>` prints);
    // in heuristic mode, the parsed section lines. Flat, line-numbered, no folds/badges.
    const flat = rawLines || model.sections.flatMap((s) => s.lines);
    let n = 0;
    for (const raw of flat) bodyEl.appendChild(renderLine(raw, ++n));
    return;
  }

  let lineNo = 0;
  for (const sec of model.sections) {
    if (sec.title === null) {
      // Preamble / flat log: render lines directly, no fold head.
      for (const raw of sec.lines) bodyEl.appendChild(renderLine(raw, ++lineNo));
      continue;
    }
    const secEl = document.createElement("div");
    secEl.className = "log-section";

    // Accent the section by outcome (a colored left rule) so pass/fail/warn read at a
    // glance, not just from the text. Cached hits are muted (low signal) and fold by default -
    // recognized from a "[cached]" head (structured) or a "(cached)" note (heuristic text).
    const st = statusToken(sec.title);
    const cached = st === "cached" || /\(cached/i.test(stripAnsi(sec.title));
    const status = cached ? "cached" : st;
    if (status) secEl.classList.add("status-" + status);

    const head = document.createElement("button");
    head.type = "button";
    head.className = "log-section-head";

    // The head IS the header line and doubles as the fold toggle. The line counts as
    // row `++lineNo` so search and line numbers stay in step; not repeated in the body.
    const headNo = ++lineNo;
    const ln = document.createElement("span");
    ln.className = "ln";
    ln.textContent = String(headNo);
    const twist = document.createElement("span");
    twist.className = "twist"; // caret drawn in CSS (.twist); no glyph, so the source stays ASCII
    twist.setAttribute("aria-hidden", "true");
    const title = document.createElement("span");
    title.className = "sec-title lc";
    renderContent(title, sec.title);
    const bodyLines = sec.lines.slice(1);

    // A cached target contributed nothing new this run, so fold it away by default -
    // the fresh work (and any failure) is what a reader came for.
    if (cached) secEl.classList.add("collapsed");
    head.setAttribute("aria-expanded", cached ? "false" : "true");
    const count = document.createElement("span");
    count.className = "sec-count";
    count.textContent = bodyLines.length > 0 ? bodyLines.length + (bodyLines.length === 1 ? " line" : " lines") : "";

    const actions = document.createElement("span");
    actions.className = "sec-actions";
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "sec-btn outline";
    copy.textContent = "copy";
    copy.title = "Copy this section's text";
    copy.addEventListener("click", (ev) => {
      ev.stopPropagation();
      copyToClipboard(sec.lines.map(stripAnsi).join("\n"), copy);
    });
    actions.append(copy);
    // "cmd": a `magus query` one-liner scoped to this section's line range, so it can
    // be handed to an agent to fetch exactly these lines. Only when seeded by a ref.
    if (currentRef) {
      const cmd = document.createElement("button");
      cmd.type = "button";
      cmd.className = "sec-btn outline";
      cmd.textContent = "cmd";
      cmd.title = "Copy a `magus query` command for these lines";
      const start = headNo;
      const end = headNo + bodyLines.length;
      cmd.addEventListener("click", (ev) => {
        ev.stopPropagation();
        const command =
          bodyLines.length > 0
            ? "magus query " + currentRef + " | sed -n '" + start + "," + end + "p'"
            : "magus query " + currentRef;
        copyToClipboard(command, cmd);
      });
      actions.append(cmd);
    }

    head.append(ln, twist, title, count, actions);
    head.addEventListener("click", () => toggleSection(secEl, head));

    const linesWrap = document.createElement("div");
    linesWrap.className = "log-lines";
    for (const raw of bodyLines) linesWrap.appendChild(renderLine(raw, ++lineNo));

    secEl.append(head, linesWrap);
    bodyEl.appendChild(secEl);
  }
}

// STATUS_RE matches a leading magus status token like "[pass]" / "[fail]" - the
// "brackets" we render as a colored badge instead. statusToken returns the bare word.
const STATUS_RE = /^\s*\[(pass|fail|warn|error|info|dry|summary|cached)\]/i;

function statusToken(raw) {
  const m = STATUS_RE.exec(stripAnsi(raw));
  return m ? m[1].toLowerCase() : "";
}

// renderContent fills el with a line, promoting a leading "[status]" token to a
// styled badge (dropping the brackets) and rendering the remainder. Non-status lines
// fall through to the ANSI renderer unchanged.
function renderContent(el, raw) {
  const plain = stripAnsi(raw);
  const m = STATUS_RE.exec(plain);
  if (!m) {
    fillAnsi(el, raw);
    return;
  }
  const badge = document.createElement("span");
  badge.className = "log-badge badge-" + m[1].toLowerCase();
  badge.textContent = m[1].toLowerCase();
  el.appendChild(badge);
  el.appendChild(document.createTextNode(plain.slice(m[0].length)));
}

function toggleSection(secEl, head) {
  const collapsed = secEl.classList.toggle("collapsed");
  head.setAttribute("aria-expanded", collapsed ? "false" : "true");
}

function renderLine(raw, lineNo) {
  const line = document.createElement("div");
  line.className = "log-line";
  const ln = document.createElement("span");
  ln.className = "ln";
  ln.textContent = String(lineNo);
  const lc = document.createElement("span");
  lc.className = "lc";
  renderContent(lc, raw);
  line.append(ln, lc);
  return line;
}

// fillAnsi renders raw (an output line, possibly with ANSI SGR escapes) into el as
// styled spans. Shared by body lines and section heads so both carry the same color.
function fillAnsi(el, raw) {
  for (const seg of parseAnsi(raw)) {
    if (seg.cls.length) {
      const span = document.createElement("span");
      span.className = seg.cls.join(" ");
      span.textContent = seg.text;
      el.appendChild(span);
    } else {
      el.appendChild(document.createTextNode(seg.text));
    }
  }
}

// --- ANSI SGR parsing ---------------------------------------------------------
const ANSI_RE = /\x1b\[([0-9;]*)m/g;

function stripAnsi(s) {
  return s.replace(ANSI_RE, "");
}

const FG = {
  30: "a-fg-black", 31: "a-fg-red", 32: "a-fg-green", 33: "a-fg-yellow",
  34: "a-fg-blue", 35: "a-fg-magenta", 36: "a-fg-cyan", 37: "a-fg-white",
  90: "a-fg-black", 91: "a-fg-red", 92: "a-fg-green", 93: "a-fg-yellow",
  94: "a-fg-blue", 95: "a-fg-magenta", 96: "a-fg-cyan", 97: "a-fg-white",
};

// parseAnsi splits a line into {text, cls[]} runs by tracking SGR state across the
// line. Only the attributes magus emits (bold, dim, italic, underline, basic fg
// colors) are mapped; anything else is ignored so unknown codes never leak through.
function parseAnsi(line) {
  const out = [];
  const state = { bold: false, dim: false, italic: false, underline: false, fg: null };
  let last = 0;
  let m;
  ANSI_RE.lastIndex = 0;
  const push = (text) => {
    if (!text) return;
    out.push({ text, cls: classesFor(state) });
  };
  while ((m = ANSI_RE.exec(line)) !== null) {
    push(line.slice(last, m.index));
    applySGR(state, m[1]);
    last = ANSI_RE.lastIndex;
  }
  push(line.slice(last));
  if (!out.length) out.push({ text: "", cls: [] });
  return out;
}

function applySGR(state, params) {
  const codes = params === "" ? [0] : params.split(";").map((n) => parseInt(n, 10));
  for (const c of codes) {
    if (c === 0) { state.bold = state.dim = state.italic = state.underline = false; state.fg = null; }
    else if (c === 1) state.bold = true;
    else if (c === 2) state.dim = true;
    else if (c === 3) state.italic = true;
    else if (c === 4) state.underline = true;
    else if (c === 22) { state.bold = false; state.dim = false; }
    else if (c === 23) state.italic = false;
    else if (c === 24) state.underline = false;
    else if (c === 39) state.fg = null;
    else if (FG[c]) state.fg = FG[c];
  }
}

function classesFor(state) {
  const cls = [];
  if (state.bold) cls.push("a-bold");
  if (state.dim) cls.push("a-dim");
  if (state.italic) cls.push("a-italic");
  if (state.underline) cls.push("a-underline");
  if (state.fg) cls.push(state.fg);
  return cls;
}

// --- Search -------------------------------------------------------------------
let searchMarks = [];
let activeMark = -1;

function runSearch(query) {
  clearMarks();
  const searchEl = el("log-search");
  const countEl = el("search-count");
  const prevBtn = el("search-prev");
  const nextBtn = el("search-next");
  if (!query) {
    if (countEl) countEl.textContent = "";
    if (prevBtn) prevBtn.disabled = true;
    if (nextBtn) nextBtn.disabled = true;
    return;
  }
  const needle = query.toLowerCase();
  for (const lc of bodyEl.querySelectorAll(".lc")) {
    highlightIn(lc, needle);
  }
  if (countEl) countEl.textContent = searchMarks.length ? "1/" + searchMarks.length : "0";
  const has = searchMarks.length > 0;
  if (prevBtn) prevBtn.disabled = !has;
  if (nextBtn) nextBtn.disabled = !has;
  if (has) setActiveMark(0);
}

// highlightIn walks the text nodes of one line and wraps case-insensitive matches
// of needle in <mark>, preserving the surrounding ANSI-colored spans.
function highlightIn(lc, needle) {
  const walker = document.createTreeWalker(lc, NodeFilter.SHOW_TEXT);
  const textNodes = [];
  let n;
  while ((n = walker.nextNode())) textNodes.push(n);
  for (const node of textNodes) {
    const text = node.nodeValue;
    const lower = text.toLowerCase();
    let idx = lower.indexOf(needle);
    if (idx < 0) continue;
    const frag = document.createDocumentFragment();
    let pos = 0;
    while (idx >= 0) {
      if (idx > pos) frag.appendChild(document.createTextNode(text.slice(pos, idx)));
      const mark = document.createElement("mark");
      mark.textContent = text.slice(idx, idx + needle.length);
      frag.appendChild(mark);
      searchMarks.push(mark);
      pos = idx + needle.length;
      idx = lower.indexOf(needle, pos);
    }
    if (pos < text.length) frag.appendChild(document.createTextNode(text.slice(pos)));
    node.parentNode.replaceChild(frag, node);
  }
}

function clearMarks() {
  for (const mark of searchMarks) {
    const parent = mark.parentNode;
    if (!parent) continue;
    parent.replaceChild(document.createTextNode(mark.textContent), mark);
    parent.normalize();
  }
  searchMarks = [];
  activeMark = -1;
}

function setActiveMark(i) {
  if (!searchMarks.length) return;
  if (activeMark >= 0 && searchMarks[activeMark]) searchMarks[activeMark].classList.remove("active");
  activeMark = (i + searchMarks.length) % searchMarks.length;
  const mark = searchMarks[activeMark];
  mark.classList.add("active");
  // Expand a collapsed section so the active match is visible.
  const sec = mark.closest(".log-section");
  if (sec && sec.classList.contains("collapsed")) {
    sec.classList.remove("collapsed");
    const head = sec.querySelector(".log-section-head");
    if (head) head.setAttribute("aria-expanded", "true");
  }
  mark.scrollIntoView({ block: "center", behavior: "smooth" });
  const countEl = el("search-count");
  if (countEl) countEl.textContent = activeMark + 1 + "/" + searchMarks.length;
}

// --- Controls -----------------------------------------------------------------
function wireControls() {
  const copyBtn = el("copy-all-btn");
  if (copyBtn) {
    copyBtn.disabled = true;
    copyBtn.addEventListener("click", () => copyToClipboard(rawTextPlain(), copyBtn));
  }

  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) {
    cmdBtn.addEventListener("click", () => {
      if (currentRef) copyToClipboard("magus query " + currentRef, cmdBtn);
    });
  }

  // Pretty <-> raw toggle. Raw shows the exact captured text (flat, no folds/badges);
  // pretty is the stylized structural view. Re-renders and clears any active search.
  const viewBtn = el("view-toggle");
  if (viewBtn) {
    viewBtn.addEventListener("click", () => {
      pretty = !pretty;
      setBtnLabel(viewBtn, pretty ? "Raw" : "Pretty");
      viewBtn.setAttribute("aria-pressed", pretty ? "false" : "true");
      const searchEl = el("log-search");
      if (searchEl) searchEl.value = "";
      clearMarks();
      const cnt = el("search-count");
      if (cnt) cnt.textContent = "";
      if (model) render();
      const fold = el("fold-all-btn");
      if (fold) fold.hidden = !model || model.titled === 0 || !pretty;
    });
  }

  const foldBtn = el("fold-all-btn");
  if (foldBtn) {
    foldBtn.addEventListener("click", () => {
      const secs = [...bodyEl.querySelectorAll(".log-section")];
      const anyOpen = secs.some((s) => !s.classList.contains("collapsed"));
      for (const s of secs) {
        s.classList.toggle("collapsed", anyOpen);
        const head = s.querySelector(".log-section-head");
        if (head) head.setAttribute("aria-expanded", anyOpen ? "false" : "true");
      }
      setBtnLabel(foldBtn, anyOpen ? "Expand all" : "Collapse all");
    });
  }

  const searchEl = el("log-search");
  if (searchEl) {
    let t;
    searchEl.addEventListener("input", () => {
      clearTimeout(t);
      t = setTimeout(() => runSearch(searchEl.value.trim()), 120);
    });
    searchEl.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") { ev.preventDefault(); setActiveMark(activeMark + (ev.shiftKey ? -1 : 1)); }
    });
  }
  // "/" focuses search, like less/vim; ignored while typing in a field.
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "/" && searchEl && !isTyping(ev.target)) { ev.preventDefault(); searchEl.focus(); }
  });

  const pauseBtn = el("pause-btn");
  if (pauseBtn) {
    pauseBtn.addEventListener("click", () => {
      livePaused = !livePaused;
      setBtnLabel(pauseBtn, livePaused ? "Resume" : "Pause");
      pauseBtn.setAttribute("aria-pressed", livePaused ? "true" : "false");
      // Resuming jumps back to the tail so the reader rejoins the live edge.
      if (!livePaused && scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
    });
  }

  wireFullscreen();
}

function isTyping(node) {
  const t = (node && node.tagName) || "";
  return t === "INPUT" || t === "TEXTAREA" || (node && node.isContentEditable);
}

function rawTextPlain() {
  // Copy the log as plain text without ANSI escapes (they're already parsed away in
  // the DOM, but rawText holds the original which may still contain them).
  return stripAnsi(rawText);
}

function wireFullscreen() {
  const btn = el("fullscreen-btn");
  if (!btn || !panelEl || !panelEl.requestFullscreen) { if (btn) btn.hidden = true; return; }
  btn.addEventListener("click", () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else panelEl.requestFullscreen();
  });
  document.addEventListener("fullscreenchange", () => {
    const on = document.fullscreenElement === panelEl;
    btn.textContent = on ? "Exit fullscreen" : "Fullscreen";
    btn.setAttribute("aria-pressed", on ? "true" : "false");
  });
}

// --- Input: drag-and-drop -----------------------------------------------------
// Dropping a saved log file onto the panel still loads it (an undocumented convenience);
// the paste box and file picker were removed - the viewer opens links, it isn't an editor.
function wireInput() {
  if (panelEl) {
    panelEl.addEventListener("dragover", (ev) => { ev.preventDefault(); panelEl.classList.add("drag-over"); });
    panelEl.addEventListener("dragleave", () => panelEl.classList.remove("drag-over"));
    panelEl.addEventListener("drop", (ev) => {
      ev.preventDefault();
      panelEl.classList.remove("drag-over");
      const f = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
      if (f) f.text().then((text) => loadText(text, f.name));
    });
  }
}

// --- Small helpers ------------------------------------------------------------
function setStatus(msg, isErr) {
  if (!statusEl) return;
  statusEl.textContent = msg || "";
  statusEl.classList.toggle("err", !!isErr);
}

function copyToClipboard(text, btn) {
  const done = (ok) => {
    if (!btn) return;
    const prev = btn.textContent;
    btn.textContent = ok ? "copied" : "failed";
    setTimeout(() => { btn.textContent = prev; }, 1200);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(() => done(true), () => done(false));
  } else {
    done(false);
  }
}
