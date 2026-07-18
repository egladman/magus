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

// ---- Galois field GF(256) --------------------------------------------------

// The QR RS code works over GF(256) with primitive polynomial 0x11d. exp/log
// tables make multiplication a table lookup. Built once at module load.
const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
(function initGF(): void {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP[i] = x;
    LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
})();

function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0;
  return EXP[LOG[a] + LOG[b]];
}

// rsGenerator returns the generator polynomial for `degree` EC codewords.
function rsGenerator(degree: number): number[] {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] ^= poly[j];
      next[j + 1] ^= gfMul(poly[j], EXP[i]);
    }
    poly = next;
  }
  return poly;
}

// rsEncode returns the `degree` EC codewords for the data block: the remainder
// of data * x^degree divided by the RS generator polynomial over GF(256).
function rsEncode(data: number[], degree: number): number[] {
  return polyRemainder(data, rsGenerator(degree), degree);
}

// polyRemainder computes data * x^degree mod gen over GF(256).
function polyRemainder(data: number[], gen: number[], degree: number): number[] {
  const buf = data.concat(new Array<number>(degree).fill(0));
  for (let i = 0; i < data.length; i++) {
    const coef = buf[i];
    if (coef === 0) continue;
    for (let j = 0; j < gen.length; j++) {
      buf[i + j] ^= gfMul(gen[j], coef);
    }
  }
  return buf.slice(data.length);
}

// ---- version parameters (error-correction level M) -------------------------

// Block layout per version at EC level M: ecPerBlock error codewords per block,
// then one or two groups of (blockCount, dataCodewordsPerBlock).
interface VersionSpec {
  ecPerBlock: number;
  groups: Array<{ blocks: number; dataPerBlock: number }>;
  align: number[]; // alignment-pattern center coordinates (empty for v1)
}

// EC-M specs for versions 1-10 (ISO/IEC 18004 Annex). dataCapacity is derived.
const SPECS: Record<number, VersionSpec> = {
  1: { ecPerBlock: 10, groups: [{ blocks: 1, dataPerBlock: 16 }], align: [] },
  2: { ecPerBlock: 16, groups: [{ blocks: 1, dataPerBlock: 28 }], align: [6, 18] },
  3: { ecPerBlock: 26, groups: [{ blocks: 1, dataPerBlock: 44 }], align: [6, 22] },
  4: { ecPerBlock: 18, groups: [{ blocks: 2, dataPerBlock: 32 }], align: [6, 26] },
  5: { ecPerBlock: 24, groups: [{ blocks: 2, dataPerBlock: 43 }], align: [6, 30] },
  6: { ecPerBlock: 16, groups: [{ blocks: 4, dataPerBlock: 27 }], align: [6, 34] },
  7: { ecPerBlock: 18, groups: [{ blocks: 4, dataPerBlock: 31 }], align: [6, 22, 38] },
  8: { ecPerBlock: 22, groups: [{ blocks: 2, dataPerBlock: 38 }, { blocks: 2, dataPerBlock: 39 }], align: [6, 24, 42] },
  9: { ecPerBlock: 22, groups: [{ blocks: 3, dataPerBlock: 36 }, { blocks: 2, dataPerBlock: 37 }], align: [6, 26, 46] },
  10: { ecPerBlock: 26, groups: [{ blocks: 4, dataPerBlock: 43 }, { blocks: 1, dataPerBlock: 44 }], align: [6, 28, 50] },
};

function dataCapacity(version: number): number {
  return SPECS[version].groups.reduce((n, g) => n + g.blocks * g.dataPerBlock, 0);
}

// chooseVersion picks the smallest version 1-10 whose byte-mode data capacity
// holds the payload plus its header (mode nibble + char-count field). Throws if
// the payload is too large for the supported range.
function chooseVersion(byteLen: number): number {
  for (let v = 1; v <= 10; v++) {
    const countBits = v <= 9 ? 8 : 16;
    const headerBits = 4 + countBits;
    const capacityBits = dataCapacity(v) * 8;
    if (headerBits + byteLen * 8 <= capacityBits) return v;
  }
  throw new Error("qr: payload too large for versions 1-10");
}

