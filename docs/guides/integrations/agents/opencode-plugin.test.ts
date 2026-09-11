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
// command in argv (which `magus session hook` rejects, because it reads stdin), so it
// never received a verdict at all - and its fail-open arm logged one line per tool
// call and allowed everything. Both mistakes are argv-and-stdin mistakes, which is
// exactly what the assertions below pin.
//
// Coverage is by COMPOSITION, and the seam is worth naming: this file proves the
// plugin asks correctly (recorded shim = invocation shape), guard_templates.txtar
// proves the sh templates get the right answer from a real binary, and
// opencode-plugin.live.test.ts proves THIS plugin does too (live = binary
// interface) - it drives the same exported functions through a Bun.spawn shim
// backed by a real child process instead of a canned reply. Set GUARD_MAGUS_BIN to
// a built magus to run it locally; it skips loudly without one.

import assert from "node:assert/strict";
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
      input: { tool: string; callID: string },
      output: { args: Record<string, unknown> },
    ) => Promise<void>;
    "tool.execute.after": (input: { callID: string }, output: { output: string }) => Promise<void>;
    "experimental.session.compacting": (
      input: Record<string, never>,
      output: { context: string[] },
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
    h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command: "git stash" } }),
    /whole-tree git stash/,
  );

  assert.equal(calls.length, 1);
  // The exact contract, spelled out rather than pattern-matched: these are the two
  // things that were wrong, and a loose assertion would have passed on both.
  assert.deepEqual(calls[0].argv, ["session", "hook", "--agent-name", "opencode", "-o", "json"]);
  assert.equal(calls[0].stdin, "git stash");
  assert.ok(
    !calls[0].argv.includes("agent"),
    "`magus agent hook` was removed; hook lives under session",
  );
  assert.ok(!calls[0].argv.includes("--"), "the command goes on stdin; hook takes no positionals");
});

test("a file write is judged on the path surface, also over stdin", async () => {
  const calls = stubBun(() => advise);
  const h = await hooks();

  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"](
      { tool: "write", callID: "c1" },
      { args: { filePath: "gen/index.json" } },
    );
  });

  assert.deepEqual(calls[0].argv, [
    "session",
    "hook",
    "--path",
    "--agent-name",
    "opencode",
    "-o",
    "json",
  ]);
  assert.equal(calls[0].stdin, "gen/index.json");
  // An advise must not throw, and must not be logged either: it is held for the
  // call it judged and appended to that call's own result, which is the only
  // channel on this host that a model reads.
  assert.deepEqual(warnings, []);

  const result = { output: "wrote gen/index.json" };
  await h["tool.execute.after"]({ callID: "c1" }, result);
  assert.match(result.output, /wrote gen\/index\.json/);
  assert.match(result.output, /\[magus guard\] that file is generated/);
  assert.equal(calls.length, 1, "the advisory is delivered, not judged a second time");
});

test("an advisory reaches only the call it judged", async () => {
  stubBun(() => advise);
  const h = await hooks();

  await h["tool.execute.before"]({ tool: "bash", callID: "judged" }, { args: { command: "rg x" } });

  const other = { output: "unrelated" };
  await h["tool.execute.after"]({ callID: "unjudged" }, other);
  assert.equal(other.output, "unrelated");

  const mine = { output: "matches" };
  await h["tool.execute.after"]({ callID: "judged" }, mine);
  assert.match(mine.output, /\[magus guard\]/);

  // Delivered once: the entry is dropped when its call lands, so a second result
  // carrying the same id does not get the advisory again.
  const again = { output: "matches" };
  await h["tool.execute.after"]({ callID: "judged" }, again);
  assert.equal(again.output, "matches");
});

test("a compacting session is handed the brief", async () => {
  const calls = stubBun(() => null);
  const h = await hooks();

  const output: { context: string[] } = { context: [] };
  await h["experimental.session.compacting"]({}, output);

  assert.deepEqual(calls[0].argv, ["session", "--brief"]);
  assert.deepEqual(output.context, [], "an empty brief adds nothing to the compaction prompt");
});

test("a pass is silent and blocks nothing", async () => {
  stubBun(() => pass);
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command: "ls -la" } });
  });
  assert.deepEqual(warnings, []);
});

test("an unrunnable magus fails OPEN, loudly", async () => {
  stubBun(null);
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    // Must not throw: throwing is OpenCode's only way to stop a call, so a guard
    // that threw when magus was missing would make every session unusable.
    await h["tool.execute.before"](
      { tool: "bash", callID: "c1" },
      { args: { command: "git stash" } },
    );
  });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /UNGUARDED/);
});

test("a verdict from an unknown schema is ignored rather than obeyed", async () => {
  stubBun(() => ({ ...deny, schema_version: 99 }));
  const h = await hooks();
  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"](
      { tool: "bash", callID: "c1" },
      { args: { command: "git stash" } },
    );
  });
  assert.equal(warnings.length, 1);
  assert.match(warnings[0], /schema 99/);
});
