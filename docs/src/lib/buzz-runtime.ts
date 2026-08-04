// buzz-runtime.ts - lazy access to the playground's WebAssembly Buzz interpreter.
//
// The wasm artifact is multiple megabytes, so it is NEVER fetched on page load: the
// first caller that actually needs it pays, and every later caller on the page reuses
// the same module. That laziness is the whole reason this file exists as a module
// rather than a closure inside one feature - run-example's Run buttons and
// buzz-console's hidden REPL are both on every page, and two private loaders would
// mean two fetches of the same artifact on any page where a reader used both.
//
// The loading sequence mirrors playground.html exactly: append wasm_exec.js (a classic
// script defining globalThis.Go), instantiate buzz.wasm against it, then wait for the
// Go main() to install globalThis.buzz.

// The playground exposes window.buzz.* from inside its Go main(); wasm_exec.js defines
// window.Go. Declared here (not per-consumer) so the shape is stated once.
export interface BuzzOp {
  target?: string;
  name: string;
  detail?: string;
  kind: string;
}
export interface BuzzResult {
  ok: boolean;
  // The trailing `return` value's rendered form, "null" when the program returned
  // nothing. Empty in recorder mode.
  result?: string;
  output?: string;
  trace?: BuzzOp[];
}
export interface BuzzDiagnostic {
  line: number;
  col: number;
  msg: string;
}
export interface BuzzRuntime {
  evalBuzz(src: string): BuzzResult;
  evalBuzzWithRecorder(src: string): BuzzResult;
  // Pure function of the source - no session state, safe to call after a failed eval
  // purely to explain it. evalBuzz's result carries no diagnostic of its own.
  diagnostics(src: string): BuzzDiagnostic[];
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

// The site root, resolved from this bundle's own URL so playground/ is found under the
// /magus/ subpath and a local preview alike. Every module here is bundled into main.js,
// so import.meta.url is that file's URL wherever this code is called from.
export const SITE_ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

let pending: Promise<BuzzRuntime> | null = null;

// ready reports the runtime if it is already installed and usable, else null. Both the
// fast path and the post-instantiation poll ask the same question, so they ask it once.
function ready(): BuzzRuntime | null {
  const buzz = window.buzz;
  return buzz && typeof buzz.evalBuzz === "function" ? buzz : null;
}

// ensureBuzz resolves the interpreter, loading it on the first call. Concurrent callers
// share one in-flight load; a failed load is not cached, so a later call retries.
export function ensureBuzz(): Promise<BuzzRuntime> {
  const already = ready();
  if (already) return Promise.resolve(already);
  if (pending) return pending;
  pending = new Promise<BuzzRuntime>(function (resolve, reject) {
    const s = document.createElement("script");
    s.src = SITE_ROOT + "playground/wasm_exec.js";
    s.onload = function () {
      try {
        const go = new window.Go();
        const loader = fetch(SITE_ROOT + "playground/buzz.wasm");
        const startWith = function (mod: WebAssembly.WebAssemblyInstantiatedSource): void {
          go.run(mod.instance);
          // window.buzz appears from inside main(), which is async under asyncify, so
          // poll briefly rather than assuming it is there when go.run returns.
          const deadline = Date.now() + 5000;
          (function wait() {
            const buzz = ready();
            if (buzz) return resolve(buzz);
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
  // A transient load failure must not poison every later attempt; drop the cache so the
  // next call retries. This attempt's caller still sees the rejection.
  pending.catch(() => {
    pending = null;
  });
  return pending;
}
