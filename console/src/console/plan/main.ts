// main.ts - the console's Plan surface: a plan drawn as the DAG it is, from either of the two
// places a plan comes from.
//
// TWO SOURCES, ONE GRAMMAR. Both tenants answer "what is the shape of this work, and where did it
// get to", and a reader should not have to learn the picture twice, so they share the stage, the
// accessible twin list, the detail sheet, the state colours and the state marks:
//
//  - DECLARED (ledger.ts) - the table an orchestrating agent keeps while it fans work out: one row
//    per unit, with its parent, its owned and forbidden paths, what it depends on, and the state it
//    reached. A GRAPH pretending to be a list, joined here to the live activity feeds.
//  - RUN (run.ts) - the target DAG the engine resolves for plain human work. Nobody declared it, so
//    nothing about it can be stale the way a hand-kept table can, and it FOLLOWS the live run: the
//    daemon picks the anchor and the overview line says which way it picked.
//
// Which one opens is decided by the data, not by a preference: a ledger with rows in it means an
// orchestration is in flight, which is the more specific answer. Anything else - no rows, no route,
// no answer - hands the surface to Run, which is what a person doing plain work came for.
//
// Three decisions worth stating:
//
//  1. no_return IS ITS OWN COLOUR, and it belongs to the ledger ALONE. A unit that failed came back
//     and said so; a unit that never returned said nothing, and is the only state on this surface
//     that no one else will report. It is never drawn, counted, or worded as a failure - and the
//     run plan never invents one, because an engine that resolved a DAG knows what happened to
//     every node in it.
//  2. THE PICTURE IS NOT THE ACCESSIBLE SURFACE. The SVG stage is aria-hidden and the node list
//     beside it is the accessible twin - the same split the graph explorer makes between its canvas
//     and its node cloud, for the same reason: a laid-out drawing has no reading order.
//  3. IT POLLS ONLY WHILE IT IS ON SCREEN, and it refreshes the instant it comes back, so a pane
//     that was hidden never shows a stale plan on its first frame.
//
// Like the activity trail and notes it has no standalone page: activate(host) builds into a console
// host and returns a teardown.

import { createClient } from "@connectrpc/connect";
import { StatusService, type Status } from "../../gen/magus/status/v1/status_pb";
import {
  adoptDaemonOrigin,
  authHeaders,
  createDaemonTransport,
  getLiveToken,
  logsLink,
  resolveDaemonHost,
} from "../../lib/daemon";
import { registerCommand, unregisterCommand } from "../commands";
import { h } from "../view";
// The drawer OWNS the activity row model and the projections onto it (a pool slot, a lock holder, a
// finished run, all as one shape). Imported rather than re-derived so this surface joins against the
// same rows the drawer shows - a second projection would be a second answer to "what is running".
// The protobuf its Status read pulls in is a cost this bundle pays anyway for its own live read.
import { recentRows, runningRows, type ActivityRow, type RunDescriptor } from "../activityDrawer";
import {
  buildPlan,
  joinRuns,
  layoutPlan,
  loadLedger,
  overviewLine,
  treeOrder,
  NODE_H,
  NODE_W,
  STATE_LABEL,
  STATE_MARK,
  type PlanModel,
  type RunJoin,
} from "./ledger";
import {
  emptyRunPlan,
  loadRunPlan,
  runOverviewLine,
  runPlanUrl,
  RUN_STATE_LABEL,
  RUN_STATE_MARK,
  type RunPlanModel,
} from "./run";

// The refresh cadence and the deadline one read gets, both matching the activity drawer's. A plan
// is watched while work moves under it, so the operator's configured dashboard refresh (20s by
// default) is far too slow to answer "did that unit come back".
const POLL_MS = 4000;
const FETCH_TIMEOUT_MS = 4000;

const SVG_NS = "http://www.w3.org/2000/svg";

// A per-document counter so two mounted instances (a split pane, a second window) never share the
// arrow-marker ids their edges point at - a duplicate id would silently repoint one stage's markers
// at the other stage's defs.
let instanceSeq = 0;

// Every mounted instance, so the module-level setVisible the console calls (via moduleSurface) can
// reach all of them. The bundle is imported once and mounted per pane, so this is the only place
// that knows how many copies are live.
const mounted = new Set<{ setVisible(visible: boolean): void }>();

// setVisible is the console's own contract (page.ts): true when this surface's pane is the focused
// one in the active tab, false when it is backgrounded. A backgrounded plan stops polling - a plan
// nobody is looking at is not a reason to talk to the daemon - and refreshes immediately when it
// comes back, so what returns to the screen is never the picture from before it was hidden.
export function setVisible(visible: boolean): void {
  for (const m of mounted) m.setVisible(visible);
}

function svgEl<K extends keyof SVGElementTagNameMap>(
  tag: K,
  className?: string,
): SVGElementTagNameMap[K] {
  const e = document.createElementNS(SVG_NS, tag);
  if (className) e.setAttribute("class", className);
  return e;
}

// trunc keeps a node label inside its box. SVG text neither wraps nor ellipsizes, so an id longer
// than the box would run over its neighbours; the full id is on the node's <title>, in the list,
// and in the detail, so nothing is lost by shortening it here.
function trunc(s: string, max: number): string {
  return s.length <= max ? s : s.slice(0, max - 1) + "...";
}

// ---- the drawn model -------------------------------------------------------

export type PlanSource = "declared" | "run";

const SOURCE_LABEL: Record<PlanSource, string> = { declared: "Declared", run: "Run" };

