import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";
import spacerToken from "../stylelint-spacer-token.mjs";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function flagged(code) {
  const result = await stylelint.lint({ code, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/spacer-token");
}

test("spacer token accepts 0, auto, a percentage and the spacer tokens", async () => {
  const warnings = await flagged(`
    .console-a { padding: 0; margin: 0 auto; }
    .console-b { padding-inline: 50%; margin-block-start: 12.5%; }
    .console-c { gap: var(--pf-t--global--spacer--sm) var(--pf-t--global--spacer--md); }
    .console-d { padding: var(--pf-t--global--spacer--control--vertical--default) var(--pf-t--global--spacer--control--horizontal--compact); }
    .console-e { row-gap: var(--pf-t--global--spacer--gap--group-to-group--vertical--default); }
    .console-f { inset: 0; inset-inline-start: auto; inset-block: var(--pf-t--global--spacer--xs); }
    .console-g { column-gap: var(--pf-t--global--spacer--2xl); margin: -0px; }
  `);

  assert.deepEqual(warnings, []);
});

test("spacer token accepts the console spacing tokens", async () => {
  const warnings = await flagged(`
    .console-a { padding-inline: var(--console-pad); gap: var(--console-gap); }
    .console-b { padding-block: var(--console-shell-gutter); }
    .console-c { padding-inline: var(--log-pad) var(--notes-pad); }
  `);

  assert.deepEqual(warnings, []);
});

test("spacer token accepts a calc built only from those", async () => {
  const warnings = await flagged(`
    .console-a { padding: calc(var(--pf-t--global--spacer--sm) * 2); }
    .console-b { margin-inline: calc(-1 * var(--console-pad)); }
    .console-c { padding-block: calc(var(--pf-t--global--spacer--sm) + var(--pf-t--global--spacer--xs)); }
    .console-d { gap: calc(var(--console-gap) / 2) calc((var(--console-gap) + 0) * 3); }
    .console-e { padding-inline: calc(100% - var(--console-pad)); }
    .console-f { padding: max(var(--console-pad), var(--pf-t--global--spacer--md)); }
  `);

  assert.deepEqual(warnings, []);
});

test("spacer token rejects a literal length, in any unit", async () => {
  const warnings = await flagged(`
    .console-a { padding: 0.5rem; }
    .console-b { margin: 8px; }
    .console-c { gap: 0.4em; }
    .console-d { padding-inline-start: 7ch; }
    .console-e { inset-block-start: 1vh; }
    .console-f { column-gap: 2px; }
  `);

  assert.equal(warnings.length, 6);
});

// One bad component sinks the whole shorthand.
test("spacer token rejects a shorthand with one literal component", async () => {
  const warnings = await flagged(`
    .console-a { padding: 0 0.5rem; }
    .console-b { margin: var(--pf-t--global--spacer--sm) 2px; }
    .console-c { padding: 2.5rem var(--console-pad); }
  `);

  assert.equal(warnings.length, 3);
});

test("spacer token rejects a calc that carries a literal length", async () => {
  const warnings = await flagged(`
    .console-a { padding: calc(var(--pf-t--global--spacer--sm) + 2px); }
    .console-b { padding-inline: calc(0.4rem + var(--console-plan-depth, 0) * 0.6rem); }
    .console-c { margin: calc(-1 * 0.5rem); }
    .console-d { gap: min(1rem, var(--console-gap)); }
  `);

  assert.equal(warnings.length, 4);
});

// 0.15rem is 0.15 times a root size, and 0.5 times something else is a different number again.
test("spacer token does not read a near-zero length as zero", async () => {
  const warnings = await flagged(`
    .console-a { padding: 0.5; }
    .console-b { padding-block: 0.05rem; }
    .console-c { margin: 05px; }
  `);

  assert.equal(warnings.length, 3);
});

test("spacer token accepts a zero with a unit, which is still zero", async () => {
  const warnings = await flagged(`
    .console-a { padding: 0px; margin: 0rem 0; gap: 0.0em; }
  `);

  assert.deepEqual(warnings, []);
});

// spacer--100 .. --800 are the primitives the named steps are built from. PatternFly's own
// guidance is to never use a token ending in a number.
test("spacer token rejects the numbered primitives, other variables and non-spacer tokens", async () => {
  const warnings = await flagged(`
    .console-a { padding: var(--pf-t--global--spacer--200); }
    .console-b { padding: var(--bp-u); }
    .console-c { gap: var(--pf-v6-c-card--PaddingBlockStart); }
    .console-d { margin: var(--console-app-bar-h); }
    .console-e { gap: var(--pf-t--global--font--size--body--sm); }
  `);

  assert.equal(warnings.length, 5);
});

test("spacer token judges a var() fallback by the same rule", async () => {
  const warnings = await flagged(`
    .console-a { padding: var(--console-pad, var(--pf-t--global--spacer--md)); }
    .console-b { padding: var(--console-pad, 1rem); }
  `);

  assert.equal(warnings.length, 1);
  assert.equal(warnings[0].line, 3);
});

test("spacer token accepts the global keywords", async () => {
  const warnings = await flagged(`
    .console-a { padding: inherit; margin: initial; gap: unset; inset: revert; row-gap: normal; }
  `);

  assert.deepEqual(warnings, []);
});

test("spacer token watches the longhands and logical properties, and only them", async () => {
  const warnings = await flagged(`
    .console-a { padding-top: 1rem; padding-block-end: 1rem; margin-right: 1rem; margin-inline: 1rem; }
    .console-b { inset-inline-end: 1rem; row-gap: 1rem; column-gap: 1rem; grid-gap: 1rem; }
    .console-c { width: 1rem; top: 1rem; border-width: 1px; line-height: 1.5; scroll-margin: 1rem; letter-spacing: 0.04em; }
    .console-d { --console-card-pad: 1rem; --gap: 8px; }
  `);

  assert.equal(warnings.length, 8);
});

test("spacer token checks a rule inside a media query", async () => {
  const warnings = await flagged(`
    @media (pointer: coarse) {
      .console-a { padding: 0.5rem; }
    }
  `);

  assert.equal(warnings.length, 1);
});

// The one named allowance: spacing that is data. Everything else stays under the rule.
test("spacer token allows the declared geometry, and only for its selector and property", async () => {
  const warnings = await flagged(`
    .u-legend { margin: auto; padding: 4px; }
    .uplot .u-series > * { padding: 4px; }
    .console-diff-row--comment { padding-inline-start: 7ch; }
    .console-diff-row--outline:not([data-head]) { padding-inline-start: 13ch; }
    .console-diff-row--comment[data-reply] { padding-inline-start: 11ch; }
    .console-plan-list__item { padding-inline: calc(0.4rem + var(--console-plan-depth, 0) * 0.6rem) 0.5rem; }
    .console-plan-list__goal { padding-inline-start: calc(0.4rem + var(--console-plan-depth, 0) * 0.6rem); }
  `);

  assert.deepEqual(warnings, []);
});

test("spacer token holds the declared geometry to its selector and property", async () => {
  const warnings = await flagged(`
    .console-diff-row--comment { padding-block: 2px; }
    .console-diff-row { padding-inline-start: 7ch; }
    .console-diff-row--comment-extra { padding-inline-start: 7ch; }
    .console-plan-list__goal-criteria { padding-inline: 0.5rem; }
    .console-plan-list__item { gap: 0.4rem; }
    .console-log-row, .u-legend { padding: 4px; }
    .console-uplot-like { padding: 4px; }
  `);

  assert.equal(warnings.length, 7);
});

test("spacer token refuses an allowance with no reason", async () => {
  const result = await stylelint.lint({
    code: ".a { padding: 1rem; }",
    config: {
      plugins: [spacerToken],
      rules: { "magus/spacer-token": [true, { geometry: [{ selector: /^\.a$/ }] }] },
    },
  });

  assert.ok(result.results[0].invalidOptionWarnings.length > 0);
});

test("spacer token refuses an allowance that is not a selector pattern", async () => {
  const result = await stylelint.lint({
    code: ".a { padding: 1rem; }",
    config: {
      plugins: [spacerToken],
      rules: { "magus/spacer-token": [true, { geometry: [{ selector: ".a", reason: "x" }] }] },
    },
  });

  assert.ok(result.results[0].invalidOptionWarnings.length > 0);
});

test("spacer token with no allowance reports every literal", async () => {
  const result = await stylelint.lint({
    code: ".u-legend { padding: 4px; }",
    config: { plugins: [spacerToken], rules: { "magus/spacer-token": true } },
  });

  assert.equal(result.results[0].warnings.length, 1);
});
