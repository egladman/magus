---
title: OpenCode
description: Wiring magus into OpenCode - skills in .opencode/skills, the TypeScript plugin that carries both guard surfaces, the post-compaction brief, and the idle checkpoint.
tags: [agents, opencode, skills, guard, plugin]
---

# OpenCode

OpenCode discovers Agent Skills and intercepts tool calls through a plugin
rather than a hook config. Throwing from `tool.execute.before` blocks the call, so
a deny reaches the model as the tool error; an advise is appended to the tool's
own result by `tool.execute.after`, which is the same call and the same context
window. One file carries all of it.

| what             | where                                                                                         |
| ---------------- | --------------------------------------------------------------------------------------------- |
| skills           | `.opencode/skills/` (it also reads `.claude/skills/`)                                         |
| guard wiring     | `~/.config/opencode/plugins/` or `.opencode/plugins/`                                         |
| command surface  | deny and advise both reach the model                                                          |
| file surface     | deny and advise both reach the model                                                          |
| MCP call surface | not wired: `tool.execute.before` sees it, its tool-name convention is unconfirmed (see below) |
| checkpoint       | the `session.idle` bus event                                                                  |
| rehydration      | `experimental.session.compacting`                                                             |
| MCP              | [MCP](../mcp.md)                                                                              |

## Skills

```sh
magus agent install .opencode/skills
```

If you already installed into `.claude/skills` for Claude Code, OpenCode reads
those too and this step is optional. [Skills](skills.md) covers the rest of the
install surface.

## MCP

Configure MCP for OpenCode as a host-level integration; see [MCP](../mcp.md)
for the client configuration and token. An agent uses the CLI fallback when MCP
is unavailable; it does not manually start Magus solely to obtain tools.

## Guard hook

Save the plugin below to `~/.config/opencode/plugins/` or `.opencode/plugins/`,
then confirm OpenCode loaded it:

```sh
opencode debug config
```

It encodes no magus rule. Every decision comes from `magus session hook`, which keeps it
host-only glue rather than a second rule set that drifts out of step with the
other templates.

