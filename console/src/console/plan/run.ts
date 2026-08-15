// run.ts - the DERIVED run plan: the target DAG magus computes for plain human work. Its wire
// shape, the read, and the model the Plan surface draws. Everything here is pure and DOM-free, so
// what a reader ends up seeing is decided by code a test can run without a browser (run.test.ts).
//
// This is the Plan surface's SECOND tenant, beside the declared delegation ledger next door. Same
// visual grammar, two sources; this is the human-first half. A ledger is a table an orchestrating
// agent keeps while it fans work out. A run plan is what the engine itself resolves when a person
// types `magus run ci` - nobody declared it, so nothing about it can be stale in the way a
// hand-kept table can.
//
// The decision that shapes the rest of this file: the view FOLLOWS THE LIVE RUN. Nobody browses a
// hypothetical target's plan, so the default read names no target at all and lets the daemon pick
// the anchor - the running target, else the most recent run's, else ci. `anchor` reports which of
// those it did, and the overview line LEADS with it, so a reader never has to wonder whether the
// picture in front of them is live. Naming a target explicitly is an override, not the entry point.
//
// The daemon serves GET /api/v1/plan, but an OLDER one does not, and there it 404s. That is a
// FIRST-CLASS outcome here rather than an error: loadRunPlan reports "absent" so the surface can
// name the missing route instead of drawing an empty DAG, which would read as "nothing has run".

import { authHeaders } from "../../lib/daemon";
import { str } from "./ledger";

// ---- states ----------------------------------------------------------------

// The four states a target in a resolved plan can be in. There is no no_return here and the surface
// must not invent one: that state belongs to a delegated worker that never came back, and an engine
// that resolved a DAG always knows what happened to every node in it.
export const RUN_STATES = ["idle", "running", "pass", "fail"] as const;
export type RunState = (typeof RUN_STATES)[number];

export const RUN_STATE_LABEL: Record<RunState, string> = {
  idle: "idle",
  running: "running",
  pass: "pass",
  fail: "fail",
};

// The NON-COLOUR channel on a node, the same call the ledger view makes for the same reason: colour
// alone fails WCAG 1.4.1 on a surface whose entire content is states told apart. The three shared
// states keep the ledger's marks so a reader who has learned one view has learned both.
export const RUN_STATE_MARK: Record<RunState, string> = {
  idle: "IDLE",
  running: "R",
  pass: "OK",
  fail: "FAIL",
};

// normalizeRunState maps whatever the daemon said onto the four known states. An unrecognized value
// (a newer daemon, a state this console predates) becomes "idle" - the least-claiming of the four,
// because it asserts only that the node is in the plan. Nothing unknown may ever read as a pass or
// a fail. The raw string is kept on the node so the detail can show what was actually served.
export function normalizeRunState(v: unknown): RunState {
  return typeof v === "string" && (RUN_STATES as readonly string[]).includes(v)
    ? (v as RunState)
    : "idle";
}

// ---- the anchor ------------------------------------------------------------

// How the daemon chose the target this plan is rooted at. "explicit" is the only one a reader
// caused; the other three are the daemon following the work.
export const PLAN_ANCHORS = ["running", "recent", "default", "explicit"] as const;
export type PlanAnchor = (typeof PLAN_ANCHORS)[number];

// An unrecognized anchor reads as "default", which claims the least: it says the daemon fell back,
// not that anything is live. An unknown value must never read as "following the running ...".
export function normalizeAnchor(v: unknown): PlanAnchor {
  return typeof v === "string" && (PLAN_ANCHORS as readonly string[]).includes(v)
    ? (v as PlanAnchor)
    : "default";
}

// ---- the model -------------------------------------------------------------

