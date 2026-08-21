import { must, errMessage } from "../../lib/guards";
import { openSurface } from "../surface-navigation";
// main.ts - the /graph/ page's interactive knowledge-graph view.
//
// The page is DATA-AGNOSTIC (like /playground): it renders whatever node-link
// graph it is handed, in priority order:
//   1. a `#data=` URL fragment (gzip + base64url of the JSON) - the private path
//      written by `magus graph export --open`. A fragment is never sent in an HTTP request,
//      so the user's graph never leaves their machine.
//   2. a `#src=` address to fetch the JSON from - a `magus graph export --open --serve`
//      loopback (127.0.0.1) or any CORS-enabled URL (e.g. a committed graph.json's
//      raw link). The same `#src=` the playground uses.
//   3. a file the visitor drops or picks (a graph.json they exported themselves).
//   4. the site's own committed knowledge or target demo graph, shown on demand.
//
// Rendering is canvas + d3-force (bundled locally by esbuild into
// gen/graph/explorer.js - no CDN, so it works offline once the PWA has cached
// it). Colors come from the console's PatternFly-native CSS tokens read off the
// live page (readTheme), re-read on a theme toggle. The canvas is progressive
// enhancement over a semantic node list; the explain card is plain HTML.
import {
  forceSimulation,
  forceLink,
  forceManyBody,
  forceCenter,
  forceCollide,
  forceX,
  forceY,
  type Simulation,
} from "d3-force";
import { zoom as d3zoom, zoomIdentity, type ZoomBehavior, type ZoomTransform } from "d3-zoom";
import { drag as d3drag } from "d3-drag";
import { select } from "d3-selection";
// The loopback lock, the shared bearer token, and the fetch-based SSE reader used to
// be copy-pasted into all three tool pages; they now live in one audited module.
// (The ConnectRPC transport this module also exports is tree-shaken out here - the
// graph explorer only uses these four primitives.)
import {
  daemonAttach,
  consumeLiveToken,
  getLiveToken,
  adoptDaemonOrigin,
  fetchSSE,
  authHeaders,
  isRemembered,
  setRemembered,
  wantsDemo,
  createDaemonTransport,
  parseHash,
} from "../../lib/daemon";
import { createClient } from "@connectrpc/connect";
import { StatusService } from "../../gen/magus/status/v1alpha1/status_pb";
import { GraphService } from "../../gen/magus/graph/v1alpha1/graph_pb";
import {
  type GLink,
  type GNode,
  type GraphFlavor,
  type GraphPayload,
  type TargetGraphOutput,
  endpointId,
} from "./types.js";
import { LAYERED_COL_W, LAYERED_MAX, layoutLayered, layoutWaves } from "./layout.js";
import { CARD_COL_W, DOT_R_PX, cardDetail, drawCard, measureCards } from "./cards.js";
import { RADIAL_MAX_RINGS, RADIAL_RING_R, layoutRadial } from "./radial.js";
import { nodeDurationMs, formatDuration } from "./duration.js";
import {
  setFlowEdges,
  setPulses,
  resetMotion,
  tick as particlesTick,
  type FlowEdge,
} from "./particles.js";
import {
  type Insets,
  NO_INSETS,
  type Rect,
  type WorldBox,
  fitTransform,
  overlayInsets,
  recenterOn,
  usableCenter,
} from "./viewport.js";
import {
  type DependencyDegree,
  dependencyDegrees,
  disconnected,
  mostDependedOn,
  projectOwners as computeProjectOwners,
} from "./views.js";
import { flavorOf, isTargetGraph, targetGraphToNodeLink } from "./target-adapter.js";
import { installKeybindings, mergeKeymap, registerCommand, type Keymap } from "../commands";
import { wireToolbarOverflow } from "../toolbar";
import { persisted } from "../../lib/persist";
import { attachHelpPopover } from "../../ui/help-popover";
import { signal } from "../view";
import { publishStatus } from "../status";

// Runtime-only globals the monolith stashes on window: the live-mode "affected" id set that
// the SSE handler writes for the view code to read, and the PWA File Handling API entry point.
// LaunchQueue/LaunchParams are the minimal shape of the (not-yet-standard-typed) File Handling
// API this code touches; a launched file arrives as a FileSystemFileHandle.
interface LaunchParams {
  files?: readonly FileSystemFileHandle[];
}
interface LaunchQueue {
  setConsumer(consumer: (params: LaunchParams) => void): void;
}
declare global {
  interface Window {
    _liveAffectedIds?: Set<string>;
    launchQueue?: LaunchQueue;
  }
}

// The node kinds the graph can emit. Each gets a stable legend color via a CSS
// custom property (--gk-<kind>) aliased in graph.css to the theme-aware
// --console-node-<kind> palette (tokens.css), so the palette re-tints per theme
// and is read at render time. KINDS also fixes legend order
// (roughly: structure -> code -> docs -> diagnostics). `symbol` is the SCIP
// code-symbol kind introduced by `magus refs`; it lives in lazy @symbols shards
// and may appear in graphs exported with those shards loaded.
const KINDS = [
  "project",
  "module",
  "file",
  "dir",
  "target",
  "spell",
  "op",
  "tool",
  "function",
  "method",
  "symbol",
  "import",
  "package",
  "doc",
  "rationale",
  "note",
  "diagnostic",
  "charm",
  "owner",
  "author",
];

// Relations, for grouping edges in the explain card. Order = display order.
const RELATIONS = [
  "depends_on",
  "produces",
  "consumes",
  "contains",
  "imports",
  "calls",
  "uses",
  "references",
  "documents",
  "annotates",
  "rationale_for",
];

// ---- element handles (the DOM contract with graph.html) --------------------
const el = (id: string): HTMLElement | null => document.getElementById(id);

// Default keybindings for the graph explorer; single keys that dodge browser combos and match the
// log viewer's idiom. User overrides ride the shared "keymap" cell (one keymap across the console).
const GRAPH_KEYMAP: Keymap = {
  "graph.search": "/", // focus the node search
  "graph.fit": "f", // zoom to fit
  "graph.layout": "l", // cycle force / layered / waves layout
  "graph.focus.shallower": "[", // one hop less of the local graph
  "graph.focus.deeper": "]", // one hop more
};
const keymapCell = persisted<Keymap>("keymap", {});
// These handles are the DOM contract with graph.html; the page always provides them, so they are
// asserted non-null (the monolith read them unguarded). statusEl stays nullable because setStatus
// explicitly guards on it. They are resolved by resolveDom() at the top of activate(), NOT at import:
// the console imports this bundle BEFORE injecting the scaffold, so import-time getElementById would
// be null and canvas.getContext would throw. The standalone page also boots through activate(), so
// both paths bind here. Every consumer runs inside a function called after activate(), so the
// definite-assignment (!) handles are safe.
let canvas!: HTMLCanvasElement;
let legendEl!: HTMLElement;
let searchEl!: HTMLInputElement;
let listEl!: HTMLElement;
let cardEl!: HTMLElement;
let statusEl: HTMLElement | null = null;
let countEl!: HTMLElement;
let fileInput!: HTMLInputElement;

const root = document.documentElement;
let ctx!: CanvasRenderingContext2D;

function resolveDom(): void {
  canvas = el("graph-canvas") as HTMLCanvasElement;
  legendEl = el("graph-legend") as HTMLElement;
  searchEl = el("node-search") as HTMLInputElement;
  listEl = el("node-list") as HTMLElement;
  cardEl = el("explain-card") as HTMLElement;
  statusEl = el("graph-status");
  countEl = el("graph-count") as HTMLElement;
  fileInput = el("graph-file") as HTMLInputElement;
  ctx = canvas.getContext("2d") as CanvasRenderingContext2D;
}

// Graph is the loaded node-link graph plus the byId lookup, blob-URL base, and the
// lazily-built relation/adjacency indexes the query grammar and draw code read.
interface Graph {
  nodes: GNode[];
  links: GLink[];
  byId: Map<string, GNode>;
  sourceBase: string;
  relIndex?: Map<string, Set<string>>;
  adj?: Map<string, Set<string>>;
  projectOf?: Map<string, string>;
  depDegrees?: Map<string, DependencyDegree>;
}
// graph is null until the first load, but every reader runs post-load (boot gates on it),
// so it is typed non-null - `null as any` keeps the runtime null + the `if (!graph)` guards
// that remain, without forcing a null-narrow at the ~100 unguarded property accesses.
// biome-ignore lint/suspicious/noExplicitAny: deliberate escape hatch - graph is runtime-null until the first load, but is typed non-null so the ~100 post-load property accesses need no null-narrowing; the surviving if(!graph) guards keep the pre-load callers safe.
let graph: Graph = null as any; // { nodes, links }
let sim: Simulation<GNode, GLink> | null = null;
let zoomBehavior: ZoomBehavior<HTMLCanvasElement, unknown> | null = null; // the ONE d3-zoom instance (shared so centerOn stays in sync)
let transform = zoomIdentity;
let selected: string | null = null; // selected node id
// docTitle is the node this explorer currently has selected, for the console to title its tab after
// (page.ts's TitleSource). A Signal satisfies that shape structurally; the console only ever reads
// and subscribes. Written in renderCard, the one place a selection is rendered.
export const docTitle = signal<string | null>(null);
let query = ""; // current search string (lowercased)
let matchSet: Set<string> | null = null; // Set of node ids matching `query`/focus/lens, or null for "all"
let hoverId: string | null = null;
let focusId: string | null = null; // node the local/focus graph is centered on, or null
let focusDepth = 2; // hops included in the focus graph
// Layout mode: "force" (d3 simulation), "layered" (deterministic Sugiyama DAG
// layout), "waves" (deterministic topological build-order columns), or "radial"
// (BFS ego rings around a center node). Defaults are set per flavor after a
// graph loads; manual switching is allowed and survives the URL fragment
// (#layout=force|layered|waves|radial). The scale guard refuses layered/waves
// for more than 500 visible nodes; radial requires a selected/focused center.
type LayoutMode = "force" | "layered" | "waves" | "radial";
function isLayoutMode(s: string): s is LayoutMode {
  return s === "force" || s === "layered" || s === "waves" || s === "radial";
}
let layoutMode: LayoutMode = "force";
// Set once the operator picks a display mode from the [data-layout] toggle (or the layout
// keybinding). askQuestion consults it: a question may auto-switch the arrangement while the
// mode is still the flavor default, but once the operator has chosen one by hand it is a manual
// override and a question emphasizes within it instead of moving it underneath them. Reset when
// a new graph loads, since the mode resets to that flavor's default with it.
let layoutPickedByHand = false;
let graphFlavor: GraphFlavor = "knowledge"; // "knowledge" | "targets"; set in boot/replaceGraph
// Per-wave membership from the last layoutWaves() call (wavesMeta[i] = the ids in
// wave i - the array index IS the wave number), for draw()'s column bands/headers
// and switchLayout's parallelism status. null outside waves mode.
let wavesMeta: string[][] | null = null;
// Center id + BFS rings from the last layoutRadial() call (see applyRadialMode),
// for draw()'s ring guides, the re-center-on-click behavior in selectNode, and
// the live-refresh fallback in liveApplyGraphUpdate. null outside radial mode.
let radialCenter: string | null = null;
let radialRings: string[][] | null = null;
// One-shot pick mode for the "What's around this?" Ask chip: set when the chip
// is clicked with nothing selected, cleared (and radial entered) on the next
// selectNode with an id, or cleared on Esc/query/view activation.
let pendingRadialPick = false;

// Below this zoom NOTHING is labelled. At the scale that frames a whole workspace the overview's
// job is shape, and no amount of legibility work makes a name useful there: it cannot be tied to
// the dot it belongs to, so it is just text laid over the thing the reader is trying to see.
// Framing magus's own 2373 nodes lands near k=0.45. The floor is well above that rather than
// just above it, because "the whole cloud still fits on screen" is still the overview: at k=1
// every one of this workspace's 74 hubs clears the size rule at once and the core goes back to
// being a wall of text. Labels wait until the view is inspecting a REGION.
const LABEL_MIN_ZOOM = 1.6;

// Once labelling is on at all, this is the on-screen radius in CSS pixels a node must reach to
// earn one - so names arrive biggest-hub-first as the view goes deeper, rather than all at once
// the moment the floor is crossed.
const LABEL_MIN_NODE_PX = 8;

// Cards render in the DAG-shaped modes, either flavor: a named box beats a dot with the label
// floating beside it, and both modes refuse above LAYERED_MAX so a card never has to survive the
// full constellation. Radial stays circles - rings are round, wide cards do not fit.
//
// This is flavor-blind only because measureCards now covers EVERY node (see applyLayeredMode).
// While it measured the laid-out subset, turning cards on here drew the match set as rectangles
// and everything else as circles, which made the shape mean "is in the match set" and nothing
// about the node.
const cardsActive = () => layoutMode === "layered" || layoutMode === "waves";

// isDagMode is true for the deterministic Sugiyama-family layouts (layered and
// waves): the force sim stays stopped, edges route through dummy bend points,
// drag sets fx/fy manually, and arrowheads draw. Radial is NOT a dag mode - it
// draws its own ring guides and skips arrowheads (see applyRadialMode/draw()).
const isDagMode = () => layoutMode === "layered" || layoutMode === "waves";

// True when the current graph carries DurationMs timing on at least one node
// (nodeDurationMs covers all three spellings). Recomputed by
// syncConditionalViews after every graph load; read by draw()'s card branch
// and the "Color by duration" preset's conditional visibility.
let graphHasDurations = false;

// ---- question-first views -----------------------------------------------------
// Views answer developer questions with graph interactions. The active view is
// one of: null (default projection), "blast", "trace", "critical", "hubs",
// "orphans", "cycles". "affected" needs live mode and stays disabled without it.
let activeView: string | null = null; // null | "blast" | "trace" | "hubs" | "orphans" | "critical" | "cycles" | "affected"
let viewNode: string | null = null; // primary node id for blast/trace
let viewNodeTo: string | null = null; // secondary node id for trace
// The default projection shows project-level nodes only on first load.
// "unfolded" = true after user expands (or activates a view/query).
let projectionUnfolded = false;
// Set of node ids visible in the current projection (null = all).
let projectionSet: Set<string> | null = null;

// ---- live mode state -------------------------------------------------------
let liveHost: string | null = null; // host:port string when in live mode, else null
let liveToken: string | null = null; // bearer token for live mode
let liveETag: string | null = null; // last ETag from the currently loaded graph variant, for If-None-Match
// liveGraphQuery is the exact /api/v1/graph query string ("", "?level=projects",
// or "?flavor=targets") of whichever variant is currently loaded. liveRefetchGraph
// MUST reuse this (with liveETag) rather than hardcoding a variant: sending one
// variant's ETag while requesting a different one makes the server 200 with the
// other variant's body, silently downgrading (or upgrading) what is on screen.
let liveGraphQuery = "";
let liveSseAbort: AbortController | null = null; // AbortController for the SSE fetch
let liveReconnectTimer: ReturnType<typeof setTimeout> | null = null;
let liveReconnectDelay = 1000; // ms; doubles on each failure up to 30000
let liveWorkspaceName: string | null = null; // workspace name from StatusService GetStatus, for badge
let liveConnected = false; // true while the SSE stream is open; drives the badge style
let liveFlavor: string | null = null; // null (knowledge) or "targets"
// Defer hidden live updates until the pane returns.
let liveRefreshPending = false;
let surfaceVisible = true;

// Teardown handles for deactivate() (the console unmounting a graph tab/pane): the stage ResizeObserver
// and one AbortController whose signal wires every window/document lifecycle listener, so a single
// abort() removes them all. Without teardown the force simulation, its rAF, the observer, and these
// listeners would keep running in the background after the graph closes.
let stageResizeObserver: ResizeObserver | null = null;
let themeObserver: MutationObserver | null = null;
let lifecycleAbort: AbortController | null = null;
// installKeybindings' teardown. It adds its own document keydown listener rather than taking
// the lifecycle signal, so aborting the controller does not reach it: dropping this handle
// leaves one live matcher per activation, and the console caches surface modules, so every
// close/reopen adds a generation and each chord fires its command one more time.
let uninstallKeys: (() => void) | null = null;

// The graph stays gently "alive": the simulation never fully cools, so nodes
// keep drifting (the Obsidian-like wobble). Disabled under prefers-reduced-motion,
// and paused when the tab is hidden (see boot) so it isn't a background CPU drain.
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
const idleAlpha = () => (reducedMotion.matches ? 0 : 0.006);

// ---- motion layer (flow particles + live recency pulses) ------------------
// Motion is OFF outside two states: an active path/subset view (blast, trace,
// critical) with flow particles running along its edges, or a live refresh
// that just pulsed some nodes. flowOn mirrors whether particles.ts currently
// holds a non-null flow-edge list (set by buildFlowEdges, cleared at every
// view-exit site). pulsesPending is set true whenever liveApplyGraphUpdate
// calls setPulses, and cleared by draw() once tick() reports nothing left to
// paint (see the draw() motion block) - that self-clearing, plus
// motionEligible's reducedMotion/document.hidden checks, is what makes
// motionLoop below stop itself within a frame of there being nothing to show.
let flowOn = false;
let pulsesPending = false;
let motionRaf = 0;

const flowActive = () =>
  (activeView === "trace" || activeView === "critical" || activeView === "blast") && !!matchSet;

const motionEligible = () =>
  !reducedMotion.matches && !document.hidden && (flowOn || pulsesPending);

// motionLoop keeps draw() repainting once per frame while motionEligible();
// the moment it isn't (view cleared, tab hidden, reduced-motion turned on, or
// pulses expired), it zeroes motionRaf and stops re-arming itself - CPU drops
// back to the sim's own idle wobble, not zero (that baseline is unaffected).
function motionLoop() {
  if (!motionEligible()) {
    motionRaf = 0;
    return;
  }
  draw();
  motionRaf = requestAnimationFrame(motionLoop);
}

// startMotion arms the loop if it isn't already running and there is
// something eligible to animate; call it from every site that turns flow or
// pulses on. Guarded by motionRaf so a second call while the loop is already
// running is a no-op.
function startMotion() {
  if (!motionRaf && motionEligible()) motionRaf = requestAnimationFrame(motionLoop);
}

// ---- theme / palette -------------------------------------------------------
// One computed-style read per repaint; v() pulls a custom property with a
// fallback. Colors come from the console's PatternFly-native tokens (--console-*
// / --pf-t--* in tokens.css + patternfly.css), theme-aware, so a theme toggle
// re-tints the canvas with no per-theme code here; getComputedStyle resolves the
// var() chains to concrete colors (the same read the uPlot charts use). The
// per-kind fills read through --gk-<kind>, aliased in graph.css to the
// --console-node-<kind> palette.
interface Theme {
  bg: string;
  text: string;
  muted: string;
  border: string;
  accent: string;
  font: string;
  kindColor: Record<string, string>;
}
let theme: Theme | null = null;
function readTheme(): Theme {
  const cs = getComputedStyle(root);
  const v = (name: string, fallback: string): string =>
    cs.getPropertyValue(name).trim() || fallback;
  const kindColor: Record<string, string> = {};
  for (const k of KINDS) kindColor[k] = v("--gk-" + k, "#888");
  theme = {
    bg: v("--pf-t--global--background--color--primary--default", "#fff"),
    text: v("--pf-t--global--text--color--regular", "#151515"),
    muted: v("--pf-t--global--text--color--subtle", "#646b79"),
    border: v("--pf-t--global--border--color--default", "#dce3eb"),
    accent: v("--console-accent", "#0066cc"),
    font: v("--pf-t--global--font--family--body", "system-ui, sans-serif"),
    kindColor,
  };
  return theme;
}

// ---- data loading ----------------------------------------------------------
// Decode a `#data=` fragment: base64url -> bytes -> gunzip -> JSON. Uses the
// browser's DecompressionStream (widely supported); the whole path is local, so
// nothing is fetched and nothing is sent.
async function decodeFragment(b64url: string): Promise<GraphPayload> {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  const body = new Response(bytes).body;
  if (!body) throw new Error("no response body to decode");
  const stream = body.pipeThrough(new DecompressionStream("gzip"));
  const text = await new Response(stream).text();
  return JSON.parse(text);
}

// hashParams is lib/daemon's parseHash. It used to be a second implementation here, and the
// two had drifted on the case that matters: parseHash guards decodeURIComponent, this copy
// called it bare, so a truncated shared link (a malformed percent-escape) threw a URIError out
// of the graph explorer's boot path instead of degrading to the raw text. Aliased rather than
// renamed at ~5 call sites, which keeps this change to the behaviour.
const hashParams = parseHash;

async function loadGraph(): Promise<{ data: GraphPayload; source: string }> {
  const params = hashParams();
  if (params.data) {
    try {
      setStatus("Decoding local graph...");
      return { data: await decodeFragment(params.data), source: "local" };
    } catch (e) {
      setStatus("Could not decode the graph in the link (" + errMessage(e) + ").", true);
    }
  }
  // #src= fetches the JSON from an address: a loopback server (`magus graph export --open
  // --serve`, 127.0.0.1 - private) or any CORS-enabled URL (e.g. a committed
  // graph.json's raw link). The same #src= the playground uses.
  if (params.src) {
    // Only 127.0.0.1/[::1] are "loopback" - that is what connect-src actually
    // allows (see computeCSP in scribe.buzz). The `localhost` hostname is NOT
    // in connect-src, so a `#src=http://localhost:...` fetch is refused by the
    // browser's CSP before it ever reaches the network; flag it separately so
    // the status message points at the fix (127.0.0.1) instead of implying a
    // `--serve` problem.
    const loopback = /^https?:\/\/(127\.0\.0\.1|\[::1\])(:|\/)/.test(params.src);
    const localhostHost = /^https?:\/\/localhost(:|\/)/.test(params.src);
    try {
      setStatus("Fetching the graph...");
      const r = await fetch(params.src, { headers: { Accept: "application/json" } });
      if (!r.ok) throw new Error("HTTP " + r.status);
      return { data: await r.json(), source: loopback ? "loopback" : "remote" };
    } catch (e) {
      let hint = "";
      if (loopback) hint = " Is `magus graph export --open --serve` still running?";
      else if (localhostHost)
        hint =
          " The policy allows 127.0.0.1/[::1], not the `localhost` hostname: use `magus graph export --open --serve` or edit the URL to use 127.0.0.1.";
      setStatus("Could not fetch the graph from that URL (" + errMessage(e) + ")." + hint, true);
    }
  }
  // Fetch the committed demo graph for the demo button (#demo) AND for any content deep link
  // (#view/#q/#node) - those reference graph content, and the only graph available without
  // #data/#src is the demo, so a shared "explore this view of the demo" link keeps working.
  // A BARE /graph/ (no directive at all) is the cold visit that gets the empty state instead,
  // deferring the graph.json download until the visitor asks. Loading via a reload into boot
  // (not an in-place swap) renders through boot's normal pipeline - projection, fit, interactions.
  if (wantsDemo(params) || params.view || params.q || params.node) {
    try {
      // Two demos ship, both generated from THIS workspace by the root graph-generate
      // target: the knowledge graph and the target graph. Selected by the same fragment
      // param the CLI writes (cmd/magus/graph_open.go appends flavor=targets), so
      // `magus graph export --open` and a hand-typed #demo&flavor=targets land on the same data.
      const wantsTargets = params.flavor === "targets";
      setStatus(
        wantsTargets ? "Loading the magus demo target graph..." : "Loading the magus demo graph...",
      );
      // Resolve relative to THIS bundle (gen/console/graph/), not the document: standalone
      // the two share a directory, but the console mounts this surface into a page at a different path,
      // where a document-relative "./graph.json" would miss. import.meta.url makes both paths work.
      const r = await fetch(
        new URL(wantsTargets ? "./target-graph.json" : "./knowledge-graph.json", import.meta.url),
      );
      if (!r.ok) throw new Error("HTTP " + r.status);
      return { data: await r.json(), source: "demo" };
    } catch (e) {
      setStatus("Could not load the demo graph (" + errMessage(e) + ").", true);
    }
  }
  // No usable fragment: DON'T auto-fetch the demo (that download is wasted on a cold visit).
  // Return an empty graph so boot runs its full setup (interactions wired, canvas ready); boot
  // then shows the intuitive empty state, which names the fragment that loads one.
  return { data: { nodes: [], links: [] }, source: "empty" };
}

function setStatus(msg: string, isError?: boolean) {
  if (!statusEl) return;
  statusEl.textContent = msg;
  statusEl.toggleAttribute("data-error", !!isError);
}

// setResultLine narrates "what am I looking at" in the slim bar at the top of the
// canvas stage - a concise mirror of the fuller text activateView already writes to
// the bottom #graph-status line, placed where the eye already is. null hides it (the
// default state: nothing active).
function setResultLine(text: string | null) {
  const line = el("graph-result-line");
  if (!line) return;
  if (text) {
    line.textContent = text;
    line.hidden = false;
  } else {
    line.textContent = "";
    line.hidden = true;
  }
}

// ---- graph prep ------------------------------------------------------------
// Normalize the loaded JSON into d3-force's mutable shape and precompute degree
// (drives node radius) and adjacency (drives the explain card + neighbor
// highlight). Nodes/links carry id references in the JSON; d3-force's forceLink
// will replace link.source/target with the node objects in place.
function prepareGraph(raw: GraphPayload) {
  const rawNodes = raw.nodes;
  if (!rawNodes) throw new Error("graph payload has no nodes");
  // x/y/fx/fy land on each node from d3-force (or the layered layout) before any read; the
  // cast asserts that prepared invariant here so the copies satisfy GNode without a per-field seed.
  const nodes: GNode[] = rawNodes.map((n) => ({ ...n }) as GNode);
  const byId = new Map(nodes.map((n): [string, GNode] => [n.id, n]));
  const links: GLink[] = (raw.links || raw.edges || [])
    .filter((e) => byId.has(endpointId(e.source)) && byId.has(endpointId(e.target)))
    .map((e) => ({ ...e }));
  const degree = new Map<string, number>();
  for (const n of nodes) degree.set(n.id, 0);
  for (const e of links) {
    const s = endpointId(e.source);
    const t = endpointId(e.target);
    degree.set(s, (degree.get(s) ?? 0) + 1);
    degree.set(t, (degree.get(t) ?? 0) + 1);
  }
  for (const n of nodes) {
    n.degree = degree.get(n.id) || 0;
    n.r = 3 + Math.sqrt(n.degree) * 1.6;
  }
  // sourceBase (from the export's `source_base`) is the repo blob URL for turning a
  // node's relative `source` into a link to the RIGHT repo. Absent -> no link (a
  // hardcoded base would point every workspace's graph at the magus repo).
  const sourceBase = (raw.source_base || "").replace(/\/$/, "");
  return { nodes, links, byId, sourceBase };
}

// A row in the explain card's incident-edge list: the relation, the other endpoint id,
// and the edge confidence.
interface IncidentRow {
  rel: string;
  other: string;
  confidence?: string;
}

// Edges touching a node, split by direction, for the explain card.
function incidentEdges(id: string) {
  const out: IncidentRow[] = [],
    inc: IncidentRow[] = [];
  for (const e of graph.links) {
    const s = endpointId(e.source);
    const t = endpointId(e.target);
    if (s === id) out.push({ rel: e.relation, other: t, confidence: e.confidence });
    if (t === id) inc.push({ rel: e.relation, other: s, confidence: e.confidence });
  }
  return { out, inc };
}

