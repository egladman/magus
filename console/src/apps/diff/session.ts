// session.ts - the console's client for the shared review session.
//
// Two fetches, split because they cost different amounts. /api/v1/diff/patch is an index read that
// returns the patch in milliseconds and is what paints the screen; /api/v1/diff loads the
// symbol shards and walks a reverse closure, and is what decorates it. Waiting for the second
// before showing the first would hold a readable diff behind an overlay nobody is looking at
// yet.
//
// The second call also ATTACHES the session, which is what makes an agent able to find this
// review. Pairing therefore needs no setup step anyone has to remember: opening the app is
// joining.

import { authHeaders, readRefusal, reportFetchFailure, reportHttpStatus } from "../../lib/server";
import { reportFailure } from "../../lib/notifications";

// The wire shapes, mirroring types.Review and types.DiffReview. Hand-written rather than
// generated because these ride the plain JSON /api routes rather than a Connect service, the
// same as the insight and outputs readers beside them.

export type ReviewRole = "source" | "output" | "maintained" | "unclaimed";
export type ReviewVisibility = "internal" | "public" | "unknown";

// ReviewChange mirrors the DiffChange constants: what a changeset did to one symbol, read
// against a base graph. Absent when the review had no base, which is not "unchanged".
export type ReviewChange = "added" | "removed" | "signature" | "body";

// ReviewBump mirrors the DiffBump constants, ordered none < patch < minor < major.
export type ReviewBump = "none" | "patch" | "minor" | "major";

export interface DiffSymbol {
  readonly id: string;
  readonly label?: string;
  readonly ref_count: number;
  readonly file_count: number;
  readonly public_to?: readonly string[];
  readonly public_file_count: number;
  readonly public_beyond_workspace?: boolean;
  readonly change?: ReviewChange;
  // qualified names the symbol through its enclosing declarations (`DiffAPI.Signature`), so
  // two members sharing a label stay distinguishable.
  readonly qualified?: string;
  readonly signature?: string;
  readonly base_signature?: string;
  readonly checks?: readonly Check[];
}

// Check is one conformance finding about a symbol the change adds, renames or re-signs: a fact
// about how the rest of the workspace declares the same kind of thing, reported as advice.
export interface Check {
  readonly name: string;
  readonly status: string;
  readonly message?: string;
  readonly details?: readonly string[];
  readonly evidence?: string;
}

// DiffAPI is what the changeset did to the public API, present only when the review was
// given a base graph.
//
// floor is the smallest bump the evidence PROVES and likely is floor raised to major by a
// changed signature. Both are lower bounds: behavior moves under an unchanged signature, so
// a reader may raise either and should never lower one.
export interface DiffAPI {
  readonly base: string;
  readonly floor: ReviewBump;
  readonly likely: ReviewBump;
  readonly added: number;
  readonly removed: number;
  readonly signature: number;
  readonly body: number;
}

// DiffHunkInfo mirrors types.DiffHunk. index and digest are the address the parsed patch, the read
// marks and DiffOrder share; they are copied from the patch and never recomputed here.
export interface DiffHunkInfo {
  readonly index: number;
  readonly digest: string;
  readonly old_start: number;
  readonly old_count: number;
  readonly new_start: number;
  readonly new_count: number;
  readonly declaration?: string;
  readonly symbols?: readonly string[];
}

// DiffHunkRef mirrors types.DiffHunkRef: one hunk addressed across files.
export interface DiffHunkRef {
  readonly path: string;
  readonly index: number;
  readonly digest: string;
}

// DiffGroupKind mirrors types.DiffGroupKind. "unranked" is always the last group.
export type DiffGroupKind = "connected" | "generated" | "unranked";

// DiffWhyRelation mirrors the types.DiffWhy* constants: the relationship that placed a hunk.
export type DiffWhyRelation =
  | "starts"
  | "uses"
  | "used_by"
  | "implements"
  | "implemented_by"
  | "continues"
  | "continued_by"
  | "same_step"
  | "tests"
  | "generated"
  | "unranked";

