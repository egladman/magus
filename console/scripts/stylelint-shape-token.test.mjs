import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function flagged(code) {
  const result = await stylelint.lint({ code, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/shape-token");
}

test("shape token accepts the radius tokens, 0 and a percentage", async () => {
  const warnings = await flagged(`
    .console-a { border-radius: var(--pf-t--global--border--radius--tiny); }
    .console-b { border-radius: var(--pf-t--global--border--radius--pill); }
    .console-c { border-radius: var(--pf-t--global--border--radius--control--default); }
    .console-d { border-radius: var(--console-radius-micro); }
    .console-e { border-radius: 0; }
    .console-f { border-radius: 50%; }
    .console-g { border-end-start-radius: calc(var(--pf-t--global--border--radius--large) * 2); }
    .console-h { border-radius: var(--pf-t--global--border--radius--small) var(--pf-t--global--border--radius--small) 0 0; }
    .console-i { border-radius: inherit; }
  `);

  assert.deepEqual(warnings, []);
});

test("shape token rejects a radius written by hand or taken from a numbered step", async () => {
  const warnings = await flagged(`
    .console-a { border-radius: 2px; }
    .console-b { border-radius: 999px; }
    .console-c { border-radius: 0.12em; }
    .console-d { border-start-start-radius: 4px; }
    .console-e { border-radius: var(--pf-t--global--border--radius--200); }
    .console-f { border-radius: var(--pf-t--global--border--radius--small) 3px; }
    .console-g { border-radius: var(--pf-t--global--spacer--sm); }
  `);

  assert.equal(warnings.length, 7);
});

test("shape token accepts the border-width tokens and 0, in a shorthand or a longhand", async () => {
  const warnings = await flagged(`
    .console-a { border: var(--pf-t--global--border--width--regular) solid var(--pf-t--global--border--color--default); }
    .console-b { border-inline-start: var(--pf-t--global--border--width--extra-strong) solid transparent; }
    .console-c { border-block-start: var(--pf-t--global--border--width--main--default) dashed var(--console-accent); }
    .console-d { border: 0; border-inline: 0; border: none; }
    .console-e { border-width: 0; border-bottom-width: var(--pf-t--global--border--width--strong); }
    .console-f { border: var(--pf-t--global--border--width--regular) solid color-mix(in srgb, currentcolor 22%, transparent); }
    .console-g { border-color: var(--console-accent); border-style: solid; }
    .console-h { border-inline-start: var(--console-diff-bar) solid var(--console-accent); }
    .console-i { outline: var(--pf-t--global--border--width--strong) solid var(--console-accent); }
    .console-j { border-width: calc(-1 * var(--pf-t--global--border--width--regular)); }
  `);

  assert.deepEqual(warnings, []);
});

test("shape token rejects a border width written by hand", async () => {
  const warnings = await flagged(`
    .console-a { border: 1px solid var(--pf-t--global--border--color--default); }
    .console-b { border-bottom: 2px dashed var(--console-accent); }
    .console-c { border-inline: 0.3em solid transparent; }
    .console-d { border-width: 3px; }
    .console-e { border-top-width: thin; }
    .console-f { border-left: thick solid red; }
    .console-g { outline: 2px solid var(--console-accent); }
    .console-h { border-inline-start: var(--pf-t--global--border--width--200) solid var(--console-accent); }
    .console-i { border-width: var(--pf-t--global--spacer--sm); }
  `);

  assert.equal(warnings.length, 9);
});

test("shape token leaves a colour variable in a shorthand to the colour rules", async () => {
  const warnings = await flagged(`
    .console-a { border: var(--pf-t--global--border--width--regular) solid var(--agent-host-accent, var(--console-stone)); }
  `);

  assert.deepEqual(warnings, []);
});

test("shape token accepts the motion tokens, zero, and the near-zero reduced-motion idiom", async () => {
  const warnings = await flagged(`
    .console-a { transition: opacity var(--console-motion-fast) ease; }
    .console-b { transition: transform var(--pf-t--global--motion--duration--lg) var(--console-ease, ease), opacity var(--console-motion) ease; }
    .console-c { animation: spin var(--console-motion-spin) linear infinite; }
    .console-d { transition-duration: 0.01ms !important; animation-duration: 0.001ms !important; }
    .console-e { transition: none; animation: none; }
    .console-f { transition-duration: 0s; transition-delay: 0ms; }
    .console-g { animation-delay: calc(sibling-index() * var(--pf-t--global--motion--duration--xs)); }
    .console-h { transition: opacity var(--pf-t--global--motion--duration--fade--default) var(--pf-t--global--motion--timing-function--default); }
  `);

  assert.deepEqual(warnings, []);
});

test("shape token rejects a duration or delay written by hand", async () => {
  const warnings = await flagged(`
    .console-a { transition: opacity 0.18s ease; }
    .console-b { transition: transform 120ms; }
    .console-c { animation: flash 900ms ease-out; }
    .console-d { transition-duration: 200ms; }
    .console-e { animation-delay: 50ms; }
    .console-f { transition: opacity var(--console-motion, 190ms) ease; }
    .console-g { animation-delay: calc(sibling-index() * 50ms); }
  `);

  assert.equal(warnings.length, 7);
});

test("shape token ignores a time that is not in a motion property", async () => {
  const warnings = await flagged(`
    .console-a { --console-toast-lifetime: 5s; scroll-timeline: --x; will-change: transform; }
  `);

  assert.deepEqual(warnings, []);
});
