// jobs.ts - the job model: the read, the tree a job list makes, and the placement that tree is
// drawn at. Everything but the two service calls is pure and DOM-free, so what a reader ends up
// seeing is decided by code a test can run without a browser (jobs.test.ts). The surface file next
// door owns the SVG, the poll, and the keyboard.
//
// ONE KIND OF THING. A job is work with a HOLDER: the daemon holds its own maintenance jobs, and a
// session holds the ones an orchestrator handed out. JobService.ListJobs returns both, so this
// module never branches on which it has - a catalog job is a root with no children, which the tree
// below already draws.
//
// The activity feeds next to the view are what actually ran. Joining the two is the whole point:
// a job with no runs under it is work nobody started, and a run under no job is work nobody
// declared.

import { createClient, type Client } from "@connectrpc/connect";
import {
  JobHolder,
  JobService,
  SubmitState,
  type Job,
  type JobOverlap,
} from "@wire/job/v1alpha1/job_pb";
import { createDaemonTransport, getLiveToken, isCapabilityDenied } from "../../lib/daemon";
import { errMessage } from "../../lib/guards";
import { humanBytes } from "../activity/adapter";
import { layoutLayered, LAYERED_COL_W, LAYERED_ROW_H } from "../graph/layout";
import type { GLink, GNode } from "../graph/types";
// The row shape stays in lockstep with the drawer that defines it without this module pulling the
// drawer's protobuf in behind it. The VALUE side (runningRows/recentRows) is imported by main.ts,
// where that cost is already being paid.
import type { ActivityRow } from "../activityDrawer";

// ---- states ----------------------------------------------------------------

// The five states a job can be in. no_return is its OWN state and is never folded into fail: a
// worker that failed reported a failure, and one that never returned reported nothing at all. The
// second is the one that needs a human, because nobody is coming to tell you about it.
export const JOB_STATES = ["declared", "running", "pass", "fail", "no_return"] as const;
export type JobState = (typeof JOB_STATES)[number];

// STATE_LABEL is the word a reader sees. "no-return" is hyphenated everywhere - in the node mark's
// tooltip, the list, the detail, and the overview call-out - so the one state that most needs to be
// recognized always reads the same.
export const STATE_LABEL: Record<JobState, string> = {
  declared: "declared",
  running: "running",
  pass: "pass",
  fail: "fail",
  no_return: "no-return",
};

// STATE_MARK is the NON-COLOR channel on a node. Color alone fails WCAG 1.4.1, and this is a
// surface whose entire content is five states told apart - the diff surface made the same call for
// its add/delete markers. Short enough to sit inside a 152-unit-wide node beside the id.
export const STATE_MARK: Record<JobState, string> = {
  declared: "D",
  running: "R",
  pass: "OK",
  fail: "FAIL",
  no_return: "NR",
};

// Who runs it, in the one word the list and the detail both use.
export const HOLDER_LABEL: Record<JobHolder, string> = {
  [JobHolder.UNSPECIFIED]: "",
  [JobHolder.DAEMON]: "daemon",
  [JobHolder.SESSION]: "session",
};

const TERMINAL: readonly JobState[] = ["pass", "fail", "no_return"];

// isTerminal is "this job is done, however it ended". A terminal job is not competing for its paths
// and nobody is going to touch it again, which is why neither the overlap warning nor the staleness
// one is ever drawn on one.
export function isTerminal(s: JobState): boolean {
  return TERMINAL.includes(s);
}

// STALE_AFTER_MS is a RENDERING decision and lives here rather than in any config: it says when
// this view starts drawing attention to a job, and nothing downstream reads it. Ten minutes,
// because a job is re-put on every state change and a working one moves far more often than that -
// long enough that a normal running job is never called stale, short enough that a worker that died
// is noticed while the reader still remembers spawning it. The store transitions nothing on its
// own; what a stale job MEANS stays the reader's call.
export const STALE_AFTER_MS = 10 * 60 * 1000;