// DiffWhy mirrors types.DiffWhy. text is the rendered sentence, so no client derives its own.
export interface DiffWhy {
  readonly relation: DiffWhyRelation;
  readonly step?: number;
  readonly symbol?: string;
  readonly cycle?: readonly string[];
  readonly text: string;
}

// DiffStepHunk mirrors types.DiffStepHunk: one placed hunk and what placed it.
export interface DiffStepHunk {
  readonly ref: DiffHunkRef;
  readonly label?: string;
  readonly why: DiffWhy;
}

// DiffStep mirrors types.DiffStep: one screen of the reading order. number counts across the
// whole order, from 1.
export interface DiffStep {
  readonly number: number;
  readonly hunks: readonly DiffStepHunk[];
}

// DiffGroup mirrors types.DiffGroup: a set of connected steps.
export interface DiffGroup {
  readonly kind: DiffGroupKind;
  readonly label?: string;
  readonly hunk_count: number;
  readonly reach: number;
  readonly steps: readonly DiffStep[];
}

// DiffOrderCount mirrors types.DiffOrderCount, the line proving every hunk appears once.
export interface DiffOrderCount {
  readonly hunk_count: number;
  readonly placed: number;
  readonly complete: boolean;
  readonly repeated?: readonly DiffHunkRef[];
  readonly missing?: readonly DiffHunkRef[];
  readonly files_without_hunks?: readonly string[];
}

// DiffOrder mirrors types.DiffOrder: the order to read the changeset's hunks in. Absent on the
// wire when magus could not compute one, which is not an empty order.
export interface DiffOrder {
  readonly groups: readonly DiffGroup[];
  readonly count: DiffOrderCount;
}

export interface DiffCoverage {
  readonly ratio: number;
  readonly covered_stmts: number;
  readonly total_stmts: number;
}

export interface DiffChurn {
  readonly commits: number;
  readonly authors?: number;
  readonly score: number;
  readonly rank?: number;
  readonly project_trend?: number;
}

export interface DiffTouch {
  readonly host?: string;
  readonly session?: string;
  readonly transcript?: string;
  readonly read?: readonly string[];
  readonly ran?: readonly string[];
}

export interface DiffAnnotation {
  readonly path: string;
  readonly project?: string;
  readonly role: ReviewRole;
  readonly hint?: string;
  readonly coverage?: DiffCoverage;
  readonly symbols?: readonly DiffSymbol[];
  // hunks is every hunk of the file's patch, in patch order, with the address the reading order
  // refers to. Mirrors types.DiffHunk.
  readonly hunks?: readonly DiffHunkInfo[];
  // null when no symbol index was loaded, which is NOT zero: "nothing references this" and
  // "nobody looked" are different facts, and the ordering depends on this one.
  readonly reach: number | null;
  readonly visibility: ReviewVisibility;
  readonly churn?: DiffChurn;
  readonly touches?: readonly DiffTouch[];
  // read_state is whether a person recorded reading this file at the content it holds NOW,
  // across sessions - distinct from the session's own viewed marks, which are this sitting's
  // navigation state and reset with the changeset.
  //
  // Absent means nobody checked, which must never render as "unread": those are opposite
  // claims and only one of them accuses. See types.DiffReadState.
  readonly read_state?: ReviewReadState;
}

// ReviewReadState mirrors the DiffReadState constants. "stale" is the one worth surfacing
// hardest: the reader looked, and then the file moved under them.
export type ReviewReadState = "read" | "unread" | "stale";

// DiffUncoveredReason mirrors the DiffUncoveredReason constants: why the conformance checks
// could not see a touched project.
export type DiffUncoveredReason = "no-indexer";

export interface Diff {
  readonly base: string;
  readonly files?: readonly DiffAnnotation[];
  readonly seed_projects?: readonly string[];
  readonly affected_projects?: readonly { path: string; seed: boolean }[];
  readonly notes?: readonly string[];
  readonly api?: DiffAPI;
  // order is the changeset's hunks in the order to read them. Absent means the server could not
  // compute one, and focus mode then falls back to the files' own order.
  readonly order?: DiffOrder;
  // Why the conformance checks could not run. When set, no symbol carries checks, and that
  // absence means nothing was checked.
  readonly conformance_error?: {
    readonly code: string;
    readonly message: string;
    readonly url?: string;
  };
  // Touched projects the conformance checks could not see, and why.
  readonly uncovered?: readonly {
    readonly project: string;
    readonly reason: DiffUncoveredReason;
  }[];
}

