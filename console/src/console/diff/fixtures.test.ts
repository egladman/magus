// The guard that keeps fixtures.ts out of the product.
//
// It is a second unified-diff reader, kept only so render tests can express a case as a patch.
// The moment a surface imports it, the console is parsing patches again and can disagree with
// the daemon about what a changeset contains - which is the bug the consolidation removed. A
// comment saying "test only" is a rule with roughly even odds; this is the enforcement point.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { patchFixture } from "./fixtures";
import { fromWire, type DiffFile } from "./parse";
import { DEMO_FILES } from "./gen/demo";

const DIR = new URL(".", import.meta.url).pathname;

test("no product module imports the test-only patch reader", () => {
  const offenders: string[] = [];
  for (const name of readdirSync(DIR)) {
    if (!name.endsWith(".ts") || name.endsWith(".test.ts") || name === "fixtures.ts") continue;
    const src = readFileSync(join(DIR, name), "utf8");
    if (/from\s+"\.\/fixtures"/.test(src)) offenders.push(name);
  }
  assert.deepEqual(
    offenders,
    [],
    "these import the test-only reader; build their input from the wire instead",
  );
});

// It has one job and no license beyond it: whatever it returns must never carry a digest,
// because a digest minted outside the daemon is one the CLI and an agent cannot see.
test("the fixture reader never mints a hunk digest", () => {
  const files = patchFixture("diff --git a/a.go b/a.go\n@@ -1,2 +1,2 @@\n-old\n+new\n");
  assert.equal(files.length, 1);
  for (const h of files[0]?.hunks ?? []) assert.equal(h.digest, "");
});

// The drift this file's header worries about, closed rather than merely disclaimed.
//
// demo.patch is the one corpus that exists in BOTH forms: as a patch, and as the Go reader's
// output committed to gen/demo.ts. So the test-only reader can be held against the real one on
// the same bytes, and the residual risk of "render tests exercise a tree production never
// produces" goes away.
//
// Digests are excluded on purpose - this reader is forbidden to mint them, and that is pinned
// separately above. Everything else about the shape must agree.
test("the fixture reader agrees with the Go reader on the same patch", () => {
  // From the project root, not from import.meta.url: the suite runs out of .testcache, where
  // the compiled module sits beside no .patch file.
  const patch = readFileSync(join(process.cwd(), "src/console/diff/demo.patch"), "utf8");
  const mine = patchFixture(patch);
  const theirs = fromWire(DEMO_FILES);

  const shape = (files: readonly DiffFile[]): unknown =>
    files.map((f) => ({
      path: f.path,
      oldPath: f.oldPath,
      status: f.status,
      binary: f.binary,
      additions: f.additions,
      deletions: f.deletions,
      hunks: f.hunks.map((h) => ({
        header: h.header,
        oldStart: h.oldStart,
        oldCount: h.oldCount,
        newStart: h.newStart,
        newCount: h.newCount,
        lines: h.lines.map((l) => [l.kind, l.text, l.oldLine, l.newLine]),
      })),
    }));

  assert.deepEqual(shape(mine), shape(theirs));
});
