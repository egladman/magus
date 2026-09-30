// transcript.test.ts - the parser that reads back a captured body. Pure and DOM-free, so it
// runs directly under node. Run: `magus run test console`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { parseTranscript } from "./transcript";

// Byte-for-byte what internal/notes.captureBody writes. If the Go renderer changes shape this
// fixture is the thing that should go red, so keep it a literal rather than building it here.
const BODY = [
  "Captured from a review-thread on 2026-08-20T03:14:33Z.",
  "Session rev1, patch c7328485c4949ea355e25ec9cc501542.",
  "",
  "A transcript, not written prose: these are things people said, quoted, and nobody has revisited them since. Re-read the code before acting on any of it.",
  "",
  "cmd/magus/notes.go hunk 2",
  "-------------------------",
  "",
  "reviewer:",
  "",
  "Private default means this errors in a repo that only declares shared.",
  "",
  "internal/notes/capture.go hunk 1",
  "--------------------------------",
  "",
  "reviewer:",
  "",
  "Why derive anchors from the comment paths?",
  "",
  "claude (agent) (resolved):",
  "",
  "Because the caller would have to restate what the thread already says.",
].join("\n");

test("a captured body reads back as its entries", () => {
  const parsed = parseTranscript("review-thread", BODY);
  assert.ok(parsed, "the body magus wrote parses");

  assert.equal(parsed.entries.length, 3);
  assert.match(parsed.preamble, /A transcript, not written prose/);
  assert.doesNotMatch(parsed.preamble, /cmd\/magus/, "the preamble stops at the first heading");

  assert.deepEqual(
    parsed.entries.map((e) => [e.subject, e.locator, e.author, e.resolved]),
    [
      ["cmd/magus/notes.go", "hunk 2", "reviewer", false],
      ["internal/notes/capture.go", "hunk 1", "reviewer", false],
      ["internal/notes/capture.go", "hunk 1", "claude (agent)", true],
    ],
  );
  assert.equal(
    parsed.entries[2]?.body,
    "Because the caller would have to restate what the thread already says.",
  );
});

// The subject carries over to every entry under one heading, so a box can name its file
// without the reader scrolling back to the heading that introduced it.
test("entries inherit the heading they sit under", () => {
  const parsed = parseTranscript("review-thread", BODY);
  assert.ok(parsed);
  assert.equal(parsed.entries[1]?.subject, parsed.entries[2]?.subject);
});

// A kind this build does not know must reach the caller's verbatim fallback rather than being
// parsed by rules that predate it. That is the whole reason kind is a string on the wire.
test("an unknown source kind is not parsed", () => {
  assert.equal(parseTranscript("chat-log", BODY), null);
  assert.equal(parseTranscript("", BODY), null);
});

// Losing the boxes is cosmetic; losing the transcript is not. Anything that does not have the
// shape falls back rather than rendering as one empty box.
test("prose that is not a transcript falls back", () => {
  assert.equal(parseTranscript("review-thread", "Just some prose a person wrote."), null);
  assert.equal(parseTranscript("review-thread", ""), null);
  assert.equal(
    parseTranscript("review-thread", "A heading\n---------\n\nbut nobody said anything"),
    null,
    "a heading with no attributed entry under it is not a transcript",
  );
});

// A path is checked as a heading before it is checked as a speaker, so a subject that ends in
// a colon cannot be mistaken for an attribution line.
test("a heading wins over an attribution that looks like one", () => {
  const body = [
    "weird:path:name.go hunk 1",
    "-------------------------",
    "",
    "reviewer:",
    "",
    "still an entry",
  ].join("\n");
  const parsed = parseTranscript("review-thread", body);
  assert.ok(parsed);
  assert.equal(parsed.entries.length, 1);
  assert.equal(parsed.entries[0]?.subject, "weird:path:name.go");
  assert.equal(parsed.entries[0]?.author, "reviewer");
});

// A blank line inside a quoted comment is part of what was said and must survive.
test("a multi-paragraph comment keeps its blank line", () => {
  const body = [
    "a.go hunk 1",
    "-----------",
    "",
    "reviewer:",
    "",
    "first paragraph",
    "",
    "second paragraph",
  ].join("\n");
  const parsed = parseTranscript("review-thread", body);
  assert.ok(parsed);
  assert.equal(parsed.entries[0]?.body, "first paragraph\n\nsecond paragraph");
});