// CommentCredential mirrors types.Credential: the verified bearer, never its secret. id is the
// identity; name is a label a later token can reuse.
export interface CommentCredential {
  readonly class?: "operator" | "token" | "share";
  readonly id?: string;
  readonly name?: string;
  readonly grant?: { readonly tokens?: string; readonly mcp?: string; readonly console?: string };
}

export interface CommentOrigin {
  readonly user?: string;
  readonly entry_point?: string;
  readonly host?: string;
  readonly credential?: CommentCredential;
}

export interface DiffComment {
  readonly id: string;
  readonly path: string;
  readonly hunk: number;
  // The door the remark came through, stamped by the server. "unattributed" is the review
  // route: a draft its reader may publish or discard. It does not say a person wrote it.
  readonly author: "unattributed" | "agent";
  // Where the write came from: the OS account, the entry point, and the credential or MCP
  // client that carried it.
  readonly origin?: CommentOrigin;
  readonly agent_name?: string;
  readonly body: string;
  readonly resolved: boolean;
  // published is whether this remark has left for the host. A draft is private until it does,
  // which is the whole point of drafting: a review is a pass, and the fifth remark often
  // changes your mind about the first.
  readonly published?: boolean;
  // line is the new-side line the remark anchors to. A hunk index cannot serve - it means
  // nothing outside the session that produced it - so a draft carrying no line is one no host
  // can place, and the publisher drops it rather than guessing.
  readonly line?: number;
}

// ReviewComment is one comment already on the host's review, written by anybody. Read-only:
// the host holds the record every participant sees, so a reply goes through the provider
// rather than editing a local copy that would silently diverge.
export interface ReviewComment {
  readonly id: string;
  readonly path: string;
  readonly line: number;
  // hunk is the index within path's hunks of the one holding line, or -1 when no hunk in this
  // changeset does. Resolved by the server, not here: the arithmetic is the only hard part of
  // placing a thread, and two apps doing it independently is the same remark sitting
  // against different code in the terminal and the browser.
  readonly hunk: number;
  readonly author: string;
  readonly body: string;
  // new reports that this thread had not been on screen before. magus's annotation rather than
  // anything the host said, and it is true only on the response that first carried the thread.
  readonly new?: boolean;
  // root is the id of the first comment of the conversation this one belongs to, empty on the
  // root itself. The wire is flat, one record per comment, and the conversation is assembled at
  // render time (see conversations in rows.ts).
  readonly root?: string;
  // outdated reports that the line this comment was made on no longer exists in the head.
  readonly outdated?: boolean;
  // diff_hunk is the host's own text of the hunk the comment was made on, kept so an outdated
  // conversation still shows the code it was about.
  readonly diff_hunk?: string;
}

// ReviewInfo is which review this branch has open, plus what has already been said on it.
//
// A closed target always carries a reason - "no review provider wired", "no pull request for
// this branch", a host that did not answer - and NONE of them is an error. The reader's
// options are identical in all three, so the app states the reason and moves on.
export interface ReviewInfo {
  // The review's identity in the provider's own terms, opaque here. A string rather than a
  // number, though GitHub and GitLab both count: Gerrit and Phabricator identify a change by a
  // hash, and a numeric field would have made those providers unwritable for no gain.
  readonly id: string;
  readonly repo?: string;
  // host is where publishing would send to. Named in the UI before anything leaves, because an
  // Enterprise appliance and github.com are the same feature and very different destinations,
  // and the reader is the only one who can tell whether the one on screen is the one they meant.
  readonly host?: string;
  // state is what the host says became of the review: "open", "merged" or "closed". Absent when
  // the provider does not answer it, which reads as open.
  readonly state?: string;
  readonly reason?: string;
  // verdicts are the verdicts this reviewer may publish, decided by the server. The app
  // renders exactly these and never works the permission out for itself: a rule re-implemented
  // in a browser is one that eventually disagrees with the one the publish path enforces.
  //
  // Absent means a server too old to have an opinion, which reads as remarks only.
  readonly verdicts?: readonly ReviewVerdict[];
  // verdict_limit says WHY the set is only remarks: your own change, or a provider that did not
  // name either party. Different facts, and an app that renders them alike misleads.
  readonly verdict_limit?: string;
  readonly threads: readonly ReviewComment[];
}

