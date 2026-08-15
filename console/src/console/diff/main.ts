// main.ts - the console's Review surface.
//
// A magus review is not a text diff. The daemon already knows which changed files are
// generated, how widely each changed symbol is referenced, whether any of it is public API,
// and what coverage was observed - so this surface spends the reader's attention in
// CONSEQUENCE order rather than alphabetical order, and folds away the files a target
// rewrote. On magus's own tree that routinely halves the number of files anyone has to read.
//
// Three things it is built around:
//
//  1. VIRTUALIZED. The scroll space is a spacer sized to rows x ROW_HEIGHT and only the rows
//     intersecting the viewport exist as elements, so a ten-thousand-line diff costs what a
//     hundred-line one does. Fixed row height and no wrapping follow from that.
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

import { parsePatch, type DiffFile, type FileStatus } from "./parse";
import {
  buildRows,
  byHunk,
  hunkRowIndexes,
  fileRowIndexes,
  nextIndexAfter,
  prevIndexBefore,
  type Row,
  type ViewMode,
} from "./rows";
import { order, visibleFiles, stats, riskChips, type OrderedChangeset } from "./order";
import {
  fetchPatch,
  fetchSession,
  mutate,
  hunkDigest,
  HttpError,
  type ReviewSession,
  type ReviewFile,
} from "./session";
import { registerCommand, unregisterCommand } from "../commands";
import { resolveDaemonHost, parseHash, adoptDaemonOrigin } from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";

// Must equal the row height in diff.css. The virtualizer computes positions from it rather
// than measuring, so a mismatch shows as rows drifting out of the viewport while scrolling.
const ROW_HEIGHT = 20;
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
  hunks: number[];
  fileRows: number[];
  mode: ViewMode;
  cursor: number;
  session: ReviewSession | null;
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

function markerFor(kind: string): string {
  return kind === "add" ? "+" : kind === "del" ? "-" : " ";
}

function gutter(n: number | null): HTMLElement {
  // A non-breaking space, not "": an empty text node collapses and the gutters would shift
  // width row to row.
  return h("span", "console-diff-row__gutter", n === null ? " " : String(n));
}

