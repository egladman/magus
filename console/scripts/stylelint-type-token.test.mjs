import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function flagged(code) {
  const result = await stylelint.lint({ code, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/type-token");
}

test("type token accepts the PatternFly size and weight tokens", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--pf-t--global--font--size--body--sm); font-weight: var(--pf-t--global--font--weight--body--bold); }
    .console-b { font-size: var(--pf-t--global--font--size--body--default); font-weight: var(--pf-t--global--font--weight--body--default); }
    .console-c { font-size: var(--pf-t--global--font--size--body--lg); }
    .console-d { font-size: var(--pf-t--global--font--size--heading--h4); font-weight: var(--pf-t--global--font--weight--heading--bold); }
    .console-e { font-size: var(--pf-t--global--font--size--xs); }
    .console-f { font-size: var(--pf-t--global--font--size--2xl); }
  `);

  assert.deepEqual(warnings, []);
});

test("type token accepts the console tokens", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--console-control-font-size); }
    .console-b { font-size: var(--console-label-size); font-weight: var(--console-label-weight); }
    .console-c { font-size: var(--console-chip-size); font-weight: var(--console-chip-weight); }
  `);

  assert.deepEqual(warnings, []);
});

test("type token rejects a literal size, in any unit", async () => {
  const warnings = await flagged(`
    .console-a { font-size: 0.7rem; }
    .console-b { font-size: 11px; }
    .console-c { font-size: 12px; }
    .console-d { font-size: small; }
    .console-e { font-size: smaller; }
    .console-f { font-size: calc(var(--pf-t--global--font--size--body--sm) * 0.9); }
  `);

  assert.equal(warnings.length, 6);
});

test("type token rejects a literal weight, numeric or keyword", async () => {
  const warnings = await flagged(`
    .console-a { font-weight: 600; }
    .console-b { font-weight: bold; }
    .console-c { font-weight: normal; }
  `);

  assert.equal(warnings.length, 3);
});

// font--size--100 .. --800 are the primitives the named steps are built from, as are the
// font--weight--100 .. --400 steps. PatternFly's own guidance is to never use a token ending in a number.
test("type token rejects the numbered primitives", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--pf-t--global--font--size--100); }
    .console-b { font-weight: var(--pf-t--global--font--weight--300); }
  `);

  assert.equal(warnings.length, 2);
});

test("type token rejects another variable, and the wrong family", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--log-font-size); }
    .console-b { font-size: var(--pf-t--global--spacer--sm); }
    .console-c { font-size: var(--console-control-block-size); }
    .console-d { font-weight: var(--pf-t--global--font--size--body--sm); }
  `);

  assert.equal(warnings.length, 4);
});

test("type token judges a var() fallback by the same rule", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--pf-t--global--font--size--body--sm, var(--pf-t--global--font--size--sm)); }
    .console-b { font-size: var(--pf-t--global--font--size--body--sm, 0.7rem); }
  `);

  assert.equal(warnings.length, 1);
  assert.equal(warnings[0].line, 3);
});

// Relative sizes are the one literal that cannot make text smaller than the text around it.
test("type token accepts a relative size that cannot shrink below the parent", async () => {
  const warnings = await flagged(`
    .console-a { font-size: inherit; font-weight: inherit; }
    .console-b { font-size: 1em; }
    .console-c { font-size: 1.15em; }
    .console-d { font-size: 100%; }
    .console-e { font-size: 125%; }
    .console-f { font-size: larger; }
  `);

  assert.deepEqual(warnings, []);
});

test("type token rejects a relative size that can shrink below the parent", async () => {
  const warnings = await flagged(`
    .console-a { font-size: 0.85em; }
    .console-b { font-size: 90%; }
    .console-c { font-size: 0.99em; }
    .console-d { font-size: 1rem; }
    .console-e { font-size: initial; }
  `);

  assert.equal(warnings.length, 5);
});

// An em weight does not exist, so the relative allowance is for sizes only.
test("type token does not extend the relative allowance to weight", async () => {
  const warnings = await flagged(`
    .console-a { font-weight: 1em; }
    .console-b { font-weight: 100%; }
  `);

  assert.equal(warnings.length, 2);
});

// magus/label-token already pins both properties on an uppercase run to the label and chip tokens,
// so a second report from this rule would only say the same thing twice.
test("type token leaves an uppercase run to the label token", async () => {
  const warnings = await flagged(`
    .console-runs__facet-head {
      font-size: 0.66rem;
      font-weight: 600;
      text-transform: uppercase;
    }
  `);

  assert.deepEqual(warnings, []);
});

test("type token still checks a rule whose transform is not uppercase", async () => {
  const warnings = await flagged(`
    .console-notes-app__store-consequence {
      font-size: 0.7rem;
      text-transform: none;
    }
  `);

  assert.equal(warnings.length, 1);
});

test("type token checks a rule inside a media query and ignores @font-face", async () => {
  const warnings = await flagged(`
    @font-face { font-family: "x"; font-weight: 400; src: local("x"); }
    @media (pointer: coarse) {
      .console-a { font-size: 0.7rem; }
    }
  `);

  assert.equal(warnings.length, 1);
});

test("type token ignores properties that only look like type", async () => {
  const warnings = await flagged(`
    .console-a { letter-spacing: 0.04em; font-style: italic; font-variant-numeric: tabular-nums; }
  `);

  assert.deepEqual(warnings, []);
});

test("type token accepts exactly the weight tokens PatternFly defines", async () => {
  const warnings = await flagged(`
    .console-a { font-weight: var(--pf-t--global--font--weight--body--default); }
    .console-b { font-weight: var(--pf-t--global--font--weight--body--bold); }
    .console-c { font-weight: var(--pf-t--global--font--weight--heading--default); }
    .console-d { font-weight: var(--pf-t--global--font--weight--heading--bold); }
    .console-e { font-weight: var(--pf-t--global--font--weight--body--legacy); }
    .console-f { font-weight: var(--pf-t--global--font--weight--heading--legacy); }
    .console-g { font-weight: var(--pf-t--global--font--weight--body--bold--legacy); }
    .console-h { font-weight: var(--pf-t--global--font--weight--heading--bold--legacy); }
  `);

  assert.deepEqual(warnings, []);
});

test("type token rejects a weight name PatternFly does not define", async () => {
  const warnings = await flagged(`
    .console-a { font-weight: var(--pf-t--global--font--weight--body--default--legacy); }
    .console-b { font-weight: var(--pf-t--global--font--weight--heading--default--legacy); }
    .console-c { font-weight: var(--pf-t--global--font--weight--body--semibold); }
  `);

  assert.equal(warnings.length, 3);
});

test("type token accepts the named heading sizes and rejects an invented one", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--pf-t--global--font--size--heading--h1); }
    .console-b { font-size: var(--pf-t--global--font--size--heading--lg); }
    .console-c { font-size: var(--pf-t--global--font--size--heading--2xl); }
    .console-d { font-size: var(--pf-t--global--font--size--heading--huge); }
    .console-e { font-size: var(--pf-t--global--font--size--heading--h7); }
  `);

  assert.equal(warnings.length, 2);
});

