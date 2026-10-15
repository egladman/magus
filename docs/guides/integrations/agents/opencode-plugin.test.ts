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
// backed by a real child process instead of a canned reply. Set __MAGUS_BIN to
// a built magus to run it locally; it skips loudly without one.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
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

type Row = {
  info: { role: string };
  parts: Array<{ type: string; text?: string; synthetic?: boolean }>;
};

/** What the SDK client answers: session parents by id, message lists by id, prompts recorded. */
type Sessions = {
  parents?: Record<string, string>;
  messages?: Record<string, Row[]>;
  prompts?: Array<{ id: string; text: string }>;
};

/** The slice of OpenCode's PluginInput the plugin reads. */
function pluginInput(sessions: Sessions = {}) {
  return {
    directory: "/work",
    client: {
      session: {
        get: async ({ path }: { path: { id: string } }) => ({
          data: { id: path.id, parentID: sessions.parents?.[path.id] },
        }),
        messages: async ({ path }: { path: { id: string } }) => ({
          data: sessions.messages?.[path.id] ?? [],
        }),
        promptAsync: async ({
          path,
          body,
        }: {
          path: { id: string };
          body: { parts: Array<{ text: string }> };
        }) => {
          sessions.prompts?.push({ id: path.id, text: body.parts[0].text });
          return {};
        },
      },
    },
  };
}

async function hooks(sessions: Sessions = {}) {
  const plugin = MagusGuard as unknown as (input: unknown) => Promise<{
    "tool.execute.before": (
      input: { tool: string; callID: string; sessionID?: string },
      output: { args: Record<string, unknown> },
    ) => Promise<void>;
    "tool.execute.after": (
      input: { tool?: string; callID: string; sessionID?: string; args?: unknown },
      output: { output?: string; content?: unknown[]; metadata?: Record<string, unknown> },
    ) => Promise<void>;
    config: (input: Record<string, unknown>) => Promise<void>;
    "chat.message": (
      input: { sessionID: string },
      output: { parts: Array<{ type: string; text?: string; synthetic?: boolean }> },
    ) => Promise<void>;
    "shell.env": (input: { cwd: string }, output: { env: Record<string, string> }) => Promise<void>;
    event: (input: {
      event: { type: string; properties: Record<string, unknown> };
    }) => Promise<void>;
    "permission.ask": (
      input: {
        type: string;
        title: string;
        pattern?: string | string[];
        metadata: Record<string, unknown>;
      },
      output: { status: "ask" | "deny" | "allow" },
    ) => Promise<void>;
    // Optional, and asserted ABSENT below: magus registers no pre-compaction handler.
    "experimental.session.compacting"?: (
      input: Record<string, never>,
      output: { context: string[] },
    ) => Promise<void>;
  }>;
  return await plugin(pluginInput(sessions));
}

const deny: Canned = { schema_version: 1, decision: "deny", reason: "whole-tree git stash" };
const advise: Canned = { schema_version: 1, decision: "advise", context: "that file is generated" };
const pass: Canned = { schema_version: 1, decision: "pass" };

test("a shell command is judged over stdin by the top-level shell subcommand", async () => {
  const calls = stubBun(() => deny);
  const h = await hooks();

  await assert.rejects(
    h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command: "git stash" } }),
    /whole-tree git stash/,
  );

  assert.equal(calls.length, 1);
  // The exact contract, spelled out rather than pattern-matched: these are the two
  // things that were wrong, and a loose assertion would have passed on both.
  // --renders-ask is the claim that an ask never passes through unasked; without it magus
  // answers every ask with a deny.
  assert.deepEqual(calls[0].argv, [
    "shell",
    "--agent-name",
    "opencode",
    "--renders-ask",
    "-o",
    "json",
  ]);
  assert.equal(calls[0].stdin, "git stash");
  assert.ok(
    !calls[0].argv.includes("agent"),
    "`magus agent hook` was removed; hook lives under session",
  );
  assert.ok(!calls[0].argv.includes("--"), "the command goes on stdin; hook takes no positionals");
});

test("a call carries OpenCode's session, so push authority can tell the root from a subagent", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();

  await h["tool.execute.before"](
    { tool: "bash", callID: "c1", sessionID: "s1" },
    { args: { command: "git push" } },
  );

  assert.deepEqual(calls[0].argv, [
    "shell",
    "--agent-name",
    "opencode",
    "--renders-ask",
    "-o",
    "json",
    "--session",
    "s1",
  ]);
});

