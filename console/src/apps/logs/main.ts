import { must } from "../../lib/guards";
// main.ts - the Log Viewer composition root. A purpose-built, read-only viewer for a magus
// run's captured output: the #data= fragment carries a magus.viewer.v1alpha1 Journal (protobuf,
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
import { JournalSchema } from "@wire/viewer/v1alpha1/viewer_pb";
import type { Journal } from "@wire/viewer/v1alpha1/viewer_pb";
import {
  parseHash,
  wantsDemo,
  getLiveToken,
  serverAttach,
  adoptServerOrigin,
  resolveServerHost,
} from "../../lib/server";
import {
  fetchRunJournal,
  fetchRunOutput,
  initRunBrowser,
  tickRelativeTimes,
  watchRuns,
  type RunBrowserHandle,
  type RunBrowserState,
  type Selection,
} from "./runtree";
import { cancelLiveRender } from "./live";
import { decodeFragmentBytes, setFragmentParam, viewerParams } from "./fragment";
import { state, waterfallSource } from "./state";
import {
  bindRefSlot,
  bodyEl,
  copyToClipboard,
  el,
  flipToggleGroup,
  panelEl,
  resolveDom,
  scrollEl,
  setBtnLabel,
  setRefIdentity,
  setStatus,
  setToggleGroup,
  showLog,
} from "./dom";
import { stripAnsi } from "../../render/ansi";
import { setSectionOpen } from "../../render/sections";
import { buildModel, buildModelMulti, cmdLabel } from "./model";
import { render, updateTimelineControl } from "./render";
import { applyTimeRange, clearFocus } from "./waterfall";
import {
  focusFilterBox,
  mountFilterField,
  renderFilterChips,
  setFilter,
  syncFilterBox,
} from "./filter";
import { clearMarks } from "./search";
import { graphAvailable, openInGraph, shareLink } from "./share";
import {
  renderConnectPrompt,
  renderEmptyMessage,
  type EmptyStateSlots,
} from "../../desktop/connectPrompt";
import { wireMenu } from "../../ui/menu";
import { reportFailure } from "../../lib/notifications";
import { errMessage } from "../../lib/guards";
import { connectLive, setLiveVisible } from "./live";
import { publishStatus } from "../../desktop/status";
import { demoJournal, startDemo, stopDemo } from "./demo";
import {
  installKeybindings,
  mergeKeymap,
  registerCommand,
  type Keymap,
} from "../../desktop/commands";
import { mountZoomControl, type ZoomControl } from "../../desktop/zoomControl";
import { persisted } from "../../lib/persist";
import { logsZoomCell } from "../../desktop/layoutPrefs";
import { attachHelpPopover, createHelpButton } from "../../ui/help-popover";
import { signal } from "../../desktop/view";

// Per-activation teardown. The console caches app modules and re-runs activate() on every
// reopen, so anything init() binds to the DOCUMENT has to be droppable: without these, each
// open/close cycle leaves another live generation behind. The keybinding matcher owns its own
// listener (it predates the signal convention) so it needs a separate handle; everything else
// takes lifecycleAbort's signal. Null while the app is not active.
let lifecycleAbort: AbortController | null = null;
let uninstallKeys: (() => void) | null = null;

// docTitle is the run this viewer currently has open, for the console to title its tab after
// (page.ts's TitleSource). A Signal satisfies that shape structurally, which is all the console
// needs - it only ever reads and subscribes. Module-level, like the rest of the viewer's state, so
// it survives the close/reopen cycle the console puts this cached module through.
export const docTitle = signal<string | null>(null);

// init() is invoked at the BOTTOM of this module (see the final line), after every shared state
// field has initialized. The order matters: loadFromURL()'s setFilter() applies the #q= deep link,
// which must survive - the shared state.filterParsed is seeded once in state.ts, so nothing later
// clobbers it back to empty.
function init(): void {
  // Adopt the serving origin FIRST, because everything below that resolves a server depends on it.
  // Each app is its own esbuild bundle, so lib/server's "did we adopt this origin" flag is
  // PER-BUNDLE state: the shell setting it does not make it true in here, and serverAttach then
  // returns null on a console served by that very server. The activity app hit this and fixed
  // it there; the viewer had the same hole, which is why its run browser read "No server connected"
  // on a page the server itself was serving.
  adoptServerOrigin();
  wireControls();
  wireCommands();
  wireZoom();
  wireInput();
  // The run browser is mounted before the first load so the load's name and reference id have
  // somewhere to land, and so the empty state below can read what the browser finds.
  wireRunBrowser();
  void loadFromURL();
}

