// rows.ts - flattens the parsed file/hunk tree into ONE array of fixed-height rows, which is
// what makes the stream virtualizable.
//
// The flattening is the whole performance design. A virtualizer has to answer "which rows are
// on screen" in constant time, and it can only do that if every row is the same height and
// addressable by index. So: no wrapping (a long line scrolls horizontally instead, which is
// what a diff reader wants anyway), one line per row, and the file and hunk headers occupy
// rows of their own rather than being containers that would nest the geometry.
//
// It is pure and separate from the renderer because the split-view pairing below is exactly
// the kind of index arithmetic that is wrong until it is tested.

import type { DiffFile, DiffLine, Hunk } from "./parse";
import type { DiffComment, DiffTouch } from "./session";

export type ViewMode = "unified" | "split";

// commentKey addresses a comment's anchor: a file plus the hunk's index WITHIN that file.
//
// Within-file rather than a global row index, because a global one changes meaning the moment
// the generated group folds or the view mode switches - the same comment would slide onto a
// different hunk. It is also the coordinate the session and an agent speak in, so a comment
// written in the console and one written over MCP land in the same place.
export function commentKey(path: string, hunk: number): string {
  return `${path}\n${hunk}`;
}

// byHunk groups comments by their anchor so buildRows can interleave them in one pass rather
// than scanning the whole list per hunk.
export function byHunk(comments: readonly DiffComment[]): Map<string, DiffComment[]> {
  const out = new Map<string, DiffComment[]>();
  for (const c of comments) {
    const k = commentKey(c.path, c.hunk);
    const at = out.get(k);
    if (at) at.push(c);
    else out.set(k, [c]);
  }
  return out;
}

// Row is one rendered line of the stream. `file` and `hunk` rows are the headings; `line` is
// unified content; `pair` is split content, where either side may be absent because an
// add-only or delete-only run has nothing to sit opposite it.
export type Row =
  | { readonly kind: "file"; readonly file: DiffFile }
  | { readonly kind: "hunk"; readonly file: DiffFile; readonly hunk: Hunk; readonly index: number }
  | { readonly kind: "line"; readonly file: DiffFile; readonly hunk: Hunk; readonly line: DiffLine }
  | { readonly kind: "comment"; readonly file: DiffFile; readonly comment: DiffComment }
  | { readonly kind: "story"; readonly file: DiffFile; readonly touch: DiffTouch }
  | {
      readonly kind: "pair";
      readonly file: DiffFile;
      readonly hunk: Hunk;
      readonly left: DiffLine | null;
      readonly right: DiffLine | null;
    };

// buildRows flattens files into the row array for one view mode.
//
// Comments are interleaved as rows of their own, directly under the hunk they annotate, rather
// than floated beside the stream. That placement is the point: a remark read three inches from
// the code it is about is a remark the reader has to hold in their head, and the whole reason
// this surface exists is to stop asking them to do that. It costs the comment a row in the
// virtualizer's geometry, which is exactly what makes it scroll with the code.
export function buildRows(
  files: readonly DiffFile[],
  mode: ViewMode,
  comments?: Map<string, DiffComment[]>,
  touches?: Map<string, readonly DiffTouch[]>,
): Row[] {
  const rows: Row[] = [];
  for (const file of files) {
    rows.push({ kind: "file", file });
    // The story sits under the FILE heading rather than under a hunk, because that is the
    // granularity the trail records: an agent wrote the file, and what it had read applies to
    // the edit as a whole. Pinning it to one hunk would claim a precision the data lacks.
    for (const touch of touches?.get(file.path) ?? []) {
      rows.push({ kind: "story", file, touch });
    }
    file.hunks.forEach((hunk, index) => {
      rows.push({ kind: "hunk", file, hunk, index });
      for (const c of comments?.get(commentKey(file.path, index)) ?? []) {
        rows.push({ kind: "comment", file, comment: c });
      }
      if (mode === "split") pushSplit(rows, file, hunk);
      else for (const line of hunk.lines) rows.push({ kind: "line", file, hunk, line });
    });
  }
  return rows;
}

