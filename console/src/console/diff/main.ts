// main.ts - the console's Review surface: the working tree's uncommitted changes as one
// continuous, virtualized hunk stream.
//
// The whole changeset reads top to bottom in sidebar order. There is no per-file view to flip
// between, because flipping is what makes a large review feel large - the reader loses their
// place at every boundary and has to re-find it.
//
// VIRTUALIZED, and that is the feature rather than an optimization. A forge that renders every
// row of a 5000-line diff into the DOM at once is slow in a way no amount of tuning fixes,
// because the cost is the row count. Here the scroll space is a spacer sized to
// rows x ROW_HEIGHT and only the rows intersecting the viewport exist as elements, so a
// ten-thousand-line diff costs the same as a hundred-line one. Everything else in this file is
// downstream of that: fixed row height, no wrapping (long lines scroll sideways, which is what
// a diff reader wants anyway), and a flat row array from rows.ts rather than nested containers
// whose geometry would have to be measured.
//
// READ ONLY for now, deliberately. Comments are a separate store with its own write path and
// its own anchors - see the plan. This surface is the reading half, and it is useful alone.

import { parsePatch, type DiffFile, type DiffLine, type FileStatus } from "./parse";
import {
  buildRows,
  hunkRowIndexes,
  fileRowIndexes,
  nextIndexAfter,
  prevIndexBefore,
  type Row,
  type ViewMode,
} from "./rows";
import { resolveDaemonHost, authHeaders, parseHash } from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";

// ROW_HEIGHT must match the CSS line-height for a diff row exactly. The virtualizer computes
// positions from it rather than measuring, so a mismatch shows up as rows drifting out of the
// viewport as you scroll. It is asserted against the rendered height at runtime in dev builds
// via the mismatch guard in mount().
const ROW_HEIGHT = 20;

// OVERSCAN is how many rows above and below the viewport are rendered anyway, so a fast scroll
// does not show blank space while the next frame computes. Three screens' worth is generous;
// the cost is bounded and constant.
const OVERSCAN = 24;

// The same key the dashboard and notes remember their daemon under, so opening Review after
// connecting elsewhere resumes the same loopback host.
const daemonCell = persisted<string | null>("dashboard-daemon", null);
const modeCell = persisted<ViewMode>("diff-view-mode", "unified");

interface Refs {
  root: HTMLElement;
  sidebar: HTMLElement;
  scroll: HTMLElement;
  spacer: HTMLElement;
  window: HTMLElement;
  empty: HTMLElement;
  emptyTitle: HTMLElement;
  emptyBody: HTMLElement;
  toolbar: HTMLElement;
  stats: HTMLElement;
}

interface State {
  files: DiffFile[];
  rows: Row[];
  hunks: number[];
  fileRows: number[];
  mode: ViewMode;
  // cursor is the row index `[` and `]` step from. Tracked separately from scrollTop so that
  // repeated presses advance hunk by hunk rather than re-resolving from a position the smooth
  // scroll has not finished animating to.
  cursor: number;
}

// label builds a PF Label. Text goes through textContent by construction (h sets text, never
// innerHTML), which is what keeps a path or a diff line from being trusted markup - a patch is
// attacker-influenced input whenever it came from a branch someone else wrote.
function label(text: string, modifier?: string, title?: string): HTMLElement {
  const el = h("span", "pf-v6-c-label" + (modifier ? " " + modifier : ""));
  el.append(h("span", "pf-v6-c-label__content", text));
  if (title) el.title = title;
  return el;
}

// STATUS_COPY maps a file status to its sidebar label and colour. A rename is blue rather than
// green or red: nothing was added or removed, and colouring it either way misreports it.
// Keyed by FileStatus rather than by string, so every status is covered by construction and
// the lookup needs no fallback - a new status becomes a compile error here instead of
// silently rendering as "modified".
const STATUS_COPY: Record<FileStatus, { short: string; modifier: string }> = {
  added: { short: "A", modifier: "pf-m-green" },
  deleted: { short: "D", modifier: "pf-m-red" },
  modified: { short: "M", modifier: "pf-m-grey" },
  renamed: { short: "R", modifier: "pf-m-blue" },
  copied: { short: "C", modifier: "pf-m-blue" },
};

