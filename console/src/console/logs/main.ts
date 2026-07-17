// main.ts - the Log Viewer composition root. A purpose-built, read-only viewer for a magus
// run's captured output: the #data= fragment carries a magus.viewer.v1 Journal (protobuf,
// gzip+base64url), decoded here and rendered pretty from its STRUCTURE (per-target groups, exec
// command boundaries, result status) - no text-heuristic guessing. A pasted / dropped / #src=-
// fetched log has no structure, so it falls back to the heuristic text parse. Everything is
// local: nothing is ever uploaded.
//
// This module owns the load orchestration (which #-param path to take), the toolbar/keyboard
// wiring, and boot; the concern modules (fragment, model, render, waterfall, filter, search,
// live, demo, share) hold the rest. It is a standalone esbuild bundle (it imports the proto
// client), NOT composed through the docs main.ts. Every handler guards on its DOM target, so it
// is a no-op if the scaffold is absent.

import { fromBinary } from "@bufbuild/protobuf";
import { JournalSchema } from "../../gen/magus/viewer/v1/viewer_pb";
import type { Journal } from "../../gen/magus/viewer/v1/viewer_pb";
import { parseHash, wantsDemo } from "../../lib/daemon";
import { decodeFragmentBytes, viewerParams } from "./fragment";
import { state, waterfallSource } from "./state";
import {
  bodyEl, copyToClipboard, el, emptyEl, flipToggleGroup, panelEl, resolveDom, scrollEl,
  setBtnLabel, setRefIdentity, setStatus, setToggleGroup,
} from "./dom";
import { stripAnsi } from "../render/ansi";
import { buildModel, buildModelMulti } from "./model";
import { render, updateTimelineControl } from "./render";
import { applyTimeRange, clearFocus } from "./waterfall";
import { applyFilterFromInput, renderFilterChips, setFilter } from "./filter";
import { clearMarks, runSearch, stepActiveMark } from "./search";
import { graphTarget, openInGraph, shareLink } from "./share";
import { connectLive } from "./live";
import { startDemo } from "./demo";
import { installKeybindings, mergeKeymap, registerCommand, type Keymap } from "../commands";
import { wireToolbarOverflow } from "../toolbar";
import { persisted } from "../../lib/persist";
import { attachHelpPopover } from "../../ui/help-popover";

// init() is invoked at the BOTTOM of this module (see the final line), after every shared state
// field has initialized. The order matters: loadFromURL()'s setFilter() applies the #q= deep link,
// which must survive - the shared state.filterParsed is seeded once in state.ts, so nothing later
// clobbers it back to empty.
function init(): void {
  wireControls();
  wireCommands();
  wireZoom();
  wireInput();
  loadFromURL();
}

// --- Zoom -------------------------------------------------------------------
// A content zoom for the viewer body: in the text view it enlarges the log text (which re-wraps),
// in the waterfall it magnifies the timeline with scroll. Implemented with CSS `zoom` on the body
// so both views scale uniformly and the scroll container grows to match. Driven by the -/+ control
// in the status bar and the =/-/0 keys; the level persists so it sticks across loads.
const ZOOM_MIN = 0.7;
const ZOOM_MAX = 2.2;
const ZOOM_STEP = 0.1;
const zoomCell = persisted<number>("logs-zoom", 1);

function clampZoom(z: number): number {
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(z * 10) / 10));
}

function applyZoom(): void {
  const z = clampZoom(zoomCell.get());
  // One knob, two levers (in logs.css): the text view scales font-size; the waterfall zooms its
  // SVG so it grows past the panel and the scroll box picks it up. A width:100% SVG would just
  // re-fit under a plain body zoom, so the waterfall needs its own.
  bodyEl.style.setProperty("--log-zoom", String(z));
  const readout = el("console-log-zoom__readout");
  if (readout) readout.textContent = Math.round(z * 100) + "%";
}

function setZoom(z: number): void {
  zoomCell.set(clampZoom(z));
  applyZoom();
}

