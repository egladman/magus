// query.test.ts - the #q= log query grammar. parseQuery is pure and DOM-free (query.ts),
// so it runs directly under node. This grammar is what the future logs SearchProvider.parse()
// reuses, so pinning it down here de-risks that wiring. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { parseQuery } from "./query";

test("empty input is the empty query", () => {
  assert.deepEqual(parseQuery(""), { groups: [], texts: [], empty: true });
  assert.deepEqual(parseQuery("   "), { groups: [], texts: [], empty: true });
});

test("target:/status: become group filters", () => {
  assert.deepEqual(parseQuery("target:build").groups, [{ key: "target", value: "build" }]);
  assert.deepEqual(parseQuery("status:fail").groups, [{ key: "status", value: "fail" }]);
});

test("step: is a line-level text term (value only, lowercased)", () => {
  const q = parseQuery("step:Compile");
  assert.deepEqual(q.groups, []);
  assert.deepEqual(q.texts, ["compile"]);
});

test("bare words are free text, AND-combined and lowercased", () => {
  assert.deepEqual(parseQuery("Error  Timeout").texts, ["error", "timeout"]);
});

test("a mixed query splits into groups and texts", () => {
  const q = parseQuery("target:Build ERROR");
  assert.deepEqual(q.groups, [{ key: "target", value: "build" }]);
  assert.deepEqual(q.texts, ["error"]);
  assert.equal(q.empty, false);
});

test("an unknown key falls back to free text (whole token, lowercased)", () => {
  assert.deepEqual(parseQuery("foo:Bar").texts, ["foo:bar"]);
  assert.deepEqual(parseQuery("foo:Bar").groups, []);
});

test("a known key with an empty value is free text, not a filter", () => {
  assert.deepEqual(parseQuery("target:").texts, ["target:"]);
  assert.deepEqual(parseQuery("target:").groups, []);
});

test("a leading colon is not a field term", () => {
  assert.deepEqual(parseQuery(":bar").texts, [":bar"]);
  assert.deepEqual(parseQuery(":bar").groups, []);
});