// isStale is that threshold applied to one job. A terminal job is never stale - it is finished -
// and one with no timestamp is not either: an unstamped job is a fact about a daemon that did not
// send one, and dressing it as a dead worker would be an invented alarm.
export function isStale(terminal: boolean, updatedSec: number, nowMs: number): boolean {
  if (terminal || updatedSec <= 0) return false;
  return nowMs - updatedSec * 1000 >= STALE_AFTER_MS;
}

// ageLabel is how long ago the job was last touched, at the coarsest granularity that still answers
// the question. "" when it carries no timestamp, so the caller renders nothing rather than a
// confident "0s" about a time nobody recorded.
export function ageLabel(updatedSec: number, nowMs: number): string {
  if (!Number.isFinite(updatedSec) || updatedSec <= 0) return "";
  const secs = Math.max(0, Math.round(nowMs / 1000 - updatedSec));
  if (secs < 60) return secs + "s";
  if (secs < 3600) return Math.floor(secs / 60) + "m";
  if (secs < 86400) return Math.floor(secs / 3600) + "h";
  return Math.floor(secs / 86400) + "d";
}

// normalizeState maps whatever the daemon said onto the five known states. An unrecognized value (a
// newer daemon, a state this console predates) becomes "declared" - the least-claiming of the five,
// because it asserts only that the job exists. Nothing unknown may ever read as a pass or a fail.
// The raw string is kept on the node so the detail can show what was actually served.
export function normalizeState(v: unknown): JobState {
  return typeof v === "string" && (JOB_STATES as readonly string[]).includes(v)
    ? (v as JobState)
    : "declared";
}

// str is the one string coercion the derived target plan reads the wire through (run.ts), so a
// field that is not a string becomes "" in exactly one way rather than two that could drift. The
// job side needs none of it: protobuf already typed every field before it got here.
export function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// jobKey is the id a job is drawn, selected and joined by: the bare id the store records, falling
// back to the last segment of the "jobs/{job}" resource name. Both spellings name the same job, and
// the bare one is what `magus server job <name>` takes.
export function jobKey(job: Job): string {
  if (job.id) return job.id;
  return job.name.startsWith("jobs/") ? job.name.slice("jobs/".length) : job.name;
}

// sizeLine is how much there is to maintain, for a job that maintains something. Both halves are
// optional on the wire - a job that reconciles rather than trims reports no size - so a job with
// neither renders nothing rather than "0 B".
export function sizeLine(job: Job): string {
  const parts: string[] = [];
  const size = job.target ? Number(job.target.sizeBytes) : 0;
  const count = job.target ? Number(job.target.itemCount) : 0;
  if (size > 0) parts.push(humanBytes(size));
  if (count > 0) parts.push(count + (count === 1 ? " item" : " items"));
  return parts.join(", ");
}

// lastRunLine is when the job last finished and whether it worked. "" when it has not run under
// this daemon, which is a different fact from a run that failed and must not read as one.
export function lastRunLine(job: Job, nowMs: number): string {
  const last = job.lastRun;
  if (!last?.endTime) return "";
  const age = ageLabel(Number(last.endTime.seconds), nowMs);
  return "last run " + (age ? age + " ago" : "just now") + (last.ok ? "" : " (failed)");
}

// ---- the tree model --------------------------------------------------------

export interface JobNode {
  readonly id: string;
  readonly job: Job;
  readonly state: JobState;
  // Exactly what the daemon said, "" when it said nothing. Shown in the detail whenever it is not
  // one of the five, so an unrecognized state is visible rather than quietly rendered as declared.
  readonly rawState: string;
  // The parent this listing can actually resolve, or null. A job naming a parent the listing does
  // not carry is a root HERE (there is nothing to hang it under) but is not really one, so the name
  // it gave is kept in danglingParent rather than discarded.
  readonly parent: string | null;
  readonly danglingParent: string;
  readonly children: readonly string[];
  readonly depth: number;
  readonly readOnly: boolean;
  readonly holder: JobHolder;
  // The reported pairs this job is IN, both sides of each kept so the list can name the other job
  // and the paths without going back to the model. Empty on every job when the daemon reports no
  // overlaps, which is the ordinary case.
  readonly overlaps: readonly JobOverlap[];
}