// Undirected adjacency (node id -> Set of neighbor ids), built once per graph and
// cached on the graph. draw() runs every tick + every hover, and focus/local-graph
// BFS walks it, so a single precomputed map beats re-scanning all edges each time.
function adjacency() {
  if (!graph.adj) {
    const adj = new Map<string, Set<string>>();
    const add = (a: string, b: string) => {
      let s = adj.get(a);
      if (!s) {
        s = new Set();
        adj.set(a, s);
      }
      s.add(b);
    };
    for (const e of graph.links) {
      const s = endpointId(e.source),
        t = endpointId(e.target);
      add(s, t);
      add(t, s);
    }
    graph.adj = adj;
  }
  return graph.adj;
}
// Cached on the graph like adjacency(): the project: filter runs it once per node over the whole
// list. prepareGraph returns a fresh object on every load, so the cache resets with the data.
function projectOwners() {
  if (!graph.projectOf) graph.projectOf = computeProjectOwners(graph.nodes, graph.links);
  return graph.projectOf;
}

function neighbors(id: string | null) {
  return id ? adjacency().get(id) || null : null;
}

// neighborhood collects a node plus everything within `depth` hops - the node set
// for a local/focus graph (Obsidian's local view). Reuses the adjacency map.
function neighborhood(id: string, depth: number) {
  const set = new Set<string>([id]);
  let frontier = [id];
  for (let d = 0; d < depth; d++) {
    const next = [];
    for (const nid of frontier) {
      for (const nb of adjacency().get(nid) || []) {
        if (!set.has(nb)) {
          set.add(nb);
          next.push(nb);
        }
      }
    }
    frontier = next;
  }
  return set;
}

// ---- layered DAG layout (see layout.ts for the pure Sugiyama algorithm) ----

// PARKED_X is the off-canvas coordinate hidden nodes are pinned to (radial's unplaced set, the
// default projection's non-projects) so the force simulation does not waste cycles on them.
const PARKED_X = -1e6;

// applyLayoutedMode: switch to layered layout for the visible node/link set.
// Returns false (with a status message) when the scale guard fires.
// Stops the force simulation so no ticks disturb the fixed positions.
function applyLayeredMode() {
  const visNodes = matchSet ? graph.nodes.filter((n) => must(matchSet).has(n.id)) : graph.nodes;
  if (visNodes.length > LAYERED_MAX) {
    setStatus(
      "layered layout is capped at 500 nodes: narrow with a query or the local graph (the CLI applies the same rule to -o mermaid)",
      true,
    );
    return false;
  }
  if (sim) {
    sim?.stop();
  }
  if (cardsActive()) {
    // Measure EVERY node, not just the laid-out subset. draw() paints a card only for a node
    // carrying n.w, so measuring the subset makes the SHAPE mean "is in the match set" - matches
    // as rectangles, everything else as circles, on one canvas - and n.w is sticky, so a node
    // keeps its rectangle after the match set moves on. Card-or-dot is a property of the MODE.
    measureCards(ctx, graph.nodes, (theme ?? readTheme()).font);
    layoutLayered(visNodes, graph.links, { colW: CARD_COL_W, rowH: 48 });
  } else {
    layoutLayered(visNodes, graph.links);
  }
  draw();
  return true;
}

// applyWavesMode: switch to the waves layout (see layout.ts's layoutWaves) for
// the visible node/link set. Mirrors applyLayeredMode's scale guard, card
// measurement, and stopped-sim invariant; additionally stores the per-wave
// membership in wavesMeta so draw() can paint column bands/headers and
// switchLayout can report parallelism.
function applyWavesMode() {
  const visNodes = matchSet ? graph.nodes.filter((n) => must(matchSet).has(n.id)) : graph.nodes;
  if (visNodes.length > LAYERED_MAX) {
    setStatus("waves layout is capped at 500 nodes: narrow with a query or the local graph", true);
    wavesMeta = null;
    return false;
  }
  if (sim) {
    sim?.stop();
  }
  if (cardsActive()) {
    measureCards(ctx, graph.nodes, (theme ?? readTheme()).font); // see applyLayeredMode
    wavesMeta = layoutWaves(visNodes, graph.links, { colW: CARD_COL_W, rowH: 48 }).waves;
  } else {
    wavesMeta = layoutWaves(visNodes, graph.links).waves;
  }
  draw();
  return true;
}

// reapplyDagLayout re-runs whichever dag layout is currently active (layered or
// waves) - the shared dispatcher every re-layout call site (query, focus, live
// refresh, ...) uses so it does not need to know which dag mode is on screen.
function reapplyDagLayout(): boolean {
  return layoutMode === "waves" ? applyWavesMode() : applyLayeredMode();
}

// applyRadialMode: lay out the BFS ego rings (see radial.ts) around the
// selected (or focused) node. Unlike layered/waves, radial computes its OWN
// placed set (everything within RADIAL_MAX_RINGS hops of the center, over the
// visible subset) rather than accepting matchSet as-is - it narrows matchSet
// to that placed set afterward so the node list/status agree with what's
// drawn. Nodes that were visible but fell outside the placed set are parked
// off-canvas (fx/fy = PARKED_X, the same convention parkHiddenNodes uses) so they
// don't sit stacked at the origin. Stops the force simulation - radial pins
// fx/fy like the dag modes, even though it is not one (see isDagMode).
function applyRadialMode(): boolean {
  const center = selected ?? focusId;
  if (!center) return false; // switchLayout guards via layoutBlockedReason; safe fallback
  const subsetAll = matchSet ? graph.nodes.filter((n) => must(matchSet).has(n.id)) : graph.nodes;
  const subset = subsetAll.some((n) => n.id === center) ? subsetAll : graph.nodes;
  sim?.stop();
  // Radial deliberately takes the undirected adjacency() rather than graph.links: the ego
  // neighborhood spans every edge kind (contains, imports, uses, ...), not just depends_on,
  // so it has no colW/rowH opts like the DAG layouts - ring radius is fixed (RADIAL_RING_R).
  const { rings } = layoutRadial(center, subset, adjacency());
  radialRings = rings;
  radialCenter = center;
  const placed = new Set<string>();
  for (const ring of rings) for (const id of ring) placed.add(id);
  let hiddenCount = 0;
  for (const n of subset) {
    if (!placed.has(n.id)) {
      n.fx = PARKED_X;
      n.fy = PARKED_X;
      n.x = PARKED_X;
      n.y = PARKED_X;
      hiddenCount++;
    }
  }
  matchSet = placed;
  const centerNode = graph.byId.get(center);
  setStatus(
    "radial: " +
      placed.size +
      " nodes within " +
      RADIAL_MAX_RINGS +
      " hops of " +
      (centerNode ? centerNode.label : center) +
      (hiddenCount ? "; " + hiddenCount + " more hidden" : ""),
  );
  // matchSet just became the placed set, so the count row and node cloud have to be redrawn
  // with it or they keep reporting whatever set was showing before radial narrowed it.
  renderList();
  // Frame the freshly-placed rings. Radial centers on world origin, so without
  // this the camera stays wherever the previous layout left it and the rings
  // render off-screen (fitView calls draw()).
  fitView(matchSet);
  return true;
}

// unparkNodes releases only the nodes sitting at the parking coordinate, returning them to the
// origin. Resetting x/y is not optional: a released node keeps its parked coordinate until the
// simulation moves it, and a fitView over bounds a million units wide collapses the zoom to its
// floor, so the canvas comes back blank.
function unparkNodes() {
  for (const n of graph.nodes) {
    if (n.fx !== PARKED_X) continue;
    n.fx = null;
    n.fy = null;
    n.x = 0;
    n.y = 0;
  }
}

// unpinAllNodes hands every node back to the simulation - the pins a DAG or radial layout set
// as well as the parking pins - and lifts any parked node off PARKED_X on the way out.
function unpinAllNodes() {
  for (const n of graph.nodes) {
    n.fx = null;
    n.fy = null;
    if (n.x === PARKED_X) {
      n.x = 0;
      n.y = 0;
    }
  }
}

// layoutBlockedReason says whether a mode can run right now; the returned string is
// the reason to show in the button title when it cannot.
function layoutBlockedReason(mode: LayoutMode): string | null {
  if (mode === "waves" || mode === "layered") {
    const visCount = (matchSet ? matchSet.size : graph?.nodes.length) ?? 0;
    if (visCount > LAYERED_MAX) return "capped at 500 visible nodes: narrow with a query first";
  }
  if (mode === "radial" && !selected && !focusId) return "select a node first";
  return null;
}

// The layout toggle group's per-mode titles when available (overridden by
// layoutBlockedReason's reason string when not). Mirrors scaffold.html's static
// title attributes so the initial render and syncLayoutToggle agree.
const LAYOUT_TITLES: Record<LayoutMode, string> = {
  force:
    "Free-floating physics layout: clusters and highly-connected hubs pop out. Best for exploring the whole graph.",
  layered:
    "Left-to-right dependency flow: what depends on what, arranged in tiers. Best for reading direction.",
  waves:
    "Build order: each column is a set of targets magus can run in parallel. Best for seeing what runs when.",
  radial: "Rings around one node by distance: its neighborhood at a glance. Pick a node first.",
};
// Cycle order for the graph.layout command / l key: skips modes layoutBlockedReason rejects.
const LAYOUT_ORDER: LayoutMode[] = ["force", "layered", "waves", "radial"];
function cycleLayout() {
  const start = LAYOUT_ORDER.indexOf(layoutMode);
  for (let i = 1; i <= LAYOUT_ORDER.length; i++) {
    const next = LAYOUT_ORDER[(start + i) % LAYOUT_ORDER.length];
    if (!layoutBlockedReason(next)) {
      layoutPickedByHand = true;
      switchLayout(next);
      return;
    }
  }
}

// switchLayout changes layoutMode and applies it, wiring the DOM toggle state.
function switchLayout(mode: LayoutMode) {
  if (mode === "radial") {
    // Refuse without touching layoutMode (stay on whatever was showing); just
    // re-sync the toggle so a stale disabled/title doesn't linger.
    if (layoutBlockedReason("radial")) {
      setStatus("select a node first (click one), then Radial", true);
      syncLayoutToggle();
      return;
    }
  } else if (layoutMode === "radial") {
    unparkNodes();
    radialCenter = null;
    radialRings = null;
    // Restore the FILTER's own matches rather than nulling: radial narrowed matchSet to its
    // placed set, but the query box still reads what the operator typed, and a box that reads
    // `kind:target` over an unfiltered graph is lying about the state.
    matchSet = matchSetFor(query);
  }
  layoutMode = mode;
  if (mode !== "waves") wavesMeta = null;
  syncLayoutToggle();
  updateHash();

  if (mode === "layered" || mode === "waves") {
    const ok = mode === "waves" ? applyWavesMode() : applyLayeredMode();
    if (!ok) {
      // Scale guard fired: revert to force mode. This is the one mode change the operator did
      // not ask for, so it has to be announced; the status line is a live region and the layout
      // toggle is not. Name the reason, not the new mode - the toggle already shows which won.
      layoutMode = "force";
      wavesMeta = null;
      syncLayoutToggle();
      setStatus("too many nodes to lay out as " + mode + "; showing Force instead", true);
      // Clear fixed positions so the sim can move nodes, and any routes from
      // the dag pass so force mode never draws a stale curve.
      unpinAllNodes();
      for (const e of graph.links) delete e.points;
      if (sim) {
        sim.alpha(0.5).restart();
      } else {
        startSimulation();
      }
      // Don't write layout=<mode> to the hash.
      updateHash();
      draw();
    } else if (mode === "layered") {
      // Clear the refusal a previous, wider attempt left standing. Nothing else clears it, so
      // narrowing the query and switching again succeeded under a banner still saying the
      // layout was refused - the arrangement the operator is looking at, captioned as impossible.
      setStatus("");
    } else if (mode === "waves" && wavesMeta) {
      const widest = wavesMeta.reduce((m, ids) => Math.max(m, ids.length), 0);
      setStatus(
        "waves: " +
          wavesMeta.length +
          " waves; widest wave " +
          widest +
          " targets (max parallelism)",
      );
    }
    // Frame the new arrangement. Here and not in applyLayeredMode/applyWavesMode, which
    // liveApplyGraphUpdate re-runs on every SSE refresh - re-framing there would yank the
    // camera out from under someone reading.
    if (ok) frameArrangement();
  } else if (mode === "radial") {
    applyRadialMode();
  } else {
    // Force mode: clear fixed positions so the simulation takes over, and any
    // edge routes left over from a dag pass so force mode never draws a stale
    // curve.
    unpinAllNodes();
    for (const e of graph.links) delete e.points;
    if (sim) {
      sim.alpha(0.5).restart();
    } else {
      startSimulation();
    }
    draw();
    // Force unwinds the previous arrangement over about a second, so one frame taken now frames
    // positions that are on their way somewhere else - the same reason the load-time reveal
    // spends beats instead of a single fit. Each beat re-reads the camera: panning ends the
    // follow (centeredOn is cleared), and a beat can land after another switch.
    frameArrangement();
    for (const at of REVEAL_BEATS_MS) {
      setTimeout(() => {
        if (layoutMode !== "force") return;
        if (centeredOn) centerOn(centeredOn);
        else if (!cameraOwnedByOperator) fitView(matchSet);
      }, at);
    }
  }
  // LAST, not beside the layoutMode assignment above: the scale guard can revert layered/waves to
  // force after that point, and an overview written before the revert reports an arrangement that
  // is not on the canvas.
  refreshOverview();
}

// ---- simulation + canvas ---------------------------------------------------
function resizeCanvas() {
  const dpr = window.devicePixelRatio || 1;
  const rect = canvas.getBoundingClientRect();
  canvas.width = Math.round(rect.width * dpr);
  canvas.height = Math.round(rect.height * dpr);
  return { w: rect.width, h: rect.height, dpr };
}

// seedBigBang collapses every node onto one point so the simulation blows them outward into
// place: the first thing a freshly loaded graph does is build its own shape in front of you,
// with the load-time reveal pulling the camera back to keep the expanding cloud framed.
//
// It only seeds POSITIONS - d3-force uses a node's existing x/y when it has one and falls back
// to its own phyllotaxis spiral otherwise, so this replaces that seed and nothing else.
//
// A DISC, not a point. Many-body repulsion grows without bound as distance goes to zero, so
// stacking a few thousand nodes on one coordinate fires them out at a speed nothing recovers
// from: the weakly-held ones - and this workspace has 556 with no dependency edge at all - keep
// going until the fit clamps to its minimum scale and the graph reads as a starburst of
// streaks. The radius grows with the square root of the node count, which is how the settled
// area grows, so the burst stays proportionate on a small graph and a large one alike.
//
// Force mode only (the DAG layouts compute final coordinates, so there is nothing to expand
// from), and never under prefers-reduced-motion, where a burst of movement is precisely the
// thing being opted out of.
function seedBigBang() {
  if (isDagMode() || reducedMotion.matches || !graph?.nodes.length) return;
  const { w, h } = resizeCanvas();
  const c = usableCenter(w, h, stageInsets());
  const radius = 24 + Math.sqrt(graph.nodes.length) * 3;
  for (const n of graph.nodes) {
    // sqrt() on the radius keeps the disc evenly filled instead of clumping at the centre,
    // which is the same singularity in miniature.
    const a = Math.random() * 2 * Math.PI;
    const r = Math.sqrt(Math.random()) * radius;
    n.x = c.x + Math.cos(a) * r;
    n.y = c.y + Math.sin(a) * r;
    n.vx = 0;
    n.vy = 0;
  }
}

function startSimulation() {
  if (sim) sim?.stop(); // stop the prior run (e.g. after loading a new file) - its timer would keep ticking
  const { w, h } = resizeCanvas();
  // Settle in the part of the canvas the legend and toolbar do not cover: the cold view is
  // never fitted, so wherever the cloud lands is the operator's first impression of the graph.
  const simCenter = usableCenter(w, h, stageInsets());
  sim = forceSimulation<GNode, GLink>(graph.nodes)
    .force(
      "link",
      forceLink<GNode, GLink>(graph.links)
        .id((d) => d.id)
        .distance(55)
        .strength(0.4),
    )
    // Repulsion is what makes the layout say anything, but only the SHORT-RANGE half of it.
    // Raising the strength alone (-60 to -180) and letting it reach further just inflates the
    // periphery: the low-degree nodes sail out, the fit zooms out to hold them, and the core
    // that needed the room arrives back on screen smaller and denser than it started. Pushing
    // hard over a SHORTER range spends the force where the crowding is, so clusters separate
    // while the graph's overall extent stays roughly put.
    .force("charge", forceManyBody().strength(-180).distanceMax(250))
    .force("center", forceCenter(simCenter.x, simCenter.y))
    .force(
      "collide",
      forceCollide<GNode>().radius((d) => d.r + 2),
    )
    .force("x", forceX(simCenter.x).strength(0.02))
    .force("y", forceY(simCenter.y).strength(0.02))
    .alphaTarget(idleAlpha()) // decay toward a small floor, not 0, so it keeps gently moving
    // d3's default decay spends about 300 ticks - five seconds - reaching that floor, which is
    // a long time to watch a graph wobble before it is worth reading, and far too long for the
    // opening burst to feel like one event. This settles in roughly a second.
    .alphaDecay(0.06)
    .on("tick", draw);
}

function draw() {
  const th = theme ?? readTheme();
  const dpr = window.devicePixelRatio || 1;
  ctx.save();
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  ctx.scale(dpr, dpr);
  ctx.translate(transform.x, transform.y);
  ctx.scale(transform.k, transform.k);

  // Waves mode: paint alternating column bands and per-wave headers first, so
  // nodes/edges/labels draw on top. x comes from any member node's x (all
  // share it, per layoutWaves); the y-range is the min/max of the wave
  // membership's node y. colW matches what applyWavesMode passed to
  // layoutWaves. Text is drawn in world units (like card labels), so it scales
  // with zoom; skip it below k=0.35 where it would be unreadable anyway.
  if (layoutMode === "waves" && wavesMeta) {
    const colW = cardsActive() ? CARD_COL_W : LAYERED_COL_W;
    let minY = Infinity,
      maxY = -Infinity;
    for (const ids of wavesMeta) {
      for (const id of ids) {
        const n = graph.byId.get(id);
        if (n && n.x != null) {
          minY = Math.min(minY, n.y);
          maxY = Math.max(maxY, n.y);
        }
      }
    }
    if (minY <= maxY) {
      wavesMeta.forEach((ids, i) => {
        const first = graph.byId.get(ids[0]);
        if (!first || first.x == null) return;
        const waveX = first.x;
        if (i % 2 === 0) {
          ctx.globalAlpha = 0.06;
          ctx.fillStyle = th.border;
          ctx.fillRect(waveX - colW / 2, minY - 40, colW, maxY - minY + 64);
        }
        ctx.globalAlpha = 1;
        // Wave headers are CHROME, so they paint at a constant screen size rather than scaling
        // with the graph: sized in world units they were gated off below k=0.35, and the fit
        // that frames a tall build DAG lands nearer k=0.15 - so the one view whose whole point
        // is "these run at the same time" showed unlabelled bands at its own default zoom.
        // What still degrades is how MUCH is written, decided by the room a column has on
        // screen rather than by the zoom alone.
        const px = (n: number) => n / transform.k; // world units that paint n screen pixels
        const colScreenW = colW * transform.k;
        // Three tiers, because a waves layout is far taller than it is wide - 63 targets stacked
        // against 8 columns - so the fit that shows all of it leaves each column only ~32px.
        // A bare index still tells the operator the bands are ordered stages.
        if (colScreenW >= 18) {
          ctx.fillStyle = th.muted;
          ctx.textAlign = "center";
          const full = colScreenW >= 76;
          const named = colScreenW >= 40;
          ctx.font = px(full ? 10 : 9) + "px " + th.font;
          ctx.fillText(named ? "wave " + i : String(i), waveX, minY - px(full ? 28 : 16));
          if (full) {
            ctx.font = px(9) + "px " + th.font;
            ctx.fillText(ids.length + " in parallel", waveX, minY - px(16));
          }
        }
      });
    }
  }

  // Radial mode: paint concentric ring guides centered on the radial center
  // node, before edges/nodes, so they read as a quiet background grid (same
  // idiom as the waves bands above). e.points from a prior dag pass may be
  // stale here (applyRadialMode never computes them), so the edge pass below
  // gates the routed-curve branch on isDagMode(), not just "not force".
  const radialPlacedCount =
    layoutMode === "radial" && radialRings
      ? radialRings.reduce((n, ring) => n + ring.length, 0)
      : 0;
  if (layoutMode === "radial" && radialCenter && radialRings) {
    const centerNode = graph.byId.get(radialCenter);
    if (centerNode && centerNode.x != null) {
      ctx.strokeStyle = th.border;
      ctx.globalAlpha = 0.25;
      ctx.lineWidth = 1 / transform.k;
      for (let d = 1; d < radialRings.length; d++) {
        ctx.beginPath();
        ctx.arc(centerNode.x, centerNode.y, d * RADIAL_RING_R, 0, 2 * Math.PI);
        ctx.stroke();
      }
      ctx.globalAlpha = 1;
    }
  }

  // Two emphasis layers, COMPOSED rather than one overriding the other:
  //   scope     - matchSet, set by the query box, a view, or local-graph focus. What the user
  //               asked to see. Out-of-scope nodes fade and their edges are skipped.
  //   highlight - the selected or hovered node. A spotlight that ADDS its own neighborhood on
  //               top of the scope, reaching outside it when the clicked node is not a match.
  // The highlight must never suppress the scope: a selection left over from an earlier click
  // would otherwise cancel the emphasis of every view and query run after it, so a chain the
  // result line reports would not be the thing drawn on the canvas.
  const highlight = selected || hoverId;
  const near = neighbors(highlight);
  const lit = (id: string) => id === highlight || !!near?.has(id);
  // Cap on how big a neighbourhood still earns the glow - see the shadow cost note in the node
  // pass. A hub with hundreds of neighbours reads as a lit blob anyway, so the effect it buys
  // there is small and the per-frame cost is not.
  const glowNeighborhood = !!highlight && (near?.size ?? 0) <= 120;

  // Edges first, under the nodes. Dim edges not touching the highlighted node;
  // under a query filter, dim edges not between two matches, so the
  // matching subgraph stands out instead of a full bright web.
  // projectionActive: hide all non-projection nodes/edges from the draw.
  // (Same flag computed below for nodes; computed here first for edges.)
  // projectionActive is the projection id set when the default projection is showing, else null;
  // holding the Set (not a bool) lets its truthiness narrow away the nullable in the checks below.
  const projectionActive: Set<string> | null =
    !projectionUnfolded && projectionSet && !query && !focusId && !activeView
      ? projectionSet
      : null;
  // The projection is its own visibility rule (nodes outside it are absent, not dim), so it
  // stands in for the scope while it is active.
  const scope = matchSet && !projectionActive ? matchSet : null;
  for (const e of graph.links) {
    // By draw time d3-force has resolved source/target from id strings to the node objects.
    const s = e.source as GNode,
      t = e.target as GNode;
    if (s.x == null || t.x == null) continue; // not "!s.x": a node validly at x=0 must still draw
    // Default projection: only draw edges where both endpoints are in the projection.
    if (projectionActive && !(projectionActive.has(s.id) && projectionActive.has(t.id))) continue;
    // Card side-attach: when cards are active, lines attach to card sides
    // (the lower-x endpoint's right edge -> the higher-x endpoint's left
    // edge) instead of centers, so edges terminate at the card border. sx/sy
    // and tx/ty stand in for s.x/s.y and t.x/t.y everywhere below; outside
    // card mode (or for a node without .w) they equal the plain center.
    const cardsOn = cardsActive();
    let sx = s.x,
      sy = s.y,
      tx = t.x,
      ty = t.y;
    if (cardsOn && (s.w || t.w)) {
      const sLeft = s.x <= t.x;
      if (s.w) sx = sLeft ? s.x + s.w / 2 : s.x - s.w / 2;
      if (t.w) tx = sLeft ? t.x - t.w / 2 : t.x + t.w / 2;
    }
    let active;
    if (scope) {
      // Under a filter, draw ONLY edges between two matches - skipping the rest keeps the
      // matching subgraph clean instead of a faint full-web haze - plus the highlight's own
      // edges, so clicking a node outside the filter still shows what it touches.
      const inScope = scope.has(s.id) && scope.has(t.id);
      if (!inScope && !(s.id === highlight || t.id === highlight)) continue;
      active = true;
    } else if (highlight) active = s.id === highlight || t.id === highlight;
    else active = true;
    // Critical-path emphasis: an edge between two nodes both on the current
    // critical chain reads as the spine - thicker and at full alpha instead
    // of the default muted stroke.
    const criticalEdge =
      activeView === "critical" && !!matchSet && matchSet.has(s.id) && matchSet.has(t.id);
    // An edge touching the hovered or selected node draws in the accent, not the muted grey.
    // Dimming everything else already isolates the neighbourhood, but only by subtraction - the
    // reader has to notice what did NOT fade. Colouring the incident edges says which lines are
    // the answer, and it is the connections, not the nodes, that carry "what is this attached to".
    const incident = !!highlight && (s.id === highlight || t.id === highlight);
    ctx.strokeStyle = incident ? th.accent : active ? th.muted : th.border;
    ctx.globalAlpha = incident || criticalEdge ? 1 : active ? 0.55 : 0.1;
    ctx.lineWidth = incident
      ? 1.6 / transform.k
      : criticalEdge
        ? 2.2 / transform.k
        : 0.6 / transform.k;
    // Cycle edges (from the target-graph adapter) get a dashed stroke so they
    // stand out from normal dependency edges. Layout-reversed edges (cycle-break
    // in layered mode) also render dashed.
    const dashed = e.cycle || e.layoutReversed;
    if (dashed) ctx.setLineDash([4 / transform.k, 3 / transform.k]);
    // Routed edges (multi-layer spans in a DAG mode) carry world-space bend
    // points ordered ascending-x (dependency end -> dependent end, see
    // types.ts). Assemble the full polyline (both endpoints + bend points)
    // sorted by x so it reads correctly regardless of which of s/t is the
    // geometrically-left endpoint (layoutReversed edges can flip that).
    const routePts: { x: number; y: number }[] | null =
      isDagMode() && e.points && e.points.length
        ? [{ x: sx, y: sy }, { x: tx, y: ty }, ...e.points].sort((a, b) => a.x - b.x)
        : null;
    ctx.beginPath();
    if (routePts) {
      // Smooth curve through the bend points via quadratic segments to their
      // midpoints (a cheap Catmull-Rom-ish approximation), ending with a
      // straight segment into the final endpoint.
      ctx.moveTo(routePts[0].x, routePts[0].y);
      for (let i = 1; i < routePts.length - 1; i++) {
        const midX = (routePts[i].x + routePts[i + 1].x) / 2,
          midY = (routePts[i].y + routePts[i + 1].y) / 2;
        ctx.quadraticCurveTo(routePts[i].x, routePts[i].y, midX, midY);
      }
      ctx.lineTo(routePts[routePts.length - 1].x, routePts[routePts.length - 1].y);
    } else if (isDagMode()) {
      // Short edge (single layer span, no bend points): a gentle horizontal-out
      // bezier so the DAG still reads as flow instead of a ruler-straight line.
      ctx.moveTo(sx, sy);
      const dx = tx - sx;
      if (Math.abs(dx) > 24) ctx.bezierCurveTo(sx + dx * 0.4, sy, tx - dx * 0.4, ty, tx, ty);
      else ctx.lineTo(tx, ty);
    } else {
      ctx.moveTo(s.x, s.y);
      ctx.lineTo(t.x, t.y);
    }
    ctx.stroke();
    if (dashed) ctx.setLineDash([]);

    // Arrowheads: only in dag modes (layered/waves - they add clarity on the
    // DAG's directed edges; in force mode at demo-graph density they would be
    // visual noise). Convention matches the Go mermaid emitter (LR direction):
    // the dependency is placed at a lower x (left) and the dependent at a
    // higher x (right). In link terms: e.source = dependent (right), e.target
    // = dependency (left). The arrowhead is drawn at the SOURCE end (the
    // dependent node on the right), matching mermaid `dependency --> dependent`
    // reading left-to-right. For layout-reversed back-edges the arrow tip moves
    // to the target end (the reversed direction is layout-only fiction; the
    // mark calls it out).
    if (isDagMode() && active && e.relation === "depends_on") {
      const isReversed = !!e.layoutReversed;
      // Arrow tip: at source (dependent, right) for normal edges; at target for reversed.
      const tipNode = isReversed ? t : s;
      // Attach-point coords for the tip end (equal to the plain center outside
      // card mode, since sx/tx only diverge from s.x/t.x when cardsOn).
      const tipAttachX = tipNode === s ? sx : tx;
      const tipAttachY = tipNode === s ? sy : ty;
      // Direction vector from the other end toward the tip. For a routed edge,
      // use the nearest bend point instead of the opposite endpoint so the
      // arrow aligns with the curve's final segment rather than the chord.
      const fromNode = isReversed ? s : t;
      let fromX = fromNode === s ? sx : tx,
        fromY = fromNode === s ? sy : ty;
      if (routePts && routePts.length > 2) {
        const nearest = routePts[0].x === tipAttachX ? routePts[1] : routePts[routePts.length - 2];
        fromX = nearest.x;
        fromY = nearest.y;
      }
      const dx = tipAttachX - fromX,
        dy = tipAttachY - fromY;
      const len = Math.sqrt(dx * dx + dy * dy) || 1;
      const ux = dx / len,
        uy = dy / len;
      // Place the tip at the node's edge: the card's near side (already
      // computed above as the attach point) when cards are active, else the
      // circle radius plus a small gap.
      let tipX: number, tipY: number;
      if (cardsOn && tipNode.w) {
        tipX = tipAttachX;
        tipY = tipAttachY;
      } else {
        const tipR = tipNode.r || 5;
        tipX = tipNode.x - ux * (tipR + 1 / transform.k);
        tipY = tipNode.y - uy * (tipR + 1 / transform.k);
      }
      const aLen = 8 / transform.k; // arrowhead length
      const aWid = 4 / transform.k; // arrowhead half-width
      // Perpendicular vector.
      const px = -uy,
        py = ux;
      ctx.beginPath();
      ctx.moveTo(tipX, tipY);
      ctx.lineTo(tipX - ux * aLen + px * aWid, tipY - uy * aLen + py * aWid);
      ctx.lineTo(tipX - ux * aLen - px * aWid, tipY - uy * aLen - py * aWid);
      ctx.closePath();
      ctx.fillStyle = active ? th.muted : th.border;
      ctx.fill();
    }
  }
  ctx.globalAlpha = 1;

  // Nodes. When something is highlighted, fade non-neighbors; when a search is
  // active, fade non-matches; when the default projection is active (no query),
  // fully hide nodes outside the projection set (projects only).
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    // Default projection: hide non-project nodes entirely (not dimmed; truly absent).
    if (projectionActive && !projectionActive.has(n.id)) continue;
    let alpha = 1;
    if (scope) alpha = scope.has(n.id) || lit(n.id) ? 1 : 0.12;
    else if (highlight) alpha = lit(n.id) ? 1 : 0.15;
    if (cardsActive() && n.w) {
      const kindColor = groupColorFor(n) || th.kindColor[n.kind] || "#888";
      drawCard(ctx, n, {
        theme: th,
        kindColor,
        alpha,
        selected: n.id === selected,
        anchor: n.kind === "target" && n.attrs?.anchor === "true",
        zoomK: transform.k,
        durationText:
          graphHasDurations && nodeDurationMs(n) > 0 ? formatDuration(nodeDurationMs(n)) : null,
      });
      continue;
    }
    ctx.globalAlpha = alpha;
    const nodeColor = groupColorFor(n) || th.kindColor[n.kind] || "#888";
    // The hovered node and everything one hop from it GLOW, rather than merely failing to dim.
    // Blur is divided by the zoom so the halo stays a constant size on screen instead of
    // ballooning as the view zooms in. Skipped when the neighbourhood is large: a canvas shadow
    // is redrawn per node per frame, and a hub with hundreds of neighbours would pay for it on
    // every tick of a simulation that never fully cools.
    const glowing = glowNeighborhood && lit(n.id);
    if (glowing) {
      ctx.shadowColor = th.accent;
      ctx.shadowBlur = (n.id === highlight ? 16 : 9) / transform.k;
    }
    ctx.beginPath();
    ctx.arc(n.x, n.y, n.r, 0, 2 * Math.PI);
    ctx.fillStyle = nodeColor;
    ctx.fill();
    if (glowing) ctx.shadowBlur = 0;
    // Anchor ring: target-graph anchor targets (top-level, nothing depends on
    // them within their project) get an outer ring in the same kind color so
    // they stand out without adding a new palette entry.
    if (n.kind === "target" && n.attrs && n.attrs.anchor === "true") {
      ctx.lineWidth = 1.5 / transform.k;
      ctx.strokeStyle = nodeColor;
      ctx.globalAlpha = alpha * 0.55;
      ctx.beginPath();
      ctx.arc(n.x, n.y, n.r + 3 / transform.k, 0, 2 * Math.PI);
      ctx.stroke();
      ctx.globalAlpha = alpha;
    }
    if (n.id === selected) {
      ctx.lineWidth = 2 / transform.k;
      ctx.strokeStyle = th.accent;
      ctx.beginPath();
      ctx.arc(n.x, n.y, n.r, 0, 2 * Math.PI);
      ctx.stroke();
    }
  }
  ctx.globalAlpha = 1;

  // Motion layer: flow particles along the active view's edges, and
  // recency-pulse rings from a live refresh. Gated on prefers-reduced-motion
  // and tab visibility here too (not just in motionEligible/motionLoop) so
  // nothing paints even when draw() is triggered by something other than the
  // motion loop (a click, a drag, a theme toggle) while motion should stay
  // suppressed. Painted after nodes/cards, before labels, so a pulse ring
  // doesn't clip label text. tick() returning null (no flow, no unexpired
  // pulses) is what lets pulsesPending self-clear, which is in turn what lets
  // motionLoop stop re-arming itself once nothing is left to animate.
  if (!reducedMotion.matches && !document.hidden) {
    const motion = particlesTick(performance.now());
    if (!motion) {
      pulsesPending = false;
    } else {
      if (motion.flowPoints.length) {
        ctx.fillStyle = th.accent;
        const flowR = 2.2 / transform.k;
        for (const p of motion.flowPoints) {
          ctx.globalAlpha = p.alpha;
          ctx.beginPath();
          ctx.arc(p.x, p.y, flowR, 0, 2 * Math.PI);
          ctx.fill();
        }
      }
      if (motion.pulses.size) {
        ctx.strokeStyle = th.accent;
        ctx.lineWidth = 1.5 / transform.k;
        for (const [id, progress] of motion.pulses) {
          const n = graph.byId.get(id);
          if (!n || n.x == null) continue;
          ctx.globalAlpha = 0.5 * (1 - progress);
          if (cardsActive() && n.w) {
            const grow = (progress * 26) / transform.k;
            const h = n.h ?? 0;
            ctx.strokeRect(n.x - n.w / 2 - grow, n.y - h / 2 - grow, n.w + grow * 2, h + grow * 2);
          } else {
            ctx.beginPath();
            ctx.arc(n.x, n.y, n.r + (progress * 26) / transform.k, 0, 2 * Math.PI);
            ctx.stroke();
          }
        }
      }
      ctx.globalAlpha = 1;
    }
  }

  // Labels: greedy collision-culling. Draw in priority order (the selection, then the
  // highest-degree nodes) and skip any label whose box overlaps one already drawn this
  // frame, so text stays readable instead of stacking into an unreadable smear. This is
  // the d3fc "greedy / removeOverlaps" idea done inline - d3 core has no label placer,
  // and this is what its greedy strategy does under the hood. Still gated to big nodes,
  // the selection, or a zoomed-in view; and culled to the viewport first so the overlap
  // scan stays cheap. Boxes are compared in world units (the same scale as the on-screen
  // 11px text), so the overlap test is zoom-consistent.
  ctx.fillStyle = th.text;
  ctx.font = "500 " + 11 / transform.k + "px " + th.font;
  ctx.textAlign = "left";
  ctx.textBaseline = "middle";
  const vw = canvas.width / dpr,
    vh = canvas.height / dpr; // viewport in CSS px
  const labelPad = 2 / transform.k;
  const lineH = 13 / transform.k; // ~1.2x the 11px font, in world units
  const labelCandidates = [];
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    if (projectionActive && !projectionActive.has(n.id)) continue;
    if (cardsActive() && n.w) continue; // the label is painted inside the card
    // Two exemptions from the zoom floor, both cases where the reader ASKED for a name. The
    // first is the hovered or selected node AND EVERYTHING ONE HOP FROM IT: naming only the node
    // already under the cursor answers a question nobody asked, since the point of pointing at a
    // node is to learn what it is attached to. The greedy overlap pass below is what keeps a
    // dense neighbourhood from stacking, and it ranks the focus first. The second is radial,
    // which exists to name one node's neighbours and never places more than a few dozen.
    const show =
      lit(n.id) ||
      (layoutMode === "radial" && radialPlacedCount <= 60) ||
      (transform.k >= LABEL_MIN_ZOOM &&
        (transform.k > 2.2 || (n.degree > 24 && n.r * transform.k >= LABEL_MIN_NODE_PX)));
    if (!show) continue;
    if (matchSet && !projectionActive && !matchSet.has(n.id) && n.id !== highlight) continue;
    // Viewport cull (CSS px): drop off-screen labels so the greedy scan below only
    // weighs what's actually visible.
    const cx = transform.x + n.x * transform.k,
      cy = transform.y + n.y * transform.k;
    if (cx < -120 || cx > vw + 20 || cy < -20 || cy > vh + 20) continue;
    labelCandidates.push(n);
  }
  // Priority order: the selected node always wins a slot, then denser (more-connected)
  // nodes, so the labels we keep are the ones carrying the most signal.
  labelCandidates.sort(
    (a, b) => (b.id === highlight ? 1 : 0) - (a.id === highlight ? 1 : 0) || b.degree - a.degree,
  );
  const placedLabels = [];
  for (const n of labelCandidates) {
    const lx = n.x + n.r + labelPad;
    const ly = n.y - lineH / 2;
    const lw = ctx.measureText(n.label).width;
    let clash = false;
    for (const p of placedLabels) {
      if (lx < p.x + p.w && lx + lw > p.x && ly < p.y + lineH && ly + lineH > p.y) {
        clash = true;
        break;
      }
    }
    if (clash) continue;
    placedLabels.push({ x: lx, y: ly, w: lw });
    ctx.fillText(n.label, lx, n.y);
  }
  ctx.restore();
}

