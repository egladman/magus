// cast.test.ts - a once-ever flourish has exactly one failure mode that matters: spending its single
// showing on a tab nobody is looking at, and then recording it as shown.

import assert from "node:assert/strict";
import { test } from "node:test";
import { shouldCast } from "./cast";

const opts = (o: Partial<Parameters<typeof shouldCast>[0]> = {}) => ({
  seen: false,
  hidden: false,
  reducedMotion: false,
  ...o,
});

test("a workspace seen before is never cast again", () => {
  assert.equal(shouldCast(opts({ seen: true })), "skip");
  assert.equal(shouldCast(opts({ seen: true, hidden: true })), "skip");
});

// The hazard the whole module is arranged around. A browser does not advance transitions in a hidden
// tab, so casting there would draw nothing - and if that counted as shown, the one showing is gone.
test("a hidden tab defers rather than skipping", () => {
  assert.equal(shouldCast(opts({ hidden: true })), "defer");
});

// Someone who asked for less movement did not ask for a ceremony. They still get the sigil; they just
// do not get it drawn, so there is nothing left owing and it is not deferred forever.
test("reduced motion skips outright, and is not deferred", () => {
  assert.equal(shouldCast(opts({ reducedMotion: true })), "skip");
  assert.equal(shouldCast(opts({ reducedMotion: true, hidden: true })), "skip");
});

test("a fresh workspace in a visible tab is cast", () => {
  assert.equal(shouldCast(opts()), "cast");
});

// Precedence matters: seen beats everything, so a return visit never defers and never waits on a
// visibility change that will not come.
test("seen wins over every other reason", () => {
  assert.equal(shouldCast({ seen: true, hidden: true, reducedMotion: true }), "skip");
});
