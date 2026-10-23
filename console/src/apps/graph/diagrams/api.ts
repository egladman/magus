// Typed client for GET /api/v1/diagrams and /api/v1/diagrams/{id}. Every answer that is not a
// figure is reported here (a toast at minimum) and also returned as a read the view shows inline.

import { reportFailure } from "../../../lib/notifications";
import {
  authHeaders,
  readRefusal,
  reportFetchFailure,
  reportHttpStatus,
} from "../../../lib/server";
import { errMessage, errName } from "../../../lib/guards";
import { fail, succeed, type Result } from "../result";
import { lensQuery, type Lens } from "./lens";

export interface DiagramEntry {
  readonly id: string;
  readonly kind: string;
  readonly title: string;
  readonly project?: string;
  // Set only on the import figure: false means the server has no symbol index for it.
  readonly indexed?: boolean;
}

export interface DiagramNode {
  // The server's row id; main.ts re-keys it to the figure's data-node id (wasm.ts drawnNodes).
  readonly id: string;
  // The workspace-relative path the box depicts; "" for a node that depicts none.
  readonly anchor: string;
  readonly label: string;
}

export interface RenderedDiagram {
  readonly id: string;
  readonly title: string;
  readonly svg: string;
  readonly nodes: readonly DiagramNode[];
  // A blob URL template with {path} and {line}, or "" when the remote is not one magus links.
  readonly sourceUrl: string;
}

// DiagramRead keeps the server's answers apart because each names a different fix: a refused
// figure (422) the lens change, an unindexed one (409) the index command, a bad lens (400) the field.
export type DiagramRead<T> =
  | { readonly kind: "ok"; readonly value: T }
  | { readonly kind: "refused"; readonly detail: string }
  | { readonly kind: "unindexed"; readonly detail: string }
  | { readonly kind: "bad-lens"; readonly detail: string }
  | { readonly kind: "absent"; readonly detail: string }
  | { readonly kind: "unreadable"; readonly detail: string }
  // A superseded request or a closed pane. Not a failure; never shown.
  | { readonly kind: "aborted" };

// DiagramFailure is every read a reader is shown: not a figure, not an abort.
export type DiagramFailure = Exclude<DiagramRead<never>, { kind: "ok" } | { kind: "aborted" }>;

export interface DiagramClientOptions {
  // host:port of the server, already resolved.
  readonly host: string;
  readonly signal?: AbortSignal;
  // Injected by tests; the browser's fetch otherwise.
  readonly fetch?: typeof fetch;
}

const SOURCE = "Figures";

export function diagramsUrl(host: string): string {
  return "http://" + host + "/api/v1/diagrams";
}

export function diagramUrl(host: string, id: string, lens: Lens): string {
  return diagramsUrl(host) + "/" + encodeURI(id) + lensQuery(lens);
}

export function listDiagrams(opts: DiagramClientOptions): Promise<DiagramRead<DiagramEntry[]>> {
  return read(opts, diagramsUrl(opts.host), "the figure list", parseListing);
}

export function renderDiagram(
  opts: DiagramClientOptions,
  id: string,
  lens: Lens,
): Promise<DiagramRead<RenderedDiagram>> {
  return read(opts, diagramUrl(opts.host, id, lens), "figure " + id, parseRendered);
}

// readFailure maps a non-2xx status to a failure. body is the server's message (see readRefusal),
// used verbatim since it names the fix.
export function readFailure(status: number, body: string, what: string): DiagramFailure {
  const detail = body.trim();
  switch (status) {
    case 422:
      return { kind: "refused", detail: detail || "The server refused to draw " + what + "." };
    case 409:
      return {
        kind: "unindexed",
        detail: detail || "The server has no symbol index for " + what + ".",
      };
    case 400:
      return { kind: "bad-lens", detail: detail || "The server did not accept this lens." };
    case 404:
      return { kind: "absent", detail: "This server does not serve " + what + "." };
    default:
      return {
        kind: "unreadable",
        detail: "The server answered HTTP " + status + " for " + what + ".",
      };
  }
}

