// surface-stubs.mjs - emit per-surface index.html stubs for the clean /console/<segment>/ deep
// links (the canonical server-origin form magus mints). The server serves the shell for these
// paths via an SPA fallback, but the HOSTED static host (GitHub Pages, no rewrites) has none, so
// each surface path needs a PHYSICAL index.html. The stub is the shell index.html with a single
// <base href="../"> injected as the first <head> child: the shell loads its assets by RELATIVE
// path (./console.js, ./patternfly.css) so one built index works at both the hosted origin and
// the server, and served one level deep at /console/<segment>/ those refs must resolve against
// the parent /console/, which the base makes so. (The shell's lazy imports resolve against
// import.meta.url, i.e. console.js's URL, so they are unaffected.) This mirrors the server's
// serveConsoleShell injection exactly, and the server reads its surface routes back out of these
// stubs, so this is the one place the served segments are decided.
//
// The segments come from the app manifests: every src/apps/<id>/app.ts's path and mode segments.
// Node strips the types and runs app.ts as is, which holds while a manifest imports only types.
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

// surfaceSegments lists every clean path segment the app manifests under appsDir declare.
export async function surfaceSegments(appsDir = "src/apps") {
  const segments = [];
  for (const entry of readdirSync(appsDir, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const mod = await import(pathToFileURL(resolve(appsDir, entry.name, "app.ts")).href);
    const [app] = Object.values(mod);
    if (app.path) segments.push(app.path);
    segments.push(...Object.keys(app.modes ?? {}));
  }
  return segments.sort();
}

if (process.argv[1]?.endsWith("surface-stubs.mjs")) {
  const shell = readFileSync("index.html", "utf8").replace("<head>", '<head>\n  <base href="../">');
  for (const segment of await surfaceSegments()) {
    mkdirSync(join("gen", segment), { recursive: true });
    writeFileSync(join("gen", segment, "index.html"), shell);
  }
}
