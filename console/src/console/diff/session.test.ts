import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchContext } from "./session";

// The SAME golden vector internal/diff/session_test.go asserts.
//
// If these two drift the feature silently half-works: a hunk the person marked read in the
// browser still looks unread to an agent reading the same session, and neither side reports
// an error. Two tests over one literal turns that into a build failure.
// The digest tests that were here are gone with the code they covered. hunkDigest and
// patchDigest were TypeScript reimplementations of internal/diff's, kept in step by a golden
// vector pasted from Go - which is the arrangement that let the two readers drift in the first
// place. The daemon computes both now and ships them, so there is nothing here to pin.

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