```ts
// magus guard hook for OpenCode. OpenCode has no shell-command hook config: a
// plugin intercepts tool calls instead, and throwing from tool.execute.before
// blocks the call.
//
// This file is the source of truth. Copy it to ~/.config/opencode/plugins/ (or
// .opencode/plugins/) and adjust to taste; nothing in it is magus-internal.
//
// It encodes no magus rule. Every decision comes from `magus session hook`, so
// this stays host-only glue rather than a second rule set that drifts out of
// step with the other hosts' templates. `--agent-name opencode` only labels the
// observation magus records; it cannot change a verdict.
//
// Covers BOTH guard surfaces, so OpenCode gets the same rules Claude Code does:
//   bash          the command rules
//   edit | write  the declared-output rule
//
// One handler (`apply`) serves both, and both decisions now reach the model: a
// deny throws and its reason arrives as the tool error, while an advise is
// appended to the tool's own result by tool.execute.after, joined to the call it
// belongs to by callID. That append is what replaced a console.warn, which reached
// the person and never the model. The declarations below record it, and they
// are machine-read by the host-parity gate; see the longer note in
// magus-guard-command.sh.
//
// It also carries the two jobs that are not verdicts: a compacting session is
// handed this checkout back through the compaction prompt, and a checkpoint is
// recorded when the session goes idle, since OpenCode has no session-end event and
// idle is the proxy its own docs name.
// magus-guard-template: 13
// magus-guard-coverage: schema=1 host=opencode surface=command deny=model advise=model pass=none
// magus-guard-coverage: schema=1 host=opencode surface=path deny=model advise=model pass=none
// magus-guard-coverage: schema=1 host=opencode surface=mcp deny=none advise=none pass=none
// NOT because tool.execute.before/.after cannot see an MCP call: they are generic and already
// intercept every tool call OpenCode makes, MCP included - only the two branches below (bash,
// edit/write) narrow that down by tool NAME. What is missing is knowing what name OpenCode
// gives an MCP tool call at all; no vendored source documents its convention (SOURCES.md:
// OpenCode ships no hook config schema, only the typed Plugin interface this file already
// type-checks against), and a third `if (input.tool === ...)` branch keyed on a guessed string
// risks silently misjudging an unrelated tool rather than catching magus's own calls. Flip this
// once that naming convention is confirmed against a real OpenCode session.
//
// PATH contract: this shells out to `magus` by name, inheriting PATH from the
// opencode process. If magus lives in a prefix PATH does not include (mise,
// brew, asdf, ~/.local/bin), set GUARD_MAGUS_BIN to an absolute path. That name
// deliberately avoids the MAGUS_* space, which is magus's own config surface.

import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import type { Plugin } from "@opencode-ai/plugin";

/** The verdict schema this plugin understands; a bump means re-read the docs. */
const SUPPORTED_SCHEMA = 1;

/**
 * magus's guard verdict. A discriminated union on `decision`, so the compiler
 * enforces that `reason` is only read on a deny and `context` on an advise.
 */
type Verdict =
  | { schema_version: number; decision: "pass" }
  | { schema_version: number; decision: "advise"; context: string }
  | { schema_version: number; decision: "deny"; reason: string };

/**
 * Narrows untrusted JSON to a Verdict. A type guard rather than a cast because
 * this is another process's stdout: a cast would let a malformed payload reach
 * the branches below as though it had been checked.
 */
function isVerdict(value: unknown): value is Verdict {
  if (typeof value !== "object" || value === null) return false;
  const fields = value as Record<string, unknown>;
  if (typeof fields.schema_version !== "number") return false;
  switch (fields.decision) {
    case "pass":
      return true;
    case "advise":
      return typeof fields.context === "string";
    case "deny":
      return typeof fields.reason === "string";
    default:
      return false;
  }
}

/** First non-empty string among `keys` in a tool's untyped args, else "". */
function argString(args: unknown, keys: readonly string[]): string {
  if (typeof args !== "object" || args === null) return "";
  const fields = args as Record<string, unknown>;
  for (const key of keys) {
    const value = fields[key];
    if (typeof value === "string" && value.length > 0) return value;
  }
  return "";
}

/**
 * The binary belonging to the workspace this process is inside, found by walking up to
 * the magusfile, or null when that workspace has not built one.
 *
 * Walked rather than testing `./magus` alone: a plugin runs in the host's session
 * directory, and that is not always the workspace root - a session opened in a
 * subdirectory, or opened in one checkout while the work happens in another, tests a
 * `./magus` that is not there and falls through to PATH. Where PATH's copy cannot load
 * the workspace at all, that is the entire guard failing open.
 */
function workspaceMagus(): string | null {
  for (let dir = process.cwd(); ; ) {
    if (existsSync(join(dir, "magusfile.buzz"))) {
      const bin = join(dir, "magus");
      return existsSync(bin) ? bin : null;
    }
    const parent = dirname(dir);
    if (parent === dir) return null;
    dir = parent;
  }
}

export const MagusGuard: Plugin = async () => {
  // Prefer the workspace's own ./magus over PATH, for the reason spelled out in
  // magus-guard-command.sh: an older PATH binary does not fail when it lacks a rule, it
  // fails to recognize the config key that arms the rule and returns pass.
  const magus = process.env.GUARD_MAGUS_BIN ?? workspaceMagus() ?? "magus";

  // Said once per session. This plugin instance lives as long as the session does, so a
  // flag here IS the session and needs no marker on disk, unlike the sh templates whose
  // process ends with each tool call. The notice reports a broken installation: a fact
  // for the person, with nothing in it a model can act on, so a repeat is noise.
  // Measured over recent sessions: 99% of these firings were same-session repeats.
  let saidUnguarded = false;

  // Advisories waiting for the call they belong to, keyed by callID. An advise is
  // produced BEFORE a call and delivered AFTER it, because tool.execute.after is the
  // only hook that can rewrite what the model reads, and judging a second time there
  // would record two verdicts for one call.
  //
  // An entry is dropped when its call lands. A call that never reaches
  // tool.execute.after strands one string for the life of the session, which is a
  // cheaper leak than judging everything twice to avoid it.
  const pending = new Map<string, string>();

  /**
   * Runs one `magus session hook` invocation and returns its raw stdout, or null when the
   * binary could not be run at all (missing, not executable). An older binary that
   * rejects a flag still runs and exits, so that case comes back as "" here, not
   * null - the caller distinguishes them.
   */
  const runOnce = async (args: readonly string[], input: string): Promise<string | null> => {
    try {
      const proc = Bun.spawn([magus, ...args], {
        stdin: new TextEncoder().encode(input),
        stdout: "pipe",
        stderr: "ignore",
      });
      const stdout = await new Response(proc.stdout).text();
      await proc.exited;
      return stdout;
    } catch {
      return null;
    }
  };

  /**
   * Runs one guard query. Returns null when no verdict could be obtained, which
   * every caller treats as allow.
   *
   * Failing OPEN is deliberate. Throwing is OpenCode's only way to stop a call,
   * so a guard that threw whenever magus was missing would block every tool
   * call and make the session unusable - worse than no guard. The failure is
   * logged rather than swallowed, so an unguarded session stays visible.
   *
   * The thing being judged goes in on STDIN, never in argv. `magus session hook` takes
   * no positional arguments at all, and that is not an incidental preference:
   * a command is arbitrary text, and a shell command passed as an argument is
   * one quoting mistake away from being re-parsed. Passing it in argv does not
   * misjudge the command, it gets NO verdict - which fails open, quietly, on
   * every call.
   *
   * `--agent-name` in `args` is ATTRIBUTION, not policy (see the header comment), and
   * postdates the current release this plugin is downloaded and run against.
   * Passing it unconditionally does not degrade the guard, it BREAKS it - an older
   * binary rejects the unknown flag and prints its usage, so `-o json` comes back
   * empty and every verdict silently disappears. So: try as called, and on an
   * empty reply - which under `-o json` only happens when the call itself failed,
   * since even a pass renders `{"decision":"pass",...}` - retry with `--agent-name` and
   * its value stripped out. Same shape as magus-guard-command.sh's `guard()`
   * fallback, fixed there after the same gap (memory:
   * agent-host-attribution-not-captured) and ported here so this plugin degrades
   * the same way.
   */
  const judge = async (args: readonly string[], input: string): Promise<Verdict | null> => {
    const unguarded = () => {
      if (saidUnguarded) return;
      saidUnguarded = true;
      console.warn(
        `[magus guard] could not run ${magus}; this call is UNGUARDED. ` +
          "Install magus, or set GUARD_MAGUS_BIN to its path.",
      );
    };

    let stdout = await runOnce(args, input);
    if (stdout === null) {
      unguarded();
      return null;
    }
    if (stdout.trim() === "") {
      const nameIndex = args.indexOf("--agent-name");
      const withoutName =
        nameIndex === -1 ? args : [...args.slice(0, nameIndex), ...args.slice(nameIndex + 2)];
      stdout = await runOnce(withoutName, input);
      if (stdout === null) {
        unguarded();
        return null;
      }
    }

    let parsed: unknown;
    try {
      parsed = JSON.parse(stdout);
    } catch {
      console.warn("[magus guard] verdict was not JSON; allowing");
      return null;
    }
    if (!isVerdict(parsed)) {
      console.warn("[magus guard] unrecognized verdict shape; allowing");
      return null;
    }
    if (parsed.schema_version !== SUPPORTED_SCHEMA) {
      console.warn(
        `[magus guard] verdict schema ${parsed.schema_version} differs from the expected ` +
          `${SUPPORTED_SCHEMA}; allowing. Update this plugin from the magus docs.`,
      );
      return null;
    }
    return parsed;
  };

  /**
   * Throws on a deny, which is OpenCode's only way to stop a call. Returns an
   * advise's context for the caller to hold until the call lands, and "" for
   * everything else.
   */
  const apply = (verdict: Verdict | null): string => {
    if (verdict === null) return "";
    switch (verdict.decision) {
      case "deny":
        throw new Error(`[magus guard] ${verdict.reason}`);
      case "advise":
        return verdict.context;
      case "pass":
        return "";
    }
  };

  /** Holds an advisory for the call it judged, so tool.execute.after can deliver it. */
  const remember = (callID: string, context: string): void => {
    if (context !== "") pending.set(callID, context);
  };

  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool === "bash") {
        const command = argString(output.args, ["command"]);
        if (command === "") return;
        const args = ["session", "hook", "--agent-name", "opencode", "-o", "json"];
        remember(input.callID, apply(await judge(args, command)));
        return;
      }

      if (input.tool === "edit" || input.tool === "write") {
        // OpenCode names this filePath; the fallbacks cost nothing and keep the
        // plugin working if a future tool spells it differently.
        const path = argString(output.args, ["filePath", "file_path", "path"]);
        if (path === "") return;
        const args = ["session", "hook", "--path", "--agent-name", "opencode", "-o", "json"];
        remember(input.callID, apply(await judge(args, path)));
      }
    },

    "tool.execute.after": async (input, output) => {
      const context = pending.get(input.callID);
      if (context === undefined) return;
      pending.delete(input.callID);
      // Appended to the result the model already reads, rather than replacing it:
      // the advisory explains what to do differently NEXT time, and the tool's own
      // output is what the call was for.
      output.output = `${output.output}\n\n[magus guard] ${context}`;
    },

    "experimental.session.compacting": async (_input, output) => {
      // Compaction replaces a session's history with a summary, and the model then
      // works from prose. This puts state back in front of it instead: branch,
      // revision, unpushed commits, the classified dirty tree, live leases and the
      // last run's failures, all read off the disk at the moment it runs.
      const brief = await runOnce(["session", "--brief"], "");
      if (brief === null) return;
      const text = brief.trim();
      if (text !== "") output.context.push(text);
    },

    event: async ({ event }) => {
      // OpenCode has no session-end event; session.idle is the proxy. The checkpoint
      // records where the work stands so whoever comes back reads `magus session`
      // rather than reconstructing it. It judges nothing and its output is ignored.
      if (event.type !== "session.idle") return;
      await runOnce(["session", "checkpoint", "--agent-name", "opencode"], "");
    },
  };
};

export default MagusGuard;
```

