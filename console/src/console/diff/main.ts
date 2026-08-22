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

import { parsePatch, type DiffFile, type DiffLine, type FileStatus, type Hunk } from "./parse";
import {
  buildRows,
  byHunk,
  hunkRowIndexes,
  fileRowIndexes,
  nextIndexAfter,
  prevIndexBefore,
  rowOffsets,
  rowAt,
  fileOfRow,
  maxLineChars,
  storyText,
  LINE_PREFIX_CHARS,
  type Row,
  type ViewMode,
} from "./rows";
import { order, visibleFiles, stats, riskChips, type OrderedChangeset } from "./order";
import { emphasis, pairForEmphasis, type Span } from "./words";
import { languageFor, tokenize, type Language } from "./syntax";
import {
  fetchPatch,
  fetchContext,
  fetchSession,
  fetchReviewSession,
  mutate,
  hunkDigest,
  patchDigest,
  HttpError,
  type DiffSession,
  type DiffAnnotation,
  type DiffTouch,
} from "./session";
import { DEMO_PATCH, demoSession, applyDemoOp } from "./demo";
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
  viewed: Set<string>;
  // digestByRow maps a hunk row index to its content digest, computed once per rebuild so a
  // keypress never awaits a hash.
  digestByRow: Map<number, string>;
  showGenerated: boolean;
  overview: boolean;
  phase: Phase;
  collaboration: CollaborationState;
}

