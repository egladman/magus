import { test } from "node:test";
import assert from "node:assert/strict";
import { parsePatch, countLines } from "./parse";

// at() indexes without a non-null assertion (the repo bans them) and fails the test with a
// useful message instead of a TypeError three lines later.
function at<T>(xs: readonly T[], i: number, what: string): T {
  const v = xs[i];
  if (v === undefined) throw new Error(`missing ${what} at index ${i}`);
  return v;
}

test("parses a single modified file with correct line numbers", () => {
  const patch = [
    "diff --git a/src/x.ts b/src/x.ts",
    "index 111..222 100644",
    "--- a/src/x.ts",
    "+++ b/src/x.ts",
    "@@ -10,4 +10,5 @@ function f() {",
    " keep",
    "-gone",
    "+added one",
    "+added two",
    " tail",
    "",
  ].join("\n");

  const files = parsePatch(patch);
  assert.equal(files.length, 1);
  const f = at(files, 0, "files");
  assert.equal(f.path, "src/x.ts");
  assert.equal(f.status, "modified");
  assert.equal(f.additions, 2);
  assert.equal(f.deletions, 1);
  assert.equal(f.hunks.length, 1);

  const h = at(f.hunks, 0, "hunk");
  assert.equal(h.oldStart, 10);
  assert.equal(h.oldCount, 4);
  assert.equal(h.newStart, 10);
  assert.equal(h.newCount, 5);

  // The numbering is the part a virtualized renderer cannot re-derive, so assert it exactly.
  assert.deepEqual(
    h.lines.map((l) => [l.kind, l.oldLine, l.newLine]),
    [
      ["context", 10, 10],
      ["del", 11, null],
      ["add", null, 11],
      ["add", null, 12],
      ["context", 12, 13],
    ],
  );
});

// The counts are OPTIONAL in the format. Reading an absent count as 0 instead of 1 silently
// drops every single-line hunk, which is the most common shape of all.
test("a hunk header without counts means one line per side", () => {
  const files = parsePatch(["--- a/x", "+++ b/x", "@@ -1 +1 @@", "-a", "+b", ""].join("\n"));
  const h = at(at(files, 0, "file").hunks, 0, "hunk");
  assert.equal(h.oldCount, 1);
  assert.equal(h.newCount, 1);
});

test("detects an added file and keeps its new path", () => {
  const patch = [
    "diff --git a/new.ts b/new.ts",
    "new file mode 100644",
    "--- /dev/null",
    "+++ b/new.ts",
    "@@ -0,0 +1,2 @@",
    "+one",
    "+two",
    "",
  ].join("\n");
  const f = at(parsePatch(patch), 0, "file");
  assert.equal(f.status, "added");
  assert.equal(f.path, "new.ts");
  assert.equal(f.additions, 2);
});

// A deletion has no new path. "/dev/null" must never become the file's identity - a sidebar
// cannot list it and an anchor cannot name it.
test("a deleted file is identified by its old path, never /dev/null", () => {
  const patch = [
    "diff --git a/gone.ts b/gone.ts",
    "deleted file mode 100644",
    "--- a/gone.ts",
    "+++ /dev/null",
    "@@ -1,2 +0,0 @@",
    "-one",
    "-two",
    "",
  ].join("\n");
  const f = at(parsePatch(patch), 0, "file");
  assert.equal(f.status, "deleted");
  assert.equal(f.path, "gone.ts");
  assert.equal(f.deletions, 2);
});

test("detects a rename and keeps both paths", () => {
  const patch = [
    "diff --git a/old/name.ts b/new/name.ts",
    "similarity index 95%",
    "rename from old/name.ts",
    "rename to new/name.ts",
    "",
  ].join("\n");
  const f = at(parsePatch(patch), 0, "file");
  assert.equal(f.status, "renamed");
  assert.equal(f.oldPath, "old/name.ts");
  assert.equal(f.path, "new/name.ts");
});

// A pure mode change produces NO hunks, so without the flags it renders as an empty entry
// that reads like "nothing changed".
test("a pure mode change is captured even with no hunks", () => {
  const patch = ["diff --git a/run.sh b/run.sh", "old mode 100644", "new mode 100755", ""].join(
    "\n",
  );
  const f = at(parsePatch(patch), 0, "file");
  assert.equal(f.oldMode, "100644");
  assert.equal(f.newMode, "100755");
  assert.equal(f.hunks.length, 0);
});

test("flags a binary file rather than showing it as empty", () => {
  const patch = [
    "diff --git a/logo.png b/logo.png",
    "index 111..222 100644",
    "Binary files a/logo.png and b/logo.png differ",
    "",
  ].join("\n");
  const f = at(parsePatch(patch), 0, "file");
  assert.equal(f.binary, true);
  assert.equal(f.hunks.length, 0);
});

