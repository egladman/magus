import { test } from "node:test";
import assert from "node:assert/strict";
import { hunkDigest } from "./session";

// The SAME golden vector internal/review/session_test.go asserts.
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