// One node of GET /api/v1/plan's JSON, normalized. Hand-written rather than generated because it
// rides the plain /api routes, the same as the ledger and the outputs feed beside it. Every field
// is the type it claims here even where the route declares it required: this is parsed from the
// network, and a missing project must render as a blank line rather than the string "undefined".
export interface RunPlanNode {
  readonly id: string;
  readonly project: string;
  readonly target: string;
  readonly state: RunState;
  // Exactly what the daemon said, "" when it said nothing. Shown in the detail whenever it is not
  // one of the four, so an unrecognized state is visible rather than quietly rendered as idle.
  readonly rawState: string;
  // The most recent captured output for this node, "" when it has never run.
  //
  // INDEPENDENT OF state, which is the trap: a RUNNING node carries the ref from the run BEFORE
  // this one, because the run in flight has captured nothing yet. That is deliberate on the wire -
  // it means there is always something to open - but it means nothing here may word this ref as
  // "this run's log". The surface says "last log", and says so out loud on a running node.
  readonly ref: string;
}

// from is the DEPENDENCY (upstream, drawn left), to is the DEPENDENT - the same orientation the
// ledger's PlanEdge already draws, so one stage renderer serves both sources.
export interface RunPlanEdge {
  readonly from: string;
  readonly to: string;
}

export interface RunPlanModel {
  readonly target: string;
  readonly anchor: PlanAnchor;
  readonly nodes: readonly RunPlanNode[];
  readonly edges: readonly RunPlanEdge[];
  readonly byId: ReadonlyMap<string, RunPlanNode>;
  readonly counts: Readonly<Record<RunState, number>>;
}

// parseRunPlan normalizes the response body into a model every later pass can be TOTAL over. It is
// one function rather than the ledger's parse-then-build pair because a resolved DAG needs none of
// that assembly: the daemon already did the resolving, so there is nothing here to infer.
//
// Anything that is not the documented shape yields an EMPTY plan, not a throw - a surface that
// cannot read the plan says so, it does not break.
export function parseRunPlan(body: unknown): RunPlanModel {
  const b = (body ?? {}) as Record<string, unknown>;
  const nodes: RunPlanNode[] = [];
  const byId = new Map<string, RunPlanNode>();
  const counts: Record<RunState, number> = { idle: 0, running: 0, pass: 0, fail: 0 };

  const rawNodes = Array.isArray(b.nodes) ? b.nodes : [];
  for (const raw of rawNodes) {
    if (typeof raw !== "object" || raw === null) continue;
    const r = raw as Record<string, unknown>;
    const id = str(r.id);
    // A node with no usable id is dropped rather than becoming something no edge can reach. First
    // row wins on a duplicate: something has to, and a later row silently replacing an earlier one
    // would move edges under a reader mid-poll.
    if (!id || byId.has(id)) continue;
    const state = normalizeRunState(r.state);
    counts[state]++;
    const node: RunPlanNode = {
      id,
      project: str(r.project),
      target: str(r.target),
      state,
      rawState: str(r.state),
      ref: str(r.ref),
    };
    nodes.push(node);
    byId.set(id, node);
  }

  const edges: RunPlanEdge[] = [];
  const rawEdges = Array.isArray(b.edges) ? b.edges : [];
  for (const raw of rawEdges) {
    if (typeof raw !== "object" || raw === null) continue;
    const r = raw as Record<string, unknown>;
    const from = str(r.from);
    const to = str(r.to);
    // An edge needs two endpoints that are both here, and a self-edge would be a loop in the
    // layout. Neither is hidden by dropping it: the node it names is either drawn or absent.
    if (!from || !to || from === to || !byId.has(from) || !byId.has(to)) continue;
    edges.push({ from, to });
  }

  return { target: str(b.target), anchor: normalizeAnchor(b.anchor), nodes, edges, byId, counts };
}

// emptyRunPlan is the model the surface holds before its first read answers - a real model rather
// than a null, so every render path is total without a "have we loaded yet" branch.
export function emptyRunPlan(): RunPlanModel {
  return parseRunPlan(null);
}

// ---- the overview line -----------------------------------------------------

