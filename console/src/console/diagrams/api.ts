// api.ts - the typed client for GET /api/v1/diagrams, /api/v1/diagrams/{id} and
// /api/v1/diagrams/source. It is the surface's one choke point to the server: every answer that is
// not a figure is reported here (a toast at minimum) AND handed back as a read the view renders as
// an inline notice, so no failure can reach the page as an empty figure.

import { reportFailure } from "../../lib/notifications";
import { authHeaders, reportFetchFailure, reportHttpStatus } from "../../lib/server";
import { errMessage, errName } from "../../lib/guards";
import { lensQuery, type Lens } from "./lens";

export interface DiagramEntry {
  readonly id: string;
  readonly kind: string;
  readonly title: string;
  readonly project?: string;
  // Set only on the import figure: false means the server has no symbol index to draw it from.
  readonly indexed?: boolean;
}

export interface DiagramNode {
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

// The flow library the server evaluates, keyed by workspace path (libs/diagram/flow.buzz, ...).
export type DiagramSources = Readonly<Record<string, string>>;

// DiagramRead keeps the server's answers apart because each asks something different of the
// reader: a refused figure (422) names the lens change that fixes it, an unindexed one (409)
// names the command that builds the index, a bad lens (400) names the field.
export type DiagramRead<T> =
  | { readonly kind: "ok"; readonly value: T }
  | { readonly kind: "refused"; readonly detail: string }
  | { readonly kind: "unindexed"; readonly detail: string }
  | { readonly kind: "bad-lens"; readonly detail: string }
  | { readonly kind: "absent"; readonly detail: string }
  | { readonly kind: "unreadable"; readonly detail: string }
  // The caller aborted: a superseded request or a closed pane. Not a failure, never shown.
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

const SOURCE = "Diagrams";

export function diagramsUrl(host: string): string {
  return "http://" + host + "/api/v1/diagrams";
}

export function diagramUrl(host: string, id: string, lens: Lens): string {
  return diagramsUrl(host) + "/" + encodeURI(id) + lensQuery(lens);
}

export function listDiagrams(opts: DiagramClientOptions): Promise<DiagramRead<DiagramEntry[]>> {
  return read(opts, diagramsUrl(opts.host), "the diagram list", parseListing);
}

export function renderDiagram(
  opts: DiagramClientOptions,
  id: string,
  lens: Lens,
): Promise<DiagramRead<RenderedDiagram>> {
  return read(opts, diagramUrl(opts.host, id, lens), "diagram " + id, parseRendered);
}

export function loadDiagramSources(
  opts: DiagramClientOptions,
): Promise<DiagramRead<DiagramSources>> {
  return read(opts, diagramsUrl(opts.host) + "/source", "the diagram library source", parseSources);
}

// readFailure maps a non-2xx status and the body the server wrote with http.Error (plain text,
// used verbatim: the server's sentence names the fix and a restatement here would be a guess).
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
  parse: (body: unknown) => T,
): Promise<DiagramRead<T>> {
  const doFetch = opts.fetch ?? fetch;
  let res: Response;
  try {
    res = await doFetch(url, { headers: authHeaders(), cache: "no-store", signal: opts.signal });
  } catch (e) {
    // reported: reportFetchFailure is the toast, the read is the inline notice
    if (errName(e) === "AbortError") return { kind: "aborted" };
    reportFetchFailure(opts.host, what, e);
    return { kind: "unreadable", detail: errMessage(e) };
  }
  if (!res.ok) {
    let body = "";
    try {
      body = await res.text();
    } catch (e) {
      // reported: the status below is what the reader acts on; the unread body only loses words
      body = errMessage(e);
    }
    const failure = readFailure(res.status, body, what);
    if (res.status === 401 || failure.kind === "unreadable")
      reportHttpStatus(opts.host, what, res.status);
    else reportFailure(SOURCE, failure.detail, "diagrams:" + failure.kind + ":" + url);
    return failure;
  }
  try {
    return { kind: "ok", value: parse(await res.json()) };
  } catch (e) {
    const detail = "Could not read " + what + ": " + errMessage(e);
    reportFailure(SOURCE, detail, "diagrams:parse:" + url);
    return { kind: "unreadable", detail };
  }
}

function record(v: unknown, what: string): Record<string, unknown> {
  if (typeof v !== "object" || v === null || Array.isArray(v))
    throw new Error(what + " is not an object");
  return v as Record<string, unknown>;
}

function str(v: unknown, what: string): string {
  if (typeof v !== "string") throw new Error(what + " is not a string");
  return v;
}

export function parseListing(body: unknown): DiagramEntry[] {
  const list = record(body, "the listing").diagrams;
  if (!Array.isArray(list)) throw new Error("the listing has no diagrams array");
  return list.map((raw, i) => {
    const e = record(raw, "diagram " + i);
    const entry: { -readonly [K in keyof DiagramEntry]: DiagramEntry[K] } = {
      id: str(e.id, "diagram " + i + " id"),
      kind: str(e.kind, "diagram " + i + " kind"),
      title: str(e.title, "diagram " + i + " title"),
    };
    if (typeof e.project === "string") entry.project = e.project;
    if (typeof e.indexed === "boolean") entry.indexed = e.indexed;
    return entry;
  });
}

export function parseRendered(body: unknown): RenderedDiagram {
  const r = record(body, "the figure");
  const nodes = Array.isArray(r.nodes) ? r.nodes : [];
  return {
    id: str(r.id, "the figure id"),
    title: str(r.title, "the figure title"),
    svg: str(r.svg, "the figure svg"),
    nodes: nodes.map((raw, i) => {
      const n = record(raw, "node " + i);
      return {
        id: str(n.id, "node " + i + " id"),
        anchor: typeof n.anchor === "string" ? n.anchor : "",
        label: typeof n.label === "string" ? n.label : str(n.id, "node " + i + " id"),
      };
    }),
    sourceUrl: typeof r.source_url === "string" ? r.source_url : "",
  };
}

export function parseSources(body: unknown): DiagramSources {
  const files = record(record(body, "the source").files, "the source files");
  const out: Record<string, string> = {};
  for (const [path, text] of Object.entries(files)) out[path] = str(text, path);
  return out;
}
