// buzz-runtime.ts - the one loader for the playground's Buzz WASM.
//
// Extracted from run-example.ts once a second consumer appeared (the hero terminal
// on the landing page). It has to be shared rather than copied: buzz.wasm is 4.2 MB
// and `go.run()` starts a Go runtime, so two modules each doing their own bootstrap
// would download it twice and leave two runtimes racing to define window.buzz.
//
// The module-level promise is the whole point - whoever calls ensureBuzz() first
// pays for the load, everyone after that awaits the same promise.

// The playground WASM exposes window.buzz.* inside its Go main(), and wasm_exec.js
// defines window.Go; declare just the surface these modules touch.
export interface BuzzOp {
  target?: string;
  name: string;
  detail?: string;
  kind: string;
}
// A structured evaluation failure: the interpreter's message plus a 1-based
// source position. Present only when ok is false.
export interface BuzzDiag {
  msg: string;
  line: number;
  col: number;
}
export interface BuzzResult {
  ok: boolean;
  output?: string;
  result?: string;
  trace?: BuzzOp[];
  diag?: BuzzDiag | null;
}
export interface BuzzRuntime {
  evalBuzz(src: string): BuzzResult;
  evalBuzzWithRecorder(src: string): BuzzResult;
}
interface GoInstance {
  run(instance: WebAssembly.Instance): void;
  importObject: WebAssembly.Imports;
}

declare global {
  interface Window {
    buzz?: BuzzRuntime;
    Go: { new (): GoInstance };
  }
}

// Resolve the playground/ folder relative to this bundle so links work under the
// /magus/ subpath and local preview alike.
export const ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

let wasmPromise: Promise<void> | null = null;

// buzzReady reports whether the runtime is already usable, so a caller can take a
// synchronous path instead of awaiting a promise that would resolve immediately.
export function buzzReady(): boolean {
  return !!(window.buzz && typeof window.buzz.evalBuzz === "function");
}

// ensureBuzz resolves once window.buzz is ready. Safe to call repeatedly and from
// several modules; the first call does the work.
export function ensureBuzz(): Promise<void> {
  if (buzzReady()) return Promise.resolve();
  if (wasmPromise) return wasmPromise;
  wasmPromise = new Promise<void>(function (resolve, reject) {
    // wasm_exec.js is a classic script that defines globalThis.Go; append it,
    // wait for load, then instantiate buzz.wasm exactly like playground.html.
    const s = document.createElement("script");
    s.src = ROOT + "playground/wasm_exec.js";
    s.onload = function () {
      try {
        const go = new window.Go();
        const loader = fetch(ROOT + "playground/buzz.wasm");
        const startWith = function (mod: WebAssembly.WebAssemblyInstantiatedSource): void {
          go.run(mod.instance);
          // The playground exposes window.buzz.evalBuzz inside main(); poll briefly
          // for it to appear before resolving (Go's main is async under asyncify).
          const deadline = Date.now() + 5000;
          (function wait() {
            if (buzzReady()) return resolve();
            if (Date.now() > deadline) return reject(new Error("buzz.evalBuzz not ready"));
            setTimeout(wait, 30);
          })();
        };
        if (WebAssembly.instantiateStreaming) {
          WebAssembly.instantiateStreaming(loader, go.importObject).then(startWith).catch(reject);
        } else {
          loader
            .then(function (r) {
              return r.arrayBuffer();
            })
            .then(function (bs) {
              return WebAssembly.instantiate(bs, go.importObject);
            })
            .then(startWith)
            .catch(reject);
        }
      } catch (e) {
        reject(e);
      }
    };
    s.onerror = function () {
      reject(new Error("wasm_exec.js failed to load"));
    };
    document.head.appendChild(s);
  });
  // A transient load failure must not poison every later attempt; drop the cache so
  // the next call retries. The caller still sees this attempt's rejection.
  wasmPromise.catch(() => {
    wasmPromise = null;
  });
  return wasmPromise;
}

// warmBuzz starts the load speculatively and swallows failures. It exists so a
// surface can be ready BEFORE the visitor asks for anything: the service worker
// already precaches buzz.wasm, so on a repeat visit this resolves out of Cache
// Storage with no network at all, and on a first visit it overlaps the load with
// however long the person spends reading. Nothing awaits it and nothing reports
// it - a speculative warm that fails must be indistinguishable from one that was
// never started, and the real call site will retry and surface the error itself.
export function warmBuzz(): void {
  if (buzzReady() || wasmPromise) return;
  const start = function () {
    ensureBuzz().catch(function () {
      /* speculative: the on-demand path reports for real */
    });
  };
  // requestIdleCallback keeps the 4.2 MB fetch off the critical path so it cannot
  // compete with the page's own render. Safari has no requestIdleCallback, hence
  // the timeout fallback.
  const ric = (window as unknown as { requestIdleCallback?: (cb: () => void) => void })
    .requestIdleCallback;
  if (typeof ric === "function") ric(start);
  else setTimeout(start, 1200);
}
