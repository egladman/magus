import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import test from "node:test";

const fixtures = new URL("./fixtures/", import.meta.url);

function analyze(name) {
  return execFileSync(process.execPath, ["tools/analyze.mjs", new URL(name, fixtures).pathname], {
    cwd: new URL("../", import.meta.url).pathname,
    encoding: "utf8",
  });
}

test("analyze detects a planted paired effect", () => {
  assert.match(analyze("results-synthetic.json"), /REAL REAL/);
});

test("analyze leaves noise indistinguishable", () => {
  assert.match(analyze("results-noise.json"), /NOT DISTINGUISHABLE NOT DISTINGUISHABLE/);
});
