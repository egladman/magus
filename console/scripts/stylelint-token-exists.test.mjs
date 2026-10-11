import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";
import { patternflyNames } from "../stylelint-pf.mjs";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function flagged(code, codeFilename) {
  const result = await stylelint.lint({ code, codeFilename, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/token-exists");
}

test("the names come from the installed PatternFly sheets", () => {
  const { tokens, properties } = patternflyNames();

  assert.ok(tokens.has("--pf-t--global--font--size--body--sm"));
  assert.ok(tokens.has("--pf-t--global--spacer--control--vertical--compact"));
  assert.ok(properties.has("--pf-v6-c-button--PaddingBlockStart"));
  assert.ok(properties.has("--pf-v6-l-gallery--GridTemplateColumns"));
  assert.ok(tokens.size > 500, "the base sheet declares hundreds of tokens");
});

test("token exists accepts real tokens and component properties", async () => {
  const warnings = await flagged(`
    .console-a { font-size: var(--pf-t--global--font--size--body--sm); }
    .console-b { --pf-v6-c-button--PaddingBlockStart: var(--pf-t--global--spacer--control--vertical--compact); }
    .console-c { color: var(--console-accent); padding: var(--not-pf-at-all); }
  `);

  assert.deepEqual(warnings, []);
});

test("token exists rejects a token PatternFly does not define, and names the real one", async () => {
  const warnings = await flagged(`
    .console-a { font: var(--pf-t--global--font--body--sm); }
    .console-b { background: var(--pf-t--global--background--color--status--warning--default); }
  `);

  assert.equal(warnings.length, 2);
  assert.match(warnings[0].text, /did you mean --pf-t--global--font--size--body--sm/);
});

test("token exists rejects a component property PatternFly does not define", async () => {
  const warnings = await flagged(`
    .console-a {
      --pf-v6-c-button--PaddingBlock: 0;
      --pf-v6-c-button--PaddingInline: 0;
    }
    .console-b { padding: var(--pf-v6-c-button--PaddingBlock); }
  `);

  assert.equal(warnings.length, 3);
});

test("token exists reads a var() fallback", async () => {
  const warnings = await flagged(`
    .console-a { color: var(--console-accent, var(--pf-t--global--color--missing--default)); }
  `);

  assert.equal(warnings.length, 1);
});

test("token exists applies in tokens.css as well", async () => {
  const warnings = await flagged(
    ":root { --pf-t--global--border--radius--tiny: 2px; --pf-t--global--border--radius--teeny: 1px; }",
    "/work/console/src/styles/base/tokens.css",
  );

  assert.equal(warnings.length, 1);
});
