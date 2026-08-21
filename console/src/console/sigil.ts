// sigil.ts - one unique, mathematically generated mark per workspace.
//
// The identicon idea, in this product's own idiom. A GitHub identicon is a grid of coloured squares
// and would read as GitHub's; a SIGIL is a bounding circle, a figure inscribed by joining points on
// it, and runes cut around the rim. Same job - you recognise your own at a glance and never had to
// name it - drawn in a vocabulary this app already speaks.
//
// PURE and DETERMINISTIC. The same root yields the same mark forever, on every machine, with nothing
// persisted anywhere. That is the whole trick: a workspace's identity falls out of its path, so two
// worktrees of one repo are visibly different and neither had to be configured.
//
// It does not change over time and nothing feeds it. An earlier version grew with cache hits; the
// mark is an IDENTIFIER, and an identifier that changes is not one.
//
// Static by construction, with no animation - the house rule is that anything arriving on screen does
// so through a transition, because a background tab does not advance keyframes.

// FNV-1a, 32-bit: short, well-known, and stable across engines - a mark must not change because a
// browser changed its hashing. Not a security function and never used as one.
function hash(seed: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

function stepper(seed: number): () => number {
  let s = seed || 1;
  return () => {
    s ^= s << 13;
    s >>>= 0;
    s ^= s >> 17;
    s ^= s << 5;
    s >>>= 0;
    return s;
  };
}

// The earthy palette, by token name. Decorative per the house rule: the functional accent stays one
// muted hue, and this is a mark rather than a control.
export const SIGIL_HUES = [
  "--console-moss",
  "--console-sage",
  "--console-spruce",
  "--console-clay",
  "--console-rust",
  "--console-gold",
  "--console-slate",
  "--console-indigo",
] as const;

export function sigilHue(root: string): string {
  return SIGIL_HUES[hash(root) % SIGIL_HUES.length];
}

function gcd(a: number, b: number): number {
  return b === 0 ? a : gcd(b, a % b);
}

// sigilFigure resolves the two numbers that decide the shape: how many points sit on the rim, and the
// stride the line takes between them. Exported because the FIGURE is the mark - it is the thing worth
// pinning in a test.
//
// The stride must be coprime with the count so the line reaches every vertex before closing, AND
// strictly inside [2, points-2] so the result is a STAR. A stride of 1 draws the regular polygon and
// points-1 draws it backwards; both are coprime, and both rendered a twelve-pointed sigil as a plain
// ring with no figure inside it at all.
// Five to twelve points, MINUS SIX. Below five there is no star to inscribe, and past twelve the lines
// crowd into a disc where two marks stop being tellable apart. Six is excluded because it has no
// single-path star at all: every stride in [2, 4] shares a factor with 6, which is why a hexagram is
// drawn as two separate triangles. Listing the workable counts beats searching for one and hoping.
const POINT_CHOICES = [5, 7, 8, 9, 10, 11, 12] as const;

export function sigilFigure(root: string): { points: number; step: number } {
  const next = stepper(hash(root));
  const points = POINT_CHOICES[next() % POINT_CHOICES.length];
  // Enumerate rather than search: every stride that both reaches all points and skips at least one.
  const strides: number[] = [];
  for (let k = 2; k <= points - 2; k++) if (gcd(k, points) === 1) strides.push(k);
  return { points, step: strides[next() % strides.length] };
}

// sigilSvg draws the mark. `size` is the viewBox edge; the caller sizes it in CSS.
export function sigilSvg(root: string, size = 28): string {
  const { points, step } = sigilFigure(root);
  const next = stepper(hash(root) ^ 0x9e3779b9);
  const c = size / 2;
  const r = size / 2 - 4;
  const at = (i: number, radius: number): [number, number] => {
    // Twelve o'clock, not three: the mark reads upright, so one that happens to be symmetric looks
    // deliberate rather than tilted.
    const a = (i / points) * Math.PI * 2 - Math.PI / 2;
    return [c + radius * Math.cos(a), c + radius * Math.sin(a)];
  };
  const order: number[] = [];
  for (let i = 0; i < points; i++) order.push((i * step) % points);
  const d = order
    .map(
      (v, i) =>
        (i === 0 ? "M" : "L") +
        at(v, r)
          .map((q) => q.toFixed(2))
          .join(" "),
    )
    .join("");

  const parts = [
    '<circle cx="' + c + '" cy="' + c + '" r="' + r + '" opacity="0.3"/>',
    '<path d="' + d + 'Z"/>',
  ];
  // A CORE, chosen from four. Point count and hue alone were not enough separation: with seven counts
  // and eight hues, three of a dozen real workspaces came out as blue-ish pentagrams that no one would
  // tell apart at a glance. This is the cheapest axis that reads instantly - it sits at the centre,
  // where the eye already is - and it multiplies the distinguishable space fourfold.
  const core = next() % 4;
  if (core === 1) {
    parts.push('<circle cx="' + c + '" cy="' + c + '" r="2" fill="currentColor" stroke="none"/>');
  } else if (core === 2) {
    parts.push('<circle cx="' + c + '" cy="' + c + '" r="' + (r * 0.32).toFixed(2) + '"/>');
  } else if (core === 3) {
    // A small counter-rotated triangle: reads as a different KIND of mark rather than a smaller one.
    const t = [0, 1, 2].map((k) => {
      const a = (k / 3) * Math.PI * 2 + Math.PI / 2;
      return [c + r * 0.34 * Math.cos(a), c + r * 0.34 * Math.sin(a)];
    });
    parts.push(
      '<path d="M' + t.map((q) => q.map((v) => v.toFixed(2)).join(" ")).join("L") + 'Z"/>',
    );
  }
  // Two or three runes on the rim, so two sigils that happen to share a figure still differ. Seeded
  // from a different stream than the figure, so the two do not move together.
  const runes = 2 + (next() % 2);
  for (let i = 0; i < runes; i++) {
    const a = ((next() % points) / points) * Math.PI * 2 - Math.PI / 2;
    const [x1, y1] = [c + (r + 1) * Math.cos(a), c + (r + 1) * Math.sin(a)];
    const [x2, y2] = [c + (r + 3.5) * Math.cos(a), c + (r + 3.5) * Math.sin(a)];
    parts.push(
      '<line x1="' +
        x1.toFixed(2) +
        '" y1="' +
        y1.toFixed(2) +
        '" x2="' +
        x2.toFixed(2) +
        '" y2="' +
        y2.toFixed(2) +
        '"/>',
    );
  }
  return (
    '<svg viewBox="0 0 ' +
    size +
    " " +
    size +
    '" width="' +
    size +
    '" height="' +
    size +
    '" fill="none" stroke="currentColor" stroke-width="1.1" stroke-linecap="round" ' +
    'stroke-linejoin="round" aria-hidden="true">' +
    parts.join("") +
    "</svg>"
  );
}