test("a file write is judged on the path input, also over stdin", async () => {
  const calls = stubBun(() => advise);
  const h = await hooks();

  const warnings = await withWarnings(async () => {
    await h["tool.execute.before"](
      { tool: "write", callID: "c1" },
      { args: { filePath: "gen/index.json" } },
    );
  });

  assert.deepEqual(calls[0].argv, [
    "shell",
    "--path",
    "--agent-name",
    "opencode",
    "--renders-ask",
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

test("nothing is written into the compaction prompt", async () => {
  const h = await hooks();

  // Pinned as an ABSENCE because the handler was there and was removed on purpose. The
  // hook appends to OpenCode's summarizer prompt, so anything registered here shapes what
  // the summary keeps -- which no other host magus wires can do, and which contradicts the
  // brief's own contract that it is state read from disk rather than prose retold. See the
  // comment where the handler used to be in opencode-plugin.ts.
  assert.equal(
    h["experimental.session.compacting"],
    undefined,
    "magus must not register a pre-compaction handler: it steers the summary on the one host that allows it",
  );
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

const ask: Canned = {
  schema_version: 1,
  decision: "ask",
  reason: "pushing abc1234, which no passing gate covers",
};
const pushPrompt = { permission: { bash: { "git push": "ask", "git push *": "ask" } } };
type Status = "ask" | "deny" | "allow";

test("an unknown decision refuses the call, never allows it", async () => {
  stubBun(() => ({ schema_version: 1, decision: "maybe", reason: "?" }));
  const h = await hooks();
  await assert.rejects(
    h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command: "ls" } }),
    /does not know/,
  );
  await assert.rejects(
    h["tool.execute.before"]({ tool: "write", callID: "c2" }, { args: { filePath: "README.md" } }),
    /does not know/,
  );
});

test("an ask on a push reaches OpenCode's prompt when the config asks for push", async () => {
  stubBun(() => ask);
  const h = await hooks();
  await h.config(pushPrompt);
  await h["tool.execute.before"](
    { tool: "bash", callID: "c1" },
    { args: { command: "git push origin HEAD" } },
  );
});

test("an ask with no push prompt configured refuses, naming the person's terminal", async () => {
  stubBun(() => ask);
  const h = await hooks();
  await h.config({ permission: { bash: { "git push *": "allow" } } });
  await assert.rejects(
    h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command: "git push" } }),
    /own terminal/,
  );
});

test("an ask no push prompt can match refuses", async () => {
  stubBun(() => ask);
  const h = await hooks();
  await h.config(pushPrompt);
  await assert.rejects(
    h["tool.execute.before"](
      { tool: "bash", callID: "c1" },
      { args: { command: "git -C x push" } },
    ),
    /own terminal/,
  );
  await assert.rejects(
    h["tool.execute.before"]({ tool: "write", callID: "c2" }, { args: { filePath: "README.md" } }),
    /own terminal/,
  );
});

test("permission.ask answers a push prompt from the verdict", async () => {
  const cases: Array<[Canned, Status]> = [
    [pass, "allow"],
    [{ schema_version: 1, decision: "advise", context: "gate ran" }, "allow"],
    [ask, "ask"],
    [deny, "deny"],
    [{ schema_version: 1, decision: "maybe" }, "deny"],
  ];
  for (const [verdict, want] of cases) {
    const calls = stubBun(() => verdict);
    const h = await hooks();
    await h.config(pushPrompt);
    const output: { status: Status } = { status: "ask" };
    await h["permission.ask"](
      {
        type: "bash",
        title: "git push origin HEAD",
        pattern: ["git push origin HEAD"],
        metadata: { command: "git push origin HEAD" },
      },
      output,
    );
    assert.equal(output.status, want, `decision ${verdict.decision}`);
    assert.equal(calls[0].stdin, "git push origin HEAD");
  }
});

const everyPushPrompt = {
  permission: {
    bash: {
      "git push": "ask",
      "git push *": "ask",
      "hg push": "ask",
      "hg push *": "ask",
      "sl push": "ask",
      "sl push *": "ask",
      "jj git push": "ask",
      "jj git push *": "ask",
    },
  },
};

