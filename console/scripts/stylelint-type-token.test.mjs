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
    .console-a { line-height: 1.5; letter-spacing: 0.04em; font-family: monospace; font: inherit; }
  `);

  assert.deepEqual(warnings, []);
});
