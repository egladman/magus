import assert from "node:assert/strict";
import test from "node:test";
import stylelint from "stylelint";

const configFile = new URL("../stylelint.config.mjs", import.meta.url).pathname;
const appFile = "/repo/console/src/apps/logs/logs.css";

async function flagged(code, codeFilename = appFile) {
  const result = await stylelint.lint({ code, codeFilename, configFile });
  return result.results[0].warnings.filter((warning) => warning.rule === "magus/container-query");
}

test("container query rejects a viewport width query in an app stylesheet", async () => {
  const warnings = await flagged(`
    @media (max-width: 40rem) { .console-a { display: none; } }
    @media (min-width: 40rem) { .console-a { display: block; } }
    @media (width >= 40rem) { .console-a { display: block; } }
    @media (width<40rem) { .console-a { display: block; } }
    @media (400px <= width <= 700px) { .console-a { display: block; } }
    @media screen and (max-width:40rem) and (hover: hover) { .console-a { display: block; } }
    @media (min-device-width: 40rem) { .console-a { display: block; } }
  `);

  assert.equal(warnings.length, 7);
});

test("container query keeps the pointer, hover, prefers-* and print queries", async () => {
  const warnings = await flagged(`
    @media (pointer: coarse) { .console-a { min-block-size: 44px; } }
    @media (hover: none) { .console-a { opacity: 1; } }
    @media (prefers-reduced-motion: reduce) { .console-a { animation: none; } }
    @media (prefers-color-scheme: dark) { .console-a { color-scheme: dark; } }
    @media print { .console-a { display: none; } }
    @media (min-resolution: 2dppx) { .console-a { border-width: 0.5px; } }
    @media (orientation: portrait) and (min-height: 30rem) { .console-a { display: grid; } }
  `);

  assert.deepEqual(warnings, []);
});

test("container query accepts @container", async () => {
  const warnings = await flagged(`
    .console-pane { container-type: inline-size; }
    @container (max-width: 40rem) { .console-a { display: none; } }
    @container pane (inline-size < 30rem) { .console-a { display: none; } }
  `);

  assert.deepEqual(warnings, []);
});

test("container query names the offending query", async () => {
  const warnings = await flagged("@media (max-width: 40rem) { .console-a { display: none; } }");

  assert.equal(warnings.length, 1);
  assert.match(warnings[0].text, /@media \(max-width: 40rem\)/);
  assert.match(warnings[0].text, /@container/);
});

// The shell frame is not an app: it is the window, and a viewport query is the right question there.
test("container query leaves files outside src/apps alone", async () => {
  const code = "@media (max-width: 40rem) { .console-a { display: none; } }";

  assert.deepEqual(await flagged(code, "/repo/console/src/styles/console.css"), []);
  assert.deepEqual(await flagged(code, "/repo/console/src/render/render.css"), []);
  assert.deepEqual(await flagged(code, "/repo/console/src/appsish/x.css"), []);
  assert.equal((await flagged(code, "/repo/console/src/apps/dashboard/plan/plan.css")).length, 1);
});

test("container query is silent for code with no file", async () => {
  const result = await stylelint.lint({
    code: "@media (max-width: 40rem) { .console-a { display: none; } }",
    configFile,
  });

  assert.deepEqual(
    result.results[0].warnings.filter((warning) => warning.rule === "magus/container-query"),
    [],
  );
});
