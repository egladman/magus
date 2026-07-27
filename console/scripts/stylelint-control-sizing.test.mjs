import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function lint(code) {
  return stylelint.lint({ code, configFile });
}

test("shared control sizing accepts the shared token", async () => {
  const result = await lint(`
    [data-control-size="compact"] button {
      height: var(--console-control-block-size);
    }
  `);

  assert.equal(result.errored, false);
});

test("shared control sizing rejects a hand-rolled size", async () => {
  const result = await lint(`
    [data-control-size="compact"] button {
      height: 2rem;
    }
  `);

  assert.equal(result.errored, true);
  assert.ok(result.results[0].warnings.some((warning) => warning.rule === "magus/control-size-token"));
});
