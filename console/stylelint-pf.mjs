import { readdirSync, readFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";

const require = createRequire(import.meta.url);

// Names are read from the installed @patternfly/patternfly rather than copied into a list, so a
// version bump changes what exists without anyone editing the rule. Component sheets are
// scanned for their own --pf-v6-c-* and --pf-v6-l-* properties; the design tokens all live in
// patternfly-base.css.
const tokenDeclaration = /(?<![\w-])(--pf-t--[A-Za-z0-9-]+)\s*:/g;
const componentDeclaration = /(?<![\w-])(--pf-v6-[cl]-[A-Za-z0-9_-]+)\s*:/g;

let cached;

function collect(text, pattern, into) {
  for (const match of text.matchAll(pattern)) into.add(match[1]);
}

function cssFiles(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return cssFiles(full);
    return entry.name.endsWith(".css") && !entry.name.endsWith(".min.css") ? [full] : [];
  });
}

// { tokens, properties } for the installed PatternFly: `tokens` is every --pf-t-- name
// the base sheet declares, `properties` every component or layout custom property a PF sheet declares.
export function patternflyNames() {
  if (cached) return cached;

  const root = path.dirname(require.resolve("@patternfly/patternfly/package.json"));
  const tokens = new Set();
  const properties = new Set();
  collect(readFileSync(path.join(root, "patternfly-base.css"), "utf8"), tokenDeclaration, tokens);
  for (const dir of ["components", "layouts"]) {
    for (const file of cssFiles(path.join(root, dir))) {
      collect(readFileSync(file, "utf8"), componentDeclaration, properties);
    }
  }

  cached = { tokens, properties };
  return cached;
}
