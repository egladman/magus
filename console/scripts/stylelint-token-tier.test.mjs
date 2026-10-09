import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";
import tokenTier from "../stylelint-token-tier.mjs";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function flagged(code, codeFilename) {
  const result = await stylelint.lint({ code, codeFilename, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/token-tier");
}

test("token tier accepts semantic tokens", async () => {
  const warnings = await flagged(`
    .console-a { z-index: var(--pf-t--global--z-index--md); }
    .console-b { border-width: var(--pf-t--global--border--width--strong); }
    .console-c { color: var(--pf-t--global--color--status--danger--default); }
    .console-d { padding: var(--pf-t--global--spacer--2xl); }
    .console-e { font-size: var(--pf-t--global--font--size--heading--h1); }
    .console-f { color: var(--console-accent); }
  `);

  assert.deepEqual(warnings, []);
});

test("token tier rejects a token that ends in a number, and says what to take", async () => {
  const warnings = await flagged(`
    .console-a { z-index: var(--pf-t--global--z-index--200); }
    .console-b { border-width: var(--pf-t--global--border--width--200); }
    .console-c { color: var(--pf-t--global--color--status--danger--100); }
    .console-d { padding: var(--pf-t--global--spacer--200); }
    .console-e { border-radius: var(--pf-t--global--border--radius--0); }
    .console-f { color: var(--pf-t--global--color--nonstatus--blue--300); }
  `);

  assert.equal(warnings.length, 6);
  assert.match(warnings[0].text, /z-index--xs \.\. --2xl/);
  assert.match(warnings[1].text, /border--width--regular/);
  assert.match(warnings[2].text, /status--\*--default or --hover/);
});

test("token tier rejects a palette token, whether or not it ends in a number", async () => {
  const warnings = await flagged(`
    .console-a { background: var(--pf-t--color--white); }
    .console-b { color: var(--pf-t--color--purple--50); }
  `);

  assert.equal(warnings.length, 2);
  assert.match(warnings[0].text, /palette token/);
});

test("token tier reads fallbacks and the property being declared", async () => {
  const warnings = await flagged(`
    .console-a { color: var(--console-accent, var(--pf-t--global--color--brand--200)); }
    .console-b { --pf-t--global--border--radius--100: 2px; }
  `);

  assert.equal(warnings.length, 2);
});

// A name only ends in a number when the last segment is all digits.
test("token tier does not take 2xl, h1 or a mid-name number for a base token", async () => {
  const warnings = await flagged(`
    .console-a { z-index: var(--pf-t--global--z-index--2xl); font-size: var(--pf-t--global--font--size--heading--h1); }
    .console-b { margin: var(--pf-t--global--spacer--3xl); }
  `);

  assert.deepEqual(warnings, []);
});

test("token tier lets tokens.css adapt the base layer, and nothing else", async () => {
  const code = ":root { --pf-t--global--color--brand--200: #3f6b60; --console-x: var(--pf-t--color--white); }";

  assert.deepEqual(await flagged(code, "/work/console/src/styles/tokens.css"), []);
  assert.equal((await flagged(code, "/work/console/src/styles/overrides.css")).length, 2);
});

test("token tier refuses an allowance with no reason or a file that is not a pattern", async () => {
  for (const definition of [{ file: /tokens\.css$/ }, { file: "tokens.css", reason: "x" }]) {
    const result = await stylelint.lint({
      code: ".a { z-index: var(--pf-t--global--z-index--200); }",
      config: {
        plugins: [tokenTier],
        rules: { "magus/token-tier": [true, { definitions: [definition] }] },
      },
    });

    assert.ok(result.results[0].invalidOptionWarnings.length > 0);
  }
});