// zoomSeg builds one control as a real <button>: the -/+ steppers and the percent readout (which
// doubles as the reset control). Keyboard activation and focus come for free from the button; the
// status-bar styling in logs.css keeps them plain and dense (no PF button chrome).
function zoomSeg(key: string, label: string, aria: string): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.className = key === "reset" ? "console-log-zoom__readout" : "console-log-zoom__btn";
  if (key === "reset") b.id = "console-log-zoom__readout"; // applyZoom updates the percent readout by this id
  b.dataset.zoom = key;
  b.textContent = label;
  b.setAttribute("aria-label", aria);
  b.title = aria;
  return b;
}

function wireZoom(): void {
  // The control lives in the shared status bar's right cluster, by the event count.
  const right = document.querySelector("#console-statusbar .console-shell-statusbar__right");
  if (right) {
    const ctl = document.createElement("div");
    ctl.className = "console-log-zoom console-shell-statusbar__item";
    ctl.setAttribute("role", "group");
    ctl.setAttribute("aria-label", "Zoom");
    ctl.append(zoomSeg("out", "-", "Zoom out"), zoomSeg("reset", "100%", "Reset zoom"), zoomSeg("in", "+", "Zoom in"));
    // Buttons fire click on Enter/Space natively, so a delegated click handler is all we need.
    ctl.addEventListener("click", (ev) => {
      const t = (ev.target as HTMLElement).closest("[data-zoom]") as HTMLElement | null;
      if (!t) return;
      const k = t.dataset.zoom;
      if (k === "in") setZoom(zoomCell.get() + ZOOM_STEP);
      else if (k === "out") setZoom(zoomCell.get() - ZOOM_STEP);
      else setZoom(1);
    });
    right.prepend(ctl);
  }
  registerCommand({ id: "logs.zoomIn", label: "Zoom in", group: "Log Viewer", run: () => setZoom(zoomCell.get() + ZOOM_STEP) });
  registerCommand({ id: "logs.zoomOut", label: "Zoom out", group: "Log Viewer", run: () => setZoom(zoomCell.get() - ZOOM_STEP) });
  registerCommand({ id: "logs.zoomReset", label: "Reset zoom", group: "Log Viewer", run: () => setZoom(1) });
  applyZoom();
}

// --- Keyboard commands --------------------------------------------------------
// The log viewer's actions double as named commands so a keybinding (and, later, the console's
// menu/command bar) can trigger them. Each command DISPATCHES TO the existing control - the button's
// own click handler stays the single source of truth for the action, so there is no duplicated
// behavior. The default chords are single keys, matching the viewer's existing "/" idiom (a
// keyboard-driven reader, like less/gh) and deliberately avoiding browser-owned combos
// (mod+r reload, mod+t/mod+shift+t tab). A user override lives in the shared persisted keymap.
const LOGS_KEYMAP: Keymap = {
  "logs.filter": "/",     // focus the filter box
  "logs.raw": "r",        // toggle raw / pretty
  "logs.timeline": "t",   // toggle timeline / log
  "logs.fold": "f",       // collapse / expand all
  "logs.zoomIn": "=",     // enlarge the view (text bigger / waterfall magnified)
  "logs.zoomOut": "-",    // shrink the view
  "logs.zoomReset": "0",  // back to 100%
};
const keymapCell = persisted<Keymap>("keymap", {});

// clickControl triggers a toolbar button's own handler; a disabled control is a no-op, exactly as
// clicking it in the UI would be (so a keybinding never does what the button cannot).
function clickControl(id: string): void {
  const btn = el(id) as HTMLButtonElement | null;
  if (btn && !btn.disabled) btn.click();
}

function wireCommands(): void {
  registerCommand({ id: "logs.filter", label: "Focus filter", group: "Log Viewer", run: () => { const f = el("log-filter") || el("log-search"); if (f) f.focus(); } });
  registerCommand({ id: "logs.raw", label: "Toggle raw / pretty", group: "Log Viewer", run: () => flipToggleGroup("view-mode") });
  registerCommand({ id: "logs.timeline", label: "Toggle timeline / log", group: "Log Viewer", run: () => flipToggleGroup("timeline-mode") });
  registerCommand({ id: "logs.fold", label: "Collapse / expand all", group: "Log Viewer", run: () => clickControl("fold-all-btn") });
  installKeybindings(() => mergeKeymap(LOGS_KEYMAP, keymapCell.get()));
}

