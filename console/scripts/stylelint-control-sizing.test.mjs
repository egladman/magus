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

// A token-sized control MUST be able to zero its own padding, or the rule has no satisfiable form:
// the height wants the token and the padding is watched, and no value is both.
test("shared control sizing accepts zeroed padding beside the token", async () => {
  const result = await lint(`
    [data-control-size] .pf-v6-c-button {
      block-size: var(--console-control-block-size);
      padding-block: 0;
    }
  `);

  assert.equal(result.errored, false);
});

test("shared control sizing still rejects a hand-picked padding", async () => {
  const result = await lint(`
    [data-control-size] .pf-v6-c-button {
      padding-block: 0.15rem;
    }
  `);

  assert.equal(result.errored, true);
});

// The escape hatch used to be spelled --pf-v6-c-(button|form-control), so a toggle group or a tab
// strip could set its own PF padding custom property under an opted-in group and pass.
test("shared control sizing reaches any PF component's padding custom property", async () => {
  const result = await lint(`
    [data-control-size] .pf-v6-c-toggle-group__button {
      --pf-v6-c-toggle-group__button--PaddingBlockStart: 0.15rem;
    }
  `);

  assert.equal(result.errored, true);
});