test("every backend's plain push reaches OpenCode's prompt when the config asks for it", async () => {
  for (const command of ["hg push", "sl push --to main", "jj git push -b main"]) {
    stubBun(() => ask);
    const h = await hooks();
    await h.config(everyPushPrompt);
    await h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command } });
  }
});

test("a push whose own backend is not asked about is refused", async () => {
  stubBun(() => ask);
  const h = await hooks();
  await h.config(pushPrompt);
  await assert.rejects(
    h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command: "hg push" } }),
    /own terminal/,
  );
});

test("permission.ask approves each backend's covered push, and nothing else", async () => {
  for (const command of ["hg push", "sl push --to main", "jj git push -b main"]) {
    const calls = stubBun(() => pass);
    const h = await hooks();
    await h.config(everyPushPrompt);
    const output: { status: Status } = { status: "ask" };
    await h["permission.ask"]({ type: "bash", title: command, metadata: { command } }, output);
    assert.equal(output.status, "allow", command);
    assert.equal(calls[0].stdin, command);
  }
  for (const command of ["hg pull", "jj git fetch", "sl push && rm -rf x"]) {
    const calls = stubBun(() => pass);
    const h = await hooks();
    await h.config(everyPushPrompt);
    const output: { status: Status } = { status: "ask" };
    await h["permission.ask"]({ type: "bash", title: command, metadata: { command } }, output);
    assert.equal(output.status, "ask", command);
    assert.equal(calls.length, 0, `${command} is not a push magus configured`);
  }
});

test("permission.ask leaves every other prompt to the person", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();
  const output: { status: Status } = { status: "ask" };
  await h["permission.ask"](
    { type: "bash", title: "rm -rf build", metadata: { command: "rm -rf build" } },
    output,
  );
  await h["permission.ask"]({ type: "edit", title: "README.md", metadata: {} }, output);
  assert.equal(output.status, "ask");
  assert.equal(calls.length, 0, "a prompt magus did not configure is not judged");
});

test("permission.ask with no runnable magus keeps the prompt", async () => {
  stubBun(null);
  const h = await hooks();
  await h.config(pushPrompt);
  const output: { status: Status } = { status: "ask" };
  await withWarnings(async () => {
    await h["permission.ask"](
      { type: "bash", title: "git push", metadata: { command: "git push" } },
      output,
    );
  });
  assert.equal(output.status, "ask");
});

const humanOnlyPrompt = {
  permission: {
    bash: {
      "magus run agent-bench-run": "ask",
      "magus run agent-bench-run *": "ask",
      "./magus run agent-bench-run": "ask",
      "./magus run agent-bench-run *": "ask",
    },
  },
};

const humanOnlyAsk: Canned = {
  schema_version: 1,
  decision: "ask",
  reason: "`magus run agent-bench-run .` is run by a person: launches paid sessions",
};

test("an ask on a human-only run reaches OpenCode's prompt when the config asks for it", async () => {
  for (const command of ["magus run agent-bench-run .", "./magus run agent-bench-run"]) {
    stubBun(() => humanOnlyAsk);
    const h = await hooks();
    await h.config(humanOnlyPrompt);
    await h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command } });
  }
});

test("an ask on a run no human-only prompt matches refuses", async () => {
  for (const command of ["magus run --no-cache agent-bench-run .", "magus affected agent-bench-run"]) {
    stubBun(() => humanOnlyAsk);
    const h = await hooks();
    await h.config(humanOnlyPrompt);
    await assert.rejects(
      h["tool.execute.before"]({ tool: "bash", callID: "c1" }, { args: { command } }),
      /own terminal/,
      command,
    );
  }
});

test("permission.ask answers a human-only run's prompt, and no run magus did not configure", async () => {
  const calls = stubBun(() => humanOnlyAsk);
  const h = await hooks();
  await h.config(humanOnlyPrompt);
  const asked: { status: Status } = { status: "allow" };
  const command = "magus run agent-bench-run .";
  await h["permission.ask"]({ type: "bash", title: command, metadata: { command } }, asked);
  assert.equal(asked.status, "ask");
  assert.equal(calls[0].stdin, command);

  const other: { status: Status } = { status: "ask" };
  await h["permission.ask"](
    { type: "bash", title: "magus run lint .", metadata: { command: "magus run lint ." } },
    other,
  );
  assert.equal(other.status, "ask");
  assert.equal(calls.length, 1, "a run whose prompt magus did not configure is not judged");
});