// DrawnNode is the least a node needs to be placed, marked, and told apart - whatever produced it.
// Both sources project into this so there is ONE stage renderer and ONE list renderer: two would be
// two answers to what running looks like, and the whole point of the second tenant is that a reader
// who has learned one view has learned both.
interface DrawnNode {
  readonly id: string;
  // The data-state value. plan.css maps it to a colour; the two sources' vocabularies do not
  // overlap except where they mean the same thing (running, pass, fail).
  readonly state: string;
  readonly mark: string;
  readonly label: string;
  // What is drawn in the box and read in the list row. Usually the id.
  readonly text: string;
  readonly meta: readonly string[];
  readonly readOnly: boolean;
  readonly depth: number;
}

// Field named `nodes` rather than `rows` so a Drawn IS a ledger.Placeable: layoutPlan takes it
// directly, which is what makes the reuse literal rather than a claim in a comment.
interface Drawn {
  readonly nodes: readonly DrawnNode[];
  readonly edges: readonly { readonly from: string; readonly to: string; readonly kind: string }[];
}

const NOTHING_DRAWN: Drawn = { nodes: [], edges: [] };

// declaredDrawn projects the delegation ledger. Reading order is the tree walk, so the list reads
// parents before children; the stage places by the layout and does not care about the order.
function declaredDrawn(model: PlanModel): Drawn {
  const nodes: DrawnNode[] = [];
  for (const id of treeOrder(model)) {
    const n = model.byId.get(id);
    if (!n) continue;
    const meta = [STATE_LABEL[n.state]];
    if (n.readOnly) meta.push("read only");
    if (n.unit.tier) meta.push(n.unit.tier);
    nodes.push({
      id: n.id,
      state: n.state,
      mark: STATE_MARK[n.state],
      label: STATE_LABEL[n.state],
      text: n.id,
      meta,
      readOnly: n.readOnly,
      // Capped so a deeply nested plan does not indent itself off the panel.
      depth: Math.min(n.depth, 6),
    });
  }
  return { nodes, edges: model.edges.map((e) => ({ from: e.from, to: e.to, kind: e.kind })) };
}

// runDrawn projects the resolved run plan. Served order is reading order - the daemon resolved the
// DAG and the console has no better claim about which target to read first - and there is no depth
// to indent by, because a run plan is a dependency graph rather than a tree of delegations.
function runDrawn(model: RunPlanModel): Drawn {
  return {
    nodes: model.nodes.map((n) => ({
      id: n.id,
      state: n.state,
      mark: RUN_STATE_MARK[n.state],
      label: RUN_STATE_LABEL[n.state],
      text: n.id,
      meta: [RUN_STATE_LABEL[n.state]],
      readOnly: false,
      depth: 0,
    })),
    // Every edge in a run plan is a dependency, so there is nothing to tell it apart FROM. It still
    // carries the depends_on kind - that is what it is - and plan.css drops the dashed accent under
    // data-source="run", where the distinction it exists to draw has nothing to distinguish.
    edges: model.edges.map((e) => ({ from: e.from, to: e.to, kind: "depends_on" })),
  };
}

// fetchStatus reads one Status frame for the running half of the join (pool slots and lock holders).
// Resolves undefined on any failure so a blip leaves the plan standing with no runs on it rather
// than throwing into the poll timer.
async function fetchStatus(host: string): Promise<Status | undefined> {
  try {
    const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
    return (await client.getStatus({})).status;
  } catch {
    return undefined;
  }
}

