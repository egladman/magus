import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

// The console's deep-linkable surfaces are named in THREE hand-maintained lists, in two languages:
//
//   internal/service/console/url.go   KnownSurfaces        the daemon's SPA fallback
//   console/scripts/surface-stubs.mjs SURFACES             the hosted static stubs
//   console/src/console/main.ts       CLEAN_PATH_SURFACES  the boot router
//
// Each carries a comment telling the next person to keep it in step with the others, and until now
// that comment was the only thing enforcing it. The failure it guards against is quiet and
// asymmetric: a surface missing from the stubs 404s on the hosted site while working perfectly on a
// daemon, and one missing from the router serves the shell and then opens nothing.
//
// Parsed out of the sources as TEXT rather than imported, because two of the three are not
// JavaScript this test could load - which is the same reason the drift was possible.
//
// Paths are relative to the console/ package root, not to this file: pnpm always runs package
// scripts from there, which is the same convention the surface stubs and the CSS tests use.
const GO = "../internal/service/console/url.go";
const STUBS = "scripts/surface-stubs.mjs";
const SHELL = "src/console/main.ts";

// arrayLiteral pulls the quoted entries out of the ONE LINE that assigns `name`. Line-based rather
// than bracket-matched, which keeps it indifferent to the two syntaxes it has to read: Go spells the
// same literal `[]string{...}`, whose empty `[]` swallows a naive scan for the opening bracket.
// A comment mentioning the name is skipped, so the doc comment above each declaration cannot win.
function arrayLiteral(path, name) {
  const line = readFileSync(path, "utf8")
    .split("\n")
    .find((l) => !l.trimStart().startsWith("//") && l.includes(name) && l.includes("="));
  assert.ok(line, `no single-line assignment of ${name} in ${path}`);
  return [...line.matchAll(/"([^"]+)"/g)].map((m) => m[1]);
}

test("the daemon's surface list and the hosted stubs name exactly the same surfaces", () => {
  const known = arrayLiteral(GO, "KnownSurfaces");
  const stubs = arrayLiteral(STUBS, "SURFACES");
  assert.ok(known.length > 0, "KnownSurfaces parsed empty");
  // Both sides serve the shell for every surface path, so neither may carry one the other lacks: a
  // stub without a daemon route is a page the daemon 404s, and a route without a stub is a page the
  // hosted site 404s.
  assert.deepEqual([...stubs].sort(), [...known].sort());
});

test("every clean-path surface the console routes is one the daemon serves", () => {
  const known = new Set(arrayLiteral(GO, "KnownSurfaces"));
  const routed = arrayLiteral(SHELL, "CLEAN_PATH_SURFACES");
  assert.ok(routed.length > 0, "CLEAN_PATH_SURFACES parsed empty");
  // A SUBSET, not an equality: "plan" is a dashboard mode with a served path and no surface of its
  // own to open, so the daemon knows it and the boot router does not. The direction that matters is
  // this one - the router must never name a path the daemon will not serve the shell for.
  for (const surface of routed) {
    assert.ok(known.has(surface), `CLEAN_PATH_SURFACES has ${surface}, KnownSurfaces does not`);
  }
});

test("each routed surface has a bundle the shell can load", () => {
  const pkg = JSON.parse(readFileSync("package.json", "utf8"));
  const buildJs = pkg.scripts["build-js"];
  const shell = readFileSync(SHELL, "utf8");
  for (const surface of arrayLiteral(SHELL, "CLEAN_PATH_SURFACES")) {
    // A surface is reachable two ways: its own esbuild entry, or the shell registering it against
    // another surface's bundle. Dashboard's "plan" mode is the second kind, so the check is that
    // SOMETHING builds it, not that every name has an entry of its own.
    const built =
      buildJs.includes(`src/console/${surface}/main.ts`) ||
      shell.includes(`id: "${surface}"`) ||
      shell.includes(`pageId: "${surface}"`);
    assert.ok(built, `${surface} is routed but nothing builds or registers it`);
  }
});
