// parse.ts - the wire shape of a changeset, and the mapping from it into the types the Diff
// surface renders. Pure: JSON in, a file/hunk tree out, no DOM and no fetch.
//
// It does NOT parse a patch. It used to, and the daemon parsed the same bytes independently in
// Go, and the two drifted - this side learned Mercurial's headerless dialect and POSIX's
// tab-delimited timestamps, the Go side did not, so `magus diff` reported an empty changeset on
// an hg tree while this surface rendered it fine.
//
// That was never going to stay cosmetic. A hunk's DIGEST is its identity: it is what a read
// receipt is keyed by, and what lets a hunk marked here be seen by the CLI and by an agent. Two
// implementations computing it independently is a shared session that can disagree with itself,
// and the test pinning a digest literal computed in node was the tell. Go computes them now and
// ships them, so there is one reader and nothing to keep in step.

// LineKind is what one row of a hunk is. Context appears on both sides; add and del appear
// on one. `meta` is the "\ No newline at end of file" marker, which belongs to the hunk but
// is not a line of either file and must never be counted as one.
export type LineKind = "context" | "add" | "del" | "meta";

// Span is a half-open [start, end) range of a line's text, in the UTF-16 code units a
// JavaScript string is indexed by.
export interface Span {
  readonly start: number;
  readonly end: number;
}

export interface DiffLine {
  readonly kind: LineKind;
  readonly text: string; // the content, WITHOUT the leading +/-/space marker
  // Line numbers in the old and new files, null where the line does not exist on that side.
  // Pre-computed here rather than derived at render time: a virtualized view renders rows
  // out of order and from arbitrary offsets, so a renderer that counted as it drew would
  // number them wrong the moment it skipped a row.
  readonly oldLine: number | null;
  readonly newLine: number | null;
  // emph is WHICH PART of this line changed, for a line paired with its counterpart across a
  // rewrite. Undefined on most lines, which have nothing to mark.
  //
  // Computed by the daemon, like the digest above it. This surface used to work it out and the
  // terminal viewer worked out the same thing separately, agreeing only by hand-transcribed
  // test vectors - so the same changed line could read as two different changes depending on
  // where you opened it, and nothing would ever have said so.
  readonly emph?: Span;
}

export interface Hunk {
  // digest is the hunk's identity, computed by the daemon over the body EXACTLY as the VCS
  // emitted it. It is what a read receipt is keyed by. This surface must never recompute it:
  // the rows below have had their +/-/space markers stripped, and putting them back does not
  // round-trip - a context line whose producer dropped the trailing space arrives as "" and
  // would be rebuilt as " ", yielding a different digest for the same hunk.
  readonly digest: string;
  readonly header: string; // the raw @@ line, including any trailing section heading
  readonly oldStart: number;
  readonly oldCount: number;
  readonly newStart: number;
  readonly newCount: number;
  readonly lines: readonly DiffLine[];
}

// FileStatus is what happened to the file as a whole. Derived from the git extended headers
// where present; otherwise inferred from the /dev/null convention in the ---/+++ pair.
export type FileStatus = "added" | "deleted" | "modified" | "renamed" | "copied";

export interface DiffFile {
  // path is what the file is called NOW - the new path for a rename, and the old path for a
  // deletion (where there is no new one). It is what a sidebar lists and what an anchor
  // names, so it must never be "/dev/null".
  readonly path: string;
  readonly oldPath: string; // differs from path only for a rename or copy
  readonly status: FileStatus;
  readonly hunks: readonly Hunk[];
  readonly additions: number;
  readonly deletions: number;
  // binary files carry no hunks. Rendering them as an empty diff reads as "nothing changed",
  // which is false, so the flag is explicit and the surface says so.
  readonly binary: boolean;
  // mode changes are a real reviewable event (a script becoming executable) that produces no
  // hunks at all, so it would otherwise render as an empty entry.
  readonly oldMode?: string;
  readonly newMode?: string;
}

// WireRow, WireHunk and WireFile mirror internal/diff's JSON exactly. They are separate from
// the interfaces above on purpose: those are what the renderer wants (readonly, `digest` on the
// hunk, no snake_case), and letting the transport shape leak into them would make every future
// wire change a change to the render tree.
interface WireRow {
  kind: LineKind;
  text: string;
  old_line: number | null;
  new_line: number | null;
  emph?: Span;
}

interface WireHunk {
  header: string;
  digest: string;
  // index and lines are on the wire but unused here. They are what the MCP surface reads - an
  // agent addresses a hunk by index and needs its raw text - and they are declared rather than
  // omitted because TypeScript rejects an object literal carrying a property the type does not
  // know, which is what the generated showcase fixture is. `lines` is the body with its +/-
  // markers intact; `rows` below is the same body parsed, and rendering reads that.
  index: number;
  lines: string[] | null;
  old_start: number;
  old_count: number;
  new_start: number;
  new_count: number;
  rows: WireRow[] | null;
}

export interface WireFile {
  path: string;
  old_path: string;
  status: FileStatus;
  additions: number;
  deletions: number;
  binary: boolean;
  old_mode?: string;
  new_mode?: string;
  hunks: WireHunk[] | null;
}

// fromWire maps the daemon's changeset into the render tree. A missing or empty list yields []
// rather than throwing: a clean tree is a state, not a failure.
//
// Every array is guarded for null because Go marshals a nil slice as `null`, not `[]` - a
// binary file has no hunks and a mode-only change has no rows, so both arrive that way in
// ordinary use rather than as an edge case.
export function fromWire(files: readonly WireFile[] | null | undefined): DiffFile[] {
  if (!files || files.length === 0) return [];
  return files.map((f) => ({
    path: f.path,
    oldPath: f.old_path,
    status: f.status,
    additions: f.additions,
    deletions: f.deletions,
    binary: f.binary,
    ...(f.old_mode === undefined ? {} : { oldMode: f.old_mode }),
    ...(f.new_mode === undefined ? {} : { newMode: f.new_mode }),
    hunks: (f.hunks ?? []).map((h) => ({
      header: h.header,
      digest: h.digest,
      oldStart: h.old_start,
      oldCount: h.old_count,
      newStart: h.new_start,
      newCount: h.new_count,
      lines: (h.rows ?? []).map((r) => ({
        kind: r.kind,
        text: r.text,
        oldLine: r.old_line,
        newLine: r.new_line,
        ...(r.emph === undefined ? {} : { emph: r.emph }),
      })),
    })),
  }));
}

// countLines is the row total a virtualizer needs to size its scroll space: every hunk's
// lines plus one row per hunk header. Computed here so the renderer never walks the tree
// just to measure it.
export function countLines(files: readonly DiffFile[]): number {
  let n = 0;
  for (const f of files) {
    for (const h of f.hunks) n += h.lines.length + 1;
  }
  return n;
}