/** Parses the Claude Code envelope the plugin piped for one call. */
function sent(call: SpawnCall): Record<string, unknown> {
  return JSON.parse(call.stdin) as Record<string, unknown>;
}

const magusServer = { mcp: { magus: { type: "local", command: ["./magus", "mcp"] } } };

test("a call to magus's MCP server is judged as the tool it names, in the Claude Code envelope", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();
  await h.config(magusServer);

  await h["tool.execute.before"](
    { tool: "magus_client", callID: "m1", sessionID: "s1" },
    { args: { op: "affected" } },
  );

  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].argv, [
    "shell",
    "--agent-name",
    "opencode",
    "--renders-ask",
    "-o",
    "json",
  ]);
  assert.deepEqual(sent(calls[0]), {
    hook_event_name: "PreToolUse",
    session_id: "s1",
    tool_name: "mcp__magus__client",
    tool_input: { op: "affected" },
    cwd: "/work",
  });
});

test("the magus server is found by its command, and its tool ids are OpenCode's sanitized ones", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();
  await h.config({
    mcp: {
      "project magus": { type: "local", command: ["/opt/bin/magus", "mcp"] },
      echo: { type: "local", command: ["node", "echo.mjs"] },
      magus: { type: "local", command: ["node", "not-magus.mjs"] },
    },
  });

  await h["tool.execute.before"]({ tool: "project_magus_status", callID: "m1" }, { args: {} });
  assert.equal(sent(calls[0]).tool_name, "mcp__magus__status");

  await h["tool.execute.before"]({ tool: "echo_say_hi", callID: "m2" }, { args: { who: "x" } });
  await h["tool.execute.before"]({ tool: "magus_client", callID: "m3" }, { args: {} });
  assert.deepEqual(
    calls.slice(1).map((call) => sent(call).tool_name),
    ["echo_say_hi", "magus_client"],
    "another server's tool, and a key that runs something else, are judged under their own names, not as magus's",
  );
});

test("an MCP call with no config for magus is judged under its own name", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();
  await h["tool.execute.before"]({ tool: "magus_client", callID: "m1" }, { args: {} });
  assert.equal(calls.length, 1);
  assert.equal(sent(calls[0]).tool_name, "magus_client");
});

test("every other tool reaches magus whole, its file path spelled the way magus reads it", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();
  await h["tool.execute.before"]({ tool: "todowrite", callID: "o1" }, { args: { todos: [] } });
  await h["tool.execute.before"](
    { tool: "read", callID: "o2" },
    { args: { filePath: "README.md", limit: 40 } },
  );
  assert.equal(calls.length, 2, "nothing is left unseen");
  assert.equal(sent(calls[0]).tool_name, "todowrite");
  assert.deepEqual(sent(calls[1]).tool_input, {
    filePath: "README.md",
    limit: 40,
    file_path: "README.md",
  });
});

test("a deny on an MCP call throws, and an advise rides in the result's content", async () => {
  stubBun(() => deny);
  let h = await hooks();
  await h.config(magusServer);
  await assert.rejects(
    h["tool.execute.before"]({ tool: "magus_client", callID: "m1" }, { args: {} }),
    /whole-tree git stash/,
  );

  stubBun(() => advise);
  h = await hooks();
  await h.config(magusServer);
  await h["tool.execute.before"]({ tool: "magus_client", callID: "m2" }, { args: {} });
  const result = { content: [{ type: "text", text: "hi" }] };
  await h["tool.execute.after"]({ callID: "m2" }, result);
  assert.deepEqual(result.content, [
    { type: "text", text: "hi" },
    { type: "text", text: "[magus guard] that file is generated" },
  ]);
});

test("a patch goes to magus whole, as the envelope's patchText", async () => {
  const calls = stubBun(() => deny);
  const h = await hooks();
  const patchText = "*** Begin Patch\n*** Update File: gen/index.json\n*** End Patch";
  await assert.rejects(
    h["tool.execute.before"](
      { tool: "apply_patch", callID: "p1", sessionID: "s1" },
      { args: { patchText } },
    ),
    /whole-tree git stash/,
  );
  assert.equal(sent(calls[0]).tool_name, "apply_patch");
  assert.deepEqual(sent(calls[0]).tool_input, { patchText });
});

const spawnArgs = { subagent_type: "general", prompt: "Review shell.go.", description: "Review" };

