// textsearch.test.ts - the shared full-text search engine (textsearch.ts). runSearch,
// getPositiveTerms, and the createTextSearch factory are pure and DOM-free, so they run
// directly under node. This pins the Datadog-style grammar (AND, -exclude, field:scope, quoted
// phrase, wildcard, ranking), the injected-source factory, and the generic entry type against a
// tiny in-memory index. describeQuery and buildSnippet are not exported (they are internal to
// the lib), so they are exercised through the factory's methods. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { createTextSearch, getPositiveTerms, runSearch, type TextSearchEntry } from "./textsearch";

// A caller's record type: the engine's minimal TextSearchEntry plus a url the ranker never
// reads but hands back on each result (proving the generic preserves the caller's fields).
interface DocEntry extends TextSearchEntry {
  url: string;
}

const INDEX: DocEntry[] = [
  {
    url: "modules/cache/",
    title: "Remote cache",
    tags: ["cache", "remote"],
    description: "Share build outputs across machines.",
    text: "The remote cache stores build outputs keyed by input hash.",
  },
  {
    url: "spells/build/",
    title: "build spell",
    tags: ["spell", "build"],
    description: "Compile a project.",
    text: "The build spell runs the compiler and caches the result.",
  },
  {
    url: "guides/graph/",
    title: "Graph explorer",
    tags: ["graph", "ui"],
    description: "Browse the knowledge graph.",
    text: "Explore targets and their dependencies visually.",
  },
  {
    url: "codes/mgs1002/",
    title: "MGS1002 duplicate spell",
    tags: ["diagnostics", "spell"],
    description: "Two spells share a name.",
    text: "A duplicate spell definition was found in the workspace.",
  },
];

const titles = (raw: string): string[] => runSearch(INDEX, raw).map((r) => r.entry.title);

// describeQuery and buildSnippet are lib-internal (not exported); reach them through a ready
// factory searcher. Both are stateless over the query, so one shared instance is fine.
const facade = createTextSearch(INDEX);

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

test("getPositiveTerms drops negated terms", () => {
  assert.deepEqual(getPositiveTerms("cache -remote build"), ["cache", "build"]);
});

test("buildSnippet centers on the first matched term with ellipses", () => {
  const long =
    "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda build tail more words follow after the needle so the window has room on both sides here";
  const s = facade.buildSnippet(long, ["build"]);
  assert.ok(s.includes("build"));
  assert.ok(s.startsWith("...")); // the term sits far enough in that the window opens after the start
});

test("describeQuery reports field scope, exclusion, phrase and wildcard", () => {
  assert.deepEqual(facade.describeQuery("tag:cache"), [
    { field: "tag", value: "cache", neg: false, phrase: false, wildcard: false },
  ]);
  assert.deepEqual(facade.describeQuery("-remote"), [
    { field: null, value: "remote", neg: true, phrase: false, wildcard: false },
  ]);
  assert.deepEqual(facade.describeQuery('"remote cache"'), [
    { field: null, value: "remote cache", neg: false, phrase: true, wildcard: false },
  ]);
  const wild = facade.describeQuery("build*");
  assert.equal(wild.length, 1);
  assert.equal(wild[0].wildcard, true);
});

test("describeQuery lists each AND term in order", () => {
  const parts = facade.describeQuery("cache build");
  assert.deepEqual(
    parts.map((p) => p.value),
    ["cache", "build"],
  );
});

test("createTextSearch over an injected array is ready immediately and preserves the record type", () => {
  const s = createTextSearch(INDEX);
  assert.equal(s.isReady(), true);
  assert.equal(s.entries(), INDEX);
  const hits = s.runSearch("cache");
  assert.deepEqual(
    hits?.map((r) => r.entry.title),
    ["Remote cache", "build spell"],
  );
  // The caller's url field (unknown to the ranker) rides through on the result.
  assert.equal(hits?.[0].entry.url, "modules/cache/");
  assert.deepEqual(s.getPositiveTerms("cache -remote"), ["cache"]);
});

test("createTextSearch injects an async loader, memoizes it, and searches once it lands", async () => {
  let calls = 0;
  const s = createTextSearch<DocEntry>(() => {
    calls++;
    return Promise.resolve(INDEX);
  });
  assert.equal(s.isReady(), false);
  assert.equal(s.runSearch("cache"), null); // null (not []) until the source loads
  const loaded = await s.load();
  await s.load(); // idempotent
  assert.equal(calls, 1);
  assert.equal(s.isReady(), true);
  assert.equal(loaded?.length, INDEX.length);
  assert.equal(s.runSearch("graph")?.[0].entry.title, "Graph explorer");
});

test("createTextSearch stays not-ready when the loader yields null, so a later load retries", async () => {
  let calls = 0;
  const s = createTextSearch<DocEntry>(() => {
    calls++;
    return calls === 1 ? null : INDEX; // first load fails, second succeeds
  });
  assert.equal(await s.load(), null);
  assert.equal(s.isReady(), false);
  assert.equal((await s.load())?.length, INDEX.length);
  assert.equal(s.isReady(), true);
});

test("createTextSearch recovers when a rejecting loader later succeeds (no inflight wedge)", async () => {
  let calls = 0;
  const s = createTextSearch<DocEntry>(() => {
    calls++;
    return calls === 1 ? Promise.reject(new Error("boom")) : INDEX; // first load rejects, second succeeds
  });
  await assert.rejects(() => s.load()); // the rejection propagates to the caller
  assert.equal(s.isReady(), false); // a rejected load must leave the searcher not-ready
  // The wedge bug: the rejected inflight promise was replayed forever. A retry must re-run the loader.
  assert.equal((await s.load())?.length, INDEX.length);
  assert.equal(calls, 2);
  assert.equal(s.isReady(), true);
});