// ReviewVerdict is what a published review says about the change. Mirrors types.ReviewVerdict,
// and the values are the wire spelling rather than any host's vocabulary.
export type ReviewVerdict = "comment" | "approve" | "request_changes";

export interface DiffSuggestion {
  readonly id: string;
  readonly path: string;
  readonly hunk: number;
  readonly agent_name?: string;
  readonly reason: string;
  readonly accepted: boolean;
  readonly declined: boolean;
}

// DiffOutline is what an agent suggests a conversation should cover, held for the person to read.
// Mirrors types.DiffOutline. It is a pointer to topics, never a draft: the person types the reply.
export interface DiffOutline {
  // thread is the id of the conversation's root comment.
  readonly thread: string;
  readonly topics: readonly string[];
  readonly agent_name?: string;
}

export interface DiffReview {
  readonly id: string;
  readonly base: string;
  // The patch identity the server used to compute this session. Context requests carry it back
  // to the server, so a reviewer never sees current-file lines presented as context for an older
  // patch after the working tree has moved.
  readonly as_of?: string;
  readonly diff: Diff;
  readonly cursor: { path?: string; hunk: number };
  readonly viewed?: readonly string[];
  readonly comments?: readonly DiffComment[];
  readonly suggestions?: readonly DiffSuggestion[];
  readonly outlines?: readonly DiffOutline[];
}

import type { WireFile } from "./parse";

export interface DiffResponse {
  // The changeset arrives PARSED. The server owns the reader, including the hunk digests a
  // read receipt is keyed by, so this app never hashes anything.
  readonly files: readonly WireFile[];
  // The same changeset as raw text. Unused here, and kept on the type because the route sends
  // it - a caller wanting the interchange format itself has it without a second request.
  readonly patch: string;
  // digest identifies the changeset as a whole, for telling a current answer from a frozen one.
  readonly digest: string;
  readonly clean: boolean;
}

export interface DiffContext {
  readonly path: string;
  readonly as_of: string;
  readonly start: number;
  readonly lines: readonly string[];
}

// Fetches user-requested working-tree context for one hunk.
export async function fetchContext(
  host: string,
  path: string,
  asOf: string,
  start: number,
  end: number,
  signal: AbortSignal,
): Promise<DiffContext> {
  const q = new URLSearchParams({
    path,
    as_of: asOf,
    start: String(start),
    end: String(end),
    radius: "12",
  });
  const res = await fetch(`http://${host}/api/v1/diff/context?${q}`, {
    headers: authHeaders(),
    signal,
  });
  if (!res.ok) throw new HttpError(res.status);
  return (await res.json()) as DiffContext;
}

// fetchPatch reads the working tree's unified patch. Fast path, painted immediately.
export async function fetchPatch(host: string, signal: AbortSignal): Promise<DiffResponse> {
  const res = await fetch(`http://${host}/api/v1/diff/patch`, { headers: authHeaders(), signal });
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
): Promise<DiffReview> {
  const q = paths.map((p) => `path=${encodeURIComponent(p)}`).join("&");
  const res = await fetch(`http://${host}/api/v1/diff?${q}`, { headers: authHeaders(), signal });
  if (!res.ok) throw new HttpError(res.status);
  return (await res.json()) as DiffReview;
}

// Fetches the live session without replacing the review snapshot.
export async function fetchReviewSession(host: string, signal: AbortSignal): Promise<DiffReview> {
  const res = await fetch(`http://${host}/api/v1/diff/session`, { headers: authHeaders(), signal });
  if (!res.ok) throw new HttpError(res.status);
  return (await res.json()) as DiffReview;
}

