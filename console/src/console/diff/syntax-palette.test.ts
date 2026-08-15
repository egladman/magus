import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// docs/src/styles/theme.css is the source of truth for these five tokens (One Light / One
// Dark); diff.css's own header comment explains why the values are copied here instead of
// imported (the console owns its own bundle - see docs/package.json).
//
// If the two drift, code renders in one palette on the docs site and a different one in the
// console, with nothing to say so: a hand-copied value carries no signal when it goes stale.
// Reading both files and comparing the literal values turns that into a build failure.
//
// Paths are relative to the console/ package root (like scripts/surface-stubs.mjs's
// readFileSync("index.html")), not to this file's own location: esbuild bundles every
// *.test.ts into .testcache with an outbase that varies with how many files are in the
// build, which would make an import.meta.url-relative path correct in a full test run and
// wrong in a scoped one. process.cwd() is stable either way because pnpm always runs
// package scripts from the package root.
const consoleCss = readFileSync("src/console/diff/diff.css", "utf8");
const docsCss = readFileSync("../docs/src/styles/theme.css", "utf8");

// Slices the declarations out of one top-level CSS rule, found by its selector. Every block
// read here holds only custom-property declarations and no nested rule, so scanning to the
// next "}" is exact.
function block(css: string, selector: RegExp, label: string): string {
  const m = selector.exec(css);
  assert.ok(m, `${label}: selector ${selector} not found`);
  const start = m.index + m[0].length;
  const end = css.indexOf("}", start);
  assert.ok(end > start, `${label}: no closing brace after the selector`);
  return css.slice(start, end);
}

function value(cssBlock: string, name: string, label: string): string {
  const m = new RegExp(`--${name}:\\s*(#[0-9a-fA-F]{6})\\s*;`).exec(cssBlock);
  assert.ok(m, `${label}: --${name} not found`);
  return m[1].toLowerCase();
}

const consoleLight = block(consoleCss, /:root\s*\{/, "console light");
const consoleDark = block(consoleCss, /:root\.pf-v6-theme-dark\s*\{/, "console dark");
const docsLight = block(docsCss, /:root:not\(\[data-theme="dark"\]\)\s*\{/, "docs light");
const docsDark = block(docsCss, /\[data-theme="dark"\]\s*\{/, "docs dark");

const TOKENS = ["comment", "keyword", "string", "number", "function"] as const;

test("console syntax palette matches the docs site's --syn-* tokens (light)", () => {
  for (const token of TOKENS) {
    const got = value(consoleLight, `console-syn-${token}`, "console light");
    const want = value(docsLight, `syn-${token}`, "docs light");
    assert.equal(
      got,
      want,
      `--console-syn-${token} (${got}) has drifted from docs --syn-${token} (${want})`,
    );
  }
});

test("console syntax palette matches the docs site's --syn-* tokens (dark)", () => {
  for (const token of TOKENS) {
    const got = value(consoleDark, `console-syn-${token}`, "console dark");
    const want = value(docsDark, `syn-${token}`, "docs dark");
    assert.equal(
      got,
      want,
      `--console-syn-${token} (${got}) has drifted from docs --syn-${token} (${want})`,
    );
  }
});