// anchorPhrase is how the view says what it is anchored to, and it leads the overview line because
// it is the first thing a reader needs: whether they are watching work happen or reading a record
// of work that finished. A plan whose target the daemon did not name gets no phrase rather than an
// invented one.
export function anchorPhrase(model: RunPlanModel): string {
  if (!model.target) return "";
  switch (model.anchor) {
    case "running":
      return "following the running " + model.target;
    case "recent":
      return "last run: " + model.target;
    default:
      return model.target;
  }
}

// runOverviewLine is the one sentence the polite live region announces: how the view is anchored,
// how many targets, and the breakdown - with the FAIL count always, even at zero, the same
// discipline the ledger's no-return call-out follows. A call-out that vanishes when it reads zero
// is a call-out a reader stops trusting, and "0 fail" is worth stating on a plan still running.
//
// The idle count is deliberately NOT in the line. On a plan nobody has started every node is idle,
// so it is the one count that is almost always just the total again.
export function runOverviewLine(model: RunPlanModel): string {
  const n = model.nodes.length;
  const head = n + (n === 1 ? " target" : " targets");
  const parts: string[] = [];
  for (const s of ["running", "pass"] as const) {
    if (model.counts[s] > 0) parts.push(model.counts[s] + " " + RUN_STATE_LABEL[s]);
  }
  const tail = model.counts.fail + " " + RUN_STATE_LABEL.fail;
  const body = parts.length ? head + " - " + parts.join(", ") + " - " + tail : head + " - " + tail;
  const lead = anchorPhrase(model);
  return lead ? lead + " - " + body : body;
}

// ---- the read --------------------------------------------------------------

// RunPlanRead is the four answers the endpoint can give, kept apart because they mean different
// things to a reader staring at an empty screen: the plan came back, the route is not there, the
// target named does not exist, or the daemon could not be read. Collapsing them into one "no data"
// is how a missing feature comes to look like an idle one.
export type RunPlanRead =
  | { readonly kind: "ok"; readonly plan: RunPlanModel }
  | { readonly kind: "absent" }
  | { readonly kind: "unknown-target"; readonly detail: string }
  | { readonly kind: "unreadable"; readonly detail: string };

// runPlanUrl carries ?target= ONLY for an override. The bare URL is the default read, and it is the
// bareness that tells the daemon to pick the anchor itself rather than being handed one.
export function runPlanUrl(host: string, target: string): string {
  return (
    "http://" + host + "/api/v1/plan" + (target ? "?target=" + encodeURIComponent(target) : "")
  );
}

// unknownTargetDetail carries the daemon's OWN words for a 400 through to the screen. The console
// does not hold the workspace's target list, so any sentence it wrote here itself would be a guess;
// the daemon named what it could not resolve, and that is what a reader can act on. The body IS the
// message: the route writes it with http.Error, so it arrives as plain text and is used verbatim.
async function unknownTargetDetail(res: Response): Promise<string> {
  try {
    return (await res.text()).trim();
  } catch {
    return "";
  }
}

// loadRunPlan reads GET /api/v1/plan under the same bearer + no-store rules as the ledger beside
// it. 404 and 501 are "absent", not failures: on any daemon predating the run plan that is the
// honest answer, and the surface says so by name.
//
// A cross-origin console (a hosted page reaching a loopback daemon over #port=) cannot always TELL
// the two apart - a 404 that carries no CORS headers surfaces to the browser as a network error
// with no status at all - so the unreadable copy names the missing route as a possible cause too
// rather than blaming the connection.
export async function loadRunPlan(
  host: string,
  target: string,
  signal?: AbortSignal,
): Promise<RunPlanRead> {
  let res: Response;
  try {
    res = await fetch(runPlanUrl(host, target), {
      headers: authHeaders(),
      cache: "no-store",
      signal,
    });
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
  if (res.status === 404 || res.status === 501) return { kind: "absent" };
  if (res.status === 400) return { kind: "unknown-target", detail: await unknownTargetDetail(res) };
  if (!res.ok) return { kind: "unreadable", detail: "HTTP " + res.status };
  try {
    return { kind: "ok", plan: parseRunPlan(await res.json()) };
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
}