async function loadFromURL(): Promise<void> {
  const params = viewerParams();
  const ref = params.ref || "";
  // Apply a shared #q= filter (the graph explorer's convention, read via the shared parseHash)
  // BEFORE any mode renders, and seed the filter box. It combines with #ref/#data/#live/#demo,
  // so a deep link like `#demo&q=status:fail` lands already narrowed.
  const q = parseHash().q || "";
  setFilter(q);
  renderFilterChips();
  const filterEl = el("log-filter");
  if (filterEl) (filterEl as HTMLInputElement).value = q;
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
      let journal: Journal | null = null;
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

function loadText(text: string, ref: string): void {
  state.rawLines = null;
  state.currentJournal = null;
  state.currentJournals = null; // a plain text log is a single invocation; drop any prior multi
  state.model = buildModel(text);
  state.rawText = text;
  finishLoad(ref, summarize(text));
}

// loadJournal renders a magus.viewer.v1 Journal (the structured #data path): it builds the
// SAME section model the heuristic produces - so render()/search/fold/copy work unchanged -
// but from EVENTS, so grouping and status are exact, not regex-guessed.
function loadJournal(journal: Journal, ref: string): void {
  state.currentJournal = journal;
  state.currentJournals = null; // a single loaded journal; drop any prior multi-invocation set
  const built = buildModelMulti(waterfallSource());
  state.model = { sections: built.sections, titled: built.titled };
  state.rawLines = built.rawLines;
  state.rawText = built.rawLines.join("\n");
  finishLoad(ref, built.summary);
}

function finishLoad(ref: string, statusMsg: string): void {
  state.currentRef = looksLikeRef(ref) ? ref : "";
  if (emptyEl) emptyEl.hidden = true;
  setRefIdentity(ref || "log", looksLikeRef(ref));
  // Resolve the Timeline button (and reset the mode if the new log has no timing) before
  // render() so a stale timeline=true from a previous log cannot try to plot a text log.
  updateTimelineControl();
  render();
  setStatus(statusMsg);
  const foldBtn = el("fold-all-btn");
  if (foldBtn) (foldBtn as HTMLButtonElement).disabled = state.timeline || state.model!.titled === 0 || !state.pretty;
  const copyBtn = el("copy-all-btn");
  if (copyBtn) (copyBtn as HTMLButtonElement).disabled = false;
  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) (cmdBtn as HTMLButtonElement).disabled = !state.currentRef;
  const shareBtn = el("share-btn");
  if (shareBtn) (shareBtn as HTMLButtonElement).disabled = false;
  // "Open in graph" only makes sense with a real ref AND a target the graph knows about
  // (a project + target from the result event); hide it otherwise.
  const graphBtn = el("graph-btn");
  if (graphBtn) (graphBtn as HTMLButtonElement).disabled = !graphTarget();
}

// looksLikeRef mirrors the CLI's cache.LooksLikeRef: the "copy as command" buttons
// only make sense when the page was seeded by a real ref (not a pasted file name).
function looksLikeRef(s: string): boolean {
  return typeof s === "string" && /^out[0-9a-f]+$/.test(s);
}

function summarize(text: string): string {
  const lines = text ? text.split("\n").length : 0;
  const bytes = new Blob([text]).size;
  return lines + " line" + (lines === 1 ? "" : "s") + ", " + humanBytes(bytes);
}