// ---- interaction -----------------------------------------------------------
function nodeAtPointer(event: MouseEvent): GNode | null | undefined {
  const rect = canvas.getBoundingClientRect();
  const px = (event.clientX - rect.left - transform.x) / transform.k;
  const py = (event.clientY - rect.top - transform.y) / transform.k;
  // Card mode: rectangular hit test in reverse draw order (so an overlapping
  // card on top wins), since sim.find's circle radius doesn't match card shape.
  // The target has to match what was DRAWN: zoomed out far enough that cards paint as dots
  // (cardDetail), a card-sized rectangle would claim a patch of empty canvas many times the
  // mark the operator is aiming at. Dots take a generous screen-constant radius instead, so
  // they stay reachable at the zoom that frames the whole DAG.
  if (cardsActive()) {
    const dots = cardDetail(transform.k) === "dot";
    const hitR = (DOT_R_PX + 5) / transform.k;
    for (let i = graph.nodes.length - 1; i >= 0; i--) {
      const n = graph.nodes[i];
      if (n.x == null) continue;
      if (dots) {
        if ((px - n.x) ** 2 + (py - n.y) ** 2 <= hitR * hitR) return n;
        continue;
      }
      if (!n.w || !n.h) continue;
      if (Math.abs(px - n.x) <= n.w / 2 && Math.abs(py - n.y) <= n.h / 2) return n;
    }
    return null;
  }
  // In layered mode the simulation may be stopped, but sim.find still works on
  // the node positions. Fall back to a manual scan when sim is null (shouldn't
  // happen, but be safe).
  if (sim) return sim.find(px, py, 30 / transform.k);
  let best = null,
    bestDist = 30 / transform.k;
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    const d = Math.sqrt((n.x - px) ** 2 + (n.y - py) ** 2);
    if (d < bestDist) {
      bestDist = d;
      best = n;
    }
  }
  return best;
}

function setupZoomDrag() {
  zoomBehavior = d3zoom<HTMLCanvasElement, unknown>()
    .scaleExtent([0.1, 8])
    .filter((event) => !event.button && event.type !== "dblclick")
    .on("zoom", (event) => {
      // sourceEvent is null for the programmatic transforms fitView applies; a real wheel or
      // drag means the operator has framed the graph themselves and the load-time reveal must
      // stop moving the camera under their hands.
      if (event.sourceEvent) {
        cameraOwnedByOperator = true;
        centeredOn = null; // panning away from a centred node ends the follow
      }
      transform = event.transform;
      draw();
    });
  select(canvas).call(zoomBehavior);

  const dragBehavior = d3drag<HTMLCanvasElement, unknown, GNode | undefined>()
    .subject((event) => nodeAtPointer(event.sourceEvent) ?? undefined)
    .on("start", (event) => {
      if (!event.subject) return;
      if (!isDagMode() && !event.active) sim?.alphaTarget(0.2).restart();
      event.subject.fx = event.subject.x;
      event.subject.fy = event.subject.y;
    })
    .on("drag", (event) => {
      if (!event.subject) return;
      event.subject.fx = (event.x - transform.x) / transform.k;
      event.subject.fy = (event.y - transform.y) / transform.k;
      // In a dag mode the sim is stopped; draw manually on each drag event.
      if (isDagMode()) {
        event.subject.x = event.subject.fx;
        event.subject.y = event.subject.fy;
        draw();
      }
    })
    .on("end", (event) => {
      if (!event.subject) return;
      if (isDagMode()) {
        // Keep the manually dragged position (fx/fy stay set); just redraw.
        draw();
        return;
      }
      if (!event.active) sim?.alphaTarget(idleAlpha()); // back to the gentle floor, not a dead stop
      event.subject.fx = null;
      event.subject.fy = null;
    });
  select(canvas).call(dragBehavior);

  canvas.addEventListener("click", (event) => {
    const n = nodeAtPointer(event);
    if (n) selectNode(n.id, false);
    else selectNode(null, false);
  });
  // Double-click a node -> local/focus graph around it (d3-zoom's own dblclick
  // zoom is disabled via the filter above, so this is free to use).
  canvas.addEventListener("dblclick", (event) => {
    const n = nodeAtPointer(event);
    if (n) focusNode(n.id, focusDepth);
  });
  canvas.addEventListener("mousemove", (event) => {
    const n = nodeAtPointer(event);
    const id = n ? n.id : null;
    if (id !== hoverId) {
      releaseHoverPin();
      hoverId = id;
      pinHovered(id);
      canvas.style.cursor = id ? "pointer" : "grab";
      draw();
    }
  });
  // A pointer that leaves the canvas fires no further mousemove, so without this the node it was
  // last over stays pinned for good - frozen out of the simulation with nothing to unfreeze it.
  canvas.addEventListener("mouseleave", () => {
    if (!hoverId && !hoverPinned) return;
    releaseHoverPin();
    hoverId = null;
    draw();
  });
}

// ---- explain card ----------------------------------------------------------
function escapeHtml(s: unknown) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// safeUrl returns u only if it is an http(s) URL, else null - a graph.json is
// untrusted input (a visitor can drop any file), so attrs.url must not become a
// `javascript:` href.
function safeUrl(u: string) {
  try {
    const p = new URL(u, location.href);
    return p.protocol === "http:" || p.protocol === "https:" ? u : null;
  } catch {
    return null;
  }
}

// A node reference rendered as a button that re-selects it (edges link to their
// other endpoint).
function nodeRefHtml(id: string) {
  const n = graph.byId.get(id);
  const label = n ? n.label : id;
  return (
    '<button type="button" class="console-graph-card__ref" data-id="' +
    escapeHtml(id) +
    '">' +
    escapeHtml(label) +
    "</button>"
  );
}

function relSectionHtml(title: string, rows: IncidentRow[]) {
  if (!rows.length) return "";
  const byRel = new Map<string, IncidentRow[]>();
  for (const r of rows) {
    if (!byRel.has(r.rel)) byRel.set(r.rel, []);
    byRel.get(r.rel)?.push(r);
  }
  let html = "<dt>" + escapeHtml(title) + "</dt><dd>";
  const relRank = (r: string) => {
    const i = RELATIONS.indexOf(r);
    return i < 0 ? RELATIONS.length : i; // a relation this list predates trails the ones it names
  };
  const rels = [...byRel.keys()].sort((a, b) => relRank(a) - relRank(b) || (a < b ? -1 : 1));
  for (const rel of rels) {
    const items = byRel.get(rel);
    if (!items) continue;
    html +=
      '<div class="console-graph-card__relgroup"><span class="console-graph-card__relname">' +
      escapeHtml(rel) +
      ' <span class="console-graph-card__relcount">(' +
      items.length +
      ")</span></span> ";
    html += items
      .slice(0, 40)
      .map((r) => nodeRefHtml(r.other))
      .join(" ");
    if (items.length > 40)
      html += ' <span class="console-graph-card__muted">+' + (items.length - 40) + " more</span>";
    html += "</div>";
  }
  return html + "</dd>";
}

// graphSource names where the loaded graph came from ("demo", "live", "local", "loopback",
// "remote", "empty"), so the overview can say which one a reader is looking at.
let graphSource = "empty";

// SOURCE_PROSE turns that into a sentence. A reader who cannot tell the committed demo from
// their own workspace cannot trust anything else the panel says.
const SOURCE_PROSE: Record<string, string> = {
  demo: "the committed demo export of the magus repo, not your workspace",
  live: "a live workspace, refreshing as it changes",
  local: "a graph.json you opened from this machine",
  loopback: "a one-shot snapshot from a local magus",
  remote: "a graph fetched from a URL",
};

// renderOverview fills the detail column when NOTHING is selected. That column used to collapse,
// which left the surface answering "what am I looking at" nowhere at all: the arrangement, the
// colouring and the scope were each legible only as a lit-up button somewhere in the sidebar, and
// the canvas alone cannot say whether it is showing a filtered subset or a different graph.
// It states the graph, then every setting currently acting on it, in sentences.
function renderOverview() {
  const scope = matchSet
    ? matchSet.size + " of " + graph.nodes.length + " nodes match " + (query || "the active view")
    : "No filter: every node is showing.";
  const colour = activePreset
    ? (PRESET_RESULT_LINES[activePreset] ?? "A colour grouping is active.")
    : "Coloured by node kind; the legend lists them.";
  const rows: Array<[string, string]> = [
    [
      "Graph",
      (graphFlavor === "targets" ? "The target graph" : "The knowledge graph") +
        " - " +
        (SOURCE_PROSE[graphSource] ?? "an unnamed source") +
        ".",
    ],
    ["Size", graph.nodes.length + " nodes, " + graph.links.length + " edges."],
    ["Arrangement", LAYOUT_TITLES[layoutMode]],
    ["Colour", colour],
    ["Scope", scope],
  ];
  let html = '<p class="console-graph-card__section">What you are looking at</p>';
  html += "<dl>"; // #explain-card dl already carries the two-column grid
  for (const [k, v] of rows) {
    html += "<dt>" + escapeHtml(k) + "</dt><dd>" + escapeHtml(v) + "</dd>";
  }
  html += "</dl>";
  html +=
    '<p class="console-graph-card__hint">Click a node for its details. The questions on the left' +
    " run the same things the CLI does: the first three ask about ONE node you pick, the next" +
    " three ask about the whole graph at once.</p>";
  cardEl.innerHTML = html;
  cardEl.hidden = false;
  document.body.toggleAttribute("data-has-card", true);
}

// refreshOverview repaints the overview when it is what the detail column is showing. Every
// setting it reports is owned elsewhere, so each owner has to say when it moved. NOT hooked into
// draw() (that runs per frame, and this writes innerHTML) and not into renderList() alone (which
// the arrangement never calls).
function refreshOverview() {
  if (!selected && graph?.nodes) renderOverview();
}

function renderCard(id: string | null) {
  const n = id ? graph.byId.get(id) : null;
  if (!n) {
    renderOverview();
    docTitle.set(null); // nothing selected: the tab falls back to "Graph Explorer"
    return;
  }
  document.body.toggleAttribute("data-has-card", true);
  // The selected node is what this surface has open, so the console names its tab after it. Driven
  // from here rather than from selectNode's several assignment sites: the card is the ONE place a
  // selection is rendered, so the tab cannot disagree with what the panel shows.
  docTitle.set(n.label);
  const { out, inc } = incidentEdges(n.id);
  let html = "";
  html += '<p class="console-graph-card__section">Node details</p>';
  html += '<header class="console-graph-card__head">';
  html += '<span class="console-graph-kinddot" data-kind="' + escapeHtml(n.kind) + '"></span>';
  html += "<h2>" + escapeHtml(n.label) + "</h2>";
  html += '<span class="console-graph-card__kindtag">' + escapeHtml(n.kind) + "</span>";
  html += "</header>";
  html += "<dl>";
  html += "<dt>id</dt><dd><code>" + escapeHtml(n.id) + "</code></dd>";
  html += "<dt>degree</dt><dd>" + n.degree + " edge" + (n.degree === 1 ? "" : "s") + "</dd>";
  if (n.doc) html += "<dt>doc</dt><dd>" + escapeHtml(n.doc) + "</dd>";
  if (n.source) {
    // source is "path" or "path:line". Link to the repo's blob URL only when the
    // graph carries a source_base for the workspace it came from; otherwise show
    // the path plainly rather than guessing a (probably wrong) repo.
    const path = n.source.split(":")[0];
    const base = graph.sourceBase;
    // graph.json/source_base is untrusted, so scheme-guard the built href with safeUrl (matching the
    // sibling attrs.url case below); fall back to the plain path when it is not an http(s) URL.
    const sourceHref = base ? safeUrl(base + "/" + path) : null;
    html += sourceHref
      ? '<dt>source</dt><dd><a href="' +
        escapeHtml(sourceHref) +
        '" target="_blank" rel="noopener"><code>' +
        escapeHtml(n.source) +
        "</code></a></dd>"
      : "<dt>source</dt><dd><code>" + escapeHtml(n.source) + "</code></dd>";
  }
  if (n.attrs && n.attrs.url && safeUrl(n.attrs.url)) {
    html +=
      '<dt>reference</dt><dd><a href="' +
      escapeHtml(n.attrs.url) +
      '" target="_blank" rel="noopener">' +
      escapeHtml(n.attrs.url) +
      "</a></dd>";
  }
  html += relSectionHtml("outgoing", out);
  html += relSectionHtml("incoming", inc);
  html += "</dl>";
  html +=
    '<div class="console-graph-card__actions"><button type="button" class="console-graph-card__noteslink" title="Open the workspace notes that explain decisions around this graph">Open workspace notes</button></div>';
  cardEl.innerHTML = html;
  cardEl.hidden = false;
  cardEl
    .querySelectorAll<HTMLElement>(".console-graph-card__ref")
    .forEach((b) => b.addEventListener("click", () => selectNode(b.dataset.id ?? null, true)));
  const notesCardBtn = cardEl.querySelector<HTMLElement>(".console-graph-card__noteslink");
  if (notesCardBtn) notesCardBtn.addEventListener("click", () => openSurface({ pageId: "notes" }));
}

// ---- selection, search, list, deep links -----------------------------------
function selectNode(id: string | null, center: boolean) {
  // Default projection - clicking a project node in projection mode unfolds it.
  if (!projectionUnfolded && id && projectionSet && projectionSet.has(id)) {
    const n = graph.byId ? graph.byId.get(id) : null;
    if (n && n.kind === "project") {
      // Unfold this project: show its contains neighborhood.
      projectionUnfolded = true;
      projectionSet = null;
      unparkNodes();
      const projectNeighborhood = new Set([id]);
      for (const e of graph.links) {
        const s = endpointId(e.source),
          t = endpointId(e.target);
        if (
          (s === id && e.relation === "contains") ||
          (t === id && e.relation === "depends_on" && s === id)
        ) {
          projectNeighborhood.add(t);
        }
        if (t === id && e.relation === "contains") projectNeighborhood.add(s);
      }
      matchSet = projectNeighborhood;
      // Keep "Show full graph" visible: this branch narrows to ONE project's neighborhood, so
      // the button is the way back out, and the status line below names it as such.
      setStatus("Showing " + id + " neighborhood. Press Esc or Show full graph to see everything.");
      renderList();
      if (matchSet.size) fitView(matchSet);
      updateHash();
      draw();
      return;
    }
  }

  // Blast view picking mode - clicking a node activates blast on it.
  if (activeView === "blast" && id && !viewNode) {
    activateView("blast", id);
    return;
  }

  // Trace view picking mode - two-click flow.
  if (activeView === "trace" && id) {
    if (!viewNode) {
      viewNode = id;
      const n = graph.byId ? graph.byId.get(id) : null;
      setStatus("First node: " + (n ? n.label : id) + ". Now click the second node.");
      renderViewCommand("trace", id, null);
      updateHash();
      return;
    } else if (!viewNodeTo && id !== viewNode) {
      activateView("trace", viewNode, id);
      return;
    }
  }

  selected = id;
  renderCard(id);
  updateHash();
  if (id && center) centerOn(id);
  syncListSelection();
  syncLayoutToggle(); // availability tracks selection
  draw();

  // Radial re-centers on whatever gets selected next; the Ask chip's pick-flow
  // (pendingRadialPick) enters radial the first time a node is selected. Both
  // go through switchLayout("radial") - never back through selectNode - so
  // this cannot recurse.
  if (id && (pendingRadialPick || layoutMode === "radial")) {
    pendingRadialPick = false;
    switchLayout("radial");
  }
}

function centerOn(id: string) {
  const n = graph.byId.get(id);
  if (!n || n.x == null || !zoomBehavior) return;
  // Putting a node in the middle is a camera the operator asked for, so it is theirs from here.
  // Selecting also OPENS the explain card, which narrows the stage: the resize lands AFTER this
  // runs, so the centring is computed against the wider canvas and would be half a card off by
  // the time it is seen - and the resize re-frame would refit the whole match set on top of
  // that, sailing the node just clicked off the screen. Remembering the subject lets the resize
  // re-centre on it instead.
  cameraOwnedByOperator = true;
  centeredOn = id;
  const { w, h } = resizeCanvas();
  // Centre in the part of the canvas the legend and toolbar leave visible, not the raw middle.
  const c = usableCenter(w, h, stageInsets());
  // Shorter than a fit: this is a small move to a node the operator is already looking at, and
  // a long glide there would feel like the view hesitating.
  glideTo(
    zoomIdentity.translate(c.x - n.x * transform.k, c.y - n.y * transform.k).scale(transform.k),
    260,
  );
}

// fitView frames a set of nodes (or all when ids is null) in the viewport - the
// zoom-to-fit / reset-view action. Reuses the shared zoomBehavior + transform.
// The stage floats chrome over the canvas rather than beside it. These are the pieces that do,
// measured live so a hidden one (the legend is closed on mobile, the toolbar collapses to a
// kebab) contributes nothing. The explain card is NOT here: it sits beside the canvas.
const STAGE_OVERLAYS = [
  "#graph-legend-panel",
  "#legend-toggle",
  ".console-graph-stage__tools",
  ".console-graph-stage__result",
];

// stageInsets measures how far the stage chrome covers the canvas, so the framing below can
// centre the graph in what the operator can see instead of behind the legend.
function stageInsets(): Insets {
  if (!canvas) return NO_INSETS;
  const view = canvas.getBoundingClientRect() as Rect;
  const overlays: Rect[] = [];
  for (const sel of STAGE_OVERLAYS) {
    for (const node of document.querySelectorAll<HTMLElement>(sel)) {
      if (node.hidden) continue;
      overlays.push(node.getBoundingClientRect());
    }
  }
  return overlayInsets(view, overlays);
}

