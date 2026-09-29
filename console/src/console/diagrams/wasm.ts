// wasm.ts - the Buzz runtime a figure upgrades to on request: the docs playground's wasm, loaded
// the way docs/src/site/buzz-runtime.ts loads it, then fed the same flow sources the server
// evaluates (GET /api/v1/diagrams/source) with a driver built the way the handler builds its own.
// Once loaded, a lens change re-lays the figure out here instead of asking the server.
//
// Never loaded automatically: it is 4.2MB and a Go runtime, and the static render already is the
// figure. The explicit control is the whole policy.

import type { Declaration, Lens } from "./lens";
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

// flowId is the handler's ids.of for the figure id: flow ids take letters, digits, - and _.
export function flowId(id: string): string {
  const base = id.replace(/[^A-Za-z0-9_-]/g, "-").replace(/^-+|-+$/g, "");
  return base || "root";
}

export interface FigureMeta {
  readonly id: string;
  readonly title: string;
  // The edge claim the server gives this figure kind: imports for the import graph, flow else.
  readonly claim: string;
  // A template with {path}, or "" for no links.
  readonly anchorHref: string;
}

const SVG_MARK = "svg\n";
const FINDINGS_MARK = "findings\n";

// driverFor declares decl to flow and draws it. It returns ONE string, marked, because the
// playground hands back a result as its string form and a list would arrive flattened.
export function driverFor(decl: Declaration, meta: FigureMeta, desc: string): string {
  const lines = [
    "fun serveFigure() > str !> str {",
    "    final f = flow(" +
      buzzString(flowId(meta.id)) +
      ").title(" +
      buzzString(meta.title) +
      ").desc(" +
      buzzString(desc) +
      ");",
  ];
  for (const n of decl.nodes)
    lines.push(
      "    f.node(" +
        buzzString(n.id) +
        ", label: " +
        buzzString(n.label) +
        ", anchor: " +
        buzzString(n.anchor) +
        ");",
    );
  for (const [a, b] of decl.edges)
    lines.push(
      "    f.edge(" +
        buzzString(a) +
        ", dst: " +
        buzzString(b) +
        ", claim: " +
        buzzString(meta.claim) +
        ");",
    );
  lines.push(
    meta.anchorHref
      ? "    return f.svg(cssVarPalette(), s: Style{ anchorHref = " +
          buzzString(meta.anchorHref) +
          " });"
      : "    return f.svg(cssVarPalette());",
  );
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

const DECL = /^(?:export\s+)?(?:mut\s+)?(?:fun|final|var|object|enum)\s+([A-Za-z_]\w*)/gm;

function declaredNames(src: string): Set<string> {
  return new Set([...src.matchAll(DECL)].map((m) => m[1]));
}

// stripModule turns a module file into program text: its namespace line and imports go (the
// program imports std once, and the renderer is inlined rather than imported), and so do its
// test blocks, which need the assert module the playground does not carry. Test blocks open
// with `test "` at column 0 and close at the next column-0 `}`, the form both files use.
export function stripModule(src: string): string {
  const out: string[] = [];
  let inTest = false;
  for (const line of src.split("\n")) {
    if (inTest) {
      if (line === "}") inTest = false;
      continue;
    }
    if (line.startsWith('test "')) {
      inTest = !line.trimEnd().endsWith("}");
      continue;
    }
    if (/^namespace\s+\w+\s*;/.test(line) || /^import\s+"/.test(line)) continue;
    out.push(line);
  }
  return out.join("\n");
}

export const FLOW_PATH = "libs/diagram/flow.buzz";
export const RENDERER_PATH = "libs/diagram/diagram.buzz";

// assembleProgram inlines the renderer ahead of flow, which is what the server's flat import
// amounts to, then the driver. One program has one scope, so a private helper both files define
// (imin, snap) is renamed in the renderer, where flow never calls it.
export function assembleProgram(sources: DiagramSources, driver: string): string {
  const flow = sources[FLOW_PATH];
  const renderer = sources[RENDERER_PATH];
  if (flow === undefined || renderer === undefined)
    throw new Error(
      "the server's library source is missing " + (flow === undefined ? FLOW_PATH : RENDERER_PATH),
    );
  let r = stripModule(renderer);
  const f = stripModule(flow);
  const flowNames = declaredNames(f);
  for (const name of declaredNames(r)) {
    if (!flowNames.has(name)) continue;
    r = r.replace(new RegExp("(?<![.\\w\\\\])" + name + "\\b", "g"), "diagram_" + name);
  }
  return 'import "std";\n' + r + "\n" + f + "\n" + driver;
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
  // The whole figure as the server declared it, before any lens.
  readonly decl: Declaration;
  readonly meta: FigureMeta;
  readonly lens: Lens;
}

// relayout cuts the declaration through the lens and lays it out with flow, in the page.
export function relayout(input: RelayoutInput): Relayout {
  const cut = cutDeclaration(input.decl, input.lens);
  if (!cut.ok) return { kind: "bad-lens", detail: cut.error };
  let program: string;
  try {
    program = assembleProgram(
      input.sources,
      driverFor(cut.decl, input.meta, describeLens(input.lens)),
    );
  } catch (e) {
    return { kind: "failed", detail: e instanceof Error ? e.message : String(e) };
  }
  return parseRelayout(input.runtime.evalBuzz(program));
}
