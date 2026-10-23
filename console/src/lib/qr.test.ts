import test from "node:test";
import assert from "node:assert/strict";
import { encodeMatrix } from "./qr";

// matrixOf encodes text and fails the test when the encoder returns an error.
function matrixOf(text: string): number[][] {
  const result = encodeMatrix(text);
  assert.ok(result.ok, result.ok ? "" : result.error);
  return result.value;
}

// Version selection: a short string fits v1 (21x21); longer payloads step up.
test("qr picks a version whose size holds the payload", () => {
  const small = matrixOf("hi");
  assert.equal(small.length, 21, "short text should be version 1 (21x21)");

  const url = matrixOf("http://192.168.1.20:54321/console/#token=mgs_" + "a".repeat(49));
  // ~93 bytes needs version 5 (37x37) at EC level M (v4 holds 64, v5 holds 86...
  // actually 93 bytes needs v6). Assert the size formula holds and it is bigger.
  assert.equal((url.length - 17) % 4, 0, "size must be 4*version+17");
  assert.ok(url.length > 21, "a ~90 byte URL needs more than version 1");
});

// The largest supported version holds 213 payload bytes at level M (216 data
// codewords less the 20-bit header); one more byte is the user-facing failure.
test("qr fills the largest version and rejects input past it", () => {
  const full = matrixOf("a".repeat(213));
  assert.equal(full.length, 57, "213 bytes should be version 10 (57x57)");

  const tooLong = encodeMatrix("a".repeat(214));
  assert.equal(tooLong.ok, false);
  assert.ok(!tooLong.ok && tooLong.error.includes("214 bytes"), "error names the input size");
  assert.ok(!tooLong.ok && tooLong.error.includes("213 bytes"), "error names the limit");
});

// Multi-byte characters count by their UTF-8 length, not by character.
test("qr counts bytes, not characters, against the limit", () => {
  const result = encodeMatrix("é".repeat(107));
  assert.equal(result.ok, false, "107 two-byte characters are 214 bytes");
});

// Finder patterns: the three 7x7 corners must be present, each a solid ring with
// a 3x3 center. This is the structural signature a scanner locks onto.
test("qr places the three finder patterns", () => {
  const m = matrixOf("magus");
  const n = m.length;
  const corners = [
    [0, 0],
    [0, n - 7],
    [n - 7, 0],
  ];
  for (const [r0, c0] of corners) {
    // Outer ring dark, inner ring (offset 1) light, 3x3 core dark.
    assert.equal(m[r0][c0], 1, "finder top-left corner dark");
    assert.equal(m[r0 + 1][c0 + 1], 0, "finder inner ring light");
    assert.equal(m[r0 + 3][c0 + 3], 1, "finder center dark");
    assert.equal(m[r0 + 6][c0 + 6], 1, "finder bottom-right corner dark");
  }
});

// Timing patterns: row 6 and column 6 alternate 1,0,1,0 between the finders.
test("qr lays alternating timing patterns", () => {
  const m = matrixOf("magus");
  const n = m.length;
  for (let i = 8; i < n - 8; i++) {
    const want = i % 2 === 0 ? 1 : 0;
    assert.equal(m[6][i], want, "row-6 timing at " + i);
    assert.equal(m[i][6], want, "col-6 timing at " + i);
  }
});

// The dark module is always set (bottom-left of the top-right region: [4*v+9][8]).
test("qr sets the mandatory dark module", () => {
  const m = matrixOf("magus");
  const n = m.length;
  assert.equal(m[n - 8][8], 1, "dark module must be set");
});

// Every module is a bit: no cell is left unset (-1) after encoding.
test("qr fills every module", () => {
  const m = matrixOf("http://192.168.1.20:54321/console/#token=mgs_deadbeef");
  for (const row of m) {
    for (const v of row) {
      assert.ok(v === 0 || v === 1, "module must be 0 or 1, got " + v);
    }
  }
});

// Determinism: the same input yields the identical matrix (stable mask choice).
test("qr is deterministic", () => {
  const a = matrixOf("determinism check 12345");
  const b = matrixOf("determinism check 12345");
  assert.deepEqual(a, b);
});