// SessionOp is one mutation of the human's half of the session. Every one of these is stamped
// ReviewAuthorHuman by the server because it arrives on this route; an agent reaches the
// session through MCP and is stamped there. Authorship is decided by transport, never by
// payload, so nothing here needs to (or can) assert who is writing.
// SeenOp is the reader's claim that these threads have been put in front of them, and it is the
// only thing that advances the watermark deciding what counts as new.
//
// The client says it because only the client knows. The review lookup used to advance the mark as
// it composed the response, so an aborted fetch, a refresh mid-flight or a second tab consumed the
// badges - and the notification with them, since it compares against the same watermark.
export interface SeenOp {
  readonly op: "seen";
  readonly ids: readonly string[];
}

export type SessionOp =
  | { op: "cursor"; path: string; hunk: number }
  | { op: "viewed"; digest: string; on: boolean }
  | { op: "comment"; path: string; hunk: number; line?: number; body: string }
  | { op: "discard"; id: string }
  | { op: "resolve"; id: string; on: boolean }
  | { op: "answer"; id: string; on: boolean }
  | SeenOp;

// mutate applies one op and returns the updated session.
//
// Failures RESOLVE to null rather than throwing. Every caller is a keypress handler, and a
// review that threw because a cursor sync failed would take the whole app down over
// bookkeeping the reader never asked for. The local view is already correct; the server copy
// catching up is best-effort.
export async function mutate(
  host: string,
  op: SessionOp,
  signal: AbortSignal,
): Promise<DiffReview | null> {
  try {
    const res = await fetch(`http://${host}/api/v1/diff/session`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify(op),
      signal,
    });
    if (!res.ok) {
      reportHttpStatus(host, "the review session", res.status);
      return null;
    }
    return (await res.json()) as DiffReview;
  } catch (e) {
    reportFetchFailure(host, "the review session", e);
    return null;
  }
}

// fetchReview reads which review is open and the threads already on it.
//
// Its own request, made AFTER the diff is on screen. This is the one call that leaves the
// machine, and a reader must never wait on somebody else's forge to see their own changes.
//
// Failure resolves to a closed target rather than throwing, matching the server: the app
// says the review could not be reached and stays a diff viewer, which is what it was before
// any of this existed.
export async function fetchReview(host: string, signal: AbortSignal): Promise<ReviewInfo> {
  try {
    const res = await fetch(`http://${host}/api/v1/diff/review`, {
      headers: authHeaders(),
      signal,
    });
    if (!res.ok) {
      reportHttpStatus(host, "the open review", res.status);
      return { id: "", reason: `server answered ${res.status}`, threads: [] };
    }
    return (await res.json()) as ReviewInfo;
  } catch (e) {
    reportFetchFailure(host, "the open review", e);
    return { id: "", reason: "the server could not be reached", threads: [] };
  }
}

// publish sends every unpublished draft as one review and returns the updated session.
//
// It does NOT go through mutate, and that is the point. Every other write on this route is
// bookkeeping the reader did not ask about, so a failure is swallowed and the local view stays
// correct. This one puts sentences in front of colleagues: a swallowed failure would leave the
// reader believing their review landed when it never left, and they would find out from the
// colleague who never replied.
//
// The server derives WHICH drafts go - unpublished, and written by the person - from the
// session itself. Nothing is named here, so nothing here can widen the set.
export async function publish(
  host: string,
  summary: string,
  verdict: ReviewVerdict,
  signal: AbortSignal,
): Promise<DiffReview> {
  const res = await fetch(`http://${host}/api/v1/diff/session`, {
    method: "POST",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify({ op: "publish", summary, verdict }),
    signal,
  });
  if (!res.ok) {
    // The server's body is the reason - "no pull request for this branch", "no credential",
    // an HTTP status from the host - and it is the only thing that tells the reader which of
    // several unrelated situations they are in.
    throw new Error((await readRefusal(res))?.message ?? `server answered ${res.status}`);
  }
  return (await res.json()) as DiffReview;
}

