import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

// docs/src/styles/theme.css is the source of truth for these five colours (One Light / One Dark).
// tokens.css carries copies because docs/ and console/ are separate bundles: a shared file both
// could read is the better fix and a bigger change than this one. Until then a hand-copied value
// carries no signal when it goes stale, so this reads both files and turns drift into a failure.
//
// Paths are relative to the console/ package root, like scripts/app-stubs.mjs's
// readFileSync("index.html"): esbuild bundles every *.test.ts into .testcache with an outbase that
// varies with how many files are in the build, which would make an import.meta.url-relative path
// correct in a full test run and wrong in a scoped one.
const consoleCss = readFileSync("src/styles/base/tokens.css", "utf8");
const docsCss = readFileSync("../docs/src/styles/theme.css", "utf8");

// The declarations of the first rule that matches `selector` and declares `marker`. Every block
// read here holds only custom-property declarations and no nested rule, so scanning to the next
// "}" is exact.
function block(css: string, selector: RegExp, marker: string, label: string): string {
  const pattern = new RegExp(selector.source, "g");
  for (const m of css.matchAll(pattern)) {
    const start = m.index + m[0].length;
    const end = css.indexOf("}", start);
    assert.ok(end > start, `${label}: no closing brace after the selector`);
    const body = css.slice(start, end);
    if (body.includes(marker)) return body;
  }
  assert.fail(`${label}: no ${selector} block declares ${marker}`);
}

function value(cssBlock: string, name: string, label: string): string {
  const m = new RegExp(`--${name}:\\s*(#[0-9a-fA-F]{6})\\s*;`).exec(cssBlock);
  assert.ok(m, `${label}: --${name} is not a six-digit hex literal`);
  return m[1].toLowerCase();
}

const consoleLight = block(consoleCss, /:root\s*\{/, "--console-syntax-comment", "console light");
const consoleDark = block(
  consoleCss,
  /:root\.pf-v6-theme-dark\s*\{/,
  "--console-syntax-comment",
  "console dark",
);
const docsLight = block(
  docsCss,
  /:root:not\(\[data-theme="dark"\]\)\s*\{/,
  "--syn-comment",
  "docs light",
);
const docsDark = block(docsCss, /\[data-theme="dark"\]\s*\{/, "--syn-comment", "docs dark");
// The docs site declares its dark palette twice: once for an explicit [data-theme="dark"] and once
// under @media (prefers-color-scheme: dark) for a reader who never picked.
const docsDarkAuto = block(
  docsCss,
  /:root:not\(\[data-theme="light"\]\)\s*\{/,
  "--syn-comment",
  "docs dark (prefers-color-scheme)",
);

const TOKENS = ["comment", "keyword", "string", "number", "function"] as const;

test("the console syntax palette matches the docs site's --syn-* tokens (light)", () => {
  for (const token of TOKENS) {
    const got = value(consoleLight, `console-syntax-${token}`, "console light");
    const want = value(docsLight, `syn-${token}`, "docs light");
    assert.equal(
      got,
      want,
      `--console-syntax-${token} (${got}) has drifted from docs --syn-${token} (${want})`,
    );
  }
});

test("the console syntax palette matches the docs site's --syn-* tokens (dark)", () => {
  for (const token of TOKENS) {
    const got = value(consoleDark, `console-syntax-${token}`, "console dark");
    const want = value(docsDark, `syn-${token}`, "docs dark");
    assert.equal(
      got,
      want,
      `--console-syntax-${token} (${got}) has drifted from docs --syn-${token} (${want})`,
    );
  }
});

// With the two docs blocks pinned to each other, a failure names which pair drifted - the console
// from the docs, or the docs from themselves - instead of leaving three values and no verdict.
test("the docs site's two dark palettes agree with each other", () => {
  for (const token of TOKENS) {
    const explicit = value(docsDark, `syn-${token}`, "docs dark");
    const auto = value(docsDarkAuto, `syn-${token}`, "docs dark (prefers-color-scheme)");
    assert.equal(
      auto,
      explicit,
      `docs --syn-${token} is ${auto} under prefers-color-scheme: dark and ${explicit} under [data-theme="dark"]`,
    );
  }
});

test("the diff tokens read the syntax palette and carry no colour of their own", () => {
  const diffCss = readFileSync("src/apps/diff/diff.css", "utf8");

  assert.doesNotMatch(diffCss, /--console-syn-/, "the retired --console-syn-* names");
  assert.doesNotMatch(diffCss, /--console-syntax-[a-z]+\s*:/, "diff.css defines a syntax colour");
  for (const token of TOKENS) {
    assert.match(
      diffCss,
      new RegExp(`var\\(--console-syntax-${token}\\)`),
      `diff.css never reads ${token}`,
    );
  }
});
