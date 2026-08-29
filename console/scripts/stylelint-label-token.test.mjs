import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function lint(code) {
  return stylelint.lint({ code, configFile });
}

test("label token accepts the label recipe", async () => {
  const result = await lint(`
    .console-runs__facet-head {
      font-size: var(--console-label-size);
      font-weight: var(--console-label-weight);
      letter-spacing: var(--console-label-tracking);
      text-transform: uppercase;
    }
  `);

  assert.equal(result.errored, false);
});

test("label token accepts the chip recipe", async () => {
  const result = await lint(`
    .console-render-badge {
      font-size: var(--console-chip-size);
      font-weight: var(--console-chip-weight);
      letter-spacing: var(--console-chip-tracking);
      text-transform: uppercase;
    }
  `);

  assert.equal(result.errored, false);
});

test("label token rejects a hand-picked size, weight and tracking", async () => {
  const result = await lint(`
    .console-runs__facet-head {
      font-size: 0.66rem;
      font-weight: 600;
      letter-spacing: 0.04em;
      text-transform: uppercase;
    }
  `);

  assert.equal(result.errored, true);
  const flagged = result.results[0].warnings
    .filter((warning) => warning.rule === "magus/label-token")
    .length;
  assert.equal(flagged, 3);
});

// Mixing the two recipes in one rule is not what this catches. The rule pins the VALUES to the
// vocabulary; which of the two a given element belongs to is the author's call.
test("label token leaves a rule with no uppercase alone", async () => {
  const result = await lint(`
    .console-runs__row-cmd {
      font-size: 0.88rem;
      letter-spacing: 0.04em;
    }
  `);

  assert.equal(result.errored, false);
});

// text-transform carries other values, and none of them make a run a label.
test("label token ignores a non-uppercase transform", async () => {
  const result = await lint(`
    .console-notes-app__store-consequence {
      font-size: 0.7rem;
      text-transform: none;
    }
  `);

  assert.equal(result.errored, false);
});