export function activate(host: HTMLElement): () => void {
  const controller = new AbortController();
  let disposed = false;

  const state: State = {
    changeset: { primary: [], generated: [] },
    files: [],
    rows: [],
    hunks: [],
    fileRows: [],
    mode: modeCell.get() ?? "unified",
    cursor: -1,
    session: null,
    viewed: new Set(),
    digestByRow: new Map(),
    showGenerated: false,
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

  const scroll = h("div", "console-diff-scroll");
  scroll.tabIndex = 0;
  const spacer = h("div", "console-diff-spacer");
  const windowEl = h("div", "console-diff-window");
  spacer.append(windowEl);
  scroll.append(spacer);

  const overview = h("div", "console-diff-overview");
  const empty = h("div", "pf-v6-c-empty-state console-diff-empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "Loading");
  const emptyBodyWrap = h("div", "pf-v6-c-empty-state__body");
  const emptyBody = h("p", undefined, "Reading the working tree.");
  emptyBodyWrap.append(emptyBody);
  emptyContent.append(emptyTitle, emptyBodyWrap);
  empty.append(emptyContent);

  main.append(toolbar, rail, scroll, overview, empty);
  root.append(sidebar, main);
  host.append(root);
  root.dataset.phase = "loading";

  // --- rendering ------------------------------------------------------------

  const annotationFor = (path: string): ReviewFile | undefined => {
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
    for (const c of riskChips(annotationFor(file.path))) {
      el.append(label(c.text, TONE_CLASS[c.tone], c.title));
    }
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
      el.append(
        gutter(row.line.oldLine),
        gutter(row.line.newLine),
        h("span", "console-diff-row__marker", markerFor(row.line.kind)),
        h("span", "console-diff-row__text", row.line.text),
      );
      return el;
    }
    const el = h("div", "console-diff-row console-diff-row--pair");
    el.append(side(row.left, "left"), side(row.right, "right"));
    return el;
  };

  const side = (
    line: { kind: string; text: string; oldLine: number | null; newLine: number | null } | null,
    which: "left" | "right",
  ): HTMLElement => {
    const cell = h("div", "console-diff-row__side");
    cell.dataset.side = which;
    if (!line) {
      cell.dataset.kind = "empty";
      cell.append(gutter(null), h("span", "console-diff-row__text", " "));
      return cell;
    }
    cell.dataset.kind = line.kind;
    cell.append(
      gutter(which === "left" ? line.oldLine : line.newLine),
      h("span", "console-diff-row__marker", markerFor(line.kind)),
      h("span", "console-diff-row__text", line.text || " "),
    );
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
    const first = Math.max(0, Math.floor(top / ROW_HEIGHT) - OVERSCAN);
    const last = Math.min(total, Math.ceil((top + height) / ROW_HEIGHT) + OVERSCAN);

    const frag = document.createDocumentFragment();
    for (let i = first; i < last; i++) {
      const row = state.rows[i];
      if (!row) continue;
      const el = renderRow(row, i);
      el.dataset.row = String(i);
      if (i === state.cursor) el.dataset.cursor = "";
      frag.append(el);
    }
    windowEl.style.transform = `translateY(${first * ROW_HEIGHT}px)`;
    windowEl.replaceChildren(frag);
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
    const hp = host_();
    if (!hp) return;
    void mutate(hp, op, controller.signal).then((s) => {
      if (!disposed && s) applySession(s);
    });
  };

  // applySession takes the daemon's copy as authoritative and re-lays the stream, because a
  // comment - the human's or an agent's - is a ROW, so it changes the scroll geometry. Only
  // repainting would leave the new remark invisible until the next unrelated rebuild.
  const applySession = (s: ReviewSession, relayout = true): void => {
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
    state.rows = buildRows(state.files, state.mode, byHunk(state.session?.comments ?? []));
    state.hunks = hunkRowIndexes(state.rows);
    state.fileRows = fileRowIndexes(state.rows);
    spacer.style.height = `${state.rows.length * ROW_HEIGHT}px`;

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

  const renderToolbar = (): void => {
    const s = stats(state.changeset);
    const chips: HTMLElement[] = [
      label(
        `${s.files} ${s.files === 1 ? "file" : "files"}`,
        undefined,
        "Files worth reading; generated output is excluded",
      ),
      label(`+${s.additions}`, "pf-m-green"),
      label(`-${s.deletions}`, "pf-m-red"),
    ];
    if (s.generated > 0) {
      const g = h("button", "console-diff-toolbar__fold");
      g.type = "button";
      g.textContent = state.showGenerated
        ? `hide ${s.generated} generated`
        : `${s.generated} generated folded`;
      g.title =
        "Declared target outputs. Reviewing their diff is reading a machine's restatement of a change made elsewhere - read the source change instead. Press . to toggle.";
      g.addEventListener("click", () => void toggleGenerated());
      chips.push(g);
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
      const g = h("button", "console-diff-sidebar__group");
      g.type = "button";
      g.textContent = `${state.changeset.generated.length} generated`;
      g.title = "Declared target outputs, folded. Press . to expand.";
      g.addEventListener("click", () => void toggleGenerated());
      frag.append(g);
    }
    sidebar.replaceChildren(frag);
  };

  // The suggestion rail: an agent asking for attention. It renders as a peripheral affordance
  // the reader accepts with one key and NEVER as a scroll - see types.ReviewSuggestion.
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
    const seeds = state.session?.review?.seed_projects ?? [];
    const affected = state.session?.review?.affected_projects ?? [];
    if (affected.length > 0) {
      box.append(
        line(
          "projects",
          `${seeds.length} edited, ${affected.length} rebuild`,
          "The gap is downstream: projects that rebuild because they sit below one you edited",
        ),
      );
    }
    for (const n of state.session?.review?.notes ?? []) {
      const note = h("p", "console-diff-overview__note");
      note.textContent = n;
      box.append(note);
    }
    const top = state.changeset.primary.slice(0, 5);
    if (top.length > 0) {
      box.append(h("h3", "console-diff-overview__subtitle", "Read these first"));
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
    scroll.scrollTo({ top: i * ROW_HEIGHT, behavior: "smooth" });
    const row = state.rows[i];
    if (row && row.kind !== "file") {
      const path = row.kind === "hunk" ? row.hunk : null;
      void path;
    }
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
    const from = state.cursor >= 0 ? state.cursor : Math.floor(scroll.scrollTop / ROW_HEIGHT);
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
    const from = state.cursor >= 0 ? state.cursor : Math.floor(scroll.scrollTop / ROW_HEIGHT);
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
  // Registered so every action appears in the command bar and the Actions surface and can be
  // rebound, which a private keydown table would not give. The single-letter keys stay bound
  // on the scroll container below rather than as global chords: a bare "v" must not fire
  // while someone is typing in another surface.
  const COMMANDS: { id: string; label: string; run: () => void; key?: string }[] = [
    {
      id: "review.hunk.next",
      label: "Review: next hunk",
      run: () => step(1, state.hunks),
      key: "]",
    },
    {
      id: "review.hunk.prev",
      label: "Review: previous hunk",
      run: () => step(-1, state.hunks),
      key: "[",
    },
    {
      id: "review.file.next",
      label: "Review: next file",
      run: () => step(1, state.fileRows),
      key: "}",
    },
    {
      id: "review.file.prev",
      label: "Review: previous file",
      run: () => step(-1, state.fileRows),
      key: "{",
    },
    { id: "review.viewed.toggle", label: "Review: mark hunk read", run: toggleViewed, key: "v" },
    {
      id: "review.generated.toggle",
      label: "Review: fold or unfold generated files",
      run: () => void toggleGenerated(),
      key: ".",
    },
    {
      id: "review.view.unified",
      label: "Review: unified view",
      run: () => void setMode("unified"),
      key: "1",
    },
    {
      id: "review.view.split",
      label: "Review: split view",
      run: () => void setMode("split"),
      key: "2",
    },
    {
      id: "review.view.toggle",
      label: "Review: toggle split and unified",
      run: () => void setMode(state.mode === "split" ? "unified" : "split"),
      key: "0",
    },
    {
      id: "review.suggestion.accept",
      label: "Review: go to the agent's suggestion",
      run: () => acceptSuggestion(),
      key: "g",
    },
    {
      id: "review.suggestion.skip",
      label: "Review: skip the agent's suggestion",
      run: () => {
        const p = (state.session?.suggestions ?? []).find((s) => !s.accepted && !s.declined);
        if (p) sync({ op: "answer", id: p.id, on: false });
      },
      key: "x",
    },
    {
      id: "review.comment",
      label: "Review: comment on this hunk",
      run: composeComment,
      key: "c",
    },
    {
      id: "review.comment.resolve",
      label: "Review: resolve the comment here",
      run: resolveHere,
      key: "r",
    },
    {
      id: "review.overview",
      label: "Review: changeset overview",
      run: toggleOverview,
      key: "Escape",
    },
  ];
  for (const c of COMMANDS)
    registerCommand({ id: c.id, label: c.label, group: "Review", run: c.run });

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

  const showEmpty = (title: string, body: string): void => {
    state.phase = "empty";
    root.dataset.phase = "empty";
    emptyTitle.textContent = title;
    emptyBody.textContent = body;
  };

  const load = async (): Promise<void> => {
    const hp = host_();
    if (!hp) {
      showEmpty(
        "No daemon connected",
        "Review reads the working tree through a local daemon. Start one with `magus server start`.",
      );
      return;
    }
    let patch: string;
    try {
      const res = await fetchPatch(hp, controller.signal);
      if (res.clean) {
        showEmpty("Nothing to review", "The working tree is clean - every change is committed.");
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
      showEmpty("Nothing to review", "The daemon returned a patch this reader could not parse.");
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