test("a spawn is judged by its prompt, and the finished one names the child", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();

  await h["tool.execute.before"](
    { tool: "task", callID: "t1", sessionID: "root" },
    { args: spawnArgs },
  );
  assert.equal(sent(calls[0]).hook_event_name, "PreToolUse");
  assert.deepEqual(sent(calls[0]).tool_input, spawnArgs);

  await h["tool.execute.after"](
    { tool: "task", callID: "t1", sessionID: "root", args: spawnArgs },
    { output: "done", metadata: { sessionId: "child", parentSessionId: "root" } },
  );
  assert.equal(calls.length, 2);
  assert.equal(sent(calls[1]).hook_event_name, "PostToolUse");
  assert.deepEqual(sent(calls[1]).tool_response, { agent_id: "child" });
  assert.deepEqual(sent(calls[1]).tool_input, spawnArgs);
});

test("a spawn with no prompt is not judged, and a denied spawn throws", async () => {
  const calls = stubBun(() => deny);
  const h = await hooks();
  await h["tool.execute.before"]({ tool: "task", callID: "t0" }, { args: { description: "x" } });
  assert.equal(calls.length, 0);
  await assert.rejects(
    h["tool.execute.before"]({ tool: "task", callID: "t1" }, { args: spawnArgs }),
    /whole-tree git stash/,
  );
});

test("a subagent's calls carry the root session and the child as the agent", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks({ parents: { child: "root" } });

  await h["tool.execute.before"](
    { tool: "bash", callID: "c1", sessionID: "child" },
    { args: { command: "ls" } },
  );
  assert.deepEqual(calls[0].argv.slice(-4), ["--session", "root", "--agent", "child"]);

  await h["tool.execute.before"](
    { tool: "write", callID: "c2", sessionID: "child" },
    { args: { filePath: "a.md" } },
  );
  assert.deepEqual(calls[1].argv.slice(-4), ["--session", "root", "--agent", "child"]);

  await h["tool.execute.before"](
    { tool: "task", callID: "c3", sessionID: "child" },
    { args: spawnArgs },
  );
  assert.equal(sent(calls[2]).session_id, "root");
  assert.equal(sent(calls[2]).agent_id, "child");
});

test("a session.created event gives a session's parent without asking the SDK", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks();
  await h.event({
    event: { type: "session.created", properties: { info: { id: "kid", parentID: "root" } } },
  });
  await h["tool.execute.before"](
    { tool: "bash", callID: "c1", sessionID: "kid" },
    { args: { command: "ls" } },
  );
  assert.deepEqual(calls[0].argv.slice(-4), ["--session", "root", "--agent", "kid"]);
});

test("a message the person types goes to magus shell --message", async () => {
  const calls = stubBun(() => pass);
  const h = await hooks({ parents: { child: "root" } });

  await h["chat.message"](
    { sessionID: "s1" },
    {
      parts: [
        { type: "text", text: "fix the build" },
        { type: "text", text: "injected", synthetic: true },
      ],
    },
  );
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].argv, [
    "shell",
    "--message",
    "--agent-name",
    "opencode",
    "--session",
    "s1",
  ]);
  assert.equal(calls[0].stdin, "fix the build");

  await h["chat.message"](
    { sessionID: "child" },
    { parts: [{ type: "text", text: "the spawn prompt" }] },
  );
  await h["chat.message"]({ sessionID: "s1" }, { parts: [{ type: "text", text: "  " }] });
  assert.equal(calls.length, 1, "a subagent's prompt and an empty message record nothing");
});

test("the shell env is quiet unless the person set it", async () => {
  const h = await hooks();
  const saved = process.env.MAGUS_LOG_SILENT;
  try {
    delete process.env.MAGUS_LOG_SILENT;
    const env: Record<string, string> = {};
    await h["shell.env"]({ cwd: "/work" }, { env });
    assert.equal(env.MAGUS_LOG_SILENT, "true");

    const set: Record<string, string> = { MAGUS_LOG_SILENT: "false" };
    await h["shell.env"]({ cwd: "/work" }, { env: set });
    assert.equal(set.MAGUS_LOG_SILENT, "false");

    process.env.MAGUS_LOG_SILENT = "false";
    const inherited: Record<string, string> = {};
    await h["shell.env"]({ cwd: "/work" }, { env: inherited });
    assert.equal(inherited.MAGUS_LOG_SILENT, undefined);
  } finally {
    if (saved === undefined) delete process.env.MAGUS_LOG_SILENT;
    else process.env.MAGUS_LOG_SILENT = saved;
  }
});