// A JobEdge always runs left to right in the drawing: `from` is the parent or the dependency, `to`
// is the child or the dependent. One list carries both kinds so the renderer draws them in a fixed
// order and the layout sees the identical list - the two must not diverge, or an edge is drawn
// between nodes that were placed as if it did not exist.
export interface JobEdge {
  readonly from: string;
  readonly to: string;
  readonly kind: "parent" | "depends_on";
}

export interface JobTree {
  readonly nodes: readonly JobNode[];
  readonly edges: readonly JobEdge[];
  readonly roots: readonly string[];
  readonly byId: ReadonlyMap<string, JobNode>;
  readonly counts: Readonly<Record<JobState, number>>;
  // The jobs that named a parent this listing does not carry. A partial listing is a fact worth
  // reporting, not a shape to silently flatten.
  readonly dangling: readonly string[];
  // The overlapping pairs, exactly as the daemon reported them and in its order.
  readonly overlaps: readonly JobOverlap[];
}

// buildJobTree assembles the tree. It is total over any job list, including the ones an agent
// writing its own rows produces: a duplicate id (the first wins), a job that is its own parent, a
// parent cycle, a depends_on naming something that is not here. None of those may hang or throw -
// the reader gets the tree that CAN be drawn plus, where it matters, a note about what could not.
export function buildJobTree(jobs: readonly Job[], overlaps: readonly JobOverlap[] = []): JobTree {
  const byId = new Map<string, JobNode>();
  const kept: { id: string; job: Job }[] = [];
  for (const job of jobs) {
    const id = jobKey(job);
    // A job with no usable id is dropped rather than becoming a node no edge can reach and no run
    // can join to. First entry wins on a duplicate: something has to, and a later one silently
    // replacing an earlier one would move edges under a reader mid-poll.
    if (!id || byId.has(id)) continue;
    byId.set(id, placeholder(id, job));
    kept.push({ id, job });
  }

  const childrenOf = new Map<string, string[]>();
  for (const u of kept) childrenOf.set(u.id, []);

  // Resolve each job's parent first, so depth and children both read one answer.
  const parentOf = new Map<string, string | null>();
  const dangling: string[] = [];
  for (const u of kept) {
    const named = u.job.parent;
    if (!named || named === u.id) {
      // A job that is its own parent is a typo in a table someone typed. Treating it as a root is
      // the only reading that draws something; a self-edge would be a loop in the layout.
      parentOf.set(u.id, null);
      continue;
    }
    if (!byId.has(named)) {
      parentOf.set(u.id, null);
      dangling.push(u.id);
      continue;
    }
    parentOf.set(u.id, named);
    childrenOf.get(named)?.push(u.id);
  }

  // Depth by walking up, with a guard: a parent cycle (a -> b -> a) bottoms out at 0 rather than
  // recursing forever. Depth only drives list indentation, so a cycle reads as flat, and the
  // layout's own cycle-break is what keeps the drawn edges honest about it.
  const depthOf = new Map<string, number>();
  for (const u of kept) {
    let d = 0;
    const seen = new Set<string>([u.id]);
    let at = parentOf.get(u.id) ?? null;
    while (at && !seen.has(at)) {
      seen.add(at);
      d++;
      at = parentOf.get(at) ?? null;
    }
    depthOf.set(u.id, at ? 0 : d);
  }

  const counts: Record<JobState, number> = {
    declared: 0,
    running: 0,
    pass: 0,
    fail: 0,
    no_return: 0,
  };
  // A pair naming a job this listing does not carry is dropped rather than drawn on the one side it
  // can reach: a warning that cannot say who the other party is asks a reader to go find something
  // that is not on their screen.
  const kernel = overlaps.filter((o) => byId.has(o.jobA) && byId.has(o.jobB));
  const overlapsOf = new Map<string, JobOverlap[]>();
  for (const o of kernel) {
    for (const id of [o.jobA, o.jobB]) {
      const at = overlapsOf.get(id);
      if (at) at.push(o);
      else overlapsOf.set(id, [o]);
    }
  }

  const nodes: JobNode[] = [];
  for (const u of kept) {
    const state = jobState(u.job);
    counts[state]++;
    const parent = parentOf.get(u.id) ?? null;
    const named = u.job.parent;
    const node: JobNode = {
      id: u.id,
      job: u.job,
      state,
      rawState: u.job.state,
      parent,
      danglingParent: parent === null && named !== "" && named !== u.id ? named : "",
      children: childrenOf.get(u.id) ?? [],
      depth: depthOf.get(u.id) ?? 0,
      readOnly: u.job.readOnly,
      holder: u.job.holder,
      overlaps: overlapsOf.get(u.id) ?? [],
    };
    nodes.push(node);
    byId.set(u.id, node);
  }

  // Edges in served order, parent before dependencies per job, so the list is deterministic and the
  // layout's parallel link array lines up index for index.
  const edges: JobEdge[] = [];
  for (const n of nodes) {
    if (n.parent) edges.push({ from: n.parent, to: n.id, kind: "parent" });
    for (const dep of n.job.dependsOn) {
      // A depends_on naming something outside this listing has no second endpoint, so there is no
      // edge to draw. It is not hidden: the detail lists the raw depends_on as written.
      if (dep !== n.id && byId.has(dep)) edges.push({ from: dep, to: n.id, kind: "depends_on" });
    }
  }

  return {
    nodes,
    edges,
    roots: nodes.filter((n) => n.parent === null).map((n) => n.id),
    byId,
    counts,
    dangling,
    overlaps: kernel,
  };
}