// reply answers one conversation on the host's review.
//
// root is the id of the conversation's first comment: a thread's own `root`, or its `id` when it
// is itself a root (see rootOf in rows.ts). The host threads a reply by its root, so any other
// comment's id would start a second conversation instead of joining this one.
//
// Throws like publish and for the same reason: it is a sentence a colleague is waiting for,
// and a reader told it was sent when it never left will believe the conversation is finished.
//
// It returns nothing useful, deliberately. A reply belongs to the host's record, so the way to
// see it is to re-read the review - which is also how the reader finds out what everyone else
// said while they were typing.
export async function reply(
  host: string,
  root: string,
  body: string,
  signal: AbortSignal,
): Promise<void> {
  const res = await fetch(`http://${host}/api/v1/diff/session`, {
    method: "POST",
    headers: { ...authHeaders(), "Content-Type": "application/json" },
    body: JSON.stringify({ op: "reply", root, body }),
    signal,
  });
  if (!res.ok) {
    throw new Error((await readRefusal(res))?.message ?? `server answered ${res.status}`);
  }
}

// fetchThreadBrief reads the brief for one conversation: the text a person pastes to their own
// model, built by the server from the graph. root is the conversation's first comment (see
// rootOf in rows.ts).
//
// Reading it posts nothing and sends nothing anywhere; carrying it to a model is the person's act.
// A refusal (no review open, no such conversation) surfaces as a toast carrying the server's own
// words and resolves to null, so the caller copies nothing.
export async function fetchThreadBrief(
  host: string,
  root: string,
  signal: AbortSignal,
): Promise<string | null> {
  const what = "the thread brief";
  try {
    const q = new URLSearchParams({ id: root });
    const res = await fetch(`http://${host}/api/v1/diff/thread?${q}`, {
      headers: authHeaders(),
      signal,
    });
    if (!res.ok) {
      const refusal = res.status === 401 ? null : await readRefusal(res);
      if (refusal) reportFailure("Review", refusal.message, `thread:${res.status}`, refusal.help);
      else reportHttpStatus(host, what, res.status);
      return null;
    }
    const body = (await res.json()) as { brief?: unknown };
    if (typeof body.brief !== "string" || body.brief === "") {
      reportFailure("Review", "The server sent no brief for this thread.", "thread:empty");
      return null;
    }
    return body.brief;
  } catch (e) {
    reportFetchFailure(host, what, e);
    return null;
  }
}

// Reading is what the reading op answers: whether this review is marked as being read, since
// when (unix milliseconds), and the command the PERSON may run to tell the review's participants.
export interface Reading {
  readonly reading: boolean;
  readonly since?: number;
  readonly command?: string;
}

// setReading marks, or unmarks, the review as being read right now.
//
// The mark is local to magus. command is a line for the person to copy and run if they want
// colleagues to see it; nothing here runs it, because the one sentence that leaves the machine is
// typed by a person.
//
// A refusal (502 no review, 409 already merged) surfaces as a toast carrying the server's own
// words and resolves to null, so the toggle can put itself back: the mark did not change.
export async function setReading(
  host: string,
  on: boolean,
  signal: AbortSignal,
): Promise<Reading | null> {
  try {
    const res = await fetch(`http://${host}/api/v1/diff/session`, {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({ op: "reading", on }),
      signal,
    });
    if (!res.ok) {
      const refusal = res.status === 401 ? null : await readRefusal(res);
      if (refusal) reportFailure("Review", refusal.message, `reading:${res.status}`, refusal.help);
      else reportHttpStatus(host, "the reading mark", res.status);
      return null;
    }
    return (await res.json()) as Reading;
  } catch (e) {
    reportFetchFailure(host, "the reading mark", e);
    return null;
  }
}

// BranchChange is one other line of work and the paths it changes, as of the reader's LAST FETCH.
// magus does not fetch to answer this - going to the network for it would be an act nobody asked
// for - so anything rendered from it says which moment it describes.
export interface BranchChange {
  readonly ref: string;
  readonly paths: readonly string[];
  // local separates a branch in this repository from a remote-tracking copy of somebody else's.
  // The two differ in what the answer is AS OF: a local branch is current, a tracking copy is
  // exactly as fresh as the last fetch. Optional, so a server older than the field reads as
  // remote-tracking - which is what every answer was before it existed.
  readonly local?: boolean;
}

