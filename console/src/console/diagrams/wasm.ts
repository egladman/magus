// wasm.ts - the Buzz runtime a figure upgrades to on request: the docs playground's wasm, loaded
// the way docs/src/site/buzz-runtime.ts loads it, then fed the magus/figure source the server
// evaluates (GET /api/v1/diagrams/source) with a driver shaped like internal/handler/diagram's.
// Once loaded, a lens change re-lays the figure out here instead of asking the server.
//
// Never loaded automatically: it is 4.2MB and a Go runtime, and the static render already is the
// figure. The explicit control is the whole policy.

import type { Declaration, DeclaredNode, Lens } from "./lens";
import { cutDeclaration, describeLens } from "./lens";
import type { DiagramSources } from "./api";

export interface BuzzDiag {
  readonly msg: string;
  readonly line: number;
  readonly col: number;
}

export interface BuzzResult {
  readonly ok: boolean;
  readonly result?: string;
  readonly output?: string;
  readonly diag?: BuzzDiag | null;
}

export interface BuzzRuntime {
  evalBuzz(src: string): BuzzResult;
}

interface GoInstance {
  run(instance: WebAssembly.Instance): Promise<void> | void;
  importObject: WebAssembly.Imports;
}

type GoConstructor = new () => GoInstance;

function hasFunction<K extends string>(
  v: unknown,
  key: K,
): v is Record<K, (...args: never[]) => unknown> {
  return (
    typeof v === "object" && v !== null && typeof (v as Record<string, unknown>)[key] === "function"
  );
}

// runtimeFrom narrows globalThis.buzz, which the wasm's Go main() installs.
export function runtimeFrom(g: unknown): BuzzRuntime | null {
  if (typeof g !== "object" || g === null) return null;
  const buzz = (g as { buzz?: unknown }).buzz;
  if (!hasFunction(buzz, "evalBuzz")) return null;
  return {
    evalBuzz: (src: string): BuzzResult => {
      const raw: unknown = (buzz.evalBuzz as (s: string) => unknown)(src);
      if (typeof raw !== "object" || raw === null)
        return { ok: false, diag: { msg: "the runtime returned nothing", line: 0, col: 0 } };
      const r = raw as { ok?: unknown; result?: unknown; output?: unknown; diag?: unknown };
      const d = r.diag as { msg?: unknown; line?: unknown; col?: unknown } | null | undefined;
      return {
        ok: r.ok === true,
        result: typeof r.result === "string" ? r.result : undefined,
        output: typeof r.output === "string" ? r.output : undefined,
        diag:
          d && typeof d.msg === "string"
            ? { msg: d.msg, line: Number(d.line) || 0, col: Number(d.col) || 0 }
            : null,
      };
    },
  };
}

// wasmRoot is gen/wasm/ beside this bundle's gen/diagrams/, where the console build copies the
// playground's buzz.wasm and wasm_exec.js.
export function wasmRoot(moduleUrl: string = import.meta.url): string {
  return new URL("../wasm/", moduleUrl).href;
}

let loading: Promise<BuzzRuntime> | null = null;

export interface LoadOptions {
  readonly root?: string;
  // How long Go's main() may take to install globalThis.buzz after the module starts.
  readonly readyMs?: number;
}

// ensureBuzz resolves with the runtime, loading it at most once per page. A failed load is
// forgotten so the next click retries; the caller reports the failure.
export function ensureBuzz(opts: LoadOptions = {}): Promise<BuzzRuntime> {
  const ready = runtimeFrom(globalThis);
  if (ready) return Promise.resolve(ready);
  if (loading) return loading;
  const root = opts.root ?? wasmRoot();
  const readyMs = opts.readyMs ?? 5000;
  loading = new Promise<BuzzRuntime>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = root + "wasm_exec.js";
    script.onerror = () =>
      reject(
        new Error(
          "could not load " +
            script.src +
            "; the console build ships it only when docs' build_playground ran first",
        ),
      );
    script.onload = () => {
      const Go = (globalThis as { Go?: unknown }).Go;
      if (typeof Go !== "function") {
        reject(new Error("wasm_exec.js loaded but defined no Go runtime"));
        return;
      }
      const go = new (Go as GoConstructor)();
      const url = root + "buzz.wasm";
      const start = async (): Promise<void> => {
        const res = await fetch(url);
        if (!res.ok) throw new Error("could not load " + url + ": HTTP " + res.status);
        const mod = await WebAssembly.instantiate(await res.arrayBuffer(), go.importObject);
        void go.run(mod.instance);
        // Go's main() installs globalThis.buzz asynchronously after run() returns.
        const deadline = Date.now() + readyMs;
        for (;;) {
          const rt = runtimeFrom(globalThis);
          if (rt) {
            resolve(rt);
            return;
          }
          if (Date.now() > deadline)
            throw new Error("the Buzz runtime started but never exposed evalBuzz");
          await new Promise((r) => setTimeout(r, 30));
        }
      };
      start().catch(reject);
    };
    document.head.append(script);
  });
  loading.catch(() => {
    // reported: the caller of ensureBuzz reports this same rejection; dropping it lets a retry reload
    loading = null;
  });
  return loading;
}