function humanBytes(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

function rawTextPlain(): string {
  // Copy the log as plain text without ANSI escapes (they're already parsed away in
  // the DOM, but rawText holds the original which may still contain them).
  return stripAnsi(state.rawText);
}

// --- Controls -----------------------------------------------------------------
function wireControls(): void {
  const copyBtn = el("copy-all-btn");
  if (copyBtn) {
    (copyBtn as HTMLButtonElement).disabled = true;
    copyBtn.addEventListener("click", () => copyToClipboard(rawTextPlain(), copyBtn));
  }

  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) {
    cmdBtn.addEventListener("click", () => {
      if (state.currentRef) copyToClipboard("magus query " + state.currentRef, cmdBtn);
    });
  }

  const shareBtn = el("share-btn");
  if (shareBtn) {
    (shareBtn as HTMLButtonElement).disabled = true;
    shareBtn.addEventListener("click", () => shareLink(shareBtn));
  }

  const graphBtn = el("graph-btn");
  if (graphBtn) {
    graphBtn.addEventListener("click", openInGraph);
  }

  // The two mode switches are PF ToggleGroups (segmented controls). A delegated click on the group
  // reads which option was chosen (data-mode) and flips the corresponding view state. Clearing the
  // active search + re-rendering is shared; the fold button only applies in the pretty log view.
  const clearSearch = (): void => {
    const searchEl = el("log-search");
    if (searchEl) (searchEl as HTMLInputElement).value = "";
    clearMarks();
    const cnt = el("search-count");
    if (cnt) cnt.textContent = "";
  };
  const syncFold = (): void => {
    const fold = el("fold-all-btn");
    if (fold) fold.hidden = state.timeline || !state.model || state.model.titled === 0 || !state.pretty;
  };

  // Pretty <-> raw. Raw shows the exact captured text (flat, no folds/badges); pretty is the
  // stylized structural view.
  const viewGroup = el("view-mode");
  if (viewGroup) {
    viewGroup.addEventListener("click", (ev) => {
      const btn = (ev.target as HTMLElement).closest<HTMLButtonElement>(".pf-v6-c-toggle-group__button");
      if (!btn || btn.disabled) return;
      const raw = btn.dataset.mode === "raw";
      if (state.pretty === !raw) return; // already selected
      state.pretty = !raw;
      setToggleGroup("view-mode", raw);
      clearSearch();
      if (state.model) render();
      syncFold();
    });
  }

  // Timeline <-> log. Switches the body between the trace waterfall and the log view, and re-syncs
  // the sibling controls (pretty/raw + fold) that do not apply while the waterfall is shown.
  const timelineGroup = el("timeline-mode");
  if (timelineGroup) {
    timelineGroup.addEventListener("click", (ev) => {
      const btn = (ev.target as HTMLElement).closest<HTMLButtonElement>(".pf-v6-c-toggle-group__button");
      if (!btn || btn.disabled) return;
      const timeline = btn.dataset.mode === "timeline";
      if (state.timeline === timeline) return;
      state.timeline = timeline;
      setToggleGroup("timeline-mode", timeline);
      clearSearch();
      updateTimelineControl();
      if (state.model) render();
      syncFold();
    });
  }

  const foldBtn = el("fold-all-btn");
  if (foldBtn) {
    foldBtn.addEventListener("click", () => {
      const secs = [...bodyEl.querySelectorAll(".console-render-section")];
      const anyOpen = secs.some((s) => !s.hasAttribute("data-collapsed"));
      for (const s of secs) {
        s.toggleAttribute("data-collapsed", anyOpen);
        const head = s.querySelector(".console-render-section__head");
        if (head) head.setAttribute("aria-expanded", anyOpen ? "false" : "true");
      }
      setBtnLabel(foldBtn, anyOpen ? "Expand all" : "Collapse all");
    });
  }

  const searchEl = el("log-search");
  if (searchEl) {
    let t: ReturnType<typeof setTimeout>;
    searchEl.addEventListener("input", () => {
      clearTimeout(t);
      t = setTimeout(() => runSearch((searchEl as HTMLInputElement).value.trim()), 120);
    });
    searchEl.addEventListener("keydown", (ev) => {
      if ((ev as KeyboardEvent).key === "Enter") { ev.preventDefault(); stepActiveMark((ev as KeyboardEvent).shiftKey ? -1 : 1); }
    });
  }
  // Filter syntax help: the "?" trigger's title= is a tooltip only (invisible on touch, no click
  // handler); attachHelpPopover upgrades it into a tap-to-open popover, reading that same title=
  // as the body text.
  const filterHelpBtn = el("log-filter-help");
  if (filterHelpBtn) attachHelpPopover(filterHelpBtn);

  // Filter box: debounced live-filter that narrows both views and syncs the #q= fragment.
  const filterEl = el("log-filter");
  if (filterEl) {
    let ft: ReturnType<typeof setTimeout>;
    filterEl.addEventListener("input", () => {
      clearTimeout(ft);
      ft = setTimeout(() => applyFilterFromInput((filterEl as HTMLInputElement).value), 150);
    });
    // Escape clears the filter (and the #q= fragment) for a quick reset.
    filterEl.addEventListener("keydown", (ev) => {
      if ((ev as KeyboardEvent).key === "Escape") { ev.preventDefault(); (filterEl as HTMLInputElement).value = ""; applyFilterFromInput(""); }
    });
  }

  // Time range: the wall-clock preset picker and the brushed-window reset.
  const timeSel = el("time-range");
  if (timeSel) timeSel.addEventListener("change", () => applyTimeRange((timeSel as HTMLSelectElement).value));
  const focusResetBtn = el("console-log-focus__reset");
  if (focusResetBtn) focusResetBtn.addEventListener("click", clearFocus);

  const pauseBtn = el("pause-btn");
  if (pauseBtn) {
    pauseBtn.addEventListener("click", () => {
      state.livePaused = !state.livePaused;
      setBtnLabel(pauseBtn, state.livePaused ? "Resume" : "Pause");
      pauseBtn.setAttribute("aria-pressed", state.livePaused ? "true" : "false");
      // Resuming jumps back to the tail so the reader rejoins the live edge.
      if (!state.livePaused && scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
    });
  }

  wireFullscreen();

  // Collapse the secondary controls behind the PF toolbar toggle on narrow viewports (the shared
  // responsive-toolbar pattern the graph explorer uses too).
  wireToolbarOverflow();
}

function wireFullscreen(): void {
  const btn = el("fullscreen-btn");
  const panel = panelEl;
  if (!btn || !panel || !panel.requestFullscreen) { if (btn) (btn as HTMLButtonElement).disabled = true; return; }
  btn.addEventListener("click", () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else panel.requestFullscreen();
  });
  document.addEventListener("fullscreenchange", () => {
    const on = document.fullscreenElement === panel;
    btn.textContent = on ? "Exit fullscreen" : "Fullscreen";
    btn.setAttribute("aria-pressed", on ? "true" : "false");
  });
}

