// docsearch.test.ts - the ported documentation-search grammar (docsearch.ts). runSearch,
// positiveTerms, and snippet are pure and DOM-free, so they run directly under node. This
// pins the ported Datadog-style grammar (AND, -exclude, field:scope, quoted phrase,
// wildcard, ranking) against a tiny in-memory index. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { runSearch, positiveTerms, snippet, type DocSearchEntry } from "./docsearch";

const INDEX: DocSearchEntry[] = [
  { url: "modules/cache/", title: "Remote cache", tags: ["cache", "remote"], description: "Share build outputs across machines.", text: "The remote cache stores build outputs keyed by input hash." },
  { url: "spells/build/", title: "build spell", tags: ["spell", "build"], description: "Compile a project.", text: "The build spell runs the compiler and caches the result." },
  { url: "guides/graph/", title: "Graph explorer", tags: ["graph", "ui"], description: "Browse the knowledge graph.", text: "Explore targets and their dependencies visually." },
  { url: "codes/mgs1002/", title: "MGS1002 duplicate spell", tags: ["diagnostics", "spell"], description: "Two spells share a name.", text: "A duplicate spell definition was found in the workspace." },
];

const titles = (raw: string): string[] => runSearch(INDEX, raw).map((r) => r.entry.title);

test("a bare word AND-matches across all fields", () => {
  assert.deepEqual(titles("cache"), ["Remote cache", "build spell"]);
});

test("multiple bare words are AND-ed", () => {
  // "build" hits both spell and cache records; "compiler" only the build spell body.
  assert.deepEqual(titles("build compiler"), ["build spell"]);
});

test("-term excludes matches", () => {
  const got = titles("spell -duplicate");
  assert.ok(got.includes("build spell"));
  assert.ok(!got.includes("MGS1002 duplicate spell"));
});

test("tag: is whole-tag equality, not substring", () => {
  assert.deepEqual(titles("tag:remote"), ["Remote cache"]);
  // "rem" is a substring of the "remote" tag but not the whole tag, so it does not match.
  assert.deepEqual(titles("tag:rem"), []);
});

test("title: scopes the term to the title field", () => {
  // "graph" appears in the graph record's title and the mgs record's body; title: keeps only the former.
  assert.deepEqual(titles("title:graph"), ["Graph explorer"]);
});

test("a quoted phrase must appear contiguously", () => {
  assert.deepEqual(titles('"remote cache"'), ["Remote cache"]);
  // Both words appear in the cache record but never contiguously in this order.
  assert.deepEqual(titles('"outputs remote"'), []);
});

test("a trailing wildcard matches a prefix", () => {
  const got = titles("dup*");
  assert.deepEqual(got, ["MGS1002 duplicate spell"]);
});

test("field:wildcard on a tag anchors the whole tag", () => {
  assert.deepEqual(titles("tag:diag*"), ["MGS1002 duplicate spell"]);
});

test("title matches outrank body-only matches", () => {
  // "graph" is a title hit for the explorer and a body hit for nothing else here.
  const ranked = runSearch(INDEX, "graph");
  assert.equal(ranked[0].entry.title, "Graph explorer");
});

test("OR unions the two sides", () => {
  const got = titles("graph OR duplicate");
  assert.ok(got.includes("Graph explorer"));
  assert.ok(got.includes("MGS1002 duplicate spell"));
});

test("a pure exclusion matches nothing (needs a positive term)", () => {
  assert.deepEqual(titles("-cache"), []);
});

test("typo-tolerant title subsequence rescues a near-miss", () => {
  // "grph" is not a substring of any title but is a subsequence of "Graph explorer".
  assert.deepEqual(titles("grph"), ["Graph explorer"]);
});

test("positiveTerms drops negated terms", () => {
  assert.deepEqual(positiveTerms("cache -remote build"), ["cache", "build"]);
});

test("snippet centers on the first matched term with ellipses", () => {
  const long = "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda build tail more words follow after the needle so the window has room on both sides here";
  const s = snippet(long, ["build"]);
  assert.ok(s.includes("build"));
  assert.ok(s.startsWith("...")); // the term sits far enough in that the window opens after the start
});
