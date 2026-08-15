// words.ts - intra-line emphasis: WHICH PART of a changed line changed.
//
// A line diff says "this line is different". On a line where one identifier moved, one
// argument was added, or one string changed, that leaves the reader to spot the difference by
// eye across two nearly identical rows - which is the single most common act of reading a
// diff and the one every tool used to make you do manually. Zed, GitHub, and delta all
// highlight it; this is the same idea at the smallest honest scope.
//
// The algorithm is deliberately NOT a full word-level diff. It takes the common prefix and
// the common suffix on TOKEN boundaries and emphasises the span between them. That is exact
// when a line has one edit region - overwhelmingly the common case - and it degrades to
// "the whole middle changed" otherwise, which is true rather than merely plausible. A real
// LCS would be more precise on multi-region edits and would also, on the lines where it
// disagrees with the eye, produce confident nonsense; the cheap version is never wrong, only
// sometimes coarse.

/// A half-open [start, end) range of the line's text to emphasise.
export interface Span {
  readonly start: number;
  readonly end: number;
}

// Token boundaries: identifier runs stay whole so a rename highlights the whole name rather
// than the three letters it happens to share with the old one.
const WORDISH = /[A-Za-z0-9_$]/;

function isWordChar(c: string): boolean {
  return WORDISH.test(c);
}

// backToBoundary walks an index back to the start of the token it lands inside, so a common
// prefix never cuts an identifier in half.
function backToBoundary(s: string, i: number): number {
  let n = i;
  while (n > 0 && isWordChar(s[n - 1] ?? "") && isWordChar(s[n] ?? "")) n--;
  return n;
}

// forwardToBoundary is the same for a suffix start.
function forwardToBoundary(s: string, i: number): number {
  let n = i;
  while (n < s.length && isWordChar(s[n] ?? "") && isWordChar(s[n - 1] ?? "")) n++;
  return n;
}

/// emphasis returns the changed span of each side, or null for a side with nothing to mark.
///
/// Both nulls means the two lines are identical, or differ so completely that emphasising
/// everything would be noise rather than signal - in either case the plain add/delete colour
/// already says all there is to say.
export function emphasis(
  before: string,
  after: string,
): { before: Span | null; after: Span | null } {
  if (before === after || before === "" || after === "") {
    return { before: null, after: null };
  }

  const max = Math.min(before.length, after.length);
  let p = 0;
  while (p < max && before[p] === after[p]) p++;
  p = backToBoundary(before, p) === backToBoundary(after, p) ? backToBoundary(before, p) : 0;

  let s = 0;
  while (s < max - p && before[before.length - 1 - s] === after[after.length - 1 - s]) s++;
  const bTail = forwardToBoundary(before, before.length - s);
  const aTail = forwardToBoundary(after, after.length - s);

  const b: Span = { start: p, end: Math.max(p, bTail) };
  const a: Span = { start: p, end: Math.max(p, aTail) };

  // If the "changed" span is the entire line on both sides, emphasis adds nothing the row
  // colour did not already carry, so say so by returning nothing.
  if (b.start === 0 && b.end === before.length && a.start === 0 && a.end === after.length) {
    return { before: null, after: null };
  }
  return {
    before: b.end > b.start ? b : null,
    after: a.end > a.start ? a : null,
  };
}

/// pairForEmphasis matches deleted lines to added lines inside one run of changes.
///
/// Pairing is strictly positional and only within a run of equal length. An unequal run means
/// lines were added or removed rather than rewritten, and pairing across that boundary invents
/// a correspondence the patch does not contain - which would emphasise the wrong half of two
/// unrelated lines and read as a confident lie.
export function pairForEmphasis<T>(dels: readonly T[], adds: readonly T[]): Array<[T, T]> {
  if (dels.length === 0 || dels.length !== adds.length) return [];
  const out: Array<[T, T]> = [];
  for (let i = 0; i < dels.length; i++) {
    const d = dels[i];
    const a = adds[i];
    if (d !== undefined && a !== undefined) out.push([d, a]);
  }
  return out;
}
