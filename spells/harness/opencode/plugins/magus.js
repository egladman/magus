// OpenCode loads JavaScript modules; the integration itself lives in Go and Buzz.
import { readFile } from "node:fs/promises";

let initialized;

async function initialize() {
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
  if (failure) throw failure;
  const api = globalThis.__magusOpenCodeWasmV1;
  if (api?.version !== 1 || typeof api.createPlugin !== "function") {
    throw new Error("magus OpenCode runtime ABI mismatch");
  }
  return api;
}

export async function MagusGuard(context) {
  initialized ??= initialize();
  return (await initialized).createPlugin(context);
}