// --- Input: drag-and-drop -----------------------------------------------------
// Dropping a saved log file onto the panel still loads it (an undocumented convenience);
// the paste box and file picker were removed - the viewer opens links, it isn't an editor.
function wireInput(): void {
  const panel = panelEl;
  if (panel) {
    panel.addEventListener("dragover", (ev) => { ev.preventDefault(); panel.setAttribute("data-drag-over", ""); });
    panel.addEventListener("dragleave", () => panel.removeAttribute("data-drag-over"));
    panel.addEventListener("drop", (ev) => {
      ev.preventDefault();
      panel.removeAttribute("data-drag-over");
      const f = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
      if (f) f.text().then((text) => loadText(text, f.name));
    });
  }
}

// activate boots the viewer: resolve the DOM handles (the scaffold must already be present), then
// wire and load. Exported so the console's logs PageModule can drive it after injecting the scaffold
// into a host; the standalone page auto-boots below. init()'s ordering is preserved - every shared
// state field is initialized before it runs, so loadFromURL()'s #q= setFilter is not clobbered.
export function activate(): void {
  resolveDom();
  if (bodyEl && scrollEl) init();
}

// deactivate aborts a live stream if one is running, so closing the logs tab or pane leaves no SSE
// connection open. Static logs (the common case) never open a stream, so this is a no-op then. The
// console's logs PageModule calls it on deactivate; the standalone page does not.
export function deactivate(): void {
  if (state.liveAbort) { state.liveAbort.abort(); state.liveAbort = null; }
}

// Standalone auto-boot: only when the scaffold is already in the document at load. In the console the
// scaffold is injected into a host AFTER this module imports, so the console calls activate() itself.
if (document.getElementById("log-body")) activate();