// wireRunBrowser docks the run browser (runtree.ts) to the left of the viewer. It reads the server's
// run and output feeds (or, in the #demo showcase, a synthetic set) and, on selection, loads that run
// into this same viewer. Purely additive: with no reachable server and no demo the tree stays
// empty/hidden, so the #data/#src load and live-attach paths above are untouched.
// runBrowser is the mounted browser's handle, kept module-level because the viewer names the body
// header from whatever it has loaded and loadFromURL runs BEFORE the panel is mounted - a #inv= link
// therefore settles its title before there is anything to write it to. bodyTitleText remembers it so
// the mount can apply it.
let runBrowser: RunBrowserHandle | null = null;
let bodyTitleText = "";
// What the run browser last found, which the cold empty state repeats so the two never disagree.
let browserState: RunBrowserState | null = null;
let emptySlots: EmptyStateSlots | null = null;
// The run panel's auto-refresh stream, split into "how to start one" and "the running one's stop".
// Module-level like the rest of this app's state, because the console re-activates this cached
// module on every reopen and a stream left behind would outlive the panel it feeds.
let startBrowserWatch: (() => () => void) | null = null;
let stopBrowserWatch: (() => void) | null = null;

// setBodyTitle names the run on screen in the body header, whether it arrived by click or by link.
function setBodyTitle(text: string): void {
  bodyTitleText = text;
  runBrowser?.setBodyTitle(text);
}

function wireRunBrowser(): void {
  const scroll = el("log-scroll");
  if (!scroll) return;
  const demo = wantsDemo(parseHash());
  // resolveServerHost, NOT getDefaultHost: the browser auto-connects, and the server it should
  // reach is usually the one SERVING this page, which getDefaultHost cannot see (it only reads the
  // address typed into Settings). On the server-origin console that read the panel as "No server
  // connected" while the status bar beside it said "server ready" - so the browser was empty in
  // exactly the setup it exists for.
  const host = resolveServerHost(parseHash()) ?? "";
  const token = getLiveToken();
  runBrowser?.dispose();
  browserState = null;
  emptySlots = null;
  runBrowser = initRunBrowser({
    scroll,
    host,
    token,
    demo,
    nowMs: () => Date.now(),
    onSelect: (sel) => {
      void openSelection(sel, demo, host, token);
    },
    onState: (s) => {
      browserState = s;
      paintEmpty(host, demo);
    },
  });
  runBrowser.setBodyTitle(bodyTitleText);
  bindRefSlot(runBrowser.bodyMeta);
  paintEmpty(host, demo);
  // The panel keeps itself current while this app is on screen, so a run finished in a terminal
  // is already in the tree when the reader looks for it. setVisible tears the stream down for a
  // backgrounded tab, and deactivate() catches the close.
  // The stream and the clock are separate concerns: the labels age whether or not anything runs, so
  // the ticker also covers the demo and an offline page, which never open a stream at all.
  startBrowserWatch = () => {
    const unwatch = watchRuns(host, token, () => runBrowser?.refresh());
    const untick = tickRelativeTimes(scroll.parentElement ?? scroll);
    return () => {
      unwatch();
      untick();
    };
  };
  stopBrowserWatch?.();
  stopBrowserWatch = startBrowserWatch();
}

