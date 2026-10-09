// theme-color.test.ts - the PWA colour has one source. index.html's two theme-color metas, the
// manifest's theme_color and background_color, and the colours theme.ts writes when the reader picks
// a theme by hand had three different pairs of values, so the browser chrome around the installed app
// was a different grey from the app's own chrome. All of them are --console-chrome: PatternFly's
// secondary background in light (gray 10) and #242424 in dark (tokens.css).

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const LIGHT = "#f2f2f2";
const DARK = "#242424";

// The suite runs with the console directory as its working directory, as apps.test.ts relies on.
const read = (rel: string): string => readFileSync(rel, "utf8");

test("index.html names the chrome colour for each scheme", () => {
  const html = read("index.html");
  assert.match(html, new RegExp(`content="${LIGHT}" media="\\(prefers-color-scheme: light\\)"`));
  assert.match(html, new RegExp(`content="${DARK}" media="\\(prefers-color-scheme: dark\\)"`));
});

test("the manifest agrees with the light chrome, which the metas override per scheme", () => {
  const manifest = JSON.parse(read("manifest.webmanifest")) as Record<string, string>;
  assert.equal(manifest.theme_color, LIGHT);
  assert.equal(manifest.background_color, LIGHT);
});

test("theme.ts writes the same two colours when the theme is picked by hand", () => {
  const ts = read("src/theme.ts");
  assert.match(ts, new RegExp(`CHROME_LIGHT = "${LIGHT}"`));
  assert.match(ts, new RegExp(`CHROME_DARK = "${DARK}"`));
  assert.doesNotMatch(ts, /#ffffff|#13171f/i, "the old, different pair is gone");
});

test("tokens.css still defines the chrome as those colours", () => {
  const tokens = read("src/styles/tokens.css");
  assert.match(
    tokens,
    /--console-chrome: var\(--pf-t--global--background--color--secondary--default\)/,
  );
  assert.match(tokens, new RegExp(`--console-chrome: ${DARK}`));
});