// buzzString is the handler's buzzString: a Buzz string literal with braces escaped (a bare one
// opens an interpolation) and control bytes as three-digit decimal escapes.
export function buzzString(s: string): string {
  let out = '"';
  for (const ch of s) {
    const c = ch.charCodeAt(0);
    switch (ch) {
      case '"':
      case "\\":
      case "{":
      case "}":
        out += "\\" + ch;
        break;
      case "\n":
        out += "\\n";
        break;
      case "\t":
        out += "\\t";
        break;
      case "\r":
        out += "\\r";
        break;
      default:
        out += c < 0x20 || c === 0x7f ? "\\" + String(c).padStart(3, "0") : ch;
    }
  }
  return out + '"';
}

// figureId is the handler's ids{}.of for the figure id: letters, digits, - and _ survive.
export function figureId(id: string): string {
  const base = id.replace(/[^A-Za-z0-9_-]/g, "-").replace(/^-+|-+$/g, "");
  return base || "root";
}

// anchorTemplate is the handler's: a node anchors a path, never a line.
export function anchorTemplate(sourceUrl: string): string {
  const line = "#L{line}";
  return sourceUrl.endsWith(line) ? sourceUrl.slice(0, -line.length) : sourceUrl;
}

// linkTo is the handler's linkTo: one pass, so an anchor holding {line} stays as written.
export function linkTo(anchorHref: string, anchor: string): string {
  if (!anchorHref) return "";
  return anchorHref.replace(/\{path\}|\{line\}/g, (m) => (m === "{path}" ? anchor : ""));
}

// actorNames is the handler's: figure keys an actor by name, so a label two nodes share takes
// the node's anchor. Aligned with nodes by index.
export function actorNames(nodes: readonly DeclaredNode[]): string[] {
  const count = new Map<string, number>();
  for (const n of nodes) count.set(n.label, (count.get(n.label) ?? 0) + 1);
  return nodes.map((n) =>
    (count.get(n.label) ?? 0) > 1 ? n.label + " (" + n.anchor + ")" : n.label,
  );
}

// IMPORTS is the claim of the handler's import figure, the one kind drawn from Dir records.
export const IMPORTS = "imports";

// drawnNodes re-keys the server's node rows by the id magus/figure draws as data-node: a box by
// its directory, an actor as external:<name>. The SVG's data-edge pairs use those ids, so the
// node list, focus and a lens cut need them too.
export function drawnNodes(nodes: readonly DeclaredNode[], claim: string): DeclaredNode[] {
  if (claim === IMPORTS) return nodes.map((n) => ({ ...n, id: n.anchor }));
  const names = actorNames(nodes);
  return nodes.map((n, i) => ({ ...n, id: "external:" + names[i] }));
}

export interface FigureMeta {
  readonly id: string;
  readonly title: string;
  // IMPORTS for the import figure; any other claim draws actors, as the handler's driver does.
  readonly claim: string;
  // The node link template, {path} filled per node, or "" for no links.
  readonly anchorHref: string;
}

const SVG_MARK = "svg\n";
const FINDINGS_MARK = "findings\n";

// The handler's serveDir, byte for byte. A host hands a Dir over as a map, and typing the map
// through any is how a record reaches figure without the magus module.
const SERVE_DIR = String.raw`fun serveDir(path: str, imports: [str]) > magus\Dir {
    final fields: {str: any} = {
        "path": path, "id": "dir:" + path, "layer": "", "language": "go",
        "imports": imports, "importedBy": [<str>], "importsIndexed": true,
        "calls": [<magus\DirCall>], "calledBy": [<magus\DirCall>], "children": [<str>], "files": 1,
    };
    final record: any = fields;
    return record;
}

`;

