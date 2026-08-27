// main.ts - the console's Diff surface.
//
// A magus review is not a text diff. The daemon already knows which changed files are
// generated, how widely each changed symbol is referenced, whether any of it is public API,
// and what coverage was observed - so this surface spends the reader's attention in
// CONSEQUENCE order rather than alphabetical order, and folds away the files a target
// rewrote. On magus's own tree that routinely halves the number of files anyone has to read.
//
// Three things it is built around:
//
//  1. VIRTUALIZED. The scroll space is a spacer sized to the summed row heights and only the
//     rows intersecting the viewport exist as elements, so a ten-thousand-line diff costs what
//     a hundred-line one does. Declared row heights and no wrapping follow from that.
//  2. TWO-PHASE. The patch paints immediately; the annotations decorate it when they land.
//     Holding a readable diff behind the slower overlay would trade the thing the reader
//     wants for the thing they have not asked for yet.
//  3. PAIRED. Everything here reads and writes a session the daemon owns, which an agent
//     joins over MCP. The agent can see where the reader is and suggest; only the reader
//     navigates.
//
// KEYBOARD FIRST, MOUSE COMPLETE. Every action is a registered command (so it appears in the
// command bar and the Actions surface, and can be rebound) AND has a click target. The
// single-letter keys are handled on the scroll container rather than as global chords on
// purpose: a bare "v" must not fire while someone is typing in another surface.

import {
  fromWire,
  type DiffFile,
  type DiffLine,
  type FileStatus,
  type Hunk,
  type WireFile,
} from "./parse";
import {
  buildRows,
  byHunk,
  commentKey,
  narrowToHunk,
  hunkRowIndexes,
  hunksRead,
  activeFileTarget,
  fileRowIndexes,
  nextIndexAfter,
  prevIndexBefore,
  rowOffsets,
  rowAt,
  fileOfRow,
  anchorLine,
  maxLineChars,
  placeThreads,
  storyText,
  LINE_PREFIX_CHARS,
  type PlacedThreads,
  type Row,
  type ViewMode,
} from "./rows";
import { modeChange, order, visibleFiles, stats, riskChips, type OrderedChangeset } from "./order";
import { languageFor, tokenize, type Language } from "./syntax";
import {
  fetchPatch,
  fetchContext,
  fetchSession,
  fetchReview,
  fetchReviewSession,
  fetchBranches,
  runTarget,
  mutate,
  publish,
  reply,
  HttpError,
  type DiffComment,
  type DiffSession,
  type DiffAnnotation,
  type DiffTouch,
  type ReviewInfo,
  type ReviewThread,
  type ReviewVerdict,
} from "./session";
import { setMarkdown } from "./markdown";
import { mergedNotice } from "../../lib/review-notice";
import {
  demoSession,
  demoReview,
  demoRun,
  applyDemoPublish,
  applyDemoReply,
  applyDemoOp,
} from "./demo";
import { DEMO_FILES } from "./gen/demo";
import { registerCommand, unregisterCommand } from "../commands";
import { resolveDaemonHost, parseHash, adoptDaemonOrigin, wantsDemo } from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";
import type { SurfaceInstance } from "../standalone";

// Rows rendered beyond the viewport so a fast scroll never shows blank space. Bounded and
// constant, unlike the diff.
const OVERSCAN = 24;

// Virtualize file rows to keep large changesets responsive.
const SIDEBAR_FILE_HEIGHT = 40;
const SIDEBAR_PROJECT_HEIGHT = 28;
const SIDEBAR_OVERSCAN = 8;

type FileIndexEntry =
  | { kind: "project"; project: string; count: number }
  | { kind: "file"; project: string; changeIndex: number };

const daemonCell = persisted<string | null>("dashboard-daemon", null);
const modeCell = persisted<ViewMode>("diff-view-mode", "unified");
const sidebarCell = persisted<boolean>("diff-sidebar-collapsed", false);
// Remembered like the view mode and the sidebar beside it: whoever reads this way reads this way
// every time, and asking them to re-enter it per pass is the friction the mode exists to remove.
const focusCell = persisted<boolean>("diff-focus-mode", false);

type Phase = "loading" | "ready" | "empty";
type CollaborationState = "unavailable" | "live" | "degraded" | "stale";

interface State {
  changeset: OrderedChangeset;
  files: DiffFile[];
  rows: Row[];
  // Derived from rows, and rebuilt with them: row i's top edge, plus a final total. See rowOffsets.
  offsets: number[];
  hunks: number[];
  // For any rendered row, its hunk number inside the current file (or -1 before the first
  // hunk). This keeps collaboration cursor updates O(1) even in a multi-megabyte patch.
  hunkOrdinalByRow: Int32Array;
  fileRows: number[];
  // Row index -> the file row governing it, so a scroll position resolves to a file in one lookup.
  fileOf: number[];
  mode: ViewMode;
  cursor: number;
  session: DiffSession | null;
  // review is which pull request this branch has open and what has been said on it, or a
  // closed target carrying the reason. Null until the lookup lands - and it lands LAST, after
  // the patch and the annotations, because it is the only one that leaves the machine.
  review: ReviewInfo | null;
  // threads is review.threads resolved against the hunks actually on screen. Rebuilt with the
  // rows, since a fold or a mode switch changes which hunk a line sits in.
  threads: PlacedThreads | null;
  viewed: Set<string>;
  // digestByRow maps a hunk row index to the digest the daemon computed for that hunk. Filled
  // in full at rebuild - the browser hashes nothing, so every digest is known before the first
  // paint rather than resolving as rows scroll into view.
  digestByRow: Map<number, string>;
  showGenerated: boolean;
  // showSettled reveals the files a receipt already covers at their current content. Folded by
  // default, which is what makes a second pass cost only the second pass: a reviewer who asked
  // for changes comes back to a changeset that is mostly what they already read.
  showSettled: boolean;
  // focus narrows the stream to ONE hunk. The counts, the reading order and the threads are all
  // still computed over the whole changeset - what changes is how much of it is asked of the
  // reader at once.
  focus: boolean;
  // focusAt is the hunk focus mode is showing, held as a path and the hunk's own index rather
  // than a row number: rows are rebuilt on every fold, mode switch and annotation, and a row
  // number would point at different code afterwards.
  focusAt: { path: string; index: number } | null;
  // pairs is every (file, hunk) in the visible changeset, in reading order, and it is built
  // BEFORE the focus slice - which is what lets "hunk 4 of 14" and the progress bar keep
  // describing the whole pass while the stream shows one hunk.
  pairs: { path: string; index: number; digest: string }[];
  // branches maps a path to the other branches changing it, as of the reader's last fetch. Null
  // until the lookup lands, and null is not an empty map: one means "not asked yet or the backend
  // cannot say", the other would mean "asked, and nothing competes".
  branches: Map<string, BranchChange[]> | null;
  // branchesUnsupported names the backend that cannot answer, empty when one did. It is what
  // keeps an empty map from reading as "nothing competes" on a backend that never looked.
  branchesUnsupported: string;
  overview: boolean;
  phase: Phase;
  collaboration: CollaborationState;
  // verdicts maps a project to what its test target last decided, plus the changeset digest that
  // was on screen when the answer arrived.
  //
  // The digest is the honest half. magus keys its cache on a target's sources, so a verdict -
  // replayed or freshly run - is a true statement about the tree it was computed from, and the
  // only way it becomes a lie is the tree moving afterwards. Holding the digest lets the surface
  // say "passed, for code you have since edited" instead of a green tick over changed code, which
  // is a wrong answer delivered confidently.
  verdicts: Map<string, Verdict>;
}

// Verdict is one project's last run of its test target, as the surface knows it.
interface Verdict {
  readonly state: "running" | "passed" | "failed" | "unknown";
  readonly error?: string;
  readonly durationMs?: number;
  // asOf is the changeset digest that was on screen when this landed. A verdict whose asOf is not
  // the current digest describes code the reader has moved past.
  readonly asOf: string;
  // undeclared names the gap when the project declares no test target, so "we did not run it" is
  // never rendered as "it did not pass".
  readonly undeclared?: string;
}

const STATUS_COPY: Record<FileStatus, { short: string; modifier: string }> = {
  added: { short: "A", modifier: "pf-m-green" },
  deleted: { short: "D", modifier: "pf-m-red" },
  modified: { short: "M", modifier: "pf-m-grey" },
  renamed: { short: "R", modifier: "pf-m-blue" },
  copied: { short: "C", modifier: "pf-m-blue" },
};

// What each verdict is called in front of a person. The wire words are magus's vocabulary; these
// are what a reviewer would say they are doing.
const VERDICT_COPY: Record<ReviewVerdict, string> = {
  comment: "Remarks only",
  approve: "Approve",
  request_changes: "Request changes",
};

const TONE_CLASS: Record<string, string> = {
  neutral: "",
  info: "pf-m-blue",
  ok: "pf-m-green",
  warn: "pf-m-orange",
  danger: "pf-m-red",
};

// label builds a PF Label. Text goes through textContent by construction (h sets text, never
// innerHTML), which is what keeps a path, a diff line, or an agent's comment from being
// trusted markup - all three are attacker-influenceable on a branch someone else wrote.
function label(text: string, modifier?: string, title?: string): HTMLElement {
  const el = h("span", `pf-v6-c-label${modifier ? ` ${modifier}` : ""}`);
  el.append(h("span", "pf-v6-c-label__content", text));
  if (title) el.title = title;
  return el;
}

// The marker is the NON-COLOR channel for add and delete. Color alone fails WCAG 1.4.1 and
// fails anyone with a color vision deficiency, and a diff is exactly the case where the two
// states must be told apart to be read at all.
function markerFor(kind: string): string {
  return kind === "add" ? "+" : kind === "del" ? "-" : " ";
}

// A screen reader announcing "+" reads it as "plus", which is not what the row means. The
// visible glyph stays; this is what assistive tech is given instead.
function kindLabel(kind: string): string {
  return kind === "add" ? "added line" : kind === "del" ? "removed line" : "context line";
}

// prefersReducedMotion reports the OS-level request for less animation. Read live rather than
// cached: the setting can change mid-session, and matchMedia is cheap.
function prefersReducedMotion(): boolean {
  return globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
}

// lineText renders a line's text with syntax color and intra-line emphasis.
//
// The two compose rather than compete: a syntax token owns the FOREGROUND, the emphasis range
// owns the BACKGROUND. So the line is cut at the union of both boundaries and each piece
// carries whichever classes apply to it, which is why emphasis can highlight half a string
// literal without losing the fact that it is a string.
//
// Built from spans and textContent, never innerHTML: every character here comes from a diff on
// a branch someone else may have written.
function lineText(line: DiffLine, lang: Language): HTMLElement {
  const el = h("span", "console-diff-row__text");
  const text = line.text || " ";
  // The daemon computed this, exactly as it computed the hunk digests. The browser used to
  // work it out itself and Go worked out the same thing for the terminal viewer, with a
  // comment on the Go side asking whoever edited the TypeScript to re-transcribe its test
  // vectors by hand - so the same changed line could be highlighted two ways and nothing
  // anywhere would notice.
  const span = line.emph;
  const toks = tokenize(text, lang);

  if (toks.length === 0 && !span) {
    el.textContent = text;
    return el;
  }

  // Every boundary either signal introduces, so no piece straddles one.
  const cuts = new Set<number>([0, text.length]);
  for (const t of toks) {
    if (t.start <= text.length) cuts.add(t.start);
    if (t.end <= text.length) cuts.add(t.end);
  }
  if (span && span.end <= text.length) {
    cuts.add(span.start);
    cuts.add(span.end);
  }
  const bounds = [...cuts].sort((a, b) => a - b);

  for (let i = 0; i < bounds.length - 1; i++) {
    const from = bounds[i] ?? 0;
    const to = bounds[i + 1] ?? 0;
    if (to <= from) continue;
    const piece = text.slice(from, to);
    const tok = toks.find((t) => t.start <= from && t.end >= to);
    const emphasized = span !== undefined && span.start <= from && span.end >= to;
    if (!tok && !emphasized) {
      el.append(document.createTextNode(piece));
      continue;
    }
    const classes = ["console-diff-row__text-part"];
    if (tok) classes.push(`console-diff-tok--${tok.cls}`);
    if (emphasized) classes.push("console-diff-row__word");
    el.append(h("span", classes.join(" "), piece));
  }
  return el;
}

function gutter(n: number | null): HTMLElement {
  // A non-breaking space, not "": an empty text node collapses and the gutters would shift
  // width row to row.
  return h("span", "console-diff-row__gutter", n === null ? " " : String(n));
}