// paintEmpty writes the cold empty state from what the run browser beside it knows. It repeats the
// connection states the panel reports (through the shared prompt) and otherwise says in one sentence
// what to do next, so the two halves of the screen never tell different stories about the server.
function paintEmpty(host: string, demo: boolean): void {
  if (!emptySlots) {
    const title = el("log-empty-title");
    const message = el("log-empty-message");
    const actions = el("log-empty-actions");
    if (!title || !message || !actions) return;
    emptySlots = { title, message, actions };
  }
  const slots = emptySlots;
  if (!host && !demo) {
    renderConnectPrompt(
      slots,
      { connection: "none" },
      { purpose: "Pick a run to read its output, or open a link from magus query output --open." },
    );
    return;
  }
  if (!browserState?.loaded) {
    renderEmptyMessage(slots, "Loading runs", "");
    return;
  }
  if (browserState.unreachable) {
    renderConnectPrompt(
      slots,
      { connection: "disconnected", host },
      { onRetry: () => runBrowser?.refresh() },
    );
    return;
  }
  if (browserState.runs === 0) {
    renderEmptyMessage(slots, "No runs kept yet", "Run a target, then refresh Recent runs.");
    return;
  }
  renderEmptyMessage(slots, "Choose a run", "Pick one from Recent runs to read its output.");
  const show = document.createElement("button");
  show.type = "button";
  show.className = "pf-v6-c-button pf-m-primary";
  show.textContent = "Show recent runs";
  show.addEventListener("click", () => runBrowser?.open());
  slots.actions.append(show);
}

// openSelection loads a browsed row into the viewer. An invocation opens its whole journal; a
// target opens the SAME journal narrowed to that target, because one step's events only mean
// something beside the run that scheduled them. Either way the structured path is tried first and
// the verbatim blob is the fallback - see openRunOutput.
async function openSelection(
  sel: Selection,
  demo: boolean,
  host: string,
  token: string | null,
): Promise<void> {
  // A browsed run takes over the viewer, so end any in-progress #demo stream first - otherwise its
  // interval keeps re-rendering the showcase over the run just loaded.
  stopDemo();
  if (sel.kind === "invocation") {
    await openInvocation(sel.inv, sel.label, demo, host, token);
    return;
  }
  await openRunOutput(sel.run.ref, sel.run.inv, sel.focus, demo, host, token);
}

// openInvocation renders one past run from its journal - the same structured path a `#data=` link
// takes, so a browsed run gets per-target sections, exact statuses and the waterfall rather than the
// heuristic parse a text blob gets. focus, when set, pre-narrows the filter to one target.
async function openInvocation(
  inv: string,
  label: string,
  demo: boolean,
  host: string,
  token: string | null,
  focus?: string,
): Promise<void> {
  setStatus("Loading " + label + "...");
  if (demo) {
    const journal = demoJournal(inv);
    if (!journal) {
      setStatus("The demo has no journal for " + label + ".", true);
      return;
    }
    // Defer one frame so any render the #demo stream already scheduled (scheduleLiveRender, rAF)
    // flushes FIRST - otherwise it would repaint the showcase over the run just loaded.
    requestAnimationFrame(() => showJournal(journal, inv, focus));
    return;
  }
  if (!host) {
    setStatus("No server is connected. Set a server address in Settings.", true);
    return;
  }
  const bytes = await fetchRunJournal(host, token, { inv });
  if (!bytes) {
    setStatus("Could not load " + label + ". Its journal may have aged out.", true);
    return;
  }
  if (!renderJournalBytes(bytes, inv, focus)) {
    setStatus("Could not decode " + label + ".", true);
  }
}

// renderJournalBytes decodes a Journal protobuf and hands it to the same loader the #data path
// uses. Returns false on an undecodable payload so the caller can report it rather than leaving a
// half-loaded view. focus applies the target filter AFTER the load, so the chips render against
// the sections the journal actually produced.
function renderJournalBytes(bytes: Uint8Array, ref: string, focus?: string): boolean {
  let journal: Journal | null = null;
  try {
    const j = fromBinary(JournalSchema, bytes);
    if (j && j.events && j.events.length) journal = j;
  } catch (_) {
    journal = null;
  }
  if (!journal) return false;
  showJournal(journal, ref, focus);
  return true;
}

