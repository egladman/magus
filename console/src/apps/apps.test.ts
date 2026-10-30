// apps.test.ts - the apps/ directory is the one list of launcher tiles; everything else that names
// the apps reads it, and this holds each of those readers to it in both directions. It reads the
// tree from disk rather than a list of its own, so adding an app directory is what it checks.
//
// Paths are relative to the console/ project root: the test target runs node from there.

import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { appSegments } from "../../scripts/app-stubs.mjs";
import { createStore } from "../lib/store";
import { startDemo } from "./dashboard/demo";
import { initialState } from "./dashboard/state";
import { APPS } from "./index";
import { isWholeMotion } from "./manifest";

const APPS_DIR = "src/apps";

const dirs = readdirSync(APPS_DIR, { withFileTypes: true })
  .filter((e) => e.isDirectory())
  .map((e) => e.name)
  .sort();

const sorted = (xs: Iterable<string>): string[] => [...xs].sort();

// Every served segment the manifests declare: each app's path and its mode segments.
const segments = sorted(
  APPS.flatMap((app) => [...(app.path ? [app.path] : []), ...Object.keys(app.modes ?? {})]),
);

test("every app directory carries a manifest and an entry", () => {
  assert.ok(dirs.length > 0, "no app directories under " + APPS_DIR);
  for (const dir of dirs) {
    assert.ok(existsSync(join(APPS_DIR, dir, "app.ts")), `apps/${dir} has no app.ts`);
    assert.ok(existsSync(join(APPS_DIR, dir, "main.ts")), `apps/${dir} has no main.ts`);
  }
});

test("apps/index.ts imports exactly the app directories", () => {
  const imported = [
    ...readFileSync(join(APPS_DIR, "index.ts"), "utf8").matchAll(/from "\.\/([^/"]+)\/app"/g),
  ].map((m) => m[1] ?? "");
  assert.deepEqual(sorted(imported), dirs);
  // The bundle and stub paths are built from the id, so it has to be the directory's name.
  assert.deepEqual(sorted(APPS.map((app) => app.id)), dirs);
  assert.equal(new Set(segments).size, segments.length, "two apps claim one path segment");
});

test("the build bundles exactly the apps the shell loads lazily", () => {
  // The magusfile globs src/apps/*/main.ts and skips an app.ts carrying this literal.
  const buzz = readFileSync("magusfile.buzz", "utf8");
  assert.ok(buzz.includes('fs\\glob("src/apps/*/main.ts")'), "the build no longer globs the apps");
  assert.ok(buzz.includes('"kind: \\"shell\\""'), "the build no longer keys on kind: shell");
  for (const app of APPS) {
    const source = readFileSync(join(APPS_DIR, app.id, "app.ts"), "utf8");
    assert.equal(
      source.includes('kind: "shell"'),
      app.load.kind === "shell",
      `${app.id}: the build and the manifest disagree on whether it has a bundle`,
    );
    if (app.load.kind === "shell") continue;
    // gen/<dir>/<file>.css is built from src/apps/<dir>/<file>.css.
    assert.ok(existsSync(join(APPS_DIR, app.load.css)), `${app.id}: no source for ${app.load.css}`);
    assert.equal(
      existsSync(join(APPS_DIR, app.id, "scaffold.html")),
      app.load.kind === "page",
      `${app.id}: a page app lifts a scaffold.html, a module app has none`,
    );
  }
});

test("the hosted stubs name exactly the manifests' segments", async () => {
  assert.deepEqual(await appSegments(APPS_DIR), segments);
});

test("the server's link vocabulary names exactly the manifests' segments", () => {
  // Parsed as text: Go is not something this test can load. One line, one literal.
  const line = readFileSync("../internal/service/console/url.go", "utf8")
    .split("\n")
    .find((l) => l.startsWith("var KnownApps = "));
  assert.ok(line, "no single-line KnownApps in url.go");
  const known = [...line.matchAll(/"([^"]+)"/g)].map((m) => m[1] ?? "");
  assert.deepEqual(sorted(known), segments);
});

test("each glyph carries the motion its manifest names", () => {
  for (const app of APPS) {
    const marked = [...app.glyph.matchAll(/data-motion="([^"]+)"/g)].map((m) => m[1]);
    const want = app.motion && !isWholeMotion(app.motion) ? [app.motion] : [];
    assert.deepEqual(marked, want, `${app.id}: glyph motion markers`);
  }
});

// A server-only app goes here, keyed by id or id/mode, with the reason.
const DEMO_OPT_OUT: Readonly<Record<string, string>> = {};

function demoSources(): { name: string; file: string }[] {
  return APPS.flatMap((app) => {
    if (app.load.kind === "shell") return [];
    return [
      { name: app.id, file: join(APPS_DIR, app.id, "main.ts") },
      ...Object.keys(app.modes ?? {}).map((seg) => ({
        name: `${app.id}/${seg}`,
        file: join(APPS_DIR, app.id, seg, "main.ts"),
      })),
    ];
  });
}

test("every app and mode with a bundle opens a demo or says why not", () => {
  const sources = demoSources();
  for (const { name, file } of sources) {
    if (name in DEMO_OPT_OUT) continue;
    assert.ok(existsSync(file), `${name}: no ${file}`);
    assert.ok(
      /\bwantsDemo\(/.test(readFileSync(file, "utf8")),
      `${name}: ${file} never asks wantsDemo; add a demo or list it in DEMO_OPT_OUT`,
    );
  }
  for (const name of Object.keys(DEMO_OPT_OUT)) {
    assert.ok(
      sources.some((s) => s.name === name),
      `DEMO_OPT_OUT names ${name}, which is not an app or mode`,
    );
  }
});

test("demo fixtures reach for no network", () => {
  const files = readdirSync(APPS_DIR, { recursive: true, encoding: "utf8" }).filter((p) =>
    /(^|\/)demo\.ts$/.test(p),
  );
  assert.ok(files.length > 0, "no demo.ts under " + APPS_DIR);
  for (const f of files) {
    const code = readFileSync(join(APPS_DIR, f), "utf8").replace(/\/\/.*$/gm, "");
    assert.ok(
      !/\b(fetch|XMLHttpRequest|WebSocket|EventSource|sendBeacon)\b/.test(code),
      `${f} names a network API`,
    );
  }
});

test("the dashboard demo fills every field the live feed does", () => {
  const host = globalThis as { window?: unknown };
  const had = host.window;
  host.window = globalThis;
  const store = createStore(initialState());
  const handle = startDemo(store);
  try {
    for (const [key, value] of Object.entries(store.get())) {
      if (key === "liveHost") continue; // null on purpose: no host to link to
      const unset = value === null || (Array.isArray(value) && value.length === 0);
      assert.ok(!unset, `the dashboard demo leaves ${key} empty, so its tile shows nothing`);
    }
  } finally {
    handle.stop();
    host.window = had;
  }
});
