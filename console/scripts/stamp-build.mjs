// stamp-build.mjs - rewrite the built service worker's BUILD_ID to a digest of the
// bytes it precaches, so the cache name changes exactly when the shell changes.
//
// BUILD_ID names the SW's cache, and a new worker only installs when sw.js itself
// differs from the copy the browser already has. It used to be a hand-written
// string ("w4-patternfly"), which made the whole update path dead on arrival: ship a
// rebuilt console, sw.js comes out byte-identical, the browser finds no update, and
// the service worker keeps serving the old bundle from the old cache. The refresh
// prompt downstream was never reached because nothing upstream ever fired. That is
// the failure mode of every counter a human is asked to remember: not carelessness,
// just that nothing tells them when they forgot.
//
// Derived from content instead. The digest covers the built artifacts sw.js lists in
// PRECACHE, so any change to the shell produces a new id and no change produces the
// same one - self-maintaining in both directions, and impossible to forget.
//
// Run AFTER the bundles are built and sw.js is copied into gen/.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

// stampBuild rewrites <dir>/sw.js in place and returns the digest it stamped.
export function stampBuild(dir) {
  const swPath = join(dir, "sw.js");
  const sw = readFileSync(swPath, "utf8");

  // Read the precache list out of the worker itself rather than keeping a second
  // copy here. Two lists would drift, and the one that matters is the worker's.
  const listMatch = sw.match(/const PRECACHE = \[([\s\S]*?)\];/);
  if (!listMatch) {
    throw new Error("stamp-build: no PRECACHE array in " + swPath);
  }
  const files = [...listMatch[1].matchAll(/BASE \+ "([^"]+)"/g)].map((m) => m[1]).sort();
  if (files.length === 0) {
    throw new Error("stamp-build: PRECACHE names no files; refusing to stamp a digest of nothing");
  }

  const h = createHash("sha256");
  for (const f of files) {
    // The name goes in as well as the bytes, so moving identical contents between
    // two paths still counts as a change.
    h.update(f);
    h.update(readFileSync(join(dir, f)));
  }
  const id = h.digest("hex").slice(0, 12);

  const stamped = sw.replace(/const BUILD_ID = "[^"]*";/, 'const BUILD_ID = "' + id + '";');
  if (stamped === sw) {
    throw new Error("stamp-build: no BUILD_ID assignment in " + swPath);
  }
  writeFileSync(swPath, stamped);
  return id;
}

// Entry point when run as a script from console/ (see package.json copy-static).
if (process.argv[1] && process.argv[1].endsWith("stamp-build.mjs")) {
  stampBuild(process.argv[2] || "gen");
}
