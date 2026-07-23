import { must, errMessage } from "../../lib/guards";
// main.ts - the /graph/ page's interactive knowledge-graph view.
//
// The page is DATA-AGNOSTIC (like /playground): it renders whatever node-link
// graph it is handed, in priority order:
//   1. a `#data=` URL fragment (gzip + base64url of the JSON) - the private path
//      written by `magus graph open`. A fragment is never sent in an HTTP request,
//      so the user's graph never leaves their machine.
//   2. a `#src=` address to fetch the JSON from - a `magus graph open --serve`
//      loopback (127.0.0.1) or any CORS-enabled URL (e.g. a committed graph.json's
//      raw link). The same `#src=` the playground uses.
//   3. a file the visitor drops or picks (a graph.json they exported themselves).
//   4. the site's own committed demo graph at ./graph.json (the magus repo's
//      graph), shown to a plain visitor.
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
  type ForceManyBody,
  type ForceLink,
  type ForceX,
  type ForceY,
} from "d3-force";
import { zoom as d3zoom, zoomIdentity, type ZoomBehavior } from "d3-zoom";
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
  fetchSSE,
  authHeaders,
  isRemembered,
  setRemembered,
  wantsDemo,
  createDaemonTransport,
} from "../../lib/daemon";
import { createClient } from "@connectrpc/connect";
import { StatusService } from "../../gen/magus/status/v1/status_pb";
import {
  type GLink,
  type GNode,
  type GraphFlavor,
  type GraphPayload,
  type TargetGraphOutput,
  endpointId,
} from "./types.js";
import { LAYERED_COL_W, LAYERED_MAX, layoutLayered, layoutWaves } from "./layout.js";
import { CARD_COL_W, drawCard, measureCards } from "./cards.js";
import { RADIAL_MAX_RINGS, RADIAL_RING_R, layoutRadial } from "./radial.js";
import { nodeDurationMs, formatDuration } from "./duration.js";
import {
  setFlowEdges,
  setPulses,
  resetMotion,
  tick as particlesTick,
  type FlowEdge,
} from "./particles.js";
import { toMermaid } from "./mermaid.js";
import { detectFlavor, isTargetGraphOutput, targetGraphToNodeLink } from "./target-adapter.js";
import { installKeybindings, mergeKeymap, registerCommand, type Keymap } from "../commands";
import { wireToolbarOverflow } from "../toolbar";
import { persisted } from "../../lib/persist";
import { attachHelpPopover } from "../../ui/help-popover";

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
  "spell",
  "op",
  "charm",
  "target",
  "module",
  "method",
  "import",
  "function",
  "file",
  "doc",
  "rationale",
  "diagnostic",
  "symbol",
];

// Relations, for grouping edges in the explain card. Order = display order.
const RELATIONS = [
  "depends_on",
  "contains",
  "imports",
  "calls",
  "uses",
  "references",
  "documents",
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

// Cards render for the targets flavor in the DAG-shaped modes only; the
// knowledge constellation keeps circles (density makes cards unreadable).
// Radial also stays circles for targets - rings are round, wide cards don't fit.
const cardsActive = () =>
  graphFlavor === "targets" && (layoutMode === "layered" || layoutMode === "waves");

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

// ---- Phase 5: question-first views -----------------------------------------
// Views answer developer questions with graph interactions. The active view is
// one of: null (default projection), "blast", "trace", "critical", "hubs",
// "orphans", "cycles". "affected" is Phase 9 (disabled until live).
let activeView: string | null = null; // null | "blast" | "trace" | "hubs" | "orphans" | "critical" | "cycles" | "affected"
let viewNode: string | null = null; // primary node id for blast/trace
let viewNodeTo: string | null = null; // secondary node id for trace
// The default projection shows project-level nodes only on first load.
// "unfolded" = true after user expands (or activates a view/query).
let projectionUnfolded = false;
// Set of node ids visible in the current projection (null = all).
let projectionSet: Set<string> | null = null;

// ---- Phase 9: live mode state ----------------------------------------------
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

// Teardown handles for deactivate() (the console unmounting a graph tab/pane): the stage ResizeObserver
// and one AbortController whose signal wires every window/document lifecycle listener, so a single
// abort() removes them all. Without teardown the force simulation, its rAF, the observer, and these
// listeners would keep running in the background after the graph closes.
let stageResizeObserver: ResizeObserver | null = null;
let themeObserver: MutationObserver | null = null;
let lifecycleAbort: AbortController | null = null;

// The graph stays gently "alive": the simulation never fully cools, so nodes
// keep drifting (the Obsidian-like wobble). Disabled under prefers-reduced-motion,
// and paused when the tab is hidden (see boot) so it isn't a background CPU drain.
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
const idleAlpha = () => (reducedMotion.matches ? 0 : 0.006);

// ---- Phase 6: motion layer (flow particles + live recency pulses) ---------
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

function hashParams(): Record<string, string> {
  const h = location.hash.replace(/^#/, "");
  const out: Record<string, string> = {};
  for (const part of h.split("&")) {
    if (!part) continue;
    const eq = part.indexOf("=");
    // Keep a bare token (no "=") with an empty value, matching lib/daemon's parseHash,
    // so the shared `#demo` fragment (which has no "=") is detected by wantsDemo.
    if (eq < 0) {
      out[part] = "";
      continue;
    }
    out[part.slice(0, eq)] = decodeURIComponent(part.slice(eq + 1));
  }
  return out;
}

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
  // #src= fetches the JSON from an address: a loopback server (`magus graph open
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
      if (loopback) hint = " Is `magus graph open --serve` still running?";
      else if (localhostHost)
        hint =
          " The policy allows 127.0.0.1/[::1], not the `localhost` hostname - use `magus graph open --serve` or edit the URL to use 127.0.0.1.";
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
      setStatus("Loading the magus demo graph...");
      // Resolve graph.json relative to THIS bundle (gen/console/graph/), not the document: standalone
      // the two share a directory, but the console mounts this surface into a page at a different path,
      // where a document-relative "./graph.json" would miss. import.meta.url makes both paths work.
      const r = await fetch(new URL("./graph.json", import.meta.url));
      if (!r.ok) throw new Error("HTTP " + r.status);
      return { data: await r.json(), source: "demo" };
    } catch (e) {
      setStatus("Could not load the demo graph (" + errMessage(e) + ").", true);
    }
  }
  // No usable fragment: DON'T auto-fetch the demo (that download is wasted on a cold visit).
  // Return an empty graph so boot runs its full setup (interactions wired, canvas ready); boot
  // then shows the intuitive empty state, and the demo loads only when asked (loadDemoGraph).
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

