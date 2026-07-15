// assets-modules.d.ts - ambient types for the same-origin vendor bundles the
// syntax-highlight and mermaid modules lazy-load at runtime.
//
// Those modules do `import("./assets/hljs.js")` / `import("./assets/mermaid.js")`,
// paths that resolve relative to the emitted gen/main.js (where esbuild bundles
// main.ts) - so gen/assets/hljs.js and gen/assets/mermaid.js, built separately by
// `magus run build-hljs` / `build-mermaid` from src/vendor/*. They do not exist in
// the src/ source tree, and esbuild deliberately leaves these dynamic imports as
// external runtime imports. These untyped third-party bundles ship no declarations,
// so the default export is `any`; the wildcard specifier matches the relative import
// regardless of the importing page's URL depth.
declare module "*/assets/hljs.js" {
  const hljs: any;
  export default hljs;
}
declare module "*/assets/mermaid.js" {
  const mermaid: any;
  export default mermaid;
}