// Set once the operator pans or zooms by hand. The reveal below stops re-framing after that:
// a camera that keeps moving while someone is reading is worse than a frame that came out loose.
let cameraOwnedByOperator = false;
// The node the camera is currently holding in the middle, if any. A stage resize re-centres on
// it rather than re-framing, and a pan, zoom or fit releases it.
let centeredOn: string | null = null;

// The node currently held still because the pointer is on it. The simulation never fully cools -
// that gentle drift is deliberate - but it means a node can wander out from under a stationary
// cursor, dropping the highlight and darkening the neighbourhood while the reader did nothing.
// So the thing being pointed at stops; its neighbours carry on moving around it.
let hoverPinned: string | null = null;

function pinHovered(id: string | null) {
  // Force mode only. The DAG layouts and radial pin every node themselves and run with the
  // simulation stopped, so nothing drifts there and a pin of ours would be indistinguishable
  // from theirs when it came time to release it.
  if (!id || isDagMode() || layoutMode === "radial") return;
  const n = graph?.byId.get(id);
  // Only pin what is NOT already pinned: a parked node, or one held by a drag, belongs to
  // whoever pinned it, and releasing that on mouseout would undo their work.
  if (!n || n.fx != null) return;
  n.fx = n.x;
  n.fy = n.y;
  hoverPinned = id;
}

function releaseHoverPin() {
  if (!hoverPinned) return;
  const n = graph?.byId.get(hoverPinned);
  if (n && n.fx !== PARKED_X) {
    n.fx = null;
    n.fy = null;
  }
  hoverPinned = null;
}

// Camera moves GLIDE. fitView and centerOn used to write straight to the zoom behavior, and the
// load reveal fires three fits inside the first second and a half - three instant jumps read as
// the view being yanked about rather than as one camera moving. d3-transition would do this and
// is not a dependency here; the easing is short enough to keep.
let cameraTween = 0;

function applyTransform(t: ZoomTransform) {
  transform = t;
  // Drive the REAL zoom behavior (not a throwaway d3zoom()) so a later pan/zoom continues from
  // here instead of snapping back to a stale internal transform. sourceEvent is null on these,
  // which is how the zoom handler tells them from a gesture and leaves cameraOwnedByOperator be.
  if (zoomBehavior) select(canvas).call(zoomBehavior.transform, t);
}

// glideTo eases the camera to `to`. Scale interpolates GEOMETRICALLY - zoom is multiplicative,
// so a linear ramp between two scales races at one end and crawls at the other.
function glideTo(to: ZoomTransform, ms = 340) {
  if (cameraTween) {
    cancelAnimationFrame(cameraTween);
    cameraTween = 0;
  }
  if (!zoomBehavior) return;
  const from = transform;
  const still = Math.abs(from.k - to.k) < 1e-4 && Math.hypot(from.x - to.x, from.y - to.y) < 0.5;
  if (ms <= 0 || still || reducedMotion.matches) {
    applyTransform(to);
    return;
  }
  const started = performance.now();
  const ratio = to.k / from.k;
  const step = () => {
    const p = Math.min(1, (performance.now() - started) / ms);
    const e = 1 - (1 - p) ** 3; // ease-out cubic: fast to start, settles rather than stops
    applyTransform(
      zoomIdentity
        .translate(from.x + (to.x - from.x) * e, from.y + (to.y - from.y) * e)
        .scale(from.k * ratio ** e),
    );
    cameraTween = p < 1 ? requestAnimationFrame(step) : 0;
  };
  cameraTween = requestAnimationFrame(step);
}

// Frames at which the load-time reveal re-checks the framing of a FORCE layout. A cold force
// layout keeps spreading for seconds, so a single early fit frames a cloud that then grows
// straight back out of view - which is how a 2373-node graph came to land cropped to a corner.
// The first beat is quick feedback, the last one catches the settled extent. A DAG layout needs
// none of this: layoutLayered places every node in one pass, so its extent is final at once.
const REVEAL_BEATS_MS = [300, 750, 1400];

// revealWholeGraph frames the graph on load so it lands centered instead of cropped. Radial
// frames itself in applyRadialMode, and a projection is already its own subset.
function revealWholeGraph() {
  if (!projectionUnfolded || !graph?.nodes.length) return;
  // Only view/q/node name a SUBSET whose own framing must win. #data= and #src= say where the
  // graph came from, not what to look at, so treating them as directives left every
  // `magus graph open` link - and the whole targets flavor, which arrives that way - opening on
  // an unframed corner of its own layout.
  const p = hashParams();
  if (p.view || p.q || p.node) return;
  cameraOwnedByOperator = false;
  // The beats are measured from when the canvas HAS A SIZE, not from now. The console can mount
  // this surface into a pane that is still display:none, and it stays zero-width for as long as
  // that takes - measured at over three seconds, past every beat. Scheduling on the wall clock
  // meant all of them fired against a zero viewport, fitView refused each one, and the graph
  // kept whatever framing the cold layout happened to leave it with.
  void whenCanvasSized().then(() => {
    // One pass places every node in a DAG layout, so its extent is final immediately.
    if (isDagMode()) {
      if (!cameraOwnedByOperator) fitView(matchSet);
      return;
    }
    for (const at of REVEAL_BEATS_MS) {
      setTimeout(() => {
        // Re-check the mode too: a beat can land after the operator switched layout or ran a view.
        if (cameraOwnedByOperator || isDagMode() || activeView || focusId || query) return;
        fitView(null);
      }, at);
    }
  });
}

// whenCanvasSized resolves once the canvas has a non-zero box, or gives up after ~20s. Distinct
// from waitForCanvasWidth, whose ~1s cap suits a load the operator is already watching; this one
// waits out a surface mounted hidden, where there is nothing to be late for.
//
// A TIMER, not requestAnimationFrame: rAF stops entirely while the page is not being rendered -
// the same pause the idle wobble relies on - and the whole point here is to wait out a surface
// that is not being rendered yet, so an rAF poll deadlocks on exactly the case it exists for.
function whenCanvasSized(): Promise<void> {
  return new Promise((resolve) => {
    if (canvas.clientWidth > 0) {
      resolve();
      return;
    }
    let waited = 0;
    const timer = setInterval(() => {
      waited += 100;
      if (canvas.clientWidth > 0 || waited > 20_000) {
        clearInterval(timer);
        resolve();
      }
    }, 100);
  });
}

// A fit asked for while the canvas had no box, held until it has one. The console can mount this
// surface into a pane that is still display:none for seconds, and every fit in that window - a
// view's, a focus's, radial's - would otherwise be computed against a zero viewport, clamp to the
// minimum scale and be silently dropped. Only the LAST one is worth replaying: they supersede.
let pendingFit: { ids: Set<string> | null } | null = null;
let pendingFitArmed = false;

// worldBox is the bounding box of `ids` (or of every placed node when null), in world units.
// Null when nothing in the set has a position yet. In a card mode the box measures the drawn
// card rather than the dot radius, so a fit does not clip the labels it exists to make readable.
function worldBox(ids: Set<string> | null): WorldBox | null {
  const pts = graph.nodes.filter((n) => n.x != null && (!ids || ids.has(n.id)));
  if (!pts.length) return null;
  let minX = Infinity,
    minY = Infinity,
    maxX = -Infinity,
    maxY = -Infinity;
  const cards = cardsActive();
  for (const n of pts) {
    const hw = cards && n.w ? n.w / 2 : n.r;
    const hh = cards && n.h ? n.h / 2 : n.r;
    minX = Math.min(minX, n.x - hw);
    maxX = Math.max(maxX, n.x + hw);
    minY = Math.min(minY, n.y - hh);
    maxY = Math.max(maxY, n.y + hh);
  }
  return { minX, minY, maxX, maxY };
}

function fitView(ids: Set<string> | null) {
  const pts = graph.nodes.filter((n) => n.x != null && (!ids || ids.has(n.id)));
  if (!pts.length || !zoomBehavior) return; // setupZoomDrag has not run yet
  if (canvas.clientWidth <= 0) {
    pendingFit = { ids };
    if (!pendingFitArmed) {
      pendingFitArmed = true;
      void whenCanvasSized().then(() => {
        pendingFitArmed = false;
        const want = pendingFit;
        pendingFit = null;
        if (want && !cameraOwnedByOperator) fitView(want.ids);
      });
    }
    return;
  }
  let minX = Infinity,
    minY = Infinity,
    maxX = -Infinity,
    maxY = -Infinity;
  const cards = cardsActive();
  for (const n of pts) {
    const hw = cards && n.w ? n.w / 2 : n.r;
    const hh = cards && n.h ? n.h / 2 : n.r;
    minX = Math.min(minX, n.x - hw);
    maxX = Math.max(maxX, n.x + hw);
    minY = Math.min(minY, n.y - hh);
    maxY = Math.max(maxY, n.y + hh);
  }
  centeredOn = null; // framing a set supersedes holding one node in the middle
  const { w, h } = resizeCanvas();
  const t = fitTransform({ minX, minY, maxX, maxY }, w, h, stageInsets());
  glideTo(zoomIdentity.translate(t.x, t.y).scale(t.k));
  draw();
}

// frameArrangement frames a freshly applied arrangement. A plain fit answers "where is the
// set", and with a node selected that is not the question being asked: the operator is reading
// one node, and every arrangement moves it somewhere else, so a fit hands back a legible layout
// with the thing they were looking at lost inside it. Keep the fit's scale, land on the
// selection. With nothing selected an arrangement switch is itself a request to re-frame, so it
// supersedes a camera the operator had moved by hand.
function frameArrangement() {
  const n = selected ? graph.byId.get(selected) : null;
  if (n && n.x != null && zoomBehavior && canvas.clientWidth > 0) {
    const box = worldBox(matchSet);
    if (box) {
      const { w, h } = resizeCanvas();
      const insets = stageInsets();
      const t = recenterOn(fitTransform(box, w, h, insets), { x: n.x, y: n.y }, w, h, insets);
      cameraOwnedByOperator = true;
      centeredOn = selected;
      glideTo(zoomIdentity.translate(t.x, t.y).scale(t.k));
      draw();
      return;
    }
  }
  cameraOwnedByOperator = false;
  fitView(matchSet);
}

// focusNode builds a LOCAL graph around a node (Obsidian's local view): the node
// plus everything within `depth` hops become the match set, so the existing
// dim-non-matches / hide-outside-edges rendering isolates the neighborhood. It
// also selects the node (explain card) and fits the view.
function focusNode(id: string, depth: number) {
  const focusNodeObj = graph.byId.get(id);
  if (!focusNodeObj) return;
  // A local graph is its own emphasis, so it retires whatever view was driving the canvas.
  // Without this the chip stays lit, the result line keeps reporting the old view's answer and
  // the command bar keeps offering its CLI idiom, while the canvas shows this neighborhood -
  // and a canvas dblclick fires a plain click first, so it is one gesture.
  if (activeView) clearView();
  focusId = id;
  focusDepth = depth;
  matchSet = neighborhood(id, depth);
  query = "";
  searchEl.value = "";
  selected = id;
  renderCard(id);
  setListExpanded(true);
  renderList();
  syncListSelection();
  setStatus(
    "Local graph around " +
      focusNodeObj.label +
      ", " +
      matchSet.size +
      " nodes within " +
      depth +
      " hop" +
      (depth === 1 ? "" : "s") +
      ". Press Esc to clear, [ / ] to change depth.",
  );
  // Re-run the dag layout (layered or waves) on the new (local) subset.
  if (isDagMode()) {
    for (const e of graph.links) {
      delete e.layoutReversed;
      delete e.points;
    }
    reapplyDagLayout();
  }
  updateHash();
  fitView(matchSet);
}

function changeFocusDepth(delta: number) {
  if (!focusId) return;
  focusNode(focusId, Math.max(1, Math.min(5, focusDepth + delta)));
}

function clearFocusOrQuery() {
  focusId = null;
  matchSet = null;
  query = "";
  pendingRadialPick = false;
  if (searchEl) searchEl.value = "";
  setStatus("");
  setResultLine(null);
  // Clear any active view.
  if (activeView) {
    activeView = null;
    viewNode = null;
    viewNodeTo = null;
    document
      .querySelectorAll<HTMLElement>(".console-graph-views__chip")
      .forEach((b) => b.removeAttribute("data-active"));
    renderViewCommand(null, null, null);
    setFlowEdges(null);
    flowOn = false;
  }
  renderList();
  updateHash();
  if (layoutMode === "radial") {
    // Radial without a center is meaningless; return to the flavor default.
    switchLayout(graphFlavor === "targets" ? "layered" : "force");
    return;
  }
  if (isDagMode()) {
    for (const e of graph.links) {
      delete e.layoutReversed;
      delete e.points;
    }
    reapplyDagLayout();
  } else {
    draw();
  }
}

// syncKindList fills the search-syntax reference with the kinds THIS graph carries, and hides
// the kind:symbol example unless it has symbol nodes to find. The list used to be a hardcoded
// enumeration in the scaffold, which drifted from both the schema and the payload; a search the
// help advertises must be one the loaded data can answer.
function syncKindList(counts: Map<string, number>) {
  const kinds = legendKinds(counts);
  if (kinds.length) {
    // An empty graph leaves the scaffold's "the kinds in the legend" wording standing rather
    // than blanking the sentence mid-clause.
    for (const slot of document.querySelectorAll<HTMLElement>("[data-kindlist]")) {
      slot.textContent = kinds.join(", ");
    }
  }
  const noSymbols = !counts.has("symbol");
  for (const ex of document.querySelectorAll<HTMLElement>('[data-q="kind:symbol"]')) {
    const row = ex.closest("dt");
    ex.toggleAttribute("data-conditional", noSymbols);
    row?.toggleAttribute("data-conditional", noSymbols);
    const dd = row?.nextElementSibling;
    if (dd instanceof HTMLElement) dd.toggleAttribute("data-conditional", noSymbols);
  }
}

// syncConditionalViews shows or hides the "What's slow?" (critical) view button
// based on whether the current graph has DurationMs timing data. Called after
// each graph load (boot and replaceGraph) so the button tracks the data.
function syncConditionalViews() {
  graphHasDurations = !!graph && graph.nodes.some((n) => nodeDurationMs(n) > 0);
  document.querySelectorAll<HTMLElement>("[data-view='critical']").forEach((btn) => {
    btn.toggleAttribute("data-conditional", !graphHasDurations);
  });
  // The "Colour by duration" preset shares the same conditional as the critical-path view: both
  // need timing data to mean anything. The attribute rides the toggle-group ITEM, not the button
  // inside it: hiding only the button leaves an empty cell holding the group's :last-child end
  // cap, so the row renders squared off mid-air with the rounding on a cell nobody can see.
  document.querySelectorAll<HTMLElement>("[data-preset-item='duration']").forEach((item) => {
    item.toggleAttribute("data-conditional", !graphHasDurations);
  });
  syncAffectedView();
}

// syncAffectedView shows the affected chip exactly while an affected set is available, and
// retires the view if one goes away underneath it. No status producer populates
// StatusOutput.Affected yet (see fetchLiveStatus), so today it never shows - hidden rather
// than visible-and-disabled, which would be a control the user can see and never reach.
// Called separately from syncConditionalViews because the live SSE path can change the set
// without reloading the graph.
function syncAffectedView() {
  const has = !!window._liveAffectedIds?.size;
  document.querySelectorAll<HTMLElement>("[data-view='affected']").forEach((btn) => {
    btn.toggleAttribute("data-conditional", !has);
  });
  if (!has && activeView === "affected") clearView();
}

// ---- color groups ----------------------------------------------------------
// Each group paints every node matching a query one chosen color, ON TOP of the
// kind palette - so several groups can coexist (unlike the single match set). The
// groups reuse the same query grammar (parseQuery/termMatches) as the filter box.
// A parsed query term: an optional field filter, the lowercased value, and whether a
// leading `-` negated it.
interface QueryTerm {
  field: string | null;
  value: string;
  negated: boolean;
}

// A color group paints every node its query (or nodeSet) matches one color, layered
// over the kind palette. nodeSet is set by presets that match ids directly.
interface ColorGroup {
  query: string;
  color: string;
  terms: QueryTerm[];
  nodeSet?: Set<string>;
}

const groups: ColorGroup[] = []; // { query, color, terms }

function groupColorFor(node: GNode) {
  for (const g of groups) {
    // Groups with a nodeSet (e.g. depth preset) match directly by id, bypassing
    // the query grammar so a fake `layer:N` string doesn't silently match nothing.
    if (g.nodeSet) {
      if (g.nodeSet.has(node.id)) return g.color;
    } else if (g.terms.length && g.terms.every((t) => termMatches(node, t))) {
      return g.color;
    }
  }
  return null;
}

// ---- query grammar (the browser twin of `magus query`) ---------------------
// The SAME fielded grammar the CLI speaks: space-separated terms are ANDed;
// field filters kind:/project:/relation:/id:/symbol:; free text matches
// id/label/doc; "quoted" spans stay one term; a leading - negates. So a query
// typed here (or arriving in #q=) selects the same nodes `magus query` would.
//
// Fidelity contract: every field accepted here is also accepted by the
// real `magus query` CLI. The data-q example buttons in graph.html double as a
// drift fixture (cmd/magus/testdata/script/query_syntax.txtar). Verified fields:
// kind, project, relation, id, symbol (KindSymbol prefix for SCIP symbol nodes).
const QUERY_FIELDS = ["kind", "project", "relation", "id", "symbol"];

function parseQuery(str: string) {
  const terms: QueryTerm[] = [];
  let i = 0;
  while (i < str.length) {
    while (i < str.length && /\s/.test(str[i])) i++;
    if (i >= str.length) break;
    let negated = false;
    if (str[i] === "-") {
      negated = true;
      i++;
    }
    let field: string | null = null;
    const fm = /^([a-zA-Z]+):/.exec(str.slice(i));
    if (fm && QUERY_FIELDS.includes(fm[1].toLowerCase())) {
      field = fm[1].toLowerCase();
      i += fm[0].length;
    }
    let value;
    if (str[i] === '"') {
      const end = str.indexOf('"', i + 1);
      value = end < 0 ? str.slice(i + 1) : str.slice(i + 1, end);
      i = end < 0 ? str.length : end + 1;
    } else {
      let j = i;
      while (j < str.length && !/\s/.test(str[j])) j++;
      value = str.slice(i, j);
      i = j;
    }
    if (value !== "") terms.push({ field, value: value.toLowerCase(), negated });
  }
  return terms;
}

// relIndex: node id -> Set of relations of edges touching it (for relation:).
function relationIndex() {
  const idx = new Map<string, Set<string>>();
  const add = (id: string, rel: string) => {
    let s = idx.get(id);
    if (!s) {
      s = new Set<string>();
      idx.set(id, s);
    }
    s.add(rel);
  };
  for (const e of graph.links) {
    add(endpointId(e.source), e.relation);
    add(endpointId(e.target), e.relation);
  }
  return idx;
}

function termMatches(node: GNode, term: QueryTerm) {
  const v = term.value;
  let hit;
  switch (term.field) {
    case "kind":
      hit = node.kind === v;
      break;
    case "project":
      // Knowledge-graph ids: project nodes are "project:<name>", target nodes
      // are "target:<project>:<name>". Target-graph ids: project nodes are the
      // raw path (e.g. "."), target/spell nodes carry attrs.project = path.
      hit =
        node.id === "project:" + v ||
        (node.kind === "target" && node.id.toLowerCase().startsWith("target:" + v + ":")) ||
        (node.attrs && (node.attrs.project || "").toLowerCase() === v) ||
        node.id.toLowerCase() === v ||
        // Everything the project CONTAINS, transitively - its dirs, docs, files and the
        // functions inside them. The id comparisons above only ever reach the project node
        // and its targets.
        (projectOwners().get(node.id) ?? "").toLowerCase() === "project:" + v;
      break;
    case "relation": {
      // relIndex is always built by the callers that run relation queries; treat an
      // absent index as no match rather than asserting it non-null.
      const rel = graph.relIndex;
      hit = !!rel && (rel.get(node.id)?.has(v) ?? false);
      break;
    }
    case "id":
      hit = node.id.toLowerCase().includes(v);
      break;
    // symbol: prefix targets SCIP code-symbol nodes by their symbol: id prefix.
    // The CLI treats `symbol:` as free text (no typed field); the box accepts a superset
    // syntactically (restricts to kind=symbol + id substring), but the CLI accepts the query.
    case "symbol":
      hit = node.kind === "symbol" && node.id.toLowerCase().includes("symbol:" + v);
      break;
    default:
      hit =
        node.id.toLowerCase().includes(v) ||
        node.label.toLowerCase().includes(v) ||
        (node.doc && node.doc.toLowerCase().includes(v));
  }
  return term.negated ? !hit : hit;
}

// serverScores is the rank the daemon gave each id for the CURRENT query, when a daemon
// answered. Null offline, before the first answer, and whenever the query moves on - so the
// list falls back to degree rather than ranking by a previous question's scores.
let serverScores: Map<string, number> | null = null;

// queryGeneration invalidates an in-flight refinement. Bumped by every applyQuery, including
// the offline ones, so a slow answer can never land on top of a newer query.
let queryGeneration = 0;

// refineQueryFromServer replaces the locally-computed match set with the daemon's, which is
// the actual magus query grammar rather than the subset reimplemented below.
//
// The local pass still runs FIRST and is what the operator sees while typing: a round trip per
// keystroke would make the box feel broken, and the local answer is a good approximation of
// the server's. This supersedes it when it lands.
//
// Only in LIVE mode, and only for the knowledge flavor. A snapshot (#data=/#src=) or the demo
// is not the daemon's graph even when a daemon happens to be running, and GraphService answers
// about the knowledge graph, not the target graph - refining either against it would filter
// the canvas by a query run over a different graph entirely.
// graphClient is the typed GraphService client, or null when there is nothing to ask. Every verb
// below is live-only and knowledge-only: a snapshot or the demo is not the daemon's graph even
// when a daemon happens to be running, and GraphService answers about the knowledge graph rather
// than the target graph.
function graphClient() {
  if (!liveHost || !liveToken || graphFlavor === "targets") return null;
  return createClient(GraphService, createDaemonTransport(liveHost, liveToken));
}

// refineBlastFromServer corrects the blast COUNT against the whole workspace graph.
//
// It cannot replace the local set: ExplainNode returns a radius, not the reachable ids, and the
// canvas needs ids to light up. What it can do is say when the local number is an UNDERCOUNT -
// the browser walks the payload it was sent, which excludes symbol shards and anything a
// projection dropped, so "42 dependents" can be a confident answer about a smaller graph than the
// one the operator is asking about.
async function refineBlastFromServer(nodeId: string, localCount: number, gen: number) {
  const client = graphClient();
  if (!client) return;
  try {
    const res = await client.explainNode({ name: nodeId });
    if (gen !== viewGeneration) return;
    const real = res.blastRadius;
    if (real > localCount) {
      setStatus(
        "The workspace graph reaches " +
          real +
          " - this view shows the " +
          localCount +
          " that are loaded here.",
      );
    }
  } catch {
    // No daemon answer leaves the local count standing, which is the offline behaviour.
  }
}

// refineTraceFromServer replaces the traced path with the daemon's.
//
// The local walk follows depends_on only, over the loaded payload; the daemon walks every
// relation over the whole graph, so it finds chains the browser cannot and reports the relation
// per hop. The server's answer is only USABLE here when every node on it is loaded - the canvas
// cannot light up what it was never sent - so a path through an absent node is reported rather
// than drawn.
async function refineTraceFromServer(from: string, to: string, gen: number) {
  const client = graphClient();
  if (!client) return;
  try {
    const res = await client.findPath({ from, to });
    if (gen !== viewGeneration || activeView !== "trace") return;
    if (!res.found) return; // the local walk already said so, in the operator's own words
    const ids = [res.from, ...res.steps.map((s) => s.to)];
    const missing = ids.filter((id) => !graph.byId.has(id));
    if (missing.length) {
      setStatus(
        "The workspace has a " +
          res.steps.length +
          "-step path, through " +
          missing.length +
          " node(s) this graph does not carry. Showing the local one.",
      );
      return;
    }
    matchSet = new Set(ids);
    setStatus(
      "Path (" +
        res.steps.length +
        " steps): " +
        res.steps
          .map((s) => s.relation + " -> " + (graph.byId.get(s.to)?.label ?? s.to))
          .join(", "),
    );
    renderList();
    draw();
  } catch {
    // Local answer stands.
  }
}

// viewGeneration invalidates an in-flight view refinement, the same guard the query box uses: a
// slow answer must never land on a view the operator has since replaced.
let viewGeneration = 0;

// suggestNodes fills the query box's completion list from the daemon's ranked candidates. It runs
// on the SAME debounce as the query itself and shares its generation, so a stale answer cannot
// repopulate the list under a newer prefix.
//
// A fielded term (kind:, project:, relation:) is left alone: ResolveNodes ranks NODE references,
// so completing "kind:spe" against node ids would offer nonsense.
async function suggestNodes(prefix: string, gen: number) {
  const list = el("node-suggestions");
  if (!list) return;
  const client = graphClient();
  if (!client || prefix.length < 2 || prefix.includes(":")) {
    list.innerHTML = "";
    return;
  }
  try {
    const res = await client.resolveNodes({ reference: prefix, limit: 8 });
    if (gen !== queryGeneration) return;
    list.innerHTML = res.matches
      .map((m) => '<option value="' + escapeHtml(m.id) + '">' + escapeHtml(m.label) + "</option>")
      .join("");
  } catch {
    list.innerHTML = "";
  }
}

async function refineQueryFromServer(q: string, gen: number) {
  if (!liveHost || !liveToken || graphFlavor === "targets") return;
  try {
    const client = createClient(GraphService, createDaemonTransport(liveHost, liveToken));
    const res = await client.queryNodes({ query: q });
    if (gen !== queryGeneration) return; // a newer query won the race
    // The daemon searches the WHOLE graph; the canvas holds what it was sent. Symbols in
    // particular are excluded from the browser's payload, so a symbol query can match on the
    // server and have nothing to light up here. Intersect, then say what was left out - a
    // silently smaller match count is the same lie the old local-only filter told.
    const present = res.matches.filter((m) => graph.byId.has(m.id));
    matchSet = new Set(present.map((m) => m.id));
    serverScores = new Map(present.map((m) => [m.id, m.score]));
    const offCanvas = res.matches.length - present.length;
    const verdict = res.answer?.verdict ?? "";
    const notes: string[] = [];
    if (offCanvas > 0) notes.push(offCanvas + " more matched but are not in this graph");
    if (verdict === "unknown") notes.push("coverage unknown (" + (res.answer?.reason ?? "") + ")");
    if (notes.length)
      setStatus(matchSet.size + " shown; " + notes.join("; "), verdict === "unknown");
    setListExpanded(true);
    renderList();
    syncLayoutToggle();
    draw();
  } catch {
    // A refinement that cannot reach the daemon leaves the local answer standing, which is
    // the offline behavior and already on screen. Nothing to report.
  }
}

// matchSetFor resolves a query string to the set of ids it matches, or null when the query
// is empty. null means NO FILTER, which is not the same as a filter that matched nothing.
//
// This is a REIMPLEMENTATION of the magus query grammar, and a partial one. It stays because
// the explorer runs with no daemon on every static path - the demo, a #data= snapshot, the
// docs site - where there is nothing to ask. refineQueryFromServer supersedes it in live mode.
function matchSetFor(q: string): Set<string> | null {
  const terms = q ? parseQuery(q) : [];
  if (!terms.length) return null;
  if (!graph.relIndex) graph.relIndex = relationIndex();
  const out = new Set<string>();
  for (const n of graph.nodes) {
    if (terms.every((t) => termMatches(n, t))) out.add(n.id);
  }
  return out;
}

