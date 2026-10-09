// readingorder.ts - the unit focus mode reads: a STEP, one or more hunks shown together.
//
// The server orders a changeset's hunks by the relationships between the symbols they change
// (types.DiffOrder) and says why each hunk sits where it does. This module only reshapes that
// answer for the screen: it never ranks, and it never words a reason itself. With no order on the
// wire, every hunk is its own step in file order, which is what focus mode was before the order
// existed.
//
// Pure, so the arithmetic that is wrong until it is tested (a folded file, a patch that moved
// under the order, a step that spans files) can be pinned without a DOM.

import type { DiffFile, Hunk } from "./parse";
import { commentKey } from "./rows";
import type { DiffGroupKind, DiffOrder, DiffWhy } from "./session";

// HunkAddress names a hunk across files. digest is part of the address: an order computed for one
// patch must not be applied to a hunk that has the same index in a patch that has since moved.
export interface HunkAddress {
  readonly path: string;
  readonly index: number;
  readonly digest: string;
}

export interface ReadingHunk extends HunkAddress {
  readonly label: string;
  readonly why: DiffWhy;
}

// ReadingStep is one screen of the pass. group is absent when there is no order to place it in.
export interface ReadingStep {
  readonly number: number;
  readonly group?: DiffGroupKind;
  readonly groupLabel: string;
  readonly hunks: readonly ReadingHunk[];
}

// UNPLACED is why a hunk the order does not mention is still shown: dropping it would tell the
// reader a change is not there.
const UNPLACED: DiffWhy = {
  relation: "unranked",
  text: "the reading order does not place this hunk",
};

const addressKey = (a: { path: string; index: number }): string => commentKey(a.path, a.index);

// fileSteps is the order a changeset has when the server computed none: each hunk is a step.
export function fileSteps(pairs: readonly HunkAddress[]): ReadingStep[] {
  return pairs.map((p, i) => ({
    number: i + 1,
    groupLabel: "",
    hunks: [{ ...p, label: "", why: UNPLACED }],
  }));
}

// readingSteps lays the server's order over the hunks actually on screen.
//
// Hunks the reader cannot see (a folded generated file, a settled one) leave their steps, and a
// step left empty goes with them. A visible hunk the order does not place, or whose digest
// differs from the one it was placed under, is appended as a step of its own after the last
// group rather than lost. A hunk the order places twice is shown at its first place only.
//
// Returns the per-hunk fallback when order is absent, so a caller handles one shape.
export function readingSteps(
  order: DiffOrder | undefined,
  visible: readonly HunkAddress[],
): ReadingStep[] {
  if (!order) return fileSteps(visible);

  const want = new Map<string, HunkAddress>();
  for (const v of visible) want.set(addressKey(v), v);
  const placed = new Set<string>();
  const out: ReadingStep[] = [];

  for (const group of order.groups) {
    for (const step of group.steps) {
      const hunks: ReadingHunk[] = [];
      for (const h of step.hunks) {
        const key = addressKey(h.ref);
        const seen = want.get(key);
        if (!seen || seen.digest !== h.ref.digest || placed.has(key)) continue;
        placed.add(key);
        hunks.push({ ...seen, label: h.label ?? "", why: h.why });
      }
      if (hunks.length === 0) continue;
      out.push({
        number: step.number,
        group: group.kind,
        groupLabel: group.label ?? "",
        hunks,
      });
    }
  }

  let next = out.reduce((n, s) => Math.max(n, s.number), 0);
  for (const v of visible) {
    if (placed.has(addressKey(v))) continue;
    next++;
    out.push({
      number: next,
      group: "unranked",
      groupLabel: "",
      hunks: [{ ...v, label: "", why: UNPLACED }],
    });
  }
  return out;
}

// stepIndexOf is the position of the step holding the hunk at (path, index), or -1.
export function stepIndexOf(
  steps: readonly ReadingStep[],
  at: { path: string; index: number } | null,
): number {
  if (!at) return -1;
  return steps.findIndex((s) => s.hunks.some((h) => h.path === at.path && h.index === at.index));
}

// firstUnreadStep is the first step with a hunk the reader has not marked, which is where an
// interrupted pass resumes. Null when every step is read.
export function firstUnreadStep(
  steps: readonly ReadingStep[],
  viewed: ReadonlySet<string>,
): ReadingStep | null {
  return steps.find((s) => s.hunks.some((h) => !viewed.has(h.digest))) ?? null;
}

// StepHead is what the step's heading row says.
export interface StepHead {
  // position counts the steps on screen from 1; the server's number can skip the folded ones.
  readonly position: number;
  readonly total: number;
  readonly group?: DiffGroupKind;
  readonly label: string;
  readonly hunks: number;
}

export interface StepPlacement {
  readonly label: string;
  readonly why: DiffWhy;
}

// StepRows is the order's contribution to one screen's rows: the heading and a reason per hunk.
export interface StepRows {
  readonly head: StepHead;
  readonly places: Map<string, StepPlacement>;
}

// stepFiles narrows the file list to one step: only its hunks, in its order. Consecutive hunks of
// one file share a single entry, so a step that spans files (a cycle) lists each file where the
// step reaches it. A hunk carries its own index, so no remark is renumbered by the slice.
export function stepFiles(step: ReadingStep, files: readonly DiffFile[]): DiffFile[] {
  const byPath = new Map(files.map((f) => [f.path, f]));
  const out: DiffFile[] = [];
  let last: { path: string; hunks: Hunk[]; file: DiffFile } | null = null;
  for (const h of step.hunks) {
    const file = byPath.get(h.path);
    const hunk = file?.hunks.find((x) => x.index === h.index);
    if (!file || !hunk) continue;
    if (last && last.path === h.path) {
      last.hunks.push(hunk);
      continue;
    }
    last = { path: h.path, hunks: [hunk], file };
    out.push({ ...file, hunks: last.hunks });
  }
  return out;
}

// stepRows describes step `at` of `steps` for the row builder.
export function stepRows(steps: readonly ReadingStep[], at: number): StepRows | null {
  const step = steps[at];
  if (!step) return null;
  const places = new Map<string, StepPlacement>();
  for (const h of step.hunks) places.set(addressKey(h), { label: h.label, why: h.why });
  return {
    head: {
      position: at + 1,
      total: steps.length,
      group: step.group,
      label: step.groupLabel,
      hunks: step.hunks.length,
    },
    places,
  };
}
