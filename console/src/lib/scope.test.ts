// scope.test.ts - which records a workspace scope admits. The rule decides what a scoped tab shows,
// so getting it wrong means someone stares at an empty board while their work runs somewhere else.

import assert from "node:assert/strict";
import { test } from "node:test";
import { ALL_WORKSPACES, inScope, shortName } from "./scope";

const ACME = "/Users/eli/Repos/acme";
const MAGUS = "/Users/eli/Repos/magus";

// The default. Nothing is hidden until someone chooses a scope, so a fresh console can never be
// silently showing a subset - the "where is my stuff" failure needs an unasked-for filter to happen.
test("the daemon-wide scope admits everything", () => {
  assert.equal(inScope(ACME, ALL_WORKSPACES), true);
  assert.equal(inScope(MAGUS, ALL_WORKSPACES), true);
  assert.equal(inScope("", ALL_WORKSPACES), true);
});

test("a workspace scope admits its own records and refuses another's", () => {
  assert.equal(inScope(ACME, ACME), true);
  assert.equal(inScope(MAGUS, ACME), false);
});

// activity.proto: a record with no workspace is a daemon-wide action (an MCP call) rather than a
// record whose workspace is unknown. Hiding it under a scope would claim it belongs somewhere else,
// so it shows everywhere - the same reasoning that keeps pool and cache readings daemon-wide.
test("an unattributed record shows in every scope", () => {
  assert.equal(inScope("", ACME), true);
  assert.equal(inScope("", MAGUS), true);
});

// The menu shows the last segment because that is what people call a workspace; the full root rides
// as a tooltip, since two checkouts can share a leaf name.
test("a workspace is named by its last path segment", () => {
  assert.equal(shortName(ACME), "acme");
  assert.equal(shortName("/Users/eli/Repos/magus/"), "magus");
  assert.equal(shortName(ALL_WORKSPACES), "All workspaces");
  assert.equal(shortName("acme"), "acme");
});