function buildScaffold(host: HTMLElement): Refs {
  const root = h("div", "console-diff-layout");

  // Sidebar: the file list, in patch order. Order matters - it is the same order the stream
  // scrolls in, so the sidebar doubles as a map rather than being a second sorted index.
  const sidebar = h("nav", "console-diff-sidebar");
  sidebar.setAttribute("aria-label", "Changed files");

  const main = h("div", "console-diff-main");

  const toolbar = h("div", "pf-v6-c-toolbar console-diff-toolbar");
  const toolbarContent = h("div", "pf-v6-c-toolbar__content");
  const toolbarItem = h("div", "pf-v6-c-toolbar__content-section");
  const stats = h("div", "console-diff-toolbar__stats");
  toolbarItem.append(stats);
  toolbarContent.append(toolbarItem);
  toolbar.append(toolbarContent);

  const scroll = h("div", "console-diff-scroll");
  scroll.tabIndex = 0; // focusable so [ and ] reach it without a document-wide listener
  const spacer = h("div", "console-diff-spacer");
  const windowEl = h("div", "console-diff-window");
  spacer.append(windowEl);
  scroll.append(spacer);

  const empty = h("div", "pf-v6-c-empty-state console-diff-empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "No daemon connected");
  const emptyBodyWrap = h("div", "pf-v6-c-empty-state__body");
  const emptyBody = h("p");
  emptyBody.textContent = "Review shows what you have changed but not yet committed.";
  emptyBodyWrap.append(emptyBody);
  emptyContent.append(emptyTitle, emptyBodyWrap);
  empty.append(emptyContent);

  main.append(toolbar, scroll, empty);
  root.append(sidebar, main);
  host.append(root);
  return {
    root,
    sidebar,
    scroll,
    spacer,
    window: windowEl,
    empty,
    emptyTitle,
    emptyBody,
    toolbar,
    stats,
  };
}

// renderRow builds one row element. Called only for rows in (or near) the viewport, so its
// cost is bounded by screen height rather than by diff size.
function renderRow(row: Row): HTMLElement {
  if (row.kind === "file") return renderFileRow(row.file);
  if (row.kind === "hunk") {
    const el = h("div", "console-diff-row console-diff-row--hunk");
    el.append(h("span", "console-diff-row__text", row.hunk.header));
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
  // pair (split). The row carries its own shape, so the view mode never has to be consulted
  // here - buildRows already decided which kind to emit.
  const el = h("div", "console-diff-row console-diff-row--pair");
  el.append(side(row.left, "left"), side(row.right, "right"));
  return el;
}

function markerFor(kind: string): string {
  if (kind === "add") return "+";
  if (kind === "del") return "-";
  return " ";
}

function gutter(n: number | null): HTMLElement {
  // A non-breaking space, not "", so an empty gutter still occupies its column: an empty text
  // node collapses and the two gutters would shift width row to row.
  const el = h("span", "console-diff-row__gutter", n === null ? " " : String(n));
  return el;
}

// side renders one column of a split row. A null side is an empty CELL, not an absent one -
// the two columns must stay aligned, so the placeholder carries the same structure.
function side(
  line: { kind: string; text: string; oldLine: number | null; newLine: number | null } | null,
  which: "left" | "right",
): HTMLElement {
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
}

function renderFileRow(file: DiffFile): HTMLElement {
  const el = h("div", "console-diff-row console-diff-row--file");
  const status = STATUS_COPY[file.status];
  el.append(label(status.short, status.modifier, file.status));

  const name = h("span", "console-diff-row__path");
  // A rename shows both names: "what is this file now" and "what was it" are different
  // questions and a reader looking for the old path must not have to guess.
  name.textContent =
    file.status === "renamed" || file.status === "copied"
      ? file.oldPath + " -> " + file.path
      : file.path;
  el.append(name);

  if (file.binary) el.append(label("binary", "pf-m-grey", "No text diff to show"));
  if (!file.binary && file.oldMode && file.newMode && file.oldMode !== file.newMode) {
    el.append(label(file.oldMode + " -> " + file.newMode, "pf-m-orange", "Mode changed"));
  }
  if (file.additions > 0) el.append(label("+" + file.additions, "pf-m-green"));
  if (file.deletions > 0) el.append(label("-" + file.deletions, "pf-m-red"));
  return el;
}

export function activate(host: HTMLElement): () => void {
  const refs = buildScaffold(host);
  const state: State = {
    files: [],
    rows: [],
    hunks: [],
    fileRows: [],
    mode: modeCell.get() ?? "unified",
    cursor: -1,
  };

  let disposed = false;
  const controller = new AbortController();

  // paint renders exactly the rows intersecting the viewport (plus OVERSCAN). It is the hot
  // path: called on every scroll frame, so it allocates one fragment and does no measurement.
  const paint = (): void => {
    const total = state.rows.length;
    if (total === 0) {
      refs.window.replaceChildren();
      return;
    }
    const top = refs.scroll.scrollTop;
    const height = refs.scroll.clientHeight || 1;
    const first = Math.max(0, Math.floor(top / ROW_HEIGHT) - OVERSCAN);
    const last = Math.min(total, Math.ceil((top + height) / ROW_HEIGHT) + OVERSCAN);

    const frag = document.createDocumentFragment();
    for (let i = first; i < last; i++) {
      const row = state.rows[i];
      if (!row) continue;
      const el = renderRow(row);
      el.dataset.row = String(i);
      frag.append(el);
    }
    // The window is absolutely positioned inside the spacer and translated to the first
    // rendered row, so the browser scrolls the full height while the DOM holds one screen.
    refs.window.style.transform = "translateY(" + first * ROW_HEIGHT + "px)";
    refs.window.replaceChildren(frag);
  };

  let ticking = false;
  const onScroll = (): void => {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(() => {
      ticking = false;
      if (!disposed) paint();
    });
  };
  refs.scroll.addEventListener("scroll", onScroll, { passive: true, signal: controller.signal });

  const scrollToRow = (i: number): void => {
    state.cursor = i;
    refs.scroll.scrollTo({ top: i * ROW_HEIGHT, behavior: "smooth" });
  };

  // rebuild recomputes the row model for the current mode and repaints from the top.
  const rebuild = (): void => {
    state.rows = buildRows(state.files, state.mode);
    state.hunks = hunkRowIndexes(state.rows);
    state.fileRows = fileRowIndexes(state.rows);
    refs.spacer.style.height = state.rows.length * ROW_HEIGHT + "px";
    paint();
  };

  const onKey = (e: KeyboardEvent): void => {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    // The cursor is resolved from scroll position only when it has not been set, so repeated
    // presses step rather than re-anchoring to wherever a smooth scroll currently is.
    const from = state.cursor >= 0 ? state.cursor : Math.floor(refs.scroll.scrollTop / ROW_HEIGHT);
    if (e.key === "]") {
      const i = nextIndexAfter(state.hunks, from);
      if (i !== null) scrollToRow(i);
      e.preventDefault();
      return;
    }
    if (e.key === "[") {
      const i = prevIndexBefore(state.hunks, from);
      if (i !== null) scrollToRow(i);
      e.preventDefault();
      return;
    }
    // 1 unified, 2 split, 0 toggle - the same vocabulary hunk uses, so muscle memory carries.
    if (e.key === "1" || e.key === "2" || e.key === "0") {
      const next: ViewMode =
        e.key === "1"
          ? "unified"
          : e.key === "2"
            ? "split"
            : state.mode === "split"
              ? "unified"
              : "split";
      if (next !== state.mode) {
        state.mode = next;
        modeCell.set(next);
        rebuild();
        renderToolbar();
      }
      e.preventDefault();
    }
  };
  refs.scroll.addEventListener("keydown", onKey, { signal: controller.signal });

  const renderToolbar = (): void => {
    const adds = state.files.reduce((n, f) => n + f.additions, 0);
    const dels = state.files.reduce((n, f) => n + f.deletions, 0);
    refs.stats.replaceChildren(
      label(state.files.length + (state.files.length === 1 ? " file" : " files")),
      label("+" + adds, "pf-m-green"),
      label("-" + dels, "pf-m-red"),
      label(
        state.mode === "split" ? "split" : "unified",
        "pf-m-blue",
        "1 unified, 2 split, 0 toggle",
      ),
      label(
        state.hunks.length + (state.hunks.length === 1 ? " hunk" : " hunks"),
        undefined,
        "[ and ] step between hunks",
      ),
    );
  };

  const renderSidebar = (): void => {
    const frag = document.createDocumentFragment();
    state.files.forEach((f, i) => {
      const item = h("button", "console-diff-sidebar__item");
      item.type = "button";
      const status = STATUS_COPY[f.status];
      item.append(label(status.short, status.modifier, f.status));
      const name = h("span", "console-diff-sidebar__path");
      name.textContent = f.path;
      name.title = f.path;
      item.append(name);
      const counts = h("span", "console-diff-sidebar__counts");
      counts.textContent =
        (f.additions ? "+" + f.additions : "") + (f.deletions ? " -" + f.deletions : "");
      item.append(counts);
      item.addEventListener("click", () => {
        const row = state.fileRows[i];
        if (row !== undefined) scrollToRow(row);
        refs.scroll.focus();
      });
      frag.append(item);
    });
    refs.sidebar.replaceChildren(frag);
  };

  const showEmpty = (title: string, body: string): void => {
    refs.emptyTitle.textContent = title;
    refs.emptyBody.textContent = body;
    refs.root.dataset.empty = "";
  };

  const load = async (): Promise<void> => {
    const host = resolveDaemonHost(parseHash()) ?? daemonCell.get();
    if (!host) {
      showEmpty(
        "No daemon connected",
        "Review reads the working tree through a local daemon. Start one with `magus server start`.",
      );
      return;
    }
    try {
      const res = await fetch("http://" + host + "/api/v1/diff", {
        headers: authHeaders(),
        signal: controller.signal,
      });
      if (!res.ok) {
        showEmpty(
          res.status === 503 ? "No workspace" : "Could not read the diff",
          res.status === 503
            ? "The daemon is running but has not opened a workspace yet."
            : "The daemon answered " + res.status + ".",
        );
        return;
      }
      const body = (await res.json()) as { patch: string; clean: boolean };
      if (body.clean) {
        // A clean tree is a STATE worth naming, not an error and not an empty list.
        showEmpty("Nothing to review", "The working tree is clean - every change is committed.");
        return;
      }
      state.files = parsePatch(body.patch);
      if (state.files.length === 0) {
        showEmpty("Nothing to review", "The daemon returned a patch this reader could not parse.");
        return;
      }
      delete refs.root.dataset.empty;
      rebuild();
      renderSidebar();
      renderToolbar();
      refs.scroll.focus();
    } catch (e) {
      if (disposed) return;
      showEmpty("Could not reach the daemon", String(e));
    }
  };

  void load();

  return () => {
    disposed = true;
    controller.abort();
    host.replaceChildren();
  };
}
