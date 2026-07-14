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
  bodyEl, copyToClipboard, el, emptyEl, isTyping, panelEl, scrollEl,
  setBtnLabel, setRefIdentity, setStatus,
} from "./dom";
import { stripAnsi } from "./ansi";
import { buildModel, buildModelMulti } from "./model";
import { render, updateTimelineControl } from "./render";
import { applyTimeRange, clearFocus } from "./waterfall";
import { applyFilterFromInput, renderFilterChips, setFilter } from "./filter";
import { clearMarks, runSearch, stepActiveMark } from "./search";
import { graphTarget, openInGraph, shareLink } from "./share";
import { connectLive } from "./live";
import { startDemo } from "./demo";

// init() is invoked at the BOTTOM of this module (see the final line), after every shared state
// field has initialized. The order matters: loadFromURL()'s setFilter() applies the #q= deep link,
// which must survive - the shared state.filterParsed is seeded once in state.ts, so nothing later
// clobbers it back to empty.
function init(): void {
  wireControls();
  wireInput();
  loadFromURL();
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

  // Pretty <-> raw toggle. Raw shows the exact captured text (flat, no folds/badges);
  // pretty is the stylized structural view. Re-renders and clears any active search.
  const viewBtn = el("view-toggle");
  if (viewBtn) {
    viewBtn.addEventListener("click", () => {
      state.pretty = !state.pretty;
      setBtnLabel(viewBtn, state.pretty ? "Raw" : "Pretty");
      viewBtn.setAttribute("aria-pressed", state.pretty ? "false" : "true");
      const searchEl = el("log-search");
      if (searchEl) (searchEl as HTMLInputElement).value = "";
      clearMarks();
      const cnt = el("search-count");
      if (cnt) cnt.textContent = "";
      if (state.model) render();
      const fold = el("fold-all-btn");
      if (fold) fold.hidden = !state.model || state.model.titled === 0 || !state.pretty;
    });
  }

  // Timeline <-> log toggle. Switches the body between the trace waterfall and the log view;
  // clears any active search (the waterfall has no searchable lines) and re-syncs the sibling
  // controls (pretty/raw + fold) that do not apply while the waterfall is shown.
  const timelineBtn = el("timeline-btn");
  if (timelineBtn) {
    timelineBtn.addEventListener("click", () => {
      state.timeline = !state.timeline;
      const searchEl = el("log-search");
      if (searchEl) (searchEl as HTMLInputElement).value = "";
      clearMarks();
      const cnt = el("search-count");
      if (cnt) cnt.textContent = "";
      updateTimelineControl();
      if (state.model) render();
      const fold = el("fold-all-btn");
      if (fold) fold.hidden = state.timeline || !state.model || state.model.titled === 0 || !state.pretty;
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
    let t: ReturnType<typeof setTimeout>;
    searchEl.addEventListener("input", () => {
      clearTimeout(t);
      t = setTimeout(() => runSearch((searchEl as HTMLInputElement).value.trim()), 120);
    });
    searchEl.addEventListener("keydown", (ev) => {
      if ((ev as KeyboardEvent).key === "Enter") { ev.preventDefault(); stepActiveMark((ev as KeyboardEvent).shiftKey ? -1 : 1); }
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
  const focusResetBtn = el("focus-reset");
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
    panel.addEventListener("dragover", (ev) => { ev.preventDefault(); panel.classList.add("drag-over"); });
    panel.addEventListener("dragleave", () => panel.classList.remove("drag-over"));
    panel.addEventListener("drop", (ev) => {
      ev.preventDefault();
      panel.classList.remove("drag-over");
      const f = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
      if (f) f.text().then((text) => loadText(text, f.name));
    });
  }
}

// Boot last: every shared state field above is now initialized, so loadFromURL()'s setFilter()
// (for a #q= deep link) will not be clobbered by a later initializer.
if (bodyEl && scrollEl) {
  init();
}