const STATUS_COPY: Record<FileStatus, { short: string; modifier: string }> = {
  added: { short: "A", modifier: "pf-m-green" },
  deleted: { short: "D", modifier: "pf-m-red" },
  modified: { short: "M", modifier: "pf-m-grey" },
  renamed: { short: "R", modifier: "pf-m-blue" },
  copied: { short: "C", modifier: "pf-m-blue" },
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

// emphasisFor holds each changed line's intra-line span, computed once per rebuild. Keyed by
// the line OBJECT: parse.ts freezes lines and the virtualizer renders them repeatedly from
// arbitrary offsets, so recomputing per paint would redo the same work on every scroll frame.
const emphasisFor = new WeakMap<DiffLine, Span>();

// Cache word-level emphasis per immutable hunk.
const emphasisMarked = new WeakSet<Hunk>();
function markEmphasis(hunk: Hunk): void {
  if (emphasisMarked.has(hunk)) return;
  let dels: DiffLine[] = [];
  let adds: DiffLine[] = [];
  const flush = (): void => {
    for (const [d, a] of pairForEmphasis(dels, adds)) {
      const e = emphasis(d.text, a.text);
      if (e.before) emphasisFor.set(d, e.before);
      if (e.after) emphasisFor.set(a, e.after);
    }
    dels = [];
    adds = [];
  };
  for (const line of hunk.lines) {
    if (line.kind === "del" && adds.length === 0) dels.push(line);
    else if (line.kind === "add" && dels.length > 0) adds.push(line);
    else {
      flush();
      if (line.kind === "del") dels.push(line);
    }
  }
  flush();
  emphasisMarked.add(hunk);
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
  const span = emphasisFor.get(line);
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

  // The daemon-free showcase, on the fragment every other surface reads. Derived ONCE, here,
  // rather than per fetch: a console served BY a daemon would otherwise answer host_() with a
  // real origin and the showcase would start writing a stranger's review session.
  const demo = wantsDemo(parseHash());

  // The shell's 48rem inversion. Both width-dependent defaults on this surface key off it: the file
  // index below, and the view mode in the state literal.
  const narrow = window.matchMedia("(max-width: 47.999rem)");

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
    // narrow pane into two columns that each spend ~7ch on gutters and markers before any code, and
    // the preference is NOT overwritten here - widen the window and split comes back.
    mode: narrow.matches ? "unified" : (modeCell.get() ?? "unified"),
    cursor: -1,
    session: null,
    viewed: new Set(),
    digestByRow: new Map(),
    // Open. The activity view folds its sections shut because a run's output is long and
    // mostly uninteresting; a changeset's file list is the thing the reader came for, so the
    // sidebar starts showing everything and folds on request rather than the other way round.
    showGenerated: true,
    overview: false,
    phase: "loading",
    collaboration: demo ? "live" : "unavailable",
  };

  // --- scaffold -------------------------------------------------------------
  const root = h("div", "console-diff-layout");
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
  const sidebarFilter = h("input", "console-diff-sidebar__filter") as HTMLInputElement;
  sidebarFilter.type = "search";
  sidebarFilter.placeholder = "Filter files";
  sidebarFilter.setAttribute("aria-label", "Filter changed files");
  sidebarFilter.autocomplete = "off";
  const sidebarIndex = h("div", "console-diff-sidebar__index");
  sidebarIndex.setAttribute("role", "list");
  sidebarIndex.setAttribute("aria-label", "Changed files index");
  const sidebarSpacer = h("div", "console-diff-sidebar__spacer");
  const sidebarWindow = h("div", "console-diff-sidebar__window");
  sidebarSpacer.append(sidebarWindow);
  sidebarIndex.append(sidebarSpacer);
  const sidebarGenerated = h("div", "console-diff-sidebar__generated");
  sidebar.append(sidebarHead, sidebarFilter, sidebarIndex, sidebarGenerated);

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
  hideBtn.addEventListener("click", () => {
    sidebarCell.set(true);
    applySidebar(true);
  });
  reopenBtn.addEventListener("click", () => {
    sidebarCell.set(false);
    applySidebar(false);
  });
  sidebar.setAttribute("aria-label", "Changed files");

  const main = h("div", "console-diff-main");
  const toolbar = h("div", "console-diff-toolbar");
  const statsEl = h("div", "console-diff-toolbar__stats");
  const collaborationNotice = h("span", "console-diff-collaboration");
  collaborationNotice.setAttribute("role", "status");
  collaborationNotice.setAttribute("aria-live", "polite");
  toolbar.append(statsEl, collaborationNotice);
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

  main.append(toolbar, context, rail, viewport, overview, empty);
  root.append(sidebar, reopenBtn, main);
  // Below the shell's 48rem inversion the index starts COLLAPSED, whatever is stored. Its column
  // floor is 180px, so on a 375px phone it took 48% of the screen and left the hunk stream 194px to
  // render 1163px of content. The stored preference is deliberately NOT consulted here: it is
  // overwhelmingly set on a desktop, and honouring an "open" from there is exactly how the 48%
  // sidebar comes back. Opening it on a phone still works and lasts the session, which is the same
  // per-session treatment the log viewer's run browser gives its own index (logs/runtree.ts).
  // JS-driven rather than a media query because the collapse is expressed with the hidden attribute.
  applySidebar(narrow.matches ? true : sidebarCell.get());
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
    if (file.additions > 0) el.append(label(`+${file.additions}`, "pf-m-green"));
    if (file.deletions > 0) el.append(label(`-${file.deletions}`, "pf-m-red"));

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
      el.append(h("span", "console-diff-row__text", row.hunk.header));
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
    if (row.kind === "comment") {
      const el = h("div", "console-diff-row console-diff-row--comment");
      el.dataset.author = row.comment.author;
      if (row.comment.resolved) el.dataset.resolved = "";
      const who = h("span", "console-diff-row__who");
      // The agent's own label when it gave one, else the role. Attribution is stamped by the
      // daemon from the transport, so this is reporting who wrote it rather than repeating a
      // claim the writer made about itself.
      who.textContent = row.comment.author === "agent" ? row.comment.agent_name || "agent" : "you";
      const body = h("span", "console-diff-row__comment", row.comment.body);
      el.append(who, body);
      if (row.comment.resolved) el.append(label("resolved", "pf-m-green"));
      return el;
    }
    if (row.kind === "line") {
      markEmphasis(row.hunk);
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
    markEmphasis(row.hunk);
    const el = h("div", "console-diff-row console-diff-row--pair");
    const lang = languageFor(row.file.path);
    el.append(side(row.left, "left", lang), side(row.right, "right", lang));
    return el;
  };

  // Takes the DiffLine itself rather than a structural copy: intra-line emphasis is keyed by
  // the line object, so a shape-compatible clone would silently lose it.
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
  let digestPaintQueued = false;

  const primeVisibleHunkDigests = (first: number, last: number): void => {
    // Paint only hunks in the virtual window.
    let lo = 0;
    let hi = state.hunks.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if ((state.hunks[mid] ?? Number.POSITIVE_INFINITY) < first) lo = mid + 1;
      else hi = mid;
    }
    for (let i = lo; i < state.hunks.length; i++) {
      const row = state.hunks[i];
      if (row === undefined || row >= last) break;
      if (state.digestByRow.has(row) || pendingDigests.has(row)) continue;
      void digestForHunk(row).finally(() => {
        if (digestPaintQueued || disposed) return;
        digestPaintQueued = true;
        requestAnimationFrame(() => {
          digestPaintQueued = false;
          if (!disposed) paint(true);
        });
      });
    }
  };

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
    primeVisibleHunkDigests(first, last);
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
    activeSidebarFile = sidebarItems.get(fileRow);
    activeSidebarFile?.setAttribute("aria-current", "true");
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
    // Demo fixtures cannot provide workspace context.
    if (demo) return;
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
    contextBody.textContent = "Loading surrounding code…";
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

  // Materialize hunk digests only when a review action needs them.
  let pendingDigests = new Map<number, Promise<string>>();
  const digestForHunk = async (rowIndex: number): Promise<string | undefined> => {
    const known = state.digestByRow.get(rowIndex);
    if (known) return known;
    const pending = pendingDigests.get(rowIndex);
    if (pending) return pending;
    const row = state.rows[rowIndex];
    if (!row || row.kind !== "hunk") return undefined;
    const body = row.hunk.lines.map((line) =>
      line.kind === "meta" ? `\\${line.text}` : `${markerFor(line.kind)}${line.text}`,
    );
    const work = hunkDigest(row.file.path, body);
    pendingDigests.set(rowIndex, work);
    try {
      const digest = await work;
      if (!disposed && state.rows[rowIndex] === row) state.digestByRow.set(rowIndex, digest);
      return digest;
    } finally {
      pendingDigests.delete(rowIndex);
    }
  };

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

  const rebuild = async (): Promise<void> => {
    state.files = visibleFiles(state.changeset, state.showGenerated);
    // Touches come from the annotations, so the first paint has none and the stream gains the
    // story rows when the review lands - the same two-phase shape everything else here uses.
    const touches = new Map<string, readonly DiffTouch[]>();
    for (const f of state.session?.diff?.files ?? []) {
      if (f.touches?.length) touches.set(f.path, f.touches);
    }
    state.rows = buildRows(state.files, state.mode, byHunk(state.session?.comments ?? []), touches);
    state.hunks = hunkRowIndexes(state.rows);
    state.hunkOrdinalByRow = hunkOrdinal(state.rows);
    state.fileRows = fileRowIndexes(state.rows);
    state.offsets = rowOffsets(state.rows);
    state.fileOf = fileOfRow(state.rows);
    spacer.style.height = `${state.offsets[state.rows.length]}px`;
    const rowFloorPx = (maxLineChars(state.rows) + LINE_PREFIX_CHARS) * monoCharWidth();
    scroll.style.setProperty("--console-diff-min-row-width", `${rowFloorPx}px`);
    // Rebuilds invalidate row-indexed digest entries.
    state.digestByRow = new Map();
    pendingDigests = new Map();
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
    // Session state remains authoritative before an off-screen digest exists.
    const read = state.viewed.size;
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
    collaborationNotice.textContent = collaboration.notice;
    statsEl.replaceChildren(...chips);
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
    for (const [project, entries] of groups) {
      const shown = needle
        ? entries.filter(({ o }) =>
            `${project}/${o.file.path}`.toLocaleLowerCase().includes(needle),
          )
        : entries;
      if (shown.length === 0) continue;
      sidebarEntries.push({ kind: "project", project, count: shown.length });
      for (const { i } of shown) sidebarEntries.push({ kind: "file", project, changeIndex: i });
    }
    sidebarOffsets = [0];
    for (const entry of sidebarEntries)
      sidebarOffsets.push((sidebarOffsets.at(-1) ?? 0) + sidebarEntryHeight(entry));
    sidebarSpacer.style.height = `${sidebarOffsets.at(-1) ?? 0}px`;
    sidebarTitle.textContent = `Files (${state.changeset.primary.length})`;
    sidebarIndex.scrollTop = 0;
    sidebarPaintedFirst = -1;
    sidebarPaintedLast = -1;
    paintSidebar(true);
    const generated = document.createDocumentFragment();
    if (state.changeset.generated.length > 0) {
      // Same fold affordance as an activity section - the twist caret plus aria-expanded and
      // data-collapsed - so a collapsible in the sidebar reads the way collapsibles read
      // everywhere else in the console. It was a bare button styled like nothing else.
      const g = h("button", "console-diff-sidebar__group console-render-section__head");
      g.type = "button";
      g.setAttribute("aria-expanded", state.showGenerated ? "true" : "false");
      const twist = h("span", "console-render-section__twist");
      twist.setAttribute("aria-hidden", "true");
      const gLabel = h("span", "console-render-section__title");
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
      go.textContent = "go [g]";
      go.disabled = !canCollaborate();
      if (go.disabled) go.title = "Agent collaboration is unavailable";
      go.addEventListener("click", () => acceptSuggestion(s.id));
      const skip = h("button", "console-diff-rail__skip");
      skip.type = "button";
      skip.textContent = "skip [x]";
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
    box.append(
      h(
        "p",
        "console-diff-overview__hint",
        "Esc returns to the diff. ] and [ step hunks, } and { step files, v marks read, . folds generated.",
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

  // toggleViewed marks the hunk the cursor is in. It is the READER's claim, which is why no
  // agent surface can make it.
  const toggleViewed = async (): Promise<void> => {
    const i = currentHunkRow();
    if (i === null) return;
    const digest = await digestForHunk(i);
    if (!digest) return;
    if (!canCollaborate()) return;
    const on = !state.viewed.has(digest);
    void sync({ op: "viewed", digest, on });
  };

  const currentHunkRow = (): number | null => {
    const from = state.cursor >= 0 ? state.cursor : rowAt(state.offsets, scroll.scrollTop);
    return prevIndexBefore(state.hunks, from + 1) ?? state.hunks[0] ?? null;
  };

  // comment opens a one-line composer pinned under the hunk the cursor is in.
  //
  // A prompt() would have been fewer lines and is the wrong shape: it steals focus from the
  // page, cannot show WHICH hunk is being annotated, and gives an agent's reader no way to see
  // the code they are remarking on while they type. The composer sits in the stream for the
  // same reason the comments do.
  const composeComment = (): void => {
    if (!canCollaborate()) return;
    const i = currentHunkRow();
    if (i === null) return;
    const row = state.rows[i];
    if (!row || row.kind !== "hunk") return;

    // One composer at a time; a second press re-focuses rather than stacking boxes.
    const existing = scroll.querySelector<HTMLInputElement>(".console-diff-composer__input");
    if (existing) {
      existing.focus();
      return;
    }

    const box = h("div", "console-diff-composer");
    const where = h("span", "console-diff-composer__where");
    where.textContent = `${row.file.path} hunk ${row.index + 1}`;
    const input = h("input", "console-diff-composer__input");
    input.type = "text";
    input.placeholder =
      "Say what is wrong, or what you had to work out. Enter to post, Esc to cancel.";
    const close = (): void => {
      box.remove();
      scroll.focus();
    };
    input.addEventListener("keydown", (e) => {
      // Stopped here so the surface's own single-letter keys do not fire while typing - a
      // bare "v" in a comment must be the letter v.
      e.stopPropagation();
      if (e.key === "Escape") {
        e.preventDefault();
        close();
        return;
      }
      if (e.key !== "Enter") return;
      e.preventDefault();
      const body = input.value.trim();
      close();
      if (!body) return;
      sync({ op: "comment", path: row.file.path, hunk: row.index, body });
    });
    box.append(where, input);
    // Pinned rather than inserted into the virtualized window: the window is replaced wholesale
    // on every scroll frame, so a composer living in it would be destroyed mid-sentence.
    scroll.append(box);
    input.focus();
  };

  // resolveHere closes the first unresolved comment on the hunk the cursor is in. Either party
  // may resolve - see the store - so this needs no author check.
  const resolveHere = (): void => {
    if (!canCollaborate()) return;
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
    if (!canCollaborate()) return;
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

  const setMode = async (m: ViewMode): Promise<void> => {
    if (m === state.mode) return;
    state.mode = m;
    modeCell.set(m);
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
      id: "diff.hunk.next",
      label: "Diff: next hunk",
      run: () => step(1, state.hunks),
      key: "]",
    },
    {
      id: "diff.hunk.prev",
      label: "Diff: previous hunk",
      run: () => step(-1, state.hunks),
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
      run: () => void toggleViewed(),
      key: "v",
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
  for (const c of COMMANDS)
    registerCommand({ id: c.id, label: c.label, group: "Diff", run: c.run });

  const byKey = new Map(COMMANDS.filter((c) => c.key).map((c) => [c.key as string, c.run]));
  scroll.addEventListener(
    "keydown",
    (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const run = byKey.get(e.key);
      if (!run) return;
      e.preventDefault();
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
      emptyBody.append(
        " ",
        "Pick Demo data from the Workspace menu to see a fabricated changeset.",
      );
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
      state.changeset = order(parsePatch(DEMO_PATCH), sess);
      state.phase = "ready";
      root.dataset.phase = "ready";
      root.dataset.overview = "off";
      applySession(sess, false);
      await rebuild();
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
    let patch: string;
    try {
      const res = await fetchPatch(hp, controller.signal);
      if (res.clean) {
        showEmpty("Nothing to read", "The working tree is clean. Every change is committed.");
        return;
      }
      patch = res.patch;
    } catch (e) {
      if (disposed) return;
      const status = e instanceof HttpError ? e.status : 0;
      showEmpty(
        status === 503 ? "No workspace" : "Could not read the diff",
        status === 503 ? "The daemon is running but has not opened a workspace yet." : String(e),
      );
      return;
    }

    const parsed = parsePatch(patch);
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
      const snapshot = await patchDigest(patch);
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
    } catch {
      // Keep the reader available, but never imply that comments, read marks, or suggestions are
      // synchronized when the pairing step did not complete.
      setCollaboration("degraded");
    }
  };

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