// applyLayoutedMode: switch to layered layout for the visible node/link set.
// Returns false (with a status message) when the scale guard fires.
// Stops the force simulation so no ticks disturb the fixed positions.
function applyLayeredMode() {
  const visNodes = matchSet ? graph.nodes.filter((n) => must(matchSet).has(n.id)) : graph.nodes;
  if (visNodes.length > LAYERED_MAX) {
    setStatus(
      "layered layout is capped at 500 nodes - narrow with a query or the local graph (the CLI applies the same rule to -o mermaid)",
      true,
    );
    return false;
  }
  if (sim) {
    sim?.stop();
  }
  if (cardsActive()) {
    measureCards(ctx, visNodes, (theme ?? readTheme()).font);
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
    setStatus(
      "waves layout is capped at 500 nodes - narrow with a query or the local graph",
      true,
    );
    wavesMeta = null;
    return false;
  }
  if (sim) {
    sim?.stop();
  }
  if (cardsActive()) {
    measureCards(ctx, visNodes, (theme ?? readTheme()).font);
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
// off-canvas (fx/fy = -1e6, the same convention parkHiddenNodes uses) so they
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
      n.fx = -1e6;
      n.fy = -1e6;
      n.x = -1e6;
      n.y = -1e6;
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
  // Frame the freshly-placed rings. Radial centers on world origin, so without
  // this the camera stays wherever the previous layout left it and the rings
  // render off-screen (fitView calls draw()).
  fitView(matchSet);
  return true;
}

// layoutBlockedReason says whether a mode can run right now; the returned string is
// the reason to show in the button title when it cannot.
function layoutBlockedReason(mode: LayoutMode): string | null {
  if (mode === "waves" || mode === "layered") {
    const visCount = (matchSet ? matchSet.size : graph?.nodes.length) ?? 0;
    if (visCount > LAYERED_MAX) return "capped at 500 visible nodes - narrow with a query first";
  }
  if (mode === "radial" && !selected && !focusId) return "select a node first";
  return null;
}

// The layout toggle group's per-mode titles when available (overridden by
// layoutBlockedReason's reason string when not). Mirrors scaffold.html's static
// title attributes so the initial render and syncLayoutToggle agree.
const LAYOUT_TITLES: Record<LayoutMode, string> = {
  force:
    "Free-floating physics layout - clusters and highly-connected hubs pop out. Best for exploring the whole graph.",
  layered:
    "Left-to-right dependency flow - what depends on what, arranged in tiers. Best for reading direction.",
  waves:
    "Build order - each column is a set of targets magus can run in parallel. Best for seeing what runs when.",
  radial: "Rings around one node by distance - its neighborhood at a glance. Pick a node first.",
};
// Cycle order for the graph.layout command / l key: skips modes layoutBlockedReason rejects.
const LAYOUT_ORDER: LayoutMode[] = ["force", "layered", "waves", "radial"];
function cycleLayout() {
  const start = LAYOUT_ORDER.indexOf(layoutMode);
  for (let i = 1; i <= LAYOUT_ORDER.length; i++) {
    const next = LAYOUT_ORDER[(start + i) % LAYOUT_ORDER.length];
    if (!layoutBlockedReason(next)) {
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
    // Leaving radial: release whatever it parked off-canvas so the next
    // layout can place those nodes again. Also reset x/y away from the -1e6
    // parking spot (fitView(null) after this would otherwise include it in
    // the bounds and collapse zoom to k=0.1) and clear matchSet, so the next
    // layout shows the full graph instead of radial's placed set - the same
    // recovery the other park-release sites use.
    for (const n of graph.nodes) {
      if (n.fx === -1e6) {
        n.fx = null;
        n.fy = null;
        n.x = 0;
        n.y = 0;
      }
    }
    radialCenter = null;
    radialRings = null;
    matchSet = null;
  }
  layoutMode = mode;
  if (mode !== "waves") wavesMeta = null;
  syncLayoutToggle();
  updateHash();

  if (mode === "layered" || mode === "waves") {
    const ok = mode === "waves" ? applyWavesMode() : applyLayeredMode();
    if (!ok) {
      // Scale guard fired: revert to force mode.
      layoutMode = "force";
      wavesMeta = null;
      syncLayoutToggle();
      // Clear fixed positions so the sim can move nodes, and any routes from
      // the dag pass so force mode never draws a stale curve.
      for (const n of graph.nodes) {
        n.fx = null;
        n.fy = null;
      }
      for (const e of graph.links) delete e.points;
      if (sim) {
        sim.alpha(0.5).restart();
      } else {
        startSimulation();
      }
      // Don't write layout=<mode> to the hash.
      updateHash();
      draw();
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
  } else if (mode === "radial") {
    applyRadialMode();
  } else {
    // Force mode: clear fixed positions so the simulation takes over, and any
    // edge routes left over from a dag pass so force mode never draws a stale
    // curve.
    for (const n of graph.nodes) {
      n.fx = null;
      n.fy = null;
    }
    for (const e of graph.links) delete e.points;
    if (sim) {
      sim.alpha(0.5).restart();
    } else {
      startSimulation();
    }
    draw();
  }
}

// ---- simulation + canvas ---------------------------------------------------
function resizeCanvas() {
  const dpr = window.devicePixelRatio || 1;
  const rect = canvas.getBoundingClientRect();
  canvas.width = Math.round(rect.width * dpr);
  canvas.height = Math.round(rect.height * dpr);
  return { w: rect.width, h: rect.height, dpr };
}

function startSimulation() {
  if (sim) sim?.stop(); // stop the prior run (e.g. after loading a new file) - its timer would keep ticking
  const { w, h } = resizeCanvas();
  sim = forceSimulation<GNode, GLink>(graph.nodes)
    .force(
      "link",
      forceLink<GNode, GLink>(graph.links)
        .id((d) => d.id)
        .distance(40)
        .strength(0.4),
    )
    .force("charge", forceManyBody().strength(-60).distanceMax(400))
    .force("center", forceCenter(w / 2, h / 2))
    .force(
      "collide",
      forceCollide<GNode>().radius((d) => d.r + 2),
    )
    .force("x", forceX(w / 2).strength(0.02))
    .force("y", forceY(h / 2).strength(0.02))
    .alphaTarget(idleAlpha()) // decay toward a small floor, not 0, so it keeps gently moving
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
        if (transform.k >= 0.35) {
          ctx.fillStyle = th.muted;
          ctx.textAlign = "center";
          ctx.font = "10px " + th.font;
          ctx.fillText("wave " + i, waveX, minY - 28);
          ctx.font = "9px " + th.font;
          ctx.fillText(ids.length + " in parallel", waveX, minY - 16);
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

  const highlight = selected || hoverId;
  const near = neighbors(highlight);

  // Edges first, under the nodes. Dim edges not touching the highlighted node;
  // under a query filter (no selection), dim edges not between two matches, so the
  // matching subgraph stands out instead of a full bright web.
  // projectionActive: hide all non-projection nodes/edges from the draw.
  // (Same flag computed below for nodes; computed here first for edges.)
  // projectionActive is the projection id set when the default projection is showing, else null;
  // holding the Set (not a bool) lets its truthiness narrow away the nullable in the checks below.
  const projectionActive: Set<string> | null =
    !projectionUnfolded && projectionSet && !query && !focusId && !activeView
      ? projectionSet
      : null;
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
    if (highlight) active = s.id === highlight || t.id === highlight;
    else if (matchSet && !projectionActive) {
      // Under a query filter, draw ONLY edges between two matches - skipping the
      // rest keeps the matching subgraph clean instead of a faint full-web haze.
      if (!(matchSet.has(s.id) && matchSet.has(t.id))) continue;
      active = true;
    } else active = true;
    // Critical-path emphasis: an edge between two nodes both on the current
    // critical chain reads as the spine - thicker and at full alpha instead
    // of the default muted stroke.
    const criticalEdge =
      activeView === "critical" && !!matchSet && matchSet.has(s.id) && matchSet.has(t.id);
    ctx.strokeStyle = active ? th.muted : th.border;
    ctx.globalAlpha = criticalEdge ? 1 : active ? 0.55 : 0.1;
    ctx.lineWidth = criticalEdge ? 2.2 / transform.k : 0.6 / transform.k;
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
        ? [
            { x: sx, y: sy },
            { x: tx, y: ty },
            ...e.points,
          ].sort((a, b) => a.x - b.x)
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
        const nearest =
          routePts[0].x === tipAttachX ? routePts[1] : routePts[routePts.length - 2];
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
    if (highlight) alpha = n.id === highlight || (near && near.has(n.id)) ? 1 : 0.15;
    else if (matchSet && !projectionActive) alpha = matchSet.has(n.id) ? 1 : 0.12;
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
    ctx.beginPath();
    ctx.arc(n.x, n.y, n.r, 0, 2 * Math.PI);
    ctx.fillStyle = nodeColor;
    ctx.fill();
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

  // Phase 6 motion layer: flow particles along the active view's edges, and
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
    const show =
      n.id === highlight ||
      n.degree > 24 ||
      transform.k > 2.2 ||
      (layoutMode === "radial" && radialPlacedCount <= 60);
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
  if (cardsActive()) {
    for (let i = graph.nodes.length - 1; i >= 0; i--) {
      const n = graph.nodes[i];
      if (n.x == null || !n.w || !n.h) continue;
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
      hoverId = id;
      canvas.style.cursor = id ? "pointer" : "grab";
      draw();
    }
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
  const rels = [...byRel.keys()].sort((a, b) => RELATIONS.indexOf(a) - RELATIONS.indexOf(b));
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

function renderCard(id: string | null) {
  const n = id ? graph.byId.get(id) : null;
  if (!n) {
    cardEl.innerHTML = "";
    cardEl.hidden = true;
    document.body.toggleAttribute("data-has-card", false);
    return;
  }
  document.body.toggleAttribute("data-has-card", true);
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
  // Copy as Mermaid: copies the focus neighborhood (or current match set) as mermaid.
  // It lives in the card so it is immediately reachable when a node is selected. It is
  // a link-styled action (not a chunky button) so it sits quietly in the dense card and
  // doesn't compete with the canvas toolbar's Copy as Mermaid; still a <button> because
  // it acts (copies to clipboard) rather than navigates.
  html +=
    '<div class="console-graph-card__actions"><button type="button" class="console-graph-card__mermaidlink" title="Copy this node\'s neighborhood as a Mermaid diagram (double-click the node first to focus its local graph, then copy). Mirrors the CLI: magus graph export -o mermaid --select id"><span class="console-graph-card__copyglyph" aria-hidden="true">&#10697;</span> Copy as Mermaid</button></div>';
  cardEl.innerHTML = html;
  cardEl.hidden = false;
  cardEl
    .querySelectorAll<HTMLElement>(".console-graph-card__ref")
    .forEach((b) => b.addEventListener("click", () => selectNode(b.dataset.id ?? null, true)));
  const mermaidCardBtn = cardEl.querySelector<HTMLElement>(".console-graph-card__mermaidlink");
  if (mermaidCardBtn) mermaidCardBtn.addEventListener("click", copyAsMermaid);
}

// ---- selection, search, list, deep links -----------------------------------
function selectNode(id: string | null, center: boolean) {
  // Phase 5: default projection - clicking a project node in projection mode unfolds it.
  if (!projectionUnfolded && id && projectionSet && projectionSet.has(id)) {
    const n = graph.byId ? graph.byId.get(id) : null;
    if (n && n.kind === "project") {
      // Unfold this project: show its contains neighborhood.
      projectionUnfolded = true;
      projectionSet = null;
      // Release any nodes that were parked off-screen by the projection.
      for (const nd of graph.nodes) {
        if (nd.fx === -1e6) {
          nd.fx = null;
          nd.fy = null;
        }
      }
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
      const btn = el("projection-unfold-btn");
      if (btn) btn.hidden = true;
      setStatus("Showing " + id + " neighborhood. Press Esc or Show full graph to see everything.");
      renderList();
      if (matchSet.size) fitView(matchSet);
      updateHash();
      draw();
      return;
    }
  }

  // Phase 5: blast view picking mode - clicking a node activates blast on it.
  if (activeView === "blast" && id && !viewNode) {
    activateView("blast", id);
    return;
  }

  // Phase 5: trace view picking mode - two-click flow.
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
  const { w, h } = resizeCanvas();
  transform = zoomIdentity
    .translate(w / 2 - n.x * transform.k, h / 2 - n.y * transform.k)
    .scale(transform.k);
  // Drive the REAL zoom behavior (not a throwaway d3zoom()) so a later pan/zoom
  // continues from here instead of snapping back to a stale internal transform.
  select(canvas).call(zoomBehavior.transform, transform);
}

// fitView frames a set of nodes (or all when ids is null) in the viewport - the
// zoom-to-fit / reset-view action. Reuses the shared zoomBehavior + transform.
function fitView(ids: Set<string> | null) {
  const pts = graph.nodes.filter((n) => n.x != null && (!ids || ids.has(n.id)));
  if (!pts.length || !zoomBehavior) return;
  let minX = Infinity,
    minY = Infinity,
    maxX = -Infinity,
    maxY = -Infinity;
  const cards = cardsActive();
  for (const n of pts) {
    const hw = cards && n.w ? n.w / 2 : n.r;
    const hh = cards && n.w ? n.h! / 2 : n.r;
    minX = Math.min(minX, n.x - hw);
    maxX = Math.max(maxX, n.x + hw);
    minY = Math.min(minY, n.y - hh);
    maxY = Math.max(maxY, n.y + hh);
  }
  const { w, h } = resizeCanvas();
  const pad = 48;
  const k = Math.max(
    0.1,
    Math.min(8, Math.min((w - 2 * pad) / (maxX - minX || 1), (h - 2 * pad) / (maxY - minY || 1))),
  );
  const cx = (minX + maxX) / 2,
    cy = (minY + maxY) / 2;
  transform = zoomIdentity.translate(w / 2 - cx * k, h / 2 - cy * k).scale(k);
  select(canvas).call(zoomBehavior.transform, transform);
  draw();
}

// focusNode builds a LOCAL graph around a node (Obsidian's local view): the node
// plus everything within `depth` hops become the match set, so the existing
// dim-non-matches / hide-outside-edges rendering isolates the neighborhood. It
// also selects the node (explain card) and fits the view.
function focusNode(id: string, depth: number) {
  const focusNodeObj = graph.byId.get(id);
  if (!focusNodeObj) return;
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
      " - " +
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

// applyLens is the legacy entry point for [data-lens] clicks; now delegates to
// activateView so the view system handles state/hash/CLI idiom uniformly.
function applyLens(name: string) {
  activateView(name === "hubs" ? "hubs" : "orphans");
}

// syncConditionalViews shows or hides the "What's slow?" (critical) view button
// based on whether the current graph has DurationMs timing data. Called after
// each graph load (boot and replaceGraph) so the button tracks the data.
function syncConditionalViews() {
  graphHasDurations = !!graph && graph.nodes.some((n) => nodeDurationMs(n) > 0);
  document.querySelectorAll<HTMLElement>("[data-view='critical']").forEach((btn) => {
    btn.toggleAttribute("data-conditional", !graphHasDurations);
  });
  // The "Color by duration" preset shares the same conditional as the
  // critical-path view: both need timing data to mean anything. It is a PF
  // button, not a `.console-graph-views__chip`, so graph.css has no
  // `[data-conditional]` display rule for it - set `hidden` directly (the
  // global `[hidden] { display: none !important; }` override covers it),
  // keeping `data-conditional` too as the same semantic marker the chip uses.
  document.querySelectorAll<HTMLElement>("[data-preset='duration']").forEach((btn) => {
    btn.toggleAttribute("data-conditional", !graphHasDurations);
    btn.hidden = !graphHasDurations;
  });
  // The "What runs in parallel?" chip only makes sense for target graphs.
  document.querySelectorAll<HTMLElement>("[data-layoutjump]").forEach((btn) => {
    btn.hidden = graphFlavor !== "targets";
  });
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
// Fidelity contract (Phase 5): every field accepted here is also accepted by the
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
        node.id.toLowerCase() === v;
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
  const terms = query ? parseQuery(query) : [];
  if (!terms.length) {
    matchSet = null;
  } else {
    if (!graph.relIndex) graph.relIndex = relationIndex();
    matchSet = new Set();
    for (const n of graph.nodes) {
      if (terms.every((t) => termMatches(n, t))) matchSet.add(n.id);
    }
    setListExpanded(true); // a query reveals its matches
  }
  renderList();
  updateHash();
  syncLayoutToggle(); // availability tracks matchSet size
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
      // Clear pinned positions so the force sim can move all nodes.
      for (const n of graph.nodes) {
        n.fx = null;
        n.fy = null;
      }
      if (sim) sim?.alpha(0.3).restart();
    }
    return;
  }
  draw();
}

// The node cloud is collapsed by default (canvas-first on load); a query, or the
// count toggle, reveals it.
let listExpanded = false;
// Mobile only (graph.css scopes #node-list's position:fixed to the same breakpoint): the node
// cloud overlays the canvas instead of pushing it down in-flow, since the stacked sidebar has
// no headroom to push into without shoving the canvas off-screen. Desktop's #node-list keeps
// its plain in-flow disclosure, so this query gates the JS half of that split.
const mobileListQuery = matchMedia("(max-width: 900px)");
let overlayResizeWired = false; // guards the resize listener in bootWireEvents against a second wiring
// Places the fixed-position node-list panel directly under #list-toggle, clamped into the
// viewport - the same placement idiom as help-popover.ts's place(). Re-run on open and on
// resize/orientation change so a rotated phone doesn't leave the panel stranded.
function positionNodeListOverlay() {
  if (!mobileListQuery.matches) return;
  const btn = el("list-toggle");
  if (!btn) return;
  const r = btn.getBoundingClientRect();
  const margin = 8;
  const w = listEl.getBoundingClientRect().width || 320;
  let left = r.left;
  if (left + w > window.innerWidth - margin) left = window.innerWidth - margin - w;
  if (left < margin) left = margin;
  listEl.style.left = left + "px";
  listEl.style.top = r.bottom + 6 + "px";
}
function setListExpanded(v: boolean) {
  listExpanded = v;
  listEl.hidden = !v;
  const btn = el("list-toggle");
  if (btn) btn.setAttribute("aria-expanded", v ? "true" : "false");
  if (v) positionNodeListOverlay();
}

// The node list is the accessible twin of the canvas: it always reflects the
// current query (or the highest-degree nodes when there is no query).
function renderList() {
  const ms = matchSet;
  const pool = ms ? graph.nodes.filter((n) => ms.has(n.id)) : graph.nodes.slice();
  pool.sort((a, b) => b.degree - a.degree || a.label.localeCompare(b.label));
  const shown = pool.slice(0, 300);
  countEl.textContent = matchSet
    ? matchSet.size + " match" + (matchSet.size === 1 ? "" : "es")
    : graph.nodes.length +
      " node" +
      (graph.nodes.length === 1 ? "" : "s") +
      ", " +
      graph.links.length +
      " edge" +
      (graph.links.length === 1 ? "" : "s");
  // Compact rows: a kind-colored dot (keyed to the legend) + the label. The kind
  // name lives in the title tooltip rather than a column, to keep rows dense.
  listEl.innerHTML = shown
    .map(
      (n) =>
        '<li><button type="button" class="console-graph-nodelist__pill" data-id="' +
        escapeHtml(n.id) +
        '"' +
        ' title="' +
        escapeHtml(n.kind + " · " + n.label) +
        '"' +
        (n.id === selected ? ' aria-current="true"' : "") +
        ">" +
        '<span class="console-graph-kinddot" data-kind="' +
        escapeHtml(n.kind) +
        '"></span>' +
        '<span class="console-graph-nodelist__label">' +
        escapeHtml(n.label) +
        "</span>" +
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

function renderLegend() {
  const counts = new Map();
  for (const n of graph.nodes) counts.set(n.kind, (counts.get(n.kind) || 0) + 1);
  // Each legend row is a button that filters to kind:<k> (the CLI query it maps to),
  // so clicking a color isolates that kind - a quick, Obsidian-style filter.
  legendEl.innerHTML = KINDS.filter((k) => counts.has(k))
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

// Reflect selection, query, layout mode, active view, and color preset in the
// hash WITHOUT clobbering a #data= fragment (round-tripping the whole graph
// through history on every click would break the private-data contract).
let suppressHash = false;
function updateHash() {
  if (suppressHash) return;
  const params = hashParams();
  if (params.data || params.src || params.port !== undefined) return; // keep fragment data/loopback/attach links intact
  const parts = [];
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

function applyDeepLinks() {
  const params = hashParams();
  // Restore view state: #view=<id>&node=<id>[&to=<id>]
  const validViews = ["blast", "trace", "critical", "hubs", "orphans", "cycles"];
  if (params.view && validViews.includes(params.view)) {
    projectionUnfolded = true; // views show the full graph
    activateView(params.view, params.node || null, params.to || null);
    return; // view takes precedence over q/node
  }
  if (params.q) {
    searchEl.value = params.q;
    applyQuery(params.q);
  }
  if (params.node && graph.byId.has(params.node)) selectNode(params.node, true);
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
  // A graph is now loaded, so the "Ask" panel (a <details> collapsed while nothing is
  // loaded) is worth opening - the questions operate on the loaded graph.
  const askPanel = el("ask-panel") as HTMLDetailsElement | null;
  if (askPanel) askPanel.open = true;
  // Detect and adapt flavor before prepareGraph, same as boot(). The knowledge
  // path is unchanged; the targets path is converted client-side.
  graphFlavor = detectFlavor(data);
  let raw: GraphPayload;
  if (isTargetGraphOutput(data)) {
    const nl = targetGraphToNodeLink(data);
    raw = { nodes: nl.nodes, links: nl.links };
    const nProjects = (data.projects || []).length;
    const nTargets = nl.nodes.filter((n) => n.kind === "target").length;
    statusMsg =
      "target graph - " +
      nProjects +
      " project" +
      (nProjects === 1 ? "" : "s") +
      " - " +
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
  focusId = null;
  matchSet = null;
  radialCenter = null;
  radialRings = null;
  pendingRadialPick = false;
  resetMotion(); // clears flow edges AND any recency pulse mid-flight from the graph being replaced
  flowOn = false;
  pulsesPending = false;
  if (searchEl) searchEl.value = "";
  // Reset Phase 5 view/projection state.
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
  // Apply projection: show only projects by default if the count is small.
  const ps = buildProjectionSet();
  if (ps) {
    projectionUnfolded = false;
    projectionSet = ps;
    matchSet = new Set(ps);
  } else projectionUnfolded = true;
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
  // Park hidden nodes after the sim is built (projection reduces the visible set).
  if (!projectionUnfolded && projectionSet) {
    for (const n of graph.nodes) {
      if (!projectionSet.has(n.id)) {
        n.fx = -1e6;
        n.fy = -1e6;
        n.x = -1e6;
        n.y = -1e6;
      }
    }
  }
  draw();
}

// syncLayoutToggle updates the layout toggle group's selected state, each mode
// button's disabled/title (from layoutBlockedReason), the force sliders'
// visibility, and the bottom-left stage mode indicator to match the current
// layoutMode WITHOUT switching the mode (used after loading a new graph where
// the mode is set directly, and called from selectNode/applyQuery so
// availability tracks state as it changes).
function syncLayoutToggle() {
  document.querySelectorAll<HTMLButtonElement>("[data-layout]").forEach((btn) => {
    const mode = btn.dataset.layout;
    if (!mode || !isLayoutMode(mode)) return;
    btn.classList.toggle("pf-m-selected", mode === layoutMode);
    const reason = layoutBlockedReason(mode);
    btn.disabled = !!reason;
    btn.title = reason ?? LAYOUT_TITLES[mode] ?? "";
  });
  const forceControls = document.querySelector<HTMLElement>(".console-graph-display__forces");
  if (forceControls) forceControls.hidden = layoutMode !== "force";
  const modeIndicator = el("graph-mode-indicator");
  if (modeIndicator) {
    modeIndicator.textContent = "mode: " + layoutMode[0].toUpperCase() + layoutMode.slice(1);
  }
}

// loadDemoGraph swaps the committed demo graph in place via renderLoadedGraph. NOT a page reload: the SPA
// router clobbers #demo on reboot, so the old reload path never reached the demo. Fetch stays click-lazy.
async function loadDemoGraph(): Promise<void> {
  setStatus("Loading the magus demo graph...");
  try {
    // Relative to THIS bundle (gen/graph/), not the document - the console mounts the surface elsewhere.
    const r = await fetch(new URL("./graph.json", import.meta.url));
    if (!r.ok) throw new Error("HTTP " + r.status);
    const data = await r.json();
    const empty = el("graph-empty-state");
    if (empty) empty.hidden = true;
    // Un-hiding un-collapses the canvas on a later frame; wait for real width or the sim centers on a
    // zero viewport and the graph lands cramped in a corner.
    await waitForCanvasWidth();
    renderLoadedGraph({ data, source: "demo" });
  } catch (e) {
    setStatus("Could not load the demo graph (" + errMessage(e) + ").", true);
  }
}

// waitForCanvasWidth resolves once the canvas has real width (its relayout landed), or after ~1s as a cap.
function waitForCanvasWidth(): Promise<void> {
  return new Promise((resolve) => {
    let tries = 0;
    const check = () => {
      if (canvas.clientWidth > 0 || ++tries > 60) resolve();
      else requestAnimationFrame(check);
    };
    check();
  });
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

// ---- Phase 5: default projection -------------------------------------------
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
  // Release all parked nodes so the force sim (or layered layout) can place them.
  if (graph) {
    for (const n of graph.nodes) {
      if (n.fx === -1e6) {
        n.fx = null;
        n.fy = null;
      }
    }
  }
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

// ---- Phase 5: views ---------------------------------------------------------
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
      const top = graph.nodes
        .slice()
        .sort((a, b) => b.degree - a.degree)
        .slice(0, 12);
      matchSet = new Set(top.map((n) => n.id));
      setStatus("What's a hub? The " + matchSet.size + " highest-degree nodes.");
      setResultLine("The " + matchSet.size + " most depended-on targets.");
      break;
    }
    case "orphans": {
      matchSet = new Set(graph.nodes.filter((n) => n.degree === 0).map((n) => n.id));
      setStatus(
        "What's dead? " +
          matchSet.size +
          " orphan node" +
          (matchSet.size === 1 ? "" : "s") +
          " with no edges.",
      );
      setResultLine(
        matchSet.size +
          " target" +
          (matchSet.size === 1 ? "" : "s") +
          " nothing depends on.",
      );
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
            " target(s) caught in a loop - a configuration error to fix.",
        );
        setResultLine(ids.size + " target" + (ids.size === 1 ? "" : "s") + " in circular dependencies.");
      } else {
        matchSet = null;
        setStatus("No circular dependencies - the dependency graph is acyclic.");
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
function askQuestion(view: string) {
  const want = preferredModeForView(view);
  if (want && want !== layoutMode && !layoutBlockedReason(want)) switchLayout(want);
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
        "affected view: requires live mode (magus graph open --live) with a computed diff.",
        true,
      );
      return;
    }
    activateView("affected");
    return;
  }
  activateView(view);
  if (view === "critical" && graphHasDurations && activePreset !== "duration") {
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

// ---- Phase 5: CLI idiom rendering -------------------------------------------
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

// ---- Phase 7: Copy as Mermaid (toMermaid lives in mermaid.ts) --------------

// Compute the node/link scope for "Copy as Mermaid": local-graph if a focus/ego
// view is active, else current query matches, else all nodes (refused if >150).
// Returns { nodes, links, refused } where refused is a plain-ASCII string or null.
function mermaidScope() {
  if (!graph) return { nodes: [], links: [], refused: "no graph loaded" };

  let nodeIds;
  if (focusId) {
    // Local/ego view is active: use the focus neighborhood.
    nodeIds = neighborhood(focusId, focusDepth);
  } else if (matchSet) {
    // Query filter or lens is active: use the match set.
    nodeIds = matchSet;
  } else {
    // No filter: check size guard.
    if (graph.nodes.length > 150) {
      return {
        nodes: [],
        links: [],
        refused: "narrow your focus first (double-click a node or add a query)",
      };
    }
    nodeIds = null; // all
  }

  const nodes = nodeIds ? graph.nodes.filter((n) => nodeIds.has(n.id)) : graph.nodes;
  if (nodes.length > 150) {
    return {
      nodes: [],
      links: [],
      refused: "narrow your focus first (double-click a node or add a query)",
    };
  }
  const nodeSet = new Set(nodes.map((n) => n.id));
  const links = graph.links.filter((e) => {
    const s = endpointId(e.source),
      t = endpointId(e.target);
    return nodeSet.has(s) && nodeSet.has(t);
  });
  return { nodes, links, refused: null };
}

// copyAsMermaid: compute scope, emit mermaid, copy to clipboard, set status.
function copyAsMermaid() {
  if (!navigator.clipboard) {
    setStatus("clipboard unavailable in this context", true);
    return;
  }
  const { nodes, links, refused } = mermaidScope();
  if (refused) {
    setStatus(refused, true);
    return;
  }
  const text = toMermaid(nodes, links, graphFlavor);
  navigator.clipboard
    .writeText("```mermaid\n" + text + "\n```")
    .then(() => {
      setStatus(
        "Mermaid diagram copied (" + nodes.length + " nodes) - paste into a GitHub comment or PR.",
      );
    })
    .catch((err) => {
      setStatus("Could not copy to clipboard: " + err.message, true);
    });
}

// Build the full CLI command string for copy-to-clipboard.
function viewCommandStr(name: string | null, nodeId?: string | null, nodeTo?: string | null) {
  switch (name) {
    case "blast":
      if (!nodeId) return "magus explain <node-id>";
      return "magus explain " + shellQuote(nodeId);
    case "trace":
      if (!nodeId || !nodeTo) return "magus path <a> <b>";
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

// ---- Phase 5: empty-state suggestions (chips) -------------------------------
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
      text: "See the build order - what runs in parallel?",
      action: () => switchLayout("waves"),
    });
  }

  // 1. Highest-degree node ("biggest hub").
  if (graph.nodes.length > 1 && chips.length < 3) {
    const top = graph.nodes.slice().sort((a, b) => b.degree - a.degree)[0];
    if (top && top.degree > 0) {
      chips.push({
        text: top.label + " is the biggest hub - what depends on it?",
        action: () => activateView("blast", top.id),
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
      text: "A dependency cycle was detected - trace its path?",
      action: src ? () => activateView("trace", src) : () => activateView("hubs"),
    });
  }

  // 3. Orphan count.
  const orphans = graph.nodes.filter((n) => n.degree === 0);
  if (orphans.length > 0 && chips.length < 3) {
    chips.push({
      text:
        orphans.length +
        " node" +
        (orphans.length === 1 ? "" : "s") +
        " with no edges - what's dead?",
      action: () => activateView("orphans"),
    });
  }

  if (!chips.length) {
    wrap.hidden = true;
    return;
  }
  wrap.hidden = false;
  wrap.innerHTML = chips
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

// ---- Phase 5: color preset lenses -------------------------------------------
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
  draw();
  updateHash();
}

// ---- Phase 9: live mode ----------------------------------------------------

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
      if (p.fx != null && p.fx !== -1e6) {
        n.fx = p.fx;
        n.fy = p.fy;
      }
    }
    // New nodes: the simulation will place them; no hint needed.
  }
}

// recomputeLiveMatchSet recomputes matchSet (and, for the default projection,
// projectionSet) against the CURRENT graph for whatever activeView/query/
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
      case "hubs": {
        const top = graph.nodes
          .slice()
          .sort((a, b) => b.degree - a.degree)
          .slice(0, 12);
        matchSet = new Set(top.map((n) => n.id));
        break;
      }
      case "orphans":
        matchSet = new Set(graph.nodes.filter((n) => n.degree === 0).map((n) => n.id));
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
  if (query) {
    const terms = parseQuery(query);
    if (!terms.length) {
      matchSet = null;
      return;
    }
    if (!graph.relIndex) graph.relIndex = relationIndex();
    matchSet = new Set();
    for (const n of graph.nodes) {
      if (terms.every((t) => termMatches(n, t))) matchSet.add(n.id);
    }
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
  const flavor = detectFlavor(data);
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
      setStatus("radial center no longer exists in the refreshed graph; showing the default layout.");
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
      liveRefetchGraph();
      fetchLiveStatus();
    },
  );
}

function showDisconnectBanner() {
  const banner = el("live-disconnect-banner");
  if (!banner) return;
  const now = new Date();
  const hhmm =
    now.getHours().toString().padStart(2, "0") + ":" + now.getMinutes().toString().padStart(2, "0");
  banner.textContent = "disconnected - showing workspace as of " + hhmm + ", reconnecting...";
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
  const conn = document.getElementById("console-conn");
  if (conn) {
    if (liveHost) {
      conn.textContent = liveConnected ? "connected" : "connecting...";
      conn.dataset.state = liveConnected ? "connected" : "connecting";
      delete conn.dataset.health;
    } else {
      conn.textContent = "not connected";
      conn.dataset.state = "none";
      delete conn.dataset.health;
    }
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
  wavesMeta = null;
  syncLayoutToggle();
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
        n.fx = -1e6;
        n.fy = -1e6;
        n.x = -1e6;
        n.y = -1e6;
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
  // Phase 5: emit empty-state suggestion chips.
  renderSuggestions();
}

// renderLoadedGraph runs boot's data-to-view pipeline (detect/prepare/project/status/layout/reveal),
// excluding the one-time interaction wiring, so the demo button can re-run it in place.
function renderLoadedGraph(loaded: { data: GraphPayload; source: string }): void {
  const flavor = detectFlavor(loaded.data);
  graphFlavor = flavor;
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
      "target graph - " +
      nProjects +
      " project" +
      (nProjects === 1 ? "" : "s") +
      " - " +
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
          ? "Your workspace graph - it never left your machine."
          : loaded.source === "loopback"
            ? "Your workspace graph, served over loopback - it never left your network."
            : "",
      );
  }

  renderLegend();
  renderList();

  const initialParams = hashParams();
  applyLayoutAndSimulation(initialParams.layout, flavor);
  parkHiddenNodes();

  // Wow reveal: once the cold force layout has spread out, frame the whole graph so it lands centered and
  // fully in view instead of cropped to a corner. Only for the default full-graph view - a deep link or
  // the perf-guard projection already frames its own subset, and layered layout is framed by applyLayeredMode.
  if (projectionUnfolded && !hasFragmentDirective && !isDagMode() && graph.nodes.length) {
    setTimeout(() => fitView(null), 700);
  }
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

  // Phase 9: attempt a live-mode connection on an explicit daemon attach (#port, or the
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

  // Empty state: nothing loaded (no #data/#src, no live attach), so show the prompt instead. The pipeline ran on
  // an empty graph, so interactions are wired; the demo loads via loadDemoGraph, a dropped file via
  // replaceGraph, both dismissing this.
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
  let queryTimer = 0;
  searchEl.addEventListener("input", () => {
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
  document.querySelectorAll<HTMLElement>(".console-graph-views__chip").forEach((b) => {
    b.addEventListener("click", () => {
      const v = b.dataset.view;
      if (!v) return;
      if ((b as HTMLButtonElement).disabled || b.hasAttribute("data-disabled")) return;
      if (activeView === v) {
        clearView();
        return;
      }
      askQuestion(v);
    });
  });

  // Wire the clear-view button.
  const clearViewBtn = el("clear-view-btn");
  if (clearViewBtn) clearViewBtn.addEventListener("click", clearView);

  // The count row toggles the (default-collapsed) node cloud.
  const listToggle = el("list-toggle");
  if (listToggle) listToggle.addEventListener("click", () => setListExpanded(!listExpanded));

  // Keep the mobile node-list overlay glued under its toggle across a resize/rotation. Wired
  // once (bootWireEvents can re-run from the live-mode path); the listener itself no-ops via
  // positionNodeListOverlay's own mobileListQuery/listExpanded guards, so a second registration
  // would just be redundant rather than harmful, but there's no reason to keep both around.
  if (!overlayResizeWired) {
    overlayResizeWired = true;
    window.addEventListener("resize", () => {
      if (listExpanded) positionNodeListOverlay();
    });
  }

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

  // Lenses (magus graph stats parity): hubs / orphans set the match set. The lens
  // buttons are marked by their data-lens hook (no presentational class of their own).
  document.querySelectorAll<HTMLElement>("[data-lens]").forEach((b) =>
    b.addEventListener("click", () => {
      const lens = b.dataset.lens;
      if (lens) applyLens(lens);
    }),
  );

  // Phase 5: color preset buttons.
  document.querySelectorAll<HTMLElement>(".console-graph-colorgroup__preset").forEach((b) => {
    b.addEventListener("click", () => {
      const preset = b.dataset.preset;
      if (preset) applyPreset(preset);
    });
  });

  // Live force sliders: adjust the running simulation and gently reheat.
  const wireForce = (id: string, apply: (v: number) => void) => {
    const input = el(id) as HTMLInputElement | null;
    if (!input) return;
    input.addEventListener("input", () => {
      if (sim) {
        apply(+input.value);
        sim?.alpha(0.3).restart();
      }
    });
  };
  // d3's untyped `force(name)` accessor returns the base Force; cast to the concrete
  // force type to reach its strength/distance setters. sim is non-null when wireForce
  // invokes these (it guards on it), so the optional chain never short-circuits.
  wireForce("force-charge", (v) =>
    (sim?.force("charge") as ForceManyBody<GNode> | undefined)?.strength(-v),
  );
  wireForce("force-link", (v) =>
    (sim?.force("link") as ForceLink<GNode, GLink> | undefined)?.distance(v),
  );
  wireForce("force-gravity", (v) => {
    (sim?.force("x") as ForceX<GNode> | undefined)?.strength(v / 100);
    (sim?.force("y") as ForceY<GNode> | undefined)?.strength(v / 100);
  });

  // Keyboard: Esc clears a focus/query; [ and ] shrink/grow the focus depth.
  document.addEventListener(
    "keydown",
    (e) => {
      if (e.key === "Escape") {
        clearFocusOrQuery();
        if (searchEl.blur) searchEl.blur();
        return;
      }
      if (e.target === searchEl) return; // don't hijack typing
      if (e.key === "[") changeFocusDepth(-1);
      else if (e.key === "]") changeFocusDepth(1);
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
  installKeybindings(() => mergeKeymap(GRAPH_KEYMAP, keymapCell.get()));

  // Query-syntax reference: each example runs itself in the filter (teach-by-doing).
  // Scope to [data-q] so the lens/add-group buttons (which share .console-graph-help__example for its
  // chip styling but carry no data-q) aren't wired as examples.
  document.querySelectorAll<HTMLElement>(".console-graph-help__example[data-q]").forEach((b) =>
    b.addEventListener("click", () => {
      const q = b.dataset.q ?? "";
      searchEl.value = q;
      applyQuery(q);
      searchEl.focus();
      must(document.querySelector<HTMLElement>(".console-graph-app")).scrollIntoView({
        behavior: "smooth",
        block: "nearest",
      });
    }),
  );

  // "Copy as Mermaid" toolbar button: emit the current scope as a mermaid diagram.
  const copyMermaidBtn = el("copy-mermaid-btn");
  if (copyMermaidBtn) copyMermaidBtn.addEventListener("click", copyAsMermaid);

  // "Open file" toolbar button proxies to the hidden <input type=file>.
  const openBtn = el("open-file-btn");
  if (openBtn && fileInput) openBtn.addEventListener("click", () => fileInput.click());
  if (fileInput) fileInput.addEventListener("change", () => readGraphFile(fileInput.files?.[0]));

  // "Explore the magus graph" fetches the committed demo graph.json on demand and renders it in place
  // (loadDemoGraph), dismissing the empty state.
  const demoExplore = el("demo-explore-btn");
  if (demoExplore) demoExplore.addEventListener("click", () => loadDemoGraph());

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
          sim.force("center", forceCenter(canvas.clientWidth / 2, canvas.clientHeight / 2));
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
  let t = 0;
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
        sim.force("center", forceCenter(canvas.clientWidth / 2, canvas.clientHeight / 2));
        sim.alpha(0.1).restart();
      }
      draw();
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
  // startMotion() re-arms the Phase 6 motion loop on the same two triggers -
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
      if (mode && isLayoutMode(mode)) switchLayout(mode);
    });
  });

  // The Ask panel's "What runs in parallel?" chip is a layout jump, not a view:
  // it carries no data-view attr, so the .console-graph-views__chip wiring
  // above skips it.
  document.querySelectorAll<HTMLElement>("[data-layoutjump]").forEach((b) => {
    b.addEventListener("click", () => {
      const mode = b.dataset.layoutjump;
      if (mode && isLayoutMode(mode)) switchLayout(mode);
    });
  });

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

  // Phase 9: wire the live-mode "Remember this workspace" checkbox.
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
      "live mode: no token found. Re-run magus graph open --live to get a fresh link.",
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
    const flavor = detectFlavor(skeletonData);
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
        const ff = detectFlavor(fullData);
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
// stops the force simulation (its rAF wobble is the main background CPU drain), cancels the Phase 6
// motion loop and clears its state, aborts a live SSE stream and cancels its reconnect timer,
// disconnects the stage ResizeObserver and the theme MutationObserver, and removes the
// window/document lifecycle listeners (via the one AbortController). Idempotent. The standalone
// page never calls it (the graph lives for the page's lifetime); the console's graph PageModule
// calls it on deactivate.
export function deactivate(): void {
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
}

// Standalone auto-boot: only when the scaffold is already in the document at load. In the console the
// scaffold is injected into a host AFTER this module imports, so the console calls activate() itself.
if (document.getElementById("graph-canvas")) activate();