function applyQuery(q: string) {
  focusId = null; // typing a query exits focus/lens/view mode
  pendingRadialPick = false;
  // Typing a query unfolds the projection (user is exploring details).
  if (!projectionUnfolded && q.trim()) {
    projectionUnfolded = true;
    projectionSet = null;
    const btn = el("projection-unfold-btn");
    if (btn) btn.hidden = true;
  }
  // Clear active view when query is typed.
  if (activeView) {
    activeView = null;
    viewNode = null;
    viewNodeTo = null;
    document
      .querySelectorAll<HTMLElement>(".console-graph-views__chip")
      .forEach((b) => b.removeAttribute("data-active"));
    renderViewCommand(null, null, null);
    setFlowEdges(null);
    flowOn = false;
  }
  query = q.trim();
  matchSet = matchSetFor(query);
  serverScores = null;
  const gen = ++queryGeneration;
  if (query) void refineQueryFromServer(query, gen);
  void suggestNodes(query, gen);
  if (matchSet) setListExpanded(true); // a query reveals its matches
  renderList();
  updateHash();
  syncLayoutToggle(); // availability tracks matchSet size
  refreshOverview();
  // Radial owns placement: it pins its ego rings and parks everything else off-canvas. Left
  // alone it would report matches sitting a million units off the canvas as visible, so the
  // filter narrows to the matches radial actually PLACED. Re-placing over the whole graph
  // first (matchSet cleared) rather than over the matches keeps the rings meaningful - the
  // paths between matches run through nodes the filter excludes, and a BFS restricted to the
  // matches alone would reach almost none of them.
  if (layoutMode === "radial") {
    const matches = matchSet;
    matchSet = null;
    applyRadialMode(); // re-places over the full graph; leaves matchSet as the placed set
    const placed = matchSet as Set<string> | null;
    if (matches && placed) {
      matchSet = new Set([...matches].filter((id) => placed.has(id)));
      const unreachable = matches.size - matchSet.size;
      const centerNode = radialCenter ? graph.byId.get(radialCenter) : null;
      setStatus(
        matchSet.size +
          " match" +
          (matchSet.size === 1 ? "" : "es") +
          " within " +
          RADIAL_MAX_RINGS +
          " hops of " +
          (centerNode ? centerNode.label : radialCenter) +
          (unreachable ? "; " + unreachable + " further out" : ""),
      );
      renderList();
      fitView(matchSet);
    }
    return;
  }
  // Re-run the dag layout (layered or waves) on the new visible subset.
  if (isDagMode()) {
    // Clear prior layout-reversed flags so cycle-break reruns cleanly.
    for (const e of graph.links) {
      delete e.layoutReversed;
      delete e.points;
    }
    if (!reapplyDagLayout()) {
      // Scale guard: too many nodes; fall back to force.
      layoutMode = "force";
      wavesMeta = null;
      syncLayoutToggle();
      unpinAllNodes();
      if (sim) sim?.alpha(0.3).restart();
    }
    return;
  }
  draw();
}

// The node cloud is collapsed by default (canvas-first on load); a query, or the
// count toggle, reveals it.
let listExpanded = false;
function setListExpanded(v: boolean) {
  const changed = listExpanded !== v;
  listExpanded = v;
  listEl.hidden = !v;
  const btn = el("list-toggle");
  if (btn) btn.setAttribute("aria-expanded", v ? "true" : "false");
  // The toggle's own label reads "Show/Hide the N matching nodes" under a scope, so it has to be
  // repainted when the disclosure flips. Guarded against the re-entry renderList would cause,
  // since renderList itself calls setListExpanded on a fresh query.
  if (changed && graph) renderList();
}

// Cached per graph like adjacency(): the hubs ranking reads it once per node.
function depDegrees() {
  if (!graph.depDegrees) graph.depDegrees = dependencyDegrees(graph.nodes, graph.links);
  return graph.depDegrees;
}

// rowMetric is the number the ACTIVE VIEW ranked by, when it ranked by one. A list that is
// ordered but never says by what asks the reader to take the order on trust; worse, the fallback
// order is raw degree, which is not what either of these views sorted on.
function rowMetric(): { sort: (n: GNode) => number; text: (n: GNode) => string } | null {
  if (activeView === "hubs") {
    const deg = depDegrees();
    return {
      sort: (n) => deg.get(n.id)?.dependents ?? 0,
      text: (n) => {
        const d = deg.get(n.id)?.dependents ?? 0;
        return d + (d === 1 ? " dependent" : " dependents");
      },
    };
  }
  if (activeView === "critical" && graphHasDurations) {
    return { sort: (n) => nodeDurationMs(n), text: (n) => formatDuration(nodeDurationMs(n)) };
  }
  return null;
}

// The node list is the accessible twin of the canvas: it always reflects the
// current query (or the highest-degree nodes when there is no query).
function renderList() {
  const ms = matchSet;
  const pool = ms ? graph.nodes.filter((n) => ms.has(n.id)) : graph.nodes.slice();
  const metric = rowMetric();
  const scores = serverScores;
  if (metric) {
    pool.sort((a, b) => metric.sort(b) - metric.sort(a) || a.label.localeCompare(b.label));
  } else if (scores) {
    // The daemon's own relevance ranking, which is what `magus query` orders by. Degree below
    // is the offline stand-in: it ranks the best-connected node first whether or not it has
    // anything to do with what was typed.
    pool.sort(
      (a, b) => (scores.get(b.id) ?? 0) - (scores.get(a.id) ?? 0) || a.label.localeCompare(b.label),
    );
  } else {
    pool.sort((a, b) => b.degree - a.degree || a.label.localeCompare(b.label));
  }
  const shown = pool.slice(0, 300);
  // The scope bar carries "N of TOTAL" whenever anything is applied, so this row drops the number
  // then and states its own job instead - two counts within a few hundred pixels read as a
  // disagreement waiting to happen.
  countEl.textContent = matchSet
    ? (listExpanded ? "Hide" : "Show") +
      " the " +
      matchSet.size +
      " matching node" +
      (matchSet.size === 1 ? "" : "s")
    : graph.nodes.length +
      " node" +
      (graph.nodes.length === 1 ? "" : "s") +
      ", " +
      graph.links.length +
      " edge" +
      (graph.links.length === 1 ? "" : "s");
  // An empty pool still needs a row: a query reveals this panel, and on a phone the panel is a
  // fixed overlay carrying its own border, fill and shadow, so no rows reads as a stray white
  // card floating over the canvas rather than as "nothing matched".
  if (!pool.length) {
    listEl.innerHTML =
      '<li class="console-graph-nodelist__empty">' +
      (matchSet ? "No matches. Widen the search, or clear it." : "Nothing to list.") +
      "</li>";
    return;
  }
  // Compact rows: a kind-colored dot (keyed to the legend) + the label. The kind
  // name lives in the title tooltip rather than a column, to keep rows dense.
  listEl.innerHTML = shown
    .map(
      (n) =>
        '<li><button type="button" class="console-graph-nodelist__pill" data-id="' +
        escapeHtml(n.id) +
        '"' +
        ' title="' +
        escapeHtml(n.kind + " - " + n.label) +
        '"' +
        (n.id === selected ? ' aria-current="true"' : "") +
        ">" +
        '<span class="console-graph-kinddot" data-kind="' +
        escapeHtml(n.kind) +
        '"></span>' +
        '<span class="console-graph-nodelist__label">' +
        escapeHtml(n.label) +
        "</span>" +
        (metric
          ? '<span class="console-graph-nodelist__metric">' + escapeHtml(metric.text(n)) + "</span>"
          : "") +
        "</button></li>",
    )
    .join("");
  if (pool.length > shown.length) {
    listEl.innerHTML +=
      '<li class="console-graph-nodelist__more">+' +
      (pool.length - shown.length) +
      " more (refine the search)</li>";
  }
  listEl.querySelectorAll<HTMLElement>(".console-graph-nodelist__pill").forEach((b) => {
    b.addEventListener("click", () => selectNode(b.dataset.id ?? null, true));
    b.addEventListener("dblclick", () => {
      const id = b.dataset.id;
      if (id) focusNode(id, focusDepth);
    });
  });
}

function syncListSelection() {
  listEl.querySelectorAll<HTMLElement>(".console-graph-nodelist__pill").forEach((b) => {
    if (b.dataset.id === selected) b.setAttribute("aria-current", "true");
    else b.removeAttribute("aria-current");
  });
}

// legendKinds lists the kinds actually present, KINDS order first and anything else after it,
// alphabetically. Filtering to KINDS alone silently dropped every kind the graph carries that
// this list predates - `dir`, `package`, `tool` and `note` in magus's own graph - so the legend
// claimed to enumerate node kinds while its counts did not add up to the node total. An unknown
// kind has no --gk-<kind>, and both the dot and the canvas already fall back to grey.
function legendKinds(counts: Map<string, number>): string[] {
  const known = KINDS.filter((k) => counts.has(k));
  const rest = [...counts.keys()].filter((k) => !KINDS.includes(k)).sort();
  return [...known, ...rest];
}

// legendTitleEl is the legend's heading. It names what the SWATCHES mean, which stops being
// "node kinds" the moment a colour preset repaints the canvas by something else.
function setLegendTitle(text: string) {
  const t = document.querySelector<HTMLElement>(".console-graph-legend__title");
  if (t) t.textContent = text;
  if (legendEl) legendEl.setAttribute("aria-label", text);
}

// renderGroupLegend lists the ACTIVE colour grouping - one row per project, spell or depth band
// with the hue it was given. A colour preset repaints every node, and the kind legend beside it
// then describes a palette that is no longer on the canvas: the one panel whose whole job is to
// say what the colours mean is the one saying something false. Rows are not clickable here, and
// deliberately: a kind row filters to kind:<k>, but these groups already ARE the whole graph
// partitioned, so "filter to this group" is the query box's job, not a legend click.
function renderGroupLegend(preset: string) {
  const counts = new Array<number>(groups.length).fill(0);
  for (const n of graph.nodes) {
    for (let i = 0; i < groups.length; i++) {
      const g = groups[i];
      const hit = g.nodeSet
        ? g.nodeSet.has(n.id)
        : g.terms.length > 0 && g.terms.every((t) => termMatches(n, t));
      if (hit) {
        counts[i]++;
        break; // first match wins, exactly as groupColorFor resolves it
      }
    }
  }
  setLegendTitle(GROUP_LEGEND_TITLES[preset] ?? "Colour groups");
  legendEl.innerHTML = groups
    .map((g, i) =>
      counts[i] === 0
        ? ""
        : '<li><span class="console-graph-legend__row" role="presentation">' +
          '<span class="console-graph-kinddot" style="background:' +
          escapeHtml(g.color) +
          '"></span>' +
          escapeHtml(groupLabel(g.query)) +
          ' <span class="console-graph-legend__count">' +
          counts[i] +
          "</span></span></li>",
    )
    .join("");
}

// The legend heading for each preset: what one swatch MEANS, in the reader's terms.
const GROUP_LEGEND_TITLES: Record<string, string> = {
  project: "Projects",
  spell: "Spells (toolchain)",
  depth: "Dependency depth",
  duration: "Run duration",
};

// groupLabel strips the field prefix a group's query carries ("project:console" -> "console"),
// because the heading above already says which field this is. `layer:N` reads as "depth N": the
// number is a position in the dependency chain, and the bare integer says nothing.
function groupLabel(q: string): string {
  const i = q.indexOf(":");
  if (i < 0) return q;
  const field = q.slice(0, i);
  const value = q.slice(i + 1);
  return field === "layer" ? "depth " + value : value;
}

function renderLegend() {
  const counts = new Map<string, number>();
  for (const n of graph.nodes) counts.set(n.kind, (counts.get(n.kind) || 0) + 1);
  syncKindList(counts);
  if (activePreset && groups.length) {
    renderGroupLegend(activePreset);
    return;
  }
  setLegendTitle("Node kinds");
  // Each legend row is a button that filters to kind:<k> (the CLI query it maps to),
  // so clicking a color isolates that kind - a quick, Obsidian-style filter.
  legendEl.innerHTML = legendKinds(counts)
    .map(
      (k) =>
        '<li><button type="button" class="console-graph-legend__row" data-kind="' +
        escapeHtml(k) +
        '" title="Filter to kind:' +
        escapeHtml(k) +
        '">' +
        '<span class="console-graph-kinddot" data-kind="' +
        escapeHtml(k) +
        '"></span>' +
        escapeHtml(k) +
        ' <span class="console-graph-legend__count">' +
        counts.get(k) +
        "</span></button></li>",
    )
    .join("");
  legendEl.querySelectorAll<HTMLElement>(".console-graph-legend__row").forEach((b) =>
    b.addEventListener("click", () => {
      const q = "kind:" + b.dataset.kind;
      // Toggle: clicking the active kind filter clears it.
      const next = query === q ? "" : q;
      searchEl.value = next;
      applyQuery(next);
    }),
  );
}

// ---- scope bar ---------------------------------------------------------------
// One row naming every emphasis in effect. A query, a view, a local-graph focus, the default
// projection and a colour preset can all be applied at once; before this the operator inferred
// that from five widgets that never referred to each other.
//
// It RENDERS the live state rather than owning it - each pill delegates to the same clear path
// the rest of the code uses. Making it the single source of truth is the separate refactor that
// wants the graph contract first.
interface ScopePill {
  label: string;
  title: string;
  clear: () => void;
}

// termText renders a parsed term back to the grammar. parseQuery lowercases values and drops the
// original spans, so this is a normalized form, not the operator's literal keystrokes - which is
// also what makes a pill removable: the remaining terms re-serialize into a valid query.
function termText(t: QueryTerm): string {
  const value = /\s/.test(t.value) ? '"' + t.value + '"' : t.value;
  return (t.negated ? "-" : "") + (t.field ? t.field + ":" : "") + value;
}

function labelFor(id: string): string {
  return graph?.byId.get(id)?.label ?? id;
}

function scopePills(): ScopePill[] {
  const pills: ScopePill[] = [];
  const terms = query ? parseQuery(query) : [];
  terms.forEach((t, i) => {
    pills.push({
      label: termText(t),
      title: "Remove this term",
      clear: () => {
        const rest = terms
          .filter((_, j) => j !== i)
          .map(termText)
          .join(" ");
        searchEl.value = rest;
        applyQuery(rest);
      },
    });
  });
  if (activeView) {
    const subject = viewNodeTo
      ? labelFor(viewNode ?? "") + " to " + labelFor(viewNodeTo)
      : viewNode
        ? labelFor(viewNode)
        : "";
    pills.push({
      label: subject ? activeView + ": " + subject : activeView,
      title: "Clear this view",
      clear: clearView,
    });
  }
  if (focusId) {
    pills.push({
      label:
        "around " + labelFor(focusId) + ", " + focusDepth + (focusDepth === 1 ? " hop" : " hops"),
      title: "Leave the local graph",
      clear: clearFocus,
    });
  }
  if (!projectionUnfolded && projectionSet) {
    pills.push({
      label: "projects only",
      title: "Show the full graph",
      clear: unfoldProjection,
    });
  }
  if (activePreset) {
    const preset = activePreset;
    pills.push({
      // The preset labels read "Color by project"; the pill already says colour, so drop the prefix.
      label:
        "colour: " +
        (COLOR_PRESETS.find((p) => p.id === preset)?.label ?? preset).replace(/^Color by /, ""),
      title: "Clear the colour preset",
      clear: () => applyPreset(preset),
    });
  }
  return pills;
}

// clearFocus leaves the local graph WITHOUT taking the query with it - clearFocusOrQuery clears
// both, which is right for Esc and wrong for a pill that names only the focus.
function clearFocus() {
  focusId = null;
  matchSet = matchSetFor(query);
  setStatus("");
  renderList();
  updateHash();
  if (isDagMode()) reapplyDagLayout();
  draw();
}

// renderScope repaints the bar. `previewCount` is the count for a query the operator is still
// typing, which has not been applied yet - shown immediately so the field answers every
// keystroke instead of waiting out the debounce.
function renderScope(previewCount?: number) {
  const bar = el("scope-bar");
  const pillsEl = el("scope-pills");
  const countEl = el("scope-count");
  if (!bar || !pillsEl || !countEl) return;
  const pills = graph ? scopePills() : [];
  const typing = previewCount !== undefined;
  if (!pills.length && !typing) {
    bar.hidden = true;
    return;
  }
  bar.hidden = false;
  pillsEl.replaceChildren();
  for (const p of pills) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "console-graph-scope__pill";
    b.textContent = p.label;
    b.title = p.title;
    b.addEventListener("click", p.clear);
    pillsEl.append(b);
  }
  const total = graph?.nodes.length ?? 0;
  const shown = typing ? previewCount : (matchSet?.size ?? total);
  countEl.textContent =
    shown + " of " + total + (typing ? " would match" : shown === total ? " shown" : " shown");
}

// Reflect selection, query, layout mode, active view, and color preset in the
// hash WITHOUT clobbering a #data= fragment (round-tripping the whole graph
// through history on every click would break the private-data contract).
let suppressHash = false;

// Fragment keys that name the GRAPH rather than the view: updateHash copies these through
// rather than rewriting them. `data` is absent on purpose - it bails out of updateHash entirely.
const SOURCE_HASH_KEYS = ["src", "port", "demo", "flavor"];

function updateHash() {
  renderScope(); // called wherever the applied state changes, which is what the bar reflects
  if (suppressHash) return;
  const params = hashParams();
  // #data= carries the whole gzipped graph. Rewriting a six-figure fragment through
  // history.replaceState on every hover and click is not worth the shareability, so that one
  // transport keeps its link untouched and its view state stays local.
  if (params.data) return;
  // The fragment carries two kinds of key and updateHash owns only one of them. SOURCE keys
  // say which graph is loaded (where it came from, which flavor, whether this is the demo);
  // they belong to whoever wrote the link and are copied through untouched. VIEW keys say what
  // is being looked at, and are rewritten from live state below. Dropping the source keys is
  // what left a `magus graph export --open --serve` session unable to share the view it was
  // showing, and a reload of the demo landing on the empty state.
  const parts = [];
  for (const key of SOURCE_HASH_KEYS) {
    const v = params[key];
    if (v === undefined) continue;
    parts.push(v === "" ? key : key + "=" + encodeURIComponent(v));
  }
  if (activeView) {
    parts.push("view=" + encodeURIComponent(activeView));
    if (viewNode) parts.push("node=" + encodeURIComponent(viewNode));
    if (viewNodeTo) parts.push("to=" + encodeURIComponent(viewNodeTo));
  } else {
    if (query) parts.push("q=" + encodeURIComponent(query));
    if (selected) parts.push("node=" + encodeURIComponent(selected));
  }
  // Only serialize the layout key when it differs from the flavor default, so
  // clean URLs stay clean. (targets -> layered default; knowledge -> force default).
  const defaultLayout = graphFlavor === "targets" ? "layered" : "force";
  if (layoutMode !== defaultLayout) parts.push("layout=" + layoutMode);
  if (activePreset) parts.push("preset=" + encodeURIComponent(activePreset));
  const next = parts.length ? "#" + parts.join("&") : "#";
  if (location.hash !== next) history.replaceState(null, "", next);
}

// applyDeepLinks restores everything updateHash serializes. The two must stay symmetric: a
// key written but not read back makes a shared link a lie about what the sender was looking at.
function applyDeepLinks() {
  const params = hashParams();
  // Restore view state: #view=<id>&node=<id>[&to=<id>]
  const validViews = ["blast", "trace", "critical", "hubs", "orphans", "cycles"];
  const view = params.view && validViews.includes(params.view) ? params.view : null;
  if (view) {
    projectionUnfolded = true; // views show the full graph
    activateView(view, params.node || null, params.to || null);
  } else {
    // q/node only apply when no view claimed them: a view owns #node= as its subject.
    if (params.q) {
      searchEl.value = params.q;
      applyQuery(params.q);
    }
    if (params.node && graph.byId.has(params.node)) selectNode(params.node, true);
  }
  // Restore layout mode from the fragment (#layout=force|layered|waves|radial).
  // Only switch when the value is valid and differs from the current mode.
  // radial additionally requires a #node= that resolved above (selectNode sets
  // `selected`); without one it falls back to whatever layout is already
  // showing (the flavor default) with a status note - radial with no center is
  // meaningless.
  if (params.layout && isLayoutMode(params.layout)) {
    if (params.layout === "radial" && !selected) {
      setStatus("radial needs a #node= to center on; showing the default layout instead.");
    } else if (params.layout !== layoutMode) {
      layoutPickedByHand = true; // a mode named in the link is the sender's deliberate choice
      switchLayout(params.layout);
    }
  }
  // Restore color preset from #preset=<id>.
  if (params.preset) {
    const preset = COLOR_PRESETS.find((p) => p.id === params.preset);
    if (preset) applyPreset(params.preset);
  }
}

// Swap in a graph loaded from a local file (the Open-file button and drag-drop
// share this). Resets view state and restarts the layout.
function replaceGraph(data: GraphPayload | TargetGraphOutput, statusMsg: string) {
  // A locally opened/dropped file supersedes whatever provenance badge was
  // showing for the graph that loaded at boot.
  updateSnapshotBadge(null);
  // Detect and adapt flavor before prepareGraph, same as boot(). The knowledge
  // path is unchanged; the targets path is converted client-side.
  graphFlavor = flavorOf(data);
  let raw: GraphPayload;
  if (isTargetGraph(data)) {
    const nl = targetGraphToNodeLink(data);
    raw = { nodes: nl.nodes, links: nl.links };
    const nProjects = (data.projects || []).length;
    const nTargets = nl.nodes.filter((n) => n.kind === "target").length;
    statusMsg =
      "target graph, " +
      nProjects +
      " project" +
      (nProjects === 1 ? "" : "s") +
      ", " +
      nTargets +
      " target" +
      (nTargets === 1 ? "" : "s") +
      (nl.cycleWarnings.length ? "; " + nl.cycleWarnings.join("; ") : "");
  } else {
    raw = data;
  }
  graph = prepareGraph(raw);
  // Clear any layout-reversed flags (and stale edge routes) from a previous layered pass.
  for (const e of graph.links) {
    delete e.layoutReversed;
    delete e.points;
  }
  selected = null;
  hoverId = null;
  hoverPinned = null; // the pinned node belonged to the graph being replaced
  focusId = null;
  matchSet = null;
  radialCenter = null;
  radialRings = null;
  pendingRadialPick = false;
  resetMotion(); // clears flow edges AND any recency pulse mid-flight from the graph being replaced
  flowOn = false;
  pulsesPending = false;
  if (searchEl) searchEl.value = "";
  // Reset view/projection state.
  activeView = null;
  viewNode = null;
  viewNodeTo = null;
  activePreset = null;
  groups.splice(0, groups.length);
  projectionUnfolded = false;
  projectionSet = null;
  document
    .querySelectorAll<HTMLElement>(".console-graph-views__chip")
    .forEach((b) => b.removeAttribute("data-active"));
  document
    .querySelectorAll<HTMLElement>(".console-graph-colorgroup__preset")
    .forEach((b) => b.removeAttribute("data-active"));
  renderViewCommand(null, null, null);
  // Same scale-guard decision boot() makes, through the same function: a second collapse rule
  // here would make one file read differently depending on how it was opened. A dropped file
  // carries no fragment directive - it supersedes whatever the URL said.
  computeDefaultProjection(false);
  const ub = el("projection-unfold-btn");
  if (ub) ub.hidden = projectionUnfolded;
  renderCard(null);
  setStatus(projectionUnfolded ? statusMsg : "");
  if (!projectionUnfolded) updateProjectionStatus();
  renderLegend();
  renderList();
  renderSuggestions();
  syncConditionalViews();
  // Default layout mode per flavor: targets -> layered, knowledge -> force.
  // Check if the URL fragment requests a specific mode (user override persists).
  // radial additionally requires its #node= to resolve in the freshly loaded
  // graph (selected was just reset above); otherwise it falls back to the
  // flavor default like an invalid layout value would.
  const fragParams = hashParams();
  const radialNodeOk =
    fragParams.layout === "radial" && !!fragParams.node && graph.byId.has(fragParams.node);
  const requestedLayout: LayoutMode =
    isLayoutMode(fragParams.layout) && (fragParams.layout !== "radial" || radialNodeOk)
      ? fragParams.layout
      : graphFlavor === "targets"
        ? "layered"
        : "force";
  layoutMode = requestedLayout;
  layoutPickedByHand = false; // a fresh graph resets to its flavor default; so does the override
  if (layoutMode === "radial") selected = fragParams.node;
  wavesMeta = null;
  syncLayoutToggle();
  if (isDagMode()) {
    startSimulation(); // initializes node positions even if we stop it
    sim?.stop();
    if (!reapplyDagLayout()) {
      // Scale guard fired; fall back to force.
      layoutMode = "force";
      wavesMeta = null;
      syncLayoutToggle();
      startSimulation();
    }
  } else if (layoutMode === "radial") {
    startSimulation(); // initializes node positions even if we stop it
    applyRadialMode();
    renderCard(selected);
    syncListSelection();
  } else {
    startSimulation();
  }
  parkHiddenNodes(); // after the sim is built: the projection reduces the visible set
  draw();
  revealWholeGraph();
  syncGraphKindToggle();
}

// syncLayoutToggle updates the layout toggle group's selected state, each mode
// button's disabled/title (from layoutBlockedReason), and the bottom-left stage
// mode indicator to match the current
// layoutMode WITHOUT switching the mode (used after loading a new graph where
// the mode is set directly, and called from selectNode/applyQuery so
// availability tracks state as it changes).
function syncLayoutToggle() {
  document.querySelectorAll<HTMLButtonElement>("[data-layout]").forEach((btn) => {
    const mode = btn.dataset.layout;
    if (!mode || !isLayoutMode(mode)) return;
    const current = mode === layoutMode;
    btn.classList.toggle("pf-m-selected", current);
    const reason = layoutBlockedReason(mode);
    // Never disable the mode that is showing. layoutBlockedReason reads matchSet.size, so a
    // filter widening under a running Layered/Waves layout can block the very mode drawing the
    // canvas; rendering it selected AND disabled says the operator both is and cannot be here.
    // The reason still reaches them through the title.
    btn.disabled = !!reason && !current;
    btn.title = reason ?? LAYOUT_TITLES[mode] ?? "";
  });
}

// The graph-kind toggle group's per-kind titles when live. Mirrors scaffold.html's
// static title attributes so the initial render and syncGraphKindToggle agree.
const GRAPHKIND_TITLES: Record<GraphFlavor, string> = {
  targets:
    "The target graph: targets and what they depend on. Switch requires a live workspace (magus graph export --open --follow).",
  knowledge:
    "The full code graph: projects, targets, spells, modules, files, docs. Switch requires a live workspace (magus graph export --open --follow).",
};
const GRAPHKIND_LIVE_HINT =
  "Connect a live workspace to switch graphs: magus graph export --open --follow";

// syncGraphKindToggle updates the graph-source toggle group's selected state and
// each button's disabled/title to match graphFlavor and live-mode availability.
// Switching graphs only works against a live daemon (which serves both flavors);
// in a static snapshot both buttons are disabled with a hint, but the selected
// one still shows which graph is currently loaded. Called after every graph
// load (renderLoadedGraph/replaceGraph/liveApplyGraphUpdate) and once at boot.
function syncGraphKindToggle() {
  document.querySelectorAll<HTMLButtonElement>("[data-graphkind]").forEach((btn) => {
    const kind = btn.dataset.graphkind;
    if (kind !== "targets" && kind !== "knowledge") return;
    btn.classList.toggle("pf-m-selected", kind === graphFlavor);
    btn.disabled = !liveHost;
    btn.title = liveHost ? GRAPHKIND_TITLES[kind] : GRAPHKIND_LIVE_HINT;
  });
  // Say WHY on the surface, not only on hover. A disabled control with the reason buried in a
  // title attribute reads as broken: you click Target, nothing happens, and the explanation is
  // somewhere you have to already suspect. Most sessions are a snapshot or the demo, so this is
  // the common case, not the edge one.
  const note = el("graphkind-note");
  if (note) {
    note.textContent = liveHost ? "" : "Live workspace only";
    note.hidden = !!liveHost;
  }
}