// showJournal loads a decoded journal and settles what a browsed run needs beyond the #data path:
// the body header's name, the filter (pre-narrowed when one target was chosen), and the page's
// address, so a reload reopens the run the reader was looking at rather than the empty state.
function showJournal(journal: Journal, ref: string, focus?: string): void {
  loadJournal(journal, ref);
  // The command is taken from the JOURNAL rather than from the row that was clicked, so a run
  // opened from a link is named the same as one opened from the tree.
  const name = cmdLabel(journal.invocation?.command) + (focus ? " - " + focus : "");
  setBodyTitle(name);
  docTitle.set(name);
  const isInv = ref.startsWith("inv");
  setFragmentParam(isInv ? "inv" : "ref", ref);
  setFragmentParam(isInv ? "ref" : "inv", "");
  const q = focus ? "target:" + focus : "";
  setFilter(q);
  renderFilterChips();
  syncFilterBox(q);
  if (focus) render();
}

// openRunOutput loads one browsed target. It asks the server for the RUN that produced the ref
// first, because the journal carries structure the stored blob has thrown away (exec boundaries,
// per-target results, timing the waterfall plots); journals rotate on a coarser cap than outputs,
// so a ref whose run has aged out still opens as verbatim text.
async function openRunOutput(
  ref: string,
  inv: string | undefined,
  focus: string,
  demo: boolean,
  host: string,
  token: string | null,
): Promise<void> {
  if (demo) {
    await openInvocation(inv ?? "", ref, demo, host, token, focus);
    return;
  }
  setStatus("Loading " + ref + "...");
  if (host) {
    const bytes = await fetchRunJournal(host, token, { ref });
    if (bytes && renderJournalBytes(bytes, ref, focus)) return;
  }
  // No journal: a stored blob is verbatim text with no per-step timing, so the waterfall the
  // previous selection may have left showing would plot nothing.
  state.timeline = false;
  const text = await fetchRunOutput(host, token, ref);
  if (text == null) {
    setStatus("Could not load " + ref + ". It may have aged out.", true);
    return;
  }
  loadText(text, ref);
  // No journal, so no command to name it by - the ref is all this blob knows about itself.
  setBodyTitle(ref);
  setFragmentParam("ref", ref);
  setFragmentParam("inv", "");
}

// --- Zoom -------------------------------------------------------------------
// A content zoom for the viewer body: in the text view it enlarges the log text (which re-wraps),
// in the waterfall it magnifies the timeline with scroll. Implemented with CSS `zoom` on the body
// so both views scale uniformly and the scroll container grows to match. Driven by the -/+ control
// in the status bar and the =/-/0 keys; the level persists so it sticks across loads.
const ZOOM_MIN = 0.7;
const ZOOM_MAX = 2.2;
const ZOOM_STEP = 0.1;
// The mounted stepper, so applyZoom can repaint its readout after a command or a restore.
let zoomCtl: ZoomControl | null = null;
// One definition, so the mount at activate() and the remount on setVisible(true) cannot drift.
const zoomOpts = () => ({
  get: () => zoomCell.get(),
  zoomIn: () => setZoom(zoomCell.get() + ZOOM_STEP),
  zoomOut: () => setZoom(zoomCell.get() - ZOOM_STEP),
  reset: () => setZoom(1),
});
const zoomCell = logsZoomCell;

function clampZoom(z: number): number {
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(z * 10) / 10));
}

function applyZoom(): void {
  const z = clampZoom(zoomCell.get());
  // One knob, two levers: the text view zooms the body (frame.css); the waterfall zooms its SVG
  // (logs.css) so it grows past the panel and the scroll box picks it up. A width:100% SVG would
  // just re-fit under a plain body zoom, so the waterfall needs its own.
  bodyEl.style.setProperty("--log-zoom", String(z));
  zoomCtl?.sync();
}

function setZoom(z: number): void {
  zoomCell.set(clampZoom(z));
  applyZoom();
}

