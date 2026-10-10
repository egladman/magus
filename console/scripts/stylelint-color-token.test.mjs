import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";
import colorToken from "../stylelint-color-token.mjs";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;

async function flagged(code, codeFilename) {
  const result = await stylelint.lint({ code, codeFilename, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/color-token");
}

test("color token accepts the PatternFly and console colour tokens", async () => {
  const warnings = await flagged(`
    .console-a { color: var(--pf-t--global--text--color--subtle); background: var(--console-accent); }
    .console-b { border-color: var(--console-status-ok); fill: var(--console-node-spell); }
    .console-c { background: color-mix(in srgb, var(--console-accent) 20%, transparent); }
    .console-d { color: currentcolor; outline-color: transparent; }
  `);

  assert.deepEqual(warnings, []);
});

test("color token rejects a hex colour of every length", async () => {
  const warnings = await flagged(`
    .console-a { color: #fff; }
    .console-b { color: #ffff; }
    .console-c { color: #8891a4; }
    .console-d { color: #8891a4cc; }
    .console-e { border: 1px solid #ABC; }
  `);

  assert.equal(warnings.length, 5);
});

test("color token rejects rgb, rgba, hsl and hsla", async () => {
  const warnings = await flagged(`
    .console-a { color: rgb(0 0 0 / 50%); }
    .console-b { background: rgba(0, 0, 0, 0.5); }
    .console-c { color: hsl(165 40% 40%); }
    .console-d { color: HSLA(165, 40%, 40%, 0.5); }
    .console-e { background: linear-gradient(to right, transparent, rgba(0, 0, 0, 0.2)); }
  `);

  assert.equal(warnings.length, 5);
});

test("color token rejects a literal in a var() fallback and in a colour mix", async () => {
  const warnings = await flagged(`
    .console-a { color: var(--console-accent, #3f6b60); }
    .console-b { background: color-mix(in srgb, #3f6b60 20%, transparent); }
  `);

  assert.equal(warnings.length, 2);
});

test("color token reads a literal in any property, not only colour properties", async () => {
  const warnings = await flagged(`
    .console-a { outline: 2px solid #3f6b60; }
    .console-b { text-decoration-color: rgb(1 2 3); }
    .console-c { border-inline-start: 3px solid #fff; }
  `);

  assert.equal(warnings.length, 3);
});

test("color token does not mistake an id, a fragment or text for a colour", async () => {
  const warnings = await flagged(`
    #console-titlebar { background: var(--console-chrome); }
    .console-a { fill: url(#arrow); mask: url("icons.svg#mask"); }
    .console-b { content: "#fff"; }
    .console-c { grid-area: a; animation-name: fade-in; width: calc(100% - 3px); }
    .console-d { font-family: rgb-mono, monospace; }
  `);

  assert.deepEqual(warnings, []);
});

test("color token reads a colour percent-encoded inside a data URI", async () => {
  const warnings = await flagged(`
    .console-a { background-image: url("data:image/svg+xml,%3Csvg stroke='%238891a4'/%3E"); }
    .console-b { background-image: url("data:image/svg+xml,%3Csvg stroke='currentColor'/%3E"); }
    .console-c { background-image: url("icons/search.svg"); }
  `);

  assert.equal(warnings.length, 1);
  assert.equal(warnings[0].line, 2);
});

test("color token accepts box-shadow none and the PatternFly shadow tokens", async () => {
  const warnings = await flagged(`
    .console-a { box-shadow: none; }
    .console-b { box-shadow: var(--pf-t--global--box-shadow--md); }
    .console-c { box-shadow: var(--pf-t--global--box-shadow--sm--bottom), var(--pf-t--global--box-shadow--lg); }
    .console-d { box-shadow: var(--pf-t--global--box-shadow--lg--left); }
    .console-e { box-shadow: inherit; }
  `);

  assert.deepEqual(warnings, []);
});

test("color token rejects a raw box-shadow, even one with no colour literal", async () => {
  const warnings = await flagged(`
    .console-a { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2); }
    .console-b { box-shadow: 0 0 0 2px var(--console-accent); }
    .console-c { box-shadow: inset 0 1px 0 var(--pf-t--global--border--color--default); }
    .console-d { box-shadow: 0 0 0 1px; }
  `);

  assert.equal(warnings.length, 4);
});

// The pieces a shadow is assembled from are not shadows.
test("color token rejects a box-shadow built from a PatternFly part", async () => {
  const warnings = await flagged(`
    .console-a { box-shadow: var(--pf-t--global--box-shadow--blur--sm); }
    .console-b { box-shadow: var(--pf-t--global--box-shadow--color--md--default); }
    .console-c { box-shadow: var(--pf-t--global--box-shadow--md), 0 0 2px #000; }
    .console-d { box-shadow: var(--pf-t--global--box-shadow--md, 0 1px 2px rgba(0, 0, 0, 0.2)); }
  `);

  assert.equal(warnings.length, 4);
});

test("color token reports a raw shadow once, not once per reading", async () => {
  const warnings = await flagged(`
    .console-a { box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2); }
  `);

  assert.equal(warnings.length, 1);
});


// A palette has to be written somewhere. tokens.css is the one file that may define colours in
// custom properties, and only there.
test("color token lets tokens.css define colours in custom properties", async () => {
  const warnings = await flagged(
    `
    :root {
      --console-spruce: #3f6b60;
      --pf-t--global--color--brand--200: rgb(63 107 96);
    }
  `,
    "/repo/console/src/styles/base/tokens.css",
  );

  assert.deepEqual(warnings, []);
});

test("color token still rejects a colour tokens.css writes into a real property", async () => {
  const warnings = await flagged(
    `
    :root { color: #3f6b60; box-shadow: 0 0 2px #000; }
    [data-control-size] { background: rgb(1 2 3); }
  `,
    "/repo/console/src/styles/base/tokens.css",
  );

  assert.equal(warnings.length, 3);
});

test("color token rejects a colour defined in a custom property anywhere else", async () => {
  const warnings = await flagged(
    `
    :root { --console-node-spell: #3f6b60; }
  `,
    "/repo/console/src/apps/graph/graph.css",
  );

  assert.equal(warnings.length, 1);
});

test("color token refuses a definition allowance with no reason or a string file", async () => {
  for (const definitions of [[{ file: /tokens\.css$/ }], [{ file: "tokens.css", reason: "x" }]]) {
    const result = await stylelint.lint({
      code: ".a { color: #fff; }",
      config: { plugins: [colorToken], rules: { "magus/color-token": [true, { definitions }] } },
    });

    assert.ok(result.results[0].invalidOptionWarnings.length > 0);
  }
});

test("color token rejects a named colour in a colour property and in a custom property", async () => {
  const warnings = await flagged(`
    .console-a { color: red; }
    .console-b { background: white url("x.svg") no-repeat; }
    .console-c { border: var(--pf-t--global--border--width--regular) solid Gray; }
    .console-d { background: linear-gradient(to right, transparent, Black); }
    .console-e { fill: rebeccapurple; stroke: tan; }
    .console-f { --console-x: olive; }
  `);

  assert.equal(warnings.length, 7);
});

test("color token accepts transparent, currentcolor and a name that is not a colour here", async () => {
  const warnings = await flagged(`
    .console-a { color: currentcolor; background: transparent; border-color: transparent; }
    .console-b { font-family: var(--pf-t--global--font--family--mono); grid-area: header; }
    .console-c { animation-name: red; content: "red"; background: url("red.svg") var(--console-red); }
    .console-d { color: var(--console-plum); }
  `);

  assert.deepEqual(warnings, []);
});

test("color token lets tokens.css define a named colour in a custom property only", async () => {
  const code = ":root { --console-x: olive; } .a { color: olive; }";
  const warnings = await flagged(code, "/repo/console/src/styles/base/tokens.css");

  assert.equal(warnings.length, 1);
});

test("color token allows text-shadow none only", async () => {
  const accepted = await flagged(`
    .console-a { text-shadow: none; }
    .console-b { text-shadow: inherit; }
  `);
  const rejected = await flagged(`
    .console-a { text-shadow: 0 1px 0 var(--console-accent); }
    .console-b { text-shadow: var(--pf-t--global--box-shadow--md); }
  `);

  assert.deepEqual(accepted, []);
  assert.equal(rejected.length, 2);
});

test("color token rejects a drop-shadow filter and leaves other filters alone", async () => {
  const accepted = await flagged(`
    .console-a { filter: blur(2px); backdrop-filter: blur(7px); }
    .console-b { filter: none; }
  `);
  const rejected = await flagged(`
    .console-a { filter: drop-shadow(0 1px 2px var(--console-accent)); }
    .console-b { filter: blur(1px) drop-shadow(0 0 4px); }
  `);

  assert.deepEqual(accepted, []);
  assert.equal(rejected.length, 2);
});
