// ledger.ts - the delegation ledger: its wire shape, the read, the unit tree the Plan surface
// draws, and the placement it draws that tree at. Everything here is pure and DOM-free, so what a
// reader ends up seeing is decided by code a test can run without a browser (ledger.test.ts). The
// surface file next door owns the SVG, the poll, and the keyboard.
//
// The ledger is the table an orchestrating agent keeps while it fans work out (the
// magus-delegate-multi-agent skill: one row per unit, descendants in the same table). It is the
// PLAN. The activity feeds next to it are what actually ran. Joining the two is the whole point of
// the surface: a plan with no runs under it is a plan nobody started, and a run under no unit is
// work nobody planned.
//
// The endpoint lands with the sibling branch. Until then GET /api/v1/ledger 404s, which is a
// FIRST-CLASS outcome here rather than an error: loadLedger reports "absent" so the surface can say
// what is missing instead of showing an empty plan, which would read as "nothing was delegated".

import { authHeaders } from "../../lib/daemon";
import { layoutLayered, LAYERED_COL_W, LAYERED_ROW_H } from "../graph/layout";
import type { GLink, GNode } from "../graph/types";
// The row shape stays in lockstep with the drawer that defines it without this module pulling the
// drawer's protobuf in behind it. The VALUE side (runningRows/recentRows) is imported by main.ts,
// where that cost is already being paid.
import type { ActivityRow } from "../activityDrawer";

// ---- states ----------------------------------------------------------------

// The five states a unit can be in. no_return is its OWN state and is never folded into fail: a
// worker that failed reported a failure, and one that never returned reported nothing at all. The
// second is the one that needs a human, because nobody is coming to tell you about it.
export const UNIT_STATES = ["declared", "running", "pass", "fail", "no_return"] as const;
export type UnitState = (typeof UNIT_STATES)[number];

// STATE_LABEL is the word a reader sees. "no-return" is hyphenated everywhere - in the node mark's
// tooltip, the list row, the detail, and the overview call-out - so the one state that most needs
// to be recognized always reads the same.
export const STATE_LABEL: Record<UnitState, string> = {
  declared: "declared",
  running: "running",
  pass: "pass",
  fail: "fail",
  no_return: "no-return",
};

// STATE_MARK is the NON-COLOUR channel on a node. Colour alone fails WCAG 1.4.1, and this is a
// surface whose entire content is five states told apart - the diff surface made the same call for
// its add/delete markers. Short enough to sit inside a 152-unit-wide node beside the id.
export const STATE_MARK: Record<UnitState, string> = {
  declared: "D",
  running: "R",
  pass: "OK",
  fail: "FAIL",
  no_return: "NR",
};

// normalizeState maps whatever the ledger said onto the five known states. An unrecognized value
// (a newer daemon, a typo in a hand-kept table) becomes "declared" - the least-claiming of the
// five, because it asserts only that a unit exists. Nothing unknown may ever read as a pass or a
// fail. The raw string is kept on the node so the detail panel can show what was actually written.
export function normalizeState(v: unknown): UnitState {
  return typeof v === "string" && (UNIT_STATES as readonly string[]).includes(v)
    ? (v as UnitState)
    : "declared";
}

// ---- the wire shape --------------------------------------------------------

// DelegationUnit mirrors one row of GET /api/v1/ledger's JSON. Hand-written rather than generated
// because it rides the plain /api routes, the same as the diff session and the outputs feed beside
// it. Every field but the id is optional here even where the route declares it required: this is
// parsed from the network, and a missing goal must render as a blank line rather than the string
// "undefined".
export interface DelegationUnit {
  readonly id: string;
  readonly parent?: string;
  readonly goal?: string;
  readonly checkpoint?: string;
  readonly owned_paths?: readonly string[];
  readonly forbidden_paths?: readonly string[];
  readonly depends_on?: readonly string[];
  readonly tier?: string;
  readonly validation?: string;
  readonly state?: string;
  readonly read_only?: boolean;
  readonly created?: string;
  readonly updated?: string;
}

