import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// The syntax colours are PatternFly palette steps, not literals: the colour rule keeps hex in
// tokens.css, and a literal here is a colour that ignores the theme. What is left to hold is the
// shape that keeps code legible in both themes - a dark step on the light ground, a light step on
// the dark one - because the plain palette tokens do not follow the theme and a step picked for one
// ground reads as a smudge on the other. diff.css's header carries the measured contrast.
//
// The path is relative to the console/ package root, like scripts/app-stubs.mjs's
// readFileSync("index.html"): esbuild bundles every *.test.ts into .testcache with an outbase that
// varies with how many files are in the build, which would make an import.meta.url-relative path
// correct in a full test run and wrong in a scoped one.
const css = readFileSync("src/apps/diff/diff.css", "utf8");

// Slices the declarations out of one top-level CSS rule, found by its selector. Every block read
// here holds only custom-property declarations and no nested rule, so scanning to the next "}" is
// exact.
function block(selector: RegExp, label: string): string {
  const m = selector.exec(css);
  assert.ok(m, `${label}: selector ${selector} not found`);
  const start = m.index + m[0].length;
  const end = css.indexOf("}", start);
  assert.ok(end > start, `${label}: no closing brace after the selector`);
  return css.slice(start, end);
}

function declared(cssBlock: string, name: string, label: string): string {
  const m = new RegExp(`--${name}:\\s*var\\((--pf-t--[a-z0-9-]+)\\)\\s*;`).exec(cssBlock);
  assert.ok(m, `${label}: --${name} is not a var() of a PatternFly token`);
  return m[1];
}

const light = block(/:root\s*\{/, "light");
const dark = block(/:root\.pf-v6-theme-dark\s*\{/, "dark");

const HUED = ["keyword", "string", "number", "function"] as const;

// step reads the palette step off a token like --pf-t--color--purple--50.
function step(token: string): number {
  const m = /--(\d+)$/.exec(token);
  assert.ok(m, `${token} names no palette step`);
  return Number(m[1]);
}

test("the syntax colours are theme-aware tokens, never literals", () => {
  assert.doesNotMatch(light, /#[0-9a-fA-F]{3,8}\b/, "a hex literal in the light block");
  assert.doesNotMatch(dark, /#[0-9a-fA-F]{3,8}\b/, "a hex literal in the dark block");
  // The comment colour is PatternFly's subtle text, which follows the theme on its own.
  assert.equal(
    declared(light, "console-syn-comment", "light"),
    "--pf-t--global--text--color--subtle",
  );
  assert.doesNotMatch(dark, /--console-syn-comment/, "the comment token follows the theme itself");
});

test("each hued syntax colour is a dark step in light and a light step in dark", () => {
  for (const name of HUED) {
    const lightToken = declared(light, `console-syn-${name}`, "light");
    const darkToken = declared(dark, `console-syn-${name}`, "dark");
    assert.ok(step(lightToken) >= 50, `light ${name} (${lightToken}) is too pale for white paper`);
    assert.ok(step(darkToken) <= 40, `dark ${name} (${darkToken}) is too dark for the dark ground`);
  }
});

test("the five syntax colours stay distinct from each other in each theme", () => {
  for (const [label, scope] of [
    ["light", light],
    ["dark", dark],
  ] as const) {
    const tokens = HUED.map((name) => declared(scope, `console-syn-${name}`, label));
    const hues = tokens.map((t) => t.replace(/--\d+$/, ""));
    assert.equal(new Set(hues).size, hues.length, `${label} reuses a hue: ${tokens.join(", ")}`);
  }
});
