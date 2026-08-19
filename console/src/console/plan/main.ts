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
//     that was hidden never shows a stale plan on its first frame. On screen is a PER-PANE fact, so
//     the switch that carries it hangs off the mount rather than off the module.
//
// Like the activity trail and notes it has no standalone page: activate(host) builds into a console
// host and returns the controller for that mount.

import { createClient } from "@connectrpc/connect";
import { StatusService, type Status } from "../../gen/magus/status/v1alpha1/status_pb";
import {
  adoptDaemonOrigin,
  authHeaders,
  createDaemonTransport,
  getLiveToken,
  logsLink,
  parseHash,
  resolveDaemonHost,
  wantsDemo,
} from "../../lib/daemon";
import { demoUnits, demoOverlaps } from "./demo";
import { persisted } from "../../lib/persist";
import { mountZoomControl, type ZoomControl } from "../zoomControl";
import { registerCommand, unregisterCommand } from "../commands";
import { h } from "../view";
// The drawer OWNS the activity row model and the projections onto it (a pool slot, a lock holder, a
// finished run, all as one shape). Imported rather than re-derived so this surface joins against the
// same rows the drawer shows - a second projection would be a second answer to "what is running".
// The protobuf its Status read pulls in is a cost this bundle pays anyway for its own live read.
import {
  parseDescriptors,
  recentRows,
  runningRows,
  type ActivityRow,
  type RunDescriptor,
} from "../activityDrawer";
import {
  ageLabel,
  buildPlan,
  isStale,
  isTerminal,
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
  type UnitRelease,
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

// The unit index collapses to a rail, as the diff's file index and the activity trail's event index
// do. Persisted, because a reader working in a narrow tile should not re-close it every visit.
const treeCell = persisted<boolean>("plan-tree-collapsed", false);
const FETCH_TIMEOUT_MS = 4000;

const SVG_NS = "http://www.w3.org/2000/svg";

// A per-document counter so two mounted instances (a split pane, a second window) never share the
// arrow-marker ids their edges point at - a duplicate id would silently repoint one stage's markers
// at the other stage's defs.
let instanceSeq = 0;

// ---- the shared commands ---------------------------------------------------

// PlanCommands is what a command DOES to one mount. Named rather than closed over so the shared
// registration below has something to dispatch AT.
interface PlanCommands {
  next(): void;
  prev(): void;
  reload(): void;
  clearSelection(): void;
  toggleSource(): void;
  zoomBy(factor: number): void;
  zoomReset(): void;
}

// The Plan commands are ONE set of ids however many panes are open: the console's registry is keyed
// by id, so a second mount's registration REPLACES the first's rather than adding to it. They are
// therefore registered once, for as long as at least one mount is live, and dispatch to whichever
// mount the console last made visible - the focused pane. Registering per instance is the shape that
// looks right and is not: with two Plan panes open, closing EITHER unregistered the ids for both,
// and the command bar then offered no Plan commands at all while a Plan pane was still on screen.
const COMMANDS: readonly {
  readonly id: string;
  readonly label: string;
  readonly keys: readonly string[];
  readonly run: (c: PlanCommands) => void;
}[] = [
  {
    id: "plan.unit.next",
    label: "Plan: next unit",
    keys: ["j", "ArrowDown"],
    run: (c) => c.next(),
  },
  {
    id: "plan.unit.prev",
    label: "Plan: previous unit",
    keys: ["k", "ArrowUp"],
    run: (c) => c.prev(),
  },
  {
    id: "plan.refresh",
    label: "Plan: reload",
    keys: ["r"],
    run: (c) => c.reload(),
  },
  {
    id: "plan.select.clear",
    label: "Plan: clear the selected unit",
    keys: ["Escape"],
    run: (c) => c.clearSelection(),
  },
  // No key: the two single letters left are worth more to a reader who is stepping through nodes
  // than to a switch they make once a session, and the toggle itself is a real focusable button.
  // Zoom is reachable without a pointer, and without a modifier gesture nobody discovers. The
  // console is keyboard-first, so ctrl/cmd + wheel is the convenience, not the interface.
  {
    id: "plan.zoom.in",
    label: "Plan: zoom in",
    keys: ["+", "="],
    run: (c) => c.zoomBy(1.25),
  },
  {
    id: "plan.zoom.out",
    label: "Plan: zoom out",
    keys: ["-"],
    run: (c) => c.zoomBy(1 / 1.25),
  },
  {
    id: "plan.zoom.reset",
    label: "Plan: fit the plan to the pane",
    keys: ["0"],
    run: (c) => c.zoomReset(),
  },
  {
    id: "plan.source.toggle",
    label: "Plan: switch between the declared and run plans",
    keys: [],
    run: (c) => c.toggleSource(),
  },
];

// Every live mount, and the one a shared command is addressed to.
const live = new Set<PlanCommands>();
let focusedMount: PlanCommands | null = null;

// attachCommands registers the shared ids on the FIRST mount and returns the detach that
// unregisters them on the LAST, so one pane closing while another is open leaves the commands where
// they are.
function attachCommands(c: PlanCommands): () => void {
  live.add(c);
  focusedMount ??= c;
  if (live.size === 1) {
    for (const cmd of COMMANDS) {
      registerCommand({
        id: cmd.id,
        label: cmd.label,
        group: "Plan",
        run: () => {
          if (focusedMount) cmd.run(focusedMount);
        },
      });
    }
  }
  return () => {
    live.delete(c);
    // The commands outlive this mount, so they need somewhere to point. Any surviving mount will
    // do: the console corrects it with the next setVisible.
    if (focusedMount === c) focusedMount = live.values().next().value ?? null;
    if (live.size === 0) for (const cmd of COMMANDS) unregisterCommand(cmd.id);
  };
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
  // The warnings drawn beside the state, in words. Both sources project into this even though only
  // the ledger has anything to say today: a run plan the engine resolved cannot have two owners.
  readonly warn: readonly string[];
  readonly readOnly: boolean;
  readonly depth: number;
  // When the row was last touched (unix seconds, 0 when nothing said), and whether it is finished.
  // The pair is what the age pass reads; it is deliberately NOT folded into meta, because an age
  // recomputed every tick would change the signature and rebuild the list under the reader.
  readonly updated: number;
  readonly terminal: boolean;
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
      // Both units of a reported pair carry the warning, because either row is where a reader
      // might be standing when they need to know the other one exists.
      warn: n.overlaps.length ? ["overlap"] : [],
      readOnly: n.readOnly,
      // Capped so a deeply nested plan does not indent itself off the panel.
      depth: Math.min(n.depth, 6),
      updated: n.unit.updated ?? 0,
      terminal: isTerminal(n.state),
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
      warn: [],
      readOnly: false,
      depth: 0,
      // A resolved run plan has no declared row to go stale: the engine knows what happened to
      // every node in it, so there is no timestamp to watch and nothing to call possibly dead.
      updated: 0,
      terminal: false,
    })),
    // Every edge in a run plan is a dependency, so there is nothing to tell it apart FROM. It still
    // carries the depends_on kind - that is what it is - and plan.css drops the dashed accent under
    // data-source="run", where the distinction it exists to draw has nothing to distinguish.
    edges: model.edges.map((e) => ({ from: e.from, to: e.to, kind: "depends_on" })),
  };
}