// driverFor is the handler's driver over decl, whose node ids are drawnNodes ids. Only the tail
// differs: the playground hands a result back as its string form, where a list would arrive
// flattened, so it returns ONE marked string instead of [svg, findings].
export function driverFor(decl: Declaration, meta: FigureMeta, desc: string): string {
  const lines = [
    SERVE_DIR + "fun serveFigure() > str !> str {",
    "    final f = of(" +
      buzzString(figureId(meta.id)) +
      ").title(" +
      buzzString(meta.title) +
      ").desc(" +
      buzzString(desc) +
      ");",
  ];
  if (meta.claim === IMPORTS) {
    const anchor = new Map(decl.nodes.map((n) => [n.id, n.anchor]));
    const imports = new Map<string, string[]>();
    for (const [src, dst] of decl.edges)
      imports.set(src, [...(imports.get(src) ?? []), anchor.get(dst) ?? ""]);
    for (const n of decl.nodes) {
      const quoted = (imports.get(n.id) ?? []).map(buzzString);
      const list = quoted.length ? "[" + quoted.join(", ") + "]" : "[<str>]";
      lines.push(
        "    f.box(serveDir(" +
          buzzString(n.anchor) +
          ", imports: " +
          list +
          "), label: " +
          buzzString(n.label) +
          ");",
      );
    }
    lines.push("    f.edgesFromGraph();");
  } else {
    lines.push(
      "    f.unscoped(why: " + buzzString("served from the workspace graph: " + desc) + ");",
    );
    const names = actorNames(decl.nodes);
    const index = new Map<string, number>();
    decl.nodes.forEach((n, i) => {
      index.set(n.id, i);
      lines.push(
        "    final a" +
          i +
          " = external(" +
          buzzString(names[i]) +
          ", link: " +
          buzzString(linkTo(meta.anchorHref, n.anchor)) +
          ", look: Look.plain);",
        "    f.actor(a" + i + ");",
      );
    });
    for (const [src, dst] of decl.edges) {
      const a = index.get(src);
      const b = index.get(dst);
      if (a === undefined || b === undefined)
        throw new Error("the declaration has an edge " + src + "->" + dst + " to no node");
      lines.push("    f.flowAcross(a" + a + ", dst: a" + b + ");");
    }
  }
  lines.push("    return f.svg(Theme.page, anchorHref: " + buzzString(meta.anchorHref) + ");");
  lines.push("}");
  lines.push("var served = " + buzzString(SVG_MARK) + ";");
  lines.push("try {");
  lines.push("    served = served + serveFigure();");
  lines.push("} catch (e: str) {");
  lines.push("    served = " + buzzString(FINDINGS_MARK) + " + e;");
  lines.push("}");
  lines.push("return served;");
  return lines.join("\n") + "\n";
}

// FIGURE_PATH is the handler's FigureSource: the one file GET /api/v1/diagrams/source serves.
export const FIGURE_PATH = "libs/figure/figure.buzz";

// programFor is what the handler evaluates: the module's own source, then the driver. The driver
// runs inside the module because a program importing it cannot reach the private layout helpers
// its methods call.
export function programFor(sources: DiagramSources, driver: string): string {
  const src = sources[FIGURE_PATH];
  if (src === undefined) throw new Error("the server's figure source is missing " + FIGURE_PATH);
  return src + "\n" + driver;
}

export type Relayout =
  | { readonly kind: "ok"; readonly svg: string }
  | { readonly kind: "refused"; readonly detail: string }
  | { readonly kind: "bad-lens"; readonly detail: string }
  | { readonly kind: "failed"; readonly detail: string };

export function parseRelayout(r: BuzzResult): Relayout {
  if (!r.ok) {
    const d = r.diag;
    return {
      kind: "failed",
      detail: d ? d.msg + (d.line ? " (line " + d.line + ")" : "") : "evaluation failed",
    };
  }
  const out = r.result ?? "";
  if (out.startsWith(SVG_MARK)) return { kind: "ok", svg: out.slice(SVG_MARK.length) };
  if (out.startsWith(FINDINGS_MARK))
    return { kind: "refused", detail: out.slice(FINDINGS_MARK.length) };
  return { kind: "failed", detail: "the driver returned something that is not a figure" };
}

export interface RelayoutInput {
  readonly runtime: BuzzRuntime;
  readonly sources: DiagramSources;
  // The whole figure as the server declared it, before any lens, keyed by drawnNodes ids.
  readonly decl: Declaration;
  readonly meta: FigureMeta;
  readonly lens: Lens;
}

// relayout cuts the declaration through the lens and lays it out with magus/figure, in the page.
export function relayout(input: RelayoutInput): Relayout {
  const cut = cutDeclaration(input.decl, input.lens);
  if (!cut.ok) return { kind: "bad-lens", detail: cut.error };
  let program: string;
  try {
    program = programFor(input.sources, driverFor(cut.decl, input.meta, describeLens(input.lens)));
  } catch (e) {
    return { kind: "failed", detail: e instanceof Error ? e.message : String(e) };
  }
  return parseRelayout(input.runtime.evalBuzz(program));
}