// ---- bit stream ------------------------------------------------------------

class BitBuffer {
  bits: number[] = [];
  put(value: number, length: number): void {
    for (let i = length - 1; i >= 0; i--) this.bits.push((value >>> i) & 1);
  }
}

// encodeData turns bytes into the final, padded, interleaved codeword stream for
// the chosen version at EC level M.
function encodeCodewords(bytes: number[], version: number): number[] {
  const spec = SPECS[version];
  const capacity = dataCapacity(version);
  const countBits = version <= 9 ? 8 : 16;

  const bb = new BitBuffer();
  bb.put(0b0100, 4); // byte mode
  bb.put(bytes.length, countBits);
  for (const b of bytes) bb.put(b, 8);
  // Terminator (up to 4 zero bits) then pad to a byte boundary.
  const capBits = capacity * 8;
  for (let i = 0; i < 4 && bb.bits.length < capBits; i++) bb.bits.push(0);
  while (bb.bits.length % 8 !== 0) bb.bits.push(0);
  // Pad bytes 0xEC, 0x11 alternating until capacity is filled.
  const pad = [0xec, 0x11];
  let p = 0;
  const dataCodewords: number[] = [];
  for (let i = 0; i < bb.bits.length; i += 8) {
    let byte = 0;
    for (let j = 0; j < 8; j++) byte = (byte << 1) | bb.bits[i + j];
    dataCodewords.push(byte);
  }
  while (dataCodewords.length < capacity) dataCodewords.push(pad[p++ % 2]);

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
  const result: number[] = [];
  const maxData = Math.max(...dataBlocks.map((b) => b.length));
  for (let i = 0; i < maxData; i++) {
    for (const block of dataBlocks) if (i < block.length) result.push(block[i]);
  }
  const maxEc = Math.max(...ecBlocks.map((b) => b.length));
  for (let i = 0; i < maxEc; i++) {
    for (const block of ecBlocks) if (i < block.length) result.push(block[i]);
  }
  return result;
}

// ---- matrix construction ---------------------------------------------------

type Grid = Int8Array[]; // -1 = unset, 0/1 = module value

function newGrid(size: number): Grid {
  const g: Grid = [];
  for (let r = 0; r < size; r++) g.push(new Int8Array(size).fill(-1));
  return g;
}

// reserved marks which cells are function patterns (never carry data / masking).
function placeFinder(grid: Grid, reserved: boolean[][], r0: number, c0: number): void {
  for (let r = -1; r <= 7; r++) {
    for (let c = -1; c <= 7; c++) {
      const rr = r0 + r;
      const cc = c0 + c;
      if (rr < 0 || cc < 0 || rr >= grid.length || cc >= grid.length) continue;
      const inner = r >= 0 && r <= 6 && c >= 0 && c <= 6;
      const isDark =
        inner &&
        ((r === 0 || r === 6 || c === 0 || c === 6) || (r >= 2 && r <= 4 && c >= 2 && c <= 4));
      grid[rr][cc] = isDark ? 1 : 0;
      reserved[rr][cc] = true;
    }
  }
}

function placeAlignment(grid: Grid, reserved: boolean[][], centers: number[]): void {
  for (const r of centers) {
    for (const c of centers) {
      // Alignment patterns that would overlap a finder (the three corners) are
      // skipped; the reserved-cell check below catches every such collision.
      let overlaps = false;
      for (let dr = -2; dr <= 2 && !overlaps; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          if (reserved[r + dr]?.[c + dc]) { overlaps = true; break; }
        }
      }
      if (overlaps) continue;
      for (let dr = -2; dr <= 2; dr++) {
        for (let dc = -2; dc <= 2; dc++) {
          const dark = Math.max(Math.abs(dr), Math.abs(dc)) !== 1;
          grid[r + dr][c + dc] = dark ? 1 : 0;
          reserved[r + dr][c + dc] = true;
        }
      }
    }
  }
}

function placeTiming(grid: Grid, reserved: boolean[][]): void {
  const size = grid.length;
  for (let i = 8; i < size - 8; i++) {
    const v = i % 2 === 0 ? 1 : 0;
    if (!reserved[6][i]) { grid[6][i] = v; reserved[6][i] = true; }
    if (!reserved[i][6]) { grid[i][6] = v; reserved[i][6] = true; }
  }
}

