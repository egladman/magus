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
    const loopback = /^https?:\/\/(127\.0\.0\.1|localhost)(:|\/)/.test(params.src);
    try {
      setStatus("Fetching the graph...");
      const r = await fetch(params.src, { headers: { Accept: "application/json" } });
      if (!r.ok) throw new Error("HTTP " + r.status);
      return { data: await r.json(), source: loopback ? "loopback" : "remote" };
    } catch (e) {
      setStatus("Could not fetch the graph from that URL (" + e.message + ")." + (loopback ? " Is `magus graph open --serve` still running?" : ""), true);
    }
  }
  // No (usable) fragment: fall back to the committed demo graph beside this page.
  try {
    setStatus("Loading the magus demo graph...");
    const r = await fetch("./graph.json");
    if (!r.ok) throw new Error("HTTP " + r.status);
    return { data: await r.json(), source: "demo" };
  } catch (e) {
    setStatus("No graph loaded. Drop a graph.json exported with `magus graph export -o json`.", true);
    return null;
  }
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
  ctx.lineWidth = 0.6 / transform.k;
  for (const e of graph.links) {
    const s = e.source, t = e.target;
    if (s.x == null || t.x == null) continue; // not "!s.x": a node validly at x=0 must still draw
    let active;
    if (highlight) active = s.id === highlight || t.id === highlight;
    else if (matchSet) {
      // Under a query filter, draw ONLY edges between two matches - skipping the
      // rest keeps the matching subgraph clean instead of a faint full-web haze.
      if (!(matchSet.has(s.id) && matchSet.has(t.id))) continue;
      active = true;
    } else active = true;
    ctx.strokeStyle = active ? theme.muted : theme.border;
    ctx.globalAlpha = active ? 0.55 : 0.1;
    // Cycle edges (from the target-graph adapter) get a dashed stroke so they
    // stand out from normal dependency edges.
    if (e.cycle) ctx.setLineDash([4 / transform.k, 3 / transform.k]);
    ctx.beginPath();
    ctx.moveTo(s.x, s.y);
    ctx.lineTo(t.x, t.y);
    ctx.stroke();
    if (e.cycle) ctx.setLineDash([]);
  }
  ctx.globalAlpha = 1;

  // Nodes. When something is highlighted, fade non-neighbors; when a search is
  // active, fade non-matches.
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    let alpha = 1;
    if (highlight) alpha = n.id === highlight || (near && near.has(n.id)) ? 1 : 0.15;
    else if (matchSet) alpha = matchSet.has(n.id) ? 1 : 0.12;
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

  // Labels: only for big nodes, the selection, or when zoomed in - avoids
  // painting 1600 labels into an unreadable smear.
  ctx.fillStyle = theme.text;
  ctx.font = "500 " + (11 / transform.k) + "px " + theme.font;
  ctx.textAlign = "left";
  ctx.textBaseline = "middle";
  for (const n of graph.nodes) {
    if (n.x == null) continue;
    const show = n.id === highlight || n.degree > 24 || transform.k > 2.2;
    if (!show) continue;
    if (matchSet && !matchSet.has(n.id) && n.id !== highlight) continue;
    ctx.fillText(n.label, n.x + n.r + 2 / transform.k, n.y);
  }
  ctx.restore();
}

