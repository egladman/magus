// log-viewer.js - the /logs/ Log Viewer. A purpose-built, read-only viewer for a magus
// run's captured output. The `#data=` fragment carries a magus.viewer.v1 Journal (protobuf,
// gzip+base64url), decoded here with the generated @bufbuild/protobuf client and rendered
// pretty from its STRUCTURE (per-target groups, exec command boundaries, result status) -
// no text-heuristic guessing. A pasted / dropped / `#src=`-fetched log has no structure, so
// it falls back to the heuristic text parse. Everything is local: nothing is ever uploaded.
//
// Bundled (esbuild) because it imports the proto client; every handler guards on its DOM
// target, so it is a no-op if the scaffold is absent (e.g. main.js loading on another page).

import { fromBinary, toBinary, create } from "@bufbuild/protobuf";
import { JournalSchema, EventSchema, Kind, Status, Stream } from "../../gen/magus/viewer/v1/viewer_pb";
// The loopback lock, the shared bearer token, and the fetch-based SSE reader are the
// SAME security-critical helpers all three tool pages use; they live in one audited
// module now instead of being copy-pasted here. Live-mode host is /events on the
// ephemeral per-run server (not the daemon's /api/v1/events), but the helpers are identical.
import { validateLiveHost, consumeLiveToken, getLiveToken, fetchSSE, parseHash, wantsDemo } from "../../lib/daemon";

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
// init() is invoked at the BOTTOM of this module (see the final line), after every top-level
// `let` (pretty/timeline/filterParsed/...) has initialized. Calling it here would run
// loadFromURL()'s synchronous setFilter() before `let filterParsed = parseQuery("")` executes,
// and that later initializer would then clobber the loaded #q= filter back to empty.

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

