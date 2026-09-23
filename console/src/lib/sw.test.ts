import assert from "node:assert/strict";
import test from "node:test";
import { buildIdOf, watchServedBuild } from "./sw";

test("buildIdOf reads a stamped worker and ignores an unstamped one", () => {
  assert.equal(buildIdOf('const BUILD_ID = "abc123def456";\n'), "abc123def456");
  assert.equal(buildIdOf('const BUILD_ID = "unstamped";\n'), null);
  assert.equal(buildIdOf("self.addEventListener('fetch', () => {});"), null);
});

// A daemon restarted onto a rebuilt console serves a new sw.js while this page keeps running the
// build it loaded. The watch reports that once, rather than letting the old bundle render empty.
test("a daemon that starts serving another build asks for one reload", async () => {
  const realFetch = globalThis.fetch;
  const builds = ["aaaaaaaaaaaa", "aaaaaaaaaaaa", "bbbbbbbbbbbb", "bbbbbbbbbbbb"];
  globalThis.fetch = (async () =>
    new Response('const BUILD_ID = "' + (builds.shift() ?? "bbbbbbbbbbbb") + '";', {
      status: 200,
    })) as typeof fetch;
  const stale: [string, string][] = [];
  const stop = watchServedBuild(
    "http://127.0.0.1:7391/console/sw.js",
    (r, s) => stale.push([r, s]),
    5,
  );
  try {
    await new Promise((r) => setTimeout(r, 60));
    assert.deepEqual(stale, [["aaaaaaaaaaaa", "bbbbbbbbbbbb"]]);
  } finally {
    stop();
    globalThis.fetch = realFetch;
  }
});
