// tiling.ts - the split-pane layout as a binary tree, exactly how tiling window
// managers and VS Code model it: a node is either a LEAF holding one surface, or a
// SPLIT of two children with a direction and a ratio. Every operation here is a PURE
// function over that tree (no DOM, no mutation - it returns a new tree), so the layout
// algebra is unit-testable in isolation. Splits carry their own id so a draggable
// divider is addressable (setRatio) and the render layer can key on it. The DOM that
// renders a Pane tree into a CSS grid with draggable dividers lives with the console app
// (Phase 6); it reads the tree and calls these ops. New ids are passed IN (not minted
// here) so the functions stay pure and deterministic under test.

export interface Leaf {
  kind: "leaf";
  id: string;
  pageId: string;
}

export interface Split {
  kind: "split";
  id: string;
  dir: "row" | "col"; // row = side by side, col = stacked
  ratio: number; // fraction [0,1] of the axis given to child `a`
  a: Pane;
  b: Pane;
}

// A DISCRIMINATED UNION: the `kind` field tells TS (and `if (p.kind === "leaf")`) which
// variant is in hand, narrowing to Leaf or Split automatically.
export type Pane = Leaf | Split;

export const MIN_RATIO = 0.05;
export const MAX_RATIO = 0.95;

// A minimal rectangle (a subset of DOMRect) so the geometry helpers below are pure and testable
// without a real layout - the render layer passes each pane's getBoundingClientRect(), a superset.
export interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}

// A screen direction for keyboard pane navigation (alt+hjkl / the focus commands).
export type Direction = "left" | "right" | "up" | "down";

// pickAxis chooses a split direction from the pane's aspect ratio, so a split needs no direction
// UI: a wider-than-tall pane splits into a "row" (the new pane to the right); a taller pane splits
// into a "col" (the new pane below). Called on each new leaf, this walks a Fibonacci-spiral tiling.
// Ported from Tack's pickAxis (its 'v'/'h' become our "row"/"col").
export function pickAxis(rect: Rect): Split["dir"] {
  return rect.width >= rect.height ? "row" : "col";
}

// neighborInDirection finds the id of the candidate pane whose centroid is nearest to `from` in the
// requested direction - the target of an alt+hjkl focus move. A candidate qualifies only if its
// centroid sits strictly in that half-plane (a small epsilon avoids picking a pane merely aligned on
// the axis); among those, the least Manhattan-distant centroid wins. Returns null when nothing lies
// that way. Ported from Tack's neighborInDirection, re-expressed over our Rect + Direction.
export function neighborInDirection(
  from: Rect,
  candidates: { id: string; rect: Rect }[],
  dir: Direction,
): string | null {
  const cx = from.left + from.width / 2;
  const cy = from.top + from.height / 2;
  let best: string | null = null;
  let bestDist = Infinity;
  for (const { id, rect } of candidates) {
    const tx = rect.left + rect.width / 2;
    const ty = rect.top + rect.height / 2;
    const inPlane =
      dir === "left" ? tx < cx - 4
      : dir === "right" ? tx > cx + 4
      : dir === "up" ? ty < cy - 4
      : ty > cy + 4;
    if (!inPlane) continue;
    const dist = Math.abs(tx - cx) + Math.abs(ty - cy);
    if (dist < bestDist) { bestDist = dist; best = id; }
  }
  return best;
}

// leaves collects every leaf in document order - "which surfaces are currently tiled".
export function leaves(p: Pane): Leaf[] {
  if (p.kind === "leaf") return [p];
  return [...leaves(p.a), ...leaves(p.b)];
}

// splitLeaf replaces the leaf `targetId` with a split of [that leaf, a new leaf], so
// the surface stays put and the new one appears beside/below it. `first` puts the new
// leaf on the a-side (left/top) rather than the b-side. An unknown target returns the
// tree unchanged. newSplitId/newLeaf are supplied by the caller to keep this pure.
export function splitLeaf(
  root: Pane,
  targetId: string,
  dir: Split["dir"],
  newSplitId: string,
  newLeaf: { id: string; pageId: string },
  first = false,
): Pane {
  const leaf: Leaf = { kind: "leaf", ...newLeaf };
  const walk = (p: Pane): Pane => {
    if (p.kind === "leaf") {
      if (p.id !== targetId) return p;
      const split: Split = {
        kind: "split",
        id: newSplitId,
        dir,
        ratio: 0.5,
        a: first ? leaf : p,
        b: first ? p : leaf,
      };
      return split;
    }
    return { ...p, a: walk(p.a), b: walk(p.b) };
  };
  return walk(root);
}

// closePane removes the leaf `id` and collapses its parent split into the surviving
// sibling subtree (the divider disappears). Closing the only pane (root is that leaf)
// returns null - an empty layout the caller handles. Closing an unknown id returns the
// tree unchanged.
export function closePane(root: Pane, id: string): Pane | null {
  if (root.kind === "leaf") return root.id === id ? null : root;
  const walk = (p: Pane): Pane => {
    if (p.kind === "leaf") return p;
    // If a direct child is the leaf being closed, promote the other child.
    if (p.a.kind === "leaf" && p.a.id === id) return walk(p.b);
    if (p.b.kind === "leaf" && p.b.id === id) return walk(p.a);
    return { ...p, a: walk(p.a), b: walk(p.b) };
  };
  return walk(root);
}

// setRatio moves the divider of the split `splitId`, clamped so neither side can be
// dragged shut. Unknown ids leave the tree unchanged.
export function setRatio(root: Pane, splitId: string, ratio: number): Pane {
  const clamped = Math.min(MAX_RATIO, Math.max(MIN_RATIO, ratio));
  const walk = (p: Pane): Pane => {
    if (p.kind === "leaf") return p;
    if (p.id === splitId) return { ...p, ratio: clamped, a: walk(p.a), b: walk(p.b) };
    return { ...p, a: walk(p.a), b: walk(p.b) };
  };
  return walk(root);
}
