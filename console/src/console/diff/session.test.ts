import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchContext, hunkDigest, patchDigest } from "./session";

// The SAME golden vector internal/diff/session_test.go asserts.
//
// If these two drift the feature silently half-works: a hunk the person marked read in the
// browser still looks unread to an agent reading the same session, and neither side reports
// an error. Two tests over one literal turns that into a build failure.
test("hunkDigest matches the Go golden vector", async () => {
  assert.equal(await hunkDigest("a.go", [" ctx", "-old", "+new"]), "9a0125a4f7864894");
});

test("hunkDigest separates identical hunks in different files", async () => {
  const body = [" ctx", "-old", "+new"];
  assert.notEqual(await hunkDigest("a.go", body), await hunkDigest("b.go", body));
});

test("hunkDigest changes when the body does", async () => {
  assert.notEqual(
    await hunkDigest("a.go", ["-old", "+new"]),
    await hunkDigest("a.go", ["-old", "+newer"]),
  );
});

test("patchDigest matches the daemon's SHA-256 first-16-byte identity", async () => {
  assert.equal(await patchDigest("abc"), "ba7816bf8f01cfea414140de5dae2223");
});

test("context requests carry the reviewed patch identity", async () => {
  const realFetch = globalThis.fetch;
  let url = "";
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    url = String(input);
    return new Response(JSON.stringify({ path: "a.go", as_of: "snapshot-a", start: 2, lines: [] }));
  }) as typeof fetch;
  try {
    await fetchContext("127.0.0.1:7391", "a.go", "snapshot-a", 3, 3, new AbortController().signal);
    const query = new URL(url).searchParams;
    assert.equal(query.get("path"), "a.go");
    assert.equal(query.get("as_of"), "snapshot-a");
  } finally {
    globalThis.fetch = realFetch;
  }
});
