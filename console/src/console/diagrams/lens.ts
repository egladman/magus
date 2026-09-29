// lens.ts - the declared lens a figure is drawn through (scope, focus, depth), its query string
// for the server render, and its round trip through the console's #fragment so a view is
// addressable.

export interface Lens {
  readonly scope: readonly string[];
  readonly focus: string;
  readonly depth: number | null;
}

export const EMPTY_LENS: Lens = { scope: [], focus: "", depth: null };

// lensQuery mirrors the handler's parseLens: scope repeats, depth is sent only when set, and an
// empty lens is the bare figure URL.
export function lensQuery(lens: Lens): string {
  const q = new URLSearchParams();
  for (const s of lens.scope) q.append("scope", s);
  if (lens.focus) q.set("focus", lens.focus);
  if (lens.depth !== null) q.set("depth", String(lens.depth));
  const s = q.toString();
  return s ? "?" + s : "";
}

export type LensParse = { ok: true; lens: Lens } | { ok: false; error: string };

export interface LensFields {
  scope: string;
  focus: string;
  depth: string;
}

// parseLensFields reads the form. Scope is comma or whitespace separated. Depth without a focus
// is refused here because the server refuses it too, and a form error beats a round trip.
export function parseLensFields(fields: LensFields): LensParse {
  const scope = fields.scope
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
  const focus = fields.focus.trim();
  const rawDepth = fields.depth.trim();
  let depth: number | null = null;
  if (rawDepth !== "") {
    if (!/^\d+$/.test(rawDepth))
      return { ok: false, error: "Depth must be a whole number, 0 or more." };
    depth = Number(rawDepth);
    if (!focus) return { ok: false, error: "Depth needs a focus." };
  }
  return { ok: true, lens: { scope, focus, depth } };
}

export function lensIsEmpty(lens: Lens): boolean {
  return lens.scope.length === 0 && lens.focus === "" && lens.depth === null;
}

export interface DeclaredNode {
  readonly id: string;
  readonly anchor: string;
  readonly label: string;
}

// Declaration is a figure's input before a lens cuts it: what the server declared to flow.
export interface Declaration {
  readonly nodes: readonly DeclaredNode[];
  readonly edges: readonly (readonly [string, string])[];
}

export type Cut = { ok: true; decl: Declaration } | { ok: false; error: string };

function cleanScope(s: string): string {
  const parts = s.split("/").filter((p) => p !== "" && p !== ".");
  return parts.length ? parts.join("/") : ".";
}

function inScope(anchor: string, scope: readonly string[]): boolean {
  if (scope.length === 0) return true;
  return scope.some((raw) => {
    const s = cleanScope(raw);
    return s === "." || anchor === s || anchor.startsWith(s + "/");
  });
}

// cutDeclaration is the handler's graph.cut, so a lens applied in the browser keeps exactly the
// nodes and edges the server would: scope first, then everything within depth undirected hops of
// the focus, walking only nodes the scope kept. It never adds an edge.
export function cutDeclaration(decl: Declaration, lens: Lens): Cut {
  const depth = lens.depth ?? (lens.focus ? 1 : 0);
  if (!lens.focus && depth > 0) return { ok: false, error: "depth needs a focus" };
  const keep = new Set(decl.nodes.filter((n) => inScope(n.anchor, lens.scope)).map((n) => n.id));
  if (lens.focus) {
    const f = decl.nodes.find(
      (n) => keep.has(n.id) && (n.id === lens.focus || n.anchor === lens.focus),
    );
    if (!f) return { ok: false, error: 'focus "' + lens.focus + '" is not in the figure' };
    const adj = new Map<string, string[]>();
    for (const [a, b] of decl.edges) {
      if (!keep.has(a) || !keep.has(b)) continue;
      adj.set(a, [...(adj.get(a) ?? []), b]);
      adj.set(b, [...(adj.get(b) ?? []), a]);
    }
    const seen = new Set([f.id]);
    let frontier = [f.id];
    for (let i = 0; i < depth; i++) {
      const next: string[] = [];
      for (const id of frontier)
        for (const o of adj.get(id) ?? [])
          if (!seen.has(o)) {
            seen.add(o);
            next.push(o);
          }
      frontier = next;
    }
    keep.clear();
    for (const id of seen) keep.add(id);
  }
  return {
    ok: true,
    decl: {
      nodes: decl.nodes.filter((n) => keep.has(n.id)),
      edges: decl.edges.filter(([a, b]) => keep.has(a) && keep.has(b)),
    },
  };
}

export function lensFields(lens: Lens): LensFields {
  return {
    scope: lens.scope.join(", "),
    focus: lens.focus,
    depth: lens.depth === null ? "" : String(lens.depth),
  };
}

// describeLens is the handler's Lens.describe, so a figure laid out in the browser carries the
// same subtitle as one the server drew. The server defaults depth to 1 under a focus.
export function describeLens(lens: Lens): string {
  const parts: string[] = [];
  if (lens.scope.length) parts.push("scope " + lens.scope.join(", "));
  if (lens.focus) parts.push("focus " + lens.focus + ", depth " + (lens.depth ?? 1));
  return parts.join("; ");
}

// A view is addressable: the figure and its lens ride in the #fragment beside whatever else the
// console keeps there (port, token, demo), which these keys never touch.
const VIEW_KEYS = ["diagram", "scope", "focus", "depth"];

export interface DiagramView {
  readonly id: string | null;
  readonly lens: Lens;
}

// viewFromHash reads what fragmentForView writes. A malformed depth is dropped rather than
// refused, so a hand-edited link still opens its figure.
export function viewFromHash(params: Readonly<Record<string, string>>): DiagramView {
  const id = params.diagram ? params.diagram : null;
  const scope = (params.scope ?? "").split(",").filter(Boolean);
  const focus = params.focus ?? "";
  const depth = params.depth && /^\d+$/.test(params.depth) && focus ? Number(params.depth) : null;
  return { id, lens: { scope, focus, depth } };
}

// fragmentForView rewrites only the view keys and keeps every other key where it was.
export function fragmentForView(
  params: Readonly<Record<string, string>>,
  view: DiagramView,
): string {
  const parts: string[] = [];
  for (const [k, v] of Object.entries(params)) {
    if (VIEW_KEYS.includes(k)) continue;
    const key = encodeURIComponent(k);
    parts.push(v === "" ? key : key + "=" + encodeURIComponent(v));
  }
  if (view.id) {
    parts.push("diagram=" + encodeURIComponent(view.id));
    if (view.lens.scope.length)
      parts.push("scope=" + view.lens.scope.map(encodeURIComponent).join(","));
    if (view.lens.focus) parts.push("focus=" + encodeURIComponent(view.lens.focus));
    if (view.lens.depth !== null) parts.push("depth=" + view.lens.depth);
  }
  return parts.length ? "#" + parts.join("&") : "";
}