// ---- interaction -----------------------------------------------------------
function nodeAtPointer(event) {
  const rect = canvas.getBoundingClientRect();
  const px = (event.clientX - rect.left - transform.x) / transform.k;
  const py = (event.clientY - rect.top - transform.y) / transform.k;
  return sim.find(px, py, 30 / transform.k);
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
      if (!event.active) sim.alphaTarget(0.2).restart();
      event.subject.fx = event.subject.x;
      event.subject.fy = event.subject.y;
    })
    .on("drag", (event) => {
      if (!event.subject) return;
      event.subject.fx = (event.x - transform.x) / transform.k;
      event.subject.fy = (event.y - transform.y) / transform.k;
    })
    .on("end", (event) => {
      if (!event.subject) return;
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
  cardEl.innerHTML = html;
  cardEl.hidden = false;
  cardEl.querySelectorAll(".node-ref").forEach((b) =>
    b.addEventListener("click", () => selectNode(b.dataset.id, true)));
}

// ---- selection, search, list, deep links -----------------------------------
function selectNode(id, center) {
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
  searchEl.value = "";
  setStatus("");
  renderList();
  draw();
}

// Lenses mirror `magus graph stats`: hubs = the highest-degree "god" nodes,
// orphans = nodes with no edges. Each just sets the match set, reusing the filter
// rendering + list.
function applyLens(name) {
  focusId = null;
  query = "";
  searchEl.value = "";
  if (name === "hubs") {
    const top = graph.nodes.slice().sort((a, b) => b.degree - a.degree).slice(0, 12);
    matchSet = new Set(top.map((n) => n.id));
    setStatus("Hubs: the " + matchSet.size + " highest-degree nodes (the `magus graph stats` god nodes).");
  } else {
    matchSet = new Set(graph.nodes.filter((n) => n.degree === 0).map((n) => n.id));
    setStatus(matchSet.size + " orphan node" + (matchSet.size === 1 ? "" : "s") + " with no edges (the `magus graph stats` orphans).");
  }
  setListExpanded(true);
  renderList();
  if (matchSet.size) fitView(matchSet);
  draw();
}

// ---- color groups ----------------------------------------------------------
// Each group paints every node matching a query one chosen color, ON TOP of the
// kind palette - so several groups can coexist (unlike the single match set). The
// groups reuse the same query grammar (parseQuery/termMatches) as the filter box.
const groups = []; // { query, color, terms }

function groupColorFor(node) {
  for (const g of groups) {
    if (g.terms.length && g.terms.every((t) => termMatches(node, t))) return g.color;
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
// field filters kind:/project:/relation:/id:; free text matches id/label/doc;
// "quoted" spans stay one term; a leading - negates. So a query typed here (or
// arriving in #q=) selects the same nodes `magus query` would seed.
const QUERY_FIELDS = ["kind", "project", "relation", "id"];

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
    default:
      hit = node.id.toLowerCase().includes(v) || node.label.toLowerCase().includes(v) ||
        (node.doc && node.doc.toLowerCase().includes(v));
  }
  return term.negated ? !hit : hit;
}

function applyQuery(q) {
  focusId = null; // typing a query exits focus/lens mode
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

// Reflect selection + query in the hash WITHOUT clobbering a #data= fragment
// (that would round-trip the whole graph through history on every click).
let suppressHash = false;
function updateHash() {
  if (suppressHash) return;
  const params = hashParams();
  if (params.data || params.src) return; // keep the private data/loopback link intact; don't rewrite it
  const parts = [];
  if (query) parts.push("q=" + encodeURIComponent(query));
  if (selected) parts.push("node=" + encodeURIComponent(selected));
  const next = parts.length ? "#" + parts.join("&") : "#";
  if (location.hash !== next) history.replaceState(null, "", next);
}

function applyDeepLinks() {
  const params = hashParams();
  if (params.q) { searchEl.value = params.q; applyQuery(params.q); }
  if (params.node && graph.byId.has(params.node)) selectNode(params.node, true);
}

// Swap in a graph loaded from a local file (the Open-file button and drag-drop
// share this). Resets view state and restarts the layout.
function replaceGraph(data, statusMsg) {
  // Detect and adapt flavor before prepareGraph, same as boot(). The knowledge
  // path is unchanged; the targets path is converted client-side.
  const flavor = detectFlavor(data);
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
  selected = null;
  hoverId = null;
  focusId = null;
  matchSet = null;
  searchEl.value = "";
  renderCard(null);
  setStatus(statusMsg);
  renderLegend();
  renderList();
  startSimulation();
  draw();
}

async function readGraphFile(file) {
  if (!file) return;
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

// ---- boot ------------------------------------------------------------------
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

  const loaded = await loadGraph();
  if (!loaded) { document.body.classList.add("graph-empty"); return; }

  // Detect graph flavor at the top of the pipeline, before prepareGraph.
  // The knowledge path is byte-identical to before; the targets path is
  // converted client-side. See detectFlavor + targetGraphToNodeLink for the
  // wire-type details (types/describe.go vs types/knowledge.go).
  const flavor = detectFlavor(loaded.data);
  let rawForPrepare = loaded.data;
  let cycleWarnings = [];

  if (flavor === "targets") {
    const nl = targetGraphToNodeLink(loaded.data);
    rawForPrepare = { nodes: nl.nodes, links: nl.links };
    cycleWarnings = nl.cycleWarnings;
  }

  graph = prepareGraph(rawForPrepare);

  // Status line: targets flavor shows a summary; knowledge/demo shows the
  // existing brief confirmation or nothing (demo remains statusless).
  if (flavor === "targets") {
    const nProjects = (loaded.data.projects || []).length;
    const nTargets = rawForPrepare.nodes.filter((n) => n.kind === "target").length;
    const base = "target graph - " + nProjects + " project" + (nProjects === 1 ? "" : "s") +
      " - " + nTargets + " target" + (nTargets === 1 ? "" : "s");
    setStatus(cycleWarnings.length ? base + "; " + cycleWarnings.join("; ") : base);
  } else {
    // Only the private modes get a brief confirmation; the demo shows nothing (the
    // status box hides when empty) rather than a persistent overlay on the graph.
    setStatus(loaded.source === "local"
      ? "Your workspace graph - it never left your machine."
      : loaded.source === "loopback"
      ? "Your workspace graph, served over loopback - it never left your network."
      : "");
  }

  renderLegend();
  renderList();
  startSimulation();
  setupZoomDrag();
  applyDeepLinks();

  // Debounce typing so a large graph isn't re-filtered + re-rendered on every
  // keystroke; the legend/example/deep-link paths call applyQuery directly (no wait).
  let queryTimer = 0;
  searchEl.addEventListener("input", () => {
    clearTimeout(queryTimer);
    queryTimer = setTimeout(() => applyQuery(searchEl.value), 120);
  });
  searchEl.disabled = false;

  // The count row toggles the (default-collapsed) node cloud.
  const listToggle = el("list-toggle");
  if (listToggle) listToggle.addEventListener("click", () => setListExpanded(!listExpanded));

  // Zoom-to-fit: frame the current matches (or the whole graph) in the viewport.
  const fitBtn = el("fit-btn");
  if (fitBtn) fitBtn.addEventListener("click", () => fitView(matchSet && matchSet.size ? matchSet : null));

  // Lenses (magus graph stats parity): hubs / orphans set the match set.
  document.querySelectorAll(".lens-btn").forEach((b) =>
    b.addEventListener("click", () => applyLens(b.dataset.lens)));

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

  // "Open file" toolbar button proxies to the hidden <input type=file>.
  const openBtn = el("open-file-btn");
  if (openBtn && fileInput) openBtn.addEventListener("click", () => fileInput.click());
  if (fileInput) fileInput.addEventListener("change", () => readGraphFile(fileInput.files[0]));

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

  window.addEventListener("resize", () => { resizeCanvas(); if (sim) { sim.force("center", forceCenter(canvas.clientWidth / 2, canvas.clientHeight / 2)); sim.alpha(0.1).restart(); } draw(); });
  window.addEventListener("hashchange", () => { suppressHash = true; applyDeepLinks(); suppressHash = false; });

  // Keep the gentle wobble from being a background CPU drain: stop the sim while
  // the tab is hidden, resume when it returns. Also honor a live change to the
  // reduced-motion preference.
  document.addEventListener("visibilitychange", () => {
    if (!sim) return;
    if (document.hidden) sim.stop();
    else sim.alphaTarget(idleAlpha()).restart();
  });
  reducedMotion.addEventListener("change", () => {
    if (sim) sim.alphaTarget(idleAlpha()).restart();
  });
}

boot();