test("type token takes line-height from the two PatternFly tokens", async () => {
  const warnings = await flagged(`
    .console-a { line-height: var(--pf-t--global--font--line-height--body); }
    .console-b { line-height: var(--pf-t--global--font--line-height--heading); }
    .console-c { line-height: var(--console-diff-row-line-height); }
    .console-d { line-height: inherit; }
    .console-e { line-height: normal; }
  `);

  assert.deepEqual(warnings, []);
});

// 0 collapses the line box and 1 is the glyph's em; neither is leading for running text.
test("type token allows line-height 0 and 1 and rejects every other number or length", async () => {
  const warnings = await flagged(`
    .console-a { line-height: 0; }
    .console-b { line-height: 1; }
    .console-c { line-height: 1.5; }
    .console-d { line-height: 1.35; }
    .console-e { line-height: 24px; }
    .console-f { line-height: 1rem; }
    .console-g { line-height: var(--pf-t--global--font--line-height--200); }
    .console-h { line-height: var(--pf-t--global--spacer--md); }
  `);

  assert.equal(warnings.length, 6);
});

test("type token takes font-family from the three PatternFly family tokens", async () => {
  const accepted = await flagged(`
    .console-a { font-family: var(--pf-t--global--font--family--body); }
    .console-b { font-family: var(--pf-t--global--font--family--heading); }
    .console-c { font-family: var(--pf-t--global--font--family--mono); }
    .console-d { font-family: inherit; }
  `);
  const rejected = await flagged(`
    .console-a { font-family: monospace; }
    .console-b { font-family: "Menlo", monospace; }
    .console-c { font-family: var(--pf-t--global--font--family--300); }
    .console-d { font-family: var(--pf-t--global--font--family--mono, monospace); }
  `);

  assert.deepEqual(accepted, []);
  assert.equal(rejected.length, 4);
});

test("type token reads the font shorthand piece by piece", async () => {
  const accepted = await flagged(`
    .console-a { font: inherit; }
    .console-b { font: var(--pf-t--global--font--size--body--sm) / var(--pf-t--global--font--line-height--body) var(--pf-t--global--font--family--body); }
    .console-c { font: italic var(--pf-t--global--font--weight--body--bold) var(--pf-t--global--font--size--body--default) var(--pf-t--global--font--family--mono); }
  `);
  const rejected = await flagged(`
    .console-a { font: 12px sans-serif; }
    .console-b { font: bold var(--pf-t--global--font--size--body--sm); }
    .console-c { font: var(--pf-t--global--font--body--sm); }
    .console-d { font: var(--pf-t--global--spacer--sm); }
  `);

  assert.deepEqual(accepted, []);
  assert.equal(rejected.length, 4);
});

test("type token still checks leading and family on an uppercase run", async () => {
  const warnings = await flagged(`
    .console-a {
      font-size: 0.66rem;
      text-transform: uppercase;
      line-height: 1.7;
      font-family: monospace;
    }
  `);

  assert.equal(warnings.length, 2);
});
