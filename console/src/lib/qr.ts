// qr.ts - a tiny, dependency-free QR code encoder, vendored so "share to phone"
// can draw a scannable code with no npm dependency and no network fetch.
//
// Scope is deliberately narrow: BYTE mode only, error-correction level M, and
// versions 1-10 (up to 216 data bytes at level M) - far more than a share URL of
// ~90 bytes ever needs. That covers the one caller (encodeToCanvas of a LAN
// share URL) without dragging in a general library. The implementation follows
// ISO/IEC 18004: GF(256) Reed-Solomon ECC, the standard block-interleave, the
// function-pattern layout, all eight data masks with the spec's penalty scoring,
// and BCH-coded format information.
//
// It exposes the module matrix (encodeMatrix) so the geometry is unit-testable
// without a DOM, plus encodeToCanvas for the caller. Nothing here touches the
// network or any global except the <canvas> the caller hands in.
//
// Failures are values, never exceptions: both entry points return a QrResult.

export type QrResult<T> = { ok: true; value: T } | { ok: false; error: string };

const done: QrResult<void> = { ok: true, value: undefined };

function fail(error: string): { ok: false; error: string } {
  return { ok: false, error };
}

// firstFailure returns the first failed step, or done when every step passed.
function firstFailure(steps: QrResult<void>[]): QrResult<void> {
  for (const step of steps) if (!step.ok) return step;
  return done;
}

// ---- Galois field GF(256) --------------------------------------------------

