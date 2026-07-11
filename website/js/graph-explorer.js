// graph-explorer.js - the /graph/ page's interactive knowledge-graph view.
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
// it). Colors come from Pico CSS variables read off the live page, re-read on a
// theme toggle, exactly like js/mermaid.js. The canvas is progressive
// enhancement over a semantic node list; the explain card is plain HTML.
import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide, forceX, forceY } from "d3-force";
import { zoom as d3zoom, zoomIdentity } from "d3-zoom";
import { drag as d3drag } from "d3-drag";
import { select } from "d3-selection";
// The loopback lock, the shared bearer token, and the fetch-based SSE reader used to
// be copy-pasted into all three tool pages; they now live in one audited module.
// (The ConnectRPC transport this module also exports is tree-shaken out here - the
// graph explorer only uses these four primitives.)
import { validateLiveHost, consumeLiveToken, getLiveToken, fetchSSE } from "./lib/daemon";

// The node kinds the graph can emit. Each gets a stable legend color via a CSS
// custom property (--gk-<kind>) defined for both themes in graph.css, so the
// palette is themeable and read at render time. KINDS also fixes legend order
// (roughly: structure -> code -> docs -> diagnostics). `symbol` is the SCIP
// code-symbol kind introduced by `magus refs`; it lives in lazy @symbols shards
// and may appear in graphs exported with those shards loaded.
const KINDS = [
  "project", "spell", "op", "charm", "target",
  "module", "method", "import", "function",
  "file", "doc", "rationale", "diagnostic", "symbol",
];

// Relations, for grouping edges in the explain card. Order = display order.
const RELATIONS = ["depends_on", "contains", "imports", "calls", "uses", "references", "documents", "rationale_for"];

// ---- element handles (the DOM contract with graph.html) --------------------
const el = (id) => document.getElementById(id);
const canvas = el("graph-canvas");
const legendEl = el("graph-legend");
const searchEl = el("node-search");
const listEl = el("node-list");
const cardEl = el("explain-card");
const statusEl = el("graph-status");
const countEl = el("graph-count");
const fileInput = el("graph-file");

const root = document.documentElement;
const ctx = canvas.getContext("2d");

let graph = null;         // { nodes, links }
let sim = null;
let zoomBehavior = null;  // the ONE d3-zoom instance (shared so centerOn stays in sync)
let transform = zoomIdentity;
let selected = null;      // selected node id
let query = "";           // current search string (lowercased)
let matchSet = null;      // Set of node ids matching `query`/focus/lens, or null for "all"
let hoverId = null;
let focusId = null;       // node the local/focus graph is centered on, or null
let focusDepth = 2;       // hops included in the focus graph
// Layout mode: "force" (d3 simulation) or "layered" (deterministic Sugiyama DAG layout).
// Defaults are set per flavor after a graph loads; manual toggle is allowed and survives
// the URL fragment (#layout=force or #layout=layered). The scale guard refuses layered
// for more than 500 visible nodes.
let layoutMode = "force"; // "force" | "layered"
let graphFlavor = "knowledge"; // "knowledge" | "targets"; set in boot/replaceGraph

// ---- Phase 5: question-first views -----------------------------------------
// Views answer developer questions with graph interactions. The active view is
// one of: null (default projection), "blast", "trace", "critical", "hubs",
// "orphans". "affected" is Phase 9 (disabled). Max 7 total.
let activeView = null; // null | "blast" | "trace" | "hubs" | "orphans" | "critical"
let viewNode = null;   // primary node id for blast/trace
let viewNodeTo = null; // secondary node id for trace
// The default projection shows project-level nodes only on first load.
// "unfolded" = true after user expands (or activates a view/query).
let projectionUnfolded = false;
// Set of node ids visible in the current projection (null = all).
let projectionSet = null;

// ---- Phase 9: live mode state ----------------------------------------------
let liveHost = null;     // host:port string when in live mode, else null
let liveToken = null;    // bearer token for live mode
let liveETag = null;     // last ETag from the currently loaded graph variant, for If-None-Match
// liveGraphQuery is the exact /api/v1/graph query string ("", "?level=projects",
// or "?flavor=targets") of whichever variant is currently loaded. liveRefetchGraph
// MUST reuse this (with liveETag) rather than hardcoding a variant: sending one
// variant's ETag while requesting a different one makes the server 200 with the
// other variant's body, silently downgrading (or upgrading) what is on screen.
let liveGraphQuery = "";
let liveSseAbort = null; // AbortController for the SSE fetch
let liveReconnectTimer = null;
let liveReconnectDelay = 1000; // ms; doubles on each failure up to 30000
let liveWorkspaceName = null;  // workspace name from /api/v1/status, for badge
let liveConnected = false; // true while the SSE stream is open; drives the badge style
let liveFlavor = null;   // null (knowledge) or "targets"

// The graph stays gently "alive": the simulation never fully cools, so nodes
// keep drifting (the Obsidian-like wobble). Disabled under prefers-reduced-motion,
// and paused when the tab is hidden (see boot) so it isn't a background CPU drain.
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
const idleAlpha = () => (reducedMotion.matches ? 0 : 0.006);

// ---- theme / palette -------------------------------------------------------
// One computed-style read per repaint; pico() pulls a custom property with a
// fallback (mirrors js/mermaid.js). Colors are cached per repaint in `theme`.
let theme = null;
function readTheme() {
  const cs = getComputedStyle(root);
  const v = (name, fallback) => cs.getPropertyValue(name).trim() || fallback;
  const kindColor = {};
  for (const k of KINDS) kindColor[k] = v("--gk-" + k, "#888");
  theme = {
    bg: v("--pico-background-color", "#fff"),
    text: v("--pico-color", "#373c44"),
    muted: v("--pico-muted-color", "#646b79"),
    border: v("--pico-muted-border-color", "#dce3eb"),
    accent: v("--pico-primary", "#0172ad"),
    font: v("--pico-font-family", "system-ui, sans-serif"),
    kindColor,
  };
}

// ---- data loading ----------------------------------------------------------
// Decode a `#data=` fragment: base64url -> bytes -> gunzip -> JSON. Uses the
// browser's DecompressionStream (widely supported); the whole path is local, so
// nothing is fetched and nothing is sent.
async function decodeFragment(b64url) {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  const stream = new Response(bytes).body.pipeThrough(new DecompressionStream("gzip"));
  const text = await new Response(stream).text();
  return JSON.parse(text);
}