// deadline is the signal one read runs under: the read's own abort (a teardown, or a newer read
// superseding this one) OR the cap above, whichever fires first. Both halves are needed - a timeout
// alone keeps a torn-down pane talking to the daemon, and an abort alone lets a read that never
// answers hold the next tick's place forever.
function deadline(signal: AbortSignal): AbortSignal {
  return AbortSignal.any([signal, AbortSignal.timeout(FETCH_TIMEOUT_MS)]);
}

// why is what a failed read contributes to the sentence a reader sees. An aborted or timed-out read
// arrives as a DOMException whose message says which of the two it was, and that distinction is the
// whole reason the reason is carried.
function why(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// Feeds is one tick of both activity feeds: the rows, and why they are short when a read failed.
// The reason is carried rather than folded into an empty list because the detail sheet has to tell
// "nothing is attributed to this unit" apart from "the feeds it would be attributed FROM did not
// answer" - the first is a fact about the plan, the second is a fact about the daemon.
interface Feeds {
  readonly rows: ActivityRow[];
  readonly unread: string; // "" when both feeds answered
}

// fetchStatus reads one Status frame for the running half of the join (pool slots and lock holders).
// Never throws, so a blip leaves the plan standing with no runs on it rather than throwing into the
// poll timer - but it says what went wrong, because an unread feed and an empty one look identical
// on screen otherwise.
async function fetchStatus(
  host: string,
  signal: AbortSignal,
): Promise<{ status?: Status; failed: string }> {
  try {
    const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
    const resp = await client.getStatus({}, { signal: deadline(signal) });
    return { status: resp.status, failed: "" };
  } catch (e) {
    return { failed: why(e) };
  }
}

// fetchRuns reads the daemon's retained run descriptors - the same feed the drawer's RECENT section
// and the log viewer's run browser read - through the drawer's own parser, so a malformed row is
// dropped here exactly as it is there. Null (not []) on failure, so "could not read" stays
// distinguishable from "nothing has run".
async function fetchRuns(
  host: string,
  signal: AbortSignal,
): Promise<{ runs: RunDescriptor[] | null; failed: string }> {
  try {
    const res = await fetch("http://" + host + "/api/v1/outputs", {
      headers: authHeaders(),
      cache: "no-store",
      signal: deadline(signal),
    });
    if (!res.ok) return { runs: null, failed: "HTTP " + res.status };
    return { runs: parseDescriptors(await res.json()), failed: "" };
  } catch (e) {
    return { runs: null, failed: why(e) };
  }
}

// activityRows reads both feeds for one tick and returns them as one list, running first. They are
// fetched together and fail independently: a status read that fails must not blank a run list that
// answered, so both reasons are kept rather than the first one winning.
async function activityRows(host: string, signal: AbortSignal): Promise<Feeds> {
  const [status, runs] = await Promise.all([fetchStatus(host, signal), fetchRuns(host, signal)]);
  const now = Date.now();
  return {
    rows: [...runningRows(status.status, now), ...(runs.runs ? recentRows(runs.runs, now) : [])],
    unread: [status.failed, runs.failed].filter(Boolean).join("; "),
  };
}

interface Refs {
  root: HTMLElement;
  summary: HTMLElement;
  note: HTMLElement;
  tree: HTMLElement;
  list: HTMLElement;
  stage: SVGSVGElement;
  stageBox: HTMLElement;
  // The stage's two layers, carried rather than re-queried per paint: buildScaffold appends them, so
  // a render that could not find them would mean the scaffold it was handed is not this one.
  edgeLayer: SVGGElement;
  nodeLayer: SVGGElement;
  detail: HTMLElement;
  emptyTitle: HTMLElement;
  emptyBody: HTMLElement;
  emptyFooter: HTMLElement;
  demoBtn: HTMLButtonElement;
  treeHead: HTMLElement;
  treeHide: HTMLButtonElement;
  treeReopen: HTMLButtonElement;
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

  // No Reload control. The surface polls every POLL_MS and pauses while the tab is hidden, so the
  // plan on screen is already the plan the daemon has; a button that re-asks is chrome implying
  // the view might be stale when it is not. `plan.refresh` stays registered for the command bar
  // and its keybinding, which is where a deliberate re-read belongs.
  toolbar.append(sourceGroup, summary, note, targetWrap);

  const tree = h("nav", "console-plan-tree");
  const treeHead = h("div", "console-plan-tree__head");
  const treeTitle = h("span", "console-plan-tree__heading", "Units");
  const treeHide = h("button", "console-plan-tree__toggle") as HTMLButtonElement;
  treeHide.type = "button";
  treeHide.title = "Hide the unit index";
  treeHide.setAttribute("aria-label", "Hide the unit index");
  treeHide.textContent = "\u2039";
  treeHead.append(treeTitle, treeHide);
  const treeReopen = h("button", "console-plan-reopen") as HTMLButtonElement;
  treeReopen.type = "button";
  treeReopen.title = "Show the unit index";
  treeReopen.setAttribute("aria-label", "Show the unit index");
  treeReopen.textContent = "\u203a";
  const list = h("ul", "console-plan-list");
  list.setAttribute("role", "list");
  tree.append(treeHead, list);

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
  // The showcase offer, as the diff surface makes it: someone with no daemon running meets this
  // first, and a dead end is a worse first impression than a fabricated plan clearly labelled as
  // one. Hidden unless the caller offers it - "nothing delegated" is a real answer about a real
  // workspace, and burying it under a demo button would be answering a different question.
  const emptyFooter = h("div", "pf-v6-c-empty-state__footer");
  const emptyActions = h("div", "pf-v6-c-empty-state__actions");
  const demoBtn = h("button", "pf-v6-c-button pf-m-primary");
  demoBtn.type = "button";
  demoBtn.append(h("span", "pf-v6-c-button__text", "See the demo"));
  emptyActions.append(demoBtn);
  emptyFooter.append(emptyActions);
  emptyFooter.hidden = true;
  emptyContent.append(emptyTitle, emptyBodyWrap, emptyFooter);
  empty.append(emptyContent);

  root.append(toolbar, tree, treeReopen, stageBox, detail, empty);
  host.append(root);
  return {
    root,
    summary,
    note,
    tree,
    list,
    stage,
    stageBox,
    edgeLayer,
    nodeLayer,
    detail,
    emptyTitle,
    emptyBody,
    emptyFooter,
    demoBtn,
    treeHead,
    treeHide,
    treeReopen,
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

// releaseField renders what the unit gave up: the path, and the version of it the next unit
// inherits. Compact by design - a short digest is enough to COMPARE, which is the only thing a
// reader does with it, and the full one would push the path off the sheet.
//
// A digest that is not a hash arrives as a word ("absent" for a path with nothing on disk, "dir"
// for a directory) and is shown as it came: shortening it would produce a hash-shaped lie.
function releaseField(dl: HTMLElement, releases: readonly UnitRelease[]): void {
  if (!releases.length) return;
  dl.append(h("dt", "console-plan-detail__label", "Released"));
  const dd = h("dd", "console-plan-detail__value");
  const ul = h("ul", "console-plan-detail__releases");
  ul.setAttribute("role", "list");
  for (const r of releases) {
    const li = h("li", "console-plan-detail__release");
    li.append(h("code", "console-plan-detail__releasepath", r.path));
    li.append(h("span", "console-plan-detail__releasedigest", shortDigest(r.digest)));
    ul.append(li);
  }
  dd.append(ul);
  dl.append(dd);
}

function shortDigest(digest: string): string {
  const hex = digest.startsWith("sha256:") ? digest.slice("sha256:".length) : "";
  return hex ? "sha256:" + hex.slice(0, 12) : digest;
}

// stamp renders a unix-second timestamp for the detail sheet, "" when the row carries none. The
// reader's own locale: this is a local daemon's clock being read on the same machine.
function stamp(seconds: number): string {
  return seconds > 0 ? new Date(seconds * 1000).toLocaleString() : "";
}

// PlanInstance is what ONE mount hands back: its teardown, and its own visibility switch. Visibility
// belongs to the instance and not to the module because the console drives it per PANE (tileView's
// applyVisibility calls each pane's controller). A module-level export cannot tell two mounts of
// this bundle apart, so backgrounding one pane silenced the poll in a pane that was still on screen,
// and unhiding either restarted both.
export interface PlanInstance {
  deactivate(): void;
  setVisible(visible: boolean): void;
}

export function activate(host: HTMLElement): PlanInstance {
  // Per-bundle, not per-page: lib/daemon's origin-adoption flag is module state, so the shell having
  // adopted this origin does not make it adopted in here. Without it the surface works only after
  // some other surface has persisted a host, which is the shape of bug that looks fine on the
  // developer's machine. Called ONCE per mount, per its own contract - it consumes the token out of
  // the hash, so a call per refresh would be asking a question that has already been answered.
  adoptDaemonOrigin();

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
  // The shared #demo fragment, read once at mount. Polling is never started in the demo: there is
  // no daemon to poll and the fixture does not move.
  let demo = wantsDemo(parseHash());
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
  // Why the last activity read came up short, "" when both feeds answered. The detail sheet reads it
  // so an unread feed cannot masquerade as a unit nothing has been attributed to.
  let feedsUnread = "";
  let selected: string | null = null;
  let painted = ""; // the signature of the plan currently on screen
  let paintedDetail = ""; // and of the detail sheet beside it

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

  const showEmpty = (title: string, body: string, cmd?: string, offerDemo = false): void => {
    refs.root.dataset.phase = "empty";
    refs.emptyTitle.textContent = title;
    refs.emptyBody.textContent = body;
    // A real <code> element, as every other surface writes a command.
    if (cmd) refs.emptyBody.append(" ", h("code", undefined, cmd), ".");
    refs.emptyFooter.hidden = !offerDemo;
  };

  // signature is what decides whether the plan on screen still matches the plan in hand. The list is
  // rebuilt only when it changes, so a poll that returns the same plan cannot destroy the button a
  // reader has focused - the surface repaints around them, not under them.
  //
  // It covers everything the list ROW draws, meta included. Leaving meta out made the signature a
  // near-match rather than a match: a unit whose tier changed under an unchanged state drew the same
  // signature, and the row went on reading the old tier until something else moved.
  const signature = (d: Drawn): string =>
    d.nodes
      .map((n) =>
        [n.id, n.state, n.depth, n.readOnly, n.meta.join("/"), n.warn.join("/")].join(":"),
      )
      .join("|") +
    "#" +
    d.edges.map((e) => e.kind + ":" + e.from + ">" + e.to).join("|");

  // detailSignature is the same guard for the sheet on the right, and it needs its own because the
  // sheet draws from the SELECTED node rather than from the drawn list: everything in it can change
  // while the plan's own signature holds still, and it holds the surface's only link. A repaint that
  // rebuilds an unchanged sheet takes the focus off "Open the last log" with it, so the sheet is
  // rebuilt only when what it says has actually changed.
  const detailSignature = (): string => {
    if (!selected) return source + ":none";
    if (source === "run") {
      const n = runModel.byId.get(selected);
      return n
        ? "run:" + JSON.stringify([n.id, n.state, n.rawState, n.project, n.target, n.ref, lastHost])
        : "run:gone";
    }
    const n = model.byId.get(selected);
    if (!n) return "declared:gone";
    const runs = (join.byUnit.get(n.id) ?? []).map((r) => [r.id, r.title, r.detail, r.outcome]);
    return (
      "declared:" +
      JSON.stringify([
        n.id,
        n.state,
        n.rawState,
        n.parent,
        n.danglingParent,
        n.unit,
        n.overlaps,
        runs,
        feedsUnread,
      ])
    );
  };

  // syncAges writes the heartbeat onto rows that already exist: how long since the unit's row was
  // last touched, and the stale mark once that gap passes the surface's threshold. It runs on every
  // paint and touches only text and one attribute, so a plan that has not changed is never rebuilt
  // just because time passed - which is what keeps a clock from taking the focus off the row a
  // reader is standing on.
  //
  // Terminal rows carry no age. A unit that finished is not going to be touched again, and an
  // ever-growing "2h" beside a pass reads as a problem where there is none.
  const syncAges = (): void => {
    const now = Date.now();
    const byId = new Map(drawn.nodes.map((n) => [n.id, n]));
    for (const btn of refs.list.querySelectorAll<HTMLElement>(".console-plan-list__item")) {
      const n = byId.get(btn.dataset.id ?? "");
      const el = btn.querySelector<HTMLElement>(".console-plan-list__age");
      if (!n || !el) continue;
      const age = n.terminal ? "" : ageLabel(n.updated, now);
      const stale = isStale(n.terminal, n.updated, now);
      // The word rides along with the number: colour alone cannot carry a state on this surface.
      const label = age && stale ? age + " stale" : age;
      if (el.textContent !== label) el.textContent = label;
      el.toggleAttribute("data-stale", stale);
    }
    for (const g of refs.stage.querySelectorAll<SVGElement>(".console-plan-node")) {
      const n = byId.get(g.dataset.id ?? "");
      if (n && isStale(n.terminal, n.updated, now)) g.dataset.stale = "";
      else delete g.dataset.stale;
    }
  };

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
      const metaEl = h("span", "console-plan-list__meta");
      for (const item of n.meta) metaEl.append(h("span", "console-plan-list__meta-item", item));
      btn.append(mark, idEl, metaEl);
      if (n.warn.length) {
        btn.dataset.warn = "";
        const warning = h("span", "console-plan-list__warn");
        for (const item of n.warn) warning.append(h("span", "console-plan-list__warn-item", item));
        btn.append(warning);
      }
      // Filled by syncAges rather than here: age moves on its own, and rebuilding this list to
      // advance a clock would take the focus off whatever row a reader is standing on.
      btn.append(h("span", "console-plan-list__age"));
      btn.title = `${n.id}: ${n.label}`;
      li.append(btn);
      return li;
    });
    refs.list.replaceChildren(...items);
  };

  const renderStage = (): void => {
    const layout = layoutPlan(drawn);
    refs.stage.setAttribute("viewBox", layout.viewBox);

    // Attach points are FANNED along an edge rather than shared. Every connector leaving
    // claims-audience used to start at the same pixel, so five lines left as one stroke and only
    // separated somewhere out in the middle - which is what made the drawing unreadable however
    // the curves were shaped. Counting them first is what lets each one own a point.
    const outOf = new Map<string, number>();
    const intoOf = new Map<string, number>();
    for (const e of drawn.edges) {
      outOf.set(e.from, (outOf.get(e.from) ?? 0) + 1);
      intoOf.set(e.to, (intoOf.get(e.to) ?? 0) + 1);
    }
    const outSeen = new Map<string, number>();
    const inSeen = new Map<string, number>();
    // Spread over the box's height: point k of n sits at h * k/(n+1), so they are evenly placed
    // and never land on a corner. NODE_H is 30, so a heavily-fanned node packs tighter than the
    // 12px an authored diagram would use - still far better than one shared point.
    const fan = (n: number, k: number): number => (NODE_H * (k + 1)) / (n + 1) - NODE_H / 2;

    const paths = drawn.edges.map((e, i) => {
      const a = layout.at.get(e.from);
      const b = layout.at.get(e.to);
      const path = svgEl("path", "console-plan-edge");
      path.dataset.kind = e.kind;
      if (layout.back.has(i)) path.dataset.back = "";
      path.setAttribute("marker-end", "url(#" + markerBase + "-" + e.kind + ")");
      if (!a || !b) return path;

      const ok = outSeen.get(e.from) ?? 0;
      const ik = inSeen.get(e.to) ?? 0;
      outSeen.set(e.from, ok + 1);
      inSeen.set(e.to, ik + 1);

      const sx = a.x + NODE_W / 2;
      const sy = a.y + fan(outOf.get(e.from) ?? 1, ok);
      const tx = b.x - NODE_W / 2;
      const ty = b.y + fan(intoOf.get(e.to) ?? 1, ik);

      // ORTHOGONAL elbows with quarter-arc corners, not a bezier. A curve whose shape is a
      // function of its endpoints gives every connector a different sweep, and a screenful of
      // them reads as tangle rather than as structure - the house diagram rules call a diagonal
      // connector an outright failure for exactly this reason. Right angles share one vocabulary:
      // out, across, in. The eye follows an axis far more easily than it follows an arc, and two
      // lines running the same leg stay legible because they are parallel rather than nested.
      //
      // The run breaks at the midline between the columns, so every connector in a column pair
      // turns on the same x and the verticals line up into a channel instead of scattering.
      if (Math.abs(ty - sy) < 0.5) {
        path.setAttribute("d", `M${sx} ${sy} H${tx}`);
        return path;
      }
      const mx = (sx + tx) / 2;
      const dir = ty > sy ? 1 : -1;
      // Never let a corner eat more than half a leg, or the two arcs would overlap into a lump.
      const r = Math.max(2, Math.min(8, Math.abs(ty - sy) / 2, Math.abs(tx - sx) / 2));
      path.setAttribute(
        "d",
        `M${sx} ${sy} H${mx - r}` +
          ` Q${mx} ${sy} ${mx} ${sy + dir * r}` +
          ` V${ty - dir * r}` +
          ` Q${mx} ${ty} ${mx + r} ${ty}` +
          ` H${tx}`,
      );
      return path;
    });
    refs.edgeLayer.replaceChildren(...paths);

    const nodes = drawn.nodes.map((n) => {
      const at = layout.at.get(n.id) ?? { x: 0, y: 0 };
      const g = svgEl("g", "console-plan-node");
      g.dataset.id = n.id;
      g.dataset.state = n.state;
      if (n.readOnly) g.dataset.readonly = "";
      if (n.warn.length) g.dataset.warn = "";
      if (n.id === selected) g.dataset.selected = "";
      g.setAttribute("transform", `translate(${at.x} ${at.y})`);
      const title = svgEl("title");
      title.textContent = [n.id, n.label, n.readOnly ? "read only" : "", ...n.warn]
        .filter(Boolean)
        .join("\n");
      const box = svgEl("rect", "console-plan-node__box");
      box.setAttribute("x", String(-NODE_W / 2));
      box.setAttribute("y", String(-NODE_H / 2));
      box.setAttribute("width", String(NODE_W));
      box.setAttribute("height", String(NODE_H));
      // 6, on the 4/6/8 radius scale the rest of the console and the house diagram rules use. At 2
      // the node read as a bare rectangle beside PatternFly's rounded cards and chips, which is
      // the deviation - not a softer corner.
      box.setAttribute("rx", "6");
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
    refs.nodeLayer.replaceChildren(...nodes);
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
    // Every pair this unit is in, naming the OTHER unit and the paths THAT unit declared. Its
    // declarations rather than this one's: the reader is already looking at their own owned
    // paths a few rows up, and what they cannot see is what the other agent claimed. A fact,
    // and worded as one - magus derived it from two rows an agent wrote, and it blocks nothing.
    for (const o of n.overlaps) {
      const mine = o.unit_a === n.id;
      const other = mine ? o.unit_b : o.unit_a;
      field(dl, "Overlaps", other + ": " + (mine ? o.paths_b : o.paths_a).join(", "));
    }
    releaseField(dl, n.unit.releases ?? []);
    // An absolute time rather than an age: the row list carries the age, which moves, and a sheet
    // that changed every second would rebuild itself out from under the link it holds.
    field(dl, "Created", stamp(n.unit.created ?? 0));
    field(dl, "Updated", stamp(n.unit.updated ?? 0));

    const runs = join.byUnit.get(n.id) ?? [];
    const runsBox = h("div", "console-plan-detail__runs");
    runsBox.append(h("h3", "console-plan-detail__runshead", "Runs"));
    if (!runs.length) {
      // Two different facts, and only one of them is about the plan. The feeds not answering means
      // nothing can be attributed to ANY unit right now; the feeds answering with nothing means the
      // attribution itself does not exist yet. Reporting the first as the second would blame the
      // ledger for a daemon that is not talking.
      runsBox.append(
        h(
          "p",
          "console-plan-detail__hint",
          feedsUnread
            ? "The activity feeds could not be read (" +
                feedsUnread +
                "), so nothing can be attributed to this unit right now."
            : "No runs are attributed to this unit. Nothing stamps a unit onto the activity feeds yet, so this stays empty until something does.",
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
    // Worded "last", never "this run's": a node's ref is its most recent captured output
    // INDEPENDENT of the state beside it, so a running target links to the run before this one.
    // run.ts's RunPlanNode.ref carries the reason the wire does that.
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
  // the data-selected on one node - so choosing a unit never rebuilds the list under the caret. The
  // detail sheet goes through its signature for the same reason.
  const syncSelection = (): void => {
    for (const b of refs.list.querySelectorAll<HTMLElement>(".console-plan-list__item")) {
      if (b.dataset.id === selected) b.setAttribute("aria-current", "true");
      else b.removeAttribute("aria-current");
    }
    for (const g of refs.stage.querySelectorAll<SVGElement>(".console-plan-node")) {
      if (g.dataset.id === selected) g.dataset.selected = "";
      else delete g.dataset.selected;
    }
    const sig = detailSignature();
    if (sig === paintedDetail) return;
    paintedDetail = sig;
    renderDetail();
  };

  // The drawn nodes ARE the selectable set, whichever source produced them: selecting something the
  // reader cannot see would leave the detail sheet describing a node that is not on the screen.
  const drawnOrder = (): string[] => drawn.nodes.map((n) => n.id);

  const select = (id: string | null): void => {
    selected = id && drawn.nodes.some((n) => n.id === id) ? id : null;
    syncSelection();
  };

  // selectRow is select plus the focus move, and it is its own function rather than a flag on
  // select: focus moves ONLY because a NAVIGATION asked for it - a key, or a click on the drawn
  // node - so the two call sites that move the caret say which they are by name. Nothing on a poll
  // tick, and nothing on mount, calls this one.
  const selectRow = (id: string | null): void => {
    select(id);
    if (!selected) return;
    // Matched by walking the rows rather than by an attribute selector: a unit id is an agent's
    // free text, so building a selector out of it is a quoting bug waiting for the first id with a
    // quote in it.
    const btn = [...refs.list.querySelectorAll<HTMLElement>(".console-plan-list__item")].find(
      (b) => b.dataset.id === selected,
    );
    btn?.focus();
    btn?.scrollIntoView({ block: "nearest" });
  };

  const step = (delta: 1 | -1): void => {
    const order = drawnOrder();
    if (!order.length) return;
    const at = selected ? order.indexOf(selected) : -1;
    const next = at < 0 ? (delta > 0 ? 0 : order.length - 1) : at + delta;
    if (next < 0 || next >= order.length) return;
    selectRow(order[next] ?? null);
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
    // Always, whether or not the plan changed: the rows may be identical and the heartbeat still
    // advances, and it is the one thing a poll returning the same plan has to move.
    syncAges();
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

  // Every read is stamped with the generation it opened in and the source it was opened FOR, and it
  // may paint only while BOTH still hold. The pair is the guard: the generation catches a poll that
  // has been overtaken - a four-second cadence over a slow daemon answers out of order - and the
  // source catches a reader who switched tenants while a read was in flight. Without the second, a
  // ledger that answers after the switch to Run repaints the run view with declared nodes, which is
  // exactly how a no_return, the one state a run plan can never have, would arrive on one.
  interface Read {
    readonly gen: number;
    readonly source: PlanSource;
    readonly signal: AbortSignal;
  }

  let generation = 0;
  let reading: AbortController | null = null;

  // stopReading retires whatever is in flight: the abort ends the request, and the bumped generation
  // means a response already on its way in can no longer paint. The abort is not tidiness - switching
  // source or naming a target starts a read that must win, and leaving the old one running has the
  // daemon answering a question nobody is waiting for while the answer that matters queues behind it.
  const stopReading = (): void => {
    generation++;
    reading?.abort();
    reading = null;
  };

  // beginRead retires whatever is in flight and opens the next one.
  const beginRead = (forSource: PlanSource): Read => {
    stopReading();
    const ac = new AbortController();
    reading = ac;
    return {
      gen: generation,
      source: forSource,
      signal: AbortSignal.any([controller.signal, ac.signal]),
    };
  };

  // fresh reports whether a read's answer may still be painted.
  const fresh = (r: Read): boolean => !disposed && r.gen === generation && r.source === source;

  // blank clears what is drawn before an empty state replaces it, so a plan that WAS on screen does
  // not survive underneath a sentence saying there is none - and so switching source cannot leave
  // the other tenant's nodes selectable behind the empty panel.
  const blank = (): void => {
    drawn = NOTHING_DRAWN;
    painted = "";
    paintedDetail = "";
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
    const token = beginRead("declared");
    const [read, feeds] = await Promise.all([
      loadLedger(daemonHost, deadline(token.signal)),
      activityRows(daemonHost, token.signal),
    ]);
    if (!fresh(token)) return;
    feedsUnread = feeds.unread;
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
    model = buildPlan(read.units, read.overlaps);
    join = joinRuns(model, feeds.rows);
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
    const token = beginRead("run");
    const read = await loadRunPlan(daemonHost, targetOverride, deadline(token.signal));
    if (!fresh(token)) return;
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

  // The showcase joins the pipeline one step in, with the ledger the daemon would have returned.
  // Everything below buildPlan() is the production path, so what it shows off is the surface itself
  // rather than a rendering of it. No fetch is issued at all, which is what makes
  // /console/plan/#demo work with no daemon, no workspace and offline.
  const showDemo = (): void => {
    stopReading();
    source = "declared";
    sourceDecided = true;
    paintSource();
    model = buildPlan(demoUnits(Date.now()), demoOverlaps());
    join = joinRuns(model, []);
    drawn = declaredDrawn(model);
    if (selected && !model.byId.has(selected)) selected = null;
    render(overviewLine(model), "");
  };

  const refresh = async (): Promise<void> => {
    if (demo) {
      showDemo();
      return;
    }
    const daemonHost = resolveDaemonHost();
    lastHost = daemonHost;
    if (!daemonHost) {
      // No read to start, and any read still out belongs to the daemon that just went away: retiring
      // it here is what stops it painting a plan over "not connected".
      stopReading();
      blank();
      showEmpty(
        "No daemon connected",
        source === "declared"
          ? "The plan view reads the delegation ledger from a local daemon. Start one with:"
          : "The run plan comes from a local daemon. Start one with:",
        "magus server start",
        true,
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

  // Zoom. The stage keeps its viewBox and the SVG's RENDERED size is scaled instead, so the
  // container's existing overflow does the panning for free and nothing about the layout, the
  // hit targets or the text rendering has to know a zoom exists.
  let zoom = 1;
  const ZOOM_MIN = 0.5;
  const ZOOM_MAX = 4;
  const applyZoom = (): void => {
    const vb = refs.stage.getAttribute("viewBox");
    if (!vb) return;
    const parts = vb.split(/\s+/).map(Number);
    const w = parts[2];
    const h = parts[3];
    if (!w || !h) return;
    if (zoom === 1) {
      // Back to fitting the pane: drop the inline sizes and let the stylesheet's 100% govern
      // again, rather than freezing the graph at whatever the pane happened to be.
      refs.stage.style.removeProperty("width");
      refs.stage.style.removeProperty("height");
      return;
    }
    const box = refs.stageBox.getBoundingClientRect();
    refs.stage.style.width = `${Math.round(box.width * zoom)}px`;
    refs.stage.style.height = `${Math.round(box.height * zoom)}px`;
  };
  const setZoom = (next: number): void => {
    const clamped = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.round(next * 100) / 100));
    if (clamped === zoom) return;
    zoom = clamped;
    applyZoom();
    // Every route in - the stepper, ctrl+wheel, the commands - lands here, so the readout is
    // repainted once rather than at each call site.
    zoomCtl?.sync();
  };
  // Mounted on VISIBILITY, not at construction: the status bar is per-tab and the mount resolves to
  // whichever bar is on screen, so a pane built while another tab is active would dock its stepper in
  // that tab's bar.
  let zoomCtl: ZoomControl | null = null;
  const mountZoom = (): void => {
    zoomCtl = mountZoomControl({
      get: () => zoom,
      zoomIn: () => setZoom(zoom * 1.25),
      zoomOut: () => setZoom(zoom / 1.25),
      reset: () => setZoom(1),
    });
  };
  const unmountZoom = (): void => {
    zoomCtl?.remove();
    zoomCtl = null;
  };
  // Mounted here too, not only on the visibility transition: `visible` starts true (a pane is
  // constructed focused), so setVisible(true) short-circuits on `v === visible` and would never
  // reach the mount. A pane that turns out to be backgrounded gets setVisible(false) and gives the
  // bar straight back.
  mountZoom();
  // ctrl/cmd + wheel, the gesture every map and canvas already uses - and it leaves a PLAIN wheel
  // scrolling the pane, which is what a reader with a long plan actually wants most of the time.
  refs.stageBox.addEventListener(
    "wheel",
    (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      setZoom(zoom * (e.deltaY < 0 ? 1.1 : 1 / 1.1));
    },
    { passive: false, signal: controller.signal },
  );

  // Drag to pan. The stage is already a scroll container, so panning is just driving its scroll
  // offsets - no transform to keep in step with the layout, and the scrollbars stay honest about
  // where you are.
  //
  // A drag must not eat a click: the nodes are selectable, and a reader who presses on one and
  // releases without moving has clicked it. So panning only ENGAGES past a few pixels of travel,
  // and only a drag that engaged swallows the click that follows it.
  let panning = false;
  let moved = false;
  let startX = 0;
  let startY = 0;
  let startLeft = 0;
  let startTop = 0;
  const PAN_SLOP = 4;
  refs.stageBox.addEventListener(
    "pointerdown",
    (e) => {
      if (e.button !== 0) return;
      panning = true;
      moved = false;
      startX = e.clientX;
      startY = e.clientY;
      startLeft = refs.stageBox.scrollLeft;
      startTop = refs.stageBox.scrollTop;
    },
    { signal: controller.signal },
  );
  refs.stageBox.addEventListener(
    "pointermove",
    (e) => {
      if (!panning) return;
      const dx = e.clientX - startX;
      const dy = e.clientY - startY;
      if (!moved && Math.abs(dx) + Math.abs(dy) < PAN_SLOP) return;
      if (!moved) {
        moved = true;
        refs.stageBox.dataset.panning = "";
        refs.stageBox.setPointerCapture(e.pointerId);
      }
      refs.stageBox.scrollLeft = startLeft - dx;
      refs.stageBox.scrollTop = startTop - dy;
    },
    { signal: controller.signal },
  );
  const endPan = (e: PointerEvent): void => {
    if (!panning) return;
    panning = false;
    delete refs.stageBox.dataset.panning;
    if (refs.stageBox.hasPointerCapture(e.pointerId)) {
      refs.stageBox.releasePointerCapture(e.pointerId);
    }
  };
  refs.stageBox.addEventListener("pointerup", endPan, { signal: controller.signal });
  refs.stageBox.addEventListener("pointercancel", endPan, { signal: controller.signal });
  // Capture phase, so the node's own handler never sees a click that was really the end of a pan.
  refs.stageBox.addEventListener(
    "click",
    (e) => {
      if (!moved) return;
      moved = false;
      e.stopPropagation();
      e.preventDefault();
    },
    { capture: true, signal: controller.signal },
  );

  const applyTree = (collapsed: boolean): void => {
    refs.root.dataset.tree = collapsed ? "collapsed" : "open";
    refs.tree.hidden = collapsed;
    refs.treeReopen.hidden = !collapsed;
    refs.treeHide.setAttribute("aria-expanded", collapsed ? "false" : "true");
  };
  refs.treeHide.addEventListener(
    "click",
    () => {
      treeCell.set(true);
      applyTree(true);
    },
    { signal: controller.signal },
  );
  refs.treeReopen.addEventListener(
    "click",
    () => {
      treeCell.set(false);
      applyTree(false);
    },
    { signal: controller.signal },
  );
  applyTree(treeCell.get() ?? false);

  // Entering the showcase in place, NOT by reloading: inside the console a reload would tear down
  // the whole SPA (every tab) instead of this one surface. replaceState so a standalone refresh
  // stays in the demo and the URL reads as a shareable /console/plan/#demo.
  refs.demoBtn.addEventListener(
    "click",
    () => {
      history.replaceState(null, "", "#demo");
      demo = true;
      void refresh();
    },
    { signal: controller.signal },
  );

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
      if (g?.dataset.id) selectRow(g.dataset.id);
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

  // What a shared command does to THIS mount. Registration is module-wide (see attachCommands) so
  // that two Plan panes share one set of ids, but the work is always addressed to one mount.
  const commands: PlanCommands = {
    next: () => step(1),
    prev: () => step(-1),
    reload: () => void refresh(),
    clearSelection: () => select(null),
    toggleSource: () => chooseSource(source === "declared" ? "run" : "declared"),
    zoomBy: (factor) => setZoom(zoom * factor),
    zoomReset: () => setZoom(1),
  };
  // Commands, so every action appears in the command bar and the Actions surface and can be
  // rebound - a private keydown table would give none of that. The single-letter keys are bound on
  // THIS surface's root rather than as global chords (the diff surface's refinement of the graph
  // explorer's global GRAPH_KEYMAP): a bare "j" must not step through a plan while someone is
  // typing in another surface. The keydown goes to the mount it happened IN, not to the focused
  // one - a keystroke belongs to the pane it was typed into.
  const detachCommands = attachCommands(commands);
  const byKey = new Map<string, () => void>();
  for (const c of COMMANDS) for (const k of c.keys) byKey.set(k, () => c.run(commands));

  refs.root.addEventListener(
    "keydown",
    (e) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target;
      // Never eat a keystroke meant for a field: the target override is one, and "r" typed into it
      // would otherwise reload the plan instead of naming a target.
      if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement) return;
      const run = byKey.get(e.key);
      if (!run) return;
      e.preventDefault();
      run();
    },
    { signal: controller.signal },
  );

  paintSource();
  renderTargetOptions();
  syncSelection();
  void refresh();
  startPolling();

  return {
    // setVisible is the console's own contract (page.ts): true when THIS pane is the focused one in
    // the active tab, false when it is backgrounded. A backgrounded plan stops polling - a plan
    // nobody is looking at is not a reason to talk to the daemon - and refreshes immediately when it
    // comes back, so what returns to the screen is never the picture from before it was hidden.
    setVisible(v: boolean): void {
      // Recorded before the early return: a pane that mounts already focused is told setVisible(true)
      // with nothing to change, and it still has to be the mount the shared commands act on.
      if (v) focusedMount = commands;
      if (v === visible) return;
      visible = v;
      if (v) {
        mountZoom();
        void refresh();
        startPolling();
      } else {
        unmountZoom();
        stopPolling();
      }
    },
    deactivate(): void {
      disposed = true;
      stopPolling();
      detachCommands();
      controller.abort();
      // The status bar outlives this surface, so the stepper has to be taken down by hand -
      // host.replaceChildren() below cannot reach it.
      unmountZoom();
      host.replaceChildren();
    },
  };
}
