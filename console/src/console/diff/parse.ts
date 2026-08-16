// parse.ts - a unified-diff reader. Pure: text in, a file/hunk tree out, no DOM and no
// fetch, so it is unit-testable and reusable.
//
// Reusable is the point rather than a nicety. A unified patch is the one interchange format
// every backend and every forge already emits, so this same reader serves the working-tree
// patch the daemon produces today AND a pull-request patch fetched from a provider later.
// Parsing on the Go side would have forked that in two and made the provider case the odd
// one out.
//
// It reads GIT's extended unified format, which is a superset of the POSIX one: the
// `diff --git` line and its `old mode`/`new file`/`rename from`/`index` headers carry the
// facts a `---`/`+++` pair alone cannot express - a rename, a pure mode change, a binary
// file. hg and jj emit their own header lines around the same unified body; unrecognized
// header lines are skipped rather than rejected, so those patches parse to the same shape
// with the git-only extras left unset.

// LineKind is what one row of a hunk is. Context appears on both sides; add and del appear
// on one. `meta` is the "\ No newline at end of file" marker, which belongs to the hunk but
// is not a line of either file and must never be counted as one.
export type LineKind = "context" | "add" | "del" | "meta";

export interface DiffLine {
  readonly kind: LineKind;
  readonly text: string; // the content, WITHOUT the leading +/-/space marker
  // Line numbers in the old and new files, null where the line does not exist on that side.
  // Pre-computed here rather than derived at render time: a virtualized view renders rows
  // out of order and from arbitrary offsets, so a renderer that counted as it drew would
  // number them wrong the moment it skipped a row.
  readonly oldLine: number | null;
  readonly newLine: number | null;
}