function hashParams() {
  const h = location.hash.replace(/^#/, "");
  const out = {};
  for (const part of h.split("&")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    out[part.slice(0, eq)] = decodeURIComponent(part.slice(eq + 1));
  }
  return out;
}

async function loadGraph() {
  const params = hashParams();
  if (params.data) {
    try {
      setStatus("Decoding local graph...");
      return { data: await decodeFragment(params.data), source: "local" };
    } catch (e) {
      setStatus("Could not decode the graph in the link (" + e.message + ").", true);
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
      else if (localhostHost) hint = " The policy allows 127.0.0.1/[::1], not the `localhost` hostname - use `magus graph open --serve` or edit the URL to use 127.0.0.1.";
      setStatus("Could not fetch the graph from that URL (" + e.message + ")." + hint, true);
    }
  }
  // Fetch the committed demo graph for the demo button (#demo) AND for any content deep link
  // (#view/#q/#node) - those reference graph content, and the only graph available without
  // #data/#src is the demo, so a shared "explore this view of the demo" link keeps working.
  // A BARE /graph/ (no directive at all) is the cold visit that gets the empty state instead,
  // deferring the graph.json download until the visitor asks. Loading via a reload into boot
  // (not an in-place swap) renders through boot's normal pipeline - projection, fit, interactions.
  if (params.demo || params.view || params.q || params.node) {
    try {
      setStatus("Loading the magus demo graph...");
      const r = await fetch("./graph.json");
      if (!r.ok) throw new Error("HTTP " + r.status);
      return { data: await r.json(), source: "demo" };
    } catch (e) {
      setStatus("Could not load the demo graph (" + e.message + ").", true);
    }
  }
  // No usable fragment: DON'T auto-fetch the demo (that download is wasted on a cold visit).
  // Return an empty graph so boot runs its full setup (interactions wired, canvas ready); boot
  // then shows the intuitive empty state, and the demo loads only when asked (loadDemoGraph).
  return { data: { nodes: [], links: [] }, source: "empty" };
}

function setStatus(msg, isError) {
  if (!statusEl) return;
  statusEl.textContent = msg;
  statusEl.classList.toggle("err", !!isError);
}

// ---- graph prep ------------------------------------------------------------
// Normalize the loaded JSON into d3-force's mutable shape and precompute degree
// (drives node radius) and adjacency (drives the explain card + neighbor
// highlight). Nodes/links carry id references in the JSON; d3-force's forceLink
// will replace link.source/target with the node objects in place.
function prepareGraph(raw) {
  const nodes = raw.nodes.map((n) => ({ ...n }));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const links = (raw.links || raw.edges || [])
    .filter((e) => byId.has(e.source) && byId.has(e.target))
    .map((e) => ({ ...e }));
  const degree = new Map();
  for (const n of nodes) degree.set(n.id, 0);
  for (const e of links) {
    degree.set(e.source, degree.get(e.source) + 1);
    degree.set(e.target, degree.get(e.target) + 1);
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

// Edges touching a node, split by direction, for the explain card.
function incidentEdges(id) {
  const out = [], inc = [];
  for (const e of graph.links) {
    const s = e.source.id || e.source;
    const t = e.target.id || e.target;
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
    const adj = new Map();
    const add = (a, b) => { let s = adj.get(a); if (!s) { s = new Set(); adj.set(a, s); } s.add(b); };
    for (const e of graph.links) {
      const s = e.source.id || e.source, t = e.target.id || e.target;
      add(s, t); add(t, s);
    }
    graph.adj = adj;
  }
  return graph.adj;
}
function neighbors(id) {
  return id ? adjacency().get(id) || null : null;
}

// neighborhood collects a node plus everything within `depth` hops - the node set
// for a local/focus graph (Obsidian's local view). Reuses the adjacency map.
function neighborhood(id, depth) {
  const set = new Set([id]);
  let frontier = [id];
  for (let d = 0; d < depth; d++) {
    const next = [];
    for (const nid of frontier) {
      for (const nb of adjacency().get(nid) || []) {
        if (!set.has(nb)) { set.add(nb); next.push(nb); }
      }
    }
    frontier = next;
  }
  return set;
}

// ---- layered DAG layout (Sugiyama-style, no deps, fully deterministic) ----
// layoutLayered assigns node.fx / node.fy for the visible subset.
// Only `depends_on` edges are used for layering; `uses` and `contains` edges
// render but do not influence placement. All tie-breaking is by node.id
// (lexicographic) so the same input always produces identical coordinates.
//
// Algorithm:
//   1. Cycle-break: iterative DFS on the depends_on subgraph; back-edges are
//      reversed FOR LAYOUT ONLY (e.layoutReversed = true; rendered dashed).
//   2. Longest-path layering: layer(n) = 1 + max(layer(deps)). Roots at 0.
//   3. Barycenter ordering: 3 passes (down, up, down) within each layer.
//      Ties broken by node.id (determinism).
//   4. Coordinates: fixed column width; row height scales to the max layer
//      occupancy. n.fx = col * COL_W; n.fy = order * ROW_H.
//
// The force simulation is NOT ticked in layered mode. d3-zoom, drag, hover,
// and selection operate on the same draw() function unchanged. Drag updates
// n.fx/n.fy directly.
const LAYERED_COL_W = 180; // horizontal spacing between layers (columns)
const LAYERED_ROW_H = 48;  // vertical spacing between nodes within a layer
const LAYERED_MAX   = 500; // scale guard: refuse layered above this count

function layoutLayered(nodes, links) {
  // Work on the visible subset by id.
  const ids = new Set(nodes.map((n) => n.id));

  // Collect depends_on edges (only those within the visible subset).
  // We work on index arrays to avoid mutating the real link objects (except
  // the layoutReversed flag, which IS written back for the draw pass).
  const depEdges = []; // { s: id, t: id, linkRef }
  for (const e of links) {
    if (e.relation !== "depends_on") continue;
    const s = e.source.id || e.source;
    const t = e.target.id || e.target;
    if (!ids.has(s) || !ids.has(t)) continue;
    if (s === t) continue; // self-loop: skip to prevent infinite recursion in getLayer
    depEdges.push({ s, t, linkRef: e });
  }

  // ---- Step 1: cycle-break via iterative DFS --------------------------------
  // Find back-edges (edges that lead to an ancestor in DFS) and mark them
  // reversed for layout. Two-phase: (a) identify back-edges via DFS on the
  // original edges, (b) reverse those edges in depEdges and write the flag.
  // Using a snapshot of outgoing edges per node means the DFS is stable even
  // as we later reverse edges.

  // Sort entry points for determinism: process nodes in id order.
  const sortedIds = nodes.map((n) => n.id).sort();

  // Build a stable DFS snapshot: for each node, a sorted list of [targetId, edgeRef]
  // pairs. We snapshot the original target id separately from the edge object so
  // that later reversals of e.s/e.t don't corrupt the DFS traversal.
  // Sorting by original targetId gives deterministic traversal order.
  const outSnap = new Map(); // nodeId -> [{ origTarget: id, edgeRef }]
  for (const id of ids) outSnap.set(id, []);
  for (const e of depEdges) outSnap.get(e.s).push({ origTarget: e.t, edgeRef: e });
  for (const arr of outSnap.values()) arr.sort((a, b) => a.origTarget < b.origTarget ? -1 : a.origTarget > b.origTarget ? 1 : 0);

  const visited = new Set();
  const inStack = new Set();

  for (const startId of sortedIds) {
    if (visited.has(startId)) continue;
    // Iterative DFS: stack entries are [nodeId, childIndex].
    const dfsStack = [[startId, 0]];
    while (dfsStack.length) {
      const top = dfsStack[dfsStack.length - 1];
      const [nid, idx] = top;
      if (idx === 0) {
        visited.add(nid);
        inStack.add(nid);
      }
      const children = outSnap.get(nid) || [];
      if (idx < children.length) {
        top[1]++;
        const { origTarget, edgeRef } = children[idx];
        if (inStack.has(origTarget)) {
          // Back-edge: reverse it for layout only. The snapshot key (origTarget)
          // does not change; we only mutate the edge object so predMap is correct.
          edgeRef.linkRef.layoutReversed = true;
          [edgeRef.s, edgeRef.t] = [edgeRef.t, edgeRef.s];
        } else if (!visited.has(origTarget)) {
          dfsStack.push([origTarget, 0]);
        }
      } else {
        // All children visited: pop.
        inStack.delete(nid);
        dfsStack.pop();
      }
    }
  }

  // ---- Step 2: longest-path layering ----------------------------------------
  // layer(n) = 0 if no depends_on predecessors; else 1 + max(layer(pred)).
  // We build a predecessor map from depEdges (which are now cycle-free for
  // layout purposes - back-edges have been reversed).
  const predMap = new Map(); // nodeId -> Set of predecessor ids
  for (const id of ids) predMap.set(id, new Set());
  for (const e of depEdges) {
    // e.s = source = dependent (who has the dependency)
    // e.t = target = dependency (what is depended on)
    // A dependent's predecessor is its dependency: layer(dependent) = 1 + layer(dependency).
    // This puts dependencies at lower x (left) and dependents at higher x (right),
    // matching the Go emitter (dependency --> dependent, LR direction).
    predMap.get(e.s).add(e.t);
  }

  const layerOf = new Map(); // nodeId -> layer index
  function getLayer(id) {
    if (layerOf.has(id)) return layerOf.get(id);
    const preds = predMap.get(id) || new Set();
    // Guard against any residual cycle (reversed edges should have eliminated
    // them, but be safe): if no preds, layer = 0.
    let maxPred = -1;
    for (const p of preds) {
      // Simple recursion is safe because we broke all cycles above.
      maxPred = Math.max(maxPred, getLayer(p));
    }
    const l = maxPred + 1;
    layerOf.set(id, l);
    return l;
  }
  for (const id of sortedIds) getLayer(id);

  // Group nodes by layer and sort within each layer by id for initial order.
  const layerGroups = new Map(); // layer -> [nodeId, ...]
  for (const [id, l] of layerOf) {
    if (!layerGroups.has(l)) layerGroups.set(l, []);
    layerGroups.get(l).push(id);
  }
  for (const arr of layerGroups.values()) arr.sort();

  // ---- Step 3: barycenter ordering (3 passes: down, up, down) ---------------
  // Within each layer, order nodes by the mean positional index of their
  // neighbors in adjacent layers. Ties broken by node.id (determinism).
  const sortedLayers = [...layerGroups.keys()].sort((a, b) => a - b);

  // pos[id] = current order index within its layer.
  const pos = new Map();
  for (const l of sortedLayers) {
    layerGroups.get(l).forEach((id, i) => pos.set(id, i));
  }

  // Build directed edge sets for sweep (predecessor in layer l-1, successor in l+1).
  const succMap = new Map(); // nodeId -> [nodeId]  (target of depends_on)
  const prevMap = new Map(); // nodeId -> [nodeId]  (source of depends_on)
  for (const id of ids) { succMap.set(id, []); prevMap.set(id, []); }
  for (const e of depEdges) {
    succMap.get(e.s).push(e.t);
    prevMap.get(e.t).push(e.s);
  }

  function barycentricSort(arr, neighborFn) {
    const scored = arr.map((id) => {
      const nbs = neighborFn(id);
      if (!nbs.length) return { id, score: Infinity }; // no neighbors: keep at end
      const mean = nbs.reduce((s, nb) => s + (pos.get(nb) ?? 0), 0) / nbs.length;
      return { id, score: mean };
    });
    // Stable sort by score then id (determinism for ties; codepoint < for locale-independence).
    scored.sort((a, b) => a.score - b.score || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    return scored.map((x) => x.id);
  }

  // Sweep order: down (left-to-right layers), up (right-to-left), down again.
  const sweeps = [
    { order: sortedLayers, neighborFn: (id) => prevMap.get(id) || [] },
    { order: [...sortedLayers].reverse(), neighborFn: (id) => succMap.get(id) || [] },
    { order: sortedLayers, neighborFn: (id) => prevMap.get(id) || [] },
  ];

  for (const { order, neighborFn } of sweeps) {
    for (const l of order) {
      const arr = layerGroups.get(l);
      const sorted = barycentricSort(arr, neighborFn);
      layerGroups.set(l, sorted);
      sorted.forEach((id, i) => pos.set(id, i));
    }
  }

  // ---- Step 4: assign coordinates -------------------------------------------
  // x = layer index * COL_W (left = layer 0 = roots/sources)
  // y = order index * ROW_H, centered vertically within the layer.
  const maxOccupancy = Math.max(...[...layerGroups.values()].map((a) => a.length), 1);
  const totalH = maxOccupancy * LAYERED_ROW_H;
  const byId = new Map(nodes.map((n) => [n.id, n]));

  for (const l of sortedLayers) {
    const arr = layerGroups.get(l);
    const layerH = arr.length * LAYERED_ROW_H;
    const yOffset = (totalH - layerH) / 2 + LAYERED_ROW_H / 2;
    for (let i = 0; i < arr.length; i++) {
      const n = byId.get(arr[i]);
      if (!n) continue;
      n.fx = l * LAYERED_COL_W + LAYERED_COL_W / 2;
      n.fy = yOffset + i * LAYERED_ROW_H;
      // Also set x/y so the initial draw is immediate (before any tick).
      n.x = n.fx;
      n.y = n.fy;
    }
  }
}

// applyLayoutedMode: switch to layered layout for the visible node/link set.
// Returns false (with a status message) when the scale guard fires.
// Stops the force simulation so no ticks disturb the fixed positions.
function applyLayeredMode() {
  const visNodes = matchSet
    ? graph.nodes.filter((n) => matchSet.has(n.id))
    : graph.nodes;
  if (visNodes.length > LAYERED_MAX) {
    setStatus(
      "layered layout is capped at 500 nodes - narrow with a query or the local graph (the CLI applies the same rule to -o mermaid)",
      true
    );
    return false;
  }
  if (sim) { sim.stop(); }
  layoutLayered(visNodes, graph.links);
  draw();
  return true;
}

// switchLayout changes layoutMode and applies it, wiring the DOM toggle state.
function switchLayout(mode) {
  layoutMode = mode;
  const btn = el("layout-toggle-btn");
  if (btn) {
    btn.textContent = mode === "layered" ? "Force" : "Layered";
    btn.title = mode === "layered" ? "Switch to force-directed simulation" : "Switch to layered DAG layout";
  }
  // Show/hide force sliders: hidden in layered mode.
  const forceControls = document.querySelector(".force-controls");
  if (forceControls) forceControls.hidden = (mode === "layered");

  updateHash();

  if (mode === "layered") {
    if (!applyLayeredMode()) {
      // Scale guard fired: revert to force mode.
      layoutMode = "force";
      syncLayoutToggle();
      // Clear fixed positions so the sim can move nodes.
      for (const n of graph.nodes) { n.fx = null; n.fy = null; }
      if (sim) { sim.alpha(0.5).restart(); } else { startSimulation(); }
      // Don't write layout=layered to the hash.
      updateHash();
      draw();
    }
  } else {
    // Force mode: clear fixed positions so the simulation takes over.
    for (const n of graph.nodes) { n.fx = null; n.fy = null; }
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
  if (sim) sim.stop(); // stop the prior run (e.g. after loading a new file) - its timer would keep ticking
  const { w, h } = resizeCanvas();
  sim = forceSimulation(graph.nodes)
    .force("link", forceLink(graph.links).id((d) => d.id).distance(40).strength(0.4))
    .force("charge", forceManyBody().strength(-60).distanceMax(400))
    .force("center", forceCenter(w / 2, h / 2))
    .force("collide", forceCollide().radius((d) => d.r + 2))
    .force("x", forceX(w / 2).strength(0.02))
    .force("y", forceY(h / 2).strength(0.02))
    .alphaTarget(idleAlpha()) // decay toward a small floor, not 0, so it keeps gently moving
    .on("tick", draw);
}

function draw() {
  if (!theme) readTheme();
  const dpr = window.devicePixelRatio || 1;
  ctx.save();
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  ctx.scale(dpr, dpr);
  ctx.translate(transform.x, transform.y);
  ctx.scale(transform.k, transform.k);

  const highlight = selected || hoverId;
  const near = neighbors(highlight);

  // Edges first, under the nodes. Dim edges not touching the highlighted node;
  // under a query filter (no selection), dim edges not between two matches, so the
  // matching subgraph stands out instead of a full bright web.
  // projectionActive: hide all non-projection nodes/edges from the draw.
  // (Same flag computed below for nodes; computed here first for edges.)
  const projectionActive = !projectionUnfolded && projectionSet && !query && !focusId && !activeView;
  ctx.lineWidth = 0.6 / transform.k;
  for (const e of graph.links) {
    const s = e.source, t = e.target;
    if (s.x == null || t.x == null) continue; // not "!s.x": a node validly at x=0 must still draw
    // Default projection: only draw edges where both endpoints are in the projection.
    if (projectionActive && !(projectionSet.has(s.id) && projectionSet.has(t.id))) continue;
    let active;
    if (highlight) active = s.id === highlight || t.id === highlight;
    else if (matchSet && !projectionActive) {
      // Under a query filter, draw ONLY edges between two matches - skipping the
      // rest keeps the matching subgraph clean instead of a faint full-web haze.
      if (!(matchSet.has(s.id) && matchSet.has(t.id))) continue;
      active = true;
    } else active = true;
    ctx.strokeStyle = active ? theme.muted : theme.border;
    ctx.globalAlpha = active ? 0.55 : 0.1;
    // Cycle edges (from the target-graph adapter) get a dashed stroke so they
    // stand out from normal dependency edges. Layout-reversed edges (cycle-break
    // in layered mode) also render dashed.
    const dashed = e.cycle || e.layoutReversed;
    if (dashed) ctx.setLineDash([4 / transform.k, 3 / transform.k]);
    ctx.beginPath();
    ctx.moveTo(s.x, s.y);
    ctx.lineTo(t.x, t.y);
    ctx.stroke();
    if (dashed) ctx.setLineDash([]);

    // Arrowheads: only in layered mode (they add clarity on the DAG's directed
    // edges; in force mode at demo-graph density they would be visual noise).
    // Convention matches the Go mermaid emitter (LR direction): the dependency
    // is placed at a lower x (left) and the dependent at a higher x (right).
    // In link terms: e.source = dependent (right), e.target = dependency (left).
    // The arrowhead is drawn at the SOURCE end (the dependent node on the right),
    // matching mermaid `dependency --> dependent` reading left-to-right.
    // For layout-reversed back-edges the arrow tip moves to the target end
    // (the reversed direction is layout-only fiction; the mark calls it out).
    if (layoutMode === "layered" && active && e.relation === "depends_on") {
      const isReversed = !!e.layoutReversed;
      // Arrow tip: at source (dependent, right) for normal edges; at target for reversed.
      const tipNode = isReversed ? t : s;
      // Direction vector from the other end toward the tip.
      const fromNode = isReversed ? s : t;
      const dx = tipNode.x - fromNode.x, dy = tipNode.y - fromNode.y;
      const len = Math.sqrt(dx * dx + dy * dy) || 1;
      const ux = dx / len, uy = dy / len;
      // Place the tip at the node's edge (radius + small gap).
      const tipR = tipNode.r || 5;
      const tipX = tipNode.x - ux * (tipR + 1 / transform.k);
      const tipY = tipNode.y - uy * (tipR + 1 / transform.k);
      const aLen = 8 / transform.k; // arrowhead length
      const aWid = 4 / transform.k; // arrowhead half-width
      // Perpendicular vector.
      const px = -uy, py = ux;
      ctx.beginPath();
      ctx.moveTo(tipX, tipY);
      ctx.lineTo(tipX - ux * aLen + px * aWid, tipY - uy * aLen + py * aWid);
      ctx.lineTo(tipX - ux * aLen - px * aWid, tipY - uy * aLen - py * aWid);
      ctx.closePath();
      ctx.fillStyle = active ? theme.muted : theme.border;
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
    if (projectionActive && !projectionSet.has(n.id)) continue;
    let alpha = 1;
    if (highlight) alpha = n.id === highlight || (near && near.has(n.id)) ? 1 : 0.15;
    else if (matchSet && !projectionActive) alpha = matchSet.has(n.id) ? 1 : 0.12;
    ctx.globalAlpha = alpha;
    const nodeColor = groupColorFor(n) || theme.kindColor[n.kind] || "#888";
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
      ctx.strokeStyle = theme.accent;
      ctx.beginPath();
      ctx.arc(n.x, n.y, n.r, 0, 2 * Math.PI);
      ctx.stroke();
    }
  }
  ctx.globalAlpha = 1;

  // Labels: greedy collision-culling. Draw in priority order (the selection, then the
  // highest-degree nodes) and skip any label whose box overlaps one already drawn this
  // frame, so text stays readable instead of stacking into an unreadable smear. This is
  // the d3fc "greedy / removeOverlaps" idea done inline - d3 core has no label placer,
  // and this is what its greedy strategy does under the hood. Still gated to big nodes,
  // the selection, or a zoomed-in view; and culled to the viewport first so the overlap
  // scan stays cheap. Boxes are compared in world units (the same scale as the on-screen
  // 11px text), so the overlap test is zoom-consistent.
  ctx.fillStyle = theme.text;
  ctx.font = "500 " + (11 / transform.k) + "px " + theme.font;
  ctx.textAlign = "left";
  ctx.textBaseline = "middle";
  const vw = canvas.width / dpr, vh = canvas.height / dpr; // viewport in CSS px
  const labelPad = 2 / transform.k;
  const lineH = 13 / transform.k; // ~1.2x the 11px font, in world units
  const labelCandidates = [];
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    if (projectionActive && !projectionSet.has(n.id)) continue;
    const show = n.id === highlight || n.degree > 24 || transform.k > 2.2;
    if (!show) continue;
    if (matchSet && !projectionActive && !matchSet.has(n.id) && n.id !== highlight) continue;
    // Viewport cull (CSS px): drop off-screen labels so the greedy scan below only
    // weighs what's actually visible.
    const cx = transform.x + n.x * transform.k, cy = transform.y + n.y * transform.k;
    if (cx < -120 || cx > vw + 20 || cy < -20 || cy > vh + 20) continue;
    labelCandidates.push(n);
  }
  // Priority order: the selected node always wins a slot, then denser (more-connected)
  // nodes, so the labels we keep are the ones carrying the most signal.
  labelCandidates.sort((a, b) =>
    (b.id === highlight) - (a.id === highlight) || b.degree - a.degree);
  const placedLabels = [];
  for (const n of labelCandidates) {
    const lx = n.x + n.r + labelPad;
    const ly = n.y - lineH / 2;
    const lw = ctx.measureText(n.label).width;
    let clash = false;
    for (const p of placedLabels) {
      if (lx < p.x + p.w && lx + lw > p.x && ly < p.y + lineH && ly + lineH > p.y) { clash = true; break; }
    }
    if (clash) continue;
    placedLabels.push({ x: lx, y: ly, w: lw });
    ctx.fillText(n.label, lx, n.y);
  }
  ctx.restore();
}

// ---- interaction -----------------------------------------------------------
function nodeAtPointer(event) {
  const rect = canvas.getBoundingClientRect();
  const px = (event.clientX - rect.left - transform.x) / transform.k;
  const py = (event.clientY - rect.top - transform.y) / transform.k;
  // In layered mode the simulation may be stopped, but sim.find still works on
  // the node positions. Fall back to a manual scan when sim is null (shouldn't
  // happen, but be safe).
  if (sim) return sim.find(px, py, 30 / transform.k);
  let best = null, bestDist = 30 / transform.k;
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    const d = Math.sqrt((n.x - px) ** 2 + (n.y - py) ** 2);
    if (d < bestDist) { bestDist = d; best = n; }
  }
  return best;
}

function setupZoomDrag() {
  zoomBehavior = d3zoom()
    .scaleExtent([0.1, 8])
    .filter((event) => !event.button && event.type !== "dblclick")
    .on("zoom", (event) => { transform = event.transform; draw(); });
  select(canvas).call(zoomBehavior);

  const dragBehavior = d3drag()
    .subject((event) => nodeAtPointer(event.sourceEvent))
    .on("start", (event) => {
      if (!event.subject) return;
      if (layoutMode !== "layered" && !event.active) sim.alphaTarget(0.2).restart();
      event.subject.fx = event.subject.x;
      event.subject.fy = event.subject.y;
    })
    .on("drag", (event) => {
      if (!event.subject) return;
      event.subject.fx = (event.x - transform.x) / transform.k;
      event.subject.fy = (event.y - transform.y) / transform.k;
      // In layered mode the sim is stopped; draw manually on each drag event.
      if (layoutMode === "layered") { event.subject.x = event.subject.fx; event.subject.y = event.subject.fy; draw(); }
    })
    .on("end", (event) => {
      if (!event.subject) return;
      if (layoutMode === "layered") {
        // Keep the manually dragged position (fx/fy stay set); just redraw.
        draw();
        return;
      }
      if (!event.active) sim.alphaTarget(idleAlpha()); // back to the gentle floor, not a dead stop
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
    if (id !== hoverId) { hoverId = id; canvas.style.cursor = id ? "pointer" : "grab"; draw(); }
  });
}

// ---- explain card ----------------------------------------------------------
function escapeHtml(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

// safeUrl returns u only if it is an http(s) URL, else null - a graph.json is
// untrusted input (a visitor can drop any file), so attrs.url must not become a
// `javascript:` href.
function safeUrl(u) {
  try {
    const p = new URL(u, location.href);
    return p.protocol === "http:" || p.protocol === "https:" ? u : null;
  } catch {
    return null;
  }
}

// A node reference rendered as a button that re-selects it (edges link to their
// other endpoint, per the plan).
function nodeRefHtml(id) {
  const n = graph.byId.get(id);
  const label = n ? n.label : id;
  return '<button type="button" class="node-ref" data-id="' + escapeHtml(id) + '">' + escapeHtml(label) + "</button>";
}

function relSectionHtml(title, rows) {
  if (!rows.length) return "";
  const byRel = new Map();
  for (const r of rows) {
    if (!byRel.has(r.rel)) byRel.set(r.rel, []);
    byRel.get(r.rel).push(r);
  }
  let html = "<dt>" + escapeHtml(title) + "</dt><dd>";
  const rels = [...byRel.keys()].sort((a, b) => RELATIONS.indexOf(a) - RELATIONS.indexOf(b));
  for (const rel of rels) {
    const items = byRel.get(rel);
    html += '<div class="rel-group"><span class="rel-name">' + escapeHtml(rel) +
      ' <span class="rel-count">(' + items.length + ")</span></span> ";
    html += items.slice(0, 40).map((r) => nodeRefHtml(r.other)).join(" ");
    if (items.length > 40) html += " <span class=\"muted\">+" + (items.length - 40) + " more</span>";
    html += "</div>";
  }
  return html + "</dd>";
}

function renderCard(id) {
  const n = graph.byId.get(id);
  if (!n) { cardEl.innerHTML = ""; cardEl.hidden = true; document.body.classList.remove("has-card"); return; }
  document.body.classList.add("has-card");
  const { out, inc } = incidentEdges(id);
  let html = "";
  html += '<p class="card-section">Node details</p>';
  html += '<header class="card-head">';
  html += '<span class="kind-dot k-' + escapeHtml(n.kind) + '"></span>';
  html += "<h2>" + escapeHtml(n.label) + "</h2>";
  html += '<span class="kind-tag">' + escapeHtml(n.kind) + "</span>";
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
    html += base
      ? '<dt>source</dt><dd><a href="' + escapeHtml(base + "/" + path) + '" target="_blank" rel="noopener"><code>' + escapeHtml(n.source) + "</code></a></dd>"
      : "<dt>source</dt><dd><code>" + escapeHtml(n.source) + "</code></dd>";
  }
  if (n.attrs && n.attrs.url && safeUrl(n.attrs.url)) {
    html += '<dt>reference</dt><dd><a href="' + escapeHtml(n.attrs.url) + '" target="_blank" rel="noopener">' + escapeHtml(n.attrs.url) + "</a></dd>";
  }
  html += relSectionHtml("outgoing", out);
  html += relSectionHtml("incoming", inc);
  html += "</dl>";
  // Copy as Mermaid: copies the focus neighborhood (or current match set) as mermaid.
  // It lives in the card so it is immediately reachable when a node is selected. It is
  // a link-styled action (not a chunky button) so it sits quietly in the dense card and
  // doesn't compete with the canvas toolbar's Copy as Mermaid; still a <button> because
  // it acts (copies to clipboard) rather than navigates.
  html += '<div class="card-actions"><button type="button" class="card-mermaid-link" title="Copy this node\'s neighborhood as a Mermaid diagram (double-click the node first to focus its local graph, then copy). Mirrors the CLI: magus graph export -o mermaid --select id"><span class="copy-glyph" aria-hidden="true">&#10697;</span> Copy as Mermaid</button></div>';
  cardEl.innerHTML = html;
  cardEl.hidden = false;
  cardEl.querySelectorAll(".node-ref").forEach((b) =>
    b.addEventListener("click", () => selectNode(b.dataset.id, true)));
  const mermaidCardBtn = cardEl.querySelector(".card-mermaid-link");
  if (mermaidCardBtn) mermaidCardBtn.addEventListener("click", copyAsMermaid);
}

// ---- selection, search, list, deep links -----------------------------------
function selectNode(id, center) {
  // Phase 5: default projection - clicking a project node in projection mode unfolds it.
  if (!projectionUnfolded && id && projectionSet && projectionSet.has(id)) {
    const n = graph.byId ? graph.byId.get(id) : null;
    if (n && n.kind === "project") {
      // Unfold this project: show its contains neighborhood.
      projectionUnfolded = true;
      projectionSet = null;
      // Release any nodes that were parked off-screen by the projection.
      for (const nd of graph.nodes) { if (nd.fx === -1e6) { nd.fx = null; nd.fy = null; } }
      const projectNeighborhood = new Set([id]);
      for (const e of graph.links) {
        const s = e.source.id || e.source, t = e.target.id || e.target;
        if ((s === id && e.relation === "contains") || (t === id && e.relation === "depends_on" && s === id)) {
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
  draw();
}

function centerOn(id) {
  const n = graph.byId.get(id);
  if (!n || n.x == null || !zoomBehavior) return;
  const { w, h } = resizeCanvas();
  transform = zoomIdentity.translate(w / 2 - n.x * transform.k, h / 2 - n.y * transform.k).scale(transform.k);
  // Drive the REAL zoom behavior (not a throwaway d3zoom()) so a later pan/zoom
  // continues from here instead of snapping back to a stale internal transform.
  select(canvas).call(zoomBehavior.transform, transform);
}

// fitView frames a set of nodes (or all when ids is null) in the viewport - the
// zoom-to-fit / reset-view action. Reuses the shared zoomBehavior + transform.
function fitView(ids) {
  const pts = graph.nodes.filter((n) => n.x != null && (!ids || ids.has(n.id)));
  if (!pts.length || !zoomBehavior) return;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const n of pts) {
    minX = Math.min(minX, n.x - n.r); maxX = Math.max(maxX, n.x + n.r);
    minY = Math.min(minY, n.y - n.r); maxY = Math.max(maxY, n.y + n.r);
  }
  const { w, h } = resizeCanvas();
  const pad = 48;
  const k = Math.max(0.1, Math.min(8, Math.min((w - 2 * pad) / (maxX - minX || 1), (h - 2 * pad) / (maxY - minY || 1))));
  const cx = (minX + maxX) / 2, cy = (minY + maxY) / 2;
  transform = zoomIdentity.translate(w / 2 - cx * k, h / 2 - cy * k).scale(k);
  select(canvas).call(zoomBehavior.transform, transform);
  draw();
}

// focusNode builds a LOCAL graph around a node (Obsidian's local view): the node
// plus everything within `depth` hops become the match set, so the existing
// dim-non-matches / hide-outside-edges rendering isolates the neighborhood. It
// also selects the node (explain card) and fits the view.
function focusNode(id, depth) {
  if (!graph.byId.has(id)) return;
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
  const n = graph.byId.get(id);
  setStatus("Local graph around " + n.label + " - " + matchSet.size + " nodes within " + depth + " hop" + (depth === 1 ? "" : "s") + ". Press Esc to clear, [ / ] to change depth.");
  // Re-run layered layout on the new (local) subset when in layered mode.
  if (layoutMode === "layered") {
    for (const e of graph.links) delete e.layoutReversed;
    applyLayeredMode();
  }
  fitView(matchSet);
}

function changeFocusDepth(delta) {
  if (!focusId) return;
  focusNode(focusId, Math.max(1, Math.min(5, focusDepth + delta)));
}

function clearFocusOrQuery() {
  focusId = null;
  matchSet = null;
  query = "";
  if (searchEl) searchEl.value = "";
  setStatus("");
  // Clear any active view.
  if (activeView) {
    activeView = null; viewNode = null; viewNodeTo = null;
    document.querySelectorAll(".view-btn").forEach((b) => b.classList.remove("view-active"));
    renderViewCommand(null, null, null);
  }
  renderList();
  updateHash();
  if (layoutMode === "layered") {
    for (const e of graph.links) delete e.layoutReversed;
    applyLayeredMode();
  } else {
    draw();
  }
}

// applyLens is the legacy entry point for .lens-btn clicks; now delegates to
// activateView so the view system handles state/hash/CLI idiom uniformly.
function applyLens(name) {
  activateView(name === "hubs" ? "hubs" : "orphans");
}

// syncConditionalViews shows or hides the "What's slow?" (critical) view button
// based on whether the current graph has DurationMs timing data. Called after
// each graph load (boot and replaceGraph) so the button tracks the data.
function syncConditionalViews() {
  const hasDuration = graph && graph.nodes.some((n) =>
    (n.DurationMs || 0) > 0 || (n.duration_ms || 0) > 0 || ((n.attrs && n.attrs.DurationMs) || 0) > 0
  );
  document.querySelectorAll("[data-view='critical']").forEach((btn) => {
    btn.classList.toggle("view-conditional", !hasDuration);
  });
}

// ---- color groups ----------------------------------------------------------
// Each group paints every node matching a query one chosen color, ON TOP of the
// kind palette - so several groups can coexist (unlike the single match set). The
// groups reuse the same query grammar (parseQuery/termMatches) as the filter box.
const groups = []; // { query, color, terms }

function groupColorFor(node) {
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

function addGroup() {
  const q = el("group-query").value.trim();
  if (!q) return;
  if (!graph.relIndex) graph.relIndex = relationIndex();
  groups.push({ query: q, color: el("group-color").value, terms: parseQuery(q) });
  el("group-query").value = "";
  renderGroups();
  draw();
}

function renderGroups() {
  const list = el("group-list");
  list.innerHTML = groups.map((g, i) =>
    '<span class="group-chip"><span class="group-swatch" style="background:' + escapeHtml(g.color) + '"></span>' +
    escapeHtml(g.query) + '<button type="button" class="group-x" data-i="' + i + '" aria-label="Remove group">&times;</button></span>').join("");
  list.querySelectorAll(".group-x").forEach((b) =>
    b.addEventListener("click", () => { groups.splice(+b.dataset.i, 1); renderGroups(); draw(); }));
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

function parseQuery(str) {
  const terms = [];
  let i = 0;
  while (i < str.length) {
    while (i < str.length && /\s/.test(str[i])) i++;
    if (i >= str.length) break;
    let negated = false;
    if (str[i] === "-") { negated = true; i++; }
    let field = null;
    const fm = /^([a-zA-Z]+):/.exec(str.slice(i));
    if (fm && QUERY_FIELDS.includes(fm[1].toLowerCase())) { field = fm[1].toLowerCase(); i += fm[0].length; }
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
  const idx = new Map();
  const add = (id, rel) => { let s = idx.get(id); if (!s) { s = new Set(); idx.set(id, s); } s.add(rel); };
  for (const e of graph.links) {
    add(e.source.id || e.source, e.relation);
    add(e.target.id || e.target, e.relation);
  }
  return idx;
}

function termMatches(node, term) {
  const v = term.value;
  let hit;
  switch (term.field) {
    case "kind": hit = node.kind === v; break;
    case "project":
      // Knowledge-graph ids: project nodes are "project:<name>", target nodes
      // are "target:<project>:<name>". Target-graph ids: project nodes are the
      // raw path (e.g. "."), target/spell nodes carry attrs.project = path.
      hit = node.id === "project:" + v ||
        (node.kind === "target" && node.id.toLowerCase().startsWith("target:" + v + ":")) ||
        (node.attrs && (node.attrs.project || "").toLowerCase() === v) ||
        node.id.toLowerCase() === v;
      break;
    case "relation": hit = graph.relIndex.has(node.id) && graph.relIndex.get(node.id).has(v); break;
    case "id": hit = node.id.toLowerCase().includes(v); break;
    // symbol: prefix targets SCIP code-symbol nodes by their symbol: id prefix.
    // The CLI treats `symbol:` as free text (no typed field); the box accepts a superset
    // syntactically (restricts to kind=symbol + id substring), but the CLI accepts the query.
    case "symbol": hit = node.kind === "symbol" && node.id.toLowerCase().includes("symbol:" + v); break;
    default:
      hit = node.id.toLowerCase().includes(v) || node.label.toLowerCase().includes(v) ||
        (node.doc && node.doc.toLowerCase().includes(v));
  }
  return term.negated ? !hit : hit;
}

function applyQuery(q) {
  focusId = null; // typing a query exits focus/lens/view mode
  // Typing a query unfolds the projection (user is exploring details).
  if (!projectionUnfolded && q.trim()) {
    projectionUnfolded = true;
    projectionSet = null;
    const btn = el("projection-unfold-btn");
    if (btn) btn.hidden = true;
  }
  // Clear active view when query is typed.
  if (activeView) {
    activeView = null; viewNode = null; viewNodeTo = null;
    document.querySelectorAll(".view-btn").forEach((b) => b.classList.remove("view-active"));
    renderViewCommand(null, null, null);
  }
  query = q.trim();
  const terms = query ? parseQuery(query) : [];
  if (!terms.length) { matchSet = null; }
  else {
    if (!graph.relIndex) graph.relIndex = relationIndex();
    matchSet = new Set();
    for (const n of graph.nodes) {
      if (terms.every((t) => termMatches(n, t))) matchSet.add(n.id);
    }
    setListExpanded(true); // a query reveals its matches
  }
  renderList();
  updateHash();
  // Re-run layered layout on the new visible subset.
  if (layoutMode === "layered") {
    // Clear prior layout-reversed flags so cycle-break reruns cleanly.
    for (const e of graph.links) delete e.layoutReversed;
    if (!applyLayeredMode()) {
      // Scale guard: too many nodes; fall back to force.
      layoutMode = "force";
      syncLayoutToggle();
      // Clear pinned positions so the force sim can move all nodes.
      for (const n of graph.nodes) { n.fx = null; n.fy = null; }
      if (sim) sim.alpha(0.3).restart();
    }
    return;
  }
  draw();
}

// The node cloud is collapsed by default (canvas-first on load); a query, or the
// count toggle, reveals it.
let listExpanded = false;
function setListExpanded(v) {
  listExpanded = v;
  listEl.hidden = !v;
  const btn = el("list-toggle");
  if (btn) btn.setAttribute("aria-expanded", v ? "true" : "false");
}

// The node list is the accessible twin of the canvas: it always reflects the
// current query (or the highest-degree nodes when there is no query).
function renderList() {
  const pool = matchSet
    ? graph.nodes.filter((n) => matchSet.has(n.id))
    : graph.nodes.slice();
  pool.sort((a, b) => b.degree - a.degree || a.label.localeCompare(b.label));
  const shown = pool.slice(0, 300);
  countEl.textContent = matchSet
    ? matchSet.size + " match" + (matchSet.size === 1 ? "" : "es")
    : graph.nodes.length + " node" + (graph.nodes.length === 1 ? "" : "s") +
      ", " + graph.links.length + " edge" + (graph.links.length === 1 ? "" : "s");
  // Compact rows: a kind-colored dot (keyed to the legend) + the label. The kind
  // name lives in the title tooltip rather than a column, to keep rows dense.
  listEl.innerHTML = shown.map((n) =>
    '<li><button type="button" class="node-pill" data-id="' + escapeHtml(n.id) + '"' +
    ' title="' + escapeHtml(n.kind + " · " + n.label) + '"' +
    (n.id === selected ? ' aria-current="true"' : "") + ">" +
    '<span class="kind-dot k-' + escapeHtml(n.kind) + '"></span>' +
    "<span class=\"row-label\">" + escapeHtml(n.label) + "</span>" +
    "</button></li>").join("");
  if (pool.length > shown.length) {
    listEl.innerHTML += '<li class="muted list-more">+' + (pool.length - shown.length) + " more (refine the search)</li>";
  }
  listEl.querySelectorAll(".node-pill").forEach((b) => {
    b.addEventListener("click", () => selectNode(b.dataset.id, true));
    b.addEventListener("dblclick", () => focusNode(b.dataset.id, focusDepth));
  });
}

function syncListSelection() {
  listEl.querySelectorAll(".node-pill").forEach((b) => {
    if (b.dataset.id === selected) b.setAttribute("aria-current", "true");
    else b.removeAttribute("aria-current");
  });
}

function renderLegend() {
  const counts = new Map();
  for (const n of graph.nodes) counts.set(n.kind, (counts.get(n.kind) || 0) + 1);
  // Each legend row is a button that filters to kind:<k> (the CLI query it maps to),
  // so clicking a color isolates that kind - a quick, Obsidian-style filter.
  legendEl.innerHTML = KINDS.filter((k) => counts.has(k)).map((k) =>
    '<li><button type="button" class="legend-row" data-kind="' + escapeHtml(k) + '" title="Filter to kind:' + escapeHtml(k) + '">' +
    '<span class="kind-dot k-' + escapeHtml(k) + '"></span>' +
    escapeHtml(k) + ' <span class="muted">' + counts.get(k) + "</span></button></li>").join("");
  legendEl.querySelectorAll(".legend-row").forEach((b) =>
    b.addEventListener("click", () => {
      const q = "kind:" + b.dataset.kind;
      // Toggle: clicking the active kind filter clears it.
      const next = query === q ? "" : q;
      searchEl.value = next;
      applyQuery(next);
    }));
}

// Reflect selection, query, layout mode, active view, and color preset in the
// hash WITHOUT clobbering a #data= fragment (round-tripping the whole graph
// through history on every click would break the private-data contract).
let suppressHash = false;
function updateHash() {
  if (suppressHash) return;
  const params = hashParams();
  if (params.data || params.src || params.live) return; // keep fragment data/loopback/live links intact
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
  const defaultLayout = (graphFlavor === "targets") ? "layered" : "force";
  if (layoutMode !== defaultLayout) parts.push("layout=" + layoutMode);
  if (activePreset) parts.push("preset=" + encodeURIComponent(activePreset));
  const next = parts.length ? "#" + parts.join("&") : "#";
  if (location.hash !== next) history.replaceState(null, "", next);
}

function applyDeepLinks() {
  const params = hashParams();
  // Restore view state: #view=<id>&node=<id>[&to=<id>]
  const validViews = ["blast", "trace", "critical", "hubs", "orphans"];
  if (params.view && validViews.includes(params.view)) {
    projectionUnfolded = true; // views show the full graph
    activateView(params.view, params.node || null, params.to || null);
    return; // view takes precedence over q/node
  }
  if (params.q) { searchEl.value = params.q; applyQuery(params.q); }
  if (params.node && graph.byId.has(params.node)) selectNode(params.node, true);
  // Restore layout mode from the fragment (#layout=force or #layout=layered).
  // Only switch when the value is valid and differs from the current mode.
  if (params.layout === "force" || params.layout === "layered") {
    if (params.layout !== layoutMode) switchLayout(params.layout);
  }
  // Restore color preset from #preset=<id>.
  if (params.preset) {
    const preset = COLOR_PRESETS.find((p) => p.id === params.preset);
    if (preset) applyPreset(params.preset);
  }
}

// Swap in a graph loaded from a local file (the Open-file button and drag-drop
// share this). Resets view state and restarts the layout.
function replaceGraph(data, statusMsg) {
  // A locally opened/dropped file supersedes whatever provenance badge was
  // showing for the graph that loaded at boot.
  updateSnapshotBadge(null);
  // Detect and adapt flavor before prepareGraph, same as boot(). The knowledge
  // path is unchanged; the targets path is converted client-side.
  const flavor = detectFlavor(data);
  graphFlavor = flavor;
  let raw = data;
  if (flavor === "targets") {
    const nl = targetGraphToNodeLink(data);
    raw = { nodes: nl.nodes, links: nl.links };
    const nProjects = (data.projects || []).length;
    const nTargets = nl.nodes.filter((n) => n.kind === "target").length;
    statusMsg = "target graph - " + nProjects + " project" + (nProjects === 1 ? "" : "s") +
      " - " + nTargets + " target" + (nTargets === 1 ? "" : "s") +
      (nl.cycleWarnings.length ? "; " + nl.cycleWarnings.join("; ") : "");
  }
  graph = prepareGraph(raw);
  // Clear any layout-reversed flags from a previous layered pass.
  for (const e of graph.links) delete e.layoutReversed;
  selected = null;
  hoverId = null;
  focusId = null;
  matchSet = null;
  if (searchEl) searchEl.value = "";
  // Reset Phase 5 view/projection state.
  activeView = null; viewNode = null; viewNodeTo = null;
  activePreset = null;
  groups.splice(0, groups.length);
  projectionUnfolded = false;
  projectionSet = null;
  document.querySelectorAll(".view-btn").forEach((b) => b.classList.remove("view-active"));
  document.querySelectorAll(".preset-btn").forEach((b) => b.classList.remove("preset-active"));
  renderViewCommand(null, null, null);
  // Apply projection: show only projects by default if the count is small.
  const ps = buildProjectionSet();
  if (ps) { projectionUnfolded = false; projectionSet = ps; matchSet = new Set(ps); }
  else projectionUnfolded = true;
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
  const fragParams = hashParams();
  const requestedLayout = (fragParams.layout === "force" || fragParams.layout === "layered")
    ? fragParams.layout
    : (flavor === "targets" ? "layered" : "force");
  layoutMode = requestedLayout;
  syncLayoutToggle();
  if (layoutMode === "layered") {
    startSimulation(); // initializes node positions even if we stop it
    sim.stop();
    if (!applyLayeredMode()) {
      // Scale guard fired; fall back to force.
      layoutMode = "force";
      syncLayoutToggle();
      startSimulation();
    }
  } else {
    startSimulation();
  }
  // Park hidden nodes after the sim is built (projection reduces the visible set).
  if (!projectionUnfolded && projectionSet) {
    for (const n of graph.nodes) {
      if (!projectionSet.has(n.id)) { n.fx = -1e6; n.fy = -1e6; n.x = -1e6; n.y = -1e6; }
    }
  }
  draw();
}

// syncLayoutToggle updates the toggle button label and slider visibility to
// match the current layoutMode WITHOUT switching the mode (used after loading
// a new graph where the mode is set directly).
function syncLayoutToggle() {
  const btn = el("layout-toggle-btn");
  if (btn) {
    btn.textContent = layoutMode === "layered" ? "Force" : "Layered";
    btn.title = layoutMode === "layered" ? "Switch to force-directed simulation" : "Switch to layered DAG layout";
  }
  const forceControls = document.querySelector(".force-controls");
  if (forceControls) forceControls.hidden = (layoutMode === "layered");
}

// loadDemoGraph reloads into the demo via #demo=1. The reload (rather than an in-place swap)
// re-enters boot, whose pipeline renders the demo exactly like a real graph - default
// projection, fit-to-view, wired interactions - so there is no partial/unframed render. The
// demo graph.json is fetched by loadGraph ONLY when this flag is present (see there), so a
// cold visit still pays no graph download; it also makes /graph/#demo=1 a shareable demo link.
function loadDemoGraph() {
  location.hash = "demo=1";
  location.reload();
}

async function readGraphFile(file) {
  if (!file) return;
  // A user graph supersedes the empty state: dismiss it if it's still up.
  const empty = el("graph-empty-state");
  if (empty) empty.hidden = true;
  try {
    replaceGraph(JSON.parse(await file.text()), "Loaded " + file.name + " (local file; it stays on your machine).");
  } catch (e) {
    setStatus("Could not read " + file.name + ": " + e.message, true);
  }
}

// ---- target-graph adapter --------------------------------------------------
// The CLI emits two graph shapes:
//   knowledge: KnowledgeGraphOutput  { definition, nodes, links, ... }
//   targets:   TargetGraphOutput     { definition, projects[] }
//
// detectFlavor tells them apart so the existing knowledge-graph path stays
// byte-identical in behavior; the target path is converted client-side via
// targetGraphToNodeLink before entering prepareGraph.
function detectFlavor(raw) {
  return Array.isArray(raw.projects) && typeof raw.definition === "string"
    ? "targets"
    : "knowledge";
}

// targetGraphToNodeLink converts a TargetGraphOutput to the { nodes, links }
// shape prepareGraph accepts (raw.nodes + raw.links). Wire types (verified
// against types/describe.go):
//   TargetGraphProject { path, engine?, nodes?, cycle?, depends_on? }
//   TargetGraphNode    { name, doc?, dependencies?, charms?, spells?, cross_dependencies? }
//   TargetSpellUse     { spell, ops }      <- read s.spell, not s itself
//   CrossTargetRef     { project, target } <- read c.project / c.target
//
// All edges carry confidence "high" + score 1 so existing code paths that
// inspect those fields keep working.
function targetGraphToNodeLink(tg) {
  const nodes = [];
  const links = [];
  const nodeIds = new Set(); // for skipping dangling edges

  // Pass 1: build all nodes so edge validation can reference them.
  const projectPaths = [];
  for (const p of tg.projects || []) {
    // Project node.
    nodes.push({ id: p.path, kind: "project", label: p.path, attrs: { project: p.path } });
    nodeIds.add(p.path);
    projectPaths.push(p.path);

    // Target nodes.
    for (const n of p.nodes || []) {
      const id = p.path + "#" + n.name;
      nodes.push({
        id,
        kind: "target",
        label: n.name,
        doc: n.doc || undefined,
        attrs: { project: p.path },
      });
      nodeIds.add(id);
    }

    // Spell nodes (distinct by name across all targets in all projects).
    for (const n of p.nodes || []) {
      for (const s of n.spells || []) {
        const spellId = "spell:" + s.spell;
        if (!nodeIds.has(spellId)) {
          nodes.push({ id: spellId, kind: "spell", label: s.spell, attrs: {} });
          nodeIds.add(spellId);
        }
      }
    }
  }

  // Pass 2: collect same-project depends_on targets so anchor detection is correct.
  // A target is an anchor when no same-project depends_on edge points at it.
  const referencedByIntraProject = new Set(); // ids that appear as dep within same project
  for (const p of tg.projects || []) {
    for (const n of p.nodes || []) {
      for (const d of n.dependencies || []) {
        referencedByIntraProject.add(p.path + "#" + d);
      }
    }
  }

  // Pass 3: build edges.
  const cycleProjects = []; // projects with a non-empty cycle field
  for (const p of tg.projects || []) {
    // Containment: project -> each of its targets.
    for (const n of p.nodes || []) {
      const targetId = p.path + "#" + n.name;
      links.push({ source: p.path, target: targetId, relation: "contains", confidence: "high", score: 1 });
    }

    // Build a set of cycle-edge pairs for this project (consecutive pairs in
    // the cycle array form the cycle edges).
    const cycleEdgePairs = new Set();
    if (p.cycle && p.cycle.length >= 2) {
      for (let ci = 0; ci < p.cycle.length - 1; ci++) {
        cycleEdgePairs.add(p.path + "#" + p.cycle[ci] + "->" + p.path + "#" + p.cycle[ci + 1]);
      }
    }

    // Same-project depends_on edges.
    for (const n of p.nodes || []) {
      const srcId = p.path + "#" + n.name;
      for (const d of n.dependencies || []) {
        const dstId = p.path + "#" + d;
        if (!nodeIds.has(dstId)) continue; // skip dangling (prepareGraph also filters)
        const isCycle = cycleEdgePairs.has(srcId + "->" + dstId);
        links.push({ source: srcId, target: dstId, relation: "depends_on", confidence: "high", score: 1, ...(isCycle ? { cycle: true } : {}) });
      }

      // Cross-project depends_on edges.
      for (const c of n.cross_dependencies || []) {
        const dstId = c.project + "#" + c.target;
        // The cross-project target node may or may not be in this graph; only
        // emit the edge if the destination node exists (avoid phantom nodes).
        if (!nodeIds.has(dstId)) continue;
        links.push({ source: srcId, target: dstId, relation: "depends_on", confidence: "high", score: 1 });
      }

      // Spell edges: target -> spell node.
      for (const s of n.spells || []) {
        const spellId = "spell:" + s.spell;
        links.push({ source: srcId, target: spellId, relation: "uses", confidence: "high", score: 1 });
      }
    }

    // Project-level depends_on edges (project -> project).
    for (const q of p.depends_on || []) {
      if (!nodeIds.has(q)) continue;
      links.push({ source: p.path, target: q, relation: "depends_on", confidence: "high", score: 1 });
    }

    // Track projects with cycles for the status warning.
    if (p.cycle && p.cycle.length) cycleProjects.push(p);
  }

  // Mark anchors: targets with no incoming same-project depends_on edge.
  for (const n of nodes) {
    if (n.kind !== "target") continue;
    if (!referencedByIntraProject.has(n.id)) {
      n.attrs = n.attrs || {};
      n.attrs.anchor = "true";
    }
  }

  // Emit cycle warnings on the status line (deferred; boot reads this).
  const cycleWarnings = cycleProjects.map((p) =>
    "cycle detected in " + p.path + ": " + (p.cycle || []).join(" -> "));

  return { nodes, links, cycleWarnings };
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
  if (projectIds.size > 50) return null;  // already small; show everything
  return projectIds;
}

// Apply (or clear) the default projection. Called at boot and when user unfolds.
function applyProjection() {
  if (projectionUnfolded) {
    projectionSet = null;
    const btn = el("projection-unfold-btn");
    if (btn) btn.hidden = true;
    matchSet = null;
    renderList();
    updateHash();
    if (layoutMode === "layered") {
      for (const e of graph.links) delete e.layoutReversed;
      applyLayeredMode();
    } else {
      draw();
    }
    return;
  }
  const ps = buildProjectionSet();
  projectionSet = ps;
  // If no projection applies, nothing to do.
  if (!ps) return;
  // Set matchSet = projectionSet so the node list and count reflect the projection.
  matchSet = new Set(ps);
  // Park non-projection nodes out of the force simulation by pinning them at a
  // far-off location. They are not drawn (draw() skips them when projectionActive),
  // and pinning removes them from the effective sim space so visible nodes settle
  // without interference from the hidden 1600-node soup.
  if (graph) {
    for (const n of graph.nodes) {
      if (!ps.has(n.id)) { n.fx = -1e6; n.fy = -1e6; n.x = -1e6; n.y = -1e6; }
    }
  }
  renderList();
  updateProjectionStatus();
}

function updateProjectionStatus() {
  if (!projectionSet || projectionUnfolded) return;
  const n = projectionSet.size;
  setStatus("Showing " + n + " project" + (n === 1 ? "" : "s") + ". Click a project node to expand, or Show full graph.");
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
      if (n.fx === -1e6) { n.fx = null; n.fy = null; }
    }
  }
  renderList();
  const btn = el("projection-unfold-btn");
  if (btn) btn.hidden = true;
  setStatus("");
  updateHash();
  if (layoutMode === "layered") { for (const e of graph.links) delete e.layoutReversed; applyLayeredMode(); } else draw();
}

// ---- Phase 5: views ---------------------------------------------------------
// Each view answers a named question; max 7 total. View state serializes into
// the URL fragment as #view=<id>&node=<id>[&to=<id>].

// Reverse BFS over depends_on edges to collect transitive dependents of a node.
function transitiveDependents(nodeId) {
  // Build reverse adjacency for depends_on edges only.
  const revAdj = new Map();
  for (const e of graph.links) {
    const s = e.source.id || e.source;
    const t = e.target.id || e.target;
    if (e.relation !== "depends_on") continue;
    // In depends_on: source depends on target. Reverse: target -> source (dependents).
    let set = revAdj.get(t); if (!set) { set = new Set(); revAdj.set(t, set); }
    set.add(s);
  }
  const visited = new Set([nodeId]);
  let frontier = [nodeId];
  while (frontier.length) {
    const next = [];
    for (const id of frontier) {
      for (const dep of revAdj.get(id) || []) {
        if (!visited.has(dep)) { visited.add(dep); next.push(dep); }
      }
    }
    frontier = next;
  }
  return visited;
}

// Shortest path between two nodes over depends_on edges (bidirectional BFS).
function shortestDependsOnPath(fromId, toId) {
  if (fromId === toId) return [fromId];
  // Build adjacency for depends_on (directed).
  const fwdAdj = new Map(), bwdAdj = new Map();
  for (const e of graph.links) {
    const s = e.source.id || e.source, t = e.target.id || e.target;
    if (e.relation !== "depends_on") continue;
    let sf = fwdAdj.get(s); if (!sf) { sf = new Set(); fwdAdj.set(s, sf); } sf.add(t);
    let sb = bwdAdj.get(t); if (!sb) { sb = new Set(); bwdAdj.set(t, sb); } sb.add(s);
  }
  // BFS from fromId (forward), also from toId (backward). Meet in middle.
  const fwd = new Map([[fromId, [fromId]]]);
  const bwd = new Map([[toId, [toId]]]);
  let fQueue = [fromId], bQueue = [toId];
  for (let step = 0; step < graph.nodes.length; step++) {
    // Advance the smaller frontier first.
    if (!fQueue.length && !bQueue.length) break;
    if (fQueue.length) {
      const next = [];
      for (const n of fQueue) {
        for (const nb of fwdAdj.get(n) || []) {
          if (!fwd.has(nb)) { fwd.set(nb, [...fwd.get(n), nb]); next.push(nb); }
          if (bwd.has(nb)) return [...fwd.get(nb).slice(0, -1), ...bwd.get(nb).slice().reverse()];
        }
      }
      fQueue = next;
    }
    if (bQueue.length) {
      const next = [];
      for (const n of bQueue) {
        for (const nb of bwdAdj.get(n) || []) {
          if (!bwd.has(nb)) { bwd.set(nb, [...bwd.get(n), nb]); next.push(nb); }
          // Drop the meet node (nb) from the forward path to avoid duplication:
          // fwd.get(nb) ends at nb, bwd.get(nb) also starts at nb after reverse.
          if (fwd.has(nb)) return [...fwd.get(nb).slice(0, -1), ...bwd.get(nb).slice().reverse()];
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
  const hasDuration = graph.nodes.some((n) => n.DurationMs > 0 || (n.attrs && n.attrs.DurationMs > 0));
  if (!hasDuration) return null;
  const dur = (n) => +(n.DurationMs || (n.attrs && n.attrs.DurationMs) || 0);
  // Longest path in DAG (depends_on subgraph), weighted by node duration.
  const fwdAdj = new Map();
  for (const e of graph.links) {
    const s = e.source.id || e.source, t = e.target.id || e.target;
    if (e.relation !== "depends_on") continue;
    let sf = fwdAdj.get(s); if (!sf) { sf = new Set(); fwdAdj.set(s, sf); } sf.add(t);
  }
  const memo = new Map(); // id -> { cost, next }
  function dp(id) {
    if (memo.has(id)) return memo.get(id);
    let best = { cost: dur(graph.byId.get(id) || {}), next: null };
    for (const nb of fwdAdj.get(id) || []) {
      const child = dp(nb);
      const c = dur(graph.byId.get(id) || {}) + child.cost;
      if (c > best.cost) best = { cost: c, next: nb };
    }
    memo.set(id, best);
    return best;
  }
  // Find roots (no incoming depends_on).
  const hasIncoming = new Set();
  for (const e of graph.links) { if (e.relation === "depends_on") hasIncoming.add(e.target.id || e.target); }
  const roots = graph.nodes.filter((n) => !hasIncoming.has(n.id));
  let bestRoot = null, bestCost = -Infinity;
  for (const r of roots) {
    const { cost } = dp(r.id);
    if (cost > bestCost) { bestCost = cost; bestRoot = r.id; }
  }
  if (!bestRoot) return null;
  // Reconstruct path.
  const path = [];
  let cur = bestRoot;
  while (cur) { path.push(cur); cur = dp(cur).next; }
  return path.length > 1 ? path : null;
}

// Apply a named view. Updates activeView, viewNode, viewNodeTo, matchSet,
// and the CLI idiom display. Serializes into the fragment via updateHash().
function activateView(name, nodeId, nodeTo) {
  activeView = name;
  viewNode = nodeId || null;
  viewNodeTo = nodeTo || null;
  focusId = null;
  projectionUnfolded = true; // a view always shows the full graph context
  projectionSet = null;
  matchSet = null;
  if (searchEl) { searchEl.value = ""; }
  query = "";

  // Sync button active state and show the clear button.
  document.querySelectorAll(".view-btn").forEach((b) => {
    b.classList.toggle("view-active", b.dataset.view === name);
  });
  const cvb = el("clear-view-btn");
  if (cvb) cvb.hidden = false;

  // Render the CLI idiom command for this view.
  renderViewCommand(name, nodeId, nodeTo);

  switch (name) {
    case "blast": {
      if (!nodeId) {
        setStatus("Click a node to see what depends on it (blast view). CLI: magus explain <node-id>");
        renderList(); draw(); updateHash();
        return;
      }
      const deps = transitiveDependents(nodeId);
      matchSet = deps;
      const n = graph.byId ? graph.byId.get(nodeId) : null;
      setStatus("What breaks if you change " + (n ? n.label : nodeId) + "? " + (deps.size - 1) + " dependent" + (deps.size - 1 === 1 ? "" : "s") + ".");
      break;
    }
    case "trace": {
      if (!nodeId || !nodeTo) {
        setStatus("Click two nodes to find the path between them (trace view). CLI: magus path <a> <b>");
        renderList(); draw(); updateHash();
        return;
      }
      const path = shortestDependsOnPath(nodeId, nodeTo);
      if (!path) {
        const na = graph.byId ? graph.byId.get(nodeId) : null;
        const nb = graph.byId ? graph.byId.get(nodeTo) : null;
        setStatus("No depends_on path from " + (na ? na.label : nodeId) + " to " + (nb ? nb.label : nodeTo) + ".");
        matchSet = new Set([nodeId, nodeTo]);
      } else {
        matchSet = new Set(path);
        setStatus("Path: " + path.map((id) => { const n = graph.byId.get(id); return n ? n.label : id; }).join(" -> "));
      }
      break;
    }
    case "critical": {
      const path = criticalPath();
      if (!path) {
        setStatus("No duration data in this graph. Run `magus graph deps -o json` after a build to include timing.");
        matchSet = null;
      } else {
        matchSet = new Set(path);
        setStatus("Critical path: " + path.length + " node" + (path.length === 1 ? "" : "s") + " (longest duration-weighted chain).");
      }
      break;
    }
    case "hubs": {
      const top = graph.nodes.slice().sort((a, b) => b.degree - a.degree).slice(0, 12);
      matchSet = new Set(top.map((n) => n.id));
      setStatus("What's a hub? The " + matchSet.size + " highest-degree nodes.");
      break;
    }
    case "orphans": {
      matchSet = new Set(graph.nodes.filter((n) => n.degree === 0).map((n) => n.id));
      setStatus("What's dead? " + matchSet.size + " orphan node" + (matchSet.size === 1 ? "" : "s") + " with no edges.");
      break;
    }
    case "affected": {
      // Live mode: affected set is provided by caller or stored in window._liveAffectedIds.
      const aff = (typeof nodeId === "object" && nodeId) ? nodeId : window._liveAffectedIds;
      if (!aff || !aff.size) {
        setStatus("no affected nodes in current diff", true);
        matchSet = null;
      } else {
        matchSet = aff;
        setStatus("What does my diff touch? " + aff.size + " affected node" + (aff.size === 1 ? "" : "s") + " (live workspace).");
      }
      break;
    }
  }
  setListExpanded(true);
  renderList();
  if (matchSet && matchSet.size) fitView(matchSet);
  updateHash();
  draw();
}

function clearView() {
  activeView = null;
  viewNode = null;
  viewNodeTo = null;
  document.querySelectorAll(".view-btn").forEach((b) => b.classList.remove("view-active"));
  const cvb = el("clear-view-btn");
  if (cvb) cvb.hidden = true;
  renderViewCommand(null, null, null);
  matchSet = null;
  if (searchEl) searchEl.value = "";
  query = "";
  renderList();
  updateHash();
  draw();
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

function shellQuote(s) {
  // Single-quote wrap if the value contains spaces, quotes, or other special chars.
  if (/[\s'"\\$`!;|&<>(){}*?[\]#~%]/.test(s)) return "'" + s.replace(/'/g, "'\\''") + "'";
  return s;
}

// Build the full shell command for "magus query <terms>".
// Uses `--` before the terms when any term starts with `-` (negation), so the
// flag parser doesn't treat it as a flag. This matches what the CLI needs and
// what the drift fixture (query_syntax.txtar) verifies.
function buildQueryCmd(queryStr) {
  if (!queryStr) return "magus query";
  const needsDDash = queryStr.trimStart().startsWith("-");
  return "magus query " + (needsDDash ? "-- " : "") + shellQuote(queryStr);
}

// ---- Phase 7: Copy as Mermaid -----------------------------------------------
// toMermaid emits a Mermaid flowchart for a node+link subset. The output FORMAT
// is intentionally identical to the Go CLI emitters so that a diagram copied
// from the explorer would render identically to one produced by the CLI:
//
//   targets flavor  ->  WriteTargetGraphMermaid  (internal/render/targetgraph.go)
//   knowledge flavor -> WriteKnowledgeMermaid    (internal/render/knowledgegraph.go)
//
// ANTI-DRIFT: the classDef names and node shapes below MUST match the Go emitters.
// A Go test in internal/render/mermaid_drift_test.go reads this file from disk
// and asserts each name appears verbatim, so a rename on either side fails CI.
// See the KEYWORDS mirror pattern in website/js/editor.js for the house idiom.
//
// Go source literals copied for each flavor:
//
//   targets  - graph_ir.go shapeRounded ("...") / shapeHexagon {{""}} / shapeSubroutine [[""]]
//              targetgraph.go targetRoleClasses: "anchor", "target"
//              targetgraph.go externalClass:     "external"
//              targetgraph.go spellClass:        "spell"
//
//   knowledge - knowledgegraph.go prefix "kind_" + mermaidID(kind)
//               kinds seen in the graph determine the set, e.g. kind_project, kind_spell, ...
//
// Node-shape mapping (matches graph_ir.go emitNode switch):
//   shapeRounded    ->  id("label")
//   shapeHexagon    ->  id{{"label"}}
//   shapeSubroutine ->  id[["label"]]
//   shapeBox        ->  id["label"]  (default)
function toMermaid(nodes, links, flavor) {
  // Stable ordering: sort nodes and links by id for determinism.
  const sortedNodes = [...nodes].sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
  const sortedLinks = [...links].sort((a, b) => {
    const as = a.source.id || a.source, at = a.target.id || a.target;
    const bs = b.source.id || b.source, bt = b.target.id || b.target;
    if (as < bs) return -1; if (as > bs) return 1;
    if (at < bt) return -1; if (at > bt) return 1;
    return 0;
  });

  // Build a Mermaid-safe id from a node id: replace any non-word characters.
  // Mirrors the Go mermaidID helper (internal/render/render.go).
  const mermaidID = (s) => s.replace(/[^A-Za-z0-9_]/g, "_");

  // Assign stable aliases keyed by node id.
  const alias = new Map();
  // Sort the raw ids first, then enumerate, so aliases are deterministic.
  const rawIDs = [...new Set(sortedNodes.map((n) => n.id))].sort();
  rawIDs.forEach((id, i) => alias.set(id, "n" + i));

  const lines = ["graph LR"];

  if (flavor === "targets") {
    // --- targets flavor: mirror WriteTargetGraphMermaid ---
    // Shapes per kind:
    //   project   -> shapeSubroutine [["..."]]  (cross-project / external box)
    //   target    -> shapeRounded    ("...")
    //   anchor    -> shapeRounded    ("...")     (same shape, different class)
    //   spell     -> shapeHexagon    {{"..."}}
    // Classes: anchor, target, external, spell  (exact names from targetgraph.go)
    for (const n of sortedNodes) {
      const a = alias.get(n.id);
      const lbl = (n.label || n.id).replace(/"/g, "'");
      let shape;
      if (n.kind === "spell") {
        shape = `${a}{{"${lbl}"}}`;
      } else if (n.kind === "project") {
        // INTENTIONAL adaptation: Go's WriteTargetGraphMermaid renders projects as
        // subgraphs (not nodes); the explorer synthesizes real project nodes (Phase 3)
        // so we borrow the external/subroutine vocabulary ([[...]]/external class) for them.
        shape = `${a}[["${lbl}"]]`;
      } else {
        // target and anchor nodes are both shapeRounded
        shape = `${a}("${lbl}")`;
      }
      lines.push("  " + shape);
    }
    // Edges
    const seen = new Set();
    for (const e of sortedLinks) {
      const s = alias.get(e.source.id || e.source);
      const t = alias.get(e.target.id || e.target);
      if (!s || !t || s === t) continue;
      const key = s + "\x00" + t;
      if (seen.has(key)) continue;
      seen.add(key);
      const dashed = e.dashed || e.cycle || e.layoutReversed;
      const arrow = dashed ? "-.->": "-->";
      const lbl = e.relation ? `|"${e.relation}"|` : "";
      lines.push(`  ${s} ${arrow}${lbl} ${t}`);
    }
    // classDefs - exact names from targetgraph.go targetRoleClasses / externalClass / spellClass
    lines.push('  classDef anchor fill:#2563eb,color:#ffffff,stroke:#1e40af,stroke-width:2px');
    lines.push('  classDef target fill:#e2e8f0,color:#0f172a,stroke:#94a3b8');
    lines.push('  classDef external fill:#fef9c3,color:#713f12,stroke:#ca8a04,stroke-dasharray:5 3');
    lines.push('  classDef spell fill:#ede9fe,color:#4c1d95,stroke:#a78bfa');
    // class assignments
    const byClass = { anchor: [], target: [], external: [], spell: [] };
    for (const n of sortedNodes) {
      const a = alias.get(n.id);
      if (n.kind === "spell") { byClass.spell.push(a); }
      else if (n.kind === "project") { byClass.external.push(a); }
      else if (n.attrs && n.attrs.anchor === "true") { byClass.anchor.push(a); }
      else { byClass.target.push(a); }
    }
    for (const [cls, ids] of Object.entries(byClass)) {
      if (ids.length) lines.push("  class " + ids.join(",") + " " + cls);
    }
  } else {
    // --- knowledge flavor: mirror WriteKnowledgeMermaid ---
    // Shapes per kind (matches knowledgeShape in knowledgegraph.go):
    //   project -> shapeHexagon    {{"..."}}
    //   spell   -> shapeRounded    ("...")
    //   doc     -> shapeSubroutine [["..."]]
    //   default -> shapeBox        ["..."]
    for (const n of sortedNodes) {
      const a = alias.get(n.id);
      const lbl = (n.label || n.id).replace(/"/g, "'");
      let shape;
      if (n.kind === "project") {
        shape = `${a}{{"${lbl}"}}`;
      } else if (n.kind === "spell") {
        shape = `${a}("${lbl}")`;
      } else if (n.kind === "doc") {
        shape = `${a}[["${lbl}"]]`;
      } else {
        shape = `${a}["${lbl}"]`;
      }
      lines.push("  " + shape);
    }
    // Edges
    const seen = new Set();
    for (const e of sortedLinks) {
      const s = alias.get(e.source.id || e.source);
      const t = alias.get(e.target.id || e.target);
      if (!s || !t || s === t) continue;
      const key = s + "\x00" + t + "\x00" + (e.relation || "");
      if (seen.has(key)) continue;
      seen.add(key);
      const lbl = e.relation ? `|"${e.relation}"|` : "";
      lines.push(`  ${s} -->${lbl} ${t}`);
    }
    // classDefs: kind_<kind> for each kind present (sorted for determinism).
    // Palette keys are the FULL classDef names (kind_<kind>) mirroring
    // knowledgeKindPalette in knowledgegraph.go. The names below are the EXACT
    // strings the Go CLI emits; mermaid_drift_test.go asserts they appear here.
    const kindClassPalette = {
      kind_project:    { fill: "#00ADD8", text: "#fff" },
      kind_target:     { fill: "#3178C6", text: "#fff" },
      kind_spell:      { fill: "#5d4d7a", text: "#fff" },
      kind_op:         { fill: "#8a7ca8", text: "#fff" },
      kind_charm:      { fill: "#b5651d", text: "#fff" },
      kind_module:     { fill: "#2e8b57", text: "#fff" },
      kind_method:     { fill: "#3cb371", text: "#000" },
      kind_diagnostic: { fill: "#c0392b", text: "#fff" },
      kind_doc:        { fill: "#d4a017", text: "#000" },
    };
    const kindsPresent = [...new Set(sortedNodes.map((n) => n.kind))].sort();
    for (const k of kindsPresent) {
      const cls = "kind_" + mermaidID(k);
      const p = kindClassPalette[cls] || { fill: "#888888", text: "#fff" };
      lines.push(`  classDef ${cls} fill:${p.fill},color:${p.text}`);
    }
    // class assignments
    const byKind = new Map();
    for (const n of sortedNodes) {
      const cls = "kind_" + mermaidID(n.kind);
      if (!byKind.has(cls)) byKind.set(cls, []);
      byKind.get(cls).push(alias.get(n.id));
    }
    const sortedKinds = [...byKind.keys()].sort();
    for (const k of sortedKinds) {
      lines.push("  class " + byKind.get(k).join(",") + " " + k);
    }
  }

  return lines.join("\n");
}

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
    const s = e.source.id || e.source, t = e.target.id || e.target;
    return nodeSet.has(s) && nodeSet.has(t);
  });
  return { nodes, links, refused: null };
}

// copyAsMermaid: compute scope, emit mermaid, copy to clipboard, set status.
function copyAsMermaid() {
  if (!navigator.clipboard) { setStatus("clipboard unavailable in this context", true); return; }
  const { nodes, links, refused } = mermaidScope();
  if (refused) {
    setStatus(refused, true);
    return;
  }
  const text = toMermaid(nodes, links, graphFlavor);
  navigator.clipboard.writeText("```mermaid\n" + text + "\n```").then(() => {
    setStatus("Mermaid diagram copied (" + nodes.length + " nodes) - paste into a GitHub comment or PR.");
  }).catch((err) => {
    setStatus("Could not copy to clipboard: " + err.message, true);
  });
}

// Build the full CLI command string for copy-to-clipboard.
function viewCommandStr(name, nodeId, nodeTo) {
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
function renderViewCommand(name, nodeId, nodeTo) {
  const wrap = el("view-cmd");
  if (!wrap) return;
  const cmd = viewCommandStr(name, nodeId, nodeTo);
  if (!cmd) { wrap.hidden = true; return; }
  const verb = name === "blast" ? "magus explain" : name === "trace" ? "magus path" : null;
  if (!verb) { wrap.hidden = true; return; }
  const args = cmd.slice(verb.length).trim();
  wrap.hidden = false;
  wrap.innerHTML =
    '<span class="ps1" aria-hidden="true">' + escapeHtml(verb) + ' <span class="chevron">&#10095;</span></span>' +
    '<span class="view-cmd-args">' + escapeHtml(args) + '</span>' +
    '<button type="button" class="cmd-copy" title="Copy this command to the clipboard" aria-label="Copy command">&#10697;</button>';
  wrap.querySelector(".cmd-copy").addEventListener("click", () => {
    navigator.clipboard.writeText(cmd).then(() => {
      setStatus("Copied: " + cmd);
    });
  });
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
// After a graph loads, compute 3 quick facts and show clickable suggestion chips.
function renderSuggestions() {
  const wrap = el("suggestions");
  if (!wrap || !graph) { if (wrap) wrap.hidden = true; return; }
  const chips = [];

  // 1. Highest-degree node ("biggest hub").
  if (graph.nodes.length > 1) {
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
  if (hasCycle) {
    // Find the first cycle edge source as the starting point for a trace.
    const cycleEdge = graph.links.find((e) => e.cycle);
    const src = cycleEdge ? (cycleEdge.source.id || cycleEdge.source) : null;
    chips.push({
      text: "A dependency cycle was detected - trace its path?",
      action: src ? () => activateView("trace", src) : () => activateView("hubs"),
    });
  }

  // 3. Orphan count.
  const orphans = graph.nodes.filter((n) => n.degree === 0);
  if (orphans.length > 0 && chips.length < 3) {
    chips.push({
      text: orphans.length + " node" + (orphans.length === 1 ? "" : "s") + " with no edges - what's dead?",
      action: () => activateView("orphans"),
    });
  }

  if (!chips.length) { wrap.hidden = true; return; }
  wrap.hidden = false;
  wrap.innerHTML = chips.map((c, i) =>
    '<button type="button" class="suggestion-chip" data-i="' + i + '">' + escapeHtml(c.text) + '</button>'
  ).join("");
  wrap.querySelectorAll(".suggestion-chip").forEach((b) => {
    b.addEventListener("click", () => {
      chips[+b.dataset.i].action();
      wrap.hidden = true; // hide after first use
    });
  });
}

// ---- Phase 5: color preset lenses -------------------------------------------
// Three one-click presets over the existing color-group machinery. Each preset
// emits the same group entries the user could type by hand. The active preset
// serializes into the fragment as #preset=<id>.

const COLOR_PRESETS = [
  {
    id: "spell",
    label: "Color by spell",
    groups: () => {
      // One color per distinct spell in the graph.
      const spellIds = graph.nodes.filter((n) => n.kind === "spell").map((n) => n.id);
      const palette = ["#8b5cf6", "#a855f7", "#6366f1", "#0891b2", "#059669", "#d97706", "#dc2626", "#ec4899"];
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
      const palette = ["#2563eb", "#0a7ea4", "#059669", "#d97706", "#dc2626", "#8b5cf6", "#0891b2", "#ca8a04"];
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
        const s = e.source.id || e.source, t = e.target.id || e.target;
        if (e.relation !== "depends_on" || !ids.has(s) || !ids.has(t)) continue;
        let sf = fwdAdj.get(s); if (!sf) { sf = new Set(); fwdAdj.set(s, sf); } sf.add(t);
      }
      const layers = new Map();
      function layer(id) {
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
        let s = byLayer.get(l); if (!s) { s = []; byLayer.set(l, s); } s.push(id);
      }
      const maxLayer = Math.max(...byLayer.keys(), 0);
      // Return one entry per layer; each entry carries a nodeSet so groupColorFor
      // can match directly without going through parseQuery/termMatches (which
      // would require a real `layer:` query field that doesn't exist in the CLI).
      return [...byLayer.entries()].sort((a, b) => a[0] - b[0]).map(([l, ids_]) => {
        const idx = Math.round((l / Math.max(maxLayer, 1)) * (palette.length - 1));
        return { query: "layer:" + l, color: palette[idx], nodeSet: new Set(ids_) };
      });
    },
  },
];

let activePreset = null; // preset id string or null

function applyPreset(presetId) {
  const preset = COLOR_PRESETS.find((p) => p.id === presetId);
  if (!preset) return;
  // Clear previous groups.
  groups.splice(0, groups.length);
  if (activePreset === presetId) {
    // Toggle off.
    activePreset = null;
    document.querySelectorAll(".preset-btn").forEach((b) => b.classList.remove("preset-active"));
    renderGroups(); draw(); updateHash();
    return;
  }
  activePreset = presetId;
  document.querySelectorAll(".preset-btn").forEach((b) =>
    b.classList.toggle("preset-active", b.dataset.preset === presetId));
  if (!graph.relIndex) graph.relIndex = relationIndex();
  const newGroups = preset.groups();
  for (const g of newGroups) {
    // Preserve a nodeSet when the preset provides one (e.g. depth: direct id set,
    // bypasses query grammar so the coloring works even for large layers).
    const entry = { query: g.query, color: g.color, terms: parseQuery(g.query) };
    if (g.nodeSet) entry.nodeSet = g.nodeSet;
    groups.push(entry);
  }
  renderGroups();
  draw();
  updateHash();
}

// ---- Phase 9: live mode ----------------------------------------------------

// validateLiveHost, consumeLiveToken, getLiveToken, and fetchSSE now live in
// ./lib/daemon (imported at the top of this file) - the ONE audited copy of the
// loopback lock, the shared bearer token, and the fetch-based SSE reader.

// capturePositions: before replacing the graph on a live refresh, record existing
// node positions keyed by id so they can be applied to the new graph.
function capturePositions() {
  const pos = new Map();
  if (graph) {
    for (const n of graph.nodes) {
      if (n.x != null) pos.set(n.id, { x: n.x, y: n.y, fx: n.fx, fy: n.fy });
    }
  }
  return pos;
}

function applyPositions(newNodes, prevPos) {
  if (!prevPos || !prevPos.size) return;
  for (const n of newNodes) {
    const p = prevPos.get(n.id);
    if (p) {
      n.x = p.x; n.y = p.y;
      if (p.fx != null && p.fx !== -1e6) { n.fx = p.fx; n.fy = p.fy; }
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
        const top = graph.nodes.slice().sort((a, b) => b.degree - a.degree).slice(0, 12);
        matchSet = new Set(top.map((n) => n.id));
        break;
      }
      case "orphans":
        matchSet = new Set(graph.nodes.filter((n) => n.degree === 0).map((n) => n.id));
        break;
      case "affected": {
        const aff = window._liveAffectedIds;
        matchSet = (aff && aff.size) ? aff : null;
        break;
      }
      default:
        matchSet = null;
    }
    return;
  }
  if (query) {
    const terms = parseQuery(query);
    if (!terms.length) { matchSet = null; return; }
    if (!graph.relIndex) graph.relIndex = relationIndex();
    matchSet = new Set();
    for (const n of graph.nodes) {
      if (terms.every((t) => termMatches(n, t))) matchSet.add(n.id);
    }
    return;
  }
  if (!projectionUnfolded) {
    const ps = buildProjectionSet();
    if (ps) { projectionSet = ps; matchSet = new Set(ps); }
    else { projectionUnfolded = true; projectionSet = null; matchSet = null; }
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
function liveApplyGraphUpdate(data) {
  const flavor = detectFlavor(data);
  graphFlavor = flavor;
  let raw = data;
  if (flavor === "targets") {
    const nl = targetGraphToNodeLink(data);
    raw = { nodes: nl.nodes, links: nl.links };
  }
  const prevPos = capturePositions();
  graph = prepareGraph(raw);

  // Drop references to nodes the refresh removed rather than carry dangling ids.
  if (selected && !graph.byId.has(selected)) selected = null;
  if (hoverId && !graph.byId.has(hoverId)) hoverId = null;
  if (focusId && !graph.byId.has(focusId)) focusId = null;

  recomputeLiveMatchSet();
  parkHiddenNodes(); // re-park if the default projection is still active

  renderLegend();
  renderList();
  syncConditionalViews();

  // Rebuild the simulation against the new node/link arrays (same pattern as
  // boot/replaceGraph), then reapply captured positions and reheat gently
  // rather than a full alpha=1 restart.
  startSimulation();
  applyPositions(graph.nodes, prevPos);
  if (layoutMode === "layered") {
    sim.stop();
    for (const e of graph.links) delete e.layoutReversed;
    if (!applyLayeredMode()) {
      layoutMode = "force";
      syncLayoutToggle();
      startSimulation();
      applyPositions(graph.nodes, prevPos);
      sim.alpha(0.3).restart();
    }
  } else {
    sim.alpha(0.3).restart();
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
  const headers = { Authorization: "Bearer " + liveToken };
  if (liveETag) headers["If-None-Match"] = liveETag;
  let resp;
  try {
    resp = await fetch(url, { headers });
  } catch (e) {
    return; // network error on refetch; SSE reconnect will handle it
  }
  if (resp.status === 304) return; // graph unchanged; ETag matched
  if (!resp.ok) return;
  liveETag = resp.headers.get("ETag") || null;
  let data;
  try { data = await resp.json(); } catch { return; }
  liveApplyGraphUpdate(data);
}

function liveConnect() {
  if (!liveHost || !liveToken) return;
  if (liveSseAbort) liveSseAbort.abort();
  liveSseAbort = new AbortController();
  clearTimeout(liveReconnectTimer); // a fresh connect attempt supersedes any pending reconnect
  liveReconnectTimer = null;
  const url = "http://" + liveHost + "/api/v1/events";
  const headers = { Authorization: "Bearer " + liveToken };

  fetchSSE(url, headers, (eventType) => {
    if (eventType === "graph") {
      liveRefetchGraph();
    } else if (eventType === "status") {
      fetchLiveStatus();
    }
  }, (err) => {
    // Stream ended or errored: flip to disconnected, schedule reconnect.
    liveConnected = false;
    updateLiveBadge();
    showDisconnectBanner();
    clearTimeout(liveReconnectTimer);
    liveReconnectTimer = setTimeout(() => {
      liveConnect();
    }, Math.min(liveReconnectDelay, 30000));
    liveReconnectDelay = Math.min(liveReconnectDelay * 2, 30000);
  }, liveSseAbort.signal, () => {
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
  });
}

function showDisconnectBanner() {
  const banner = el("live-disconnect-banner");
  if (!banner) return;
  const now = new Date();
  const hhmm = now.getHours().toString().padStart(2, "0") + ":" + now.getMinutes().toString().padStart(2, "0");
  banner.textContent = "disconnected - showing workspace as of " + hhmm + ", reconnecting...";
  banner.hidden = false;
}

function clearDisconnectBanner() {
  const banner = el("live-disconnect-banner");
  if (banner) { banner.textContent = ""; banner.hidden = true; }
}

function updateLiveBadge() {
  const badge = el("live-badge");
  if (!badge) return;
  if (liveHost) {
    const ws = liveWorkspaceName || liveHost;
    badge.textContent = liveConnected ? "live: " + ws : "live: " + ws + " (connecting)";
    badge.hidden = false;
    badge.className = liveConnected ? "live-badge live-badge-connected" : "live-badge live-badge-disconnected";
  } else {
    badge.hidden = true;
  }
}

// updateSnapshotBadge shows "snapshot: <provenance>" for the private,
// non-live sources (a #data= fragment or a --serve loopback fetch) - the
// counterpart to the live badge for the common case of a one-shot `magus
// graph open` without a running daemon. Hidden for "demo" and "remote", and
// always hidden once live mode is active (bootLive never calls this).
function updateSnapshotBadge(source) {
  const badge = el("snapshot-badge");
  if (!badge) return;
  if (source === "local" || source === "loopback") {
    badge.textContent = "snapshot: " + source;
    badge.hidden = false;
  } else {
    badge.hidden = true;
  }
}

async function fetchLiveStatus() {
  if (!liveHost || !liveToken) return;
  try {
    const resp = await fetch("http://" + liveHost + "/api/v1/status", {
      headers: { Authorization: "Bearer " + liveToken }
    });
    if (!resp.ok) return;
    const status = await resp.json();
    // Extract workspace name from the first loaded workspace.
    if (status.pool && status.pool.workspaces && status.pool.workspaces.length > 0) {
      liveWorkspaceName = status.pool.workspaces[0].root;
    }
    // Render status strip.
    const strip = el("live-status-strip");
    if (strip && status.pool) {
      const p = status.pool;
      strip.textContent = "pool: " + p.in_use + "/" + p.capacity + " in use";
      if (p.waiting > 0) strip.textContent += ", " + p.waiting + " waiting";
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
  } catch { /* network error; badge stays */ }
}

// ---- boot ------------------------------------------------------------------
// computeDefaultProjection sets projectionUnfolded/projectionSet/matchSet for
// the initial default-projection decision at boot: a projection is shown when
// no fragment directive is present (no view/q/node[/data/src], per the
// caller's own hasFragmentDirective) and the graph has a project-node count
// buildProjectionSet is willing to collapse. Shared by boot() and bootLive()
// so the two boot paths cannot drift on this decision.
function computeDefaultProjection(hasFragmentDirective) {
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
function applyLayoutAndSimulation(requestedLayout, flavor) {
  if (requestedLayout === "force" || requestedLayout === "layered") {
    layoutMode = requestedLayout;
  } else {
    layoutMode = (flavor === "targets") ? "layered" : "force";
  }
  syncLayoutToggle();
  startSimulation();
  if (layoutMode === "layered") {
    sim.stop();
    if (!applyLayeredMode()) {
      // Scale guard fired; fall back to force.
      layoutMode = "force";
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
      if (!projectionSet.has(n.id)) { n.fx = -1e6; n.fy = -1e6; n.x = -1e6; n.y = -1e6; }
    }
  }
}

// finishInteractiveSetup wires zoom/drag, restores any view/query/layout/preset
// deep link, and renders the empty-state suggestions and conditional views.
// One-time boot wiring - shared by boot() and bootLive(), NOT called on a live
// refresh (liveApplyGraphUpdate), which reseeds data without re-wiring input.
function finishInteractiveSetup() {
  setupZoomDrag();
  // applyDeepLinks handles q= and node= (layout= is handled by
  // applyLayoutAndSimulation above; applyDeepLinks skips switching when the
  // mode already matches).
  applyDeepLinks();
  // Phase 5: emit empty-state suggestion chips.
  renderSuggestions();
  // Reveal the "What's slow?" (critical) view only when the graph has DurationMs data.
  syncConditionalViews();
}

async function boot() {
  readTheme();

  // Register file-open listeners before any early return so the installed PWA
  // can open a .json file even when the demo graph fails to load (no #data/#src
  // and the fetch of ./graph.json fails). readGraphFile/replaceGraph rebuild
  // from scratch so they tolerate an empty initial graph state.

  // Drag-drop a graph.json onto the canvas.
  canvas.addEventListener("dragover", (e) => e.preventDefault());
  canvas.addEventListener("drop", (e) => { e.preventDefault(); readGraphFile(e.dataTransfer.files[0]); });

  // File handler: when the installed PWA is launched with "Open with" on a .json file,
  // the browser delivers it here via launchQueue. Uses the same readGraphFile path as
  // drag-drop so behavior is identical. Feature-detected; no effect in browsers that
  // lack the File Handling API (all non-Chromium, and Chromium without the PWA installed).
  if ("launchQueue" in window) {
    window.launchQueue.setConsumer(async (launchParams) => {
      if (!launchParams.files || launchParams.files.length === 0) return;
      try {
        const fileHandle = launchParams.files[0];
        const f = await fileHandle.getFile();
        readGraphFile(f);
      } catch (e) {
        setStatus("Could not open the launched file: " + e.message, true);
      }
    });
  }

  // Phase 9: check for #live= fragment and attempt live-mode connection.
  // Returns true if handled (either connected or errored); false falls through.
  if (await bootLive()) return;

  const loaded = await loadGraph();
  if (!loaded) { document.body.classList.add("graph-empty"); return; }

  // Detect graph flavor at the top of the pipeline, before prepareGraph.
  // The knowledge path is byte-identical to before; the targets path is
  // converted client-side. See detectFlavor + targetGraphToNodeLink for the
  // wire-type details (types/describe.go vs types/knowledge.go).
  const flavor = detectFlavor(loaded.data);
  graphFlavor = flavor;
  let rawForPrepare = loaded.data;
  let cycleWarnings = [];

  if (flavor === "targets") {
    const nl = targetGraphToNodeLink(loaded.data);
    rawForPrepare = { nodes: nl.nodes, links: nl.links };
    cycleWarnings = nl.cycleWarnings;
  }

  graph = prepareGraph(rawForPrepare);

  // Phase 5: determine whether the default projection applies.
  // A projection is shown when no fragment directives are present (no view/q/node)
  // and the graph has project nodes whose count is <= 50.
  const bootParams = hashParams();
  // #data= and #src= mean the user (or `magus graph open`) loaded a specific graph:
  // show its full detail, not the collapsed projects-only projection.
  const hasFragmentDirective = !!(bootParams.view || bootParams.q || bootParams.node || bootParams.data || bootParams.src);
  computeDefaultProjection(hasFragmentDirective);

  // Show the unfold button when the projection is active.
  const unfoldBtn = el("projection-unfold-btn");
  if (unfoldBtn) unfoldBtn.hidden = projectionUnfolded;

  // Snapshot-mode badge: shown whenever the graph came privately from this
  // machine (a #data= fragment or a --serve loopback fetch) but is NOT live -
  // the counterpart to the live-badge, so it is always clear whether the view
  // updates automatically or is a point-in-time snapshot.
  updateSnapshotBadge(loaded.source);

  // Status line: targets flavor shows a summary; knowledge/demo shows the
  // existing brief confirmation or nothing (demo remains statusless).
  if (flavor === "targets") {
    const nProjects = (loaded.data.projects || []).length;
    const nTargets = rawForPrepare.nodes.filter((n) => n.kind === "target").length;
    const base = "target graph - " + nProjects + " project" + (nProjects === 1 ? "" : "s") +
      " - " + nTargets + " target" + (nTargets === 1 ? "" : "s");
    if (!projectionUnfolded) {
      updateProjectionStatus(); // shows projection message instead
    } else {
      setStatus(cycleWarnings.length ? base + "; " + cycleWarnings.join("; ") : base);
    }
  } else {
    // Only the private modes get a brief confirmation; the demo shows nothing (the
    // status box hides when empty) rather than a persistent overlay on the graph.
    if (!projectionUnfolded) {
      updateProjectionStatus();
    } else {
      setStatus(loaded.source === "local"
        ? "Your workspace graph - it never left your machine."
        : loaded.source === "loopback"
        ? "Your workspace graph, served over loopback - it never left your network."
        : "");
    }
  }

  renderLegend();
  renderList();

  // Determine layout mode: fragment overrides flavor default.
  // targets -> layered; knowledge -> force.
  const initialParams = hashParams();
  applyLayoutAndSimulation(initialParams.layout, flavor);
  parkHiddenNodes();
  finishInteractiveSetup();

  // Wow reveal: once the cold force layout has spread out, frame the whole graph so it
  // lands centered and fully in view instead of cropped to a corner. Only for the
  // default full-graph view - a deep link (#node/#view/#q) or the perf-guard projection
  // already frames its own subset, and layered layout is framed by applyLayeredMode.
  if (projectionUnfolded && !hasFragmentDirective && layoutMode !== "layered" && graph.nodes.length) {
    setTimeout(() => fitView(null), 700);
  }

  // Empty state: nothing was loaded (no #data/#src/#live), so instead of a graph we show an
  // intuitive prompt - the command to open your own workspace, plus a button to try the demo.
  // The full pipeline above ran on an empty graph, so interactions are wired; the demo (or a
  // dropped file) loads via replaceGraph and dismisses this. See loadGraph / loadDemoGraph.
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

  // Wire the projection unfold button ("Show full graph").
  const unfoldBtnWire = el("projection-unfold-btn");
  if (unfoldBtnWire) {
    unfoldBtnWire.addEventListener("click", () => {
      unfoldProjection();
      renderSuggestions(); // re-render suggestions after full graph is visible
    });
  }

  // Wire view buttons (.view-btn). Blast and trace need node-picking mode.
  document.querySelectorAll(".view-btn").forEach((b) => {
    b.addEventListener("click", () => {
      const v = b.dataset.view;
      if (b.disabled || b.classList.contains("view-disabled")) return;
      if (activeView === v) { clearView(); return; }
      if (v === "blast" || v === "trace") {
        // Enter picking mode: status tells user to click a node.
        activeView = v;
        viewNode = null;
        viewNodeTo = null;
        document.querySelectorAll(".view-btn").forEach((x) => x.classList.toggle("view-active", x.dataset.view === v));
        renderViewCommand(v, null, null);
        if (v === "blast") setStatus("Click a node to see what breaks if you change it.");
        else setStatus("Click the first node for the path (trace view).");
        updateHash();
      } else if (v === "affected") {
        // Affected view is wired separately in live mode; clicking here
        // when not in live mode shows a hint.
        const aff = window._liveAffectedIds;
        if (!aff || !aff.size) {
          setStatus("affected view: requires live mode (magus graph open --live) with a computed diff.", true);
          return;
        }
        activateView("affected", aff);
      } else {
        activateView(v);
      }
    });
  });

  // Wire the clear-view button.
  const clearViewBtn = el("clear-view-btn");
  if (clearViewBtn) clearViewBtn.addEventListener("click", clearView);

  // The count row toggles the (default-collapsed) node cloud.
  const listToggle = el("list-toggle");
  if (listToggle) listToggle.addEventListener("click", () => setListExpanded(!listExpanded));

  // Zoom-to-fit: frame the current matches (or the whole graph) in the viewport.
  const fitBtn = el("fit-btn");
  if (fitBtn) fitBtn.addEventListener("click", () => fitView(matchSet && matchSet.size ? matchSet : null));

  // Mobile-only legend toggle: on narrow screens the kind legend is collapsed off
  // the canvas by default (CSS) so it doesn't cover the graph; this flips it open.
  // Harmless on desktop, where the toggle is display:none and the legend is always
  // shown.
  const legendToggle = el("legend-toggle");
  const legendEl = el("graph-legend");
  if (legendToggle && legendEl) {
    legendToggle.addEventListener("click", () => {
      const open = legendEl.classList.toggle("legend-open");
      legendToggle.setAttribute("aria-expanded", open ? "true" : "false");
    });
  }

  // Lenses (magus graph stats parity): hubs / orphans set the match set.
  document.querySelectorAll(".lens-btn").forEach((b) =>
    b.addEventListener("click", () => applyLens(b.dataset.lens)));

  // Phase 5: color preset buttons.
  document.querySelectorAll(".preset-btn").forEach((b) => {
    b.addEventListener("click", () => applyPreset(b.dataset.preset));
  });

  // Color groups: add a query -> color painting.
  const groupAdd = el("group-add");
  if (groupAdd) {
    groupAdd.addEventListener("click", addGroup);
    el("group-query").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); addGroup(); } });
  }

  // Live force sliders: adjust the running simulation and gently reheat.
  const wireForce = (id, apply) => {
    const input = el(id);
    if (!input) return;
    input.addEventListener("input", () => { if (sim) { apply(+input.value); sim.alpha(0.3).restart(); } });
  };
  wireForce("force-charge", (v) => sim.force("charge").strength(-v));
  wireForce("force-link", (v) => sim.force("link").distance(v));
  wireForce("force-gravity", (v) => { sim.force("x").strength(v / 100); sim.force("y").strength(v / 100); });

  // Keyboard: Esc clears a focus/query; [ and ] shrink/grow the focus depth.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { clearFocusOrQuery(); if (searchEl.blur) searchEl.blur(); return; }
    if (e.target === searchEl) return; // don't hijack typing
    if (e.key === "[") changeFocusDepth(-1);
    else if (e.key === "]") changeFocusDepth(1);
  });

  // Query-syntax reference: each example runs itself in the filter (teach-by-doing).
  // Scope to [data-q] so the lens/add-group buttons (which share .q-example for its
  // chip styling but carry no data-q) aren't wired as examples.
  document.querySelectorAll(".q-example[data-q]").forEach((b) =>
    b.addEventListener("click", () => {
      const q = b.dataset.q;
      searchEl.value = q;
      applyQuery(q);
      searchEl.focus();
      document.querySelector(".graph-app").scrollIntoView({ behavior: "smooth", block: "nearest" });
    }));

  // "Copy as Mermaid" toolbar button: emit the current scope as a mermaid diagram.
  const copyMermaidBtn = el("copy-mermaid-btn");
  if (copyMermaidBtn) copyMermaidBtn.addEventListener("click", copyAsMermaid);

  // "Open file" toolbar button proxies to the hidden <input type=file>.
  const openBtn = el("open-file-btn");
  if (openBtn && fileInput) openBtn.addEventListener("click", () => fileInput.click());
  if (fileInput) fileInput.addEventListener("change", () => readGraphFile(fileInput.files[0]));

  // Empty state: "Explore the magus graph" fetches the committed demo graph.json ON DEMAND
  // (not at boot) and renders it via replaceGraph, then dismisses the empty state. A dropped
  // file or `magus graph open` link dismisses it the same way (loadDemoGraph / readGraphFile).
  const demoExplore = el("demo-explore-btn");
  if (demoExplore) demoExplore.addEventListener("click", () => loadDemoGraph());

  // Fullscreen toggle: expand the whole explorer panel (like the playground).
  // Hidden if the browser lacks the Fullscreen API rather than showing a dead
  // button; label + aria-pressed follow fullscreenchange so Esc stays in sync.
  const fsBtn = el("fullscreen-btn");
  const appEl = document.querySelector(".graph-app");
  if (fsBtn && appEl && appEl.requestFullscreen) {
    fsBtn.addEventListener("click", () => {
      if (document.fullscreenElement) document.exitFullscreen();
      else appEl.requestFullscreen();
    });
    const fsLabel = fsBtn.querySelector(".btn-label");
    document.addEventListener("fullscreenchange", () => {
      const on = document.fullscreenElement === appEl;
      if (fsLabel) fsLabel.textContent = on ? "Exit" : "Fullscreen";
      fsBtn.setAttribute("aria-pressed", on ? "true" : "false");
      // The canvas is sized to its box; refit after the panel resizes.
      resizeCanvas();
      if (sim) { sim.force("center", forceCenter(canvas.clientWidth / 2, canvas.clientHeight / 2)); sim.alpha(0.15).restart(); }
      draw();
    });
  } else if (fsBtn) {
    fsBtn.hidden = true;
  }

  // Re-read Pico variables and repaint on a theme toggle (mirrors mermaid.js).
  let t = 0;
  const rerender = () => { clearTimeout(t); t = setTimeout(() => { readTheme(); renderLegend(); renderList(); draw(); }, 0); };
  new MutationObserver(rerender).observe(root, { attributes: true, attributeFilter: ["data-theme"] });
  matchMedia("(prefers-color-scheme: dark)").addEventListener("change", rerender);

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
      if (sim) { sim.force("center", forceCenter(canvas.clientWidth / 2, canvas.clientHeight / 2)); sim.alpha(0.1).restart(); }
      draw();
    });
  };
  new ResizeObserver(onStageResize).observe(canvas);
  window.addEventListener("hashchange", () => { suppressHash = true; applyDeepLinks(); suppressHash = false; });

  // Keep the gentle wobble from being a background CPU drain: stop the sim while
  // the tab is hidden, resume when it returns. Also honor a live change to the
  // reduced-motion preference. In layered mode the sim stays stopped (no wobble).
  document.addEventListener("visibilitychange", () => {
    if (!sim) return;
    if (document.hidden) sim.stop();
    else if (layoutMode !== "layered") sim.alphaTarget(idleAlpha()).restart();
  });
  reducedMotion.addEventListener("change", () => {
    if (sim && layoutMode !== "layered") sim.alphaTarget(idleAlpha()).restart();
  });

  // Wire the layout toggle button.
  const layoutToggleBtn = el("layout-toggle-btn");
  if (layoutToggleBtn) {
    layoutToggleBtn.addEventListener("click", () => {
      switchLayout(layoutMode === "layered" ? "force" : "layered");
    });
  }

  // Phase 9: wire the live-mode "Remember this workspace" checkbox.
  const rememberCb = el("live-remember-cb");
  if (rememberCb) {
    rememberCb.checked = localStorage.getItem("magus-live-remember") === "1";
    rememberCb.addEventListener("change", () => {
      if (rememberCb.checked) {
        localStorage.setItem("magus-live-remember", "1");
        if (liveToken) localStorage.setItem("magus-live-token", liveToken);
      } else {
        localStorage.removeItem("magus-live-remember");
        localStorage.removeItem("magus-live-token");
      }
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
  if (!params.live) return false;

  const hostPort = params.live;
  const normalizedHost = validateLiveHost(hostPort);
  if (!normalizedHost) {
    setStatus("live mode refused: host must be literally 127.0.0.1 or [::1] - got: " + hostPort + ". No network request was made.", true);
    document.body.classList.add("graph-empty");
    return true; // handled (with error); don't fall through
  }

  liveHost = normalizedHost;
  liveFlavor = params.flavor || null;

  // Consume and store the token (strips it from the URL fragment).
  if (params.token) {
    consumeLiveToken(params);
  }
  liveToken = getLiveToken();
  if (!liveToken) {
    setStatus("live mode: no token found. Re-run magus graph open --live to get a fresh link.", true);
    document.body.classList.add("graph-empty");
    return true;
  }

  // Skeleton-first: fetch ?level=projects first (KBs at any scale).
  setStatus("Connecting to live workspace...");
  try {
    const skeletonUrl = "http://" + liveHost + "/api/v1/graph?level=projects";
    const skeletonResp = await fetch(skeletonUrl, {
      headers: { Authorization: "Bearer " + liveToken }
    });
    if (!skeletonResp.ok) throw new Error("HTTP " + skeletonResp.status);
    liveETag = skeletonResp.headers.get("ETag") || null;
    liveGraphQuery = "?level=projects";
    const skeletonData = await skeletonResp.json();

    // Fetch /api/v1/status for workspace name and pool info.
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
      const fullResp = await fetch(fullUrl, { headers: { Authorization: "Bearer " + liveToken } });
      if (fullResp.ok) {
        liveETag = fullResp.headers.get("ETag") || null;
        liveGraphQuery = fullQuery;
        const fullData = await fullResp.json();
        const ff = detectFlavor(fullData);
        graphFlavor = ff;
        let rr = fullData;
        if (ff === "targets") { const nl = targetGraphToNodeLink(fullData); rr = { nodes: nl.nodes, links: nl.links }; }
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

    renderLegend(); renderList();

    applyLayoutAndSimulation(params.layout, graphFlavor);
    parkHiddenNodes();
    finishInteractiveSetup();

    // Connect SSE for live updates.
    liveConnect();

    // Wire all common event listeners.
    bootWireEvents();
    return true;
  } catch (e) {
    setStatus("live mode: could not connect to daemon at " + liveHost + ": " + e.message + ". Start it with: magus server start", true);
    liveHost = null; liveToken = null;
    return false; // fall through to normal load
  }
}

boot();