The thing being judged goes in on stdin, never in argv. `magus session hook` takes no
positional arguments, and a plugin that passes the command as one does not get a
wrong verdict - it gets no verdict, which fails open on every call.

If magus lives in a prefix the OpenCode process does not have on PATH (mise,
brew, asdf, `~/.local/bin`), set `GUARD_MAGUS_BIN` to an absolute path.

## Notifications

Invoke `magus session notify` from the plugin with the same envelope every other host
uses; see [Attention hooks](notifications.md).

## Handing a compacted session its state back

`experimental.session.compacting` runs while OpenCode is building the summary
that will replace a session's history, and takes `context: string[]` straight
into the compaction prompt. The plugin puts `magus session --brief` there: branch
and revision, commits not yet on the base ref, the dirty tree split into sources,
generated outputs and unclaimed paths, the live leases, the last recorded run's
failures, and where the rules live.

Every line is read off the disk at the moment it runs, so nothing in it is a
retelling of a retelling. Run `magus session --brief` yourself to see what a
compacting session will be handed.

## Recording where the work stands

OpenCode has no session-end event. `session.idle` on the read-only bus is the
proxy its own docs name, and the plugin subscribes to it and runs
`magus session checkpoint --agent-name opencode`: the revision, branch and
dirtiness of the tree, which `magus session` then lists.