async function read<T>(
  opts: DiagramClientOptions,
  url: string,
  what: string,
  parse: (body: unknown) => Result<T>,
): Promise<DiagramRead<T>> {
  const doFetch = opts.fetch ?? fetch;
  let res: Response;
  try {
    res = await doFetch(url, {
      headers: authHeaders(),
      cache: "no-store",
      ...(opts.signal ? { signal: opts.signal } : {}),
    });
  } catch (e) {
    // reported: reportFetchFailure is the toast, the read is the inline notice
    if (errName(e) === "AbortError") return { kind: "aborted" };
    reportFetchFailure(opts.host, what, e);
    return { kind: "unreadable", detail: errMessage(e) };
  }
  if (!res.ok) {
    const failure = readFailure(res.status, (await readRefusal(res))?.message ?? "", what);
    if (res.status === 401 || failure.kind === "unreadable")
      reportHttpStatus(opts.host, what, res.status);
    else reportFailure(SOURCE, failure.detail, "diagrams:" + failure.kind + ":" + url);
    return failure;
  }
  let body: unknown;
  try {
    body = await res.json();
  } catch (e) {
    return unreadableBody(url, "Could not read " + what + ": " + errMessage(e));
  }
  const parsed = parse(body);
  if (!parsed.ok)
    return unreadableBody(url, "Could not read " + what + ": " + parsed.error.message);
  return { kind: "ok", value: parsed.value };
}

function unreadableBody(url: string, detail: string): DiagramFailure {
  reportFailure(SOURCE, detail, "diagrams:parse:" + url);
  return { kind: "unreadable", detail };
}

function record(v: unknown, what: string): Result<Record<string, unknown>> {
  if (typeof v !== "object" || v === null || Array.isArray(v))
    return fail(what + " is not an object");
  return succeed(Object.fromEntries(Object.entries(v)));
}

function str(v: unknown, what: string): Result<string> {
  if (typeof v !== "string") return fail(what + " is not a string");
  return succeed(v);
}

export function parseListing(body: unknown): Result<DiagramEntry[]> {
  const top = record(body, "the listing");
  if (!top.ok) return top;
  const list = top.value.diagrams;
  if (!Array.isArray(list)) return fail("the listing has no diagrams array");
  const entries: DiagramEntry[] = [];
  for (const [i, raw] of list.entries()) {
    const e = record(raw, "diagram " + i);
    if (!e.ok) return e;
    const id = str(e.value.id, "diagram " + i + " id");
    if (!id.ok) return id;
    const kind = str(e.value.kind, "diagram " + i + " kind");
    if (!kind.ok) return kind;
    const title = str(e.value.title, "diagram " + i + " title");
    if (!title.ok) return title;
    const project = e.value.project;
    const indexed = e.value.indexed;
    entries.push({
      id: id.value,
      kind: kind.value,
      title: title.value,
      ...(typeof project === "string" ? { project } : {}),
      ...(typeof indexed === "boolean" ? { indexed } : {}),
    });
  }
  return succeed(entries);
}

export function parseRendered(body: unknown): Result<RenderedDiagram> {
  const top = record(body, "the figure");
  if (!top.ok) return top;
  const r = top.value;
  const id = str(r.id, "the figure id");
  if (!id.ok) return id;
  const title = str(r.title, "the figure title");
  if (!title.ok) return title;
  const svg = str(r.svg, "the figure svg");
  if (!svg.ok) return svg;
  const nodes: DiagramNode[] = [];
  for (const [i, raw] of (Array.isArray(r.nodes) ? r.nodes : []).entries()) {
    const n = record(raw, "node " + i);
    if (!n.ok) return n;
    const nodeId = str(n.value.id, "node " + i + " id");
    if (!nodeId.ok) return nodeId;
    const anchor = n.value.anchor;
    const label = n.value.label;
    nodes.push({
      id: nodeId.value,
      anchor: typeof anchor === "string" ? anchor : "",
      label: typeof label === "string" ? label : nodeId.value,
    });
  }
  return succeed({
    id: id.value,
    title: title.value,
    svg: svg.value,
    nodes,
    sourceUrl: typeof r.source_url === "string" ? r.source_url : "",
  });
}