const idle = (sessionID: string) => ({
  event: { type: "session.idle", properties: { sessionID } },
});

/** Runs `body` with no reply script discoverable, so an idle records only its checkpoint. */
async function withoutReplyScript(body: () => Promise<void>): Promise<void> {
  const saved = process.env.__PROOFREAD_REPLY_BUZZ;
  process.env.__PROOFREAD_REPLY_BUZZ = "/nonexistent/proofread-reply.buzz";
  try {
    await body();
  } finally {
    if (saved === undefined) delete process.env.__PROOFREAD_REPLY_BUZZ;
    else process.env.__PROOFREAD_REPLY_BUZZ = saved;
  }
}

test("a root session going idle records a checkpoint; a subagent's idle records none", async () => {
  await withoutReplyScript(async () => {
    const calls = stubBun(() => pass);
    const h = await hooks({ parents: { child: "root" } });

    await h.event(idle("child"));
    assert.equal(calls.length, 0);

    await h.event(idle("root"));
    assert.deepEqual(calls[0].argv, ["session", "checkpoint", "--agent-name", "opencode"]);
  });
});

/** Runs `body` with an empty proofread-reply.buzz where the plugin looks first. */
async function withReplyScript(body: (script: string) => Promise<void>): Promise<void> {
  const dir = mkdtempSync(join(tmpdir(), "plugin-reply-"));
  const script = join(dir, "proofread-reply.buzz");
  writeFileSync(script, "");
  const saved = process.env.__PROOFREAD_REPLY_BUZZ;
  process.env.__PROOFREAD_REPLY_BUZZ = script;
  try {
    await body(script);
  } finally {
    if (saved === undefined) delete process.env.__PROOFREAD_REPLY_BUZZ;
    else process.env.__PROOFREAD_REPLY_BUZZ = saved;
    rmSync(dir, { recursive: true, force: true });
  }
}

const turn = (reply: string): Row[] => [
  { info: { role: "user" }, parts: [{ type: "text", text: "question" }] },
  { info: { role: "assistant" }, parts: [{ type: "text", text: reply }, { type: "tool" }] },
  { info: { role: "assistant" }, parts: [{ type: "tool" }] },
];

const followUp = "proofread judged your last reply. Send it again.";
const findings = { prompt: followUp } as unknown as Canned;

test("findings on a root session's reply go back as one follow-up prompt per turn", async () => {
  await withReplyScript(async (script) => {
    const calls = stubBun((call) => (call.argv[0] === "buzz" ? findings : pass));
    const prompts: Array<{ id: string; text: string }> = [];
    const h = await hooks({ messages: { root: turn("Done. Want me to go on?") }, prompts });
    const judged = () => calls.filter((call) => call.argv[0] === "buzz");

    await h.event(idle("root"));
    assert.equal(judged().length, 1);
    assert.deepEqual(judged()[0].argv.slice(1), ["-s", script, "--", "--agent-name", "opencode"]);
    assert.deepEqual(JSON.parse(judged()[0].stdin), {
      last_assistant_message: "Done. Want me to go on?",
      stop_hook_active: false,
    });
    assert.deepEqual(prompts, [{ id: "root", text: followUp }]);

    // The idle after the follow-up is the follow-up's own reply: not judged, so no loop.
    await h["chat.message"]({ sessionID: "root" }, { parts: [{ type: "text", text: followUp }] });
    await h.event(idle("root"));
    assert.equal(judged().length, 1);
    assert.equal(prompts.length, 1);

    // A message from the person starts a fresh turn, which is judged again.
    await h["chat.message"]({ sessionID: "root" }, { parts: [{ type: "text", text: "thanks" }] });
    await h.event(idle("root"));
    assert.equal(judged().length, 2);
    assert.equal(prompts.length, 2);
  });
});

test("no findings, no reply, or a subagent means no follow-up", async () => {
  await withReplyScript(async () => {
    const prompts: Array<{ id: string; text: string }> = [];

    stubBun(() => pass);
    let h = await hooks({ messages: { root: turn("Done.") }, prompts });
    await h.event(idle("root"));

    stubBun(() => findings);
    h = await hooks({ messages: { root: [turn("x")[0]] }, prompts });
    await h.event(idle("root"));

    h = await hooks({ parents: { child: "root" }, messages: { child: turn("Done.") }, prompts });
    await h.event(idle("child"));

    assert.deepEqual(prompts, []);
  });
});