// fetchRuns reads the daemon's retained run descriptors - the same feed the drawer's RECENT section
// and the log viewer's run browser read. Null (not []) on failure, so "could not read" stays
// distinguishable from "nothing has run".
async function fetchRuns(host: string): Promise<RunDescriptor[] | null> {
  try {
    const res = await fetch("http://" + host + "/api/v1/outputs", {
      headers: authHeaders(),
      cache: "no-store",
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    const body = (await res.json()) as { outputs?: RunDescriptor[] };
    return Array.isArray(body.outputs) ? body.outputs : [];
  } catch {
    return null;
  }
}

// activityRows reads both feeds for one tick and returns them as one list, running first. They are
// fetched together and fail independently: a status read that fails must not blank a run list that
// answered.
async function activityRows(host: string): Promise<ActivityRow[]> {
  const [status, runs] = await Promise.all([fetchStatus(host), fetchRuns(host)]);
  const now = Date.now();
  return [...runningRows(status, now), ...(runs ? recentRows(runs, now) : [])];
}

interface Refs {
  root: HTMLElement;
  summary: HTMLElement;
  note: HTMLElement;
  tree: HTMLElement;
  list: HTMLElement;
  stage: SVGSVGElement;
  detail: HTMLElement;
  emptyTitle: HTMLElement;
  emptyBody: HTMLElement;
  refreshBtn: HTMLButtonElement;
  sourceBtns: [PlanSource, HTMLButtonElement][];
  targetWrap: HTMLElement;
  targetInput: HTMLInputElement;
  targetList: HTMLElement;
}

// buildScaffold assembles the surface on PatternFly plus the console-plan-* classes the formula
// allows for what PF has no component for (the stage, the node list, the detail sheet). Three
// columns: the accessible twin list, the drawn plan, and the selected node's detail.
function buildScaffold(host: HTMLElement, markerBase: string): Refs {
  const root = h("div", "console-plan-layout");
  root.dataset.phase = "loading";

  const toolbar = h("div", "console-plan-toolbar");

  // The source switch, a PF ToggleGroup - the console's segmented-control idiom (the settings
  // surface's Pretty|Raw switch, the log viewer's Log|Timeline). First in the toolbar because it
  // decides what every other control in it is talking about.
  const sourceGroup = h("div", "pf-v6-c-toggle-group console-plan-source");
  sourceGroup.setAttribute("role", "group");
  sourceGroup.setAttribute("aria-label", "Plan source");
  const sourceBtns: [PlanSource, HTMLButtonElement][] = [];
  for (const s of ["declared", "run"] as const) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button") as HTMLButtonElement;
    btn.type = "button";
    btn.dataset.source = s;
    btn.title =
      s === "declared"
        ? "The delegation ledger: the units an orchestrating agent declared"
        : "The run plan: the target DAG magus resolves, following the live run";
    btn.append(h("span", "pf-v6-c-toggle-group__text", SOURCE_LABEL[s]));
    item.append(btn);
    sourceGroup.append(item);
    sourceBtns.push([s, btn]);
  }

  // The polite live region, exactly as the activity drawer does it: the SUMMARY is live, the lists
  // are not. A list rebuilt on a four-second timer inside a live region re-announces every row on
  // every tick, which is how a considerate feature becomes an unusable one.
  const summary = h("p", "console-plan-summary", "Reading the plan.");
  summary.setAttribute("aria-live", "polite");
  const note = h("p", "console-plan-note");
  note.hidden = true;

  // The target override. NOT the entry point and deliberately small: the run view follows the live
  // run, so naming a target is the exception - what to do when you want the plan for something that
  // is not what just ran. Emptying it hands the anchor back to the daemon.
  const targetWrap = h("div", "console-plan-target");
  const targetLabel = h("label", "console-plan-target__label", "Target");
  const targetControl = h("span", "pf-v6-c-form-control");
  const targetInput = h("input") as HTMLInputElement;
  targetInput.id = "console-plan-target-" + markerBase;
  targetInput.type = "text";
  targetInput.placeholder = "follow";
  targetInput.spellcheck = false;
  targetInput.autocomplete = "off";
  targetInput.setAttribute("aria-label", "Anchor the run plan at a named target");
  const targetList = h("datalist");
  targetList.id = "console-plan-targets-" + markerBase;
  targetInput.setAttribute("list", targetList.id);
  targetLabel.setAttribute("for", targetInput.id);
  targetControl.append(targetInput);
  targetWrap.append(targetLabel, targetControl, targetList);

  const refreshBtn = h(
    "button",
    "pf-v6-c-button pf-m-plain console-plan-refresh",
  ) as HTMLButtonElement;
  refreshBtn.type = "button";
  refreshBtn.append(h("span", "pf-v6-c-button__text", "Reload"));
  toolbar.append(sourceGroup, summary, note, targetWrap, refreshBtn);

  const tree = h("nav", "console-plan-tree");
  const list = h("ul", "console-plan-list");
  list.setAttribute("role", "list");
  tree.append(list);

  const stageBox = h("div", "console-plan-stage");
  const stage = svgEl("svg", "console-plan-stage__svg");
  // The drawing is decoration over the list next door, which carries the same units in reading
  // order with the same states in words. Announcing a laid-out graph twice, once with no reading
  // order, is worse than announcing it once.
  stage.setAttribute("aria-hidden", "true");
  stage.setAttribute("preserveAspectRatio", "xMinYMin meet");
  const defs = svgEl("defs");
  for (const kind of ["parent", "depends_on"] as const) {
    const marker = svgEl("marker");
    marker.setAttribute("id", markerBase + "-" + kind);
    marker.setAttribute("viewBox", "0 0 8 8");
    marker.setAttribute("refX", "7");
    marker.setAttribute("refY", "4");
    marker.setAttribute("markerWidth", "6");
    marker.setAttribute("markerHeight", "6");
    marker.setAttribute("orient", "auto-start-reverse");
    const head = svgEl("path", "console-plan-arrow");
    head.setAttribute("d", "M0 1 L7 4 L0 7 z");
    head.dataset.kind = kind;
    marker.append(head);
    defs.append(marker);
  }
  const edgeLayer = svgEl("g", "console-plan-edges");
  const nodeLayer = svgEl("g", "console-plan-nodes");
  stage.append(defs, edgeLayer, nodeLayer);
  stageBox.append(stage);

  const detail = h("aside", "console-plan-detail");
  detail.setAttribute("aria-label", "Node detail");

  const empty = h("div", "pf-v6-c-empty-state console-plan-empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "Reading the plan");
  const emptyBodyWrap = h("div", "pf-v6-c-empty-state__body");
  const emptyBody = h("p", undefined, "");
  emptyBodyWrap.append(emptyBody);
  emptyContent.append(emptyTitle, emptyBodyWrap);
  empty.append(emptyContent);

  root.append(toolbar, tree, stageBox, detail, empty);
  host.append(root);
  return {
    root,
    summary,
    note,
    tree,
    list,
    stage,
    detail,
    emptyTitle,
    emptyBody,
    refreshBtn,
    sourceBtns,
    targetWrap,
    targetInput,
    targetList,
  };
}

// field renders one labelled row of the detail sheet. An absent value renders NOTHING rather than
// an empty row: a plan that declares no checkpoint and a plan whose checkpoint is blank look the
// same on screen otherwise, and only one of them is a gap worth noticing.
function field(dl: HTMLElement, label: string, value: string): void {
  if (!value) return;
  dl.append(h("dt", "console-plan-detail__label", label));
  dl.append(h("dd", "console-plan-detail__value", value));
}

// pathField renders a path list as PF Labels, so a long owned-paths set wraps as chips instead of
// one unreadable line. Each path goes through textContent (h never sets innerHTML) - a ledger is
// written by an agent, so nothing in it is trusted markup.
function pathField(dl: HTMLElement, label: string, paths: readonly string[]): void {
  if (!paths.length) return;
  dl.append(h("dt", "console-plan-detail__label", label));
  const dd = h("dd", "console-plan-detail__value");
  const group = h("div", "pf-v6-c-label-group");
  const main = h("div", "pf-v6-c-label-group__main");
  for (const p of paths) {
    const item = h("div", "pf-v6-c-label-group__list-item");
    const chip = h("span", "pf-v6-c-label");
    chip.append(h("span", "pf-v6-c-label__content", p));
    item.append(chip);
    main.append(item);
  }
  group.append(main);
  dd.append(group);
  dl.append(dd);
}

export function activate(host: HTMLElement): () => void {
  const markerBase = "console-plan-arrow-" + ++instanceSeq;
  const refs = buildScaffold(host, markerBase);
  const controller = new AbortController();
  let disposed = false;
  let visible = true;
  let timer: ReturnType<typeof setInterval> | null = null;

  let source: PlanSource = "declared";
  // The auto rule fires ONCE, on the first ledger read that completes, and an explicit pick retires
  // it for good. Without that latch a reader who chose Declared over an empty ledger would be moved
  // back to Run four seconds later, and again on every tick after that.
  let sourceDecided = false;
  let model: PlanModel = buildPlan([]);
  let join: RunJoin = joinRuns(model, []);
  let runModel: RunPlanModel = emptyRunPlan();
  let drawn: Drawn = NOTHING_DRAWN;
  // "" means FOLLOW: the read names no target and the daemon picks the anchor. A non-empty value is
  // the reader's override, and the only thing that puts ?target= on the wire.
  let targetOverride = "";
  // The host the last read resolved, kept so the detail can build a log-viewer deep link without
  // re-resolving it mid-render.
  let lastHost: string | null = null;
  let selected: string | null = null;
  let painted = ""; // the signature of the plan currently on screen

  // --- painting -------------------------------------------------------------

  // paintSource reflects the active source on the chrome: which toggle button is pressed, what the
  // list and the Reload button are called, and whether the target override is offered at all. It
  // owns every string that differs between the two tenants, so adding a third would not mean
  // hunting for source checks scattered through the render path.
  const paintSource = (): void => {
    refs.root.dataset.source = source;
    for (const [s, btn] of refs.sourceBtns) {
      const on = s === source;
      btn.classList.toggle("pf-m-selected", on);
      btn.setAttribute("aria-pressed", on ? "true" : "false");
    }
    const declared = source === "declared";
    refs.tree.setAttribute("aria-label", declared ? "Delegation units" : "Plan targets");
    const reload = declared ? "Reload the delegation ledger" : "Reload the run plan";
    refs.refreshBtn.setAttribute("aria-label", reload);
    refs.refreshBtn.title = reload;
    refs.targetWrap.hidden = declared;
  };

  // renderTargetOptions offers the targets THIS plan mentions as completions for the override. They
  // are not the workspace's target list - the console holds no such list - which is why the control
  // is a datalist (a hint) rather than a select (a closed set): a target that is not in the plan on
  // screen is exactly the reason someone reaches for the override in the first place.
  const renderTargetOptions = (): void => {
    const names = [...new Set(runModel.nodes.map((n) => n.target).filter(Boolean))].sort();
    refs.targetList.replaceChildren(
      ...names.map((name) => {
        const opt = h("option");
        opt.value = name;
        return opt;
      }),
    );
  };

  const setSummary = (line: string): void => {
    // Written only when it actually changed: assigning the same string still counts as a mutation
    // to some assistive tech, which re-announces an unchanged count on every four-second tick.
    if (refs.summary.textContent !== line) refs.summary.textContent = line;
  };

  const setNote = (line: string): void => {
    refs.note.textContent = line;
    refs.note.hidden = line === "";
  };

  const showEmpty = (title: string, body: string): void => {
    refs.root.dataset.phase = "empty";
    refs.emptyTitle.textContent = title;
    refs.emptyBody.textContent = body;
  };

  // signature is what decides whether the plan on screen still matches the plan in hand. The list
  // is rebuilt only when it changes, so a poll that returns the same plan cannot destroy the button
  // a reader has focused - the surface repaints around them, not under them.
  const signature = (d: Drawn): string =>
    d.nodes.map((n) => [n.id, n.state, n.depth, n.readOnly].join(":")).join("|") +
    "#" +
    d.edges.map((e) => e.kind + ":" + e.from + ">" + e.to).join("|");

  const renderList = (): void => {
    const items = drawn.nodes.map((n) => {
      const li = h("li", "console-plan-list__row");
      const btn = h("button", "console-plan-list__item") as HTMLButtonElement;
      btn.type = "button";
      btn.dataset.id = n.id;
      btn.dataset.state = n.state;
      // Indentation is depth. Handed to CSS as a custom property rather than an inline padding: the
      // presentation stays in the stylesheet, and only the number that CANNOT be known there comes
      // from here.
      btn.style.setProperty("--console-plan-depth", String(n.depth));
      if (n.id === selected) btn.setAttribute("aria-current", "true");
      const mark = h("span", "console-plan-list__mark", n.mark);
      mark.dataset.state = n.state;
      const idEl = h("span", "console-plan-list__id", n.text);
      const metaEl = h("span", "console-plan-list__meta", n.meta.join(" - "));
      btn.append(mark, idEl, metaEl);
      btn.title = n.id + " - " + n.label;
      li.append(btn);
      return li;
    });
    refs.list.replaceChildren(...items);
  };

  const renderStage = (): void => {
    const layout = layoutPlan(drawn);
    refs.stage.setAttribute("viewBox", layout.viewBox);
    const edgeLayer = refs.stage.querySelector(".console-plan-edges");
    const nodeLayer = refs.stage.querySelector(".console-plan-nodes");
    if (!edgeLayer || !nodeLayer) return;

    const paths = drawn.edges.map((e, i) => {
      const a = layout.at.get(e.from);
      const b = layout.at.get(e.to);
      const path = svgEl("path", "console-plan-edge");
      path.dataset.kind = e.kind;
      if (layout.back.has(i)) path.dataset.back = "";
      path.setAttribute("marker-end", "url(#" + markerBase + "-" + e.kind + ")");
      if (!a || !b) return path;
      const sx = a.x + NODE_W / 2;
      const tx = b.x - NODE_W / 2;
      const dx = tx - sx;
      // A gentle horizontal-out bezier, the shape the graph explorer's DAG modes draw, so the plan
      // reads as flow. Too short a span curves into itself, so those stay straight.
      path.setAttribute(
        "d",
        Math.abs(dx) > 24
          ? `M${sx} ${a.y} C${sx + dx * 0.4} ${a.y} ${tx - dx * 0.4} ${b.y} ${tx} ${b.y}`
          : `M${sx} ${a.y} L${tx} ${b.y}`,
      );
      return path;
    });
    edgeLayer.replaceChildren(...paths);

    const nodes = drawn.nodes.map((n) => {
      const at = layout.at.get(n.id) ?? { x: 0, y: 0 };
      const g = svgEl("g", "console-plan-node");
      g.dataset.id = n.id;
      g.dataset.state = n.state;
      if (n.readOnly) g.dataset.readonly = "";
      if (n.id === selected) g.dataset.selected = "";
      g.setAttribute("transform", `translate(${at.x} ${at.y})`);
      const title = svgEl("title");
      title.textContent = n.id + " - " + n.label + (n.readOnly ? " - read only" : "");
      const box = svgEl("rect", "console-plan-node__box");
      box.setAttribute("x", String(-NODE_W / 2));
      box.setAttribute("y", String(-NODE_H / 2));
      box.setAttribute("width", String(NODE_W));
      box.setAttribute("height", String(NODE_H));
      box.setAttribute("rx", "2");
      const mark = svgEl("text", "console-plan-node__mark");
      mark.setAttribute("x", String(-NODE_W / 2 + 8));
      mark.setAttribute("y", "4");
      mark.textContent = n.mark;
      const label = svgEl("text", "console-plan-node__id");
      label.setAttribute("x", String(-NODE_W / 2 + 46));
      label.setAttribute("y", "4");
      label.textContent = trunc(n.text, 17);
      g.append(title, box, mark, label);
      if (n.readOnly) {
        const ro = svgEl("text", "console-plan-node__ro");
        ro.setAttribute("x", String(NODE_W / 2 - 6));
        ro.setAttribute("y", "4");
        ro.setAttribute("text-anchor", "end");
        ro.textContent = "ro";
        g.append(ro);
      }
      return g;
    });
    nodeLayer.replaceChildren(...nodes);
  };

  const renderDeclaredDetail = (): void => {
    const n = selected ? model.byId.get(selected) : undefined;
    if (!n) {
      const hint = h(
        "p",
        "console-plan-detail__hint",
        "Select a unit to read its goal, its checkpoint, and the runs attributed to it.",
      );
      refs.detail.replaceChildren(hint);
      return;
    }
    const head = h("div", "console-plan-detail__head");
    head.append(h("h2", "console-plan-detail__title", n.id));
    const state = h("span", "console-plan-detail__state", STATE_LABEL[n.state]);
    state.dataset.state = n.state;
    head.append(state);
    if (n.readOnly) head.append(h("span", "console-plan-detail__ro", "read only"));

    const dl = h("dl", "console-plan-detail__fields");
    field(dl, "Goal", n.unit.goal ?? "");
    field(dl, "Checkpoint", n.unit.checkpoint ?? "");
    field(dl, "Tier", n.unit.tier ?? "");
    field(dl, "Validation", n.unit.validation ?? "");
    pathField(dl, "Owned paths", n.unit.owned_paths ?? []);
    pathField(dl, "Forbidden paths", n.unit.forbidden_paths ?? []);
    pathField(dl, "Depends on", n.unit.depends_on ?? []);
    field(dl, "Parent", n.parent ?? "");
    if (n.danglingParent) {
      field(dl, "Parent", n.danglingParent + " (not in this ledger)");
    }
    // Only when the ledger said something this console does not know. A state it DOES know is
    // already the word in the header, and repeating it would just be noise.
    if (n.rawState && n.rawState !== n.state) {
      field(dl, "Ledger state", n.rawState + " (unrecognized, shown as declared)");
    }
    field(dl, "Created", n.unit.created ?? "");
    field(dl, "Updated", n.unit.updated ?? "");

    const runs = join.byUnit.get(n.id) ?? [];
    const runsBox = h("div", "console-plan-detail__runs");
    runsBox.append(h("h3", "console-plan-detail__runshead", "Runs"));
    if (!runs.length) {
      runsBox.append(
        h(
          "p",
          "console-plan-detail__hint",
          "No runs are attributed to this unit. Nothing stamps a unit onto the activity feeds yet, so this stays empty until something does.",
        ),
      );
    } else {
      const ul = h("ul", "console-plan-detail__runlist");
      ul.setAttribute("role", "list");
      for (const row of runs) {
        const li = h("li", "console-plan-detail__run");
        if (row.outcome) li.dataset.outcome = row.outcome;
        li.append(h("code", "console-plan-detail__runtitle", row.title));
        li.append(h("span", "console-plan-detail__runmeta", row.detail));
        ul.append(li);
      }
      runsBox.append(ul);
    }
    refs.detail.replaceChildren(head, dl, runsBox);
  };

  // renderRunDetail is the run plan's half: what this node IS (project, target, state) and the one
  // place a reader goes next when it has already run. There is no runs list here - the ref IS the
  // run, and it is the log viewer's job to show it.
  const renderRunDetail = (): void => {
    const n = selected ? runModel.byId.get(selected) : undefined;
    if (!n) {
      const hint = h(
        "p",
        "console-plan-detail__hint",
        "Select a target to read its project, its state, and its captured output.",
      );
      refs.detail.replaceChildren(hint);
      return;
    }
    const head = h("div", "console-plan-detail__head");
    head.append(h("h2", "console-plan-detail__title", n.id));
    const state = h("span", "console-plan-detail__state", RUN_STATE_LABEL[n.state]);
    state.dataset.state = n.state;
    head.append(state);

    const dl = h("dl", "console-plan-detail__fields");
    field(dl, "Project", n.project);
    field(dl, "Target", n.target);
    // Only when the daemon said something this console does not know. A state it DOES know is
    // already the word in the header, and repeating it would just be noise.
    if (n.rawState && n.rawState !== n.state) {
      field(dl, "Served state", n.rawState + " (unrecognized, shown as idle)");
    }
    // The ref is the node's most recent captured output INDEPENDENT of the state beside it: a
    // RUNNING target carries the ref from the run BEFORE this one, because the run in flight has
    // captured nothing yet. The wire does that on purpose - there is always something to open - but
    // it means nothing here may word the link as this run's log. A reader who clicks expecting live
    // output and gets the previous run has been lied to by the label, so the label says "last".
    if (n.ref) {
      field(dl, "Last output ref", n.ref);
      dl.append(h("dt", "console-plan-detail__label", "Last output"));
      const dd = h("dd", "console-plan-detail__value");
      // The same deep link the dashboard hands out for a ref (logsLink): it carries the daemon's
      // port so the viewer re-attaches to THIS daemon rather than whatever the reader last used.
      const a = h("a", "pf-v6-c-button pf-m-link pf-m-inline") as HTMLAnchorElement;
      a.href = logsLink(lastHost, { ref: n.ref });
      a.title = "The most recent captured output for this target";
      a.append(h("span", "pf-v6-c-button__text", "Open the last log"));
      dd.append(a);
      // Said in words rather than left to the reader to infer from "last": running is the one state
      // where the gap between what the label promises and what opens is a whole run wide.
      if (n.state === "running") {
        dd.append(
          h(
            "p",
            "console-plan-detail__hint",
            "This target is running now, so the log above is from its previous run.",
          ),
        );
      }
      dl.append(dd);
    }
    refs.detail.replaceChildren(head, dl);
  };

  const renderDetail = (): void => {
    if (source === "run") renderRunDetail();
    else renderDeclaredDetail();
  };

  // syncSelection repaints only what the selection changed - the aria-current on one list row and
  // the data-selected on one node - so choosing a unit never rebuilds the list under the caret.
  const syncSelection = (): void => {
    for (const b of refs.list.querySelectorAll<HTMLElement>(".console-plan-list__item")) {
      if (b.dataset.id === selected) b.setAttribute("aria-current", "true");
      else b.removeAttribute("aria-current");
    }
    for (const g of refs.stage.querySelectorAll<SVGElement>(".console-plan-node")) {
      if (g.dataset.id === selected) g.dataset.selected = "";
      else delete g.dataset.selected;
    }
    renderDetail();
  };

  // The drawn nodes ARE the selectable set, whichever source produced them: selecting something the
  // reader cannot see would leave the detail sheet describing a node that is not on the screen.
  const drawnOrder = (): string[] => drawn.nodes.map((n) => n.id);

  const select = (id: string | null, focusRow = false): void => {
    selected = id && drawn.nodes.some((n) => n.id === id) ? id : null;
    syncSelection();
    if (!focusRow || !selected) return;
    // Matched by walking the rows rather than by an attribute selector: a unit id is an agent's
    // free text, so building a selector out of it is a quoting bug waiting for the first id with a
    // quote in it.
    const btn = [...refs.list.querySelectorAll<HTMLElement>(".console-plan-list__item")].find(
      (b) => b.dataset.id === selected,
    );
    // Focus moves ONLY because a navigation command asked for it. Nothing on a poll tick, and
    // nothing on mount, ever calls this.
    btn?.focus();
    btn?.scrollIntoView({ block: "nearest" });
  };

  const step = (delta: 1 | -1): void => {
    const order = drawnOrder();
    if (!order.length) return;
    const at = selected ? order.indexOf(selected) : -1;
    const next = at < 0 ? (delta > 0 ? 0 : order.length - 1) : at + delta;
    if (next < 0 || next >= order.length) return;
    select(order[next] ?? null, true);
  };

  // render paints whatever the caller has already put in `drawn`, plus the two lines only the
  // caller knows how to word. Keeping the sentences OUT of here is what lets one render path serve
  // two tenants that have nothing to say to each other.
  const render = (overview: string, noteLine: string): void => {
    refs.root.dataset.phase = "ready";
    const sig = signature(drawn);
    if (sig !== painted) {
      painted = sig;
      renderList();
      renderStage();
    }
    setSummary(overview);
    setNote(noteLine);
    syncSelection();
  };

  // The ledger's stale-plan note. A run naming a unit the ledger does not carry means the plan on
  // screen is older than the work, which is exactly when a reader should stop trusting the picture.
  const staleNote = (): string => {
    const stale = join.unmatched.length;
    return stale
      ? stale +
          (stale === 1 ? " run names a unit" : " runs name units") +
          " this ledger does not carry, so the plan on screen is older than the work."
      : "";
  };

  // --- loading --------------------------------------------------------------

  // blank clears what is drawn before an empty state replaces it, so a plan that WAS on screen does
  // not survive underneath a sentence saying there is none - and so switching source cannot leave
  // the other tenant's nodes selectable behind the empty panel.
  const blank = (): void => {
    drawn = NOTHING_DRAWN;
    painted = "";
    selected = null;
    renderList();
    renderStage();
    setNote("");
  };

  // settleSource is the auto rule, and it runs at most once. A ledger with rows means an
  // orchestration is in flight, which is the more specific answer, so Declared opens. Anything
  // else - no rows, no route, no answer - hands the surface to Run: the human-first half is what a
  // person doing plain work came for, and a secondary endpoint that is missing or broken has no
  // business taking the surface over. Returns true when it moved, so the caller can read the other
  // source instead of painting an empty state nobody is going to look at.
  const settleSource = (ledgerHasRows: boolean): boolean => {
    if (sourceDecided) return false;
    sourceDecided = true;
    if (ledgerHasRows) return false;
    source = "run";
    paintSource();
    blank();
    return true;
  };

  const refreshDeclared = async (daemonHost: string): Promise<void> => {
    const [read, rows] = await Promise.all([
      loadLedger(daemonHost, controller.signal),
      activityRows(daemonHost),
    ]);
    if (disposed) return;
    if (read.kind === "absent") {
      if (settleSource(false)) return refreshRun(daemonHost);
      blank();
      showEmpty(
        "No delegation ledger endpoint",
        "No delegation ledger endpoint; the plan view lights up when the daemon serves /api/v1/ledger.",
      );
      setSummary("No delegation ledger endpoint.");
      return;
    }
    if (read.kind === "unreadable") {
      if (settleSource(false)) return refreshRun(daemonHost);
      blank();
      showEmpty(
        "Could not read the delegation ledger",
        "GET http://" +
          daemonHost +
          "/api/v1/ledger did not answer (" +
          read.detail +
          "). If this daemon predates the delegation ledger the route is not there yet; the plan view lights up when the daemon serves /api/v1/ledger.",
      );
      setSummary("Could not read the delegation ledger.");
      return;
    }
    if (settleSource(read.units.length > 0)) return refreshRun(daemonHost);
    model = buildPlan(read.units);
    join = joinRuns(model, rows);
    if (!model.nodes.length) {
      blank();
      showEmpty(
        "Nothing delegated",
        "The daemon serves the delegation ledger and it is empty: no unit has been declared in this workspace yet.",
      );
      setSummary(overviewLine(model));
      return;
    }
    drawn = declaredDrawn(model);
    if (selected && !model.byId.has(selected)) selected = null;
    render(overviewLine(model), staleNote());
  };

  const refreshRun = async (daemonHost: string): Promise<void> => {
    const read = await loadRunPlan(daemonHost, targetOverride, controller.signal);
    if (disposed) return;
    if (read.kind === "absent") {
      blank();
      showEmpty(
        "No run plan endpoint",
        "No run plan endpoint; this view lights up when the daemon serves /api/v1/plan.",
      );
      setSummary("No run plan endpoint.");
      return;
    }
    // The daemon knows the workspace's targets and this console does not, so its sentence is the
    // one that can be acted on. It is shown verbatim rather than restated.
    if (read.kind === "unknown-target") {
      blank();
      showEmpty(
        "Unknown target",
        read.detail || "The daemon does not know a target named " + targetOverride + ".",
      );
      setSummary("Unknown target: " + targetOverride + ".");
      return;
    }
    if (read.kind === "unreadable") {
      blank();
      showEmpty(
        "Could not read the run plan",
        "GET " +
          runPlanUrl(daemonHost, targetOverride) +
          " did not answer (" +
          read.detail +
          "). If this daemon predates the run plan the route is not there yet; this view lights up when the daemon serves /api/v1/plan.",
      );
      setSummary("Could not read the run plan.");
      return;
    }
    runModel = read.plan;
    renderTargetOptions();
    if (!runModel.nodes.length) {
      blank();
      // Two different facts. With no target named, an empty plan means the daemon had nothing to
      // anchor to - nothing has run. With one named, it means that target resolved to nothing here.
      if (targetOverride) {
        showEmpty("No plan for that target", "No targets answer to " + targetOverride + " here.");
        setSummary("No targets answer to " + targetOverride + ".");
      } else {
        showEmpty(
          "Nothing has run here yet",
          "Nothing has run here yet. The run plan follows the live run, so it fills in the moment a target starts.",
        );
        setSummary("Nothing has run here yet.");
      }
      return;
    }
    drawn = runDrawn(runModel);
    if (selected && !runModel.byId.has(selected)) selected = null;
    render(runOverviewLine(runModel), "");
  };

  const refresh = async (): Promise<void> => {
    // Per-bundle, not per-page: lib/daemon's origin-adoption flag is module state, so the shell
    // having adopted this origin does not make it adopted in here. Without this the surface works
    // only after some other surface has persisted a host, which is the shape of bug that looks fine
    // on the developer's machine.
    adoptDaemonOrigin();
    const daemonHost = resolveDaemonHost();
    lastHost = daemonHost;
    if (!daemonHost) {
      blank();
      showEmpty(
        "No daemon connected",
        source === "declared"
          ? "The plan view reads the delegation ledger from a local daemon. Start one with: magus server start"
          : "The run plan comes from a local daemon. Start one with: magus server start",
      );
      setSummary("Not connected to a daemon.");
      return;
    }
    if (source === "run") return refreshRun(daemonHost);
    return refreshDeclared(daemonHost);
  };

  const startPolling = (): void => {
    if (timer) return;
    timer = setInterval(() => void refresh(), POLL_MS);
  };
  const stopPolling = (): void => {
    if (!timer) return;
    clearInterval(timer);
    timer = null;
  };

  // --- events ---------------------------------------------------------------

  refs.refreshBtn.addEventListener("click", () => void refresh(), { signal: controller.signal });

  refs.list.addEventListener(
    "click",
    (e) => {
      const btn = (e.target as Element | null)?.closest<HTMLElement>(".console-plan-list__item");
      if (btn?.dataset.id) select(btn.dataset.id);
    },
    { signal: controller.signal },
  );

  refs.stage.addEventListener(
    "click",
    (e) => {
      const g = (e.target as Element | null)?.closest<SVGElement>(".console-plan-node");
      // Clicking a node moves focus to its row in the accessible twin: the picture is where the
      // eye is, but the list is where the keyboard lives, and leaving them apart strands a reader
      // who switches between them mid-read.
      if (g?.dataset.id) select(g.dataset.id, true);
    },
    { signal: controller.signal },
  );

  const chooseSource = (next: PlanSource): void => {
    if (next === source) return;
    // An explicit pick retires the auto rule for good - the reader has answered the question it
    // exists to answer, and a poll four seconds later must not overrule them.
    sourceDecided = true;
    source = next;
    paintSource();
    blank();
    refs.root.dataset.phase = "loading";
    setSummary("Reading the plan.");
    void refresh();
  };

  for (const [s, btn] of refs.sourceBtns) {
    btn.addEventListener("click", () => chooseSource(s), { signal: controller.signal });
  }

  // change, not input: a target name is a whole word, and refetching per keystroke would ask the
  // daemon to resolve a DAG for every prefix of it. Emptying the field hands the anchor back.
  refs.targetInput.addEventListener(
    "change",
    () => {
      const next = refs.targetInput.value.trim();
      if (next === targetOverride) return;
      targetOverride = next;
      blank();
      void refresh();
    },
    { signal: controller.signal },
  );

  // Commands, so every action appears in the command bar and the Actions surface and can be
  // rebound - a private keydown table would give none of that. The single-letter keys are bound on
  // THIS surface's root rather than as global chords (the diff surface's refinement of the graph
  // explorer's global GRAPH_KEYMAP): a bare "j" must not step through a plan while someone is
  // typing in another surface.
  const COMMANDS: { id: string; label: string; run: () => void; keys: string[] }[] = [
    {
      id: "plan.unit.next",
      label: "Plan: next unit",
      run: () => step(1),
      keys: ["j", "ArrowDown"],
    },
    {
      id: "plan.unit.prev",
      label: "Plan: previous unit",
      run: () => step(-1),
      keys: ["k", "ArrowUp"],
    },
    {
      id: "plan.refresh",
      label: "Plan: reload",
      run: () => void refresh(),
      keys: ["r"],
    },
    {
      id: "plan.select.clear",
      label: "Plan: clear the selected unit",
      run: () => select(null),
      keys: ["Escape"],
    },
    // No key: the two single letters left are worth more to a reader who is stepping through nodes
    // than to a switch they make once a session, and the toggle itself is a real focusable button.
    {
      id: "plan.source.toggle",
      label: "Plan: switch between the declared and run plans",
      run: () => chooseSource(source === "declared" ? "run" : "declared"),
      keys: [],
    },
  ];
  for (const c of COMMANDS)
    registerCommand({ id: c.id, label: c.label, group: "Plan", run: c.run });
  const byKey = new Map<string, () => void>();
  for (const c of COMMANDS) for (const k of c.keys) byKey.set(k, c.run);

  refs.root.addEventListener(
    "keydown",
    (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target;
      // Never eat a keystroke meant for a field. There is none on this surface today; there is no
      // reason for the guard to arrive with the first one.
      if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement) return;
      const run = byKey.get(e.key);
      if (!run) return;
      e.preventDefault();
      run();
    },
    { signal: controller.signal },
  );

  const instance = {
    setVisible(v: boolean): void {
      if (v === visible) return;
      visible = v;
      if (v) {
        void refresh();
        startPolling();
      } else {
        stopPolling();
      }
    },
  };
  mounted.add(instance);

  paintSource();
  renderTargetOptions();
  renderDetail();
  void refresh();
  startPolling();

  return () => {
    disposed = true;
    stopPolling();
    mounted.delete(instance);
    controller.abort();
    for (const c of COMMANDS) unregisterCommand(c.id);
    host.replaceChildren();
  };
}