// --- Fragment encode (the inverse of decodeFragmentBytes) ---------------------
// bytes -> gzip -> base64url. Mirrors internal/render EncodeFragmentRaw so a link built
// here round-trips through the same decode path. Local only: the Share button never leaves
// the page. base64url = RawURLEncoding (base64, then +/- and //_ swaps, no "=" padding).
async function encodeFragmentBytes(bytes) {
  const stream = new Response(bytes).body.pipeThrough(new CompressionStream("gzip"));
  const gz = new Uint8Array(await new Response(stream).arrayBuffer());
  let bin = "";
  for (let i = 0; i < gz.length; i++) bin += String.fromCharCode(gz[i]);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
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
  // Apply a shared #q= filter (the graph explorer's convention, read via the shared parseHash)
  // BEFORE any mode renders, and seed the filter box. It combines with #ref/#data/#live/#demo,
  // so a deep link like `#demo&q=status:fail` lands already narrowed.
  const q = parseHash().q || "";
  setFilter(q);
  renderFilterChips();
  const filterEl = el("log-filter");
  if (filterEl) filterEl.value = q;
  // The shared bare `#demo` fragment (wantsDemo, from lib/daemon - the same trigger the
  // dashboard and graph explorer use) enters the daemon-free showcase: a synthetic run
  // streams in with a live-filling waterfall.
  if (wantsDemo(parseHash())) {
    startDemo();
    return;
  }
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
// currentJournal holds the structured Journal when one is loaded (the #data protobuf path),
// null in heuristic/text mode. It backs Share (re-encode the exact structure) and Open in
// graph (read project/target off the result event).
let currentJournal = null;
// currentJournals, when set, is a LIST of journals (multiple invocations) rendered as separate
// groups. Takes precedence over currentJournal; the live buffer streams alongside it.
let currentJournals = null;
// pretty toggles the stylized structural view (default) vs the raw captured text.
let pretty = true;
// timeline toggles the trace-waterfall view (targets + steps on a shared time axis) over
// the log/section view. Only offered when the loaded Journal carries enough timing to plot
// (see buildSpans); a pasted/text log has none, so the Timeline button stays hidden.
let timeline = false;
// filterQuery is the raw filter string (mirrored to the #q= fragment); filterParsed is its
// parsed form ({groups, texts, empty}). It narrows the pretty view (hides non-matching lines /
// target groups) and dims the waterfall's non-matching spans. See parseQuery for the grammar.
let filterQuery = "";
let filterParsed = parseQuery("");
// focusWin is the active time-range focus, {a, b} in absolute ms, or null for the full run.
// Set by dragging (brush) across the waterfall or by the wall-clock preset picker. It is a
// plain time window over events, so it is invocation-agnostic and extends to multi-invocation.
let focusWin = null;

// rawLines holds the pure captured output (reconstructed from a Journal's output events)
// so the RAW view shows exactly what `magus query <ref>` prints. null in heuristic (text)
// mode, where the RAW view falls back to the parsed section lines.
let rawLines = null;

function loadText(text, ref) {
  rawLines = null;
  currentJournal = null;
  currentJournals = null; // a plain text log is a single invocation; drop any prior multi
  model = buildModel(text);
  rawText = text;
  finishLoad(ref, summarize(text));
}

// loadJournal renders a magus.viewer.v1 Journal (the structured #data path): it builds the
// SAME section model the heuristic produces - so render()/search/fold/copy work unchanged -
// but from EVENTS, so grouping and status are exact, not regex-guessed.
function loadJournal(journal, ref) {
  currentJournal = journal;
  currentJournals = null; // a single loaded journal; drop any prior multi-invocation set
  const built = buildModelMulti(waterfallSource());
  model = { sections: built.sections, titled: built.titled };
  rawLines = built.rawLines;
  rawText = built.rawLines.join("\n");
  finishLoad(ref, built.summary);
}

function finishLoad(ref, statusMsg) {
  currentRef = looksLikeRef(ref) ? ref : "";
  if (emptyEl) emptyEl.hidden = true;
  setRefIdentity(ref || "log", looksLikeRef(ref));
  // Resolve the Timeline button (and reset the mode if the new log has no timing) before
  // render() so a stale timeline=true from a previous log cannot try to plot a text log.
  updateTimelineControl();
  render();
  setStatus(statusMsg);
  const foldBtn = el("fold-all-btn");
  if (foldBtn) foldBtn.disabled = timeline || model.titled === 0 || !pretty;
  const copyBtn = el("copy-all-btn");
  if (copyBtn) copyBtn.disabled = false;
  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) cmdBtn.disabled = !currentRef;
  const shareBtn = el("share-btn");
  if (shareBtn) shareBtn.disabled = false;
  // "Open in graph" only makes sense with a real ref AND a target the graph knows about
  // (a project + target from the result event); hide it otherwise.
  const graphBtn = el("graph-btn");
  if (graphBtn) graphBtn.disabled = !graphTarget();
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
      const key = ev.project + " " + ev.target;
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
    // meta carries the structured (label, status) the filter matches target:/status: against,
    // so it need not re-parse them out of the rendered title. "" status (no result yet) reads
    // as "running". Preamble sections have no meta and so never match a target:/status: term.
    const label = (g.project && g.project !== "." ? g.project + ":" : "") + (g.target || "output");
    sections.push({ title, lines: [title, ...g.body], meta: { label, status: statusName(g.result ? g.result.status : Status.UNSPECIFIED) || "running" } });
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

// --- Trace waterfall (#data / live invocation) --------------------------------
// A Datadog-style waterfall of the invocation: the magus OTel model is invocation=trace,
// target-exec=span, step=child span. The magus.viewer.v1 wire format does NOT carry explicit
// trace_id/span_id/parent_span_id (an Event has none - see proto), so the span tree is
// reconstructed structurally from what the events DO carry: the Invocation's start/end frame
// the axis; each (project,target) group is a target span (it ends at its RESULT event's time
// and starts result.time - result.duration, the exact recorded run window); and the EXEC
// events within a target are its step child-spans (each starts at its own time and ends at the
// next EXEC in the same target, or the target's end). EXEC carries no explicit duration, so a
// step's end is inferred from the next boundary - a best effort. When a target has no EXEC
// events (or only RESULT timing survives), it degrades to a target-only bar with no children.
const WF_NS = "http://www.w3.org/2000/svg";
const WF_VIEW_W = 900;   // viewBox width; the SVG scales to the panel via its viewBox
const WF_LABEL_W = 230;  // left gutter for span labels (indented for steps)
const WF_RIGHT = 64;     // right gutter for the per-target duration text
const WF_AXIS_H = 18;    // top strip for the time axis
const WF_ROW_H = 20;     // one span row
const WF_BAR_H = 11;     // a target bar
const WF_STEP_BAR_H = 7; // a step (child-span) bar
const WF_PLOT_W = WF_VIEW_W - WF_RIGHT - WF_LABEL_W;

function wfSvg(tag) {
  return document.createElementNS(WF_NS, tag);
}

// tsMs converts a protobuf Timestamp ({seconds: bigint, nanos: number}) to epoch millis, or
// null when unset (e.g. a still-running invocation's end_time).
function tsMs(ts) {
  if (!ts) return null;
  return Number(ts.seconds || 0n) * 1000 + Number(ts.nanos || 0) / 1e6;
}

// durMs converts a protobuf Duration to millis (0 when unset).
function durMs(d) {
  if (!d) return 0;
  return Number(d.seconds || 0n) * 1000 + Number(d.nanos || 0) / 1e6;
}

// durMsText renders a raw millisecond span as "12ms" / "1.20s" (the axis/label sibling of
// durText, which takes a Duration message).
function durMsText(ms) {
  if (ms < 1) return "0ms";
  return ms < 1000 ? Math.round(ms) + "ms" : (ms / 1000).toFixed(ms < 10000 ? 2 : 1) + "s";
}

// wfTrunc keeps an SVG label inside its gutter (SVG <text> does not clip); the full text
// rides a <title> tooltip.
function wfTrunc(s, max) {
  return s.length > max ? s.slice(0, max - 3) + "..." : s;
}

// waterfallSource returns the events + invocation the waterfall is built from: the loaded
// Journal (the #data path) or, in live mode, the events seen so far.
// waterfallSource returns the list of invocation-sources to render: one entry per invocation.
// Usually a single loaded/streaming invocation, but the log viewer is invocation-agnostic - a
// static currentJournals list (e.g. the multi-invocation demo) renders alongside the live
// buffer, so several invocations show as separate groups on a shared time axis.
function waterfallSource() {
  const out = [];
  if (currentJournals) currentJournals.forEach((j) => out.push({ events: j.events || [], invocation: j.invocation }));
  else if (currentJournal) return [{ events: currentJournal.events || [], invocation: currentJournal.invocation }];
  if (currentJournals || liveEvents.length || liveInvocation) out.push({ events: liveEvents, invocation: liveInvocation });
  return out.length ? out : [{ events: liveEvents, invocation: liveInvocation }];
}

// cmdLabel renders an invocation's command for a group header, e.g. "magus run vmlinux".
function cmdLabel(command) {
  if (!command) return "invocation";
  const args = (command.args || []).join(" ");
  return "magus " + (command.verb || "run") + (args ? " " + args : "");
}

// buildSpansMulti builds per-invocation span groups (each via buildSpans) and the combined
// time domain across all of them - the shared axis a multi-invocation waterfall plots on.
function buildSpansMulti(sources) {
  const groups = [];
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

// buildModelMulti builds the pretty-view model across all invocation sources: a single
// invocation renders as before; several are concatenated, each preceded by a command divider.
function buildModelMulti(sources) {
  if (sources.length <= 1) {
    const s = sources[0] || { events: [], invocation: null };
    return buildModelFromEvents(s.events || [], s.invocation);
  }
  const sections = [];
  const rawLines = [];
  let titled = 0;
  for (const s of sources) {
    const b = buildModelFromEvents(s.events || [], s.invocation);
    if (!b.sections.length) continue;
    sections.push({ title: null, lines: ["", cmdLabel(s.invocation && s.invocation.command)] });
    for (const sec of b.sections) sections.push(sec);
    for (const l of b.rawLines) rawLines.push(l);
    titled += b.titled;
  }
  return { sections, titled, rawLines, summary: sources.length + " invocations" };
}

// buildSpans reconstructs the {t0, t1, targets:[{label,status,ref,s,e,steps:[{label,s,e}]}]}
// span tree from an event stream. See the section comment for how each span's window is
// derived; returns an empty targets list when nothing carries plottable timing.
function buildSpans(events, invocation) {
  const groups = new Map();
  const order = [];
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

  const targets = [];
  for (const g of order) {
    let e = g.result && g.result.t !== null ? g.result.t : g.last;
    let s = g.result && g.result.t !== null && g.result.dur > 0 ? g.result.t - g.result.dur : g.first;
    if (s === null) continue; // no timing at all for this group; nothing to plot
    if (e === null || e < s) e = s;
    const label = (g.project && g.project !== "." ? g.project + ":" : "") + (g.target || "output");
    const steps = [];
    const timed = g.execs.filter((x) => x.t !== null);
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
function timelineAvailable() {
  return buildSpansMulti(waterfallSource()).groups.length > 0;
}

function wfTimeX(t, sp) {
  const span = sp.t1 - sp.t0 || 1;
  const c = Math.min(sp.t1, Math.max(sp.t0, t));
  return WF_LABEL_W + ((c - sp.t0) / span) * WF_PLOT_W;
}

// renderWaterfall draws the current invocation's span tree into the log body as inline SVG
// (presentation attributes + CSS classes for color, no chart library, no external request).
function renderWaterfall() {
  const multi = buildSpansMulti(waterfallSource());
  if (!multi.groups.length) {
    const p = document.createElement("p");
    p.className = "wf-empty";
    p.textContent = "This log has no timing data to plot as a waterfall.";
    bodyEl.appendChild(p);
    return;
  }

  const total = multi.t1 - multi.t0;
  // dom is the visible time domain: the focus window (clamped to the run) when set, else the
  // full span across all invocations. drawWfAxis/drawWfRow scale to dom, so a focus window
  // zooms; the shared axis is what makes the time range meaningful across invocations.
  const dom = focusFor(multi);
  const q = filterParsed;
  const filtering = !q.empty;
  const outOfWin = (s, e) => focusWin && (e < dom.t0 || s > dom.t1);
  // Multiple invocations get a labelled group header each; a single one renders headerless.
  const showHeaders = multi.groups.length > 1;

  const allTargets = multi.groups.flatMap((g) => g.targets);
  const nt = allTargets.length;
  const nMatch = filtering ? allTargets.filter((t) => targetRelevant(q, t)).length : nt;
  const caption = document.createElement("p");
  caption.className = "wf-caption";
  caption.textContent = (showHeaders ? multi.groups.length + " invocations, " : "") +
    nt + (nt === 1 ? " target" : " targets") + " over " + durMsText(total) +
    (filtering ? " - " + nMatch + " matching" : "") +
    (focusWin ? " - focused to " + durMsText(dom.t1 - dom.t0) : "");
  bodyEl.appendChild(caption);

  let rows = 0;
  for (const g of multi.groups) { rows += showHeaders ? 1 : 0; for (const t of g.targets) rows += 1 + t.steps.length; }
  const h = WF_AXIS_H + rows * WF_ROW_H + 6;

  const root = wfSvg("svg");
  root.setAttribute("viewBox", "0 0 " + WF_VIEW_W + " " + h);
  root.setAttribute("class", "wf-svg");
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
function drawWfGroupHead(root, label, y) {
  const t = wfSvg("text");
  t.setAttribute("x", "4");
  t.setAttribute("y", String(y + WF_ROW_H / 2 + 3));
  t.setAttribute("class", "wf-group-head");
  t.textContent = wfTrunc(label || "invocation", 48);
  root.appendChild(t);
}

// focusFor clamps the active focus window to the run's span, or returns the full domain.
function focusFor(spans) {
  if (!focusWin) return { t0: spans.t0, t1: spans.t1 };
  const t0 = Math.max(spans.t0, Math.min(focusWin.a, focusWin.b));
  const t1 = Math.min(spans.t1, Math.max(focusWin.a, focusWin.b));
  return t1 - t0 >= 1 ? { t0, t1 } : { t0: spans.t0, t1: spans.t1 };
}

// attachWfBrush lets you drag horizontally across the waterfall to set a focus window (the
// Datadog trace-zoom gesture). It converts client x -> SVG x -> time via the current domain,
// draws a selection rect, and on release sets focusWin and re-renders. A tiny drag is a no-op.
function attachWfBrush(root, dom, h) {
  let sx = null, rect = null;
  const toTime = (clientX) => {
    const r = root.getBoundingClientRect();
    const svgX = (clientX - r.left) * (WF_VIEW_W / r.width);
    const frac = (svgX - WF_LABEL_W) / WF_PLOT_W;
    return dom.t0 + Math.min(1, Math.max(0, frac)) * (dom.t1 - dom.t0);
  };
  const svgXOf = (clientX) => {
    const r = root.getBoundingClientRect();
    return Math.min(WF_VIEW_W, Math.max(WF_LABEL_W, (clientX - r.left) * (WF_VIEW_W / r.width)));
  };
  root.addEventListener("pointerdown", (ev) => {
    if (ev.button !== 0) return;
    sx = ev.clientX;
    rect = wfSvg("rect");
    rect.setAttribute("class", "wf-brush");
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
  const finish = (ev) => {
    if (sx === null) return;
    const a = toTime(sx), b = toTime(ev.clientX);
    sx = null; if (rect) { rect.remove(); rect = null; }
    if (Math.abs(b - a) < (dom.t1 - dom.t0) * 0.01) return; // too small: treat as a click
    focusWin = { a: Math.min(a, b), b: Math.max(a, b) };
    const sel = el("time-range"); if (sel) sel.value = "custom";
    render();
  };
  root.addEventListener("pointerup", finish);
  root.addEventListener("pointercancel", () => { sx = null; if (rect) { rect.remove(); rect = null; } });
}

// updateFocusUI reflects the focus window into the readout + reset, and enables the time-range
// picker only in waterfall mode (where a time window is meaningful).
function updateFocusUI(spans) {
  const sel = el("time-range");
  const win = el("focus-window");
  const reset = el("focus-reset");
  const active = timeline && spans && spans.groups && spans.groups.length > 0;
  if (sel) sel.disabled = !active;
  if (win) {
    if (focusWin && active) { const d = focusFor(spans); win.textContent = durMsText(d.t1 - d.t0) + " window"; win.hidden = false; }
    else win.hidden = true;
  }
  if (reset) reset.hidden = !(focusWin && active);
}

// clearFocus resets to the full run.
function clearFocus() {
  focusWin = null;
  const sel = el("time-range"); if (sel) sel.value = "all";
  if (model) render();
}

// applyTimeRange sets the focus window from a wall-clock preset (seconds back from the latest
// event), or clears it for "all". Invocation-agnostic: it is a window over event time.
function applyTimeRange(value) {
  if (value === "all" || value === "custom") { if (value === "all") focusWin = null; if (model) render(); return; }
  const secs = Number(value);
  const multi = buildSpansMulti(waterfallSource());
  if (!multi.groups.length || !Number.isFinite(secs)) return;
  focusWin = { a: multi.t1 - secs * 1000, b: multi.t1 };
  if (model) render();
}

function drawWfAxis(root, sp, h) {
  const line = wfSvg("line");
  line.setAttribute("x1", String(WF_LABEL_W));
  line.setAttribute("x2", String(WF_VIEW_W - WF_RIGHT));
  line.setAttribute("y1", String(WF_AXIS_H));
  line.setAttribute("y2", String(WF_AXIS_H));
  line.setAttribute("class", "wf-axis-line");
  root.appendChild(line);
  const total = sp.t1 - sp.t0;
  const ticks = [[sp.t0, "0"], [sp.t0 + total / 2, durMsText(total / 2)], [sp.t1, durMsText(total)]];
  for (const [t, txt] of ticks) {
    const x = wfTimeX(t, sp);
    const grid = wfSvg("line");
    grid.setAttribute("x1", String(x));
    grid.setAttribute("x2", String(x));
    grid.setAttribute("y1", String(WF_AXIS_H));
    grid.setAttribute("y2", String(h));
    grid.setAttribute("class", "wf-grid");
    root.appendChild(grid);
    const label = wfSvg("text");
    const atEnd = t === sp.t1;
    label.setAttribute("x", String(atEnd ? x - 2 : x + 2));
    label.setAttribute("y", "11");
    label.setAttribute("class", "wf-axis-label");
    if (atEnd) label.setAttribute("text-anchor", "end");
    label.textContent = txt;
    root.appendChild(label);
  }
}

function drawWfRow(root, row, y, sp) {
  const dur = row.e - row.s;
  const dim = row.dim ? " wf-dim" : ""; // dimmed when the filter excludes this span
  const label = wfSvg("text");
  label.setAttribute("x", String(row.step ? 20 : 6));
  label.setAttribute("y", String(y + WF_ROW_H / 2 + 3));
  label.setAttribute("class", "wf-label " + (row.step ? "wf-step-label" : "wf-target-label") + dim);
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
  rect.setAttribute("class", "wf-bar" + (row.step ? " wf-step" : "") + (row.status ? " status-" + row.status : "") + dim);
  const rt = wfSvg("title");
  rt.textContent = row.label + " - " + durMsText(dur);
  rect.appendChild(rt);
  root.appendChild(rect);

  // Duration text in the right gutter, target rows only (step rows would crowd it).
  if (!row.step) {
    const d = wfSvg("text");
    d.setAttribute("x", String(WF_VIEW_W - 2));
    d.setAttribute("y", String(y + WF_ROW_H / 2 + 3));
    d.setAttribute("class", "wf-dur" + dim);
    d.setAttribute("text-anchor", "end");
    d.textContent = durMsText(dur);
    root.appendChild(d);
  }
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
// demoTimer drives the #demo showcase's incremental reveal; demoActive marks the reveal in
// progress so the Timeline control is not force-disabled between frames. Both cleared on stop.
let demoTimer = null;
let demoActive = false;

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
    pauseBtn.disabled = false;
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
    const built = buildModelMulti(waterfallSource());
    model = { sections: built.sections, titled: built.titled };
    rawLines = built.rawLines;
    rawText = built.rawLines.join("\n");
    render();
    setLiveStatus(liveAbort && liveAbort.signal.aborted ? "done" : "streaming");
    updateTimelineControl();
    const foldBtn = el("fold-all-btn");
    if (foldBtn) foldBtn.disabled = timeline || model.titled === 0 || !pretty;
    const copyBtn = el("copy-all-btn");
    if (copyBtn) copyBtn.disabled = false;
    const shareBtn = el("share-btn");
    if (shareBtn) shareBtn.disabled = false;
    if (!livePaused && scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
  });
}

function setLiveStatus(state) {
  // Drive the shared console status bar's connection dot (the same element the dashboard uses), so
  // the log viewer reads the same as its sibling apps. A live stream is "connected" (green) with the
  // event count; a finished stream is "done" (still green - it completed cleanly); connecting/
  // disconnected map to those states. A statically loaded log never calls this, so the dot stays at
  // its default "not connected", which is accurate (no live daemon link).
  const conn = document.getElementById("console-conn");
  if (!conn) return;
  if (state === "streaming") {
    conn.textContent = "connected"; conn.dataset.state = "connected"; delete conn.dataset.health;
  } else if (state === "connecting") {
    conn.textContent = "connecting..."; conn.dataset.state = "connecting"; delete conn.dataset.health;
  } else if (state === "done") {
    conn.textContent = "done"; conn.dataset.state = "connected"; delete conn.dataset.health;
  } else if (state === "disconnected") {
    conn.textContent = "disconnected"; conn.dataset.state = "disconnected"; delete conn.dataset.health;
  }
  // The event count sits on the FAR RIGHT of the bar (its own item, like observing-since), not
  // appended to the connection state.
  const count = document.getElementById("console-count");
  if (count) {
    const n = liveEvents.length;
    count.textContent = n ? n + " events" : "";
    count.hidden = !n;
  }
}

function base64ToBytes(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

// --- Demo mode (#demo) --------------------------------------------------------
// A daemon-free showcase: synthesize a realistic single-invocation Journal (a `magus affected
// ci` run over ~6 targets, one of which fails), then REVEAL its events incrementally so the
// page feels like a live run streaming in. It reuses the live-stream buffer (liveEvents /
// liveInvocation) and scheduleLiveRender end to end - the synthetic events are the SAME
// magus.viewer.v1 Event messages the wire carries (built with create(EventSchema, ...)), so
// buildModelFromEvents (pretty view), buildSpans, renderWaterfall, waterfallSource, and
// updateTimelineControl all run exactly as they would for a real journal. No new render path,
// no transport, nothing fetched.

// demoTs / demoDur build protobuf Timestamp / Duration inits ({seconds: bigint, nanos}) from a
// millisecond value, the shapes tsMs / durMs decode.
function demoTs(ms) {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.floor((ms % 1000) * 1e6) };
}
function demoDur(ms) {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.floor((ms % 1000) * 1e6) };
}

// demoOutputFor returns a plausible stdout line for a synthesized EXEC command. The demo is
// an easter egg themed as a Linux kernel build, so these echo authentic kbuild chatter.
function demoOutputFor(cmd) {
  if (cmd.startsWith("CC ")) return "  " + cmd;
  if (cmd.startsWith("AR ")) return "  " + cmd;
  if (cmd.startsWith("LD ")) return "  " + cmd;
  if (cmd.startsWith("OBJCOPY")) return "  " + cmd;
  if (cmd.startsWith("MODPOST")) return "  " + cmd;
  if (cmd.startsWith("make")) return "  SYNC   include/config/auto.conf";
  return "  " + cmd;
}

// synthDemoJournal builds the showcase Journal: a STARTED/SCOPE preamble, then per target a
// set of EXEC step markers (each with an output line) staggered across the target's window and
// a terminal RESULT (status + duration + a synthetic ref), then FINISHED. Start offsets and
// durations overlap so the waterfall renders a real cascade; the Invocation start/end frame the
// axis. It is an easter egg: the whole run is themed as a Linux kernel build (`make -j` over
// the arch/mm/fs/net subsystems into vmlinux). drivers/net fails with a modpost undefined
// symbol for the red span; arch/x86 is a fast incremental (cached) hit. Everything is
// synthesized log TEXT only - no kernel source is copied, the lines are plausible fiction.
function synthDemoJournal() {
  const base = Date.now();
  // [project, target, exec command lines (kbuild steps), status, start offset ms, duration ms]
  const plan = [
    { project: "arch/x86", target: "vmlinux", execs: ["SYNC .config", "CC arch/x86/kernel/cpu/common.o"], status: Status.CACHED, start: 0, dur: 60 },
    { project: "kernel", target: "built-in", execs: ["CC kernel/sched/core.o", "CC kernel/fork.o", "AR kernel/built-in.a"], status: Status.PASS, start: 200, dur: 2600 },
    { project: "mm", target: "built-in", execs: ["CC mm/page_alloc.o", "CC mm/slub.o", "AR mm/built-in.a"], status: Status.PASS, start: 350, dur: 2200 },
    { project: "fs", target: "built-in", execs: ["CC fs/namei.o", "CC fs/ext4/inode.o", "AR fs/built-in.a"], status: Status.PASS, start: 600, dur: 3000 },
    { project: "drivers/net", target: "built-in", execs: ["CC drivers/net/ethernet/intel/e1000/e1000_main.o", "MODPOST modules-only.symvers"], status: Status.FAIL, start: 1100, dur: 2400 },
    { project: ".", target: "vmlinux", execs: ["LD vmlinux", "OBJCOPY arch/x86/boot/bzImage"], status: Status.PASS, start: 3700, dur: 1600 },
  ];
  const command = { verb: "run", args: ["vmlinux", "--", "make", "-j$(nproc)"], cwd: "/usr/src/linux", trigger: 1 };
  const events = [];
  events.push(create(EventSchema, { kind: Kind.STARTED, time: demoTs(base), command, magusVersion: "demo" }));
  events.push(create(EventSchema, { kind: Kind.SCOPE, time: demoTs(base), text: "projects: arch/x86, kernel, mm, fs, drivers/net" }));

  let maxEnd = 0;
  let n = 0;
  for (const p of plan) {
    const start = base + p.start;
    for (let i = 0; i < p.execs.length; i++) {
      const at = start + Math.round((p.dur * i) / p.execs.length);
      events.push(create(EventSchema, { kind: Kind.EXEC, time: demoTs(at), project: p.project, target: p.target, text: p.execs[i] }));
      const out = demoOutputFor(p.execs[i]);
      if (out) events.push(create(EventSchema, { kind: Kind.OUTPUT, time: demoTs(at + 12), project: p.project, target: p.target, stream: Stream.STDOUT, text: out }));
    }
    const end = start + p.dur;
    maxEnd = Math.max(maxEnd, end - base);
    if (p.status === Status.FAIL) {
      events.push(create(EventSchema, { kind: Kind.OUTPUT, time: demoTs(end - 8), project: p.project, target: p.target, stream: Stream.STDERR, text: "ERROR: modpost: \"e1000_probe\" [drivers/net/ethernet/intel/e1000/e1000.ko] undefined!" }));
    }
    if (p.status === Status.PASS && p.project === "." && p.target === "vmlinux") {
      events.push(create(EventSchema, { kind: Kind.OUTPUT, time: demoTs(end - 8), project: p.project, target: p.target, stream: Stream.STDOUT, text: "Kernel: arch/x86/boot/bzImage is ready  (#1)" }));
    }
    const ref = "refd" + (n++).toString(16).padStart(6, "0");
    events.push(create(EventSchema, { kind: Kind.RESULT, time: demoTs(end), project: p.project, target: p.target, status: p.status, ref, duration: demoDur(p.dur) }));
  }
  events.push(create(EventSchema, { kind: Kind.FINISHED, time: demoTs(base + maxEnd), level: "error" }));
  // Reveal in time order so the pretty view and waterfall both cascade as events arrive.
  events.sort((a, b) => (tsMs(a.time) || 0) - (tsMs(b.time) || 0));

  const invocation = { id: "invdemo01", command, startTime: demoTs(base), endTime: demoTs(base + maxEnd), magusVersion: "demo" };
  return create(JournalSchema, { invocation, events });
}

// startDemo enters the showcase: it frames the axis from the synthetic Invocation, opens the
// waterfall, and streams the events in over a few seconds via the shared live buffer.
function startDemo() {
  const journal = synthDemoJournal();
  const ordered = journal.events;
  // A SECOND, already-finished invocation shown alongside the streaming one, so the demo also
  // showcases multi-invocation (two groups on a shared axis). Relabelled so its header differs.
  const j2 = synthDemoJournal();
  if (j2.invocation && j2.invocation.command) { j2.invocation.command.verb = "affected"; j2.invocation.command.args = ["ci"]; }

  stopDemo();
  demoActive = true;
  // Reveal the shared demo indicator in the app bar (the one affordance every console app shares).
  // The log viewer only enters demo via the #demo fragment and never leaves it without a reload,
  // so it is shown once here; stopDemo (stream completion) leaves it up - the data is still demo.
  const demoPill = document.getElementById("console-demo");
  if (demoPill) demoPill.hidden = false;
  liveEvents = [];
  liveInvocation = journal.invocation; // frames the axis (start/end) and the command preamble
  livePaused = false;
  currentJournal = null; // waterfallSource then reads the live buffer, as in a real live run
  currentJournals = [j2]; // the completed sibling invocation, rendered as its own group
  timeline = true;       // open straight into the waterfall so it visibly fills in
  currentRef = "";
  if (emptyEl) emptyEl.hidden = true;
  setRefIdentity("demo", false);
  setLiveStatus("streaming");

  // Prime the first frame with enough events that a target span exists immediately (so the
  // waterfall is never momentarily empty): the STARTED/SCOPE preamble plus the first EXEC.
  let i = 0;
  const prime = Math.min(3, ordered.length);
  for (; i < prime; i++) liveEvents.push(ordered[i]);
  scheduleLiveRender();

  const BATCH = 3;
  const TICK_MS = 360;
  demoTimer = window.setInterval(() => {
    if (i >= ordered.length) {
      // Everything is already revealed and rendered by the prior batch tick; just settle the
      // status pill. (Re-rendering here would race scheduleLiveRender's rAF back to "streaming".)
      stopDemo();
      setLiveStatus("done");
      return;
    }
    for (let k = 0; k < BATCH && i < ordered.length; k++) liveEvents.push(ordered[i++]);
    scheduleLiveRender();
  }, TICK_MS);

  // Clear the reveal if the page is being torn down, so no timer fires after navigation.
  window.addEventListener("pagehide", stopDemo, { once: true });
}

function stopDemo() {
  if (demoTimer !== null) {
    window.clearInterval(demoTimer);
    demoTimer = null;
  }
  demoActive = false;
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

// --- Filter (#q=) -------------------------------------------------------------
// A pragmatic, log-shaped query (NOT the graph's node grammar): whitespace-split terms combined
// with AND, case-insensitive. A `key:value` term with a known key is a field filter; anything
// else is free text. Fields:
//   target:<substr>  - the event's target label (project:target) contains substr  (group-level)
//   status:pass|fail|cached|running - the target's result status                  (group-level)
//   step:<substr>    - an EXEC command / output line contains substr              (line-level)
//   <bare text>      - same as step: matches command / output text                (line-level)
// Group-level terms keep or drop a whole target group; line-level terms narrow to matching
// lines. The result serializes to the #q= fragment so a filtered view is shareable and
// deep-linkable (the dashboard/graph will build #q=-filtered log links).
function parseQuery(q) {
  const groups = [];
  const texts = [];
  for (const tok of (q || "").trim().split(/\s+/)) {
    if (!tok) continue;
    const ci = tok.indexOf(":");
    if (ci > 0) {
      const key = tok.slice(0, ci).toLowerCase();
      const val = tok.slice(ci + 1).toLowerCase();
      if (val && (key === "target" || key === "status")) { groups.push({ key, value: val }); continue; }
      if (val && key === "step") { texts.push(val); continue; }
      // Unknown key (or empty value): fall through and treat the whole token as free text.
    }
    texts.push(tok.toLowerCase());
  }
  return { groups, texts, empty: groups.length === 0 && texts.length === 0 };
}

// setFilter records a new filter string and its parsed form; render()/renderWaterfall read the
// module-level filterParsed, so every mode (static, live, demo) filters through the one path.
function setFilter(q) {
  filterQuery = (q || "").trim();
  filterParsed = parseQuery(filterQuery);
}

// matchGroup tests the group-level terms (target:/status:) against a target's label + status.
function matchGroup(q, label, status) {
  const lab = (label || "").toLowerCase();
  const st = (status || "").toLowerCase();
  for (const g of q.groups) {
    if (g.key === "target" && !lab.includes(g.value)) return false;
    if (g.key === "status" && !st.includes(g.value)) return false;
  }
  return true;
}

// matchAllTexts tests that a string contains EVERY free-text/step term (AND).
function matchAllTexts(q, str) {
  const s = (str || "").toLowerCase();
  for (const t of q.texts) if (!s.includes(t)) return false;
  return true;
}

// sectionMeta returns a target section's {label, status} for filtering: the structured meta
// attached by buildModelFromEvents when present, else derived from a heuristic section's title.
function sectionMeta(sec) {
  if (sec.meta) return sec.meta;
  return { label: stripAnsi(sec.title || ""), status: statusToken(sec.title || "") || "running" };
}

// targetRelevant tests whether a waterfall target span matches the filter (used to decide which
// rows stay bright vs dim, and for the caption's match count).
function targetRelevant(q, t) {
  if (q.empty) return true;
  if (!matchGroup(q, t.label, t.status || "running")) return false;
  return q.texts.length === 0 || matchAllTexts(q, t.label) || t.steps.some((s) => matchAllTexts(q, s.label));
}

// setQueryFragment mirrors the active filter to #q= via replaceState (no history spam),
// preserving every OTHER fragment part (#ref/#data/#live/#demo, the #L line token). Clearing
// the filter drops the q= part entirely.
function setQueryFragment(query) {
  const kept = [];
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    if (!part) continue;
    const eq = part.indexOf("=");
    const key = eq < 0 ? part : part.slice(0, eq);
    if (key === "q") continue;
    kept.push(part);
  }
  if (query) kept.push("q=" + encodeURIComponent(query));
  const frag = kept.join("&");
  history.replaceState(null, "", location.pathname + location.search + (frag ? "#" + frag : ""));
}

// renderFilterChips echoes the parsed filter under the bar as chips - fields as bordered
// pills, free text as plain chips, joined by "AND" connectives - so you can SEE how the query
// was interpreted, the same "how your query parsed" cue the docs search page shows. Reuses
// site.css's .search-chips/.qchip/.qop classes verbatim.
function renderFilterChips() {
  const host = el("filter-chips");
  if (!host) return;
  host.textContent = "";
  if (filterParsed.empty) { host.hidden = true; return; }
  const parts = filterParsed.groups.map((g) => ({ field: g.key, value: g.value }))
    .concat(filterParsed.texts.map((t) => ({ text: t })));
  parts.forEach((p, i) => {
    if (i > 0) {
      const op = document.createElement("span");
      op.className = "qop";
      op.textContent = "AND";
      host.appendChild(op);
    }
    const chip = document.createElement("span");
    if (p.field !== undefined) {
      chip.className = "qchip qchip-field";
      const b = document.createElement("b");
      b.textContent = p.field;
      chip.appendChild(b);
      chip.appendChild(document.createTextNode(":" + p.value));
    } else {
      chip.className = "qchip";
      chip.textContent = p.text;
    }
    host.appendChild(chip);
  });
  host.hidden = false;
}

// applyFilterFromInput is the debounced input handler: record the filter, sync #q=, echo the
// parsed chips, drop any stale search highlights (the DOM is rebuilt), and re-render.
function applyFilterFromInput(value) {
  setFilter(value);
  setQueryFragment(filterQuery);
  renderFilterChips();
  clearMarks();
  const cnt = el("search-count");
  if (cnt) cnt.textContent = "";
  if (model) render();
}

function render() {
  bodyEl.textContent = "";
  bodyEl.classList.toggle("raw", !pretty && !timeline);
  bodyEl.classList.toggle("wf-mode", timeline);
  // Timeline view: a trace waterfall built from the events' timing, not the log text.
  if (timeline) {
    renderWaterfall();
    return;
  }
  // Raw view: the exact captured text, flat - line numbers + ANSI color, no folds,
  // no badges, no structural chrome. The pretty view (default) styles it below.
  if (!pretty) {
    // RAW: in Journal mode, the exact reconstructed output (what `magus query <ref>` prints);
    // in heuristic mode, the parsed section lines. Flat, line-numbered, no folds/badges.
    const flat = rawLines || model.sections.flatMap((s) => s.lines);
    let n = 0;
    for (const raw of flat) bodyEl.appendChild(renderLine(raw, ++n));
    applyLineHighlight();
    return;
  }

  // The active filter narrows the pretty view: non-matching lines and whole target groups are
  // hidden. lineNo still advances over hidden rows so line numbers (and #L links) stay stable.
  const q = filterParsed;
  const filtering = !q.empty;
  let shown = 0;

  let lineNo = 0;
  for (const sec of model.sections) {
    if (sec.title === null) {
      // Preamble / flat log: no target/status, so any group-level term excludes it; with only
      // text terms, keep matching lines; unfiltered, keep all.
      for (const raw of sec.lines) {
        const n = ++lineNo;
        const keep = !filtering || (q.groups.length === 0 && matchAllTexts(q, stripAnsi(raw)));
        if (keep) { bodyEl.appendChild(renderLine(raw, n)); shown++; }
      }
      continue;
    }

    // Filter this target group: drop it entirely if a group-level term excludes it, or if text
    // terms match neither its title nor any body line. showAllBody is true when the group is
    // kept for its title/status (show the whole group); otherwise only matching lines show.
    const bodyLinesAll = sec.lines.slice(1);
    let showAllBody = true;
    if (filtering) {
      const meta = sectionMeta(sec);
      const noText = q.texts.length === 0;
      const titleHit = noText || matchAllTexts(q, stripAnsi(sec.title));
      const anyBody = noText || bodyLinesAll.some((l) => matchAllTexts(q, stripAnsi(l)));
      if (!(matchGroup(q, meta.label, meta.status) && (titleHit || anyBody))) {
        lineNo += sec.lines.length; // advance numbering past the hidden head + body
        continue;
      }
      showAllBody = titleHit;
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
    for (const raw of bodyLines) {
      const n = ++lineNo;
      if (!filtering || showAllBody || matchAllTexts(q, stripAnsi(raw))) {
        linesWrap.appendChild(renderLine(raw, n));
        shown++;
      }
    }

    secEl.append(head, linesWrap);
    bodyEl.appendChild(secEl);
    shown++; // the visible head row
  }
  if (filtering && shown === 0) {
    const note = document.createElement("p");
    note.className = "filter-empty";
    note.textContent = "No lines match the filter.";
    bodyEl.appendChild(note);
  }
  applyLineHighlight();
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
  // Clicking a line number deep-links it (GitHub-style #L<n>); shift-click extends a range.
  ln.addEventListener("click", (ev) => onLineNumberClick(lineNo, ev));
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

// --- Share: re-encode the loaded log into a #data= link -----------------------
// The Share button rebuilds the exact fragment link the viewer decodes and copies it to the
// clipboard, so a run's output can be handed off without re-running magus. The payload is the
// SAME format loadFromURL reads: toBinary(JournalSchema, ...) of a Journal, gzip+base64url.
// A structured log ships its real Journal; a heuristic/pasted log is wrapped into a minimal
// Journal (one KindOutput event per line) so the link still round-trips the structured path.
async function shareLink(btn) {
  try {
    const bytes = shareBytes();
    const blob = await encodeFragmentBytes(bytes);
    // Build the base from origin+pathname so any existing fragment/query is dropped; the
    // whole link then rides the fragment, which the browser never transmits to a server.
    const base = location.origin + location.pathname;
    const frag = (currentRef ? "ref=" + currentRef + "&" : "") + "data=" + blob;
    const url = base + "#" + frag;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(url);
      flashBtnLabel(btn, "Copied");
    } else {
      flashBtnLabel(btn, "Failed");
    }
  } catch (_) {
    flashBtnLabel(btn, "Failed");
  }
}

// shareBytes serializes the loaded log to the Journal wire bytes the viewer decodes: the real
// Journal when one is loaded, else a minimal Journal synthesized from the rendered lines (the
// same flat lines the RAW view shows) so a text/pasted log still round-trips as structure.
function shareBytes() {
  if (currentJournal) return toBinary(JournalSchema, currentJournal);
  const lines = rawLines || (model ? model.sections.flatMap((s) => s.lines) : []);
  const events = lines.map((text) => create(EventSchema, { kind: Kind.OUTPUT, text }));
  const journal = create(JournalSchema, { events });
  return toBinary(JournalSchema, journal);
}

// flashBtnLabel swaps a toolbar button's label to a transient message (e.g. "Copied") and
// reverts it after ~1.5s, without disturbing the icon (setBtnLabel touches only .btn-label).
function flashBtnLabel(btn, text) {
  if (!btn) return;
  const label = btn.querySelector(".btn-label");
  const prev = label ? label.textContent : btn.textContent;
  setBtnLabel(btn, text);
  setTimeout(() => setBtnLabel(btn, prev), 1500);
}

// --- Open in graph: jump to the target's knowledge-graph node -----------------
// graphTarget reads the (project, target) off the loaded Journal's result event - the pair
// that names a target node. Only meaningful with a real ref seed; returns null otherwise, so
// the button stays hidden for pasted/live logs that do not identify a graph target.
function graphTarget() {
  if (!currentRef || !currentJournal) return null;
  for (const ev of currentJournal.events || []) {
    if (ev.kind === Kind.RESULT && ev.project && ev.target) return { project: ev.project, target: ev.target };
  }
  return null;
}

// openInGraph builds the knowledge-graph node id exactly as internal/knowledge/id.go targetID
// spells it ("target:<project>:<target>") and opens the graph explorer on that node in a new
// tab. The node id rides the graph page's own #node= fragment, so nothing leaves the machine.
function openInGraph() {
  const t = graphTarget();
  if (!t) return;
  const nodeId = "target:" + t.project + ":" + t.target;
  window.open("../graph/#node=" + encodeURIComponent(nodeId), "_blank");
}

// --- Line-range highlight (#L10-L20, GitHub-style) ----------------------------
// A fragment token like `L10-L20` (or a single `L10`) highlights those line rows and scrolls
// the first into view. It coexists with data=/ref= (which viewerParams parses); this token
// has no "=" so it is read separately here. highlightStart tracks the anchor for shift-click.
let highlightStart = null;

function lineRangeFromHash() {
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    const m = /^L(\d+)(?:-L?(\d+))?$/.exec(part);
    if (!m) continue;
    const a = parseInt(m[1], 10);
    const b = m[2] ? parseInt(m[2], 10) : a;
    return { start: Math.min(a, b), end: Math.max(a, b) };
  }
  return null;
}

// applyLineHighlight (re)paints the .line-highlight rows from the current fragment. Called at
// the end of every render() so it survives view toggles, folds, and live re-renders.
function applyLineHighlight() {
  for (const r of bodyEl.querySelectorAll(".line-highlight")) r.classList.remove("line-highlight");
  const range = lineRangeFromHash();
  if (!range) return;
  let first = null;
  for (const ln of bodyEl.querySelectorAll(".ln")) {
    const n = parseInt(ln.textContent, 10);
    if (!(n >= range.start && n <= range.end)) continue;
    const row = ln.parentElement; // .log-line, or the .log-section-head button for a head row
    row.classList.add("line-highlight");
    // Expand a collapsed section so a highlighted body line is actually visible.
    const sec = row.closest && row.closest(".log-section");
    if (sec && sec.classList.contains("collapsed")) {
      sec.classList.remove("collapsed");
      const head = sec.querySelector(".log-section-head");
      if (head) head.setAttribute("aria-expanded", "true");
    }
    if (first === null) first = row;
  }
  if (first) first.scrollIntoView({ block: "center" });
}

// onLineNumberClick sets the fragment to a GitHub-style L token and re-highlights: a plain
// click anchors a single line (L<n>); shift-click extends from the anchor to a range (L<a>-L<b>).
function onLineNumberClick(n, ev) {
  ev.stopPropagation();
  let start = n;
  let end = n;
  if (ev.shiftKey && highlightStart !== null) {
    start = Math.min(highlightStart, n);
    end = Math.max(highlightStart, n);
  } else {
    highlightStart = n;
  }
  setLineFragment(start, end);
  applyLineHighlight();
}

// setLineFragment replaces the L token in the fragment (preserving ref=/data= and other keys)
// via replaceState, so the highlight is shareable/bookmarkable without adding a history entry.
function setLineFragment(start, end) {
  const kept = [];
  for (const part of location.hash.replace(/^#/, "").split("&")) {
    if (!part || /^L\d+/.test(part)) continue;
    kept.push(part);
  }
  kept.push(start === end ? "L" + start : "L" + start + "-L" + end);
  history.replaceState(null, "", location.pathname + location.search + "#" + kept.join("&"));
}

// updateTimelineControl shows/hides the Timeline button by whether the loaded log carries
// plottable timing, forces the mode off when it does not (a new text log), and syncs the
// button label + the sibling controls that do not apply in the waterfall view.
function updateTimelineControl() {
  const tlBtn = el("timeline-btn");
  const ok = timelineAvailable();
  if (tlBtn) tlBtn.disabled = !ok;
  // Fall back to the log view when the loaded log has no timing (a text/pasted log). During
  // the #demo reveal the first frame may briefly precede any target span, so keep the mode on.
  if (!ok && timeline && !demoActive) timeline = false;
  if (tlBtn) {
    setBtnLabel(tlBtn, timeline ? "Log" : "Timeline");
    tlBtn.setAttribute("aria-pressed", timeline ? "true" : "false");
  }
  // The pretty/raw toggle is meaningless in the waterfall; hide it while timeline is on.
  const viewBtn = el("view-toggle");
  if (viewBtn) viewBtn.disabled = timeline;
  // The time range only applies to the waterfall; renderWaterfall refreshes the readout when
  // it draws, but when NOT in timeline mode nothing else does, so disable the picker here.
  if (!timeline) updateFocusUI(null);
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

  const shareBtn = el("share-btn");
  if (shareBtn) {
    shareBtn.disabled = true;
    shareBtn.addEventListener("click", () => shareLink(shareBtn));
  }

  const graphBtn = el("graph-btn");
  if (graphBtn) {
    graphBtn.addEventListener("click", openInGraph);
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

  // Timeline <-> log toggle. Switches the body between the trace waterfall and the log view;
  // clears any active search (the waterfall has no searchable lines) and re-syncs the sibling
  // controls (pretty/raw + fold) that do not apply while the waterfall is shown.
  const timelineBtn = el("timeline-btn");
  if (timelineBtn) {
    timelineBtn.addEventListener("click", () => {
      timeline = !timeline;
      const searchEl = el("log-search");
      if (searchEl) searchEl.value = "";
      clearMarks();
      const cnt = el("search-count");
      if (cnt) cnt.textContent = "";
      updateTimelineControl();
      if (model) render();
      const fold = el("fold-all-btn");
      if (fold) fold.hidden = timeline || !model || model.titled === 0 || !pretty;
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
  // "/" focuses the filter, like less/vim; ignored while typing in a field.
  document.addEventListener("keydown", (ev) => {
    const focusEl = el("log-filter") || searchEl;
    if (ev.key === "/" && focusEl && !isTyping(ev.target)) { ev.preventDefault(); focusEl.focus(); }
  });

  // Filter box: debounced live-filter that narrows both views and syncs the #q= fragment.
  const filterEl = el("log-filter");
  if (filterEl) {
    let ft;
    filterEl.addEventListener("input", () => {
      clearTimeout(ft);
      ft = setTimeout(() => applyFilterFromInput(filterEl.value), 150);
    });
    // Escape clears the filter (and the #q= fragment) for a quick reset.
    filterEl.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape") { ev.preventDefault(); filterEl.value = ""; applyFilterFromInput(""); }
    });
  }

  // Time range: the wall-clock preset picker and the brushed-window reset.
  const timeSel = el("time-range");
  if (timeSel) timeSel.addEventListener("change", () => applyTimeRange(timeSel.value));
  const focusResetBtn = el("focus-reset");
  if (focusResetBtn) focusResetBtn.addEventListener("click", clearFocus);

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
  if (!btn || !panelEl || !panelEl.requestFullscreen) { if (btn) btn.disabled = true; return; }
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
  // The separate live event-count pill is a live-mode thing; keep it out of ref/error status.
  const countEl = el("log-count");
  if (countEl) { countEl.textContent = ""; countEl.hidden = true; }
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

// Boot last: every module-level `let` above is now initialized, so loadFromURL()'s setFilter()
// (for a #q= deep link) will not be clobbered by a later initializer.
if (bodyEl && scrollEl) {
  init();
}
