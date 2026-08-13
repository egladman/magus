// Binary-interface twin of opencode-plugin.test.ts.
//
// That file proves the plugin ASKS magus correctly (the argv shape, stdin-not-argv,
// the top-level `hook` subcommand) against a Bun.spawn shim that never runs a real
// process. It cannot catch the other half: a future `magus hook` flag or behavior
// change that the plugin's call no longer matches. That is the OpenCode incident's
// exact class - the plugin invoked a removed subcommand for weeks because nothing
// executed it against a real binary.
//
// This file closes that gap by supplying a Bun.spawn shim that DELEGATES to
// node:child_process.spawn and actually runs the resolved magus binary, then drives
// the plugin's real exported functions (not a hand-rolled `spawnSync` call) through
// three cases that exercise all three verdicts.
//
// Recorded shim (opencode-plugin.test.ts) = invocation shape.
// Live shim (this file) = binary interface.

import assert from "node:assert/strict";
import { spawn as nodeSpawn } from "node:child_process";
import { accessSync, constants } from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { MagusGuard } from "./opencode-plugin.ts";

const HERE = path.dirname(fileURLToPath(import.meta.url));
// docs/guides/integrations/agents -> repo root.
const WORKSPACE_ROOT = path.resolve(HERE, "../../../..");

function isExecutable(candidate: string): boolean {
  try {
    accessSync(candidate, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function resolveOnPath(name: string): string | null {
  for (const dir of (process.env.PATH ?? "").split(path.delimiter)) {
    if (dir === "") continue;
    const candidate = path.join(dir, name);
    if (isExecutable(candidate)) return candidate;
  }
  return null;
}

/**
 * GUARD_MAGUS_BIN, else ./magus at the workspace root, else magus on PATH -
 * the same order docs/guides/integrations/agents.md documents for GUARD_MAGUS_BIN
 * itself. Returns null when none resolves; every case below skips rather than
 * fails, because a fresh clone has no binary and a gate that needed one would
 * fail on checkout.
 */
function resolveMagusBinary(): string | null {
  const envBin = process.env.GUARD_MAGUS_BIN;
  if (envBin && isExecutable(envBin)) return envBin;
  const rootBin = path.join(WORKSPACE_ROOT, "magus");
  if (isExecutable(rootBin)) return rootBin;
  return resolveOnPath("magus");
}

const magusBin = resolveMagusBinary();
const skip =
  magusBin === null
    ? "no magus binary found (set GUARD_MAGUS_BIN, build ./magus at the workspace root, or put magus on PATH)"
    : false;
if (magusBin === null) {
  console.warn(`[opencode-plugin.live.test] skipping: ${skip}`);
}

/** `bin` is argv[0] as the plugin spreads it; `argv` is everything after it. */
type SpawnCall = { bin: string; argv: string[]; stdin: string };

/**
 * Installs a Bun.spawn shim that runs a REAL child process via
 * node:child_process.spawn, mirroring the shape of the recorded shim in
 * opencode-plugin.test.ts (same argv/stdin capture) but backed by the resolved
 * magus binary instead of a canned reply.
 */
function stubBunWithRealChild(bin: string): SpawnCall[] {
  const calls: SpawnCall[] = [];
  const spawn = (spawned: string[], opts: { stdin?: Uint8Array }) => {
    const stdin = opts.stdin ? new TextDecoder().decode(opts.stdin) : "";
    calls.push({ bin: spawned[0], argv: spawned.slice(1), stdin });

    const child = nodeSpawn(spawned[0], spawned.slice(1), {
      cwd: WORKSPACE_ROOT,
      stdio: ["pipe", "pipe", "ignore"],
    });
    child.stdin.end(opts.stdin ?? new Uint8Array());

    const stdout = new ReadableStream<Uint8Array>({
      start(controller) {
        child.stdout.on("data", (chunk: Buffer) => controller.enqueue(new Uint8Array(chunk)));
        child.stdout.on("end", () => controller.close());
      },
    });
    const exited = new Promise<number>((resolve) => {
      child.on("close", (code) => resolve(code ?? 0));
    });
    return { stdout, exited };
  };
  (globalThis as unknown as { Bun: unknown }).Bun = { spawn };
  return calls;
}

/** Collects console.warn for the duration of `body`. */
async function withWarnings(body: () => Promise<void>): Promise<string[]> {
  const warnings: string[] = [];
  const original = console.warn;
  console.warn = (...args: unknown[]) => {
    warnings.push(args.join(" "));
  };
  try {
    await body();
  } finally {
    console.warn = original;
  }
  return warnings;
}

/** The plugin ignores its PluginInput entirely (it is declared `async () =>`). */
async function hooks() {
  const plugin = MagusGuard as unknown as () => Promise<{
    "tool.execute.before": (
      input: { tool: string },
      output: { args: Record<string, unknown> },
    ) => Promise<void>;
  }>;
  return await plugin();
}

test("live: bash arm denies git stash with the whole-tree reason", { skip }, async () => {
  stubBunWithRealChild(magusBin as string);
  const h = await hooks();
  await assert.rejects(
    h["tool.execute.before"]({ tool: "bash" }, { args: { command: "git stash" } }),
    /whole-tree/,
  );
});

// go test ./... now has an exact magus equivalent (magus run go::go-test) and is
// DENIED, not advised - the plan this test was written from predates that rule.
// git push stays an advise: a push can legitimately carry work-in-progress, so the
// guard only reminds rather than blocks (see guardPushRe in cmd/magus/agent.go).
test("live: bash arm advises on git push and logs non-empty context", { skip }, async () => {
  stubBunWithRealChild(magusBin as string);
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"]({ tool: "bash" }, { args: { command: "git push origin main" } });
  });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /\[magus guard\] .+\S/);
});

test("live: edit arm passes on a path that is not a declared output", { skip }, async () => {
  stubBunWithRealChild(magusBin as string);
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"]({ tool: "edit" }, { args: { filePath: "cmd/magus/agent.go" } });
  });
  assert.deepEqual(warnings, []);
});