// The QR RS code works over GF(256) with primitive polynomial 0x11d. Products
// are computed bitwise (shift-and-add), which needs no lookup tables.
function gfMul(a: number, b: number): number {
  let product = 0;
  let x = a;
  for (let y = b; y > 0; y >>= 1) {
    if (y & 1) product ^= x;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  return product;
}

// zipWith combines a and b pairwise, stopping at the shorter one.
function zipWith(
  a: readonly number[],
  b: readonly number[],
  f: (x: number, y: number) => number,
): number[] {
  const out: number[] = [];
  const rest = b.values();
  for (const x of a) {
    const y = rest.next();
    if (y.done) break;
    out.push(f(x, y.value));
  }
  return out;
}

// rsGenerator returns the generator polynomial for `degree` EC codewords: the
// product of (x - 2^i) for i < degree, leading coefficient first.
function rsGenerator(degree: number): number[] {
  let poly = [1];
  let root = 1;
  for (let i = 0; i < degree; i++) {
    const next: number[] = [];
    let carry = 0;
    for (const coef of poly) {
      next.push(coef ^ gfMul(carry, root));
      carry = coef;
    }
    next.push(gfMul(carry, root));
    poly = next;
    root = gfMul(root, 2);
  }
  return poly;
}

// rsEncode returns the `degree` EC codewords for the data block: the remainder
// of data * x^degree divided by the RS generator polynomial over GF(256).
function rsEncode(data: number[], degree: number): number[] {
  const tail = rsGenerator(degree).slice(1);
  let rem = new Array<number>(degree).fill(0);
  for (const d of data) {
    const [head = 0, ...rest] = rem;
    const factor = d ^ head;
    rem = zipWith(tail, [...rest, 0], (g, r) => r ^ gfMul(g, factor));
  }
  return rem;
}

// ---- version parameters (error-correction level M) -------------------------

// Block layout per version at EC level M: ecPerBlock error codewords per block,
// then one or two groups of (blockCount, dataCodewordsPerBlock).
interface VersionSpec {
  ecPerBlock: number;
  groups: Array<{ blocks: number; dataPerBlock: number }>;
  align: number[]; // alignment-pattern center coordinates (empty for v1)
}

// EC-M specs for versions 1-10 (ISO/IEC 18004 Annex), in version order: the
// entry at index i is version i + 1. dataCapacity is derived.
const SPECS: readonly VersionSpec[] = [
  { ecPerBlock: 10, groups: [{ blocks: 1, dataPerBlock: 16 }], align: [] },
  { ecPerBlock: 16, groups: [{ blocks: 1, dataPerBlock: 28 }], align: [6, 18] },
  { ecPerBlock: 26, groups: [{ blocks: 1, dataPerBlock: 44 }], align: [6, 22] },
  { ecPerBlock: 18, groups: [{ blocks: 2, dataPerBlock: 32 }], align: [6, 26] },
  { ecPerBlock: 24, groups: [{ blocks: 2, dataPerBlock: 43 }], align: [6, 30] },
  { ecPerBlock: 16, groups: [{ blocks: 4, dataPerBlock: 27 }], align: [6, 34] },
  { ecPerBlock: 18, groups: [{ blocks: 4, dataPerBlock: 31 }], align: [6, 22, 38] },
  {
    ecPerBlock: 22,
    groups: [
      { blocks: 2, dataPerBlock: 38 },
      { blocks: 2, dataPerBlock: 39 },
    ],
    align: [6, 24, 42],
  },
  {
    ecPerBlock: 22,
    groups: [
      { blocks: 3, dataPerBlock: 36 },
      { blocks: 2, dataPerBlock: 37 },
    ],
    align: [6, 26, 46],
  },
  {
    ecPerBlock: 26,
    groups: [
      { blocks: 4, dataPerBlock: 43 },
      { blocks: 1, dataPerBlock: 44 },
    ],
    align: [6, 28, 50],
  },
];

function dataCapacity(spec: VersionSpec): number {
  return spec.groups.reduce((n, g) => n + g.blocks * g.dataPerBlock, 0);
}

// charCountBits is the width of the byte-mode character-count field.
function charCountBits(version: number): number {
  return version <= 9 ? 8 : 16;
}

// headerBits is the mode nibble plus the character-count field.
function headerBits(version: number): number {
  return 4 + charCountBits(version);
}

// maxPayloadBytes is how many payload bytes a version holds after its header.
function maxPayloadBytes(version: number, spec: VersionSpec): number {
  return Math.floor((dataCapacity(spec) * 8 - headerBits(version)) / 8);
}

interface ChosenVersion {
  version: number;
  spec: VersionSpec;
}

// chooseVersion picks the smallest version 1-10 whose byte-mode data capacity
// holds the payload plus its header. A payload too large for the supported range
// is the one user-facing failure, so the error says how large the limit is.
function chooseVersion(byteLen: number): QrResult<ChosenVersion> {
  let largest = 0;
  for (const [i, spec] of SPECS.entries()) {
    const version = i + 1;
    if (headerBits(version) + byteLen * 8 <= dataCapacity(spec) * 8) {
      return { ok: true, value: { version, spec } };
    }
    largest = maxPayloadBytes(version, spec);
  }
  return fail(
    `The text is ${byteLen} bytes, more than the ${largest} bytes the largest supported QR code (version ${SPECS.length}, level M) holds.`,
  );
}

// ---- bit stream ------------------------------------------------------------

class BitBuffer {
  bits: number[] = [];
  put(value: number, length: number): void {
    for (let i = length - 1; i >= 0; i--) this.bits.push((value >>> i) & 1);
  }
}

// encodeCodewords turns bytes into the final, padded, interleaved codeword
// stream for the chosen version at EC level M.
function encodeCodewords(bytes: number[], version: number, spec: VersionSpec): number[] {
  const capacity = dataCapacity(spec);

  const bb = new BitBuffer();
  bb.put(0b0100, 4); // byte mode
  bb.put(bytes.length, charCountBits(version));
  for (const b of bytes) bb.put(b, 8);
  // Terminator (up to 4 zero bits) then pad to a byte boundary.
  const capBits = capacity * 8;
  for (let i = 0; i < 4 && bb.bits.length < capBits; i++) bb.bits.push(0);
  while (bb.bits.length % 8 !== 0) bb.bits.push(0);
  const dataCodewords: number[] = [];
  for (let i = 0; i < bb.bits.length; i += 8) {
    dataCodewords.push(bb.bits.slice(i, i + 8).reduce((byte, bit) => (byte << 1) | bit, 0));
  }
  // Pad bytes 0xEC, 0x11 alternating until capacity is filled.
  const filled = dataCodewords.length;
  while (dataCodewords.length < capacity) {
    dataCodewords.push((dataCodewords.length - filled) % 2 === 0 ? 0xec : 0x11);
  }

  // Split into blocks, compute EC per block.
  const dataBlocks: number[][] = [];
  const ecBlocks: number[][] = [];
  let idx = 0;
  for (const g of spec.groups) {
    for (let b = 0; b < g.blocks; b++) {
      const block = dataCodewords.slice(idx, idx + g.dataPerBlock);
      idx += g.dataPerBlock;
      dataBlocks.push(block);
      ecBlocks.push(rsEncode(block, spec.ecPerBlock));
    }
  }

  // Interleave data codewords, then EC codewords (the standard column order).
  // Blocks within a version can differ in length, so a short block runs out early.
  const result: number[] = [];
  const maxData = Math.max(...dataBlocks.map((b) => b.length));
  for (let i = 0; i < maxData; i++) {
    for (const block of dataBlocks) {
      const cw = block[i];
      if (cw !== undefined) result.push(cw);
    }
  }
  const maxEc = Math.max(...ecBlocks.map((b) => b.length));
  for (let i = 0; i < maxEc; i++) {
    for (const block of ecBlocks) {
      const cw = block[i];
      if (cw !== undefined) result.push(cw);
    }
  }
  return result;
}

// ---- matrix construction ---------------------------------------------------

// A cell is -1 until a function pattern or a data bit claims it, then 0 or 1. A
// cell that is not -1 is reserved: data placement and masking leave it alone.
type Grid = Int8Array[];

type ModuleWrite = readonly [row: number, col: number, value: number];

function newGrid(size: number): Grid {
  const g: Grid = [];
  for (let r = 0; r < size; r++) g.push(new Int8Array(size).fill(-1));
  return g;
}

function readModule(grid: Grid, r: number, c: number): QrResult<number> {
  const value = grid[r]?.[c];
  if (value === undefined) {
    return fail(`QR module (${r}, ${c}) is outside the ${grid.length}x${grid.length} grid.`);
  }
  return { ok: true, value };
}

function writeModules(grid: Grid, cells: readonly ModuleWrite[]): QrResult<void> {
  for (const [r, c, value] of cells) {
    const row = grid[r];
    if (row === undefined || c < 0 || c >= row.length) {
      return fail(`QR module (${r}, ${c}) is outside the ${grid.length}x${grid.length} grid.`);
    }
    row[c] = value;
  }
  return done;
}

function placeFinder(grid: Grid, r0: number, c0: number): QrResult<void> {
  const cells: ModuleWrite[] = [];
  for (let r = -1; r <= 7; r++) {
    for (let c = -1; c <= 7; c++) {
      const rr = r0 + r;
      const cc = c0 + c;
      if (rr < 0 || cc < 0 || rr >= grid.length || cc >= grid.length) continue;
      const inner = r >= 0 && r <= 6 && c >= 0 && c <= 6;
      const isDark =
        inner &&
        (r === 0 || r === 6 || c === 0 || c === 6 || (r >= 2 && r <= 4 && c >= 2 && c <= 4));
      cells.push([rr, cc, isDark ? 1 : 0]);
    }
  }
  return writeModules(grid, cells);
}

// touchesPattern reports whether the 5x5 square around (r, c) holds a claimed cell.
function touchesPattern(grid: Grid, r: number, c: number): QrResult<boolean> {
  for (let dr = -2; dr <= 2; dr++) {
    for (let dc = -2; dc <= 2; dc++) {
      const cell = readModule(grid, r + dr, c + dc);
      if (!cell.ok) return cell;
      if (cell.value !== -1) return { ok: true, value: true };
    }
  }
  return { ok: true, value: false };
}

function placeAlignment(grid: Grid, centers: number[]): QrResult<void> {
  const cells: ModuleWrite[] = [];
  for (const r of centers) {
    for (const c of centers) {
      // Alignment patterns that would overlap a finder (the three corners) are
      // skipped; the claimed-cell check catches every such collision.
      const overlaps = touchesPattern(grid, r, c);
      if (!overlaps.ok) return overlaps;
      if (overlaps.value) continue;
      for (let dr = -2; dr <= 2; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          const dark = Math.max(Math.abs(dr), Math.abs(dc)) !== 1;
          cells.push([r + dr, c + dc, dark ? 1 : 0]);
        }
      }
    }
  }
  return writeModules(grid, cells);
}