test("with no reply script the idle records its checkpoint and judges no reply", async () => {
  await withoutReplyScript(async () => {
    const calls = stubBun(() => findings);
    const h = await hooks({ messages: { root: turn("Done.") } });
    await h.event(idle("root"));
    assert.ok(!calls.some((call) => call.argv[0] === "buzz"));
  });
});

test("the reply check is off by default: with the variable unset or empty nothing is judged", async () => {
  const saved = process.env.__PROOFREAD_REPLY_BUZZ;
  try {
    for (const value of [undefined, ""]) {
      if (value === undefined) delete process.env.__PROOFREAD_REPLY_BUZZ;
      else process.env.__PROOFREAD_REPLY_BUZZ = value;
      const calls = stubBun(() => findings);
      const prompts: Array<{ id: string; text: string }> = [];
      const h = await hooks({ messages: { root: turn("Done.") }, prompts });
      await h.event(idle("root"));
      assert.ok(!calls.some((call) => call.argv[0] === "buzz"), `judged with the variable ${value === undefined ? "unset" : "empty"}`);
      assert.deepEqual(prompts, []);
    }
  } finally {
    if (saved === undefined) delete process.env.__PROOFREAD_REPLY_BUZZ;
    else process.env.__PROOFREAD_REPLY_BUZZ = saved;
  }
});

/**
 * Runs `body` with the process in `cwd` and no __MAGUS_BIN, so the plugin resolves its own
 * binary the way an installed copy does.
 */
async function inDirectory(cwd: string, body: () => Promise<void>): Promise<void> {
  const before = process.cwd();
  const pinned = process.env.__MAGUS_BIN;
  delete process.env.__MAGUS_BIN;
  process.chdir(cwd);
  try {
    await body();
  } finally {
    process.chdir(before);
    if (pinned !== undefined) process.env.__MAGUS_BIN = pinned;
  }
}

/** The binary the plugin spawns for a shell command judged from `cwd`. */
async function resolvedBinary(cwd: string): Promise<string> {
  const calls = stubBun(() => pass);
  await inDirectory(cwd, async () => {
    const h = await hooks();
    await h["tool.execute.before"]({ tool: "bash", callID: "r1" }, { args: { command: "ls" } });
  });
  return calls[0].bin;
}

test("a call from a nested project resolves the workspace root's binary", async () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "plugin-root-")));
  try {
    writeFileSync(join(root, "magus.yaml"), "");
    writeFileSync(join(root, "magus"), "");
    mkdirSync(join(root, "console", "src"), { recursive: true });
    writeFileSync(join(root, "console", "magusfile.buzz"), "");
    assert.equal(await resolvedBinary(join(root, "console", "src")), join(root, "magus"));
    assert.equal(await resolvedBinary(join(root, "console")), join(root, "magus"));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("a worktree under another checkout never climbs into it", async () => {
  const parent = realpathSync(mkdtempSync(join(tmpdir(), "plugin-parent-")));
  try {
    writeFileSync(join(parent, "magus.yaml"), "");
    writeFileSync(join(parent, "magus"), "");
    const worktree = join(parent, ".claude", "worktrees", "job");
    mkdirSync(join(worktree, "console"), { recursive: true });
    writeFileSync(join(worktree, "magus.yaml"), "");
    writeFileSync(join(worktree, "console", "magusfile.buzz"), "");
    assert.equal(
      await resolvedBinary(join(worktree, "console")),
      "magus",
      "the parent's binary is never the answer",
    );
    writeFileSync(join(worktree, "magus"), "");
    assert.equal(await resolvedBinary(join(worktree, "console")), join(worktree, "magus"));
  } finally {
    rmSync(parent, { recursive: true, force: true });
  }
});

test("a tree with no magus.yaml falls back to the nearest magusfile", async () => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), "plugin-bare-")));
  try {
    mkdirSync(join(root, "sub", "deep"), { recursive: true });
    writeFileSync(join(root, "sub", "magusfile.buzz"), "");
    writeFileSync(join(root, "sub", "magus"), "");
    assert.equal(await resolvedBinary(join(root, "sub", "deep")), join(root, "sub", "magus"));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