// str is the one string coercion both of the surface's parsers read the wire through - the ledger's
// units here, the run plan's nodes in run.ts - so a field that is not a string becomes "" in exactly
// one way rather than two that could drift.
export function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function strList(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string" && x !== "") : [];
}

// parseUnits normalizes the response body into units this module's model can be TOTAL over: every
// field is the type it claims, and a row with no usable id is dropped rather than becoming a node
// that no edge can reach and no run can join to. Anything that is not the documented shape yields
// an empty list, not a throw - a surface that cannot read the ledger says so, it does not break.
export function parseUnits(body: unknown): DelegationUnit[] {
  const list = (body as { units?: unknown } | null)?.units;
  if (!Array.isArray(list)) return [];
  const out: DelegationUnit[] = [];
  for (const raw of list) {
    if (typeof raw !== "object" || raw === null) continue;
    const r = raw as Record<string, unknown>;
    const id = str(r.id);
    if (!id) continue;
    out.push({
      id,
      parent: str(r.parent),
      goal: str(r.goal),
      checkpoint: str(r.checkpoint),
      owned_paths: strList(r.owned_paths),
      forbidden_paths: strList(r.forbidden_paths),
      depends_on: strList(r.depends_on),
      tier: str(r.tier),
      validation: str(r.validation),
      state: str(r.state),
      read_only: r.read_only === true,
      created: str(r.created),
      updated: str(r.updated),
    });
  }
  return out;
}

// ---- the tree model --------------------------------------------------------

export interface PlanNode {
  readonly id: string;
  readonly unit: DelegationUnit;
  readonly state: UnitState;
  // Exactly what the ledger said, "" when it said nothing. Shown in the detail whenever it is not
  // one of the five, so an unrecognized state is visible rather than quietly rendered as declared.
  readonly rawState: string;
  // The parent this ledger can actually resolve, or null. A unit naming a parent the ledger does
  // not carry is a root HERE (there is nothing to hang it under) but is not really one, so the
  // name it gave is kept in danglingParent rather than discarded.
  readonly parent: string | null;
  readonly danglingParent: string;
  readonly children: readonly string[];
  readonly depth: number;
  readonly readOnly: boolean;
}

// A PlanEdge always runs left to right in the drawn plan: `from` is the parent or the dependency,
// `to` is the child or the dependent. One list carries both kinds so the renderer draws them in a
// fixed order and the layout sees the identical list - the two must not diverge, or an edge is
// drawn between nodes that were placed as if it did not exist.
export interface PlanEdge {
  readonly from: string;
  readonly to: string;
  readonly kind: "parent" | "depends_on";
}

export interface PlanModel {
  readonly nodes: readonly PlanNode[];
  readonly edges: readonly PlanEdge[];
  readonly roots: readonly string[];
  readonly byId: ReadonlyMap<string, PlanNode>;
  readonly counts: Readonly<Record<UnitState, number>>;
  // The units that named a parent this ledger does not carry. A stale or partial ledger is a fact
  // worth reporting, not a shape to silently flatten.
  readonly dangling: readonly string[];
}