// fetchBranches asks which other branches are changing these files.
//
// Its own route because it forks once per branch, so it must never hold the patch.
//
// `unsupported` is why this returns a pair rather than a list. An empty list means "nothing else
// is touching these files", which is reassurance; a backend that has not implemented the lookup
// is a gap in magus. Collapsing both into emptiness tells the reader the first when the truth is
// the second, and a marker whose absence cannot be trusted is worth nothing.
// RunVerdict is what /api/v1/diff/run answers with, for both the submit and the poll.
export interface RunVerdict {
  readonly state: "running" | "passed" | "failed" | "unknown";
  readonly started?: boolean;
  readonly finished_ms?: number;
  readonly duration_ms?: number;
  readonly error?: string;
  readonly undeclared?: string;
  readonly available?: readonly string[];
}

// runTarget asks the local workspace to run one declared target for one project, or - with
// start=false - just reports what the last run of it decided.
//
// This is the one review capability with no provider or backend behind it: it asks the machine
// the code is on, so it behaves identically on GitHub, GitLab, git, hg, or no forge at all. The
// server refuses any target the magusfile does not declare for that project, which is what keeps
// a browser-reachable button from being able to name arbitrary work.
//
// A transport failure reports "unknown" rather than "failed": the run did not fail, the question
// did, and rendering those the same way puts a red mark on code nobody judged.
export async function runTarget(
  host: string,
  target: string,
  project: string,
  start: boolean,
  signal: AbortSignal,
): Promise<RunVerdict> {
  try {
    const url = `http://${host}/api/v1/diff/run`;
    const res = start
      ? await fetch(url, {
          method: "POST",
          headers: { ...authHeaders(), "Content-Type": "application/json" },
          body: JSON.stringify({ target, project }),
          signal,
        })
      : await fetch(
          `${url}?target=${encodeURIComponent(target)}&project=${encodeURIComponent(project)}`,
          { headers: authHeaders(), signal },
        );
    if (!res.ok) {
      reportHttpStatus(host, "the run of " + target, res.status);
      return { state: "unknown" };
    }
    const body = (await res.json()) as RunVerdict;
    const state = body?.state;
    if (state !== "running" && state !== "passed" && state !== "failed") {
      return { state: "unknown", undeclared: body?.undeclared, available: body?.available };
    }
    return body;
  } catch (e) {
    reportFetchFailure(host, "the run of " + target, e);
    return { state: "unknown" };
  }
}

export async function fetchBranches(
  host: string,
  signal: AbortSignal,
): Promise<{ branches: BranchChange[]; unsupported: string }> {
  try {
    const res = await fetch(`http://${host}/api/v1/diff/branches`, {
      headers: authHeaders(),
      signal,
    });
    if (!res.ok) {
      reportHttpStatus(host, "the branch list", res.status);
      return { branches: [], unsupported: "" };
    }
    const body = (await res.json()) as { branches?: BranchChange[]; unsupported?: string };
    const got = body.branches ?? [];
    // Shape-checked, not just cast. `Paths []string` on the Go side has no omitempty, so a nil
    // slice marshals to `null` - and iterating that throws inside a `void`-ed caller, where the
    // rejection is swallowed and the feature simply never appears with nothing logged anywhere.
    return {
      branches: got.filter((b) => typeof b?.ref === "string" && Array.isArray(b.paths)),
      unsupported: typeof body.unsupported === "string" ? body.unsupported : "",
    };
  } catch (e) {
    reportFetchFailure(host, "the branch list", e);
    return { branches: [], unsupported: "" };
  }
}

// HttpError carries the status so a caller can tell "no workspace yet" (503) from a real
// failure and render the right empty state.
export class HttpError extends Error {
  readonly status: number;
  constructor(status: number) {
    super(`server answered ${status}`);
    this.status = status;
  }
}

// reportSessionFailure reports what a throwing read here (fetchSession, fetchReviewSession) failed
// with, since those leave the reporting to the caller.
export function reportSessionFailure(host: string, what: string, e: unknown): void {
  if (e instanceof HttpError) reportHttpStatus(host, what, e.status);
  else reportFetchFailure(host, what, e);
}