function placeTiming(grid: Grid): QrResult<void> {
  const cells: ModuleWrite[] = [];
  for (let i = 8; i < grid.length - 8; i++) {
    const v = i % 2 === 0 ? 1 : 0;
    const horizontal = readModule(grid, 6, i);
    if (!horizontal.ok) return horizontal;
    if (horizontal.value === -1) cells.push([6, i, v]);
    const vertical = readModule(grid, i, 6);
    if (!vertical.ok) return vertical;
    if (vertical.value === -1) cells.push([i, 6, v]);
  }
  return writeModules(grid, cells);
}

// reserveFormatAreas claims (as 0, not yet the real bit) the unset format-info
// cells and the dark module so data placement skips them. A cell a function
// pattern already holds, such as the timing line crossing row and column 8, keeps
// its value. placeFormat writes the claimed cells later.
function reserveFormatAreas(grid: Grid): QrResult<void> {
  const size = grid.length;
  const positions: Array<readonly [number, number]> = [];
  for (let i = 0; i < 9; i++) {
    positions.push([8, i]);
    positions.push([i, 8]);
  }
  // Second format copy: 8 cells up column 8 (rows size-1..size-8) and 7 cells
  // along row 8 (cols size-7..size-1). Row 8's right run is 7, not 8 - col size-8
  // is a data cell, so reserving it would leave it unfilled.
  for (let i = 0; i < 8; i++) positions.push([size - 1 - i, 8]);
  for (let i = 0; i < 7; i++) positions.push([8, size - 1 - i]);
  positions.push([size - 8, 8]);
  const cells: ModuleWrite[] = [];
  for (const [r, c] of positions) {
    const cell = readModule(grid, r, c);
    if (!cell.ok) return cell;
    if (cell.value === -1) cells.push([r, c, 0]);
  }
  return writeModules(grid, cells);
}