// switchGraphKind switches the live-loaded graph between the target and knowledge
// flavors. Live-only: a static snapshot has no server to ask for the other flavor,
// so a click there just re-syncs the toggle back and explains why. switchingGraphKind
// guards against a second click landing mid-refetch, which would put two flavor
// fetches in flight whose responses could arrive out of order and leave graphFlavor
// disagreeing with the toggle.
let switchingGraphKind = false;
async function switchGraphKind(kind: "targets" | "knowledge") {
  if (kind === graphFlavor || switchingGraphKind) return;
  if (!liveHost) {
    setStatus(
      "To switch between the target and knowledge graphs, open a live workspace: magus graph export --open --follow",
      true,
    );
    syncGraphKindToggle();
    return;
  }
  switchingGraphKind = true;
  liveFlavor = kind === "targets" ? "targets" : null;
  liveGraphQuery = kind === "targets" ? "?flavor=targets" : "";
  liveETag = null;
  try {
    await liveRefetchGraph(); // reseeds graph data and sets graphFlavor via liveApplyGraphUpdate
  } finally {
    switchingGraphKind = false;
  }
  syncGraphKindToggle();
  setStatus("Switched to the " + (kind === "targets" ? "target" : "knowledge") + " graph.");
}

async function readGraphFile(file: File | undefined) {
  if (!file) return;
  // A user graph supersedes the empty state: dismiss it if it's still up.
  const empty = el("graph-empty-state");
  if (empty) empty.hidden = true;
  try {
    replaceGraph(
      JSON.parse(await file.text()),
      "Loaded " + file.name + " (local file; it stays on your machine).",
    );
  } catch (e) {
    setStatus("Could not read " + file.name + ": " + errMessage(e), true);
  }
}

// ---- default projection ----------------------------------------------------
// On first load with no fragment directives, show only project nodes + project
// -> project depends_on edges. Clicking a project unfolds it (targets flavor:
// its target children; knowledge flavor: its `contains` neighborhood).
//
// "Show everything" = one click on the "Show full graph" button (or any query).

function buildProjectionSet() {
  if (!graph || projectionUnfolded) return null;
  // Build the set of project ids + any node clicked open.
  const projectIds = new Set(graph.nodes.filter((n) => n.kind === "project").map((n) => n.id));
  if (projectIds.size === 0) return null; // no project nodes; show everything
  if (projectIds.size > 50) return null; // already small; show everything
  return projectIds;
}

function updateProjectionStatus() {
  if (!projectionSet || projectionUnfolded) return;
  const n = projectionSet.size;
  setStatus(
    "Showing " +
      n +
      " project" +
      (n === 1 ? "" : "s") +
      ". Click a project node to expand, or Show full graph.",
  );
}

function unfoldProjection() {
  projectionUnfolded = true;
  projectionSet = null;
  matchSet = null;
  if (searchEl) searchEl.value = "";
  query = "";
  if (graph) unparkNodes();
  renderList();
  const btn = el("projection-unfold-btn");
  if (btn) btn.hidden = true;
  setStatus("");
  updateHash();
  if (isDagMode()) {
    for (const e of graph.links) {
      delete e.layoutReversed;
      delete e.points;
    }
    reapplyDagLayout();
  } else draw();
}

// ---- views ------------------------------------------------------------------
// Each view answers a named question; max 7 total. View state serializes into
// the URL fragment as #view=<id>&node=<id>[&to=<id>].

// Reverse BFS over depends_on edges to collect transitive dependents of a node.
function transitiveDependents(nodeId: string) {
  // Build reverse adjacency for depends_on edges only.
  const revAdj = new Map();
  for (const e of graph.links) {
    const s = endpointId(e.source);
    const t = endpointId(e.target);
    if (e.relation !== "depends_on") continue;
    // In depends_on: source depends on target. Reverse: target -> source (dependents).
    let set = revAdj.get(t);
    if (!set) {
      set = new Set();
      revAdj.set(t, set);
    }
    set.add(s);
  }
  const visited = new Set([nodeId]);
  let frontier = [nodeId];
  while (frontier.length) {
    const next = [];
    for (const id of frontier) {
      for (const dep of revAdj.get(id) || []) {
        if (!visited.has(dep)) {
          visited.add(dep);
          next.push(dep);
        }
      }
    }
    frontier = next;
  }
  return visited;
}

// Shortest path between two nodes over depends_on edges (bidirectional BFS).
function shortestDependsOnPath(fromId: string, toId: string) {
  if (fromId === toId) return [fromId];
  // Build adjacency for depends_on (directed).
  const fwdAdj = new Map(),
    bwdAdj = new Map();
  for (const e of graph.links) {
    const s = endpointId(e.source),
      t = endpointId(e.target);
    if (e.relation !== "depends_on") continue;
    let sf = fwdAdj.get(s);
    if (!sf) {
      sf = new Set();
      fwdAdj.set(s, sf);
    }
    sf.add(t);
    let sb = bwdAdj.get(t);
    if (!sb) {
      sb = new Set();
      bwdAdj.set(t, sb);
    }
    sb.add(s);
  }
  // BFS from fromId (forward), also from toId (backward). Meet in middle.
  const fwd = new Map([[fromId, [fromId]]]);
  const bwd = new Map([[toId, [toId]]]);
  let fQueue = [fromId],
    bQueue = [toId];
  for (let step = 0; step < graph.nodes.length; step++) {
    // Advance the smaller frontier first.
    if (!fQueue.length && !bQueue.length) break;
    if (fQueue.length) {
      const next = [];
      for (const n of fQueue) {
        for (const nb of fwdAdj.get(n) || []) {
          if (!fwd.has(nb)) {
            fwd.set(nb, [...(fwd.get(n) ?? []), nb]);
            next.push(nb);
          }
          if (bwd.has(nb))
            return [...(fwd.get(nb) ?? []).slice(0, -1), ...(bwd.get(nb) ?? []).slice().reverse()];
        }
      }
      fQueue = next;
    }
    if (bQueue.length) {
      const next = [];
      for (const n of bQueue) {
        for (const nb of bwdAdj.get(n) || []) {
          if (!bwd.has(nb)) {
            bwd.set(nb, [...(bwd.get(n) ?? []), nb]);
            next.push(nb);
          }
          // Drop the meet node (nb) from the forward path to avoid duplication:
          // fwd.get(nb) ends at nb, bwd.get(nb) also starts at nb after reverse.
          if (fwd.has(nb))
            return [...(fwd.get(nb) ?? []).slice(0, -1), ...(bwd.get(nb) ?? []).slice().reverse()];
        }
      }
      bQueue = next;
    }
  }
  return null; // no path
}

// Longest duration-weighted chain (critical path) using node.DurationMs.
// Returns an array of node ids, or null if no duration data is present.
function criticalPath() {
  if (!graphHasDurations) return null;
  const dur = (n: GNode | undefined) => (n ? nodeDurationMs(n) : 0);
  // Longest path in DAG (depends_on subgraph), weighted by node duration.
  const fwdAdj = new Map<string, Set<string>>();
  for (const e of graph.links) {
    const s = endpointId(e.source),
      t = endpointId(e.target);
    if (e.relation !== "depends_on") continue;
    let sf = fwdAdj.get(s);
    if (!sf) {
      sf = new Set<string>();
      fwdAdj.set(s, sf);
    }
    sf.add(t);
  }
  const memo = new Map<string, { cost: number; next: string | null }>();
  const onStack = new Set<string>(); // nodes on the current recursion stack: guards depends_on cycles
  function dp(id: string): { cost: number; next: string | null } {
    const cached = memo.get(id);
    if (cached) return cached;
    onStack.add(id);
    const self = dur(graph.byId.get(id));
    let best: { cost: number; next: string | null } = { cost: self, next: null };
    for (const nb of fwdAdj.get(id) || []) {
      if (onStack.has(nb)) continue; // back-edge: skip to break the cycle
      const child = dp(nb);
      const c = self + child.cost;
      if (c > best.cost) best = { cost: c, next: nb };
    }
    onStack.delete(id);
    memo.set(id, best);
    return best;
  }
  // Find roots (no incoming depends_on).
  const hasIncoming = new Set();
  for (const e of graph.links) {
    if (e.relation === "depends_on") hasIncoming.add(endpointId(e.target));
  }
  const roots = graph.nodes.filter((n) => !hasIncoming.has(n.id));
  let bestRoot = null,
    bestCost = -Infinity;
  for (const r of roots) {
    const { cost } = dp(r.id);
    if (cost > bestCost) {
      bestCost = cost;
      bestRoot = r.id;
    }
  }
  if (!bestRoot) return null;
  // Reconstruct path.
  const path = [];
  let cur: string | null = bestRoot;
  while (cur) {
    path.push(cur);
    cur = dp(cur).next;
  }
  return path.length > 1 ? path : null;
}

// How many nodes the hubs view calls hubs. A cutoff has to be somewhere and the ranking is
// long-tailed; a dozen fills the canvas without turning the answer back into the whole graph.
const HUB_LIMIT = 12;

// Apply a named view. Updates activeView, viewNode, viewNodeTo, matchSet,
// and the CLI idiom display. Serializes into the fragment via updateHash().
function activateView(name: string, nodeId?: string | null, nodeTo?: string | null) {
  activeView = name;
  viewNode = nodeId || null;
  viewNodeTo = nodeTo || null;
  focusId = null;
  pendingRadialPick = false;
  projectionUnfolded = true; // a view always shows the full graph context
  projectionSet = null;
  matchSet = null;
  setFlowEdges(null); // cleared up front; the tail below rebuilds it once matchSet is final
  flowOn = false;
  if (searchEl) {
    searchEl.value = "";
  }
  query = "";

  // Sync button active state and show the clear button.
  document.querySelectorAll<HTMLElement>(".console-graph-views__chip").forEach((b) => {
    b.toggleAttribute("data-active", b.dataset.view === name);
  });
  const cvb = el("clear-view-btn");
  if (cvb) cvb.hidden = false;

  // Render the CLI idiom command for this view.
  renderViewCommand(name, nodeId, nodeTo);

  switch (name) {
    case "blast": {
      if (!nodeId) {
        setStatus(
          "Click a node to see what depends on it (blast view). CLI: magus explain <node-id>",
        );
        setResultLine(null);
        renderList();
        draw();
        updateHash();
        return;
      }
      const deps = transitiveDependents(nodeId);
      matchSet = deps;
      const n = graph.byId ? graph.byId.get(nodeId) : null;
      setStatus(
        "What breaks if you change " +
          (n ? n.label : nodeId) +
          "? " +
          (deps.size - 1) +
          " dependent" +
          (deps.size - 1 === 1 ? "" : "s") +
          ".",
      );
      setResultLine(
        "Showing the " +
          (deps.size - 1) +
          " target" +
          (deps.size - 1 === 1 ? "" : "s") +
          " that rebuild if you change " +
          (n ? n.label : nodeId) +
          ".",
      );
      void refineBlastFromServer(nodeId, deps.size - 1, ++viewGeneration);
      break;
    }
    case "trace": {
      if (!nodeId || !nodeTo) {
        setStatus(
          "Click two nodes to find the path between them (trace view). CLI: magus path <a> <b>",
        );
        setResultLine(null);
        renderList();
        draw();
        updateHash();
        return;
      }
      const na = graph.byId ? graph.byId.get(nodeId) : null;
      const nb = graph.byId ? graph.byId.get(nodeTo) : null;
      const path = shortestDependsOnPath(nodeId, nodeTo);
      if (!path) {
        setStatus(
          "No depends_on path from " +
            (na ? na.label : nodeId) +
            " to " +
            (nb ? nb.label : nodeTo) +
            ".",
        );
        setResultLine(
          "No path from " + (na ? na.label : nodeId) + " to " + (nb ? nb.label : nodeTo) + ".",
        );
        matchSet = new Set([nodeId, nodeTo]);
      } else {
        matchSet = new Set(path);
        setStatus(
          "Path: " +
            path
              .map((id) => {
                const n = graph.byId.get(id);
                return n ? n.label : id;
              })
              .join(" -> "),
        );
        setResultLine(
          "Path from " +
            (na ? na.label : nodeId) +
            " to " +
            (nb ? nb.label : nodeTo) +
            ", " +
            (path.length - 1) +
            " step" +
            (path.length - 1 === 1 ? "" : "s") +
            ".",
        );
      }
      void refineTraceFromServer(nodeId, nodeTo, ++viewGeneration);
      break;
    }
    case "critical": {
      const path = criticalPath();
      if (!path) {
        setStatus(
          "No duration data in this graph. Run `magus graph deps -o json` after a build to include timing.",
        );
        setResultLine(null);
        matchSet = null;
      } else {
        matchSet = new Set(path);
        const total = path.reduce((sum, id) => {
          const n = graph.byId.get(id);
          return sum + (n ? nodeDurationMs(n) : 0);
        }, 0);
        setStatus(
          "Critical path: " +
            path.length +
            " node" +
            (path.length === 1 ? "" : "s") +
            ", " +
            formatDuration(total) +
            " total (longest duration-weighted chain).",
        );
        setResultLine(
          "Slowest chain: " +
            path.length +
            " target" +
            (path.length === 1 ? "" : "s") +
            ", " +
            formatDuration(total) +
            " total.",
        );
      }
      break;
    }
    case "hubs": {
      matchSet = new Set(mostDependedOn(graph.nodes, graph.links, HUB_LIMIT));
      const n = matchSet.size;
      setStatus(
        n
          ? "What's a hub? The " + n + " most depended-on node" + (n === 1 ? "" : "s") + "."
          : "Nothing in this graph has a dependent.",
      );
      setResultLine(n ? "The " + n + " most depended-on nodes." : "Nothing has a dependent.");
      break;
    }
    case "orphans": {
      matchSet = new Set(disconnected(graph.nodes, graph.links));
      const n = matchSet.size;
      setStatus(
        "What's dead? " +
          n +
          " node" +
          (n === 1 ? "" : "s") +
          " of a kind that normally has dependencies, but with none either way.",
      );
      setResultLine(n + " node" + (n === 1 ? "" : "s") + " with no dependencies either way.");
      break;
    }
    case "cycles": {
      const ids = new Set<string>();
      for (const e of graph.links) {
        if (e.cycle) {
          ids.add(endpointId(e.source));
          ids.add(endpointId(e.target));
        }
      }
      if (ids.size) {
        matchSet = ids;
        setStatus(
          "Circular dependencies: " +
            ids.size +
            " target(s) caught in a loop: a configuration error to fix.",
        );
        setResultLine(
          ids.size + " target" + (ids.size === 1 ? "" : "s") + " in circular dependencies.",
        );
      } else {
        matchSet = null;
        setStatus("No circular dependencies: the dependency graph is acyclic.");
        setResultLine("No circular dependencies.");
      }
      break;
    }
    case "affected": {
      // Live mode: affected set is provided by caller or stored in window._liveAffectedIds.
      const aff = typeof nodeId === "object" && nodeId ? nodeId : window._liveAffectedIds;
      if (!aff || !aff.size) {
        setStatus("no affected nodes in current diff", true);
        setResultLine(null);
        matchSet = null;
      } else {
        matchSet = aff;
        setStatus(
          "What does my diff touch? " +
            aff.size +
            " affected node" +
            (aff.size === 1 ? "" : "s") +
            " (live workspace).",
        );
        setResultLine(
          aff.size + " target" + (aff.size === 1 ? "" : "s") + " affected by your last change.",
        );
      }
      break;
    }
  }
  setListExpanded(true);
  renderList();
  if (matchSet && matchSet.size) fitView(matchSet);
  updateHash();
  buildFlowEdges(); // matchSet is final now; no-ops for non-flow views
  draw();
}

function clearView() {
  activeView = null;
  viewNode = null;
  viewNodeTo = null;
  document
    .querySelectorAll<HTMLElement>(".console-graph-views__chip")
    .forEach((b) => b.removeAttribute("data-active"));
  const cvb = el("clear-view-btn");
  if (cvb) cvb.hidden = true;
  renderViewCommand(null, null, null);
  matchSet = null;
  if (searchEl) searchEl.value = "";
  query = "";
  setFlowEdges(null);
  flowOn = false;
  setResultLine(null);
  renderList();
  updateHash();
  draw();
}

// preferredModeForView says which display mode a question chip should switch
// to before applying its view (plan section 14, Decision 2). Two orthogonal
// axes: display mode (arrangement) and question view (emphasis/filter).
// Questions are the front door - clicking one may auto-switch the mode - but
// the mode control stays a manual override, so a question never fights a mode
// the user just picked by hand. null means "keep whatever mode is already
// showing" (blast/trace/critical/cycles/affected are fine in any DAG mode or
// radial; they only need to leave Force, where arrows/direction don't read).
function preferredModeForView(view: string): LayoutMode | null {
  switch (view) {
    case "blast":
    case "trace":
    case "critical":
    case "cycles":
    case "affected":
      return layoutMode === "force" ? "layered" : null;
    case "hubs":
    case "orphans":
      return "force"; // connectivity/disconnection reads best as the physics web
    default:
      // Unknown view (not one of the question chips above): keep whatever mode is
      // already showing rather than guessing.
      return null;
  }
}

// askQuestion is the single entry point every "Ask a question" chip
// (.console-graph-views__chip[data-view]) click routes through: it applies the
// view's preferred display mode (see preferredModeForView), guarded by
// layoutBlockedReason so an over-cap or center-less mode is skipped rather than
// forced (the view still applies in whatever mode is already showing), then
// dispatches the view exactly like the pre-Decision-2 click handler did -
// blast/trace enter node-picking mode, affected checks for a live diff, and
// everything else goes through activateView. This only fires from an EXPLICIT
// chip click; switchLayout calls from the [data-layout] toggle never call
// this, so a manual mode pick is never second-guessed.
// applyPreferredMode runs the arrangement half of a question: switch to the mode the view
// reads best in, unless the operator has picked one by hand or the mode is unavailable at this
// scale. Shared with the empty-state suggestion chips, which ask the same questions and so must
// not behave differently from the Explore chips that ask them.
function applyPreferredMode(view: string) {
  const want = preferredModeForView(view);
  if (want && want !== layoutMode && !layoutPickedByHand && !layoutBlockedReason(want)) {
    switchLayout(want);
  }
}

function askQuestion(view: string) {
  // The empty state is dismissed before the graph finishes arriving, so the chips are live for
  // a beat over no data. Answering then writes a wrong answer that nothing recomputes once the
  // nodes land - "Nothing has a dependent" over a graph with six thousand edges.
  if (!graph?.nodes.length) {
    setStatus("No graph loaded yet.", true);
    return;
  }
  applyPreferredMode(view);
  if (view === "blast" || view === "trace") {
    // Enter picking mode: status tells user to click a node. Bypasses
    // activateView (no node picked yet), so clear any flow left over from
    // whatever view was active before this click - otherwise particles from
    // the OLD view's matchSet would keep animating through the picking-mode gap.
    activeView = view;
    viewNode = null;
    viewNodeTo = null;
    pendingRadialPick = false;
    setFlowEdges(null);
    flowOn = false;
    document
      .querySelectorAll<HTMLElement>(".console-graph-views__chip")
      .forEach((x) => x.toggleAttribute("data-active", x.dataset.view === view));
    renderViewCommand(view, null, null);
    if (view === "blast") setStatus("Click a node to see what breaks if you change it.");
    else setStatus("Click the first node for the path (trace view).");
    updateHash();
    return;
  }
  if (view === "affected") {
    // Affected view is wired separately in live mode; clicking here when not
    // in live mode shows a hint instead of an empty view.
    const aff = window._liveAffectedIds;
    if (!aff || !aff.size) {
      setStatus(
        "affected view: requires live mode (magus graph export --open --follow) with a computed diff.",
        true,
      );
      return;
    }
    activateView("affected");
    return;
  }
  activateView(view);
  // Only fill an EMPTY preset slot: a preset the operator picked is theirs, and the timing
  // colors are a convenience for reading the chain, not a requirement of the view.
  if (view === "critical" && graphHasDurations && activePreset === null) {
    applyPreset("duration");
  }
}

// buildFlowEdges (re)builds particles.ts's flow-edge list from the CURRENT
// activeView/matchSet/graph positions: trace/critical flow along the path
// (consecutive path nodes joined by their depends_on link, in either stored
// orientation), blast flows along every depends_on link with both endpoints
// in matchSet. Each polyline runs dependency -> dependent (e.target is the
// dependency, e.source the dependent - see the edge-direction convention
// note in draw()'s edge pass), reusing the same routed bend points (e.points)
// the DAG layouts computed, so particles follow the curve on screen instead
// of cutting a chord through it. Call after matchSet is final for the view;
// it no-ops (and clears any prior flow) when the view isn't a flow view, so
// it's safe to call unconditionally at a view's settle point.
function buildFlowEdges() {
  if (!flowActive()) {
    setFlowEdges(null);
    flowOn = false;
    return;
  }
  const edges: FlowEdge[] = [];
  const pushLinkPolyline = (link: GLink) => {
    // s = dependent (e.source), t = dependency (e.target); flow travels t -> s.
    const s = graph.byId.get(endpointId(link.source));
    const t = graph.byId.get(endpointId(link.target));
    if (!s || !t || s.x == null || t.x == null) return;
    // Match draw()'s routePts assembly: collect both endpoints plus any routed
    // bend points and sort the whole polyline ascending by x. link.points runs
    // dependency -> dependent for a normal edge, but a layoutReversed cycle
    // back-edge stores it descending, which would zigzag the particle path if
    // just concatenated in source/target order.
    const pts = [{ x: t.x, y: t.y }, { x: s.x, y: s.y }, ...(link.points ?? [])].sort(
      (a, b) => a.x - b.x,
    );
    edges.push({ pts });
  };
  if (activeView === "trace" || activeView === "critical") {
    const path =
      activeView === "trace"
        ? viewNode && viewNodeTo
          ? shortestDependsOnPath(viewNode, viewNodeTo)
          : null
        : criticalPath();
    if (path) {
      for (let i = 0; i < path.length - 1; i++) {
        const a = path[i],
          b = path[i + 1];
        const link = graph.links.find(
          (l) =>
            l.relation === "depends_on" &&
            ((endpointId(l.source) === a && endpointId(l.target) === b) ||
              (endpointId(l.source) === b && endpointId(l.target) === a)),
        );
        if (link) pushLinkPolyline(link);
      }
    }
  } else if (activeView === "blast" && matchSet) {
    for (const l of graph.links) {
      if (l.relation !== "depends_on") continue;
      if (matchSet.has(endpointId(l.source)) && matchSet.has(endpointId(l.target)))
        pushLinkPolyline(l);
    }
  }
  flowOn = setFlowEdges(edges.length ? edges : null);
  if (flowOn) startMotion();
}

// ---- CLI idiom rendering ----------------------------------------------------
// The search box and view-command bar both use the .term-prompt styling from
// playground.html. The prefix swaps verb based on context:
//   search: "magus query"
//   blast:  "magus explain"
//   trace:  "magus path"
//   others: no command (no CLI equivalent maps cleanly)
//
// "Earn the prompt" rule (section 0.5): a surface shows the prompt ONLY when its
// behavior corresponds to a real CLI behavior backed by the drift fixture.

