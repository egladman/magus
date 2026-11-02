import assert from "node:assert/strict";
import { MagusGuard } from "../plugins/magus.js";

// Synthetic activation: load the installed artifact and prove its pre-tool hook
// forwards an event and turns a guard denial into a refused tool call.
let asked = false;
const runtime = {
  spawn(argv: string[], options: { stdin?: Uint8Array }) {
    const body = options.stdin ? new TextDecoder().decode(options.stdin) : "";
    if (argv.includes("shell")) {
      asked = true;
    }
    return {
      stdout: new Response(JSON.stringify({schema_version: 1, decision: "deny", reason: "magus-install-probe"})).body,
      stderr: new Response("").body,
      exited: Promise.resolve(0),
    };
  },
};
(globalThis as unknown as {Bun: unknown}).Bun = runtime;
const client = {
  session: {get: async () => ({data: {id: "installation-probe"}})},
  app: {log: async () => ({})},
  tui: {showToast: async () => ({})},
};
const activate = MagusGuard as unknown as (input: unknown) => Promise<Record<string, (...args: unknown[]) => Promise<void>>>;
const hooks = await activate({directory: process.cwd(), client});
assert.equal(typeof hooks["tool.execute.before"], "function");
await assert.rejects(
  hooks["tool.execute.before"]({tool: "bash", callID: "installation-probe", sessionID: "installation-probe"}, {args: {command: "echo installation-probe"}}),
  /magus-install-probe/,
);
assert.equal(asked, true, "the installed plugin must ask the guard before refusing");