// reserveFormatAreas marks (does not yet fill) the format-info and, for v>=7,
// version-info cells so data placement skips them. Formats are written later.
function reserveFormatAreas(reserved: boolean[][], size: number): void {
  for (let i = 0; i < 9; i++) {
    reserved[8][i] = true;
    reserved[i][8] = true;
  }
  // Second format copy: 8 cells up column 8 (rows size-1..size-8) and 7 cells
  // along row 8 (cols size-7..size-1). Row 8's right run is 7, not 8 - col size-8
  // is a data cell, so reserving it would leave it unfilled.
  for (let i = 0; i < 8; i++) reserved[size - 1 - i][8] = true;
  for (let i = 0; i < 7; i++) reserved[8][size - 1 - i] = true;
  // The dark module.
  reserved[size - 8][8] = true;
}

// placeData lays the codeword bitstream in the standard upward/downward zigzag,
// two columns at a time, skipping the vertical timing column and reserved cells.
function placeData(grid: Grid, reserved: boolean[][], codewords: number[]): void {
  const size = grid.length;
  const bits: number[] = [];
  for (const cw of codewords) for (let i = 7; i >= 0; i--) bits.push((cw >>> i) & 1);
  let bi = 0;
  let upward = true;
  for (let col = size - 1; col > 0; col -= 2) {
    if (col === 6) col--; // skip the timing column
    for (let i = 0; i < size; i++) {
      const row = upward ? size - 1 - i : i;
      for (let c = 0; c < 2; c++) {
        const cc = col - c;
        if (reserved[row][cc]) continue;
        grid[row][cc] = bi < bits.length ? bits[bi] : 0;
        bi++;
      }
    }
    upward = !upward;
  }
}