// buildPlan assembles the tree. It is total over any unit list, including the ones a hand-kept
// table produces: a duplicate id (the first wins), a unit that is its own parent, a parent cycle,
// a depends_on naming something that is not here. None of those may hang or throw - the reader
// gets the plan that CAN be drawn plus, where it matters, a note about what could not.
export function buildPlan(units: readonly DelegationUnit[]): PlanModel {
  const byId = new Map<string, PlanNode>();
  const kept: DelegationUnit[] = [];
  for (const u of units) {
    // First row wins on a duplicate id. Something has to, and the alternative - a later row
    // silently replacing an earlier one - would move edges under a reader mid-poll.
    if (byId.has(u.id)) continue;
    byId.set(u.id, placeholder(u));
    kept.push(u);
  }

  const childrenOf = new Map<string, string[]>();
  for (const u of kept) childrenOf.set(u.id, []);

  // Resolve each unit's parent first, so depth and children both read one answer.
  const parentOf = new Map<string, string | null>();
  const dangling: string[] = [];
  for (const u of kept) {
    const named = u.parent ?? "";
    if (!named || named === u.id) {
      // A unit that is its own parent is a typo in a table someone typed. Treating it as a root
      // is the only reading that draws something; a self-edge would be a loop in the layout.
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

  const counts: Record<UnitState, number> = {
    declared: 0,
    running: 0,
    pass: 0,
    fail: 0,
    no_return: 0,
  };
  const nodes: PlanNode[] = [];
  for (const u of kept) {
    const state = normalizeState(u.state);
    counts[state]++;
    const parent = parentOf.get(u.id) ?? null;
    const named = u.parent ?? "";
    const node: PlanNode = {
      id: u.id,
      unit: u,
      state,
      rawState: u.state ?? "",
      parent,
      danglingParent: parent === null && named !== "" && named !== u.id ? named : "",
      children: childrenOf.get(u.id) ?? [],
      depth: depthOf.get(u.id) ?? 0,
      readOnly: u.read_only === true,
    };
    nodes.push(node);
    byId.set(u.id, node);
  }

  // Edges in ledger order, parent before dependencies per unit, so the list is deterministic and
  // the layout's parallel link array lines up index for index.
  const edges: PlanEdge[] = [];
  for (const n of nodes) {
    if (n.parent) edges.push({ from: n.parent, to: n.id, kind: "parent" });
    for (const dep of n.unit.depends_on ?? []) {
      // A depends_on naming something outside this ledger has no second endpoint, so there is no
      // edge to draw. It is not hidden: the detail panel lists the raw depends_on as written.
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
  };
}

// placeholder is the node buildPlan seeds byId with while it is still resolving parents (the
// resolution needs to know which ids exist before it can know which parents are real). Every entry
// is overwritten with the finished node in the same call.
function placeholder(u: DelegationUnit): PlanNode {
  return {
    id: u.id,
    unit: u,
    state: normalizeState(u.state),
    rawState: u.state ?? "",
    parent: null,
    danglingParent: "",
    children: [],
    depth: 0,
    readOnly: u.read_only === true,
  };
}

// treeOrder lists the ids depth-first from the roots, parents before their children, which is the
// order the accessible twin list reads in. Units left unreachable by that walk (only possible
// inside a parent cycle) are appended in ledger order so the list never silently omits a unit.
export function treeOrder(model: PlanModel): string[] {
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

// overviewLine is the one sentence the polite live region announces: how many units, the breakdown
// over the states that actually occur, and the no-return count ALWAYS, even at zero. The zero is
// the point - a call-out that vanishes when it reads zero is a call-out a reader stops trusting,
// and "0 no-return" is a fact worth stating on a plan that is still running.
export function overviewLine(model: PlanModel): string {
  const n = model.nodes.length;
  const head = n + (n === 1 ? " unit" : " units");
  const parts: string[] = [];
  for (const s of ["declared", "running", "pass", "fail"] as const) {
    if (model.counts[s] > 0) parts.push(model.counts[s] + " " + STATE_LABEL[s]);
  }
  const tail = model.counts.no_return + " " + STATE_LABEL.no_return;
  return parts.length ? head + " - " + parts.join(", ") + " - " + tail : head + " - " + tail;
}

// ---- the live join ---------------------------------------------------------

// RunJoin is the activity feed indexed by the unit each row claims. unmatched is not noise: a row
// naming a unit this ledger does not carry means the plan on screen is older than the work, which
// is exactly when a reader should stop trusting the picture.
export interface RunJoin {
  readonly byUnit: ReadonlyMap<string, ActivityRow[]>;
  readonly unmatched: readonly ActivityRow[];
}

// joinRuns indexes activity rows by unit id. Input order is preserved within a unit, so a caller
// that passes running rows before finished ones gets them back that way. A row with no unit is not
// unmatched - it is unattributed, which is every row today (nothing stamps the field yet), and
// counting the whole feed as evidence of a stale ledger would be a permanent false alarm.
export function joinRuns(model: PlanModel, rows: readonly ActivityRow[]): RunJoin {
  const byUnit = new Map<string, ActivityRow[]>();
  const unmatched: ActivityRow[] = [];
  for (const row of rows) {
    const unit = row.unit ?? "";
    if (!unit) continue;
    if (!model.byId.has(unit)) {
      unmatched.push(row);
      continue;
    }
    const bucket = byUnit.get(unit);
    if (bucket) bucket.push(row);
    else byUnit.set(unit, [row]);
  }
  return { byUnit, unmatched };
}

// ---- placement -------------------------------------------------------------

// Node geometry, in the same world units the layered layout spaces columns and rows by
// (LAYERED_COL_W 180 / LAYERED_ROW_H 48), so a node sits inside its cell with a real gap.
export const NODE_W = 152;
export const NODE_H = 30;
const PAD = 24;

export interface PlanLayout {
  readonly at: ReadonlyMap<string, { readonly x: number; readonly y: number }>;
  readonly viewBox: string;
  // Indexes into model.edges the layout had to reverse to break a cycle. The edge still means what
  // it said; the REVERSAL is layout fiction, so the renderer marks those edges instead of hiding
  // the fact - the graph explorer draws the same case dashed for the same reason.
  readonly back: ReadonlySet<number>;
}

// Placeable is the least layoutPlan needs: ids, and the pairs between them. Every model the surface
// draws - the declared ledger, the resolved run plan - places through this one pass, so the tenants
// cannot drift into two different drawings of the same shape.
export interface Placeable {
  readonly nodes: readonly { readonly id: string }[];
  readonly edges: readonly { readonly from: string; readonly to: string }[];
}

// layoutPlan places the nodes with the graph explorer's layered (Sugiyama-style) layout - the same
// pure, deterministic, dependency-free pass the DAG modes there use. Reused rather than rewritten:
// a delegation plan IS a layered DAG (a unit sits to the right of its parent and of everything it
// depends on), and so is a resolved run plan (a target sits to the right of what it needs), and
// that module already solves cycle-breaking, longest-path layering, barycentric crossing reduction,
// and long-edge routing.
//
// Both of the ledger's edge kinds feed the layering, which is the honest reading: a child cannot
// start before its parent delegated it, and a dependent cannot start before its dependency
// finished. The KINDS stay distinct in the drawing, not in the placement.
export function layoutPlan(model: Placeable): PlanLayout {
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

// ---- the read --------------------------------------------------------------

// LedgerRead is the three answers the endpoint can give, kept apart because they mean different
// things to a reader staring at an empty screen: the plan is empty, the route is not there, or the
// daemon could not be read. Collapsing them into one "no data" is how a missing feature comes to
// look like an idle one.
export type LedgerRead =
  | { readonly kind: "ok"; readonly units: DelegationUnit[] }
  | { readonly kind: "absent" }
  | { readonly kind: "unreadable"; readonly detail: string };

// loadLedger reads GET /api/v1/ledger under the same bearer + no-store rules as the outputs feed
// the drawer reads. 404 and 501 are "absent", not failures: on any daemon predating the ledger
// that is the honest answer, and the surface says so by name.
//
// A cross-origin console (a hosted page reaching a loopback daemon over #port=) cannot always TELL
// the two apart - a 404 that carries no CORS headers surfaces to the browser as a network error
// with no status at all - so the unreadable copy names the missing route as a possible cause too
// rather than blaming the connection.
export async function loadLedger(host: string, signal?: AbortSignal): Promise<LedgerRead> {
  let res: Response;
  try {
    res = await fetch("http://" + host + "/api/v1/ledger", {
      headers: authHeaders(),
      cache: "no-store",
      signal,
    });
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
  if (res.status === 404 || res.status === 501) return { kind: "absent" };
  if (!res.ok) return { kind: "unreadable", detail: "HTTP " + res.status };
  try {
    return { kind: "ok", units: parseUnits(await res.json()) };
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
}