function wireZoom(): void {
  // The shared stepper (console/zoomControl.ts), not a local one: the Plan app docks the same
  // control in the same place, and two near-identical copies is how they stop being identical.
  zoomCtl = mountZoomControl(zoomOpts());
  registerCommand({
    id: "logs.zoomIn",
    label: "Zoom in",
    group: "Log Viewer",
    run: () => setZoom(zoomCell.get() + ZOOM_STEP),
  });
  registerCommand({
    id: "logs.zoomOut",
    label: "Zoom out",
    group: "Log Viewer",
    run: () => setZoom(zoomCell.get() - ZOOM_STEP),
  });
  registerCommand({
    id: "logs.zoomReset",
    label: "Reset zoom",
    group: "Log Viewer",
    run: () => setZoom(1),
  });
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
  "logs.filter": "/", // focus the filter box
  "logs.raw": "r", // toggle raw / pretty
  "logs.timeline": "t", // toggle timeline / log
  "logs.fold": "f", // collapse / expand all
  "logs.zoomIn": "=", // enlarge the view (text bigger / waterfall magnified)
  "logs.zoomOut": "-", // shrink the view
  "logs.zoomReset": "0", // back to 100%
};
const keymapCell = persisted<Keymap>("keymap", {});

// clickControl triggers a toolbar button's own handler; a disabled control is a no-op, exactly as
// clicking it in the UI would be (so a keybinding never does what the button cannot).
function clickControl(id: string): void {
  const btn = el(id) as HTMLButtonElement | null;
  if (btn && !btn.disabled) btn.click();
}

function wireCommands(): void {
  registerCommand({
    id: "logs.filter",
    label: "Focus filter",
    group: "Log Viewer",
    run: focusFilterBox,
  });
  registerCommand({
    id: "logs.raw",
    label: "Toggle raw / pretty",
    group: "Log Viewer",
    run: () => flipToggleGroup("view-mode"),
  });
  registerCommand({
    id: "logs.timeline",
    label: "Toggle timeline / log",
    group: "Log Viewer",
    run: () => flipToggleGroup("timeline-mode"),
  });
  registerCommand({
    id: "logs.fold",
    label: "Collapse / expand all",
    group: "Log Viewer",
    run: () => clickControl("fold-all-btn"),
  });
  uninstallKeys?.();
  uninstallKeys = installKeybindings(() => mergeKeymap(LOGS_KEYMAP, keymapCell.get()));
}

async function loadFromURL(): Promise<void> {
  const params = viewerParams();
  const ref = params.ref || "";
  // Apply a shared #q= filter (the graph explorer's convention, read via the shared parseHash)
  // BEFORE any mode renders, and seed the filter box. It combines with #ref/#data/#port/#demo,
  // so a deep link like `#demo&q=status:fail` lands already narrowed.
  const q = parseHash().q || "";
  setFilter(q);
  renderFilterChips();
  syncFilterBox(q);
  // The shared BARE `#demo` fragment (wantsDemo, from lib/server - the same trigger the
  // dashboard and graph explorer use) enters the server-free showcase: a synthetic run
  // streams in with a live-filling waterfall.
  //
  // Bare is the operative word. `#demo&inv=` names one run of the demo scenario, which is what the
  // Runs app mints for "Open the whole run" while the console is in demo mode, so it has to
  // reach the inv branch below rather than being swallowed here. openInvocation has always known how
  // to read a synthetic journal; returning first is what made that path unreachable from a URL.
  const demo = wantsDemo(parseHash());
  if (demo && !params.inv && !params.ref) {
    startDemo();
    return;
  }
  if (params.data) {
    setStatus("Decoding the log...");
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
      setStatus("Could not decode the log: " + errMessage(e) + ".", true);
    }
    return;
  }
  if (params.src) {
    setStatus("Fetching the log...");
    try {
      const u = new URL(params.src, location.href);
      if (u.protocol !== "https:" && u.protocol !== "http:") {
        throw new Error("only http and https addresses are read");
      }
      const r = await fetch(params.src, { headers: { Accept: "text/plain" } });
      // An arbitrary address, not the magus server, so a 401 here must not sign the console out;
      // setStatus raises the failure toast itself.
      if (!r.ok) throw new Error("the address answered HTTP " + r.status);
      loadText(await r.text(), ref);
    } catch (e) {
      setStatus("Could not fetch the log: " + errMessage(e) + ".", true);
    }
    return;
  }
  // #inv= and a bare #ref= address a stored run in the LOCAL cache - what the run browser writes
  // when a row is selected, so reopening the page lands back on the run the reader was reading.
  // Both need the server, which is why they sit after the two offline paths: a `magus query output
  // --open` link carries #ref= alongside its own #data= payload and returns above, so a ref reaching
  // here is one this page wrote and the server can still resolve.
  if (params.inv || params.ref) {
    const host = resolveServerHost(params) ?? "";
    const token = getLiveToken();
    // demo carries through: under #demo the ref names a run of the synthetic scenario, and reaching
    // for a server for it would fail on a machine that has none - which is the whole point of demo.
    if (params.inv) await openInvocation(params.inv, params.inv, demo, host, token);
    else await openRunOutput(params.ref, undefined, "", demo, host, token);
    return;
  }
  // No static content requested: connect live if an explicit `#port=` link resolves. A static
  // #data/#src above always wins, so a live attach never clobbers a pasted or fetched log.
  //
  // A #port LINK specifically, not any resolved server: connectLive streams from `/events`, which is
  // the EPHEMERAL per-run server `magus run --open` spins up, and the server does not serve that
  // route at all (its own SSE is /api/v1/events, a graph-change feed). Attaching to a server origin
  // here therefore 404s and parks the app on "disconnected" over an empty body - strictly worse
  // than the empty state, which at least says how to load something.
  const port = parseHash().port;
  const attach = port === undefined ? null : serverAttach(parseHash());
  if (attach) {
    connectLive(attach, params);
    return;
  }
  // Nothing to show: leave the empty state visible.
}