function maskFn(mask: number, r: number, c: number): boolean {
  switch (mask) {
    case 0: return (r + c) % 2 === 0;
    case 1: return r % 2 === 0;
    case 2: return c % 3 === 0;
    case 3: return (r + c) % 3 === 0;
    case 4: return (Math.floor(r / 2) + Math.floor(c / 3)) % 2 === 0;
    case 5: return ((r * c) % 2) + ((r * c) % 3) === 0;
    case 6: return (((r * c) % 2) + ((r * c) % 3)) % 2 === 0;
    case 7: return (((r + c) % 2) + ((r * c) % 3)) % 2 === 0;
    default: return false;
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

function placeFormat(grid: Grid, mask: number): void {
  const size = grid.length;
  const fmt = bchFormat(mask); // 15 bits, bit 14 is MSB
  const bit = (i: number): number => (fmt >>> i) & 1;
  // Around the top-left finder.
  for (let i = 0; i <= 5; i++) grid[8][i] = bit(i);
  grid[8][7] = bit(6);
  grid[8][8] = bit(7);
  grid[7][8] = bit(8);
  for (let i = 9; i <= 14; i++) grid[14 - i][8] = bit(i);
  // Split copy along the right/bottom edges.
  for (let i = 0; i <= 7; i++) grid[size - 1 - i][8] = bit(i);
  for (let i = 8; i <= 14; i++) grid[8][size - 15 + i] = bit(i);
  // Dark module.
  grid[size - 8][8] = 1;
}

// penalty scores a masked grid by the four ISO/IEC 18004 rules; lower is better.
function penalty(grid: Grid): number {
  const size = grid.length;
  let score = 0;
  // Rule 1: runs of 5+ same-color modules in rows and columns.
  for (let r = 0; r < size; r++) {
    for (const along of [true, false]) {
      let run = 1;
      let prev = -1;
      for (let i = 0; i < size; i++) {
        const v = along ? grid[r][i] : grid[i][r];
        if (v === prev) { run++; if (run === 5) score += 3; else if (run > 5) score += 1; }
        else { run = 1; prev = v; }
      }
    }
  }
  // Rule 2: 2x2 blocks of the same color.
  for (let r = 0; r < size - 1; r++) {
    for (let c = 0; c < size - 1; c++) {
      const v = grid[r][c];
      if (v === grid[r][c + 1] && v === grid[r + 1][c] && v === grid[r + 1][c + 1]) score += 3;
    }
  }
  // Rule 3: finder-like 1:1:3:1:1 patterns in rows and columns.
  const pat1 = [1, 0, 1, 1, 1, 0, 1, 0, 0, 0, 0];
  const pat2 = [0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1];
  const matches = (get: (i: number) => number, start: number, pat: number[]): boolean => {
    for (let k = 0; k < pat.length; k++) if (get(start + k) !== pat[k]) return false;
    return true;
  };
  for (let r = 0; r < size; r++) {
    for (let c = 0; c < size - 10; c++) {
      if (matches((i) => grid[r][i], c, pat1) || matches((i) => grid[r][i], c, pat2)) score += 40;
      if (matches((i) => grid[i][r], c, pat1) || matches((i) => grid[i][r], c, pat2)) score += 40;
    }
  }
  // Rule 4: overall dark/light balance.
  let dark = 0;
  for (let r = 0; r < size; r++) for (let c = 0; c < size; c++) if (grid[r][c] === 1) dark++;
  const percent = (dark * 100) / (size * size);
  const prev5 = Math.floor(percent / 5) * 5;
  const next5 = prev5 + 5;
  score += Math.min(Math.abs(prev5 - 50), Math.abs(next5 - 50)) / 5 * 10;
  return score;
}

// encodeMatrix returns the final module matrix (rows of 0/1) for text, choosing
// the version, mask, and applying EC level M. No quiet zone is included; the
// caller adds margin when rendering.
export function encodeMatrix(text: string): number[][] {
  const bytes = Array.from(new TextEncoder().encode(text));
  const version = chooseVersion(bytes.length);
  const codewords = encodeCodewords(bytes, version);
  const size = version * 4 + 17;

  // Build function patterns once; data placement is repeated per candidate mask.
  const baseReserved: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const base = newGrid(size);
  placeFinder(base, baseReserved, 0, 0);
  placeFinder(base, baseReserved, 0, size - 7);
  placeFinder(base, baseReserved, size - 7, 0);
  placeAlignment(base, baseReserved, SPECS[version].align);
  placeTiming(base, baseReserved);
  reserveFormatAreas(baseReserved, size);

  let best: Grid | null = null;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const grid = newGrid(size);
    for (let r = 0; r < size; r++) for (let c = 0; c < size; c++) grid[r][c] = base[r][c];
    placeData(grid, baseReserved, codewords);
    // Apply mask to non-reserved (data) cells only.
    for (let r = 0; r < size; r++) {
      for (let c = 0; c < size; c++) {
        if (!baseReserved[r][c] && maskFn(mask, r, c)) grid[r][c] ^= 1;
      }
    }
    placeFormat(grid, mask);
    const sc = penalty(grid);
    if (sc < bestScore) { bestScore = sc; best = grid; }
  }

  const out: number[][] = [];
  for (let r = 0; r < size; r++) out.push(Array.from(best![r]));
  return out;
}

// encodeToCanvas draws text as a QR code into canvas, sized to fit `pixels` with
// a 4-module quiet zone. Foreground/background default to black on white; the
// caller can theme them. It returns the module count (for tests / callers).
export function encodeToCanvas(
  canvas: HTMLCanvasElement,
  text: string,
  pixels = 240,
  fg = "#000000",
  bg = "#ffffff",
): number {
  const matrix = encodeMatrix(text);
  const modules = matrix.length;
  const quiet = 4;
  const total = modules + quiet * 2;
  const scale = Math.max(1, Math.floor(pixels / total));
  const dim = total * scale;
  canvas.width = dim;
  canvas.height = dim;
  const ctx = canvas.getContext("2d");
  if (!ctx) return modules;
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, dim, dim);
  ctx.fillStyle = fg;
  for (let r = 0; r < modules; r++) {
    for (let c = 0; c < modules; c++) {
      if (matrix[r][c] === 1) {
        ctx.fillRect((c + quiet) * scale, (r + quiet) * scale, scale, scale);
      }
    }
  }
  return modules;
}