// pushSplit zips a hunk into two columns.
//
// The rule that matters: a run of deletions immediately followed by a run of additions is a
// REPLACEMENT, and the two runs must sit side by side so the reader can compare them. Emitting
// each line on its own row as it arrives would stack the old block above the new one - which
// is the unified view, printed in two columns, and useless.
//
// So deletions buffer instead of emitting, and flush against the additions that follow.
function pushSplit(rows: Row[], file: DiffFile, hunk: Hunk): void {
  let dels: DiffLine[] = [];
  let adds: DiffLine[] = [];

  const flush = (): void => {
    // Zip to the LONGER run, so the shorter side pads with nulls rather than truncating.
    // Taking the shorter length here silently drops the tail of an uneven replacement.
    const n = Math.max(dels.length, adds.length);
    for (let i = 0; i < n; i++) {
      rows.push({ kind: "pair", file, hunk, left: dels[i] ?? null, right: adds[i] ?? null });
    }
    dels = [];
    adds = [];
  };

  for (const line of hunk.lines) {
    if (line.kind === "del") {
      // An addition already buffered means the previous replacement is over and a new run
      // has started; flush before buffering, or the two get zipped together.
      if (adds.length > 0) flush();
      dels.push(line);
      continue;
    }
    if (line.kind === "add") {
      adds.push(line);
      continue;
    }
    // Context and meta belong to both sides, so they close any pending replacement and then
    // occupy one full-width row.
    flush();
    rows.push({ kind: "pair", file, hunk, left: line, right: line });
  }
  flush();
}

// hunkRowIndexes lists the row index of every hunk heading, in order. It is what `[` and `]`
// step through, computed once per build rather than by scanning the array on each keypress.
export function hunkRowIndexes(rows: readonly Row[]): number[] {
  const out: number[] = [];
  rows.forEach((row, i) => {
    if (row.kind === "hunk") out.push(i);
  });
  return out;
}

// hunksRead counts how many of the CURRENT hunks (a stream after the generated fold, a filter, or
// a rebuild) are marked viewed. It is the intersection of hunks with viewed, not viewed.size: that
// count is the whole session's marks, which is not pruned when a hunk leaves the stream (the
// generated group folds, a filter narrows), so it can read higher than the total it is meant to be
// a fraction of. A hunk whose digest is not yet known - not yet scrolled into view, so not yet
// primed - counts as unread rather than matched; that is the direction a wrong guess should err in,
// since the alternative is claiming more read than the stream actually holds.
export function hunksRead(
  hunks: readonly number[],
  digestByRow: ReadonlyMap<number, string>,
  viewed: ReadonlySet<string>,
): number {
  let read = 0;
  for (const row of hunks) {
    const digest = digestByRow.get(row);
    if (digest && viewed.has(digest)) read++;
  }
  return read;
}

// ActiveFileTarget is what the sidebar should highlight as "here" for a scroll position - the
// primary file's own entry, the generated group's fold toggle (a real file, just not one the
// index lists individually), or nothing (a row before the first file heading).
export type ActiveFileTarget = "file" | "generated" | "none";

// activeFileTarget decides that highlight. The sidebar lists PRIMARY files only, so a scroll
// position inside a generated file resolves to a real row (fileRow >= 0) with no sidebar entry -
// the caller used to read that as "no target" and simply cleared the last highlight, which left
// the reader with no position indicator at all the moment they scrolled somewhere the index
// cannot show individually. The generated group's own toggle is the honest stand-in for that case.
export function activeFileTarget(fileRow: number, hasSidebarEntry: boolean): ActiveFileTarget {
  if (hasSidebarEntry) return "file";
  if (fileRow >= 0) return "generated";
  return "none";
}

// fileRowIndexes lists the row index of every file heading, in order - what the sidebar jumps
// to, indexed the same way as the files array it was built from.
export function fileRowIndexes(rows: readonly Row[]): number[] {
  const out: number[] = [];
  rows.forEach((row, i) => {
    if (row.kind === "file") out.push(i);
  });
  return out;
}

// nextIndexAfter returns the first entry of `sorted` strictly greater than `from`, or the last
// entry when there is none - so `]` at the end of the stream holds still instead of wrapping
// to the top, which reads as a scroll bug rather than as navigation.
export function nextIndexAfter(sorted: readonly number[], from: number): number | null {
  const last = sorted[sorted.length - 1];
  if (last === undefined) return null;
  let lo = 0;
  let hi = sorted.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if ((sorted[mid] ?? Number.POSITIVE_INFINITY) <= from) lo = mid + 1;
    else hi = mid;
  }
  return sorted[lo] ?? last;
}

// prevIndexBefore is nextIndexAfter's mirror, clamping at the first entry.
export function prevIndexBefore(sorted: readonly number[], from: number): number | null {
  const first = sorted[0];
  if (first === undefined) return null;
  let lo = 0;
  let hi = sorted.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if ((sorted[mid] ?? Number.NEGATIVE_INFINITY) < from) lo = mid + 1;
    else hi = mid;
  }
  return sorted[lo - 1] ?? first;
}

// fileOfRow maps every row index to the index of the file row that governs it, so a scroll
// position answers "which file am I in" in one lookup. -1 for any row before the first file
// header, which the changeset's leading rows can be.
export function fileOfRow(rows: readonly Row[]): number[] {
  const out = new Array<number>(rows.length);
  let current = -1;
  for (let i = 0; i < rows.length; i++) {
    if ((rows[i] as Row).kind === "file") current = i;
    out[i] = current;
  }
  return out;
}