function shellQuote(s: string) {
  // Single-quote wrap if the value contains spaces, quotes, or other special chars.
  if (/[\s'"\\$`!;|&<>(){}*?[\]#~%]/.test(s)) return "'" + s.replace(/'/g, "'\\''") + "'";
  return s;
}

// Build the full shell command for "magus query <terms>".
// Uses `--` before the terms when any term starts with `-` (negation), so the
// flag parser doesn't treat it as a flag. This matches what the CLI needs and
// what the drift fixture (query_syntax.txtar) verifies.
function buildQueryCmd(queryStr: string) {
  if (!queryStr) return "magus query";
  const needsDDash = queryStr.trimStart().startsWith("-");
  return "magus query " + (needsDDash ? "-- " : "") + shellQuote(queryStr);
}

// Build the full CLI command string for copy-to-clipboard.
function viewCommandStr(name: string | null, nodeId?: string | null, nodeTo?: string | null) {
  switch (name) {
    case "blast":
      if (!nodeId) return "magus explain <node-id>";
      return "magus explain " + shellQuote(nodeId);
    case "trace":
      // Half-picked: name the node already chosen rather than printing a command that throws
      // away what the surface knows.
      if (!nodeId) return "magus path <a> <b>";
      if (!nodeTo) return "magus path " + shellQuote(nodeId) + " <b>";
      return "magus path " + shellQuote(nodeId) + " " + shellQuote(nodeTo);
    default:
      return null; // no valid CLI equivalent
  }
}

// Render the view command into the #view-cmd element (the term-prompt variant).
function renderViewCommand(name: string | null, nodeId?: string | null, nodeTo?: string | null) {
  const wrap = el("view-cmd");
  if (!wrap) return;
  const cmd = viewCommandStr(name, nodeId, nodeTo);
  if (!cmd) {
    wrap.hidden = true;
    return;
  }
  const verb = name === "blast" ? "magus explain" : name === "trace" ? "magus path" : null;
  if (!verb) {
    wrap.hidden = true;
    return;
  }
  const args = cmd.slice(verb.length).trim();
  wrap.hidden = false;
  wrap.innerHTML =
    '<span class="console-graph-prompt__ps1" aria-hidden="true">' +
    escapeHtml(verb) +
    ' <span class="console-graph-prompt__chevron">&#10095;</span></span>' +
    '<span class="console-graph-views__cmdargs">' +
    escapeHtml(args) +
    "</span>" +
    '<button type="button" class="console-graph-views__copy" title="Copy this command to the clipboard" aria-label="Copy command">&#10697;</button>';
  must(wrap.querySelector<HTMLElement>(".console-graph-views__copy")).addEventListener(
    "click",
    () => {
      navigator.clipboard
        .writeText(cmd)
        .then(() => {
          setStatus("Copied: " + cmd);
        })
        .catch((err) => setStatus("Could not copy: " + errMessage(err), true));
    },
  );
}

// Update the search-box copy button with the full command to copy.
function updateSearchCopyBtn() {
  const btn = el("search-copy-btn");
  if (!btn) return;
  const val = searchEl ? searchEl.value.trim() : "";
  const cmd = buildQueryCmd(val);
  btn.title = "Copy: " + cmd;
  btn.dataset.cmd = cmd;
}

// ---- empty-state suggestions (chips) ----------------------------------------
// After a graph loads, compute up to 3 quick facts and show clickable suggestion
// chips. Targets graphs lead with the build-order suggestion (the discoverability
// phase's "wow" - see the plan's ORDERING note); the rest fill remaining slots.
function renderSuggestions() {
  const wrap = el("suggestions");
  if (!wrap || !graph) {
    if (wrap) wrap.hidden = true;
    return;
  }
  const chips: { text: string; action: () => void }[] = [];

  // 0. Targets flavor: the build-order view is unique to magus - lead with it.
  if (graphFlavor === "targets") {
    chips.push({
      text: "See the build order: what runs in parallel?",
      action: () => {
        layoutPickedByHand = true; // an explicit "show me this arrangement", same as the chip
        switchLayout("waves");
      },
    });
  }

  // 1. The most depended-on node.
  if (graph.nodes.length > 1 && chips.length < 3) {
    const topId = mostDependedOn(graph.nodes, graph.links, 1)[0];
    const top = topId ? graph.byId.get(topId) : null;
    if (top) {
      chips.push({
        text: top.label + " is the biggest hub: what depends on it?",
        action: () => {
          applyPreferredMode("blast");
          activateView("blast", top.id);
        },
      });
    }
  }

  // 2. Cycle present?
  const hasCycle = graph.links.some((e) => e.cycle);
  if (hasCycle && chips.length < 3) {
    // Find the first cycle edge source as the starting point for a trace.
    const cycleEdge = graph.links.find((e) => e.cycle);
    const src = cycleEdge ? endpointId(cycleEdge.source) : null;
    chips.push({
      text: "A dependency cycle was detected: trace its path?",
      action: src
        ? () => {
            applyPreferredMode("trace");
            activateView("trace", src);
          }
        : () => {
            applyPreferredMode("hubs");
            activateView("hubs");
          },
    });
  }

  // 3. Dead-node count.
  const dead = disconnected(graph.nodes, graph.links);
  if (dead.length > 0 && chips.length < 3) {
    chips.push({
      text:
        dead.length + " node" + (dead.length === 1 ? "" : "s") + " depend on nothing: what's dead?",
      action: () => {
        applyPreferredMode("orphans");
        activateView("orphans");
      },
    });
  }

  if (!chips.length) {
    wrap.hidden = true;
    return;
  }
  wrap.hidden = false;
  // The heading rides in the same innerHTML as the chips so it appears and disappears with them;
  // without it these read as a third, unlabelled group of Ask chips rather than as facts already
  // computed about the graph in front of you.
  wrap.innerHTML =
    '<p class="console-graph-sidebar__viewslabel">In this graph</p>' +
    chips
      .map(
        (c, i) =>
          '<button type="button" class="console-graph-views__chip console-graph-sidebar__suggestion" data-i="' +
          i +
          '">' +
          escapeHtml(c.text) +
          "</button>",
      )
      .join("");
  wrap.querySelectorAll<HTMLElement>(".console-graph-sidebar__suggestion").forEach((b) => {
    b.addEventListener("click", () => {
      chips[Number(b.dataset.i)].action();
      wrap.hidden = true; // hide after first use
    });
  });
}

// ---- color preset lenses ----------------------------------------------------
// Three one-click presets over the existing color-group machinery. Each preset
// emits the same group entries the user could type by hand. The active preset
// serializes into the fragment as #preset=<id>.

// A preset emits color-group entries (like the ones a user could type by hand); a
// depth preset also carries a nodeSet so it can match ids directly.
interface PresetGroup {
  query: string;
  color: string;
  nodeSet?: Set<string>;
}
interface ColorPreset {
  id: string;
  label: string;
  groups: () => PresetGroup[];
}

const COLOR_PRESETS: ColorPreset[] = [
  {
    id: "spell",
    label: "Color by spell",
    groups: () => {
      // One color per distinct spell in the graph.
      const spellIds = graph.nodes.filter((n) => n.kind === "spell").map((n) => n.id);
      const palette = [
        "#8b5cf6",
        "#a855f7",
        "#6366f1",
        "#0891b2",
        "#059669",
        "#d97706",
        "#dc2626",
        "#ec4899",
      ];
      return spellIds.map((id, i) => ({
        query: "id:" + id,
        color: palette[i % palette.length],
      }));
    },
  },
  {
    id: "project",
    label: "Color by project",
    groups: () => {
      const projects = graph.nodes.filter((n) => n.kind === "project").map((n) => n.id);
      const palette = [
        "#2563eb",
        "#0a7ea4",
        "#059669",
        "#d97706",
        "#dc2626",
        "#8b5cf6",
        "#0891b2",
        "#ca8a04",
      ];
      return projects.map((id, i) => {
        // Knowledge-graph project ids are "project:<name>"; the project: field
        // matcher prepends "project:" again, producing "project:project:web" which
        // matches nothing. Strip the leading "project:" so the query is "project:web".
        const bare = id.startsWith("project:") ? id.slice("project:".length) : id;
        return { query: "project:" + bare, color: palette[i % palette.length] };
      });
    },
  },
  {
    id: "depth",
    label: "Color by DAG depth",
    groups: () => {
      // Use the layered layout's longest-path layer index.
      // Assign colors from deep (source) to shallow (sink).
      const palette = ["#0a7ea4", "#2563eb", "#059669", "#d97706", "#dc2626"];
      // Compute layer assignments (same as layoutLayered step 2, but read-only).
      const ids = new Set(graph.nodes.map((n) => n.id));
      const fwdAdj = new Map();
      for (const e of graph.links) {
        const s = endpointId(e.source),
          t = endpointId(e.target);
        if (e.relation !== "depends_on" || !ids.has(s) || !ids.has(t)) continue;
        let sf = fwdAdj.get(s);
        if (!sf) {
          sf = new Set();
          fwdAdj.set(s, sf);
        }
        sf.add(t);
      }
      const layers = new Map();
      function layer(id: string) {
        if (layers.has(id)) return layers.get(id);
        layers.set(id, 0); // mark for cycle guard
        let max = -1;
        for (const nb of fwdAdj.get(id) || []) {
          if (layers.get(nb) === 0 && !layers.has(nb + "_done")) continue; // skip back-edges
          max = Math.max(max, layer(nb));
        }
        const l = max + 1;
        layers.set(id, l);
        layers.set(id + "_done", true);
        return l;
      }
      for (const n of graph.nodes) layer(n.id);
      // Group by layer.
      const byLayer = new Map();
      for (const [id, l] of layers) {
        if (typeof l !== "number" || id.endsWith("_done")) continue;
        let s = byLayer.get(l);
        if (!s) {
          s = [];
          byLayer.set(l, s);
        }
        s.push(id);
      }
      const maxLayer = Math.max(...byLayer.keys(), 0);
      // Return one entry per layer; each entry carries a nodeSet so groupColorFor
      // can match directly without going through parseQuery/termMatches (which
      // would require a real `layer:` query field that doesn't exist in the CLI).
      return [...byLayer.entries()]
        .sort((a, b) => a[0] - b[0])
        .map(([l, ids_]) => {
          const idx = Math.round((l / Math.max(maxLayer, 1)) * (palette.length - 1));
          return { query: "layer:" + l, color: palette[idx], nodeSet: new Set(ids_) };
        });
    },
  },
  {
    id: "duration",
    label: "Color by duration",
    groups: () => {
      // Quintile buckets by RANK, not linear thresholds: build times are
      // long-tailed, so a handful of slow outliers would otherwise stretch a
      // linear scale until everything else looks the same color. Reuse the
      // depth preset's cool->hot ramp; hot = slow.
      const palette = ["#0a7ea4", "#2563eb", "#059669", "#d97706", "#dc2626"];
      const timed = graph.nodes
        .map((n) => ({ id: n.id, ms: nodeDurationMs(n) }))
        .filter((n) => n.ms > 0)
        .sort((a, b) => a.ms - b.ms || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
      if (!timed.length) return [];
      const buckets: Set<string>[] = palette.map(() => new Set<string>());
      timed.forEach((n, i) => {
        const bucket = Math.min(
          palette.length - 1,
          Math.floor((i * palette.length) / timed.length),
        );
        buckets[bucket].add(n.id);
      });
      return buckets
        .map((nodeSet, i) => ({ query: "duration q" + (i + 1), color: palette[i], nodeSet }))
        .filter((g) => g.nodeSet.size > 0);
    },
  },
];

let activePreset: string | null = null; // preset id string or null

function applyPreset(presetId: string) {
  const preset = COLOR_PRESETS.find((p) => p.id === presetId);
  if (!preset) return;
  // Clear previous groups.
  groups.splice(0, groups.length);
  if (activePreset === presetId) {
    // Toggle off.
    activePreset = null;
    document
      .querySelectorAll<HTMLElement>(".console-graph-colorgroup__preset")
      .forEach((b) => b.removeAttribute("data-active"));
    renderLegend(); // back to the kind palette, which is what the canvas shows again
    setStatus("Back to colouring by node kind.");
    refreshOverview();
    draw();
    updateHash();
    return;
  }
  activePreset = presetId;
  document
    .querySelectorAll<HTMLElement>(".console-graph-colorgroup__preset")
    .forEach((b) => b.toggleAttribute("data-active", b.dataset.preset === presetId));
  if (!graph.relIndex) graph.relIndex = relationIndex();
  const newGroups = preset.groups();
  for (const g of newGroups) {
    // Preserve a nodeSet when the preset provides one (e.g. depth: direct id set,
    // bypasses query grammar so the coloring works even for large layers).
    const entry: ColorGroup = { query: g.query, color: g.color, terms: parseQuery(g.query) };
    if (g.nodeSet) entry.nodeSet = g.nodeSet;
    groups.push(entry);
  }
  // Repaint the legend onto the new grouping and SAY what changed. A preset recolours every
  // node on the canvas and, before this, reported that nowhere: the legend kept describing the
  // kind palette and the only trace of the click was the button's own fill.
  renderLegend();
  // The bottom status bar carries this, NOT the floating result line over the canvas. The result
  // line is a strip centred on the stage: it lands on top of the legend - the one panel a reader
  // is looking at when they change the colouring - and it puts the explanation nowhere near the
  // control that produced it. The status bar already spans the foot of the app.
  setStatus(
    (PRESET_RESULT_LINES[presetId] ?? "Recoloured.") +
      " " +
      newGroups.length +
      " group" +
      (newGroups.length === 1 ? "" : "s") +
      "; the legend lists them. Click " +
      presetId +
      " again to go back to node kinds.",
  );
  refreshOverview();
  draw();
  updateHash();
}

// One line per preset saying what the canvas now MEANS - not what the control is called. These
// read on the result bar above the canvas, where the eye already is.
const PRESET_RESULT_LINES: Record<string, string> = {
  project: "Coloured by project: one hue per project, so boundaries show.",
  spell: "Coloured by spell: one hue per toolchain, so layers show.",
  depth: "Coloured by dependency depth: pale depends on nothing, dark depends on the most.",
  duration: "Coloured by run duration: hot is slow.",
};

// ---- live mode -------------------------------------------------------------

// daemonAttach, consumeLiveToken, getLiveToken, and fetchSSE now live in ./lib/daemon
// (imported at the top of this file) - the ONE audited copy of host resolution, the
// loopback lock, the shared bearer token, and the fetch-based SSE reader.

// A captured node position, keyed by node id, carried across a live refresh.
interface NodePos {
  x: number;
  y: number;
  fx: number | null;
  fy: number | null;
}

// capturePositions: before replacing the graph on a live refresh, record existing
// node positions keyed by id so they can be applied to the new graph.
function capturePositions() {
  const pos = new Map<string, NodePos>();
  if (graph) {
    for (const n of graph.nodes) {
      if (n.x != null) pos.set(n.id, { x: n.x, y: n.y, fx: n.fx, fy: n.fy });
    }
  }
  return pos;
}

function applyPositions(newNodes: GNode[], prevPos: Map<string, NodePos>) {
  if (!prevPos || !prevPos.size) return;
  for (const n of newNodes) {
    const p = prevPos.get(n.id);
    if (p) {
      n.x = p.x;
      n.y = p.y;
      if (p.fx != null && p.fx !== PARKED_X) {
        n.fx = p.fx;
        n.fy = p.fy;
      }
    }
    // New nodes: the simulation will place them; no hint needed.
  }
}

// recomputeLiveMatchSet recomputes matchSet (and, for the default projection,
// projectionSet) against the CURRENT graph for whatever activeView/focus/query/
// projection state is already in effect. It mirrors the per-view logic in
// activateView and the filter logic in applyQuery, but - unlike calling those
// functions directly - does not touch activeView/query/projectionUnfolded/
// layoutMode/search or refit the camera. Used by liveApplyGraphUpdate so a
// live refresh reseeds data without resetting the user's current exploration.
function recomputeLiveMatchSet() {
  if (activeView) {
    switch (activeView) {
      case "blast":
        matchSet = viewNode ? transitiveDependents(viewNode) : null;
        break;
      case "trace":
        if (viewNode && viewNodeTo) {
          const path = shortestDependsOnPath(viewNode, viewNodeTo);
          matchSet = path ? new Set(path) : new Set([viewNode, viewNodeTo]);
        } else {
          matchSet = null;
        }
        break;
      case "critical": {
        const path = criticalPath();
        matchSet = path ? new Set(path) : null;
        break;
      }
      case "hubs":
        matchSet = new Set(mostDependedOn(graph.nodes, graph.links, HUB_LIMIT));
        break;
      case "orphans":
        matchSet = new Set(disconnected(graph.nodes, graph.links));
        break;
      case "cycles": {
        const ids = new Set<string>();
        for (const e of graph.links) {
          if (e.cycle) {
            ids.add(endpointId(e.source));
            ids.add(endpointId(e.target));
          }
        }
        matchSet = ids.size ? ids : null;
        break;
      }
      case "affected": {
        const aff = window._liveAffectedIds;
        matchSet = aff && aff.size ? aff : null;
        break;
      }
      default:
        matchSet = null;
    }
    return;
  }
  // A local graph is emphasis too, and liveApplyGraphUpdate keeps focusId across a refresh:
  // without this branch the refresh would drop the neighborhood while the status line still
  // said the operator was in it.
  if (focusId) {
    matchSet = graph.byId.has(focusId) ? neighborhood(focusId, focusDepth) : null;
    if (!matchSet) focusId = null; // the focus node vanished in this refresh
    return;
  }
  if (query) {
    matchSet = matchSetFor(query);
    return;
  }
  if (!projectionUnfolded) {
    const ps = buildProjectionSet();
    if (ps) {
      projectionSet = ps;
      matchSet = new Set(ps);
    } else {
      projectionUnfolded = true;
      projectionSet = null;
      matchSet = null;
    }
    return;
  }
  matchSet = null;
}

// liveApplyGraphUpdate: state-preserving live refresh. Reseeds graph data and
// node positions from a fresh /api/v1/graph response and recomputes the active
// view/query/projection against the new data, but - unlike replaceGraph, which
// is a full reset for the "open a different file" case - does NOT reset
// activeView/query/activePreset/projectionUnfolded/layoutMode/search. Positions
// carry over by node id via capturePositions/applyPositions (unchanged).
function liveApplyGraphUpdate(data: GraphPayload) {
  const flavor = flavorOf(data);
  graphFlavor = flavor;
  let raw: GraphPayload = data;
  if (flavor === "targets") {
    const nl = targetGraphToNodeLink(data);
    raw = { nodes: nl.nodes, links: nl.links };
  }
  // Snapshot per-node durations from the OLD graph before it's overwritten below,
  // so the recency-pulse diff at the end of this function (added nodes, or
  // nodes whose duration changed) has something to compare the new graph against.
  const prevDurations = new Map<string, number>();
  if (graph) {
    for (const n of graph.nodes) prevDurations.set(n.id, nodeDurationMs(n));
  }
  const prevPos = capturePositions();
  graph = prepareGraph(raw);

  // Drop references to nodes the refresh removed rather than carry dangling ids.
  if (selected && !graph.byId.has(selected)) selected = null;
  if (hoverId && !graph.byId.has(hoverId)) hoverId = null;
  if (focusId && !graph.byId.has(focusId)) focusId = null;

  // graphHasDurations must reflect the NEW graph before recomputeLiveMatchSet
  // runs, since a "critical" activeView calls criticalPath(), which now reads
  // that cached flag instead of re-probing durations itself.
  syncConditionalViews();
  recomputeLiveMatchSet();
  parkHiddenNodes(); // re-park if the default projection is still active

  renderLegend();
  renderList();

  // Rebuild the simulation against the new node/link arrays (same pattern as
  // boot/replaceGraph), then reapply captured positions and reheat gently
  // rather than a full alpha=1 restart.
  startSimulation();
  applyPositions(graph.nodes, prevPos);
  if (isDagMode()) {
    sim?.stop();
    for (const e of graph.links) {
      delete e.layoutReversed;
      delete e.points;
    }
    if (!reapplyDagLayout()) {
      layoutMode = "force";
      wavesMeta = null;
      syncLayoutToggle();
      startSimulation();
      applyPositions(graph.nodes, prevPos);
      sim?.alpha(0.3).restart();
    }
  } else if (layoutMode === "radial") {
    sim?.stop();
    if (radialCenter && graph.byId.has(radialCenter)) {
      applyRadialMode();
    } else {
      // The radial center vanished in the refresh; radial without a center is
      // meaningless, so fall back to the flavor default.
      switchLayout(graphFlavor === "targets" ? "layered" : "force");
      setStatus(
        "radial center no longer exists in the refreshed graph; showing the default layout.",
      );
    }
  } else {
    sim?.alpha(0.3).restart();
  }
  // Rebuild flow edges (positions/routes are now settled) and fire recency
  // pulses for nodes the refresh added or whose duration changed. A refresh
  // that touches more than 40 nodes reads as noise rather than signal, so it
  // is skipped entirely rather than pulsing the whole graph.
  buildFlowEdges();
  const changedIds: string[] = [];
  for (const n of graph.nodes) {
    const prev = prevDurations.get(n.id);
    if (prev === undefined || prev !== nodeDurationMs(n)) changedIds.push(n.id);
  }
  if (changedIds.length > 0 && changedIds.length <= 40) {
    setPulses(changedIds, performance.now());
    pulsesPending = true;
    startMotion();
  }
  draw();
  updateLiveBadge();
  syncGraphKindToggle();
}

// liveRefetchGraph re-fetches whichever graph variant is currently loaded
// (liveGraphQuery) using its OWN ETag (liveETag). Sending one variant's ETag
// while requesting another variant's URL would make the server 200 with that
// other variant's body (e.g. downgrading a full knowledge graph to the
// projects-only skeleton), so the query string and ETag always travel together.
async function liveRefetchGraph() {
  if (!liveHost || !liveToken) return;
  const url = "http://" + liveHost + "/api/v1/graph" + liveGraphQuery;
  const headers = authHeaders(liveToken);
  if (liveETag) headers["If-None-Match"] = liveETag;
  let resp;
  try {
    resp = await fetch(url, { headers });
  } catch {
    return; // network error on refetch; SSE reconnect will handle it
  }
  if (resp.status === 304) return; // graph unchanged; ETag matched
  if (!resp.ok) return;
  liveETag = resp.headers.get("ETag") || null;
  let data;
  try {
    data = await resp.json();
  } catch {
    return;
  }
  liveApplyGraphUpdate(data);
}

function liveConnect() {
  if (!liveHost || !liveToken) return;
  if (liveSseAbort) liveSseAbort.abort();
  liveSseAbort = new AbortController();
  clearTimeout(liveReconnectTimer ?? undefined); // a fresh connect attempt supersedes any pending reconnect
  liveReconnectTimer = null;
  const url = "http://" + liveHost + "/api/v1/events";
  const headers = authHeaders(liveToken);

  fetchSSE(
    url,
    headers,
    (eventType) => {
      if (!surfaceVisible) {
        liveRefreshPending = true;
        return;
      }
      if (eventType === "graph") {
        liveRefetchGraph();
      } else if (eventType === "status") {
        fetchLiveStatus();
      }
    },
    (err) => {
      // Stream ended or errored: flip to disconnected, schedule reconnect.
      liveConnected = false;
      updateLiveBadge();
      showDisconnectBanner();
      clearTimeout(liveReconnectTimer ?? undefined);
      liveReconnectTimer = setTimeout(
        () => {
          liveConnect();
        },
        Math.min(liveReconnectDelay, 30000),
      );
      liveReconnectDelay = Math.min(liveReconnectDelay * 2, 30000);
    },
    liveSseAbort.signal,
    () => {
      // Stream opened successfully: reset backoff, clear the disconnect banner,
      // and refresh once. Without this, a reconnect after a gap (or the very
      // first connect racing the skeleton render) leaves the view stale until
      // the NEXT graph event, which may be minutes away.
      liveConnected = true;
      liveReconnectDelay = 1000;
      clearDisconnectBanner();
      updateLiveBadge();
      if (surfaceVisible) {
        liveRefetchGraph();
        fetchLiveStatus();
      } else {
        liveRefreshPending = true;
      }
    },
  );
}

function showDisconnectBanner() {
  const banner = el("live-disconnect-banner");
  if (!banner) return;
  const now = new Date();
  const hhmm =
    now.getHours().toString().padStart(2, "0") + ":" + now.getMinutes().toString().padStart(2, "0");
  banner.textContent = "disconnected, showing workspace as of " + hhmm + ", reconnecting...";
  banner.hidden = false;
}

function clearDisconnectBanner() {
  const banner = el("live-disconnect-banner");
  if (banner) {
    banner.textContent = "";
    banner.hidden = true;
  }
}

function updateLiveBadge() {
  const badge = el("live-badge");
  if (badge) {
    if (liveHost) {
      const ws = liveWorkspaceName || liveHost;
      const content = badge.querySelector<HTMLElement>(".pf-v6-c-label__content");
      if (content)
        content.textContent = liveConnected ? "live: " + ws : "live: " + ws + " (connecting)";
      badge.hidden = false;
      // Blue PF Label when connected, grey while (re)connecting or disconnected.
      badge.classList.toggle("pf-m-blue", liveConnected);
      badge.classList.toggle("pf-m-grey", !liveConnected);
    } else {
      badge.hidden = true;
    }
  }
  // Mirror the live state onto the shared console status bar's connection dot, so the graph explorer
  // reads the same as the dashboard and log viewer. A snapshot/demo graph has no live daemon link, so
  // the dot stays at its default "not connected".
  if (surfaceVisible) {
    publishStatus(
      liveHost
        ? {
            connection: liveConnected ? "connected" : "connecting",
            label: liveConnected ? "connected" : "connecting...",
          }
        : { connection: "none", label: "not connected" },
    );
  }
}

// updateSnapshotBadge shows "snapshot: <provenance>" for the private,
// non-live sources (a #data= fragment or a --serve loopback fetch) - the
// counterpart to the live badge for the common case of a one-shot `magus
// graph open` without a running daemon. Hidden for "demo" and "remote", and
// always hidden once live mode is active (bootLive never calls this).
function updateSnapshotBadge(source: string | null) {
  const badge = el("snapshot-badge");
  if (!badge) return;
  if (source === "local" || source === "loopback") {
    const content = badge.querySelector<HTMLElement>(".pf-v6-c-label__content");
    if (content) content.textContent = "snapshot: " + source;
    badge.hidden = false;
  } else {
    badge.hidden = true;
  }
}

async function fetchLiveStatus() {
  if (!liveHost || !liveToken) return;
  try {
    const client = createClient(StatusService, createDaemonTransport(liveHost, liveToken));
    const res = await client.getStatus({});
    const status = res.status;
    if (!status) return;
    // Extract workspace name from the first loaded workspace.
    if (status.pool && status.pool.workspaces.length > 0) {
      liveWorkspaceName = status.pool.workspaces[0].root;
    }
    // Render status strip.
    const strip = el("live-status-strip");
    if (strip && status.pool) {
      const p = status.pool;
      strip.textContent = "pool: " + p.running + "/" + p.capacity + " running";
      if (p.queued > 0) strip.textContent += ", " + p.queued + " queued";
      strip.hidden = false;
    }
    // Affected view (deferred): types.StatusOutput.Affected exists on the wire
    // type but neither status producer (cmd/magus/status.go - no workspace/VCS
    // context at that call site - or internal/webbridge/bridge.go) populates it
    // yet, so status.pool.affected never arrives. Rather than ship client code
    // that pretends to enable a view that can never actually receive data, the
    // "affected" view button stays disabled (see the `disabled` attribute in
    // graph.html) until a real Affected computation is wired server-side.
    updateLiveBadge();
  } catch {
    /* network error; badge stays */
  }
}

// ---- boot ------------------------------------------------------------------
// computeDefaultProjection sets projectionUnfolded/projectionSet/matchSet for
// the initial default-projection decision at boot: a projection is shown when
// no fragment directive is present (no view/q/node[/data/src], per the
// caller's own hasFragmentDirective) and the graph has a project-node count
// buildProjectionSet is willing to collapse. Shared by boot() and bootLive()
// so the two boot paths cannot drift on this decision.
function computeDefaultProjection(hasFragmentDirective: boolean) {
  // Default view is the FULL graph: the whole workspace at a glance is the wow moment
  // on load. The projects-only projection is kept only as a scale guard for very large
  // graphs, where a cold force layout of many thousands of nodes would jank the reveal;
  // there it collapses to project nodes with the "Show full graph" unfold still offered.
  const PROJECTION_GUARD = 2500; // node count above which we collapse on load for perf
  if (!hasFragmentDirective && graph && graph.nodes.length > PROJECTION_GUARD) {
    const ps = buildProjectionSet();
    if (ps) {
      projectionUnfolded = false;
      projectionSet = ps;
      matchSet = new Set(ps);
      return;
    }
  }
  projectionUnfolded = true;
}

// applyLayoutAndSimulation picks the layout mode (fragment override, else the
// per-flavor default), starts the force simulation, and runs the layered
// layout with its scale-guard fallback. Shared by boot() and bootLive().
function applyLayoutAndSimulation(requestedLayout: string, flavor: GraphFlavor) {
  if (
    requestedLayout === "force" ||
    requestedLayout === "layered" ||
    requestedLayout === "waves" ||
    requestedLayout === "radial"
  ) {
    layoutMode = requestedLayout;
  } else {
    layoutMode = flavor === "targets" ? "layered" : "force";
  }
  // Radial needs a resolved center (selected/focusId), which isn't available yet
  // at this point in boot - applyDeepLinks (run afterward by finishInteractiveSetup)
  // selects the #node= and switches into radial once it resolves, with its own
  // node-required guard. Fall back to the flavor default here so layoutMode never
  // sits on "radial" without applyRadialMode ever having run.
  if (layoutMode === "radial") {
    layoutMode = flavor === "targets" ? "layered" : "force";
  }
  layoutPickedByHand = false; // a fresh graph resets to its flavor default; so does the override
  wavesMeta = null;
  syncLayoutToggle();
  seedBigBang();
  startSimulation();
  if (isDagMode()) {
    sim?.stop();
    if (!reapplyDagLayout()) {
      // Scale guard fired; fall back to force.
      layoutMode = "force";
      wavesMeta = null;
      syncLayoutToggle();
      startSimulation();
    }
  }
}

// parkHiddenNodes moves every node outside the active default projection far
// off-canvas so the force sim does not waste cycles on the full soup while
// only project nodes are visible. Shared by boot(), bootLive(), and
// liveApplyGraphUpdate (a live refresh that lands while the projection is
// still active must re-park the same way a fresh load does).
function parkHiddenNodes() {
  if (!projectionUnfolded && projectionSet) {
    for (const n of graph.nodes) {
      if (!projectionSet.has(n.id)) {
        n.fx = PARKED_X;
        n.fy = PARKED_X;
        n.x = PARKED_X;
        n.y = PARKED_X;
      }
    }
  }
}

// finishInteractiveSetup wires zoom/drag, restores any view/query/layout/preset
// deep link, and renders the empty-state suggestions and conditional views.
// One-time boot wiring - shared by boot() and bootLive(), NOT called on a live
// refresh (liveApplyGraphUpdate), which reseeds data without re-wiring input.
function finishInteractiveSetup() {
  setupZoomDrag();
  // Reveal the "What's slow?" (critical) view/preset only when the graph has
  // DurationMs data. Runs BEFORE applyDeepLinks: a `#view=critical` deep link
  // calls activateView("critical") -> criticalPath(), which reads the
  // graphHasDurations flag this sets - it must be current for THIS graph
  // before that can fire.
  syncConditionalViews();
  // applyDeepLinks handles q= and node= (layout= is handled by
  // applyLayoutAndSimulation above; applyDeepLinks skips switching when the
  // mode already matches).
  applyDeepLinks();
  // Emit empty-state suggestion chips.
  renderSuggestions();
  // Fill the detail column. AFTER applyDeepLinks, so a #q=/#view= link is already reflected in
  // what the overview reports rather than being described a frame before it applies.
  refreshOverview();
}

// renderLoadedGraph runs boot's data-to-view pipeline (detect/prepare/project/status/layout/reveal),
// excluding the one-time interaction wiring, so the demo button can re-run it in place.
function renderLoadedGraph(loaded: { data: GraphPayload; source: string }): void {
  const flavor = flavorOf(loaded.data);
  graphFlavor = flavor;
  graphSource = loaded.source;
  let rawForPrepare = loaded.data;
  let cycleWarnings: string[] = [];
  if (flavor === "targets") {
    const nl = targetGraphToNodeLink(loaded.data);
    rawForPrepare = { nodes: nl.nodes, links: nl.links };
    cycleWarnings = nl.cycleWarnings;
  }
  graph = prepareGraph(rawForPrepare);

  // #data=/#src= (and #view/#q/#node) mean a specific graph or view was requested: show its full detail,
  // not the projects-only projection. computeDefaultProjection otherwise keeps the full graph unless it
  // trips the large-graph perf guard.
  const bootParams = hashParams();
  const hasFragmentDirective = !!(
    bootParams.view ||
    bootParams.q ||
    bootParams.node ||
    bootParams.data ||
    bootParams.src
  );
  computeDefaultProjection(hasFragmentDirective);

  const unfoldBtn = el("projection-unfold-btn");
  if (unfoldBtn) unfoldBtn.hidden = projectionUnfolded;

  updateSnapshotBadge(loaded.source);

  // Status line: targets flavor shows a summary; knowledge/demo shows a brief confirmation or nothing.
  if (flavor === "targets") {
    const nProjects = (loaded.data.projects || []).length;
    const nTargets = (rawForPrepare.nodes || []).filter((n) => n.kind === "target").length;
    const base =
      "target graph, " +
      nProjects +
      " project" +
      (nProjects === 1 ? "" : "s") +
      ", " +
      nTargets +
      " target" +
      (nTargets === 1 ? "" : "s");
    if (!projectionUnfolded) updateProjectionStatus();
    else setStatus(cycleWarnings.length ? base + "; " + cycleWarnings.join("; ") : base);
  } else {
    if (!projectionUnfolded) updateProjectionStatus();
    else
      setStatus(
        loaded.source === "local"
          ? "Your workspace graph: it never left your machine."
          : loaded.source === "loopback"
            ? "Your workspace graph, served over loopback: it never left your network."
            : "",
      );
  }

  renderLegend();
  renderList();

  const initialParams = hashParams();
  applyLayoutAndSimulation(initialParams.layout, flavor);
  parkHiddenNodes();

  syncGraphKindToggle();
}

// activate boots the graph explorer against the scaffold already in the document. el() resolves DOM
// handles at call time via getElementById, so it needs no separate resolve step - just the scaffold
// present. Exported so the console's graph PageModule can drive it after injecting the scaffold into
// a host; the standalone page auto-boots below. Chrome (nav/search/drawer/settings) comes from the
// shared main.js on the standalone page, which the console does not load, so there is no self-wired
// chrome to guard (unlike the dashboard).
export async function activate() {
  resolveDom();
  readTheme();

  // Collapse the stage tools behind the PF toolbar kebab on narrow viewports (the shared
  // responsive-toolbar pattern the log viewer uses too). Wired before any early return so it
  // works in the live/empty states as well.
  wireToolbarOverflow();

  // Register file-open listeners before any early return so the installed PWA
  // can open a .json file even when the demo graph fails to load (no #data/#src
  // and the fetch of ./graph.json fails). readGraphFile/replaceGraph rebuild
  // from scratch so they tolerate an empty initial graph state.

  // Drag-drop a graph.json onto the canvas.
  canvas.addEventListener("dragover", (e) => e.preventDefault());
  canvas.addEventListener("drop", (e) => {
    e.preventDefault();
    readGraphFile(must(e.dataTransfer).files[0]);
  });

  // File handler: when the installed PWA is launched with "Open with" on a .json file,
  // the browser delivers it here via launchQueue. Uses the same readGraphFile path as
  // drag-drop so behavior is identical. Feature-detected; no effect in browsers that
  // lack the File Handling API (all non-Chromium, and Chromium without the PWA installed).
  if ("launchQueue" in window) {
    window.launchQueue?.setConsumer(async (launchParams: LaunchParams) => {
      if (!launchParams.files || launchParams.files.length === 0) return;
      try {
        const fileHandle = launchParams.files[0];
        const f = await fileHandle.getFile();
        readGraphFile(f);
      } catch (e) {
        setStatus("Could not open the launched file: " + errMessage(e), true);
      }
    });
  }

  // Attempt a live-mode connection on an explicit daemon attach (#port, or the
  // daemon-origin/shared console). Returns true if handled; false falls through.
  if (await bootLive()) return;

  // Show the load spinner while loadGraph() is in flight (it fetches the ~1.4MB demo graph.json on a
  // #demo / deep-link boot). A cold visit returns instantly, so the spinner never visibly flashes.
  const loadingEl = el("graph-loading");
  if (loadingEl) loadingEl.hidden = false;
  let loaded;
  try {
    loaded = await loadGraph();
  } finally {
    if (loadingEl) loadingEl.hidden = true;
  }
  if (!loaded) {
    document.body.classList.add("graph-empty");
    return;
  }

  // Run boot's data-to-view pipeline, then the one-time interaction wiring. Splitting the two lets the
  // demo button re-run just the render (renderLoadedGraph) in place, without re-wiring listeners.
  renderLoadedGraph(loaded);
  finishInteractiveSetup();
  // AFTER the wiring: fitView needs the zoom behavior setupZoomDrag installs and returns
  // silently without it, so a reveal from inside renderLoadedGraph never framed anything.
  revealWholeGraph();

  // Empty state: nothing loaded (no #data/#src, no live attach), so show the prompt instead. The pipeline ran on
  // an empty graph, so interactions are wired; a dropped file arrives via replaceGraph and dismisses
  // this, and the demo comes in through boot on #demo rather than being swapped in place.
  if (loaded.source === "empty") {
    const empty = el("graph-empty-state");
    if (empty) empty.hidden = false;
  }

  bootWireEvents();
}

// bootWireEvents wires all the event listeners that are the same for both the
// normal load path and the live-mode load path. Called at the end of boot() and
// from the live-mode path before it returns.
function bootWireEvents() {
  // One AbortController for every window/document lifecycle listener this function wires
  // (keydown, fullscreenchange, the theme matchMedia change, hashchange, visibilitychange,
  // reduced-motion change), created up front so every addEventListener call below can route
  // through its signal - deactivate() then removes them all with a single abort(). A reopened
  // graph re-runs this block with a fresh controller.
  lifecycleAbort = new AbortController();
  const lifecycleSignal = lifecycleAbort.signal;

  // Debounce typing so a large graph isn't re-filtered + re-rendered on every
  // keystroke; the legend/example/deep-link paths call applyQuery directly (no wait).
  // ReturnType<typeof setTimeout>, not number: the browser's setTimeout returns a
  // number but Node's returns a Timeout object, and the test type-check program pulls
  // in @types/node - so a bare `number` only compiles as long as nothing checks this
  // file against Node's lib.
  let queryTimer: ReturnType<typeof setTimeout> | undefined;
  searchEl.addEventListener("input", () => {
    // Answer the keystroke immediately with the count, then apply on the debounce. Matching is a
    // single pass over the node list, far cheaper than the re-layout and repaint applyQuery
    // triggers - which is what the 120ms is protecting, not the matching.
    const typed = searchEl.value.trim();
    renderScope(
      typed === query ? undefined : (matchSetFor(typed)?.size ?? graph?.nodes.length ?? 0),
    );
    clearTimeout(queryTimer);
    queryTimer = setTimeout(() => {
      applyQuery(searchEl.value);
      updateSearchCopyBtn();
    }, 120);
  });
  searchEl.disabled = false;
  updateSearchCopyBtn();

  // Wire the copy button beside the search box.
  const searchCopyBtn = el("search-copy-btn");
  if (searchCopyBtn) {
    searchCopyBtn.addEventListener("click", () => {
      const cmd = searchCopyBtn.dataset.cmd || "magus query";
      navigator.clipboard.writeText(cmd).then(() => setStatus("Copied: " + cmd));
    });
  }

  // The query-syntax "?" button used to be a bare title= tooltip - invisible on touch, since
  // hover never fires there. attachHelpPopover upgrades it to a tap-to-open popover (reusing
  // the title text as the body, then stripping it); a re-run of bootWireEvents is a no-op since
  // the title is already gone by then.
  const queryHelpBtn = el("graph-query-help");
  if (queryHelpBtn) attachHelpPopover(queryHelpBtn);

  // Wire the projection unfold button ("Show full graph").
  const unfoldBtnWire = el("projection-unfold-btn");
  if (unfoldBtnWire) {
    unfoldBtnWire.addEventListener("click", () => {
      unfoldProjection();
      renderSuggestions(); // re-render suggestions after full graph is visible
    });
  }

  // Wire view buttons (.console-graph-views__chip). Every explicit click routes through
  // askQuestion (Decision 2: a question may auto-switch the display mode, guarded by
  // layoutBlockedReason) - never the [data-layout] toggle, which switches mode alone.
  //
  // Must stay delegated: the Reference drawer CLONES this surface's [data-ref-section] blocks,
  // and cloneNode copies no listeners, so a querySelectorAll snapshot over the sources reaches
  // no chip in the drawer.
  document.addEventListener(
    "click",
    (ev) => {
      const b = (ev.target as HTMLElement | null)?.closest<HTMLElement>(
        ".console-graph-views__chip",
      );
      const v = b?.dataset.view;
      if (!b || !v) return;
      if ((b as HTMLButtonElement).disabled || b.hasAttribute("data-disabled")) return;
      if (activeView === v) {
        clearView();
        return;
      }
      askQuestion(v);
    },
    { signal: lifecycleSignal },
  );

  // Wire the clear-view button.
  const clearViewBtn = el("clear-view-btn");
  if (clearViewBtn) clearViewBtn.addEventListener("click", clearView);

  // The count row toggles the (default-collapsed) node cloud.
  const listToggle = el("list-toggle");
  if (listToggle) listToggle.addEventListener("click", () => setListExpanded(!listExpanded));

  // Zoom-to-fit: frame the current matches (or the whole graph) in the viewport.
  const fitBtn = el("fit-btn");
  if (fitBtn)
    fitBtn.addEventListener("click", () => fitView(matchSet && matchSet.size ? matchSet : null));

  // Mobile-only legend toggle: on narrow screens the kind legend is collapsed off
  // the canvas by default (CSS) so it doesn't cover the graph; this flips it open.
  // Harmless on desktop, where the toggle is display:none and the legend is always
  // shown.
  const legendToggle = el("legend-toggle");
  const legendPanel = el("graph-legend-panel");
  if (legendToggle && legendPanel) {
    legendToggle.addEventListener("click", () => {
      const open = legendPanel.toggleAttribute("data-open");
      legendToggle.setAttribute("aria-expanded", open ? "true" : "false");
    });
  }

  // No separate lens wiring. The hubs/orphans buttons are ordinary view chips - they carry
  // .console-graph-views__chip and data-view like every other question - so the chip handler
  // above already dispatches them. A second [data-lens] listener ran on the SAME click and
  // called activateView unconditionally, which undid the chip handler's clearView the instant
  // it fired: clicking an active hubs/orphans chip cleared the view and re-applied it, so the
  // toggle could never be turned off. The data-lens attribute is gone from the scaffold too;
  // it was only ever a hook for the listener this comment replaces.

  // Color preset buttons.
  document.querySelectorAll<HTMLElement>(".console-graph-colorgroup__preset").forEach((b) => {
    b.addEventListener("click", () => {
      const preset = b.dataset.preset;
      if (preset) applyPreset(preset);
    });
  });

  // Esc clears a focus/query. It stays a raw listener rather than a registered command because
  // it is the conventional global dismiss and has to fire while the search box has focus, which
  // the keybinding layer deliberately suppresses (isTyping). The focus-depth keys DID live here
  // too; they are commands now, so they reach the Shortcuts view and can be rebound.
  document.addEventListener(
    "keydown",
    (e) => {
      if (e.key !== "Escape") return;
      clearFocusOrQuery();
      if (searchEl.blur) searchEl.blur();
    },
    { signal: lifecycleSignal },
  );

  // Command surface + keybindings, the same shape as the log viewer: each action is a named command
  // (dispatching to the existing control) bound to a single key that dodges browser combos and is
  // guarded against typing. The user's overrides ride the shared persisted keymap.
  const clickGraph = (id: string): void => {
    const b = el(id) as HTMLButtonElement | null;
    if (b && !b.disabled) b.click();
  };
  registerCommand({
    id: "graph.search",
    label: "Focus search",
    group: "Graph",
    run: () => searchEl.focus(),
  });
  registerCommand({
    id: "graph.fit",
    label: "Zoom to fit",
    group: "Graph",
    run: () => clickGraph("fit-btn"),
  });
  registerCommand({
    id: "graph.layout",
    label: "Cycle layout",
    group: "Graph",
    run: () => cycleLayout(),
  });
  registerCommand({
    id: "graph.focus.shallower",
    label: "Local graph: one hop less",
    group: "Graph",
    run: () => changeFocusDepth(-1),
  });
  registerCommand({
    id: "graph.focus.deeper",
    label: "Local graph: one hop more",
    group: "Graph",
    run: () => changeFocusDepth(1),
  });
  uninstallKeys?.();
  uninstallKeys = installKeybindings(() => mergeKeymap(GRAPH_KEYMAP, keymapCell.get()));

  // Query-syntax reference: each example runs itself in the filter (teach-by-doing).
  // Scope to [data-q] so the lens/add-group buttons (which share .console-graph-help__example for its
  // chip styling but carry no data-q) aren't wired as examples. Delegated for the same reason the
  // view chips are: these examples only ever render as reference-drawer clones.
  document.addEventListener(
    "click",
    (ev) => {
      const b = (ev.target as HTMLElement | null)?.closest<HTMLElement>(
        ".console-graph-help__example[data-q]",
      );
      if (!b) return;
      const q = b.dataset.q ?? "";
      searchEl.value = q;
      applyQuery(q);
      searchEl.focus();
      must(document.querySelector<HTMLElement>(".console-graph-app")).scrollIntoView({
        behavior: "smooth",
        block: "nearest",
      });
    },
    { signal: lifecycleSignal },
  );

  // "Open file" toolbar button proxies to the hidden <input type=file>.
  const openBtn = el("open-file-btn");
  if (openBtn && fileInput) openBtn.addEventListener("click", () => fileInput.click());
  if (fileInput) fileInput.addEventListener("change", () => readGraphFile(fileInput.files?.[0]));

  // Fullscreen toggle: expand the whole explorer panel (like the playground).
  // Hidden if the browser lacks the Fullscreen API rather than showing a dead
  // button; label + aria-pressed follow fullscreenchange so Esc stays in sync.
  const fsBtn = el("fullscreen-btn");
  const appEl = document.querySelector<HTMLElement>(".console-graph-app");
  if (fsBtn && appEl && appEl.requestFullscreen) {
    fsBtn.addEventListener("click", () => {
      if (document.fullscreenElement) document.exitFullscreen();
      else appEl.requestFullscreen();
    });
    const fsLabel = fsBtn.querySelector<HTMLElement>(".console-render-btn__label");
    document.addEventListener(
      "fullscreenchange",
      () => {
        const on = document.fullscreenElement === appEl;
        if (fsLabel) fsLabel.textContent = on ? "Exit" : "Fullscreen";
        fsBtn.setAttribute("aria-pressed", on ? "true" : "false");
        // The canvas is sized to its box; refit after the panel resizes.
        resizeCanvas();
        if (sim) {
          const c = usableCenter(canvas.clientWidth, canvas.clientHeight, stageInsets());
          sim.force("center", forceCenter(c.x, c.y));
          sim.alpha(0.15).restart();
        }
        draw();
      },
      { signal: lifecycleSignal },
    );
  } else if (fsBtn) {
    fsBtn.hidden = true;
  }

  // Re-read the console tokens and repaint on a theme toggle.
  let t: ReturnType<typeof setTimeout> | undefined;
  const rerender = () => {
    clearTimeout(t);
    t = setTimeout(() => {
      readTheme();
      renderLegend();
      renderList();
      draw();
    }, 0);
  };
  themeObserver = new MutationObserver(rerender);
  themeObserver.observe(root, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", rerender, {
    signal: lifecycleSignal,
  });

  // Keep the canvas bitmap in lockstep with its CSS box. A ResizeObserver (not just
  // window "resize") is what makes this robust: the stage also changes size when the
  // details card opens/closes (the grid goes to three columns), when a disclosure
  // above the app expands, or on fullscreen - none of which fire a window resize.
  // Without this the bitmap keeps its old dimensions and the browser stretches it,
  // squishing the graph's aspect ratio. rAF coalesces the burst a drag produces into
  // one resize per frame. Setting canvas.width/height doesn't change the CSS box
  // (width/height are 100%), so this can't feedback-loop.
  let resizePending = false;
  const onStageResize = () => {
    if (resizePending) return;
    resizePending = true;
    requestAnimationFrame(() => {
      resizePending = false;
      resizeCanvas();
      if (sim) {
        const c = usableCenter(canvas.clientWidth, canvas.clientHeight, stageInsets());
        sim.force("center", forceCenter(c.x, c.y));
        sim.alpha(0.1).restart();
      }
      // Selecting a node narrows the stage by the explain card's width, and a DAG layout keeps
      // its pinned coordinates through that, so the framing goes stale with nothing to correct
      // it. What the correction should BE depends on what the camera was doing: holding one
      // node in the middle, re-centre on it in the new box; framing the graph, re-fit; being
      // driven by hand, leave it alone - moving it then yanks the view out from under a click.
      if (centeredOn) centerOn(centeredOn);
      else if (!cameraOwnedByOperator) fitView(matchSet);
      else draw();
    });
  };
  // stageResizeObserver is disconnected (and the lifecycleSignal aborted, removing every
  // listener wired above and below through it) by deactivate() - a reopened graph re-runs
  // this block with fresh handles.
  stageResizeObserver = new ResizeObserver(onStageResize);
  stageResizeObserver.observe(canvas);
  window.addEventListener(
    "hashchange",
    () => {
      suppressHash = true;
      applyDeepLinks();
      suppressHash = false;
    },
    { signal: lifecycleSignal },
  );

  // Keep the gentle wobble from being a background CPU drain: stop the sim while
  // the tab is hidden, resume when it returns. Also honor a live change to the
  // reduced-motion preference. In a dag mode the sim stays stopped (no wobble).
  // startMotion() re-arms the motion loop on the same two triggers -
  // it self-checks motionEligible(), so it's a no-op when there's nothing to
  // animate or the tab/preference still says not to.
  document.addEventListener(
    "visibilitychange",
    () => {
      if (sim) {
        if (document.hidden) sim.stop();
        else if (!isDagMode()) sim.alphaTarget(idleAlpha()).restart();
      }
      if (!document.hidden) startMotion();
    },
    { signal: lifecycleSignal },
  );
  reducedMotion.addEventListener(
    "change",
    () => {
      if (sim && !isDagMode()) sim.alphaTarget(idleAlpha()).restart();
      startMotion();
    },
    { signal: lifecycleSignal },
  );

  // Wire the layout toggle group: each [data-layout] button switches directly
  // to its mode (disabled buttons - see layoutBlockedReason/syncLayoutToggle - are
  // inert; the browser already blocks their click, this guard is defensive).
  document.querySelectorAll<HTMLButtonElement>("[data-layout]").forEach((btn) => {
    btn.addEventListener("click", () => {
      if (btn.disabled) return;
      const mode = btn.dataset.layout;
      if (!mode || !isLayoutMode(mode)) return;
      layoutPickedByHand = true;
      switchLayout(mode);
    });
  });

  // Wire the graph-source toggle group: each [data-graphkind] button switches
  // between the build and knowledge graphs (live-only; see switchGraphKind).
  document.querySelectorAll<HTMLButtonElement>("[data-graphkind]").forEach((btn) => {
    btn.addEventListener("click", () => {
      if (btn.disabled) return;
      const kind = btn.dataset.graphkind;
      if (kind === "targets" || kind === "knowledge") switchGraphKind(kind);
    });
  });
  syncGraphKindToggle();

  // The Ask panel's "What's around this?" chip: jump straight to radial when a
  // node is already selected/focused, else enter a one-shot pick mode
  // (pendingRadialPick) that switchLayout("radial")s on the next selectNode
  // with an id (see selectNode).
  document.querySelectorAll<HTMLElement>("[data-radialpick]").forEach((b) => {
    b.addEventListener("click", () => {
      if (selected || focusId) {
        switchLayout("radial");
      } else {
        pendingRadialPick = true;
        setStatus("Click a node to center the radial view.");
      }
    });
  });

  // Wire the live-mode "Remember this workspace" checkbox.
  const rememberCb = el("live-remember-cb") as HTMLInputElement | null;
  if (rememberCb) {
    rememberCb.checked = isRemembered();
    rememberCb.addEventListener("change", () => {
      setRemembered(rememberCb.checked);
    });
  }
  // Show the remember row when in live mode.
  if (liveHost) {
    const rememberRow = el("live-remember-row");
    if (rememberRow) rememberRow.hidden = false;
  }
}

// bootLive: live-mode boot path. Fetches skeleton, wires SSE, then returns.
// Returns true if live mode connected, false to fall through to normal load.
async function bootLive() {
  const params = hashParams();
  // A static graph was explicitly requested (#data/#src): never take over the live path, so those
  // offline links keep working even when a default daemon is configured.
  if (params.data || params.src) return false;

  // The graph is a SEPARATE bundle from the shell, so the shell's adoptDaemonOrigin() does not
  // set THIS bundle's own-origin flag. Run it here too so a daemon-origin link from `magus graph export --open
  // --follow` (which carries a #token but no #port) is recognized as own-origin and daemonAttach adopts
  // location.host. It is a no-op for a #port attach (which needs no origin adoption) and for a cold,
  // token-less visit. The shell may have already stripped the #token from the URL; getLiveToken() reads
  // the stashed copy, so adoption still fires.
  adoptDaemonOrigin();

  // Explicit-attach only: a #port link, or the daemon-origin/shared console. A mere configured default
  // must not force the explorer into live mode - a cold visit shows the static empty state instead.
  const host = daemonAttach(params);
  if (!host) return false;

  liveHost = host;
  liveFlavor = params.flavor || null;

  // Consume and store the token (strips it from the URL fragment).
  if (params.token) {
    consumeLiveToken(params);
  }
  liveToken = getLiveToken();
  if (!liveToken) {
    setStatus(
      "live mode: no token found. Re-run magus graph export --open --follow to get a fresh link.",
      true,
    );
    document.body.classList.add("graph-empty");
    return true;
  }

  // Skeleton-first: fetch ?level=projects first (KBs at any scale).
  setStatus("Connecting to live workspace...");
  try {
    const skeletonUrl = "http://" + liveHost + "/api/v1/graph?level=projects";
    const skeletonResp = await fetch(skeletonUrl, {
      headers: authHeaders(liveToken),
    });
    if (!skeletonResp.ok) throw new Error("HTTP " + skeletonResp.status);
    liveETag = skeletonResp.headers.get("ETag") || null;
    liveGraphQuery = "?level=projects";
    const skeletonData = await skeletonResp.json();

    // Fetch StatusService GetStatus for workspace name and pool info.
    await fetchLiveStatus();
    updateLiveBadge();

    // Render the skeleton immediately.
    const flavor = flavorOf(skeletonData);
    graphFlavor = flavor;
    let rawForPrepare = skeletonData;
    if (flavor === "targets") {
      const nl = targetGraphToNodeLink(skeletonData);
      rawForPrepare = { nodes: nl.nodes, links: nl.links };
    }
    graph = prepareGraph(rawForPrepare);

    // Check node_count: full-fetch when skeleton is small enough to be manageable.
    const nodeCount = skeletonData.node_count || graph.nodes.length;
    if (nodeCount < 20000) {
      const fullQuery = liveFlavor === "targets" ? "?flavor=targets" : "";
      const fullUrl = "http://" + liveHost + "/api/v1/graph" + fullQuery;
      const fullResp = await fetch(fullUrl, { headers: authHeaders(liveToken) });
      if (fullResp.ok) {
        liveETag = fullResp.headers.get("ETag") || null;
        liveGraphQuery = fullQuery;
        const fullData = await fullResp.json();
        const ff = flavorOf(fullData);
        graphFlavor = ff;
        let rr = fullData;
        if (ff === "targets") {
          const nl = targetGraphToNodeLink(fullData);
          rr = { nodes: nl.nodes, links: nl.links };
        }
        graph = prepareGraph(rr);
      }
    }

    // Determine projection.
    const hasFragmentDirective = !!(params.view || params.q || params.node);
    computeDefaultProjection(hasFragmentDirective);

    const unfoldBtnLive = el("projection-unfold-btn");
    if (unfoldBtnLive) unfoldBtnLive.hidden = projectionUnfolded;
    if (!projectionUnfolded) updateProjectionStatus();
    else setStatus("live workspace connected");

    renderLegend();
    renderList();

    applyLayoutAndSimulation(params.layout, graphFlavor);
    parkHiddenNodes();
    finishInteractiveSetup();

    // Connect SSE for live updates.
    liveConnect();

    // Wire all common event listeners.
    bootWireEvents();
    return true;
  } catch (e) {
    setStatus(
      "live mode: could not connect to daemon at " +
        liveHost +
        ": " +
        errMessage(e) +
        ". Start it with: magus server start",
      true,
    );
    liveHost = null;
    liveToken = null;
    return false; // fall through to normal load
  }
}

// deactivate tears down everything with a lifetime when the console unmounts a graph tab or pane: it
// stops the force simulation (its rAF wobble is the main background CPU drain), cancels the
// motion loop and clears its state, aborts a live SSE stream and cancels its reconnect timer,
// disconnects the stage ResizeObserver and the theme MutationObserver, and removes the
// window/document lifecycle listeners (via the one AbortController). Idempotent. The standalone
// page never calls it (the graph lives for the page's lifetime); the console's graph PageModule
// calls it on deactivate.
// setVisible is the console's surface contract (page.ts). Here it is not a formality: the force
// simulation decays toward a small non-zero floor so the drawing keeps gently moving, which
// deactivate() below calls "the main background CPU drain" - and until now only CLOSING the tab
// stopped it. A backgrounded graph went on ticking and repainting a canvas nobody could see.
//
// Stopped rather than throttled, and restarted at the same idle floor on return, so what comes back
// is the layout the reader left rather than one that drifted while they were elsewhere.
export function setVisible(visible: boolean): void {
  surfaceVisible = visible;
  if (visible) {
    if (sim) sim.alphaTarget(idleAlpha()).restart();
    updateLiveBadge();
    if (liveRefreshPending) {
      liveRefreshPending = false;
      liveRefetchGraph();
      fetchLiveStatus();
    }
  } else if (sim) {
    sim.stop();
  }
}

export function deactivate(): void {
  // Forget the selected node: this module is a singleton the console re-activates on reopen, so a
  // stale label left here would name the reopened tab after a node it is no longer showing.
  docTitle.set(null);
  if (sim) sim?.stop();
  if (motionRaf) {
    cancelAnimationFrame(motionRaf);
    motionRaf = 0;
  }
  resetMotion();
  flowOn = false;
  pulsesPending = false;
  if (liveSseAbort) {
    liveSseAbort.abort();
    liveSseAbort = null;
  }
  if (liveReconnectTimer) {
    clearTimeout(liveReconnectTimer);
    liveReconnectTimer = null;
  }
  if (stageResizeObserver) {
    stageResizeObserver.disconnect();
    stageResizeObserver = null;
  }
  if (themeObserver) {
    themeObserver.disconnect();
    themeObserver = null;
  }
  if (lifecycleAbort) {
    lifecycleAbort.abort();
    lifecycleAbort = null;
  }
  uninstallKeys?.();
  uninstallKeys = null;
}

// Standalone auto-boot: only when the scaffold is already in the document at load. In the console the
// scaffold is injected into a host AFTER this module imports, so the console calls activate() itself.
if (document.getElementById("graph-canvas")) activate();
