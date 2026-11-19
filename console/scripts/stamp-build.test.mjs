import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { stampBuild } from "./stamp-build.mjs";

const SW = `const BUILD_ID = "unstamped";
const CACHE = "magus-console-" + BUILD_ID;
const BASE = new URL("./", self.location).pathname;
const PRECACHE = [
  BASE,
  BASE + "console.js",
  BASE + "dashboard/dashboard.js",
];
`;

// build writes a fake gen/ tree and returns its path.
function build(files) {
  const dir = mkdtempSync(join(tmpdir(), "stamp-build-"));
  writeFileSync(join(dir, "sw.js"), SW);
  for (const [name, body] of Object.entries(files)) {
    const path = join(dir, name);
    mkdirSync(join(path, ".."), { recursive: true });
    writeFileSync(path, body);
  }
  return dir;
}

const FILES = { "console.js": "one", "dashboard/dashboard.js": "two" };

test("the stamped id replaces the placeholder", () => {
  const dir = build(FILES);
  const id = stampBuild(dir).value;

  assert.match(id, /^[0-9a-f]{12}$/);
  assert.match(readFileSync(join(dir, "sw.js"), "utf8"), new RegExp(`const BUILD_ID = "${id}";`));
});

// The whole point: same shell, same id, so an unchanged rebuild does not churn every
// client's cache and announce a refresh nobody needs.
test("identical bundles stamp identically", () => {
  assert.equal(stampBuild(build(FILES)).value, stampBuild(build(FILES)).value);
});

// And the direction that was broken before: a changed bundle MUST produce a new id,
// or the browser sees a byte-identical sw.js and never installs the new worker.
test("a changed bundle stamps differently", () => {
  const before = stampBuild(build(FILES)).value;
  const after = stampBuild(build({ ...FILES, "console.js": "one, but edited" })).value;
  assert.notEqual(before, after);
});

// Moving identical content between paths is a real change to the shell, and hashing
// bytes alone would miss it.
test("renaming a file with identical content stamps differently", () => {
  const before = stampBuild(build({ "console.js": "same", "dashboard/dashboard.js": "other" })).value;
  const after = stampBuild(build({ "console.js": "other", "dashboard/dashboard.js": "same" })).value;
  assert.notEqual(before, after);
});

// Failing loudly matters more than usual here: a silent no-op would leave the shipped
// worker on "unstamped" forever, which is exactly the bug this replaced.
test("a worker with no BUILD_ID assignment is an error", () => {
  const dir = build(FILES);
  writeFileSync(join(dir, "sw.js"), SW.replace(/const BUILD_ID = "[^"]*";/, ""));
  assert.match(stampBuild(dir).error.message, /no BUILD_ID assignment/);
});

test("a worker with no PRECACHE array is an error", () => {
  const dir = build(FILES);
  writeFileSync(join(dir, "sw.js"), 'const BUILD_ID = "unstamped";\n');
  assert.match(stampBuild(dir).error.message, /no PRECACHE array/);
});
