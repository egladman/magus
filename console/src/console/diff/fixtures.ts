// fixtures.ts - TEST SCAFFOLDING ONLY. Never import this from a surface.
//
// The product has exactly one unified-diff reader and it is in Go (internal/diff). This file
// is a second one, deliberately, and the distinction that makes that acceptable is narrow:
// what it produces is fed to buildRows and compared against rendered output, never to a hunk
// DIGEST and never to a read receipt. Digests are the identity a mark is keyed by, and two
// implementations computing those independently is what let the console and the daemon
// disagree about the same session. Nothing here computes one.
//
// The render tests express their cases as patches because that is what a reader of those tests
// needs to see - a five-line patch says what is being rendered, a hand-written tree of rows
// does not. `patchFixture` exists so they can keep doing that without the app carrying a parser.
//
// Two tests hold it there, because a rule that lives only in a comment is a rule with roughly
// even odds. One fails if any non-test module imports this. The other parses demo.patch with
// BOTH readers and compares the trees - that patch is the one corpus which exists in both
// forms, since Go's output for it is committed to gen/demo.ts, so "these two could drift" is
// answered on every run rather than promised in a header.

import type { DiffFile, DiffLine, FileStatus, Hunk, LineKind } from "./parse";

const HUNK_HEADER = /^@@+ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

function stripPrefix(raw: string): string {
  let p = raw.trim();
  const tab = p.indexOf("\t");
  if (tab >= 0) p = p.slice(0, tab).trim();
  if (p === "/dev/null") return p;
  return p.startsWith("a/") || p.startsWith("b/") ? p.slice(2) : p;
}

interface Mutable {
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

// patchFixture reads a unified patch into the render tree, for tests that describe their case
// as a patch. Digest is always "" - see the header: nothing here may mint one.
export function patchFixture(patch: string): DiffFile[] {
  if (!patch.trim()) return [];
  const lines = patch.replace(/\n$/, "").split("\n");
  const files: DiffFile[] = [];
  let cur: Mutable | null = null;
  let lineRows: DiffLine[] = [];
  let head: { header: string; o: number; oc: number; n: number; nc: number } | null = null;
  let oldNo = 0;
  let newNo = 0;

  const closeHunk = (): void => {
    if (!cur || !head) return;
    cur.hunks.push({
      digest: "",
      // Empty for the same reason digest is: the daemon parses it out of the header, and nothing
      // in the browser may mint one. fromWire defaults it the same way.
      declaration: "",
      index: cur.hunks.length,
      header: head.header,
      oldStart: head.o,
      oldCount: head.oc,
      newStart: head.n,
      newCount: head.nc,
      lines: lineRows,
    });
    head = null;
    lineRows = [];
  };
  const closeFile = (): void => {
    closeHunk();
    if (!cur) return;
    const path = cur.path && cur.path !== "/dev/null" ? cur.path : cur.oldPath;
    const oldPath = cur.oldPath && cur.oldPath !== "/dev/null" ? cur.oldPath : path;
    files.push({ ...cur, path, oldPath });
    cur = null;
  };
  // Returns rather than assigning: TypeScript cannot follow an assignment made inside a
  // closure, so `cur` narrows to never at every use after one.
  const open = (oldPath: string, path: string): Mutable => ({
    path,
    oldPath,
    status: "modified",
    hunks: [],
    additions: 0,
    deletions: 0,
    binary: false,
  });
  const push = (kind: LineKind, text: string, o: number | null, n: number | null): void => {
    lineRows.push({ kind, text, oldLine: o, newLine: n });
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    if (line.startsWith("diff --git ")) {
      closeFile();
      const rest = line.slice("diff --git ".length);
      const cut = rest.lastIndexOf(" b/");
      if (cut < 0) {
        const parts = rest.split(" ");
        cur = open(stripPrefix(parts[0] ?? ""), stripPrefix(parts[parts.length - 1] ?? ""));
      } else {
        cur = open(stripPrefix(rest.slice(0, cut)), stripPrefix(rest.slice(cut + 1)));
      }
      continue;
    }
    if (line.startsWith("--- ") && (lines[i + 1] ?? "").startsWith("+++ ")) {
      const o = stripPrefix(line.slice(4));
      const n = stripPrefix((lines[i + 1] ?? "").slice(4));
      if (!cur) cur = open(o, n);
      if (o === "/dev/null" && n !== "/dev/null") cur.status = "added";
      else if (n === "/dev/null" && o !== "/dev/null") cur.status = "deleted";
      i++;
      continue;
    }
    const m = HUNK_HEADER.exec(line);
    if (m && cur) {
      closeHunk();
      oldNo = Number(m[1]);
      newNo = Number(m[3]);
      head = {
        header: line,
        o: oldNo,
        oc: m[2] === undefined ? 1 : Number(m[2]),
        n: newNo,
        nc: m[4] === undefined ? 1 : Number(m[4]),
      };
      continue;
    }
    if (head && cur) {
      if (line.startsWith("\\")) {
        push("meta", line.slice(1).trim(), null, null);
        continue;
      }
      if (line.startsWith("+")) {
        push("add", line.slice(1), null, newNo++);
        cur.additions++;
        continue;
      }
      if (line.startsWith("-")) {
        push("del", line.slice(1), oldNo++, null);
        cur.deletions++;
        continue;
      }
      if (line.startsWith(" ") || line === "") {
        push("context", line.slice(1), oldNo++, newNo++);
        continue;
      }
      closeHunk();
    }
    if (cur) {
      if (line.startsWith("new file mode ")) cur.status = "added";
      else if (line.startsWith("deleted file mode ")) cur.status = "deleted";
      else if (line.startsWith("old mode ")) cur.oldMode = line.slice("old mode ".length).trim();
      else if (line.startsWith("new mode ")) cur.newMode = line.slice("new mode ".length).trim();
      else if (line.startsWith("rename from ")) {
        cur.status = "renamed";
        cur.oldPath = line.slice("rename from ".length).trim();
      } else if (line.startsWith("rename to ")) {
        cur.status = "renamed";
        cur.path = line.slice("rename to ".length).trim();
      } else if (line.startsWith("Binary files ")) cur.binary = true;
    }
  }
  closeFile();
  return files;
}
