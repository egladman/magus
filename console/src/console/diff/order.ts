// order.ts - what to show, in what order, and what to fold away.
//
// This is the part that makes a magus review different from a text diff, and it is pure so it
// can be tested without a DOM or a daemon.
//
// Two ideas, both of which depend on facts only the workspace has:
//
//  1. NOISE COLLAPSE. A declared target output is generated: reviewing its diff is reading a
//     machine's restatement of a change made somewhere else. Folding those into one row is
//     not hiding information, it is refusing to spend the reader's attention on the answer
//     when the question is one screen down. On magus's own tree this routinely removes half
//     the changed files from a review.
//
//  2. CONSEQUENCE ORDER. Alphabetical order spends attention at random. The server already
//     ranks files by what they can break (types.Review.SortForReview), and this applies that
//     ranking to the parsed patch so the stream scrolls in the same order the sidebar lists.
//
// Both degrade cleanly: with no annotations at all, every file is unknown-role and the order
// falls back to the patch's own, which is exactly the plain diff viewer it started as.

import type { DiffFile } from "./parse";
import type { ReviewFile, ReviewSession } from "./session";

export interface OrderedFile {
  readonly file: DiffFile;
  // annotation is absent when the review has not landed yet (first paint) or when the daemon
  // could not classify the path. Callers must render without it rather than waiting for it.
  readonly annotation?: ReviewFile;
}

export interface OrderedChangeset {
  // primary is everything worth reading, in consequence order.
  readonly primary: readonly OrderedFile[];
  // generated is the folded group: declared outputs, kept out of primary so they neither
  // occupy reading order nor inflate the progress denominator.
  readonly generated: readonly OrderedFile[];
}

// order applies the server's ranking and the generated split to a parsed patch.
//
// The server's order is authoritative and is NOT recomputed here. One definition of review
// order (SortForReview) has to serve the console, the CLI, and a Buzz advisor writing a
// pull-request comment, and a second implementation in the browser would drift from it the
// first time either changed.
//
// A file in the patch that the review does not mention keeps its patch position at the END of
// primary rather than being dropped. Dropping it would silently hide a real change because an
// annotation was missing, which is the one failure mode a review must not have.
export function order(files: readonly DiffFile[], session: ReviewSession | null): OrderedChangeset {
  const byPath = new Map<string, DiffFile>();
  for (const f of files) byPath.set(f.path, f);

  const primary: OrderedFile[] = [];
  const generated: OrderedFile[] = [];
  const placed = new Set<string>();

  for (const a of session?.review?.files ?? []) {
    const file = byPath.get(a.path);
    if (!file) continue; // annotated but not in the patch (a path scoped out); nothing to show
    placed.add(a.path);
    const entry: OrderedFile = { file, annotation: a };
    if (a.role === "output") generated.push(entry);
    else primary.push(entry);
  }

  for (const f of files) {
    if (!placed.has(f.path)) primary.push({ file: f });
  }
  return { primary, generated };
}

// visibleFiles is the file list the stream renders: primary always, plus the generated group
// only when the reader has expanded it.
export function visibleFiles(cs: OrderedChangeset, showGenerated: boolean): DiffFile[] {
  const out = cs.primary.map((o) => o.file);
  if (showGenerated) out.push(...cs.generated.map((o) => o.file));
  return out;
}

// ReviewStats is the progress line. The denominator EXCLUDES generated files, because "you
// have 40 files left" is a lie when 30 of them are lockfiles a target rewrote. Progress has to
// mean progress through what the reader is actually responsible for.
export interface ReviewStats {
  readonly files: number;
  readonly generated: number;
  readonly additions: number;
  readonly deletions: number;
  readonly publicSurface: number;
  readonly untested: number;
}

// stats summarizes a changeset for the toolbar.
//
// `untested` counts files with MEASURED zero coverage, never files with none measured. A file
// nobody ran coverage over is not a file with no tests, and reporting it as one would make the
// number worthless the first time somebody noticed.
export function stats(cs: OrderedChangeset): ReviewStats {
  let additions = 0;
  let deletions = 0;
  let publicSurface = 0;
  let untested = 0;
  for (const { file, annotation } of cs.primary) {
    additions += file.additions;
    deletions += file.deletions;
    if (annotation?.surface === "public") publicSurface++;
    const cov = annotation?.coverage;
    if (cov && cov.total_stmts > 0 && cov.covered_stmts === 0) untested++;
  }
  return {
    files: cs.primary.length,
    generated: cs.generated.length,
    additions,
    deletions,
    publicSurface,
    untested,
  };
}

// riskChips are the short, self-explaining labels shown beside a file. They are EVIDENCE, in
// magus's house style: each states a measured fact, and none renders a verdict the reader did
// not ask for.
export interface Chip {
  readonly text: string;
  readonly tone: "neutral" | "info" | "warn" | "danger" | "ok";
  readonly title: string;
}

export function riskChips(a: ReviewFile | undefined): Chip[] {
  if (!a) return [];
  const chips: Chip[] = [];

  if (a.surface === "public") {
    const api = (a.symbols ?? []).filter((s) => s.module_api).map((s) => s.label ?? s.id);
    const across = [...new Set((a.symbols ?? []).flatMap((s) => s.external_projects ?? []))];
    chips.push({
      text: "public surface",
      tone: "warn",
      title:
        (api.length > 0 ? `Exported from the module: ${api.slice(0, 8).join(", ")}. ` : "") +
        (across.length > 0 ? `Also used by: ${across.join(", ")}. ` : "") +
        "A change here is API surface - consider whether it needs a version bump.",
    });
  }

  // An unmeasured reach shows no chip at all. A "0 referents" chip on an unindexed workspace
  // would be a measurement magus never took.
  const reach = a.reach;
  if (reach !== null && reach > 0) {
    chips.push({
      text: `${reach} referents`,
      tone: reach >= 20 ? "danger" : reach >= 5 ? "warn" : "info",
      title: `The most widely used symbol changed here is referenced from ${reach} files.`,
    });
  }

  // Churn: is this file being rewritten over and over? The rank is shown only when it is
  // high enough to mean something - see types.ReviewChurn.notableRankCutoff. The cutoff is
  // duplicated rather than shipped on the wire because it is a DISPLAY decision, and the two
  // surfaces are allowed to disagree about presentation while agreeing about the data.
  const ch = a.churn;
  if (ch && ch.commits > 0) {
    const notable = (ch.rank ?? 0) > 0 && (ch.rank ?? 0) <= 50;
    const rising = notable && (ch.project_trend ?? 0) > 0;
    const parts = [`${ch.commits} ${ch.commits === 1 ? "commit" : "commits"}`];
    if ((ch.authors ?? 0) > 1) parts.push(`${ch.authors} authors`);
    chips.push({
      text: rising ? `${parts.join(", ")} and rising` : parts.join(", "),
      tone: rising ? "danger" : notable ? "warn" : "neutral",
      title: rising
        ? `This file is among the workspace's most-changed (hotspot #${ch.rank}) and its project's churn is accelerating. Worth asking why it keeps changing rather than only whether this change is right.`
        : `Changed in ${ch.commits} of the last commits scanned${notable ? ` - hotspot #${ch.rank}` : ""}.`,
    });
  }

  const cov = a.coverage;
  if (cov && cov.total_stmts > 0) {
    const pct = Math.round(cov.ratio * 100);
    chips.push({
      text: `${pct}% covered`,
      tone: pct === 0 ? "danger" : pct < 50 ? "warn" : "ok",
      title: `${cov.covered_stmts} of ${cov.total_stmts} statements covered by the last coverage run.`,
    });
  }

  return chips;
}
