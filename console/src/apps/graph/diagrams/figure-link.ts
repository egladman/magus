// The graph a shared link carries in its fragment: #figure=<unpadded base64url of UTF-8 JSON>.
// .github/actions/advice/figure-link.buzz writes it, so a change to the shape is a change there.
// A fragment never reaches a server, which is why the hosted console can draw it with no backend.

export const FIGURE_LINK_VERSION = 1;

export interface LinkedNode {
  readonly id: string;
  readonly label: string;
  // The node the change edited.
  readonly seed: boolean;
}

export interface LinkedFigure {
  readonly title: string;
  readonly nodes: readonly LinkedNode[];
  // [dependency id, dependent id], both ids of a node above.
  readonly edges: readonly (readonly [string, string])[];
}

export type FigureLinkRead =
  | { readonly ok: true; readonly figure: LinkedFigure }
  | { readonly ok: false; readonly error: string };

const fail = (error: string): FigureLinkRead => ({ ok: false, error });

function record(v: unknown): Record<string, unknown> | null {
  return typeof v === "object" && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : null;
}

// bytesOf undoes the unpadded base64url. The alphabet is checked first because atob skips
// whitespace and accepts what a link never carries.
function bytesOf(raw: string): Uint8Array | string {
  const payload = raw.replace(/=+$/, "");
  if (payload === "") return "the link carries no figure";
  if (!/^[A-Za-z0-9_-]+$/.test(payload)) return "the link is not base64url";
  if (payload.length % 4 === 1) return "the link is truncated";
  const std = payload.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(std + "=".repeat((4 - (std.length % 4)) % 4));
  return Uint8Array.from(binary, (c) => c.charCodeAt(0));
}

// decodeFigureLink reads the payload after "#figure=". Every failure names what was wrong
// with the link; none throws.
export function decodeFigureLink(payload: string): FigureLinkRead {
  const bytes = bytesOf(payload);
  if (typeof bytes === "string") return fail(bytes);
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return fail("the link is not UTF-8 text");
  }
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch (e) {
    return fail("the link is not JSON: " + (e instanceof Error ? e.message : String(e)));
  }
  const top = record(doc);
  if (!top) return fail("the link holds no object");
  if (top.v !== FIGURE_LINK_VERSION)
    return fail(
      "the link is version " +
        JSON.stringify(top.v) +
        " and this console reads version " +
        FIGURE_LINK_VERSION,
    );
  if (top.kind !== "deps")
    return fail('the link draws a "' + String(top.kind) + '" figure and this console draws "deps"');
  if (typeof top.title !== "string") return fail("the link has no title");
  if (!Array.isArray(top.nodes)) return fail("the link has no nodes");

  const nodes: LinkedNode[] = [];
  const ids = new Set<string>();
  for (const [i, raw] of top.nodes.entries()) {
    const n = record(raw);
    if (!n || typeof n.id !== "string" || n.id === "" || typeof n.label !== "string")
      return fail("node " + i + " has no id and label");
    if (ids.has(n.id)) return fail("node id " + JSON.stringify(n.id) + " appears twice");
    ids.add(n.id);
    nodes.push({ id: n.id, label: n.label, seed: n.seed === true });
  }

  if (!Array.isArray(top.edges)) return fail("the link has no edges");
  const edges: [string, string][] = [];
  for (const [i, raw] of top.edges.entries()) {
    if (!Array.isArray(raw) || raw.length !== 2 || raw.some((end) => typeof end !== "string"))
      return fail("edge " + i + " is not a [from, to] pair of ids");
    const [from, to] = raw as [string, string];
    for (const end of [from, to])
      if (!ids.has(end)) return fail("edge " + i + " names " + JSON.stringify(end) + ", no node");
    edges.push([from, to]);
  }
  return { ok: true, figure: { title: top.title, nodes, edges } };
}
