// OpenCode loads JavaScript modules; the integration itself lives in Go and Buzz.
import { readFile } from "node:fs/promises";

let initialized;

async function initialize() {
  try {
    await import("../magus/wasm_exec.js");
    const GoRuntime = globalThis.Go;
    const go = new GoRuntime();
    const bytes = await readFile(new URL("../magus/magus-opencode.wasm", import.meta.url));
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    let failure;
    const running = go.run(instance).then(
      () => { failure = new Error("magus OpenCode runtime exited"); },
      (error) => { failure = error; },
    );
    await Promise.race([running, new Promise((resolve) => setTimeout(resolve, 0))]);
    if (failure) return { api: null, error: failure };
    const api = globalThis.__magusOpenCodeWasmV1;
    if (api?.version !== 1 || typeof api.createPlugin !== "function") {
      return { api: null, error: new Error("magus OpenCode runtime ABI mismatch") };
    }
    return { api, error: null };
  } catch (error) {
    return { api: null, error };
  }
}

// A throw from here is OpenCode's only way to refuse to load the guard.
export async function MagusGuard(context) {
  initialized ??= initialize();
  const { api, error } = await initialized;
  if (error) throw error;
  return api.createPlugin(context);
}