// ROW_HEIGHT and FILE_ROW_HEIGHT MUST equal the heights in diff.css. The virtualizer computes
// positions from them rather than measuring, so a mismatch shows as rows drifting out of the
// viewport while scrolling.
//
// Two heights, because the file header is not a line of code: it carries an M/A/D/R badge and
// the risk chips. In a 24px row those overflowed their own row by 8px and collided with the line
// above. Padding cannot buy the room - box-sizing is border-box, so padding eats the height
// instead of adding to it. Raising the SHARED height was the wrong answer too: it pays for the
// header's chips out of every code line, and fitting code on the screen is why this virtualizes.
export const ROW_HEIGHT = 24;
export const FILE_ROW_HEIGHT = 40;

// heightOf is the single source of truth for a row's height; rowOffsets is derived from it.
export function heightOf(row: Row): number {
  return row.kind === "file" ? FILE_ROW_HEIGHT : ROW_HEIGHT;
}

// rowOffsets is the running y of every row, plus a final entry holding the total, so offsets[i]
// is row i's top edge and offsets[n] is the full scroll height. Mixed heights mean a position is
// no longer index * ROW_HEIGHT, and summing per paint would be O(rows) on every scroll frame.
export function rowOffsets(rows: readonly Row[]): number[] {
  const offs = new Array<number>(rows.length + 1);
  let y = 0;
  for (let i = 0; i < rows.length; i++) {
    offs[i] = y;
    y += heightOf(rows[i] as Row);
  }
  offs[rows.length] = y;
  return offs;
}

// rowAt is the inverse: the index of the row containing y. Binary search, because it runs on
// every scroll frame. Returns the LAST row whose top is <= y, clamped into range - so a y past
// the end answers with the final row rather than running off the array.
export function rowAt(offsets: readonly number[], y: number): number {
  let lo = 0;
  let hi = offsets.length - 2;
  if (hi < 0) return 0;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if ((offsets[mid] as number) <= y) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

// storyText is the sentence a story row renders (main.ts renderRow reads it too, so the two
// cannot drift the way a second copy of this composition would). "wrote this after reading X,
// Y" is the whole point of the row - it is what the agent was looking at when it decided to
// write this, which no forge can say - and with no reads recorded the sentence stops at the
// author rather than inventing a reason.
export function storyText(touch: DiffTouch): string {
  const read = touch.read ?? [];
  return read.length > 0 ? `wrote this after reading ${read.slice(0, 4).join(", ")}` : "wrote this";
}

// LINE_PREFIX_CHARS is what sits before a unified code line's own text: the old and new
// line-number gutters (5ch each) and the +/- marker (2ch). main.ts adds it to maxLineChars to
// get the row-width floor described there.
export const LINE_PREFIX_CHARS = 12;

// TAB_SIZE mirrors the browser's default CSS tab-size, which diff.css never overrides. A tab
// does not cost one character of width the way every other character does - it advances to the
// next multiple of TAB_SIZE - so a single leading tab in a code line (column 0 to column 8)
// costs as much rendered width as seven ordinary characters. columnWidth below has to model
// that, or a short, deeply-indented line (several leading tabs, little text) can render WIDER
// than a longer but tab-free line of the same raw .length, becoming a new widest row that the
// character-count floor never accounted for.
const TAB_SIZE = 8;

// columnWidth is a string's rendered width in character-columns once tabs expand to their next
// tab stop, which is what a monospace `white-space: pre` line actually occupies - `.length`
// alone undercounts every tab.
function columnWidth(text: string): number {
  let col = 0;
  for (const ch of text) col += ch === "\t" ? TAB_SIZE - (col % TAB_SIZE) : 1;
  return col;
}

// maxLineChars is the rendered width, in character-columns, of the longest line of text the
// unified view renders - a code line, but also a hunk header, a comment, or a story, any of
// which can outrun the longest code line. The renderer turns it into a per-row min-width floor
// (diff.css), because a virtualized row otherwise sizes to its OWN text and stops there: a
// row shorter than whichever row is actually widest had its background end at its own last
// character instead of continuing to the scroll width that widest row established, so
// scrolling past it exposed untinted background. Measured in character-columns rather than
// pixels because the row is set in a monospace font, where a column count converts to a CSS
// `ch` width with no DOM measurement needed.
export function maxLineChars(rows: readonly Row[]): number {
  let max = 0;
  const bump = (text: string): void => {
    const width = columnWidth(text);
    if (width > max) max = width;
  };
  for (const row of rows) {
    if (row.kind === "line") bump(row.line.text);
    else if (row.kind === "hunk") bump(row.hunk.header);
    else if (row.kind === "comment") bump(row.comment.body);
    else if (row.kind === "story") bump(storyText(row.touch));
  }
  return max;
}
