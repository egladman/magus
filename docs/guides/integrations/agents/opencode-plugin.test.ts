// Transport parity for the OpenCode plugin: does it ASK magus correctly?
//
// The sh templates get this from testscript (cmd/magus/testdata/script/guard_templates.txtar),
// which runs them against real events with a real binary. The plugin cannot join
// them there: it calls Bun.spawn, and this repo's toolchain pins node, not bun.
// Adding a second JS runtime to every contributor's setup for one 150-line file
// is a worse trade than what this file does instead - supply Bun.spawn, which is
// the only Bun API the plugin touches, and leave the artifact OpenCode loads
// completely unmodified.
//
// This is the half that was missing when the plugin shipped broken for months. It
// invoked `magus agent hook` (a subcommand that no longer exists) and passed the
// command in argv (which `magus hook` rejects, because it reads stdin), so it
// never received a verdict at all - and its fail-open arm logged one line per tool
// call and allowed everything. Both mistakes are argv-and-stdin mistakes, which is
// exactly what the assertions below pin.
//
// Coverage is by COMPOSITION, and the seam is worth naming: this file proves the
// plugin asks correctly, and guard_templates.txtar proves that question gets the
// right answer from a real binary. Set GUARD_MAGUS_BIN to a built magus to close
// the seam locally and run the live case at the bottom against it.

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { accessSync, constants } from "node:fs";
import test from "node:test";

import { MagusGuard } from "./opencode-plugin.ts";

/** `bin` is argv[0] as the plugin spreads it; `argv` is everything after it. */
type SpawnCall = { bin: string; argv: string[]; stdin: string };

/** The verdicts a fake magus can be told to return, by the input it is asked about. */
type Canned = { schema_version: number; decision: string; reason?: string; context?: string };

/**
 * Installs a Bun.spawn that records how it was called and answers from `reply`.
 * Returns the recorded calls. `reply` receiving null means "no such binary",
 * which is how the fail-open path is exercised.
 */
function stubBun(reply: ((call: SpawnCall) => Canned | null) | null): SpawnCall[] {
  const calls: SpawnCall[] = [];
  const spawn = (spawned: string[], opts: { stdin?: Uint8Array }) => {
    const stdin = opts.stdin ? new TextDecoder().decode(opts.stdin) : "";
    const call = { bin: spawned[0], argv: spawned.slice(1), stdin };
    calls.push(call);
    if (reply === null) throw new Error("spawn failed: no such file or directory");
    const verdict = reply(call);
    const body = verdict === null ? "" : JSON.stringify(verdict);
    return {
      stdout: new ReadableStream<Uint8Array>({
        start(controller) {
          controller.enqueue(new TextEncoder().encode(body));
          controller.close();
        },
      }),
      exited: Promise.resolve(0),
    };
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

const deny: Canned = { schema_version: 1, decision: "deny", reason: "whole-tree git stash" };
const advise: Canned = { schema_version: 1, decision: "advise", context: "that file is generated" };
const pass: Canned = { schema_version: 1, decision: "pass" };

test("a shell command is judged over stdin by the top-level hook subcommand", async () => {
  const calls = stubBun(() => deny);
  const h = await hooks();

  await assert.rejects(
    h["tool.execute.before"]({ tool: "bash" }, { args: { command: "git stash" } }),
    /whole-tree git stash/,
  );

  assert.equal(calls.length, 1);
  // The exact contract, spelled out rather than pattern-matched: these are the two
  // things that were wrong, and a loose assertion would have passed on both.
  assert.deepEqual(calls[0].argv, ["hook", "--host", "opencode", "-o", "json"]);
  assert.equal(calls[0].stdin, "git stash");
  assert.ok(!calls[0].argv.includes("agent"), "`magus agent hook` was removed; hook is top level");
  assert.ok(!calls[0].argv.includes("--"), "the command goes on stdin; hook takes no positionals");
});

test("a file write is judged on the path surface, also over stdin", async () => {
  const calls = stubBun(() => advise);
  const h = await hooks();

  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"]({ tool: "write" }, { args: { filePath: "gen/index.json" } });
  });

  assert.deepEqual(calls[0].argv, ["hook", "--path", "--host", "opencode", "-o", "json"]);
  assert.equal(calls[0].stdin, "gen/index.json");
  // Declared advise=human: OpenCode has no context-injection arm, so an advise
  // reaches the person and never the model. It must not throw.
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /that file is generated/);
});

test("a pass is silent and blocks nothing", async () => {
  stubBun(() => pass);
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"]({ tool: "bash" }, { args: { command: "ls -la" } });
  });
  assert.deepEqual(warnings, []);
});

test("an unrunnable magus fails OPEN, loudly", async () => {
  stubBun(null);
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    // Must not throw: throwing is OpenCode's only way to stop a call, so a guard
    // that threw when magus was missing would make every session unusable.
    await h["tool.execute.before"]({ tool: "bash" }, { args: { command: "git stash" } });
  });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /UNGUARDED/);
});

test("a verdict from an unknown schema is ignored rather than obeyed", async () => {
  stubBun(() => ({ ...deny, schema_version: 99 }));
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"]({ tool: "bash" }, { args: { command: "git stash" } });
  });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /schema 99/);
});

// The seam-closing case. Skipped unless GUARD_MAGUS_BIN names a real binary,
// because a fresh clone has none and a gate that needs one would fail on checkout.
// Run it with: GUARD_MAGUS_BIN=$PWD/magus pnpm --dir docs/guides/integrations/agents test
const magusBin = process.env.GUARD_MAGUS_BIN;
const haveBinary = (() => {
  if (magusBin === undefined || magusBin === "") return false;
  try {
    accessSync(magusBin, constants.X_OK);
    return true;
  } catch {
    return false;
  }
})();

test("live: the recorded argv really produces a deny from magus", { skip: !haveBinary }, () => {
  const argv = ["hook", "--host", "opencode", "-o", "json"];
  const proc = spawnSync(magusBin as string, argv, { input: "git stash", encoding: "utf8" });
  const verdict = JSON.parse(proc.stdout);
  assert.equal(verdict.decision, "deny");
  assert.equal(verdict.schema_version, 1);
});
