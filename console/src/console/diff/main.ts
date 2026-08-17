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

import { parsePatch, type DiffFile, type DiffLine, type FileStatus } from "./parse";
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
  type Row,
  type ViewMode,
} from "./rows";
import { order, visibleFiles, stats, riskChips, type OrderedChangeset } from "./order";
import { emphasis, pairForEmphasis, type Span } from "./words";
import { languageFor, tokenize, type Language } from "./syntax";
import {
  fetchPatch,
  fetchSession,
  mutate,
  hunkDigest,
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

// Rows rendered beyond the viewport so a fast scroll never shows blank space. Bounded and
// constant, unlike the diff.
const OVERSCAN = 24;

const daemonCell = persisted<string | null>("dashboard-daemon", null);
const modeCell = persisted<ViewMode>("diff-view-mode", "unified");

type Phase = "loading" | "ready" | "empty";

interface State {
  changeset: OrderedChangeset;
  files: DiffFile[];
  rows: Row[];
  // Derived from rows, and rebuilt with them: row i's top edge, plus a final total. See rowOffsets.
  offsets: number[];
  hunks: number[];
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

// The marker is the NON-COLOUR channel for add and delete. Colour alone fails WCAG 1.4.1 and
// fails anyone with a colour vision deficiency, and a diff is exactly the case where the two
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

// markEmphasis pairs each run of deleted lines with the run of added lines that follows it and
// records which part of each changed. Runs of unequal length are left alone - see
// pairForEmphasis for why inventing that correspondence would be worse than showing nothing.
function markEmphasis(files: readonly DiffFile[]): void {
  for (const f of files) {
    for (const hunk of f.hunks) {
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
    }
  }
}

// lineText renders a line's text with syntax colour and intra-line emphasis.
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
    const emphasised = span !== undefined && span.start <= from && span.end >= to;
    if (!tok && !emphasised) {
      el.append(document.createTextNode(piece));
      continue;
    }
    const classes = ["console-diff-row__text-part"];
    if (tok) classes.push(`console-diff-tok--${tok.cls}`);
    if (emphasised) classes.push("console-diff-row__word");
    el.append(h("span", classes.join(" "), piece));
  }
  return el;
}

function gutter(n: number | null): HTMLElement {
  // A non-breaking space, not "": an empty text node collapses and the gutters would shift
  // width row to row.
  return h("span", "console-diff-row__gutter", n === null ? " " : String(n));
}

