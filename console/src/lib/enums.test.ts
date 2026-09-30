// enums.test.ts - the word a protobuf enum value reads as. The real generated enums are the
// fixtures: the helper's whole claim is that it tracks the names the server ships.

import assert from "node:assert/strict";
import { test } from "node:test";
import { LifecycleState, Support, Verdict } from "@wire/tool/v1alpha1/tool_pb";
import { enumWord } from "./enums";

test("an underscored value name reads as spaced lowercase words", () => {
  assert.equal(enumWord(Verdict, Verdict.TOO_OLD, "unknown"), "too old");
  assert.equal(enumWord(Verdict, Verdict.TOO_NEW, "unknown"), "too new");
  assert.equal(enumWord(Verdict, Verdict.INSIDE, "unknown"), "inside");
});

test("a single-word value reads as that word", () => {
  assert.equal(enumWord(Support, Support.EOL, ""), "eol");
  assert.equal(enumWord(Support, Support.UNANNOUNCED, ""), "unannounced");
  assert.equal(enumWord(LifecycleState, LifecycleState.UNREACHED, "unwired"), "unreached");
});

test("UNSPECIFIED reads as the word the caller gave, including empty", () => {
  assert.equal(enumWord(Verdict, Verdict.UNSPECIFIED, "unknown"), "unknown");
  assert.equal(enumWord(LifecycleState, LifecycleState.UNSPECIFIED, "unwired"), "unwired");
  assert.equal(enumWord(Support, Support.UNSPECIFIED, ""), "");
});

test("an explicit UNKNOWN stays its own word, distinct from UNSPECIFIED", () => {
  assert.equal(enumWord(Support, Support.UNKNOWN, ""), "unknown");
  assert.equal(enumWord(Verdict, Verdict.UNKNOWN, "unset"), "unknown");
});

test("a value this build has no name for reads as the unspecified word", () => {
  assert.equal(enumWord(Verdict, 99, "unknown"), "unknown");
});
