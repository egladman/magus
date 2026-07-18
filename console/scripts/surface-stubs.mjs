// surface-stubs.mjs - emit per-surface index.html stubs for the clean /console/<surface>/ deep
// links (the canonical daemon-origin form magus mints). The daemon serves the shell for these
// paths via an SPA fallback, but the HOSTED static host (GitHub Pages, no rewrites) has none, so
// each surface path needs a PHYSICAL index.html. The stub is the shell index.html with a single
// <base href="../"> injected as the first <head> child: the shell loads its assets by RELATIVE
// path (./console.js, ./patternfly.css) so one built index works at both the hosted origin and
// the daemon, and served one level deep at /console/<surface>/ those refs must resolve against
// the parent /console/, which the base makes so. (The shell's lazy imports resolve against
// import.meta.url, i.e. console.js's URL, so they are unaffected.) This mirrors the daemon's
// serveConsoleShell injection exactly. Keep SURFACES in step with the daemon's KnownSurfaces
// (internal/service/console) and the console's CLEAN_PATH_SURFACES.
import { readFileSync, writeFileSync, mkdirSync } from "node:fs";

const SURFACES = ["logs", "dashboard", "graph", "activity"];
const shell = readFileSync("index.html", "utf8").replace("<head>", '<head>\n  <base href="../">');
for (const d of SURFACES) {
  mkdirSync(`gen/${d}`, { recursive: true });
  writeFileSync(`gen/${d}/index.html`, shell);
}