test("parses several files and several hunks per file", () => {
  const patch = [
    "diff --git a/a.ts b/a.ts",
    "--- a/a.ts",
    "+++ b/a.ts",
    "@@ -1,2 +1,2 @@",
    " x",
    "-y",
    "+z",
    "@@ -10,2 +10,3 @@",
    " p",
    "+q",
    " r",
    "diff --git a/b.ts b/b.ts",
    "--- a/b.ts",
    "+++ b/b.ts",
    "@@ -5,1 +5,2 @@",
    " m",
    "+n",
    "",
  ].join("\n");
  const files = parsePatch(patch);
  assert.equal(files.length, 2);
  assert.equal(at(files, 0, "files").path, "a.ts");
  assert.equal(at(files, 0, "files").hunks.length, 2);
  // The second hunk restarts numbering from its own header, not from where the first ended.
  assert.equal(at(at(at(files, 0, "file").hunks, 1, "hunk").lines, 0, "line").oldLine, 10);
  assert.equal(at(files, 1, "files").path, "b.ts");
  assert.equal(at(files, 1, "files").hunks.length, 1);
});

// The marker consumes no line number on either side; counting it as a line shifts every
// subsequent row by one.
test("the no-newline marker is meta and consumes no line number", () => {
  const patch = [
    "--- a/x",
    "+++ b/x",
    "@@ -1,2 +1,2 @@",
    " a",
    "-b",
    "\\ No newline at end of file",
    "+b",
    "\\ No newline at end of file",
    "",
  ].join("\n");
  const h = at(at(parsePatch(patch), 0, "file").hunks, 0, "hunk");
  const meta = h.lines.filter((l) => l.kind === "meta");
  assert.equal(meta.length, 2);
  for (const m of meta) {
    assert.equal(m.oldLine, null);
    assert.equal(m.newLine, null);
  }
  // One add and one del, each numbered as if the markers were not there.
  assert.deepEqual(
    h.lines.filter((l) => l.kind === "del").map((l) => l.oldLine),
    [2],
  );
  assert.deepEqual(
    h.lines.filter((l) => l.kind === "add").map((l) => l.newLine),
    [2],
  );
});

// A blank line inside a hunk is a context line whose content is empty - some producers drop
// the trailing space. Terminating the hunk there cuts it short at the first blank line.
test("a fully blank line inside a hunk stays context", () => {
  const patch = ["--- a/x", "+++ b/x", "@@ -1,3 +1,3 @@", " a", "", "-b", "+c", ""].join("\n");
  const h = at(at(parsePatch(patch), 0, "file").hunks, 0, "hunk");
  assert.equal(h.lines.length, 4);
  assert.equal(at(h.lines, 1, "line").kind, "context");
});

// Paths may contain spaces; a whitespace split puts half a filename in each field.
test("a path containing spaces survives the git header split", () => {
  const patch = [
    "diff --git a/dir/my file.ts b/dir/my file.ts",
    "--- a/dir/my file.ts",
    "+++ b/dir/my file.ts",
    "@@ -1 +1 @@",
    "-a",
    "+b",
    "",
  ].join("\n");
  assert.equal(at(parsePatch(patch), 0, "file").path, "dir/my file.ts");
});

// POSIX diff appends a tab-delimited timestamp that is not part of the path.
test("a tab-delimited timestamp is stripped from the path", () => {
  const patch = [
    "--- a/x.ts\t2026-08-14 10:00:00.000000000 +0000",
    "+++ b/x.ts\t2026-08-14 10:01:00.000000000 +0000",
    "@@ -1 +1 @@",
    "-a",
    "+b",
    "",
  ].join("\n");
  assert.equal(at(parsePatch(patch), 0, "file").path, "x.ts");
});

// A patch with no `diff --git` line at all (plain POSIX diff, hg) must still parse.
test("parses a bare patch with no git header", () => {
  const patch = [
    "--- a/one.ts",
    "+++ b/one.ts",
    "@@ -1 +1 @@",
    "-a",
    "+b",
    "--- a/two.ts",
    "+++ b/two.ts",
    "@@ -1 +1 @@",
    "-c",
    "+d",
    "",
  ].join("\n");
  const files = parsePatch(patch);
  assert.equal(files.length, 2);
  assert.equal(at(files, 0, "files").path, "one.ts");
  assert.equal(at(files, 1, "files").path, "two.ts");
});

test("a clean tree parses to no files rather than throwing", () => {
  assert.deepEqual(parsePatch(""), []);
  assert.deepEqual(parsePatch("   \n\t\n"), []);
});

test("countLines totals every hunk line plus one row per hunk header", () => {
  const patch = [
    "diff --git a/a.ts b/a.ts",
    "--- a/a.ts",
    "+++ b/a.ts",
    "@@ -1,2 +1,2 @@",
    " x",
    "-y",
    "+z",
    "@@ -10,1 +10,2 @@",
    " p",
    "+q",
    "",
  ].join("\n");
  // hunk one: 3 lines + header; hunk two: 2 lines + header.
  assert.equal(countLines(parsePatch(patch)), 7);
});
