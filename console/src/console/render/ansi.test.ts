// ansi.test.ts - stripAnsi and parseAnsi are the log viewer's escape handling. The
// cases that matter are the non-SGR ones: magus writes cursor and scroll-margin
// escapes to a real terminal, and a log captured through a pty (script, docker -t,
// some CI runners) can carry them into a pane that must not render them as text.

import { test } from "node:test";
import assert from "node:assert/strict";
import { parseAnsi, stripAnsi } from "./ansi";

test("stripAnsi removes SGR color sequences", () => {
  assert.equal(stripAnsi("\x1b[31mfail\x1b[0m"), "fail");
});

test("stripAnsi removes non-SGR control sequences", () => {
  assert.equal(stripAnsi("\x1b[20;1H\x1b[Kfail"), "fail");
  assert.equal(stripAnsi("\x1b[1;18rboom"), "boom");
  assert.equal(stripAnsi("\x1b[2Jcleared"), "cleared");
  assert.equal(stripAnsi("\x1b[sSaved\x1b[u"), "Saved");
});

test("stripAnsi leaves ordinary bracketed text untouched", () => {
  assert.equal(stripAnsi("[fail] api build"), "[fail] api build");
});

test("parseAnsi splits a line into runs carrying the active SGR state", () => {
  const segs = parseAnsi("ok \x1b[31mbad\x1b[0m done");
  assert.deepEqual(
    segs.map((s) => s.text),
    ["ok ", "bad", " done"],
  );
  assert.ok(segs[1].cls.includes("console-render-ansi__fg--red"));
  assert.deepEqual(segs[0].cls, []);
  assert.deepEqual(segs[2].cls, []);
});

// The regression this guards: a cursor-position escape used to fall through the
// SGR-only matcher and be pushed into the DOM as literal text.
test("parseAnsi drops cursor control instead of emitting it as text", () => {
  const text = parseAnsi("\x1b[20;1H\x1b[Kboom")
    .map((s) => s.text)
    .join("");
  assert.equal(text, "boom");
  assert.ok(!text.includes("\x1b"));
});

test("parseAnsi keeps color state across an interleaved control sequence", () => {
  const colored = parseAnsi("\x1b[31mred\x1b[Kstill red").filter((s) => s.text.length > 0);
  for (const seg of colored) {
    assert.ok(seg.cls.includes("console-render-ansi__fg--red"));
  }
  assert.equal(colored.map((s) => s.text).join(""), "redstill red");
});

test("parseAnsi returns one empty run for an empty line", () => {
  assert.deepEqual(parseAnsi(""), [{ text: "", cls: [] }]);
});
