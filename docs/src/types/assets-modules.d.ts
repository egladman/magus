// assets-modules.d.ts - ambient types for the same-origin vendor bundles the
// syntax-highlight and mermaid modules lazy-load at runtime.
//
// Those modules do `import("./assets/hljs.js")` / `import("./assets/mermaid.js")`,
// paths that resolve relative to the emitted gen/main.js (where esbuild bundles
// main.ts) - so gen/assets/hljs.js and gen/assets/mermaid.js, built separately by
// `magus run build-hljs` / `build-mermaid` from src/vendor/*. They do not exist in
// the src/ source tree, and esbuild deliberately leaves these dynamic imports as
// external runtime imports. The bundles ship no declarations, so rather than a bare
// `any` we hand-type the narrow slice each caller actually uses; the wildcard
// specifier matches the relative import regardless of the importing page's URL depth.

// One node in a highlight.js language definition (only the fields the Buzz grammar
// in syntax-highlight.ts sets).
interface HljsMode {
  className?: string;
  begin?: RegExp | string;
  end?: RegExp | string;
  contains?: HljsMode[];
  excludeBegin?: boolean;
  returnEnd?: boolean;
}
interface HljsLanguage {
  name?: string;
  keywords?: Record<string, string>;
  contains: HljsMode[];
}
interface HljsApi {
  readonly C_LINE_COMMENT_MODE: HljsMode;
  readonly C_BLOCK_COMMENT_MODE: HljsMode;
  readonly C_NUMBER_MODE: HljsMode;
  readonly BACKSLASH_ESCAPE: HljsMode;
  registerLanguage(name: string, def: (hljs: HljsApi) => HljsLanguage): void;
  configure(opts: { ignoreUnescapedHTML?: boolean }): void;
  getLanguage(name: string): unknown;
  highlightElement(el: Element): void;
}
declare module "*/assets/hljs.js" {
  const hljs: HljsApi;
  export default hljs;
}

interface MermaidApi {
  initialize(config: Record<string, unknown>): void;
  run(opts: { nodes: Element[] }): Promise<void>;
}
declare module "*/assets/mermaid.js" {
  const mermaid: MermaidApi;
  export default mermaid;
}