function loadText(text: string, ref: string): void {
  state.rawLines = null;
  state.currentJournal = null;
  state.currentJournals = null; // a plain text log is a single invocation; drop any prior multi
  state.model = buildModel(text);
  state.rawText = text;
  finishLoad(ref, summarize(text));
}

// loadJournal renders a magus.viewer.v1alpha1 Journal (the structured #data path): it builds the
// SAME section model the heuristic produces - so render()/search/fold/copy work unchanged -
// but from EVENTS, so grouping and status are exact, not regex-guessed.
function loadJournal(journal: Journal, ref: string): void {
  state.currentJournal = journal;
  state.currentJournals = null; // a single loaded journal; drop any prior multi-invocation set
  const built = buildModelMulti(waterfallSource());
  state.model = { sections: built.sections, titled: built.titled };
  state.rawLines = built.rawLines;
  state.rawText = built.rawLines.join("\n");
  // A run is called by the command that made it; the ref is only the fallback for a journal that
  // never recorded one.
  const command = journal.invocation?.command;
  finishLoad(ref, built.summary, command ? cmdLabel(command) : undefined);
}

function finishLoad(ref: string, statusMsg: string, name?: string): void {
  state.currentRef = looksLikeRef(ref) ? ref : "";
  showLog();
  setRefIdentity(ref || "log", looksLikeRef(ref));
  // The loaded run is this app's open document, so the console names its tab after it. A reader
  // calls a run by its command ("magus affected ci"), not by an opaque id, so that leads; the ref or
  // file name is the fallback. Empty until something loads, which leaves the tab reading "Log Viewer".
  docTitle.set(name ?? (ref || null));
  if (name) setBodyTitle(name);
  // Resolve the Timeline button (and reset the mode if the new log has no timing) before
  // render() so a stale timeline=true from a previous log cannot try to plot a text log.
  updateTimelineControl();
  render();
  // Clear the strip rather than parking the summary in it: the strip is for a state the reader is
  // waiting on or has to act on, and a permanent info alert over every log that loaded fine is
  // neither. The summary is metadata, so it goes to the shared bar's count slot - the same slot the
  // live tail already uses for "N events".
  setStatus("");
  publishStatus({ count: statusMsg });
  const foldBtn = el("fold-all-btn");
  if (foldBtn)
    (foldBtn as HTMLButtonElement).disabled =
      state.timeline || must(state.model).titled === 0 || !state.pretty;
  const copyBtn = el("copy-all-btn");
  if (copyBtn) (copyBtn as HTMLButtonElement).disabled = false;
  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) (cmdBtn as HTMLButtonElement).disabled = !state.currentRef;
  const shareBtn = el("share-btn");
  if (shareBtn) (shareBtn as HTMLButtonElement).disabled = false;
  // Open Graph once the loaded journal names at least one target in a result event.
  const graphBtn = el("graph-btn");
  if (graphBtn) (graphBtn as HTMLButtonElement).disabled = !graphAvailable();
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
  // The filter box and the two "?" buttons are built from the shared parts. They are mounted
  // first because a #q= link seeds the box before any log loads.
  const filterItem = el("log-filter-item");
  if (filterItem) {
    const dispose = mountFilterField(filterItem);
    lifecycleAbort?.signal.addEventListener("abort", dispose);
  }
  const rangeHelpSlot = el("time-range-help-slot");
  if (rangeHelpSlot) {
    const help = createHelpButton("What Range does");
    const dispose = attachHelpPopover(help, {
      text:
        "Scope the waterfall to a slice of wall-clock time, counted back from the end of the run. " +
        "Choose Full run or one of the windows. Dragging across the waterfall sets a custom window " +
        "and shows Reset range beside this.",
      label: "What Range does",
    });
    rangeHelpSlot.replaceChildren(help);
    lifecycleAbort?.signal.addEventListener("abort", dispose);
  }

  // The More menu holds the actions used once and then forgotten; Pause and Copy stay on the row.
  const moreBtn = el("log-more-btn");
  const moreMenu = el("log-more-menu");
  if (moreBtn && moreMenu) {
    const dispose = wireMenu(moreMenu, moreBtn);
    lifecycleAbort?.signal.addEventListener("abort", dispose);
  }

  const copyBtn = el("copy-all-btn");
  if (copyBtn) {
    (copyBtn as HTMLButtonElement).disabled = true;
    copyBtn.addEventListener("click", () => copyToClipboard(rawTextPlain(), copyBtn));
  }

  // A menu item cannot show its own confirmation, because the menu has closed by the time the copy
  // lands, so these two confirm in a toast.
  const cmdBtn = el("copy-cmd-btn");
  if (cmdBtn) {
    cmdBtn.addEventListener("click", () => {
      if (state.currentRef) {
        copyToClipboard(
          "magus query " + state.currentRef,
          cmdBtn,
          "the command",
          "Copied a command that prints this output.",
        );
      }
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
    clearMarks();
  };
  const syncFold = (): void => {
    const fold = el("fold-all-btn");
    if (fold)
      fold.hidden = state.timeline || !state.model || state.model.titled === 0 || !state.pretty;
  };

  // Pretty <-> raw. Raw shows the exact captured text (flat, no folds/badges); pretty is the
  // stylized structural view.
  const viewGroup = el("view-mode");
  if (viewGroup) {
    viewGroup.addEventListener("click", (ev) => {
      const btn = (ev.target as HTMLElement).closest<HTMLButtonElement>(
        ".pf-v6-c-toggle-group__button",
      );
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
      const btn = (ev.target as HTMLElement).closest<HTMLButtonElement>(
        ".pf-v6-c-toggle-group__button",
      );
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
      for (const s of secs) setSectionOpen(s, !anyOpen);
      setBtnLabel(foldBtn, anyOpen ? "Expand sections" : "Collapse sections");
    });
  }

  // Time range: the wall-clock preset picker and the brushed-window reset.
  const timeSel = el("time-range");
  if (timeSel)
    timeSel.addEventListener("change", () => applyTimeRange((timeSel as HTMLSelectElement).value));
  const focusResetBtn = el("log-focus-reset");
  if (focusResetBtn) focusResetBtn.addEventListener("click", clearFocus);

  const pauseBtn = el("pause-btn");
  if (pauseBtn) {
    pauseBtn.addEventListener("click", () => {
      state.livePaused = !state.livePaused;
      setBtnLabel(pauseBtn, state.livePaused ? "Resume following" : "Pause following");
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
  if (!btn || !panel || !panel.requestFullscreen) {
    if (btn) (btn as HTMLButtonElement).disabled = true;
    return;
  }
  btn.addEventListener("click", () => {
    if (document.fullscreenElement) document.exitFullscreen();
    else panel.requestFullscreen();
  });
  document.addEventListener(
    "fullscreenchange",
    () => {
      setBtnLabel(btn, document.fullscreenElement === panel ? "Exit fullscreen" : "Fullscreen");
    },
    { signal: lifecycleAbort?.signal },
  );
}

// --- Input: drag-and-drop -----------------------------------------------------
// Dropping a saved log file onto the panel still loads it (an undocumented convenience);
// the paste box and file picker were removed - the viewer opens links, it isn't an editor.
function wireInput(): void {
  const panel = panelEl;
  if (panel) {
    panel.addEventListener("dragover", (ev) => {
      ev.preventDefault();
      panel.setAttribute("data-drag-over", "");
    });
    panel.addEventListener("dragleave", () => panel.removeAttribute("data-drag-over"));
    panel.addEventListener("drop", (ev) => {
      ev.preventDefault();
      panel.removeAttribute("data-drag-over");
      const f = ev.dataTransfer && ev.dataTransfer.files && ev.dataTransfer.files[0];
      if (f) {
        f.text().then(
          (text) => loadText(text, f.name),
          (e) => setStatus("Could not read " + f.name + ": " + errMessage(e) + ".", true),
        );
      }
    });
  }
}

// activate boots the viewer: resolve the DOM handles (the scaffold must already be present), then
// wire and load. Exported so the console's logs PageModule can drive it after injecting the scaffold
// into a host; the standalone page auto-boots below. init()'s ordering is preserved - every shared
// state field is initialized before it runs, so loadFromURL()'s #q= setFilter is not clobbered.
export function activate(): void {
  resolveDom();
  // A fresh controller per activation. The console caches this module (standalone.ts), so
  // activate() runs again on every reopen and anything bound to `document` in init() would
  // otherwise stack up one live copy per open - the keybinding matcher most visibly, where
  // two generations means every chord runs its command twice.
  lifecycleAbort?.abort();
  lifecycleAbort = new AbortController();
  if (bodyEl && scrollEl) init();
}

// setVisible is the console's contract (page.ts): this app writes the SHARED status bar - the
// connection pill, the event count and the zoom stepper - so it has to give all three back while its
// tab is hidden. Without it a background stream writes the active tab's bar.
export function setVisible(visible: boolean): void {
  setLiveVisible(visible);
  if (visible) {
    zoomCtl = mountZoomControl(zoomOpts());
    applyZoom();
    // Resume the run panel's stream and catch up on what happened while this pane was backgrounded.
    if (!stopBrowserWatch && startBrowserWatch) {
      stopBrowserWatch = startBrowserWatch();
      runBrowser?.refresh();
    }
  } else {
    zoomCtl?.remove();
    zoomCtl = null;
    stopBrowserWatch?.();
    stopBrowserWatch = null;
  }
}

// deactivate aborts a live stream if one is running, drops the keybinding matcher, and cuts every
// document-level listener init() registered against the lifecycle signal - so closing the logs tab
// or pane leaves no SSE connection open and nothing bound to the document. Static logs (the common
// case) never open a stream, so the abort is a no-op then. The console's logs PageModule calls this;
// the standalone page does not (the app lives as long as the page).
export function deactivate(): void {
  // Forget the loaded run: this module is a singleton the console re-activates on reopen, so a
  // stale ref left here would name the reopened tab after a log it is no longer showing.
  docTitle.set(null);
  bindRefSlot(null);
  browserState = null;
  emptySlots = null;
  runBrowser?.dispose();
  if (state.liveAbort) {
    state.liveAbort.abort();
    state.liveAbort = null;
  }
  cancelLiveRender();
  uninstallKeys?.();
  uninstallKeys = null;
  // The status bar outlives this module - it is a singleton the console re-activates on reopen - so
  // a stepper left behind would sit in the bar driving an app nobody is looking at.
  zoomCtl?.remove();
  zoomCtl = null;
  stopBrowserWatch?.();
  stopBrowserWatch = null;
  startBrowserWatch = null;
  runBrowser = null;
  lifecycleAbort?.abort();
  lifecycleAbort = null;
}

// Standalone auto-boot: only when the scaffold is already in the document at load. In the console the
// scaffold is injected into a host AFTER this module imports, so the console calls activate() itself.
if (document.getElementById("log-body")) activate();
