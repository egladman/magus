// sigil.ts - one mark per workspace, built on rotational symmetry.
//
// WHY SYMMETRY. An earlier version traced a name across a planetary magic square, which is the
// historical way sigils are made. It looked like scribble, and the reason is mathematical rather than
// a matter of taste: a magic square is CONSTRUCTED so that consecutive numbers land in unrelated
// cells - that scattering is what makes the rows sum equally - so the resulting path has no spatial
// logic by design. The eye reads an asymmetric scatter as noise and a symmetric figure as intentional,
// which is why every identicon that looks designed is built on mirror or rotational symmetry.
//
// So: generate ONE small motif from the hash, then repeat it k times around the centre. Symmetry does
// the aesthetic work, and it cannot come out ugly; the motif carries the entropy.
//
// The motif begins and ends on the two edges of its wedge AT THE SAME RADIUS, so the rotated copies
// join end to end into a single closed curve rather than sitting apart as k separate spokes. That one
// constraint is the difference between a flower and a pinwheel of unrelated marks.
//
// NOT REVERSIBLE, deliberately. Recovering a path from a picture would mean a screenshot leaking
// `/Users/<someone>/Repos/<unreleased-thing>`. What is wanted is that two marks rarely repeat, and
// that comes from the size of the visual space - see assignSigils for the part that makes it a
// guarantee rather than a probability.

// FNV-1a, 32-bit: short, well-known, stable across engines - a mark must not change because a browser
// changed its hashing. Not a security function and never used as one.
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

// A point in the wedge, in polar form: t is the fraction across the wedge, r the fraction of the
// radius.
type Pt = { t: number; r: number };

export interface SigilSpec {
  folds: number; // how many times the motif repeats around the centre
  mirror: boolean; // reflect within each wedge, for dihedral rather than plain rotational symmetry
  edge: number; // the radius where one wedge hands over to the next
  motif: Pt[];
  core: 0 | 1 | 2; // nothing, a dot, or an inner ring
  ring: boolean; // the enclosing circle
  // How far each segment bows away from the straight line between its ends. This is what decides
  // whether the mark reads as crystalline or as grown: at 0 it is a star, and away from 0 the same
  // figure becomes petals and curls. Signed, so a figure can bulge outward or cup inward.
  bulge: number;
  orbs: boolean; // small filled dots at each petal tip
  hue: string;
}

// Three to eight folds. Two reads as a leaf rather than a figure; past eight the wedges are too narrow
// for the motif to show at the size this renders at.
const FOLDS = [3, 4, 5, 6, 7, 8];

export function sigilSpec(seed: string): SigilSpec {
  const next = stepper(hash(seed));
  const pick = (n: number): number => next() % n;
  const folds = FOLDS[pick(FOLDS.length)];
  // Quantized rather than continuous: a motif that can land anywhere produces near-identical marks
  // that differ by a pixel, which is worse than a smaller set of clearly distinct ones.
  const edge = 0.42 + pick(4) * 0.12;
  const count = 2 + pick(3);
  const motif: Pt[] = [];
  for (let i = 0; i < count; i++) {
    motif.push({ t: (1 + pick(7)) / 8, r: 0.25 + pick(7) * 0.11 });
  }
  // Force one point to stand clear of the hand-over radius. Without this, a seed whose motif happens
  // to sit near `edge` throughout draws a plain regular polygon - technically a valid mark and a
  // completely forgettable one. `magus` came out as a bare heptagon. The reach alternates in and out
  // by seed, so the correction produces both stars and flowers rather than one house shape.
  const REACH = 0.3;
  if (!motif.some((q) => Math.abs(q.r - edge) >= REACH)) {
    const i = pick(motif.length);
    const out = edge < 0.6 ? edge + REACH : edge - REACH;
    motif[i] = { t: motif[i].t, r: Math.max(0.12, Math.min(0.98, out)) };
  }
  // Never zero: a straight-sided figure is the crystalline look this deliberately moves away from.
  // The sign is seeded too, so some workspaces bloom outward and others cup inward.
  const BULGES = [-0.34, -0.22, -0.13, 0.13, 0.22, 0.34, 0.46];
  return {
    folds,
    mirror: pick(2) === 0,
    edge,
    motif,
    core: pick(3) as 0 | 1 | 2,
    ring: pick(4) > 0,
    bulge: BULGES[pick(BULGES.length)],
    orbs: pick(3) === 0,
    hue: SIGIL_HUES[pick(SIGIL_HUES.length)],
  };
}

// specKey is the VISUAL identity of a spec - two workspaces whose specs share a key draw the same
// picture. Collision resolution keys on this rather than on the seed, because the seed differing is
// not the point; the picture differing is.
export function specKey(s: SigilSpec): string {
  return [
    s.folds,
    s.mirror ? "m" : "-",
    s.edge.toFixed(2),
    s.core,
    s.ring ? "r" : "-",
    s.bulge.toFixed(2),
    s.orbs ? "o" : "-",
    s.hue,
    s.motif.map((p) => p.t.toFixed(2) + ":" + p.r.toFixed(2)).join(","),
  ].join("|");
}