Idle fires when the agent stops working rather than when the session is closed,
so a long session records several checkpoints. That is the same shape a `Stop`
hook produces on the hosts that have one, and the listing is ordered.

If you would rather not have the plugin do it, running
[`magus-checkpoint.sh`](guard-templates.md#magus-checkpointsh) with
`GUARD_AGENT_NAME=opencode` records the identical row.
`magus session checkpoint --note "..."` writes it by hand.

## Coverage and limits

- An `advise` verdict is appended to the tool result that call produced, joined
  to it by `callID`. It is judged once, before the call, and delivered after it -
  judging again in `tool.execute.after` would record two verdicts for one call.
  An advised call whose result never arrives strands one string for the life of
  the session, which is the cheaper of the two leaks.
- The plugin fails open when magus cannot be run, and says so in the log.
  Throwing is OpenCode's only way to stop a call, so a guard that threw on a
  missing binary would block every tool call and make the session unusable.
- Tool identifiers were confirmed against an installed OpenCode 1.18.5: `bash`,
  `edit`, `write`, `read`, `patch` and `glob` all appear in its binary, and
  `filePath` is the field its edit tools carry. An MCP tool's `input.tool`
  string was NOT among them (OpenCode has no hook config schema to check it
  against either, per `testdata/hostschemas/SOURCES.md`), so the MCP call
  surface is feasible - `tool.execute.before`/`.after` already see every call,
  MCP included - but not wired: a branch keyed on a guessed name risks judging
  an unrelated tool rather than magus's own calls. Confirm the string against a
  live session, then add a third `if (input.tool === ...)` branch beside `bash`
  and `edit`/`write`.
- Lease capture is FEASIBLE but not wired. `tool.execute.before` fires for
  every tool and hands the plugin `input.tool` plus the call's arguments, so a
  branch alongside the `bash` and `edit`/`write` ones could pipe a sub-agent
  tool's prompt to `magus session hook` and get the same `agent_spawn` event. Which tool
  identifier to match on has not been confirmed against an installed OpenCode,
  so the plugin above does not guess at one.
- `shell.env` could export `BAGGAGE=magus.lease=<id>` into every shell the
  session runs, and deliberately does not. The only place the plugin could read
  that id is the marker `magus session lease` writes into the checkout, and the
  guard and the sandbox already read that marker directly; it exists precisely
  because a host runs its hooks with its own environment. Exporting a copy of it
  would be a second source of truth that can go stale, for a lease the tools can
  already see.
- OpenCode's documentation confirms that a throw blocks, but does not promise
  the thrown message reaches the model. Treat the deny as a hard stop whose
  explanation is best effort, and confirm against
  [OpenCode plugins](https://opencode.ai/docs/plugins).

## Verify

```sh
opencode debug config
magus doctor
```
