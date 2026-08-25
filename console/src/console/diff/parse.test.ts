// parse.test.ts - the wire mapping. The PARSING these tests used to cover moved to Go with the
// reader itself (internal/diff/parse_test.go carries the ported cases); what is left here is
// the part TypeScript still owns: turning the daemon's JSON into the render tree, and the
// null-handling that Go's marshalling makes routine rather than exotic.

import { test } from "node:test";
import assert from "node:assert/strict";
import { countLines, fromWire, type WireFile } from "./parse";

function wireFile(over: Partial<WireFile> = {}): WireFile {
  return {
    path: "a.go",
    old_path: "a.go",
    status: "modified",
    additions: 1,
    deletions: 1,
    binary: false,
    hunks: [
      {
        header: "@@ -1,2 +1,2 @@",
        digest: "abc123",
        index: 0,
        lines: ["-old", "+new"],
        old_start: 1,
        old_count: 2,
        new_start: 1,
        new_count: 2,
        rows: [
          { kind: "del", text: "old", old_line: 1, new_line: null },
          { kind: "add", text: "new", old_line: null, new_line: 1 },
        ],
      },
    ],
    ...over,
  };
}

test("fromWire maps the changeset into the render tree", () => {
  const [f] = fromWire([wireFile()]);
  assert.equal(f?.path, "a.go");
  assert.equal(f?.status, "modified");
  assert.equal(f?.additions, 1);
  const h = f?.hunks[0];
  assert.equal(h?.oldStart, 1);
  assert.equal(h?.newCount, 2);
  assert.deepEqual(
    h?.lines.map((l) => [l.kind, l.text, l.oldLine, l.newLine]),
    [
      ["del", "old", 1, null],
      ["add", "new", null, 1],
    ],
  );
});

// The digest is the hunk's identity and a read receipt is keyed by it. It comes from the
// daemon and must survive the mapping untouched - this surface has no way to recompute it,
// because the rows have had their markers stripped and putting them back does not round-trip.
test("fromWire carries the daemon's hunk digest through unchanged", () => {
  const [f] = fromWire([wireFile()]);
  assert.equal(f?.hunks[0]?.digest, "abc123");
});

// Go marshals a nil slice as null, not []. A binary file has no hunks and a mode-only change
// has no rows, so both arrive that way in ordinary use rather than as an edge case.
test("a null hunk or row list maps to an empty one", () => {
  const [binary] = fromWire([wireFile({ binary: true, hunks: null })]);
  assert.equal(binary?.binary, true);
  assert.deepEqual(binary?.hunks, []);

  const [modeOnly] = fromWire([
    wireFile({
      hunks: [
        {
          header: "",
          digest: "",
          index: 0,
          lines: null,
          old_start: 0,
          old_count: 0,
          new_start: 0,
          new_count: 0,
          rows: null,
        },
      ],
    }),
  ]);
  assert.deepEqual(modeOnly?.hunks[0]?.lines, []);
});

test("an absent changeset maps to no files rather than throwing", () => {
  assert.deepEqual(fromWire(null), []);
  assert.deepEqual(fromWire(undefined), []);
  assert.deepEqual(fromWire([]), []);
});

// A rename carries two names and the sidebar lists the new one; both must survive.
test("fromWire keeps both sides of a rename", () => {
  const [f] = fromWire([wireFile({ path: "new.go", old_path: "old.go", status: "renamed" })]);
  assert.equal(f?.path, "new.go");
  assert.equal(f?.oldPath, "old.go");
  assert.equal(f?.status, "renamed");
});

// The optional modes are omitted rather than set to undefined, so a consumer spreading the
// object does not gain keys the wire never sent.
test("modes appear only when the wire carried them", () => {
  const [plain] = fromWire([wireFile()]);
  assert.equal("oldMode" in (plain ?? {}), false);
  const [moded] = fromWire([wireFile({ old_mode: "100644", new_mode: "100755" })]);
  assert.equal(moded?.oldMode, "100644");
  assert.equal(moded?.newMode, "100755");
});

test("countLines totals every hunk line plus one row per hunk header", () => {
  assert.equal(countLines(fromWire([wireFile()])), 3); // 2 lines + 1 header
  assert.equal(countLines([]), 0);
});