// The console's surface contract (page.ts): a teardown plus setVisible, so the shell can tell this
// pane when it stops being the visible one. Every surface hands back this shape - a bare teardown
// function is still accepted by the normalizer, but it leaves a surface with nowhere to put the
// hook, which is how the log viewer ended up writing a backgrounded tab's status bar.
export function activate(host: HTMLElement): SurfaceInstance {
  const controller = new AbortController();
  let disposed = false;
  // Dismissing the merged notice holds for this session only. It is not persisted: the surface
  // is reopened per branch, and a preference remembered across them would silence the offer on a
  // review the reader has not seen yet.
  let mergedSeen = false;

  // The daemon-free showcase, on the fragment every other surface reads. Derived ONCE, here,
  // rather than per fetch: a console served BY a daemon would otherwise answer host_() with a
  // real origin and the showcase would start writing a stranger's review session.
  const demo = wantsDemo(parseHash());

  // Both width-dependent defaults on this surface key off this: the file index, and the view mode.
  //
  // The measurement is the PANE, not the window. This surface tiles, so two panes on a 1440px
  // desktop give it ~700px each while a viewport query still reads "wide" - and it would then open
  // a 180px index floor and a two-column split inside a pane with no room for either, which is the
  // exact geometry the defaults exist to avoid. The window is only the bootstrap guess, used for
  // the state literal below because no DOM exists yet to measure; the observer at the foot of
  // activate() corrects it as soon as the surface has a box, and on every retile after that.
  const NARROW_PX = 768; // the shell's 48rem inversion, in px
  let paneNarrow = window.innerWidth < NARROW_PX;

  const state: State = {
    changeset: { primary: [], generated: [] },
    files: [],
    rows: [],
    offsets: [0],
    hunks: [],
    hunkOrdinalByRow: new Int32Array(),
    fileRows: [],
    fileOf: [],
    // Unified on a phone whatever the reader last picked on a desktop. Split halves an already
    // narrow pane into two columns that each spend ~7ch on gutters and markers before any code.
    // The preference is NOT overwritten, so the next load on a wide viewport is split again -
    // this is read ONCE at mount and nothing listens for a resize, so widening the CURRENT window
    // does not bring split back until the surface is remounted.
    mode: paneNarrow ? "unified" : modeCell.get(),
    cursor: -1,
    session: null,
    review: null,
    threads: null,
    viewed: new Set(),
    digestByRow: new Map(),
    // Open. The activity view folds its sections shut because a run's output is long and
    // mostly uninteresting; a changeset's file list is the thing the reader came for, so the
    // sidebar starts showing everything and folds on request rather than the other way round.
    showGenerated: true,
    showSettled: false,
    focus: focusCell.get(),
    focusAt: null,
    pairs: [],
    branches: null,
    branchesUnsupported: "",
    overview: false,
    phase: "loading",
    collaboration: demo ? "live" : "unavailable",
    verdicts: new Map(),
  };

  // --- scaffold -------------------------------------------------------------
  const root = h("div", "console-diff-layout");
  // Stamped here as well as in setFocus: the preference is remembered, so a reader who left in
  // focus mode arrives back in it, and the attribute is what the layout keys off.
  root.dataset.focus = state.focus ? "on" : "off";
  const sidebar = h("nav", "console-diff-sidebar");
  sidebar.setAttribute("aria-label", "Changed files");

  // The file index collapses to a rail, the way the activity trail's event index does. The diff
  // authors its own sheet (see standalone.ts) so it cannot reuse logs.css's panel, but the
  // affordance is the same one: a chevron in the header hides it, a chevron on the rail brings it
  // back, and the choice persists - a reader who works in a narrow tile should not re-close it
  // every visit.
  const sidebarHead = h("div", "console-diff-sidebar__head");
  const sidebarTitle = h("span", "console-diff-sidebar__heading", "Files");
  const hideBtn = h("button", "console-diff-sidebar__toggle");
  hideBtn.type = "button";
  hideBtn.title = "Hide the file index";
  hideBtn.setAttribute("aria-label", "Hide the file index");
  hideBtn.textContent = "‹";
  sidebarHead.append(sidebarTitle, hideBtn);

  // Keep filtering in the index at every diff size.
  const sidebarFilterWrap = h("span", "pf-v6-c-form-control console-diff-sidebar__filter");
  const sidebarFilter = h("input", "pf-v6-c-form-control__text") as HTMLInputElement;
  sidebarFilter.type = "search";
  sidebarFilter.placeholder = "Filter files";
  sidebarFilter.setAttribute("aria-label", "Filter changed files");
  sidebarFilter.autocomplete = "off";
  sidebarFilterWrap.append(sidebarFilter);
  const sidebarIndex = h("div", "console-diff-sidebar__index");
  sidebarIndex.setAttribute("role", "list");
  sidebarIndex.setAttribute("aria-label", "Changed files index");
  const sidebarSpacer = h("div", "console-diff-sidebar__spacer");
  const sidebarWindow = h("div", "console-diff-sidebar__window");
  sidebarSpacer.append(sidebarWindow);
  sidebarIndex.append(sidebarSpacer);
  const sidebarGenerated = h("div", "console-diff-sidebar__generated");
  sidebar.append(sidebarHead, sidebarFilterWrap, sidebarIndex, sidebarGenerated);

  const reopenBtn = h("button", "console-diff-reopen");
  reopenBtn.type = "button";
  reopenBtn.title = "Show the file index";
  reopenBtn.setAttribute("aria-label", "Show the file index");
  reopenBtn.textContent = "›";

  const applySidebar = (collapsed: boolean): void => {
    root.dataset.sidebar = collapsed ? "collapsed" : "open";
    sidebar.hidden = collapsed;
    reopenBtn.hidden = !collapsed;
    hideBtn.setAttribute("aria-expanded", collapsed ? "false" : "true");
  };
  // Below the breakpoint the toggle applies for the session but is NOT written down. The collapse
  // there is forced by width rather than chosen, so persisting it would let one phone visit rewrite
  // a preference the reader set on a desktop - in both directions.
  const rememberSidebar = (collapsed: boolean): void => {
    if (!paneNarrow) sidebarCell.set(collapsed);
  };
  hideBtn.addEventListener("click", () => {
    rememberSidebar(true);
    applySidebar(true);
  });
  reopenBtn.addEventListener("click", () => {
    rememberSidebar(false);
    applySidebar(false);
  });
  sidebar.setAttribute("aria-label", "Changed files");

  const main = h("div", "console-diff-main");
  const toolbar = h("div", "console-diff-toolbar");
  const statsEl = h("div", "console-diff-toolbar__stats");
  const collaborationNotice = h("span", "console-diff-collaboration");
  collaborationNotice.setAttribute("role", "status");
  collaborationNotice.setAttribute("aria-live", "polite");
  collaborationNotice.addEventListener("animationend", () =>
    collaborationNotice.classList.remove("is-flash"),
  );
  // The keys, stated on screen, the way the terminal viewer states them in its footer - and
  // for the reason recorded there: a viewer whose bindings are only in the documentation is
  // a viewer nobody drives with anything but the arrow keys.
  //
  // The console's hold-"?" cheat sheet cannot carry these: it renders only commands that
  // resolve to a chord in the console-wide keymap, and these are surface-local single keys
  // that never enter it. Until then the only summary lived inside the Esc overview - behind
  // the one key a first-time reader is least likely to try.
  // Each key is its own element rather than a run of text. Written as a sentence, the pair that
  // steps by file reads as `}/{` beside `]/[`, and a reader who has met a template engine sees
  // mustache syntax and a rendering bug - the author of this surface did, off a screenshot.
  // Punctuation is only legible AS a key when it is drawn as one.
  const keysEl = h("div", "console-diff-toolbar__keys");
  for (const [keys, what] of [
    [["]", "["], "hunk"],
    [["}", "{"], "file"],
    [["v"], "read"],
    [["u"], "next unread"],
    [["."], "generated"],
    [["Esc"], "overview"],
  ] as [string[], string][]) {
    const group = h("span", "console-diff-toolbar__key");
    for (const key of keys) group.append(h("kbd", undefined, key));
    group.append(h("span", "console-diff-toolbar__keyname", what));
    keysEl.append(group);
  }
  // The progress of the pass, shown only while reading one hunk at a time. A bar for the glance
  // and the numbers beside it, because a bar over hunks of unequal size advances unevenly and
  // would be read as lying about how much is left; the counts are the honest version it is
  // approximating.
  const progressEl = h("div", "console-diff-progress");
  progressEl.hidden = true;
  const progressBar = h("div", "console-diff-progress__bar");
  const progressFill = h("div", "console-diff-progress__fill");
  progressBar.setAttribute("role", "presentation");
  progressBar.append(progressFill);
  const progressText = h("span", "console-diff-progress__text");
  progressEl.append(progressBar, progressText);

  const focusButton = h("button", "pf-v6-c-button pf-m-link console-diff-toolbar__focus");
  focusButton.type = "button";
  focusButton.addEventListener("click", () => setFocus(!state.focus));

  // ONE run control, for the project of the file in view - not one per file heading. The question
  // a reader asks is "does the thing I am looking at still pass", and they ask it about one place
  // at a time; a button on every heading would answer the same question n times in a column and
  // turn the surface into a control panel.
  const verdictButton = h(
    "button",
    "pf-v6-c-button pf-m-link console-diff-toolbar__verdict",
  ) as HTMLButtonElement;
  verdictButton.type = "button";
  verdictButton.hidden = true;
  verdictButton.addEventListener("click", () => void startRun());

  toolbar.append(statsEl, collaborationNotice, progressEl, verdictButton, focusButton, keysEl);
  // Keep context outside the fixed-height virtual stream.
  const context = h("aside", "console-diff-context");
  context.hidden = true;
  context.tabIndex = -1;
  context.setAttribute("role", "region");
  context.setAttribute("aria-label", "Surrounding code");
  const contextHead = h("div", "console-diff-context__head");
  const contextTitle = h("span", "console-diff-context__title");
  const contextClose = h(
    "button",
    "pf-v6-c-button pf-m-plain console-diff-context__close",
  ) as HTMLButtonElement;
  contextClose.type = "button";
  contextClose.setAttribute("aria-label", "Close surrounding code");
  contextClose.textContent = "×";
  const contextBody = h("pre", "console-diff-context__body");
  contextBody.setAttribute("aria-live", "polite");
  contextHead.append(contextTitle, contextClose);
  context.append(contextHead, contextBody);

  const rail = h("div", "console-diff-rail");
  rail.setAttribute("aria-label", "Agent suggestions");
  // An interruption nobody is told about does not exist to a screen-reader user, and one
  // announced on every repaint is unusable. polite + atomic announces the suggestion once,
  // when it lands, without interrupting whatever is being read.
  rail.setAttribute("aria-live", "polite");
  rail.setAttribute("aria-atomic", "true");

  const scroll = h("div", "console-diff-scroll");
  scroll.tabIndex = 0;
  // A grid rather than a list: rows have cells (gutters, marker, text) and the surface is
  // two-dimensionally navigable. aria-rowcount is set on every paint from the true row total.
  scroll.setAttribute("role", "grid");
  scroll.setAttribute("aria-label", "Changed lines");
  const spacer = h("div", "console-diff-spacer");
  const windowEl = h("div", "console-diff-window");
  spacer.append(windowEl);
  scroll.append(spacer);

  // The pinned file header: ONE element, outside the virtualized window and outside the scroll
  // container, overlaying the top of the viewport. It is what a sticky row cannot be here - the
  // row for the file being read is evicted from the DOM as soon as it scrolls out, so there is
  // nothing left to pin. This is always present and re-rendered from whichever file the topmost
  // visible row belongs to.
  //
  // aria-hidden: it is a visual copy of a row assistive tech already reached in the grid, and
  // announcing the same file header a second time would be noise, not orientation.
  const viewport = h("div", "console-diff-viewport");
  const pinned = h("div", "console-diff-pinned");
  pinned.hidden = true;
  pinned.setAttribute("aria-hidden", "true");
  viewport.append(scroll, pinned);

  const overview = h("div", "console-diff-overview");
  const empty = h("div", "pf-v6-c-empty-state console-diff-empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "Loading");
  const emptyBodyWrap = h("div", "pf-v6-c-empty-state__body");
  const emptyBody = h("p", undefined, "Reading the working tree.");
  emptyBodyWrap.append(emptyBody);
  // No demo BUTTON here any more, on any page. Inside the console the title bar's Workspace menu is
  // the single way in; standalone the fragment still is (/console/diff/#demo), and showEmpty says so.
  // Six buttons for one thing was five too many, and each showed a different amount of the product.
  emptyContent.append(emptyTitle, emptyBodyWrap);
  empty.append(emptyContent);

  // The end of a review, offered once. A merged pull request is where a conversation stops being
  // live and starts being the only record of why the code is the way it is, and that record is
  // on somebody else's website. This does not write the note - notes are human-authored by
  // construction - it names the command that does.
  const merged = h("div", "console-diff-merged");
  merged.hidden = true;
  merged.setAttribute("role", "status");
  const mergedText = h("span", "console-diff-merged__text");
  const mergedDismiss = h("button", "console-diff-merged__dismiss", "dismiss");
  mergedDismiss.type = "button";
  mergedDismiss.addEventListener("click", () => {
    mergedSeen = true;
    merged.hidden = true;
  });
  merged.append(mergedText, mergedDismiss);

  main.append(toolbar, merged, context, rail, viewport, overview, empty);
  root.append(sidebar, reopenBtn, main);
  // Below the shell's 48rem inversion the index starts COLLAPSED, whatever is stored. Its column
  // floor is 180px, so on a 375px phone it took 48% of the screen and left the hunk stream 194px to
  // render 1163px of content. The stored preference is deliberately NOT consulted here: it is
  // overwhelmingly set on a desktop, and honouring an "open" from there is exactly how the 48%
  // sidebar comes back. Opening it on a phone still works and lasts the session, which is the same
  // per-session treatment the log viewer's run browser gives its own index (logs/runtree.ts).
  // JS-driven rather than a media query because the collapse is expressed with the hidden attribute.
  applySidebar(paneNarrow ? true : sidebarCell.get());
  host.append(root);
  root.dataset.phase = "loading";

  // --- rendering ------------------------------------------------------------

  const annotationFor = (path: string): DiffAnnotation | undefined => {
    for (const o of state.changeset.primary) if (o.file.path === path) return o.annotation;
    for (const o of state.changeset.generated) if (o.file.path === path) return o.annotation;
    return undefined;
  };

  const renderFileRow = (file: DiffFile): HTMLElement => {
    const el = h("div", "console-diff-row console-diff-row--file");
    const st = STATUS_COPY[file.status];
    el.append(label(st.short, st.modifier, file.status));

    const name = h("span", "console-diff-row__path");
    name.textContent =
      file.status === "renamed" || file.status === "copied"
        ? `${file.oldPath} -> ${file.path}`
        : file.path;
    el.append(name);

    if (file.binary) el.append(label("binary", "pf-m-grey", "No text diff to show"));
    // A mode change produces no hunks either, and without this the row is a filename and a
    // churn count with nothing to say why it is in the changeset - the reader is left to
    // assume the surface dropped something. A script gaining +x is a real reviewable event.
    const mode = modeChange(file);
    if (mode !== null) {
      el.append(
        label(
          mode,
          "pf-m-grey",
          file.newMode === "100755" ? "Now executable" : "File mode changed",
        ),
      );
    }
    if (file.additions > 0) el.append(label(`+${file.additions}`, "pf-m-green"));
    if (file.deletions > 0) el.append(label(`-${file.deletions}`, "pf-m-red"));

    // branchTooltip names the branches and says WHEN each answer was true.
  //
  // Two captions rather than one, because the freshness differs and a single line would overstate
  // half of them: nothing here fetches, so a remote-tracking answer is as old as the last fetch,
  // while a local branch is simply current. Before local branches were scanned at all, every
  // answer was the remote kind and one caption was honest.
  const branchTooltip = (alsoOn: readonly BranchChange[]): string => {
    const local = alsoOn.filter((b) => b.local).map((b) => b.ref);
    const remote = alsoOn.filter((b) => !b.local).map((b) => b.ref);
    const parts: string[] = [];
    if (local.length > 0) parts.push(`${local.join(", ")} - here now`);
    if (remote.length > 0) parts.push(`${remote.join(", ")} - as of your last fetch`);
    return parts.join("; ");
  };

  // Who else is editing this file. Reported, never predicted: two branches touching one file is
    // ordinary and usually fine, so this says what is true and leaves the conclusion alone -
    // "conflict likely" would be magus guessing at an outcome it cannot see.
    const alsoOn = state.branches?.get(file.path) ?? [];
    if (alsoOn.length > 0) {
      el.append(
        label(
          `also on ${alsoOn.length} ${alsoOn.length === 1 ? "branch" : "branches"}`,
          "pf-m-orange",
          branchTooltip(alsoOn),
        ),
      );
    }

    // The blast rail: what the workspace knows about this file. Evidence, not verdicts.
    //
    // Wrapped, and display: contents so the chips still lay out as row children: the wrapper
    // exists only to give the stylesheet a positional handle, so a narrow pane can drop the
    // trailing chips whole rather than slicing one down the middle. riskChips emits them
    // most-important first (public surface, then reach, then churn), so shedding from the END
    // gives up the least, and "public surface" is the last thing to go.
    const risks = h("span", "console-diff-row__risks");
    for (const c of riskChips(annotationFor(file.path))) {
      risks.append(label(c.text, TONE_CLASS[c.tone], c.title));
    }
    el.append(risks);
    return el;
  };

  const renderRow = (row: Row, index: number): HTMLElement => {
    if (row.kind === "file") return renderFileRow(row.file);
    if (row.kind === "hunk") {
      const el = h("div", "console-diff-row console-diff-row--hunk");
      const digest = state.digestByRow.get(index);
      if (digest && state.viewed.has(digest)) el.dataset.viewed = "";
      // The @@ coordinates are wire syntax, and this surface already prints line numbers in its
      // gutters, so what the heading says is what a reader wanted from it: the declaration they
      // are inside of. A hunk git could name none for keeps its position alone.
      el.append(
        h(
          "span",
          "console-diff-row__text",
          row.hunk.declaration || `line ${row.hunk.newStart}`,
        ),
      );
      if (!demo && !row.file.binary && row.file.status !== "deleted") {
        const peek = h(
          "button",
          "console-diff-row__peek",
          "Peek surrounding code",
        ) as HTMLButtonElement;
        peek.type = "button";
        peek.title = "Show the current file around this hunk without leaving the diff";
        peek.addEventListener("click", (event) => {
          event.stopPropagation();
          void showContext(row.file, row.hunk);
        });
        el.append(peek);
      }
      if (digest && state.viewed.has(digest))
        el.append(label("read", "pf-m-green", "Marked read. Press v to unmark."));
      return el;
    }
    if (row.kind === "story") {
      const el = h("div", "console-diff-row console-diff-row--story");
      const who = h("span", "console-diff-row__who");
      who.textContent = row.touch.host || "agent";
      const what = h("span", "console-diff-row__story");
      what.textContent = storyText(row.touch);
      el.append(who, what);
      if (row.touch.transcript) {
        const t = h("span", "console-diff-row__transcript", "transcript");
        // A POINTER: magus never opens it, and neither does this - the path is shown so the
        // reader can open their host's own log themselves.
        t.title = row.touch.transcript;
        el.append(t);
      }
      return el;
    }
    if (row.kind === "thread") {
      // A colleague's remark, already on the host's review. It renders in the comment row's
      // shape and NOT in its colors: the reader has to be able to tell at a glance what is
      // still theirs to send from what the world has already seen.
      const el = h("div", "console-diff-row console-diff-row--comment");
      el.dataset.author = "review";
      const who = h("span", "console-diff-row__who");
      who.textContent = row.thread.author || "review";
      const said = h("span", "console-diff-row__comment console-diff-md");
      setMarkdown(said, row.thread.body);
      el.append(who, said);
      // "new" first: it is the reason to read this row rather than skim past it, and the daemon
      // marks it only on the response that first carried the thread - so it answers "since last
      // time" rather than "recently", which decays into a badge that is always on.
      if (row.thread.new) el.append(label("new", "pf-m-orange"));
      el.append(label("on the review", "pf-m-blue"));
      return el;
    }
    if (row.kind === "comment") {
      const el = h("div", "console-diff-row console-diff-row--comment");
      el.dataset.author = row.comment.author;
      if (row.comment.resolved) el.dataset.resolved = "";
      const who = h("span", "console-diff-row__who");
      // The agent's own label when it gave one, else the role. Attribution is stamped by the
      // daemon from the transport, so this is reporting who wrote it rather than repeating a
      // claim the writer made about itself.
      who.textContent = row.comment.author === "agent" ? row.comment.agent_name || "agent" : "you";
      // Rendered the same way a colleague's remark is: a draft that reads differently here than
      // it will on the review is a draft you cannot proofread.
      const body = h("span", "console-diff-row__comment console-diff-md");
      setMarkdown(body, row.comment.body);
      el.append(who, body);
      if (row.comment.resolved) el.append(label("resolved", "pf-m-green"));
      return el;
    }
    if (row.kind === "line") {
      const el = h("div", "console-diff-row");
      el.dataset.kind = row.line.kind;
      const marker = h("span", "console-diff-row__marker", markerFor(row.line.kind));
      // The glyph is for eyes; the label is for ears. Announcing "plus" would be noise.
      marker.setAttribute("aria-hidden", "true");
      const text = lineText(row.line, languageFor(row.file.path));
      text.setAttribute("aria-label", `${kindLabel(row.line.kind)}: ${row.line.text}`);
      el.append(gutter(row.line.oldLine), gutter(row.line.newLine), marker, text);
      return el;
    }
    const el = h("div", "console-diff-row console-diff-row--pair");
    const lang = languageFor(row.file.path);
    el.append(side(row.left, "left", lang), side(row.right, "right", lang));
    return el;
  };

  const side = (line: DiffLine | null, which: "left" | "right", lang: Language): HTMLElement => {
    const cell = h("div", "console-diff-row__side");
    cell.dataset.side = which;
    if (!line) {
      cell.dataset.kind = "empty";
      cell.append(gutter(null), h("span", "console-diff-row__text", " "));
      return cell;
    }
    cell.dataset.kind = line.kind;
    const marker = h("span", "console-diff-row__marker", markerFor(line.kind));
    marker.setAttribute("aria-hidden", "true");
    const text = lineText(line, lang);
    text.setAttribute("aria-label", `${kindLabel(line.kind)}: ${line.text}`);
    cell.append(gutter(which === "left" ? line.oldLine : line.newLine), marker, text);
    return cell;
  };

  let paintedFirst = -1;
  let paintedLast = -1;
  const paint = (force = false): void => {
    const total = state.rows.length;
    if (total === 0) {
      windowEl.replaceChildren();
      paintedFirst = -1;
      paintedLast = -1;
      return;
    }
    const top = scroll.scrollTop;
    const height = scroll.clientHeight || 1;
    const first = Math.max(0, rowAt(state.offsets, top) - OVERSCAN);
    const last = Math.min(total, rowAt(state.offsets, top + height) + 1 + OVERSCAN);

    // Virtualization is invisible to assistive tech unless the grid says how big it really
    // is: rows outside the window are not in the DOM, so a screen reader would otherwise
    // report the OVERSCAN-sized slice as the whole diff. aria-rowcount carries the true
    // total, and each row carries its ABSOLUTE 1-based position below.
    scroll.setAttribute("aria-rowcount", String(total));

    if (force || first !== paintedFirst || last !== paintedLast) {
      const frag = document.createDocumentFragment();
      for (let i = first; i < last; i++) {
        const row = state.rows[i];
        if (!row) continue;
        const el = renderRow(row, i);
        el.dataset.row = String(i);
        // Use the absolute row index for grid semantics.
        el.setAttribute("role", "row");
        el.setAttribute("aria-rowindex", String(i + 1));
        // Expose each virtualized row as a real grid row.
        for (const cell of el.children) cell.setAttribute("role", "gridcell");
        if (i === state.cursor) el.dataset.cursor = "";
        frag.append(el);
      }
      windowEl.style.transform = `translateY(${state.offsets[first]}px)`;
      windowEl.replaceChildren(frag);
      paintedFirst = first;
      paintedLast = last;
    }
    paintPinned(top);
    markActiveFile(top);
  };

  // paintPinned keeps the file header of whatever is being read at the top of the viewport.
  //
  // Vertically, it shows only once the file's REAL header row has scrolled above the top edge,
  // so the two are never on screen together: while the real row is visible it is the header, and
  // the copy would just be a duplicate of the line below it.
  //
  // Horizontally, the real row's own position:sticky (diff.css) cannot do this job: it lives
  // inside .console-diff-window, which is transform-translated every scroll frame for the
  // virtualizer, and a transform on any ancestor breaks position:sticky in every browser - the
  // row silently scrolls away instead of staying put. This copy sits outside that transformed
  // subtree (a sibling of .console-diff-scroll), so it is the only thing that CAN track the
  // reader's horizontal position; show it whenever there is horizontal scroll to cover for, not
  // only once scrolled past vertically.
  const paintPinned = (top: number): void => {
    const i = state.fileOf[rowAt(state.offsets, top)] ?? -1;
    const row = i >= 0 ? state.rows[i] : undefined;
    if (!row || row.kind !== "file") {
      pinned.hidden = true;
      return;
    }
    const scrolledPast = (state.offsets[i] ?? 0) < top;
    if (!scrolledPast && scroll.scrollLeft === 0) {
      pinned.hidden = true;
      return;
    }
    pinned.replaceChildren(renderFileRow(row.file));
    pinned.hidden = false;
  };

  // Keep direct button references for active-file updates.
  const sidebarItems = new Map<number, HTMLButtonElement>();
  let activeSidebarFile: HTMLButtonElement | undefined;
  let activeSidebarRow = -1;
  // The sidebar lists PRIMARY files only, so a row inside a generated file resolves to no entry
  // in sidebarItems - markActiveFile used to just clear the last highlight and set nothing, which
  // blanked the one indicator the reader has for where they are the moment they scrolled somewhere
  // the index cannot show. The generated group's own toggle is the honest stand-in.
  let activeGeneratedToggle: HTMLButtonElement | undefined;
  let sidebarEntries: FileIndexEntry[] = [];
  let sidebarOffsets = [0];
  let sidebarPaintedFirst = -1;
  let sidebarPaintedLast = -1;

  const sidebarEntryHeight = (entry: FileIndexEntry): number =>
    entry.kind === "project" ? SIDEBAR_PROJECT_HEIGHT : SIDEBAR_FILE_HEIGHT;

  const renderSidebarItem = (
    o: (typeof state.changeset.primary)[number],
    index: number,
    project: string,
  ): HTMLButtonElement => {
    const item = h("button", "console-diff-sidebar__item") as HTMLButtonElement;
    item.type = "button";
    item.setAttribute("role", "listitem");
    const st = STATUS_COPY[o.file.status];
    item.append(label(st.short, st.modifier, o.file.status));
    // Give the filename priority over its directory.
    const slash = o.file.path.lastIndexOf("/");
    const wrap = h("span", "console-diff-sidebar__file");
    const name = h("span", "console-diff-sidebar__path");
    name.textContent = slash >= 0 ? o.file.path.slice(slash + 1) : o.file.path;
    wrap.append(name);
    // Omit an empty project-relative directory.
    const rel = o.file.path.startsWith(`${project}/`)
      ? o.file.path.slice(project.length + 1, slash < 0 ? undefined : slash)
      : slash > 0
        ? o.file.path.slice(0, slash)
        : "";
    if (rel) wrap.append(h("span", "console-diff-sidebar__dir", rel));
    item.append(wrap);
    // Keep the full path in the native tooltip.
    item.title = o.annotation?.hint ? `${o.file.path}\n\n${o.annotation.hint}` : o.file.path;
    if (o.annotation?.surface === "public") item.dataset.surface = "public";
    if (o.annotation?.reach) {
      const r = h("span", "console-diff-sidebar__counts");
      r.textContent = String(o.annotation.reach);
      r.title = `${o.annotation.reach} files reference the widest changed symbol here`;
      item.append(r);
    }
    // Grouping changes order, so retain the source row index.
    const row = state.fileRows[index];
    if (row !== undefined) {
      item.dataset.fileRow = String(row);
      sidebarItems.set(row, item);
    }
    item.addEventListener("click", () => {
      if (row !== undefined) scrollToRow(row);
      scroll.focus();
    });
    return item;
  };

  const paintSidebar = (force = false): void => {
    const total = sidebarEntries.length;
    if (total === 0) {
      sidebarWindow.replaceChildren();
      sidebarPaintedFirst = -1;
      sidebarPaintedLast = -1;
      sidebarItems.clear();
      activeSidebarFile = undefined;
      activeSidebarRow = -1;
      return;
    }
    const top = sidebarIndex.scrollTop;
    // Hidden panes report zero height; render a small initial slice.
    const height = sidebarIndex.clientHeight || 320;
    const first = Math.max(0, rowAt(sidebarOffsets, top) - SIDEBAR_OVERSCAN);
    const last = Math.min(total, rowAt(sidebarOffsets, top + height) + 1 + SIDEBAR_OVERSCAN);
    if (force || first !== sidebarPaintedFirst || last !== sidebarPaintedLast) {
      const frag = document.createDocumentFragment();
      sidebarItems.clear();
      activeSidebarFile = undefined;
      activeSidebarRow = -1;
      for (let i = first; i < last; i++) {
        const entry = sidebarEntries[i];
        if (!entry) continue;
        if (entry.kind === "project") {
          const head = h("div", "console-diff-sidebar__project");
          head.setAttribute("role", "presentation");
          const pName = h("span", "console-diff-sidebar__project-name", entry.project);
          pName.title = entry.project;
          const pCount = h("span", "console-diff-sidebar__project-count", String(entry.count));
          head.append(pName, pCount);
          frag.append(head);
          continue;
        }
        const change = state.changeset.primary[entry.changeIndex];
        if (change) frag.append(renderSidebarItem(change, entry.changeIndex, entry.project));
      }
      sidebarWindow.style.transform = `translateY(${sidebarOffsets[first]}px)`;
      sidebarWindow.replaceChildren(frag);
      sidebarPaintedFirst = first;
      sidebarPaintedLast = last;
    }
    markActiveFile(scroll.scrollTop);
  };

  let sidebarTicking = false;
  sidebarIndex.addEventListener(
    "scroll",
    () => {
      if (sidebarTicking) return;
      sidebarTicking = true;
      requestAnimationFrame(() => {
        sidebarTicking = false;
        if (!disposed) paintSidebar();
      });
    },
    { passive: true, signal: controller.signal },
  );
  // Repaint the virtual window after layout changes.
  const sidebarResize =
    typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => paintSidebar(true));
  sidebarResize?.observe(sidebarIndex);
  controller.signal.addEventListener("abort", () => sidebarResize?.disconnect(), { once: true });

  // markActiveFile keeps the sidebar in step with the stream, in BOTH directions: it is the
  // acknowledgement that a click landed (a smooth scroll over a long diff is slow enough to read
  // as nothing happening) and it is a position indicator while scrolling by hand.
  const markActiveFile = (top: number): void => {
    const fileRow = state.fileOf[rowAt(state.offsets, top)] ?? -1;
    if (fileRow === activeSidebarRow) return;
    activeSidebarFile?.removeAttribute("aria-current");
    activeGeneratedToggle?.removeAttribute("aria-current");
    const candidate = sidebarItems.get(fileRow);
    // activeFileTarget (rows.ts) makes the DECISION; this is just wiring it to the two elements
    // that can show it. "generated" is a real file row the index has no entry for - the whole
    // point of the fallback - so hasSidebarEntry is exactly "did the lookup above find one".
    switch (activeFileTarget(fileRow, candidate !== undefined)) {
      case "file":
        activeSidebarFile = candidate;
        activeGeneratedToggle = undefined;
        activeSidebarFile?.setAttribute("aria-current", "true");
        break;
      case "generated":
        activeSidebarFile = undefined;
        // Re-queried rather than cached: the toggle button is replaced on every renderSidebar.
        activeGeneratedToggle =
          sidebarGenerated.querySelector<HTMLButtonElement>(".console-diff-sidebar__group") ??
          undefined;
        activeGeneratedToggle?.setAttribute("aria-current", "true");
        break;
      case "none":
        activeSidebarFile = undefined;
        activeGeneratedToggle = undefined;
        break;
    }
    activeSidebarRow = fileRow;
  };

  let ticking = false;
  scroll.addEventListener(
    "scroll",
    () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(() => {
        ticking = false;
        if (!disposed) paint();
      });
    },
    { passive: true, signal: controller.signal },
  );

  // --- session sync ---------------------------------------------------------

  // host_ resolves which daemon to read.
  //
  // adoptDaemonOrigin() is called HERE, not left to the shell, and the reason is easy to miss:
  // each surface is its own bundle, so lib/daemon's module-level "did we adopt this origin"
  // flag is per-bundle state. The shell adopting it does not make it true in here. Without
  // this call the surface falls back to whatever host the dashboard happened to persist, so
  // Review would report "no daemon connected" on a console served BY that very daemon until
  // the reader had visited the dashboard first.
  const host_ = (): string | null => {
    adoptDaemonOrigin();
    return resolveDaemonHost(parseHash()) ?? daemonCell.get();
  };

  const canCollaborate = (): boolean => demo || state.collaboration === "live";
  // The mouse path to these actions disables its button and explains why in a title; a keyboard
  // shortcut has no button to disable, so this restarts the toolbar notice's flash animation to
  // point at the sentence that already gives the same reason, instead of a second copy of it.
  const flashCollaborationNotice = (): void => {
    collaborationNotice.classList.remove("is-flash");
    void collaborationNotice.offsetWidth;
    collaborationNotice.classList.add("is-flash");
  };
  // transientNotice is an answer to something the reader just pressed - why the send did
  // nothing, most of the time. It outranks the collaboration sentence while it stands and is
  // cleared by the next command, so it lasts exactly as long as the question it answers.
  let transientNotice = "";
  const flashPublishNotice = (text: string): void => {
    transientNotice = text;
    renderToolbar();
    flashCollaborationNotice();
  };
  const setCollaboration = (next: CollaborationState): void => {
    if (state.collaboration === next) return;
    state.collaboration = next;
    renderToolbar();
    renderRail();
  };

  let contextRequest: AbortController | null = null;
  let contextRequestID = 0;
  const closeContext = (): void => {
    contextRequest?.abort();
    contextRequest = null;
    context.hidden = true;
    scroll.focus();
  };
  contextClose.addEventListener("click", closeContext, { signal: controller.signal });
  controller.signal.addEventListener("abort", () => contextRequest?.abort(), { once: true });

  const showContext = async (
    file: DiffFile,
    hunk: { newStart: number; newCount: number },
    focus = false,
  ): Promise<void> => {
    // Demo fixtures cannot provide workspace context. The row button is withheld in demo for the
    // same reason (see renderRow), but the p key and command-bar entry have no button to withhold,
    // so they still need to say why nothing happened rather than doing nothing silently.
    if (demo) {
      context.hidden = false;
      contextTitle.textContent = file.path;
      contextBody.textContent = "Surrounding code is unavailable in this showcase.";
      if (focus) context.focus();
      return;
    }
    const asOf = state.session?.as_of;
    if (!asOf || state.collaboration !== "live") {
      context.hidden = false;
      contextTitle.textContent = file.path;
      contextBody.textContent =
        "Surrounding code is unavailable until this review is paired to a current snapshot.";
      if (focus) context.focus();
      return;
    }
    contextRequest?.abort();
    const request = new AbortController();
    contextRequest = request;
    const requestID = ++contextRequestID;
    context.hidden = false;
    contextTitle.textContent = file.path;
    contextBody.textContent = "Loading surrounding code...";
    if (focus) context.focus();
    const hp = host_();
    if (!hp) {
      contextBody.textContent = "Connect a daemon to read the current working-tree file.";
      return;
    }
    try {
      const end = hunk.newStart + Math.max(1, hunk.newCount) - 1;
      const result = await fetchContext(hp, file.path, asOf, hunk.newStart, end, request.signal);
      if (disposed || requestID !== contextRequestID) return;
      contextTitle.textContent = `${result.path}:${result.start}`;
      contextBody.textContent = result.lines
        .map((line, index) => `${String(result.start + index).padStart(5)}  ${line}`)
        .join("\n");
    } catch (error) {
      if (disposed || requestID !== contextRequestID || request.signal.aborted) return;
      contextBody.textContent =
        error instanceof HttpError && error.status === 409
          ? "The review snapshot changed. Refresh the diff before peeking at surrounding code."
          : "Could not load surrounding code: " + String(error);
    }
  };

  const sync = async (op: Parameters<typeof mutate>[1]): Promise<DiffSession | null> => {
    // In the showcase the store is in memory: the reader's marks, comments and answers have to
    // land somewhere or the affordances read as broken, and there is no daemon to land them in.
    if (demo) {
      if (!state.session) return null;
      const next = applyDemoOp(state.session, op);
      applySession(next);
      return next;
    }
    if (!canCollaborate()) return null;
    const hp = host_();
    if (!hp) {
      setCollaboration("unavailable");
      return null;
    }
    const s = await mutate(hp, op, controller.signal);
    if (disposed) return null;
    if (!s) {
      setCollaboration("degraded");
      return null;
    }
    applySession(s);
    return s;
  };

  // applySession takes the daemon's copy as authoritative and re-lays the stream, because a
  // comment - the human's or an agent's - is a ROW, so it changes the scroll geometry. Only
  // repainting would leave the new remark invisible until the next unrelated rebuild.
  const applySession = (s: DiffSession, relayout = true): boolean => {
    if (state.session?.as_of && s.as_of && state.session.as_of !== s.as_of) {
      setCollaboration("stale");
      return false;
    }
    const before = (state.session?.comments ?? []).length;
    state.session = s;
    state.viewed = new Set(s.viewed ?? []);
    setCollaboration("live");
    renderRail();
    if (relayout && (s.comments ?? []).length !== before) void rebuild();
    else renderToolbar();
    return true;
  };

  // Keep the patch stable while polling the coordination session.
  let surfaceVisible = true;
  let pollTimer: number | null = null;
  let polling = false;
  const stopPolling = (): void => {
    if (pollTimer !== null) window.clearTimeout(pollTimer);
    pollTimer = null;
  };
  const schedulePoll = (): void => {
    stopPolling();
    if (demo || disposed || !surfaceVisible || !state.session || state.collaboration === "stale")
      return;
    pollTimer = window.setTimeout(() => void pollSession(), 4_000);
  };
  const pollSession = async (): Promise<void> => {
    if (polling || demo || disposed || !surfaceVisible || !state.session) return;
    const hp = host_();
    if (!hp) {
      setCollaboration("unavailable");
      return;
    }
    polling = true;
    try {
      const next = await fetchReviewSession(hp, controller.signal);
      if (!disposed) applySession(next);
    } catch {
      if (!disposed) setCollaboration("degraded");
    } finally {
      polling = false;
      schedulePoll();
    }
  };
  const startPolling = (): void => schedulePoll();

  // --- model rebuild --------------------------------------------------------

  // The daemon computed every digest, so this is a lookup rather than a hash. It used to hash
  // in the browser, lazily and per visible hunk, which is why the read count lagged: a hunk
  // already marked in an earlier session stayed uncounted until it scrolled into view.
  const digestForHunk = (rowIndex: number): string | undefined => state.digestByRow.get(rowIndex);

  const hunkOrdinal = (rows: readonly Row[]): Int32Array => {
    const ordinals = new Int32Array(rows.length);
    let withinFile = -1;
    for (let i = 0; i < rows.length; i++) {
      const row = rows[i];
      if (!row) continue;
      if (row.kind === "file") withinFile = -1;
      else if (row.kind === "hunk") withinFile++;
      ordinals[i] = withinFile;
    }
    return ordinals;
  };

  // monoCharWidth measures the mono font's actual character advance, in pixels, so the row-width
  // floor below can be set in px instead of ch. ch is relative to EACH element's own font, and a
  // comment or story row renders in the body font (see .console-diff-row--comment), not mono - the
  // same ch count on those rows resolved to a smaller pixel width than on a code line, so their
  // background fell short of the scroll width all over again. The probe is measured off-DOM
  // (position: absolute, visibility: hidden) and over many characters, not one, because a single
  // glyph's rect can round to a whole pixel and drift the floor over a long line.
  const monoCharWidth = (): number => {
    const probe = document.createElement("span");
    probe.style.cssText =
      "position:absolute;visibility:hidden;white-space:pre;" +
      "font-family:var(--pf-t--global--font--family--mono);" +
      "font-size:var(--pf-t--global--font--size--sm);";
    probe.textContent = "0".repeat(64);
    scroll.appendChild(probe);
    const width = probe.getBoundingClientRect().width / 64;
    probe.remove();
    return width;
  };

  // focusSlice narrows the file list to the one hunk focus mode is showing.
  //
  // A slice, not a filter of the rows: buildRows takes files, so everything downstream - the
  // offsets, the hunk marks, the thread placement - recomputes for what is actually on screen
  // rather than being patched afterwards. It is safe to renumber nothing because a hunk carries
  // its own index (see Hunk.index); keying by array position here would move every remark.
  const focusSlice = (files: DiffFile[]): DiffFile[] => {
    const want = state.focusAt;
    for (const file of files) {
      if (want && file.path !== want.path) continue;
      for (const hunk of file.hunks) {
        if (want && hunk.index !== want.index) continue;
        state.focusAt = { path: file.path, index: hunk.index };
        return [{ ...file, hunks: [hunk] }];
      }
    }
    // The hunk went away - a fold, a re-read, a tree that moved. Fall back to the first one
    // rather than showing nothing: an empty stream would read as "the change is gone".
    const first = files[0];
    const firstHunk = first?.hunks[0];
    if (!first || !firstHunk) return files;
    state.focusAt = { path: first.path, index: firstHunk.index };
    return [{ ...first, hunks: [firstHunk] }];
  };

  const rebuild = async (): Promise<void> => {
    state.files = visibleFiles(state.changeset, state.showGenerated, state.showSettled);
    // Built from the WHOLE visible changeset, before any narrowing, because the counts describe
    // the pass and not the screen.
    state.pairs = state.files.flatMap((f) =>
      f.hunks.map((hunk) => ({ path: f.path, index: hunk.index, digest: hunk.digest })),
    );
    // Resume on the FIRST rebuild of a remembered pass, not just when the mode is toggled on.
    // setFocus seeds focusAt, but a reader who left in focus mode arrives with it null, and
    // focusSlice's fallback is the first hunk - so the remembered preference, which is the common
    // path, restarted the pass at the top every time and the docs promised otherwise.
    if (state.focus && state.pairs.length > 0) {
      state.focusAt ??= firstUnread();
      state.files = focusSlice(state.files);
    }
    // Touches come from the annotations, so the first paint has none and the stream gains the
    // story rows when the review lands - the same two-phase shape everything else here uses.
    const touches = new Map<string, readonly DiffTouch[]>();
    for (const f of state.session?.diff?.files ?? []) {
      if (f.touches?.length) touches.set(f.path, f.touches);
    }
    // Placed against the files ACTUALLY on screen, on every rebuild: folding the generated
    // group or switching to split changes which hunk a line sits in, and a placement computed
    // once would leave a colleague's remark pinned to whatever used to be there.
    state.threads = state.review ? placeThreads(state.files, state.review.threads) : null;
    // Focus mode renders ONE hunk, so a remark on any other hunk of the same file is bucketed
    // under a key nothing emits - rendered nowhere, counted nowhere, and absent from the
    // elsewhere listing that exists to guarantee no remark is ever silently dropped. Moving them
    // to elsewhere is what keeps that guarantee true when the stream narrows.
    if (state.focus && state.threads) {
      state.threads = narrowToHunk(
        state.threads,
        state.focusAt ? commentKey(state.focusAt.path, state.focusAt.index) : "",
      );
    }
    state.rows = buildRows(
      state.files,
      state.mode,
      byHunk(state.session?.comments ?? []),
      touches,
      state.threads ?? undefined,
    );
    state.hunks = hunkRowIndexes(state.rows);
    state.hunkOrdinalByRow = hunkOrdinal(state.rows);
    state.fileRows = fileRowIndexes(state.rows);
    state.offsets = rowOffsets(state.rows);
    state.fileOf = fileOfRow(state.rows);
    spacer.style.height = `${state.offsets[state.rows.length]}px`;
    const rowFloorPx = (maxLineChars(state.rows) + LINE_PREFIX_CHARS) * monoCharWidth();
    scroll.style.setProperty("--console-diff-min-row-width", `${rowFloorPx}px`);
    // Row indices move on every rebuild, so the map is rebuilt with them - but it is filled
    // completely here, from digests that arrived with the changeset.
    state.digestByRow = new Map();
    for (const row of state.hunks) {
      const r = state.rows[row];
      if (r?.kind === "hunk") state.digestByRow.set(row, r.hunk.digest);
    }
    paint(true);
    renderSidebar();
    renderToolbar();
  };

  // --- chrome ---------------------------------------------------------------

  // ranked reports whether the server had a ranking key at all. False means every file's reach
  // is unmeasured, so the order is path order wearing a ranking's clothes - and this surface
  // says "Read these first", which is a claim it cannot keep in that state. Mirrors
  // types.Diff.Ranked; the server's order is still authoritative, this only labels it.
  const ranked = (): boolean =>
    (state.session?.diff?.files ?? []).some((f) => f.reach !== null && f.reach !== undefined);

  const UNRANKED_TITLE =
    "No symbol index. Files use path order. Run magus graph build to order them by impact.";

  // reviewLabel names the review the way its host does.
  //
  // The "#" is a NUMBERING convention, so it goes on a number and nowhere else: "#482" is how
  // GitHub and GitLab write theirs, while a Gerrit change is "I8473b95" and "#I8473b95" would
  // be magus inventing a spelling its own provider does not use.
  const reviewLabel = (info: ReviewInfo): string =>
    /^\d+$/.test(info.id) ? `#${info.id}` : info.id;

  // destination is the whole address a write would go to, for the sentence shown before one
  // leaves: repo and review, so "send" never means "somewhere you would have to guess".
  const destination = (info: ReviewInfo): string =>
    info.repo ? `${info.repo} ${reviewLabel(info)}` : reviewLabel(info);

  // reviewChips says where this pass is going and what is waiting for it.
  //
  // Nothing at all until the lookup lands, and nothing when no review is open. A branch with no
  // pull request is the ordinary state of most branches most of the time, and a chip saying so
  // on every one of them would be a permanent complaint about nothing.
  const reviewChips = (): HTMLElement[] => {
    const info = state.review;
    if (!info?.id) return [];
    const chips = [
      label(reviewLabel(info), "pf-m-blue", info.repo ? `Open on ${info.repo}` : "The open review"),
    ];
    const pending = drafts().length;
    if (pending > 0) {
      chips.push(
        label(
          `${pending} ${pending === 1 ? "draft" : "drafts"}`,
          "pf-m-orange",
          "Written, not sent. Press s to read the batch and send it.",
        ),
      );
    }
    // A reason alongside an OPEN review is not "there is no review" - it is a review that was
    // read incompletely, most often a thread the provider returned in a shape magus could not
    // decode. Shown, because the alternative is a conversation quietly missing a remark.
    if (info.reason) {
      chips.push(label("partly read", "pf-m-red", info.reason));
    }
    // Threads on files this changeset does not touch have nowhere in the stream to sit. Counted
    // rather than dropped: "your colleague said nothing" is the one thing a review surface must
    // never say by accident.
    const elsewhere = state.threads?.elsewhere.length ?? 0;
    if (elsewhere > 0) {
      chips.push(
        label(
          `${elsewhere} elsewhere`,
          undefined,
          "Comments on the review, on files this view is not showing",
        ),
      );
    }
    return chips;
  };

  const renderToolbar = (): void => {
    const s = stats(state.changeset);
    const chips: HTMLElement[] = [];
    const collaboration = {
      live: { text: "agent session live", tone: "pf-m-blue", notice: "" },
      unavailable: {
        text: "agent session unavailable",
        tone: "pf-m-orange",
        notice:
          "Agent comments and review marks are unavailable until the review session connects.",
      },
      degraded: {
        text: "agent sync unavailable",
        tone: "pf-m-orange",
        notice:
          "Agent collaboration is temporarily unavailable. Review actions are disabled until it reconnects.",
      },
      stale: {
        text: "review snapshot changed",
        tone: "pf-m-orange",
        notice:
          "The shared review now describes a different patch. Refresh the diff before collaborating.",
      },
    }[state.collaboration];
    // Demo state is NOT chipped here. Every other surface says it in one place - the shell's
    // connection pill - and a second badge in this toolbar made the diff the one surface that
    // announced it twice, in a style nothing else uses.
    //
    // The tileable case (toolbar on screen, status bar hidden, numbers reading as your own
    // tree) is real and is the reason this existed. It is answered by the pill rather than by
    // a per-surface badge: fixing it here only would leave every other tileable surface with
    // the same gap and a different answer.
    chips.push(
      label(
        `${s.files} ${s.files === 1 ? "file" : "files"}`,
        undefined,
        "Files worth reading; generated output is excluded",
      ),
      label(`+${s.additions}`, "pf-m-green"),
      label(`-${s.deletions}`, "pf-m-red"),
    );
    // A LABEL, not a button. This row is a readout - counts and warnings - and the one control
    // hiding among them read as a different kind of thing because it was one. The fold lives on
    // the sidebar's "N generated" group, where the files it folds are, and on the . key.
    if (s.generated > 0) {
      chips.push(
        label(
          state.showGenerated ? `${s.generated} generated` : `${s.generated} generated folded`,
          undefined,
          "Declared target outputs. Review the source change instead. Fold these files in the sidebar or press period.",
        ),
      );
    }
    // Said out loud for the reason the generated count is: a folded file the reader was never
    // told about is the one failure this surface cannot have. It is the second pass's whole
    // value, so it reads as progress rather than as a warning.
    if (s.settled > 0) {
      chips.push(
        label(
          state.showSettled ? `${s.settled} already read` : `${s.settled} already read, folded`,
          undefined,
          "You read these at exactly this content and they have not changed since. Press n to show them.",
        ),
      );
    }
    if (!ranked()) {
      chips.push(label("unranked", "pf-m-orange", UNRANKED_TITLE));
    }
    if (s.publicSurface > 0) {
      chips.push(
        label(
          `${s.publicSurface} public surface`,
          "pf-m-orange",
          "Files whose changed symbols are reachable outside their project or module",
        ),
      );
    }
    if (s.untested > 0) {
      chips.push(
        label(
          `${s.untested} untested`,
          "pf-m-red",
          "Files with measured zero coverage (files nobody measured are not counted)",
        ),
      );
    }
    // state.viewed.size used to be the numerator here, and it is the WHOLE SESSION's marked set -
    // every hunk ever read, including ones no longer in state.hunks (folded into "generated",
    // filtered out, or from a build that has since rebuilt). Denominator is the CURRENT stream.
    // Fold the generated group after reading hunks inside it and the two stop describing the same
    // set: the chip read "12/8" and the green complete state fired on a coincidence, not on the
    // stream actually being read. hunksRead (rows.ts) counts the intersection instead - see it for
    // the undercount tradeoff that fix makes.
    // Files that moved after they were read. Shown only when there are some, and shown as a
    // COUNT of a thing that happened rather than as a fraction of a thing to finish: the
    // hunks-read chip below is this sitting's navigation state, which is a bookmark; this is a
    // finding, and it is the one the reader cannot get by scrolling because those files look
    // finished. Its absence is also the answer to "is there anything left to pick up".
    const stale = state.changeset.primary.filter(
      (o) => o.annotation?.read_state === "stale",
    ).length;
    if (stale > 0) {
      chips.push(
        label(
          `${stale} changed since read`,
          "pf-m-red",
          "You read these and they changed afterwards. Press u to go to the first one.",
        ),
      );
    }
    const read = hunksRead(state.hunks, state.digestByRow, state.viewed);
    chips.push(
      label(
        `${read}/${state.hunks.length} hunks read`,
        read === state.hunks.length && read > 0 ? "pf-m-green" : undefined,
        "Marks are keyed to hunk CONTENT, so they survive a rebase that did not touch the hunk",
      ),
    );
    chips.push(
      label(
        state.mode === "split" ? "split" : "unified",
        "pf-m-blue",
        "1 unified, 2 split, 0 toggle",
      ),
    );
    if (state.collaboration !== "live") chips.push(label(collaboration.text, collaboration.tone));
    // The review chips come last: they describe what happens to this pass when it is over,
    // which is the least urgent thing on a row about what is in front of the reader now.
    chips.push(...reviewChips());
    collaborationNotice.textContent = transientNotice || collaboration.notice;
    statsEl.replaceChildren(...chips);
    renderProgress();
    renderMerged();
  };

  const renderMerged = (): void => {
    const said = (state.review?.threads.length ?? 0) + (state.session?.comments?.length ?? 0);
    const text = mergedSeen ? "" : mergedNotice(state.review, said);
    merged.hidden = text === "";
    mergedText.textContent = text;
  };

  // The target an inline run asks for. Hard-coded to the canonical test target rather than
  // offered as a picker: the reader is asking one question - does this still pass - and a menu of
  // every declared target turns that into a decision they did not want to make. A project that
  // declares no `test` comes back undeclared, and the surface says so.
  const RUN_TARGET = "test";

  // currentProject is the project of the file the reader is on, which is what the one run control
  // is about. Focus mode has an exact answer; the dense view uses the file at the cursor, and
  // falls back to the first changed file so the control is never blank on arrival.
  // treeState identifies the code a verdict is about: the patch digest the daemon computed for
  // the working tree, the same one the context lookup pins its reads to. It moves the moment the
  // reader edits anything, which is exactly when a verdict stops describing what is on screen.
  const treeState = (): string => state.session?.as_of ?? "";

  const currentProject = (): string => {
    const path = state.focus ? state.focusAt?.path : (state.files[state.cursor]?.path ?? "");
    const primary =
      state.changeset.primary.find((o) => o.file.path === path) ?? state.changeset.primary[0];
    return primary?.annotation?.project ?? "";
  };

  // renderVerdict draws the one run control: what the project in view last decided, and whether
  // that answer still describes the code on screen.
  //
  // A verdict computed against a different changeset digest is shown as STALE rather than hidden
  // or silently reused. Hiding it loses the reader's own work; reusing it is the confident wrong
  // answer this surface exists not to give.
  const renderVerdict = (): void => {
    const project = currentProject();
    if (!project) {
      verdictButton.hidden = true;
      return;
    }
    verdictButton.hidden = false;
    const v = state.verdicts.get(project);
    const stale = v !== undefined && v.asOf !== treeState();
    verdictButton.disabled = v?.state === "running";
    verdictButton.dataset.state = stale ? "stale" : (v?.state ?? "unknown");
    if (v?.undeclared) {
      // A gap in what the workspace declares, not a failure. Said plainly, because "no tests ran"
      // rendered as silence reads as "nothing to worry about".
      verdictButton.textContent = `${project}: no ${RUN_TARGET} target`;
      verdictButton.title = v.undeclared;
      verdictButton.disabled = true;
      return;
    }
    const secs = v?.durationMs ? ` ${(v.durationMs / 1000).toFixed(1)}s` : "";
    switch (v?.state) {
      case "running":
        verdictButton.textContent = `Testing ${project}...`;
        verdictButton.title = `magus run ${RUN_TARGET} ${project} is in flight`;
        break;
      case "passed":
        verdictButton.textContent = stale
          ? `${project} passed - since edited`
          : `${project} passed${secs}`;
        verdictButton.title = stale
          ? "This verdict is about the changeset as it was, not as it is now. Run again."
          : "Run again";
        break;
      case "failed":
        verdictButton.textContent = stale
          ? `${project} failed - since edited`
          : `${project} failed`;
        verdictButton.title = v.error || "Run again";
        break;
      default:
        verdictButton.textContent = `Test ${project}`;
        verdictButton.title = `Run ${RUN_TARGET} for ${project} on this machine`;
    }
  };

  // startRun submits the project in view, then polls until it settles.
  //
  // The daemon coalesces, so pressing this while the reader's own terminal is already running the
  // same target joins that run rather than starting a second - and the reply says which happened,
  // which is why the surface can show "Testing..." without ever having to claim it started it.
  const startRun = async (): Promise<void> => {
    const project = currentProject();
    if (!project) return;
    const asOf = treeState();
    state.verdicts.set(project, { state: "running", asOf });
    renderVerdict();
    if (demo) {
      const v = await demoRun();
      state.verdicts.set(project, { state: v.state, durationMs: v.duration_ms, asOf });
      renderVerdict();
      return;
    }
    const hp = host_();
    if (!hp) {
      state.verdicts.delete(project);
      renderVerdict();
      return;
    }
    const first = await runTarget(hp, RUN_TARGET, project, true, controller.signal);
    if (first.undeclared) {
      state.verdicts.set(project, { state: "unknown", asOf, undeclared: first.undeclared });
      renderVerdict();
      return;
    }
    await pollRun(project, asOf);
  };

  // pollRun watches one run to completion. Polling rather than streaming: the verdict is a single
  // bit arriving once, and a socket per surface to deliver it would cost more than it saves.
  const pollRun = async (project: string, asOf: string): Promise<void> => {
    // Bounded so a run that never reports back leaves the control usable rather than stuck on
    // "Testing..." forever. At 1.5s that is ten minutes, past any test run worth watching inline.
    for (let i = 0; i < 400; i++) {
      await new Promise((r) => setTimeout(r, 1500));
      const hp = host_();
      if (controller.signal.aborted || !hp) return;
      const v = await runTarget(hp, RUN_TARGET, project, false, controller.signal);
      if (v.state === "running") continue;
      state.verdicts.set(project, {
        state: v.state,
        error: v.error,
        durationMs: v.duration_ms,
        asOf,
      });
      renderVerdict();
      return;
    }
    state.verdicts.delete(project);
    renderVerdict();
  };

  // renderProgress draws where the pass is. Only in focus mode: the dense view already answers
  // this with its "n/m hunks read" chip, and two counts of the same thing in one row is how a
  // reader stops believing either.
  const renderProgress = (): void => {
    focusButton.textContent = state.focus ? "Leave focus" : "Focus";
    focusButton.title = state.focus
      ? "Show the whole changeset again (f)"
      : "Read one hunk at a time (f)";
    progressEl.hidden = !state.focus;
    statsEl.hidden = state.focus;
    renderVerdict();
    if (!state.focus) return;
    const total = state.pairs.length;
    const read = readCount();
    const at = pairAt();
    progressFill.style.width = total > 0 ? `${(read / total) * 100}%` : "0%";
    const left = state.files[0]?.path ?? "";
    const drafted = drafts().length;
    // Position, then what is left, then what the pass has produced so far. The last one is why a
    // draft count belongs here: it is the evidence that reading is turning into something.
    progressText.textContent =
      `hunk ${at + 1} of ${total}, ${read} read` +
      (drafted > 0 ? `, ${drafted} drafted` : "") +
      (left ? ` - ${left}` : "");
  };

  const renderSidebar = (): void => {
    // Grouped by PROJECT, not by directory depth. In a monorepo a path answers two questions -
    // which project owns this, and which file is it - and everything between them is filler that
    // grows without bound as the tree nests. The project is the bounded, meaningful half, magus
    // already knows it per file (DiffAnnotation.project, the same unit that decides cache keys
    // and the affected set), and writing it once per group means the per-file line only ever
    // carries the path RELATIVE to it. That is one or two segments whether the repo is three
    // levels deep or ten, so the item stops getting worse as the monorepo grows.
    const groups = new Map<string, { o: (typeof state.changeset.primary)[number]; i: number }[]>();
    state.changeset.primary.forEach((o, i) => {
      // Falling back to the top-level segment keeps a file magus does not attribute in a sane
      // group rather than in a nameless one; every demo and real annotation carries a project.
      const key = o.annotation?.project ?? (o.file.path.split("/")[0] || o.file.path);
      const bucket = groups.get(key);
      if (bucket) bucket.push({ o, i });
      else groups.set(key, [{ o, i }]);
    });
    const needle = sidebarFilter.value.trim().toLocaleLowerCase();
    sidebarEntries = [];
    let shownTotal = 0;
    for (const [project, entries] of groups) {
      const shown = needle
        ? entries.filter(({ o }) =>
            `${project}/${o.file.path}`.toLocaleLowerCase().includes(needle),
          )
        : entries;
      if (shown.length === 0) continue;
      shownTotal += shown.length;
      sidebarEntries.push({ kind: "project", project, count: shown.length });
      for (const { i } of shown) sidebarEntries.push({ kind: "file", project, changeIndex: i });
    }
    sidebarOffsets = [0];
    for (const entry of sidebarEntries)
      sidebarOffsets.push((sidebarOffsets.at(-1) ?? 0) + sidebarEntryHeight(entry));
    sidebarSpacer.style.height = `${sidebarOffsets.at(-1) ?? 0}px`;
    // The total, always - filtering to 2 of 7 files while the heading kept reading "Files (7)"
    // was a count that stopped matching what was under it the moment the reader typed anything.
    const total = state.changeset.primary.length;
    sidebarTitle.textContent =
      shownTotal === total ? `Files (${total})` : `Files (${shownTotal} of ${total})`;
    sidebarIndex.scrollTop = 0;
    sidebarPaintedFirst = -1;
    sidebarPaintedLast = -1;
    paintSidebar(true);
    const generated = document.createDocumentFragment();
    if (state.changeset.generated.length > 0) {
      // Its own twist caret rather than the log viewer's console-render-section one: that class is
      // styled only in logs.css, which this surface never loads (verified cold - the button rendered
      // display:inline-block with a 0x0 caret when Diff was the first surface opened in a session,
      // and only looked right because an earlier visit to Logs/Activity/Notes had pulled the sheet
      // in already). The rotation is also driven off this button's OWN aria-expanded rather than a
      // [data-collapsed] ancestor, because this button has no such ancestor - logs.css's selector
      // could never have matched it even with the sheet loaded.
      const g = h("button", "console-diff-sidebar__group");
      g.type = "button";
      g.setAttribute("aria-expanded", state.showGenerated ? "true" : "false");
      const twist = h("span", "console-diff-sidebar__grouptwist");
      twist.setAttribute("aria-hidden", "true");
      const gLabel = h("span", "console-diff-sidebar__grouplabel");
      gLabel.textContent = `${state.changeset.generated.length} generated`;
      g.append(twist, gLabel);
      g.title = state.showGenerated
        ? "Declared target outputs. Press . to fold."
        : "Declared target outputs, folded. Press . to expand.";
      g.addEventListener("click", () => void toggleGenerated());
      generated.append(g);
    }
    sidebarGenerated.replaceChildren(generated);
  };

  sidebarFilter.addEventListener("input", () => renderSidebar(), { signal: controller.signal });

  // Suggestions stay separate from the review flow.
  const renderRail = (): void => {
    const pending = (state.session?.suggestions ?? []).filter((s) => !s.accepted && !s.declined);
    if (pending.length === 0) {
      rail.replaceChildren();
      rail.dataset.empty = "";
      return;
    }
    delete rail.dataset.empty;
    const frag = document.createDocumentFragment();
    for (const s of pending) {
      const item = h("div", "console-diff-rail__item");
      const who = h("span", "console-diff-rail__who");
      who.textContent = s.agent_name || "agent";
      const where = h("span", "console-diff-rail__where");
      where.textContent = `${s.path}${s.hunk >= 0 ? `:${s.hunk}` : ""}`;
      const reason = h("span", "console-diff-rail__reason", s.reason);
      const go = h("button", "console-diff-rail__go");
      go.type = "button";
      // The key rides as its own <kbd>, the same chip the Shortcuts overlay and the Actions
      // surface use for a physical key - not "[g]" folded into the label, which reads as part
      // of the word rather than a key you can press.
      go.append("go ", h("kbd", "console-cheatsheet-kbd", "g"));
      go.disabled = !canCollaborate();
      if (go.disabled) go.title = "Agent collaboration is unavailable";
      go.addEventListener("click", () => acceptSuggestion(s.id));
      const skip = h("button", "console-diff-rail__skip");
      skip.type = "button";
      skip.append("skip ", h("kbd", "console-cheatsheet-kbd", "x"));
      skip.disabled = !canCollaborate();
      if (skip.disabled) skip.title = "Agent collaboration is unavailable";
      skip.addEventListener("click", () => sync({ op: "answer", id: s.id, on: false }));
      item.append(who, where, reason, go, skip);
      frag.append(item);
    }
    rail.replaceChildren(frag);
  };

  const renderOverview = (): void => {
    const s = stats(state.changeset);
    const box = h("div", "console-diff-overview__box");
    box.append(h("h2", "console-diff-overview__title", "This changeset"));

    const line = (k: string, v: string, title?: string): HTMLElement => {
      const r = h("div", "console-diff-overview__row");
      const kk = h("span", "console-diff-overview__k", k);
      const vv = h("span", "console-diff-overview__v", v);
      if (title) r.title = title;
      r.append(kk, vv);
      return r;
    };
    box.append(line("to read", `${s.files} files, +${s.additions} -${s.deletions}`));
    if (s.generated > 0)
      box.append(line("folded away", `${s.generated} generated`, "Declared target outputs"));
    if (s.publicSurface > 0)
      box.append(
        line(
          "public surface",
          `${s.publicSurface} files`,
          "Changed symbols reachable outside their project or module",
        ),
      );
    if (s.untested > 0) box.append(line("measured untested", `${s.untested} files`));
    const seeds = state.session?.diff?.seed_projects ?? [];
    const affected = state.session?.diff?.affected_projects ?? [];
    if (affected.length > 0) {
      box.append(
        line(
          "projects",
          `${seeds.length} edited, ${affected.length} rebuild`,
          "Edited counts projects a person changed a source file in; generated-only changes do not count as an edit. Rebuild is the full downstream closure over every changed path, generated included, because a regenerated output still invalidates a cache key.",
        ),
      );
    }
    for (const n of state.session?.diff?.notes ?? []) {
      const note = h("p", "console-diff-overview__note");
      note.textContent = n;
      box.append(note);
    }
    const top = state.changeset.primary.slice(0, 5);
    if (top.length > 0) {
      // The heading is a CLAIM, so it changes when the ranking behind it is absent rather than
      // asserting a priority the order does not carry.
      if (ranked()) {
        box.append(h("h3", "console-diff-overview__subtitle", "Read these first"));
      } else {
        box.append(h("h3", "console-diff-overview__subtitle", "First by path (unranked)"));
        const why = h("p", "console-diff-overview__note");
        why.textContent = UNRANKED_TITLE;
        box.append(why);
      }
      for (const o of top) {
        const r = h("button", "console-diff-overview__file");
        r.type = "button";
        r.textContent = o.file.path;
        r.addEventListener("click", () => {
          state.overview = false;
          root.dataset.overview = "off";
          const i = state.changeset.primary.indexOf(o);
          const row = state.fileRows[i];
          if (row !== undefined) scrollToRow(row);
          scroll.focus();
        });
        box.append(r);
      }
    }
    // Threads with nowhere in the stream to sit. They are READ here rather than merely
    // counted: a chip saying "3 elsewhere" tells the reader something was said and withholds
    // what, which is worse than not mentioning it - they now have to leave to find out.
    // A capability gap, said out loud. Without it the overview looks identical to a repository
    // where genuinely nothing else is in flight, and the reader would take magus's silence for
    // an all-clear it never checked.
    if (state.branchesUnsupported) {
      box.append(
        h(
          "p",
          "console-diff-overview__note",
          `Other branches: ${state.branchesUnsupported}. magus cannot say what else is changing these files here.`,
        ),
      );
    }
    const elsewhere = state.threads?.elsewhere ?? [];
    if (elsewhere.length > 0) {
      box.append(h("h3", "console-diff-overview__subtitle", "Said on the review, elsewhere"));
      const why = h("p", "console-diff-overview__note");
      why.textContent =
        "These are on files this view is not showing, whether folded away or outside the changeset. A review covers commits a working diff does not.";
      box.append(why);
      for (const t of elsewhere) {
        const r = h("p", "console-diff-overview__note");
        r.append(h("span", undefined, `${t.author} on ${t.path}:${t.line} - `));
        const said = h("span", "console-diff-md");
        setMarkdown(said, t.body);
        r.append(said);
        box.append(r);
      }
    }
    box.append(
      h(
        "p",
        "console-diff-overview__hint",
        "Esc returns to the diff. ] and [ step hunks, } and { step files, v marks read, " +
          "u jumps to the next file needing attention, . folds generated.",
      ),
    );
    overview.replaceChildren(box);
  };

  // --- actions --------------------------------------------------------------

  const scrollToRow = (i: number): void => {
    state.cursor = i;
    // Smooth scrolling is motion, and for a reader who has asked the OS for less of it, a
    // whole diff sliding past on every `[` is the symptom they turned it off to avoid.
    scroll.scrollTo({
      top: state.offsets[i] ?? 0,
      behavior: prefersReducedMotion() ? "auto" : "smooth",
    });
    // Tell the session where the reader is, so an agent can be useful about it.
    const f = state.rows[i];
    if (f) {
      const path = "file" in f ? f.file.path : "";
      if (path) sync({ op: "cursor", path, hunk: hunkIndexWithinFile(i) });
    }
    paint();
  };

  // hunkIndexWithinFile converts a row index to the hunk's position inside its own file, which
  // is what the session and an agent speak in - a global row index would change meaning the
  // moment the generated group folded.
  const hunkIndexWithinFile = (rowIndex: number): number => {
    return state.hunkOrdinalByRow[rowIndex] ?? -1;
  };

  const step = (dir: 1 | -1, marks: number[]): void => {
    const from = state.cursor >= 0 ? state.cursor : rowAt(state.offsets, scroll.scrollTop);
    const i = dir === 1 ? nextIndexAfter(marks, from) : prevIndexBefore(marks, from);
    if (i !== null) scrollToRow(i);
  };

  // --- focus mode -----------------------------------------------------------

  const pairAt = (): number =>
    state.pairs.findIndex(
      (p) => p.path === state.focusAt?.path && p.index === state.focusAt?.index,
    );

  const readCount = (): number => state.pairs.filter((p) => state.viewed.has(p.digest)).length;

  // firstUnread is where a pass resumes. Read-marks already persist, so an interrupted pass
  // picks up where it stopped instead of starting at the top - which is the whole reason to
  // read this way: the pass has to survive being interrupted.
  const firstUnread = (): { path: string; index: number } | null => {
    const p = state.pairs.find((x) => !state.viewed.has(x.digest)) ?? null;
    return p ? { path: p.path, index: p.index } : null;
  };

  // focusStep moves the pass. Forward past the last hunk is not a dead end: the reading is
  // finished, so it opens the batch that reading produced. The pass gets a conclusion rather
  // than running out.
  const focusStep = (dir: 1 | -1): void => {
    const at = pairAt();
    // The focused hunk is no longer in the changeset - a fold, a rebase, a tree that moved under
    // the reader. Resume rather than arithmetic on -1, which stepped FORWARD to index 0 and
    // silently restarted the pass at the top while stepping BACK did nothing at all.
    if (at < 0) {
      state.focusAt = firstUnread() ?? state.focusAt;
      void rebuild();
      return;
    }
    const p = state.pairs[at + dir];
    if (!p) {
      if (dir === 1) endOfPass();
      return;
    }
    state.focusAt = { path: p.path, index: p.index };
    void rebuild();
  };

  const endOfPass = (): void => {
    if (drafts().length > 0) {
      composePublish();
      return;
    }
    const n = state.pairs.length;
    flashPublishNotice(`${n} of ${n} read. Nothing drafted, so there is nothing to send.`);
  };

  // focusRead is the one key a pass is made of: mark this hunk read, then move. Fused because
  // the two are one act to the reader, and a pass that costs two keystrokes per hunk is a pass
  // that gets abandoned. `]` still moves without marking, for reading something twice.
  //
  // The move does NOT wait on the mark. Recording a read is a round trip to the daemon, and a
  // pass that pauses on each keypress until it answers is a pass that feels broken; the mark and
  // the move are independent, so the move happens now and the count catches up when the write
  // lands.
  const focusRead = (): void => {
    const p = state.pairs[pairAt()];
    if (!p) return;
    if (!canCollaborate()) {
      flashCollaborationNotice();
      return;
    }
    if (!state.viewed.has(p.digest)) void sync({ op: "viewed", digest: p.digest, on: true });
    focusStep(1);
  };

  const setFocus = (on: boolean): void => {
    state.focus = on;
    focusCell.set(on);
    root.dataset.focus = on ? "on" : "off";
    // Entering lands on the first hunk still unread rather than wherever the scroll happened to
    // be: the mode's claim is that it knows what is left to do.
    if (on) state.focusAt = firstUnread() ?? state.focusAt;
    void rebuild();
  };

  // resume jumps to the first file that still wants attention: one that changed after it was
  // read, or failing that one nobody has read.
  //
  // Stale first, always. A file the reader already looked at and that then moved under them is
  // the one thing here they cannot discover by scrolling - it looks finished. Ordinary unread
  // files are the fallback because the stream is already ordered by consequence, so the first
  // one is the most consequential.
  //
  // Nothing to resume moves nothing. That state means the reader is done, and the toolbar
  // already says so by NOT carrying the "changed since read" chip - so a message here would be
  // a second copy of an answer the page is already giving.
  const resume = (): void => {
    for (const want of ["stale", "unread"] as const) {
      for (const i of state.fileRows) {
        const row = state.rows[i];
        if (row?.kind !== "file") continue;
        if (annotationFor(row.file.path)?.read_state === want) {
          scrollToRow(i);
          return;
        }
      }
    }
  };

  // toggleViewed marks the hunk the cursor is in. It is the READER's claim, which is why no
  // agent surface can make it.
  const toggleViewed = async (): Promise<void> => {
    const i = currentHunkRow();
    if (i === null) return;
    const digest = digestForHunk(i);
    if (!digest) return;
    if (!canCollaborate()) {
      flashCollaborationNotice();
      return;
    }
    const on = !state.viewed.has(digest);
    void sync({ op: "viewed", digest, on });
  };

  const currentHunkRow = (): number | null => {
    const from = state.cursor >= 0 ? state.cursor : rowAt(state.offsets, scroll.scrollTop);
    return prevIndexBefore(state.hunks, from + 1) ?? state.hunks[0] ?? null;
  };

  // composerField builds the input every composer here shares.
  //
  // Enter is a NEWLINE. A remark worth writing is often a paragraph and a code fence, and a
  // field where Enter commits cannot hold either - the reader loses the thought at the first
  // line break. Committing takes a deliberate act instead: the chord, or the button beside it.
  //
  // Both, not one. A chord alone is invisible to whoever has not read the docs, and a button
  // alone makes a keyboard pass reach for the mouse once per remark.
  const composerField = (opts: {
    placeholder: string;
    action: string;
    onCommit: (value: string) => void;
    onCancel: () => void;
  }): { wrap: HTMLElement; field: HTMLTextAreaElement; commit: HTMLButtonElement } => {
    const wrap = h("span", "console-diff-composer__input");
    const control = h("span", "pf-v6-c-form-control");
    const field = h("textarea", "pf-v6-c-form-control__text");
    field.rows = 3;
    field.placeholder = opts.placeholder;
    // Write and Preview, because the remark is markdown wherever it lands and the reader is
    // typing it blind otherwise: a fence or a list reads as its own syntax here and as rendered
    // text on the review. The tab pair is GitHub's, deliberately - it is the shape whoever
    // writes these already has in their hands.
    const tabs = h("span", "console-diff-composer__tabs");
    const writeTab = h("button", "console-diff-composer__tab");
    writeTab.type = "button";
    writeTab.textContent = "Write";
    const previewTab = h("button", "console-diff-composer__tab");
    previewTab.type = "button";
    previewTab.textContent = "Preview";
    const rendered = h("div", "console-diff-composer__preview console-diff-md");
    rendered.hidden = true;
    const show = (previewing: boolean): void => {
      control.hidden = previewing;
      rendered.hidden = !previewing;
      writeTab.setAttribute("aria-pressed", String(!previewing));
      previewTab.setAttribute("aria-pressed", String(previewing));
      if (!previewing) {
        field.focus();
        return;
      }
      const body = field.value.trim();
      if (body) setMarkdown(rendered, body);
      else rendered.textContent = "Nothing written yet.";
    };
    writeTab.addEventListener("click", () => show(false));
    previewTab.addEventListener("click", () => show(true));
    tabs.append(writeTab, previewTab);
    const commit = h("button", "pf-v6-c-button pf-m-primary console-diff-composer__send");
    commit.type = "button";
    commit.textContent = opts.action;
    // macOS reaches for Cmd where everything else reaches for Ctrl, and a label naming the wrong
    // one is worse than none: it teaches a chord that does nothing.
    commit.title = `${navigator.userAgent.includes("Mac") ? "Cmd" : "Ctrl"}+Enter`;
    field.addEventListener("keydown", (e) => {
      // Stopped here so the surface's own single-letter keys do not fire while typing - a bare
      // "v" in a remark must be the letter v.
      e.stopPropagation();
      if (e.key === "Escape") {
        e.preventDefault();
        opts.onCancel();
        return;
      }
      if (e.key !== "Enter" || !(e.metaKey || e.ctrlKey)) return;
      e.preventDefault();
      opts.onCommit(field.value.trim());
    });
    commit.addEventListener("click", () => opts.onCommit(field.value.trim()));
    control.append(field);
    const row = h("span", "console-diff-composer__row");
    row.append(control, rendered, commit);
    wrap.append(tabs, row);
    show(false);
    return { wrap, field, commit };
  };

  const composeComment = (): void => {
    if (!canCollaborate()) {
      flashCollaborationNotice();
      return;
    }
    const i = currentHunkRow();
    if (i === null) return;
    const row = state.rows[i];
    if (!row || row.kind !== "hunk") return;

    // One composer at a time; a second press re-focuses rather than stacking boxes.
    const existing = scroll.querySelector<HTMLTextAreaElement>(
      ".console-diff-composer__input textarea",
    );
    if (existing) {
      existing.focus();
      return;
    }

    const box = h("div", "console-diff-composer");
    const where = h("span", "console-diff-composer__where");
    where.textContent = `${row.file.path} hunk ${row.index + 1}`;
    const close = (): void => {
      box.remove();
      scroll.focus();
    };
    const { wrap: inputWrap, field } = composerField({
      placeholder: "Say what is wrong, or what you had to work out. Esc cancels.",
      action: "Stage draft",
      onCancel: close,
      onCommit: (body) => {
        close();
        if (!body) return;
        sync({
          op: "comment",
          path: row.file.path,
          hunk: row.index,
          line: anchorLine(row.hunk),
          body,
        });
      },
    });
    box.append(where, inputWrap);
    // Pinned rather than inserted into the virtualized window: the window is replaced wholesale
    // on every scroll frame, so a composer living in it would be destroyed mid-sentence.
    scroll.append(box);
    field.focus();
  };

  // loadReview asks which review is open and what has already been said on it, then re-lays the
  // stream so the threads take their places in it.
  //
  // Never throws and never degrades the session: a forge that cannot be reached is a diff with
  // no review attached, which is what this surface was before any of this existed.
  const loadReview = async (): Promise<void> => {
    if (disposed) return;
    if (demo) {
      state.review = demoReview();
      await rebuild();
      return;
    }
    const hp = host_();
    if (!hp) return;
    const info = await fetchReview(hp, controller.signal);
    if (disposed) return;
    state.review = info;
    await rebuild();
    // Now that they are ON SCREEN, say so. The daemon marks nothing as it answers - it cannot
    // know this fetch was rendered rather than aborted, refreshed away, or raced by a second
    // tab - so the watermark moves here, after the conversation has actually been drawn.
    const shown = info.threads.filter((t) => t.new).map((t) => t.id);
    if (shown.length > 0) void sync({ op: "seen", ids: shown });
  };

  // loadBranches asks what else is changing these files, after the patch is on screen.
  //
  // Last, like the review lookup and for the same reason: it forks once per branch, and nothing
  // that forks may hold up a diff the reader is waiting to read. A failure leaves the map empty,
  // which renders as no claim rather than as "nothing competes".
  const loadBranches = async (): Promise<void> => {
    if (disposed || demo) return;
    const hp = host_();
    if (!hp) return;
    const { branches, unsupported } = await fetchBranches(hp, controller.signal);
    if (disposed) return;
    // Recorded even when there is nothing to report, so the surface can tell "nobody else is
    // touching this" from "this backend has not implemented the lookup". Only the first is
    // reassurance, and only one of them is true.
    state.branchesUnsupported = unsupported;
    state.branches = new Map();
    for (const b of branches) {
      for (const p of b.paths) {
        state.branches.set(p, [...(state.branches.get(p) ?? []), b]);
      }
    }
    renderSidebar();
    paint(true);
  };

  // drafts are the remarks that have not left yet: written by the person, on this session.
  //
  // An agent's remark is NEVER here. It reaches the session over MCP, and the daemon derives
  // the published set from the session rather than from the request, so this is the same
  // filter stated on both sides rather than a rule one side could relax.
  const drafts = (): DiffComment[] =>
    (state.session?.comments ?? []).filter((c) => c.author === "human" && !c.published);

  // composePublish shows the batch that is about to leave and asks for the line that heads it.
  //
  // The listing is the point, and it is why this is not a one-key send. Publishing is the one
  // act on this surface a colleague can see, so the reader gets to read what they wrote as a
  // SET before it goes - which is the whole argument for drafting in the first place: the
  // fifth remark often changes your mind about the first.
  const composePublish = (): void => {
    const pending = drafts();
    if (!state.review?.id || pending.length === 0) {
      flashPublishNotice(
        pending.length === 0
          ? "Nothing drafted. Press c to comment on a hunk."
          : (state.review?.reason ?? "No review is open for this branch."),
      );
      return;
    }
    const existing = scroll.querySelector<HTMLTextAreaElement>(
      ".console-diff-composer__input textarea",
    );
    if (existing) {
      existing.focus();
      return;
    }

    const box = h("div", "console-diff-composer console-diff-composer--batch");
    const where = h("span", "console-diff-composer__where");
    where.textContent = `Send ${pending.length} ${pending.length === 1 ? "remark" : "remarks"} to ${destination(state.review)}`;
    // The network, said out loud. Everything else on this surface is local, so the one act that
    // leaves the machine must not look like the others - and it names the HOST, because an
    // appliance and github.com are the same feature and different destinations.
    const warn = h(
      "span",
      "console-diff-composer__network",
      `Posts over the network to ${state.review.host ?? "the review host"}. Nothing has left this machine yet.`,
    );
    const listing = h("ul", "console-diff-composer__batch");
    for (const d of pending) {
      const item = h("li");
      const at = h("span", "console-diff-composer__at", `${d.path}:${d.line ?? "?"}`);
      // A draft with no line is one no host can place, so it is held back rather than guessed
      // at. Said here, before the send, rather than discovered afterwards as a remark that
      // quietly never arrived.
      if (!d.line) item.dataset.unplaceable = "";
      item.append(at, h("span", "console-diff-composer__body", d.body));
      // Backing out is part of the transaction: a staged remark you have changed your mind
      // about should not have to be sent to get rid of it.
      const drop = h("button", "console-diff-composer__drop", "discard") as HTMLButtonElement;
      drop.type = "button";
      drop.title = "Remove this draft. It has not been sent.";
      drop.addEventListener("click", () => {
        void sync({ op: "discard", id: d.id }).then(() => {
          if (disposed) return;
          close();
          composePublish();
        });
      });
      item.append(drop);
      listing.append(item);
    }
    const close = (): void => {
      box.remove();
      scroll.focus();
    };

    // What this review will SAY. Rendered from the daemon's allowed set, never worked out here:
    // a permission rule re-implemented in a browser is one that eventually disagrees with the
    // one the publish path enforces. An older daemon sends none, which reads as remarks only.
    const allowed = state.review.verdicts ?? ["comment"];
    let verdict: ReviewVerdict = "comment";
    const verdicts = h("div", "console-diff-composer__verdicts");
    if (allowed.length > 1) {
      for (const v of allowed) {
        const label = h("label", "console-diff-composer__verdict");
        const radio = h("input") as HTMLInputElement;
        radio.type = "radio";
        radio.name = "console-diff-verdict";
        radio.value = v;
        radio.checked = v === "comment";
        radio.addEventListener("change", () => {
          if (radio.checked) verdict = v;
        });
        label.append(radio, h("span", undefined, VERDICT_COPY[v]));
        verdicts.append(label);
      }
    } else if (state.review.verdict_limit) {
      // Only remarks, and the REASON said out loud. "This is your own change" is how review is
      // meant to work; "magus could not tell who opened it" is a gap in what the provider
      // answered. Rendering them alike would hide the second behind the first.
      verdicts.append(h("span", "console-diff-composer__verdictnote", state.review.verdict_limit));
    }

    const {
      wrap: inputWrap,
      field,
      commit,
    } = composerField({
      placeholder: "One line about the pass as a whole. Optional. Esc cancels.",
      action: `Send to ${state.review.host ?? "the review host"}`,
      onCancel: close,
      onCommit: (summary) => {
        field.disabled = true;
        commit.disabled = true;
        where.textContent = "Sending...";
        const heldBack = pending.filter((d) => !d.line).length;
        void sendDrafts(summary, verdict).then((failure) => {
          if (disposed) return;
          if (!failure) {
            close();
            // A send that could not carry everything must SAY so. The remarks with no line stay
            // drafts and go nowhere, and a reader told only "sent" would believe the whole pass
            // reached their colleague.
            if (heldBack > 0) {
              flashPublishNotice(
                `Sent, but ${heldBack} ${heldBack === 1 ? "remark has" : "remarks have"} no line to anchor to and stayed a draft.`,
              );
            }
            return;
          }
          // The box STAYS OPEN on failure, holding what the reader typed. A send that failed
          // has changed nothing, so the next thing they do is try again - and retyping the
          // summary would be a punishment for the forge being down.
          field.disabled = false;
          commit.disabled = false;
          where.textContent = failure;
          box.dataset.failed = "";
          field.focus();
        });
      },
    });
    box.append(where, warn, listing, verdicts, inputWrap);
    scroll.append(box);
    field.focus();
  };

  // sendDrafts publishes and returns the failure to show, or "" when the batch left.
  //
  // A string rather than a thrown error, because the caller's job is to put the reason in front
  // of the reader: "no pull request for this branch" and "the host refused a comment on a line
  // it cannot see" send them to different places, and a boolean would send them to neither.
  const sendDrafts = async (summary: string, verdict: ReviewVerdict): Promise<string> => {
    if (demo) {
      // The showcase sends for real, into memory. Publishing is the one act here a colleague
      // would see, so a reader trying it must find out what it does rather than meeting a
      // disabled button and guessing.
      if (state.session) applySession(applyDemoPublish(state.session));
      return "";
    }
    const hp = host_();
    if (!hp) return "Connect a daemon to publish.";
    try {
      const next = await publish(hp, summary, verdict, controller.signal);
      if (disposed) return "";
      applySession(next);
      // Re-read the review, so what just left comes back as a thread beside the code it is
      // about. Without it the remarks would vanish from the surface at the moment they became
      // the only permanent thing on it.
      await loadReview();
      return "";
    } catch (error) {
      return error instanceof Error ? error.message : String(error);
    }
  };

  // replyHere answers the thread nearest the cursor, so a conversation can be finished without
  // leaving for the browser.
  //
  // "Nearest" is the first thread rendered under the cursor's hunk, falling back to the file's.
  // That is the same rule resolveHere uses, and it is the rule a reader already has in their
  // head: the remark they can see.
  const replyHere = (): void => {
    const i = currentHunkRow();
    const row = i === null ? undefined : state.rows[i];
    if (row?.kind !== "hunk") return;
    const key = commentKey(row.file.path, row.index);
    const thread =
      state.threads?.atHunk.get(key)?.[0] ?? state.threads?.atFile.get(row.file.path)?.[0];
    if (!thread) {
      flashPublishNotice("No thread here to answer. Press c to write a remark of your own.");
      return;
    }
    const existing = scroll.querySelector<HTMLTextAreaElement>(
      ".console-diff-composer__input textarea",
    );
    if (existing) {
      existing.focus();
      return;
    }

    const box = h("div", "console-diff-composer console-diff-composer--batch");
    const where = h("span", "console-diff-composer__where");
    // Who is being answered, and where it lands. A reply goes to a PERSON, but it is also the
    // second act on this surface that leaves the machine, so it names the destination for the
    // same reason the send box does.
    where.textContent = state.review
      ? `Reply to ${thread.author} on ${destination(state.review)}`
      : `Reply to ${thread.author}`;
    const warn = h(
      "span",
      "console-diff-composer__network",
      `Posts over the network to ${state.review?.host ?? "the review host"} when you send it.`,
    );
    const close = (): void => {
      box.remove();
      scroll.focus();
    };

    const {
      wrap: inputWrap,
      field,
      commit,
    } = composerField({
      placeholder: "Esc cancels.",
      action: "Send reply",
      onCancel: close,
      onCommit: (body) => {
        if (!body) return;
        field.disabled = true;
        commit.disabled = true;
        where.textContent = "Sending...";
        void sendReply(thread.id, body).then((failure) => {
          if (disposed) return;
          if (!failure) {
            close();
            return;
          }
          // Held open with the words still in it, exactly as the send box is. A reply that did
          // not leave has changed nothing, and retyping it would be a punishment for the forge.
          field.disabled = false;
          commit.disabled = false;
          where.textContent = failure;
          box.dataset.failed = "";
          field.focus();
        });
      },
    });
    box.append(where, warn, inputWrap);
    scroll.append(box);
    field.focus();
  };

  // sendReply posts one reply and returns the failure to show, or "" when it left.
  const sendReply = async (thread: string, body: string): Promise<string> => {
    if (demo) {
      // The showcase answers for real, into memory, so a reader trying it finds out what it
      // does rather than meeting a dead key.
      state.review = applyDemoReply(state.review, thread, body);
      await rebuild();
      return "";
    }
    const hp = host_();
    if (!hp) return "Connect a daemon to reply.";
    try {
      await reply(hp, thread, body, controller.signal);
      if (disposed) return "";
      // Re-read rather than appending locally: the thread belongs to the host, and this is
      // also how the reader finds out what else was said while they were typing.
      await loadReview();
      return "";
    } catch (error) {
      return error instanceof Error ? error.message : String(error);
    }
  };

  // resolveHere closes the first unresolved comment on the hunk the cursor is in. Either party
  // may resolve - see the store - so this needs no author check.
  const resolveHere = (): void => {
    if (!canCollaborate()) {
      flashCollaborationNotice();
      return;
    }
    const i = currentHunkRow();
    if (i === null) return;
    const row = state.rows[i];
    if (!row || row.kind !== "hunk") return;
    const open = (state.session?.comments ?? []).find(
      (c) => c.path === row.file.path && c.hunk === row.index && !c.resolved,
    );
    if (open) sync({ op: "resolve", id: open.id, on: true });
  };

  const acceptSuggestion = (id?: string): void => {
    const pending = (state.session?.suggestions ?? []).filter((s) => !s.accepted && !s.declined);
    const target = id ? pending.find((s) => s.id === id) : pending[0];
    if (!target) return;
    // Accepting is the ONLY path from an agent's suggestion to the reader's viewport.
    if (!canCollaborate()) {
      flashCollaborationNotice();
      return;
    }
    void sync({ op: "answer", id: target.id, on: true }).then((saved) => {
      if (!saved || disposed) return;
      const row = state.rows.findIndex(
        (candidate) =>
          candidate.kind === "hunk" &&
          candidate.file.path === target.path &&
          candidate.index === target.hunk,
      );
      if (row >= 0) scrollToRow(row);
      scroll.focus();
    });
  };

  const toggleGenerated = async (): Promise<void> => {
    state.showGenerated = !state.showGenerated;
    await rebuild();
  };

  // Same shape as toggleGenerated and deliberately a separate control: a generated file is a
  // machine's restatement of an edit made elsewhere, and a settled file is one this reader already
  // weighed. One key for both would fold a colleague's unreviewed generated file and the reader's
  // own finished work under one word.
  const toggleSettled = async (): Promise<void> => {
    state.showSettled = !state.showSettled;
    await rebuild();
  };

  const setMode = async (m: ViewMode): Promise<void> => {
    if (m === state.mode) return;
    state.mode = m;
    // Same rule as the index toggle: a mode the PANE forced is not a preference. Persisting it
    // would let one narrow tile rewrite the choice the reader made at full width, and the width
    // observer below drives this same function.
    if (!paneNarrow) modeCell.set(m);
    await rebuild();
  };

  const toggleOverview = (): void => {
    state.overview = !state.overview;
    root.dataset.overview = state.overview ? "on" : "off";
    if (state.overview) renderOverview();
    else scroll.focus();
  };

  // --- commands -------------------------------------------------------------
  const COMMANDS: { id: string; label: string; run: () => void; key?: string }[] = [
    {
      id: "diff.focus.toggle",
      label: "Diff: read one hunk at a time",
      run: () => setFocus(!state.focus),
      key: "f",
    },
    {
      id: "diff.hunk.next",
      label: "Diff: next hunk",
      // In focus mode the stream holds one hunk, so there is no next row to scroll to - the
      // step is a rebuild around the next one.
      run: () => (state.focus ? focusStep(1) : step(1, state.hunks)),
      key: "]",
    },
    {
      id: "diff.hunk.prev",
      label: "Diff: previous hunk",
      run: () => (state.focus ? focusStep(-1) : step(-1, state.hunks)),
      key: "[",
    },
    {
      id: "diff.file.next",
      label: "Diff: next file",
      run: () => step(1, state.fileRows),
      key: "}",
    },
    {
      id: "diff.file.prev",
      label: "Diff: previous file",
      run: () => step(-1, state.fileRows),
      key: "{",
    },
    {
      id: "diff.viewed.toggle",
      label: "Diff: mark hunk read",
      run: () => void (state.focus ? focusRead() : toggleViewed()),
      key: "v",
    },
    {
      id: "diff.resume",
      label: "Diff: go to the first file that needs reading",
      run: () => resume(),
      key: "u",
    },
    {
      id: "diff.settled.toggle",
      label: "Diff: fold or unfold files you have already read",
      run: () => void toggleSettled(),
      key: "n",
    },
    {
      id: "diff.generated.toggle",
      label: "Diff: fold or unfold generated files",
      run: () => void toggleGenerated(),
      key: ".",
    },
    {
      id: "diff.view.unified",
      label: "Diff: unified view",
      run: () => void setMode("unified"),
      key: "1",
    },
    {
      id: "diff.view.split",
      label: "Diff: split view",
      run: () => void setMode("split"),
      key: "2",
    },
    {
      id: "diff.view.toggle",
      label: "Diff: toggle split and unified",
      run: () => void setMode(state.mode === "split" ? "unified" : "split"),
      key: "0",
    },
    {
      id: "diff.suggestion.accept",
      label: "Diff: go to the agent's suggestion",
      run: () => acceptSuggestion(),
      key: "g",
    },
    {
      id: "diff.suggestion.skip",
      label: "Diff: skip the agent's suggestion",
      run: () => {
        const p = (state.session?.suggestions ?? []).find((s) => !s.accepted && !s.declined);
        if (p) sync({ op: "answer", id: p.id, on: false });
      },
      key: "x",
    },
    {
      id: "diff.comment",
      label: "Diff: comment on this hunk",
      run: composeComment,
      key: "c",
    },
    {
      id: "diff.comment.resolve",
      label: "Diff: resolve the comment here",
      run: resolveHere,
      key: "r",
    },
    {
      id: "diff.publish",
      label: "Diff: send your drafts to the review",
      run: composePublish,
      key: "s",
    },
    {
      id: "diff.thread.reply",
      label: "Diff: reply to the thread here",
      run: replyHere,
      key: "a",
    },
    {
      id: "diff.context.peek",
      label: "Diff: peek surrounding code for this hunk",
      run: () => {
        const i = currentHunkRow();
        const row = i === null ? undefined : state.rows[i];
        if (row?.kind === "hunk") void showContext(row.file, row.hunk, true);
      },
      key: "p",
    },
    {
      id: "diff.overview",
      label: "Diff: changeset overview",
      run: () => {
        if (!context.hidden) closeContext();
        else toggleOverview();
      },
      key: "Escape",
    },
  ];
  // c.key travels with the registration so the cheat sheet shows it. One declaration drives
  // dispatch and display both, which is what stops the two drifting - a rebinding that
  // updated only the handler would otherwise leave the sheet teaching the old key.
  for (const c of COMMANDS)
    registerCommand({ id: c.id, label: c.label, group: "Diff", run: c.run, key: c.key });

  // Built one at a time rather than from an array, because `new Map(pairs)` keeps the LAST
  // pair for a duplicate key and says nothing. A second command claiming a bound key then
  // becomes silently unreachable, and any string that told a reader to press it points at
  // whatever won - which is how a "press g" tooltip came to accept an agent's suggestion.
  const byKey = new Map<string, () => void>();
  for (const c of COMMANDS) {
    if (!c.key) continue;
    if (byKey.has(c.key)) throw new Error(`diff: two commands claim the "${c.key}" key: ${c.id}`);
    byKey.set(c.key, c.run);
  }
  scroll.addEventListener(
    "keydown",
    (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const run = byKey.get(e.key);
      if (!run) return;
      e.preventDefault();
      // A notice answers the key that provoked it, so the next key retires it. Cleared before
      // the command runs, since the command may well set a new one.
      if (transientNotice) {
        transientNotice = "";
        renderToolbar();
      }
      run();
    },
    { signal: controller.signal },
  );
  // Esc has to work from the overview too, where the scroll container is not focused.
  //
  // The defaultPrevented guard is load-bearing, not defensive. root is an ANCESTOR of the
  // scroll container, so an Esc pressed while reading fires the handler above, opens the
  // overview, then BUBBLES here - where the overview is now open, so this would close it
  // again and Esc would appear to do nothing at all. Skipping an event the inner handler
  // already claimed leaves this one covering only the case it exists for: Esc pressed while
  // the overview has focus, where the inner handler never runs.
  root.addEventListener(
    "keydown",
    (e) => {
      if (e.defaultPrevented) return;
      if (e.key === "Escape" && !context.hidden) {
        e.preventDefault();
        closeContext();
        return;
      }
      if (e.key === "Escape" && state.overview) {
        e.preventDefault();
        toggleOverview();
      }
    },
    { signal: controller.signal },
  );

  // --- load -----------------------------------------------------------------

  // cmd is the trailing "run this" half of an empty state, as a real <code> element the way the
  // activity trail writes it. Backticks in the string rendered as backticks - textContent does not
  // read markdown - so this surface was the one telling the reader to type punctuation.
  const showEmpty = (title: string, body: string, cmd?: string, offerDemo = false): void => {
    state.phase = "empty";
    root.dataset.phase = "empty";
    emptyTitle.textContent = title;
    emptyBody.textContent = body;
    if (cmd) emptyBody.append(" ", h("code", undefined, cmd), ".");
    // Where a populated version lives. Every /console/<surface>/ path is the SHELL with a <base>
    // injected (scripts/surface-stubs.mjs), so the Workspace menu is always on screen - there is no
    // shell-less page that would need a different sentence.
    if (offerDemo) {
      emptyBody.append(" ", "Pick acme from the Workspace menu to see a fabricated changeset.");
    }
  };

  const load = async (): Promise<void> => {
    stopPolling();
    // The showcase joins the same pipeline one step in, with the patch and the session the
    // daemon would have returned. Everything below order() is the production path, so what it
    // shows off is the surface itself rather than a rendering of it. No fetch is issued at all,
    // which is what makes /console/diff/#demo work with no daemon, no workspace and offline.
    if (demo) {
      state.collaboration = "live";
      const sess = demoSession();
      state.changeset = order(fromWire(DEMO_FILES), sess);
      state.phase = "ready";
      root.dataset.phase = "ready";
      root.dataset.overview = "off";
      applySession(sess, false);
      await loadReview();
      scroll.focus();
      return;
    }

    const hp = host_();
    if (!hp) {
      state.collaboration = "unavailable";
      showEmpty(
        "No daemon connected",
        "Diff reads the working tree through a local daemon. Start one with:",
        "magus server start",
        true,
      );
      return;
    }
    let files: readonly WireFile[];
    let digest = "";
    try {
      const res = await fetchPatch(hp, controller.signal);
      if (res.clean) {
        showEmpty("Nothing to read", "The working tree is clean. Every change is committed.");
        return;
      }
      files = res.files;
      digest = res.digest;
    } catch (e) {
      if (disposed) return;
      const status = e instanceof HttpError ? e.status : 0;
      showEmpty(
        status === 503 ? "No workspace" : "Could not read the diff",
        status === 503 ? "The daemon is running but has not opened a workspace yet." : String(e),
      );
      return;
    }

    const parsed = fromWire(files);
    if (parsed.length === 0) {
      // NOT an empty state: the daemon sent a patch and this reader failed on it. Titling that
      // "nothing to read" tells someone their tree is clean when it is not, which is the one
      // wrong answer that costs something - they stop looking.
      showEmpty(
        "Could not read the diff",
        "The daemon returned a patch this reader could not parse.",
      );
      return;
    }

    // PHASE ONE: paint the patch. No annotations yet, so this is a plain (fast) diff.
    state.changeset = order(parsed, null);
    state.phase = "ready";
    root.dataset.phase = "ready";
    root.dataset.overview = "off";
    await rebuild();
    scroll.focus();

    // PHASE TWO: annotate and attach the session. Failure here leaves a working diff viewer
    // rather than an error - the annotations are the differentiator, not the product.
    try {
      const snapshot = digest;
      const sess = await fetchSession(
        hp,
        parsed.map((f) => f.path),
        controller.signal,
      );
      if (disposed) return;
      if (sess.as_of && sess.as_of !== snapshot) {
        // Do not decorate an older patch with a session computed after it moved. The plain reader
        // remains usable, but all paired affordances are honestly held until the reader refreshes.
        setCollaboration("stale");
        return;
      }
      if (!applySession(sess)) return;
      state.changeset = order(parsed, sess);
      await rebuild();
      startPolling();
      // PHASE THREE: the review. Last, and not awaited by anything above it, because it is the
      // only call that leaves this machine - a forge taking ten seconds must cost the reader
      // nothing but a chip that arrives late.
      void loadReview();
      // Beside it, for the same reason: it forks once per branch, so it arrives rather than
      // being waited on.
      void loadBranches();
    } catch {
      // Keep the reader available, but never imply that comments, read marks, or suggestions are
      // synchronized when the pairing step did not complete.
      setCollaboration("degraded");
    }
  };

  // The pane-width defaults. Installed here, at the foot of activate(), because it drives setMode
  // and so must be declared after it. ResizeObserver fires once on observe with the current box, so
  // the bootstrap guess taken from the window above is corrected as soon as the surface is laid
  // out, and again on every retile - which is the case a viewport query cannot see at all.
  //
  // Applied only on a CHANGE of state, never per resize tick: setMode rebuilds the row model, and
  // running that on every pixel of a drag would be the whole stream re-laid out continuously.
  const applyPaneWidth = (width: number): void => {
    if (width <= 0) return; // detached or display:none - not a measurement
    const isNarrow = width < NARROW_PX;
    if (isNarrow === paneNarrow) return;
    paneNarrow = isNarrow;
    applySidebar(isNarrow ? true : sidebarCell.get());
    void setMode(isNarrow ? "unified" : modeCell.get());
  };
  const paneResize =
    typeof ResizeObserver === "undefined"
      ? null
      : new ResizeObserver((entries) => {
          if (!disposed) applyPaneWidth(entries[0].contentRect.width);
        });
  paneResize?.observe(root);
  controller.signal.addEventListener("abort", () => paneResize?.disconnect(), { once: true });

  void load();

  return {
    setVisible(visible: boolean): void {
      surfaceVisible = visible;
      if (visible) startPolling();
      else stopPolling();
    },
    deactivate(): void {
      disposed = true;
      stopPolling();
      controller.abort();
      for (const c of COMMANDS) unregisterCommand(c.id);
      host.replaceChildren();
    },
  };
}
