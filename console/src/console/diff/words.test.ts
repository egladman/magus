import { test } from "node:test";
import assert from "node:assert/strict";

import { emphasis, pairForEmphasis } from "./words";

const slice = (s: string, r: { start: number; end: number } | null): string | null =>
  r === null ? null : s.slice(r.start, r.end);

test("one changed operator is emphasized down to the character that differs", () => {
  const before = "total += x";
  const after = "total -= x";
  const e = emphasis(before, after);
  // The "=" is common to both, so it is NOT part of the change. Emphasizing "+=" would be
  // marking a character that did not change, which is the habit that makes highlighting
  // untrustworthy at the scale where it matters.
  assert.equal(slice(before, e.before), "+");
  assert.equal(slice(after, e.after), "-");
});

test("a renamed identifier is emphasized whole, not just its differing letters", () => {
  const before = "const userName = read()";
  const after = "const userLabel = read()";
  const e = emphasis(before, after);
  // Not "Name"/"Label": cutting inside the token would highlight a fragment of a word.
  assert.equal(slice(before, e.before), "userName");
  assert.equal(slice(after, e.after), "userLabel");
});

test("an insertion emphasizes only the inserted span", () => {
  const before = "call(a, b)";
  const after = "call(a, extra, b)";
  const e = emphasis(before, after);
  assert.equal(slice(before, e.before), null, "nothing was removed from the old side");
  assert.ok((slice(after, e.after) ?? "").includes("extra"));
});

test("identical lines carry no emphasis", () => {
  const e = emphasis("same", "same");
  assert.equal(e.before, null);
  assert.equal(e.after, null);
});

test("a wholly different line carries no emphasis, because the row color already says it", () => {
  const e = emphasis("alpha", "9182");
  assert.equal(e.before, null);
  assert.equal(e.after, null);
});

test("an empty side is not emphasized", () => {
  const e = emphasis("", "added");
  assert.equal(e.before, null);
  assert.equal(e.after, null);
});

test("pairing is positional and only within an equal-length run", () => {
  assert.deepEqual(pairForEmphasis(["a", "b"], ["x", "y"]), [
    ["a", "x"],
    ["b", "y"],
  ]);
  // Unequal runs mean lines were added or removed rather than rewritten. Pairing across that
  // would invent a correspondence the patch does not contain.
  assert.deepEqual(pairForEmphasis(["a"], ["x", "y"]), []);
  assert.deepEqual(pairForEmphasis([], []), []);
});