export interface Hunk {
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

// HUNK_HEADER captures the four numbers of an @@ header. The counts are optional in the
// format: `@@ -1 +1 @@` means one line on each side, and reading the absent count as 0
// (rather than defaulting to 1) silently drops single-line hunks.
const HUNK_HEADER = /^@@+ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

// parsePatch reads a whole unified patch. An empty or whitespace-only patch yields [] rather
// than throwing: a clean tree is a normal state, not a parse failure.
export function parsePatch(patch: string): DiffFile[] {
  if (!patch.trim()) return [];
  // Drop the ONE empty element a trailing newline leaves behind. It is an artifact of the
  // split, not a line of the patch, and the hunk reader below treats a bare "" as an empty
  // context line - so leaving it in appends a phantom row to whatever hunk ends the patch,
  // which then shifts every count and every line number derived from it. Exactly one is
  // removed, so a patch genuinely ending in a blank context line keeps it.
  const lines = patch.replace(/\n$/, "").split("\n");
  const files: DiffFile[] = [];

  let cur: MutableFile | null = null;
  let hunk: MutableHunk | null = null;
  // Cursors into the two files, advanced per line so DiffLine can carry absolute numbers.
  let oldNo = 0;
  let newNo = 0;

  const closeHunk = (): void => {
    if (cur && hunk) cur.hunks.push(freezeHunk(hunk));
    hunk = null;
  };
  const closeFile = (): void => {
    closeHunk();
    if (cur) files.push(freezeFile(cur));
    cur = null;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";

    if (line.startsWith("diff --git ")) {
      closeFile();
      cur = newFile(gitHeaderPaths(line));
      continue;
    }

    // A patch with no `diff --git` line (plain POSIX diff, or `hg diff`) still opens a file
    // at its ---/+++ pair. Guarded on `cur` being absent or already having produced hunks,
    // so the ---/+++ that FOLLOWS a `diff --git` line does not open a second file for the
    // same one.
    //
    // The open hunk counts. Hunks move into cur.hunks only when one closes, so at the `---`
    // that starts the SECOND file of a bare patch the first file's sole hunk is still in
    // flight and cur.hunks is empty - reading that as "no hunks yet" makes this line
    // overwrite file one's path instead of starting file two, and the whole patch parses as
    // a single file.
    if (line.startsWith("--- ") && (!cur || cur.hunks.length > 0 || hunk !== null)) {
      closeFile();
      cur = newFile({ oldPath: stripPathPrefix(line.slice(4)), path: "" });
      continue;
    }

    if (!cur) continue;

    if (line.startsWith("--- ")) {
      cur.oldPath = stripPathPrefix(line.slice(4));
      continue;
    }
    if (line.startsWith("+++ ")) {
      const p = stripPathPrefix(line.slice(4));
      // /dev/null on the new side is a deletion: keep the old path as the file's identity,
      // because "/dev/null" is not something a sidebar can list or an anchor can name.
      if (p === "/dev/null") cur.status = "deleted";
      else cur.path = p;
      continue;
    }

    const m = HUNK_HEADER.exec(line);
    if (m) {
      closeHunk();
      const oldStart = Number(m[1]);
      const newStart = Number(m[3]);
      hunk = {
        header: line,
        oldStart,
        oldCount: m[2] === undefined ? 1 : Number(m[2]),
        newStart,
        newCount: m[4] === undefined ? 1 : Number(m[4]),
        lines: [],
      };
      oldNo = oldStart;
      newNo = newStart;
      continue;
    }

    if (hunk) {
      // "\ No newline at end of file" describes the PRECEDING line and consumes no line
      // number on either side.
      if (line.startsWith("\\")) {
        hunk.lines.push({ kind: "meta", text: line.slice(1).trim(), oldLine: null, newLine: null });
        continue;
      }
      const marker = line[0];
      if (marker === "+") {
        hunk.lines.push({ kind: "add", text: line.slice(1), oldLine: null, newLine: newNo++ });
        cur.additions++;
        continue;
      }
      if (marker === "-") {
        hunk.lines.push({ kind: "del", text: line.slice(1), oldLine: oldNo++, newLine: null });
        cur.deletions++;
        continue;
      }
      if (marker === " " || line === "") {
        // A fully empty line inside a hunk is a context line whose content is empty: some
        // producers drop the trailing space. Treating it as a terminator instead would cut
        // every hunk short at the first blank line.
        hunk.lines.push({
          kind: "context",
          text: line.slice(1),
          oldLine: oldNo++,
          newLine: newNo++,
        });
        continue;
      }
      // Anything else ends the hunk and is re-read as a header below.
      closeHunk();
    }

    applyExtendedHeader(cur, line);
  }
  closeFile();
  return files;
}

// applyExtendedHeader reads git's per-file header lines. Unrecognized lines are ignored, so
// an hg or jj header, or a git extension added later, is skipped rather than fatal.
function applyExtendedHeader(f: MutableFile, line: string): void {
  if (line.startsWith("new file mode ")) {
    f.status = "added";
    f.newMode = line.slice("new file mode ".length).trim();
  } else if (line.startsWith("deleted file mode ")) {
    f.status = "deleted";
    f.oldMode = line.slice("deleted file mode ".length).trim();
  } else if (line.startsWith("old mode ")) {
    f.oldMode = line.slice("old mode ".length).trim();
  } else if (line.startsWith("new mode ")) {
    f.newMode = line.slice("new mode ".length).trim();
  } else if (line.startsWith("rename from ")) {
    f.status = "renamed";
    f.oldPath = line.slice("rename from ".length).trim();
  } else if (line.startsWith("rename to ")) {
    f.status = "renamed";
    f.path = line.slice("rename to ".length).trim();
  } else if (line.startsWith("copy from ")) {
    f.status = "copied";
    f.oldPath = line.slice("copy from ".length).trim();
  } else if (line.startsWith("copy to ")) {
    f.status = "copied";
    f.path = line.slice("copy to ".length).trim();
  } else if (line.startsWith("Binary files ") || line.startsWith("GIT binary patch")) {
    f.binary = true;
  }
}

// gitHeaderPaths splits `diff --git a/x b/x` into its two paths.
//
// It splits on " b/" rather than on whitespace, because a path may CONTAIN spaces and a
// naive split puts half a filename in each field. That still cannot disambiguate a path
// containing the literal " b/", which no format can without quoting - so the a//b prefixes
// are the tiebreak and the last occurrence wins, matching git's own reader.
function gitHeaderPaths(line: string): { oldPath: string; path: string } {
  const rest = line.slice("diff --git ".length);
  const cut = rest.lastIndexOf(" b/");
  if (cut < 0) {
    // No recognizable pair: fall back to a whitespace split so the file still gets a name.
    const parts = rest.split(" ");
    return {
      oldPath: stripPathPrefix(parts[0] ?? ""),
      path: stripPathPrefix(parts[parts.length - 1] ?? ""),
    };
  }
  return {
    oldPath: stripPathPrefix(rest.slice(0, cut)),
    path: stripPathPrefix(rest.slice(cut + 1)),
  };
}

// stripPathPrefix removes the a/ or b/ prefix and any trailing tab-delimited timestamp that
// POSIX diff appends. /dev/null is passed through untouched - callers key on it.
function stripPathPrefix(raw: string): string {
  let p = raw.trim();
  const tab = p.indexOf("\t");
  if (tab >= 0) p = p.slice(0, tab).trim();
  if (p === "/dev/null") return p;
  if (p.startsWith("a/") || p.startsWith("b/")) return p.slice(2);
  return p;
}

interface MutableHunk {
  header: string;
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: DiffLine[];
}

interface MutableFile {
  path: string;
  oldPath: string;
  status: FileStatus;
  hunks: Hunk[];
  additions: number;
  deletions: number;
  binary: boolean;
  oldMode?: string;
  newMode?: string;
}

function newFile(paths: { oldPath: string; path: string }): MutableFile {
  return {
    path: paths.path,
    oldPath: paths.oldPath,
    status: "modified",
    hunks: [],
    additions: 0,
    deletions: 0,
    binary: false,
  };
}

function freezeHunk(h: MutableHunk): Hunk {
  return h;
}

// freezeFile settles the file's identity last, once every header has been seen. A deletion
// has no new path and a creation has no old one, so `path` falls back to whichever side
// exists - the sidebar must always have something to list.
function freezeFile(f: MutableFile): DiffFile {
  const path = f.path && f.path !== "/dev/null" ? f.path : f.oldPath;
  const oldPath = f.oldPath && f.oldPath !== "/dev/null" ? f.oldPath : path;
  return { ...f, path, oldPath };
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
