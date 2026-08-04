// stamp-sw.mjs - rewrite gen/sw.js's BUILD_ID with a content hash of everything the service worker
// precaches. Runs LAST in the build (package.json's `build`), once every gen/ file exists.
//
// WHY: a browser re-installs a service worker only when the worker's OWN bytes change. sw.js carried
// a hand-written BUILD_ID that nothing bumped, so a deploy shipped a byte-identical sw.js, the
// byte-comparison found no update, `install` never fired, `activate` never dropped the previous
// CACHE, and the old bundles kept being served cache-first with no revalidation. A fix - including a
// security fix - could never reach a client that had already cached the app. Stamping the file is
// what turns a changed shell into a changed sw.js.
//
// WHY a content hash and not a timestamp: the console build is a drift gate (magusfile.buzz rebuilds
// gen/ and fails when git goes dirty), and a timestamp would dirty gen/ on every rebuild and make
// every deploy a cache-buster even when nothing changed. Hashing the precached bytes is
// deterministic - identical sources produce an identical BUILD_ID, and only a real content change
// bumps it, which is exactly when clients should drop their cache.
//
// The file list is READ OUT of the built sw.js's PRECACHE array rather than duplicated here, so the
// two cannot drift. A precache entry with no file on disk is a hard error: cache.addAll rejects on
// the first missing entry, which fails install and leaves the worker permanently stuck.
import { createHash } from "node:crypto";
import { existsSync, readFileSync, writeFileSync } from "node:fs";

const SW = "gen/sw.js";
const BUILD_ID_LINE = /^const BUILD_ID = "[^"]*";$/m;

const src = readFileSync(SW, "utf8");

const block = src.match(/const PRECACHE = \[([\s\S]*?)\n\];/);
if (!block) throw new Error("stamp-sw: no PRECACHE array found in " + SW);
if (!BUILD_ID_LINE.test(src)) throw new Error("stamp-sw: no BUILD_ID line found in " + SW);

// Strip line comments first so prose inside the array can never be mistaken for a path. Every real
// entry is `BASE + "<relative path>"`; the bare `BASE` entry contributes no literal and needs none -
// it is the directory, served as the index.html the list already names.
const rels = [...block[1].replace(/\/\/.*$/gm, "").matchAll(/"([^"]+)"/g)].map((m) => m[1]).sort();

const h = createHash("sha256");
for (const rel of rels) {
  const path = "gen/" + rel;
  if (!existsSync(path)) {
    throw new Error(
      "stamp-sw: sw.js precaches " + rel + " but " + path + " was not built; cache.addAll would " +
        "reject and the service worker would never install",
    );
  }
  // Hash the path alongside the bytes, with a separator, so a rename is a change and two adjacent
  // files cannot be concatenated into the same digest as one larger one.
  h.update(rel + "\0");
  h.update(readFileSync(path));
  h.update("\0");
}

const buildId = h.digest("hex").slice(0, 16);
writeFileSync(SW, src.replace(BUILD_ID_LINE, 'const BUILD_ID = "' + buildId + '";'));