// jobState is the state a job reads as. A catalog job the daemon is running right now carries
// `running` on its own flag rather than in the lifecycle string, so the flag is honored first: a job
// visibly in flight must never be drawn as declared.
function jobState(job: Job): JobState {
  if (job.running) return "running";
  return normalizeState(job.state);
}

// placeholder is the node buildJobTree seeds byId with while it is still resolving parents (the
// resolution needs to know which ids exist before it can know which parents are real). Every entry
// is overwritten with the finished node in the same call.
function placeholder(id: string, job: Job): JobNode {
  return {
    id,
    job,
    state: jobState(job),
    rawState: job.state,
    parent: null,
    danglingParent: "",
    children: [],
    depth: 0,
    readOnly: job.readOnly,
    holder: job.holder,
    overlaps: [],
  };
}

// treeOrder lists the ids depth-first from the roots, parents before their children, which is the
// order the accessible twin list reads in. Jobs left unreachable by that walk (only possible inside
// a parent cycle) are appended in served order so the list never silently omits one.
export function treeOrder(model: JobTree): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const walk = (id: string): void => {
    if (seen.has(id)) return;
    seen.add(id);
    out.push(id);
    for (const c of model.byId.get(id)?.children ?? []) walk(c);
  };
  for (const r of model.roots) walk(r);
  for (const n of model.nodes) {
    if (seen.has(n.id)) continue;
    seen.add(n.id);
    out.push(n.id);
  }
  return out;
}

