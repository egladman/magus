// cards.ts - card-node geometry and painting for the targets flavor in the
// layered/waves DAG modes. Extracted as a pure module (no module state, no DOM
// access beyond the CanvasRenderingContext2D a caller passes in), following the
// layout.ts extraction style: main.ts decides WHEN cards are active
// (cardsActive()) and resolves per-node paint options; this module only reads
// GNode fields and, in measureCards, writes n.w/n.h back onto the nodes it is
// given.
//
// CardTheme is a structural subset of main.ts's Theme (readTheme()'s return
// type) rather than an import of it, so this module stays leaf-level and
// cannot form an import cycle with main.ts.

import type { GNode } from "./types.js";

export const CARD_H = 34; // world units
export const CARD_MIN_W = 96;
export const CARD_MAX_W = 200;
export const CARD_PAD_X = 10;
export const CARD_COL_W = 240; // CARD_MAX_W plus gutter; the DAG column spacing for card mode

const STRIP_W = 3; // world units; a fill, so it scales with the card like the rest of its geometry

export interface CardTheme {
  bg: string;
  text: string;
  muted: string;
  border: string;
  accent: string;
  font: string;
}

function clamp(min: number, v: number, max: number): number {
  return Math.max(min, Math.min(max, v));
}

// measureCards sets ctx.font once and stamps n.w/n.h on every node from its
// label width, clamped to [CARD_MIN_W, CARD_MAX_W]. Call before layoutLayered/
// layoutWaves so column spacing can account for card width.
export function measureCards(ctx: CanvasRenderingContext2D, nodes: GNode[], font: string): void {
  ctx.font = "500 12px " + font;
  for (const n of nodes) {
    const labelW = ctx.measureText(n.label).width;
    n.w = clamp(CARD_MIN_W, labelW + 2 * CARD_PAD_X, CARD_MAX_W);
    n.h = CARD_H;
  }
}

// ellipsize returns text unchanged when it already fits within maxW (given
// ctx's current font); otherwise it grows a head/tail keep-window evenly from
// both ends and joins them with a middle ellipsis, so long identifiers keep
// their distinguishing prefix AND suffix. Deterministic: pure function of the
// text, maxW, and ctx's measureText - no randomness, no external state.
export function ellipsize(ctx: CanvasRenderingContext2D, text: string, maxW: number): string {
  if (ctx.measureText(text).width <= maxW) return text;
  const ellipsis = "...";
  const ellipsisW = ctx.measureText(ellipsis).width;
  if (ellipsisW >= maxW) return ellipsis;
  const budget = maxW - ellipsisW;
  let head = 0;
  let tail = 0;
  while (head + tail < text.length) {
    const growHead = head <= tail;
    const nextHead = growHead ? head + 1 : head;
    const nextTail = growHead ? tail : tail + 1;
    const candidate = text.slice(0, nextHead) + text.slice(text.length - nextTail);
    if (ctx.measureText(candidate).width > budget) break;
    head = nextHead;
    tail = nextTail;
  }
  if (head === 0 && tail === 0) return ellipsis;
  return text.slice(0, head) + ellipsis + text.slice(text.length - tail);
}

// drawCard paints a single card centered on n.x/n.y (the CENTER stays the
// node position so all existing edge/fit/center math keeps working
// unmodified). Background is painted at full alpha regardless of opts.alpha
// so cards occlude edges beneath them even while faded; everything else
// (border, strip, text, rings) follows opts.alpha. Fills (background, strip,
// text) are sized in constant world units, matching card w/h, so they scale
// with zoom like the card itself; strokes (border, anchor ring, selection
// outline) divide by opts.zoomK to stay a crisp constant screen width, the
// same convention draw() already uses for edges and the circle anchor ring.
export function drawCard(
  ctx: CanvasRenderingContext2D,
  n: GNode,
  opts: {
    theme: CardTheme;
    kindColor: string;
    alpha: number;
    selected: boolean;
    anchor: boolean;
    zoomK: number;
    durationText: string | null;
  },
): void {
  const { theme, kindColor, alpha, selected, anchor, zoomK, durationText } = opts;
  const w = n.w ?? CARD_MIN_W;
  const h = n.h ?? CARD_H;
  const x = n.x - w / 2;
  const y = n.y - h / 2;

  // Background: full alpha so the card occludes any edge drawn under it.
  ctx.globalAlpha = 1;
  ctx.fillStyle = theme.bg;
  ctx.fillRect(x, y, w, h);

  ctx.globalAlpha = alpha;

  // Border, square corners.
  ctx.lineWidth = 1 / zoomK;
  ctx.strokeStyle = theme.border;
  ctx.strokeRect(x, y, w, h);

  // Kind-colored strip down the left edge.
  ctx.fillStyle = kindColor;
  ctx.fillRect(x, y, STRIP_W, h);

  // Anchor: a second inset border, same color at reduced alpha - the card
  // equivalent of the circle anchor ring.
  if (anchor) {
    const priorAlpha = ctx.globalAlpha;
    ctx.globalAlpha = alpha * 0.55;
    ctx.lineWidth = 1 / zoomK;
    ctx.strokeStyle = theme.border;
    const inset = 2.5;
    ctx.strokeRect(x + inset, y + inset, w - 2 * inset, h - 2 * inset);
    ctx.globalAlpha = priorAlpha;
  }

  // Label (+ optional project subtitle), left-aligned after the strip and padding.
  const textX = x + STRIP_W + CARD_PAD_X;
  const maxTextW = Math.max(0, w - STRIP_W - 2 * CARD_PAD_X);
  const project = n.attrs?.project;

  ctx.textBaseline = "middle";
  ctx.textAlign = "left";
  ctx.fillStyle = theme.text;
  ctx.font = "500 12px " + theme.font;
  ctx.fillText(
    ellipsize(ctx, n.label, maxTextW),
    textX,
    project && project !== "." ? n.y - 6 : n.y,
  );

  if (project && project !== ".") {
    ctx.fillStyle = theme.muted;
    ctx.font = "400 9px " + theme.font;
    ctx.fillText(ellipsize(ctx, project, maxTextW), textX, n.y + 7);
  }

  // Duration, right-aligned within the card.
  if (durationText != null) {
    ctx.fillStyle = theme.muted;
    ctx.font = "400 9px " + theme.font;
    ctx.textAlign = "right";
    ctx.fillText(durationText, x + w - CARD_PAD_X, y + h - 8);
    ctx.textAlign = "left";
  }

  // Selection outline.
  if (selected) {
    ctx.lineWidth = 2 / zoomK;
    ctx.strokeStyle = theme.accent;
    ctx.strokeRect(x, y, w, h);
  }

  ctx.globalAlpha = 1;
}
