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
//     ranks files by what they can break (types.Diff.SortForReading), and this applies that
//     ranking to the parsed patch so the stream scrolls in the same order the sidebar lists.
//
// Both degrade cleanly: with no annotations at all, every file is unknown-role and the order
// falls back to the patch's own, which is exactly the plain diff viewer it started as.

import type { DiffFile } from "./parse";

// modeChange names a file-mode change, or null when there is nothing to say.
//
// A mode change carries NO hunks, so a row without this is a filename and a churn count with
// nothing explaining why the file is in the changeset - which reads as the surface having
// dropped something. A script gaining +x is a real reviewable event.
//
// Here rather than inline in the renderer so it can be pinned: the row it belongs to is inside
// a virtualized list, where a DOM test can only see whatever happens to be on screen.
export function modeChange(file: DiffFile): string | null {
  const { oldMode, newMode } = file;
  if (oldMode === undefined || newMode === undefined || oldMode === newMode) return null;
  return `mode ${oldMode} -> ${newMode}`;
}
import type { DiffAnnotation, DiffSession } from "./session";

export interface OrderedFile {
  readonly file: DiffFile;
  // annotation is absent when the review has not landed yet (first paint) or when the daemon
  // could not classify the path. Callers must render without it rather than waiting for it.
  readonly annotation?: DiffAnnotation;
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
// order (SortForReading) has to serve the console, the CLI, and a Buzz advisor writing a
// pull-request comment, and a second implementation in the browser would drift from it the
// first time either changed.
//
// A file in the patch that the review does not mention keeps its patch position at the END of
// primary rather than being dropped. Dropping it would silently hide a real change because an
// annotation was missing, which is the one failure mode a review must not have.
export function order(files: readonly DiffFile[], session: DiffSession | null): OrderedChangeset {
  const byPath = new Map<string, DiffFile>();
  for (const f of files) byPath.set(f.path, f);

  const primary: OrderedFile[] = [];
  const generated: OrderedFile[] = [];
  const placed = new Set<string>();

  for (const a of session?.diff?.files ?? []) {
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

// settled is a file a receipt covers at exactly its current content: read, and unmoved since.
//
// READ-AND-UNMOVED, never merely read. "stale" means a receipt exists at DIFFERENT content, which
// is the file that most needs a second look rather than the least, and folding it would hide the
// change from the one person who would otherwise have caught it.
//
// The predicate is spelled the same way in the terminal viewer (diff.File.Settled), against the
// same read_state the daemon computes once. The STATE is shared; only the folding is per surface.
export function settled(file: DiffFile, annotation: DiffAnnotation | undefined): boolean {
  return annotation?.read_state === "read" && !file.generated;
}

// visibleFiles is the file list the stream renders: primary always, plus the generated group and
// the already-reviewed group only when the reader has expanded them.
//
// Settled files fold by default for the reason generated ones do - they are not what the reader is
// here for - but for a different reason, so they are a separate group and a separate control. It
// is what makes a second pass cost only the second pass: a reviewer who asked for changes comes
// back to a changeset that is mostly what they already read.
export function visibleFiles(
  cs: OrderedChangeset,
  showGenerated: boolean,
  showSettled = true,
): DiffFile[] {
  const out = cs.primary
    .filter((o) => showSettled || !settled(o.file, o.annotation))
    .map((o) => o.file);
  if (showGenerated) out.push(...cs.generated.map((o) => o.file));
  return out;
}

// ReviewStats is the progress line. The denominator EXCLUDES generated files, because "you
// have 40 files left" is a lie when 30 of them are lockfiles a target rewrote. Progress has to
// mean progress through what the reader is actually responsible for.
export interface ReviewStats {
  readonly files: number;
  readonly generated: number;
  // settled is how many primary files a receipt already covers at their current content. Counted
  // so the toolbar can SAY what it folded: a hidden file nobody was told about is the one failure
  // this surface cannot have.
  readonly settled: number;
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
  let settledCount = 0;
  for (const { file, annotation } of cs.primary) {
    if (settled(file, annotation)) settledCount++;
    additions += file.additions;
    deletions += file.deletions;
    if (annotation?.surface === "public") publicSurface++;
    const cov = annotation?.coverage;
    if (cov && cov.total_stmts > 0 && cov.covered_stmts === 0) untested++;
  }
  return {
    files: cs.primary.length,
    generated: cs.generated.length,
    settled: settledCount,
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

export function riskChips(a: DiffAnnotation | undefined): Chip[] {
  if (!a) return [];
  const chips: Chip[] = [];

  // Read state leads. In a viewer the reader is working THROUGH a list, so which rows are
  // still outstanding is the navigation they came for - the same job GitHub's per-file Viewed
  // checkbox does, and the reason all three states show here while the terminal report names
  // only the finding.
  //
  // An ABSENT state still shows nothing, and that is not the same call: absent means nobody
  // checked, and rendering it as unread would turn "unmeasured" into an accusation.
  if (a.read_state === "stale") {
    chips.push({
      text: "changed since read",
      tone: "danger",
      title:
        "You recorded reading this file, and its content changed afterwards. " +
        "The version you read is not the version you are about to land.",
    });
  } else if (a.read_state === "read") {
    chips.push({
      text: "read",
      tone: "ok",
      title:
        "A read receipt covers this file at its current content. " +
        "Editing it voids the receipt.",
    });
  } else if (a.read_state === "unread") {
    chips.push({
      text: "unread",
      tone: "neutral",
      title:
        "Nobody has recorded reading this file. Read it through here, or record it " +
        "from wherever you did read it with `magus diff --ack <path>`.",
    });
  }

  if (a.surface === "public") {
    const api = (a.symbols ?? []).filter((s) => s.module_api).map((s) => s.label ?? s.id);
    const across = [...new Set((a.symbols ?? []).flatMap((s) => s.external_projects ?? []))];
    chips.push({
      text: "public surface",
      tone: "warn",
      title:
        (api.length > 0 ? `Exported from the module: ${api.slice(0, 8).join(", ")}. ` : "") +
        (across.length > 0 ? `Also used by: ${across.join(", ")}. ` : "") +
        "A change here is API surface. Consider whether it needs a version bump.",
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
  // high enough to mean something - see types.DiffChurn.notableRankCutoff. The cutoff is
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