// placeData lays the codeword bitstream in the standard upward/downward zigzag,
// two columns at a time, skipping the vertical timing column and claimed cells,
// and applies the data mask as each bit lands.
function placeData(grid: Grid, codewords: number[], mask: number): QrResult<void> {
  const size = grid.length;
  const bits = codewords.flatMap((cw) => Array.from({ length: 8 }, (_, k) => (cw >>> (7 - k)) & 1));
  let bi = 0;
  let upward = true;
  for (let col = size - 1; col > 0; col -= 2) {
    if (col === 6) col--; // skip the timing column
    for (let i = 0; i < size; i++) {
      const row = upward ? size - 1 - i : i;
      for (let c = 0; c < 2; c++) {
        const cc = col - c;
        const cell = readModule(grid, row, cc);
        if (!cell.ok) return cell;
        if (cell.value !== -1) continue;
        // The remainder modules past the last codeword are zero.
        const bit = bits[bi] ?? 0;
        bi++;
        const written = writeModules(grid, [[row, cc, maskFn(mask, row, cc) ? bit ^ 1 : bit]]);
        if (!written.ok) return written;
      }
    }
    upward = !upward;
  }
  return done;
}

function maskFn(mask: number, r: number, c: number): boolean {
  switch (mask) {
    case 0:
      return (r + c) % 2 === 0;
    case 1:
      return r % 2 === 0;
    case 2:
      return c % 3 === 0;
    case 3:
      return (r + c) % 3 === 0;
    case 4:
      return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0;
    case 5:
      return ((r * c) % 2) + ((r * c) % 3) === 0;
    case 6:
      return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0;
    case 7:
      return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0;
    default:
      return false;
  }
}

// bchFormat computes the 15-bit BCH-coded format information for EC level M and
// the given mask, XOR'd with the spec mask 0x5412. The 5-bit data is the 2-bit
// EC level (00 = M) followed by the 3-bit mask; it is BCH(15,5) coded with the
// generator 0x537 and masked so an all-zero format is never all-zero on the grid.
function bchFormat(mask: number): number {
  const data5 = (0b00 << 3) | mask; // level M (00) + 3-bit mask
  const G = 0x537; // x^10 + x^8 + x^5 + x^4 + x^2 + x + 1
  let d = data5 << 10;
  for (let i = 14; i >= 10; i--) {
    if ((d >>> i) & 1) d ^= G << (i - 10);
  }
  const bch = (data5 << 10) | (d & 0x3ff);
  return bch ^ 0x5412;
}

function placeFormat(grid: Grid, mask: number): QrResult<void> {
  const size = grid.length;
  const fmt = bchFormat(mask); // 15 bits, bit 14 is MSB
  const bit = (i: number): number => (fmt >>> i) & 1;
  const cells: ModuleWrite[] = [];
  // Around the top-left finder.
  for (let i = 0; i <= 5; i++) cells.push([8, i, bit(i)]);
  cells.push([8, 7, bit(6)]);
  cells.push([8, 8, bit(7)]);
  cells.push([7, 8, bit(8)]);
  for (let i = 9; i <= 14; i++) cells.push([14 - i, 8, bit(i)]);
  // Split copy along the right/bottom edges.
  for (let i = 0; i <= 7; i++) cells.push([size - 1 - i, 8, bit(i)]);
  for (let i = 8; i <= 14; i++) cells.push([8, size - 15 + i, bit(i)]);
  // Dark module.
  cells.push([size - 8, 8, 1]);
  return writeModules(grid, cells);
}

// columnsOf returns the grid transposed: one array per column, top to bottom.
function columnsOf(grid: Grid): number[][] {
  const cols: number[][] = [];
  for (const row of grid) {
    for (const [c, v] of row.entries()) {
      const col = cols[c];
      if (col) col.push(v);
      else cols[c] = [v];
    }
  }
  return cols;
}

