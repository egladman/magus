// session.ts - the console's client for the shared review session.
//
// Two fetches, split because they cost different amounts. /api/v1/diff is an index read that
// returns the patch in milliseconds and is what paints the screen; /api/v1/review loads the
// symbol shards and walks a reverse closure, and is what decorates it. Waiting for the second
// before showing the first would hold a readable diff behind an overlay nobody is looking at
// yet.
//
// The second call also ATTACHES the session, which is what makes an agent able to find this
// review. Pairing therefore needs no setup step anyone has to remember: opening the surface is
// joining.

import { authHeaders } from "../../lib/daemon";

// The wire shapes, mirroring types.Review and types.ReviewSession. Hand-written rather than
// generated because these ride the plain JSON /api routes rather than a Connect service, the
// same as the insight and outputs readers beside them.

export type ReviewRole = "source" | "output" | "maintained" | "unclaimed";
export type ReviewSurface = "internal" | "public" | "unknown";

export interface ReviewSymbol {
  readonly id: string;
  readonly label?: string;
  readonly ref_count: number;
  readonly file_count: number;
  readonly external_projects?: readonly string[];
  readonly external_file_count: number;
  readonly module_api?: boolean;
}

export interface ReviewCoverage {
  readonly ratio: number;
  readonly covered_stmts: number;
  readonly total_stmts: number;
}

export interface ReviewFile {
  readonly path: string;
  readonly project?: string;
  readonly role: ReviewRole;
  readonly hint?: string;
  readonly coverage?: ReviewCoverage;
  readonly symbols?: readonly ReviewSymbol[];
  readonly reach: number;
  readonly surface: ReviewSurface;
}

export interface Review {
  readonly base: string;
  readonly files?: readonly ReviewFile[];
  readonly seed_projects?: readonly string[];
  readonly affected_projects?: readonly { path: string; seed: boolean }[];
  readonly notes?: readonly string[];
}

export interface ReviewComment {
  readonly id: string;
  readonly path: string;
  readonly hunk: number;
  readonly author: "human" | "agent";
  readonly agent_name?: string;
  readonly body: string;
  readonly resolved: boolean;
}

export interface ReviewSuggestion {
  readonly id: string;
  readonly path: string;
  readonly hunk: number;
  readonly agent_name?: string;
  readonly reason: string;
  readonly accepted: boolean;
  readonly declined: boolean;
}

export interface ReviewSession {
  readonly id: string;
  readonly base: string;
  readonly review: Review;
  readonly cursor: { path?: string; hunk: number };
  readonly viewed?: readonly string[];
  readonly comments?: readonly ReviewComment[];
  readonly suggestions?: readonly ReviewSuggestion[];
}

export interface DiffResponse {
  readonly patch: string;
  readonly clean: boolean;
}

// fetchPatch reads the working tree's unified patch. Fast path, painted immediately.
export async function fetchPatch(host: string, signal: AbortSignal): Promise<DiffResponse> {
  const res = await fetch(`http://${host}/api/v1/diff`, { headers: authHeaders(), signal });
  if (!res.ok) throw new HttpError(res.status);
  return (await res.json()) as DiffResponse;
}

// fetchSession reads the annotated changeset and attaches the shared session.
//
// The paths are the ones the caller is ACTUALLY reviewing, taken from the patch it already
// parsed rather than re-derived server-side. Re-deriving would race an edit made since the
// patch was read and annotate a file the reader cannot see.
export async function fetchSession(
  host: string,
  paths: readonly string[],
  signal: AbortSignal,
): Promise<ReviewSession> {
  const q = paths.map((p) => `path=${encodeURIComponent(p)}`).join("&");
  const res = await fetch(`http://${host}/api/v1/review?${q}`, { headers: authHeaders(), signal });
  if (!res.ok) throw new HttpError(res.status);
  return (await res.json()) as ReviewSession;
}

// SessionOp is one mutation of the human's half of the session. Every one of these is stamped
// ReviewAuthorHuman by the daemon because it arrives on this route; an agent reaches the
// session through MCP and is stamped there. Authorship is decided by transport, never by
// payload, so nothing here needs to (or can) assert who is writing.
export type SessionOp =
  | { op: "cursor"; path: string; hunk: number }
  | { op: "viewed"; digest: string; on: boolean }
  | { op: "comment"; path: string; hunk: number; body: string; anchor?: string }
  | { op: "resolve"; id: string; on: boolean }
  | { op: "answer"; id: string; on: boolean };

// mutate applies one op and returns the updated session.
//
// Failures RESOLVE to null rather than throwing. Every caller is a keypress handler, and a
// review that threw because a cursor sync failed would take the whole surface down over
// bookkeeping the reader never asked for. The local view is already correct; the server copy
// catching up is best-effort.
export async function mutate(
  host: string,
  op: SessionOp,
  signal: AbortSignal,
): Promise<ReviewSession | null> {
  try {
    const res = await fetch(`http://${host}/api/v1/review/session`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify(op),
      signal,
    });
    if (!res.ok) return null;
    return (await res.json()) as ReviewSession;
  } catch {
    return null;
  }
}

// HttpError carries the status so a caller can tell "no workspace yet" (503) from a real
// failure and render the right empty state.
export class HttpError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(`daemon answered ${status}`);
    this.status = status;
  }
}

// hunkDigest MUST match internal/review.HunkDigest byte for byte, or a mark made in the
// console is invisible to the CLI and to an agent reading the same session.
//
// Path plus body, NUL-separated, each line newline-terminated; the hunk header is excluded
// because its line numbers move whenever anything above it changes, and digesting them would
// reset every mark in a file on any edit near the top. SHA-256, first 16 hex characters.
export async function hunkDigest(path: string, lines: readonly string[]): Promise<string> {
  const enc = new TextEncoder();
  const parts: Uint8Array[] = [enc.encode(path), new Uint8Array([0])];
  for (const l of lines) parts.push(enc.encode(`${l}\n`));
  let n = 0;
  for (const p of parts) n += p.length;
  const buf = new Uint8Array(n);
  let off = 0;
  for (const p of parts) {
    buf.set(p, off);
    off += p.length;
  }
  const hash = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("")
    .slice(0, 16);
}
