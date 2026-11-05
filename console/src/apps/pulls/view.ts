// view.ts - the Pulls app's sentences and markup. Formatting is pure.

import { timestampMs, type Timestamp } from "@bufbuild/protobuf/wkt";
import {
  CiState,
  Mergeable,
  Review,
  type AuthorFilter,
  type BranchState,
  type FailedJob,
  type MergeStep,
  type Pattern,
  type Pull,
  type SyncState,
} from "@wire/pulls/v1alpha1/pulls_pb";
import { h } from "../../desktop/view";

interface Dur {
  seconds: bigint;
  nanos: number;
}

export function tsMs(t: Timestamp | undefined): number | null {
  return t === undefined ? null : timestampMs(t);
}

export function durMs(d: Dur | undefined): number {
  return d === undefined ? 0 : Number(d.seconds) * 1000 + Math.round(d.nanos / 1e6);
}

// ageText rounds down to the largest whole unit: 45s, 12m, 2h, 3d.
export function ageText(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return s + "s";
  if (s < 3600) return Math.floor(s / 60) + "m";
  if (s < 86400) return Math.floor(s / 3600) + "h";
  return Math.floor(s / 86400) + "d";
}

export function clockText(ms: number): string {
  const d = new Date(ms);
  const two = (n: number): string => String(n).padStart(2, "0");
  return two(d.getHours()) + ":" + two(d.getMinutes());
}

// A failed sync keeps the last good board, so the line names both.
export function syncLine(sync: SyncState | undefined, nowMs: number): string {
  const syncedAt = tsMs(sync?.syncedAt);
  if (sync === undefined || syncedAt === null) return "not synced yet";
  if (sync.syncError !== "") {
    const failedAt = tsMs(sync.failedAt);
    const when = failedAt === null ? "just now" : ageText(nowMs - failedAt) + " ago";
    return `sync failed ${when}: ${sync.syncError}; showing the board from ${clockText(syncedAt)}`;
  }
  const next = tsMs(sync.nextSyncAt);
  const parts = [
    "synced " + ageText(nowMs - syncedAt) + " ago",
    next === null
      ? "next sync unscheduled"
      : next <= nowMs
        ? "next sync due"
        : "next in " + ageText(next - nowMs),
    sync.cadenceReason === "" ? "cadence reason not given" : sync.cadenceReason,
    sync.rateLimit > 0 ? `${sync.rateRemaining}/${sync.rateLimit} left` : "rate limit unknown",
  ];
  return parts.join(" · ");
}

export function authorsLine(f: AuthorFilter | undefined): string {
  if (f === undefined || f.everyAuthor) return "every author";
  const list = f.authors.join(", ");
  return f.isDefault ? `authors: ${list} (you, default)` : "authors: " + list;
}

export function blockedText(p: Pull): string {
  return p.blocked.join("; ");
}

export function shaText(sha: string): string {
  return sha.slice(0, 7);
}

export function overlapText(p: Pull): string[] {
  return p.overlaps.map(
    (o) => `shares ${o.files.length} ${o.files.length === 1 ? "file" : "files"} with #${o.number}`,
  );
}

export function ciText(ci: CiState): string {
  switch (ci) {
    case CiState.GREEN:
      return "CI green";
    case CiState.RED:
      return "CI red";
    case CiState.RUNNING:
      return "CI running";
    case CiState.QUEUED:
      return "CI queued";
    case CiState.NONE:
      return "no CI";
    default:
      return "CI unknown";
  }
}

export function mergeableText(m: Mergeable): string {
  switch (m) {
    case Mergeable.MERGEABLE:
      return "mergeable";
    case Mergeable.CONFLICTING:
      return "conflicting";
    default:
      return "mergeability unknown";
  }
}

export function reviewText(r: Review): string {
  switch (r) {
    case Review.APPROVED:
      return "approved";
    case Review.CHANGES_REQUESTED:
      return "changes requested";
    case Review.REQUIRED:
      return "review required";
    case Review.NONE:
      return "no review";
    default:
      return "review unknown";
  }
}

export function firstFailureText(f: FailedJob | undefined): string {
  if (f === undefined || f.name === "") return "";
  const line = f.lines[0] ?? "";
  return line === "" ? f.name : f.name + ": " + line;
}

export function countsText(p: { passed: number; failed: number; pending: number }): string {
  return `${p.passed} passed · ${p.failed} failed · ${p.pending} pending`;
}

// Only web links: a forge field must not plant a javascript: URL.
function safeHref(url: string): string | null {
  return /^https?:\/\//.test(url) ? url : null;
}

function link(url: string, text: string): HTMLElement {
  const href = safeHref(url);
  if (href === null) return h("span", undefined, text);
  const a = h("a", undefined, text);
  a.href = href;
  a.rel = "noreferrer noopener";
  a.target = "_blank";
  return a;
}