// penalty scores a masked grid by the four ISO/IEC 18004 rules; lower is better.
function penalty(grid: Grid): number {
  const size = grid.length;
  const lines = [...grid.map((row) => Array.from(row)), ...columnsOf(grid)];
  let score = 0;
  for (const line of lines) {
    // Rule 1: runs of 5+ same-color modules in rows and columns.
    let run = 1;
    let prev = -1;
    for (const v of line) {
      if (v === prev) {
        run++;
        if (run === 5) score += 3;
        else if (run > 5) score += 1;
      } else {
        run = 1;
        prev = v;
      }
    }
    // Rule 3: finder-like 1:1:3:1:1 patterns in rows and columns.
    for (let c = 0; c < size - 10; c++) {
      const window = line.slice(c, c + 11).join("");
      if (window === "10111010000" || window === "00001011101") score += 40;
    }
  }
  // Rule 2: 2x2 blocks of the same color.
  for (const [r, row] of grid.entries()) {
    const below = grid[r + 1];
    if (below === undefined) continue;
    for (const [c, v] of row.entries()) {
      if (v === row[c + 1] && v === below[c] && v === below[c + 1]) score += 3;
    }
  }
  // Rule 4: overall dark/light balance.
  const dark = grid.reduce((n, row) => n + row.reduce((m, v) => m + (v === 1 ? 1 : 0), 0), 0);
  const percent = (dark * 100) / (size * size);
  const prev5 = Math.floor(percent / 5) * 5;
  const next5 = prev5 + 5;
  score += (Math.min(Math.abs(prev5 - 50), Math.abs(next5 - 50)) / 5) * 10;
  return score;
}

// encodeMatrix returns the final module matrix (rows of 0/1) for text, choosing
// the version, mask, and applying EC level M. No quiet zone is included; the
// caller adds margin when rendering. The error is set when text is too long for
// the largest supported version, and its message is fit to show a user.
export function encodeMatrix(text: string): QrResult<number[][]> {
  const bytes = Array.from(new TextEncoder().encode(text));
  const chosen = chooseVersion(bytes.length);
  if (!chosen.ok) return chosen;
  const { version, spec } = chosen.value;
  const codewords = encodeCodewords(bytes, version, spec);
  const size = version * 4 + 17;

  // Build function patterns once; data placement is repeated per candidate mask.
  const base = newGrid(size);
  const patterns = firstFailure([
    placeFinder(base, 0, 0),
    placeFinder(base, 0, size - 7),
    placeFinder(base, size - 7, 0),
    placeAlignment(base, spec.align),
    placeTiming(base),
    reserveFormatAreas(base),
  ]);
  if (!patterns.ok) return patterns;

  let best: Grid | null = null;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const grid = base.map((row) => Int8Array.from(row));
    const laid = firstFailure([placeData(grid, codewords, mask), placeFormat(grid, mask)]);
    if (!laid.ok) return laid;
    const sc = penalty(grid);
    if (sc < bestScore) {
      bestScore = sc;
      best = grid;
    }
  }
  if (best === null) return fail("No QR mask candidate was scored.");
  return { ok: true, value: best.map((row) => Array.from(row)) };
}

// encodeToCanvas draws text as a QR code into canvas, sized to fit `pixels` with
// a 4-module quiet zone. Foreground/background default to black on white; the
// caller can theme them. On success the value is the module count (for tests /
// callers). The error is set when text does not fit the largest supported QR
// version or the canvas has no 2d context; the canvas is left undrawn.
export function encodeToCanvas(
  canvas: HTMLCanvasElement,
  text: string,
  pixels = 240,
  fg = "#000000",
  bg = "#ffffff",
): QrResult<number> {
  const encoded = encodeMatrix(text);
  if (!encoded.ok) return encoded;
  const matrix = encoded.value;
  const modules = matrix.length;
  const quiet = 4;
  const total = modules + quiet * 2;
  const scale = Math.max(1, Math.floor(pixels / total));
  const dim = total * scale;
  canvas.width = dim;
  canvas.height = dim;
  const ctx = canvas.getContext("2d");
  if (!ctx) return fail("The canvas has no 2d drawing context.");
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, dim, dim);
  ctx.fillStyle = fg;
  for (const [r, row] of matrix.entries()) {
    for (const [c, v] of row.entries()) {
      if (v === 1) ctx.fillRect((c + quiet) * scale, (r + quiet) * scale, scale, scale);
    }
  }
  return { ok: true, value: modules };
}