// assignSigils turns the workspaces a daemon has loaded into marks that are DISTINCT FROM EACH OTHER.
//
// Uniqueness cannot be guaranteed against arbitrary inputs - finitely many pictures, unboundedly many
// paths, pigeonhole. It can be guaranteed across a KNOWN SET, which is the only place a collision
// would ever be noticed: if two roots would draw the same picture, the later one is re-seeded until it
// does not. Sorted first, so which one moves does not depend on the order the daemon happened to list
// them, and the result is stable across reloads with nothing stored.
export function assignSigils(roots: readonly string[]): Map<string, SigilSpec> {
  const out = new Map<string, SigilSpec>();
  const taken = new Set<string>();
  for (const root of [...roots].sort()) {
    let spec = sigilSpec(root);
    for (let salt = 1; taken.has(specKey(spec)) && salt < 64; salt++) {
      spec = sigilSpec(root + "#" + salt);
    }
    taken.add(specKey(spec));
    out.set(root, spec);
  }
  return out;
}

// renderSigil draws a spec. `size` is the viewBox edge; the caller sizes it in CSS.
export function renderSigil(spec: SigilSpec, size = 44): string {
  const c = size / 2;
  const rad = size / 2 - 2;
  const wedge = (Math.PI * 2) / spec.folds;
  const at = (t: number, r: number, turn: number): [number, number] => {
    // From twelve o'clock, so a figure that happens to be symmetric about the vertical reads upright.
    const a = -Math.PI / 2 + turn * wedge + t * wedge;
    return [c + rad * r * Math.cos(a), c + rad * r * Math.sin(a)];
  };
  // The wedge's own run: hand-over point, the motif, then the next hand-over point. Mirroring appends
  // the motif reflected about the wedge's bisector, which turns rotational symmetry into dihedral.
  const inWedge: Pt[] = [
    { t: 0, r: spec.edge },
    ...spec.motif,
    ...(spec.mirror ? [...spec.motif].reverse().map((p) => ({ t: 1 - p.t, r: p.r })) : []),
  ];
  // Walk every wedge into one flat ring of points first, so the curve through them closes smoothly
  // rather than restarting at each wedge - the joins are the thing that would give the repetition away.
  const ring: [number, number][] = [];
  for (let k = 0; k < spec.folds; k++) {
    for (const q of inWedge) ring.push(at(q.t, q.r, k));
  }
  // QUADRATICS, not straight segments. Each control point sits at the midpoint pushed along the radius
  // by `bulge`, so a segment bows outward into a petal or inward into a cusp. This is the whole
  // difference between a spiky star and something grown - the symmetry is identical either way.
  const d: string[] = ["M" + ring[0][0].toFixed(2) + " " + ring[0][1].toFixed(2)];
  for (let i = 1; i <= ring.length; i++) {
    const [x0, y0] = ring[i - 1];
    const [x1, y1] = ring[i % ring.length];
    const mx = (x0 + x1) / 2;
    const my = (y0 + y1) / 2;
    // Push along the line from the centre through the midpoint, which keeps the bow radial and so
    // keeps the k-fold symmetry exact.
    const dx = mx - c;
    const dy = my - c;
    const len = Math.hypot(dx, dy) || 1;
    const cx = mx + (dx / len) * rad * spec.bulge;
    const cy = my + (dy / len) * rad * spec.bulge;
    d.push("Q" + cx.toFixed(2) + " " + cy.toFixed(2) + " " + x1.toFixed(2) + " " + y1.toFixed(2));
  }
  const parts = ['<path d="' + d.join("") + 'Z"/>'];
  // Seeds at the petal tips. Small enough to read as punctuation on the figure rather than as a second
  // figure competing with it.
  if (spec.orbs) {
    const tipR = Math.max(...inWedge.map((q) => q.r));
    const tip = inWedge.findIndex((q) => q.r === tipR);
    for (let k = 0; k < spec.folds; k++) {
      const [x, y] = at(inWedge[tip].t, inWedge[tip].r, k);
      parts.push(
        '<circle cx="' +
          x.toFixed(2) +
          '" cy="' +
          y.toFixed(2) +
          '" r="' +
          (size * 0.032).toFixed(2) +
          '" fill="currentColor" stroke="none"/>',
      );
    }
  }
  if (spec.ring) {
    parts.unshift(
      '<circle cx="' + c + '" cy="' + c + '" r="' + rad.toFixed(2) + '" opacity="0.22"/>',
    );
  }
  if (spec.core === 1) {
    parts.push(
      '<circle cx="' +
        c +
        '" cy="' +
        c +
        '" r="' +
        (size * 0.05).toFixed(2) +
        '" fill="currentColor" stroke="none"/>',
    );
  } else if (spec.core === 2) {
    parts.push('<circle cx="' + c + '" cy="' + c + '" r="' + (rad * 0.2).toFixed(2) + '"/>');
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
    '" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" ' +
    'stroke-linejoin="round" aria-hidden="true">' +
    parts.join("") +
    "</svg>"
  );
}

// The single-workspace path, for a console that knows of no peers to be distinct from.
export function sigilSvg(root: string, size = 44): string {
  return renderSigil(sigilSpec(root), size);
}

export function sigilHue(root: string): string {
  return sigilSpec(root).hue;
}