function field(name: string, text: string): HTMLElement {
  const s = h("span", undefined, text);
  s.dataset.field = name;
  return s;
}

function failureLine(f: FailedJob | undefined, prefix: string): string {
  const t = firstFailureText(f);
  return t === "" ? "" : prefix + t;
}

export function renderChips(
  filter: AuthorFilter | undefined,
  onRemove: (author: string) => void,
): HTMLElement {
  const ul = h("ul");
  ul.setAttribute("aria-label", "Tracked authors");
  for (const a of filter?.authors ?? []) {
    const li = h("li", undefined, a);
    const b = h("button", undefined, "×");
    b.type = "button";
    b.setAttribute("aria-label", "Stop tracking " + a);
    b.addEventListener("click", () => onRemove(a));
    li.append(" ", b);
    ul.append(li);
  }
  return ul;
}

export function renderMain(m: BranchState | undefined): HTMLElement {
  const sec = h("section");
  sec.setAttribute("aria-label", "Main");
  if (m === undefined) {
    sec.append(h("p", undefined, "main: no state yet"));
    return sec;
  }
  const p = h("p");
  p.dataset.ci = CiState[m.ci] ?? "";
  p.append(h("strong", undefined, (m.name === "" ? "main" : m.name) + " "));
  p.append(field("sha", shaText(m.sha)), " · ", field("ci", ciText(m.ci)));
  p.append(" · ", countsText(m));
  const fail = failureLine(m.firstFailure, "first failure: ");
  if (fail !== "") p.append(" · ", fail);
  sec.append(p);
  return sec;
}

export function renderPatterns(patterns: readonly Pattern[]): HTMLElement {
  const sec = h("section");
  sec.setAttribute("aria-label", "Patterns");
  if (patterns.length === 0) return sec;
  sec.append(h("h2", undefined, "Patterns"));
  const ul = h("ul");
  for (const p of patterns) ul.append(h("li", undefined, p.message));
  sec.append(ul);
  return sec;
}

export function renderOrder(steps: readonly MergeStep[]): HTMLElement {
  const sec = h("section");
  sec.setAttribute("aria-label", "Merge order");
  if (steps.length === 0) return sec;
  sec.append(h("h2", undefined, "Merge order"));
  const ol = h("ol");
  for (const s of steps) {
    ol.append(h("li", undefined, "#" + s.number + (s.note === "" ? "" : ": " + s.note)));
  }
  sec.append(ol);
  return sec;
}

function renderJob(j: FailedJob): HTMLElement {
  const li = h("li");
  li.append(h("strong", undefined, j.name));
  if (j.lines.length > 0) li.append(h("pre", undefined, j.lines.join("\n")));
  if (j.outputRef !== "") {
    const p = h("p", undefined, "output: ");
    p.append(h("code", undefined, "magus query output " + j.outputRef));
    li.append(p);
  }
  if (j.url !== "") li.append(link(j.url, "run"));
  return li;
}

export function renderRow(p: Pull, nowMs: number, open: boolean): HTMLElement {
  const d = h("details");
  d.open = open;
  d.dataset.pull = String(p.number);
  const sum = h("summary");
  sum.append(field("number", "#" + p.number), " ", field("title", p.title), " ");
  const queued = durMs(p.queuedFor);
  const bits: string[] = [
    p.author,
    shaText(p.headSha) + (p.behindBy > 0 ? ` · ${p.behindBy} behind` : ""),
    mergeableText(p.mergeable),
    ciText(p.ci) + (queued > 0 ? " · queued " + ageText(queued) : ""),
    countsText(p),
    reviewText(p.review),
  ];
  const ff = failureLine(p.firstFailure, "first failure: ");
  if (ff !== "") bits.push(ff);
  if (p.queue !== undefined && p.queue.description !== "") {
    bits.push("queue: " + p.queue.description);
  }
  if (p.draft) bits.push("draft");
  if (p.stale) bits.push("stale");
  bits.push(...overlapText(p));
  sum.append(field("meta", bits.join(" · ")));
  const blocked = blockedText(p);
  if (blocked !== "") sum.append(" ", field("blocked", "blocked: " + blocked));
  d.append(sum);

  const body = h("div");
  if (p.failedJobs.length === 0) {
    body.append(h("p", undefined, "No failed jobs."));
  } else {
    const ol = h("ol");
    for (const j of p.failedJobs) ol.append(renderJob(j));
    body.append(ol);
  }
  const pushed = tsMs(p.pushedAt);
  if (pushed !== null) {
    body.append(h("p", undefined, "last push " + ageText(nowMs - pushed) + " ago"));
  }
  body.append(link(p.url, "Open #" + p.number + " on the forge"));
  d.append(body);
  return d;
}