// overviewLine is the one sentence the polite live region announces: how many jobs, the breakdown
// over the states that actually occur, and the no-return count ALWAYS, even at zero. The zero is the
// point - a call-out that vanishes when it reads zero is a call-out a reader stops trusting, and
// "0 no-return" is a fact worth stating about work that is still running.
export function overviewLine(model: JobTree): string {
  const n = model.nodes.length;
  const head = n + (n === 1 ? " job" : " jobs");
  const parts: string[] = [];
  for (const s of ["declared", "running", "pass", "fail"] as const) {
    if (model.counts[s] > 0) parts.push(model.counts[s] + " " + STATE_LABEL[s]);
  }
  const tail = model.counts.no_return + " " + STATE_LABEL.no_return;
  return [head, parts.join(", "), tail].filter(Boolean).join(". ") + ".";
}

// ---- the live join ---------------------------------------------------------

// RunJoin is the activity feed indexed by the job each row claims. unmatched is not noise: a row
// naming a job this listing does not carry means what is on screen is older than the work, which is
// exactly when a reader should stop trusting the picture.
export interface RunJoin {
  readonly byJob: ReadonlyMap<string, ActivityRow[]>;
  readonly unmatched: readonly ActivityRow[];
}

// joinRuns indexes activity rows by job id. Input order is preserved within a job, so a caller that
// passes running rows before finished ones gets them back that way. A row claiming no job is not
// unmatched - it is unattributed, which is every row today (nothing stamps the field yet), and
// counting the whole feed as evidence of a stale picture would be a permanent false alarm.
export function joinRuns(model: JobTree, rows: readonly ActivityRow[]): RunJoin {
  const byJob = new Map<string, ActivityRow[]>();
  const unmatched: ActivityRow[] = [];
  for (const row of rows) {
    const id = row.unit ?? "";
    if (!id) continue;
    if (!model.byId.has(id)) {
      unmatched.push(row);
      continue;
    }
    const bucket = byJob.get(id);
    if (bucket) bucket.push(row);
    else byJob.set(id, [row]);
  }
  return { byJob, unmatched };
}

// ---- placement -------------------------------------------------------------

// Node geometry, in the same world units the layered layout spaces columns and rows by
// (LAYERED_COL_W 180 / LAYERED_ROW_H 48), so a node sits inside its cell with a real gap.
export const NODE_W = 152;
export const NODE_H = 30;
const PAD = 24;

export interface NodeLayout {
  readonly at: ReadonlyMap<string, { readonly x: number; readonly y: number }>;
  readonly viewBox: string;
  // Indexes into model.edges the layout had to reverse to break a cycle. The edge still means what
  // it said; the REVERSAL is layout fiction, so the renderer marks those edges instead of hiding
  // the fact - the graph explorer draws the same case dashed for the same reason.
  readonly back: ReadonlySet<number>;
}

// Placeable is the least layoutNodes needs: ids, and the pairs between them. Every model the view
// draws - the jobs, the resolved target plan - places through this one pass, so the tenants cannot
// drift into two different drawings of the same shape.
export interface Placeable {
  readonly nodes: readonly { readonly id: string }[];
  readonly edges: readonly { readonly from: string; readonly to: string }[];
}