export function activate(host: HTMLElement): () => void {
  const controller = new AbortController();
  let disposed = false;

  // The daemon-free showcase, on the fragment every other surface reads. Derived ONCE, here,
  // rather than per fetch: a console served BY a daemon would otherwise answer host_() with a
  // real origin and the showcase would start writing a stranger's review session.
  let demo = wantsDemo(parseHash());

  const state: State = {
    changeset: { primary: [], generated: [] },
    files: [],
    rows: [],
    offsets: [0],
    hunks: [],
    fileRows: [],
    fileOf: [],
    mode: modeCell.get() ?? "unified",
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
  };

  // --- scaffold -------------------------------------------------------------
  const root = h("div", "console-diff-layout");
  const sidebar = h("nav", "console-diff-sidebar");
  sidebar.setAttribute("aria-label", "Changed files");

  const main = h("div", "console-diff-main");
  const toolbar = h("div", "console-diff-toolbar");
  const statsEl = h("div", "console-diff-toolbar__stats");
  toolbar.append(statsEl);

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
  // The same second way out the dashboard and the activity trail offer: someone who has no
  // daemon running is the person most likely to be meeting this surface for the first time.
  const demoBtn = h("button", "pf-v6-c-button pf-m-primary") as HTMLButtonElement;
  demoBtn.type = "button";
  demoBtn.title = "A fabricated changeset, no daemon and no workspace needed";
  demoBtn.append(h("span", "pf-v6-c-button__text", "See the demo"));
  const emptyFooter = h("div", "pf-v6-c-empty-state__footer");
  const emptyActions = h("div", "pf-v6-c-empty-state__actions");
  emptyActions.append(demoBtn);
  emptyFooter.append(emptyActions);
  emptyFooter.hidden = true;
  emptyContent.append(emptyTitle, emptyBodyWrap, emptyFooter);
  empty.append(emptyContent);

  main.append(toolbar, rail, viewport, overview, empty);
  root.append(sidebar, main);
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
      if (digest && state.viewed.has(digest))
        el.append(label("read", "pf-m-green", "Marked read - press v to unmark"));
      return el;
    }
    if (row.kind === "story") {
      const el = h("div", "console-diff-row console-diff-row--story");
      const who = h("span", "console-diff-row__who");
      who.textContent = row.touch.host || "agent";
      const what = h("span", "console-diff-row__story");
      // "after reading X, Y" is the whole sentence: it is what the agent was looking at when
      // it decided to write this, which no forge can say. With no reads recorded the sentence
      // stops at the author rather than inventing a reason.
      const read = row.touch.read ?? [];
      what.textContent =
        read.length > 0 ? `wrote this after reading ${read.slice(0, 4).join(", ")}` : "wrote this";
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

  const paint = (): void => {
    const total = state.rows.length;
    if (total === 0) {
      windowEl.replaceChildren();
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

    const frag = document.createDocumentFragment();
    for (let i = first; i < last; i++) {
      const row = state.rows[i];
      if (!row) continue;
      const el = renderRow(row, i);
      el.dataset.row = String(i);
      // The absolute index, NOT the position within the rendered window. Using the window
      // position is the classic virtualization bug: it announces "row 1 of 10,000" at every
      // scroll offset, which is worse than silence because it sounds like an answer.
      el.setAttribute("role", "row");
      el.setAttribute("aria-rowindex", String(i + 1));
      if (i === state.cursor) el.dataset.cursor = "";
      frag.append(el);
    }
    windowEl.style.transform = `translateY(${state.offsets[first]}px)`;
    windowEl.replaceChildren(frag);
    paintPinned(top);
  };

  // paintPinned keeps the file header of whatever is being read at the top of the viewport.
  //
  // It shows only once the file's REAL header row has scrolled above the top edge, so the two are
  // never on screen together: while the real row is visible it is the header, and the copy would
  // just be a duplicate of the line below it.
  const paintPinned = (top: number): void => {
    const i = state.fileOf[rowAt(state.offsets, top)] ?? -1;
    const row = i >= 0 ? state.rows[i] : undefined;
    if (!row || row.kind !== "file" || (state.offsets[i] ?? 0) >= top) {
      pinned.hidden = true;
      return;
    }
    pinned.replaceChildren(renderFileRow(row.file));
    pinned.hidden = false;
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

  const sync = (op: Parameters<typeof mutate>[1]): void => {
    // In the showcase the store is in memory: the reader's marks, comments and answers have to
    // land somewhere or the affordances read as broken, and there is no daemon to land them in.
    if (demo) {
      if (state.session) applySession(applyDemoOp(state.session, op));
      return;
    }
    const hp = host_();
    if (!hp) return;
    void mutate(hp, op, controller.signal).then((s) => {
      if (!disposed && s) applySession(s);
    });
  };

  // applySession takes the daemon's copy as authoritative and re-lays the stream, because a
  // comment - the human's or an agent's - is a ROW, so it changes the scroll geometry. Only
  // repainting would leave the new remark invisible until the next unrelated rebuild.
  const applySession = (s: DiffSession, relayout = true): void => {
    const before = (state.session?.comments ?? []).length;
    state.session = s;
    state.viewed = new Set(s.viewed ?? []);
    renderRail();
    if (relayout && (s.comments ?? []).length !== before) void rebuild();
    else renderToolbar();
  };

  // --- model rebuild --------------------------------------------------------

  const rebuild = async (): Promise<void> => {
    state.files = visibleFiles(state.changeset, state.showGenerated);
    // Touches come from the annotations, so the first paint has none and the stream gains the
    // story rows when the review lands - the same two-phase shape everything else here uses.
    const touches = new Map<string, readonly DiffTouch[]>();
    for (const f of state.session?.diff?.files ?? []) {
      if (f.touches?.length) touches.set(f.path, f.touches);
    }
    markEmphasis(state.files);
    state.rows = buildRows(state.files, state.mode, byHunk(state.session?.comments ?? []), touches);
    state.hunks = hunkRowIndexes(state.rows);
    state.fileRows = fileRowIndexes(state.rows);
    state.offsets = rowOffsets(state.rows);
    state.fileOf = fileOfRow(state.rows);
    spacer.style.height = `${state.offsets[state.rows.length]}px`;

    // Digest every hunk once, here, so `v` never awaits a hash mid-keypress.
    const digests = new Map<number, string>();
    await Promise.all(
      state.hunks.map(async (i) => {
        const row = state.rows[i];
        if (row?.kind !== "hunk") return;
        const body = row.hunk.lines.map((l) =>
          l.kind === "meta" ? `\\${l.text}` : `${markerFor(l.kind)}${l.text}`,
        );
        digests.set(i, await hunkDigest(row.file.path, body));
      }),
    );
    if (disposed) return;
    state.digestByRow = digests;
    paint();
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
    "No symbol index, so there is no consequence to rank by. This is path order, not a ranking - build the index with `magus graph build` to order these by what they can break.";

  const renderToolbar = (): void => {
    const s = stats(state.changeset);
    const chips: HTMLElement[] = [];
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
          "Declared target outputs. Reviewing their diff is reading a machine's restatement of a change made elsewhere - read the source change instead. Fold them from the sidebar, or press . to toggle.",
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
    const read = state.hunks.filter((i) => {
      const d = state.digestByRow.get(i);
      return d && state.viewed.has(d);
    }).length;
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
    statsEl.replaceChildren(...chips);
  };

  const renderSidebar = (): void => {
    const frag = document.createDocumentFragment();
    state.changeset.primary.forEach((o, i) => {
      const item = h("button", "console-diff-sidebar__item");
      item.type = "button";
      const st = STATUS_COPY[o.file.status];
      item.append(label(st.short, st.modifier, o.file.status));
      const name = h("span", "console-diff-sidebar__path");
      name.textContent = o.file.path;
      name.title = o.annotation?.hint ?? o.file.path;
      item.append(name);
      if (o.annotation?.surface === "public") item.dataset.surface = "public";
      if (o.annotation?.reach) {
        const r = h("span", "console-diff-sidebar__counts");
        r.textContent = String(o.annotation.reach);
        r.title = `${o.annotation.reach} files reference the widest changed symbol here`;
        item.append(r);
      }
      item.addEventListener("click", () => {
        const row = state.fileRows[i];
        if (row !== undefined) scrollToRow(row);
        scroll.focus();
      });
      frag.append(item);
    });
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
      frag.append(g);
    }
    sidebar.replaceChildren(frag);
  };

  // The suggestion rail: an agent asking for attention. It renders as a peripheral affordance
  // the reader accepts with one key and NEVER as a scroll - see types.DiffSuggestion.
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
      const what = h("span", "console-diff-rail__what");
      what.textContent = `${s.path}${s.hunk >= 0 ? `:${s.hunk}` : ""} - ${s.reason}`;
      const go = h("button", "console-diff-rail__go");
      go.type = "button";
      go.textContent = "go [g]";
      go.addEventListener("click", () => acceptSuggestion(s.id));
      const skip = h("button", "console-diff-rail__skip");
      skip.type = "button";
      skip.textContent = "skip [x]";
      skip.addEventListener("click", () => sync({ op: "answer", id: s.id, on: false }));
      item.append(who, what, go, skip);
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
    let n = -1;
    for (let i = 0; i <= rowIndex; i++) {
      const r = state.rows[i];
      if (!r) continue;
      if (r.kind === "file") n = -1;
      else if (r.kind === "hunk") n++;
    }
    return n;
  };

  const step = (dir: 1 | -1, marks: number[]): void => {
    const from = state.cursor >= 0 ? state.cursor : rowAt(state.offsets, scroll.scrollTop);
    const i = dir === 1 ? nextIndexAfter(marks, from) : prevIndexBefore(marks, from);
    if (i !== null) scrollToRow(i);
  };

  // toggleViewed marks the hunk the cursor is in. It is the READER's claim, which is why no
  // agent surface can make it.
  const toggleViewed = (): void => {
    const i = currentHunkRow();
    if (i === null) return;
    const digest = state.digestByRow.get(i);
    if (!digest) return;
    const on = !state.viewed.has(digest);
    if (on) state.viewed.add(digest);
    else state.viewed.delete(digest);
    paint();
    renderToolbar();
    sync({ op: "viewed", digest, on });
  };

  const currentHunkRow = (): number | null => {
    const from = state.cursor >= 0 ? state.cursor : rowAt(state.offsets, scroll.scrollTop);
    let best: number | null = null;
    for (const i of state.hunks) {
      if (i <= from) best = i;
      else break;
    }
    return best ?? state.hunks[0] ?? null;
  };

  // comment opens a one-line composer pinned under the hunk the cursor is in.
  //
  // A prompt() would have been fewer lines and is the wrong shape: it steals focus from the
  // page, cannot show WHICH hunk is being annotated, and gives an agent's reader no way to see
  // the code they are remarking on while they type. The composer sits in the stream for the
  // same reason the comments do.
  const composeComment = (): void => {
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
    sync({ op: "answer", id: target.id, on: true });
    const idx = state.changeset.primary.findIndex((o) => o.file.path === target.path);
    if (idx >= 0) {
      const row = state.fileRows[idx];
      if (row !== undefined) scrollToRow(row);
    }
    scroll.focus();
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
    { id: "diff.viewed.toggle", label: "Diff: mark hunk read", run: toggleViewed, key: "v" },
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
      id: "diff.overview",
      label: "Diff: changeset overview",
      run: toggleOverview,
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
      if (e.key === "Escape" && state.overview) {
        e.preventDefault();
        toggleOverview();
      }
    },
    { signal: controller.signal },
  );

  // --- load -----------------------------------------------------------------

  const showEmpty = (title: string, body: string, offerDemo = false): void => {
    state.phase = "empty";
    root.dataset.phase = "empty";
    emptyTitle.textContent = title;
    emptyBody.textContent = body;
    emptyFooter.hidden = !offerDemo;
  };

  // Entering the showcase in place, NOT by reloading: inside the console a reload would tear
  // down the whole SPA (every tab) instead of this one surface. The fragment is recorded with
  // replaceState so a standalone refresh stays in the demo and the URL reads as a shareable
  // /console/diff/#demo, and no hashchange fires for a sibling pane to react to.
  demoBtn.addEventListener(
    "click",
    () => {
      history.replaceState(null, "", "#demo");
      demo = true;
      void load();
    },
    { signal: controller.signal },
  );

  const load = async (): Promise<void> => {
    // The showcase joins the same pipeline one step in, with the patch and the session the
    // daemon would have returned. Everything below order() is the production path, so what it
    // shows off is the surface itself rather than a rendering of it. No fetch is issued at all,
    // which is what makes /console/diff/#demo work with no daemon, no workspace and offline.
    if (demo) {
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
      showEmpty(
        "No daemon connected",
        "Diff reads the working tree through a local daemon. Start one with `magus server start`.",
        true,
      );
      return;
    }
    let patch: string;
    try {
      const res = await fetchPatch(hp, controller.signal);
      if (res.clean) {
        showEmpty("Nothing to read", "The working tree is clean - every change is committed.");
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
      const sess = await fetchSession(
        hp,
        parsed.map((f) => f.path),
        controller.signal,
      );
      if (disposed) return;
      applySession(sess);
      state.changeset = order(parsed, sess);
      await rebuild();
    } catch {
      // Leave the un-annotated view standing.
    }
  };

  void load();

  return () => {
    disposed = true;
    controller.abort();
    for (const c of COMMANDS) unregisterCommand(c.id);
    host.replaceChildren();
  };
}
