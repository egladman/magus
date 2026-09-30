import { test } from "node:test";
import assert from "node:assert/strict";

import { languageFor, tokenize } from "./syntax";

const slice = (s: string, t: { start: number; end: number }): string => s.slice(t.start, t.end);

// Finds a token of a class and fails loudly when it is absent, so a missing token reads as a
// clear failure rather than a null-assertion crash.
const only = (text: string, toks: ReturnType<typeof tokenize>, cls: string): string => {
  const t = toks.find((x) => x.cls === cls);
  assert.ok(t, `expected a ${cls} token in ${JSON.stringify(text)}`);
  return slice(text, t);
};
const classes = (s: string, lang: Parameters<typeof tokenize>[1]): string[] =>
  tokenize(s, lang).map((t) => `${t.cls}:${slice(s, t)}`);

test("languageFor maps by extension, and magusfile.buzz by name", () => {
  assert.equal(languageFor("magus.go"), "go");
  assert.equal(languageFor("console/src/a.ts"), "ts");
  assert.equal(languageFor("magusfile.buzz"), "buzz");
  assert.equal(languageFor("spells/go/spell.buzz"), "buzz");
  assert.equal(languageFor("README"), "none");
  assert.equal(languageFor("Dockerfile"), "none");
  assert.equal(languageFor("weird.unknownext"), "none");
});

test("keywords, strings and numbers are classed", () => {
  const line = "func Sum(xs []int) int { return 42 }";
  const got = classes(line, "go");
  assert.ok(got.includes("kw:func"), got.join(" | "));
  assert.ok(got.includes("kw:return"), got.join(" | "));
  assert.ok(got.includes("num:42"), got.join(" | "));
  assert.ok(got.includes("fn:Sum"), "a name followed by ( reads as the thing being invoked");
});

test("a line comment runs to end of line", () => {
  const line = 'x := 1 // and the rest "quoted" is still comment';
  assert.equal(
    only(line, tokenize(line, "go"), "com"),
    '// and the rest "quoted" is still comment',
  );
});

test("a closed block comment is exact", () => {
  const line = "a /* inline */ b";
  assert.equal(only(line, tokenize(line, "go"), "com"), "/* inline */");
});

// The dominant shape in this codebase: long comment blocks whose opener is on another line, so
// it cannot be derived from the hunk. Refusing to color them leaves most of the screen unstyled.
test("an unclosed block comment colors to end of line", () => {
  const line = "  /* the opener, continuing below";
  assert.equal(only(line, tokenize(line, "go"), "com"), "/* the opener, continuing below");
});

test("a continuation line starting with * is a comment body", () => {
  const line = " * so the palette follows the theme";
  const got = tokenize(line, "go");
  assert.equal(got.length, 1);
  assert.equal(got[0]?.cls, "com");
  assert.equal(only(line, got, "com"), "* so the palette follows the theme");
});

// Under-highlighting is invisible; a confidently wrong color teaches the reader to distrust
// the whole column. A fragment must stay plain.
test("an unterminated string is left plain rather than painted to end of line", () => {
  const line = 'msg := "an opener with no close';
  const got = tokenize(line, "go");
  assert.equal(
    got.find((t) => t.cls === "str"),
    undefined,
  );
});

test("a multiplication is not mistaken for a comment continuation", () => {
  const line = "total := a * b";
  const got = tokenize(line, "go");
  assert.equal(
    got.find((t) => t.cls === "com"),
    undefined,
  );
});

test("an unknown language and an empty line tokenize to nothing", () => {
  assert.deepEqual(tokenize("anything at all", "none"), []);
  assert.deepEqual(tokenize("", "go"), []);
});

test("ranges are ordered and never overlap", () => {
  const line = 'const a = "s" + 12 // note';
  const got = tokenize(line, "ts");
  for (let i = 1; i < got.length; i++) {
    assert.ok((got[i]?.start ?? 0) >= (got[i - 1]?.end ?? 0), "tokens must not overlap");
  }
  assert.ok(
    got.every((t) => t.end <= line.length),
    "no token may run past the line",
  );
});