// layoutNodes places the nodes with the graph explorer's layered (Sugiyama-style) layout - the same
// pure, deterministic, dependency-free pass the DAG modes there use. Reused rather than rewritten: a
// job tree IS a layered DAG (a job sits to the right of its parent and of everything it depends on),
// and so is a resolved target plan (a target sits to the right of what it needs), and that module
// already solves cycle-breaking, longest-path layering, barycentric crossing reduction, and
// long-edge routing.
//
// Both edge kinds feed the layering, which is the honest reading: a child cannot start before its
// parent handed it out, and a dependent cannot start before its dependency finished. The KINDS stay
// distinct in the drawing, not in the placement.
export function layoutNodes(model: Placeable): NodeLayout {
  const nodes: GNode[] = model.nodes.map((n) => ({
    id: n.id,
    kind: "unit",
    label: n.id,
    degree: 0,
    r: 0,
    x: 0,
    y: 0,
    fx: null,
    fy: null,
  }));
  // Parallel to model.edges, index for index. layoutLayered reads source as the DEPENDENT (placed
  // right) and target as the DEPENDENCY (placed left), which is `to` and `from` here.
  const links: GLink[] = model.edges.map((e) => ({
    source: e.to,
    target: e.from,
    relation: "depends_on",
  }));
  layoutLayered(nodes, links, { colW: LAYERED_COL_W, rowH: LAYERED_ROW_H });

  const at = new Map<string, { x: number; y: number }>();
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of nodes) {
    at.set(n.id, { x: n.x, y: n.y });
    minX = Math.min(minX, n.x - NODE_W / 2);
    minY = Math.min(minY, n.y - NODE_H / 2);
    maxX = Math.max(maxX, n.x + NODE_W / 2);
    maxY = Math.max(maxY, n.y + NODE_H / 2);
  }
  const back = new Set<number>();
  links.forEach((l, i) => {
    if (l.layoutReversed) back.add(i);
  });
  if (!nodes.length) return { at, viewBox: "0 0 1 1", back };
  const w = maxX - minX + PAD * 2;
  const h = maxY - minY + PAD * 2;
  return { at, viewBox: `${minX - PAD} ${minY - PAD} ${w} ${h}`, back };
}

// ---- the service -----------------------------------------------------------

// JobClient is the one connection a mount opens, built once and reused by every read and every
// submit it makes - the same shape the other Connect surfaces use (activity, status, viewer), with
// the bearer token and the daemon origin already wired by lib/daemon.
export type JobClient = Client<typeof JobService>;

export function jobClient(host: string): JobClient {
  return createClient(JobService, createDaemonTransport(host, getLiveToken()));
}

// JobsRead is the three answers the service can give, kept apart because they mean different things
// to a reader staring at an empty screen: there are no jobs, the daemon declines to serve them, or
// it could not be read at all. Collapsing them into one "no data" is how a declined capability comes
// to look like an idle workspace.
export type JobsRead =
  | { readonly kind: "ok"; readonly jobs: Job[]; readonly overlaps: JobOverlap[] }
  | { readonly kind: "denied"; readonly detail: string }
  | { readonly kind: "unreadable"; readonly detail: string };

// listJobs reads every job the daemon holds and every one a session does. A daemon that does not
// mount the service, or refuses this token, answers with a capability denial rather than an outage
// (lib/daemon's isCapabilityDenied), and that is reported as its own kind: a reader told "no jobs"
// when the truth is "this daemon will not say" has been told the wrong thing.
export async function listJobs(client: JobClient, signal?: AbortSignal): Promise<JobsRead> {
  try {
    const resp = await client.listJobs({}, { signal });
    return { kind: "ok", jobs: resp.jobs, overlaps: resp.overlaps };
  } catch (e) {
    return { kind: isCapabilityDenied(e) ? "denied" : "unreadable", detail: errMessage(e) };
  }
}

// SubmitOutcome is what a submit did. ALREADY_RUNNING is a SUCCESS on this contract - the daemon
// coalesced an identical job rather than starting a second - so it is a fact about the job, not a
// failure; only a real refusal (an unknown name, no socket, a rejected token) is one.
export type SubmitOutcome =
  | { readonly kind: "started" }
  | { readonly kind: "already-running" }
  | { readonly kind: "refused"; readonly detail: string };

// submitJob runs one job by its resource name and returns immediately, which is the whole contract:
// the daemon holds it from here, and the activity trail is where its result lands.
export async function submitJob(
  client: JobClient,
  name: string,
  signal?: AbortSignal,
): Promise<SubmitOutcome> {
  try {
    const resp = await client.runJob({ name }, { signal });
    return resp.state === SubmitState.ALREADY_RUNNING
      ? { kind: "already-running" }
      : { kind: "started" };
  } catch (e) {
    return { kind: "refused", detail: errMessage(e) };
  }
}
